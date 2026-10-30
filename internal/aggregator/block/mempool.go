package block

import (
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Mempool instant-effect overlay (spec 2026-10-29 §4.4b, owner decision
// 2026-10-30 reversing OQ-2): unconfirmed simpleblock pins take effect in
// every read view immediately; confirmation folds the pin into the confirmed
// model and consumes the overlay entry, so the transition is seamless.
//
// Storage (namespace "block", separate keys from the confirmed model):
//
//	blk:mp:pair:<from>:<to> -> pendingBlock JSON (the newest unconfirmed
//	                              mutation for the pair)
//	blk:mp:pin:<pinId>      -> pendingPinRef JSON (every unconfirmed block
//	                              pin, for confirmation consumption)
//
// Semantics mirror the social overlay: confirmed model untouched, one
// overlay entry per pair LWW by arrival seq, blockState=-1 is an unblock
// tombstone hiding the confirmed block, confirmation consumes, expiry
// (janitor) drops never-confirmed entries and reveals the confirmed state.
// The notify session-cache bridge (§4.6) stays confirmed-only — chat
// filtering is mempool-fast through the read-side union (BlockedPeerIds),
// which consults this overlay on every read.
const (
	keyPendingPair = "blk:mp:pair:"
	keyPendingPin  = "blk:mp:pin:"
)

type pendingBlock struct {
	BlockState  int64  `json:"blockState"` // 1 = block, -1 = unblock tombstone
	BlockPinId  string `json:"blockPinId"`
	BlockedAt   int64  `json:"blockedAt"`
	FirstSeenAt int64  `json:"firstSeenAt"` // unix millis, local arrival
	Seq         uint64 `json:"seq"`
}

type pendingPinRef struct {
	Pair        string `json:"pair"` // "<from>:<to>"
	FirstSeenAt int64  `json:"firstSeenAt"`
	Seq         uint64 `json:"seq"`
}

func pendingPairKey(fromGlobalMetaId, toGlobalMetaId string) []byte {
	return []byte(keyPendingPair + fromGlobalMetaId + ":" + toGlobalMetaId)
}

func pendingPinKey(pinId string) []byte {
	return []byte(keyPendingPin + pinId)
}

func (a *Aggregator) nextPendingSeq() uint64 {
	return atomic.AddUint64(&a.pendingSeq, 1)
}

// applyPendingBlock writes one unconfirmed block mutation for the pair (LWW
// by arrival seq) and registers the pin ref consumed at confirmation. A pin
// already tracked is an idempotent re-delivery.
func (a *Aggregator) applyPendingBlock(pinId, fromGlobalMetaId, toGlobalMetaId string, blockState, blockedAt int64, now time.Time) error {
	if a == nil || a.store == nil || pinId == "" || fromGlobalMetaId == "" || toGlobalMetaId == "" {
		return nil
	}
	if blockState != blockStateBlocked && blockState != blockStateUnlocked {
		return nil
	}
	if raw, err := a.store.Get(namespace, pendingPinKey(pinId)); err == nil && raw != nil {
		return nil // idempotent re-delivery
	}

	seq := a.nextPendingSeq()
	firstSeen := now.UnixMilli()
	ref, err := json.Marshal(pendingPinRef{Pair: fromGlobalMetaId + ":" + toGlobalMetaId, FirstSeenAt: firstSeen, Seq: seq})
	if err != nil {
		return err
	}
	if err := a.store.Set(namespace, pendingPinKey(pinId), ref); err != nil {
		return err
	}

	if existing := a.loadPendingBlock(fromGlobalMetaId, toGlobalMetaId); existing != nil && existing.Seq >= seq {
		return nil // a newer unconfirmed mutation already owns the pair
	}
	raw, err := json.Marshal(pendingBlock{BlockState: blockState, BlockPinId: pinId, BlockedAt: blockedAt, FirstSeenAt: firstSeen, Seq: seq})
	if err != nil {
		return err
	}
	return a.store.Set(namespace, pendingPairKey(fromGlobalMetaId, toGlobalMetaId), raw)
}

func (a *Aggregator) loadPendingBlock(fromGlobalMetaId, toGlobalMetaId string) *pendingBlock {
	if a == nil || a.store == nil || fromGlobalMetaId == "" || toGlobalMetaId == "" {
		return nil
	}
	raw, err := a.store.Get(namespace, pendingPairKey(fromGlobalMetaId, toGlobalMetaId))
	if err != nil || raw == nil {
		return nil
	}
	var entry pendingBlock
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil
	}
	return &entry
}

// loadEffectiveBlockEdge is the read-side merge every public surface uses:
// the overlay wins when present (blockState=-1 hides the pair, 1
// materializes it as pending), otherwise the confirmed edge shows through.
func (a *Aggregator) loadEffectiveBlockEdge(fromGlobalMetaId, toGlobalMetaId string) (*BlockEdge, error) {
	if entry := a.loadPendingBlock(fromGlobalMetaId, toGlobalMetaId); entry != nil {
		if entry.BlockState == blockStateUnlocked {
			return nil, nil
		}
		return &BlockEdge{
			FromGlobalMetaId: fromGlobalMetaId,
			ToGlobalMetaId:   toGlobalMetaId,
			BlockPinId:       entry.BlockPinId,
			BlockedAt:        entry.BlockedAt,
			BlockState:       blockStateBlocked,
			Active:           true,
			Pending:          true,
		}, nil
	}
	return a.loadActiveEdge(fromGlobalMetaId, toGlobalMetaId)
}

// consumePendingPin folds the overlay after the pin confirmed. The pin ref
// always goes; the pair entry survives only when a different (newer)
// unconfirmed mutation owns it.
func (a *Aggregator) consumePendingPin(pinId string) {
	if a == nil || a.store == nil || pinId == "" {
		return
	}
	raw, err := a.store.Get(namespace, pendingPinKey(pinId))
	if err != nil || raw == nil {
		return
	}
	_ = a.store.Delete(namespace, pendingPinKey(pinId))
	var ref pendingPinRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		return
	}
	parts := strings.SplitN(ref.Pair, ":", 2)
	if len(parts) != 2 {
		return
	}
	if entry := a.loadPendingBlock(parts[0], parts[1]); entry != nil && entry.BlockPinId == pinId {
		_ = a.store.Delete(namespace, pendingPairKey(parts[0], parts[1]))
	}
}

// pendingBlocksInvolving returns the overlay pairs where the subject is the
// blocker (outbound) or the blocked (inbound).
func (a *Aggregator) pendingBlocksInvolving(subject string, outbound bool) map[string]pendingBlock {
	out := make(map[string]pendingBlock)
	if a == nil || a.store == nil {
		return out
	}
	_ = a.store.ScanPrefix(namespace, []byte(keyPendingPair), func(key, value []byte) error {
		keyText := strings.TrimPrefix(string(key), keyPendingPair)
		idx := strings.LastIndex(keyText, ":")
		if idx <= 0 || idx+1 >= len(keyText) {
			return nil
		}
		var entry pendingBlock
		if err := json.Unmarshal(value, &entry); err != nil {
			return nil
		}
		if outbound && keyText[:idx] == subject {
			out[keyText[idx+1:]] = entry
		} else if !outbound && keyText[idx+1:] == subject {
			out[keyText[:idx]] = entry
		}
		return nil
	})
	return out
}

// SweepExpiredMempoolOverlay is the janitor path (spec §4.4b): consume pin
// refs whose pin already folded into the confirmed model (self-heal), drop
// refs older than expiry whose pin never confirmed (dropped/replaced mempool
// transactions) — the pair entry goes with them, revealing the confirmed
// state beneath. Returns the number of pin entries removed.
func (a *Aggregator) SweepExpiredMempoolOverlay(now time.Time, expiry time.Duration) int {
	if a == nil || a.store == nil {
		return 0
	}
	removed := 0
	var expiredPins []string
	_ = a.store.ScanPrefix(namespace, []byte(keyPendingPin), func(key, value []byte) error {
		pinId := strings.TrimPrefix(string(key), keyPendingPin)
		var ref pendingPinRef
		if err := json.Unmarshal(value, &ref); err != nil {
			expiredPins = append(expiredPins, pinId) // unreadable: drop
			return nil
		}
		parts := strings.SplitN(ref.Pair, ":", 2)
		if len(parts) == 2 {
			// Self-heal: the pin's own confirmation is already folded.
			if _, _, seen := a.loadPairByPin(pinId); seen {
				a.consumePendingPin(pinId)
				removed++
				return nil
			}
		}
		if expiry > 0 && now.UnixMilli()-ref.FirstSeenAt > expiry.Milliseconds() {
			expiredPins = append(expiredPins, pinId)
		}
		return nil
	})
	for _, pinId := range expiredPins {
		a.consumePendingPin(pinId)
		removed++
	}
	if removed > 0 {
		log.Printf("[block] mempool overlay sweep removed %d entries", removed)
	}
	return removed
}

// StartMempoolSweeper runs the overlay janitor on a ticker until stop is
// closed. Wired from main.go; interval and expiry come from config.
func (a *Aggregator) StartMempoolSweeper(interval, expiry time.Duration, stop <-chan struct{}) {
	if a == nil || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				a.SweepExpiredMempoolOverlay(time.Now(), expiry)
			}
		}
	}()
}

// sortPendingBlockPeers orders pending-only block peers newest first, ties
// by peer id (shared §4 ordering rule).
func sortPendingBlockPeers(peers []BlockPeer) {
	sort.Slice(peers, func(i, j int) bool {
		if peers[i].BlockedAt != peers[j].BlockedAt {
			return peers[i].BlockedAt > peers[j].BlockedAt
		}
		return peers[i].GlobalMetaId < peers[j].GlobalMetaId
	})
}
