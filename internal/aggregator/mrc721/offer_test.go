package mrc721

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Passive-offer ingest tests (R-E-1): create-only, latest-pin-wins per
// itemPinId, cancellation rows kept, unknown-field tolerance, and the
// offerTx verbatim-storage guarantee.

const testOfferTx = "eyJ0eCI6ImNvbXBvc2VyLXNpZ25lZCJ9AAECAw=="

// mrc721OfferPin builds a confirmed-chain pin inscription for the offer
// path; the creator MetaId doubles as the row's sellerMetaId.
func mrc721OfferPin(id string, height int64, timestamp int64, contentBody []byte, operation string) *aggregator.PinInscription {
	if operation == "" {
		operation = "create"
	}
	return &aggregator.PinInscription{
		Id:            id,
		Path:          PathOffer,
		Operation:     operation,
		ContentBody:   contentBody,
		ChainName:     "mvc",
		GenesisHeight: height,
		Timestamp:     timestamp,
		CreateAddress: testSeller1,
		MetaId:        testSeller1,
	}
}

// offerContent builds one offer payload; sensible protocol defaults keep the
// individual tests focused on the fields they care about.
func offerContent(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	if _, ok := fields["itemPinId"]; !ok {
		fields["itemPinId"] = validItemPinId
	}
	if _, ok := fields["active"]; !ok {
		fields["active"] = true
	}
	if _, ok := fields["priceSats"]; !ok {
		fields["priceSats"] = 30000
	}
	if _, ok := fields["offerTx"]; !ok {
		fields["offerTx"] = testOfferTx
	}
	if _, ok := fields["createdAt"]; !ok {
		fields["createdAt"] = 1794000000
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	return raw
}

func offerRecordCount(t *testing.T, agg *Aggregator) int {
	t.Helper()
	page, err := agg.ListOffers(OfferFilter{}, "", 100)
	if err != nil {
		t.Fatalf("list offers: %v", err)
	}
	return page.Total
}

func ingestOffer(t *testing.T, agg *Aggregator, pin *aggregator.PinInscription) {
	t.Helper()
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin offer %s: %v", pin.Id, err)
	}
}

func TestIngestOfferCreateOnly(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)

	ingestOffer(t, agg, mrc721OfferPin(testSellPinId("offer-1", 0), 100, 1794000000, offerContent(t, map[string]any{}), ""))

	// A modify op on the offer path carries no declaration semantics.
	modify := mrc721OfferPin(testSellPinId("offer-modify", 0), 101, 1794000100, offerContent(t, map[string]any{"priceSats": 99999}), "modify")
	ingestOffer(t, agg, modify)

	rec, err := agg.OfferInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("offer info: %v %v", rec, err)
	}
	if rec.PriceSats != 30000 || rec.OfferPinId != testSellPinId("offer-1", 0) {
		t.Fatalf("modify pin must not change the record: %+v", rec)
	}
	if offerRecordCount(t, agg) != 1 {
		t.Fatalf("modify pin must not add rows")
	}
}

func TestIngestOfferLatestWinsByChainOrder(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)

	first := testSellPinId("offer-first", 0)
	second := testSellPinId("offer-second", 0)
	ingestOffer(t, agg, mrc721OfferPin(first, 100, 1794000000, offerContent(t, map[string]any{"priceSats": 30000}), ""))
	ingestOffer(t, agg, mrc721OfferPin(second, 101, 1794000100, offerContent(t, map[string]any{"priceSats": 25000}), ""))

	rec, err := agg.OfferInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("offer info: %v %v", rec, err)
	}
	if rec.OfferPinId != second || rec.PriceSats != 25000 {
		t.Fatalf("latest pin must win: %+v", rec)
	}
	if offerRecordCount(t, agg) != 1 {
		t.Fatalf("re-price must stay one row per item")
	}

	// The superseded pin re-arriving (replay overlap) must not regress the
	// record or rewrite it.
	before := rec.Seq
	ingestOffer(t, agg, mrc721OfferPin(first, 100, 1794000000, offerContent(t, map[string]any{"priceSats": 30000}), ""))
	ingestOffer(t, agg, mrc721OfferPin(second, 101, 1794000100, offerContent(t, map[string]any{"priceSats": 25000}), ""))
	after, _ := agg.OfferInfo(validItemPinId)
	if after.OfferPinId != second || after.Seq != before {
		t.Fatalf("older/idempotent re-apply must be a no-op: %+v", after)
	}
}

func TestIngestOfferCancelRowKept(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)

	offerPin := testSellPinId("offer-cancelled", 0)
	cancelPin := testSellPinId("offer-cancel-pin", 0)
	ingestOffer(t, agg, mrc721OfferPin(offerPin, 100, 1794000000, offerContent(t, map[string]any{}), ""))
	ingestOffer(t, agg, mrc721OfferPin(cancelPin, 101, 1794000100, offerContent(t, map[string]any{"active": false}), ""))

	// The cancellation is the latest declaration and stays queryable.
	rec, err := agg.OfferInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("offer info after cancel: %v %v", rec, err)
	}
	if rec.Active || rec.OfferPinId != cancelPin {
		t.Fatalf("cancellation must win and be kept: %+v", rec)
	}
	if offerRecordCount(t, agg) != 1 {
		t.Fatalf("cancel must overwrite, not append")
	}

	activeTrue, activeFalse := true, false
	pageActive, err := agg.ListOffers(OfferFilter{Active: &activeTrue}, "", 100)
	if err != nil || pageActive.Total != 0 {
		t.Fatalf("cancelled offer must leave the active list: %+v %v", pageActive, err)
	}
	pageCancelled, err := agg.ListOffers(OfferFilter{Active: &activeFalse}, "", 100)
	if err != nil || pageCancelled.Total != 1 {
		t.Fatalf("cancelled offer must stay queryable: %+v %v", pageCancelled, err)
	}
}

func TestIngestOfferUnknownFieldsTolerated(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)

	ingestOffer(t, agg, mrc721OfferPin(testSellPinId("offer-extra", 0), 100, 1794000000, offerContent(t, map[string]any{
		"kind":    "mrc721-offer",
		"version": 1,
		"note":    "local bookkeeping",
		"nested":  map[string]any{"unused": true},
	}), ""))

	rec, err := agg.OfferInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("unknown fields must not reject the pin: %v %v", rec, err)
	}
	if rec.PriceSats != 30000 || !rec.Active {
		t.Fatalf("known fields must still parse: %+v", rec)
	}
}

func TestIngestOfferTxVerbatim(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)

	// Whitespace and +/ characters survive exactly: the client's signature
	// verification depends on byte identity.
	verbatim := "  ABC+/9=  "
	ingestOffer(t, agg, mrc721OfferPin(testSellPinId("offer-verbatim", 0), 100, 1794000000, offerContent(t, map[string]any{"offerTx": verbatim}), ""))

	rec, err := agg.OfferInfo(validItemPinId)
	if err != nil || rec == nil {
		t.Fatalf("offer info: %v %v", rec, err)
	}
	if rec.OfferTx != verbatim {
		t.Fatalf("offerTx must round-trip byte-identical: %q != %q", rec.OfferTx, verbatim)
	}
}

func TestIngestOfferSellerMetaIdFromPin(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)

	// The offer payload carries no seller fields; the seller identity is
	// the pin creator.
	ingestOffer(t, agg, mrc721OfferPin(testSellPinId("offer-seller", 0), 100, 1794000000, offerContent(t, map[string]any{}), ""))

	rec, _ := agg.OfferInfo(validItemPinId)
	if rec == nil || rec.SellerMetaId != testSeller1 {
		t.Fatalf("sellerMetaId must come from the pin creator: %+v", rec)
	}
}

func TestIngestOfferMalformedSkipped(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)

	cases := []struct {
		name    string
		content []byte
	}{
		{"missing offerTx", offerContent(t, map[string]any{"offerTx": ""})},
		{"missing priceSats", []byte(`{"itemPinId":"` + validItemPinId + `","active":true,"offerTx":"abc"}`)},
		{"bad itemPinId", offerContent(t, map[string]any{"itemPinId": "not-a-pin-id"})},
		{"not json", []byte("hello world")},
	}
	for _, tc := range cases {
		ingestOffer(t, agg, mrc721OfferPin(testSellPinId("offer-bad-"+tc.name, 0), 100, 1794000000, tc.content, ""))
	}
	if offerRecordCount(t, agg) != 0 {
		t.Fatalf("malformed payloads must be skipped, not indexed")
	}
}

func TestIngestOfferFlagOff(t *testing.T) {
	agg := newTestAggregator(t) // default: flag off

	ingestOffer(t, agg, mrc721OfferPin(testSellPinId("offer-gated", 0), 100, 1794000000, offerContent(t, map[string]any{}), ""))

	if rec, _ := agg.OfferInfo(validItemPinId); rec != nil {
		t.Fatalf("offer ingest must be gated by the flag: %+v", rec)
	}

	// Routes stay unmounted while the gate is off.
	router := newTestRouter(agg)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/mrc721/offers", nil)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("offer routes must be unmounted when disabled: %d", rec.Code)
	}
}
