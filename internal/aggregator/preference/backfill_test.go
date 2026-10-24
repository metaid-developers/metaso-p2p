package preference

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

// newPreferenceMANAPIServer serves MANAPI /pin/path/list pages for the
// feedback path from a static pin list (newest-first, like the real API).
func newPreferenceMANAPIServer(t *testing.T, pins []map[string]any) *httptest.Server {
	t.Helper()
	sort.SliceStable(pins, func(i, j int) bool {
		return pinTimestamp(pins[i]) > pinTimestamp(pins[j])
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/path/list" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		path := r.URL.Query().Get("path")
		if path != PathPreferenceFeedback {
			writeMANAPIList(t, w, nil, "")
			return
		}
		size := 100
		if s := r.URL.Query().Get("size"); s != "" {
			fmt.Sscanf(s, "%d", &size)
		}
		start := 0
		if c := r.URL.Query().Get("cursor"); c != "" {
			decoded, err := url.QueryUnescape(c)
			if err == nil {
				fmt.Sscanf(decoded, "%d", &start)
			}
		}
		end := start + size
		if end > len(pins) {
			end = len(pins)
		}
		nextCursor := ""
		if end < len(pins) {
			nextCursor = fmt.Sprintf("%d", end)
		}
		writeMANAPIList(t, w, pins[start:end], nextCursor)
	}))
	t.Cleanup(server.Close)
	return server
}

func pinTimestamp(pin map[string]any) int64 {
	switch v := pin["timestamp"].(type) {
	case int64:
		return v
	case int:
		return int64(v)
	case float64:
		return int64(v)
	}
	return 0
}

func writeMANAPIList(t *testing.T, w http.ResponseWriter, pins []map[string]any, nextCursor string) {
	t.Helper()
	body := map[string]any{
		"code":       0,
		"message":    "ok",
		"list":       pins,
		"nextCursor": nextCursor,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": body})
}

func manapiFeedbackPin(id string, timestamp int64, body string) map[string]any {
	return map[string]any{
		"id":           id,
		"path":         PathPreferenceFeedback,
		"operation":    "create",
		"contentType":  "application/json",
		"contentBody":  body,
		"chainName":    "mvc",
		"globalMetaId": "gid-judge-" + id,
		"metaId":       "gid-judge-" + id,
		"address":      "gid-judge-" + id,
		"timestamp":    timestamp,
	}
}

func TestBackfillIdempotentAndHonestStats(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	pins := []map[string]any{
		manapiFeedbackPin("fb-bf-2:i0", 2000, abBatch("b", "")),
		manapiFeedbackPin("fb-bf-1:i0", 1000, abBatch("a", "")),
		// A modify pin that nevertheless appears on chain: ignored.
		{
			"id": "fb-bf-mod:i0", "path": PathPreferenceFeedback, "operation": "modify",
			"contentType": "application/json", "contentBody": abBatch("a", ""),
			"chainName": "mvc", "globalMetaId": "gid-judge-x", "timestamp": 1500,
		},
	}
	server := newPreferenceMANAPIServer(t, pins)
	client := publishedcontent.NewBackfillClient(server.URL, nil)

	stats := &BackfillStats{}
	if err := pref.Backfill(BackfillOptions{Context: context.Background(), Client: client, Stats: stats}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	chainStats := stats.chain(PathPreferenceFeedback, "mvc")
	if chainStats.Fetched != 3 || chainStats.Applied != 2 || chainStats.Skipped != 1 || chainStats.Errors != 0 {
		t.Fatalf("stats: %+v", chainStats)
	}

	// Both create pins applied: A n=2 (two signers) — one win each.
	a := pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 2 || a.Judges != 2 || a.Score == nil {
		t.Fatalf("scores after backfill: %+v", a)
	}

	// Re-run: idempotent.
	stats2 := &BackfillStats{}
	if err := pref.Backfill(BackfillOptions{Context: context.Background(), Client: client, Stats: stats2}); err != nil {
		t.Fatalf("Backfill re-run: %v", err)
	}
	a = pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 2 || a.Judges != 2 {
		t.Fatalf("scores after idempotent re-run: %+v", a)
	}
	records, err := pref.listFeedbackRecords()
	if err != nil {
		t.Fatalf("listFeedbackRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("stored records: got %d want 2 (modify pin ignored)", len(records))
	}
}

func TestBackfillRespectsSinceCutoff(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	pins := []map[string]any{
		manapiFeedbackPin("fb-old:i0", 1000, abBatch("a", "")),
		manapiFeedbackPin("fb-new:i0", 5000, abBatch("a", "")),
	}
	server := newPreferenceMANAPIServer(t, pins)
	client := publishedcontent.NewBackfillClient(server.URL, nil)

	if err := pref.Backfill(BackfillOptions{
		Context: context.Background(), Client: client,
		Since: time.Unix(4000, 0),
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}
	a := pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 1 {
		t.Fatalf("since cutoff must skip the old pin: %+v", a)
	}
}
