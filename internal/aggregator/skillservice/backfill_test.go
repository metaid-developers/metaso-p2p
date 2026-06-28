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
