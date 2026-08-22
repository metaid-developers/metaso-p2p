package skillservice

import (
	"strings"
)

// PublishedServiceNames returns the deduplicated names of a provider's
// currently visible skill services (the default visibility filter used by the
// list endpoint), newest first. It scans the existing
// `service_by_provider_global` index, so no new index or backfill is needed;
// a provider with no indexed services yields an empty slice. This backs the
// `publishedSkills` field of the bot search API
// (docs/specs/2026-08-22-bot-search-api.md).
func (a *Aggregator) PublishedServiceNames(providerGlobalMetaId string) ([]string, error) {
	names := []string{}
	if a == nil || a.store == nil {
		return names, nil
	}
	providerGlobalMetaId = strings.TrimSpace(providerGlobalMetaId)
	if providerGlobalMetaId == "" {
		return names, nil
	}

	prefix := providerGlobalIndexPrefix(providerGlobalMetaId)
	seen := make(map[string]struct{})
	err := a.store.ScanPrefix(NamespaceService, prefix, func(key, _ []byte) error {
		candidate, ok := parseProviderGlobalIndexKey(string(key), string(prefix))
		if !ok {
			return nil
		}
		rec, err := a.loadService(candidate.chainName, candidate.sourcePinId)
		if err != nil || rec == nil || !rec.IsVisibleDefault() {
			return nil
		}
		name := strings.TrimSpace(rec.ServiceName)
		if name == "" {
			name = strings.TrimSpace(rec.DisplayName)
		}
		if name == "" {
			return nil
		}
		dedupeKey := strings.ToLower(name)
		if _, dup := seen[dedupeKey]; dup {
			return nil
		}
		seen[dedupeKey] = struct{}{}
		names = append(names, name)
		return nil
	})
	return names, err
}
