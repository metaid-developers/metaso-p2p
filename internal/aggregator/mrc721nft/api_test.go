package mrc721nft

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// Query-API tests: the wire contract must be byte-compatible with man-p2p's
// /api/mrc721 surface — envelope codes (1 ok / 404 param / 100 no-data),
// the `cousor|cursor` pagination precedence with size 20/200 clamps, the
// dual-spelling DTOs and the pending-channel shapes.

type apiEnvelopeProbe struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func newAPIRouter(t *testing.T, a *Aggregator) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	a.RegisterRoutes(router.Group("/api"))
	return router
}

func doGet(t *testing.T, router *gin.Engine, path string) (int, apiEnvelopeProbe, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(rec, req)
	var env apiEnvelopeProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response %s: %v\nraw: %s", path, err, rec.Body.String())
	}
	var data map[string]any
	if len(env.Data) > 0 && env.Data[0] == '{' {
		if err := json.Unmarshal(env.Data, &data); err != nil {
			t.Fatalf("decode data %s: %v", path, err)
		}
	}
	return rec.Code, env, data
}

func decodeRows(t *testing.T, data map[string]any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(data["list"])
	if err != nil {
		t.Fatalf("marshal list: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return rows
}

// seedAlbum registers a collection and mints three items (numbers 1-3) via
// the live ingest path, so the API tests run against realistic records.
// Under the issuer-only revision every indexed item is minted by the
// issuer; the second and third items are then transferred to other/holder
// so holder-view tests still see a mixed inventory.
func seedAlbum(t *testing.T, a *Aggregator, album string) {
	t.Helper()
	runPins(t, a,
		testPin(album+"-c", "/nft/mrc721/"+album+"/collection_desc", "create",
			[]byte(`{"name":"`+album+` Name","totalSupply":100,"royaltyRate":5}`), creator),
		testPin(album+"-i1", "/nft/mrc721/"+album+"/one", "create", []byte(`"1"`), creator),
		testPin(album+"-i2", "/nft/mrc721/"+album+"/two", "create", []byte(`"2"`), creator),
		testPin(album+"-i3", "/nft/mrc721/"+album+"/three", "create", []byte(`"3"`), creator),
	)
	for item, to := range map[string]string{album + "-i2": other, album + "-i3": holder} {
		transferMap, pinIdByOutput := transferFor(item, to, creator, "moved-"+item+":0", 546, false)
		a.applyTransferHook("mvc", 150, transferMap, pinIdByOutput, 1700000001)
	}
}

func TestCollectionPageListAPI(t *testing.T) {
	a := newTestAggregator(t)
	seedAlbum(t, a, "Zeta")
	seedAlbum(t, a, "Alpha")
	router := newAPIRouter(t, a)

	code, env, data := doGet(t, router, "/api/mrc721/collection/pageList")
	if code != http.StatusOK || env.Code != 1 || env.Message != "ok" {
		t.Fatalf("envelope wrong: code=%d env=%+v", code, env)
	}
	if got := int(data["total"].(float64)); got != 2 {
		t.Fatalf("total wrong: %d", got)
	}
	rows := decodeRows(t, data)
	if len(rows) != 2 || rows[0]["collectionname"] != "Alpha" || rows[1]["collectionname"] != "Zeta" {
		t.Fatalf("list must be name-keyed order: %+v", rows)
	}
	// Dual spellings must carry identical values.
	if rows[0]["collectionName"] != rows[0]["collectionname"] || rows[0]["totalSupply"] != rows[0]["totalsupply"] {
		t.Fatalf("alias spellings diverge: %+v", rows[0])
	}

	// Offset pagination via cursor; the legacy misspelling wins.
	_, env2, data2 := doGet(t, router, "/api/mrc721/collection/pageList?cousor=1&size=1")
	if int(data2["total"].(float64)) != 2 || len(decodeRows(t, data2)) != 1 {
		t.Fatalf("cousor pagination wrong: %+v", data2)
	}
	_ = env2
	_, _, data3 := doGet(t, router, "/api/mrc721/collection/pageList?cursor=1&size=1")
	rows3 := decodeRows(t, data3)
	if len(rows3) != 1 || rows3[0]["collectionname"] != "Zeta" {
		t.Fatalf("cursor pagination wrong: %+v", rows3)
	}
	// Garbage cursor falls back to 0 (same as man-p2p).
	_, _, data4 := doGet(t, router, "/api/mrc721/collection/pageList?cursor=abc")
	if int(data4["total"].(float64)) != 2 || len(decodeRows(t, data4)) != 2 {
		t.Fatalf("garbage cursor must reset to 0: %+v", data4)
	}
}

func TestCollectionInfoAPI(t *testing.T) {
	a := newTestAggregator(t)
	seedAlbum(t, a, "InfoAlbum")
	router := newAPIRouter(t, a)

	code, env, data := doGet(t, router, "/api/mrc721/collection/info?name=InfoAlbum")
	if code != http.StatusOK || env.Code != 1 {
		t.Fatalf("by name wrong: code=%d env=%+v", code, env)
	}
	if data["pinid"] != "InfoAlbum-c" || data["name"] != "InfoAlbum Name" || data["createAddress"] != creator {
		t.Fatalf("dto fields wrong: %+v", data)
	}

	_, envByPin, dataByPin := doGet(t, router, "/api/mrc721/collection/info?pinId=InfoAlbum-c")
	if envByPin.Code != 1 || dataByPin["collectionname"] != "InfoAlbum" {
		t.Fatalf("by pinId wrong: %+v", dataByPin)
	}

	// Both params: name wins (same as man-p2p).
	_, envBoth, _ := doGet(t, router, "/api/mrc721/collection/info?name=InfoAlbum&pinId=nonexistent")
	if envBoth.Code != 1 {
		t.Fatalf("name should win over pinId: %+v", envBoth)
	}

	// Neither: parameter error, data null.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/mrc721/collection/info", nil))
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if raw["code"].(float64) != 404 || raw["message"] != "Parameter error." {
		t.Fatalf("parameter error shape wrong: %v", raw)
	}
	if v, ok := raw["data"]; !ok || v != nil {
		t.Fatalf("parameter error must carry data:null, got %v (present=%v)", v, ok)
	}

	// Unknown: no-data error, data [].
	_, envMissing, _ := doGet(t, router, "/api/mrc721/collection/info?name=Ghost")
	if envMissing.Code != 100 || envMissing.Message != "no data found." {
		t.Fatalf("no-data shape wrong: %+v", envMissing)
	}
	if string(envMissing.Data) != "[]" {
		t.Fatalf("no-data must carry data:[], got %s", envMissing.Data)
	}
}

func TestCollectionItemsPageListAPI(t *testing.T) {
	a := newTestAggregator(t)
	seedAlbum(t, a, "ItmAlbum")
	router := newAPIRouter(t, a)

	// By collectionid (legacy pinid semantics).
	_, env, data := doGet(t, router, "/api/mrc721/collection/items/pageList?pinid=ItmAlbum-c")
	if env.Code != 1 {
		t.Fatalf("envelope wrong: %+v", env)
	}
	if int(data["total"].(float64)) != 3 {
		t.Fatalf("total must be the collection totalnum: %+v", data)
	}
	rows := decodeRows(t, data)
	if len(rows) != 3 || rows[0]["itemPinId"] != "ItmAlbum-i1" || rows[2]["itemPinId"] != "ItmAlbum-i3" {
		t.Fatalf("items must follow ingest order: %+v", rows)
	}
	if rows[0]["number"] != rows[0]["itemPinNumber"] || rows[0]["address"] != rows[0]["ownerAddress"] {
		t.Fatalf("item aliases diverge: %+v", rows[0])
	}
	if rows[0]["output"] != "ItmAlbum-i1:0" || rows[0]["outputValue"].(float64) != 1000 {
		t.Fatalf("mint outpoint wrong: %+v", rows[0])
	}

	// By name + window.
	_, _, data2 := doGet(t, router, "/api/mrc721/collection/items/pageList?name=ItmAlbum&cousor=1&size=1")
	rows2 := decodeRows(t, data2)
	if len(rows2) != 1 || rows2[0]["itemPinId"] != "ItmAlbum-i2" {
		t.Fatalf("window wrong: %+v", rows2)
	}

	// Unknown collectionid: no-data error.
	_, envMissing, _ := doGet(t, router, "/api/mrc721/collection/items/pageList?pinid=ghost")
	if envMissing.Code != 100 {
		t.Fatalf("unknown pinid wrong: %+v", envMissing)
	}
	// Unknown name: success with empty list (same as man-p2p).
	_, envGhostName, dataGhost := doGet(t, router, "/api/mrc721/collection/items/pageList?name=Ghost")
	if envGhostName.Code != 1 || int(dataGhost["total"].(float64)) != 0 {
		t.Fatalf("unknown name must be empty-success: %+v", dataGhost)
	}
	// Neither param: parameter error.
	_, envParam, _ := doGet(t, router, "/api/mrc721/collection/items/pageList")
	if envParam.Code != 404 {
		t.Fatalf("param error wrong: %+v", envParam)
	}
}

func TestAddressCollectionAPI(t *testing.T) {
	a := newTestAggregator(t)
	seedAlbum(t, a, "HoldOne")
	seedAlbum(t, a, "HoldTwo")
	router := newAPIRouter(t, a)

	// creator holds: HoldOne i1 + HoldTwo i1 (creatorMinted) → 2 collections.
	_, env, data := doGet(t, router, "/api/mrc721/address/collection?address="+creator)
	if env.Code != 1 {
		t.Fatalf("envelope wrong: %+v", env)
	}
	if int(data["total"].(float64)) != 2 {
		t.Fatalf("total must be distinct collections: %+v", data)
	}
	rows := decodeRows(t, data)
	if rows[0]["collectionname"] != "HoldOne" || rows[0]["ownnum"].(float64) != 1 {
		t.Fatalf("ownnum/dedup wrong: %+v", rows)
	}

	// other holds HoldOne i2 + HoldTwo i2 → 2 collections, ownnum 1 each.
	_, _, data2 := doGet(t, router, "/api/mrc721/address/collection?address="+other)
	rows2 := decodeRows(t, data2)
	if int(data2["total"].(float64)) != 2 || rows2[0]["collectionname"] != "HoldOne" || rows2[1]["collectionname"] != "HoldTwo" || rows2[0]["ownnum"].(float64) != 1 {
		t.Fatalf("two-collection holder wrong: %+v", rows2)
	}

	// Pagination across the ordered collection list.
	_, _, data3 := doGet(t, router, "/api/mrc721/address/collection?address="+creator+"&cursor=1&size=1")
	rows3 := decodeRows(t, data3)
	if len(rows3) != 1 || rows3[0]["collectionname"] != "HoldTwo" {
		t.Fatalf("holder pagination wrong: %+v", rows3)
	}

	_, envParam, _ := doGet(t, router, "/api/mrc721/address/collection")
	if envParam.Code != 404 {
		t.Fatalf("missing address must be a parameter error: %+v", envParam)
	}
}

func TestAddressItemAPI(t *testing.T) {
	a := newTestAggregator(t)
	seedAlbum(t, a, "AddrAlbum")
	router := newAPIRouter(t, a)

	_, env, data := doGet(t, router, "/api/mrc721/address/item?address="+creator)
	if env.Code != 1 {
		t.Fatalf("envelope wrong: %+v", env)
	}
	rows := decodeRows(t, data)
	if int(data["total"].(float64)) != 1 || rows[0]["itemPinId"] != "AddrAlbum-i1" {
		t.Fatalf("creator inventory wrong: %+v", rows)
	}

	// Filter by collection name.
	_, _, data2 := doGet(t, router, "/api/mrc721/address/item?address="+other+"&name=AddrAlbum")
	rows2 := decodeRows(t, data2)
	if len(rows2) != 1 || rows2[0]["itemPinId"] != "AddrAlbum-i2" {
		t.Fatalf("name-filtered inventory wrong: %+v", rows2)
	}
	// Filter by collectionid.
	_, _, data3 := doGet(t, router, "/api/mrc721/address/item?address="+other+"&pinId=AddrAlbum-c")
	if int(data3["total"].(float64)) != 1 {
		t.Fatalf("pinId-filtered inventory wrong: %+v", data3)
	}
	// Unknown collectionid filter: no-data error.
	_, envMissing, _ := doGet(t, router, "/api/mrc721/address/item?address="+other+"&pinId=ghost")
	if envMissing.Code != 100 {
		t.Fatalf("unknown filter collection wrong: %+v", envMissing)
	}
}

func TestItemInfoAPI(t *testing.T) {
	a := newTestAggregator(t)
	seedAlbum(t, a, "InfoItm")
	router := newAPIRouter(t, a)

	_, env, data := doGet(t, router, "/api/mrc721/item/info?pinId=InfoItm-i2")
	if env.Code != 1 {
		t.Fatalf("envelope wrong: %+v", env)
	}
	if data["itemPinId"] != "InfoItm-i2" || data["ownerAddress"] != other || data["metaId"] != data["ownerMetaId"] {
		t.Fatalf("item info fields wrong: %+v", data)
	}
	if _, hasPending := data["pending"]; hasPending {
		t.Fatal("pending key must be absent when no mempool spend exists")
	}

	// With a staged pending spend the overlay appears with the fixed
	// txStatus and the to/txid aliases.
	if err := a.db.SavePendingTransfer(&PendingTransfer{
		ItemPinId:     "InfoItm-i2",
		ChainName:     "mvc",
		SpentOutpoint: "InfoItm-i2:0",
		FromAddress:   other,
		ToAddress:     creator,
		TxId:          "spendtx-info",
		FirstSeenAt:   1700000900,
	}); err != nil {
		t.Fatalf("stage pending: %v", err)
	}
	_, _, data2 := doGet(t, router, "/api/mrc721/item/info?pinId=InfoItm-i2")
	pending, ok := data2["pending"].(map[string]any)
	if !ok {
		t.Fatalf("pending overlay missing: %+v", data2)
	}
	if pending["txStatus"] != "unconfirmed" || pending["to"] != creator || pending["txid"] != "spendtx-info" || pending["spentOutpoint"] != "InfoItm-i2:0" {
		t.Fatalf("pending overlay fields wrong: %+v", pending)
	}

	_, envMissing, _ := doGet(t, router, "/api/mrc721/item/info?pinId=ghost")
	if envMissing.Code != 100 {
		t.Fatalf("unknown item wrong: %+v", envMissing)
	}
	_, envParam, _ := doGet(t, router, "/api/mrc721/item/info")
	if envParam.Code != 404 {
		t.Fatalf("missing pinId wrong: %+v", envParam)
	}
}

func TestItemTransfersAPI(t *testing.T) {
	a := newTestAggregator(t)
	seedAlbum(t, a, "TrItm")
	router := newAPIRouter(t, a)

	transferMap, pinIdByOutput := transferFor("TrItm-i1", other, creator, "spend-1:1", 546, false)
	a.applyTransferHook("mvc", 300, transferMap, pinIdByOutput, 1700000500)
	transferMap, pinIdByOutput = transferFor("TrItm-i1", holder, other, "spend-2:1", 546, false)
	a.applyTransferHook("mvc", 301, transferMap, pinIdByOutput, 1700000501)
	if err := a.db.SavePendingTransfer(&PendingTransfer{
		ItemPinId:     "TrItm-i1",
		ChainName:     "mvc",
		SpentOutpoint: "spend-2:1",
		FromAddress:   holder,
		ToAddress:     creator,
		TxId:          "spendtx-live",
		FirstSeenAt:   1700000600,
	}); err != nil {
		t.Fatalf("stage pending: %v", err)
	}

	_, env, data := doGet(t, router, "/api/mrc721/item/transfers?pinId=TrItm-i1")
	if env.Code != 1 {
		t.Fatalf("envelope wrong: %+v", env)
	}
	// Confirmed rows only in list/total; the unconfirmed spend is a
	// separate always-present pending array.
	if int(data["total"].(float64)) != 2 {
		t.Fatalf("total must count confirmed rows only: %+v", data)
	}
	rows := decodeRows(t, data)
	if len(rows) != 2 || rows[0]["txId"] != "spendtx-TrItm-i1" || rows[0]["height"].(float64) != 300 {
		t.Fatalf("history rows wrong: %+v", rows)
	}
	pending, ok := data["pending"].([]any)
	if !ok || len(pending) != 1 {
		t.Fatalf("pending array wrong: %+v", data["pending"])
	}

	// No history, no pending: list/total zero, pending []. (seedAlbum moves
	// i2/i3 via transfers, so a fresh issuer-minted item is the empty probe.)
	runPins(t, a, testPin("TrItm-i4", "/nft/mrc721/TrItm/four", "create", []byte(`"4"`), creator))
	_, _, data2 := doGet(t, router, "/api/mrc721/item/transfers?pinId=TrItm-i4")
	if int(data2["total"].(float64)) != 0 || len(decodeRows(t, data2)) != 0 {
		t.Fatalf("empty history wrong: %+v", data2)
	}
	if pend, ok := data2["pending"].([]any); !ok || len(pend) != 0 {
		t.Fatalf("pending must be an empty array, not absent: %+v", data2["pending"])
	}

	_, envParam, _ := doGet(t, router, "/api/mrc721/item/transfers")
	if envParam.Code != 404 {
		t.Fatalf("missing pinId wrong: %+v", envParam)
	}
}

func TestItemVerifyAPI(t *testing.T) {
	a := newTestAggregator(t)
	seedAlbum(t, a, "VerAlbum")
	router := newAPIRouter(t, a)

	_, env, data := doGet(t, router, "/api/mrc721/item/verify?pinId=VerAlbum-i1")
	if env.Code != 1 {
		t.Fatalf("envelope wrong: %+v", env)
	}
	if data["indexed"] != true || data["collected"] != true || data["collectionid"] != "VerAlbum-c" || data["collectionPinId"] != "VerAlbum-c" {
		t.Fatalf("verify fields wrong: %+v", data)
	}
	itemGenesis, ok := data["itemGenesis"].(map[string]any)
	if !ok || itemGenesis["tx"] != "tx-VerAlbum-i1" || itemGenesis["height"].(float64) != 100 {
		t.Fatalf("itemGenesis wrong: %+v", itemGenesis)
	}
	colGenesis, ok := data["collectionGenesis"].(map[string]any)
	if !ok || colGenesis["tx"] != "tx-VerAlbum-c" {
		t.Fatalf("collectionGenesis wrong: %+v", colGenesis)
	}

	// Not indexed: fact set with false flags.
	_, _, data2 := doGet(t, router, "/api/mrc721/item/verify?pinId=ghost")
	if data2["indexed"] != false || data2["collected"] != false || data2["pinId"] != "ghost" {
		t.Fatalf("unindexed verify wrong: %+v", data2)
	}
}
