package notify

import (
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/block"
)

// Merge with the on-chain Simpleblock aggregation (spec §4.6).
//
// Authority: the on-chain block aggregation (internal/aggregator/block) is
// the authority for chain-level block state; the notify read model
// (`blocked:` entries) stays the session-layer cache for chat UI state.
// Event bridge: every ingested confirmed block pin (either block path) is
// additionally mirrored here; blockState = -1 removes the bridged entry.
// Off-chain-only blocks (add_blocked_chat, never published on-chain) keep
// working exactly as today and never leak into the chain read model.
// Read-side merge: chat surfaces filter when either source says blocked
// (union semantics) — see BlockedChatsMerged.
// Error envelopes are deliberately NOT unified in this round: the notify
// routes keep their legacy code=1 system, the social block endpoints use
// the 40000 family (spec §5.2).

// chainBlockReasonPrefix tags entries written by this bridge; a chain
// unblock only removes entries carrying it (each source keeps its own
// unblock path, spec §4.6).
const chainBlockReasonPrefix = block.ChainBlockReasonPrefix

// ApplyChainBlock records a chain-confirmed block from → to into the notify
// read model. Aliases carry every known identifier form (canonical
// GlobalMetaID first), so the session cache stays reachable whether callers
// key it by global id or legacy hex. Satisfies the GOAL_DRIVEN.md acceptance
// item: after processing a simpleprivateblock pin the record shows up under
// GET /push-base/v1/push/get_user_blocked_chats.
func (a *Aggregator) ApplyChainBlock(fromAliases, toAliases []string, blockPinId string, blockedAt int64) error {
	if a == nil || a.store == nil {
		return nil
	}
	fromAliases = normalizeAliases(fromAliases)
	toAliases = normalizeAliases(toAliases)
	if len(fromAliases) == 0 || len(toAliases) == 0 {
		return nil
	}
	peerChatId := toAliases[0]
	reason := chainBlockReasonPrefix + blockPinId

	for _, ownerAlias := range fromAliases {
		blocked, err := a.getBlockedChats(ownerAlias)
		if err != nil {
			return err
		}
		if blocked == nil {
			blocked = &UserBlockedChats{UserID: ownerAlias, BlockedChats: []BlockedChat{}}
		}

		// Replace any previous bridge entry for the same peer so the entry
		// always reflects the currently active chain pin; untouched
		// off-chain entries for the same chat survive (union semantics).
		filtered := blocked.BlockedChats[:0]
		for _, bc := range blocked.BlockedChats {
			if isSameChat(bc, peerChatId, toAliases) && isChainEntry(bc) {
				continue
			}
			filtered = append(filtered, bc)
		}
		blocked.BlockedChats = append(filtered, BlockedChat{
			ChatID:   peerChatId,
			ChatType: "private",
			MetaID:   ownerAlias,
			Reason:   reason,
		})
		if err := a.saveBlockedChats(blocked); err != nil {
			return err
		}
		a.cache.Delete("blocked:" + ownerAlias)
	}
	return nil
}

// RemoveChainBlock lifts the chain-confirmed block from → to. Only entries
// written by the bridge (chainBlockReasonPrefix provenance) are removed;
// off-chain session entries for the same peer stay untouched (spec §4.6:
// each source keeps its own unblock path).
func (a *Aggregator) RemoveChainBlock(fromAliases, toAliases []string) error {
	if a == nil || a.store == nil {
		return nil
	}
	fromAliases = normalizeAliases(fromAliases)
	toAliases = normalizeAliases(toAliases)
	if len(fromAliases) == 0 || len(toAliases) == 0 {
		return nil
	}

	for _, ownerAlias := range fromAliases {
		blocked, err := a.getBlockedChats(ownerAlias)
		if err != nil {
			return err
		}
		if blocked == nil {
			continue
		}
		removed := false
		filtered := make([]BlockedChat, 0, len(blocked.BlockedChats))
		for _, bc := range blocked.BlockedChats {
			if isSameChat(bc, toAliases[0], toAliases) && isChainEntry(bc) {
				removed = true
				continue
			}
			filtered = append(filtered, bc)
		}
		if !removed {
			continue
		}
		blocked.BlockedChats = filtered
		if err := a.saveBlockedChats(blocked); err != nil {
			return err
		}
		a.cache.Delete("blocked:" + ownerAlias)
	}
	return nil
}

// ChainBlockSource is the narrow view of the on-chain block aggregation the
// union read-side merge needs (duck-typed; implemented by
// block.Aggregator.BlockedPeerIds without a compile-time dependency here).
type ChainBlockSource interface {
	// BlockedPeerIds returns the canonical globalMetaIds of all peers with
	// an active on-chain block in either direction relative to subject.
	BlockedPeerIds(subject string) ([]string, error)
}

// BlockedChatsMerged returns the union of the off-chain session entries and
// the on-chain aggregated block state for metaID (spec §4.6 read-side
// merge). Chat surfaces that filter on this result drop a peer when either
// source says blocked; each source keeps its own unblock path.
func (a *Aggregator) BlockedChatsMerged(metaID string, chain ChainBlockSource) (*UserBlockedChats, error) {
	if a == nil {
		return nil, nil
	}
	merged, err := a.getBlockedChats(metaID)
	if err != nil {
		return nil, err
	}
	if merged == nil {
		merged = &UserBlockedChats{UserID: metaID, BlockedChats: []BlockedChat{}}
	}

	if chain != nil {
		chainPeers, err := chain.BlockedPeerIds(metaID)
		if err != nil {
			return nil, err
		}
		existing := make(map[string]struct{}, len(merged.BlockedChats))
		for _, bc := range merged.BlockedChats {
			existing[strings.ToLower(strings.TrimSpace(bc.ChatID))] = struct{}{}
		}
		for _, peer := range chainPeers {
			peer = strings.TrimSpace(peer)
			if peer == "" {
				continue
			}
			if _, dup := existing[strings.ToLower(peer)]; dup {
				continue
			}
			merged.BlockedChats = append(merged.BlockedChats, BlockedChat{
				ChatID:   peer,
				ChatType: "private",
				MetaID:   metaID,
				Reason:   chainBlockReasonPrefix + "aggregated",
			})
		}
	}
	return merged, nil
}

// IsChatBlockedMerged answers the single chat-surface filter question:
// is the peer blocked from metaID's view under the union semantics
// (off-chain session entry OR on-chain aggregated state)?
func (a *Aggregator) IsChatBlockedMerged(metaID, peerIdentity string, chain ChainBlockSource) (bool, error) {
	blocked, err := a.BlockedChatsMerged(metaID, chain)
	if err != nil {
		return false, err
	}
	if blocked == nil {
		return false, nil
	}
	peer := strings.ToLower(strings.TrimSpace(peerIdentity))
	if peer == "" {
		return false, nil
	}
	for _, bc := range blocked.BlockedChats {
		chatId := strings.ToLower(strings.TrimSpace(bc.ChatID))
		if chatId == peer {
			return true, nil
		}
	}
	return false, nil
}

func normalizeAliases(aliases []string) []string {
	out := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		key := strings.ToLower(alias)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func isChainEntry(bc BlockedChat) bool {
	return strings.HasPrefix(strings.TrimSpace(bc.Reason), chainBlockReasonPrefix)
}

func isSameChat(bc BlockedChat, primaryChatId string, aliases []string) bool {
	chatId := strings.ToLower(strings.TrimSpace(bc.ChatID))
	if chatId == "" {
		return false
	}
	if chatId == strings.ToLower(strings.TrimSpace(primaryChatId)) {
		return true
	}
	for _, alias := range aliases {
		if chatId == strings.ToLower(strings.TrimSpace(alias)) {
			return true
		}
	}
	return false
}
