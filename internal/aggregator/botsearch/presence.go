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
// Mirrors bothomepage's findPresence.
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

func identityCandidates(globalMetaId, metaId, address string) []string {
	candidates := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	for _, value := range []string{globalMetaId, metaId, address} {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, trimmed)
	}
	return candidates
}
