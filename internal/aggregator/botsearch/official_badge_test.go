package botsearch

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type stubOfficialBadgeProvider struct {
	badges map[string]*OfficialBadge
}

func (s *stubOfficialBadgeProvider) GetOfficialBotBadge(globalMetaId string) *OfficialBadge {
	return s.badges[globalMetaId]
}

func TestOfficialRoleFilterAndBadgeProjection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })

	ui := &userinfo.Aggregator{}
	cacheProvider := cache.New(store)
	if err := ui.Init(store, cacheProvider); err != nil {
		t.Fatalf("init userinfo: %v", err)
	}
	const swapGID = "idq1swapstation0000000000000000000000000000"
	const plainGID = "idq1plainbot0000000000000000000000000000000"
	for _, bot := range []struct {
		gid, mid, addr, name string
	}{
		{swapGID, "1111111111111111111111111111111111111111111111111111111111111111", "1SwapAddr1111111111111111111111111", "OfficialSwap"},
		{plainGID, "2222222222222222222222222222222222222222222222222222222222222222", "1PlainAddr111111111111111111111111", "PlainBot"},
	} {
		_, _ = ui.HandleBlockPin(&aggregator.PinInscription{
			Id:            bot.mid[:60] + "0001i0",
			Operation:     "create",
			Path:          "/info/name",
			MetaId:        bot.mid,
			GlobalMetaId:  bot.gid,
			Address:       bot.addr,
			CreateAddress: bot.addr,
			GenesisHeight: 100,
			Timestamp:     1760000000,
			ContentBody:   []byte(bot.name),
		})
	}

	bs := &Aggregator{}
	if err := bs.Init(store, nil); err != nil {
		t.Fatalf("init botsearch: %v", err)
	}
	bs.SetProfileSource(ui)
	bs.SetOfficialBadgeProvider(&stubOfficialBadgeProvider{
		badges: map[string]*OfficialBadge{
			swapGID: {
				Verified:   true,
				Role:       "swap_station",
				BadgeType:  "official_blue",
				BadgeLabel: "Official Swap Station",
			},
		},
	})

	r := gin.New()
	bs.RegisterRoutes(r.Group("/api"))

	body, _ := json.Marshal(map[string]any{
		"officialRole":  "swap_station",
		"onlineOnly":    false,
		"hasChatPubkey": false,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/bots/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	var resp searchTestBody
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != 0 || len(resp.Data.Candidates) != 1 {
		t.Fatalf("expected 1 swap_station candidate, got code=%d len=%d", resp.Code, len(resp.Data.Candidates))
	}
	cand := resp.Data.Candidates[0]
	if cand.GlobalMetaId != swapGID || cand.OfficialBadge == nil || !cand.OfficialBadge.Verified || cand.OfficialBadge.Role != "swap_station" {
		t.Fatalf("unexpected candidate badge: %+v", cand)
	}
}
