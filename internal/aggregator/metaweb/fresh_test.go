package metaweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

type freshItemRow struct {
	PinId        string         `json:"pinId"`
	CurrentPinId string         `json:"currentPinId"`
	Protocol     string         `json:"protocol"`
	Path         string         `json:"path"`
	ChainName    string         `json:"chainName"`
	CreatedAt    int64          `json:"createdAt"`
	Author       map[string]any `json:"author"`
	Title        string         `json:"title"`
	Summary      string         `json:"summary"`
	LikeCount    int            `json:"likeCount"`
	CommentCount int            `json:"commentCount"`
	IsMempool    bool           `json:"isMempool"`
	Duplicates   int            `json:"duplicates"`
	Extra        map[string]any `json:"extra"`
}

type freshEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Items      []freshItemRow   `json:"items"`
		HasMore    bool             `json:"hasMore"`
		NextCursor *string          `json:"nextCursor"`
		ServerTime int64            `json:"serverTime"`
		Suppressed *suppressedBlock `json:"suppressed"`
	} `json:"data"`
}

func doFresh(t *testing.T, router *gin.Engine, query string) freshEnvelope {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/fresh?"+query, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", recorder.Code)
	}
	var envelope freshEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return envelope
}

// freshPin builds a confirmed publishedcontent pin with an explicit
// timestamp and author address (fresh-feed tests need distinct values).
func freshPin(pinId, path, operation, originalId, jsonBody string, ts int64, address string) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:          pinId,
		Path:        path,
		Operation:   operation,
		OriginalId:  originalId,
		ContentType: "application/json",
		ContentBody: []byte(jsonBody),
		ChainName:   "mvc",
		Timestamp:   ts,
		MetaId:      "meta-" + address,
		Address:     address,
	}
}

type fakeBuzzEngagement struct {
	likes, comments int
	ok              bool
}

func (f fakeBuzzEngagement) BuzzEngagement(string) (int, int, bool) { return f.likes, f.comments, f.ok }

type fakeQAEngagement struct {
	questionLikes, questionComments, questionAnswers int
	ok                                               bool
}

func (f fakeQAEngagement) QuestionEngagement(string) (int, int, int, int, bool) {
	return f.questionLikes, 0, f.questionComments, f.questionAnswers, f.ok
}

func (f fakeQAEngagement) AnswerEngagement(string) (int, int, int, bool) {
	return 1, 0, 0, f.ok
}

func newFreshRouter(t *testing.T, published *publishedcontent.Aggregator) *gin.Engine {
	t.Helper()
	agg := newTestAggregator(t)
	agg.SetFreshLookup(published)
	agg.SetEngagementLookups(
		fakeBuzzEngagement{likes: 3, comments: 2, ok: true},
		fakeQAEngagement{questionLikes: 5, questionComments: 1, questionAnswers: 4, ok: true},
	)
	return newTestRouter(agg)
}

func TestHandleFresh_PagingNoGapsNoDuplicates(t *testing.T) {
	published := setupPublishedContent(t)

	type seed struct {
		fill byte
		idx  int
		path string
		ts   int64
		body string
	}
	baseTs := int64(1755000000)
	seeds := []seed{
		{'a', 0, publishedcontent.PathSimpleBuzz, baseTs, `{"content":"buzz one"}`},
		{'b', 0, publishedcontent.PathSimpleNote, baseTs + 10, `{"title":"Note A","content":"note body","contentType":"text/markdown"}`},
		{'c', 0, publishedcontent.PathSimpleQuestion, baseTs + 20, `{"title":"Q?","content":"question body"}`},
		{'d', 0, publishedcontent.PathSimpleBuzz, baseTs + 20, `{"content":"buzz two"}`}, // same second as the question: tiebreak
		{'e', 0, publishedcontent.PathSimpleAnswer, baseTs + 30, `{"content":"answer body","answerTo":"x"}`},
	}
	pinIdOf := func(s seed) string { return hexPinId(s.fill, s.idx) }
	for i, s := range seeds {
		if _, err := published.HandleBlockPin(freshPin(pinIdOf(s), s.path, "create", "", s.body, s.ts, "addr-bot")); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	router := newFreshRouter(t, published)

	var seen []string
	cursor := ""
	pages := 0
	for {
		query := "size=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		envelope := doFresh(t, router, query)
		if envelope.Code != 0 {
			t.Fatalf("page %d: code = %d (%s)", pages, envelope.Code, envelope.Message)
		}
		for _, item := range envelope.Data.Items {
			seen = append(seen, item.PinId)
		}
		if !envelope.Data.HasMore {
			break
		}
		if envelope.Data.NextCursor == nil || *envelope.Data.NextCursor == "" {
			t.Fatalf("page %d: hasMore with empty nextCursor", pages)
		}
		cursor = *envelope.Data.NextCursor
		pages++
		if pages > len(seeds) {
			t.Fatalf("paging did not terminate after %d pages", pages)
		}
	}
	if len(seen) != len(seeds) {
		t.Fatalf("saw %d items, want %d: %v", len(seen), len(seeds), seen)
	}
	uniq := map[string]bool{}
	for _, id := range seen {
		if uniq[id] {
			t.Errorf("duplicate item %s across pages", id)
		}
		uniq[id] = true
	}

	// Newest first, with the (createdAt DESC, pinId DESC) tiebreak: 'd…'
	// sorts after 'c…' at the same second.
	envelope := doFresh(t, router, "size=5")
	wantOrder := []string{hexPinId('e', 0), hexPinId('d', 0), hexPinId('c', 0), hexPinId('b', 0), hexPinId('a', 0)}
	for i, want := range wantOrder {
		if envelope.Data.Items[i].PinId != want {
			t.Fatalf("order[%d] = %s, want %s (full: %v)", i, envelope.Data.Items[i].PinId, want, pinIds(envelope))
		}
	}
}

func TestHandleFresh_SinceInclusiveAndProtocolFilter(t *testing.T) {
	published := setupPublishedContent(t)
	baseTs := int64(1755000000)

	older := hexPinId('a', 0)
	boundary := hexPinId('b', 0)
	newer := hexPinId('c', 0)
	for _, s := range []struct {
		pinId string
		path  string
		ts    int64
		body  string
	}{
		{older, publishedcontent.PathSimpleBuzz, baseTs - 1, `{"content":"old"}`},
		{boundary, publishedcontent.PathSimpleNote, baseTs, `{"title":"B","content":"boundary"}`},
		{newer, publishedcontent.PathSimpleBuzz, baseTs + 1, `{"content":"new"}`},
	} {
		if _, err := published.HandleBlockPin(freshPin(s.pinId, s.path, "create", "", s.body, s.ts, "addr-bot")); err != nil {
			t.Fatalf("seed %s: %v", s.pinId, err)
		}
	}

	router := newFreshRouter(t, published)
	envelope := doFresh(t, router, fmt.Sprintf("since=%d", baseTs))
	if envelope.Code != 0 {
		t.Fatalf("code = %d (%s)", envelope.Code, envelope.Message)
	}
	pins := pinIds(envelope)
	if len(pins) != 2 || pins[0] != newer || pins[1] != boundary {
		t.Fatalf("since window = %v, want [%s %s] (inclusive boundary)", pins, newer, boundary)
	}

	envelope = doFresh(t, router, fmt.Sprintf("since=%d&protocols=simplebuzz", baseTs-10))
	pins = pinIds(envelope)
	if len(pins) != 2 || pins[0] != newer || pins[1] != older {
		t.Fatalf("buzz filter = %v, want [%s %s]", pins, newer, older)
	}
}

func TestHandleFresh_ExcludesRevokedAndCollapsesVersions(t *testing.T) {
	published := setupPublishedContent(t)
	baseTs := int64(1755000000)

	source := hexPinId('a', 0)
	version2 := hexPinId('b', 1)
	if _, err := published.HandleBlockPin(freshPin(source, publishedcontent.PathSimpleNote, "create", "", `{"title":"N","content":"v1"}`, baseTs, "addr-bot")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := published.HandleBlockPin(freshPin(version2, publishedcontent.PathSimpleNote+"@"+source, "modify", source, `{"title":"N v2","content":"v2"}`, baseTs+5, "addr-bot")); err != nil {
		t.Fatalf("modify: %v", err)
	}
	revoked := hexPinId('c', 0)
	if _, err := published.HandleBlockPin(freshPin(revoked, publishedcontent.PathSimpleBuzz, "create", "", `{"content":"gone soon"}`, baseTs+6, "addr-bot")); err != nil {
		t.Fatalf("revoke target create: %v", err)
	}
	revoke := hexPinId('d', 1)
	if _, err := published.HandleBlockPin(freshPin(revoke, publishedcontent.PathSimpleBuzz+"@"+revoked, "revoke", revoked, "", baseTs+7, "addr-bot")); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	router := newFreshRouter(t, published)
	envelope := doFresh(t, router, "size=10")
	pins := pinIds(envelope)
	if len(pins) != 1 || pins[0] != source {
		t.Fatalf("items = %v, want only source %s (modify collapsed, revoked hidden)", pins, source)
	}
	if envelope.Data.Items[0].CurrentPinId != version2 {
		t.Errorf("currentPinId = %s, want %s", envelope.Data.Items[0].CurrentPinId, version2)
	}
	if envelope.Data.Items[0].CreatedAt != baseTs {
		t.Errorf("createdAt = %d, want source createdAt %d", envelope.Data.Items[0].CreatedAt, baseTs)
	}
}

func TestHandleFresh_EngagementJoinsAndExtras(t *testing.T) {
	published := setupPublishedContent(t)
	baseTs := int64(1755000000)

	buzz := hexPinId('a', 0)
	question := hexPinId('b', 0)
	note := hexPinId('c', 0)
	for _, s := range []struct {
		pinId string
		path  string
		body  string
	}{
		{buzz, publishedcontent.PathSimpleBuzz, `{"content":"buzz"}`},
		{question, publishedcontent.PathSimpleQuestion, `{"title":"Q?","content":"body"}`},
		{note, publishedcontent.PathSimpleNote, `{"title":"Note","content":"body","contentType":"text/markdown"}`},
	} {
		if _, err := published.HandleBlockPin(freshPin(s.pinId, s.path, "create", "", s.body, baseTs, "addr-bot")); err != nil {
			t.Fatalf("seed %s: %v", s.pinId, err)
		}
	}

	router := newFreshRouter(t, published)
	envelope := doFresh(t, router, "size=10")
	byId := map[string]freshItemRow{}
	for _, item := range envelope.Data.Items {
		byId[item.PinId] = item
	}
	if item := byId[buzz]; item.LikeCount != 3 || item.CommentCount != 2 {
		t.Errorf("buzz engagement = %d/%d, want 3/2", item.LikeCount, item.CommentCount)
	}
	if item := byId[question]; item.LikeCount != 5 || item.CommentCount != 1 {
		t.Errorf("question engagement = %d/%d, want 5/1", item.LikeCount, item.CommentCount)
	}
	if item := byId[question]; item.Extra["answerCount"] != float64(4) {
		t.Errorf("question extra.answerCount = %v, want 4", item.Extra["answerCount"])
	}
	if item := byId[note]; item.LikeCount != 0 || item.CommentCount != 0 {
		t.Errorf("note engagement = %d/%d, want 0/0 (no join for simplenote)", item.LikeCount, item.CommentCount)
	}
}

func TestHandleFresh_DedupeAndThrottle(t *testing.T) {
	published := setupPublishedContent(t)
	baseTs := int64(1755000000)

	// 4 identical buzz posts alternating between two authors + 2 unique
	// posts by one of them.
	for i := 0; i < 4; i++ {
		pinId := hexPinId(byte('a'+i), i)
		addr := "addr-noise"
		if i%2 == 1 {
			addr = "addr-noise2"
		}
		if _, err := published.HandleBlockPin(freshPin(pinId, publishedcontent.PathSimpleBuzz, "create", "", `{"content":"Hello MVC world!"}`, baseTs+int64(i), addr)); err != nil {
			t.Fatalf("noise %d: %v", i, err)
		}
	}
	for i := 0; i < 2; i++ {
		pinId := hexPinId(byte('e'+i), 10+i)
		if _, err := published.HandleBlockPin(freshPin(pinId, publishedcontent.PathSimpleBuzz, "create", "", fmt.Sprintf(`{"content":"unique %d"}`, i), baseTs+int64(20+i), "addr-noise")); err != nil {
			t.Fatalf("unique %d: %v", i, err)
		}
	}

	router := newFreshRouter(t, published)

	envelope := doFresh(t, router, "size=10")
	if len(envelope.Data.Items) != 6 || envelope.Data.Suppressed != nil {
		t.Fatalf("baseline = %d items suppressed=%+v, want 6 items, no block", len(envelope.Data.Items), envelope.Data.Suppressed)
	}

	// dedupe=identical: the 4 identical posts collapse onto the newest copy
	// (3 dropped), leaving 1 + the 2 unique posts.
	envelope = doFresh(t, router, "size=10&dedupe=identical")
	if len(envelope.Data.Items) != 3 {
		t.Fatalf("deduped items = %d, want 3", len(envelope.Data.Items))
	}
	var hello *freshItemRow
	for i := range envelope.Data.Items {
		if envelope.Data.Items[i].Title == "Hello MVC world!" {
			hello = &envelope.Data.Items[i]
			break
		}
	}
	if hello == nil {
		t.Fatal("deduped page lost the Hello MVC world! post")
	}
	if hello.Duplicates != 3 {
		t.Errorf("hello duplicates = %d, want 3 (carried by the newest copy)", hello.Duplicates)
	}
	if envelope.Data.Suppressed == nil || envelope.Data.Suppressed.Duplicates != 3 || envelope.Data.Suppressed.Throttled != 0 {
		t.Fatalf("suppressed = %+v, want duplicates=3 throttled=0", envelope.Data.Suppressed)
	}

	// maxPerAuthor=2: addr-noise contributed 4 posts (2 noise + 2 unique).
	envelope = doFresh(t, router, "size=10&maxPerAuthor=2")
	if envelope.Data.Suppressed == nil || envelope.Data.Suppressed.Throttled != 2 {
		t.Fatalf("throttled = %+v, want throttled=2", envelope.Data.Suppressed)
	}

	// Combined: dedupe runs first, then the per-author cap on what remains.
	envelope = doFresh(t, router, "size=10&dedupe=identical&maxPerAuthor=1")
	if envelope.Data.Suppressed == nil || envelope.Data.Suppressed.Duplicates != 3 || envelope.Data.Suppressed.Throttled < 1 {
		t.Fatalf("combined suppression = %+v", envelope.Data.Suppressed)
	}
}

func TestHandleFresh_InvalidParams(t *testing.T) {
	published := setupPublishedContent(t)
	router := newFreshRouter(t, published)

	for _, bad := range []string{
		"protocols=skill-service",
		"protocols=agentpedia",
		"size=0",
		"size=-3",
		"size=abc",
		"since=notanumber",
		"dedupe=fuzzy",
		"maxPerAuthor=0",
		"maxPerAuthor=abc",
		"cursor=%21%21bad",
	} {
		envelope := doFresh(t, router, bad)
		if envelope.Code != codeInvalidParam {
			t.Errorf("%q: code = %d, want 40000", bad, envelope.Code)
		}
	}
}

func TestHandleFresh_MissingLookupIsUnavailable(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)
	envelope := doFresh(t, router, "size=5")
	if envelope.Code != codeUnavailable {
		t.Fatalf("code = %d, want 50000", envelope.Code)
	}
}

func TestFreshResponseCache(t *testing.T) {
	cache := newFreshResponseCache()
	if _, ok := cache.get("k"); ok {
		t.Fatal("empty cache returned a hit")
	}
	cache.set("k", []byte("v1"))
	if raw, ok := cache.get("k"); !ok || string(raw) != "v1" {
		t.Fatalf("get = %q %v, want v1 true", raw, ok)
	}
	cache.set("k", []byte("v2"))
	if raw, _ := cache.get("k"); string(raw) != "v2" {
		t.Fatalf("get = %q, want v2 (overwrite)", raw)
	}
}

func TestFreshQueryCacheKey(t *testing.T) {
	a := freshQuery{protocolPaths: []string{"/protocols/simplebuzz", "/protocols/simplenote"}, since: 10, size: 50, cursor: "k:x"}
	b := freshQuery{protocolPaths: []string{"/protocols/simplenote", "/protocols/simplebuzz"}, since: 10, size: 50, cursor: "k:x"}
	if a.cacheKey() != b.cacheKey() {
		t.Fatal("equal parameter sets must share one cache entry regardless of protocol order")
	}
	c := a
	c.dedupe = true
	if a.cacheKey() == c.cacheKey() {
		t.Fatal("dedupe must change the cache identity")
	}
}

func TestFreshContentHashAndAuthorKey(t *testing.T) {
	text := &publishedcontent.Record{PayloadText: "Hello MVC world!"}
	jsonRec := &publishedcontent.Record{PayloadJSON: map[string]any{"b": 1, "a": 2}}
	if freshContentHash(text) == freshContentHash(jsonRec) {
		t.Fatal("text and JSON payloads must hash differently")
	}
	sameJSON := &publishedcontent.Record{PayloadJSON: map[string]any{"a": 2, "b": 1}}
	if freshContentHash(jsonRec) != freshContentHash(sameJSON) {
		t.Fatal("key order must not change the JSON payload hash")
	}

	rec := &publishedcontent.Record{PublisherGlobalMetaId: "IDQ1ABC", PublisherMetaId: "", PublisherAddress: "addr"}
	if got := freshAuthorKey(rec); got != "idq1abc" {
		t.Fatalf("authorKey = %q, want idq1abc (globalMetaId wins, lowercased)", got)
	}
}

func pinIds(envelope freshEnvelope) []string {
	out := make([]string, 0, len(envelope.Data.Items))
	for _, item := range envelope.Data.Items {
		out = append(out, item.PinId)
	}
	return out
}
