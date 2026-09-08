package qa

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
)

func manapiQAPinForTest(id, path string, body map[string]any, op string, ts time.Time) map[string]any {
	return map[string]any{
		"id":             id,
		"path":           path,
		"operation":      op,
		"contentType":    "application/json",
		"contentBody":    body,
		"contentSummary": "",
		"metaId":         "",
		"globalMetaId":   "backfill-asker",
		"address":        "",
		"createMetaId":   "",
		"createAddress":  "",
		"chainName":      "mvc",
		"timestamp":      ts.UnixMilli(),
		"originalId":     "",
	}
}

func newQABackfillMANAPIServer(t *testing.T, pinsByPath map[string][]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/path/list" {
			t.Errorf("request path: got %q want /pin/path/list", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		path := r.URL.Query().Get("path")
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

func TestBackfillReplaysAllFourPathsAndReportsCounts(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	q1 := testPinId("backfill-question")
	a1 := testPinId("backfill-answer")

	// Answers listed before questions (newest-first listing); the backfill
	// replays oldest-first per path and questions replay before answers.
	pins := map[string][]map[string]any{
		PathPayComment: {
			manapiQAPinForTest(testPinId("backfill-comment"), PathPayComment, map[string]any{"commentTo": q1, "content": "c"}, OperationCreate, now.Add(-2*time.Hour)),
		},
		PathPayLike: {
			manapiQAPinForTest(testPinId("backfill-like"), PathPayLike, map[string]any{"likeTo": a1, "isLike": 1}, OperationCreate, now.Add(-2*time.Hour)),
		},
		PathSimpleAnswer: {
			manapiQAPinForTest(a1, PathSimpleAnswer, map[string]any{"answerTo": q1, "content": "backfilled answer"}, OperationCreate, now.Add(-2*time.Hour)),
		},
		PathSimpleQuestion: {
			manapiQAPinForTest(q1, PathSimpleQuestion, map[string]any{"title": "Backfilled question?", "content": "body"}, OperationCreate, now.Add(-3*time.Hour)),
		},
	}
	server := newQABackfillMANAPIServer(t, pins)
	defer server.Close()

	agg, store := setupTestAggregator(t)
	defer store.Close()

	stats := &BackfillStats{}
	err := agg.Backfill(BackfillOptions{
		Client:   publishedcontent.NewBackfillClient(server.URL, server.Client()),
		PageSize: 100,
		Stats:    stats,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	rec, _ := agg.loadQuestion("mvc", q1)
	if rec == nil {
		t.Fatal("backfilled question missing")
	}
	if rec.AnswerCount != 1 || rec.CommentCount != 1 {
		t.Fatalf("question counts = answers %d comments %d", rec.AnswerCount, rec.CommentCount)
	}
	ans, _ := agg.loadAnswer("mvc", a1)
	if ans == nil || ans.LikeCount != 1 {
		t.Fatalf("backfilled answer = %+v", ans)
	}
	if rec.TopAnswer == nil || rec.TopAnswer.LikeCount != 1 {
		t.Fatalf("topAnswer after backfill = %+v", rec.TopAnswer)
	}

	for _, path := range []string{PathSimpleQuestion, PathSimpleAnswer, PathPayLike, PathPayComment} {
		cell := stats.chain(path, "mvc")
		if cell.Fetched != 1 || cell.Applied != 1 || cell.Errors != 0 {
			t.Fatalf("stats[%s] = %+v", path, cell)
		}
	}

	// Re-running the backfill is idempotent.
	if err := agg.Backfill(BackfillOptions{
		Client:   publishedcontent.NewBackfillClient(server.URL, server.Client()),
		PageSize: 100,
	}); err != nil {
		t.Fatalf("Backfill rerun: %v", err)
	}
	rec, _ = agg.loadQuestion("mvc", q1)
	if rec.AnswerCount != 1 || rec.CommentCount != 1 || rec.LikeCount != 0 {
		t.Fatalf("counts after rerun = %+v", rec)
	}
	ans, _ = agg.loadAnswer("mvc", a1)
	if ans.LikeCount != 1 {
		t.Fatalf("answer likes after rerun = %+v", ans)
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
		Client:   publishedcontent.NewBackfillClient(server.URL, server.Client()),
		PageSize: 1,
	})
	if err == nil {
		t.Fatal("Backfill should fail with a canceled context")
	}
}
