package mrc721

import (
	"encoding/json"
	"testing"
)

// Passive-offer endpoint tests (R-E-2): the sell-intent envelope ({code,
// message,data} with {list,total,cursor}), filters, pagination, and the
// 40400 info miss.

// fakeItemCollections is a scripted ItemCollectionSource for the join tests.
type fakeItemCollections struct {
	refs map[string][2]string // itemPinId → {collectionPinId, collectionName}
}

func (f *fakeItemCollections) ItemCollectionRef(itemPinId string) (string, string) {
	ref, ok := f.refs[itemPinId]
	if !ok {
		return "", ""
	}
	return ref[0], ref[1]
}

// seedOffers publishes four offers across two sellers; item4 carries a
// cancellation (active:false).
func seedOffers(t *testing.T, agg *Aggregator) {
	t.Helper()
	agg.SetOfferEnabled(true)
	publish := func(seed string, itemPinId string, height int64, priceSats int64, active bool, offerTx string) {
		content := offerContent(t, map[string]any{
			"itemPinId": itemPinId,
			"priceSats": priceSats,
			"active":    active,
			"offerTx":   offerTx,
		})
		ingestOffer(t, agg, mrc721OfferPin(testSellPinId(seed, 0), height, 1794000000+height, content, ""))
	}
	publish("seed-offer-1", validItemPinId, 100, 30000, true, testOfferTx)
	publish("seed-offer-2", hexOf("item-two")+"i0", 101, 25000, true, testOfferTx)
	publish("seed-offer-3", hexOf("item-three")+"i0", 102, 50000, true, testOfferTx)
	publish("seed-offer-4", hexOf("item-four")+"i0", 103, 40000, false, testOfferTx)
}

func decodeOfferList(t *testing.T, raw json.RawMessage) offerListData {
	t.Helper()
	var data offerListData
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatalf("decode offer list: %v\nraw: %s", err, raw)
	}
	return data
}

func decodeOfferRow(t *testing.T, raw json.RawMessage) offerRow {
	t.Helper()
	var row offerRow
	if err := json.Unmarshal(raw, &row); err != nil {
		t.Fatalf("decode offer row: %v\nraw: %s", err, raw)
	}
	return row
}

func TestOffersListDefaultActiveOnly(t *testing.T) {
	agg := newTestAggregator(t)
	seedOffers(t, agg)
	router := newTestRouter(agg)

	_, env := doMrc721Request(t, router, "/api/mrc721/offers")
	if env.Code != 1 {
		t.Fatalf("envelope code: %+v", env)
	}
	data := decodeOfferList(t, env.Data)
	if data.Total != 3 || len(data.List) != 3 || data.Cursor != "" {
		t.Fatalf("default list must serve active offers newest-first: %+v", data)
	}
	// Chain-order newest first: item-three (102), item-two (101), item-one.
	if data.List[0].ItemPinId != hexOf("item-three")+"i0" || data.List[2].ItemPinId != validItemPinId {
		t.Fatalf("list order wrong: %+v", data.List)
	}
	for _, row := range data.List {
		if !row.Active {
			t.Fatalf("cancelled rows must not appear in the default list: %+v", row)
		}
		if row.OfferTx != testOfferTx {
			t.Fatalf("offerTx must round-trip byte-identical: %q", row.OfferTx)
		}
		if row.ChainName != "mvc" || row.Height == 0 || row.TxId == "" || row.CreatedAt == 0 {
			t.Fatalf("chain facts missing: %+v", row)
		}
	}
}

func TestOffersListActiveFalseListsCancellations(t *testing.T) {
	agg := newTestAggregator(t)
	seedOffers(t, agg)
	router := newTestRouter(agg)

	_, env := doMrc721Request(t, router, "/api/mrc721/offers?active=false")
	data := decodeOfferList(t, env.Data)
	if data.Total != 1 || len(data.List) != 1 || data.List[0].Active {
		t.Fatalf("active=false must list the cancellation row: %+v", data)
	}
	if data.List[0].ItemPinId != hexOf("item-four")+"i0" {
		t.Fatalf("wrong cancellation row: %+v", data.List)
	}
}

func TestOffersListFilters(t *testing.T) {
	agg := newTestAggregator(t)
	seedOffers(t, agg)
	// Join source: items 1 and 2 sit in collection A, item 3 in collection B.
	agg.SetItemCollectionSource(&fakeItemCollections{refs: map[string][2]string{
		validItemPinId:             {testCollection, "meta-dogs"},
		hexOf("item-two") + "i0":   {testCollection, "meta-dogs"},
		hexOf("item-three") + "i0": {hexOf("coll-b") + "i0", "pixel-pets"},
	}})
	router := newTestRouter(agg)

	// sellerMetaId filter (the pin creator).
	_, env := doMrc721Request(t, router, "/api/mrc721/offers?sellerMetaId="+testSeller1)
	data := decodeOfferList(t, env.Data)
	if data.Total != 3 {
		t.Fatalf("all seeds share one creator: %+v", data)
	}

	// Unknown creator: empty page, not an error.
	_, env = doMrc721Request(t, router, "/api/mrc721/offers?sellerMetaId="+testSeller2)
	data = decodeOfferList(t, env.Data)
	if env.Code != 1 || data.Total != 0 || len(data.List) != 0 {
		t.Fatalf("unknown seller filter must be an empty page: %+v %+v", env, data)
	}

	// itemPinId filter: exact-item short circuit.
	_, env = doMrc721Request(t, router, "/api/mrc721/offers?itemPinId="+validItemPinId)
	data = decodeOfferList(t, env.Data)
	if data.Total != 1 || len(data.List) != 1 || data.List[0].ItemPinId != validItemPinId {
		t.Fatalf("itemPinId filter wrong: %+v", data)
	}

	// collectionPinId filter through the item → collection join.
	_, env = doMrc721Request(t, router, "/api/mrc721/offers?collectionPinId="+testCollection)
	data = decodeOfferList(t, env.Data)
	if data.Total != 2 || len(data.List) != 2 {
		t.Fatalf("collectionPinId filter wrong: %+v", data)
	}
	if data.List[0].ItemPinId != hexOf("item-two")+"i0" || data.List[1].ItemPinId != validItemPinId {
		t.Fatalf("collection filter order wrong: %+v", data.List)
	}
	for _, row := range data.List {
		if row.CollectionPinId != testCollection || row.CollectionName != "meta-dogs" {
			t.Fatalf("join fields missing: %+v", row)
		}
	}
}

func TestOffersListWithoutCollectionSource(t *testing.T) {
	agg := newTestAggregator(t)
	seedOffers(t, agg)
	router := newTestRouter(agg)

	// No join source wired: rows carry no collection identity and the
	// collectionPinId filter matches nothing.
	_, env := doMrc721Request(t, router, "/api/mrc721/offers")
	data := decodeOfferList(t, env.Data)
	if data.Total != 3 || data.List[0].CollectionPinId != "" {
		t.Fatalf("rows must omit collection fields without a join source: %+v", data.List[0])
	}
	_, env = doMrc721Request(t, router, "/api/mrc721/offers?collectionPinId="+testCollection)
	data = decodeOfferList(t, env.Data)
	if env.Code != 1 || data.Total != 0 {
		t.Fatalf("collection filter without a join source must be an empty page: %+v %+v", env, data)
	}
}

func TestOffersListPagination(t *testing.T) {
	agg := newTestAggregator(t)
	seedOffers(t, agg)
	router := newTestRouter(agg)

	_, env := doMrc721Request(t, router, "/api/mrc721/offers?size=2")
	data := decodeOfferList(t, env.Data)
	if data.Total != 3 || len(data.List) != 2 || data.Cursor == "" {
		t.Fatalf("first page wrong: %+v", data)
	}

	_, env = doMrc721Request(t, router, "/api/mrc721/offers?size=2&cursor="+data.Cursor)
	data2 := decodeOfferList(t, env.Data)
	if data2.Total != 3 || len(data2.List) != 1 || data2.Cursor != "" {
		t.Fatalf("second page wrong: %+v", data2)
	}
	// Pages must not overlap.
	if data2.List[0].ItemPinId == data.List[0].ItemPinId || data2.List[0].ItemPinId == data.List[1].ItemPinId {
		t.Fatalf("pages overlap: %+v vs %+v", data.List, data2.List)
	}

	// A non-active row never appears in the default pages.
	for _, row := range append(data.List, data2.List...) {
		if row.ItemPinId == hexOf("item-four")+"i0" {
			t.Fatalf("cancelled row leaked into pagination: %+v", row)
		}
	}
}

func TestOffersInvalidActiveParam(t *testing.T) {
	agg := newTestAggregator(t)
	seedOffers(t, agg)
	router := newTestRouter(agg)

	_, env := doMrc721Request(t, router, "/api/mrc721/offers?active=maybe")
	if env.Code != 40000 {
		t.Fatalf("invalid active param must be 40000: %+v", env)
	}
}

func TestOfferInfoFound(t *testing.T) {
	agg := newTestAggregator(t)
	seedOffers(t, agg)
	agg.SetItemCollectionSource(&fakeItemCollections{refs: map[string][2]string{
		validItemPinId: {testCollection, "meta-dogs"},
	}})
	router := newTestRouter(agg)

	_, env := doMrc721Request(t, router, "/api/mrc721/offer/info?itemPinId="+validItemPinId)
	if env.Code != 1 {
		t.Fatalf("envelope code: %+v", env)
	}
	row := decodeOfferRow(t, env.Data)
	if row.ItemPinId != validItemPinId || row.Itempinid != validItemPinId {
		t.Fatalf("row item identity wrong: %+v", row)
	}
	if row.OfferTx != testOfferTx || row.Offertx != testOfferTx {
		t.Fatalf("offerTx round-trip broken: %+v", row)
	}
	if row.OfferPinId != testSellPinId("seed-offer-1", 0) || !row.Active || row.PriceSats != 30000 {
		t.Fatalf("row fields wrong: %+v", row)
	}
	if row.CollectionPinId != testCollection || row.CollectionName != "meta-dogs" {
		t.Fatalf("join fields wrong: %+v", row)
	}
	if row.SellerMetaId != testSeller1 || row.ChainName != "mvc" || row.Height != 100 || row.TxId == "" || row.CreatedAt == 0 {
		t.Fatalf("chain facts / seller wrong: %+v", row)
	}
}

func TestOfferInfoReturnsLatestIncludingCancellation(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)
	ingestOffer(t, agg, mrc721OfferPin(testSellPinId("info-then-cancel", 0), 100, 1794000000, offerContent(t, map[string]any{}), ""))
	ingestOffer(t, agg, mrc721OfferPin(testSellPinId("info-cancel-pin", 0), 101, 1794000100, offerContent(t, map[string]any{"active": false}), ""))

	router := newTestRouter(agg)
	_, env := doMrc721Request(t, router, "/api/mrc721/offer/info?itemPinId="+validItemPinId)
	row := decodeOfferRow(t, env.Data)
	if row.Active || row.OfferPinId != testSellPinId("info-cancel-pin", 0) {
		t.Fatalf("info must return the latest (cancelled) declaration: %+v", row)
	}
}

func TestOfferInfoMiss40400(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetOfferEnabled(true)
	router := newTestRouter(agg)

	_, env := doMrc721Request(t, router, "/api/mrc721/offer/info?itemPinId="+hexOf("missing")+"i0")
	if env.Code != 40400 {
		t.Fatalf("miss must be 40400: %+v", env)
	}

	// Missing parameter: invalid-parameter code.
	_, env = doMrc721Request(t, router, "/api/mrc721/offer/info")
	if env.Code != 40000 {
		t.Fatalf("missing itemPinId must be 40000: %+v", env)
	}
}
