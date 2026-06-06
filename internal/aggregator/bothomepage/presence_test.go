package bothomepage

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

type fakeLocalPresence struct {
	entries []presence.OnlineEntry
	calls   int
}

func (f *fakeLocalPresence) OnlineEntries() []presence.OnlineEntry {
	f.calls++
	return append([]presence.OnlineEntry(nil), f.entries...)
}

type fakeGlobalPresence struct {
	enabled   bool
	items     []presence.OnlineEntry
	calls     int
	lastLocal []presence.OnlineEntry
	lastPage  int
	lastSize  int
}

func (f *fakeGlobalPresence) Enabled() bool {
	return f.enabled
}

func (f *fakeGlobalPresence) DefaultScope() string {
	return "global"
}

func (f *fakeGlobalPresence) OnlineList(local []presence.OnlineEntry, page int, size int) []presence.OnlineEntry {
	f.calls++
	f.lastLocal = append([]presence.OnlineEntry(nil), local...)
	f.lastPage = page
	f.lastSize = size
	return append([]presence.OnlineEntry(nil), f.items...)
}

func (f *fakeGlobalPresence) Stats(local []presence.OnlineEntry) presence.GlobalStats {
	return presence.GlobalStats{}
}

func TestResolvePresenceUnknownWithoutReaders(t *testing.T) {
	agg := &Aggregator{}

	got := agg.resolvePresence(ProfileSnapshot{GlobalMetaId: "idqBot"}, true)

	assertUnknownPresence(t, got)
}

func TestResolvePresenceOnlineFromGlobalReader(t *testing.T) {
	local := &fakeLocalPresence{entries: []presence.OnlineEntry{{
		MetaId:      "local-only",
		ConnectedAt: 100,
	}}}
	global := &fakeGlobalPresence{
		enabled: true,
		items: []presence.OnlineEntry{{
			MetaId:      "idqbot",
			ConnectedAt: 111,
			LastSeenAt:  222,
		}},
	}
	agg := &Aggregator{}
	agg.SetPresenceReaders(local, global)

	got := agg.resolvePresence(ProfileSnapshot{
		GlobalMetaId: " IDQBot ",
		MetaId:       "metaBot",
		Address:      "1BotAddress",
	}, true)

	if got.State != "online" {
		t.Fatalf("Presence.State = %q, want online", got.State)
	}
	if got.Source != "federated-presence" {
		t.Fatalf("Presence.Source = %q, want federated-presence", got.Source)
	}
	if got.UpdatedAt == nil || *got.UpdatedAt != 222 {
		t.Fatalf("Presence.UpdatedAt = %v, want 222", got.UpdatedAt)
	}
	if local.calls != 1 {
		t.Fatalf("local OnlineEntries calls = %d, want 1", local.calls)
	}
	if global.calls != 1 {
		t.Fatalf("global OnlineList calls = %d, want 1", global.calls)
	}
	if global.lastPage != 1 || global.lastSize != 100 {
		t.Fatalf("global OnlineList page/size = %d/%d, want 1/100", global.lastPage, global.lastSize)
	}
	if len(global.lastLocal) != 1 || global.lastLocal[0].MetaId != "local-only" {
		t.Fatalf("global OnlineList local entries = %+v, want local-only", global.lastLocal)
	}
}

func TestResolvePresenceDisabledByQuery(t *testing.T) {
	local := &fakeLocalPresence{entries: []presence.OnlineEntry{{
		MetaId:      "idqBot",
		ConnectedAt: 100,
	}}}
	global := &fakeGlobalPresence{
		enabled: true,
		items: []presence.OnlineEntry{{
			MetaId:      "idqBot",
			ConnectedAt: 200,
		}},
	}
	agg := &Aggregator{}
	agg.SetPresenceReaders(local, global)

	got := agg.resolvePresence(ProfileSnapshot{GlobalMetaId: "idqBot"}, false)

	assertUnknownPresence(t, got)
	if local.calls != 0 {
		t.Fatalf("local OnlineEntries calls = %d, want 0", local.calls)
	}
	if global.calls != 0 {
		t.Fatalf("global OnlineList calls = %d, want 0", global.calls)
	}
}

func TestResolvePresenceFallsBackToLocalAddressAndConnectedAt(t *testing.T) {
	local := &fakeLocalPresence{entries: []presence.OnlineEntry{{
		MetaId:      "  1botaddress  ",
		ConnectedAt: 333,
	}}}
	global := &fakeGlobalPresence{
		enabled: true,
		items: []presence.OnlineEntry{{
			MetaId:      "other",
			ConnectedAt: 200,
		}},
	}
	agg := &Aggregator{}
	agg.SetPresenceReaders(local, global)

	got := agg.resolvePresence(ProfileSnapshot{
		GlobalMetaId: "idqBot",
		MetaId:       "metaBot",
		Address:      "1BotAddress",
	}, true)

	if got.State != "online" {
		t.Fatalf("Presence.State = %q, want online", got.State)
	}
	if got.Source != "local-presence" {
		t.Fatalf("Presence.Source = %q, want local-presence", got.Source)
	}
	if got.UpdatedAt == nil || *got.UpdatedAt != 333 {
		t.Fatalf("Presence.UpdatedAt = %v, want 333", got.UpdatedAt)
	}
}

func TestResolvePresenceBuildUsesConfiguredReaders(t *testing.T) {
	agg := &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	agg.SetProfileLookup(&fakeProfileLookup{profile: &ProfileSnapshot{
		GlobalMetaId: "idqBot",
		Name:         "IDQ Bot",
	}})
	agg.SetPresenceReaders(&fakeLocalPresence{entries: []presence.OnlineEntry{{
		MetaId:      "idqBot",
		ConnectedAt: 444,
	}}}, nil)

	got, err := agg.Build("idqBot", DefaultOptions())
	if err != nil {
		t.Fatalf("Build returned error: %v", err)
	}

	if got.Presence.State != "online" {
		t.Fatalf("Build Presence.State = %q, want online", got.Presence.State)
	}
	if got.Presence.Source != "local-presence" {
		t.Fatalf("Build Presence.Source = %q, want local-presence", got.Presence.Source)
	}
	if got.Presence.UpdatedAt == nil || *got.Presence.UpdatedAt != 444 {
		t.Fatalf("Build Presence.UpdatedAt = %v, want 444", got.Presence.UpdatedAt)
	}
}

func assertUnknownPresence(t *testing.T, got Presence) {
	t.Helper()
	if got.State != "unknown" {
		t.Fatalf("Presence.State = %q, want unknown", got.State)
	}
	if got.UpdatedAt != nil {
		t.Fatalf("Presence.UpdatedAt = %v, want nil", *got.UpdatedAt)
	}
	if got.Source != "" {
		t.Fatalf("Presence.Source = %q, want empty", got.Source)
	}
}
