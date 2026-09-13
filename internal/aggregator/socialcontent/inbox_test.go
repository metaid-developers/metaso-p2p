package socialcontent

import (
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/inbox"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
)

func scPin(id, path, op string, ts int64, body string, author string) *aggregator.PinInscription {
	pin := testPin(id, path, op, "mvc", ts, []byte(body))
	switch author {
	case "owner":
		pin.GlobalMetaId = "idq1owner"
		pin.MetaId = "meta-owner"
		pin.Address = "addr-owner"
		pin.CreateMetaId = "meta-owner"
		pin.CreateAddress = "addr-owner"
	case "fan":
		pin.GlobalMetaId = "idq1fan"
		pin.MetaId = "meta-fan"
		pin.Address = "addr-fan"
		pin.CreateMetaId = "meta-fan"
		pin.CreateAddress = "addr-fan"
	}
	return pin
}

func TestInboxHits_CommentsAndLikes(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	postPin := "buzz-inbox-post:i0"
	if _, err := agg.HandleBlockPin(scPin(postPin, PathSimpleBuzz, OperationCreate, 1755000000, `{"content":"my post"}`, "owner")); err != nil {
		t.Fatalf("post: %v", err)
	}

	commentPin := "buzz-inbox-comment:i0"
	if _, err := agg.HandleBlockPin(scPin(commentPin, PathPayComment, OperationCreate, 1755000100, `{"commentTo":"`+postPin+`","content":"nice post"}`, "fan")); err != nil {
		t.Fatalf("comment: %v", err)
	}

	likePin := "buzz-inbox-like:i0"
	if _, err := agg.HandleBlockPin(scPin(likePin, PathPayLike, OperationCreate, 1755000200, `{"likeTo":"`+postPin+`","isLike":true}`, "fan")); err != nil {
		t.Fatalf("like: %v", err)
	}

	allTypes := map[string]bool{inbox.TypePayLike: true, inbox.TypePayComment: true}
	hits, err := agg.InboxHits("addr-owner", 0, allTypes, 0, "", 100)
	if err != nil {
		t.Fatalf("InboxHits: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("hits = %+v, want 2", hits)
	}
	byType := map[string]inbox.Hit{}
	for _, hit := range hits {
		if hit.TargetPinId != postPin {
			t.Errorf("hit target = %s, want %s", hit.TargetPinId, postPin)
		}
		byType[hit.Type] = hit
	}
	if hit := byType[inbox.TypePayComment]; hit.PinId != commentPin || hit.Excerpt == "" || hit.CreatedAt != 1755000100 {
		t.Errorf("comment hit = %+v", hit)
	}
	if hit := byType[inbox.TypePayLike]; hit.PinId != likePin || hit.CreatedAt != 1755000200 {
		t.Errorf("like hit = %+v", hit)
	}

	// Owner addressable by every identity form.
	for _, owner := range []string{"meta-owner", "idq1owner", "ADDR-OWNER"} {
		hits, err := agg.InboxHits(owner, 0, allTypes, 0, "", 100)
		if err != nil || len(hits) != 2 {
			t.Errorf("owner %q: hits = %d err = %v, want 2", owner, len(hits), err)
		}
	}

	// Un-like removes the row.
	unlikePin := "buzz-inbox-unlike:i0"
	if _, err := agg.HandleBlockPin(scPin(unlikePin, PathPayLike, OperationCreate, 1755000300, `{"likeTo":"`+postPin+`","isLike":false}`, "fan")); err != nil {
		t.Fatalf("unlike: %v", err)
	}
	hits, _ = agg.InboxHits("addr-owner", 0, map[string]bool{inbox.TypePayLike: true}, 0, "", 100)
	if len(hits) != 0 {
		t.Fatalf("unliked row still present: %+v", hits)
	}

	// Revoking the post retires its rows.
	revokePin := "buzz-inbox-revoke:i0"
	if _, err := agg.HandleBlockPin(scPin(revokePin, PathSimpleBuzz+"@"+postPin, OperationRevoke, 1755000400, `{"content":"my post"}`, "owner")); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	hits, _ = agg.InboxHits("addr-owner", 0, allTypes, 0, "", 100)
	if len(hits) != 0 {
		t.Fatalf("revoked target rows = %+v, want none", hits)
	}
}

func TestInboxHits_BackfillAndLatePosts(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	// Comment arrives before its post exists.
	commentPin := "buzz-early-comment:i0"
	postPin := "buzz-late-post:i0"
	if _, err := agg.HandleBlockPin(scPin(commentPin, PathPayComment, OperationCreate, 1755000000, `{"commentTo":"`+postPin+`","content":"early comment"}`, "fan")); err != nil {
		t.Fatalf("comment before post: %v", err)
	}
	hits, _ := agg.InboxHits("addr-owner", 0, map[string]bool{inbox.TypePayComment: true}, 0, "", 100)
	if len(hits) != 0 {
		t.Fatalf("pre-post comment surfaced: %+v", hits)
	}

	if _, err := agg.HandleBlockPin(scPin(postPin, PathSimpleBuzz, OperationCreate, 1755000100, `{"content":"late post"}`, "owner")); err != nil {
		t.Fatalf("post: %v", err)
	}

	// The reconcile path attaches the pending comment and its owner entry.
	hits, _ = agg.InboxHits("addr-owner", 0, map[string]bool{inbox.TypePayComment: true}, 0, "", 100)
	if len(hits) != 1 || hits[0].PinId != commentPin {
		t.Fatalf("reconciled comment = %+v, want %s", hits, commentPin)
	}

	// Simulate a legacy store: drop owner indexes + state marker, re-Init.
	for _, prefix := range [][]byte{[]byte(keyInboxCommentOwner), []byte(keyInboxLikeOwner)} {
		if err := store.DeleteByPrefix(Namespace, prefix); err != nil {
			t.Fatalf("drop %q: %v", prefix, err)
		}
	}
	if err := store.Delete(Namespace, inboxOwnerStateKey()); err != nil {
		t.Fatalf("drop state: %v", err)
	}
	reloaded := &Aggregator{}
	if err := reloaded.Init(store, cache.New(store)); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	hits, err := reloaded.InboxHits("addr-owner", 0, map[string]bool{inbox.TypePayComment: true}, 0, "", 100)
	if err != nil {
		t.Fatalf("InboxHits after backfill: %v", err)
	}
	if len(hits) != 1 || hits[0].PinId != commentPin || !strings.Contains(hits[0].Excerpt, "early") {
		t.Fatalf("backfilled hits = %+v, want %s with excerpt", hits, commentPin)
	}
}
