package mrc721nft

import (
	"encoding/json"
	"fmt"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Zero-conf read-surface acceptance tests (R-F-2 issuance, R-F-3 trading),
// replaying the requirement's Appendix A scenario shape: mints first
// (a parent→child chain), a batch item_desc, then the collection_desc —
// all mempool, album invisible on every endpoint for 25+ minutes before
// R-F; now every check passes within one poll, before any block.

// chainedMempoolMint builds a mempool mint pin whose tx spends an output of
// the previous mint tx (the parent→child chain behind numberFinal).
func chainedMempoolMint(id, path string, seq int, parent *aggregator.PinInscription, address string) *aggregator.PinInscription {
	pin := mempoolPin(id, path, "create", []byte(fmt.Sprintf(`"frame %d"`, seq)), address)
	pin.WireHash = "wirehash-" + id
	pin.Output = "ledger-" + id + ":0"
	if parent != nil {
		// The child's funding input is the parent tx's output.
		pin.Inputs = []string{parent.WireHash + ":0"}
	}
	return pin
}

func newZeroConfAPIRouter(t *testing.T, a *Aggregator) *gin.Engine {
	// The search / collections-issued routes belong to the sibling mrc721
	// aggregate; importing it here is an import cycle, so the test mounts
	// thin handlers over the same native source methods with the same wire
	// envelope ({"code":1,"data":{list,total}}) the sibling handler builds.
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	a.RegisterRoutes(router.Group("/api"))
	searchJSON := func(c *gin.Context, rows []*SearchRow, total int, next string) {
		if rows == nil {
			rows = []*SearchRow{}
		}
		c.JSON(200, gin.H{"code": 1, "message": "ok", "data": gin.H{"list": rows, "total": total, "cursor": next}})
	}
	router.GET("/api/mrc721/collection/search", func(c *gin.Context) {
		rows, total, next, err := a.SearchNativeCollections(
			c.Query("keyword"), c.Query("issuer"), c.Query("cursor"),
			parseSize(c.Query("size")), c.Query("confirmedOnly") != "1")
		if err != nil {
			c.JSON(200, gin.H{"code": 40000, "message": "invalid parameter"})
			return
		}
		searchJSON(c, rows, total, next)
	})
	router.GET("/api/mrc721/address/collections-issued", func(c *gin.Context) {
		rows, total, next, err := a.NativeCollectionsIssued(
			c.Query("address"), c.Query("cursor"), parseSize(c.Query("size")),
			c.Query("confirmedOnly") != "1")
		if err != nil {
			c.JSON(200, gin.H{"code": 40000, "message": "invalid parameter"})
			return
		}
		searchJSON(c, rows, total, next)
	})
	return router
}

// parseSize mirrors the house list clamps (default 20, max 100).
func parseSize(raw string) int {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 20
	}
	if n > 100 {
		return 100
	}
	return n
}

func TestZeroConfIssuanceMintBeforeRegister(t *testing.T) {
	a := newZeroConfAggregator(t)
	router := newZeroConfAPIRouter(t, a)

	// IDBots issuance order: 5 mints (parent→child chain), batch item_desc
	// with object metadata, collection_desc LAST.
	m1 := chainedMempoolMint("zm1", "/nft/mrc721/PixelSun/frame-1", 1, nil, creator)
	m2 := chainedMempoolMint("zm2", "/nft/mrc721/PixelSun/frame-2", 2, m1, creator)
	m3 := chainedMempoolMint("zm3", "/nft/mrc721/PixelSun/frame-3", 3, m2, creator)
	m4 := chainedMempoolMint("zm4", "/nft/mrc721/PixelSun/frame-4", 4, m3, creator)
	m5 := chainedMempoolMint("zm5", "/nft/mrc721/PixelSun/frame-5", 5, m4, creator)
	desc := mempoolPin("zd1", "/nft/mrc721/PixelSun/item_desc", "create",
		[]byte(`{"items":[`+
			`{"pinid":"zm1","name":"Frame 1","desc":"Sun 1","cover":"metafile://f1","metadata":{"contentType":"image/jpeg","generator":"Seedream 4.5"}},`+
			`{"pinid":"zm2","name":"Frame 2","desc":"Sun 2","cover":"metafile://f2","metadata":{"contentType":"image/jpeg","generator":"Seedream 4.5"}},`+
			`{"pinid":"zm3","name":"Frame 3","desc":"Sun 3","cover":"metafile://f3"},`+
			`{"pinid":"zm4","name":"Frame 4","desc":"Sun 4","cover":"metafile://f4"},`+
			`{"pinid":"zm5","name":"Frame 5","desc":"Sun 5","cover":"metafile://f5"}`+
			`]}`), creator)
	col := mempoolPin("zc1", "/nft/mrc721/PixelSun/collection_desc", "create",
		[]byte(`{"name":"Pixel Sun","totalSupply":5,"royaltyRate":0}`), creator)
	for _, pin := range []*aggregator.PinInscription{m1, m2, m3, m4, m5, desc, col} {
		if _, err := a.HandleMempoolPin(pin); err != nil {
			t.Fatalf("mempool pin %s: %v", pin.Id, err)
		}
	}

	// collection/info?pinId=<collectionid>: confirmed:false, supply 5,
	// mintedCount 5 (R-F-2 acceptance).
	_, env, data := doGet(t, router, "/api/mrc721/collection/info?pinId=zc1")
	if env.Code != 1 {
		t.Fatalf("collection/info miss for overlay album: %+v", env)
	}
	if v, _ := data["confirmed"].(bool); v {
		t.Fatalf("overlay album must serve confirmed:false: %v", data["confirmed"])
	}
	if n, _ := data["totalSupply"].(float64); int(n) != 5 {
		t.Fatalf("totalSupply wrong: %v", data["totalSupply"])
	}
	if n, _ := data["mintedCount"].(float64); int(n) != 5 {
		t.Fatalf("mintedCount (overlay included) wrong: %v", data["mintedCount"])
	}
	if n, _ := data["confirmedMintedCount"].(float64); int(n) != 0 {
		t.Fatalf("confirmedMintedCount wrong: %v", data["confirmedMintedCount"])
	}
	if h, _ := data["genesisHeight"].(float64); int(h) != -1 {
		t.Fatalf("overlay album genesisHeight must be -1: %v", data["genesisHeight"])
	}

	// collection/items/pageList: 5 items numbered 1..5 in mint order,
	// confirmed:false, numberFinal:true (parent→child chain).
	_, env, data = doGet(t, router, "/api/mrc721/collection/items/pageList?pinid=zc1")
	if env.Code != 1 {
		t.Fatalf("items pageList miss: %+v", env)
	}
	rows := decodeRows(t, data)
	if len(rows) != 5 {
		t.Fatalf("expected 5 overlay items, got %d", len(rows))
	}
	for i, row := range rows {
		wantId := fmt.Sprintf("zm%d", i+1)
		if row["itemPinId"] != wantId {
			t.Fatalf("item %d: want %s got %v", i, wantId, row["itemPinId"])
		}
		if n, _ := row["number"].(float64); int(n) != i+1 {
			t.Fatalf("item %s: want #%d got %v (provisional numbering must follow the mint chain)", wantId, i+1, row["number"])
		}
		if v, _ := row["confirmed"].(bool); v {
			t.Fatalf("overlay item must serve confirmed:false")
		}
		if v, _ := row["numberFinal"].(bool); !v {
			t.Fatalf("chained mints must serve numberFinal:true (item %s)", wantId)
		}
		if h, _ := row["genesisHeight"].(float64); int(h) != -1 {
			t.Fatalf("overlay item genesisHeight must be -1")
		}
	}

	// item/info: name, desc, cover and compact object metadata from the
	// batch desc (R-F-2 acceptance).
	_, env, data = doGet(t, router, "/api/mrc721/item/info?pinId=zm1")
	if env.Code != 1 {
		t.Fatalf("item/info miss for overlay item: %+v", env)
	}
	if data["name"] != "Frame 1" || data["desc"] != "Sun 1" || data["cover"] != "metafile://f1" {
		t.Fatalf("overlay item display fields wrong: %v/%v/%v", data["name"], data["desc"], data["cover"])
	}
	if data["metadata"] != `{"contentType":"image/jpeg","generator":"Seedream 4.5"}` {
		t.Fatalf("object metadata not compact text: %v", data["metadata"])
	}
	if v, _ := data["descConfirmed"].(bool); v {
		t.Fatalf("overlay-filled desc fields must serve descConfirmed:false")
	}

	// search + collections-issued include the album (R-F-2 acceptance).
	_, env, data = doGet(t, router, "/api/mrc721/collection/search?keyword=pixel")
	if env.Code != 1 {
		t.Fatalf("collection/search error: %+v", env)
	}
	searchRows := decodeRows(t, data)
	if len(searchRows) != 1 || searchRows[0]["pinid"] != "zc1" {
		t.Fatalf("overlay album missing from search: %+v", searchRows)
	}
	if v, _ := searchRows[0]["confirmed"].(bool); v {
		t.Fatalf("overlay search row must carry confirmed:false")
	}
	if n, _ := searchRows[0]["mintedCount"].(float64); int(n) != 5 {
		t.Fatalf("search row mintedCount wrong: %v", searchRows[0]["mintedCount"])
	}
	_, env, data = doGet(t, router, "/api/mrc721/address/collections-issued?address="+creator)
	if env.Code != 1 {
		t.Fatalf("collections-issued error: %+v", env)
	}
	if rows := decodeRows(t, data); len(rows) != 1 || rows[0]["pinid"] != "zc1" {
		t.Fatalf("overlay album missing from collections-issued")
	}

	// address/item + address/collection for the issuer include it
	// (R-F-2 acceptance).
	_, env, data = doGet(t, router, "/api/mrc721/address/item?address="+creator)
	if env.Code != 1 {
		t.Fatalf("address/item error: %+v", env)
	}
	if rows := decodeRows(t, data); len(rows) != 5 {
		t.Fatalf("issuer address/item must list the 5 overlay items, got %d", len(rows))
	}
	_, env, data = doGet(t, router, "/api/mrc721/address/collection?address="+creator)
	if env.Code != 1 {
		t.Fatalf("address/collection error: %+v", env)
	}
	if rows := decodeRows(t, data); len(rows) != 1 || rows[0]["pinid"] != "zc1" {
		t.Fatalf("overlay album missing from address/collection: %+v", rows)
	}

	// Confirmation handoff (R-F-8.1): the block folds every pin; the same
	// calls return identical numbers and fields with confirmed:true.
	for _, pin := range []*aggregator.PinInscription{m1, m2, m3, m4, m5, desc, col} {
		confirmed := *pin
		confirmed.GenesisHeight = 197806
		confirmed.TxIndex = int64(len(confirmed.Id)) % 7 // deterministic per-pin positions
		if _, err := a.HandleBlockPin(&confirmed); err != nil {
			t.Fatalf("block pin %s: %v", pin.Id, err)
		}
	}
	_, env, data = doGet(t, router, "/api/mrc721/collection/info?pinId=zc1")
	if env.Code != 1 {
		t.Fatalf("collection/info miss after confirmation: %+v", env)
	}
	if v, _ := data["confirmed"].(bool); !v {
		t.Fatalf("confirmed album must serve confirmed:true")
	}
	if n, _ := data["mintedCount"].(float64); int(n) != 5 {
		t.Fatalf("mintedCount after confirmation wrong: %v", data["mintedCount"])
	}
	_, env, data = doGet(t, router, "/api/mrc721/collection/items/pageList?pinid=zc1")
	rows = decodeRows(t, data)
	if len(rows) != 5 {
		t.Fatalf("expected 5 confirmed items, got %d", len(rows))
	}
	for i, row := range rows {
		if n, _ := row["number"].(float64); int(n) != i+1 {
			t.Fatalf("confirmed numbering must be identical: item %d got #%v", i, row["number"])
		}
		if v, _ := row["confirmed"].(bool); !v {
			t.Fatalf("confirmed item must serve confirmed:true")
		}
	}
}

func TestZeroConfSupplyGateAndOverSupply(t *testing.T) {
	a := newZeroConfAggregator(t)
	router := newAPIRouter(t, a)

	// Registered album with totalSupply 2, both slots confirmed-minted.
	runPins(t, a,
		testPin("sc1", "/nft/mrc721/SupplyGate/collection_desc", "create", []byte(`{"name":"SG","totalSupply":2}`), creator),
		testPin("sm1", "/nft/mrc721/SupplyGate/a", "create", []byte(`"1"`), creator),
		testPin("sm2", "/nft/mrc721/SupplyGate/b", "create", []byte(`"2"`), creator),
	)

	// Two overlay mints: one takes the last... none left — BOTH are
	// oversupply and stay out of lists and counts.
	for _, id := range []string{"zo1", "zo2"} {
		if _, err := a.HandleMempoolPin(mempoolPin(id, "/nft/mrc721/SupplyGate/"+id, "create", []byte(`"o"`), creator)); err != nil {
			t.Fatalf("overlay mint %s: %v", id, err)
		}
	}
	_, _, data := doGet(t, router, "/api/mrc721/collection/info?name=SupplyGate")
	if n, _ := data["mintedCount"].(float64); int(n) != 2 {
		t.Fatalf("mintedCount must stay at the supply cap: %v", data["mintedCount"])
	}
	_, _, data = doGet(t, router, "/api/mrc721/collection/items/pageList?name=SupplyGate")
	if rows := decodeRows(t, data); len(rows) != 2 {
		t.Fatalf("oversupply mints must stay out of item lists: %d rows", len(rows))
	}
	// includeOverSupply=1 serves them with overSupply:true.
	_, _, data = doGet(t, router, "/api/mrc721/collection/items/pageList?name=SupplyGate&includeOverSupply=1")
	rows := decodeRows(t, data)
	if len(rows) != 4 {
		t.Fatalf("includeOverSupply must surface the hidden mints: %d rows", len(rows))
	}
	for _, row := range rows {
		if id, _ := row["itemPinId"].(string); id == "zo1" || id == "zo2" {
			if v, _ := row["overSupply"].(bool); !v {
				t.Fatalf("oversupply mint %v must carry overSupply:true", row["itemPinId"])
			}
		}
	}
}

func TestZeroConfConfirmedOnlyByteParity(t *testing.T) {
	a := newZeroConfAggregator(t)
	router := newAPIRouter(t, a)

	// Overlay pin recorded, then confirmed — the confirmedOnly response
	// must equal today's shape (no new fields anywhere).
	if _, err := a.HandleMempoolPin(mempoolPin("co1", "/nft/mrc721/BytePar/nft-1", "create", []byte(`"x"`), creator)); err != nil {
		t.Fatalf("overlay pin: %v", err)
	}
	runPins(t, a,
		testPin("bc1", "/nft/mrc721/BytePar/collection_desc", "create", []byte(`{"name":"BP"}`), creator),
		testPin("bo1", "/nft/mrc721/BytePar/nft-1", "create", []byte(`"x"`), creator),
	)

	_, _, data := doGet(t, router, "/api/mrc721/item/info?pinId=bo1&confirmedOnly=1")
	for _, banned := range []string{"confirmed", "numberFinal", "ownershipConfirmed", "confirmedOwnerAddress", "descConfirmed", "firstSeenAt", "overSupply"} {
		if _, ok := data[banned]; ok {
			t.Fatalf("confirmedOnly response must not carry %q", banned)
		}
	}
	if _, ok := data["pending"]; ok {
		t.Fatalf("no pending expected here")
	}
	// Gate-on request WITHOUT confirmedOnly adds the fields.
	_, _, data = doGet(t, router, "/api/mrc721/item/info?pinId=bo1")
	if v, _ := data["confirmed"].(bool); !v {
		t.Fatalf("gate-on row must carry confirmed:true")
	}
}

func TestZeroConfTradingOwnershipAndHolderViews(t *testing.T) {
	a := newZeroConfAggregator(t)
	router := newAPIRouter(t, a)

	// A mints item X; A declares sale (not indexed here); B buys via an
	// unconfirmed spend; B re-transfers to C — all before any block.
	runPins(t, a,
		testPin("tc1", "/nft/mrc721/TradeFlow/collection_desc", "create", []byte(`{"name":"TF"}`), creator),
		testPin("tx1", "/nft/mrc721/TradeFlow/x", "create", []byte(`"x"`), creator),
	)
	item, _ := a.db.GetItem("tx1")
	O1 := "minttx:0"
	item.Output = O1
	_ = a.db.SaveItem(item)
	a.markTrackedDirty()

	O2 := "buytx:0"
	a.HandleMempoolSpendsIncremental("mvc", map[string]aggregator.TransferNotice{
		O1: {FromAddress: creator, ToAddress: holder, Output: O2, OutputValue: 1000, Location: O2 + ":0"},
	})
	O3 := "reselltx:0"
	a.HandleMempoolSpendsIncremental("mvc", map[string]aggregator.TransferNotice{
		O2: {FromAddress: holder, ToAddress: impostor, Output: O3, OutputValue: 1000, Location: O3 + ":0"},
	})

	// item/info shows the final overlay owner with ownershipConfirmed:false
	// and the last confirmed owner (R-F-3 acceptance).
	_, env, data := doGet(t, router, "/api/mrc721/item/info?pinId=tx1")
	if env.Code != 1 {
		t.Fatalf("item/info error: %+v", env)
	}
	if data["ownerAddress"] != impostor || data["address"] != impostor {
		t.Fatalf("chained overlay owner wrong: %v", data["ownerAddress"])
	}
	if v, _ := data["ownershipConfirmed"].(bool); v {
		t.Fatalf("overlay ownership must serve ownershipConfirmed:false")
	}
	if data["confirmedOwnerAddress"] != creator {
		t.Fatalf("confirmedOwnerAddress must be the last confirmed owner: %v", data["confirmedOwnerAddress"])
	}
	if data["output"] != O3 {
		t.Fatalf("overlay output wrong: %v", data["output"])
	}

	// Holder views: C holds X, A and B do not (R-F-3 acceptance).
	_, _, data = doGet(t, router, "/api/mrc721/address/item?address="+impostor)
	if rows := decodeRows(t, data); len(rows) != 1 || rows[0]["itemPinId"] != "tx1" {
		t.Fatalf("new owner's address/item must contain X: %+v", rows)
	}
	for _, addr := range []string{creator, holder} {
		_, _, data = doGet(t, router, "/api/mrc721/address/item?address="+addr)
		if rows := decodeRows(t, data); len(rows) != 0 {
			t.Fatalf("previous owner %s must not list X: %+v", addr, rows)
		}
	}

	// item/transfers lists the overlay transfers with confirmed:false and
	// total includes them (R-F-3.4).
	_, _, data = doGet(t, router, "/api/mrc721/item/transfers?pinId=tx1")
	if n, _ := data["total"].(float64); int(n) != 2 {
		t.Fatalf("total must include overlay transfers: %v", data["total"])
	}
	rows, err := transferRows(data)
	if err != nil {
		t.Fatalf("decode transfers: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 overlay transfer rows, got %d", len(rows))
	}
	if rows[0]["fromAddress"] != creator || rows[0]["toAddress"] != holder {
		t.Fatalf("first overlay transfer wrong: %+v", rows[0])
	}
	if v, _ := rows[0]["confirmed"].(bool); v {
		t.Fatalf("overlay transfer rows must carry confirmed:false")
	}

	// Confirmation: every field identical, confirmed:true everywhere.
	a.HandleBlockTransfers("mvc", 400, map[string]aggregator.TransferNotice{
		O1: {FromAddress: creator, ToAddress: holder, Output: O2, OutputValue: 1000, Location: "buytx:0:0"},
	})
	a.HandleBlockTransfers("mvc", 401, map[string]aggregator.TransferNotice{
		O2: {FromAddress: holder, ToAddress: impostor, Output: O3, OutputValue: 1000, Location: "reselltx:0:0"},
	})
	_, _, data = doGet(t, router, "/api/mrc721/item/info?pinId=tx1")
	if data["ownerAddress"] != impostor {
		t.Fatalf("confirmed owner must be identical to the overlay view: %v", data["ownerAddress"])
	}
	if v, _ := data["ownershipConfirmed"].(bool); !v {
		t.Fatalf("confirmed ownership must serve ownershipConfirmed:true")
	}
	_, _, data = doGet(t, router, "/api/mrc721/item/transfers?pinId=tx1")
	if n, _ := data["total"].(float64); int(n) != 2 {
		t.Fatalf("confirmed transfer total must match: %v", data["total"])
	}
}

func transferRows(data map[string]any) ([]map[string]any, error) {
	raw, err := json.Marshal(data["list"])
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
