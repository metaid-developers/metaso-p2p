package qa

// Tests of the v2 Q&A contract additions (docs/specs/2026-09-07-metaweb-qa-comments-author-api.md):
// R5 comment indexing + GET /api/qa/pins/:pinId/comments, R6 GET /api/qa/answers
// and the publisher filter on GET /api/qa/questions, plus the mempool→confirmed
// time-index re-keying.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// ---------------------------------------------------------------------------
// R5.1 comment indexing
// ---------------------------------------------------------------------------

func TestProcessComment_IndexesOnlyQATargets(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := testPinId("v2-cmt-question")
	answerId := testPinId("v2-cmt-answer")
	buzzId := testPinId("v2-cmt-buzz")
	question := questionPin(questionId, "thread target?", "body")
	mustBlock(t, agg, question)
	mustBlock(t, agg, answerPin(answerId, questionId, "answer body", 1755000100, "answerer"))

	onQuestion := makeQAPin(qaPinOpts{
		Id:   testPinId("v2-cmt-on-q"),
		Path: PathPayComment, Timestamp: 1755000200, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + questionId + `","content":"on the question"}`,
	})
	onAnswer := makeQAPin(qaPinOpts{
		Id:   testPinId("v2-cmt-on-a"),
		Path: PathPayComment, Timestamp: 1755000300, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + answerId + `","content":"on the answer"}`,
	})
	// A comment whose target is not a Q&A pin, and a reply to a comment, stay
	// out of the index.
	onBuzz := makeQAPin(qaPinOpts{
		Id:   testPinId("v2-cmt-on-buzz"),
		Path: PathPayComment, Timestamp: 1755000400, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + buzzId + `","content":"on a buzz pin"}`,
	})
	onComment := makeQAPin(qaPinOpts{
		Id:   testPinId("v2-cmt-on-c"),
		Path: PathPayComment, Timestamp: 1755000500, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + onQuestion.Id + `","content":"reply to a comment"}`,
	})
	for _, pin := range []*aggregator.PinInscription{onQuestion, onAnswer, onBuzz, onComment} {
		mustBlock(t, agg, pin)
	}

	if rec, _ := agg.loadComment("mvc", onQuestion.Id); rec == nil {
		t.Fatal("comment on question was not indexed")
	} else if rec.TargetPinId != questionId || rec.TargetKind != "q" || rec.Content != "on the question" {
		t.Fatalf("comment on question record = %+v", rec)
	}
	if rec, _ := agg.loadComment("mvc", onAnswer.Id); rec == nil {
		t.Fatal("comment on answer was not indexed")
	} else if rec.TargetPinId != answerId || rec.TargetKind != "a" {
		t.Fatalf("comment on answer record = %+v", rec)
	}
	if rec, _ := agg.loadComment("mvc", onBuzz.Id); rec != nil {
		t.Fatalf("comment on non-Q&A pin must not be indexed, got %+v", rec)
	}
	if rec, _ := agg.loadComment("mvc", onComment.Id); rec != nil {
		t.Fatalf("comment on a comment must not be indexed, got %+v", rec)
	}

	question2, err := agg.loadQuestion("mvc", questionId)
	if err != nil || question2 == nil {
		t.Fatalf("loadQuestion: %v %v", question2, err)
	}
	if question2.CommentCount != 1 {
		t.Fatalf("question commentCount = %d, want 1", question2.CommentCount)
	}
	answer2, _ := agg.loadAnswer("mvc", answerId)
	if answer2.CommentCount != 1 {
		t.Fatalf("answer commentCount = %d, want 1", answer2.CommentCount)
	}
}

func TestProcessComment_TargetAnyVersionNormalizes(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := testPinId("v2-cmt-norm-q")
	mustBlock(t, agg, questionPin(questionId, "normalisation?", "body"))
	modifiedId := testPinId("v2-cmt-norm-modify")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: modifiedId, Path: PathSimpleQuestion, Operation: OperationModify,
		OriginalId: questionId, Timestamp: 1755000100, GlobalMetaId: "asker",
		Body: `{"title":"normalisation (edited)?"}`,
	}))

	// commentTo references the modify version; the record normalises to the
	// question's stable source pin id.
	commentId := testPinId("v2-cmt-norm-c")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: commentId, Path: PathPayComment, Timestamp: 1755000200, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + modifiedId + `","content":"targets the modify version"}`,
	}))
	rec, _ := agg.loadComment("mvc", commentId)
	if rec == nil || rec.TargetPinId != questionId {
		t.Fatalf("comment target not normalised: %+v", rec)
	}
}

func TestProcessComment_ModifyAndRevoke(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := testPinId("v2-cmt-life-q")
	mustBlock(t, agg, questionPin(questionId, "lifecycle?", "body"))
	commentId := testPinId("v2-cmt-life-c")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: commentId, Path: PathPayComment, Timestamp: 1755000100, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + questionId + `","content":"before edit"}`,
	}))

	modifyId := testPinId("v2-cmt-life-modify")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: modifyId, Path: PathPayComment, Operation: OperationModify,
		OriginalId: commentId, Timestamp: 1755000200, GlobalMetaId: "commenter",
		Body: `{"content":"after edit"}`,
	}))
	rec, _ := agg.loadComment("mvc", commentId)
	if rec == nil || rec.Content != "after edit" || rec.CurrentPinId != modifyId {
		t.Fatalf("modify did not update in place: %+v", rec)
	}

	revokeId := testPinId("v2-cmt-life-revoke")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: revokeId, Path: PathPayComment, Operation: OperationRevoke,
		OriginalId: commentId, Timestamp: 1755000300, GlobalMetaId: "commenter",
	}))
	rec, _ = agg.loadComment("mvc", commentId)
	if rec == nil || !rec.Hidden {
		t.Fatalf("revoke did not hide the comment: %+v", rec)
	}
	question, _ := agg.loadQuestion("mvc", questionId)
	if question.CommentCount != 0 {
		t.Fatalf("hidden comment still counted: commentCount = %d", question.CommentCount)
	}
}

func TestProcessComment_ContentCappedAtIndexTime(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := testPinId("v2-cmt-cap-q")
	mustBlock(t, agg, questionPin(questionId, "cap?", "body"))
	long := strings.Repeat("字", 2500) // 2500 runes > 2000 cap
	body, _ := json.Marshal(map[string]any{"commentTo": questionId, "content": long})
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("v2-cmt-cap-c"), Path: PathPayComment,
		Timestamp: 1755000100, GlobalMetaId: "commenter", Body: string(body),
	}))
	rec, _ := agg.loadComment("mvc", testPinId("v2-cmt-cap-c"))
	if rec == nil {
		t.Fatal("comment not indexed")
	}
	if got := len([]rune(rec.Content)); got != 2000 {
		t.Fatalf("stored comment runes = %d, want 2000", got)
	}
}

func TestProcessComment_MempoolThenConfirm(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := testPinId("v2-cmt-mp-q")
	mustBlock(t, agg, questionPin(questionId, "mempool?", "body"))
	commentId := testPinId("v2-cmt-mp-c")

	relay := makeQAPin(qaPinOpts{
		Id: commentId, Path: PathPayComment, Timestamp: 1755000100, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + questionId + `","content":"fast view"}`,
	})
	if _, err := agg.HandleMempoolPin(relay); err != nil {
		t.Fatalf("HandleMempoolPin: %v", err)
	}
	rec, _ := agg.loadComment("mvc", commentId)
	if rec == nil || !rec.IsMempool || rec.Content != "fast view" {
		t.Fatalf("mempool comment not indexed: %+v", rec)
	}

	confirmed := makeQAPin(qaPinOpts{
		Id: commentId, Path: PathPayComment, Timestamp: 1755000095, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + questionId + `","content":"fast view"}`,
	})
	mustBlock(t, agg, confirmed)
	rec, _ = agg.loadComment("mvc", commentId)
	if rec == nil || rec.IsMempool || rec.CreatedAt != 1755000095 {
		t.Fatalf("confirmed comment did not replace mempool state: %+v", rec)
	}

	// Exactly one comment list entry and count survive the replacement.
	entries := 0
	if err := agg.store.ScanPrefix(Namespace, commentIndexPrefix(questionId), func(_, _ []byte) error {
		entries++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if entries != 1 {
		t.Fatalf("comment index entries = %d, want 1", entries)
	}
	question, _ := agg.loadQuestion("mvc", questionId)
	if question.CommentCount != 1 {
		t.Fatalf("commentCount = %d, want 1", question.CommentCount)
	}
}

// ---------------------------------------------------------------------------
// mempool→confirmed re-keying of the time indexes
// ---------------------------------------------------------------------------

func TestProcess_MempoolConfirmRekeysTimeIndexes(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := testPinId("v2-rekey-q")
	answerId := testPinId("v2-rekey-a")

	// Mempool view first (relay time 1755000100), confirmed pin later with a
	// different block time (1755000090) — the v1 indexes kept the stale key
	// keyed under the relay timestamp and served the row twice.
	relayQuestion := questionPin(questionId, "rekey?", "body")
	relayQuestion.Timestamp = 1755000100
	if _, err := agg.HandleMempoolPin(relayQuestion); err != nil {
		t.Fatal(err)
	}
	relayAnswer := answerPin(answerId, questionId, "rekey answer", 1755000101, "answerer")
	if _, err := agg.HandleMempoolPin(relayAnswer); err != nil {
		t.Fatal(err)
	}
	confirmedQuestion := questionPin(questionId, "rekey?", "body")
	confirmedQuestion.Timestamp = 1755000090
	mustBlock(t, agg, confirmedQuestion)
	confirmedAnswer := answerPin(answerId, questionId, "rekey answer", 1755000091, "answerer")
	mustBlock(t, agg, confirmedAnswer)

	countPrefix := func(prefix []byte) int {
		count := 0
		if err := agg.store.ScanPrefix(Namespace, prefix, func(_, _ []byte) error {
			count++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if got := countPrefix(questionTimePrefix()); got != 1 {
		t.Fatalf("question time entries = %d, want 1", got)
	}
	if got := countPrefix(answerIndexPrefix("mvc", questionId)); got != 1 {
		t.Fatalf("answer index entries = %d, want 1", got)
	}
	if got := countPrefix(answerTimePrefix()); got != 1 {
		t.Fatalf("answer time entries = %d, want 1", got)
	}
}

func TestQuestionRevoke_RemovesAnswersFromGlobalIndex(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := testPinId("v2-rvk-q")
	answerId := testPinId("v2-rvk-a")
	mustBlock(t, agg, questionPin(questionId, "revoke cascade?", "body"))
	mustBlock(t, agg, answerPin(answerId, questionId, "will vanish", 1755000100, "answerer"))

	visible := func() int {
		count := 0
		if err := agg.store.ScanPrefix(Namespace, answerTimePrefix(), func(_, _ []byte) error {
			count++
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if visible() != 1 {
		t.Fatalf("answer time entries before revoke = %d, want 1", visible())
	}

	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("v2-rvk-q-revoke"), Path: PathSimpleQuestion, Operation: OperationRevoke,
		OriginalId: questionId, Timestamp: 1755000200, GlobalMetaId: "asker",
	}))
	if got := visible(); got != 0 {
		t.Fatalf("answer time entries after question revoke = %d, want 0", got)
	}
}

// ---------------------------------------------------------------------------
// R5.2 GET /api/qa/pins/:pinId/comments
// ---------------------------------------------------------------------------

type commentRow struct {
	Protocol    string `json:"protocol"`
	PinId       string `json:"pinId"`
	TargetPinId string `json:"targetPinId"`
	Content     string `json:"content"`
	CreatedAt   int64  `json:"createdAt"`
	IsMempool   bool   `json:"isMempool"`
}

func seedCommentThread(t *testing.T, agg *Aggregator) (questionId string) {
	t.Helper()
	questionId = testPinId("v2-api-cmt-q")
	mustBlock(t, agg, questionPin(questionId, "api thread?", "body"))
	for i, ts := range []int64{1755000100, 1755000200, 1755000300} {
		body, _ := json.Marshal(map[string]any{"commentTo": questionId, "content": "comment " + string(rune('A'+i))})
		mustBlock(t, agg, makeQAPin(qaPinOpts{
			Id: testPinId("v2-api-cmt-" + string(rune('a'+i))), Path: PathPayComment,
			Timestamp: ts, GlobalMetaId: "commenter", Body: string(body),
		}))
	}
	return questionId
}

func TestAPI_PinComments_SortPagingErrors(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := seedCommentThread(t, agg)
	router := newTestRouter(agg)

	// newest (default): createdAt desc.
	code, env := doRequest(t, router, "/api/qa/pins/"+questionId+"/comments")
	if code != 200 || env.Code != 0 {
		t.Fatalf("newest: http=%d code=%d msg=%s", code, env.Code, env.Message)
	}
	rows := decodeRows[commentRow](t, decodeList(t, env))
	if len(rows) != 3 || rows[0].Content != "comment C" || rows[2].Content != "comment A" {
		t.Fatalf("newest order wrong: %+v", rows)
	}
	for _, row := range rows {
		if row.Protocol != "paycomment" || row.TargetPinId != questionId {
			t.Fatalf("comment row shape wrong: %+v", row)
		}
	}

	// oldest: exact reverse.
	_, env = doRequest(t, router, "/api/qa/pins/"+questionId+"/comments?sort=oldest")
	rows = decodeRows[commentRow](t, decodeList(t, env))
	if len(rows) != 3 || rows[0].Content != "comment A" || rows[2].Content != "comment C" {
		t.Fatalf("oldest order wrong: %+v", rows)
	}

	// cursor paging.
	_, env = doRequest(t, router, "/api/qa/pins/"+questionId+"/comments?size=2")
	list := decodeList(t, env)
	if !list.HasMore || list.NextCursor == nil || len(list.Items) != 2 {
		t.Fatalf("page 1 wrong: %+v", list)
	}
	_, env = doRequest(t, router, "/api/qa/pins/"+questionId+"/comments?size=2&cursor="+*list.NextCursor)
	list = decodeList(t, env)
	if list.HasMore || len(list.Items) != 1 {
		t.Fatalf("page 2 wrong: %+v", list)
	}

	for path, wantCode := range map[string]int{
		"/api/qa/pins/notapin/comments":                       40000,
		"/api/qa/pins/" + testPinId("nope") + "/comments":     40400,
		"/api/qa/pins/" + questionId + "/comments?sort=hot":   40000,
		"/api/qa/pins/" + questionId + "/comments?cursor=zzz": 40000,
		"/api/qa/pins/" + questionId + "/comments?size=0":     40000,
	} {
		_, env := doRequest(t, router, path)
		if env.Code != wantCode {
			t.Fatalf("%s: code=%d, want %d (%s)", path, env.Code, wantCode, env.Message)
		}
	}

	// size clamps at 50 rather than erroring.
	_, env = doRequest(t, router, "/api/qa/pins/"+questionId+"/comments?size=500")
	if env.Code != 0 {
		t.Fatalf("size clamp failed: %d %s", env.Code, env.Message)
	}
}

func TestAPI_PinComments_AnswerTargetAndVisibility(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	questionId := testPinId("v2-api-cmt-vis-q")
	answerId := testPinId("v2-api-cmt-vis-a")
	mustBlock(t, agg, questionPin(questionId, "visibility?", "body"))
	mustBlock(t, agg, answerPin(answerId, questionId, "answer", 1755000100, "answerer"))
	body, _ := json.Marshal(map[string]any{"commentTo": answerId, "content": "on answer"})
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("v2-api-cmt-vis-c"), Path: PathPayComment,
		Timestamp: 1755000200, GlobalMetaId: "commenter", Body: string(body),
	}))
	router := newTestRouter(agg)

	_, env := doRequest(t, router, "/api/qa/pins/"+answerId+"/comments")
	if env.Code != 0 {
		t.Fatalf("answer target: %d %s", env.Code, env.Message)
	}
	rows := decodeRows[commentRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].TargetPinId != answerId {
		t.Fatalf("answer target rows: %+v", rows)
	}

	// A revoked question hides its whole subtree from comment threads.
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("v2-api-cmt-vis-rvk"), Path: PathSimpleQuestion, Operation: OperationRevoke,
		OriginalId: questionId, Timestamp: 1755000300, GlobalMetaId: "asker",
	}))
	for _, pinId := range []string{questionId, answerId} {
		_, env := doRequest(t, router, "/api/qa/pins/"+pinId+"/comments")
		if env.Code != 40400 {
			t.Fatalf("comments on %s after question revoke: code=%d, want 40400", pinId, env.Code)
		}
	}
}

// ---------------------------------------------------------------------------
// R6 GET /api/qa/answers and the /api/qa/questions publisher filter
// ---------------------------------------------------------------------------

type feedAnswerRow struct {
	PinId         string         `json:"pinId"`
	QuestionPinId string         `json:"questionPinId"`
	Question      *questionEmbed `json:"question"`
	Publisher     struct {
		GlobalMetaId string `json:"globalMetaId"`
		MetaId       string `json:"metaid"`
	} `json:"publisher"`
	CreatedAt int64 `json:"createdAt"`
	Score     int   `json:"score"`
}

type feedQuestionRow struct {
	PinId     string `json:"pinId"`
	Publisher struct {
		GlobalMetaId string `json:"globalMetaId"`
		MetaId       string `json:"metaid"`
	} `json:"publisher"`
}

func seedAuthorCorpus(t *testing.T, agg *Aggregator) (q1, q2, a1, a2, a3 string) {
	t.Helper()
	q1 = testPinId("v2-feed-q1")
	q2 = testPinId("v2-feed-q2")
	a1 = testPinId("v2-feed-a1")
	a2 = testPinId("v2-feed-a2")
	a3 = testPinId("v2-feed-a3")

	mustBlock(t, agg, questionPin(q1, "author question one?", "body"))
	mustBlock(t, agg, questionPin(q2, "author question two?", "body"))

	// alice answers both questions; bob answers one and earns a like.
	mustBlock(t, agg, answerPin(a1, q1, "alice answer one", 1755000100, "alice"))
	mustBlock(t, agg, answerPin(a2, q2, "alice answer two", 1755000200, "alice"))
	mustBlock(t, agg, answerPin(a3, q1, "bob answer", 1755000300, "bob"))
	mustBlock(t, agg, likePin(testPinId("v2-feed-like"), a3, 1, 1755000400, "fan"))
	return
}

func TestAPI_AnswersFeed_NewestPublisherEmbed(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	q1, q2, a1, a2, a3 := seedAuthorCorpus(t, agg)
	router := newTestRouter(agg)

	_, env := doRequest(t, router, "/api/qa/answers")
	if env.Code != 0 {
		t.Fatalf("feed: %d %s", env.Code, env.Message)
	}
	rows := decodeRows[feedAnswerRow](t, decodeList(t, env))
	if len(rows) != 3 {
		t.Fatalf("feed rows = %d, want 3", len(rows))
	}
	wantOrder := []string{a3, a2, a1}
	for i, want := range wantOrder {
		if rows[i].PinId != want {
			t.Fatalf("newest order[%d] = %s, want %s", i, rows[i].PinId, want)
		}
		if rows[i].Question == nil {
			t.Fatalf("row %s missing embedded question", rows[i].PinId)
		}
	}
	if rows[2].Question.PinId != q1 || rows[1].Question.PinId != q2 {
		t.Fatalf("embedded question wrong: %+v %+v", rows[1].Question, rows[2].Question)
	}
	if rows[0].Score != 1 {
		t.Fatalf("score not exposed: %+v", rows[0])
	}

	// publisher filter by globalMetaId (alice) and metaid (bob).
	_, env = doRequest(t, router, "/api/qa/answers?publisher=alice")
	rows = decodeRows[feedAnswerRow](t, decodeList(t, env))
	if len(rows) != 2 || rows[0].PinId != a2 || rows[1].PinId != a1 {
		t.Fatalf("alice rows: %+v", rows)
	}
	_, env = doRequest(t, router, "/api/qa/answers?publisher=BOB")
	rows = decodeRows[feedAnswerRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].PinId != a3 {
		t.Fatalf("bob rows: %+v", rows)
	}

	// top sort: bob's liked answer outranks alice's unliked ones, newer first
	// among equals.
	_, env = doRequest(t, router, "/api/qa/answers?sort=top")
	rows = decodeRows[feedAnswerRow](t, decodeList(t, env))
	if len(rows) != 3 || rows[0].PinId != a3 || rows[1].PinId != a2 || rows[2].PinId != a1 {
		t.Fatalf("top rows: %+v", rows)
	}

	for path, wantCode := range map[string]int{
		"/api/qa/answers?sort=hot":   40000,
		"/api/qa/answers?cursor=zzz": 40000,
		"/api/qa/answers?size=-1":    40000,
	} {
		_, env := doRequest(t, router, path)
		if env.Code != wantCode {
			t.Fatalf("%s: code=%d want %d", path, env.Code, wantCode)
		}
	}
}

func TestAPI_AnswersFeed_HiddenQuestionExcluded(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	q1, _, a1, _, _ := seedAuthorCorpus(t, agg)
	router := newTestRouter(agg)

	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("v2-feed-q1-rvk"), Path: PathSimpleQuestion, Operation: OperationRevoke,
		OriginalId: q1, Timestamp: 1755000500, GlobalMetaId: "asker",
	}))
	_, env := doRequest(t, router, "/api/qa/answers")
	if env.Code != 0 {
		t.Fatalf("feed: %d %s", env.Code, env.Message)
	}
	rows := decodeRows[feedAnswerRow](t, decodeList(t, env))
	if len(rows) != 1 {
		t.Fatalf("rows after revoke = %d, want 1 (a1=%s excluded)", len(rows), a1)
	}
}

func TestAPI_QuestionsFeed_PublisherFilter(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	_, _, _, _, _ = seedAuthorCorpus(t, agg)
	// asker authored both questions; give one to a different asker.
	otherQ := testPinId("v2-feed-q3")
	other := questionPin(otherQ, "other asker question?", "body")
	other.GlobalMetaId = "otherasker"
	other.MetaId = "metaid_other"
	mustBlock(t, agg, other)
	router := newTestRouter(agg)

	_, env := doRequest(t, router, "/api/qa/questions?publisher=asker")
	if env.Code != 0 {
		t.Fatalf("feed: %d %s", env.Code, env.Message)
	}
	rows := decodeRows[feedQuestionRow](t, decodeList(t, env))
	if len(rows) != 2 {
		t.Fatalf("asker rows = %d, want 2", len(rows))
	}
	for _, row := range rows {
		if row.Publisher.GlobalMetaId != "asker" {
			t.Fatalf("publisher filter leaked: %+v", row)
		}
	}

	_, env = doRequest(t, router, "/api/qa/questions?publisher=METAID_OTHER")
	rows = decodeRows[feedQuestionRow](t, decodeList(t, env))
	if len(rows) != 1 || rows[0].PinId != otherQ {
		t.Fatalf("metaid filter rows: %+v", rows)
	}
}
