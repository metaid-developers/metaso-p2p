package social

// Real-chain mempool verification harness (spec 2026-10-29 §4.4b acceptance):
//
//	METASO_REAL_VERIFY=1 \
//	METASO_VERIFY_PIN_ID=<txid>i0 \
//	METASO_VERIFY_PUBLISHER_GID=<follower globalMetaId> \
//	METASO_VERIFY_TARGET_GID=<target globalMetaId> \
//	METASO_VERIFY_TS=<unix seconds> \
//	METASO_VERIFY_STORE=<dir shared by both phases> \
//	go test ./internal/aggregator/social/ -run TestRealChainMempool -v
//
// Phase 1 (pending) runs while the real /follow tx sits unconfirmed in the
// MVC mempool: the mempool pin drives HandleMempoolPin (the exact entry
// point the engine's RouteMempoolPin calls), then the REAL gin handler must
// answer the following list with a pending edge carrying the real pin id.
// The only non-production hop is mempool discovery: no MVC node RPC is
// reachable from this machine, so the pin facts (id/payload/identities) are
// supplied from the broadcast instead of polled — aggregator code paths are
// identical.
//
// Phase 2 (confirmed) runs after the block lands: the production Backfill
// replay (manapi transport) folds the confirmed pin, which must consume the
// overlay and serve the same edge confirmed, pending gone.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const defaultManapiBase = "https://manapi.metaid.io"

type realVerifyEnv struct {
	enabled   bool
	pinId     string
	publisher string
	target    string
	ts        int64
	storeDir  string
	manapi    string
}

func loadRealVerifyEnv(t *testing.T) realVerifyEnv {
	t.Helper()
	env := realVerifyEnv{
		enabled:   os.Getenv("METASO_REAL_VERIFY") == "1",
		pinId:     strings.TrimSpace(os.Getenv("METASO_VERIFY_PIN_ID")),
		publisher: strings.TrimSpace(os.Getenv("METASO_VERIFY_PUBLISHER_GID")),
		target:    strings.TrimSpace(os.Getenv("METASO_VERIFY_TARGET_GID")),
		storeDir:  strings.TrimSpace(os.Getenv("METASO_VERIFY_STORE")),
		manapi:    strings.TrimSpace(os.Getenv("MANAPI_BASE")),
	}
	if env.manapi == "" {
		env.manapi = defaultManapiBase
	}
	if ts := strings.TrimSpace(os.Getenv("METASO_VERIFY_TS")); ts != "" {
		var err error
		env.ts, err = strconv.ParseInt(ts, 10, 64)
		if err != nil {
			t.Fatalf("METASO_VERIFY_TS: %v", err)
		}
	}
	if !env.enabled {
		t.Skip("real-chain verification harness: set METASO_REAL_VERIFY=1 (see file comment)")
	}
	if env.pinId == "" || env.publisher == "" || env.target == "" || env.ts == 0 || env.storeDir == "" {
		t.Fatalf("missing harness env: need PIN_ID/PUBLISHER_GID/TARGET_GID/TS/STORE")
	}
	return env
}

func newRealVerifyAggregator(t *testing.T, env realVerifyEnv) (*gin.Engine, *Aggregator) {
	t.Helper()
	store := storage.NewPebbleStore(env.storeDir)
	t.Cleanup(func() { _ = store.Close() })
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	// Real identities; the profile plumbing is stubbed because this harness
	// verifies the social overlay path, not the userinfo store.
	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			env.publisher: {GlobalMetaId: env.publisher},
			env.target:    {GlobalMetaId: env.target},
		},
	})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))
	return router, agg
}

func realVerifyPin(env realVerifyEnv) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:           env.pinId,
		Path:         "/follow",
		Operation:    "create",
		GlobalMetaId: env.publisher,
		ChainName:    "mvc",
		Timestamp:    env.ts,
		ContentBody:  []byte(env.target),
	}
}

func getFollowing(t *testing.T, router *gin.Engine, subject string) map[string]interface{} {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/social/globalmetaid/"+subject+"/following?view=profile&size=50", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("following HTTP %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

func TestRealChainMempoolPhase1Pending(t *testing.T) {
	env := loadRealVerifyEnv(t)
	router, agg := newRealVerifyAggregator(t, env)

	if _, err := agg.HandleMempoolPin(realVerifyPin(env)); err != nil {
		t.Fatalf("HandleMempoolPin(real pin): %v", err)
	}

	body := getFollowing(t, router, env.publisher)
	data, _ := dataMap(t, body)
	list, _ := data["list"].([]interface{})
	found := false
	for _, raw := range list {
		item, _ := raw.(map[string]interface{})
		if item["globalMetaId"] != env.target {
			continue
		}
		found = true
		if item["pending"] != true {
			t.Fatalf("edge pending = %v, want true (unconfirmed window)", item["pending"])
		}
		if item["followPinId"] != env.pinId {
			t.Fatalf("followPinId = %v, want %s", item["followPinId"], env.pinId)
		}
	}
	if !found {
		t.Fatalf("pending edge to %s not visible in following list", env.target)
	}

	rel, err := agg.Relationship(RelationshipParams{SourceGlobalMetaId: env.publisher, TargetGlobalMetaId: env.target})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if !rel.Source.FollowsTarget || !rel.Source.Pending || rel.Source.FollowPinId != env.pinId {
		t.Fatalf("relationship = %+v, want pending follow %s", rel.Source, env.pinId)
	}

	out, _ := json.Marshal(body)
	fmt.Printf("PHASE1_PENDING_OK %s\n", out)
}

func TestRealChainMempoolPhase2Confirmed(t *testing.T) {
	env := loadRealVerifyEnv(t)
	router, agg := newRealVerifyAggregator(t, env)

	// Overlay entry from phase 1 must still be there (shared store).
	if entry := agg.loadPendingFollow(env.publisher, env.target); entry == nil || entry.FollowPinId != env.pinId {
		t.Fatalf("phase-2 prerequisite: overlay entry missing/changed: %+v", entry)
	}

	// Production backfill replay over manapi folds the confirmed pin and
	// must consume the overlay (spec §4.4b confirmation transition).
	since := time.Unix(env.ts, 0).Add(-2 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := agg.Backfill(BackfillOptions{
		Context:  ctx,
		Client:   NewBackfillClient(env.manapi, nil),
		Paths:    DefaultBackfillPaths(),
		Since:    since,
		PageSize: 100,
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if entry := agg.loadPendingFollow(env.publisher, env.target); entry != nil {
		t.Fatalf("overlay survived confirmed fold: %+v", entry)
	}

	body := getFollowing(t, router, env.publisher)
	data, _ := dataMap(t, body)
	list, _ := data["list"].([]interface{})
	found := false
	for _, raw := range list {
		item, _ := raw.(map[string]interface{})
		if item["globalMetaId"] != env.target {
			continue
		}
		found = true
		if pending, ok := item["pending"]; ok && pending == true {
			t.Fatalf("edge still pending after confirmed fold: %+v", item)
		}
		if item["followPinId"] != env.pinId {
			t.Fatalf("followPinId = %v after confirm, want %s (seamless transition)", item["followPinId"], env.pinId)
		}
	}
	if !found {
		t.Fatalf("confirmed edge to %s missing after backfill", env.target)
	}

	edge, err := agg.loadEffectiveEdge(env.publisher, env.target)
	if err != nil || edge == nil || edge.Pending || edge.FollowPinId != env.pinId {
		t.Fatalf("effective edge after confirm = (%v, %v), want confirmed %s", edge, err, env.pinId)
	}

	out, _ := json.Marshal(body)
	fmt.Printf("PHASE2_CONFIRMED_OK %s\n", out)
}

func dataMap(t *testing.T, body map[string]interface{}) (map[string]interface{}, bool) {
	t.Helper()
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("response missing data: %v", body)
	}
	return data, true
}
