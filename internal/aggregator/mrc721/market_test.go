package mrc721

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/idbotsca"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type stubCA struct {
	certified map[string]string
}

func (s *stubCA) IsCollectionCertified(collectionPinId string) (bool, string) {
	title, ok := s.certified[strings.ToLower(strings.TrimSpace(collectionPinId))]
	return ok, title
}

type stubProfiles struct {
	byIdentity map[string]*SellerProfileSnapshot
}

func (s *stubProfiles) LookupProfile(identity string) *SellerProfileSnapshot {
	return s.byIdentity[strings.ToLower(strings.TrimSpace(identity))]
}

func (s *stubProfiles) ResolveAddressesByGlobalMetaId(gid string) []string {
	if p := s.byIdentity[strings.ToLower(strings.TrimSpace(gid))]; p != nil && p.Address != "" {
		return []string{p.Address}
	}
	return nil
}

type stubPresence struct {
	online map[string]int64
}

func (s *stubPresence) PresenceStatus(identity string) (bool, *int64) {
	if sec, ok := s.online[strings.ToLower(strings.TrimSpace(identity))]; ok {
		v := sec
		return true, &v
	}
	return false, nil
}

func setupMarketFixture(t *testing.T) (*Aggregator, *mrc721nft.Aggregator, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })

	nftAgg := &mrc721nft.Aggregator{}
	if err := nftAgg.Init(store, nil); err != nil {
		t.Fatalf("init mrc721nft: %v", err)
	}
	nftAgg.SetZeroConfGates(true, true)

	agg := &Aggregator{}
	agg.SetOfferEnabled(true)
	agg.SetZeroConfTrade(true)
	agg.nowFn = func() int64 { return 1760001000 }
	if err := agg.Init(store, nil); err != nil {
		t.Fatalf("init mrc721: %v", err)
	}
	agg.SetNFTReader(nftAgg)
	nftAgg.SetTransferEventHook(agg.OnItemTransferred)
	nftAgg.SetCollectionMarketEnricher(agg)
	nftAgg.SetItemDetailEnricher(agg)

	ca := &stubCA{certified: map[string]string{
		strings.ToLower(testCollection): "MetaBot Genesis",
	}}
	agg.SetCAReader(ca)

	profiles := &stubProfiles{byIdentity: map[string]*SellerProfileSnapshot{
		strings.ToLower(testSeller1): {
			Address:       testSeller1,
			GlobalMetaId:  "idq1sellerone000000000000000000000000000000",
			Name:          "AliceBot",
			Avatar:        "metafile://alice",
			HasChatPubkey: true,
		},
		"idq1sellerone000000000000000000000000000000": {
			Address:       testSeller1,
			GlobalMetaId:  "idq1sellerone000000000000000000000000000000",
			Name:          "AliceBot",
			Avatar:        "metafile://alice",
			HasChatPubkey: true,
		},
		strings.ToLower(testSeller2): {
			Address:       testSeller2,
			GlobalMetaId:  "idq1sellertwo000000000000000000000000000000",
			Name:          "BobBot",
			Avatar:        "metafile://bob",
			HasChatPubkey: false,
		},
	}}
	agg.SetProfileReader(profiles)
	agg.SetPresenceReader(&stubPresence{online: map[string]int64{
		"idq1sellerone000000000000000000000000000000": 5,
	}})

	router := gin.New()
	apiGroup := router.Group("/api")
	agg.RegisterRoutes(apiGroup)
	nftAgg.RegisterRoutes(apiGroup)

	return agg, nftAgg, router
}

func seedCollectionAndItems(t *testing.T, nftAgg *mrc721nft.Aggregator) (item1, item2, item3, item4 string) {
	t.Helper()
	colRaw, _ := json.Marshal(map[string]any{
		"name":        "MetaBot",
		"desc":        "Genesis MetaBot NFTs",
		"totalSupply": 100,
		"cover":       "metafile://cover",
	})
	colPin := &aggregator.PinInscription{
		Id:            testCollection,
		Operation:     "create",
		Path:          "/nft/mrc721/metabot/collection_desc",
		Address:       testSeller1,
		CreateAddress: testSeller1,
		GenesisHeight: 100,
		Timestamp:     1760000000,
		ContentBody:   colRaw,
		ChainName:     "mvc",
		IsOpReturn:    true,
		Output:        hexOf("coltx") + ":0",
	}
	if _, err := nftAgg.HandleBlockPin(colPin); err != nil {
		t.Fatalf("handle collection pin: %v", err)
	}

	mint := func(seed, owner string, idx int64, name string) string {
		pinId := testSellPinId(seed, 0)
		pin := &aggregator.PinInscription{
			Id:                 pinId,
			Operation:          "create",
			Path:               "/nft/mrc721/metabot",
			Address:            testSeller1,
			CreateAddress:      testSeller1,
			GenesisHeight:      101 + idx,
			TxIndex:            idx,
			Timestamp:          1760000100 + idx,
			ContentBody:        []byte(`{"name":"` + name + `"}`),
			ChainName:          "mvc",
			IsOpReturn:         true,
			GenesisTransaction: hexOf(seed),
			Output:             hexOf(seed) + ":0",
			OutputValue:        1000,
		}
		if _, err := nftAgg.HandleBlockPin(pin); err != nil {
			t.Fatalf("handle mint pin %s: %v", name, err)
		}
		if owner != testSeller1 {
			nftAgg.HandleBlockTransfers("mvc", 110+idx, map[string]aggregator.TransferNotice{
				hexOf(seed) + ":0": {
					FromAddress: testSeller1,
					ToAddress:   owner,
					Output:      hexOf("xfer-"+seed) + ":0",
					OutputValue: 1000,
					Location:    hexOf("xfer-"+seed) + ":0:0",
				},
			})
		}
		return pinId
	}

	item1 = mint("item1", testSeller1, 1, "MetaBot #1310")
	item2 = mint("item2", testSeller1, 2, "MetaBot #1311")
	item3 = mint("item3", testSeller2, 3, "MetaBot #1312")
	item4 = mint("item4", testSeller2, 4, "MetaBot #1313")
	return
}

func TestSellV21SchemaAndTransferAndExpiryInvalidation(t *testing.T) {
	agg, nftAgg, router := setupMarketFixture(t)
	item1, item2, _, _ := seedCollectionAndItems(t, nftAgg)

	// Invalid active listing with prices.me.amountMilli <= 0 must be skipped.
	badPin := mrc721SellPin(testSellPinId("bad-price", 0), 200, 1760000200, sellContent(t, map[string]any{
		"itemPinId":     item1,
		"sellerAddress": testSeller1,
		"active":        true,
		"prices": map[string]any{
			"me": map[string]any{"amountMilli": 0},
		},
	}), "create")
	if _, err := agg.HandleBlockPin(badPin); err != nil {
		t.Fatalf("badPin err: %v", err)
	}
	if rec, _ := agg.SellIntentInfo(item1); rec != nil {
		t.Fatalf("expected invalid price listing skipped, got %+v", rec)
	}

	// Valid v2.1 listing with ME + CNY prices, default autoFulfillMe=true, negotiable=true.
	sellPin1 := mrc721SellPin(testSellPinId("sell1", 0), 201, 1760000300, sellContent(t, map[string]any{
		"itemPinId":        item1,
		"sellerAddress":    testSeller1,
		"collectionPinId":  testCollection,
		"active":           true,
		"negotiable":       true,
		"shopMetaAppPinId": hexOf("shop") + "i0",
		"prices": map[string]any{
			"me":   map[string]any{"amountMilli": 55000},
			"fiat": map[string]any{"currency": "CNY", "amountCents": 1800},
		},
	}), "create")
	if _, err := agg.HandleBlockPin(sellPin1); err != nil {
		t.Fatalf("sellPin1 err: %v", err)
	}
	rec1, err := agg.SellIntentInfo(item1)
	if err != nil || rec1 == nil || !rec1.Active || !rec1.EffectiveAutoFulfillMe() || !rec1.Negotiable {
		t.Fatalf("unexpected v2.1 sell intent: rec=%+v err=%v", rec1, err)
	}
	if rec1.ItemOutpoint == nil || rec1.ItemOutpoint.TxID != hexOf("item1") {
		t.Fatalf("expected auto-filled outpoint from mrc721nft, got %+v", rec1.ItemOutpoint)
	}

	// Expired listing is surfaced as active=false, inactiveReason="expired".
	sellPin2 := mrc721SellPin(testSellPinId("sell2", 0), 202, 1760000400, sellContent(t, map[string]any{
		"itemPinId":     item2,
		"sellerAddress": testSeller1,
		"active":        true,
		"expiresAt":     1760000500, // < nowFn (1760001000)
		"prices": map[string]any{
			"me": map[string]any{"amountMilli": 40000},
		},
	}), "create")
	_, _ = agg.HandleBlockPin(sellPin2)
	rec2, _ := agg.SellIntentInfo(item2)
	if rec2 == nil || rec2.Active || rec2.InactiveReason != "expired" {
		t.Fatalf("expected expired listing inactive, got %+v", rec2)
	}

	// UTXO transfer of item1 to seller2 auto-invalidates sellPin1 with inactiveReason="transferred".
	xferTx := hexOf("xfer-item1-to-seller2")
	nftAgg.HandleBlockTransfers("mvc", 205, map[string]aggregator.TransferNotice{
		hexOf("item1") + ":0": {
			FromAddress: testSeller1,
			ToAddress:   testSeller2,
			Output:      xferTx + ":0",
			OutputValue: 1000,
			Location:    xferTx + ":0:0",
		},
	})
	recAfter, _ := agg.SellIntentInfo(item1)
	if recAfter == nil || recAfter.Active || recAfter.InactiveReason != "transferred" {
		t.Fatalf("expected transferred sell intent invalidated, got %+v", recAfter)
	}

	// Verify HTTP sell/intent/info reflects inactiveReason="transferred".
	_, env := doMrc721Request(t, router, "/api/mrc721/sell/intent/info?itemPinId="+item1)
	if env.Code != 1 {
		t.Fatalf("expected code=1, got %d", env.Code)
	}
	var row sellIntentRow
	if err := json.Unmarshal(env.Data, &row); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if row.Active || row.InactiveReason != "transferred" {
		t.Fatalf("unexpected HTTP sell/intent/info after transfer: %+v", row)
	}
}

func TestMarketAggregationListingsHoldersTradesAndMetaweb(t *testing.T) {
	agg, nftAgg, router := setupMarketFixture(t)
	item1, item2, item3, item4 := seedCollectionAndItems(t, nftAgg)

	// List all 4 items with ME & CNY prices to test even-length floor/median:
	// ME prices: 10000, 12300, 12450, 30000 -> sorted: [10000, 12300, 12450, 30000]
	// middle two avg = (12300 + 12450)/2 = 12375 -> floored to 100 milli-ME = 12300!
	listItem := func(seed, itemPin, seller string, height int64, meMilli, cnyCents int64) {
		prices := map[string]any{}
		if meMilli > 0 {
			prices["me"] = map[string]any{"amountMilli": meMilli}
		}
		if cnyCents > 0 {
			prices["fiat"] = map[string]any{"currency": "CNY", "amountCents": cnyCents}
		}
		pin := mrc721SellPin(testSellPinId(seed, 0), height, 1760000500+height, sellContent(t, map[string]any{
			"itemPinId":        itemPin,
			"sellerAddress":    seller,
			"collectionPinId":  testCollection,
			"collectionName":   "MetaBot",
			"active":           true,
			"prices":           prices,
			"note":             "Fast delivery #" + seed,
			"shopMetaAppPinId": hexOf("shop") + "i0",
		}), "create")
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("list %s: %v", seed, err)
		}
	}
	listItem("l1", item1, testSeller1, 210, 12300, 1500)
	listItem("l2", item2, testSeller1, 211, 30000, 0)
	listItem("l3", item3, testSeller2, 212, 10000, 900)
	listItem("l4", item4, testSeller2, 213, 12450, 2100)

	// 1. REQ-B-04: collection summary & floor/median calculation.
	_, sumEnv := doMrc721Request(t, router, "/api/mrc721/market/collection/"+testCollection+"/summary")
	if sumEnv.Code != 0 {
		t.Fatalf("summary expected code=0, got %d (%s)", sumEnv.Code, sumEnv.Message)
	}
	var summary CollectionMarketSummary
	if err := json.Unmarshal(sumEnv.Data, &summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}
	if !summary.CaVerified || summary.DisplayTitle != "MetaBot Genesis" {
		t.Fatalf("expected CA verified MetaBot Genesis, got %+v", summary)
	}
	if summary.HolderCount != 2 || summary.ListedCount != 4 || summary.ListedMeCount != 4 || summary.ListedCnyCount != 3 {
		t.Fatalf("unexpected counts: %+v", summary)
	}
	if summary.FloorPriceMeMilli == nil || *summary.FloorPriceMeMilli != 10000 {
		t.Fatalf("expected floor ME 10000, got %v", summary.FloorPriceMeMilli)
	}
	if summary.MedianPriceMeMilli == nil || *summary.MedianPriceMeMilli != 12300 {
		t.Fatalf("expected median ME 12300 (floored to 100), got %v", summary.MedianPriceMeMilli)
	}
	if summary.FloorPriceCnyCents == nil || *summary.FloorPriceCnyCents != 900 {
		t.Fatalf("expected floor CNY 900, got %v", summary.FloorPriceCnyCents)
	}
	if summary.FloorSeller == nil || summary.FloorSeller.Address != testSeller2 {
		t.Fatalf("expected floorSeller %s, got %+v", testSeller2, summary.FloorSeller)
	}

	// /api/mrc721/market/collections
	_, colsEnv := doMrc721Request(t, router, "/api/mrc721/market/collections?caOnly=true&sort=listed_desc")
	if colsEnv.Code != 0 {
		t.Fatalf("collections expected code=0, got %d", colsEnv.Code)
	}

	// 2. REQ-B-05: multi-dimensional listing search.
	_, listEnv := doMrc721Request(t, router, "/api/mrc721/market/listings?currency=me&minPrice=10000&maxPrice=15000&sort=price_me_asc&caOnly=true")
	if listEnv.Code != 0 {
		t.Fatalf("listings expected code=0, got %d", listEnv.Code)
	}
	var listPage struct {
		List  []ListingDTO `json:"list"`
		Total int          `json:"total"`
	}
	if err := json.Unmarshal(listEnv.Data, &listPage); err != nil {
		t.Fatalf("decode listings: %v", err)
	}
	if listPage.Total != 3 || len(listPage.List) != 3 {
		t.Fatalf("expected 3 listings in [10000,15000], got total=%d len=%d", listPage.Total, len(listPage.List))
	}
	if listPage.List[0].Prices.Me.AmountMilli != 10000 || listPage.List[1].Prices.Me.AmountMilli != 12300 || listPage.List[2].Prices.Me.AmountMilli != 12450 {
		t.Fatalf("unexpected ME ascending sort: %+v", listPage.List)
	}

	// onlineOnly=true + seller by globalMetaId
	_, onlineEnv := doMrc721Request(t, router, "/api/mrc721/market/listings?onlineOnly=true&seller=idq1sellerone000000000000000000000000000000")
	if err := json.Unmarshal(onlineEnv.Data, &listPage); err != nil {
		t.Fatalf("decode online listings: %v", err)
	}
	if listPage.Total != 2 || !listPage.List[0].Seller.IsOnline {
		t.Fatalf("expected 2 online listings for seller1, got %+v", listPage)
	}

	// 3. REQ-B-06: collection holders
	_, holdEnv := doMrc721Request(t, router, "/api/mrc721/collection/holders?pinId="+testCollection)
	if holdEnv.Code != 1 {
		t.Fatalf("holders expected code=1, got %d", holdEnv.Code)
	}
	var holdersData struct {
		HolderCount int         `json:"holderCount"`
		List        []HolderDTO `json:"list"`
	}
	if err := json.Unmarshal(holdEnv.Data, &holdersData); err != nil {
		t.Fatalf("decode holders: %v", err)
	}
	if holdersData.HolderCount != 2 || len(holdersData.List) != 2 {
		t.Fatalf("expected 2 holders, got %+v", holdersData)
	}

	// 4. REQ-B-07: /protocols/mrc721trade settlement verification against mrc721nft transfer
	saleTx := hexOf("sale-item4-to-seller1")
	forgedTrade := &aggregator.PinInscription{
		Id:            testSellPinId("forged-trade", 0),
		Path:          PathTrade,
		Operation:     "create",
		GenesisHeight: 220,
		Timestamp:     1760000800,
		ContentBody: []byte(`{"txId":"` + saleTx + `","itemPinId":"` + item4 + `","buyerAddress":"1AttackerAddress11111111111111111","currency":"me","amountMilli":12450}`),
	}
	nftAgg.HandleBlockTransfers("mvc", 220, map[string]aggregator.TransferNotice{
		hexOf("xfer-item4") + ":0": {
			FromAddress: testSeller2,
			ToAddress:   testSeller1,
			Output:      saleTx + ":0",
			OutputValue: 1000,
			Location:    saleTx + ":0:0",
		},
	})
	if _, err := agg.HandleBlockPin(forgedTrade); err != nil {
		t.Fatalf("forged trade err: %v", err)
	}
	validTrade := &aggregator.PinInscription{
		Id:            testSellPinId("valid-trade", 0),
		Path:          PathTrade,
		Operation:     "create",
		GenesisHeight: 221,
		Timestamp:     1760000900,
		ContentBody: []byte(`{"txId":"` + saleTx + `","itemPinId":"` + item4 + `","sellerAddress":"` + testSeller2 + `","buyerAddress":"` + testSeller1 + `","currency":"me","amountMilli":12450,"settledAt":1760000900,"verifiedSource":"abs_escrow"}`),
	}
	if _, err := agg.HandleBlockPin(validTrade); err != nil {
		t.Fatalf("valid trade err: %v", err)
	}
	_, tradesEnv := doMrc721Request(t, router, "/api/mrc721/market/trades?collectionPinId="+url.QueryEscape(testCollection))
	if tradesEnv.Code != 0 {
		t.Fatalf("trades expected code=0, got %d", tradesEnv.Code)
	}
	var tradesPage struct {
		List  []TradeRecord `json:"list"`
		Total int           `json:"total"`
	}
	if err := json.Unmarshal(tradesEnv.Data, &tradesPage); err != nil {
		t.Fatalf("decode trades: %v", err)
	}
	if tradesPage.Total != 1 || tradesPage.List[0].TxId != saleTx {
		t.Fatalf("expected 1 verified trade %s, got %+v", saleTx, tradesPage)
	}
	// Summary tradeStats reflects the trade.
	_, sumAfterEnv := doMrc721Request(t, router, "/api/mrc721/market/collection/"+testCollection+"/summary")
	_ = json.Unmarshal(sumAfterEnv.Data, &summary)
	if summary.TradeStats.TotalTrades != 1 || summary.TradeStats.VolumeMeMilli24h != 12450 {
		t.Fatalf("unexpected tradeStats after settlement: %+v", summary.TradeStats)
	}

	// 5. REQ-B-08: metaweb unified search over mrc721-collection & mrc721-listing.
	mw := &metaweb.Aggregator{}
	if err := mw.Init(storage.NewPebbleStore(t.TempDir()), nil); err != nil {
		t.Fatalf("init metaweb: %v", err)
	}
	mw.SetDocumentSources(agg)
	mwRouter := gin.New()
	mw.RegisterRoutes(mwRouter.Group("/api"))

	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/search?q=MetaBot&protocols=mrc721-collection,mrc721-listing", nil)
	rec := httptest.NewRecorder()
	mwRouter.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("metaweb search status %d", rec.Code)
	}
	var mwResp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Protocol string `json:"protocol"`
				Title    string `json:"title"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &mwResp); err != nil {
		t.Fatalf("decode metaweb search: %v\nraw: %s", err, rec.Body.String())
	}
	if mwResp.Code != 0 || len(mwResp.Data.Items) < 2 {
		t.Fatalf("expected metaweb search results for mrc721-collection & mrc721-listing, got %+v", mwResp)
	}
	seenProtos := map[string]bool{}
	for _, it := range mwResp.Data.Items {
		seenProtos[it.Protocol] = true
	}
	if !seenProtos["mrc721-collection"] || !seenProtos["mrc721-listing"] {
		t.Fatalf("expected both mrc721-collection and mrc721-listing in results, got %+v", seenProtos)
	}

	// Keep unused idbotsca import honest if referenced in future extensions.
	_ = idbotsca.PathCA
}
