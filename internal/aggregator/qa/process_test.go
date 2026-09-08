package qa

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func setupTestAggregator(t *testing.T) (*Aggregator, *storage.PebbleStore) {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg, store
}

// testPinId derives a pattern-valid pin id (<64 hex>i0) from a seed.
func testPinId(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:]) + "i0"
}

type qaPinOpts struct {
	Id           string
	Path         string
	Operation    string
	ChainName    string
	OriginalId   string
	Timestamp    int64
	Body         string
	GlobalMetaId string
	MetaId       string
	Address      string
}

func makeQAPin(opts qaPinOpts) *aggregator.PinInscription {
	if opts.ChainName == "" {
		opts.ChainName = "mvc"
	}
	if opts.Operation == "" {
		opts.Operation = OperationCreate
	}
	return &aggregator.PinInscription{
		Id:           opts.Id,
		Path:         opts.Path,
		Operation:    opts.Operation,
		ChainName:    opts.ChainName,
		OriginalId:   opts.OriginalId,
		Timestamp:    opts.Timestamp,
		ContentBody:  []byte(opts.Body),
		ContentType:  "application/json",
		GlobalMetaId: opts.GlobalMetaId,
		MetaId:       opts.MetaId,
		Address:      opts.Address,
	}
}

func questionPin(id, title, content string, tags ...string) *aggregator.PinInscription {
	body, _ := json.Marshal(map[string]any{"title": title, "content": content, "tags": tags})
	return &aggregator.PinInscription{
		Id:           id,
		Path:         PathSimpleQuestion,
		Operation:    OperationCreate,
		ChainName:    "mvc",
		Timestamp:    1755000000,
		ContentBody:  body,
		ContentType:  "application/json",
		GlobalMetaId: "asker",
	}
}

func answerPin(id, answerTo, content string, ts int64, actor string) *aggregator.PinInscription {
	body, _ := json.Marshal(map[string]any{"answerTo": answerTo, "content": content})
	return &aggregator.PinInscription{
		Id:           id,
		Path:         PathSimpleAnswer,
		Operation:    OperationCreate,
		ChainName:    "mvc",
		Timestamp:    ts,
		ContentBody:  body,
		ContentType:  "application/json",
		GlobalMetaId: actor,
	}
}

func likePin(id, target string, isLike int, ts int64, actor string) *aggregator.PinInscription {
	body, _ := json.Marshal(map[string]any{"likeTo": target, "isLike": isLike})
	return &aggregator.PinInscription{
		Id:           id,
		Path:         PathPayLike,
		Operation:    OperationCreate,
		ChainName:    "mvc",
		Timestamp:    ts,
		ContentBody:  body,
		ContentType:  "application/json",
		GlobalMetaId: actor,
	}
}

func commentPin(id, target string, ts int64, actor string) *aggregator.PinInscription {
	body, _ := json.Marshal(map[string]any{"commentTo": target, "content": "a comment"})
	return &aggregator.PinInscription{
		Id:           id,
		Path:         PathPayComment,
		Operation:    OperationCreate,
		ChainName:    "mvc",
		Timestamp:    ts,
		ContentBody:  body,
		ContentType:  "application/json",
		GlobalMetaId: actor,
	}
}

func mustBlock(t *testing.T, agg *Aggregator, pin *aggregator.PinInscription) {
	t.Helper()
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin %s: %v", pin.Id, err)
	}
}

func TestProcessQuestion_EmptyTitleSkipped(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustBlock(t, agg, questionPin(testPinId("empty"), "", "body"))
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id:   testPinId("missing"),
		Path: PathSimpleQuestion,
		Body: `{"content":"no title"}`,
	}))

	docs := agg.searchDocSnapshot()
	if len(docs) != 0 {
		t.Fatalf("expected no indexed questions, got %d", len(docs))
	}
}

func TestProcessAnswer_JoinAndPending(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("question-1")
	a1 := testPinId("answer-1")

	// Answer arrives before its question: held pending, not surfaced.
	mustBlock(t, agg, answerPin(a1, q1, "pending answer body", 1755000100, "bot-a"))
	rec, err := agg.loadQuestion("mvc", q1)
	if err != nil {
		t.Fatalf("loadQuestion: %v", err)
	}
	if rec != nil {
		t.Fatal("question should not exist yet")
	}

	// An orphan answer targeting nothing stays excluded even after more pins.
	orphan := testPinId("orphan")
	mustBlock(t, agg, answerPin(orphan, testPinId("no-such-question"), "orphan body", 1755000101, "bot-x"))

	mustBlock(t, agg, questionPin(q1, "How to recover a wallet?", "details", "wallet"))
	rec, err = agg.loadQuestion("mvc", q1)
	if err != nil {
		t.Fatalf("loadQuestion: %v", err)
	}
	if rec == nil {
		t.Fatal("question missing after create")
	}
	if rec.AnswerCount != 1 {
		t.Fatalf("answerCount = %d, want 1 (pending answer attached)", rec.AnswerCount)
	}
	if rec.TopAnswer == nil || rec.TopAnswer.PinId != a1 {
		t.Fatalf("topAnswer = %+v, want pin %s", rec.TopAnswer, a1)
	}
	if rec.AnswersText == "" || !strings.Contains(rec.AnswersText, "pending answer body") {
		t.Fatalf("answersText = %q", rec.AnswersText)
	}

	// The orphan stays excluded.
	rec2, _ := agg.loadQuestion("mvc", testPinId("no-such-question"))
	if rec2 != nil {
		t.Fatal("orphan target must never create a question")
	}

	// Direct join after the question exists.
	a2 := testPinId("answer-2")
	mustBlock(t, agg, answerPin(a2, q1, "second answer", 1755000200, "bot-b"))
	rec, _ = agg.loadQuestion("mvc", q1)
	if rec.AnswerCount != 2 {
		t.Fatalf("answerCount = %d, want 2", rec.AnswerCount)
	}
}

func TestProcessLike_LastStatePerPublisherWins(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("like-question")
	a1 := testPinId("like-answer")
	mustBlock(t, agg, questionPin(q1, "Question?", "body"))
	mustBlock(t, agg, answerPin(a1, q1, "answer body", 1755000100, "bot-a"))

	// Like then dislike by the same actor: last state wins.
	mustBlock(t, agg, likePin(testPinId("l1"), q1, 1, 1755001000, "actor-1"))
	mustBlock(t, agg, likePin(testPinId("l2"), q1, -1, 1755002000, "actor-1"))
	// A like from a second actor.
	mustBlock(t, agg, likePin(testPinId("l3"), q1, 1, 1755003000, "actor-2"))
	// An older like replayed late must not regress the state.
	mustBlock(t, agg, likePin(testPinId("l4"), q1, 1, 1755000500, "actor-1"))
	// Cancel from a third actor (0 cancels).
	mustBlock(t, agg, likePin(testPinId("l5"), q1, 1, 1755004000, "actor-3"))
	mustBlock(t, agg, likePin(testPinId("l6"), q1, 0, 1755005000, "actor-3"))

	rec, _ := agg.loadQuestion("mvc", q1)
	if rec.LikeCount != 1 || rec.DislikeCount != 1 {
		t.Fatalf("question like/dislike = %d/%d, want 1/1", rec.LikeCount, rec.DislikeCount)
	}

	// Answer likes drive ranking; likes targeting non-QA pins are ignored.
	mustBlock(t, agg, likePin(testPinId("l7"), a1, 1, 1755006000, "actor-1"))
	mustBlock(t, agg, likePin(testPinId("l8"), a1, 1, 1755007000, "actor-2"))
	mustBlock(t, agg, likePin(testPinId("l9"), testPinId("stranger"), 1, 1755008000, "actor-1"))

	ans, _ := agg.loadAnswer("mvc", a1)
	if ans.LikeCount != 2 {
		t.Fatalf("answer likeCount = %d, want 2", ans.LikeCount)
	}
	rec, _ = agg.loadQuestion("mvc", q1)
	if rec.TopAnswer == nil || rec.TopAnswer.PinId != a1 || rec.TopAnswer.LikeCount != 2 {
		t.Fatalf("topAnswer = %+v", rec.TopAnswer)
	}
	if rec.HotPoints != 2*1+1+0+2+0 { // 2*answers + qLikes + qComments + aLikes + aComments
		t.Fatalf("hotPoints = %d", rec.HotPoints)
	}
}

func TestProcessComment_CountsPerTarget(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("comment-question")
	a1 := testPinId("comment-answer")
	mustBlock(t, agg, questionPin(q1, "Question?", "body"))
	mustBlock(t, agg, answerPin(a1, q1, "answer", 1755000100, "bot-a"))

	mustBlock(t, agg, commentPin(testPinId("c1"), q1, 1755001000, "actor-1"))
	mustBlock(t, agg, commentPin(testPinId("c2"), q1, 1755002000, "actor-2"))
	mustBlock(t, agg, commentPin(testPinId("c3"), a1, 1755003000, "actor-1"))
	// Replay of the same comment pin must not double-count.
	mustBlock(t, agg, commentPin(testPinId("c2"), q1, 1755002000, "actor-2"))
	// Comment on an unknown pin is ignored.
	mustBlock(t, agg, commentPin(testPinId("c4"), testPinId("stranger"), 1755004000, "actor-1"))

	rec, _ := agg.loadQuestion("mvc", q1)
	if rec.CommentCount != 2 {
		t.Fatalf("question commentCount = %d, want 2", rec.CommentCount)
	}
	ans, _ := agg.loadAnswer("mvc", a1)
	if ans.CommentCount != 1 {
		t.Fatalf("answer commentCount = %d, want 1", ans.CommentCount)
	}
}

func TestProcessQuestion_ModifyRevoke(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("modify-question")
	mustBlock(t, agg, questionPin(q1, "Original title?", "original body", "tag1"))

	modifyId := testPinId("modify-question-v2")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id:           modifyId,
		Path:         PathSimpleQuestion,
		Operation:    OperationModify,
		OriginalId:   "@" + q1,
		Timestamp:    1755000100,
		Body:         `{"title":"Updated title?","content":"updated body","tags":["tag2"]}`,
		GlobalMetaId: "asker",
	}))
	rec, _ := agg.loadQuestion("mvc", q1)
	if rec.Title != "Updated title?" || rec.CurrentPinId != modifyId || rec.CreatedAt != 1755000000 {
		t.Fatalf("modified record = %+v", rec)
	}
	if len(rec.Tags) != 1 || rec.Tags[0] != "tag2" {
		t.Fatalf("tags = %v", rec.Tags)
	}
	// The modify version pin resolves to the same question.
	locator, ok := agg.lookupLocator(modifyId)
	if !ok || locator.kind != "q" || locator.sourcePinId != q1 {
		t.Fatalf("locator = %+v ok=%v", locator, ok)
	}

	// Revoke hides the question everywhere.
	revokeId := testPinId("modify-question-v3")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id:         revokeId,
		Path:       PathSimpleQuestion,
		Operation:  OperationRevoke,
		OriginalId: "@" + modifyId,
		Timestamp:  1755000200,
	}))
	rec, _ = agg.loadQuestion("mvc", q1)
	if rec == nil || !rec.Hidden {
		t.Fatal("revoked question should stay stored but hidden")
	}
	if len(agg.searchDocSnapshot()) != 0 {
		t.Fatal("revoked question still in search snapshot")
	}
}

func TestProcessAnswer_RevokeDropsCount(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("revoke-question")
	a1 := testPinId("revoke-answer-1")
	a2 := testPinId("revoke-answer-2")
	mustBlock(t, agg, questionPin(q1, "Question?", "body"))
	mustBlock(t, agg, answerPin(a1, q1, "first", 1755000100, "bot-a"))
	mustBlock(t, agg, answerPin(a2, q1, "second", 1755000200, "bot-b"))

	rec, _ := agg.loadQuestion("mvc", q1)
	if rec.AnswerCount != 2 {
		t.Fatalf("answerCount = %d", rec.AnswerCount)
	}

	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id:         testPinId("revoke-answer-1-v2"),
		Path:       PathSimpleAnswer,
		Operation:  OperationRevoke,
		OriginalId: "@" + a1,
		Timestamp:  1755000300,
		Body:       `{"answerTo":"` + q1 + `","content":"first"}`,
	}))
	rec, _ = agg.loadQuestion("mvc", q1)
	if rec.AnswerCount != 1 {
		t.Fatalf("answerCount after revoke = %d, want 1", rec.AnswerCount)
	}
	if rec.TopAnswer == nil || rec.TopAnswer.PinId != a2 {
		t.Fatalf("topAnswer after revoke = %+v", rec.TopAnswer)
	}
}

func TestProcess_MempoolFreshnessThenConfirm(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("mempool-question")
	a1 := testPinId("mempool-answer")

	// Mempool question and answer are visible immediately.
	if _, err := agg.HandleMempoolPin(questionPin(q1, "Mempool question?", "body")); err != nil {
		t.Fatalf("mempool question: %v", err)
	}
	if _, err := agg.HandleMempoolPin(answerPin(a1, q1, "mempool answer", 1755000100, "bot-a")); err != nil {
		t.Fatalf("mempool answer: %v", err)
	}
	rec, _ := agg.loadQuestion("mvc", q1)
	if rec == nil || !rec.IsMempool || rec.AnswerCount != 1 {
		t.Fatalf("mempool state = %+v", rec)
	}
	if len(agg.searchDocSnapshot()) != 1 {
		t.Fatal("mempool question missing from snapshot")
	}

	// Confirmation replaces the mempool records.
	mustBlock(t, agg, questionPin(q1, "Mempool question?", "body"))
	mustBlock(t, agg, answerPin(a1, q1, "mempool answer", 1755000100, "bot-a"))
	rec, _ = agg.loadQuestion("mvc", q1)
	if rec.IsMempool {
		t.Fatal("question still flagged mempool after confirm")
	}
	if rec.AnswerCount != 1 {
		t.Fatalf("answerCount after confirm = %d", rec.AnswerCount)
	}
}

func TestTopAnswer_TieBreakNewerFirst(t *testing.T) {
	older := &AnswerRecord{SourcePinId: "old", CreatedAt: 100, LikeCount: 3, DislikeCount: 0}
	newer := &AnswerRecord{SourcePinId: "new", CreatedAt: 200, LikeCount: 3, DislikeCount: 0}
	if top := topAnswerOf([]*AnswerRecord{older, newer}); top.PinId != "new" {
		t.Fatalf("tie should break to newer, got %s", top.PinId)
	}
	best := &AnswerRecord{SourcePinId: "best", CreatedAt: 50, LikeCount: 4, DislikeCount: 0}
	if top := topAnswerOf([]*AnswerRecord{older, best}); top.PinId != "best" {
		t.Fatalf("highest score should win, got %s", top.PinId)
	}
}
