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

func TestHandleSearch_AllStopwordQueryReturnsEmpty(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: testDocs()})
	router := newTestRouter(agg)

	// An all-stopword query matches nothing: code 0 with an empty page.
	envelope := doSearch(t, router, "q=what%20is")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	if len(envelope.Data.Items) != 0 || envelope.Data.HasMore {
		t.Fatalf("all-stopword items = %+v", envelope.Data)
	}

	// Same for sort=newest admission.
	envelope = doSearch(t, router, "q=what%20is&sort=newest")
	if envelope.Code != 0 || len(envelope.Data.Items) != 0 {
		t.Fatalf("all-stopword newest items = %+v", envelope.Data)
	}
}

func TestHandleSearch_MixedStopwordQueryScoresContentWordOnly(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: []metawebdoc.Document{
		{
			ProtocolKey:    "simplenote",
			SourcePinId:    "pin-seedance:i0",
			ChainName:      "mvc",
			Title:          "Seedance video generation guide",
			Summary:        "text to video",
			Tags:           []string{},
			ContentExcerpt: "seedance body",
			CreatedAt:      1755000000,
		},
		{
			ProtocolKey:    "simplebuzz",
			SourcePinId:    "pin-chatter:i0",
			ChainName:      "mvc",
			Title:          "what is this",
			Summary:        "what is it about",
			Tags:           []string{},
			ContentExcerpt: "what is history",
			CreatedAt:      1756000000,
		},
	}})
	router := newTestRouter(agg)

	// Stopwords score nothing; only the seedance doc is returned. The
	// chatter doc must not accumulate stopword weight, and "is" must not
	// hit "this"/"history".
	envelope := doSearch(t, router, "q=what%20is%20seedance")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	if len(envelope.Data.Items) != 1 || envelope.Data.Items[0].PinId != "pin-seedance:i0" {
		t.Fatalf("items = %+v", envelope.Data.Items)
	}
}

func TestHandleSearch_SeedanceRegression(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: []metawebdoc.Document{
		{
			ProtocolKey:    "metabot-skill",
			SourcePinId:    "pin-dsh:i0",
			ChainName:      "mvc",
			Title:          "dsh ping skill",
			Summary:        "test skill",
			Tags:           []string{},
			ContentExcerpt: "ping test body",
			CreatedAt:      1756000000,
		},
		{
			ProtocolKey:    "simplenote",
			SourcePinId:    "pin-seedance:i0",
			ChainName:      "mvc",
			Title:          "Seedance video generation notes",
			Summary:        "how to drive seedance",
			Tags:           []string{"video"},
			ContentExcerpt: "seedance body",
			CreatedAt:      1755000000,
		},
	}})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=seedance")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	if len(envelope.Data.Items) == 0 || envelope.Data.Items[0].PinId != "pin-seedance:i0" {
		t.Fatalf("seedance doc not first: %+v", envelope.Data.Items)
	}
}

func TestHandleSearch_ChineseQueryRegression(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: []metawebdoc.Document{
		{
			ProtocolKey:    "simplenote",
			SourcePinId:    "pin-zh:i0",
			ChainName:      "mvc",
			Title:          "MetaID 协议：数字身份是什么",
			Summary:        "数字身份协议入门",
			Tags:           []string{},
			ContentExcerpt: "数字身份 协议 body",
			CreatedAt:      1755000000,
		},
		{
			ProtocolKey:    "simplenote",
			SourcePinId:    "pin-en:i0",
			ChainName:      "mvc",
			Title:          "MetaID protocol identity on Bitcoin",
			Summary:        "english explainer",
			Tags:           []string{},
			ContentExcerpt: "identity body",
			CreatedAt:      1756000000,
		},
	}})
	router := newTestRouter(agg)

	// CJK bigram/substring scoring must keep the Chinese doc on top.
	envelope := doSearch(t, router, "q=MetaID%20%E6%98%AF%E4%BB%80%E4%B9%88%20%E5%8D%8F%E8%AE%AE%20%E6%95%B0%E5%AD%97%E8%BA%AB%E4%BB%BD")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	if len(envelope.Data.Items) != 2 || envelope.Data.Items[0].PinId != "pin-zh:i0" {
		t.Fatalf("items = %+v", envelope.Data.Items)
	}
}

func TestHandleSearch_ProtocolPriorNearTie(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: []metawebdoc.Document{
		{
			ProtocolKey:    "simplenote",
			SourcePinId:    "pin-note:i0",
			ChainName:      "mvc",
			Title:          "knotwork intro",
			Tags:           []string{},
			ContentExcerpt: "intro body",
			CreatedAt:      1755000000,
		},
		{
			ProtocolKey:    "simplebuzz",
			SourcePinId:    "pin-buzz:i0",
			ChainName:      "mvc",
			Title:          "knotwork chatter",
			Tags:           []string{},
			ContentExcerpt: "chatter body",
			CreatedAt:      1756000000,
		},
		{
			ProtocolKey:    "metaprotocol",
			SourcePinId:    "pin-unrelated:i0",
			ChainName:      "mvc",
			Title:          "unrelated spec",
			Tags:           []string{},
			ContentExcerpt: "nothing relevant",
			CreatedAt:      1757000000,
		},
	}})
	router := newTestRouter(agg)

	// Equal raw score (title-only hit of "knotwork", halved IDF weight 2 →
	// raw 10 each; the two-token query avoids the exact-phrase boost): the
	// simplenote prior ×1.2 beats the simplebuzz prior ×0.9, and the
	// reported scores are prior-adjusted (12 vs 9).
	envelope := doSearch(t, router, "q=knotwork%20guide")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	if len(envelope.Data.Items) != 2 {
		t.Fatalf("items = %+v", envelope.Data.Items)
	}
	if envelope.Data.Items[0].PinId != "pin-note:i0" || envelope.Data.Items[0].Score != 12 {
		t.Fatalf("first item = %+v, want pin-note:i0 score 12", envelope.Data.Items[0])
	}
	if envelope.Data.Items[1].PinId != "pin-buzz:i0" || envelope.Data.Items[1].Score != 9 {
		t.Fatalf("second item = %+v, want pin-buzz:i0 score 9", envelope.Data.Items[1])
	}

	// sort=newest is untouched by priors: createdAt order, score 0.
	envelope = doSearch(t, router, "q=knotwork%20guide&sort=newest")
	if len(envelope.Data.Items) != 2 || envelope.Data.Items[0].PinId != "pin-buzz:i0" {
		t.Fatalf("newest items = %+v", envelope.Data.Items)
	}
	for _, item := range envelope.Data.Items {
		if item.Score != 0 {
			t.Fatalf("newest score = %d, want 0", item.Score)
		}
	}
}

func TestHandleSearch_ProtocolPriorDoesNotOverrideClearLead(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: []metawebdoc.Document{
		{
			ProtocolKey:    "simplenote",
			SourcePinId:    "pin-note:i0",
			ChainName:      "mvc",
			Title:          "knotwork intro",
			Tags:           []string{},
			ContentExcerpt: "intro body",
			CreatedAt:      1755000000,
		},
		{
			ProtocolKey:    "simplebuzz",
			SourcePinId:    "pin-buzz:i0",
			ChainName:      "mvc",
			Title:          "knotwork deep dive",
			Summary:        "knotwork summary",
			Tags:           []string{"knotwork"},
			ContentExcerpt: "knotwork body",
			CreatedAt:      1756000000,
		},
	}})
	router := newTestRouter(agg)

	// Raw 22 (all-field hit) × 0.9 = 19 still beats raw 10 × 1.2 = 12: the
	// prior breaks near-ties, it does not override a clear signal lead.
	envelope := doSearch(t, router, "q=knotwork%20guide")
	if len(envelope.Data.Items) != 2 || envelope.Data.Items[0].PinId != "pin-buzz:i0" {
		t.Fatalf("items = %+v", envelope.Data.Items)
	}
	if envelope.Data.Items[0].Score != 19 || envelope.Data.Items[1].Score != 12 {
		t.Fatalf("scores = %d, %d; want 19, 12",
			envelope.Data.Items[0].Score, envelope.Data.Items[1].Score)
	}
}
