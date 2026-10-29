package privatechat

import (
	"strings"
)

// ChatBlockFilter answers the unified chat-filter question of spec
// 2026-10-29 §4.6 for one chat-surface read: is the chat between viewer
// and peer filtered because either source (the off-chain session entry or
// the on-chain block aggregation) says blocked? Implemented by
// notify.NewChatBlockFilter over the notify read model merged with the
// chain aggregation; duck-typed so this package stays independent of
// notify/block.
type ChatBlockFilter interface {
	// IsChatBlocked reports whether reads of the chat between viewer and
	// peer must be filtered. peerAliases carries every known identifier
	// form of the peer (canonical GlobalMetaID first when known) so either
	// keying of a blocked entry matches.
	IsChatBlocked(viewer string, peerAliases []string) (bool, error)
}

// SetChatBlockFilter wires the unified chat filter. Optional: without it
// the chat read paths keep their legacy unfiltered behavior; a nil value
// unwires an existing filter.
func (a *Aggregator) SetChatBlockFilter(filter ChatBlockFilter) {
	a.chatBlockFilter = filter
}

// chatSurfaceBlocked consults the wired filter for one (viewer, peer) read.
// A missing filter or an empty alias list keeps legacy behavior; filter
// errors propagate so a read whose filter state cannot be determined fails
// instead of silently leaking blocked content.
func (a *Aggregator) chatSurfaceBlocked(viewer string, peerAliases []string) (bool, error) {
	if a == nil || a.chatBlockFilter == nil || len(peerAliases) == 0 {
		return false, nil
	}
	return a.chatBlockFilter.IsChatBlocked(viewer, peerAliases)
}

// peerBlockedAliases resolves every known identifier form of peer (the raw
// input first, then the profile-resolved aliases) and consults the filter.
// Without a wired filter no identity lookup happens — reads keep their
// exact pre-wiring behavior.
func (a *Aggregator) peerBlockedAliases(viewer, peer string, profiles identityProfileCache) (bool, error) {
	if a == nil || a.chatBlockFilter == nil {
		return false, nil
	}
	return a.chatSurfaceBlocked(viewer, a.identityAliasesCached(peer, profiles))
}

// emptyPrivateChatListResult is the filtered view of a whole-conversation
// read: same envelope as an empty conversation, no messages leaked.
func emptyPrivateChatListResult() *PrivateChatListResult {
	return &PrivateChatListResult{List: []*PrivateMessage{}}
}

// dropBlockedHomes removes conversation partners the viewer has blocked
// (either source) from a chat-homes listing.
func (a *Aggregator) dropBlockedHomes(viewer string, homes []*PrivateChatHome) ([]*PrivateChatHome, error) {
	if a == nil || a.chatBlockFilter == nil || len(homes) == 0 {
		return homes, nil
	}
	kept := homes[:0]
	for _, home := range homes {
		blocked, err := a.chatSurfaceBlocked(viewer, uniqueNonEmpty(home.GlobalMetaId, home.MetaId))
		if err != nil {
			return nil, err
		}
		if !blocked {
			kept = append(kept, home)
		}
	}
	return kept, nil
}

// dropBlockedPaths removes conversation paths whose other participant the
// viewer has blocked (either source). A path whose peer cannot be derived
// is kept as-is (it cannot be attributed to a blockable pair).
func (a *Aggregator) dropBlockedPaths(viewer string, paths []*PrivateGroupPath) ([]*PrivateGroupPath, error) {
	if a == nil || a.chatBlockFilter == nil || len(paths) == 0 {
		return paths, nil
	}
	profiles := make(identityProfileCache)
	kept := paths[:0]
	for _, path := range paths {
		peer, ok := pathPeer(path.Path, viewer)
		if !ok {
			kept = append(kept, path)
			continue
		}
		blocked, err := a.peerBlockedAliases(viewer, peer, profiles)
		if err != nil {
			return nil, err
		}
		if !blocked {
			kept = append(kept, path)
		}
	}
	return kept, nil
}

// pathPeer derives the other participant of a "<lo>:<hi>" conversation
// path when viewer is one of its components.
func pathPeer(path, viewer string) (string, bool) {
	if peer, found := strings.CutPrefix(path, viewer+":"); found {
		return peer, true
	}
	if peer, found := strings.CutSuffix(path, ":"+viewer); found {
		return peer, true
	}
	return "", false
}

// uniqueNonEmpty keeps the given identifiers in order, dropping empties and
// duplicates (case-insensitive).
func uniqueNonEmpty(ids ...string) []string {
	out := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, id)
	}
	return out
}
