package idbotsca

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// SyncFromMANAPI synchronizes the CA registry against MANAPI's
// /pin/path/list?path=/protocols/idbots-nft-ca endpoint on startup, advancing
// any older persisted baseline when newer Bob-signed CA pins have been mined.
// When no confirmed CA pin exists after path sync, it falls back to
// DefaultGenesisPinID.
func (a *Aggregator) SyncFromMANAPI(ctx context.Context, manapiBaseURL string, httpClient *http.Client) error {
	if a == nil {
		return nil
	}
	baseURL := strings.TrimRight(strings.TrimSpace(manapiBaseURL), "/")
	if baseURL == "" {
		return errors.New("MANAPI base URL is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	pins, err := fetchCAPathPins(ctx, baseURL, httpClient)
	if err == nil && len(pins) > 0 {
		// Replay oldest-first so newer create/modify pins advance older ones cleanly.
		for i := len(pins) - 1; i >= 0; i-- {
			if _, applyErr := a.HandleBlockPin(pins[i]); applyErr != nil {
				return applyErr
			}
		}
		if a.HasConfirmedRegistry() {
			return nil
		}
	}

	if a.HasConfirmedRegistry() {
		return err
	}

	genesisPin, genesisErr := fetchCAPinByID(ctx, baseURL, DefaultGenesisPinID, httpClient)
	if genesisErr != nil {
		if err != nil {
			return err
		}
		return genesisErr
	}
	if genesisPin != nil {
		if _, applyErr := a.HandleBlockPin(genesisPin); applyErr != nil {
			return applyErr
		}
	}
	return nil
}

// BackfillIfEmpty synchronizes the CA registry from MANAPI on startup. Kept as
// a compatible entry point that also catches up newer CA pins over an older
// persisted baseline.
func (a *Aggregator) BackfillIfEmpty(ctx context.Context, manapiBaseURL string, httpClient *http.Client) error {
	return a.SyncFromMANAPI(ctx, manapiBaseURL, httpClient)
}

type manapiCAPin struct {
	Id                 string `json:"id"`
	Number             int64  `json:"number"`
	Path               string `json:"path"`
	OriginalPath       string `json:"originalPath"`
	Operation          string `json:"operation"`
	ContentBody        string `json:"contentBody"`
	ContentSummary     string `json:"contentSummary"`
	ContentLength      int64  `json:"contentLength"`
	ContentType        string `json:"contentType"`
	Creator            string `json:"creator"`
	CreateAddress      string `json:"createAddress"`
	CreateMetaId       string `json:"createMetaId"`
	MetaId             string `json:"metaId"`
	MetaIdLower        string `json:"metaid"`
	GlobalMetaId       string `json:"globalMetaId"`
	Address            string `json:"address"`
	ChainName          string `json:"chainName"`
	GenesisTransaction string `json:"genesisTransaction"`
	GenesisHeight      int64  `json:"genesisHeight"`
	Timestamp          int64  `json:"timestamp"`
}

func (p manapiCAPin) toAggregatorPin() *aggregator.PinInscription {
	body := decodeMANAPIBody(p.ContentBody, p.ContentSummary)
	metaId := strings.TrimSpace(p.MetaId)
	if metaId == "" {
		metaId = strings.TrimSpace(p.MetaIdLower)
	}
	return &aggregator.PinInscription{
		Id:                 strings.TrimSpace(p.Id),
		Number:             p.Number,
		Path:               strings.TrimSpace(p.Path),
		OriginalPath:       strings.TrimSpace(p.OriginalPath),
		Operation:          strings.TrimSpace(p.Operation),
		ContentBody:        body,
		ContentSummary:     p.ContentSummary,
		ContentType:        strings.TrimSpace(p.ContentType),
		Creator:            strings.TrimSpace(p.Creator),
		CreateAddress:      strings.TrimSpace(p.CreateAddress),
		CreateMetaId:       strings.TrimSpace(p.CreateMetaId),
		MetaId:             metaId,
		GlobalMetaId:       strings.TrimSpace(p.GlobalMetaId),
		Address:            strings.TrimSpace(p.Address),
		ChainName:          strings.TrimSpace(p.ChainName),
		GenesisTransaction: strings.TrimSpace(p.GenesisTransaction),
		GenesisHeight:      p.GenesisHeight,
		Timestamp:          p.Timestamp,
	}
}

func decodeMANAPIBody(body, summary string) []byte {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return []byte(strings.TrimSpace(summary))
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "\"") {
		return []byte(trimmed)
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		return decoded
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(trimmed); err == nil {
		return decoded
	}
	return []byte(trimmed)
}

func needsFullContentHydration(item manapiCAPin, pin *aggregator.PinInscription) bool {
	if pin == nil || pin.Id == "" {
		return false
	}
	raw := pinPayloadBytes(pin)
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	if item.ContentLength > 0 && int64(len(raw)) < item.ContentLength {
		return true
	}
	_, err := parseCAPayload(raw)
	return err != nil
}

func fetchCAPathPins(ctx context.Context, baseURL string, httpClient *http.Client) ([]*aggregator.PinInscription, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/pin/path/list"
	q := parsed.Query()
	q.Set("path", PathCA)
	q.Set("size", strconv.Itoa(50))
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MANAPI CA path list HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env struct {
		Code    int           `json:"code"`
		Message string        `json:"message"`
		List    []manapiCAPin `json:"list"`
		Data    struct {
			List []manapiCAPin `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if env.Code != 0 && env.Code != 1 {
		return nil, fmt.Errorf("MANAPI CA path list failed: code=%d message=%s", env.Code, env.Message)
	}
	items := env.Data.List
	if len(items) == 0 {
		items = env.List
	}
	out := make([]*aggregator.PinInscription, 0, len(items))
	for i := range items {
		pin := items[i].toAggregatorPin()
		if isCAPin(pin) && isTrustedPublisher(pin) && needsFullContentHydration(items[i], pin) {
			if rawContent, cErr := fetchCAContentByID(ctx, baseURL, pin.Id, httpClient); cErr == nil && len(bytes.TrimSpace(rawContent)) > 0 {
				pin.ContentBody = rawContent
			}
			if _, pErr := parseCAPayload(pinPayloadBytes(pin)); pErr != nil {
				if fullPin, fErr := fetchCAPinByID(ctx, baseURL, pin.Id, httpClient); fErr == nil && fullPin != nil && len(fullPin.ContentBody) > 0 {
					pin.ContentBody = fullPin.ContentBody
				}
			}
		}
		out = append(out, pin)
	}
	return out, nil
}

func fetchCAContentByID(ctx context.Context, baseURL, pinId string, httpClient *http.Client) ([]byte, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/content/" + strings.TrimSpace(pinId)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MANAPI CA content HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func fetchCAPinByID(ctx context.Context, baseURL, pinId string, httpClient *http.Client) (*aggregator.PinInscription, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	parsed.Path = "/pin/" + strings.TrimSpace(pinId)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MANAPI CA pin HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env struct {
		Code    int          `json:"code"`
		Message string       `json:"message"`
		Data    *manapiCAPin `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if env.Code != 0 && env.Code != 1 {
		return nil, fmt.Errorf("MANAPI CA pin failed: code=%d message=%s", env.Code, env.Message)
	}
	if env.Data == nil {
		return nil, nil
	}
	return env.Data.toAggregatorPin(), nil
}
