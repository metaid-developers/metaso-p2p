package qa

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

func newTestRouter(agg *Aggregator) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(api.RequestTimingMiddleware())
	agg.RegisterRoutes(router.Group("/api"))
	return router
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type questionRow struct {
	Protocol     string   `json:"protocol"`
	PinId        string   `json:"pinId"`
	CurrentPinId string   `json:"currentPinId"`
	Title        string   `json:"title"`
	Summary      string   `json:"summary"`
	Tags         []string `json:"tags"`
	Publisher    struct {
		GlobalMetaId string `json:"globalMetaId"`
		MetaId       string `json:"metaid"`
		Name         string `json:"name"`
		Avatar       string `json:"avatar"`
	} `json:"publisher"`
	CreatedAt   int64          `json:"createdAt"`
	AnswerCount int            `json:"answerCount"`
	LikeCount   int            `json:"likeCount"`
	TopAnswer   *topAnswerItem `json:"topAnswer"`
	Score       *int           `json:"score"`
	HotScore    *int           `json:"hotScore"`
}

type answerRow struct {
	Protocol      string `json:"protocol"`
	PinId         string `json:"pinId"`
	QuestionPinId string `json:"questionPinId"`
	Summary       string `json:"summary"`
	Publisher     struct {
		GlobalMetaId string `json:"globalMetaId"`
		Name         string `json:"name"`
	} `json:"publisher"`
	CreatedAt    int64 `json:"createdAt"`
	LikeCount    int   `json:"likeCount"`
	DislikeCount int   `json:"dislikeCount"`
	Score        int   `json:"score"`
}

type listPayload struct {
	Items      []json.RawMessage `json:"items"`
	NextCursor *string           `json:"nextCursor"`
	HasMore    bool              `json:"hasMore"`
}

type detailPayload struct {
	Question questionRow `json:"question"`
	Answers  []answerRow `json:"answers"`
	HasMore  bool        `json:"hasMore"`
}

func doRequest(t *testing.T, router *gin.Engine, path string) (int, envelope) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(rec, req)
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response %s: %v\nraw: %s", path, err, rec.Body.String())
	}
	return rec.Code, env
}

func decodeList(t *testing.T, env envelope) listPayload {
	t.Helper()
	var payload listPayload
	if err := json.Unmarshal(env.Data, &payload); err != nil {
		t.Fatalf("decode list data: %v", err)
	}
	return payload
}

func decodeRows[T any](t *testing.T, payload listPayload) []T {
	t.Helper()
	rows := make([]T, 0, len(payload.Items))
	for _, raw := range payload.Items {
		var row T
		if err := json.Unmarshal(raw, &row); err != nil {
			t.Fatalf("decode item: %v", err)
		}
		rows = append(rows, row)
	}
	return rows
}

// seedSearchCorpus indexes three questions with different field emphasis and
// one answer so search filters and scoring have material to rank.
func seedSearchCorpus(t *testing.T, agg *Aggregator) (walletPin, mvcPin, chinesePin, answerId string) {
	t.Helper()
	walletPin = testPinId("search-wallet")
	mvcPin = testPinId("search-mvc")
	chinesePin = testPinId("search-chinese")
	answerId = testPinId("search-answer")

	mustBlock(t, agg, questionPin(walletPin, "wallet recovery guide?", "mnemonic and recovery words", "wallet", "recovery"))
	mustBlock(t, agg, questionPin(mvcPin, "mvc fee rate?", "what is the current fee", "mvc"))
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id:           chinesePin,
		Path:         PathSimpleQuestion,
		Timestamp:    1755000500,
		Body:         `{"title":"钱包恢复方法？","content":"助记词丢失后如何恢复钱包","tags":["wallet"]}`,
		GlobalMetaId: "asker",
	}))
	// Answer content participates via matched-question aggregation.
	mustBlock(t, agg, answerPin(answerId, walletPin, "the mnemonic cannot be recovered from the directory", 1755000100, "bot-a"))
	return
}

func TestAPI_SearchScoringAndFilters(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	walletPin, _, _, _ := seedSearchCorpus(t, agg)
	router := newTestRouter(agg)

	// Title hit outweighs content hit.
	status, env := doRequest(t, router, "/api/qa/search?q=wallet+recovery")
	if status != http.StatusOK || env.Code != 0 {
		t.Fatalf("search status=%d code=%d msg=%s", status, env.Code, env.Message)
	}
	rows := decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) == 0 || rows[0].PinId != walletPin {
		t.Fatalf("top match = %+v", rows)
	}
	if rows[0].Score == nil || *rows[0].Score <= 0 {
		t.Fatalf("score missing on search item: %+v", rows[0])
	}
	if rows[0].TopAnswer == nil || rows[0].TopAnswer.PinId == "" {
		t.Fatalf("topAnswer missing on search item: %+v", rows[0].TopAnswer)
	}

	// Answer keyword hits surface the question (matched-question aggregation).
	_, env = doRequest(t, router, "/api/qa/search?q=directory")
	rows = decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].PinId != walletPin {
		t.Fatalf("answer-content match rows = %+v", rows)
	}

	// CJK bigram query.
	_, env = doRequest(t, router, "/api/qa/search?q=钱包恢复")
	rows = decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].PinId != testPinId("search-chinese") {
		t.Fatalf("cjk rows = %+v", rows)
	}

	// tags filter is an AND over question tags.
	_, env = doRequest(t, router, "/api/qa/search?q=wallet&tags=wallet,recovery")
	rows = decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].PinId != walletPin {
		t.Fatalf("tag filter rows = %+v", rows)
	}
	_, env = doRequest(t, router, "/api/qa/search?q=wallet&tags=wallet,missing")
	if rows := decodeRows[questionRow](t, decodeList(t, env)); len(rows) != 0 {
		t.Fatalf("tag AND filter should exclude, got %+v", rows)
	}

	// answered filter.
	_, env = doRequest(t, router, "/api/qa/search?q=wallet&answered=true")
	if rows := decodeRows[questionRow](t, decodeList(t, env)); len(rows) != 1 {
		t.Fatalf("answered=true rows = %+v", rows)
	}
	_, env = doRequest(t, router, "/api/qa/search?q=钱包&answered=false")
	if rows := decodeRows[questionRow](t, decodeList(t, env)); len(rows) != 1 {
		t.Fatalf("answered=false rows = %+v", rows)
	}

	// publisher filter (both seed questions are published by asker).
	_, env = doRequest(t, router, "/api/qa/search?q=wallet&publisher=asker")
	if rows := decodeRows[questionRow](t, decodeList(t, env)); len(rows) != 2 {
		t.Fatalf("publisher rows = %+v", rows)
	}
	_, env = doRequest(t, router, "/api/qa/search?q=wallet&publisher=nobody")
	if rows := decodeRows[questionRow](t, decodeList(t, env)); len(rows) != 0 {
		t.Fatalf("publisher=nobody rows = %+v", rows)
	}

	// sort=newest bypasses scoring but still admits on token hits.
	_, env = doRequest(t, router, "/api/qa/search?q=wallet&sort=newest")
	rows = decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 2 {
		t.Fatalf("newest rows = %+v", rows)
	}
	if rows[0].CreatedAt < rows[1].CreatedAt {
		t.Fatalf("newest ordering broken: %+v", rows)
	}
	if rows[0].Score != nil && *rows[0].Score != 0 {
		t.Fatalf("newest should report score 0, got %v", *rows[0].Score)
	}

	// All-stopword query matches nothing without error.
	_, env = doRequest(t, router, "/api/qa/search?q=what+is")
	if env.Code != 0 {
		t.Fatalf("stopword query code=%d", env.Code)
	}
	if rows := decodeRows[questionRow](t, decodeList(t, env)); len(rows) != 0 {
		t.Fatalf("stopword rows = %+v", rows)
	}
}

func TestAPI_SearchCursorPagination(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	seedSearchCorpus(t, agg)
	router := newTestRouter(agg)

	status, env := doRequest(t, router, "/api/qa/search?q=wallet&size=1")
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	page1 := decodeList(t, env)
	if len(page1.Items) != 1 || !page1.HasMore || page1.NextCursor == nil {
		t.Fatalf("page1 = %+v", page1)
	}
	_, env = doRequest(t, router, "/api/qa/search?q=wallet&size=1&cursor="+*page1.NextCursor)
	page2 := decodeList(t, env)
	if len(page2.Items) != 1 || page2.HasMore {
		t.Fatalf("page2 = %+v", page2)
	}
	if string(page1.Items[0]) == string(page2.Items[0]) {
		t.Fatal("cursor returned the same row")
	}
}

func TestAPI_SearchParamErrors(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	router := newTestRouter(agg)

	for _, tc := range []struct {
		path string
		msg  string
	}{
		{"/api/qa/search", "missing q"},
		{"/api/qa/search?q=", "empty q"},
		{"/api/qa/search?q=x&sort=popularity", "bad sort"},
		{"/api/qa/search?q=x&answered=maybe", "bad answered"},
		{"/api/qa/search?q=x&size=0", "bad size"},
		{"/api/qa/search?q=x&size=abc", "non-numeric size"},
		{"/api/qa/search?q=x&cursor=%5Cx", "bad cursor"},
	} {
		_, env := doRequest(t, router, tc.path)
		if env.Code != codeInvalidParam {
			t.Fatalf("%s (%s): code=%d msg=%s", tc.path, tc.msg, env.Code, env.Message)
		}
	}
}

func TestAPI_QuestionsFeed(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	walletPin, mvcPin, _, _ := seedSearchCorpus(t, agg)
	router := newTestRouter(agg)

	// newest: Chinese question (latest createdAt) first.
	_, env := doRequest(t, router, "/api/qa/questions")
	rows := decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 3 {
		t.Fatalf("feed rows = %+v", rows)
	}
	if rows[0].PinId != testPinId("search-chinese") {
		t.Fatalf("newest first broken: %+v", rows)
	}

	// maxAnswers=0 returns only unanswered (the answerer-bot poll path).
	_, env = doRequest(t, router, "/api/qa/questions?maxAnswers=0")
	rows = decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 2 {
		t.Fatalf("unanswered rows = %+v", rows)
	}
	for _, row := range rows {
		if row.PinId == walletPin {
			t.Fatalf("answered question in unanswered feed: %+v", row)
		}
	}

	// minAnswers=1.
	_, env = doRequest(t, router, "/api/qa/questions?minAnswers=1")
	rows = decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].PinId != walletPin {
		t.Fatalf("minAnswers rows = %+v", rows)
	}

	// tags filter.
	_, env = doRequest(t, router, "/api/qa/questions?tags=mvc")
	rows = decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].PinId != mvcPin {
		t.Fatalf("tags rows = %+v", rows)
	}

	// invalid params.
	for _, path := range []string{
		"/api/qa/questions?minAnswers=-1",
		"/api/qa/questions?maxAnswers=abc",
		"/api/qa/questions?minAnswers=5&maxAnswers=1",
		"/api/qa/questions?sort=top",
	} {
		if _, env := doRequest(t, router, path); env.Code != codeInvalidParam {
			t.Fatalf("%s: code=%d", path, env.Code)
		}
	}
}

func TestAPI_QuestionsHot(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	qNew := testPinId("hot-new")
	qOld := testPinId("hot-old")
	answerId := testPinId("hot-answer")

	// Clock fixed inside the window; both questions eligible.
	agg.SetNow(func() int64 { return 1755000000 + 3600 })

	mustBlock(t, agg, questionPin(qNew, "hot new question?", "body"))
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id:           qOld,
		Path:         PathSimpleQuestion,
		Timestamp:    1755000000 - 8*24*3600, // outside the 7-day window
		Body:         `{"title":"hot old question?","content":"body"}`,
		GlobalMetaId: "asker",
	}))
	mustBlock(t, agg, answerPin(answerId, qNew, "an answer", 1755000100, "bot-a"))
	mustBlock(t, agg, likePin(testPinId("hot-like"), answerId, 1, 1755000200, "actor"))
	router := newTestRouter(agg)

	_, env := doRequest(t, router, "/api/qa/questions?sort=hot")
	rows := decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 1 {
		t.Fatalf("hot rows = %+v", rows)
	}
	if rows[0].PinId != qNew {
		t.Fatalf("hot row = %+v", rows[0])
	}
	if rows[0].HotScore == nil || *rows[0].HotScore != 3 { // 2*answerCount + answer likes
		t.Fatalf("hotScore = %+v", rows[0].HotScore)
	}

	// Feed pagination still applies.
	agg2, store2 := setupTestAggregator(t)
	defer store2.Close()
	agg2.SetNow(func() int64 { return 1755000000 + 3600 })
	for i := 0; i < 3; i++ {
		mustBlock(t, agg2, questionPin(testPinId("hotpage-"+string(rune('a'+i))), "q?", "body"))
	}
	router2 := newTestRouter(agg2)
	_, env = doRequest(t, router2, "/api/qa/questions?sort=hot&size=2")
	page := decodeList(t, env)
	if len(page.Items) != 2 || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("hot page = %+v", page)
	}
}

func TestAPI_QuestionDetailAndAnswers(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	q1 := testPinId("detail-question")
	a1 := testPinId("detail-answer-1")
	a2 := testPinId("detail-answer-2")
	a3 := testPinId("detail-answer-3")

	mustBlock(t, agg, questionPin(q1, "Detail question?", "body"))
	mustBlock(t, agg, answerPin(a1, q1, "first answer", 1755000100, "bot-a"))
	mustBlock(t, agg, answerPin(a2, q1, "second answer", 1755000200, "bot-b"))
	mustBlock(t, agg, answerPin(a3, q1, "third answer", 1755000300, "bot-b"))
	// a2 ends with score 2 (two likes), a1 score 1, a3 score 0.
	mustBlock(t, agg, likePin(testPinId("d-like-1"), a2, 1, 1755001000, "actor-1"))
	mustBlock(t, agg, likePin(testPinId("d-like-2"), a2, 1, 1755001100, "actor-2"))
	mustBlock(t, agg, likePin(testPinId("d-like-3"), a1, 1, 1755001200, "actor-1"))
	router := newTestRouter(agg)

	// Detail via a modify-version pin id resolves to the same question.
	modifyId := testPinId("detail-question-v2")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id:           modifyId,
		Path:         PathSimpleQuestion,
		Operation:    OperationModify,
		OriginalId:   "@" + q1,
		Timestamp:    1755000400,
		Body:         `{"title":"Detail question v2?","content":"body"}`,
		GlobalMetaId: "asker",
	}))
	_, env := doRequest(t, router, "/api/qa/questions/"+modifyId)
	if env.Code != 0 {
		t.Fatalf("detail via version pin code=%d msg=%s", env.Code, env.Message)
	}
	var detail detailPayload
	if err := json.Unmarshal(env.Data, &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Question.PinId != q1 || detail.Question.Title != "Detail question v2?" {
		t.Fatalf("detail question = %+v", detail.Question)
	}
	if len(detail.Answers) != 3 {
		t.Fatalf("detail answers = %+v", detail.Answers)
	}
	// R4 ordering: score desc (a2=2, a1=1, a3=0).
	wantOrder := []string{a2, a1, a3}
	for i, want := range wantOrder {
		if detail.Answers[i].PinId != want {
			t.Fatalf("answer order[%d] = %s, want %s", i, detail.Answers[i].PinId, want)
		}
	}
	if detail.Answers[0].Score != 2 {
		t.Fatalf("answer score = %+v", detail.Answers[0])
	}

	// Publisher filter (a bot reviewing its own answers).
	_, env = doRequest(t, router, "/api/qa/questions/"+q1+"/answers?publisher=bot-b")
	rows := decodeRows[answerRow](t, decodeList(t, env))
	if len(rows) != 2 {
		t.Fatalf("publisher rows = %+v", rows)
	}
	for _, row := range rows {
		if row.Publisher.GlobalMetaId != "bot-b" {
			t.Fatalf("publisher leak: %+v", row)
		}
		if row.QuestionPinId != q1 {
			t.Fatalf("questionPinId = %s", row.QuestionPinId)
		}
	}

	// Answers pagination.
	_, env = doRequest(t, router, "/api/qa/questions/"+q1+"/answers?size=2")
	page := decodeList(t, env)
	if len(page.Items) != 2 || !page.HasMore || page.NextCursor == nil {
		t.Fatalf("answers page = %+v", page)
	}
	_, env = doRequest(t, router, "/api/qa/questions/"+q1+"/answers?size=2&cursor="+*page.NextCursor)
	page2 := decodeList(t, env)
	if len(page2.Items) != 1 || page2.HasMore {
		t.Fatalf("answers page2 = %+v", page2)
	}

	// Unknown and malformed pins.
	if _, env := doRequest(t, router, "/api/qa/questions/"+testPinId("unknown")); env.Code != codeNotFound {
		t.Fatalf("unknown code=%d", env.Code)
	}
	if _, env := doRequest(t, router, "/api/qa/questions/not-a-pin"); env.Code != codeInvalidParam {
		t.Fatalf("malformed code=%d", env.Code)
	}
}

func TestAPI_QuestionItemShape(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	q1 := testPinId("shape-question")
	mustBlock(t, agg, questionPin(q1, "Shape question?", "body with **markdown**"))
	router := newTestRouter(agg)

	_, env := doRequest(t, router, "/api/qa/questions")
	rows := decodeRows[questionRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].Protocol != "simplequestion" || rows[0].PinId != q1 {
		t.Fatalf("rows = %+v", rows)
	}
	// Full wire check on one item.
	var item map[string]any
	if err := json.Unmarshal(decodeList(t, env).Items[0], &item); err != nil {
		t.Fatalf("unmarshal item: %v", err)
	}
	for _, key := range []string{"protocol", "pinId", "currentPinId", "chainName", "title", "summary", "tags", "publisher", "createdAt", "isMempool", "likeCount", "dislikeCount", "commentCount", "answerCount", "topAnswer"} {
		if _, ok := item[key]; !ok {
			t.Fatalf("item missing key %q: %v", key, item)
		}
	}
	if item["chainName"] != "mvc" {
		t.Fatalf("chainName = %v", item["chainName"])
	}
	if item["summary"] != "body with markdown" {
		t.Fatalf("summary = %v (markdown should be stripped)", item["summary"])
	}
}
