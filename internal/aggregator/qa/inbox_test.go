package qa

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/inbox"
)

func TestInboxHits_AnswersCommentsLikes(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	ownerPin := testPinId("question-1")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: ownerPin, Path: PathSimpleQuestion, Operation: OperationCreate, ChainName: "mvc",
		Timestamp: 1755000000, GlobalMetaId: "idq1owner", MetaId: "meta-owner", Address: "addr-owner",
		Body: `{"title":"How to surf?","content":"asking for a bot"}`,
	})); err != nil {
		t.Fatalf("question: %v", err)
	}

	answerPin := testPinId("answer-1")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: answerPin, Path: PathSimpleAnswer, Operation: OperationCreate, ChainName: "mvc",
		OriginalId: ownerPin, Timestamp: 1755000100, GlobalMetaId: "idq1fan", Address: "addr-fan",
		Body: `{"answerTo":"` + ownerPin + `","content":"catch the wave like this"}`,
	})); err != nil {
		t.Fatalf("answer: %v", err)
	}

	commentPin := testPinId("comment-1")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: commentPin, Path: PathPayComment, Operation: OperationCreate, ChainName: "mvc",
		Timestamp: 1755000200, GlobalMetaId: "idq1fan", Address: "addr-fan",
		Body: `{"commentTo":"` + ownerPin + `","content":"great question, here is more detail"}`,
	})); err != nil {
		t.Fatalf("comment: %v", err)
	}

	likePin := testPinId("like-1")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: likePin, Path: PathPayLike, Operation: OperationCreate, ChainName: "mvc",
		Timestamp: 1755000300, GlobalMetaId: "idq1fan", Address: "addr-fan",
		Body: `{"likeTo":"` + ownerPin + `","isLike":1}`,
	})); err != nil {
		t.Fatalf("like: %v", err)
	}

	allTypes := map[string]bool{inbox.TypePayLike: true, inbox.TypePayComment: true, inbox.TypeSimpleAnswer: true}

	hits, err := agg.InboxHits("addr-owner", 0, allTypes, 0, "", 100)
	if err != nil {
		t.Fatalf("InboxHits: %v", err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %d (%+v), want 3", len(hits), hits)
	}
	byType := map[string]inbox.Hit{}
	for _, hit := range hits {
		if hit.TargetPinId != ownerPin {
			t.Errorf("hit %s target = %s, want %s", hit.Type, hit.TargetPinId, ownerPin)
		}
		byType[hit.Type] = hit
	}
	if hit := byType[inbox.TypeSimpleAnswer]; hit.PinId != answerPin || hit.ActorAddress != "addr-fan" || hit.CreatedAt != 1755000100 {
		t.Errorf("answer hit = %+v", hit)
	}
	if hit := byType[inbox.TypePayComment]; hit.PinId != commentPin || hit.Excerpt == "" {
		t.Errorf("comment hit = %+v (want excerpt)", hit)
	}
	if hit := byType[inbox.TypePayLike]; hit.PinId != likePin || hit.Dislike {
		t.Errorf("like hit = %+v", hit)
	}

	// Owner addressable by any identity form.
	for _, owner := range []string{"meta-owner", "idq1owner", "ADDR-OWNER"} {
		hits, err := agg.InboxHits(owner, 0, allTypes, 0, "", 100)
		if err != nil || len(hits) != 3 {
			t.Errorf("owner %q: hits = %d err = %v, want 3", owner, len(hits), err)
		}
	}

	// Type filter.
	hits, _ = agg.InboxHits("addr-owner", 0, map[string]bool{inbox.TypeSimpleAnswer: true}, 0, "", 100)
	if len(hits) != 1 || hits[0].Type != inbox.TypeSimpleAnswer {
		t.Fatalf("answer-only filter = %+v", hits)
	}

	// since is inclusive.
	hits, _ = agg.InboxHits("addr-owner", 1755000200, allTypes, 0, "", 100)
	if len(hits) != 2 {
		t.Fatalf("since filter = %d hits, want 2", len(hits))
	}

	// Un-like removes the row; dislike marks the hit.
	unlikePin := testPinId("unlike-1")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: unlikePin, Path: PathPayLike, Operation: OperationCreate, ChainName: "mvc",
		Timestamp: 1755000400, GlobalMetaId: "idq1fan", Address: "addr-fan",
		Body: `{"likeTo":"` + ownerPin + `","isLike":0}`,
	})); err != nil {
		t.Fatalf("unlike: %v", err)
	}
	hits, _ = agg.InboxHits("addr-owner", 0, map[string]bool{inbox.TypePayLike: true}, 0, "", 100)
	if len(hits) != 0 {
		t.Fatalf("unliked row still present: %+v", hits)
	}

	dislikePin := testPinId("dislike-1")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: dislikePin, Path: PathPayLike, Operation: OperationCreate, ChainName: "mvc",
		Timestamp: 1755000500, GlobalMetaId: "idq1fan", Address: "addr-fan",
		Body: `{"likeTo":"` + ownerPin + `","isLike":-1}`,
	})); err != nil {
		t.Fatalf("dislike: %v", err)
	}
	hits, _ = agg.InboxHits("addr-owner", 0, map[string]bool{inbox.TypePayLike: true}, 0, "", 100)
	if len(hits) != 1 || !hits[0].Dislike || hits[0].Excerpt != "dislike" {
		t.Fatalf("dislike hit = %+v", hits)
	}

	// Revoking the question retires all of its inbox rows.
	revokePin := testPinId("revoke-1")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: revokePin, Path: PathSimpleQuestion + "@" + ownerPin, Operation: OperationRevoke, ChainName: "mvc",
		OriginalId: ownerPin, Timestamp: 1755000600, GlobalMetaId: "idq1owner", Address: "addr-owner",
		Body: "",
	})); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	hits, _ = agg.InboxHits("addr-owner", 0, allTypes, 0, "", 100)
	if len(hits) != 0 {
		t.Fatalf("revoked target rows = %+v, want none", hits)
	}
}

func TestInboxHits_PendingAnswerAttachesWhenQuestionArrives(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	// Answer arrives before its question.
	answerPin := testPinId("orphan-answer")
	ownerPin := testPinId("late-question")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: answerPin, Path: PathSimpleAnswer, Operation: OperationCreate, ChainName: "mvc",
		Timestamp: 1755000000, GlobalMetaId: "idq1fan", Address: "addr-fan",
		Body: `{"answerTo":"` + ownerPin + `","content":"answering early"}`,
	})); err != nil {
		t.Fatalf("orphan answer: %v", err)
	}

	allTypes := map[string]bool{inbox.TypeSimpleAnswer: true}
	hits, _ := agg.InboxHits("addr-owner", 0, allTypes, 0, "", 100)
	if len(hits) != 0 {
		t.Fatalf("pending answer surfaced: %+v", hits)
	}

	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: ownerPin, Path: PathSimpleQuestion, Operation: OperationCreate, ChainName: "mvc",
		Timestamp: 1755000100, GlobalMetaId: "idq1owner", Address: "addr-owner",
		Body: `{"title":"Late question?","content":"now indexed"}`,
	})); err != nil {
		t.Fatalf("late question: %v", err)
	}

	hits, _ = agg.InboxHits("addr-owner", 0, allTypes, 0, "", 100)
	if len(hits) != 1 || hits[0].PinId != answerPin {
		t.Fatalf("attached answer = %+v, want %s", hits, answerPin)
	}
}

func TestInboxHits_BackfillCoversLegacyRecords(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	ownerPin := testPinId("legacy-question")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: ownerPin, Path: PathSimpleQuestion, Operation: OperationCreate, ChainName: "mvc",
		Timestamp: 1755000000, GlobalMetaId: "idq1owner", Address: "addr-owner",
		Body: `{"title":"Legacy?","content":"indexed before the owner index"}`,
	})); err != nil {
		t.Fatalf("question: %v", err)
	}
	answerPin := testPinId("legacy-answer")
	if _, err := agg.HandleBlockPin(makeQAPin(qaPinOpts{
		Id: answerPin, Path: PathSimpleAnswer, Operation: OperationCreate, ChainName: "mvc",
		OriginalId: ownerPin, Timestamp: 1755000100, GlobalMetaId: "idq1fan", Address: "addr-fan",
		Body: `{"answerTo":"` + ownerPin + `","content":"legacy answer"}`,
	})); err != nil {
		t.Fatalf("answer: %v", err)
	}

	// Simulate a store whose records predate the owner index: drop the index
	// entries and the state marker, then re-Init to trigger the backfill.
	for _, prefix := range [][]byte{[]byte(keyInboxAnswerOwner), []byte(keyInboxCommentOwner), []byte(keyInboxLikeOwner)} {
		if err := store.DeleteByPrefix(Namespace, prefix); err != nil {
			t.Fatalf("drop %q: %v", prefix, err)
		}
	}
	if err := store.Delete(Namespace, inboxOwnerStateKey()); err != nil {
		t.Fatalf("drop state: %v", err)
	}
	reloaded := &Aggregator{}
	if err := reloaded.Init(store, nil); err != nil {
		t.Fatalf("re-Init: %v", err)
	}

	hits, err := reloaded.InboxHits("addr-owner", 0, map[string]bool{inbox.TypeSimpleAnswer: true}, 0, "", 100)
	if err != nil {
		t.Fatalf("InboxHits after backfill: %v", err)
	}
	if len(hits) != 1 || hits[0].PinId != answerPin {
		t.Fatalf("backfilled hits = %+v, want %s", hits, answerPin)
	}
}
