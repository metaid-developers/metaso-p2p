package social

import (
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/block"
)

// BlockPeerSource is the narrow shape of the S3 block aggregation
// (internal/aggregator/block) that backs the unified filter layer: the
// *block.Aggregator implements it directly. Keeping it duck-typed here lets
// the social package depend on the real implementation without the block
// package knowing anything about its consumers (spec §4.5 — filtered
// surfaces depend on BlockLister, never on block storage).
type BlockPeerSource interface {
	// IsBlockedBetween reports whether either direction of the pair is
	// currently active (spec §4.5); inputs accept both identifier forms.
	IsBlockedBetween(a, b string) (bool, error)
	// BlockingPeers lists peers the subject currently blocks, newest first.
	BlockingPeers(subject string) ([]block.BlockPeer, error)
	// BlockedByPeers lists peers currently blocking the subject.
	BlockedByPeers(subject string) ([]block.BlockPeer, error)
}

// NewBlockLister adapts the S3 block aggregation to the unified BlockLister
// filter consumed by the following/followers/friends/counts read paths
// (spec §4.5). Peer entries collapse to their canonical GlobalMetaIDs; a
// nil source returns nil, which SetBlockLister folds back to the no-op
// default.
func NewBlockLister(source BlockPeerSource) BlockLister {
	if source == nil {
		return nil
	}
	return blockPeerLister{source: source}
}

type blockPeerLister struct {
	source BlockPeerSource
}

func (l blockPeerLister) IsBlockedBetween(a, b string) (bool, error) {
	return l.source.IsBlockedBetween(a, b)
}

func (l blockPeerLister) BlockingBy(subject string) ([]string, error) {
	peers, err := l.source.BlockingPeers(subject)
	if err != nil {
		return nil, err
	}
	return blockPeerIds(peers), nil
}

func (l blockPeerLister) BlockedBy(subject string) ([]string, error) {
	peers, err := l.source.BlockedByPeers(subject)
	if err != nil {
		return nil, err
	}
	return blockPeerIds(peers), nil
}

func blockPeerIds(peers []block.BlockPeer) []string {
	ids := make([]string, 0, len(peers))
	for _, peer := range peers {
		ids = append(ids, peer.GlobalMetaId)
	}
	return ids
}
