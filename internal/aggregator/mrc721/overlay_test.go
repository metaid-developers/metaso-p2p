package mrc721

import (
	"testing"
	"time"
)

// Zero-conf declaration overlay tests (R-F-4): a mempool cancellation hides
// the intent immediately; evicting that cancellation restores the confirmed
// intent on the next full round; confirmation folds the cancel in and drops
// the overlay.

func newZeroConfSellAggregator(t *testing.T) *Aggregator {
	t.Helper()
	agg := newTestAggregator(t)
	agg.SetZeroConfTrade(true)
	return agg
}

func TestOverlaySellCancelHidesImmediately(t *testing.T) {
	agg := newZeroConfSellAggregator(t)

	// Confirmed declaration: active.
	sellPin := mrc721SellPin(testSellPinId("sell-live", 0), 100, 1794000000, sellContent(t, map[string]any{"active": true}), "")
	if _, err := agg.HandleBlockPin(sellPin); err != nil {
		t.Fatalf("confirmed sell pin: %v", err)
	}

	// Mempool cancellation: takes effect immediately (R-F-4 acceptance).
	cancelPin := mrc721SellPin(testSellPinId("sell-cancel", 0), -1, 1794000100, sellContent(t, map[string]any{"active": false}), "")
	if _, err := agg.HandleMempoolPin(cancelPin); err != nil {
		t.Fatalf("overlay cancel pin: %v", err)
	}

	rec, err := agg.SellIntentInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("info miss: %v", rec)
	}
	if rec.Active {
		t.Fatalf("mempool cancellation must hide the intent immediately")
	}
	if !rec.Overlay {
		t.Fatalf("the winning row must carry the overlay marker")
	}

	page, err := agg.ListSellIntents(SellIntentFilter{}, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 1 || page.List[0].Active {
		t.Fatalf("unfiltered list must show the cancelled overlay row: %+v", page.List)
	}
	active := true
	page, err = agg.ListSellIntents(SellIntentFilter{Active: &active}, "", 100)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("active-filtered list must hide the cancelled intent: %d rows", page.Total)
	}
}

func TestOverlayEvictionRestoresConfirmedIntent(t *testing.T) {
	agg := newZeroConfSellAggregator(t)

	sellPin := mrc721SellPin(testSellPinId("sell-live2", 0), 100, 1794000000, sellContent(t, map[string]any{"active": true}), "")
	if _, err := agg.HandleBlockPin(sellPin); err != nil {
		t.Fatalf("confirmed sell pin: %v", err)
	}
	cancelPin := mrc721SellPin(testSellPinId("sell-cancel2", 0), -1, 1794000100, sellContent(t, map[string]any{"active": false}), "")
	if _, err := agg.HandleMempoolPin(cancelPin); err != nil {
		t.Fatalf("overlay cancel pin: %v", err)
	}

	// The cancellation tx leaves the mempool without confirming: the next
	// full round evicts it and the confirmed intent shows again (R-F-4
	// acceptance — no recovery path needed).
	agg.ReconcileMempoolRound("mvc", []string{testSellPinId("sell-live2", 0)[:64]})

	rec, err := agg.SellIntentInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("info miss: %v", rec)
	}
	if !rec.Active || rec.Overlay {
		t.Fatalf("eviction must restore the confirmed intent: %+v", rec)
	}
}

func TestOverlaySellConfirmationHandoff(t *testing.T) {
	agg := newZeroConfSellAggregator(t)

	sellPin := mrc721SellPin(testSellPinId("sell-live3", 0), 100, 1794000000, sellContent(t, map[string]any{"active": true}), "")
	if _, err := agg.HandleBlockPin(sellPin); err != nil {
		t.Fatalf("confirmed sell pin: %v", err)
	}
	cancelPin := mrc721SellPin(testSellPinId("sell-cancel3", 0), -1, 1794000100, sellContent(t, map[string]any{"active": false}), "")
	if _, err := agg.HandleMempoolPin(cancelPin); err != nil {
		t.Fatalf("overlay cancel pin: %v", err)
	}

	// The cancellation confirms: folded on the base path first, overlay
	// dropped in the same critical section — no gap, no duplicate.
	confirmed := *cancelPin
	confirmed.GenesisHeight = 101
	if _, err := agg.HandleBlockPin(&confirmed); err != nil {
		t.Fatalf("confirm cancel: %v", err)
	}
	if len(agg.overlayDecls) != 0 {
		t.Fatalf("confirmation must drop the overlay entry: %d left", len(agg.overlayDecls))
	}
	rec, err := agg.SellIntentInfo(validItemPinId)
	if err != nil || rec == nil || rec.Active {
		t.Fatalf("confirmed cancellation must stand: %+v", rec)
	}
	if rec.Overlay {
		t.Fatalf("confirmed row must not carry the overlay marker")
	}
}

func TestOverlaySellOnlyDeclaration(t *testing.T) {
	agg := newZeroConfSellAggregator(t)

	// First-ever declaration arrives in the mempool: served immediately.
	pin := mrc721SellPin(testSellPinId("sell-fresh", 0), -1, 1794000200, sellContent(t, map[string]any{"active": true}), "")
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatalf("overlay sell pin: %v", err)
	}
	rec, err := agg.SellIntentInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("overlay-only intent miss: %v", rec)
	}
	if !rec.Active || !rec.Overlay {
		t.Fatalf("overlay-only declaration wrong: %+v", rec)
	}
	if rec.Height != -1 {
		t.Fatalf("overlay row height must be -1: %d", rec.Height)
	}

	// A newer overlay re-declaration wins over the first.
	time.Sleep(1100 * time.Millisecond)
	renewed := mrc721SellPin(testSellPinId("sell-renew", 0), -1, 1794000300, sellContent(t, map[string]any{"active": false}), "")
	if _, err := agg.HandleMempoolPin(renewed); err != nil {
		t.Fatalf("overlay re-declaration: %v", err)
	}
	rec, _ = agg.SellIntentInfo(validItemPinId)
	if rec.Active {
		t.Fatalf("latest overlay pin must win")
	}
}

func TestOverlayTradeGateOff(t *testing.T) {
	agg := newTestAggregator(t) // gate off (zero value)

	pin := mrc721SellPin(testSellPinId("sell-gateoff", 0), -1, 1794000000, sellContent(t, map[string]any{"active": true}), "")
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatalf("overlay pin with gate off: %v", err)
	}
	if len(agg.overlayDecls) != 0 {
		t.Fatalf("trade gate off must not record overlay declarations")
	}
	rec, err := agg.SellIntentInfo(validItemPinId)
	if err != nil || rec != nil {
		t.Fatalf("gate-off view must be confirmed-only: %+v", rec)
	}
}

func TestOverlayOfferLifecycle(t *testing.T) {
	agg := newZeroConfSellAggregator(t)
	agg.SetOfferEnabled(true)

	// Overlay-only offer served immediately with confirmed:false.
	pin := mrc721OfferPin(testSellPinId("offer-fresh", 0), -1, 1794000000, offerContent(t, map[string]any{}), "")
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatalf("overlay offer pin: %v", err)
	}
	rec, err := agg.OfferInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("overlay-only offer miss: %v", rec)
	}
	if !rec.Active || !rec.Overlay {
		t.Fatalf("overlay-only offer wrong: %+v", rec)
	}
	page, err := agg.ListOffers(OfferFilter{}, "", 100)
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	if page.Total != 1 || !page.List[0].Overlay {
		t.Fatalf("overlay offer must lead the list: %+v", page.List)
	}

	// Confirmation folds it in and drops the overlay.
	confirmed := *pin
	confirmed.GenesisHeight = 102
	if _, err := agg.HandleBlockPin(&confirmed); err != nil {
		t.Fatalf("confirm offer: %v", err)
	}
	if len(agg.overlayDecls) != 0 {
		t.Fatalf("confirmation must drop the offer overlay entry")
	}
	rec, _ = agg.OfferInfo(validItemPinId)
	if rec == nil || !rec.Active || rec.Overlay {
		t.Fatalf("confirmed offer must stand without the overlay marker: %+v", rec)
	}
}
