package federation

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/metaid-developers/meta-socket/internal/presence"
)

type fakeLocalReader struct {
	entries []presence.OnlineEntry
}

func (r fakeLocalReader) OnlineEntries() []presence.OnlineEntry {
	return append([]presence.OnlineEntry(nil), r.entries...)
}

func TestSnapshotBuildsDeterministicLocalPresenceSnapshot(t *testing.T) {
	reader := fakeLocalReader{entries: []presence.OnlineEntry{
		{
			MetaId:      "meta-1",
			Type:        "pc",
			ConnectedAt: 1710000000000,
			LastSeenAt:  1710000000500,
		},
	}}
	builder := NewSnapshotBuilder(
		reader,
		"node-a",
		30,
		WithClock(func() time.Time { return time.UnixMilli(1710000001000) }),
		WithSequence(func() uint64 { return 7 }),
	)

	snapshot, err := builder.Snapshot()
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	if snapshot.Protocol != ProtocolPresence {
		t.Fatalf("protocol: want %q got %q", ProtocolPresence, snapshot.Protocol)
	}
	if snapshot.Version != Version {
		t.Fatalf("version: want %q got %q", Version, snapshot.Version)
	}
	if snapshot.NodeID != "node-a" {
		t.Fatalf("node id: want node-a got %q", snapshot.NodeID)
	}
	if snapshot.GeneratedAt != 1710000001000 {
		t.Fatalf("generated at: want 1710000001000 got %d", snapshot.GeneratedAt)
	}
	if snapshot.TTLSeconds != 30 {
		t.Fatalf("ttl seconds: want 30 got %d", snapshot.TTLSeconds)
	}
	if snapshot.Sequence != 7 {
		t.Fatalf("sequence: want 7 got %d", snapshot.Sequence)
	}
	if snapshot.Signature != "" {
		t.Fatalf("signature should be empty for unsigned Task 2 snapshots, got %q", snapshot.Signature)
	}
	if len(snapshot.Items) != 1 {
		t.Fatalf("items: want 1 got %d", len(snapshot.Items))
	}
	if snapshot.Items[0].LastSeenAt != 1710000000500 {
		t.Fatalf("lastSeenAt: want 1710000000500 got %d", snapshot.Items[0].LastSeenAt)
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	want := `{"protocol":"metasocket-presence","version":"1.0.0","nodeId":"node-a","generatedAt":1710000001000,"ttlSeconds":30,"sequence":7,"items":[{"metaid":"meta-1","type":"pc","connectedAt":1710000000000,"lastSeenAt":1710000000500}],"signature":""}`
	if string(raw) != want {
		t.Fatalf("unexpected snapshot json:\nwant: %s\n got: %s", want, raw)
	}
}

func TestSnapshotExcludesSocketIDs(t *testing.T) {
	reader := fakeLocalReader{entries: []presence.OnlineEntry{
		{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000000500},
	}}
	builder := NewSnapshotBuilder(
		reader,
		"node-a",
		30,
		WithClock(func() time.Time { return time.UnixMilli(1710000001000) }),
		WithSequence(func() uint64 { return 7 }),
	)

	snapshot, err := builder.Snapshot()
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode snapshot json: %v", err)
	}
	assertNoJSONKey(t, decoded, "socketId")
	assertNoJSONKey(t, decoded, "socketID")
}

func TestSnapshotSortsItemsDeterministically(t *testing.T) {
	reader := fakeLocalReader{entries: []presence.OnlineEntry{
		{MetaId: "meta-b", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000000500},
		{MetaId: "meta-a", Type: "pc", ConnectedAt: 1710000001000, LastSeenAt: 1710000001500},
		{MetaId: "meta-a", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000000500},
		{MetaId: "meta-a", Type: "app", ConnectedAt: 1710000003000, LastSeenAt: 1710000003500},
		{MetaId: "meta-a", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000000400},
	}}
	builder := NewSnapshotBuilder(
		reader,
		"node-a",
		30,
		WithClock(func() time.Time { return time.UnixMilli(1710000004000) }),
		WithSequence(func() uint64 { return 8 }),
	)

	snapshot, err := builder.Snapshot()
	if err != nil {
		t.Fatalf("build snapshot: %v", err)
	}

	got := make([]string, 0, len(snapshot.Items))
	for _, item := range snapshot.Items {
		got = append(got, item.MetaId+"|"+item.Type+"|"+itemKeyNumber(item.ConnectedAt)+"|"+itemKeyNumber(item.LastSeenAt))
	}
	want := []string{
		"meta-a|app|1710000003000|1710000003500",
		"meta-a|pc|1710000000000|1710000000400",
		"meta-a|pc|1710000000000|1710000000500",
		"meta-a|pc|1710000001000|1710000001500",
		"meta-b|pc|1710000000000|1710000000500",
	}
	if !equalStringSlices(got, want) {
		t.Fatalf("snapshot item order:\nwant: %v\n got: %v", want, got)
	}
}

func TestSnapshotBuilderWithSigningKeyProducesVerifiableSignature(t *testing.T) {
	reader := fakeLocalReader{entries: []presence.OnlineEntry{
		{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000000500},
	}}
	builder := NewSnapshotBuilder(
		reader,
		"node-a",
		30,
		WithClock(func() time.Time { return time.UnixMilli(1710000001000) }),
		WithSequence(func() uint64 { return 7 }),
		WithSnapshotSigningKey(testPrivateKeyHex),
	)

	snapshot, err := builder.Snapshot()
	if err != nil {
		t.Fatalf("build signed snapshot: %v", err)
	}

	if snapshot.Signature == "" {
		t.Fatal("signed snapshot should include a signature")
	}
	if err := VerifySnapshot(snapshot, "node-a", testPublicKeyHex); err != nil {
		t.Fatalf("verify signed snapshot: %v", err)
	}
}

func itemKeyNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertNoJSONKey(t *testing.T, value any, forbidden string) {
	t.Helper()

	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == forbidden {
				t.Fatalf("snapshot json exposed forbidden key %q", forbidden)
			}
			assertNoJSONKey(t, child, forbidden)
		}
	case []any:
		for _, child := range typed {
			assertNoJSONKey(t, child, forbidden)
		}
	}
}
