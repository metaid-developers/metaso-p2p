package botsearch

import (
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

// presencePageSize is deliberately generous: the search may need to match
// dozens of candidates against the full online set, so truncating the merged
// presence list at a small page would silently mark online bots as offline.
const presencePageSize = 10000

// presenceAvailable reports whether any presence backend can answer. When no
// backend is wired (socket server disabled and federation disabled/absent),
// presence is unavailable and an onlineOnly query must fail with
// presence_unavailable rather than return stale rows.
func (a *Aggregator) presenceAvailable() bool {
	if a == nil {
		return false
	}
	return a.localPresence != nil || (a.globalPresence != nil && a.globalPresence.Enabled())
}

// presenceSnapshot returns the merged online set: the federated global list
// when enabled, otherwise the local entries alone. Same source preference as
// the bothomepage aggregator.
func (a *Aggregator) presenceSnapshot() []presence.OnlineEntry {
	if a == nil {
		return nil
	}
	var local []presence.OnlineEntry
	if a.localPresence != nil {
		local = a.localPresence.OnlineEntries()
	}
	if a.globalPresence != nil && a.globalPresence.Enabled() {
		return a.globalPresence.OnlineList(local, 1, presencePageSize)
	}
	return local
}

// findPresence matches any of the identity aliases (globalMetaId / metaId /
// address, case-insensitive) against the online entries' MetaId field.
// Mirrors bothomepage's findPresence. Reference implementation retained for
// tests; the handler uses presenceIndex instead — scanning the full online
// set per profile with a ToLower per comparison was 87% of request CPU
// (production pprof, 2026-11-20).
func findPresence(items []presence.OnlineEntry, candidates []string) (presence.OnlineEntry, bool) {
	if len(items) == 0 || len(candidates) == 0 {
		return presence.OnlineEntry{}, false
	}
	lookup := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		lookup[strings.ToLower(trimmed)] = struct{}{}
	}
	for _, item := range items {
		if _, ok := lookup[strings.ToLower(strings.TrimSpace(item.MetaId))]; ok {
			return item, true
		}
	}
	return presence.OnlineEntry{}, false
}

// presenceIndex is the per-request lowercase index over the merged online
// set. Building it once turns the per-profile presence join into O(1) map
// probes per identity alias.
type presenceIndex struct {
	byIdentity map[string]presence.OnlineEntry
}

func newPresenceIndex(items []presence.OnlineEntry) presenceIndex {
	index := presenceIndex{byIdentity: make(map[string]presence.OnlineEntry, len(items))}
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.MetaId))
		if key == "" {
			continue
		}
		if _, ok := index.byIdentity[key]; !ok {
			index.byIdentity[key] = item
		}
	}
	return index
}

// lookup reports whether any identity alias is online, first alias wins.
// Matches findPresence semantics up to tie-breaking: findPresence returns the
// first matching entry in list order, the index returns the entry of the
// first matching alias (globalMetaId before metaId before address).
func (p presenceIndex) lookup(candidates ...string) (presence.OnlineEntry, bool) {
	for _, candidate := range candidates {
		key := strings.ToLower(strings.TrimSpace(candidate))
		if key == "" {
			continue
		}
		if item, ok := p.byIdentity[key]; ok {
			return item, true
		}
	}
	return presence.OnlineEntry{}, false
}
