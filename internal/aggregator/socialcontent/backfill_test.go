package socialcontent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBackfillReplaysNewestFirstPagesOldestFirst(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("path") != PathSimpleBuzz {
			t.Fatalf("path = %q", r.URL.Query().Get("path"))
		}
		var pins []BackfillPin
		switch r.URL.Query().Get("cursor") {
		case "":
			pins = []BackfillPin{
				backfillPin("buzz-new:i0", OperationCreate, 300, `{"text":"new"}`),
				backfillPin("buzz-old:i0", OperationCreate, 200, `{"text":"old"}`),
			}
		default:
			pins = nil
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": pins}})
	}))
	defer server.Close()

	if err := agg.Backfill(BackfillOptions{
		Context:  context.Background(),
		Client:   NewBackfillClient(server.URL, server.Client()),
		Paths:    []string{PathSimpleBuzz},
		Since:    time.Unix(150, 0),
		PageSize: 2,
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	if requests != 2 {
		t.Fatalf("requests = %d, want 2 for the two-pass scan", requests)
	}
	result, err := agg.List(FeedParams{Size: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 2 || result.Items[0].SourcePinId != "buzz-new:i0" || result.Items[1].SourcePinId != "buzz-old:i0" {
		t.Fatalf("items = %+v", result.Items)
	}
}

func TestBackfillDoesNotStopAtAnOlderPageAndIncludesTargetPost(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		cursor := r.URL.Query().Get("cursor")
		var pins []BackfillPin
		switch {
		case r.URL.Query().Get("path") == PathSimpleBuzz && cursor == "":
			pins = []BackfillPin{backfillPin("old-buzz:i0", OperationCreate, 100, `{"text":"old"}`)}
		case r.URL.Query().Get("path") == PathSimpleBuzz && cursor == "buzz-next":
			pins = nil
		case r.URL.Query().Get("path") == PathPayLike && cursor == "":
			like := backfillPin("recent-like:i0", OperationCreate, 200, `{"isLike":true,"likeTo":"old-buzz:i0"}`)
			like.Path = PathPayLike
			pins = []BackfillPin{like}
		default:
			pins = nil
		}
		next := ""
		if cursor == "" && len(pins) > 0 {
			next = map[string]string{PathSimpleBuzz: "buzz-next", PathPayLike: "like-next"}[r.URL.Query().Get("path")]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"list": pins, "nextCursor": next}})
	}))
	defer server.Close()

	if err := agg.Backfill(BackfillOptions{
		Context:  context.Background(),
		Client:   NewBackfillClient(server.URL, server.Client()),
		Paths:    []string{PathSimpleBuzz, PathPayLike},
		Since:    time.Unix(150, 0),
		PageSize: 1,
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	post, err := agg.FindPost("old-buzz:i0", "mvc")
	if err != nil || post == nil || post.LikeCount != 1 {
		t.Fatalf("target post = %+v err=%v", post, err)
	}
	if requests != 8 {
		t.Fatalf("requests = %d, want 8 (two paths, two passes, one terminal page each)", requests)
	}
}

func TestBackfillClientDecodesStringJSONBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"list": []map[string]any{{
				"id":          "buzz:i0",
				"path":        PathSimpleBuzz,
				"operation":   OperationCreate,
				"chainName":   "mvc",
				"timestamp":   100,
				"contentType": "application/json",
				"contentBody": `{"text":"hello"}`,
			}},
		})
	}))
	defer server.Close()

	page, err := NewBackfillClient(server.URL, server.Client()).ListPath(context.Background(), PathSimpleBuzz, "", 10)
	if err != nil {
		t.Fatalf("ListPath: %v", err)
	}
	if len(page.Pins) != 1 || string(page.Pins[0].ContentBody.Bytes()) != `{"text":"hello"}` {
		t.Fatalf("page = %+v", page)
	}
}

func backfillPin(id, op string, timestamp int64, body string) BackfillPin {
	return BackfillPin{
		ID:           id,
		Path:         PathSimpleBuzz,
		Operation:    op,
		ChainName:    "mvc",
		Timestamp:    timestamp,
		ContentType:  "application/json",
		ContentBody:  backfillBody(body),
		GlobalMetaId: "idq-backfill",
		MetaId:       "meta-backfill",
		Address:      "address-backfill",
	}
}
