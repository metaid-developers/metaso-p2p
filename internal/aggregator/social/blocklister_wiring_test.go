package social

import (
	"fmt"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/block"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// chainBlockPin builds a /protocols/simpleblock pin in the same wire shape
// the block package fixtures use (canonical GlobalMetaID identities).
func chainBlockPin(id, from, to string, state int64, ts int64) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:          id,
		Path:        "/protocols/simpleblock",
		Operation:   "create",
		GlobalMetaId: from,
		ChainName:   "mvc",
		Timestamp:   ts,
		ContentBody: []byte(fmt.Sprintf(`{"to":%q,"blockState":%d}`, to, state)),
	}
}

// TestRealBlockListerWiringFiltersListsAndCounts proves the production
// wiring: the social read paths filter through the REAL block aggregation
// (block.Aggregator adapted by NewBlockLister), not a stub — a chain block
// pin flips following/followers/friends/counts, and a blockState = -1 pin
// restores everything.
func TestRealBlockListerWiringFiltersListsAndCounts(t *testing.T) {
	socialAgg, store := newTestSocialAggregator(t)
	defer store.Close()
	// ListFollowing/ListFollowers require the subject to resolve through
	// the profile lookup even in canonical form; give the fixture the same
	// shape the friends tests use.
	socialAgg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testGidB:      newTargetRef(testMetaB, testGidB, testAddrB),
		},
		byMetaId: map[string]*TargetRef{
			testMetaSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testMetaB:      newTargetRef(testMetaB, testGidB, testAddrB),
		},
	})
	blockAgg := &block.Aggregator{}
	if err := blockAgg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("block Init: %v", err)
	}

	seedFriendGraph(t, socialAgg)
	// Graph for source: following {target,a,b}=3, followers {target,a,b,c}=4,
	// friends {target,a,b}=3.

	socialAgg.SetBlockLister(NewBlockLister(blockAgg))

	// Baseline: the real lister with no blocks behaves like the no-op
	// default on every read.
	counts, err := socialAgg.Counts(testGidSource)
	if err != nil {
		t.Fatalf("Counts baseline: %v", err)
	}
	if counts.Following != 3 || counts.Followers != 4 || counts.Friends != 3 ||
		counts.BlockedCount != 0 || counts.BlockedByCount != 0 {
		t.Fatalf("baseline counts = %+v, want 3/4/3 with 0/0 block counters", counts)
	}

	// Chain block: source blocks b → b leaves every source-facing list and
	// count; blockedCount becomes real data.
	if _, err := blockAgg.HandleBlockPin(chainBlockPin("blk-sb1:i0", testGidSource, testGidB, 1, 2000)); err != nil {
		t.Fatalf("block ingest: %v", err)
	}

	counts, err = socialAgg.Counts(testGidSource)
	if err != nil {
		t.Fatalf("Counts blocked: %v", err)
	}
	if counts.Following != 2 || counts.Followers != 3 || counts.Friends != 2 {
		t.Fatalf("blocked counts = %+v, want following 2 / followers 3 / friends 2 (b excluded)", counts)
	}
	if counts.BlockedCount != 1 || counts.BlockedByCount != 0 {
		t.Fatalf("blocked counters = %+v, want blockedCount 1 / blockedByCount 0", counts)
	}

	for name, list := range map[string]func() []string{
		"following": func() []string {
			r, err := socialAgg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 10})
			if err != nil {
				t.Fatalf("ListFollowing: %v", err)
			}
			return globalIdsOf(r.List)
		},
		"followers": func() []string {
			r, err := socialAgg.ListFollowers(ListParams{GlobalMetaId: testGidSource, Size: 10})
			if err != nil {
				t.Fatalf("ListFollowers: %v", err)
			}
			return globalIdsOf(r.List)
		},
		"friends": func() []string {
			r, err := socialAgg.ListFriends(ListParams{GlobalMetaId: testGidSource, Size: 10})
			if err != nil {
				t.Fatalf("ListFriends: %v", err)
			}
			return friendIdsOf(r.List)
		},
	} {
		for _, id := range list() {
			if id == testGidB {
				t.Fatalf("%s still contains blocked peer %s", name, testGidB)
			}
		}
	}

	// The peer's own view reflects the reverse direction through the same
	// wiring (b is blocked-by source).
	countsB, err := socialAgg.Counts(testGidB)
	if err != nil {
		t.Fatalf("Counts(b): %v", err)
	}
	if countsB.Followers != 0 || countsB.Following != 0 || countsB.Friends != 0 ||
		countsB.BlockedCount != 0 || countsB.BlockedByCount != 1 {
		t.Fatalf("b counts = %+v, want all relation counters 0 with blockedByCount 1", countsB)
	}

	// Unblock on chain (blockState = -1, newest pin wins) → full restore.
	if _, err := blockAgg.HandleBlockPin(chainBlockPin("blk-sb1-off:i0", testGidSource, testGidB, -1, 2001)); err != nil {
		t.Fatalf("unblock ingest: %v", err)
	}

	counts, err = socialAgg.Counts(testGidSource)
	if err != nil {
		t.Fatalf("Counts restored: %v", err)
	}
	if counts.Following != 3 || counts.Followers != 4 || counts.Friends != 3 ||
		counts.BlockedCount != 0 || counts.BlockedByCount != 0 {
		t.Fatalf("restored counts = %+v, want 3/4/3 with 0/0 block counters", counts)
	}
	friends, err := socialAgg.ListFriends(ListParams{GlobalMetaId: testGidSource, Size: 10})
	if err != nil {
		t.Fatalf("ListFriends restored: %v", err)
	}
	if len(friends.List) != 3 {
		t.Fatalf("restored friends len = %d, want 3", len(friends.List))
	}

	// Reverse direction: b blocks source → the union semantics must filter
	// source's lists again, now via blockedByCount.
	if _, err := blockAgg.HandleBlockPin(chainBlockPin("blk-bs1:i0", testGidB, testGidSource, 1, 2002)); err != nil {
		t.Fatalf("reverse block ingest: %v", err)
	}
	counts, err = socialAgg.Counts(testGidSource)
	if err != nil {
		t.Fatalf("Counts reverse-blocked: %v", err)
	}
	if counts.Following != 2 || counts.Followers != 3 || counts.Friends != 2 ||
		counts.BlockedCount != 0 || counts.BlockedByCount != 1 {
		t.Fatalf("reverse-blocked counts = %+v, want 2/3/2 with blockedByCount 1", counts)
	}
}

// TestNewBlockListerAdapterShape covers the adapter contract itself: nil
// source folds to the no-op default and IsBlockedBetween passes through.
func TestNewBlockListerAdapterShape(t *testing.T) {
	if NewBlockLister(nil) != nil {
		t.Fatal("NewBlockLister(nil) must return nil (SetBlockLister folds nil to the no-op default)")
	}

	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	blockAgg := &block.Aggregator{}
	if err := blockAgg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("block Init: %v", err)
	}

	lister := NewBlockLister(blockAgg)
	blocking, err := lister.BlockingBy(testGidSource)
	if err != nil || len(blocking) != 0 {
		t.Fatalf("BlockingBy empty = %v, %v; want no peers", blocking, err)
	}

	if _, err := blockAgg.HandleBlockPin(chainBlockPin("blk-adapter:i0", testGidSource, testGidTarget, 1, 1000)); err != nil {
		t.Fatalf("block ingest: %v", err)
	}

	blocked, err := lister.IsBlockedBetween(testGidSource, testGidTarget)
	if err != nil || !blocked {
		t.Fatalf("IsBlockedBetween = %v, %v; want true after chain block", blocked, err)
	}
	blocking, err = lister.BlockingBy(testGidSource)
	if err != nil || len(blocking) != 1 || blocking[0] != testGidTarget {
		t.Fatalf("BlockingBy = %v, %v; want [%s]", blocking, err, testGidTarget)
	}
	blockedBy, err := lister.BlockedBy(testGidTarget)
	if err != nil || len(blockedBy) != 1 || blockedBy[0] != testGidSource {
		t.Fatalf("BlockedBy(target) = %v, %v; want [%s]", blockedBy, err, testGidSource)
	}
}

func globalIdsOf(items []ListItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GlobalMetaId)
	}
	return ids
}

func friendIdsOf(items []FriendItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.GlobalMetaId)
	}
	return ids
}
