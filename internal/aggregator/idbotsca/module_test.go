package idbotsca

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
