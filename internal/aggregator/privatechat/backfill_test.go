package privatechat

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

func TestBackfillDefaultsToHomepageSimpleMsgPath(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	var requestedPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Query().Get("path"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 1,
			"data": map[string]any{
				"list": []map[string]any{{
					"id":            "msg-create:i0",
					"path":          HomepageSimpleMsgProtocolPath,
					"operation":     "create",
					"contentType":   "application/json",
					"contentBody":   map[string]any{"from": "meta-alice", "to": "meta-bob", "content": "hello", "contentType": "text/plain"},
					"globalMetaId":  "gid-alice",
					"metaId":        "meta-alice",
					"address":       "1Alice",
					"createMetaId":  "meta-alice",
					"createAddress": "1Alice",
					"chainName":     "mvc",
					"timestamp":     int64(1719705600),
					"genesisHeight": int64(123),
				}},
				"nextCursor": "",
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
	if len(requestedPaths) != 1 || requestedPaths[0] != HomepageSimpleMsgProtocolPath {
		t.Fatalf("requested paths = %#v, want only %s", requestedPaths, HomepageSimpleMsgProtocolPath)
	}

	result, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "gid-alice",
		MetaId:       "meta-alice",
		Address:      "1Alice",
		Size:         5,
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len items = %d, want 1: %+v", len(result.Items), result.Items)
	}
	if result.Items[0].PinId != "msg-create:i0" || result.Items[0].InteractWith != "meta-bob" {
		t.Fatalf("interaction = %+v", result.Items[0])
	}
}

func TestBackfillUsesCallerContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("server should not receive request after caller context is canceled")
	}))
	defer server.Close()

	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	err := agg.Backfill(BackfillOptions{
		Context:  ctx,
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    time.Now().AddDate(0, -2, 0),
		PageSize: 1,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Backfill error = %v, want context.Canceled", err)
	}
}

func TestBackfillRejectsRepeatedCursor(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 1,
			"data": map[string]any{
				"list": []map[string]any{{
					"id":            "msg-create:i0",
					"path":          HomepageSimpleMsgProtocolPath,
					"operation":     "create",
					"contentType":   "application/json",
					"contentBody":   map[string]any{"from": "meta-alice", "to": "meta-bob", "content": "hello"},
					"globalMetaId":  "gid-alice",
					"metaId":        "meta-alice",
					"createMetaId":  "meta-alice",
					"createAddress": "1Alice",
					"chainName":     "mvc",
					"timestamp":     int64(1719705600),
				}},
				"nextCursor": "same",
			},
		})
	}))
	defer server.Close()

	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	err := agg.Backfill(BackfillOptions{
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    time.Unix(1719700000, 0),
		PageSize: 1,
	})
	if err == nil {
		t.Fatal("Backfill returned nil error, want repeated cursor error")
	}
	if !strings.Contains(err.Error(), `repeated MANAPI cursor "same"`) {
		t.Fatalf("Backfill error = %v, want repeated cursor error", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}
}
