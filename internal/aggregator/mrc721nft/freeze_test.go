package mrc721nft

import (
	"testing"
	"time"
)

// Read-freeze regression tests (2026-11-15 production incident): one stuck
// Pebble write held the ingest mutex for 2h11m and, because the read paths
// and the zero-conf gates shared that mutex, every /api/mrc721 read and the
// whole ingest pipeline froze with it. The lock split contract: while the
// ingest mutex is held (simulating the stalled Pebble write), the gates and
// the overlay read paths must still complete — they either read the atomic
// gates or copy an overlay snapshot under overlayMu.RLock and then work
// lock-free.

// mustCompleteWithin runs fn on its own goroutine and fails the test if it
// does not return within the budget. A pre-fix build deadlocks (mutex
// re-entry or lock wait) and trips the budget instead of hanging the suite.
func mustCompleteWithin(t *testing.T, what string, budget time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(budget):
		t.Fatalf("%s blocked — read paths must never wait on the ingest mutex", what)
	}
}

// TestGatesDoNotBlockOnIngestMutex: the zero-conf gate reads are atomic
// loads, not mutex acquisitions.
func TestGatesDoNotBlockOnIngestMutex(t *testing.T) {
	a := newZeroConfAggregator(t)
	a.mu.Lock()
	defer a.mu.Unlock()
	mustCompleteWithin(t, "zeroConfIssuanceActive", 2*time.Second, func() {
		if !a.zeroConfIssuanceActive(viewOptions{}) {
			t.Errorf("issuance gate must read true with the gate on")
		}
	})
	mustCompleteWithin(t, "zeroConfTradeActive", 2*time.Second, func() {
		if !a.zeroConfTradeActive(viewOptions{}) {
			t.Errorf("trade gate must read true with the gate on")
		}
	})
}

// TestOverlayReadsDoNotBlockOnIngestMutex: snapshot copies and the composed
// read projections complete while the ingest mutex is pinned.
func TestOverlayReadsDoNotBlockOnIngestMutex(t *testing.T) {
	a := newZeroConfAggregator(t)
	runPins(t, a, testPin("fr1", "/nft/mrc721/FrAlbum/collection_desc", "create", []byte(`{"name":"FR"}`), creator))
	pin := mempoolPin("fm1", "/nft/mrc721/FrAlbum/x", "create", []byte(`"x"`), creator)
	if _, err := a.HandleMempoolPin(pin); err != nil {
		t.Fatalf("mempool pin: %v", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	mustCompleteWithin(t, "overlaySnapshot", 2*time.Second, func() {
		if a.overlaySnapshot() == nil {
			t.Errorf("nil snapshot")
		}
	})
	mustCompleteWithin(t, "albumViewByPair", 2*time.Second, func() {
		if view := a.albumViewByPair("FrAlbum", creator, viewOptions{}); view == nil || view.collection == nil {
			t.Errorf("album view missing")
		}
	})
	mustCompleteWithin(t, "itemViewByPinId", 2*time.Second, func() {
		// Unknown pin id resolves to nil without touching the ingest mutex.
		_ = a.itemViewByPinId("fm1", viewOptions{})
	})
	mustCompleteWithin(t, "holderItemPinIds", 2*time.Second, func() {
		_, _ = a.holderItemPinIds(creator, "", viewOptions{})
	})
	mustCompleteWithin(t, "overlayTransfersForItem", 2*time.Second, func() {
		_ = a.overlayTransfersForItem("fm1")
	})
	mustCompleteWithin(t, "overlaySearchRows", 2*time.Second, func() {
		_ = a.overlaySearchRows()
	})
}

// TestHandleMempoolPinDoesNotBlockOnIngestMutex: the mempool pin channel is
// memory only (overlayMu) and must not queue behind an ingest write.
func TestHandleMempoolPinDoesNotBlockOnIngestMutex(t *testing.T) {
	a := newZeroConfAggregator(t)
	a.mu.Lock()
	defer a.mu.Unlock()
	mustCompleteWithin(t, "HandleMempoolPin", 2*time.Second, func() {
		if _, err := a.HandleMempoolPin(mempoolPin("fb1", "/nft/mrc721/FrAlbum/nft-b", "create", []byte(`"b"`), creator)); err != nil {
			t.Errorf("mempool pin: %v", err)
		}
	})
}

// TestConfirmationHandoffStillDropsOverlay: the reordered handoff (fold
// under the ingest mutex, drop under overlayMu) keeps its end-to-end
// semantics — the overlay entry of a confirmed tx is gone once the block
// call returns, and the confirmed facts are visible.
func TestConfirmationHandoffStillDropsOverlay(t *testing.T) {
	a := newZeroConfAggregator(t)
	runPins(t, a, testPin("fh1", "/nft/mrc721/FrAlbum/collection_desc", "create", []byte(`{"name":"FR"}`), creator))
	pin := mempoolPin("fh2", "/nft/mrc721/FrAlbum/x", "create", []byte(`"x"`), creator)
	if _, err := a.HandleMempoolPin(pin); err != nil {
		t.Fatalf("mempool pin: %v", err)
	}
	if a.overlayPins["fh2"] == nil {
		t.Fatalf("overlay entry missing before the fold")
	}
	if _, err := a.HandleBlockPin(pin); err != nil {
		t.Fatalf("block pin: %v", err)
	}
	if a.overlayPins["fh2"] != nil {
		t.Fatalf("overlay entry must drop once the confirmed fold completes")
	}
	if item, _ := a.db.GetItem("fh2"); item == nil {
		t.Fatalf("confirmed item missing after the handoff")
	}
}
