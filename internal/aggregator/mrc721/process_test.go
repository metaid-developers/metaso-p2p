package mrc721

import (
	"fmt"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// The full declaration lifecycle: declare → cancel (active:false) →
// re-declare, three create pins on the fixed path with latest-wins
// semantics at every step.
func TestIngestSellIntentLatestWinsLifecycle(t *testing.T) {
	agg := newTestAggregator(t)

	declare := mrc721SellPin(testSellPinId("declare", 0), 900, 1793868000, sellContent(t, map[string]any{
		"collectionPinId": testCollection,
		"priceHintSats":   30000,
		"note":            "v1",
	}), "")
	if applied, err := agg.ingestSellPin(declare); err != nil || !applied {
		t.Fatalf("declare: applied=%v err=%v", applied, err)
	}

	rec, err := agg.SellIntentInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("load after declare: %v %v", rec, err)
	}
	if !rec.Active || rec.SellPinId != declare.Id || rec.PriceHintSats != 30000 {
		t.Fatalf("declared record: %+v", rec)
	}
	if rec.Height != 900 || rec.TxID != hexOf("declare") || rec.UpdatedAt != 1793868000 {
		t.Fatalf("chain facts: %+v", rec)
	}

	cancel := mrc721SellPin(testSellPinId("cancel", 0), 901, 1793868100, sellContent(t, map[string]any{
		"active": false,
	}), "")
	if applied, err := agg.ingestSellPin(cancel); err != nil || !applied {
		t.Fatalf("cancel: applied=%v err=%v", applied, err)
	}
	rec, _ = agg.SellIntentInfo(validItemPinId)
	if rec.Active || rec.SellPinId != cancel.Id {
		t.Fatalf("cancellation did not win: %+v", rec)
	}

	redeclare := mrc721SellPin(testSellPinId("redeclare", 0), 902, 1793868200, sellContent(t, map[string]any{
		"priceHintSats": 45000,
		"note":          "v3",
	}), "")
	if applied, err := agg.ingestSellPin(redeclare); err != nil || !applied {
		t.Fatalf("re-declare: applied=%v err=%v", applied, err)
	}
	rec, _ = agg.SellIntentInfo(validItemPinId)
	if !rec.Active || rec.SellPinId != redeclare.Id || rec.PriceHintSats != 45000 {
		t.Fatalf("re-declaration did not win: %+v", rec)
	}

	// The recency index must hold exactly one position for the item.
	page, err := agg.ListSellIntents(SellIntentFilter{}, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 1 || len(page.List) != 1 || page.List[0].SellPinId != redeclare.Id {
		t.Fatalf("index not consistent after overwrites: total=%d list=%+v", page.Total, page.List)
	}
}

// Older declarations re-arriving after a newer one (backfill/live overlap)
// must not overwrite the latest-wins record.
func TestIngestSellIntentOlderPinLoses(t *testing.T) {
	agg := newTestAggregator(t)

	newer := mrc721SellPin(testSellPinId("newer", 0), 950, 1793869000, sellContent(t, map[string]any{
		"note": "newer",
	}), "")
	if applied, err := agg.ingestSellPin(newer); err != nil || !applied {
		t.Fatalf("newer: applied=%v err=%v", applied, err)
	}

	older := mrc721SellPin(testSellPinId("older", 0), 940, 1793868900, sellContent(t, map[string]any{
		"active": false,
		"note":   "older",
	}), "")
	applied, err := agg.ingestSellPin(older)
	if err != nil {
		t.Fatalf("older: %v", err)
	}
	if applied {
		t.Fatal("older pin unexpectedly applied")
	}
	rec, _ := agg.SellIntentInfo(validItemPinId)
	if !rec.Active || rec.SellPinId != newer.Id {
		t.Fatalf("older pin overwrote the latest state: %+v", rec)
	}

	// The exact same pin re-applied is a no-op (idempotent replay).
	applied, err = agg.ingestSellPin(newer)
	if err != nil || applied {
		t.Fatalf("re-apply same pin: applied=%v err=%v", applied, err)
	}
}

// Same height and time (pins in one tx): the lexicographically greater pin
// id wins, deterministically.
func TestIngestSellIntentSameHeightTieBreak(t *testing.T) {
	agg := newTestAggregator(t)

	pinA := mrc721SellPin(testSellPinId("same-tx-a", 3), 900, 1793868000, sellContent(t, map[string]any{
		"itemPinId": validItemPinId,
		"note":      "a",
	}), "")
	pinB := mrc721SellPin(testSellPinId("same-tx-b", 7), 900, 1793868000, sellContent(t, map[string]any{
		"itemPinId": validItemPinId,
		"note":      "b",
	}), "")
	if _, err := agg.ingestSellPin(pinA); err != nil {
		t.Fatalf("pinA: %v", err)
	}
	if _, err := agg.ingestSellPin(pinB); err != nil {
		t.Fatalf("pinB: %v", err)
	}
	rec, err := agg.SellIntentInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("record after same-tx tie: %v err=%v", rec, err)
	}
	want := pinA.Id
	if pinB.Id > want {
		want = pinB.Id
	}
	if rec.SellPinId != want {
		t.Fatalf("tie-break winner = %q, want %q", rec.SellPinId, want)
	}
}

// The protocol is create-only: modify/revoke pins on the path are ignored,
// and other paths never touch the index.
func TestIngestSellIntentCreateOnlyAndOtherPathsIgnored(t *testing.T) {
	agg := newTestAggregator(t)

	declare := mrc721SellPin(testSellPinId("declare", 0), 900, 1793868000, sellContent(t, map[string]any{}), "")
	if applied, err := agg.ingestSellPin(declare); err != nil || !applied {
		t.Fatalf("declare: applied=%v err=%v", applied, err)
	}

	modify := mrc721SellPin(testSellPinId("modify", 0), 901, 1793868100, sellContent(t, map[string]any{
		"active": false,
	}), "modify")
	applied, err := agg.ingestSellPin(modify)
	if err != nil || applied {
		t.Fatalf("modify op must be ignored: applied=%v err=%v", applied, err)
	}
	revoke := mrc721SellPin(testSellPinId("revoke", 0), 902, 1793868200, sellContent(t, map[string]any{
		"active": false,
	}), "revoke")
	applied, err = agg.ingestSellPin(revoke)
	if err != nil || applied {
		t.Fatalf("revoke op must be ignored: applied=%v err=%v", applied, err)
	}

	otherPath := mrc721SellPin(testSellPinId("other", 0), 903, 1793868300, sellContent(t, map[string]any{
		"active": false,
	}), "")
	otherPath.Path = "/protocols/simplebuzz"
	applied, err = agg.ingestSellPin(otherPath)
	if err != nil || applied {
		t.Fatalf("other path must be ignored: applied=%v err=%v", applied, err)
	}

	rec, _ := agg.SellIntentInfo(validItemPinId)
	if !rec.Active {
		t.Fatalf("ignored ops changed the record: %+v", rec)
	}
}

// Unparseable or schema-invalid payloads are logged and skipped, never
// fail the pipeline, and never produce records.
func TestIngestSellIntentInvalidPayloadSkipped(t *testing.T) {
	agg := newTestAggregator(t)

	invalids := []*aggregator.PinInscription{
		mrc721SellPin(testSellPinId("empty", 0), 900, 1793868000, nil, ""),
		mrc721SellPin(testSellPinId("garbage", 0), 900, 1793868000, []byte("not json"), ""),
		mrc721SellPin(testSellPinId("no-item", 0), 900, 1793868000, sellContent(t, map[string]any{
			"itemPinId": "not-a-pin-id",
		}), ""),
		mrc721SellPin(testSellPinId("no-active", 0), 900, 1793868000, []byte(fmt.Sprintf(`{"itemPinId":%q,"sellerAddress":%q}`, validItemPinId, testSeller1)), ""),
	}
	for _, pin := range invalids {
		applied, err := agg.ingestSellPin(pin)
		if err != nil {
			t.Fatalf("invalid payload %s must not error: %v", pin.Id, err)
		}
		if applied {
			t.Fatalf("invalid payload %s must not apply", pin.Id)
		}
	}
	page, err := agg.ListSellIntents(SellIntentFilter{}, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("invalid payloads produced records: %d", page.Total)
	}
}

// HandleMempoolPin never mutates the confirmed latest-wins record.
func TestMempoolPinsDoNotMutate(t *testing.T) {
	agg := newTestAggregator(t)

	if _, err := agg.HandleMempoolPin(mrc721SellPin(testSellPinId("mempool", 0), 0, 1793868000, sellContent(t, map[string]any{
		"active": true,
	}), "")); err != nil {
		t.Fatalf("mempool handle: %v", err)
	}
	page, err := agg.ListSellIntents(SellIntentFilter{}, "", 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if page.Total != 0 {
		t.Fatalf("mempool pin leaked into the confirmed index: %d", page.Total)
	}

	// Confirmed ingestion through the aggregator interface works too.
	declare := mrc721SellPin(testSellPinId("confirmed", 0), 900, 1793868000, sellContent(t, map[string]any{}), "")
	if _, err := agg.HandleBlockPin(declare); err != nil {
		t.Fatalf("block pin: %v", err)
	}
	page, _ = agg.ListSellIntents(SellIntentFilter{}, "", 100)
	if page.Total != 1 {
		t.Fatalf("confirmed pin not indexed: %d", page.Total)
	}
}

// CamelCase and snake payloads declare the same item and overwrite each
// other under one key.
func TestIngestSellIntentCaseFormTolerance(t *testing.T) {
	agg := newTestAggregator(t)

	camel := mrc721SellPin(testSellPinId("camel", 0), 900, 1793868000, []byte(`{"active":true,"itemPinId":"`+validItemPinId+`","sellerAddress":"`+testSeller1+`","collectionName":"pixel-cats"}`), "")
	if applied, err := agg.ingestSellPin(camel); err != nil || !applied {
		t.Fatalf("camel: applied=%v err=%v", applied, err)
	}

	// Lowercase-duplicate spelling of the same item, declared later: wins.
	snake := mrc721SellPin(testSellPinId("snake", 0), 901, 1793868100, []byte(`{"active":false,"itempinid":"`+validItemPinId+`","selleraddress":"`+testSeller1+`"}`), "")
	if applied, err := agg.ingestSellPin(snake); err != nil || !applied {
		t.Fatalf("snake: applied=%v err=%v", applied, err)
	}
	rec, _ := agg.SellIntentInfo(validItemPinId)
	if rec.Active || rec.SellPinId != snake.Id {
		t.Fatalf("snake-case declaration did not win: %+v", rec)
	}
}
