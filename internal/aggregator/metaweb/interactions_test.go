package metaweb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/inbox"
)

type fakeInboxSource struct {
	hits []inbox.Hit
	err  error
}

func (f *fakeInboxSource) InboxHits(owner string, sinceSec int64, types map[string]bool, afterTs int64, afterPinId string, limit int) ([]inbox.Hit, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]inbox.Hit, 0)
	for _, hit := range f.hits {
		if sinceSec > 0 && hit.CreatedAt < sinceSec {
			continue
		}
		if afterTs > 0 && !hit.After(afterTs, afterPinId) {
			continue
		}
		if !types[hit.Type] {
			continue
		}
		out = append(out, hit)
	}
	return out, nil
}

func interactionsRequest(t *testing.T, router *gin.Engine, query string) (int, struct {
	Code int `json:"code"`
	Data struct {
		Items []struct {
			Type        string `json:"type"`
			PinId       string `json:"pinId"`
			TargetPinId string `json:"targetPinId"`
			Actor       struct {
				Address      string `json:"address"`
				GlobalMetaId string `json:"globalMetaId"`
				Name         string `json:"name"`
			} `json:"actor"`
			CreatedAt int64  `json:"createdAt"`
			Excerpt   string `json:"excerpt"`
			Dislike   bool   `json:"dislike"`
		} `json:"items"`
		HasMore    bool    `json:"hasMore"`
		NextCursor *string `json:"nextCursor"`
		ServerTime int64   `json:"serverTime"`
	} `json:"data"`
}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/metaweb/interactions?"+query, nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Items []struct {
				Type        string `json:"type"`
				PinId       string `json:"pinId"`
				TargetPinId string `json:"targetPinId"`
				Actor       struct {
					Address      string `json:"address"`
					GlobalMetaId string `json:"globalMetaId"`
					Name         string `json:"name"`
				} `json:"actor"`
				CreatedAt int64  `json:"createdAt"`
				Excerpt   string `json:"excerpt"`
				Dislike   bool   `json:"dislike"`
			} `json:"items"`
			HasMore    bool    `json:"hasMore"`
			NextCursor *string `json:"nextCursor"`
			ServerTime int64   `json:"serverTime"`
		} `json:"data"`
	}
	_ = json.Unmarshal(recorder.Body.Bytes(), &envelope)
	return recorder.Code, envelope
}

func TestHandleInteractions_MergesSortsAndPages(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetProfileNamer(&fakeNamer{names: map[string]string{"idq1fan": "Fan"}})
	agg.SetInteractionSources(
		&fakeInboxSource{hits: []inbox.Hit{
			{Type: inbox.TypeSimpleAnswer, PinId: "aaaa", CreatedAt: 1755000100, TargetPinId: "q1", ActorGlobalMetaId: "idq1fan", Excerpt: "an answer"},
			{Type: inbox.TypePayComment, PinId: "cccc", CreatedAt: 1755000200, TargetPinId: "q1", ActorAddress: "addr-fan", Excerpt: "a comment"},
		}},
		&fakeInboxSource{hits: []inbox.Hit{
			{Type: inbox.TypePayLike, PinId: "bbbb", CreatedAt: 1755000100, TargetPinId: "p1", ActorAddress: "addr-fan", Excerpt: "like"},
			{Type: inbox.TypePayLike, PinId: "dddd", CreatedAt: 1755000000, TargetPinId: "p1", ActorAddress: "addr-fan", Excerpt: "like"},
		}},
	)
	router := newTestRouter(agg)

	code, envelope := interactionsRequest(t, router, "owner=addr-owner")
	if code != http.StatusOK || envelope.Code != 0 {
		t.Fatalf("code = %d/%d", code, envelope.Code)
	}
	// Order: cccc (200) first; at ts 100 pinId DESC puts bbbb before aaaa;
	// dddd (000) last.
	want := []string{"cccc", "bbbb", "aaaa", "dddd"}
	if len(envelope.Data.Items) != len(want) {
		t.Fatalf("items = %d, want %d", len(envelope.Data.Items), len(want))
	}
	for i, item := range envelope.Data.Items {
		if item.PinId != want[i] {
			t.Errorf("items[%d] = %s, want %s", i, item.PinId, want[i])
		}
	}
	if envelope.Data.Items[0].Actor.Name != "" {
		t.Errorf("actor without globalMetaId should not resolve a name, got %q", envelope.Data.Items[0].Actor.Name)
	}
	if envelope.Data.Items[2].Actor.Name != "Fan" {
		t.Errorf("actor name = %q, want Fan", envelope.Data.Items[2].Actor.Name)
	}
	if envelope.Data.HasMore {
		t.Error("no more expected")
	}

	// Paging: size=2 walks with the cursor, no gaps/dups.
	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		query := "owner=addr-owner&size=2"
		if cursor != "" {
			query += "&cursor=" + cursor
		}
		_, envelope := interactionsRequest(t, router, query)
		for _, item := range envelope.Data.Items {
			if seen[item.PinId] {
				t.Fatalf("duplicate %s across pages", item.PinId)
			}
			seen[item.PinId] = true
		}
		if !envelope.Data.HasMore {
			break
		}
		cursor = *envelope.Data.NextCursor
		pages++
		if pages > 4 {
			t.Fatal("paging did not terminate")
		}
	}
	if len(seen) != 4 {
		t.Fatalf("saw %d unique items, want 4", len(seen))
	}
}

func TestHandleInteractions_FiltersAndErrors(t *testing.T) {
	agg := newTestAggregator(t)
	agg.SetInteractionSources(&fakeInboxSource{hits: []inbox.Hit{
		{Type: inbox.TypeSimpleAnswer, PinId: "aaaa", CreatedAt: 1755000100},
		{Type: inbox.TypePayLike, PinId: "bbbb", CreatedAt: 1755000200},
	}})
	router := newTestRouter(agg)

	if _, envelope := interactionsRequest(t, router, "owner=addr-owner&types=simpleanswer"); len(envelope.Data.Items) != 1 || envelope.Data.Items[0].Type != inbox.TypeSimpleAnswer {
		t.Fatalf("type filter = %+v", envelope.Data.Items)
	}
	if _, envelope := interactionsRequest(t, router, "owner=addr-owner&since=1755000200"); len(envelope.Data.Items) != 1 {
		t.Fatalf("since filter = %+v", envelope.Data.Items)
	}
	for _, bad := range []string{
		"",                     // owner required
		"owner=&size=5",        // owner required
		"owner=x&types=heart",  // unsupported type
		"owner=x&size=0",       // invalid size
		"owner=x&since=abc",    // invalid since
		"owner=x&cursor=wrong", // invalid cursor
	} {
		_, envelope := interactionsRequest(t, router, bad)
		if envelope.Code != codeInvalidParam {
			t.Errorf("%q: code = %d, want 40000", bad, envelope.Code)
		}
	}

	// Source failure maps to 50000.
	failing := newTestAggregator(t)
	failing.SetInteractionSources(&fakeInboxSource{err: fmt.Errorf("store closed")})
	router = newTestRouter(failing)
	if _, envelope := interactionsRequest(t, router, "owner=addr-owner"); envelope.Code != codeUnavailable {
		t.Fatalf("source failure code = %d, want 50000", envelope.Code)
	}

	// No sources at all also maps to 50000.
	empty := newTestAggregator(t)
	router = newTestRouter(empty)
	if _, envelope := interactionsRequest(t, router, "owner=addr-owner"); envelope.Code != codeUnavailable {
		t.Fatalf("no sources code = %d, want 50000", envelope.Code)
	}
}
