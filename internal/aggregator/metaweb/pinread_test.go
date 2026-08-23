package metaweb

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type fakeFetcher struct {
	pin *RemotePin
	err error
}

func (f *fakeFetcher) FetchPin(pinId string) (*RemotePin, error) { return f.pin, f.err }

type pinReadEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		PinId        string          `json:"pinId"`
		CurrentPinId string          `json:"currentPinId"`
		Protocol     string          `json:"protocol"`
		Path         string          `json:"path"`
		ChainName    string          `json:"chainName"`
		Operation    string          `json:"operation"`
		Creator      creatorInfo     `json:"creator"`
		CreatedAt    int64           `json:"createdAt"`
		ContentType  string          `json:"contentType"`
		Payload      json.RawMessage `json:"payload"`
		Text         *string         `json:"text"`
		Truncated    *bool           `json:"truncated"`
		TotalLength  *int            `json:"totalLength"`
		Meta         pinMeta         `json:"meta"`
		Attachments  []struct {
			URI         string `json:"uri"`
			URL         string `json:"url"`
			ContentType string `json:"contentType"`
			Size        *int64 `json:"size"`
		} `json:"attachments"`
		Source string `json:"source"`
	} `json:"data"`
}

func doPinRead(t *testing.T, router *gin.Engine, pinId string) pinReadEnvelope {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/pin/"+pinId, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", recorder.Code)
	}
	var envelope pinReadEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}

// setupPublishedContent builds a real publishedcontent aggregator over a temp
// store so the pin-read tests exercise the genuine version-chain walk.
func setupPublishedContent(t *testing.T) *publishedcontent.Aggregator {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })
	agg := &publishedcontent.Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("publishedcontent Init: %v", err)
	}
	return agg
}

func hexPinId(fill byte, index int) string {
	return strings.Repeat(string(fill), 64) + fmt.Sprintf("i%d", index)
}

func TestHandlePinRead_MalformedPinId(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetPinLookups(setupPublishedContent(t), nil)
	router := newTestRouter(agg)

	// An empty pinId cannot reach the handler (no route match); these hit the
	// shape check.
	for _, pinId := range []string{"not-a-pin", strings.Repeat("a", 63) + "i0", strings.Repeat("g", 64) + "i0"} {
		envelope := doPinRead(t, router, pinId)
		if envelope.Code != codeInvalidParam {
			t.Errorf("pinId %q: code = %d, want 40000", pinId, envelope.Code)
		}
	}
}

func TestHandlePinRead_NotFoundVsUnavailable(t *testing.T) {
	published := setupPublishedContent(t)

	// Local miss + MANAPI "no pin found" → 40400.
	agg := newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	agg.SetRemotePinFetcher(&fakeFetcher{err: ErrRemotePinNotFound})
	router := newTestRouter(agg)
	if envelope := doPinRead(t, router, hexPinId('a', 0)); envelope.Code != codeNotFound {
		t.Fatalf("not found: code = %d, want 40400", envelope.Code)
	}

	// Local miss + MANAPI transport error → 50000.
	agg = newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	agg.SetRemotePinFetcher(&fakeFetcher{err: fmt.Errorf("dial tcp: timeout")})
	router = newTestRouter(agg)
	if envelope := doPinRead(t, router, hexPinId('a', 0)); envelope.Code != codeUnavailable {
		t.Fatalf("transport error: code = %d, want 50000", envelope.Code)
	}
}

func TestHandlePinRead_LocalAnyVersionResolvesLatest(t *testing.T) {
	published := setupPublishedContent(t)
	source := hexPinId('b', 0)
	version2 := hexPinId('c', 1)

	body := `{"title":"Chain Note","subtitle":"","contentType":"text/markdown","content":"v1 body"}`
	if _, err := published.HandleBlockPin(pinInscription(source, publishedcontent.PathSimpleNote, "create", "", body)); err != nil {
		t.Fatalf("create: %v", err)
	}
	bodyV2 := `{"title":"Chain Note v2","subtitle":"","contentType":"text/markdown","content":"v2 body"}`
	if _, err := published.HandleBlockPin(pinInscription(version2, publishedcontent.PathSimpleNote, "modify", source, bodyV2)); err != nil {
		t.Fatalf("modify: %v", err)
	}

	agg := newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	router := newTestRouter(agg)

	// Addressing the SOURCE pin id resolves to the latest version.
	envelope := doPinRead(t, router, source)
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	data := envelope.Data
	if data.PinId != source || data.CurrentPinId != version2 {
		t.Fatalf("pinId/currentPinId = %q / %q", data.PinId, data.CurrentPinId)
	}
	if data.Protocol != "simplenote" || data.Path != "/protocols/simplenote" || data.Source != "local" {
		t.Fatalf("protocol/path/source = %q %q %q", data.Protocol, data.Path, data.Source)
	}
	if data.Operation != "modify" {
		t.Fatalf("operation = %q, want modify", data.Operation)
	}
	if data.Text == nil || *data.Text != "v2 body" {
		t.Fatalf("text = %v, want v2 body", data.Text)
	}
	if data.Truncated == nil || *data.Truncated || data.TotalLength == nil || *data.TotalLength != 7 {
		t.Fatalf("truncated/totalLength = %v / %v", data.Truncated, data.TotalLength)
	}
	if data.Meta.Title != "Chain Note v2" {
		t.Fatalf("meta.title = %q", data.Meta.Title)
	}
	var payload map[string]any
	if err := json.Unmarshal(data.Payload, &payload); err != nil || payload["title"] != "Chain Note v2" {
		t.Fatalf("payload = %s, err = %v", string(data.Payload), err)
	}
	if data.Attachments == nil || len(data.Attachments) != 0 {
		t.Fatalf("attachments must be an empty array, got %v", data.Attachments)
	}
}

func TestHandlePinRead_TextTruncationKeepsPayloadWhole(t *testing.T) {
	published := setupPublishedContent(t)
	pinId := hexPinId('d', 0)

	longContent := strings.Repeat("长", 8500)
	body, _ := json.Marshal(map[string]any{
		"title":       "Long Note",
		"contentType": "text/markdown",
		"content":     longContent,
	})
	if _, err := published.HandleBlockPin(pinInscription(pinId, publishedcontent.PathSimpleNote, "create", "", string(body))); err != nil {
		t.Fatalf("create: %v", err)
	}

	agg := newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	router := newTestRouter(agg)

	envelope := doPinRead(t, router, pinId)
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	data := envelope.Data
	if data.Text == nil || utf8.RuneCountInString(*data.Text) != textMaxRunes {
		t.Fatalf("text length = %d, want %d", textLen(data.Text), textMaxRunes)
	}
	if data.Truncated == nil || !*data.Truncated {
		t.Fatalf("truncated = %v, want true", data.Truncated)
	}
	if data.TotalLength == nil || *data.TotalLength != 8500 {
		t.Fatalf("totalLength = %v, want 8500", data.TotalLength)
	}
	// payload is never truncated — it is the continuation path.
	var payload map[string]any
	if err := json.Unmarshal(data.Payload, &payload); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if content, _ := payload["content"].(string); utf8.RuneCountInString(content) != 8500 {
		t.Fatalf("payload content truncated: %d runes", utf8.RuneCountInString(content))
	}
}

func textLen(text *string) int {
	if text == nil {
		return -1
	}
	return utf8.RuneCountInString(*text)
}

func TestHandlePinRead_AttachmentsResolved(t *testing.T) {
	published := setupPublishedContent(t)
	pinId := hexPinId('e', 0)

	body := `{"content":"buzz with image","attachments":[{"uri":"metafile://ab12i0.png","contentType":"image/png","size":12345},{"uri":"metafile://cd34i0.jpg"}]}`
	if _, err := published.HandleBlockPin(pinInscription(pinId, publishedcontent.PathSimpleBuzz, "create", "", body)); err != nil {
		t.Fatalf("create: %v", err)
	}

	agg := newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	agg.SetAssetResolver(skillservice.NewAssetResolver("https://file.metaid.io/metafile-indexer/content"))
	router := newTestRouter(agg)

	envelope := doPinRead(t, router, pinId)
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	attachments := envelope.Data.Attachments
	if len(attachments) != 2 {
		t.Fatalf("attachments = %v", attachments)
	}
	if attachments[0].URL != "https://file.metaid.io/metafile-indexer/content/ab12i0.png" {
		t.Fatalf("attachment url = %q", attachments[0].URL)
	}
	if attachments[0].Size == nil || *attachments[0].Size != 12345 {
		t.Fatalf("attachment size = %v", attachments[0].Size)
	}
	if attachments[1].Size != nil {
		t.Fatalf("unknown size must be null, got %v", *attachments[1].Size)
	}
	// simplebuzz meta: title = first line of the stripped body.
	if envelope.Data.Meta.Title != "buzz with image" {
		t.Fatalf("meta.title = %q", envelope.Data.Meta.Title)
	}
}

func TestHandlePinRead_RemoteEncryptedAndEmpty(t *testing.T) {
	published := setupPublishedContent(t)

	// Encrypted remote body: payload and text are null, no error.
	agg := newTestAggregator(t)
	agg.SetPinLookups(published, nil)
	agg.SetRemotePinFetcher(&fakeFetcher{pin: &RemotePin{
		PinId:       hexPinId('f', 0),
		Path:        "/protocols/simplenote",
		Operation:   "create",
		ContentType: "application/json",
		ContentBody: []byte("c2VjcmV0"),
		ChainName:   "mvc",
		Timestamp:   1755000000,
		Encryption:  "1",
	}})
	router := newTestRouter(agg)
	envelope := doPinRead(t, router, hexPinId('f', 0))
	if envelope.Code != 0 {
		t.Fatalf("encrypted: code = %d message = %q", envelope.Code, envelope.Message)
	}
	if string(envelope.Data.Payload) != "null" || envelope.Data.Text != nil {
		t.Fatalf("encrypted payload/text = %s / %v, want null", string(envelope.Data.Payload), envelope.Data.Text)
	}
	if envelope.Data.Truncated != nil || envelope.Data.TotalLength != nil {
		t.Fatalf("encrypted truncated/totalLength must be null")
	}
	if envelope.Data.Source != "remote" {
		t.Fatalf("source = %q, want remote", envelope.Data.Source)
	}

	// Empty local body: payload and text are null, no error.
	published2 := setupPublishedContent(t)
	emptyPin := hexPinId('0', 0)
	if _, err := published2.HandleBlockPin(pinInscription(emptyPin, publishedcontent.PathSimpleBuzz, "create", "", "")); err != nil {
		t.Fatalf("empty create: %v", err)
	}
	agg = newTestAggregator(t)
	agg.SetPinLookups(published2, nil)
	router = newTestRouter(agg)
	envelope = doPinRead(t, router, emptyPin)
	if envelope.Code != 0 {
		t.Fatalf("empty: code = %d message = %q", envelope.Code, envelope.Message)
	}
	if string(envelope.Data.Payload) != "null" || envelope.Data.Text != nil {
		t.Fatalf("empty payload/text = %s / %v, want null", string(envelope.Data.Payload), envelope.Data.Text)
	}
}

func TestMANAPIPinFetcher_DecodesLiveShape(t *testing.T) {
	pinId := hexPinId('9', 0)
	payload := `{"title":"Remote Note","content":"remote body"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/"+pinId {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":1,"message":"ok","data":{"id":%q,"path":"/protocols/simplenote","operation":"create","contentType":"application/json","contentBody":%q,"metaid":"meta-1","globalMetaId":"gid-1","address":"addr-1","chainName":"mvc","timestamp":1755000000,"encryption":"0","modify_history":[%q,%q]}}`,
			pinId, base64.StdEncoding.EncodeToString([]byte(payload)), hexPinId('9', 1), hexPinId('9', 2))
	}))
	defer server.Close()

	fetcher := NewMANAPIPinFetcher(server.URL, nil)
	pin, err := fetcher.FetchPin(pinId)
	if err != nil {
		t.Fatalf("FetchPin: %v", err)
	}
	if !strings.Contains(string(pin.ContentBody), "remote body") {
		t.Fatalf("content body not base64-decoded: %q", string(pin.ContentBody))
	}

	// Full handler path: modify_history tail becomes currentPinId.
	agg := newTestAggregator(t)
	agg.SetPinLookups(setupPublishedContent(t), nil)
	agg.SetRemotePinFetcher(fetcher)
	router := newTestRouter(agg)
	envelope := doPinRead(t, router, pinId)
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	if envelope.Data.CurrentPinId != hexPinId('9', 2) {
		t.Fatalf("currentPinId = %q, want last modify_history entry", envelope.Data.CurrentPinId)
	}
	if envelope.Data.Text == nil || *envelope.Data.Text != "remote body" {
		t.Fatalf("text = %v", envelope.Data.Text)
	}
}

func TestMANAPIPinFetcher_NoPinFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":100,"message":"no pin found.","data":null}`))
	}))
	defer server.Close()

	fetcher := NewMANAPIPinFetcher(server.URL, nil)
	if _, err := fetcher.FetchPin(hexPinId('8', 0)); err != ErrRemotePinNotFound {
		t.Fatalf("err = %v, want ErrRemotePinNotFound", err)
	}
}

// pinInscription builds an aggregator-level pin for the publishedcontent
// handlers used by these tests.
func pinInscription(pinId, path, operation, originalId, jsonBody string) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:          pinId,
		Path:        path,
		Operation:   operation,
		OriginalId:  originalId,
		ContentType: "application/json",
		ContentBody: []byte(jsonBody),
		ChainName:   "mvc",
		Timestamp:   1755000000,
		MetaId:      "meta-user",
		Address:     "addr-user",
	}
}
