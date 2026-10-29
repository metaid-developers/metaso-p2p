package social

import (
	"context"
	"fmt"
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
			testGidSource: {MetaId: testMetaSource, GlobalMetaId: testGidSource, Address: testAddrSource, Name: "Source", NameId: "name-source:i0", AvatarId: "avatar-source:i0"},
			testGidTarget: {MetaId: testMetaTarget, GlobalMetaId: testGidTarget, Address: testAddrTarget, Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0"},
		},
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"code":1,"message":"ok","data":{"list":[`+
			`{"id":"revoke-1:i0","path":"/follow@follow-1:i0","operation":"revoke","originalId":"follow-1:i0","globalMetaId":"%s","metaId":"%s","address":"%s","createMetaId":"%s","createAddress":"%s","chainName":"mvc","timestamp":1719705720},`+
			`{"id":"follow-1:i0","path":"/follow","operation":"create","contentBody":"","contentSummary":"\"%s\"","globalMetaId":"%s","metaId":"%s","address":"%s","createMetaId":"%s","createAddress":"%s","chainName":"mvc","timestamp":1719705600},`+
			`{"id":"follow-old:i0","path":"/follow","operation":"create","contentBody":"%s","contentSummary":"%s","globalMetaId":"%s","metaId":"%s","address":"%s","createMetaId":"%s","createAddress":"%s","chainName":"mvc","timestamp":1719700000}`+
			`],"nextCursor":"","cursor":""}}`,
			testGidSource, testMetaSource, testAddrSource, testMetaSource, testAddrSource,
			testGidTarget,
			testGidSource, testMetaSource, testAddrSource, testMetaSource, testAddrSource,
			testGidTarget, testGidTarget,
			testGidSource, testMetaSource, testAddrSource, testMetaSource, testAddrSource)))
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

	// The replay is oldest-first, so the follow-1 create is materialized
	// before the revoke removes it again: the final state must be empty.
	rel, err := agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: testGidSource,
		TargetGlobalMetaId: testGidTarget,
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

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewProfile})
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
			testGidSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testGidTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
		},
	})

	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		if calls == 1 {
			_, _ = w.Write([]byte(fmt.Sprintf(`{"code":1,"message":"ok","data":{"list":[{"id":"follow-1:i0","path":"/follow","operation":"create","contentBody":"%s","contentSummary":"%s","globalMetaId":"%s","metaId":"%s","address":"%s","createMetaId":"%s","createAddress":"%s","chainName":"mvc","timestamp":1719705600}],"nextCursor":"cursor-1","cursor":"cursor-1"}}`,
				testGidTarget, testGidTarget,
				testGidSource, testMetaSource, testAddrSource, testMetaSource, testAddrSource)))
			return
		}
		_, _ = w.Write([]byte(fmt.Sprintf(`{"code":1,"message":"ok","data":{"list":[{"id":"follow-2:i0","path":"/follow","operation":"create","contentBody":"%s","contentSummary":"%s","globalMetaId":"%s","metaId":"%s","address":"%s","createMetaId":"%s","createAddress":"%s","chainName":"mvc","timestamp":1719705500}],"nextCursor":"cursor-1","cursor":"cursor-1"}}`,
			testGidTarget, testGidTarget,
			testGidSource, testMetaSource, testAddrSource, testMetaSource, testAddrSource)))
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
			testGidSource: {MetaId: testMetaSource, GlobalMetaId: testGidSource, Address: testAddrSource, Name: "Source", NameId: "name-source:i0", AvatarId: "avatar-source:i0"},
			testGidTarget: {MetaId: testMetaTarget, GlobalMetaId: testGidTarget, Address: testAddrTarget, Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0"},
		},
	})

	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Query().Get("path")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fmt.Sprintf(`{"code":1,"message":"ok","data":{"list":[{"id":"follow-1:i0","path":"/follow","operation":"create","contentBody":"","contentSummary":"\"%s\"","globalMetaId":"%s","metaId":"%s","address":"%s","createMetaId":"%s","createAddress":"%s","chainName":"mvc","timestamp":1719705600}],"nextCursor":"","cursor":""}}`,
			testGidTarget,
			testGidSource, testMetaSource, testAddrSource, testMetaSource, testAddrSource)))
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

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	assertListOrder(t, following.List, []string{testGidTarget})
	assertProfileItem(t, following.List[0], "Target", "name-target:i0", "avatar-target:i0", "", "", 1719705600, "follow-1:i0")
}
