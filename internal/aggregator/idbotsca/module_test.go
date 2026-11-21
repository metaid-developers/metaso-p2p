package idbotsca

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func newTestCAAggregator(t *testing.T) *Aggregator {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	agg := &Aggregator{}
	if err := agg.Init(store, nil); err != nil {
		t.Fatalf("init idbotsca: %v", err)
	}
	return agg
}

func makeCAPin(pinID, op, metaID, globalMetaID, addr string, height int64, body any) *aggregator.PinInscription {
	raw, _ := json.Marshal(body)
	return &aggregator.PinInscription{
		Id:             pinID,
		Operation:      op,
		Path:           PathCA,
		MetaId:         metaID,
		GlobalMetaId:   globalMetaID,
		Address:        addr,
		GenesisHeight:  height,
		Timestamp:      1760000000 + height,
		ContentSummary: string(raw),
		ContentBody:    raw,
		ChainName:      "mvc",
		Output:         "deadbeef:0",
	}
}

func TestTrustedPublisherAcceptedAndForgedRejected(t *testing.T) {
	agg := newTestCAAggregator(t)

	forgedPin := makeCAPin(
		"forged0001i0",
		"create",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"idq1forgedidentity000000000000000000000000000",
		"1ForgedAddress111111111111111111",
		100,
		map[string]any{
			"collections": []map[string]any{{
				"collectionPinId": "badcol0001i0",
				"displayTitle":    "Fake Official",
			}},
		},
	)
	if _, err := agg.HandleBlockPin(forgedPin); err != nil {
		t.Fatalf("forged pin returned error: %v", err)
	}
	if agg.HasConfirmedRegistry() {
		t.Fatal("forged publisher must not populate confirmed registry")
	}

	validPin := makeCAPin(
		DefaultGenesisPinID,
		"create",
		OfficialMetaID,
		OfficialGlobalMetaID,
		OfficialMVCAddress,
		200,
		map[string]any{
			"collections": []map[string]any{{
				"collectionPinId": "col123i0",
				"collectionName":  "MetaBot",
				"displayTitle":    "MetaBot Genesis",
				"certifiedAt":     1760000200,
			}},
			"officialBots": []map[string]any{{
				"globalMetaId": "idq1swapbot0000000000000000000000000000000",
				"metaId":       "swapbotmetaid",
				"mvcAddress":   "1SwapBotAddr",
				"role":         "swap_station",
				"badgeType":    "official_blue",
				"badgeLabel":   "Official Swap Station",
				"active":       true,
			}, {
				"globalMetaId": "idq1inactive00000000000000000000000000000",
				"role":         "escrow_guarantor",
				"badgeType":    "official_blue",
				"badgeLabel":   "Inactive Escrow",
				"active":       false,
			}},
		},
	)
	if _, err := agg.HandleBlockPin(validPin); err != nil {
		t.Fatalf("valid pin returned error: %v", err)
	}
	if !agg.HasConfirmedRegistry() {
		t.Fatal("expected confirmed registry after Bob CA pin")
	}

	ok, entry := agg.IsCollectionCertified("COL123I0")
	if !ok || entry == nil || entry.DisplayTitle != "MetaBot Genesis" {
		t.Fatalf("expected col123i0 certified, got ok=%v entry=%+v", ok, entry)
	}

	badge := agg.GetOfficialBotBadge("idq1swapbot0000000000000000000000000000000")
	if badge == nil || !badge.Verified || badge.Role != "swap_station" {
		t.Fatalf("expected verified badge for active swap bot, got %+v", badge)
	}
	if inactive := agg.GetOfficialBotBadge("idq1inactive00000000000000000000000000000"); inactive != nil {
		t.Fatalf("expected nil badge for inactive official bot, got %+v", inactive)
	}

	// Forged modify must be discarded and must not overwrite Bob's registry.
	forgedModify := makeCAPin(
		"forgedmod1i0",
		"modify",
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"idq1attacker000000000000000000000000000000",
		"1AttackerAddr",
		250,
		map[string]any{
			"collections": []map[string]any{},
		},
	)
	if _, err := agg.HandleBlockPin(forgedModify); err != nil {
		t.Fatalf("forged modify error: %v", err)
	}
	if ok, _ := agg.IsCollectionCertified("col123i0"); !ok {
		t.Fatal("forged modify overwrote valid CA registry")
	}

	// Older height modify from Bob must not regress newer registry; newer height updates.
	olderModify := makeCAPin(
		"bobolder0i0",
		"modify",
		OfficialMetaID,
		OfficialGlobalMetaID,
		OfficialMVCAddress,
		150,
		map[string]any{
			"collections": []map[string]any{},
		},
	)
	if _, err := agg.HandleBlockPin(olderModify); err != nil {
		t.Fatalf("older modify error: %v", err)
	}
	if ok, _ := agg.IsCollectionCertified("col123i0"); !ok {
		t.Fatal("older modify should not overwrite newer registry")
	}

	// HTTP endpoint GET /api/mrc721/ca/registry
	gin.SetMode(gin.TestMode)
	r := gin.New()
	apiGroup := r.Group("/api")
	agg.RegisterRoutes(apiGroup)

	req := httptest.NewRequest(http.MethodGet, "/api/mrc721/ca/registry", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Verified       bool                  `json:"verified"`
			OfficialMetaId string                `json:"officialMetaId"`
			Collections    []CollectionCertEntry `json:"collections"`
			OfficialBots   []OfficialBotEntry    `json:"officialBots"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != 0 || !resp.Data.Verified || resp.Data.OfficialMetaId != OfficialMetaID {
		t.Fatalf("unexpected CA registry envelope: %+v", resp)
	}
	if len(resp.Data.Collections) != 1 || len(resp.Data.OfficialBots) != 2 {
		t.Fatalf("unexpected CA registry payload counts: cols=%d bots=%d", len(resp.Data.Collections), len(resp.Data.OfficialBots))
	}
}

func TestStaleMempoolCleanupAndOnChangeCallback(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })

	now := int64(1760000000)
	agg := &Aggregator{nowFn: func() int64 { return now }}
	if err := agg.Init(store, nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	changes := 0
	agg.SetOnChange(func() {
		changes++
	})

	// 1. Confirmed pin at t=1000
	_, err := agg.HandleBlockPin(makeCAPin(
		"conf1i0", "create", OfficialMetaID, OfficialGlobalMetaID, OfficialMVCAddress, 100,
		map[string]any{"collections": []map[string]any{{"collectionPinId": "colConf", "displayTitle": "Confirmed"}}},
	))
	if err != nil {
		t.Fatalf("block pin: %v", err)
	}
	if changes != 1 {
		t.Fatalf("expected 1 onChange after block pin, got %d", changes)
	}

	// 2. Mempool pin at t=1100 (Timestamp > confirmed)
	mpPin := makeCAPin(
		"mp1i0", "modify", OfficialMetaID, OfficialGlobalMetaID, OfficialMVCAddress, -1,
		map[string]any{"collections": []map[string]any{{"collectionPinId": "colMem", "displayTitle": "Mempool"}}},
	)
	mpPin.Timestamp = 1760001000
	if _, err := agg.HandleMempoolPin(mpPin); err != nil {
		t.Fatalf("mempool pin: %v", err)
	}
	if ok, _ := agg.IsCollectionCertified("colMem"); !ok {
		t.Fatal("expected mempool collection certified before timeout")
	}

	// Advance clock by 31 minutes -> stale mempool auto-cleaned on read and triggers onChange!
	now += 31 * 60
	if ok, _ := agg.IsCollectionCertified("colMem"); ok {
		t.Fatal("expected stale mempool (>30m) evicted")
	}
	if ok, _ := agg.IsCollectionCertified("colConf"); !ok {
		t.Fatal("expected confirmed registry restored after stale mempool eviction")
	}
	if changes < 3 {
		t.Fatalf("expected onChange fired on eviction, got %d", changes)
	}

	// 3. Stale mempool with UpdatedAt < confirmed.UpdatedAt is evicted on read too.
	agg.mu.Lock()
	agg.mempool = &RegistrySnapshot{
		Verified:  true,
		PinId:     "oldmp1i0",
		UpdatedAt: 100,
		Collections: []CollectionCertEntry{{
			CollectionPinId: "colStaleTs",
		}},
	}
	agg.mempoolSeenAt = now
	agg.rebuildEffectiveLocked()
	agg.mu.Unlock()
	if ok, _ := agg.IsCollectionCertified("colStaleTs"); ok {
		t.Fatal("expected mempool with older UpdatedAt than confirmed to be evicted")
	}
}

func TestLiveMVCAddressPublisherAndTransferredPinRejection(t *testing.T) {
	agg := newTestCAAggregator(t)

	// 1. Live MVC ZMQ mempool pin where mvc/indexer.go populates Address,
	// CreateAddress, MetaId, and GlobalMetaId all with Bob's raw Base58 MVC address.
	rawPayload, _ := json.Marshal(map[string]any{
		"collections": []map[string]any{{
			"collectionPinId": "26b335096639b7f049138501bd10ed6f1a315efd081d787a1c7b74ae6d70ffd8i0",
			"collectionName":  "metabot-avatar",
			"displayTitle":    "MetaBot Avatar",
		}},
		"officialBots": []map[string]any{{
			"globalMetaId": "idq1gtxzx45ktj8s738s6wjak0ha27d98g8h2n059n",
			"mvcAddress":   "176C9RPWDggnvdVcWG3wrZEJcm1bHTcKM5",
			"role":         "swap_station",
			"badgeType":    "official_swap_station",
			"badgeLabel":   "官方兑换站",
			"active":       true,
		}},
	})
	liveMempoolPin := &aggregator.PinInscription{
		Id:            "651cf54aa6b3f0e5bf651a2cb94c0e6c5479378df0118bbe180847c27916be88i0",
		Operation:     "create",
		Path:          PathCA,
		ChainName:     "mvc",
		GenesisHeight: -1,
		Timestamp:     1795249500,
		Address:       OfficialMVCAddress,
		CreateAddress: OfficialMVCAddress,
		MetaId:        OfficialMVCAddress,
		GlobalMetaId:  OfficialMVCAddress,
		ContentBody:   rawPayload,
	}
	if _, err := agg.HandleMempoolPin(liveMempoolPin); err != nil {
		t.Fatalf("live MVC mempool pin failed: %v", err)
	}
	if ok, _ := agg.IsCollectionCertified("26b335096639b7f049138501bd10ed6f1a315efd081d787a1c7b74ae6d70ffd8i0"); !ok {
		t.Fatal("expected collection certified from live MVC mempool pin")
	}
	if badge := agg.GetOfficialBotBadge("idq1gtxzx45ktj8s738s6wjak0ha27d98g8h2n059n"); badge == nil || !badge.Verified {
		t.Fatalf("expected official swap_station badge from live MVC mempool pin, got %+v", badge)
	}

	// 2. Live MVC block confirmation with the same address-only identity shape persists cleanly.
	liveBlockPin := *liveMempoolPin
	liveBlockPin.GenesisHeight = 199735
	if _, err := agg.HandleBlockPin(&liveBlockPin); err != nil {
		t.Fatalf("live MVC block pin failed: %v", err)
	}
	if !agg.HasConfirmedRegistry() {
		t.Fatal("expected confirmed registry after live MVC block pin")
	}

	// 3. Attacker pin transferred to Bob (owner Address/MetaId == Bob, but Creator/CreateMetaId == Attacker) must fail closed.
	spoofPayload, _ := json.Marshal(map[string]any{
		"collections": []map[string]any{{
			"collectionPinId": "spoofedcol0001i0",
		}},
	})
	transferredSpoofPin := &aggregator.PinInscription{
		Id:            "spoofedtx0001i0",
		Operation:     "create",
		Path:          PathCA,
		ChainName:     "mvc",
		GenesisHeight: 199999,
		Timestamp:     1796000000,
		Address:       OfficialMVCAddress,
		MetaId:        OfficialMetaID,
		GlobalMetaId:  OfficialGlobalMetaID,
		Creator:       "1AttackerAddress11111111111111111",
		CreateMetaId:  "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
		ContentBody:   spoofPayload,
	}
	if _, err := agg.HandleBlockPin(transferredSpoofPin); err != nil {
		t.Fatalf("transferred spoof pin returned error: %v", err)
	}
	if ok, _ := agg.IsCollectionCertified("spoofedcol0001i0"); ok {
		t.Fatal("transferred attacker pin must never be accepted as Bob CA registry")
	}
}

func TestSyncFromMANAPIUpgradesExistingBaselineAndHydratesTruncatedBody(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewPebbleStore(dir)

	agg := &Aggregator{}
	if err := agg.Init(store, nil); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Seed an older empty baseline (simulating d0c042b3... at height 197309).
	oldPin := makeCAPin(
		"d0c042b3b54982322203fa1245f12bc3dd128c95fc74aca27f3f5b5004a38a65i0",
		"create",
		OfficialMetaID,
		OfficialGlobalMetaID,
		OfficialMVCAddress,
		197309,
		map[string]any{"collections": []any{}},
	)
	oldPin.Timestamp = 1793775354
	if _, err := agg.HandleBlockPin(oldPin); err != nil {
		t.Fatalf("seed old pin: %v", err)
	}
	_ = store.Close()

	// Reopen from Pebble (verifies persisted Height + non-nil empty arrays) and sync newer MANAPI pin.
	store2 := storage.NewPebbleStore(dir)
	t.Cleanup(func() { _ = store2.Close() })
	agg2 := &Aggregator{}
	if err := agg2.Init(store2, nil); err != nil {
		t.Fatalf("re-init: %v", err)
	}
	initialSnap := agg2.Registry()
	if initialSnap.Collections == nil || initialSnap.OfficialBots == nil {
		t.Fatal("expected non-nil empty slices after reloading empty registry from Pebble")
	}

	newPinID := "651cf54aa6b3f0e5bf651a2cb94c0e6c5479378df0118bbe180847c27916be88i0"
	fullV11Raw, _ := json.Marshal(map[string]any{
		"collections": []map[string]any{{
			"collectionPinId": "26b335096639b7f049138501bd10ed6f1a315efd081d787a1c7b74ae6d70ffd8i0",
			"collectionName":  "metabot-avatar",
			"displayTitle":    "MetaBot 官方初代头像",
		}},
		"officialBots": []map[string]any{{
			"globalMetaId": "idq1gtxzx45ktj8s738s6wjak0ha27d98g8h2n059n",
			"mvcAddress":   "176C9RPWDggnvdVcWG3wrZEJcm1bHTcKM5",
			"role":         "swap_station",
			"badgeType":    "official_swap_station",
			"badgeLabel":   "官方兑换站",
			"active":       true,
		}},
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/pin/path/list":
			// Simulate MANAPI returning lowercase "metaid", empty contentBody, and truncated contentSummary.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    1,
				"message": "ok",
				"data": map[string]any{
					"list": []map[string]any{
						{
							"id":             newPinID,
							"operation":      "create",
							"path":           PathCA,
							"metaid":         OfficialMetaID,
							"createMetaId":   OfficialMetaID,
							"creator":        OfficialMVCAddress,
							"globalMetaId":   OfficialGlobalMetaID,
							"address":        OfficialMVCAddress,
							"genesisHeight":  199735,
							"timestamp":      1795249500,
							"contentBody":    "",
							"contentLength":  len(fullV11Raw),
							"contentSummary": string(fullV11Raw[:48]), // truncated summary!
						},
						{
							"id":             oldPin.Id,
							"operation":      "create",
							"path":           PathCA,
							"metaid":         OfficialMetaID,
							"createMetaId":   OfficialMetaID,
							"creator":        OfficialMVCAddress,
							"globalMetaId":   OfficialGlobalMetaID,
							"address":        OfficialMVCAddress,
							"genesisHeight":  197309,
							"timestamp":      1793775354,
							"contentBody":    "",
							"contentLength":  18,
							"contentSummary": `{"collections":[]}`,
						},
					},
				},
			})
		case "/content/" + newPinID:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(fullV11Raw)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if err := agg2.BackfillIfEmpty(nil, srv.URL, srv.Client()); err != nil {
		t.Fatalf("SyncFromMANAPI failed: %v", err)
	}

	updated := agg2.Registry()
	if updated.PinId != newPinID {
		t.Fatalf("expected registry upgraded to %s, got %s", newPinID, updated.PinId)
	}
	if len(updated.Collections) != 1 || len(updated.OfficialBots) != 1 {
		t.Fatalf("expected 1 collection and 1 officialBot after hydration, got cols=%d bots=%d", len(updated.Collections), len(updated.OfficialBots))
	}

	// Verify that a newer confirmed block pin (height 200100) is NOT downgraded if MANAPI still serves 199735.
	newerPin := makeCAPin(
		"newerpin200100i0",
		"create",
		OfficialMetaID,
		OfficialGlobalMetaID,
		OfficialMVCAddress,
		200100,
		map[string]any{
			"collections": []map[string]any{{
				"collectionPinId": "col200100i0",
				"displayTitle":    "Newer Block State",
			}},
		},
	)
	newerPin.Timestamp = 1796000000
	if _, err := agg2.HandleBlockPin(newerPin); err != nil {
		t.Fatalf("apply newer block pin: %v", err)
	}
	if err := agg2.SyncFromMANAPI(nil, srv.URL, srv.Client()); err != nil {
		t.Fatalf("re-sync against lagging MANAPI failed: %v", err)
	}
	if got := agg2.Registry().PinId; got != "newerpin200100i0" {
		t.Fatalf("lagging MANAPI must not downgrade newer confirmed block state, got %s", got)
	}
}

func TestCertifiedTokensLookupAndRegistryProjection(t *testing.T) {
	agg := newTestCAAggregator(t)
	assetKey := "mrc20:4f92000000000000000000000000000000000000000000000000000000000001i0"
	pin := makeCAPin(
		"tokenca1i0",
		"create",
		OfficialMetaID,
		OfficialGlobalMetaID,
		OfficialMVCAddress,
		300,
		map[string]any{
			"collections":  []any{},
			"officialBots": []any{},
			"certifiedTokens": []map[string]any{{
				"assetKey":    assetKey,
				"chain":       "btc",
				"standard":    "mrc20",
				"symbol":      "METAID",
				"tokenName":   "MetaID Coin",
				"displayName": "MetaID Official",
				"decimals":    8,
				"icon":        "metafile://icon01i0",
			}},
		},
	)
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}
	ok, entry := agg.IsTokenCertified(strings.ToUpper(assetKey))
	if !ok || entry == nil || entry.DisplayName != "MetaID Official" || entry.Decimals != 8 {
		t.Fatalf("expected certified token entry, got ok=%v entry=%+v", ok, entry)
	}
	if okUnknown, _ := agg.IsTokenCertified("mrc20:unknown"); okUnknown {
		t.Fatal("expected unknown token not certified")
	}
}

