package metaweb

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// textMaxRunes caps the pin-read `text` body server-side. The payload field
// is never truncated — it is the continuation path.
const textMaxRunes = 8000

// pinIdPattern enforces the `<64 hex>i<n>` MetaID pin id shape.
var pinIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}i[0-9]+$`)

// ErrRemotePinNotFound marks MANAPI "no pin found" answers (code 100 / HTTP
// 404); the handler maps it to 40400 while transport errors map to 50000.
var ErrRemotePinNotFound = errors.New("metaweb: remote pin not found")

type creatorInfo struct {
	GlobalMetaId string `json:"globalMetaId"`
	MetaId       string `json:"metaid"`
	Name         string `json:"name"`
	Address      string `json:"address"`
}

// pinMeta mirrors the search-document meta extraction so list and detail
// views always agree.
type pinMeta struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

type attachmentItem struct {
	URI         string `json:"uri"`
	URL         string `json:"url"`
	ContentType string `json:"contentType"`
	Size        *int64 `json:"size"`
}

// pinReadData is the `data` block of GET /api/metaweb/pin/:pinId.
// Payload is never truncated; Text is capped at textMaxRunes. Truncated and
// TotalLength are null exactly when Text is null.
type pinReadData struct {
	PinId        string           `json:"pinId"`
	CurrentPinId string           `json:"currentPinId"`
	Protocol     string           `json:"protocol"`
	Path         string           `json:"path"`
	ChainName    string           `json:"chainName"`
	Operation    string           `json:"operation"`
	Creator      creatorInfo      `json:"creator"`
	CreatedAt    int64            `json:"createdAt"`
	ContentType  string           `json:"contentType"`
	Payload      any              `json:"payload"`
	Text         *string          `json:"text"`
	Truncated    *bool            `json:"truncated"`
	TotalLength  *int             `json:"totalLength"`
	Meta         pinMeta          `json:"meta"`
	Attachments  []attachmentItem `json:"attachments"`
	Source       string           `json:"source"`
}

func (a *Aggregator) handlePinRead(c *gin.Context) {
	pinId := strings.TrimSpace(c.Param("pinId"))
	if !pinIDPattern.MatchString(pinId) {
		api.RespErr(c, codeInvalidParam, "malformed pinId")
		return
	}
	if a.pinLookup == nil && a.serviceLookup == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	// Resolution order: publishedcontent, then skillservice, then MANAPI.
	if a.pinLookup != nil {
		rec, err := a.pinLookup.LookupByAnyPinId(pinId)
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return
		}
		if rec != nil {
			api.RespSuccess(c, a.pinReadFromPublishedRecord(pinId, rec))
			return
		}
	}
	if a.serviceLookup != nil {
		rec, err := a.serviceLookup.LookupServiceByAnyPinId(pinId)
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return
		}
		if rec != nil {
			api.RespSuccess(c, a.pinReadFromServiceRecord(pinId, rec))
			return
		}
	}

	if a.remoteFetcher == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	remote, err := a.remoteFetcher.FetchPin(pinId)
	if errors.Is(err, ErrRemotePinNotFound) {
		api.RespErr(c, codeNotFound, "pin not found")
		return
	}
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	api.RespSuccess(c, a.pinReadFromRemotePin(pinId, remote))
}

// creatorName resolves the best-effort userinfo display name; "" when unknown.
func (a *Aggregator) creatorName(globalMetaId, metaId string) string {
	if a.profileNamer == nil {
		return ""
	}
	name, _ := a.profileNamer.ProfileNameAvatar(globalMetaId, metaId)
	return name
}

// resolveAttachments maps payload attachments onto the wire shape, resolving
// metafile:// URIs to absolute URLs via the configured asset resolver. The
// result is always an array (never null).
func (a *Aggregator) resolveAttachments(attachments []metawebdoc.Attachment) []attachmentItem {
	out := make([]attachmentItem, 0, len(attachments))
	for _, attachment := range attachments {
		url := attachment.URI
		if a.assetResolver != nil {
			url = a.assetResolver.Resolve(attachment.URI)
		}
		out = append(out, attachmentItem{
			URI:         attachment.URI,
			URL:         url,
			ContentType: attachment.ContentType,
			Size:        attachment.Size,
		})
	}
	return out
}

// normalizeText caps the LLM-ready body at textMaxRunes. Empty bodies render
// as null with null truncated/totalLength, no error.
func normalizeText(text string) (*string, *bool, *int) {
	if strings.TrimSpace(text) == "" {
		return nil, nil, nil
	}
	total := utf8.RuneCountInString(text)
	truncated := false
	if total > textMaxRunes {
		text = string([]rune(text)[:textMaxRunes])
		truncated = true
	}
	return &text, &truncated, &total
}

func orEmptyTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// pinReadFromPublishedRecord builds the response for a local publishedcontent
// record (simplenote / simplebuzz / metaapp / metabot-skill / metaprotocol).
// Revoked records keep their last known payload per the pin-read spec.
func (a *Aggregator) pinReadFromPublishedRecord(requestedPinId string, rec *publishedcontent.Record) pinReadData {
	key := metawebdoc.ProtocolKeyForPath(rec.ProtocolPath)
	var payloadMap map[string]any
	payloadText := ""
	var payload any
	if rec.PayloadExposed {
		switch {
		case rec.PayloadJSON != nil:
			payloadMap = rec.PayloadJSON
			payload = rec.PayloadJSON
		case rec.PayloadText != "":
			payloadText = rec.PayloadText
			payload = rec.PayloadText
		}
	}
	extracted := metawebdoc.ExtractForPath(rec.ProtocolPath, payloadMap, payloadText)
	text, truncated, totalLength := normalizeText(metawebdoc.TextForProtocol(key, payloadMap, payloadText))
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return pinReadData{
		PinId:        requestedPinId,
		CurrentPinId: currentPinId,
		Protocol:     key,
		Path:         rec.ProtocolPath,
		ChainName:    rec.ChainName,
		Operation:    rec.Operation,
		Creator: creatorInfo{
			GlobalMetaId: rec.PublisherGlobalMetaId,
			MetaId:       rec.PublisherMetaId,
			Name:         a.creatorName(rec.PublisherGlobalMetaId, rec.PublisherMetaId),
			Address:      rec.PublisherAddress,
		},
		CreatedAt:   metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
		ContentType: rec.ContentType,
		Payload:     payload,
		Text:        text,
		Truncated:   truncated,
		TotalLength: totalLength,
		Meta: pinMeta{
			Title:   extracted.Title,
			Summary: extracted.Summary,
			Tags:    orEmptyTags(extracted.Tags),
		},
		Attachments: a.resolveAttachments(metawebdoc.ExtractAttachments(payloadMap)),
		Source:      "local",
	}
}

// pinReadFromServiceRecord builds the response for a local skillservice
// record. Legacy records without a persisted DeclarationPayload fall back to
// a payload reconstructed from the typed fields.
func (a *Aggregator) pinReadFromServiceRecord(requestedPinId string, rec *skillservice.ServiceRecord) pinReadData {
	payload := rec.DeclarationPayload
	if payload == nil {
		payload = servicePayloadFromRecord(rec)
	}
	extracted := metawebdoc.ExtractSkillService(metawebdoc.SkillServiceFields{
		DisplayName:    rec.DisplayName,
		ServiceName:    rec.ServiceName,
		Description:    rec.Description,
		ProviderSkill:  rec.ProviderSkill,
		Price:          rec.Price,
		Currency:       rec.Currency,
		SettlementKind: rec.SettlementKind,
	})
	text, truncated, totalLength := normalizeText(metawebdoc.TextForProtocol(metawebdoc.KeySkillService, payload, ""))
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourceServicePinId
	}
	return pinReadData{
		PinId:        requestedPinId,
		CurrentPinId: currentPinId,
		Protocol:     metawebdoc.KeySkillService,
		Path:         metawebdoc.PathSkillService,
		ChainName:    rec.ChainName,
		Operation:    rec.Operation,
		Creator: creatorInfo{
			GlobalMetaId: rec.ProviderGlobalMetaId,
			MetaId:       rec.ProviderMetaId,
			Name:         a.creatorName(rec.ProviderGlobalMetaId, rec.ProviderMetaId),
			Address:      rec.ProviderAddress,
		},
		CreatedAt:   metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
		ContentType: "application/json",
		Payload:     payload,
		Text:        text,
		Truncated:   truncated,
		TotalLength: totalLength,
		Meta: pinMeta{
			Title:   extracted.Title,
			Summary: extracted.Summary,
			Tags:    orEmptyTags(extracted.Tags),
		},
		Attachments: []attachmentItem{},
		Source:      "local",
	}
}

// servicePayloadFromRecord reconstructs the chain-declared summary object
// from the typed record fields (non-empty strings only).
func servicePayloadFromRecord(rec *skillservice.ServiceRecord) map[string]any {
	payload := make(map[string]any)
	set := func(key, value string) {
		if strings.TrimSpace(value) != "" {
			payload[key] = value
		}
	}
	set("serviceName", rec.ServiceName)
	set("displayName", rec.DisplayName)
	set("description", rec.Description)
	set("serviceIcon", rec.ServiceIcon)
	set("providerMetaBot", rec.ProviderMetaBot)
	set("providerSkill", rec.ProviderSkill)
	set("price", rec.Price)
	set("currency", rec.Currency)
	set("paymentChain", rec.PaymentChain)
	set("settlementKind", rec.SettlementKind)
	set("mrc20Ticker", rec.MRC20Ticker)
	set("mrc20Id", rec.MRC20Id)
	set("outputType", rec.OutputType)
	set("paymentAddress", rec.PaymentAddress)
	if rec.Disabled {
		payload["disabled"] = true
	}
	return payload
}

// pinReadFromRemotePin builds the response for the MANAPI passthrough.
// currentPinId is the last modify_history entry, else the pin id itself.
func (a *Aggregator) pinReadFromRemotePin(requestedPinId string, pin *RemotePin) pinReadData {
	key := metawebdoc.ProtocolKeyForPath(pin.Path)
	encrypted := pin.Encryption != "" && pin.Encryption != "0"

	var payloadMap map[string]any
	payloadText := ""
	var payload any
	if body := bytes.TrimSpace(pin.ContentBody); !encrypted && len(body) > 0 && !bytes.Contains(body, []byte{0}) {
		if body[0] == '{' {
			var decoded map[string]any
			if err := json.Unmarshal(body, &decoded); err == nil && decoded != nil {
				payloadMap = decoded
				payload = decoded
			} else {
				payloadText = string(body)
				payload = payloadText
			}
		} else {
			payloadText = string(body)
			payload = payloadText
		}
	}

	extracted := metawebdoc.ExtractForPath(pin.Path, payloadMap, payloadText)
	text, truncated, totalLength := normalizeText(metawebdoc.TextForProtocol(key, payloadMap, payloadText))

	currentPinId := requestedPinId
	if pin.PinId != "" {
		currentPinId = pin.PinId
	}
	for i := len(pin.ModifyHistory) - 1; i >= 0; i-- {
		if entry := strings.Trim(strings.TrimSpace(pin.ModifyHistory[i]), "@/"); entry != "" {
			currentPinId = entry
			break
		}
	}

	operation := strings.ToLower(strings.TrimSpace(pin.Operation))
	if operation == "" {
		operation = "create"
	}
	metaId := firstNonEmptyString(pin.MetaId, pin.CreateMetaId)
	return pinReadData{
		PinId:        requestedPinId,
		CurrentPinId: currentPinId,
		Protocol:     key,
		Path:         pin.Path,
		ChainName:    pin.ChainName,
		Operation:    operation,
		Creator: creatorInfo{
			GlobalMetaId: pin.GlobalMetaId,
			MetaId:       metaId,
			Name:         a.creatorName(pin.GlobalMetaId, metaId),
			Address:      firstNonEmptyString(pin.Address, pin.CreateAddress),
		},
		CreatedAt:   metawebdoc.NormalizeUnixSeconds(pin.Timestamp),
		ContentType: pin.ContentType,
		Payload:     payload,
		Text:        text,
		Truncated:   truncated,
		TotalLength: totalLength,
		Meta: pinMeta{
			Title:   extracted.Title,
			Summary: extracted.Summary,
			Tags:    orEmptyTags(extracted.Tags),
		},
		Attachments: a.resolveAttachments(metawebdoc.ExtractAttachments(payloadMap)),
		Source:      "remote",
	}
}

// RemotePin is the decoded MANAPI pin consumed by the pin-read remote
// fallback. ContentBody is already base64-decoded when applicable.
type RemotePin struct {
	PinId         string
	Path          string
	Operation     string
	ContentType   string
	ContentBody   []byte
	MetaId        string
	GlobalMetaId  string
	Address       string
	CreateMetaId  string
	CreateAddress string
	ChainName     string
	Timestamp     int64 // sec or ms; normalised at use
	Encryption    string
	ModifyHistory []string
}

// MANAPIPinFetcher implements RemotePinFetcher against MANAPI
// GET {base}/pin/{pinId} with a bounded timeout.
type MANAPIPinFetcher struct {
	baseURL string
	client  *http.Client
}

// NewMANAPIPinFetcher builds a fetcher; a nil client gets the contract's 5s
// timeout.
func NewMANAPIPinFetcher(baseURL string, client *http.Client) *MANAPIPinFetcher {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &MANAPIPinFetcher{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		client:  client,
	}
}

func (f *MANAPIPinFetcher) FetchPin(pinId string) (*RemotePin, error) {
	if f == nil || f.baseURL == "" {
		return nil, errors.New("MANAPI base URL is required")
	}
	resp, err := f.client.Get(f.baseURL + "/pin/" + pinId)
	if err != nil {
		return nil, fmt.Errorf("fetch MANAPI pin: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrRemotePinNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MANAPI pin read returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read MANAPI pin response: %w", err)
	}
	return decodeRemotePin(raw)
}

// manapiRemotePin mirrors the MANAPI GET /pin/{pinId} data object.
type manapiRemotePin struct {
	ID             string   `json:"id"`
	Path           string   `json:"path"`
	Operation      string   `json:"operation"`
	ContentType    string   `json:"contentType"`
	ContentBody    string   `json:"contentBody"`
	ContentSummary string   `json:"contentSummary"`
	MetaId         string   `json:"metaid"`
	GlobalMetaId   string   `json:"globalMetaId"`
	Address        string   `json:"address"`
	CreateMetaId   string   `json:"createMetaId"`
	CreateAddress  string   `json:"createAddress"`
	ChainName      string   `json:"chainName"`
	Timestamp      int64    `json:"timestamp"`
	Encryption     string   `json:"encryption"`
	ModifyHistory  []string `json:"modify_history"`
}

func decodeRemotePin(raw []byte) (*RemotePin, error) {
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode MANAPI pin response: %w", err)
	}
	if envelope.Code == 100 || strings.Contains(strings.ToLower(envelope.Message), "no pin found") {
		return nil, ErrRemotePinNotFound
	}
	if envelope.Code != 0 && envelope.Code != 1 {
		return nil, fmt.Errorf("MANAPI pin read failed: code=%d message=%s", envelope.Code, envelope.Message)
	}
	if len(bytes.TrimSpace(envelope.Data)) == 0 || bytes.Equal(bytes.TrimSpace(envelope.Data), []byte("null")) {
		return nil, ErrRemotePinNotFound
	}
	var pin manapiRemotePin
	if err := json.Unmarshal(envelope.Data, &pin); err != nil {
		return nil, fmt.Errorf("decode MANAPI pin body: %w", err)
	}
	body := strings.TrimSpace(pin.ContentBody)
	if body == "" {
		body = strings.TrimSpace(pin.ContentSummary)
	}
	return &RemotePin{
		PinId:         strings.TrimSpace(pin.ID),
		Path:          strings.TrimSpace(pin.Path),
		Operation:     strings.TrimSpace(pin.Operation),
		ContentType:   strings.TrimSpace(pin.ContentType),
		ContentBody:   decodeRemoteContentBody(body),
		MetaId:        strings.TrimSpace(pin.MetaId),
		GlobalMetaId:  strings.TrimSpace(pin.GlobalMetaId),
		Address:       strings.TrimSpace(pin.Address),
		CreateMetaId:  strings.TrimSpace(pin.CreateMetaId),
		CreateAddress: strings.TrimSpace(pin.CreateAddress),
		ChainName:     strings.TrimSpace(pin.ChainName),
		Timestamp:     pin.Timestamp,
		Encryption:    strings.TrimSpace(pin.Encryption),
		ModifyHistory: pin.ModifyHistory,
	}, nil
}

// decodeRemoteContentBody mirrors the backfill clients'
// maybeDecodeBase64Content: MANAPI serves contentBody base64-encoded; a body
// that base64-decodes to valid JSON is unwrapped, everything else passes
// through as raw text.
func decodeRemoteContentBody(text string) []byte {
	if text == "" {
		return nil
	}
	if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return []byte(text)
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(text)
		if err != nil || len(decoded) == 0 {
			continue
		}
		if json.Valid(decoded) {
			return decoded
		}
	}
	return []byte(text)
}
