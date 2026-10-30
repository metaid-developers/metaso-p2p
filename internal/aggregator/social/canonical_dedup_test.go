package social

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// newCanonicalDedupAggregator wires the profile lookup with BOTH key forms
// (hex metaid and GlobalMetaID) so legacy-keyed rows canonicalize.
func newCanonicalDedupAggregator(t *testing.T) *Aggregator {
	t.Helper()
	agg, store := newTestSocialAggregator(t)
	t.Cleanup(func() { _ = store.Close() })
	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testGidTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
			testGidA:      newTargetRef(testMetaA, testGidA, testAddrA),
		},
		byMetaId: map[string]*TargetRef{
			testMetaSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testMetaTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
			testMetaA:      newTargetRef(testMetaA, testGidA, testAddrA),
		},
	})
	return agg
}

// liveFollowPin shapes a pin like the live MVC indexer delivers it: the
// wallet chain address sits in GlobalMetaId AND MetaId (internal/chain/mvc
// indexer.go), the payload names the target by GlobalMetaID (new-follow
// convention).
func liveFollowPin(id, followerAddress, targetRef string, ts int64) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:            id,
		Path:          "/follow",
		Operation:     "create",
		GlobalMetaId:  followerAddress,
		MetaId:        followerAddress,
		CreateMetaId:  followerAddress,
		Address:       followerAddress,
		CreateAddress: followerAddress,
		ChainName:     "mvc",
		Timestamp:     ts,
		ContentBody:   []byte(targetRef),
	}
}

// §4.4c root cause: the live indexer's chain-address GlobalMetaId must not
// leak into the stored keys — the edge lands on the canonical GlobalMetaID
// pair (address converts locally through the IDAddress algorithm).
func TestIngestCanonicalizesLiveAddressGlobalMetaId(t *testing.T) {
	agg := newCanonicalDedupAggregator(t)
	if _, err := agg.HandleBlockPin(liveFollowPin("live-1:i0", testAddrSource, testGidTarget, 2000)); err != nil {
		t.Fatalf("HandleBlockPin(live address follower): %v", err)
	}

	// Confirmed pair/index keys are canonical, not address-keyed.
	edge, err := agg.loadActiveEdge(testGidSource, testGidTarget)
	if err != nil || edge == nil {
		t.Fatalf("canonical pair edge = (%v, %v), want non-nil", edge, err)
	}
	if edge.FollowPinId != "live-1:i0" {
		t.Fatalf("edge pin = %q, want live-1:i0", edge.FollowPinId)
	}
	if legacy, _ := agg.loadActiveEdge(testAddrSource, testGidTarget); legacy != nil {
		t.Fatalf("address-keyed pair edge exists: %+v", legacy)
	}

	followers, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers.List) != 1 || followers.List[0].GlobalMetaId != testGidSource || followers.List[0].Pending {
		t.Fatalf("followers = %+v, want single canonical confirmed source", followers.List)
	}

	// The same pin replayed through the backfill path (GlobalMetaID fields)
	// stays one row — the two ingest paths now share one key space.
	if _, err := agg.HandleBlockPin(followPin("live-1:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 2000)); err != nil {
		t.Fatalf("HandleBlockPin(backfill replay): %v", err)
	}
	followers, err = agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget})
	if err != nil {
		t.Fatalf("ListFollowers after replay: %v", err)
	}
	if len(followers.List) != 1 {
		t.Fatalf("followers after cross-path replay = %+v, want still 1 row", followers.List)
	}
}

// §4.4c acceptance scenario: a legacy hex-keyed confirmed row and a
// canonical idq1-keyed pending overlay entry name the same pair — the
// followers list must render ONE row. Selection rule: the newest overlay
// entry wins, so the pending follow (newer) shows as pending; a stale
// pending (older) yields to the confirmed edge.
func TestFollowersDedupHexConfirmedPlusIdqPendingSamePair(t *testing.T) {
	t.Run("pending newer shows pending", func(t *testing.T) {
		agg := newCanonicalDedupAggregator(t)
		// Pre-fix stored row: follower keyed by legacy hex metaid.
		if err := agg.saveActiveEdge(&FollowEdge{
			FollowerGlobalMetaId: testMetaSource,
			TargetGlobalMetaId:   testGidTarget,
			FollowPinId:          "legacyhex:i0",
			FollowedAt:           1000,
		}); err != nil {
			t.Fatalf("seed legacy hex edge: %v", err)
		}
		// New unconfirmed re-follow naming the pair by GlobalMetaIDs.
		if _, err := agg.HandleMempoolPin(followPin("pendingnew:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 2000)); err != nil {
			t.Fatalf("HandleMempoolPin: %v", err)
		}

		followers, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, View: ViewProfile})
		if err != nil {
			t.Fatalf("ListFollowers: %v", err)
		}
		if len(followers.List) != 1 {
			t.Fatalf("followers = %+v, want 1 merged row", followers.List)
		}
		row := followers.List[0]
		if row.GlobalMetaId != testGidSource || !row.Pending || row.FollowPinId != "pendingnew:i0" {
			t.Fatalf("merged row = %+v, want canonical pending pendingnew:i0", row)
		}
		counts, err := agg.Counts(testGidTarget)
		if err != nil {
			t.Fatalf("Counts: %v", err)
		}
		if counts.Followers != 1 {
			t.Fatalf("counts.Followers = %d, want 1 (== deduped list)", counts.Followers)
		}
	})

	t.Run("confirmed newer shows confirmed", func(t *testing.T) {
		agg := newCanonicalDedupAggregator(t)
		// Stale pending broadcast (older) on top of a newer confirmed row
		// keyed by hex.
		if err := agg.saveActiveEdge(&FollowEdge{
			FollowerGlobalMetaId: testMetaSource,
			TargetGlobalMetaId:   testGidTarget,
			FollowPinId:          "legacyhex:i0",
			FollowedAt:           3000,
		}); err != nil {
			t.Fatalf("seed legacy hex edge: %v", err)
		}
		if _, err := agg.HandleMempoolPin(followPin("pendingold:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1000)); err != nil {
			t.Fatalf("HandleMempoolPin: %v", err)
		}

		followers, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, View: ViewProfile})
		if err != nil {
			t.Fatalf("ListFollowers: %v", err)
		}
		if len(followers.List) != 1 {
			t.Fatalf("followers = %+v, want 1 merged row", followers.List)
		}
		row := followers.List[0]
		if row.Pending || row.FollowPinId != "legacyhex:i0" || row.FollowedAt != 3000 {
			t.Fatalf("merged row = %+v, want confirmed legacyhex:i0@3000", row)
		}
	})
}

// The production defect shape (AI_Sunny followers, 2026-10-30): the same
// follow pin materialized twice — live path keyed by chain address with the
// block timestamp, backfill keyed by GlobalMetaID with its own timestamp —
// renders as one row with the newer followedAt, and counts agree.
func TestFollowersDedupAddressAndCanonicalConfirmedTwins(t *testing.T) {
	agg := newCanonicalDedupAggregator(t)
	if err := agg.saveActiveEdge(&FollowEdge{
		FollowerGlobalMetaId: testAddrSource, // live-path form, manapi ts
		TargetGlobalMetaId:   testGidTarget,
		FollowPinId:          "twinpin:i0",
		FollowedAt:           1793362955,
	}); err != nil {
		t.Fatalf("seed address-keyed twin: %v", err)
	}
	if err := agg.saveActiveEdge(&FollowEdge{
		FollowerGlobalMetaId: testGidSource, // backfill form, block ts
		TargetGlobalMetaId:   testGidTarget,
		FollowPinId:          "twinpin:i0",
		FollowedAt:           1793364395,
	}); err != nil {
		t.Fatalf("seed canonical twin: %v", err)
	}

	followers, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers.List) != 1 {
		t.Fatalf("followers = %+v, want 1 merged row (got %d)", followers.List, len(followers.List))
	}
	row := followers.List[0]
	if row.GlobalMetaId != testGidSource || row.FollowPinId != "twinpin:i0" || row.FollowedAt != 1793364395 {
		t.Fatalf("merged row = %+v, want canonical twinpin:i0@1793364395", row)
	}
	counts, err := agg.Counts(testGidTarget)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Followers != 1 {
		t.Fatalf("counts.Followers = %d, want 1 (== deduped list)", counts.Followers)
	}
}

// Counts stay consistent with the deduplicated lists across several peers
// with mixed stored forms, paginating the whole list.
func TestCountsMatchDedupedListsAcrossForms(t *testing.T) {
	agg := newCanonicalDedupAggregator(t)
	// A→target confirmed (hex key), source→target confirmed (address key)
	// plus canonical, B→target confirmed (canonical only).
	edges := []*FollowEdge{
		{FollowerGlobalMetaId: testMetaA, TargetGlobalMetaId: testGidTarget, FollowPinId: "e-a:i0", FollowedAt: 1001},
		{FollowerGlobalMetaId: testAddrSource, TargetGlobalMetaId: testGidTarget, FollowPinId: "e-s1:i0", FollowedAt: 1002},
		{FollowerGlobalMetaId: testGidSource, TargetGlobalMetaId: testGidTarget, FollowPinId: "e-s2:i0", FollowedAt: 1003},
		{FollowerGlobalMetaId: testGidB, TargetGlobalMetaId: testGidTarget, FollowPinId: "e-b:i0", FollowedAt: 1004},
	}
	for _, edge := range edges {
		if err := agg.saveActiveEdge(edge); err != nil {
			t.Fatalf("seed edge %s: %v", edge.FollowPinId, err)
		}
	}

	seen := 0
	cursor := ""
	for {
		page, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, Size: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListFollowers(page @%q): %v", cursor, err)
		}
		seen += len(page.List)
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if seen != 3 {
		t.Fatalf("paginated followers total = %d, want 3 unique identities", seen)
	}
	counts, err := agg.Counts(testGidTarget)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Followers != seen {
		t.Fatalf("counts.Followers = %d, want %d (== paginated list)", counts.Followers, seen)
	}
}

// §4.4c ingest heal: folding a confirmed create sweeps the pre-fix twin row
// of the same pair under another key form — and a later revoke of the new
// pin then leaves nothing behind (no ghost follower resurrected from the
// stale twin).
func TestCreateSweepsLegacyTwinRowsAndRevokeLeavesNoGhost(t *testing.T) {
	agg := newCanonicalDedupAggregator(t)
	if err := agg.saveActiveEdge(&FollowEdge{
		FollowerGlobalMetaId: testAddrSource,
		TargetGlobalMetaId:   testGidTarget,
		FollowPinId:          "legacyaddr:i0",
		FollowedAt:           1000,
	}); err != nil {
		t.Fatalf("seed address-keyed twin: %v", err)
	}

	// New canonical follow of the same pair (backfill-shaped pin carries
	// both forms; the sweep must consume the address-keyed twin).
	if _, err := agg.HandleBlockPin(followPin("newpin:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 2000)); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}
	if legacy, _ := agg.loadActiveEdge(testAddrSource, testGidTarget); legacy != nil {
		t.Fatalf("address-keyed twin survived the create sweep: %+v", legacy)
	}
	followers, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers.List) != 1 || followers.List[0].FollowPinId != "newpin:i0" {
		t.Fatalf("followers after sweep = %+v, want single newpin:i0 row", followers.List)
	}

	// Revoking the new pin must NOT resurrect the swept twin.
	if _, err := agg.HandleBlockPin(revokePin("rev-newpin:i0", "newpin:i0", 3000)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}
	followers, err = agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget})
	if err != nil {
		t.Fatalf("ListFollowers after revoke: %v", err)
	}
	if len(followers.List) != 0 {
		t.Fatalf("followers after revoke = %+v, want empty (no ghost twin)", followers.List)
	}
	counts, err := agg.Counts(testGidTarget)
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if counts.Followers != 0 {
		t.Fatalf("counts.Followers after revoke = %d, want 0", counts.Followers)
	}
}

// A revoke reaching only one key form (pre-fix data) still sweeps the twin
// row of the same pair.
func TestRevokeSweepsLegacyTwinRows(t *testing.T) {
	agg := newCanonicalDedupAggregator(t)
	for _, edge := range []*FollowEdge{
		{FollowerGlobalMetaId: testAddrSource, TargetGlobalMetaId: testGidTarget, FollowPinId: "legacyaddr:i0", FollowedAt: 1000},
		{FollowerGlobalMetaId: testGidSource, TargetGlobalMetaId: testGidTarget, FollowPinId: "canon:i0", FollowedAt: 1001},
	} {
		if err := agg.saveActiveEdge(edge); err != nil {
			t.Fatalf("seed edge %s: %v", edge.FollowPinId, err)
		}
	}

	if _, err := agg.HandleBlockPin(revokePin("rev-canon:i0", "canon:i0", 2000)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}
	if legacy, _ := agg.loadActiveEdge(testAddrSource, testGidTarget); legacy != nil {
		t.Fatalf("address-keyed twin survived the revoke sweep: %+v", legacy)
	}
	followers, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget})
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers.List) != 0 {
		t.Fatalf("followers after revoke sweep = %+v, want empty", followers.List)
	}
}

// Single-pair reads see legacy-keyed rows: a pair stored ONLY under the
// chain-address form still answers relationship and batch queries made by
// canonical GlobalMetaIDs (profile aliases drive the probe).
func TestRelationshipSeesLegacyAddressKeyedPair(t *testing.T) {
	agg := newCanonicalDedupAggregator(t)
	if err := agg.saveActiveEdge(&FollowEdge{
		FollowerGlobalMetaId: testAddrSource,
		TargetGlobalMetaId:   testGidTarget,
		FollowPinId:          "legacyaddr:i0",
		FollowedAt:           1000,
	}); err != nil {
		t.Fatalf("seed address-keyed edge: %v", err)
	}

	rel, err := agg.Relationship(RelationshipParams{SourceGlobalMetaId: testGidSource, TargetGlobalMetaId: testGidTarget})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if !rel.Source.FollowsTarget || rel.Source.FollowPinId != "legacyaddr:i0" {
		t.Fatalf("relationship source side = %+v, want follows via legacyaddr:i0", rel.Source)
	}

	batch, err := agg.BatchRelationships(BatchRelationshipParams{SourceGlobalMetaId: testGidSource, Targets: []string{testGidTarget}})
	if err != nil {
		t.Fatalf("BatchRelationships: %v", err)
	}
	if len(batch.List) != 1 || !batch.List[0].FollowedByMe || batch.List[0].Relation != RelationFollowing {
		t.Fatalf("batch = %+v, want following via legacy row", batch.List)
	}
}

// The tuple cursor keeps the shared pagination contract: a cursor minted by
// one list is rejected on the other (kind marker), and a stale anchor
// rejects with 40000.
func TestEdgeListCursorKindAndStaleRejection(t *testing.T) {
	agg := newCanonicalDedupAggregator(t)
	if _, err := agg.HandleBlockPin(followPin("cur-1:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1001)); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}
	if _, err := agg.HandleBlockPin(followPin("cur-2:i0", testGidSource, testMetaSource, testAddrSource, testGidA, 1002)); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}

	page1, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 1})
	if err != nil {
		t.Fatalf("ListFollowing page1: %v", err)
	}
	if page1.NextCursor == "" {
		t.Fatal("page1 nextCursor empty")
	}
	if _, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, Cursor: page1.NextCursor}); err != ErrInvalidParameter {
		t.Fatalf("cross-list cursor err = %v, want ErrInvalidParameter", err)
	}

	page2, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 1, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("ListFollowing page2: %v", err)
	}
	if len(page2.List) != 1 || page2.List[0].GlobalMetaId != testGidTarget {
		t.Fatalf("page2 = %+v, want [target]", page2.List)
	}

	if _, err := agg.HandleBlockPin(revokePin("cur-rev:i0", "cur-2:i0", 2000)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}
	if _, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 1, Cursor: page1.NextCursor}); err != ErrInvalidParameter {
		t.Fatalf("stale cursor err = %v, want ErrInvalidParameter", err)
	}
}
