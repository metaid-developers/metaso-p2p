package ftmarket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/idbotsca"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	testMvcCodehash = "a242b6c2c3d4e5f60718293a4b5c6d7e8f901122"
	testMvcGenesis  = "005d112233445566778899aabbccddeeff001122"
	testMvcAssetKey = "mvc-ft:" + testMvcCodehash + ":" + testMvcGenesis

	testImpostorCodehash = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testImpostorGenesis  = "cccccccccccccccccccccccccccccccccccccccc"
	testImpostorAssetKey = "mvc-ft:" + testImpostorCodehash + ":" + testImpostorGenesis

	testMrc20Id       = "4f92000000000000000000000000000000000000000000000000000000000001i0"
	testMrc20AssetKey = "mrc20:" + testMrc20Id

	testSellerMvcAddr  = "1BNesCuvJeW2DAF42xkyCU1ifZVuNZ61mv"
	testSellerMvcAddr2 = "176C9RPWDggnvdVcWG3wrZEJcm1bHTcKM5"
	testSellerBtcAddr  = "bc1qseller00000000000000000000000000000000"
	testSellerGlobal   = "idq1seller0000000000000000000000000000000"
	testSellerGlobal2  = "idq1seller2000000000000000000000000000000"
	testBuyerBtcAddr   = "bc1qbuyer000000000000000000000000000000000"
	testBuyerMvcAddr   = "13ZLeYbqc4MT9HZGQRiamf93cNANZwR2Ec"
	testEscrowBotGid   = "idq1escrowbot0000000000000000000000000000"
)

func testHex64(seed string) string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = 'a'
	}
	for i := 0; i < len(seed) && i < 64; i++ {
		c := seed[i]
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') {
			b[i] = c
		} else {
			b[i] = byte('a' + (int(c) % 6))
		}
	}
	return string(b)
}

func testPinId(seed string, idx int) string {
	return fmt.Sprintf("%si%d", testHex64(seed), idx)
}

func newTestAggregator(t *testing.T) (*Aggregator, *storage.PebbleStore) {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	agg := &Aggregator{}
	if err := agg.Init(store, nil); err != nil {
		t.Fatalf("init ftmarket: %v", err)
	}
	agg.SetZeroConfTrade(true)
	return agg, store
}

func setupRouter(agg *Aggregator) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	agg.RegisterRoutes(r.Group("/api"))
	return r
}

func doJSONGet(t *testing.T, r *gin.Engine, path string, target any) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if target != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), target); err != nil {
			t.Fatalf("decode GET %s response (%s): %v", path, rec.Body.String(), err)
		}
	}
	return rec.Code
}

func makeSellPin(pinId string, height, ts int64, chainName string, body map[string]any) *aggregator.PinInscription {
	raw, _ := json.Marshal(body)
	return &aggregator.PinInscription{
		Id:             pinId,
		Operation:      "create",
		Path:           PathFtSell,
		ChainName:      chainName,
		GenesisHeight:  height,
		Timestamp:      ts,
		ContentBody:    raw,
		ContentSummary: string(raw),
	}
}

func TestC1_3_MutualExclusionSameAndCrossListing(t *testing.T) {
	agg, _ := newTestAggregator(t)
	now := int64(1795300000)
	agg.nowFn = func() int64 { return now }

	outpoint := testHex64("utxo1") + ":0"
	tokenListTx := testHex64("listtx1")

	// 1. Single payload declaring both mvcTokenSell and me on the same utxoOutpoint must be rejected.
	bothRails := makeSellPin(testPinId("both1", 0), 100, now, "mvc", map[string]any{
		"active":     true,
		"listingKey": testMvcAssetKey + ":" + outpoint,
		"asset": map[string]any{
			"assetKey":  testMvcAssetKey,
			"chain":     "mvc",
			"standard":  "metacontract-ft",
			"codehash":  testMvcCodehash,
			"genesis":   testMvcGenesis,
			"symbol":    "METAID",
			"tokenName": "MetaID Token",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "20000000000",
			"amountDisplay": "200",
			"utxoOutpoint":  outpoint,
		},
		"sellerAddress": testSellerMvcAddr,
		"rails": map[string]any{
			"mvcTokenSell": map[string]any{
				"sellTxid":             testHex64("sell1"),
				"tokenListTxid":        tokenListTx,
				"tokenListOutputIndex": 0,
				"priceSats":            50000000,
				"sellerAddress":        testSellerMvcAddr,
			},
			"me": map[string]any{
				"amountMilli": 29000,
				"autoFulfill": true,
			},
		},
		"createdAt": now,
	})
	if _, err := agg.HandleBlockPin(bothRails); err != nil {
		t.Fatalf("HandleBlockPin error: %v", err)
	}
	listings, err := agg.listAllEffectiveListings(true)
	if err != nil {
		t.Fatalf("listAllEffectiveListings: %v", err)
	}
	if len(listings) != 0 {
		t.Fatalf("expected conflicting single-payload listing rejected, got %d", len(listings))
	}

	// 2. Listing A declares mvcTokenSell on outpoint -> accepted.
	contractOutpoint := tokenListTx + ":0"
	listingA := makeSellPin(testPinId("listA", 0), 101, now+1, "mvc", map[string]any{
		"active":     true,
		"listingKey": "listing-A",
		"asset": map[string]any{
			"assetKey":  testMvcAssetKey,
			"chain":     "mvc",
			"standard":  "metacontract-ft",
			"codehash":  testMvcCodehash,
			"genesis":   testMvcGenesis,
			"symbol":    "METAID",
			"tokenName": "MetaID Token",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "20000000000",
			"amountDisplay": "200",
			"utxoOutpoint":  contractOutpoint,
		},
		"sellerAddress": testSellerMvcAddr,
		"rails": map[string]any{
			"mvcTokenSell": map[string]any{
				"sellTxid":             testHex64("sellA"),
				"tokenListTxid":        tokenListTx,
				"tokenListOutputIndex": 0,
				"priceSats":            50000000,
				"sellerAddress":        testSellerMvcAddr,
			},
		},
		"createdAt": now + 1,
	})
	if _, err := agg.HandleBlockPin(listingA); err != nil {
		t.Fatalf("listingA ingest failed: %v", err)
	}

	// 3. Cross-listing conflict: Listing B declares me rail on the same utxoOutpoint -> rejected.
	listingB := makeSellPin(testPinId("listB", 0), 102, now+2, "mvc", map[string]any{
		"active":     true,
		"listingKey": "listing-B",
		"asset": map[string]any{
			"assetKey":  testMvcAssetKey,
			"chain":     "mvc",
			"standard":  "metacontract-ft",
			"codehash":  testMvcCodehash,
			"genesis":   testMvcGenesis,
			"symbol":    "METAID",
			"tokenName": "MetaID Token",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "20000000000",
			"amountDisplay": "200",
			"utxoOutpoint":  contractOutpoint,
		},
		"sellerAddress": testSellerMvcAddr,
		"rails": map[string]any{
			"me": map[string]any{
				"amountMilli": 29000,
				"autoFulfill": true,
			},
		},
		"createdAt": now + 2,
	})
	if _, err := agg.HandleBlockPin(listingB); err != nil {
		t.Fatalf("listingB ingest failed: %v", err)
	}

	listings, _ = agg.listAllEffectiveListings(true)
	if len(listings) != 1 || listings[0].ListingKey != "listing-a" {
		t.Fatalf("expected only listing-a active after cross-listing conflict, got %+v", listings)
	}

	// 4. Separate utxoOutpoint in the same batchGroupId on me rail -> accepted!
	walletOutpoint := testHex64("walletutxo") + ":1"
	listingC := makeSellPin(testPinId("listC", 0), 103, now+3, "mvc", map[string]any{
		"active":     true,
		"listingKey": "listing-C",
		"asset": map[string]any{
			"assetKey":  testMvcAssetKey,
			"chain":     "mvc",
			"standard":  "metacontract-ft",
			"codehash":  testMvcCodehash,
			"genesis":   testMvcGenesis,
			"symbol":    "METAID",
			"tokenName": "MetaID Token",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "10000000000",
			"amountDisplay": "100",
			"utxoOutpoint":  walletOutpoint,
		},
		"sellerAddress": testSellerMvcAddr,
		"rails": map[string]any{
			"me": map[string]any{
				"amountMilli": 15000,
				"autoFulfill": true,
			},
		},
		"batchGroupId": "batch_1795300000_x8k2",
		"batchIndex":   2,
		"batchTotal":   2,
		"createdAt":    now + 3,
	})
	if _, err := agg.HandleBlockPin(listingC); err != nil {
		t.Fatalf("listingC ingest failed: %v", err)
	}
	listings, _ = agg.listAllEffectiveListings(true)
	if len(listings) != 2 {
		t.Fatalf("expected 2 listings with separate outpoints, got %d", len(listings))
	}
}

func TestUTXOSpentAutoInvalidationAndFloorRecalculation(t *testing.T) {
	agg, _ := newTestAggregator(t)
	now := int64(1795300000)
	agg.nowFn = func() int64 { return now }

	listTxA := testHex64("listtxA")
	outpointA := listTxA + ":0"
	listTxB := testHex64("listtxB")
	outpointB := listTxB + ":0"
	walletTxC := testHex64("wallettxC")
	outpointC := walletTxC + ":0"
	btcOutpoint := testHex64("btcutxo") + ":0"

	var stateMu sync.Mutex
	spendStatus := map[string]string{
		outpointA: "unspent",
		outpointB: "unspent",
	}
	btcStatus := 0 // 0 = Available, -1 = Spent

	upstreamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stateMu.Lock()
		defer stateMu.Unlock()
		switch r.URL.Path {
		case "/utxo/check":
			var req struct {
				OutPoints []string `json:"outPoints"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := make(map[string]any, len(req.OutPoints))
			for _, op := range req.OutPoints {
				st := spendStatus[strings.ToLower(op)]
				if st == "" {
					st = "unspent"
				}
				data[op] = map[string]any{
					"isExist":     true,
					"spendStatus": st,
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 2000, "data": data})
		case "/ft/utxos":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"utxos": []map[string]any{
						{"txid": walletTxC, "txIndex": 0},
					},
				},
			})
		case "/ft/mempool/utxos":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{
					"income": []any{},
					"spend":  []any{},
				},
			})
		case "/ft/owners":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"total": 321},
			})
		case "/api/mrc20/tx/history":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 1,
				"data": map[string]any{
					"list": []map[string]any{
						{
							"txPoint":   btcOutpoint,
							"mrc20Id":   testMrc20Id,
							"toAddress": testSellerBtcAddr,
							"status":    btcStatus,
							"verify":    true,
						},
					},
				},
			})
		case "/api/mrc20/tick/info/" + testMrc20Id:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 1,
				"data": map[string]any{
					"tick":      "METAID",
					"tokenName": "MetaID Coin",
					"decimals":  "8",
					"holders":   4280,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstreamSrv.Close()

	agg.SetUTXOVerifier(NewUpstreamUTXOVerifier(upstreamSrv.URL, upstreamSrv.URL, upstreamSrv.Client()))
	agg.SetTokenMetadataProvider(NewUpstreamTokenMetadataProvider(upstreamSrv.URL, upstreamSrv.URL, upstreamSrv.Client()))
	agg.SetVerifyCacheTTL(10 * time.Millisecond)

	// Listing A: 100 METAID for 50,000 sats -> 500 sat/token (cheapest floor)
	pinA := makeSellPin(testPinId("mvcA", 0), 100, now, "mvc", map[string]any{
		"active":     true,
		"listingKey": "mvc-lot-A",
		"asset": map[string]any{
			"assetKey": testMvcAssetKey, "chain": "mvc", "standard": "metacontract-ft",
			"codehash": testMvcCodehash, "genesis": testMvcGenesis,
			"symbol": "METAID", "tokenName": "MetaID Token", "decimals": 8,
		},
		"lot": map[string]any{"amountBase": "10000000000", "amountDisplay": "100", "utxoOutpoint": outpointA},
		"sellerAddress": testSellerMvcAddr,
		"rails": map[string]any{
			"mvcTokenSell": map[string]any{
				"sellTxid": testHex64("sellA"), "tokenListTxid": listTxA, "tokenListOutputIndex": 0,
				"priceSats": 50000, "sellerAddress": testSellerMvcAddr,
			},
		},
		"createdAt": now,
	})
	// Listing B: 100 METAID for 90,000 sats -> 900 sat/token
	pinB := makeSellPin(testPinId("mvcB", 0), 101, now+1, "mvc", map[string]any{
		"active":     true,
		"listingKey": "mvc-lot-B",
		"asset": map[string]any{
			"assetKey": testMvcAssetKey, "chain": "mvc", "standard": "metacontract-ft",
			"codehash": testMvcCodehash, "genesis": testMvcGenesis,
			"symbol": "METAID", "tokenName": "MetaID Token", "decimals": 8,
		},
		"lot": map[string]any{"amountBase": "10000000000", "amountDisplay": "100", "utxoOutpoint": outpointB},
		"sellerAddress": testSellerMvcAddr,
		"rails": map[string]any{
			"mvcTokenSell": map[string]any{
				"sellTxid": testHex64("sellB"), "tokenListTxid": listTxB, "tokenListOutputIndex": 0,
				"priceSats": 90000, "sellerAddress": testSellerMvcAddr,
			},
		},
		"createdAt": now + 1,
	})
	// Listing C: 100 METAID for 15,000 mME -> 150 mME/token
	pinC := makeSellPin(testPinId("mvcC", 0), 102, now+2, "mvc", map[string]any{
		"active":     true,
		"listingKey": "mvc-lot-C",
		"asset": map[string]any{
			"assetKey": testMvcAssetKey, "chain": "mvc", "standard": "metacontract-ft",
			"codehash": testMvcCodehash, "genesis": testMvcGenesis,
			"symbol": "METAID", "tokenName": "MetaID Token", "decimals": 8,
		},
		"lot": map[string]any{"amountBase": "10000000000", "amountDisplay": "100", "utxoOutpoint": outpointC},
		"sellerAddress": testSellerMvcAddr,
		"rails": map[string]any{
			"me": map[string]any{"amountMilli": 15000, "autoFulfill": true},
		},
		"createdAt": now + 2,
	})
	// BTC MRC-20 listing D: 200 METAID for 29,000 mME -> 145 mME/token
	pinD := makeSellPin(testPinId("btcD", 0), 103, now+3, "btc", map[string]any{
		"active":     true,
		"listingKey": "btc-lot-D",
		"asset": map[string]any{
			"assetKey": testMrc20AssetKey, "chain": "btc", "standard": "mrc20",
			"mrc20Id": testMrc20Id, "symbol": "METAID", "tokenName": "MetaID Coin", "decimals": 8,
		},
		"lot": map[string]any{"amountBase": "20000000000", "amountDisplay": "200", "utxoOutpoint": btcOutpoint},
		"sellerAddress":    testSellerBtcAddr,
		"sellerBtcAddress": testSellerBtcAddr,
		"rails": map[string]any{
			"me": map[string]any{"amountMilli": 29000, "autoFulfill": true},
		},
		"createdAt": now + 3,
	})

	for _, p := range []*aggregator.PinInscription{pinA, pinB, pinC, pinD} {
		if _, err := agg.HandleBlockPin(p); err != nil {
			t.Fatalf("ingest pin %s: %v", p.Id, err)
		}
	}

	r := setupRouter(agg)

	// Verify initial summary for MVC token: spaceUnitSats = 500, meUnitMilli = 150, listedLotCount = 3, holderCount = 321.
	var sumEnv struct {
		Code int                `json:"code"`
		Data TokenMarketSummary `json:"data"`
	}
	code := doJSONGet(t, r, "/api/ft/market/token/"+url.PathEscape(testMvcAssetKey)+"/summary", &sumEnv)
	if code != http.StatusOK || sumEnv.Code != 0 {
		t.Fatalf("unexpected summary status=%d code=%d", code, sumEnv.Code)
	}
	if sumEnv.Data.ListedLotCount != 3 || sumEnv.Data.HolderCount != 321 {
		t.Fatalf("expected listedLotCount=3 holderCount=321, got %d / %d", sumEnv.Data.ListedLotCount, sumEnv.Data.HolderCount)
	}
	if sumEnv.Data.Floors.SpaceUnitSats == nil || *sumEnv.Data.Floors.SpaceUnitSats != "500" {
		t.Fatalf("expected initial spaceUnitSats=500, got %v", sumEnv.Data.Floors.SpaceUnitSats)
	}
	if sumEnv.Data.Floors.MeUnitMilli == nil || *sumEnv.Data.Floors.MeUnitMilli != "150" {
		t.Fatalf("expected initial meUnitMilli=150, got %v", sumEnv.Data.Floors.MeUnitMilli)
	}

	// Mark Listing A's contract UTXO spent AND BTC MRC-20 UTXO spent!
	stateMu.Lock()
	spendStatus[outpointA] = "spend"
	btcStatus = -1
	stateMu.Unlock()

	time.Sleep(15 * time.Millisecond)
	invalidated := agg.VerifyActiveListings(context.Background())
	if invalidated != 2 {
		t.Fatalf("expected 2 invalidated listings (mvc-lot-A + btc-lot-D), got %d", invalidated)
	}

	// Summary floor for MVC token must now recalculate immediately to 900 sat/token and listedLotCount=2!
	var sumAfter struct {
		Code int                `json:"code"`
		Data TokenMarketSummary `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/token/"+url.PathEscape(testMvcAssetKey)+"/summary", &sumAfter)
	if sumAfter.Data.ListedLotCount != 2 {
		t.Fatalf("expected listedLotCount=2 after UTXO spend, got %d", sumAfter.Data.ListedLotCount)
	}
	if sumAfter.Data.Floors.SpaceUnitSats == nil || *sumAfter.Data.Floors.SpaceUnitSats != "900" {
		t.Fatalf("expected recalculated spaceUnitSats=900, got %v", sumAfter.Data.Floors.SpaceUnitSats)
	}

	// BTC MRC-20 listing must be completely excluded from active listings.
	var btcListings struct {
		Code int `json:"code"`
		Data struct {
			List  []ListingDTO `json:"list"`
			Total int          `json:"total"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/listings?assetKey="+url.QueryEscape(testMrc20AssetKey), &btcListings)
	if btcListings.Data.Total != 0 {
		t.Fatalf("expected spent BTC MRC-20 listing excluded, got total=%d", btcListings.Data.Total)
	}
}

func TestSymbolDisambiguationAndCAVerifiedProjection(t *testing.T) {
	agg, store := newTestAggregator(t)
	now := int64(1795300000)
	agg.nowFn = func() int64 { return now }

	// Set up real idbotsca aggregator certifying only testMrc20AssetKey as official $METAID.
	caAgg := &idbotsca.Aggregator{}
	if err := caAgg.Init(store, nil); err != nil {
		t.Fatalf("init idbotsca: %v", err)
	}
	caPayload, _ := json.Marshal(map[string]any{
		"collections":  []any{},
		"officialBots": []any{},
		"certifiedTokens": []map[string]any{{
			"assetKey":    testMrc20AssetKey,
			"chain":       "btc",
			"standard":    "mrc20",
			"symbol":      "METAID",
			"tokenName":   "MetaID Official Coin",
			"displayName": "MetaID Official Coin",
			"decimals":    8,
			"icon":        "metafile://officialicon00i0",
		}},
	})
	caPin := &aggregator.PinInscription{
		Id:             testPinId("ca01", 0),
		Operation:      "create",
		Path:           idbotsca.PathCA,
		MetaId:         idbotsca.OfficialMetaID,
		GlobalMetaId:   idbotsca.OfficialGlobalMetaID,
		Address:        idbotsca.OfficialMVCAddress,
		GenesisHeight:  200,
		Timestamp:      now,
		ContentBody:    caPayload,
		ContentSummary: string(caPayload),
		ChainName:      "mvc",
	}
	if _, err := caAgg.HandleBlockPin(caPin); err != nil {
		t.Fatalf("ingest CA pin: %v", err)
	}
	agg.SetAssetBaseURL("https://file.metaid.io/metafile-indexer/content")
	agg.SetCAReader(&testCAAdapter{ca: caAgg})

	// Ingest certified BTC METAID listing and uncertified impostor MVC METAID listing.
	officialPin := makeSellPin(testPinId("off1", 0), 100, now, "btc", map[string]any{
		"active":     true,
		"listingKey": "official-listing",
		"asset": map[string]any{
			"assetKey": testMrc20AssetKey, "chain": "btc", "standard": "mrc20",
			"mrc20Id": testMrc20Id, "symbol": "METAID", "tokenName": "MetaID Coin", "decimals": 8,
		},
		"lot": map[string]any{"amountBase": "20000000000", "amountDisplay": "200", "utxoOutpoint": testHex64("u1") + ":0"},
		"sellerAddress": testSellerBtcAddr,
		"rails":         map[string]any{"me": map[string]any{"amountMilli": 30000, "autoFulfill": true}},
		"createdAt":     now,
	})
	impostorPin := makeSellPin(testPinId("imp1", 0), 101, now+1, "mvc", map[string]any{
		"active":     true,
		"listingKey": "impostor-listing",
		"asset": map[string]any{
			"assetKey": testImpostorAssetKey, "chain": "mvc", "standard": "metacontract-ft",
			"codehash": testImpostorCodehash, "genesis": testImpostorGenesis,
			"symbol": "METAID", "tokenName": "Fake METAID", "decimals": 8,
		},
		"lot": map[string]any{"amountBase": "50000000000", "amountDisplay": "500", "utxoOutpoint": testHex64("u2") + ":0"},
		"sellerAddress": testSellerMvcAddr,
		"rails":         map[string]any{"me": map[string]any{"amountMilli": 5000, "autoFulfill": true}},
		"createdAt":     now + 1,
	})
	if _, err := agg.HandleBlockPin(officialPin); err != nil {
		t.Fatalf("officialPin: %v", err)
	}
	if _, err := agg.HandleBlockPin(impostorPin); err != nil {
		t.Fatalf("impostorPin: %v", err)
	}

	r := setupRouter(agg)

	// 1. Query by symbol=METAID returns both listings AND ambiguousAssets warning with caVerified projection!
	var symResp struct {
		Code int `json:"code"`
		Data struct {
			List            []ListingDTO         `json:"list"`
			Total           int                  `json:"total"`
			AmbiguousAssets []AmbiguousAssetInfo `json:"ambiguousAssets"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/listings?symbol=METAID", &symResp)
	if symResp.Code != 0 || symResp.Data.Total != 2 {
		t.Fatalf("expected 2 listings for symbol=METAID, got code=%d total=%d", symResp.Code, symResp.Data.Total)
	}
	if len(symResp.Data.AmbiguousAssets) != 2 {
		t.Fatalf("expected 2 ambiguousAssets entries, got %+v", symResp.Data.AmbiguousAssets)
	}
	if !symResp.Data.AmbiguousAssets[0].CaVerified || symResp.Data.AmbiguousAssets[0].AssetKey != testMrc20AssetKey {
		t.Fatalf("expected caVerified official token sorted first in ambiguousAssets, got %+v", symResp.Data.AmbiguousAssets)
	}
	if symResp.Data.AmbiguousAssets[1].CaVerified || symResp.Data.AmbiguousAssets[1].AssetKey != testImpostorAssetKey {
		t.Fatalf("expected uncertified impostor token second in ambiguousAssets, got %+v", symResp.Data.AmbiguousAssets)
	}

	// 2. Query by exact assetKey clears ambiguousAssets and resolves CA icon URL.
	var exactResp struct {
		Code int `json:"code"`
		Data struct {
			List            []ListingDTO         `json:"list"`
			Total           int                  `json:"total"`
			AmbiguousAssets []AmbiguousAssetInfo `json:"ambiguousAssets"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/listings?assetKey="+url.QueryEscape(testMrc20AssetKey), &exactResp)
	if exactResp.Data.Total != 1 || len(exactResp.Data.AmbiguousAssets) != 0 {
		t.Fatalf("expected 1 listing and empty ambiguousAssets on exact assetKey, got total=%d amb=%d", exactResp.Data.Total, len(exactResp.Data.AmbiguousAssets))
	}
	if !exactResp.Data.List[0].Asset.CaVerified || exactResp.Data.List[0].Asset.Icon != "https://file.metaid.io/metafile-indexer/content/officialicon00i0" {
		t.Fatalf("expected caVerified=true and resolved icon URL, got %+v", exactResp.Data.List[0].Asset)
	}

	// 3. GET /api/ft/market/tokens?verifiedOnly=true returns only official token.
	var tokensResp struct {
		Code int `json:"code"`
		Data struct {
			List  []TokenMarketSummary `json:"list"`
			Total int                  `json:"total"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/tokens?verifiedOnly=true", &tokensResp)
	if tokensResp.Data.Total != 1 || tokensResp.Data.List[0].Asset.AssetKey != testMrc20AssetKey {
		t.Fatalf("expected only certified token when verifiedOnly=true, got %+v", tokensResp.Data.List)
	}
}

func TestRecentGranularityQuartilesAndTradeStats(t *testing.T) {
	// Direct quartile math verification: [100, 200, 300, 400, 500] tokens (8 decimals)
	scale := int64(100000000)
	amounts := make([]string, 0, 5)
	for _, n := range []int64{500, 100, 400, 200, 300} {
		amounts = append(amounts, fmt.Sprintf("%d", n*scale))
	}
	agg, _ := newTestAggregator(t)
	now := int64(1795300000)
	agg.nowFn = func() int64 { return now }

	for i, amtBase := range amounts {
		pin := makeSellPin(testPinId(fmt.Sprintf("g%d", i), 0), int64(100+i), now+int64(i), "mvc", map[string]any{
			"active":     true,
			"listingKey": fmt.Sprintf("gran-lot-%d", i),
			"asset": map[string]any{
				"assetKey": testMvcAssetKey, "chain": "mvc", "standard": "metacontract-ft",
				"codehash": testMvcCodehash, "genesis": testMvcGenesis,
				"symbol": "METAID", "tokenName": "MetaID Token", "decimals": 8,
			},
			"lot": map[string]any{
				"amountBase":    amtBase,
				"amountDisplay": fmt.Sprintf("%d", []int64{500, 100, 400, 200, 300}[i]),
				"utxoOutpoint":  testHex64(fmt.Sprintf("gu%d", i)) + ":0",
			},
			"sellerAddress": testSellerMvcAddr,
			"rails":         map[string]any{"me": map[string]any{"amountMilli": int64(10000 + i*1000), "autoFulfill": true}},
			"createdAt":     now + int64(i),
		})
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("ingest lot %d: %v", i, err)
		}
	}

	// Mock BTC floor snapshot provider and CA role for official escrow bot trade receipt.
	agg.SetBtcFloorProvider(&fakeBtcFloorProvider{floor: "9.8"})
	agg.SetCAReader(&fakeCAReader{
		roles: map[string]string{testEscrowBotGid: "escrow_guarantor"},
	})

	// Unauthorized third-party trade pin must be rejected; official escrow bot pin accepted.
	badTradeBody, _ := json.Marshal(map[string]any{
		"txId": testHex64("badtrade"), "listingKey": "gran-lot-0", "assetKey": testMvcAssetKey,
		"chain": "mvc", "symbol": "METAID", "decimals": 8,
		"amountBase": "10000000000", "rail": "me-escrow", "amountMilli": 15000,
		"sellerAddress": testSellerMvcAddr, "buyerAddress": testBuyerMvcAddr, "settledAt": now - 60,
	})
	badTradePin := &aggregator.PinInscription{
		Id: testPinId("badtr", 0), Operation: "create", Path: PathFtTrade, ChainName: "mvc",
		GenesisHeight: 150, Timestamp: now - 60, GlobalMetaId: "idq1attacker", Address: "1Attacker",
		ContentBody: badTradeBody, ContentSummary: string(badTradeBody),
	}
	if _, err := agg.HandleBlockPin(badTradePin); err != nil {
		t.Fatalf("bad trade pin: %v", err)
	}

	goodTradeBody, _ := json.Marshal(map[string]any{
		"txId": testHex64("goodtrade"), "listingKey": "gran-lot-0", "assetKey": testMvcAssetKey,
		"chain": "mvc", "symbol": "METAID", "decimals": 8,
		"amountBase": "20000000000", "rail": "me-escrow", "amountMilli": 30000,
		"sellerAddress": testSellerMvcAddr, "buyerAddress": testBuyerMvcAddr, "settledAt": now - 30,
	})
	goodTradePin := &aggregator.PinInscription{
		Id: testPinId("goodtr", 0), Operation: "create", Path: PathFtTrade, ChainName: "mvc",
		GenesisHeight: 151, Timestamp: now - 30, GlobalMetaId: testEscrowBotGid,
		ContentBody: goodTradeBody, ContentSummary: string(goodTradeBody),
	}
	if _, err := agg.HandleBlockPin(goodTradePin); err != nil {
		t.Fatalf("good trade pin: %v", err)
	}

	r := setupRouter(agg)
	var sumEnv struct {
		Code int                `json:"code"`
		Data TokenMarketSummary `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/token/"+url.PathEscape(testMvcAssetKey)+"/summary", &sumEnv)
	// Samples include 5 active lots [100,200,300,400,500] + 1 trade [200] -> sorted [100,200,200,300,400,500]:
	// p25 (idx 1.25) = 200, median (idx 2.5) = 250, p75 (idx 3.75) = 375
	if sumEnv.Data.RecentGranularity.P25LotAmountDisplay == nil || *sumEnv.Data.RecentGranularity.P25LotAmountDisplay != "200" {
		t.Fatalf("expected p25=200, got %v", sumEnv.Data.RecentGranularity.P25LotAmountDisplay)
	}
	if sumEnv.Data.RecentGranularity.MedianLotAmountDisplay == nil || *sumEnv.Data.RecentGranularity.MedianLotAmountDisplay != "250" {
		t.Fatalf("expected median=250, got %v", sumEnv.Data.RecentGranularity.MedianLotAmountDisplay)
	}
	if sumEnv.Data.RecentGranularity.P75LotAmountDisplay == nil || *sumEnv.Data.RecentGranularity.P75LotAmountDisplay != "375" {
		t.Fatalf("expected p75=375, got %v", sumEnv.Data.RecentGranularity.P75LotAmountDisplay)
	}
	if sumEnv.Data.TradeStats24h.TradeCount != 1 || sumEnv.Data.TradeStats24h.VolumeTokenDisplay != "200" {
		t.Fatalf("expected 1 trade with volume=200, got %+v", sumEnv.Data.TradeStats24h)
	}
	if sumEnv.Data.TradeStats24h.LastUnitPriceMeMilli == nil || *sumEnv.Data.TradeStats24h.LastUnitPriceMeMilli != "150" {
		t.Fatalf("expected lastUnitPriceMeMilli=150, got %v", sumEnv.Data.TradeStats24h.LastUnitPriceMeMilli)
	}

	var tradesEnv struct {
		Code int `json:"code"`
		Data struct {
			List  []TradeRecord `json:"list"`
			Total int           `json:"total"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/trades?assetKey="+url.QueryEscape(testMvcAssetKey), &tradesEnv)
	if tradesEnv.Data.Total != 1 || tradesEnv.Data.List[0].TxId != testHex64("goodtrade") {
		t.Fatalf("expected only authorized trade in /api/ft/market/trades, got %+v", tradesEnv.Data)
	}
}

func TestSearchMetawebAndFiltersAndZeroConfOverlay(t *testing.T) {
	agg, _ := newTestAggregator(t)
	now := int64(1795300000)
	agg.nowFn = func() int64 { return now }

	agg.SetProfileReader(&fakeProfileReader{
		profiles: map[string]*SellerProfileSnapshot{
			testSellerMvcAddr:  {Address: testSellerMvcAddr, GlobalMetaId: testSellerGlobal, Name: "Alice", HasChatPubkey: true},
			testSellerGlobal:   {Address: testSellerMvcAddr, GlobalMetaId: testSellerGlobal, Name: "Alice", HasChatPubkey: true},
			testSellerMvcAddr2: {Address: testSellerMvcAddr2, GlobalMetaId: testSellerGlobal2, Name: "BobOffline", HasChatPubkey: true},
			testSellerGlobal2:  {Address: testSellerMvcAddr2, GlobalMetaId: testSellerGlobal2, Name: "BobOffline", HasChatPubkey: true},
		},
	})
	agg.SetPresenceReader(&fakePresenceReader{
		online: map[string]bool{
			testSellerGlobal:  true,
			testSellerGlobal2: false,
		},
	})

	// Confirmed online seller listing (higher price: 200 mME/token)
	pin1 := makeSellPin(testPinId("p1", 0), 100, now, "mvc", map[string]any{
		"active":     true,
		"listingKey": "lot-online",
		"asset": map[string]any{
			"assetKey": testMvcAssetKey, "chain": "mvc", "standard": "metacontract-ft",
			"codehash": testMvcCodehash, "genesis": testMvcGenesis,
			"symbol": "METAID", "tokenName": "MetaID Token", "decimals": 8,
		},
		"lot":           map[string]any{"amountBase": "10000000000", "amountDisplay": "100", "utxoOutpoint": testHex64("f1") + ":0"},
		"sellerAddress": testSellerMvcAddr,
		"sellerMetaId":  testSellerGlobal,
		"rails":         map[string]any{"me": map[string]any{"amountMilli": 20000, "autoFulfill": true}},
		"note":          "Alice fast autofulfill",
		"createdAt":     now,
	})
	if _, err := agg.HandleBlockPin(pin1); err != nil {
		t.Fatalf("pin1: %v", err)
	}

	// Zero-conf mempool offline seller listing (cheaper price: 100 mME/token)
	mpPin := makeSellPin(testPinId("mp2", 0), -1, now+10, "mvc", map[string]any{
		"active":     true,
		"listingKey": "lot-mempool-offline",
		"asset": map[string]any{
			"assetKey": testMvcAssetKey, "chain": "mvc", "standard": "metacontract-ft",
			"codehash": testMvcCodehash, "genesis": testMvcGenesis,
			"symbol": "METAID", "tokenName": "MetaID Token", "decimals": 8,
		},
		"lot":           map[string]any{"amountBase": "10000000000", "amountDisplay": "100", "utxoOutpoint": testHex64("f2") + ":0"},
		"sellerAddress": testSellerMvcAddr2,
		"sellerMetaId":  testSellerGlobal2,
		"rails":         map[string]any{"me": map[string]any{"amountMilli": 10000, "autoFulfill": true}},
		"createdAt":     now + 10,
	})
	if _, err := agg.HandleMempoolPin(mpPin); err != nil {
		t.Fatalf("mpPin: %v", err)
	}

	// Wire into metaweb.Aggregator and verify unified search!
	mwAgg := &metaweb.Aggregator{}
	if err := mwAgg.Init(nil, nil); err != nil {
		t.Fatalf("mwAgg.Init: %v", err)
	}
	mwAgg.SetDocumentSources(agg)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	agg.RegisterRoutes(apiGroup)
	mwAgg.RegisterRoutes(apiGroup)

	// 1. sort=unit_price_asc returns mempool cheaper listing first (confirmed=false), then online listing (confirmed=true)
	var listResp struct {
		Code int `json:"code"`
		Data struct {
			List  []ListingDTO `json:"list"`
			Total int          `json:"total"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/listings?rail=me-escrow&sort=unit_price_asc", &listResp)
	if listResp.Data.Total != 2 {
		t.Fatalf("expected 2 listings with zero-conf overlay, got %d", listResp.Data.Total)
	}
	if listResp.Data.List[0].ListingKey != "lot-mempool-offline" || listResp.Data.List[0].Confirmed {
		t.Fatalf("expected cheapest mempool listing first with confirmed=false, got %+v", listResp.Data.List[0])
	}

	// 2. onlineOnly=true + seller=idq1seller... filters to Alice only
	doJSONGet(t, r, "/api/ft/market/listings?onlineOnly=true&seller="+testSellerGlobal, &listResp)
	if listResp.Data.Total != 1 || listResp.Data.List[0].ListingKey != "lot-online" {
		t.Fatalf("expected only Alice online listing, got %+v", listResp.Data.List)
	}

	// 3. confirmedOnly=1 hides mempool overlay
	doJSONGet(t, r, "/api/ft/market/listings?confirmedOnly=1", &listResp)
	if listResp.Data.Total != 1 || listResp.Data.List[0].ListingKey != "lot-online" {
		t.Fatalf("expected only confirmed listing when confirmedOnly=1, got %+v", listResp.Data.List)
	}

	// 4. GET /api/metaweb/search?q=METAID&protocols=ft-token,ft-listing returns both ft-token and ft-listing!
	var searchResp struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Protocol string         `json:"protocol"`
				PinId    string         `json:"pinId"`
				Title    string         `json:"title"`
				Summary  string         `json:"summary"`
				Extra    map[string]any `json:"extra"`
			} `json:"items"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/metaweb/search?q=METAID&protocols=ft-token,ft-listing", &searchResp)
	var gotToken, gotListing int
	for _, it := range searchResp.Data.Items {
		switch it.Protocol {
		case "ft-token":
			gotToken++
		case "ft-listing":
			gotListing++
		}
	}
	if gotToken != 1 || gotListing != 2 {
		t.Fatalf("expected 1 ft-token and 2 ft-listing docs in metaweb search, got token=%d listing=%d (items=%+v)", gotToken, gotListing, searchResp.Data.Items)
	}

	// 5. ReconcileMempoolRound dropping mpPin txid evicts the unconfirmed overlay listing.
	agg.ReconcileMempoolRound("mvc", []string{})
	doJSONGet(t, r, "/api/ft/market/listings", &listResp)
	if listResp.Data.Total != 1 {
		t.Fatalf("expected mempool listing evicted after reconcile, got total=%d", listResp.Data.Total)
	}
}

func TestMvcTokenSellSeedFieldsAndIDBotsAliasCompat(t *testing.T) {
	agg, _ := newTestAggregator(t)
	now := int64(1795300500)
	agg.nowFn = func() int64 { return now }

	// 1. Explicit seedTxid/seedVout/prevTokenTxid/contractAddress/tokenSellJson on MvcTokenSellRail.
	sellTx1 := testHex64("sellseed1")
	seedTx1 := testHex64("seedtx1")
	prevTx1 := testHex64("prevtok1")
	tokenSellJSON1 := fmt.Sprintf(`{"sell_txid":%q,"sell_output_index":0,"seed_txid":%q,"seed_vout":1,"price":50000000}`, sellTx1, seedTx1)
	pinExplicit := makeSellPin(testPinId("seedexp", 0), 200, now, "mvc", map[string]any{
		"active":     true,
		"listingKey": "listing-seed-explicit",
		"asset": map[string]any{
			"assetKey":  testMvcAssetKey,
			"chain":     "mvc",
			"standard":  "metacontract-ft",
			"codehash":  testMvcCodehash,
			"genesis":   testMvcGenesis,
			"symbol":    "METAID",
			"tokenName": "MetaID Token",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "10000000000",
			"amountDisplay": "100",
			"utxoOutpoint":  sellTx1 + ":0",
		},
		"sellerAddress": testSellerMvcAddr,
		"rails": map[string]any{
			"mvcTokenSell": map[string]any{
				"sellTxid":             sellTx1,
				"tokenListTxid":        sellTx1,
				"tokenListOutputIndex": 0,
				"priceSats":            50000000,
				"sellerAddress":        testSellerMvcAddr,
				"seedTxid":             seedTx1,
				"seedVout":             1,
				"prevTokenTxid":        prevTx1,
				"contractAddress":      "1ContractAddr1111111111111111111111",
				"tokenSellJson":        tokenSellJSON1,
			},
		},
		"createdAt": now,
	})
	if _, err := agg.HandleBlockPin(pinExplicit); err != nil {
		t.Fatalf("pinExplicit: %v", err)
	}

	// 2. IDBots host alias payload: nested seller, standard="mvc-ft", rails.mvcTokenSell.txid,
	//    and tokenSellJson fallback for omitted tokenListTxid/tokenListOutputIndex/seedTxid/seedVout.
	sellTx2 := testHex64("sellalias2")
	seedTx2 := testHex64("seedtx2")
	tokenSellJSON2 := fmt.Sprintf(`{"sell_txid":%q,"sell_output_index":1,"seed_txid":%q,"seed_vout":2,"price":45000000}`, sellTx2, seedTx2)
	pinHostTokenSell := makeSellPin(testPinId("hostts", 0), 201, now+1, "mvc", map[string]any{
		"active":     true,
		"listingKey": "Listing-Host-TokenSell",
		"asset": map[string]any{
			"assetKey":  testMvcAssetKey,
			"chain":     "mvc",
			"standard":  "mvc-ft",
			"codehash":  testMvcCodehash,
			"genesis":   testMvcGenesis,
			"symbol":    "METAID",
			"tokenName": "MetaID Token",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "10000000000",
			"amountDisplay": "100",
			"utxoOutpoint":  sellTx2 + ":1",
		},
		"seller": map[string]any{
			"globalMetaId":  testSellerGlobal,
			"botMvcAddress": testSellerMvcAddr,
			"botBtcAddress": testSellerBtcAddr,
		},
		"rails": map[string]any{
			"mvcTokenSell": map[string]any{
				"txid":          sellTx2,
				"priceSats":     45000000,
				"sellerAddress": testSellerMvcAddr,
				"tokenSellJson": tokenSellJSON2,
			},
		},
		"createdAt": now + 1,
	})
	if _, err := agg.HandleBlockPin(pinHostTokenSell); err != nil {
		t.Fatalf("pinHostTokenSell: %v", err)
	}

	// 3. IDBots host alias payload on ME rail (rails.me.priceMeMilli, standard="mvc-ft", nested seller),
	//    plus host-style cancellation using nested seller and uppercase listingKey.
	meOutpoint := testHex64("hostme") + ":0"
	pinHostMe := makeSellPin(testPinId("hostme", 0), 202, now+2, "mvc", map[string]any{
		"active":     true,
		"listingKey": "Listing-Host-Me",
		"asset": map[string]any{
			"assetKey":  testMvcAssetKey,
			"standard":  "mvc-ft",
			"codehash":  testMvcCodehash,
			"genesis":   testMvcGenesis,
			"symbol":    "METAID",
			"tokenName": "MetaID Token",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "10000000000",
			"amountDisplay": "100",
			"utxoOutpoint":  meOutpoint,
		},
		"seller": map[string]any{
			"globalMetaId": testSellerGlobal2,
			"address":      testSellerMvcAddr2,
		},
		"rails": map[string]any{
			"me": map[string]any{
				"priceMeMilli": 18000,
				"autoFulfill":  true,
			},
		},
		"createdAt": now + 2,
	})
	if _, err := agg.HandleBlockPin(pinHostMe); err != nil {
		t.Fatalf("pinHostMe: %v", err)
	}

	r := setupRouter(agg)
	var listResp struct {
		Code int `json:"code"`
		Data struct {
			List  []ListingDTO `json:"list"`
			Total int          `json:"total"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/listings?sort=newest", &listResp)
	if listResp.Data.Total != 3 {
		t.Fatalf("expected 3 active listings, got %d (%+v)", listResp.Data.Total, listResp.Data.List)
	}

	byKey := map[string]ListingDTO{}
	for _, item := range listResp.Data.List {
		byKey[item.ListingKey] = item
	}

	exp := byKey["listing-seed-explicit"]
	if exp.Rails == nil || exp.Rails.MvcTokenSell == nil {
		t.Fatalf("expected mvcTokenSell rail on explicit listing")
	}
	if exp.Rails.MvcTokenSell.SeedTxid != seedTx1 || exp.Rails.MvcTokenSell.SeedVout != 1 ||
		exp.Rails.MvcTokenSell.PrevTokenTxid != prevTx1 ||
		exp.Rails.MvcTokenSell.ContractAddress != "1ContractAddr1111111111111111111111" ||
		exp.Rails.MvcTokenSell.TokenSellJson != tokenSellJSON1 {
		t.Fatalf("explicit seed fields mismatch: %+v", exp.Rails.MvcTokenSell)
	}

	hostTS := byKey["listing-host-tokensell"]
	if hostTS.Asset.Standard != "metacontract-ft" {
		t.Fatalf("expected standard normalized to metacontract-ft, got %q", hostTS.Asset.Standard)
	}
	if hostTS.Seller.Address != testSellerMvcAddr || hostTS.Seller.BtcAddress != testSellerBtcAddr || hostTS.Seller.GlobalMetaId != testSellerGlobal {
		t.Fatalf("expected seller mapped from nested host seller, got %+v", hostTS.Seller)
	}
	if hostTS.Rails == nil || hostTS.Rails.MvcTokenSell == nil {
		t.Fatalf("expected mvcTokenSell rail on host listing")
	}
	if hostTS.Rails.MvcTokenSell.SellTxid != sellTx2 ||
		hostTS.Rails.MvcTokenSell.TokenListTxid != sellTx2 ||
		hostTS.Rails.MvcTokenSell.TokenListOutputIndex != 1 ||
		hostTS.Rails.MvcTokenSell.SeedTxid != seedTx2 ||
		hostTS.Rails.MvcTokenSell.SeedVout != 2 ||
		hostTS.Rails.MvcTokenSell.TokenSellJson != tokenSellJSON2 {
		t.Fatalf("host tokenSellJson fallback fields mismatch: %+v", hostTS.Rails.MvcTokenSell)
	}

	hostME := byKey["listing-host-me"]
	if hostME.Asset.Standard != "metacontract-ft" {
		t.Fatalf("expected ME listing standard normalized to metacontract-ft, got %q", hostME.Asset.Standard)
	}
	if hostME.Rails == nil || hostME.Rails.Me == nil || hostME.Rails.Me.AmountMilli != 18000 {
		t.Fatalf("expected ME amountMilli=18000 from priceMeMilli fallback, got %+v", hostME.Rails)
	}

	// Host-style cancellation Pin using nested seller and no asset/lot fields.
	pinCancelHostMe := makeSellPin(testPinId("cancelhostme", 0), 203, now+3, "mvc", map[string]any{
		"active":     false,
		"listingKey": "Listing-Host-Me",
		"seller": map[string]any{
			"globalMetaId":  testSellerGlobal2,
			"botMvcAddress": testSellerMvcAddr2,
		},
	})
	if _, err := agg.HandleBlockPin(pinCancelHostMe); err != nil {
		t.Fatalf("pinCancelHostMe: %v", err)
	}

	doJSONGet(t, r, "/api/ft/market/listings?sort=newest", &listResp)
	if listResp.Data.Total != 2 {
		t.Fatalf("expected 2 listings after host-style cancel, got %d", listResp.Data.Total)
	}
	for _, item := range listResp.Data.List {
		if item.ListingKey == "listing-host-me" {
			t.Fatalf("expected listing-host-me canceled")
		}
	}
}

type testCAAdapter struct {
	ca *idbotsca.Aggregator
}

func (a *testCAAdapter) IsTokenCertified(assetKey string) (bool, *CertifiedTokenInfo) {
	ok, entry := a.ca.IsTokenCertified(assetKey)
	if !ok || entry == nil {
		return false, nil
	}
	return true, &CertifiedTokenInfo{
		AssetKey:            entry.AssetKey,
		DisplayName:         entry.DisplayName,
		Symbol:              entry.Symbol,
		TokenName:           entry.TokenName,
		Decimals:            entry.Decimals,
		Icon:                entry.Icon,
		SensibleId:          entry.SensibleId,
		BridgedPeerAssetKey: entry.BridgedPeerAssetKey,
		BridgedDualChain:    entry.BridgedDualChain,
	}
}

func (a *testCAAdapter) ListCertifiedTokens() []CertifiedTokenInfo {
	if a == nil || a.ca == nil {
		return nil
	}
	entries := a.ca.ListCertifiedTokens()
	out := make([]CertifiedTokenInfo, 0, len(entries))
	for _, entry := range entries {
		out = append(out, CertifiedTokenInfo{
			AssetKey:            entry.AssetKey,
			DisplayName:         entry.DisplayName,
			Symbol:              entry.Symbol,
			TokenName:           entry.TokenName,
			Decimals:            entry.Decimals,
			Icon:                entry.Icon,
			SensibleId:          entry.SensibleId,
			BridgedPeerAssetKey: entry.BridgedPeerAssetKey,
			BridgedDualChain:    entry.BridgedDualChain,
		})
	}
	return out
}

func (a *testCAAdapter) GetOfficialBotRole(globalMetaId string) (bool, string) {
	b := a.ca.GetOfficialBotBadge(globalMetaId)
	if b == nil || !b.Verified {
		return false, ""
	}
	return true, b.Role
}

type fakeCAReader struct {
	tokens map[string]CertifiedTokenInfo
	roles  map[string]string
}

func (f *fakeCAReader) IsTokenCertified(assetKey string) (bool, *CertifiedTokenInfo) {
	if c, ok := f.tokens[strings.ToLower(assetKey)]; ok {
		return true, &c
	}
	return false, nil
}

func (f *fakeCAReader) ListCertifiedTokens() []CertifiedTokenInfo {
	if f == nil || len(f.tokens) == 0 {
		return nil
	}
	out := make([]CertifiedTokenInfo, 0, len(f.tokens))
	for _, c := range f.tokens {
		out = append(out, c)
	}
	return out
}

func (f *fakeCAReader) GetOfficialBotRole(globalMetaId string) (bool, string) {
	if r, ok := f.roles[globalMetaId]; ok {
		return true, r
	}
	return false, ""
}

type fakeBtcFloorProvider struct {
	floor string
}

func (f *fakeBtcFloorProvider) FetchBtcUnitFloorSats(_ context.Context, _ string) (*string, bool) {
	if f.floor == "" {
		return nil, false
	}
	v := f.floor
	return &v, true
}

type fakeProfileReader struct {
	profiles map[string]*SellerProfileSnapshot
}

func (f *fakeProfileReader) LookupProfile(identity string) *SellerProfileSnapshot {
	return f.profiles[identity]
}

func (f *fakeProfileReader) ResolveAddressesByGlobalMetaId(globalMetaId string) []string {
	if p, ok := f.profiles[globalMetaId]; ok && p.Address != "" {
		return []string{p.Address}
	}
	return nil
}

type fakePresenceReader struct {
	online map[string]bool
}

func (f *fakePresenceReader) PresenceStatus(identity string) (bool, *int64) {
	on, ok := f.online[identity]
	if !ok {
		return false, nil
	}
	ago := int64(5)
	return on, &ago
}

func TestBuiltinCertifiedSeedsOctopusBridgeAndCAReaderList(t *testing.T) {
	agg, store := newTestAggregator(t)
	now := int64(1798000000)
	agg.nowFn = func() int64 { return now }
	agg.SetAssetBaseURL("https://file.metaid.io/metafile-indexer/content")
	agg.SetBuiltinCertifiedTokens(DefaultMainnetCertifiedTokens())

	// Also certify an extra token via idbotsca with zero local listings to verify CAReader.ListCertifiedTokens() seeding.
	extraAssetKey := "mrc20:9999000000000000000000000000000000000000000000000000000000000009i0"
	caAgg := &idbotsca.Aggregator{}
	if err := caAgg.Init(store, nil); err != nil {
		t.Fatalf("init idbotsca: %v", err)
	}
	caPayload, _ := json.Marshal(map[string]any{
		"collections":  []any{},
		"officialBots": []any{},
		"certifiedTokens": []map[string]any{{
			"assetKey":    extraAssetKey,
			"chain":       "btc",
			"standard":    "mrc20",
			"symbol":      "EXTCA",
			"tokenName":   "Extra Official",
			"displayName": "Extra Official",
			"decimals":    8,
		}},
	})
	caPin := &aggregator.PinInscription{
		Id:             testPinId("caextra", 0),
		Operation:      "create",
		Path:           idbotsca.PathCA,
		MetaId:         idbotsca.OfficialMetaID,
		GlobalMetaId:   idbotsca.OfficialGlobalMetaID,
		Address:        idbotsca.OfficialMVCAddress,
		GenesisHeight:  300,
		Timestamp:      now,
		ContentBody:    caPayload,
		ContentSummary: string(caPayload),
		ChainName:      "mvc",
	}
	if _, err := caAgg.HandleBlockPin(caPin); err != nil {
		t.Fatalf("HandleBlockPin CA: %v", err)
	}
	agg.SetCAReader(&testCAAdapter{ca: caAgg})

	r := setupRouter(agg)

	// 1. Even with zero listings in PebbleDB, all 5 mainnet seeds + 1 CA-only token resolve 200 code=0 with caVerified=true.
	for _, key := range []string{
		AssetKeyMetaidBtc,
		AssetKeyMetaidMvc,
		AssetKeyMcBtc,
		AssetKeyMcMvc,
		AssetKeyIdcoinBtc,
		extraAssetKey,
	} {
		var sumResp struct {
			Code int                `json:"code"`
			Data TokenMarketSummary `json:"data"`
		}
		httpCode := doJSONGet(t, r, "/api/ft/market/token/"+url.PathEscape(key)+"/summary", &sumResp)
		if httpCode != http.StatusOK || sumResp.Code != 0 {
			t.Fatalf("expected 200 code=0 for %s without local listings, got http=%d code=%d", key, httpCode, sumResp.Code)
		}
		if !sumResp.Data.Asset.CaVerified {
			t.Fatalf("expected caVerified=true for %s, got %+v", key, sumResp.Data.Asset)
		}
	}

	// 2. Verify Octopus dual-chain bridge fields and peerSummary on $METAID BTC & MVC.
	var btcSum, mvcSum struct {
		Code int                `json:"code"`
		Data TokenMarketSummary `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/token/"+url.PathEscape(AssetKeyMetaidBtc)+"/summary", &btcSum)
	if !btcSum.Data.Asset.BridgedDualChain || btcSum.Data.Asset.BridgedPeerAssetKey != AssetKeyMetaidMvc {
		t.Fatalf("unexpected BTC METAID bridge fields: %+v", btcSum.Data.Asset)
	}
	if btcSum.Data.PeerSummary == nil || btcSum.Data.PeerSummary.Asset.AssetKey != AssetKeyMetaidMvc {
		t.Fatalf("expected PeerSummary pointing to MVC METAID, got %+v", btcSum.Data.PeerSummary)
	}

	doJSONGet(t, r, "/api/ft/market/token/"+url.PathEscape(AssetKeyMetaidMvc)+"/summary", &mvcSum)
	if !mvcSum.Data.Asset.BridgedDualChain || mvcSum.Data.Asset.BridgedPeerAssetKey != AssetKeyMetaidBtc {
		t.Fatalf("unexpected MVC METAID bridge fields: %+v", mvcSum.Data.Asset)
	}
	if mvcSum.Data.Asset.SensibleId == nil || *mvcSum.Data.Asset.SensibleId != SensibleIdMetaidMvc {
		t.Fatalf("expected MVC METAID sensibleId=%s, got %v", SensibleIdMetaidMvc, mvcSum.Data.Asset.SensibleId)
	}
	if mvcSum.Data.PeerSummary == nil || mvcSum.Data.PeerSummary.Asset.AssetKey != AssetKeyMetaidBtc {
		t.Fatalf("expected PeerSummary pointing to BTC METAID, got %+v", mvcSum.Data.PeerSummary)
	}
}

func TestMvcSensibleIdNormalizationAndLegacyBucketMigration(t *testing.T) {
	agg, store := newTestAggregator(t)
	now := int64(1798000100)
	agg.nowFn = func() int64 { return now }
	agg.SetBuiltinCertifiedTokens(DefaultMainnetCertifiedTokens())

	// Simulate the exact mainnet /protocols/ft-sell pin that supplied 72-hex sensibleId as genesis & assetKey.
	legacyAssetKey := "mvc-ft:c9cc7bbd1010b44873959a8b1a2bcedeb62302b7:" + SensibleIdMetaidMvc
	outpoint := "b2bb8aac8c8c5e3a4bc1e8a7d65f8bb0dc181d18a496f3bfdc60179b0efdd0b3:0"
	legacyListingKey := legacyAssetKey + ":" + outpoint

	sellPin := makeSellPin(testPinId("sensible72", 0), 148500, now, "mvc", map[string]any{
		"active":     true,
		"listingKey": legacyListingKey,
		"asset": map[string]any{
			"assetKey":  legacyAssetKey,
			"chain":     "mvc",
			"standard":  "mvc-ft",
			"codehash":  "c9cc7bbd1010b44873959a8b1a2bcedeb62302b7",
			"genesis":   SensibleIdMetaidMvc,
			"symbol":    "METAID",
			"tokenName": "MetaID",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "104842259",
			"amountDisplay": "1.04842259",
			"utxoOutpoint":  outpoint,
		},
		"sellerAddress": "1GrqX7K9jdnU36212f1ef726174af31f3b",
		"rails": map[string]any{
			"me": map[string]any{
				"amountMilli": 2000,
				"autoFulfill": true,
			},
		},
		"createdAt": now,
	})
	if _, err := agg.HandleBlockPin(sellPin); err != nil {
		t.Fatalf("HandleBlockPin sensibleId sellPin: %v", err)
	}

	r := setupRouter(agg)

	// 1. GET /api/ft/market/tokens?keyword=METAID must have exactly 2 entries (BTC + 1 unified MVC, no 72-hex split!).
	var tokResp struct {
		Code int `json:"code"`
		Data struct {
			List  []TokenMarketSummary `json:"list"`
			Total int                  `json:"total"`
		} `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/tokens?keyword=METAID", &tokResp)
	if tokResp.Data.Total != 2 {
		t.Fatalf("expected exactly 2 METAID entries (1 BTC + 1 MVC, no sensibleId split), got %d: %+v", tokResp.Data.Total, tokResp.Data.List)
	}
	for _, item := range tokResp.Data.List {
		if !item.Asset.CaVerified {
			t.Fatalf("expected caVerified=true on %s", item.Asset.AssetKey)
		}
		if item.Asset.Chain == "mvc" {
			if item.Asset.AssetKey != AssetKeyMetaidMvc || item.ListedLotCount != 1 {
				t.Fatalf("expected canonical MVC assetKey with listedLotCount=1, got %+v", item)
			}
			if item.Asset.SensibleId == nil || *item.Asset.SensibleId != SensibleIdMetaidMvc {
				t.Fatalf("expected sensibleId preserved on MVC DTO, got %v", item.Asset.SensibleId)
			}
		}
	}

	// 2. Querying summary or listings by the legacy 72-hex assetKey transparently resolves to the canonical 40-hex bucket.
	var sumLegacy struct {
		Code int                `json:"code"`
		Data TokenMarketSummary `json:"data"`
	}
	doJSONGet(t, r, "/api/ft/market/token/"+url.PathEscape(legacyAssetKey)+"/summary", &sumLegacy)
	if sumLegacy.Code != 0 || sumLegacy.Data.Asset.AssetKey != AssetKeyMetaidMvc || sumLegacy.Data.ListedLotCount != 1 {
		t.Fatalf("expected legacy 72-hex summary query to resolve canonical MVC bucket, got %+v", sumLegacy)
	}

	// 3. Simulate pre-upgrade unnormalized PebbleDB records and verify Init() migrates & deduplicates them.
	rawLegacyAsset, _ := json.Marshal(TokenCatalogRecord{
		Asset: AssetInfo{
			AssetKey:  legacyAssetKey,
			Chain:     "mvc",
			Standard:  "metacontract-ft",
			Codehash:  ptrStr("c9cc7bbd1010b44873959a8b1a2bcedeb62302b7"),
			Genesis:   ptrStr(SensibleIdMetaidMvc),
			Symbol:    "METAID",
			TokenName: "MetaID",
			Decimals:  8,
		},
		UpdatedAt: now,
	})
	_ = store.Set(Namespace, assetStorageKey(legacyAssetKey), rawLegacyAsset)

	aggReloaded := &Aggregator{}
	if err := aggReloaded.Init(store, nil); err != nil {
		t.Fatalf("re-init ftmarket: %v", err)
	}
	aggReloaded.SetBuiltinCertifiedTokens(DefaultMainnetCertifiedTokens())
	r2 := setupRouter(aggReloaded)
	doJSONGet(t, r2, "/api/ft/market/tokens?keyword=METAID", &tokResp)
	if tokResp.Data.Total != 2 {
		t.Fatalf("expected legacy PebbleDB bucket migrated into 2 METAID entries after Init, got %d", tokResp.Data.Total)
	}
}

func ptrStr(s string) *string { return &s }

func TestMetaidMarketFloorAndBtcMetadataFallback(t *testing.T) {
	var requestedPaths []string
	var reqMu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqMu.Lock()
		requestedPaths = append(requestedPaths, r.URL.Path+"?"+r.URL.RawQuery)
		reqMu.Unlock()

		switch {
		case strings.HasPrefix(r.URL.Path, "/api/mrc20/tick/info/"):
			// Simulate live manapi.metaid.io returning code=100 no result found.
			_, _ = w.Write([]byte(`{"code":100,"message":"no result found","data":null}`))
		case r.URL.Path == "/api-market/api/v1/common/mrc20/tick/info":
			tickId := r.URL.Query().Get("tickId")
			switch tickId {
			case "8ce590918a3d493631ab9d9e3bbc89e322f3dd08354af87f97c07218818b78f4i0":
				_, _ = w.Write([]byte(`{
					"code":0,
					"message":"ok",
					"data":{
						"tick":"METAID",
						"tokenName":"",
						"decimals":"8",
						"holders":728,
						"metaData":"{\"tick\":\"METAID\"}",
						"deployerUserInfo":{"avatar":"/content/ae28eedfc956e742f1f05aebbe0118916f2848fafa0e86e2f5d20781d628415bi0"},
						"floorPrice":"18.50"
					}
				}`))
			case "644dba0433aced0ec4cecef9baa951eccabb1751f222d48d33e7a309738ff0d2i0":
				_, _ = w.Write([]byte(`{
					"code":0,
					"message":"ok",
					"data":{
						"tick":"MC",
						"tokenName":"MetaCoin",
						"decimals":"8",
						"holders":243,
						"metaData":"{\"icon\":\"metafile://1763f42c089e0b81637d5843676dfe4272e6f5a46be8a96306ea1b696ed1e48fi0\"}",
						"floorPrice":"0"
					}
				}`))
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// 1. MetaidMarketFloorProvider with host-only BaseURL AND /api-market BaseURL.
	for _, base := range []string{srv.URL, srv.URL + "/api-market"} {
		fp := NewMetaidMarketFloorProvider(base, srv.Client())
		floor, ok := fp.FetchBtcUnitFloorSats(context.Background(), "8ce590918a3d493631ab9d9e3bbc89e322f3dd08354af87f97c07218818b78f4i0")
		if !ok || floor == nil || *floor != "18.5" {
			t.Fatalf("base=%s expected floor=18.5, got ok=%v floor=%v", base, ok, floor)
		}
		zeroFloor, okZero := fp.FetchBtcUnitFloorSats(context.Background(), "644dba0433aced0ec4cecef9baa951eccabb1751f222d48d33e7a309738ff0d2i0")
		if okZero || zeroFloor != nil {
			t.Fatalf("base=%s expected zero floor rejected, got ok=%v floor=%v", base, okZero, zeroFloor)
		}
	}

	// 2. UpstreamTokenMetadataProvider falls back from ManBaseURL (code=100) to MetaidMarketBaseURL.
	metaProv := NewUpstreamTokenMetadataProvider("", srv.URL, srv.Client()).WithFallbackURLs("", srv.URL)
	metaidMrc := "8ce590918a3d493631ab9d9e3bbc89e322f3dd08354af87f97c07218818b78f4i0"
	meta1, ok := metaProv.FetchTokenMetadata(context.Background(), AssetInfo{
		AssetKey: AssetKeyMetaidBtc,
		Chain:    "btc",
		Standard: "mrc20",
		Mrc20Id:  &metaidMrc,
	})
	if !ok || meta1 == nil {
		t.Fatalf("expected BTC metadata fallback success")
	}
	if meta1.HolderCount != 728 || meta1.Symbol != "METAID" || meta1.Decimals != 8 || meta1.Icon != IconMetaid {
		t.Fatalf("unexpected BTC METAID fallback metadata: %+v", meta1)
	}

	mcMrc := "644dba0433aced0ec4cecef9baa951eccabb1751f222d48d33e7a309738ff0d2i0"
	meta2, ok := metaProv.FetchTokenMetadata(context.Background(), AssetInfo{
		AssetKey: AssetKeyMcBtc,
		Chain:    "btc",
		Standard: "mrc20",
		Mrc20Id:  &mcMrc,
	})
	if !ok || meta2 == nil || meta2.HolderCount != 243 || meta2.TokenName != "MetaCoin" || meta2.Icon != IconMc {
		t.Fatalf("unexpected BTC MC fallback metadata: %+v", meta2)
	}

	reqMu.Lock()
	defer reqMu.Unlock()
	foundCommonPath := false
	for _, p := range requestedPaths {
		if strings.HasPrefix(p, "/api-market/api/v1/common/mrc20/tick/info?tickId=") {
			foundCommonPath = true
			break
		}
	}
	if !foundCommonPath {
		t.Fatalf("expected /api-market/api/v1/common/mrc20/tick/info request, got %v", requestedPaths)
	}
}

func TestMvcOpenAPIFallbackUtxoMetadataAndSensibleResolver(t *testing.T) {
	customCodehash := "a2421f1e90c6048c36745edd44fad682e8644693"
	customGenesis := "11223344556677889900aabbccddeeff00112233"
	customSensible := "deadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef00000000"
	aliveTx := testHex64("mvcaliveutxo")
	spentTx := testHex64("mvcspentutxo")
	sellerAddr := testSellerMvcAddr
	contractAddr := "1MvcContractAddr111111111111111111"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/contract/ft/address/" + sellerAddr + "/utxo":
			_, _ = fmt.Fprintf(w, `[{"txid":%q,"txIndex":0,"valueString":"100000000"}]`, aliveTx)
		case "/contract/ft/address/" + contractAddr + "/utxo":
			_, _ = fmt.Fprintf(w, `[{"txid":%q,"txIndex":2,"valueString":"500000000"}]`, aliveTx)
		case "/contract/ft/" + customCodehash + "/" + customGenesis + "/owners":
			_, _ = w.Write([]byte(`{"count":857,"records":[{"name":"","symbol":"CUST","decimal":8}]}`))
		case "/contract/ft/" + customCodehash + "/" + customGenesis + "/genesis":
			_, _ = fmt.Fprintf(w, `{"name":"Custom FT","symbol":"CUST","decimal":8,"sensibleId":%q}`, customSensible)
		case "/contract/ft/summary":
			_, _ = fmt.Fprintf(w, `{"total":1,"records":[{"codeHash":%q,"genesis":%q,"sensibleId":%q,"name":"Custom FT","symbol":"CUST","decimal":8}]}`,
				customCodehash, customGenesis, customSensible)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// 1. UTXO Verifier with HigunBaseURL == "" and MvcOpenAPIBaseURL == srv.URL.
	verifier := NewUpstreamUTXOVerifier("", "", srv.Client()).WithMvcOpenAPIBaseURL(srv.URL)
	baseAsset := &AssetInfo{
		AssetKey: "mvc-ft:" + customCodehash + ":" + customGenesis,
		Chain:    "mvc",
		Standard: "metacontract-ft",
		Codehash: &customCodehash,
		Genesis:  &customGenesis,
	}

	aliveRes := verifier.CheckListingUTXO(context.Background(), &SellListingRecord{
		Asset:         baseAsset,
		Lot:           &LotInfo{UtxoOutpoint: aliveTx + ":0"},
		SellerAddress: sellerAddr,
		Rails:         &SellRails{Me: &MeRail{AmountMilli: 1000}},
	})
	if !aliveRes.Checked || !aliveRes.Alive {
		t.Fatalf("expected wallet UTXO alive via MVC OpenAPI, got %+v", aliveRes)
	}

	spentRes := verifier.CheckListingUTXO(context.Background(), &SellListingRecord{
		Asset:         baseAsset,
		Lot:           &LotInfo{UtxoOutpoint: spentTx + ":0"},
		SellerAddress: sellerAddr,
		Rails:         &SellRails{Me: &MeRail{AmountMilli: 1000}},
	})
	if !spentRes.Checked || spentRes.Alive || spentRes.Reason != "utxo_spent" {
		t.Fatalf("expected wallet UTXO spent via MVC OpenAPI, got %+v", spentRes)
	}

	// Contract rail without ContractAddress degrades safely (Checked:false, Alive:true).
	contractNoAddr := verifier.CheckListingUTXO(context.Background(), &SellListingRecord{
		Asset:         baseAsset,
		Lot:           &LotInfo{UtxoOutpoint: spentTx + ":0"},
		SellerAddress: sellerAddr,
		Rails:         &SellRails{MvcTokenSell: &MvcTokenSellRail{SellTxid: spentTx, PriceSats: 1000}},
	})
	if contractNoAddr.Checked || !contractNoAddr.Alive {
		t.Fatalf("expected contract rail without contractAddress to degrade safely, got %+v", contractNoAddr)
	}

	// Contract rail WITH ContractAddress verifies against contract address UTXOs.
	contractWithAddr := verifier.CheckListingUTXO(context.Background(), &SellListingRecord{
		Asset:         baseAsset,
		Lot:           &LotInfo{UtxoOutpoint: aliveTx + ":2"},
		SellerAddress: sellerAddr,
		Rails: &SellRails{MvcTokenSell: &MvcTokenSellRail{
			SellTxid:        aliveTx,
			PriceSats:       1000,
			ContractAddress: contractAddr,
		}},
	})
	if !contractWithAddr.Checked || !contractWithAddr.Alive {
		t.Fatalf("expected contract rail with contractAddress alive, got %+v", contractWithAddr)
	}

	// 2. Metadata & dynamic sensibleId resolution when HigunBaseURL == "".
	metaProv := NewUpstreamTokenMetadataProvider("", "", srv.Client()).WithFallbackURLs(srv.URL, "")
	agg, _ := newTestAggregator(t)
	now := int64(1798000500)
	agg.nowFn = func() int64 { return now }
	agg.SetUTXOVerifier(verifier)
	agg.SetTokenMetadataProvider(metaProv)

	// Ingest a custom MVC FT pin that supplies only the 72-hex sensibleId; HandleBlockPin
	// must dynamically resolve it via /contract/ft/summary BEFORE acquiring a.mu!
	customPin := makeSellPin(testPinId("custsens", 0), 500, now, "mvc", map[string]any{
		"active":     true,
		"listingKey": "mvc-ft:" + customCodehash + ":" + customSensible + ":" + aliveTx + ":0",
		"asset": map[string]any{
			"assetKey":  "mvc-ft:" + customCodehash + ":" + customSensible,
			"chain":     "mvc",
			"standard":  "mvc-ft",
			"codehash":  customCodehash,
			"genesis":   customSensible,
			"symbol":    "CUST",
			"tokenName": "Custom FT",
			"decimals":  8,
		},
		"lot": map[string]any{
			"amountBase":    "100000000",
			"amountDisplay": "1",
			"utxoOutpoint":  aliveTx + ":0",
		},
		"sellerAddress": sellerAddr,
		"rails": map[string]any{
			"me": map[string]any{"amountMilli": 5000, "autoFulfill": true},
		},
	})
	if _, err := agg.HandleBlockPin(customPin); err != nil {
		t.Fatalf("HandleBlockPin customPin: %v", err)
	}

	r := setupRouter(agg)
	var sumResp struct {
		Code int                `json:"code"`
		Data TokenMarketSummary `json:"data"`
	}
	canonCustomKey := "mvc-ft:" + customCodehash + ":" + customGenesis
	httpCode := doJSONGet(t, r, "/api/ft/market/token/"+url.PathEscape(canonCustomKey)+"/summary", &sumResp)
	if httpCode != http.StatusOK || sumResp.Code != 0 {
		t.Fatalf("expected canonical 40-hex summary 200 code=0, got http=%d code=%d", httpCode, sumResp.Code)
	}
	if sumResp.Data.HolderCount != 857 || sumResp.Data.ListedLotCount != 1 {
		t.Fatalf("expected holderCount=857 and listedLotCount=1, got %+v", sumResp.Data)
	}
	if sumResp.Data.Asset.SensibleId == nil || *sumResp.Data.Asset.SensibleId != customSensible {
		t.Fatalf("expected sensibleId=%s, got %v", customSensible, sumResp.Data.Asset.SensibleId)
	}
}
