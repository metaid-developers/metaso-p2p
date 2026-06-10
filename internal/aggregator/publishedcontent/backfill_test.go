package publishedcontent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBackfillStopsAtTwoMonthCutoff(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	newer := manapiPinForTest("newer-buzz:i0", PathSimpleBuzz, now.AddDate(0, -1, 0))
	older := manapiPinForTest("older-buzz:i0", PathSimpleBuzz, now.AddDate(0, -3, 0))
	server := newBackfillMANAPIServer(t, map[string][]map[string]any{
		PathSimpleBuzz: {newer, older},
	})
	defer server.Close()

	agg, store := setupTestAggregator(t)
	defer store.Close()

	err := agg.Backfill(BackfillOptions{
		Client:   NewBackfillClient(server.URL, server.Client()),
		Paths:    []string{PathSimpleBuzz},
		Since:    now.AddDate(0, -2, 0),
		PageSize: 2,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	result, err := agg.List(ListParams{ProtocolPath: PathSimpleBuzz, Size: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len: got %d want 1: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].SourcePinId != "newer-buzz:i0" {
		t.Fatalf("SourcePinId: got %q want newer-buzz:i0", result.Items[0].SourcePinId)
	}
}

func TestBackfillUsesContentSummaryFallback(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	pin := manapiPinForTest("skill-pin:i0", PathMetaBotSkill, now.Add(-time.Hour))
	pin["contentType"] = "application/json"
	pin["contentBody"] = ""
	pin["contentSummary"] = `{"name":"writer"}`
	server := newBackfillMANAPIServer(t, map[string][]map[string]any{
		PathMetaBotSkill: {pin},
	})
	defer server.Close()

	agg, store := setupTestAggregator(t)
	defer store.Close()

	err := agg.Backfill(BackfillOptions{
		Client:   NewBackfillClient(server.URL, server.Client()),
		Paths:    []string{PathMetaBotSkill},
		Since:    now.AddDate(0, -2, 0),
		PageSize: 2,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	result, err := agg.List(ListParams{ProtocolPath: PathMetaBotSkill, Size: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len: got %d want 1", len(result.Items))
	}
	if got := result.Items[0].PayloadJSON["name"]; got != "writer" {
		t.Fatalf("payload name: got %#v want writer", got)
	}
}

func TestBackfillRejectsRepeatedCursor(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	first := manapiPinForTest("first-buzz:i0", PathSimpleBuzz, now.Add(-time.Hour))
	second := manapiPinForTest("second-buzz:i0", PathSimpleBuzz, now.Add(-30*time.Minute))
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/path/list" {
			t.Fatalf("request path: got %q want /pin/path/list", r.URL.Path)
		}
		if got := r.URL.Query().Get("path"); got != PathSimpleBuzz {
			t.Fatalf("path query: got %q want %q", got, PathSimpleBuzz)
		}
		w.Header().Set("Content-Type", "application/json")
		requests++
		response := map[string]any{
			"code": 1,
			"data": map[string]any{
				"list":       []map[string]any{first},
				"nextCursor": "same",
			},
		}
		switch {
		case requests == 2:
			response["data"].(map[string]any)["list"] = []map[string]any{second}
		case requests > 2:
			response["data"].(map[string]any)["list"] = []map[string]any{}
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer server.Close()

	agg, store := setupTestAggregator(t)
	defer store.Close()

	err := agg.Backfill(BackfillOptions{
		Client:   NewBackfillClient(server.URL, server.Client()),
		Paths:    []string{PathSimpleBuzz},
		Since:    now.AddDate(0, -2, 0),
		PageSize: 1,
	})
	if err == nil {
		t.Fatal("Backfill returned nil error, want repeated cursor error")
	}
	if !strings.Contains(err.Error(), `repeated MANAPI cursor "same"`) {
		t.Fatalf("Backfill error: got %v want repeated MANAPI cursor %q", err, "same")
	}
	if requests != 2 {
		t.Fatalf("requests: got %d want 2", requests)
	}
}

func TestBackfillUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not receive request after caller context is canceled")
	}))
	defer server.Close()

	agg, store := setupTestAggregator(t)
	defer store.Close()

	err := agg.Backfill(BackfillOptions{
		Context:  ctx,
		Client:   NewBackfillClient(server.URL, server.Client()),
		Paths:    []string{PathSimpleBuzz},
		Since:    time.Now().AddDate(0, -2, 0),
		PageSize: 1,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Backfill error: got %v want context.Canceled", err)
	}
}

func manapiPinForTest(id, path string, ts time.Time) map[string]any {
	return map[string]any{
		"id":             id,
		"path":           path,
		"originalPath":   path,
		"operation":      OperationCreate,
		"contentType":    "text/plain",
		"contentBody":    "hello " + id,
		"contentSummary": "",
		"metaId":         "meta-user",
		"globalMetaId":   "gid-user",
		"address":        "addr-user",
		"createMetaId":   "meta-user",
		"createAddress":  "addr-user",
		"chainName":      "mvc",
		"timestamp":      ts.UnixMilli(),
		"genesisHeight":  int64(123),
		"originalId":     "",
	}
}

func newBackfillMANAPIServer(t *testing.T, pinsByPath map[string][]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/path/list" {
			t.Fatalf("request path: got %q want /pin/path/list", r.URL.Path)
		}
		path := r.URL.Query().Get("path")
		size := r.URL.Query().Get("size")
		if path == "" {
			t.Fatal("missing path query")
		}
		if size == "" {
			t.Fatal("missing size query")
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code": 1,
			"data": map[string]any{
				"list":       pinsByPath[path],
				"nextCursor": "",
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
}
