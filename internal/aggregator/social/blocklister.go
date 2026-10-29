package social

import (
	"fmt"
	"strings"
)

// BlockLister is the unified block filter layer of spec 2026-10-29 §4.5.
// All filtered read surfaces (following/followers/friends lists and the
// counts endpoint) depend on this interface, never on block storage directly.
// The batch relationship endpoint is the deliberate exception: it reports
// block state instead of filtering.
//
// Until the Simpleblock aggregation (S3) delivers the real implementation,
// Aggregator runs on the no-op default below; identities passed in and
// returned are canonical GlobalMetaIDs.
type BlockLister interface {
	// IsBlockedBetween reports whether either direction of the pair is
	// currently blocked (blockState = 1).
	IsBlockedBetween(a, b string) (bool, error)
	// BlockingBy lists peers the subject currently blocks.
	// BlockedBy lists peers currently blocking the subject.
	// Both are filtered-view primitives used by lists and counters.
	BlockingBy(subject string) ([]string, error)
	BlockedBy(subject string) ([]string, error)
}

// noopBlockLister is the safe-by-default BlockLister installed by Init.
type noopBlockLister struct{}

func (noopBlockLister) IsBlockedBetween(a, b string) (bool, error)  { return false, nil }
func (noopBlockLister) BlockingBy(subject string) ([]string, error) { return nil, nil }
func (noopBlockLister) BlockedBy(subject string) ([]string, error)  { return nil, nil }

// SetBlockLister swaps the block filter implementation. A nil value resets to
// the no-op default.
func (a *Aggregator) SetBlockLister(lister BlockLister) {
	if lister == nil {
		lister = noopBlockLister{}
	}
	a.blockLister = lister
}

func (a *Aggregator) currentBlockLister() BlockLister {
	if a == nil || a.blockLister == nil {
		return noopBlockLister{}
	}
	return a.blockLister
}

// blockedPeerSet returns the union of BlockingBy(subject) and
// BlockedBy(subject): every peer that must be filtered out of the subject's
// list reads (spec §4.5 — no includeBlocked escape switch).
func (a *Aggregator) blockedPeerSet(subject string) (map[string]struct{}, error) {
	lister := a.currentBlockLister()
	blocking, err := lister.BlockingBy(subject)
	if err != nil {
		return nil, unavailableWrap(err)
	}
	blockedBy, err := lister.BlockedBy(subject)
	if err != nil {
		return nil, unavailableWrap(err)
	}
	set := make(map[string]struct{}, len(blocking)+len(blockedBy))
	for _, peer := range append(blocking, blockedBy...) {
		if peer = strings.TrimSpace(peer); peer != "" {
			set[peer] = struct{}{}
		}
	}
	return set, nil
}

func unavailableWrap(err error) error {
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}
