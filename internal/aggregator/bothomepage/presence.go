package bothomepage

import (
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

// globalPresenceLookupSize matches the other presence consumers (botsearch,
// socket): point lookups must scan the full merged online set, because
// truncating the sorted list at a small page silently flips online bots to
// unknown once the federation grows past the page size.
const globalPresenceLookupSize = 10000

// globalPresenceAllReader is implemented by readers able to return the full
// merged online set without pagination.
type globalPresenceAllReader interface {
	OnlineEntries(local []presence.OnlineEntry) []presence.OnlineEntry
}

func (a *Aggregator) resolvePresence(profile ProfileSnapshot, include bool) Presence {
	if !include {
		return unknownPresence()
	}

	candidates := identityCandidates(profile)
	if len(candidates) == 0 {
		return unknownPresence()
	}

	var local []presence.OnlineEntry
	if a != nil && a.localPresence != nil {
		local = a.localPresence.OnlineEntries()
	}

	if a != nil && a.globalPresence != nil && a.globalPresence.Enabled() {
		if item, ok := findPresence(globalPresenceLookupList(a.globalPresence, local), candidates); ok {
			return presenceFromEntry(item, "federated-presence")
		}
	}

	if item, ok := findPresence(local, candidates); ok {
		return presenceFromEntry(item, "local-presence")
	}

	return unknownPresence()
}

// globalPresenceLookupList returns the merged online set for a point lookup,
// preferring the unpaginated reader fast path over the size-capped fallback.
func globalPresenceLookupList(reader presence.GlobalReader, local []presence.OnlineEntry) []presence.OnlineEntry {
	if allReader, ok := reader.(globalPresenceAllReader); ok {
		return allReader.OnlineEntries(local)
	}
	return reader.OnlineList(local, 1, globalPresenceLookupSize)
}

func identityCandidates(profile ProfileSnapshot) []string {
	candidates := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)

	for _, value := range []string{profile.GlobalMetaId, profile.MetaId, profile.Address} {
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

func presenceFromEntry(item presence.OnlineEntry, source string) Presence {
	updatedAt := item.LastSeenAt
	if updatedAt == 0 {
		updatedAt = item.ConnectedAt
	}
	return Presence{
		State:     "online",
		UpdatedAt: &updatedAt,
		Source:    source,
	}
}
