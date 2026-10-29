package block

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

type stubManapiServer struct {
	// path -> ordered pages (newest-first pages of pins)
	pagesByPath map[string][][]map[string]interface{}
	requests    []string
}

func (s *stubManapiServer) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/pin/path/list", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Query().Get("path")
		cursor := r.URL.Query().Get("cursor")
		s.requests = append(s.requests, path+"?cursor="+cursor)
		pages, ok := s.pagesByPath[path]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "list": []interface{}{}, "nextCursor": ""})
			return
		}
		index := 0
		if cursor != "" {
			index, _ = strconv.Atoi(cursor)
		}
		if index >= len(pages) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "list": []interface{}{}, "nextCursor": ""})
			return
		}
		next := ""
		if index+1 < len(pages) {
			next = strconv.Itoa(index + 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "list": pages[index], "nextCursor": next})
	})
	return mux
}

func manapiBlockPin(id, path, fromGlobal, to string, state int, ts int64) map[string]interface{} {
	body, _ := json.Marshal(map[string]interface{}{"to": to, "blockState": state})
	return map[string]interface{}{
		"id": id, "path": path, "operation": "create",
		"contentBody": string(body),
		"metaId":      "meta-" + fromGlobal, "globalMetaId": fromGlobal,
		"createMetaId": "meta-" + fromGlobal,
		"chainName":    "mvc", "timestamp": ts, "genesisHeight": 900,
	}
}

func TestBackfillReplaysBothPathsOldestFirst(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	server := &stubManapiServer{pagesByPath: map[string][][]map[string]interface{}{
		// MANAPI lists newest-first; include both ingested paths.
		"/protocols/simpleblock": {{
			manapiBlockPin("can-2:i0", "/protocols/simpleblock", idAlice, idBob, -1, 1002),
			manapiBlockPin("can-1:i0", "/protocols/simpleblock", idAlice, idBob, 1, 1000),
		}},
		"/private/block/simpleprivateblock": {{
			manapiBlockPin("cmp-1:i0", "/private/block/simpleprivateblock", idCarol, idAlice, 1, 1001),
		}},
	}}

	ts := httptest.NewServer(server.handler(t))
	defer ts.Close()

	if err := agg.Backfill(BackfillOptions{
		Context: context.Background(),
		Client:  NewBackfillClient(ts.URL, nil),
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	// Oldest-first replay: the newest pin (can-2, unblock) wins.
	blocked, _ := agg.IsBlockedBetween(idAlice, idBob)
	if blocked {
		t.Fatalf("newest unblock pin must win after oldest-first replay")
	}
	blocked, _ = agg.IsBlockedBetween(idCarol, idAlice)
	if !blocked {
		t.Fatalf("compatibility-path history must fold too")
	}

	if len(server.requests) < 2 {
		t.Fatalf("both paths must be queried; requests=%v", server.requests)
	}
	canonicalQueried, compatQueried := false, false
	for _, req := range server.requests {
		switch {
		case contains(req, "/protocols/simpleblock?"):
			canonicalQueried = true
		case contains(req, "/private/block/simpleprivateblock?"):
			compatQueried = true
		}
	}
	if !canonicalQueried || !compatQueried {
		t.Fatalf("both block paths must be backfilled; requests=%v", server.requests)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func TestBackfillHonorsSinceCutoff(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	server := &stubManapiServer{pagesByPath: map[string][][]map[string]interface{}{
		"/protocols/simpleblock": {{
			manapiBlockPin("can-new:i0", "/protocols/simpleblock", idAlice, idBob, 1, 5000),
			manapiBlockPin("can-old:i0", "/protocols/simpleblock", idCarol, idAlice, 1, 1000),
		}},
	}}
	ts := httptest.NewServer(server.handler(t))
	defer ts.Close()

	// Seconds-precision timestamps in the fixture: since at 4000s epoch.
	since := time.Unix(4000, 0)
	if err := agg.Backfill(BackfillOptions{
		Context: context.Background(),
		Client:  NewBackfillClient(ts.URL, nil),
		Since:   since,
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	if blocked, _ := agg.IsBlockedBetween(idAlice, idBob); !blocked {
		t.Fatalf("new pin (within lookback) must apply")
	}
	if blocked, _ := agg.IsBlockedBetween(idCarol, idAlice); blocked {
		t.Fatalf("old pin (before lookback) must be skipped")
	}
}

func TestBackfillRejectsRepeatedCursor(t *testing.T) {
	agg, _ := newTestAggregatorWithLookup(t)

	// A full page (default page size 100) with a repeating cursor: the loop
	// only follows nextCursor while pages are full.
	page := make([]map[string]interface{}, 0, 100)
	for i := 0; i < 100; i++ {
		page = append(page, manapiBlockPin("can-"+strconv.Itoa(i)+":i0", "/protocols/simpleblock", idAlice, idBob, 1, int64(1000+i)))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/pin/path/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"code": 0, "list": page, "nextCursor": "same"})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	err := agg.Backfill(BackfillOptions{
		Context: context.Background(),
		Client:  NewBackfillClient(ts.URL, nil),
	})
	if err == nil {
		t.Fatalf("repeated MANAPI cursor must surface as an error")
	}
}
