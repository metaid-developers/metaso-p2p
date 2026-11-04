package mrc721

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

// manapiSellServer serves /pin/path/list for the sell path, newest-first,
// exactly the MANAPI ordering the backfill consumes.
func manapiSellServer(t *testing.T, pinsNewestFirst []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/path/list" {
			t.Errorf("request path: got %q want /pin/path/list", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if got := r.URL.Query().Get("path"); got != PathSell {
			t.Errorf("path param: got %q want %q", got, PathSell)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code": 1,
			"data": map[string]any{
				"list":       pinsNewestFirst,
				"nextCursor": "",
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
}

func manapiSellPin(id string, height int64, timestampMillis int64, body map[string]any, operation string) map[string]any {
	if operation == "" {
		operation = "create"
	}
	return map[string]any{
		"id":             id,
		"path":           PathSell,
		"operation":      operation,
		"contentType":    "application/json",
		"contentBody":    body,
		"contentSummary": "",
		"metaId":         "",
		"globalMetaId":   "",
		"address":        "",
		"createMetaId":   "",
		"createAddress":  testSeller1,
		"chainName":      "mvc",
		"timestamp":      timestampMillis,
		"genesisHeight":  height,
		"originalId":     "",
	}
}

// The MANAPI listing is newest-first; the replay must apply oldest-first so
// the latest-wins record converges to the same state as live aggregation.
func TestBackfillSellReplaysOldestFirst(t *testing.T) {
	agg := newTestAggregator(t)

	itemA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + "i0"
	itemB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" + "i0"

	base := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC).UnixMilli()
	newestFirst := []map[string]any{
		manapiSellPin(testSellPinId("bf-itemB-cancel", 0), 905, base+5000, map[string]any{
			"itemPinId": itemB, "sellerAddress": testSeller1, "active": false,
		}, ""),
		manapiSellPin(testSellPinId("bf-itemB-declare", 0), 902, base+2000, map[string]any{
			"itemPinId": itemB, "sellerAddress": testSeller1, "active": true, "priceHintSats": 1000,
		}, ""),
		manapiSellPin(testSellPinId("bf-itemA-declare", 0), 901, base+1000, map[string]any{
			"itemPinId": itemA, "sellerAddress": testSeller1, "active": true,
		}, ""),
	}
	server := manapiSellServer(t, newestFirst)
	defer server.Close()

	stats := &SellBackfillStats{}
	err := agg.BackfillSell(SellBackfillOptions{
		Context:  context.Background(),
		Client:   publishedcontent.NewBackfillClient(server.URL, server.Client()),
		PageSize: 2,
		Stats:    stats,
	})
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.Fetched != 3 || stats.Applied != 3 || stats.Errors != 0 {
		t.Fatalf("stats = %+v", stats)
	}

	recA, _ := agg.SellIntentInfo(itemA)
	if recA == nil || !recA.Active {
		t.Fatalf("itemA record: %+v", recA)
	}
	recB, _ := agg.SellIntentInfo(itemB)
	if recB == nil || recB.Active || recB.SellPinId != testSellPinId("bf-itemB-cancel", 0) {
		t.Fatalf("itemB must end cancelled by its newer declaration: %+v", recB)
	}
	if recB.Height != 905 || recB.TxID != hexOf("bf-itemB-cancel") {
		t.Fatalf("itemB chain facts: %+v", recB)
	}
}

// Re-running the backfill converges (idempotent): same-pin re-applies are
// no-ops.
func TestBackfillSellIdempotent(t *testing.T) {
	agg := newTestAggregator(t)

	pins := []map[string]any{
		manapiSellPin(testSellPinId("idem-second", 0), 902, 1793868200000, map[string]any{
			"itemPinId": validItemPinId, "sellerAddress": testSeller1, "active": true,
		}, ""),
		manapiSellPin(testSellPinId("idem-first", 0), 901, 1793868100000, map[string]any{
			"itemPinId": validItemPinId, "sellerAddress": testSeller1, "active": true,
		}, ""),
	}
	server := manapiSellServer(t, pins)
	defer server.Close()

	opts := func() SellBackfillOptions {
		return SellBackfillOptions{
			Context:  context.Background(),
			Client:   publishedcontent.NewBackfillClient(server.URL, server.Client()),
			PageSize: 100,
		}
	}
	if err := agg.BackfillSell(opts()); err != nil {
		t.Fatalf("first backfill: %v", err)
	}
	stats := &SellBackfillStats{}
	run := opts()
	run.Stats = stats
	if err := agg.BackfillSell(run); err != nil {
		t.Fatalf("second backfill: %v", err)
	}
	if stats.Applied != 0 || stats.Skipped != 2 {
		t.Fatalf("second run must be a no-op, got %+v", stats)
	}
	rec, _ := agg.SellIntentInfo(validItemPinId)
	if rec == nil || rec.SellPinId != testSellPinId("idem-second", 0) {
		t.Fatalf("record after idempotent rerun: %+v", rec)
	}
}

// Invalid payloads inside the MANAPI listing are skipped without failing
// the run; non-create ops never reach the index.
func TestBackfillSellSkipsInvalidAndNonCreate(t *testing.T) {
	agg := newTestAggregator(t)

	pins := []map[string]any{
		manapiSellPin(testSellPinId("bf-invalid", 0), 903, 1793868300000, map[string]any{
			"active": true, "note": "missing itemPinId and sellerAddress",
		}, ""),
		manapiSellPin(testSellPinId("bf-modify", 0), 902, 1793868200000, map[string]any{
			"itemPinId": validItemPinId, "sellerAddress": testSeller1, "active": false,
		}, "modify"),
		manapiSellPin(testSellPinId("bf-good", 0), 901, 1793868100000, map[string]any{
			"itemPinId": validItemPinId, "sellerAddress": testSeller1, "active": true,
		}, ""),
	}
	server := manapiSellServer(t, pins)
	defer server.Close()

	stats := &SellBackfillStats{}
	if err := agg.BackfillSell(SellBackfillOptions{
		Context:  context.Background(),
		Client:   publishedcontent.NewBackfillClient(server.URL, server.Client()),
		PageSize: 100,
		Stats:    stats,
	}); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.Applied != 1 || stats.Skipped != 2 || stats.Errors != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	rec, _ := agg.SellIntentInfo(validItemPinId)
	if rec == nil || !rec.Active {
		t.Fatalf("record: %+v", rec)
	}
}
