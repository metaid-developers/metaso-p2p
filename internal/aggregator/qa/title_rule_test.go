package qa

// Tests of the R7 question-mark title rule
// (docs/specs/2026-09-08-metaweb-qa-question-title-rule.md): only questions
// whose trimmed title ends with `?` or `？` enter the Q&A index; a modify
// that drops the mark de-indexes the question together with its answers
// until the mark is restored.

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func TestTitleEndsWithQuestionMark(t *testing.T) {
	for _, tc := range []struct {
		title string
		want  bool
	}{
		{"What is MetaID?", true},
		{"MetaID 是什么？", true},
		{"Seriously??", true},
		{"真的吗？？", true},
		{"What is MetaID ?", true},
		{"  What is MetaID?  ", true},
		{"?", true},
		{"What is MetaID", false},
		{"What is MetaID.", false},
		{"What is MetaID!", false},
		{"MetaID 是什么。", false},
		{"MetaID 是什么！", false},
		{"", false},
		{"   ", false},
	} {
		if got := titleEndsWithQuestionMark(tc.title); got != tc.want {
			t.Fatalf("titleEndsWithQuestionMark(%q) = %v, want %v", tc.title, got, tc.want)
		}
	}
}

// countNamespaceKeys counts qa-namespace keys under a prefix (test helper
// for the time indexes).
func countNamespaceKeys(t *testing.T, agg *Aggregator, prefix []byte) int {
	t.Helper()
	count := 0
	if err := agg.store.ScanPrefix(Namespace, prefix, func(_, _ []byte) error {
		count++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestProcessQuestion_TitleWithoutQuestionMarkSkipped(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("r7-nomark-q")
	a1 := testPinId("r7-nomark-a")
	mustBlock(t, agg, questionPin(q1, "This is not a question", "body"))
	mustBlock(t, agg, answerPin(a1, q1, "answer to the void", 1755000100, "bot-a"))

	// No record, no pin map, no search document — the same treatment as the
	// empty-title rule.
	if rec, _ := agg.loadQuestion("mvc", q1); rec != nil {
		t.Fatal("no-mark question must not be indexed")
	}
	if _, ok := agg.lookupLocator(q1); ok {
		t.Fatal("no-mark question must not be pin-mapped")
	}
	if len(agg.searchDocSnapshot()) != 0 {
		t.Fatal("no-mark question must not enter the search snapshot")
	}
	// Its answer is held pending (unresolved) and never surfaces.
	answer, _ := agg.loadAnswer("mvc", a1)
	if answer == nil || answer.QuestionPinId != "" {
		t.Fatalf("answer should be held pending, got %+v", answer)
	}
	if got := countNamespaceKeys(t, agg, answerTimePrefix()); got != 0 {
		t.Fatalf("answer time entries = %d, want 0", got)
	}

	// A full-width mark is a first-class question.
	q2 := testPinId("r7-fullwidth-q")
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: q2, Path: PathSimpleQuestion, Timestamp: 1755000000, GlobalMetaId: "asker",
		Body: `{"title":"钱包丢了怎么找回？","content":"body"}`,
	}))
	if rec, _ := agg.loadQuestion("mvc", q2); rec == nil {
		t.Fatal("full-width question mark title must be indexed")
	}
}

func TestProcessQuestion_ModifyRemovesQuestionMark_DeIndexesAndRestores(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("r7-mod-q")
	a1 := testPinId("r7-mod-a")
	mustBlock(t, agg, questionPin(q1, "How do pins sign?", "body"))
	mustBlock(t, agg, answerPin(a1, q1, "with their keys", 1755000100, "bot-a"))
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("r7-mod-c"), Path: PathPayComment, Timestamp: 1755000200, GlobalMetaId: "commenter",
		Body: `{"commentTo":"` + q1 + `","content":"a comment"}`,
	}))
	router := newTestRouter(agg)

	if got := countNamespaceKeys(t, agg, questionTimePrefix()); got != 1 {
		t.Fatalf("question time entries before modify = %d, want 1", got)
	}
	if got := countNamespaceKeys(t, agg, answerTimePrefix()); got != 1 {
		t.Fatalf("answer time entries before modify = %d, want 1", got)
	}

	// A modify that drops the trailing question mark de-indexes the question
	// and its whole subtree; the record itself is retained.
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("r7-mod-v2"), Path: PathSimpleQuestion, Operation: OperationModify,
		OriginalId: "@" + q1, Timestamp: 1755000300, GlobalMetaId: "asker",
		Body: `{"title":"How pins sign"}`,
	}))
	rec, _ := agg.loadQuestion("mvc", q1)
	if rec == nil || rec.Title != "How pins sign" || rec.Hidden {
		t.Fatalf("record should stay retained with the updated title: %+v", rec)
	}
	if got := countNamespaceKeys(t, agg, questionTimePrefix()); got != 0 {
		t.Fatalf("question time entries after mark removed = %d, want 0", got)
	}
	if got := countNamespaceKeys(t, agg, answerTimePrefix()); got != 0 {
		t.Fatalf("answer time entries after mark removed = %d, want 0", got)
	}
	if len(agg.searchDocSnapshot()) != 0 {
		t.Fatal("de-indexed question still in search snapshot")
	}
	for _, path := range []string{"/api/qa/questions", "/api/qa/answers"} {
		_, env := doRequest(t, router, path)
		if env.Code != 0 {
			t.Fatalf("%s: code=%d msg=%s", path, env.Code, env.Message)
		}
		if list := decodeList(t, env); len(list.Items) != 0 {
			t.Fatalf("%s served %d rows after de-index", path, len(list.Items))
		}
	}
	_, env := doRequest(t, router, "/api/qa/search?q=sign")
	if list := decodeList(t, env); len(list.Items) != 0 {
		t.Fatalf("search matched de-indexed question: %d rows", len(list.Items))
	}
	if _, env := doRequest(t, router, "/api/qa/questions/"+q1); env.Code != codeNotFound {
		t.Fatalf("detail after de-index: code=%d, want 40400", env.Code)
	}
	for _, pinId := range []string{q1, a1} {
		if _, env := doRequest(t, router, "/api/qa/pins/"+pinId+"/comments"); env.Code != codeNotFound {
			t.Fatalf("comments on %s after de-index: code=%d, want 40400", pinId, env.Code)
		}
	}

	// Restoring the mark re-indexes the question and its subtree.
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("r7-mod-v3"), Path: PathSimpleQuestion, Operation: OperationModify,
		OriginalId: "@" + q1, Timestamp: 1755000400, GlobalMetaId: "asker",
		Body: `{"title":"How do pins sign exactly?"}`,
	}))
	if got := countNamespaceKeys(t, agg, questionTimePrefix()); got != 1 {
		t.Fatalf("question time entries after restore = %d, want 1", got)
	}
	if got := countNamespaceKeys(t, agg, answerTimePrefix()); got != 1 {
		t.Fatalf("answer time entries after restore = %d, want 1", got)
	}
	if _, env := doRequest(t, router, "/api/qa/questions/"+q1); env.Code != 0 {
		t.Fatalf("detail after restore: code=%d msg=%s", env.Code, env.Message)
	}
	_, env = doRequest(t, router, "/api/qa/answers")
	if rows := decodeRows[feedAnswerRow](t, decodeList(t, env)); len(rows) != 1 || rows[0].PinId != a1 {
		t.Fatalf("answers after restore = %+v", rows)
	}
	_, env = doRequest(t, router, "/api/qa/pins/"+q1+"/comments")
	if rows := decodeRows[commentRow](t, decodeList(t, env)); len(rows) != 1 {
		t.Fatalf("comments after restore = %+v", rows)
	}
}

func TestProcessQuestion_ModifyWithoutTitleKeepsVisibility(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	q1 := testPinId("r7-keep-q")
	mustBlock(t, agg, questionPin(q1, "Kept title?", "body"))
	// A modify whose payload omits the title keeps the previous title, so
	// the question stays visible.
	mustBlock(t, agg, makeQAPin(qaPinOpts{
		Id: testPinId("r7-keep-v2"), Path: PathSimpleQuestion, Operation: OperationModify,
		OriginalId: "@" + q1, Timestamp: 1755000100, GlobalMetaId: "asker",
		Body: `{"content":"edited body only"}`,
	}))
	if got := countNamespaceKeys(t, agg, questionTimePrefix()); got != 1 {
		t.Fatalf("question time entries = %d, want 1", got)
	}
	router := newTestRouter(agg)
	if _, env := doRequest(t, router, "/api/qa/questions/"+q1); env.Code != 0 {
		t.Fatalf("detail: code=%d msg=%s", env.Code, env.Message)
	}
}

// TestStartupReconcile_DeIndexesLegacyNoMarkQuestions seeds a pre-R7 store
// (question indexed under a statement title, answer globally listed) and
// verifies a fresh startup drops the stale entries without any pin replay,
// while a qualifying question survives.
func TestStartupReconcile_DeIndexesLegacyNoMarkQuestions(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer store.Close()

	legacyQ := testPinId("r7-legacy-q")
	legacyA := testPinId("r7-legacy-a")
	validQ := testPinId("r7-valid-q")
	seedLegacyQuestion := func(pinId, title string, createdAt int64) {
		t.Helper()
		if err := agg.saveQuestion(&QuestionRecord{
			SourcePinId: pinId, CurrentPinId: pinId, ChainName: "mvc",
			Title: title, Content: "body",
			Publisher: Identity{GlobalMetaId: "asker"}, Operation: OperationCreate,
			CreatedAt: createdAt, UpdatedAt: createdAt,
		}); err != nil {
			t.Fatal(err)
		}
		if err := agg.mapPin(pinId, recordLocator{kind: "q", chainName: "mvc", sourcePinId: pinId}); err != nil {
			t.Fatal(err)
		}
		if err := agg.store.Set(Namespace, questionTimeKey(createdAt, "mvc", pinId), []byte{}); err != nil {
			t.Fatal(err)
		}
	}
	seedLegacyQuestion(legacyQ, "Legacy statement title", 1755000000)
	seedLegacyQuestion(validQ, "Legacy question title?", 1755000010)
	if err := agg.saveAnswer(&AnswerRecord{
		SourcePinId: legacyA, CurrentPinId: legacyA, ChainName: "mvc",
		QuestionChain: "mvc", QuestionPinId: legacyQ, AnswerTo: legacyQ,
		Content: "legacy answer", Publisher: Identity{GlobalMetaId: "bot"},
		Operation: OperationCreate, CreatedAt: 1755000100, UpdatedAt: 1755000100,
	}); err != nil {
		t.Fatal(err)
	}
	if err := agg.store.Set(Namespace, answerIndexKey("mvc", legacyQ, 1755000100, legacyA), []byte{}); err != nil {
		t.Fatal(err)
	}
	if err := agg.store.Set(Namespace, answerTimeKey(1755000100, "mvc", legacyA), []byte{}); err != nil {
		t.Fatal(err)
	}

	// The pre-R7 state: both questions listed, legacy answer globally listed.
	if got := countNamespaceKeys(t, agg, questionTimePrefix()); got != 2 {
		t.Fatalf("seeded question time entries = %d, want 2", got)
	}
	if got := countNamespaceKeys(t, agg, answerTimePrefix()); got != 1 {
		t.Fatalf("seeded answer time entries = %d, want 1", got)
	}

	agg2 := &Aggregator{}
	if err := agg2.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init (reconcile): %v", err)
	}
	if got := countNamespaceKeys(t, agg2, questionTimePrefix()); got != 1 {
		t.Fatalf("question time entries after reconcile = %d, want 1 (valid question)", got)
	}
	if got := countNamespaceKeys(t, agg2, answerTimePrefix()); got != 0 {
		t.Fatalf("answer time entries after reconcile = %d, want 0", got)
	}
	if len(agg2.searchDocSnapshot()) != 1 {
		t.Fatalf("search snapshot after reconcile = %d docs, want 1", len(agg2.searchDocSnapshot()))
	}

	router := newTestRouter(agg2)
	_, env := doRequest(t, router, "/api/qa/questions")
	if list := decodeList(t, env); len(list.Items) != 1 {
		t.Fatalf("feed after reconcile = %d rows, want 1", len(list.Items))
	}
	if _, env := doRequest(t, router, "/api/qa/questions/"+legacyQ); env.Code != codeNotFound {
		t.Fatalf("legacy detail after reconcile: code=%d, want 40400", env.Code)
	}
	if _, env := doRequest(t, router, "/api/qa/questions/"+validQ); env.Code != 0 {
		t.Fatalf("valid detail after reconcile: code=%d msg=%s", env.Code, env.Message)
	}
}
