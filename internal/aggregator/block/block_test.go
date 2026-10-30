package block

import (
	"errors"
	"fmt"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// ---- fixtures ----

type fakeTargetLookup struct {
	byMetaId       map[string]*IdentityRef
	byGlobalMetaId map[string]*IdentityRef
	byAddress      map[string]*IdentityRef
}

func (f *fakeTargetLookup) LookupByMetaId(metaId string) (*IdentityRef, error) {
	return f.byMetaId[metaId], nil
}

func (f *fakeTargetLookup) LookupByGlobalMetaId(globalMetaId string) (*IdentityRef, error) {
	return f.byGlobalMetaId[globalMetaId], nil
}

func (f *fakeTargetLookup) LookupByAddress(address string) (*IdentityRef, error) {
	return f.byAddress[address], nil
}

const (
	idAlice = "idq1sv98gg8x84mzgnlhe0guyj8ffs2yvvjenmhn0j" // P2PKH sample from MAN GLOBALMETAID.md
	idBob   = "idp1qyq8s4zcvwyx9qvuqw8zrx3qcxqwqyujnkdxjm4tc"
	idCarol = "idq1carol0000000000000000000000000000000000"
	// 64-hex legacy sample (obvious placeholder bytes, not a real identity).
	hexDave = "a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4"
)

func newTestAggregator(t *testing.T) (*Aggregator, *storage.PebbleStore) {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	cacheProvider := cache.New(store)
	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg, store
}

func newTestAggregatorWithLookup(t *testing.T) (*Aggregator, *fakeTargetLookup) {
	t.Helper()
	agg, _ := newTestAggregator(t)
	lookup := &fakeTargetLookup{
		byGlobalMetaId: map[string]*IdentityRef{
			idAlice: {MetaId: "meta-alice", GlobalMetaId: idAlice, Address: "1Alice", Name: "Alice", NameId: "name-alice:i0", AvatarId: "avatar-alice:i0", Bio: "alice bio", BioId: "bio-alice:i0"},
			idBob:   {MetaId: "meta-bob", GlobalMetaId: idBob, Address: "1Bob", Name: "Bob", NameId: "name-bob:i0", AvatarId: "avatar-bob:i0", Bio: "bob bio", BioId: "bob-bio:i0"},
		},
		byMetaId: map[string]*IdentityRef{
			"meta-alice": {MetaId: "meta-alice", GlobalMetaId: idAlice, Address: "1Alice", Name: "Alice"},
			"meta-bob":   {MetaId: "meta-bob", GlobalMetaId: idBob, Address: "1Bob", Name: "Bob"},
			hexDave:      {MetaId: hexDave, GlobalMetaId: idCarol, Address: "1Carol", Name: "Carol"},
		},
	}
	agg.SetProfileLookup(lookup)
	return agg, lookup
}

func blockPin(id, path, fromGlobal, fromMeta, to string, state interface{}, ts int64) *aggregator.PinInscription {
	var body string
	switch v := state.(type) {
	case nil:
		body = `{"to":"` + to + `"}`
	case string:
		body = `{"to":"` + to + `","blockState":"` + v + `"}`
	default:
		body = `{"to":"` + to + `","blockState":` + fmt.Sprintf("%v", v) + `}`
	}
	return &aggregator.PinInscription{
		Id: id, Path: path, Operation: "create",
		GlobalMetaId: fromGlobal, MetaId: fromMeta, CreateMetaId: fromMeta,
		ChainName: "mvc", Timestamp: ts, ContentBody: []byte(body),
	}
}

// ---- path parsing (spec §1.4) ----

func TestIsBlockPathRecognizesBothPathsAndRejectsOthers(t *testing.T) {
	cases := map[string]bool{
		"/protocols/simpleblock":                 true,
		"protocols/simpleblock":                  true,
		"/private/block/simpleprivateblock":      true,
		"private/block/simpleprivateblock":       true,
		"/follow":                                false,
		"/private/chat/simplemsg":                false,
		"/protocols/simplegroupblock":            false,
		"/protocols/simplegroupjoinblock":        false,
		"/protocols/simpleblock@somepin:i0":      true,
		"/private/block/simpleprivateblock@x:i0": true,
	}
	for path, want := range cases {
		if got := IsBlockPath(path); got != want {
			t.Fatalf("IsBlockPath(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestDualPathIngestBothReachReadModel(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	// Compatibility path (publisher without GlobalMetaId — legacy style).
	if _, err := agg.HandleBlockPin(blockPin("blk-compat:i0", "/private/block/simpleprivateblock", "", hexDave, idAlice, 1, 1000)); err != nil {
		t.Fatalf("compat path ingest: %v", err)
	}
	// Canonical path.
	if _, err := agg.HandleBlockPin(blockPin("blk-canonical:i0", "/protocols/simpleblock", idBob, "", idAlice, 1, 1001)); err != nil {
		t.Fatalf("canonical path ingest: %v", err)
	}

	// hexDave resolves to idCarol through the profile lookup.
	blocked, err := agg.IsBlockedBetween(idCarol, idAlice)
	if err != nil || !blocked {
		t.Fatalf("compat-path pair blocked = %v, %v; want true", blocked, err)
	}
	blocked, err = agg.IsBlockedBetween(idBob, idAlice)
	if err != nil || !blocked {
		t.Fatalf("canonical-path pair blocked = %v, %v; want true", blocked, err)
	}
}

func TestOnlyCreateOperationsIngested(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	pin := blockPin("blk-modify:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)
	pin.Operation = "modify"
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("modify ingest: %v", err)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); blocked {
		t.Fatalf("modify operation must not create an edge")
	}

	pin = blockPin("blk-revoke:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1001)
	pin.Operation = "revoke"
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("revoke ingest: %v", err)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); blocked {
		t.Fatalf("revoke operation must not create an edge (unblock is a new create, spec §1.1)")
	}

	if _, err := agg.HandleBlockPin(blockPin("blk-create:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1002)); err != nil {
		t.Fatalf("create ingest: %v", err)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); !blocked {
		t.Fatalf("create operation must create an edge")
	}
}

// ---- last-write-wins folding (spec §1.1) ----

func TestLastWriteWinsFoldingAndUnblockRecovery(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	// block → unblock → block: final state blocked at the newest pin.
	if _, err := agg.HandleBlockPin(blockPin("blk-1:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("block 1: %v", err)
	}
	if _, err := agg.HandleBlockPin(blockPin("blk-2:i0", "/protocols/simpleblock", idAlice, "", idBob, -1, 1001)); err != nil {
		t.Fatalf("unblock 2: %v", err)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); blocked {
		t.Fatalf("pair must be unblocked after blockState=-1")
	}
	if _, err := agg.HandleBlockPin(blockPin("blk-3:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1002)); err != nil {
		t.Fatalf("re-block 3: %v", err)
	}
	blocked, err := agg.IsBlockedBetween(idAlice, idBob)
	if err != nil || !blocked {
		t.Fatalf("re-blocked pair = %v, %v; want true", blocked, err)
	}

	// Listing shows exactly the newest active pin.
	peers, err := agg.BlockingPeers(idAlice)
	if err != nil || len(peers) != 1 {
		t.Fatalf("BlockingPeers = %v, %v; want 1 entry", peers, err)
	}
	if peers[0].BlockPinId != "blk-3:i0" || peers[0].BlockedAt != 1002 {
		t.Fatalf("active pin = %+v; want blk-3:i0@1002", peers[0])
	}
	// No stale index entries for the earlier pins.
	by, err := agg.BlockedByPeers(idBob)
	if err != nil || len(by) != 1 || by[0].BlockPinId != "blk-3:i0" {
		t.Fatalf("BlockedByPeers = %v, %v; want single blk-3:i0", by, err)
	}

	// Unblock at the end removes the pair from every view.
	if _, err := agg.HandleBlockPin(blockPin("blk-4:i0", "/protocols/simpleblock", idAlice, "", idBob, -1, 1003)); err != nil {
		t.Fatalf("unblock 4: %v", err)
	}
	blocked, _ = agg.IsBlockedBetween(idAlice, idBob)
	if blocked {
		t.Fatalf("pair must be unblocked after final -1")
	}
	peers, _ = agg.BlockingPeers(idAlice)
	if len(peers) != 0 {
		t.Fatalf("BlockingPeers after unblock = %v; want empty", peers)
	}
	result, err := agg.ListBlocks(BlockListParams{GlobalMetaId: idAlice, Type: TypeAll})
	if err != nil || len(result.List) != 0 {
		t.Fatalf("ListBlocks after unblock = %v, %v; want empty", result.List, err)
	}
}

func TestReplayDedupeKeepsFoldStable(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	if _, err := agg.HandleBlockPin(blockPin("blk-1:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("block: %v", err)
	}
	if _, err := agg.HandleBlockPin(blockPin("blk-2:i0", "/protocols/simpleblock", idAlice, "", idBob, -1, 1001)); err != nil {
		t.Fatalf("unblock: %v", err)
	}

	// Replaying the older block pin must not resurrect the block: the pair
	// was superseded by the newer unblock (last write wins).
	if _, err := agg.HandleBlockPin(blockPin("blk-1:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("replay block: %v", err)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); blocked {
		t.Fatalf("replayed stale block pin resurrected the block")
	}

	// Replaying the newest pin is a no-op too.
	if _, err := agg.HandleBlockPin(blockPin("blk-2:i0", "/protocols/simpleblock", idAlice, "", idBob, -1, 1001)); err != nil {
		t.Fatalf("replay unblock: %v", err)
	}
	peers, _ := agg.BlockingPeers(idAlice)
	if len(peers) != 0 {
		t.Fatalf("replayed unblock duplicated state: %v", peers)
	}
}

// ---- BlockLister (spec §4.5) ----

func TestBlockListerBothDirectionsAndFailOpen(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	// Alice blocks Bob.
	if _, err := agg.HandleBlockPin(blockPin("blk-ab:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("alice blocks bob: %v", err)
	}
	var lister BlockLister = agg

	blocked, err := lister.IsBlockedBetween(idAlice, idBob)
	if err != nil || !blocked {
		t.Fatalf("forward direction = %v, %v; want true", blocked, err)
	}
	blocked, err = lister.IsBlockedBetween(idBob, idAlice)
	if err != nil || !blocked {
		t.Fatalf("reverse direction = %v, %v; want true (either direction)", blocked, err)
	}

	// Legacy-hex inputs normalize through the profile lookup (spec §2.3).
	blocked, err = lister.IsBlockedBetween("meta-alice", "meta-bob")
	if err != nil || !blocked {
		t.Fatalf("hex inputs = %v, %v; want true", blocked, err)
	}

	// Unknown identities fail open: no known block between them.
	blocked, err = lister.IsBlockedBetween("idq1unknown000000000000000000000000000000000", idAlice)
	if err != nil || blocked {
		t.Fatalf("unknown identity = %v, %v; want false,nil (fail-open)", blocked, err)
	}
	blocked, err = lister.IsBlockedBetween(hexDave, "not-an-identity")
	if err != nil || blocked {
		t.Fatalf("unresolvable input = %v, %v; want false,nil", blocked, err)
	}

	// Same identity on both sides is never "blocked between".
	blocked, err = lister.IsBlockedBetween(idAlice, idAlice)
	if err != nil || blocked {
		t.Fatalf("self pair = %v, %v; want false", blocked, err)
	}
}

func TestBlockCountsAndBlockedPeerIds(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	if _, err := agg.HandleBlockPin(blockPin("blk-1:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("alice blocks bob: %v", err)
	}
	if _, err := agg.HandleBlockPin(blockPin("blk-2:i0", "/protocols/simpleblock", idCarol, "", idAlice, 1, 1001)); err != nil {
		t.Fatalf("carol blocks alice: %v", err)
	}

	blocking, blockedBy, err := agg.BlockCounts(idAlice)
	if err != nil {
		t.Fatalf("BlockCounts: %v", err)
	}
	if blocking != 1 || blockedBy != 1 {
		t.Fatalf("BlockCounts = (%d, %d); want (1, 1)", blocking, blockedBy)
	}

	peerIds, err := agg.BlockedPeerIds("meta-alice") // legacy hex input normalizes
	if err != nil {
		t.Fatalf("BlockedPeerIds: %v", err)
	}
	if len(peerIds) != 2 {
		t.Fatalf("BlockedPeerIds = %v; want both directions (2 entries)", peerIds)
	}
	found := map[string]bool{}
	for _, id := range peerIds {
		found[id] = true
	}
	if !found[idBob] || !found[idCarol] {
		t.Fatalf("BlockedPeerIds missing peers: %v", peerIds)
	}
}

// ---- unresolved target policy (spec §2.5 spirit) ----

func TestUnresolvedTargetRecordsPinButNoEdge(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	if _, err := agg.HandleBlockPin(blockPin("blk-unresolved:i0", "/protocols/simpleblock", idAlice, "", hexDave, 1, 1000)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	// hexDave resolves to idCarol via byMetaId, so this pair exists:
	blocked, _ := agg.IsBlockedBetween(idAlice, idCarol)
	if !blocked {
		t.Fatalf("resolved legacy hex target must create the edge")
	}

	// A completely unknown reference creates no edge.
	if _, err := agg.HandleBlockPin(blockPin("blk-unknown:i0", "/protocols/simpleblock", idAlice, "", "ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000ffff0000", 1, 1001)); err != nil {
		t.Fatalf("ingest unknown target: %v", err)
	}
	if _, _, seen := agg.loadPairByPin("blk-unknown:i0"); !seen {
		t.Fatalf("pin-level index must record the pin for auditability")
	}
}

// ---- mempool pins take effect via the overlay; the confirmed model stays
// confirmed-only (spec §4.4b, OQ-2 reversed 2026-10-30) ----

func TestMempoolPinsMaterializeOverlayOnly(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	if _, err := agg.HandleMempoolPin(blockPin("blk-mem:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("HandleMempoolPin: %v", err)
	}
	// Effective view: blocked immediately (pending, spec §4.4b).
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); !blocked {
		t.Fatalf("mempool pin must take effect immediately (pending)")
	}
	// Confirmed model: untouched — the pin index must NOT record it.
	if _, _, seen := agg.loadPairByPin("blk-mem:i0"); seen {
		t.Fatalf("mempool pin must not be folded into the confirmed model")
	}
	if confirmed, _ := agg.loadActiveEdge(idAlice, idBob); confirmed != nil {
		t.Fatalf("mempool pin must not materialize a confirmed edge")
	}
}

// ---- bridge failure never blocks ingest (authority semantics, spec §4.6) ----

type failingBridge struct{}

func (failingBridge) ApplyChainBlock(fromAliases, toAliases []string, blockPinId string, blockedAt int64) error {
	return errors.New("bridge down")
}

func (failingBridge) RemoveChainBlock(fromAliases, toAliases []string) error {
	return errors.New("bridge down")
}

func TestBridgeFailureDoesNotBlockIngest(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)
	agg.SetChatBlockBridge(failingBridge{})

	if _, err := agg.HandleBlockPin(blockPin("blk-1:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("ingest must survive a failing bridge: %v", err)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); !blocked {
		t.Fatalf("chain read model is the authority and must stay correct")
	}
}
