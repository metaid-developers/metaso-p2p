package federation

import (
	"testing"
	"time"

	"github.com/metaid-developers/meta-socket/internal/presence"
)

func TestGlobalOnlineEmptyRemoteStoreReturnsLocalEntries(t *testing.T) {
	now := time.UnixMilli(1710000100000)
	store := newAggregateStore(now)
	local := []presence.OnlineEntry{
		{MetaId: "meta-local", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000000500},
	}

	got := store.GlobalOnline(local, now)

	if len(got) != 1 {
		t.Fatalf("global online entries: want 1 got %d", len(got))
	}
	if got[0].MetaId != "meta-local" || got[0].Type != "pc" {
		t.Fatalf("global online entry should preserve local identity, got %#v", got[0])
	}
	if got[0].ConnectedAt != 1710000000000 {
		t.Fatalf("connectedAt: want 1710000000000 got %d", got[0].ConnectedAt)
	}
	if got[0].LastSeenAt != 1710000000500 {
		t.Fatalf("lastSeenAt: want 1710000000500 got %d", got[0].LastSeenAt)
	}
	if got[0].Sources != 1 {
		t.Fatalf("sources: want 1 got %d", got[0].Sources)
	}
	assertStringSliceEqual(t, got[0].SourceNodeIds, []string{"node-local"})
}

func TestGlobalOnlineAggregatesSameMetaIdTypeAcrossNodes(t *testing.T) {
	now := time.UnixMilli(1710000100000)
	store := newAggregateStore(now)
	store.UpsertPeer(testRegistryNode("node-remote"))
	store.UpsertSnapshot(testPresenceSnapshot("node-remote", now.Add(-time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000009000},
	}))
	local := []presence.OnlineEntry{
		{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000005000, LastSeenAt: 1710000006000},
	}

	got := store.GlobalOnline(local, now)

	if len(got) != 1 {
		t.Fatalf("global online entries should merge duplicate metaid/type: got %d", len(got))
	}
	if got[0].ConnectedAt != 1710000000000 {
		t.Fatalf("connectedAt should choose earliest value: got %d", got[0].ConnectedAt)
	}
	if got[0].LastSeenAt != 1710000009000 {
		t.Fatalf("lastSeenAt should choose latest value: got %d", got[0].LastSeenAt)
	}
	if got[0].Sources != 2 {
		t.Fatalf("sources should count source observations before merge: got %d", got[0].Sources)
	}
	assertStringSliceEqual(t, got[0].SourceNodeIds, []string{"node-local", "node-remote"})
}

func TestGlobalOnlineExcludesExpiredSnapshots(t *testing.T) {
	now := time.UnixMilli(1710000100000)
	store := newAggregateStore(now)
	store.UpsertPeer(testRegistryNode("node-expired"))
	store.UpsertSnapshot(testPresenceSnapshot("node-expired", now.Add(-91*time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-expired", Type: "app", ConnectedAt: 1710000000000, LastSeenAt: 1710000009000},
	}))
	local := []presence.OnlineEntry{
		{MetaId: "meta-local", Type: "pc", ConnectedAt: 1710000005000, LastSeenAt: 1710000006000},
	}

	got := store.GlobalOnline(local, now)

	if len(got) != 1 {
		t.Fatalf("expired remote snapshot should be excluded, got %d entries", len(got))
	}
	if got[0].MetaId != "meta-local" {
		t.Fatalf("expired remote metaid should not appear, got %#v", got[0])
	}
}

func TestGlobalOnlineExcludesUnknownPeerSnapshots(t *testing.T) {
	now := time.UnixMilli(1710000100000)
	store := newAggregateStore(now)
	store.UpsertSnapshot(testPresenceSnapshot("node-unknown", now.Add(-time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-unknown", Type: "app", ConnectedAt: 1710000000000, LastSeenAt: 1710000009000},
	}))
	local := []presence.OnlineEntry{
		{MetaId: "meta-local", Type: "pc", ConnectedAt: 1710000005000, LastSeenAt: 1710000006000},
	}

	got := store.GlobalOnline(local, now)

	if len(got) != 1 {
		t.Fatalf("unknown peer snapshot should be excluded, got %d entries", len(got))
	}
	if got[0].MetaId != "meta-local" {
		t.Fatalf("unknown peer metaid should not appear, got %#v", got[0])
	}

	stats := store.StatsAt(local, now)
	if stats.TotalConnections != 1 {
		t.Fatalf("unknown peer snapshot should not contribute totalConnections, got %d", stats.TotalConnections)
	}
	if stats.UniqueMetaIds != 1 {
		t.Fatalf("unknown peer snapshot should not contribute uniqueMetaIds, got %d", stats.UniqueMetaIds)
	}
	if stats.Nodes != 1 {
		t.Fatalf("unknown peer snapshot should not contribute nodes, got %d", stats.Nodes)
	}
}

func TestGlobalOnlineExcludesLateSnapshotAfterRemovePeer(t *testing.T) {
	now := time.UnixMilli(1710000100000)
	store := newAggregateStore(now)
	store.UpsertPeer(testRegistryNode("node-removed"))
	store.UpsertSnapshot(testPresenceSnapshot("node-removed", now.Add(-2*time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-before-remove", Type: "app", ConnectedAt: 1710000000000, LastSeenAt: 1710000008000},
	}))
	store.RemovePeer("node-removed")
	store.UpsertSnapshot(testPresenceSnapshot("node-removed", now.Add(-time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-after-remove", Type: "app", ConnectedAt: 1710000000000, LastSeenAt: 1710000009000},
	}))
	local := []presence.OnlineEntry{
		{MetaId: "meta-local", Type: "pc", ConnectedAt: 1710000005000, LastSeenAt: 1710000006000},
	}

	got := store.GlobalOnline(local, now)

	if len(got) != 1 {
		t.Fatalf("late snapshot after RemovePeer should be excluded, got %d entries", len(got))
	}
	if got[0].MetaId != "meta-local" {
		t.Fatalf("late removed-peer metaid should not appear, got %#v", got[0])
	}

	stats := store.StatsAt(local, now)
	if stats.TotalConnections != 1 {
		t.Fatalf("late removed-peer snapshot should not contribute totalConnections, got %d", stats.TotalConnections)
	}
	if stats.UniqueMetaIds != 1 {
		t.Fatalf("late removed-peer snapshot should not contribute uniqueMetaIds, got %d", stats.UniqueMetaIds)
	}
	if stats.Nodes != 1 {
		t.Fatalf("late removed-peer snapshot should not contribute nodes, got %d", stats.Nodes)
	}
}

func TestAggregateOnlineListPaginatesAfterMergeAndStableSort(t *testing.T) {
	now := time.UnixMilli(1710000100000)
	store := newAggregateStore(now)
	store.UpsertPeer(testRegistryNode("node-a"))
	store.UpsertSnapshot(testPresenceSnapshot("node-a", now.Add(-time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-a", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000007000},
		{MetaId: "meta-a", Type: "app", ConnectedAt: 1710000000000, LastSeenAt: 1710000005000},
		{MetaId: "meta-b", Type: "app", ConnectedAt: 1710000000000, LastSeenAt: 1710000005000},
	}))
	local := []presence.OnlineEntry{
		{MetaId: "meta-a", Type: "pc", ConnectedAt: 1710000001000, LastSeenAt: 1710000001000},
		{MetaId: "meta-c", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000001000},
	}

	got := store.OnlineList(local, 2, 2)

	if len(got) != 2 {
		t.Fatalf("page 2 online entries: want 2 got %d", len(got))
	}
	if got[0].MetaId != "meta-b" || got[0].Type != "app" {
		t.Fatalf("first page-2 item should be meta-b app after merge/sort, got %#v", got[0])
	}
	if got[1].MetaId != "meta-c" || got[1].Type != "pc" {
		t.Fatalf("second page-2 item should be meta-c pc after merge/sort, got %#v", got[1])
	}
}

func TestAggregateStatsCountsObservationsBeforeMerge(t *testing.T) {
	now := time.UnixMilli(1710000100000)
	store := newAggregateStore(now)
	store.UpsertPeer(testRegistryNode("node-remote"))
	store.UpsertSnapshot(testPresenceSnapshot("node-remote", now.Add(-time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000003000},
	}))
	local := []presence.OnlineEntry{
		{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000001000, LastSeenAt: 1710000001000},
		{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000002000, LastSeenAt: 1710000002000},
	}

	stats := store.StatsAt(local, now)

	if stats.TotalConnections != 3 {
		t.Fatalf("totalConnections should count observations before merge: got %d", stats.TotalConnections)
	}
	if stats.UniqueMetaIds != 1 {
		t.Fatalf("uniqueMetaIds should count distinct metaids after filtering: got %d", stats.UniqueMetaIds)
	}
	if stats.Nodes != 2 {
		t.Fatalf("nodes should count local plus non-expired remote nodes: got %d", stats.Nodes)
	}
}

func TestAggregateStatsUniqueMetaIdsAndNodesSemantics(t *testing.T) {
	now := time.UnixMilli(1710000100000)
	store := newAggregateStore(now)
	store.UpsertPeer(testRegistryNode("node-active"))
	store.UpsertPeer(testRegistryNode("node-expired"))
	store.UpsertSnapshot(testPresenceSnapshot("node-active", now.Add(-time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-1", Type: "app", ConnectedAt: 1710000001000, LastSeenAt: 1710000002000},
		{MetaId: "meta-2", Type: "pc", ConnectedAt: 1710000001000, LastSeenAt: 1710000002000},
	}))
	store.UpsertSnapshot(testPresenceSnapshot("node-expired", now.Add(-91*time.Second), 90, []presence.OnlineEntry{
		{MetaId: "meta-expired", Type: "pc", ConnectedAt: 1710000001000, LastSeenAt: 1710000009000},
	}))
	local := []presence.OnlineEntry{
		{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000001000, LastSeenAt: 1710000002000},
	}

	stats := store.StatsAt(local, now)

	if stats.TotalConnections != 3 {
		t.Fatalf("totalConnections should include local plus active remote observations: got %d", stats.TotalConnections)
	}
	if stats.UniqueMetaIds != 2 {
		t.Fatalf("uniqueMetaIds should exclude expired snapshots and dedupe metaids: got %d", stats.UniqueMetaIds)
	}
	if stats.Nodes != 2 {
		t.Fatalf("nodes should be local plus active non-expired remote nodes: got %d", stats.Nodes)
	}
}

func newAggregateStore(now time.Time) *Store {
	return NewStore("node-local", WithStoreClock(func() time.Time { return now }))
}

func testRegistryNode(nodeID string) RegistryNode {
	return RegistryNode{
		NodeID:      nodeID,
		PresenceURL: "https://example.test/" + nodeID + "/presence",
		PublicKey:   "02abcdef",
		ValidUntil:  1710000200000,
	}
}

func testPresenceSnapshot(nodeID string, generatedAt time.Time, ttlSeconds int64, items []presence.OnlineEntry) presence.Snapshot {
	return presence.Snapshot{
		Protocol:    ProtocolPresence,
		Version:     Version,
		NodeID:      nodeID,
		GeneratedAt: generatedAt.UnixMilli(),
		TTLSeconds:  ttlSeconds,
		Sequence:    1,
		Items:       append([]presence.OnlineEntry(nil), items...),
		Signature:   "signature",
	}
}

func assertStringSliceEqual(t *testing.T, got, want []string) {
	t.Helper()

	if len(got) != len(want) {
		t.Fatalf("string slice length:\nwant: %v\n got: %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("string slice:\nwant: %v\n got: %v", want, got)
		}
	}
}
