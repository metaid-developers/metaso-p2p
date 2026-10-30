package block

import (
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func overlayCounts(agg *Aggregator) (pairs, pins int) {
	_ = agg.store.ScanPrefix(namespace, []byte(keyPendingPair), func(_, _ []byte) error {
		pairs++
		return nil
	})
	_ = agg.store.ScanPrefix(namespace, []byte(keyPendingPin), func(_, _ []byte) error {
		pins++
		return nil
	})
	return
}

// §4.4b: a mempool simpleblock pin blocks immediately — IsBlockedBetween,
// BlockingPeers/BlockedByPeers (the unified filter primitives) and the
// blocks listing all reflect the pending edge.
func TestMempoolBlockTakesEffectImmediately(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)
	if _, err := agg.HandleMempoolPin(blockPin("blkmp1i0", pathCanonicalSimpleblock, idAlice, "meta-alice", idBob, 1, 1793370000)); err != nil {
		t.Fatalf("HandleMempoolPin: %v", err)
	}

	blocked, err := agg.IsBlockedBetween(idAlice, idBob)
	if err != nil || !blocked {
		t.Fatalf("IsBlockedBetween(alice,bob) after mempool block = (%v, %v), want true", blocked, err)
	}
	// The confirmed model is untouched.
	if confirmed, _ := agg.loadActiveEdge(idAlice, idBob); confirmed != nil {
		t.Fatalf("confirmed model wrote the mempool pin: %+v", confirmed)
	}

	blocking, err := agg.BlockingPeers(idAlice)
	if err != nil {
		t.Fatalf("BlockingPeers: %v", err)
	}
	if len(blocking) != 1 || blocking[0].GlobalMetaId != idBob || !blocking[0].Pending || blocking[0].BlockPinId != "blkmp1i0" {
		t.Fatalf("blocking peers = %+v, want [bob pending blkmp1i0]", blocking)
	}
	blockedBy, err := agg.BlockedByPeers(idBob)
	if err != nil {
		t.Fatalf("BlockedByPeers: %v", err)
	}
	if len(blockedBy) != 1 || blockedBy[0].GlobalMetaId != idAlice || !blockedBy[0].Pending {
		t.Fatalf("blockedBy peers = %+v, want [alice pending]", blockedBy)
	}

	list, err := agg.ListBlocks(BlockListParams{GlobalMetaId: idAlice, Type: TypeAll})
	if err != nil {
		t.Fatalf("ListBlocks: %v", err)
	}
	if len(list.List) != 1 || list.List[0].GlobalMetaId != idBob || list.List[0].Direction != DirectionBlocking || !list.List[0].Pending {
		t.Fatalf("blocks list = %+v, want [bob blocking pending]", list.List)
	}

	ids, err := agg.BlockedPeerIds(idAlice)
	if err != nil {
		t.Fatalf("BlockedPeerIds: %v", err)
	}
	if len(ids) != 1 || ids[0] != idBob {
		t.Fatalf("BlockedPeerIds = %v, want [bob]", ids)
	}
}

// Confirmation transitions the overlay into the confirmed model seamlessly;
// re-delivery after confirmation never resurrects the overlay.
func TestBlockConfirmationTransitionsSeamlessly(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)
	pin := blockPin("blkmp2i0", pathCanonicalSimpleblock, idAlice, "meta-alice", idBob, 1, 1793370100)
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatalf("HandleMempoolPin: %v", err)
	}
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}

	edge, err := agg.loadEffectiveBlockEdge(idAlice, idBob)
	if err != nil || edge == nil {
		t.Fatalf("effective edge after confirm = (%v, %v), want non-nil", edge, err)
	}
	if edge.Pending || edge.BlockPinId != "blkmp2i0" {
		t.Fatalf("edge after confirm = %+v, want confirmed blkmp2i0", edge)
	}
	if pairs, pins := overlayCounts(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after confirm = (%d, %d), want (0, 0)", pairs, pins)
	}

	// Idempotent: replay + stale mempool re-delivery keep the overlay empty.
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin replay: %v", err)
	}
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatalf("stale HandleMempoolPin: %v", err)
	}
	if pairs, pins := overlayCounts(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after replay + stale re-delivery = (%d, %d), want (0, 0)", pairs, pins)
	}
}

// A mempool unblock (blockState=-1) lifts a confirmed block immediately;
// expiry of the unblock restores the confirmed block.
func TestMempoolUnblockAndExpiry(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)
	if _, err := agg.HandleBlockPin(blockPin("blkmp3fi0", pathCanonicalSimpleblock, idAlice, "meta-alice", idBob, 1, 1793370200)); err != nil {
		t.Fatalf("HandleBlockPin follow-up: %v", err)
	}
	if _, err := agg.HandleMempoolPin(blockPin("blkmp3ui0", pathCompatPrivateBlock, idAlice, "meta-alice", idBob, -1, 1793370201)); err != nil {
		t.Fatalf("HandleMempoolPin unblock: %v", err)
	}

	blocked, err := agg.IsBlockedBetween(idAlice, idBob)
	if err != nil || blocked {
		t.Fatalf("IsBlockedBetween after pending unblock = (%v, %v), want false", blocked, err)
	}
	blocking, err := agg.BlockingPeers(idAlice)
	if err != nil {
		t.Fatalf("BlockingPeers: %v", err)
	}
	if len(blocking) != 0 {
		t.Fatalf("blocking peers after pending unblock = %+v, want empty", blocking)
	}

	// The unblock never confirms: past the expiry threshold the sweep drops
	// it and the confirmed block shows through again.
	if removed := agg.SweepExpiredMempoolOverlay(time.Now().Add(3*time.Hour), 2*time.Hour); removed != 1 {
		t.Fatalf("sweep removed = %d, want 1", removed)
	}
	blocked, err = agg.IsBlockedBetween(idAlice, idBob)
	if err != nil || !blocked {
		t.Fatalf("IsBlockedBetween after unblock expiry = (%v, %v), want true", blocked, err)
	}
}

// A confirmed unblock consumes the pending tombstone for good.
func TestConfirmedUnblockConsumesOverlay(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)
	if _, err := agg.HandleBlockPin(blockPin("blkmp4fi0", pathCanonicalSimpleblock, idAlice, "meta-alice", idBob, 1, 1793370300)); err != nil {
		t.Fatalf("HandleBlockPin block: %v", err)
	}
	unblock := blockPin("blkmp4ui0", pathCanonicalSimpleblock, idAlice, "meta-alice", idBob, -1, 1793370301)
	if _, err := agg.HandleMempoolPin(unblock); err != nil {
		t.Fatalf("HandleMempoolPin unblock: %v", err)
	}
	if _, err := agg.HandleBlockPin(unblock); err != nil {
		t.Fatalf("HandleBlockPin unblock: %v", err)
	}
	if blocked, err := agg.IsBlockedBetween(idAlice, idBob); err != nil || blocked {
		t.Fatalf("IsBlockedBetween after confirmed unblock = (%v, %v), want false", blocked, err)
	}
	if pairs, pins := overlayCounts(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after confirmed unblock = (%d, %d), want (0, 0)", pairs, pins)
	}
}

// LWW per pair: a newer pending mutation owns the pair; the older pin's
// confirmation consumes only its own ref.
func TestBlockMempoolPairLWW(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)
	first := blockPin("blkmp5ai0", pathCanonicalSimpleblock, idAlice, "meta-alice", idBob, 1, 1793370400)
	second := blockPin("blkmp5bi0", pathCanonicalSimpleblock, idAlice, "meta-alice", idBob, -1, 1793370401)
	if _, err := agg.HandleMempoolPin(first); err != nil {
		t.Fatalf("first mempool block: %v", err)
	}
	if _, err := agg.HandleMempoolPin(second); err != nil {
		t.Fatalf("second mempool unblock: %v", err)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); blocked {
		t.Fatalf("newer pending unblock lost LWW")
	}
	// Older block confirms: pair still owned by the pending unblock → still
	// unblocked; newer unblock confirms → overlay drains.
	if _, err := agg.HandleBlockPin(first); err != nil {
		t.Fatalf("first confirm: %v", err)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); blocked {
		t.Fatalf("pending unblock shadowed by older confirmed block")
	}
	if _, err := agg.HandleBlockPin(second); err != nil {
		t.Fatalf("second confirm: %v", err)
	}
	if pairs, pins := overlayCounts(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after both confirms = (%d, %d), want (0, 0)", pairs, pins)
	}
	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); blocked {
		t.Fatalf("confirmed unblock final state = blocked, want unblocked")
	}
}

// The sweeper self-heals an overlay entry whose pin already folded (missed
// consumption) and expires never-confirmed entries.
func TestBlockSweepSelfHealAndExpiry(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	// Self-heal: apply overlay, then fold confirmed without consumption.
	if err := agg.applyPendingBlock("blkmp6ai0", idAlice, idBob, 1, 1793370500, time.Now()); err != nil {
		t.Fatalf("applyPendingBlock: %v", err)
	}
	if err := agg.saveActiveBlock(&BlockEdge{
		FromGlobalMetaId: idAlice,
		ToGlobalMetaId:   idBob,
		BlockPinId:       "blkmp6ai0",
		BlockedAt:        1793370500,
		BlockState:       1,
	}); err != nil {
		t.Fatalf("saveActiveBlock: %v", err)
	}
	// The real fold path (IngestBlockPin) records the pin pair for replay
	// dedupe; the self-heal probe checks exactly this index.
	if err := agg.recordPinPair("blkmp6ai0", idAlice, idBob); err != nil {
		t.Fatalf("recordPinPair: %v", err)
	}
	if removed := agg.SweepExpiredMempoolOverlay(time.Now(), 2*time.Hour); removed != 1 {
		t.Fatalf("self-heal sweep removed = %d, want 1", removed)
	}
	if pairs, pins := overlayCounts(agg); pairs != 0 || pins != 0 {
		t.Fatalf("overlay after self-heal = (%d, %d), want (0, 0)", pairs, pins)
	}

	// Expiry: a fresh entry survives, an aged one drops.
	if err := agg.applyPendingBlock("blkmp6bi0", idAlice, idBob, 1, 1793370501, time.Now()); err != nil {
		t.Fatalf("applyPendingBlock: %v", err)
	}
	if removed := agg.SweepExpiredMempoolOverlay(time.Now(), 2*time.Hour); removed != 0 {
		t.Fatalf("fresh entry swept: %d, want 0", removed)
	}
	if removed := agg.SweepExpiredMempoolOverlay(time.Now().Add(3*time.Hour), 2*time.Hour); removed != 1 {
		t.Fatalf("aged entry sweep removed = %d, want 1", removed)
	}
}

// Overlay persists across a store reopen (restart), same as the social side.
func TestBlockOverlayPersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewPebbleStore(dir)
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if _, err := agg.HandleMempoolPin(blockPin("blkmp7i0", pathCanonicalSimpleblock, idAlice, "", idBob, 1, 1793370600)); err != nil {
		t.Fatalf("HandleMempoolPin: %v", err)
	}
	_ = store.Close()

	store2 := storage.NewPebbleStore(dir)
	defer func() { _ = store2.Close() }()
	agg2 := &Aggregator{}
	if err := agg2.Init(store2, cache.New(store2)); err != nil {
		t.Fatalf("Init(reopened): %v", err)
	}
	if blocked, err := agg2.IsBlockedBetween(idAlice, idBob); err != nil || !blocked {
		t.Fatalf("IsBlockedBetween after reopen = (%v, %v), want true", blocked, err)
	}
}
