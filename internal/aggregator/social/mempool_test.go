package social

import (
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// newMempoolTestAggregator wires a social aggregator with a profile lookup
// covering the source/target identities, so list/relationship read paths
// resolve.
func newMempoolTestAggregator(t *testing.T) *Aggregator {
	t.Helper()
	agg, _ := newTestSocialAggregator(t)
	lookup := &fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testGidTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
			testGidA:      newTargetRef(testMetaA, testGidA, testAddrA),
			testGidB:      newTargetRef(testMetaB, testGidB, testAddrB),
		},
	}
	agg.SetProfileLookup(lookup)
	return agg
}

func mustHandle(t *testing.T, agg *Aggregator, pin *aggregator.PinInscription, mempool bool) {
	t.Helper()
	var err error
	if mempool {
		_, err = agg.HandleMempoolPin(pin)
	} else {
		_, err = agg.HandleBlockPin(pin)
	}
	if err != nil {
		t.Fatalf("handle pin %s (mempool=%v): %v", pin.Id, mempool, err)
	}
}

func overlayKeys(agg *Aggregator) (pairs, pins int) {
	_ = agg.forEachPendingPair(func(_, _ string, _ *pendingFollow) error {
		pairs++
		return nil
	})
	_ = agg.store.ScanPrefix(namespace, pendingPinPrefix(), func(_, _ []byte) error {
		pins++
		return nil
	})
	return
}

// §4.4b: a mempool /follow pin materializes the edge immediately (pending),
// in every read surface: pair load, relationship, lists, counts.
func TestMempoolFollowTakesEffectImmediately(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	mustHandle(t, agg, followPin("pinmp1i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360000), true)

	edge, err := agg.loadEffectiveEdge(testGidSource, testGidTarget)
	if err != nil || edge == nil {
		t.Fatalf("effective edge after mempool follow = (%v, %v), want non-nil", edge, err)
	}
	if !edge.Pending || edge.FollowPinId != "pinmp1i0" || edge.FollowedAt != 1793360000 {
		t.Fatalf("pending edge = %+v, want Pending with pinmp1i0@1793360000", edge)
	}
	// The confirmed model is untouched.
	if confirmed, _ := agg.loadActiveEdge(testGidSource, testGidTarget); confirmed != nil {
		t.Fatalf("confirmed model wrote the mempool pin: %+v", confirmed)
	}

	rel, err := agg.Relationship(RelationshipParams{SourceGlobalMetaId: testGidSource, TargetGlobalMetaId: testGidTarget})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if !rel.Source.FollowsTarget || !rel.Source.Pending || rel.Source.FollowPinId != "pinmp1i0" {
		t.Fatalf("relationship source side = %+v, want pending follow pinmp1i0", rel.Source)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 1 || following.List[0].GlobalMetaId != testGidTarget || !following.List[0].Pending {
		t.Fatalf("following list = %+v, want [target pending]", following.List)
	}
	if following.List[0].FollowPinId != "pinmp1i0" {
		t.Fatalf("following item pin = %q, want pinmp1i0", following.List[0].FollowPinId)
	}

	followers, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget})
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers.List) != 1 || followers.List[0].GlobalMetaId != testGidSource || !followers.List[0].Pending {
		t.Fatalf("followers list = %+v, want [source pending]", followers.List)
	}

	counts, err := agg.Counts(testGidSource)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Following != 1 || counts.Followers != 0 {
		t.Fatalf("counts(source) = %+v, want following=1 followers=0", counts)
	}
}

// Confirmation transitions the overlay entry into the confirmed model with
// no visible jump: same pin id, pending flag gone, overlay keys consumed.
func TestConfirmationTransitionsSeamlessly(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	pin := followPin("pinmp2i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360100)
	mustHandle(t, agg, pin, true)
	mustHandle(t, agg, pin, false)

	edge, err := agg.loadEffectiveEdge(testGidSource, testGidTarget)
	if err != nil || edge == nil {
		t.Fatalf("effective edge after confirm = (%v, %v), want non-nil", edge, err)
	}
	if edge.Pending || edge.FollowPinId != "pinmp2i0" {
		t.Fatalf("edge after confirm = %+v, want confirmed pinmp2i0", edge)
	}
	if confirmed, _ := agg.loadActiveEdge(testGidSource, testGidTarget); confirmed == nil || confirmed.FollowPinId != "pinmp2i0" {
		t.Fatalf("confirmed model after confirm = %+v, want pinmp2i0", confirmed)
	}
	if pairs, pins := overlayKeys(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after confirm = (%d pairs, %d pins), want (0, 0)", pairs, pins)
	}

	// Re-fold of the same confirmed pin (backfill replay) stays idempotent,
	// and a stale mempool re-delivery must not resurrect the overlay.
	mustHandle(t, agg, pin, false)
	mustHandle(t, agg, pin, true)
	if pairs, pins := overlayKeys(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after replay + stale re-delivery = (%d pairs, %d pins), want (0, 0)", pairs, pins)
	}
}

// Idempotent mempool re-delivery keeps one overlay entry with the original
// ownership.
func TestMempoolRedeliveryIsIdempotent(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	pin := followPin("pinmp3i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360200)
	mustHandle(t, agg, pin, true)
	mustHandle(t, agg, pin, true)
	mustHandle(t, agg, pin, true)

	pairs, pins := overlayKeys(agg)
	if pairs != 1 || pins != 1 {
		t.Fatalf("overlay after 3 deliveries = (%d pairs, %d pins), want (1, 1)", pairs, pins)
	}
	if entry := agg.loadPendingFollow(testGidSource, testGidTarget); entry == nil || entry.FollowPinId != "pinmp3i0" {
		t.Fatalf("pair entry after re-delivery = %+v, want pinmp3i0", entry)
	}
}

// LWW per pair by arrival: a second unconfirmed refollow owns the pair; the
// first pin's confirmation consumes only its own pin entry.
func TestMempoolPairLWWByArrival(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	first := followPin("pinmp4ai0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360300)
	second := followPin("pinmp4bi0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360301)
	mustHandle(t, agg, first, true)
	mustHandle(t, agg, second, true)

	entry := agg.loadPendingFollow(testGidSource, testGidTarget)
	if entry == nil || entry.FollowPinId != "pinmp4bi0" {
		t.Fatalf("pair owner after two mempool follows = %+v, want pinmp4bi0", entry)
	}

	// The older pin confirms: its pin entry is consumed, the newer pending
	// mutation keeps owning the pair.
	mustHandle(t, agg, first, false)
	if entry = agg.loadPendingFollow(testGidSource, testGidTarget); entry == nil || entry.FollowPinId != "pinmp4bi0" {
		t.Fatalf("pair owner after older confirm = %+v, want pinmp4bi0", entry)
	}
	edge, _ := agg.loadEffectiveEdge(testGidSource, testGidTarget)
	if edge == nil || !edge.Pending || edge.FollowPinId != "pinmp4bi0" {
		t.Fatalf("effective edge after older confirm = %+v, want pending pinmp4bi0", edge)
	}

	// The newer pin confirms: the overlay drains and the confirmed model
	// holds the newest state.
	mustHandle(t, agg, second, false)
	if pairs, pins := overlayKeys(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after both confirms = (%d, %d), want (0, 0)", pairs, pins)
	}
	if confirmed, _ := agg.loadActiveEdge(testGidSource, testGidTarget); confirmed == nil || confirmed.FollowPinId != "pinmp4bi0" {
		t.Fatalf("confirmed model after both confirms = %+v, want pinmp4bi0", confirmed)
	}
}

// A mempool revoke hides the pair immediately; when the revoked follow is
// still unconfirmed and the revoke confirms, nothing resurfaces.
func TestMempoolRevokeHidesPendingFollow(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	mustHandle(t, agg, followPin("pinmp5fi0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360400), true)
	mustHandle(t, agg, revokePin("pinmp5ri0", "pinmp5fi0", 1793360401), true)

	if edge, _ := agg.loadEffectiveEdge(testGidSource, testGidTarget); edge != nil {
		t.Fatalf("effective edge after pending revoke = %+v, want nil", edge)
	}
	// The revoke's confirmation folds (the follow never confirmed → no-op on
	// the confirmed model) and consumes its own overlay ownership. The
	// revoked follow pin's ref survives: if that pin still confirms in a
	// later block, its fold must stay consumable (and expiry drops it if it
	// never lands).
	mustHandle(t, agg, revokePin("pinmp5ri0", "pinmp5fi0", 1793360401), false)
	pairs, pins := overlayKeys(agg)
	if pairs != 0 || pins != 1 {
		t.Fatalf("overlay after revoke confirm = (%d pairs, %d pins), want (0, 1 follow-pin ref)", pairs, pins)
	}
	if removed := agg.SweepExpiredMempoolOverlay(time.Now().Add(3*time.Hour), 2*time.Hour); removed != 1 {
		t.Fatalf("aged follow-pin ref sweep removed=%d, want 1", removed)
	}
	if pairs, pins = overlayKeys(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after full expiry = (%d, %d), want (0, 0)", pairs, pins)
	}
}

// A mempool revoke over a CONFIRMED follow hides it immediately; expiry of
// the revoke (never confirmed) restores the confirmed edge.
func TestMempoolRevokeExpiryRestoresConfirmedEdge(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	mustHandle(t, agg, followPin("pinmp6fi0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360500), false)
	mustHandle(t, agg, revokePin("pinmp6ri0", "pinmp6fi0", 1793360501), true)

	if edge, _ := agg.loadEffectiveEdge(testGidSource, testGidTarget); edge != nil {
		t.Fatalf("effective edge after pending revoke = %+v, want nil", edge)
	}

	// Force the revoke entry old, then sweep with a shorter-than-age expiry.
	future := time.Now().Add(3 * time.Hour)
	if removed := agg.SweepExpiredMempoolOverlay(future, 2*time.Hour); removed != 1 {
		t.Fatalf("sweep removed = %d, want 1", removed)
	}
	edge, err := agg.loadEffectiveEdge(testGidSource, testGidTarget)
	if err != nil || edge == nil || edge.Pending {
		t.Fatalf("effective edge after expiry = (%v, %v), want confirmed non-pending", edge, err)
	}
	if edge.FollowPinId != "pinmp6fi0" {
		t.Fatalf("restored edge pin = %q, want pinmp6fi0", edge.FollowPinId)
	}
}

// A mempool revoke over a confirmed follow, then the revoke confirms: the
// edge disappears for good (confirmed fold deletes it).
func TestMempoolRevokeConfirmDeletesConfirmedEdge(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	mustHandle(t, agg, followPin("pinmp7fi0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360600), false)
	rev := revokePin("pinmp7ri0", "pinmp7fi0", 1793360601)
	mustHandle(t, agg, rev, true)
	mustHandle(t, agg, rev, false)

	if edge, _ := agg.loadEffectiveEdge(testGidSource, testGidTarget); edge != nil {
		t.Fatalf("effective edge after revoke confirm = %+v, want nil", edge)
	}
	if pairs, pins := overlayKeys(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after revoke confirm = (%d, %d), want (0, 0)", pairs, pins)
	}
}

// The sweeper self-heals an overlay entry whose pin already folded into the
// confirmed model with its consumption missed (e.g. across a restart).
func TestSweepSelfHealsConfirmedOverlay(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	// Write the overlay, then fold the confirmed edge directly, bypassing
	// processPin's consumption.
	mustHandle(t, agg, followPin("pinmp8i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360700), true)
	if err := agg.saveActiveEdge(&FollowEdge{
		FollowerGlobalMetaId: testGidSource,
		TargetGlobalMetaId:   testGidTarget,
		FollowPinId:          "pinmp8i0",
		FollowedAt:           1793360700,
	}); err != nil {
		t.Fatalf("saveActiveEdge: %v", err)
	}

	if removed := agg.SweepExpiredMempoolOverlay(time.Now(), 2*time.Hour); removed != 1 {
		t.Fatalf("sweep removed = %d, want 1 (self-heal)", removed)
	}
	if pairs, pins := overlayKeys(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after self-heal sweep = (%d, %d), want (0, 0)", pairs, pins)
	}
	if confirmed, _ := agg.loadActiveEdge(testGidSource, testGidTarget); confirmed == nil {
		t.Fatalf("confirmed edge vanished after self-heal")
	}
}

// Expiry drops never-confirmed entries and reveals the confirmed state
// beneath; entries inside the threshold survive.
func TestSweepExpiryThreshold(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	mustHandle(t, agg, followPin("pinmp9ai0", testGidA, testMetaA, testAddrA, testGidB, 1793360800), true)

	// Fresh entry survives a sweep at the default threshold.
	if removed := agg.SweepExpiredMempoolOverlay(time.Now(), 2*time.Hour); removed != 0 {
		t.Fatalf("fresh entry swept: removed=%d, want 0", removed)
	}
	// Same sweep three hours later drops it.
	if removed := agg.SweepExpiredMempoolOverlay(time.Now().Add(3*time.Hour), 2*time.Hour); removed != 1 {
		t.Fatalf("aged entry sweep removed=%d, want 1", removed)
	}
	if pairs, pins := overlayKeys(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after expiry = (%d, %d), want (0, 0)", pairs, pins)
	}
}

// Mutual (friend) flips the moment the second direction lands in mempool:
// relationship, friends list and friends count all reflect the pending edge.
func TestMempoolSecondFollowFlipsMutual(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	mustHandle(t, agg, followPin("pinmp10ai0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793360900), false)
	if rel, err := agg.Relationship(RelationshipParams{SourceGlobalMetaId: testGidSource, TargetGlobalMetaId: testGidTarget}); err != nil || rel.Mutual {
		t.Fatalf("mutual before second follow = (%v, err=%v), want false", rel != nil && rel.Mutual, err)
	}

	mustHandle(t, agg, followPin("pinmp10bi0", testGidTarget, testMetaTarget, testAddrTarget, testGidSource, 1793360901), true)

	rel, err := agg.Relationship(RelationshipParams{SourceGlobalMetaId: testGidSource, TargetGlobalMetaId: testGidTarget})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if !rel.Mutual {
		t.Fatalf("mutual after pending second follow = false, want true")
	}
	if !rel.Target.Pending || rel.Target.FollowPinId != "pinmp10bi0" {
		t.Fatalf("target side = %+v, want pending pinmp10bi0", rel.Target)
	}

	friends, err := agg.ListFriends(ListParams{GlobalMetaId: testGidSource, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFriends: %v", err)
	}
	if len(friends.List) != 1 || friends.List[0].GlobalMetaId != testGidTarget || !friends.List[0].Pending {
		t.Fatalf("friends list = %+v, want [target pending]", friends.List)
	}
	if friends.List[0].FollowPinId != "pinmp10ai0" || friends.List[0].FriendSince != 1793360901 {
		t.Fatalf("friends item = %+v, want subject pin pinmp10ai0, friendSince 1793360901", friends.List[0])
	}

	counts, err := agg.Counts(testGidSource)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Friends != 1 || counts.Following != 1 || counts.Followers != 1 {
		t.Fatalf("counts = %+v, want friends=1 following=1 followers=1", counts)
	}

	// Batch relationship endpoint answers friend on the pending edge too.
	batch, err := agg.BatchRelationships(BatchRelationshipParams{SourceGlobalMetaId: testGidSource, Targets: []string{testGidTarget}})
	if err != nil {
		t.Fatalf("BatchRelationships: %v", err)
	}
	if len(batch.List) != 1 || batch.List[0].Relation != RelationFriend || !batch.List[0].IsFriend {
		t.Fatalf("batch = %+v, want friend", batch.List)
	}
}

// A pending revoke breaking one direction dissolves the friendship in the
// read views while the confirmed mutual index still holds it.
func TestMempoolRevokeDissolvesMutual(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	mustHandle(t, agg, followPin("pinmp11ai0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1793361000), false)
	mustHandle(t, agg, followPin("pinmp11bi0", testGidTarget, testMetaTarget, testAddrTarget, testGidSource, 1793361001), false)

	mustHandle(t, agg, revokePin("pinmp11ri0", "pinmp11ai0", 1793361002), true)

	rel, err := agg.Relationship(RelationshipParams{SourceGlobalMetaId: testGidSource, TargetGlobalMetaId: testGidTarget})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if rel.Mutual {
		t.Fatalf("mutual after pending revoke = true, want false")
	}
	friends, err := agg.ListFriends(ListParams{GlobalMetaId: testGidSource})
	if err != nil {
		t.Fatalf("ListFriends: %v", err)
	}
	if len(friends.List) != 0 {
		t.Fatalf("friends after pending revoke = %+v, want empty", friends.List)
	}
	counts, err := agg.Counts(testGidSource)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Friends != 0 {
		t.Fatalf("friends count after pending revoke = %d, want 0", counts.Friends)
	}
}

// Pending-only edges beyond one page surface on the first page only, capped
// at size; the cursor contract over the confirmed index stays intact.
func TestListPaginationWithPendingOnlyFirstPage(t *testing.T) {
	agg := newMempoolTestAggregator(t)
	// Two confirmed follows (A→B, A→target) plus one pending-only (A→source
	// direction swap: use B as follower elsewhere; here subject A follows
	// target confirmed and source pending).
	mustHandle(t, agg, followPin("pinmp12ai0", testGidA, testMetaA, testAddrA, testGidTarget, 1793361100), false)
	mustHandle(t, agg, followPin("pinmp12bi0", testGidA, testMetaA, testAddrA, testGidB, 1793361101), false)
	mustHandle(t, agg, followPin("pinmp12ci0", testGidA, testMetaA, testAddrA, testGidSource, 1793361102), true)

	page1, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidA, Size: 2})
	if err != nil {
		t.Fatalf("ListFollowing page1: %v", err)
	}
	if len(page1.List) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1.List))
	}
	if page1.List[0].GlobalMetaId != testGidSource || !page1.List[0].Pending {
		t.Fatalf("page1[0] = %+v, want pending source first", page1.List[0])
	}
	if page1.List[1].GlobalMetaId != testGidB || page1.List[1].Pending {
		t.Fatalf("page1[1] = %+v, want confirmed B", page1.List[1])
	}

	page2, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidA, Size: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("ListFollowing page2: %v", err)
	}
	if len(page2.List) != 1 || page2.List[0].GlobalMetaId != testGidTarget || page2.List[0].Pending {
		t.Fatalf("page2 = %+v, want [confirmed target]", page2.List)
	}
}

// The overlay survives a store close/reopen (Pebble persistence): a fresh
// aggregator instance over the same directory still serves the pending edge.
func TestOverlayPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewPebbleStore(dir)
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	mustHandle(t, agg, followPin("pinmp13i0", testGidSource, "", "", testGidTarget, 1793361200), true)
	_ = store.Close()

	store2 := storage.NewPebbleStore(dir)
	defer func() { _ = store2.Close() }()
	agg2 := &Aggregator{}
	if err := agg2.Init(store2, cache.New(store2)); err != nil {
		t.Fatalf("Init(reopened): %v", err)
	}
	edge, err := agg2.loadEffectiveEdge(testGidSource, testGidTarget)
	if err != nil || edge == nil || !edge.Pending || edge.FollowPinId != "pinmp13i0" {
		t.Fatalf("edge after reopen = (%v, %v), want pending pinmp13i0", edge, err)
	}
}
