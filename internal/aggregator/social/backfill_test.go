package social

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBackfillReplaysWithinLookbackOldestFirst(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": {MetaId: "meta-source", GlobalMetaId: "idq-source", Address: "1Source", Name: "Source", NameId: "name-source:i0", AvatarId: "avatar-source:i0"},
			"idq-target": {MetaId: "meta-target", GlobalMetaId: "idq-target", Address: "1Target", Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0"},
		},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"message":"ok","data":{"list":[{"id":"revoke-1:i0","path":"/follow@follow-1:i0","operation":"revoke","originalId":"follow-1:i0","globalMetaId":"idq-source","metaId":"meta-source","address":"1Source","createMetaId":"meta-source","createAddress":"1Source","chainName":"mvc","timestamp":1719705720},{"id":"follow-1:i0","path":"/follow","operation":"create","contentBody":"","contentSummary":"\"idq-target\"","globalMetaId":"idq-source","metaId":"meta-source","address":"1Source","createMetaId":"meta-source","createAddress":"1Source","chainName":"mvc","timestamp":1719705600},{"id":"follow-old:i0","path":"/follow","operation":"create","contentBody":"idq-target","contentSummary":"idq-target","globalMetaId":"idq-source","metaId":"meta-source","address":"1Source","createMetaId":"meta-source","createAddress":"1Source","chainName":"mvc","timestamp":1719700000}],"nextCursor":"","cursor":""}}`))
	}))
	defer server.Close()

	since := time.Unix(1719705500, 0)
	err := agg.Backfill(BackfillOptions{
		Context:  context.Background(),
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    since,
		PageSize: 100,
	})
	if err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	rel, err := agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: "idq-source",
		TargetGlobalMetaId: "idq-target",
	})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if rel.Source.FollowsTarget {
		t.Fatalf("source follows target = true, want false after in-window revoke; rel=%+v", rel)
	}
	if rel.Source.FollowPinId != "" || rel.Source.FollowedAt != 0 {
		t.Fatalf("source metadata = %+v, want cleared after revoke", rel.Source)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 0 {
		t.Fatalf("ListFollowing len = %d, want 0; list=%s", len(following.List), marshalItems(t, following.List))
	}
}

func TestBackfillRejectsRepeatedCursor(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": newTargetRef("meta-source", "idq-source", "1Source"),
			"idq-target": newTargetRef("meta-target", "idq-target", "1Target"),
		},
	})

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(`{"code":1,"message":"ok","data":{"list":[{"id":"follow-1:i0","path":"/follow","operation":"create","contentBody":"idq-target","contentSummary":"idq-target","globalMetaId":"idq-source","metaId":"meta-source","address":"1Source","createMetaId":"meta-source","createAddress":"1Source","chainName":"mvc","timestamp":1719705600}],"nextCursor":"cursor-1","cursor":"cursor-1"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":1,"message":"ok","data":{"list":[{"id":"follow-2:i0","path":"/follow","operation":"create","contentBody":"idq-target","contentSummary":"idq-target","globalMetaId":"idq-source","metaId":"meta-source","address":"1Source","createMetaId":"meta-source","createAddress":"1Source","chainName":"mvc","timestamp":1719705500}],"nextCursor":"cursor-1","cursor":"cursor-1"}}`))
	}))
	defer server.Close()

	err := agg.Backfill(BackfillOptions{
		Context:  context.Background(),
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    time.Unix(1719700000, 0),
		PageSize: 1,
	})
	if err == nil || err.Error() != `repeated MANAPI cursor "cursor-1" for path /follow` {
		t.Fatalf("Backfill err = %v, want repeated cursor error", err)
	}
}

func TestBackfillDefaultsToFollowPathAndUsesContentSummaryFallback(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": {MetaId: "meta-source", GlobalMetaId: "idq-source", Address: "1Source", Name: "Source", NameId: "name-source:i0", AvatarId: "avatar-source:i0"},
			"idq-target": {MetaId: "meta-target", GlobalMetaId: "idq-target", Address: "1Target", Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0"},
		},
	})

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Query().Get("path")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"message":"ok","data":{"list":[{"id":"follow-1:i0","path":"/follow","operation":"create","contentBody":"","contentSummary":"\"idq-target\"","globalMetaId":"idq-source","metaId":"meta-source","address":"1Source","createMetaId":"meta-source","createAddress":"1Source","chainName":"mvc","timestamp":1719705600}],"nextCursor":"","cursor":""}}`))
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
	if gotPath != "/follow" {
		t.Fatalf("requested path = %q, want /follow", gotPath)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	assertListOrder(t, following.List, []string{"idq-target"})
	assertProfileItem(t, following.List[0], "Target", "name-target:i0", "avatar-target:i0", "", "", 1719705600, "follow-1:i0")
}
