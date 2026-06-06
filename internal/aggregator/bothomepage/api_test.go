package bothomepage

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newHandlerFixture(t *testing.T) (*gin.Engine, *Aggregator, *fakeProfileLookup) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.now = func() int64 { return 1780760000000 }
	agg.SetAssetBaseURL("https://file.metaid.io/metafile-indexer/content")

	lookup := &fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId:  "idqCanonicalBotLongValue",
		MetaId:        "metaBot",
		Address:       "1BotAddress",
		Name:          "Fortune Bot",
		Avatar:        "/content/avatar-pin",
		Background:    "/content/background-pin",
		Bio:           "Reads the chain and answers directly.",
		ChatPublicKey: "02chatpubkey",
		ChainName:     "mvc",
	}}
	agg.SetProfileLookup(lookup)

	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))

	return router, agg, lookup
}

func callHomepage(t *testing.T, router *gin.Engine, path string) (int, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v; body=%q", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestHandleGlobalMetaIDSuccessEnvelope(t *testing.T) {
	router, _, _ := newHandlerFixture(t)

	status, body := callHomepage(t, router, "/api/bot-homepage/globalmetaid/idqBot")

	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if body["code"] != float64(0) {
		t.Fatalf("code = %v, want 0; body=%v", body["code"], body)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("data = %T, want object; body=%v", body["data"], body)
	}
	if data["schemaVersion"] != "botHomepage.v1" {
		t.Fatalf("schemaVersion = %v, want botHomepage.v1; data=%v", data["schemaVersion"], data)
	}
}

func TestHandleGlobalMetaIDInvalidParameter(t *testing.T) {
	router, _, _ := newHandlerFixture(t)

	status, body := callHomepage(t, router, "/api/bot-homepage/globalmetaid/%20%20")

	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if body["code"] != float64(40000) {
		t.Fatalf("code = %v, want 40000; body=%v", body["code"], body)
	}
	if body["message"] != "invalid parameter" {
		t.Fatalf("message = %v, want invalid parameter; body=%v", body["message"], body)
	}
}

func TestHandleGlobalMetaIDUnknownBot(t *testing.T) {
	router, _, lookup := newHandlerFixture(t)
	lookup.profile = nil

	status, body := callHomepage(t, router, "/api/bot-homepage/globalmetaid/missingBot")

	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if body["code"] != float64(40400) {
		t.Fatalf("code = %v, want 40400; body=%v", body["code"], body)
	}
	if body["message"] != "bot homepage not found" {
		t.Fatalf("message = %v, want bot homepage not found; body=%v", body["message"], body)
	}
}

func TestHandleGlobalMetaIDInvalidQuery(t *testing.T) {
	router, _, _ := newHandlerFixture(t)

	status, body := callHomepage(t, router, "/api/bot-homepage/globalmetaid/idqBot?includeServices=maybe")

	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if body["code"] != float64(40000) {
		t.Fatalf("code = %v, want 40000; body=%v", body["code"], body)
	}
	if body["message"] != "invalid parameter" {
		t.Fatalf("message = %v, want invalid parameter; body=%v", body["message"], body)
	}
}

func TestHandleGlobalMetaIDAggregationUnavailable(t *testing.T) {
	router, _, lookup := newHandlerFixture(t)
	lookup.err = errors.New("lookup failed")

	status, body := callHomepage(t, router, "/api/bot-homepage/globalmetaid/idqBot")

	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if body["code"] != float64(50000) {
		t.Fatalf("code = %v, want 50000; body=%v", body["code"], body)
	}
	if body["message"] != "aggregation unavailable" {
		t.Fatalf("message = %v, want aggregation unavailable; body=%v", body["message"], body)
	}
}
