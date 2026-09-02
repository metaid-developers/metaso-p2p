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

	// Equal raw score (title-only hit of "knotwork"; each namespace has a
	// single doc, so df=1 keeps the full weight 4 → raw 20 each; the
	// two-token query avoids the exact-phrase boost): the simplenote prior
	// ×1.2 beats the simplebuzz prior ×0.9, and the reported scores are
	// prior-adjusted (24 vs 18).
	envelope := doSearch(t, router, "q=knotwork%20guide")
	if envelope.Code != 0 {
		t.Fatalf("code = %d message = %q", envelope.Code, envelope.Message)
	}
	if len(envelope.Data.Items) != 2 {
		t.Fatalf("items = %+v", envelope.Data.Items)
	}
	if envelope.Data.Items[0].PinId != "pin-note:i0" || envelope.Data.Items[0].Score != 24 {
		t.Fatalf("first item = %+v, want pin-note:i0 score 24", envelope.Data.Items[0])
	}
	if envelope.Data.Items[1].PinId != "pin-buzz:i0" || envelope.Data.Items[1].Score != 18 {
		t.Fatalf("second item = %+v, want pin-buzz:i0 score 18", envelope.Data.Items[1])
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

	// Raw 44 (all-field hit, full weight 4: each namespace has one doc, df=1)
	// × 0.9 = 39 still beats raw 20 × 1.2 = 24: the prior breaks near-ties,
	// it does not override a clear signal lead.
	envelope := doSearch(t, router, "q=knotwork%20guide")
	if len(envelope.Data.Items) != 2 || envelope.Data.Items[0].PinId != "pin-buzz:i0" {
		t.Fatalf("items = %+v", envelope.Data.Items)
	}
	if envelope.Data.Items[0].Score != 39 || envelope.Data.Items[1].Score != 24 {
		t.Fatalf("scores = %d, %d; want 39, 24",
			envelope.Data.Items[0].Score, envelope.Data.Items[1].Score)
	}
}

// Q1b end-to-end: same-publisher duplicates collapse to one row with
// extra.duplicateCount, while a cross-publisher identical copy is kept
// (quote/citation reposts are out of scope).
func TestHandleSearch_DedupeEndToEnd(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: []metawebdoc.Document{
		{
			ProtocolKey:           "simplenote",
			SourcePinId:           "pin-dup1:i0",
			CurrentPinId:          "pin-dup1:i0",
			ChainName:             "mvc",
			Title:                 "dedupe target tutorial",
			ContentExcerpt:        "identical body text",
			PublisherGlobalMetaId: "gid-same",
			CreatedAt:             1755000000,
			Extra:                 map[string]any{"contentType": "text/markdown"},
		},
		{
			ProtocolKey:           "simplenote",
			SourcePinId:           "pin-dup2:i0",
			CurrentPinId:          "pin-dup2:i0",
			ChainName:             "mvc",
			Title:                 "dedupe target tutorial",
			ContentExcerpt:        "identical body text",
			PublisherGlobalMetaId: "gid-same",
			CreatedAt:             1754000000,
		},
		{
			ProtocolKey:           "simplebuzz",
			SourcePinId:           "pin-quote:i0",
			CurrentPinId:          "pin-quote:i0",
			ChainName:             "mvc",
			Title:                 "dedupe target tutorial",
			ContentExcerpt:        "identical body text",
			PublisherGlobalMetaId: "gid-quoter",
			CreatedAt:             1754500000,
		},
	}})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=dedupe%20target")
	if len(envelope.Data.Items) != 2 {
		t.Fatalf("items = %d, want 2 (one collapsed row + cross-publisher copy)", len(envelope.Data.Items))
	}
	first := envelope.Data.Items[0]
	if first.PinId != "pin-dup1:i0" {
		t.Fatalf("representative = %s, want pin-dup1:i0 (highest score, then newest)", first.PinId)
	}
	if got, ok := first.Extra["duplicateCount"].(float64); !ok || int(got) != 1 {
		t.Fatalf("extra.duplicateCount = %v, want 1", first.Extra["duplicateCount"])
	}
	if first.Extra["contentType"] != "text/markdown" {
		t.Fatalf("protocol extra lost: %v", first.Extra)
	}
	if _, ok := first.Extra["versions"]; ok {
		t.Fatalf("hard collapse must not carry versions: %v", first.Extra)
	}
	if _, ok := envelope.Data.Items[1].Extra["duplicateCount"]; ok {
		t.Fatalf("cross-publisher copy must not be annotated: %v", envelope.Data.Items[1].Extra)
	}
}

// Q1b end-to-end: explicit version markers form one versions-group row,
// representative = newest, every member listed newest first.
func TestHandleSearch_DedupeVersionsGroupEndToEnd(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: []metawebdoc.Document{
		{
			ProtocolKey:           "simplenote",
			SourcePinId:           "pin-v1:i0",
			CurrentPinId:          "pin-v1:i0",
			ChainName:             "mvc",
			Title:                 "versioned report（v1）",
			ContentExcerpt:        "versioned body v1",
			PublisherGlobalMetaId: "gid-same",
			CreatedAt:             1754000000,
		},
		{
			ProtocolKey:           "simplenote",
			SourcePinId:           "pin-v2:i0",
			CurrentPinId:          "pin-v2:i0",
			ChainName:             "mvc",
			Title:                 "versioned report（v2）",
			ContentExcerpt:        "versioned body v2",
			PublisherGlobalMetaId: "gid-same",
			CreatedAt:             1755000000,
		},
	}})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=versioned%20report")
	if len(envelope.Data.Items) != 1 {
		t.Fatalf("items = %d, want 1 versions-group row", len(envelope.Data.Items))
	}
	item := envelope.Data.Items[0]
	if item.PinId != "pin-v2:i0" {
		t.Fatalf("representative = %s, want pin-v2:i0 (newest)", item.PinId)
	}
	if got, ok := item.Extra["duplicateCount"].(float64); !ok || int(got) != 1 {
		t.Fatalf("extra.duplicateCount = %v, want 1", item.Extra["duplicateCount"])
	}
	versions, ok := item.Extra["versions"].([]any)
	if !ok || len(versions) != 2 {
		t.Fatalf("extra.versions = %v, want 2 entries", item.Extra["versions"])
	}
	v0 := versions[0].(map[string]any)
	if v0["pinId"] != "pin-v2:i0" || v0["version"] != "v2" {
		t.Fatalf("versions[0] = %v, want pin-v2:i0 / v2", v0)
	}
	v1 := versions[1].(map[string]any)
	if v1["pinId"] != "pin-v1:i0" || v1["version"] != "v1" {
		t.Fatalf("versions[1] = %v, want pin-v1:i0 / v1", v1)
	}
}

// Q1b cursor semantics: suppression happens before slicing, so walking the
// cursor visits every deduped row exactly once and hasMore terminates.
func TestHandleSearch_DedupePaginationTerminates(t *testing.T) {
	docs := []metawebdoc.Document{}
	// Three duplicate pairs (same publisher, identical title+body) plus two
	// singletons: 8 matches, 5 deduped rows.
	for _, pair := range []string{"alpha", "beta", "gamma"} {
		for _, suffix := range []string{"a", "b"} {
			docs = append(docs, metawebdoc.Document{
				ProtocolKey:           "simplebuzz",
				SourcePinId:           "pin-" + pair + "-" + suffix + ":i0",
				CurrentPinId:          "pin-" + pair + "-" + suffix + ":i0",
				ChainName:             "mvc",
				Title:                 "walk " + pair + " tutorial",
				ContentExcerpt:        "walk " + pair + " body",
				PublisherGlobalMetaId: "gid-walk",
				CreatedAt:             1755000000,
			})
		}
	}
	docs = append(docs,
		metawebdoc.Document{
			ProtocolKey:           "simplebuzz",
			SourcePinId:           "pin-solo-1:i0",
			CurrentPinId:          "pin-solo-1:i0",
			ChainName:             "mvc",
			Title:                 "walk solo one",
			ContentExcerpt:        "walk solo body one",
			PublisherGlobalMetaId: "gid-walk",
			CreatedAt:             1755000001,
		},
		metawebdoc.Document{
			ProtocolKey:           "simplebuzz",
			SourcePinId:           "pin-solo-2:i0",
			CurrentPinId:          "pin-solo-2:i0",
			ChainName:             "mvc",
			Title:                 "walk solo two",
			ContentExcerpt:        "walk solo body two",
			PublisherGlobalMetaId: "gid-walk",
			CreatedAt:             1755000002,
		},
	)
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: docs})
	router := newTestRouter(agg)

	seen := map[string]int{}
	cursor := ""
	pages := 0
	for {
		query := "q=walk&sort=newest&size=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		envelope := doSearch(t, router, query)
		pages++
		if pages > 10 {
			t.Fatalf("pagination did not terminate")
		}
		for _, item := range envelope.Data.Items {
			seen[item.PinId]++
		}
		if !envelope.Data.HasMore {
			if envelope.Data.NextCursor != nil {
				t.Fatalf("last page must carry null nextCursor")
			}
			break
		}
		cursor = *envelope.Data.NextCursor
	}
	if len(seen) != 5 {
		t.Fatalf("walked %d rows, want 5 deduped rows: %v", len(seen), seen)
	}
	for pinId, count := range seen {
		if count != 1 {
			t.Fatalf("row %s visited %d times", pinId, count)
		}
	}
}

// Q1a regression: one modify chain is one search document — only the latest
// version is linked, and no second row can appear for older versions.
func TestHandleSearch_ModifyChainCollapse(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetDocumentSources(&fakeDocSource{docs: []metawebdoc.Document{
		{
			ProtocolKey:           "simplenote",
			SourcePinId:           "pin-chain-v1:i0",
			CurrentPinId:          "pin-chain-v3:i0",
			ChainName:             "mvc",
			Title:                 "chained document",
			ContentExcerpt:        "chained body",
			PublisherGlobalMetaId: "gid-chain",
			CreatedAt:             1755000000,
		},
	}})
	router := newTestRouter(agg)

	envelope := doSearch(t, router, "q=chained")
	if len(envelope.Data.Items) != 1 {
		t.Fatalf("items = %d, want exactly 1 row per modify chain", len(envelope.Data.Items))
	}
	item := envelope.Data.Items[0]
	if item.PinId != "pin-chain-v1:i0" || item.CurrentPinId != "pin-chain-v3:i0" {
		t.Fatalf("pin fields = %s / %s", item.PinId, item.CurrentPinId)
	}
	if item.Links["pin"] != "/api/metaweb/pin/pin-chain-v3:i0" {
		t.Fatalf("links.pin = %v, want latest version", item.Links["pin"])
	}
}
