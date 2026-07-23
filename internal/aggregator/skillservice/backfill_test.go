package skillservice

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBackfillDefaultsToSkillServiceAndUsesContentSummaryFallback(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	var requestedPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		requestedPaths = append(requestedPaths, path)
		w.Header().Set("Content-Type", "application/json")
		pins := []map[string]any{}
		if path == PathSkillService {
			pins = append(pins, map[string]any{
				"id":             "service-create:i0",
				"path":           PathSkillService,
				"operation":      OperationCreate,
				"contentBody":    "",
				"contentSummary": `{"serviceName":"weibo-hot-trend-service","displayName":"微博热搜","description":"获取微博热搜榜数据","providerSkill":"weibo-hot-trend","outputType":"text","price":"0.00001","currency":"SPACE","paymentChain":"mvc","settlementKind":"address","paymentAddress":"1Pay","disabled":false,"contextSchema":{"type":"object"}}`,
				"globalMetaId":   "idq-provider",
				"metaId":         "meta-provider",
				"address":        "1Provider",
				"createMetaId":   "meta-provider",
				"createAddress":  "1Provider",
				"chainName":      "mvc",
				"timestamp":      int64(1719705600),
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    1,
			"message": "ok",
			"data": map[string]any{
				"list":       pins,
				"nextCursor": "",
				"cursor":     "",
			},
		})
	}))
	defer server.Close()

	err := agg.Backfill(BackfillOptions{
		Context:  context.Background(),
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    time.Unix(1719700000, 0),
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if len(requestedPaths) == 0 || requestedPaths[0] != PathSkillService {
		t.Fatalf("first requested path = %v, want %s", requestedPaths, PathSkillService)
	}

	rec, err := agg.loadService("mvc", "service-create:i0")
	if err != nil {
		t.Fatalf("loadService: %v", err)
	}
	if rec == nil {
		t.Fatal("service was not backfilled from contentSummary")
	}
	if rec.SettlementKind != "address" {
		t.Fatalf("SettlementKind = %q, want address", rec.SettlementKind)
	}
	if got, ok := rec.DeclarationPayload["contextSchema"].(map[string]any); !ok || got["type"] != "object" {
		t.Fatalf("DeclarationPayload contextSchema = %#v, want preserved object", rec.DeclarationPayload["contextSchema"])
	}
	if disabled, ok := rec.DeclarationPayload["disabled"].(bool); !ok || disabled {
		t.Fatalf("DeclarationPayload disabled = %#v, want explicit false", rec.DeclarationPayload["disabled"])
	}
	if _, err := store.Get(NamespaceService, homepageProviderGlobalIndexStateKey()); err == nil {
		t.Fatal("service backfill left homepage provider-global index marked ready")
	}
}

func TestBackfillReplaysServicePinsOldestFirstWithinLookback(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		pins := []map[string]any{}
		if r.URL.Query().Get("path") == PathSkillService {
			pins = []map[string]any{
				skillBackfillServicePin("service-modify:i0", PathSkillService+"@service-create:i0", OperationModify, "service-create:i0", "v2", 1719705720),
				skillBackfillServicePin("service-create:i0", PathSkillService, OperationCreate, "", "v1", 1719705600),
				skillBackfillServicePin("service-old:i0", PathSkillService, OperationCreate, "", "old", 1719700000),
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    1,
			"message": "ok",
			"data": map[string]any{
				"list":       pins,
				"nextCursor": "",
				"cursor":     "",
			},
		})
	}))
	defer server.Close()

	err := agg.Backfill(BackfillOptions{
		Context:  context.Background(),
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    time.Unix(1719705500, 0),
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	rec, err := agg.loadService("mvc", "service-create:i0")
	if err != nil {
		t.Fatalf("loadService: %v", err)
	}
	if rec == nil {
		t.Fatal("expected folded service record")
	}
	if rec.CurrentPinId != "service-modify:i0" {
		t.Fatalf("CurrentPinId = %q, want service-modify:i0", rec.CurrentPinId)
	}
	if rec.DisplayName != "v2" {
		t.Fatalf("DisplayName = %q, want v2", rec.DisplayName)
	}
	if rec.CreatedAt != 1719705600 || rec.UpdatedAt != 1719705720 {
		t.Fatalf("timestamps = created %d updated %d, want 1719705600/1719705720", rec.CreatedAt, rec.UpdatedAt)
	}
	if old, err := agg.loadService("mvc", "service-old:i0"); err != nil || old != nil {
		t.Fatalf("old service = %#v err=%v, want skipped by cutoff", old, err)
	}
}

func TestBackfillDiscoversRecentSourceTargetVersionsAndUnlistedRevoke(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	weatherSource := skillBackfillServicePin("weather-source:i0", PathSkillService, OperationCreate, "", "旧天气服务", 1000)
	postSource := skillBackfillServicePin("post-source:i0", PathSkillService, OperationCreate, "", "旧发布服务", 1100)
	weatherFirst := skillBackfillServicePin("weather-first:i0", "@weather-source:i0", OperationModify, "weather-source:i0", "Weather v2", 4100)
	// Path results can reveal history that the source-list response omitted.
	weatherFirst["modify_history"] = []string{"weather-source:i0", "weather-first:i0", "weather-old:i0"}
	weatherLatest := skillBackfillServicePin("weather-en:i0", "@weather-old:i0", OperationModify, "weather-old:i0", "Free Weather Service", 5000)
	weatherLatest["contentBody"].(map[string]any)["providerSkill"] = []string{"weather"}
	// MANAPI's modify_history can omit revoke pins, so @source must always be queried.
	postRevoke := skillBackfillServicePin("post-revoke:i0", "@post-source:i0", OperationRevoke, "post-source:i0", "Publish Buzz", 4500)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var pins []map[string]any
		switch r.URL.Query().Get("path") {
		case PathSkillService:
			pins = []map[string]any{postSource, weatherSource}
		case "@weather-source:i0":
			pins = []map[string]any{weatherFirst}
		case "@weather-old:i0":
			pins = []map[string]any{weatherLatest}
		case "@post-source:i0":
			pins = []map[string]any{postRevoke}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 1,
			"data": map[string]any{"list": pins, "nextCursor": "", "cursor": ""},
		})
	}))
	defer server.Close()

	if err := agg.Backfill(BackfillOptions{
		Context: context.Background(), Client: NewBackfillClient(server.URL, server.Client()),
		Paths: []string{PathSkillService}, Since: time.Unix(4000, 0), PageSize: 100,
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	weather, err := agg.loadService("mvc", "weather-source:i0")
	if err != nil || weather == nil {
		t.Fatalf("weather service = %#v err=%v", weather, err)
	}
	if weather.CurrentPinId != "weather-en:i0" || weather.DisplayName != "Free Weather Service" || weather.ProviderSkill != "weather" {
		t.Fatalf("weather latest projection = %+v", weather)
	}
	post, err := agg.loadService("mvc", "post-source:i0")
	if err != nil || post == nil {
		t.Fatalf("post service = %#v err=%v", post, err)
	}
	if post.CurrentPinId != "post-revoke:i0" || post.Operation != OperationRevoke || post.IsVisibleDefault() {
		t.Fatalf("post revoke projection = %+v", post)
	}
}

func TestBackfillRefreshesLegacyCreateDeclarationPayload(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	legacy := &ServiceRecord{
		SourceServicePinId:   "service-create:i0",
		CurrentPinId:         "service-create:i0",
		ChainName:            "mvc",
		ProviderMetaId:       "meta-provider",
		ProviderGlobalMetaId: "idq-provider",
		ProviderAddress:      "1Provider",
		ServiceName:          "weibo-hot-trend-service",
		DisplayName:          "微博热搜",
		Description:          "old",
		ProviderSkill:        "weibo-hot-trend",
		Price:                "0.00001",
		Currency:             "SPACE",
		PaymentChain:         "mvc",
		SettlementKind:       "native",
		OutputType:           "text",
		PaymentAddress:       "1Pay",
		Status:               StatusConfirmed,
		Operation:            OperationCreate,
		CreatedAt:            1719705600,
		UpdatedAt:            1719705600,
	}
	if err := agg.saveService(legacy, nil); err != nil {
		t.Fatalf("save legacy service: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		pins := []map[string]any{}
		if r.URL.Query().Get("path") == PathSkillService {
			pins = []map[string]any{{
				"id":        "service-create:i0",
				"path":      PathSkillService,
				"operation": OperationCreate,
				"contentBody": map[string]any{
					"serviceName":     "weibo-hot-trend-service",
					"displayName":     "微博热搜",
					"description":     "获取微博热搜榜数据",
					"serviceIcon":     "",
					"providerMetaBot": "idq-provider",
					"providerSkill":   "weibo-hot-trend",
					"price":           "0.00001",
					"currency":        "SPACE",
					"paymentChain":    "mvc",
					"settlementKind":  "native",
					"mrc20Ticker":     nil,
					"mrc20Id":         nil,
					"skillDocument":   "",
					"inputType":       "text",
					"outputType":      "text",
					"endpoint":        "simplemsg",
					"paymentAddress":  "1Pay",
				},
				"globalMetaId":  "idq-provider",
				"metaId":        "meta-provider",
				"address":       "1Provider",
				"createMetaId":  "meta-provider",
				"createAddress": "1Provider",
				"chainName":     "mvc",
				"timestamp":     int64(1719705600),
			}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":    1,
			"message": "ok",
			"data": map[string]any{
				"list":       pins,
				"nextCursor": "",
				"cursor":     "",
			},
		})
	}))
	defer server.Close()

	err := agg.Backfill(BackfillOptions{
		Context:  context.Background(),
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    time.Unix(1719700000, 0),
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	rec, err := agg.loadService("mvc", "service-create:i0")
	if err != nil {
		t.Fatalf("loadService: %v", err)
	}
	if rec == nil {
		t.Fatal("service missing after backfill")
	}
	for key, want := range map[string]any{
		"serviceIcon":     "",
		"providerMetaBot": "idq-provider",
		"skillDocument":   "",
		"inputType":       "text",
		"endpoint":        "simplemsg",
	} {
		if got, exists := rec.DeclarationPayload[key]; !exists || got != want {
			t.Fatalf("DeclarationPayload[%q] = %#v exists=%v, want %#v; payload=%#v", key, got, exists, want, rec.DeclarationPayload)
		}
	}
	for _, key := range []string{"mrc20Ticker", "mrc20Id"} {
		if got, exists := rec.DeclarationPayload[key]; !exists || got != nil {
			t.Fatalf("DeclarationPayload[%q] = %#v exists=%v, want explicit null; payload=%#v", key, got, exists, rec.DeclarationPayload)
		}
	}
}

func skillBackfillServicePin(id, path, operation, originalID, displayName string, timestamp int64) map[string]any {
	return map[string]any{
		"id":         id,
		"path":       path,
		"operation":  operation,
		"originalId": originalID,
		"contentBody": map[string]any{
			"serviceName":    "svc-" + displayName,
			"displayName":    displayName,
			"description":    "desc " + displayName,
			"providerSkill":  "provider-" + displayName,
			"outputType":     "text",
			"price":          "1",
			"currency":       "SPACE",
			"paymentChain":   "mvc",
			"settlementKind": "native",
			"paymentAddress": "1Pay",
		},
		"globalMetaId":  "idq-provider",
		"metaId":        "meta-provider",
		"address":       "1Provider",
		"createMetaId":  "meta-provider",
		"createAddress": "1Provider",
		"chainName":     "mvc",
		"timestamp":     timestamp,
	}
}
