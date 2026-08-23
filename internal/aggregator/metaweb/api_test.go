package metaweb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

type fakeDocSource struct {
	docs []metawebdoc.Document
}

func (f *fakeDocSource) SearchDocuments() []metawebdoc.Document { return f.docs }

type fakeNamer struct {
	names map[string]string
}

func (f *fakeNamer) ProfileNameAvatar(globalMetaId, metaId string) (string, string) {
	if name, ok := f.names[globalMetaId]; ok {
		return name, "metafile://avatar-" + globalMetaId
	}
	return "", ""
}

func newTestAggregator(t *testing.T) *Aggregator {
	t.Helper()
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg
}

func newTestRouter(agg *Aggregator) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.RequestTimingMiddleware())
	agg.RegisterRoutes(router.Group("/api"))
	return router
}

type searchEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items []struct {
			Protocol     string         `json:"protocol"`
			PinId        string         `json:"pinId"`
			CurrentPinId string         `json:"currentPinId"`
			Title        string         `json:"title"`
			Tags         []string       `json:"tags"`
			Score        int            `json:"score"`
			CreatedAt    int64          `json:"createdAt"`
			Links        map[string]any `json:"links"`
			Extra        map[string]any `json:"extra"`
			Publisher    struct {
				GlobalMetaId string `json:"globalMetaId"`
				Name         string `json:"name"`
				Avatar       string `json:"avatar"`
			} `json:"publisher"`
		} `json:"items"`
		NextCursor *string `json:"nextCursor"`
		HasMore    bool    `json:"hasMore"`
	} `json:"data"`
}

func doSearch(t *testing.T, router *gin.Engine, query string) searchEnvelope {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/search?"+query, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200 (envelope errors stay 200)", recorder.Code)
	}
	var envelope searchEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}

func testDocs() []metawebdoc.Document {
	return []metawebdoc.Document{
		{
			ProtocolKey:           "simplenote",
			SourcePinId:           "pin-note:i0",
			CurrentPinId:          "pin-note-v2:i0",
			ChainName:             "mvc",
			Title:                 "IDBots Beginner Tutorial",
			Summary:               "One-line abstract",
			Tags:                  []string{"idbots", "tutorial"},
			ContentExcerpt:        "tutorial body",
			PublisherGlobalMetaId: "gid-wufeng",
			PublisherMetaId:       "meta-wufeng",
			CreatedAt:             1755000000,
			Extra:                 map[string]any{"contentType": "text/markdown"},
		},
		{
			ProtocolKey:           "skill-service",
			SourcePinId:           "pin-svc:i0",
			CurrentPinId:          "pin-svc:i0",
			ChainName:             "mvc",
			Title:                 "Fortune Teller",
			Summary:               "Daily fortune reading",
			Tags:                  []string{"fortune-skill"},
			ContentExcerpt:        "Daily fortune reading",
			PublisherGlobalMetaId: "gid-other",
			CreatedAt:             1756000000,
			Extra:                 map[string]any{"price": "100"},
		},
		{
			ProtocolKey:           "simplebuzz",
			SourcePinId:           "pin-buzz:i0",
			CurrentPinId:          "pin-buzz:i0",
			ChainName:             "btc",
			Title:                 "Tutorial buzz",
			Summary:               "short note",
			Tags:                  []string{},
			ContentExcerpt:        "tutorial buzz body",
			PublisherGlobalMetaId: "gid-wufeng",
			PublisherMetaId:       "meta-wufeng",
			CreatedAt:             1754000000,
			Extra:                 map[string]any{},
		},
	}
}

func TestHandleSearch_RelevanceAndEnrichment(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: testDocs()})
	agg.SetProfileNamer(&fakeNamer{names: map[string]string{"gid-wufeng": "WuFenGBot"}})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=tutorial")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	// "tutorial" hits note (title+tags+summary?+content) and buzz; note wins.
	if len(envelope.Data.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(envelope.Data.Items))
	}
	first := envelope.Data.Items[0]
	if first.PinId != "pin-note:i0" {
		t.Fatalf("first item = %q, want pin-note:i0", first.PinId)
	}
	if first.Links["pin"] != "/api/metaweb/pin/pin-note-v2:i0" {
		t.Fatalf("links.pin = %v, want currentPinId link", first.Links["pin"])
	}
	if first.Publisher.Name != "WuFenGBot" || first.Publisher.Avatar != "metafile://avatar-gid-wufeng" {
		t.Fatalf("publisher enrichment = %+v", first.Publisher)
	}
	if first.Score <= 0 {
		t.Fatalf("score = %d", first.Score)
	}
	if envelope.Data.HasMore || envelope.Data.NextCursor != nil {
		t.Fatalf("unexpected hasMore/nextCursor: %v %v", envelope.Data.HasMore, envelope.Data.NextCursor)
	}
}

func TestHandleSearch_ProtocolAndPublisherFilters(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: testDocs()})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=tutorial&protocols=simplenote")
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].Protocol != "simplenote" {
		t.Fatalf("protocol filter items = %+v", envelope.Data.Items)
	}

	// publisher matches globalMetaId OR metaid, case-insensitive.
	envelope = doSearch(t, router, "q=tutorial&publisher=META-WUFENG")
	if len(envelope.Data.Items) != 2 {
		t.Fatalf("publisher metaid filter items = %d, want 2", len(envelope.Data.Items))
	}
	envelope = doSearch(t, router, "q=tutorial&publisher=gid-other")
	if len(envelope.Data.Items) != 0 {
		t.Fatalf("publisher gid-other filter items = %d, want 0", len(envelope.Data.Items))
	}
}

func TestHandleSearch_SinceUntilFilter(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: testDocs()})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=tutorial&since=1754500000&until=1755500000")
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].PinId != "pin-note:i0" {
		t.Fatalf("since/until filter items = %+v", envelope.Data.Items)
	}
}

func TestHandleSearch_NewestSort(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: testDocs()})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=tutorial&sort=newest")
	if len(envelope.Data.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(envelope.Data.Items))
	}
	// newest: createdAt desc, score bypassed (reported 0).
	if envelope.Data.Items[0].PinId != "pin-note:i0" || envelope.Data.Items[1].PinId != "pin-buzz:i0" {
		t.Fatalf("newest order = %q, %q", envelope.Data.Items[0].PinId, envelope.Data.Items[1].PinId)
	}
	for _, item := range envelope.Data.Items {
		if item.Score != 0 {
			t.Fatalf("newest score = %d, want 0", item.Score)
		}
	}
}

func TestHandleSearch_Pagination(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: testDocs()})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=tutorial&size=1")
	if len(envelope.Data.Items) != 1 || !envelope.Data.HasMore || envelope.Data.NextCursor == nil {
		t.Fatalf("first page = %+v", envelope.Data)
	}
	envelope = doSearch(t, router, "q=tutorial&size=1&cursor="+*envelope.Data.NextCursor)
	if len(envelope.Data.Items) != 1 || envelope.Data.HasMore || envelope.Data.NextCursor != nil {
		t.Fatalf("second page = %+v", envelope.Data)
	}

	// Offset past the result end yields an empty page with hasMore=false.
	envelope = doSearch(t, router, "q=tutorial&cursor="+encodeSearchCursor(99))
	if envelope.Code != 0 || len(envelope.Data.Items) != 0 || envelope.Data.HasMore {
		t.Fatalf("past-end page = %+v", envelope.Data)
	}
}

func TestHandleSearch_SizeClamp(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: testDocs()})
	router := newTestRouter(agg)

	// size=100 clamps to 50 — accepted, no 40000.
	envelope := doSearch(t, router, "q=tutorial&size=100")
	if envelope.Code != 0 {
		t.Fatalf("size=100 rejected: code=%d message=%q", envelope.Code, envelope.Message)
	}
	if len(envelope.Data.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(envelope.Data.Items))
	}
}

func TestHandleSearch_ParamValidation(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: testDocs()})
	router := newTestRouter(agg)

	cases := map[string]string{
		"":                                     "empty q",
		"q=%20%20":                             "whitespace q",
		"q=x&protocols=simplenote,unknownprot": "unknown protocol",
		"q=x&since=abc":                        "non-numeric since",
		"q=x&until=soon":                       "non-numeric until",
		"q=x&since=20&until=10":                "since > until",
		"q=x&sort=random":                      "invalid sort",
		"q=x&size=abc":                         "non-numeric size",
		"q=x&size=0":                           "size < 1",
		"q=x&cursor=!!!":                       "invalid cursor",
	}
	for query, name := range cases {
		envelope := doSearch(t, router, query)
		if envelope.Code != codeInvalidParam {
			t.Errorf("%s: code = %d, want 40000 (message %q)", name, envelope.Code, envelope.Message)
		}
	}
}

func TestHandleSearch_NoDocumentSources(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=tutorial")
	if envelope.Code != codeUnavailable {
		t.Fatalf("code = %d, want 50000", envelope.Code)
	}
}
