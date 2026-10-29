package notify

// ChatBlockFilter adapts the §4.6 read-side merge (IsChatBlockedMerged) to
// the per-read filter question consumed by the privatechat surfaces. It is
// duck-typed into privatechat.ChatBlockFilter at the wiring site; this
// package stays independent of privatechat.
type ChatBlockFilter struct {
	agg   *Aggregator
	chain ChainBlockSource
}

// NewChatBlockFilter builds the unified chat filter over the notify session
// read model merged with the on-chain block aggregation (spec §4.6 union
// semantics: a chat surface is filtered when either source says blocked).
// notifyAgg is the notify read model; chain is the on-chain aggregation
// (the block aggregator satisfies ChainBlockSource). A nil notifyAgg yields
// a nil filter (nothing to merge — callers keep legacy behavior); a nil
// chain keeps the merge off-chain-only.
func NewChatBlockFilter(notifyAgg *Aggregator, chain ChainBlockSource) *ChatBlockFilter {
	if notifyAgg == nil {
		return nil
	}
	return &ChatBlockFilter{agg: notifyAgg, chain: chain}
}

// IsChatBlocked reports whether reads of the chat between viewer and peer
// must be filtered. peerAliases carries every known identifier form of the
// peer (canonical GlobalMetaID first when known): blocked entries may be
// keyed by either form, so the pair is blocked when any alias matches.
func (f *ChatBlockFilter) IsChatBlocked(viewer string, peerAliases []string) (bool, error) {
	if f == nil || f.agg == nil {
		return false, nil
	}
	for _, alias := range peerAliases {
		blocked, err := f.agg.IsChatBlockedMerged(viewer, alias, f.chain)
		if err != nil {
			return false, err
		}
		if blocked {
			return true, nil
		}
	}
	return false, nil
}
