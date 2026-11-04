package mrc721

import (
	"encoding/json"
	"testing"
)

type sellListData struct {
	List   []map[string]any `json:"list"`
	Total  int              `json:"total"`
	Cursor string           `json:"cursor"`
}

type collectionListRows struct {
	List   []map[string]any `json:"list"`
	Total  int              `json:"total"`
	Cursor string           `json:"cursor"`
}

func decodeSellList(t *testing.T, env envelope) sellListData {
	t.Helper()
	var data sellListData
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode list data: %v", err)
	}
	return data
}

func decodeCollectionList(t *testing.T, env envelope) collectionListRows {
	t.Helper()
	var data collectionListRows
	if err := json.Unmarshal(env.Data, &data); err != nil {
		t.Fatalf("decode collection list data: %v", err)
	}
	return data
}

func str(m map[string]any, key string) string {
	value, _ := m[key].(string)
	return value
}

func num(m map[string]any, key string) int64 {
	value, _ := m[key].(float64)
	return int64(value)
}

// declareItems ingests one declaration per item pin id at increasing
// heights; returns the sell pin ids newest-first by declaration order.
func declareItems(t *testing.T, agg *Aggregator, itemPinIds []string) {
	t.Helper()
	for i, itemPinId := range itemPinIds {
		pin := mrc721SellPin(testSellPinId("item-"+itemPinId, i), int64(1000+i), int64(1793868000+i), sellContent(t, map[string]any{
			"itemPinId":     itemPinId,
			"sellerAddress": testSeller1,
		}), "")
		if applied, err := agg.ingestSellPin(pin); err != nil || !applied {
			t.Fatalf("declare %s: applied=%v err=%v", itemPinId, applied, err)
		}
	}
}

func TestSellIntentsEndpointEnvelopeAndOrdering(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)

	items := []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + "i0",
	}
	itemB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + "i0"
	itemC := "cccc0000cccc0000cccc0000cccc0000cccc0000cccc0000cccc0000cccc0000" + "i0"
	declareItems(t, agg, []string{items[0], itemB, itemC})

	status, env := doMrc721Request(t, router, "/api/mrc721/sell/intents")
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if env.Code != 1 || env.Message != "ok" {
		t.Fatalf("envelope = %+v", env)
	}
	data := decodeSellList(t, env)
	if data.Total != 3 || len(data.List) != 3 {
		t.Fatalf("list = %+v", data)
	}
	// Newest declaration first.
	if str(data.List[0], "itemPinId") != itemC {
		t.Fatalf("ordering: first row = %v", data.List[0])
	}
	if data.Cursor != "" {
		t.Fatalf("full page must not carry a cursor, got %q", data.Cursor)
	}

	// Row shape: camelCase plus the lowercase duplicate spellings.
	row := data.List[0]
	for _, pair := range [][2]string{
		{"itemPinId", "itempinid"},
		{"sellPinId", "sellpinid"},
		{"sellerAddress", "selleraddress"},
	} {
		if str(row, pair[0]) == "" || str(row, pair[0]) != str(row, pair[1]) {
			t.Fatalf("duplicate key %v mismatch: %v", pair, row)
		}
	}
	if _, ok := row["active"].(bool); !ok {
		t.Fatalf("active must be boolean: %v", row)
	}
}

func TestSellIntentsEndpointFilters(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)

	itemA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + "i0"
	itemB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + "i0"
	declareItems(t, agg, []string{itemA, itemB})

	cancel := mrc721SellPin(testSellPinId("item-"+itemB, 9), 1099, 1793868999, sellContent(t, map[string]any{
		"itemPinId":     itemB,
		"active":        false,
		"sellerAddress": testSeller2,
	}), "")
	if applied, err := agg.ingestSellPin(cancel); err != nil || !applied {
		t.Fatalf("cancel: applied=%v err=%v", applied, err)
	}

	// active=true only returns the live declaration.
	_, env := doMrc721Request(t, router, "/api/mrc721/sell/intents?active=true")
	data := decodeSellList(t, env)
	if data.Total != 1 || str(data.List[0], "itemPinId") != itemA {
		t.Fatalf("active=true: %+v", data)
	}

	// active=false returns the cancelled one.
	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intents?active=false")
	data = decodeSellList(t, env)
	if data.Total != 1 || str(data.List[0], "itemPinId") != itemB {
		t.Fatalf("active=false: %+v", data)
	}

	// sellerAddress filter.
	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intents?sellerAddress="+testSeller2)
	data = decodeSellList(t, env)
	if data.Total != 1 || str(data.List[0], "itemPinId") != itemB {
		t.Fatalf("sellerAddress: %+v", data)
	}

	// itemPinId filter short-circuits.
	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intents?itemPinId="+itemA)
	data = decodeSellList(t, env)
	if data.Total != 1 || len(data.List) != 1 || str(data.List[0], "itemPinId") != itemA {
		t.Fatalf("itemPinId: %+v", data)
	}

	// Unknown item: empty list, zero total, still code=1.
	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intents?itemPinId=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffi0")
	data = decodeSellList(t, env)
	if env.Code != 1 || data.Total != 0 || len(data.List) != 0 {
		t.Fatalf("unknown item: env=%+v data=%+v", env, data)
	}

	// collectionPinId / collectionName filters (declared on the collection
	// record through a dedicated item).
	pin := mrc721SellPin(testSellPinId("coll-item", 0), 1100, 1793869100, sellContent(t, map[string]any{
		"itemPinId":       "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" + "i0",
		"collectionPinId": testCollection,
		"collectionName":  "Pixel-Cats",
	}), "")
	if applied, err := agg.ingestSellPin(pin); err != nil || !applied {
		t.Fatalf("coll item: applied=%v err=%v", applied, err)
	}
	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intents?collectionPinId="+testCollection)
	data = decodeSellList(t, env)
	if data.Total != 1 {
		t.Fatalf("collectionPinId filter: %+v", data)
	}
	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intents?collectionName=pixel-cats")
	data = decodeSellList(t, env)
	if data.Total != 1 {
		t.Fatalf("collectionName filter (case-insensitive): %+v", data)
	}
}

func TestSellIntentsEndpointPagination(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)

	items := make([]string, 5)
	for i := range items {
		items[i] = hexOfItem(i) + "i0"
	}
	declareItems(t, agg, items)

	_, env := doMrc721Request(t, router, "/api/mrc721/sell/intents?size=2")
	data := decodeSellList(t, env)
	if data.Total != 5 || len(data.List) != 2 || data.Cursor == "" {
		t.Fatalf("page 1 = %+v", data)
	}
	seen := map[string]bool{str(data.List[0], "itemPinId"): true, str(data.List[1], "itemPinId"): true}

	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intents?size=2&cursor="+data.Cursor)
	data = decodeSellList(t, env)
	if data.Total != 5 || len(data.List) != 2 {
		t.Fatalf("page 2 = %+v", data)
	}
	for _, row := range data.List {
		seen[str(row, "itemPinId")] = true
	}

	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intents?size=2&cursor="+data.Cursor)
	data = decodeSellList(t, env)
	if len(data.List) != 1 || data.Cursor != "" {
		t.Fatalf("page 3 = %+v", data)
	}
	seen[str(data.List[0], "itemPinId")] = true
	if len(seen) != 5 {
		t.Fatalf("pagination walked %d distinct items, want 5", len(seen))
	}
}

func hexOfItem(i int) string {
	return hexOf("hexOfItem" + string(rune('a'+i)))
}

func TestSellIntentInfoEndpoint(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)

	pin := mrc721SellPin(testSellPinId("info", 0), 1200, 1793869200, sellContent(t, map[string]any{
		"collectionName": "pixel-cats",
		"priceHintSats":  int64(30000),
		"itemOutpoint":   map[string]any{"txid": hexOf("outpoint-tx"), "vout": 0, "satoshis": 1},
		"settlement":     []string{"atomic-swap"},
	}), "")
	if applied, err := agg.ingestSellPin(pin); err != nil || !applied {
		t.Fatalf("declare: applied=%v err=%v", applied, err)
	}

	_, env := doMrc721Request(t, router, "/api/mrc721/sell/intent/info?itemPinId="+validItemPinId)
	if env.Code != 1 || env.Message != "ok" {
		t.Fatalf("envelope = %+v", env)
	}
	var row map[string]any
	if err := json.Unmarshal(env.Data, &row); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if str(row, "itemPinId") != validItemPinId || str(row, "itempinid") != validItemPinId {
		t.Fatalf("row identity: %v", row)
	}
	if str(row, "collectionName") != "pixel-cats" || str(row, "collectionname") != "pixel-cats" {
		t.Fatalf("row collection: %v", row)
	}
	if num(row, "priceHintSats") != 30000 || num(row, "pricehintsats") != 30000 {
		t.Fatalf("row price: %v", row)
	}
	if num(row, "height") != 1200 {
		t.Fatalf("row chain facts: %v", row)
	}
	outpoint, ok := row["itemOutpoint"].(map[string]any)
	if !ok || str(outpoint, "txid") != hexOf("outpoint-tx") {
		t.Fatalf("row outpoint: %v", row["itemOutpoint"])
	}
	settlement, ok := row["settlement"].([]any)
	if !ok || len(settlement) != 1 || settlement[0] != "atomic-swap" {
		t.Fatalf("row settlement: %v", row["settlement"])
	}

	// Miss: structured non-1 code (the client treats it as a null answer).
	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intent/info?itemPinId=ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffi0")
	if env.Code == 1 {
		t.Fatalf("miss must not be code 1: %+v", env)
	}

	// Missing parameter.
	_, env = doMrc721Request(t, router, "/api/mrc721/sell/intent/info")
	if env.Code != 40000 {
		t.Fatalf("missing itemPinId: %+v", env)
	}
}

func TestSellIntentsEndpointInvalidParameters(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)

	cases := []string{
		"/api/mrc721/sell/intents?active=maybe",
		"/api/mrc721/sell/intents?size=abc",
		"/api/mrc721/sell/intents?size=0",
		"/api/mrc721/sell/intents?size=1000",
		"/api/mrc721/sell/intents?cursor=!!!notbase64!!!",
	}
	for _, path := range cases {
		_, env := doMrc721Request(t, router, path)
		if env.Code != 40000 {
			t.Fatalf("%s: expected code 40000, got %+v", path, env)
		}
	}
}
