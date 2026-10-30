package social

import (
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

// Mempool instant-effect overlay (spec 2026-10-29 §4.4b, owner decision
// 2026-10-30 reversing OQ-2): unconfirmed /follow pins take effect in every
// read view immediately; confirmation folds the pin into the confirmed model
// and consumes the overlay entry, so the transition is seamless.
//
// Storage (namespace "social", separate keys from the confirmed model):
//
//	edge:mp:pair:<follower>:<target> -> pendingFollow JSON (the newest
//	                                    unconfirmed mutation for the pair)
//	edge:mp:pin:<pinId>              -> pendingPinRef JSON (every unconfirmed
//	                                    social pin, for confirmation
//	                                    consumption and revoke resolution)
//
// Overlay semantics:
//   - The confirmed model is untouched: it stays the single source of truth
//     for confirmed state, and backfill replay keeps working unchanged.
//   - One overlay entry per pair, last-write-wins by arrival sequence:
//     a newer unconfirmed mutation (e.g. a revoke after a pending follow)
//     replaces the previous overlay entry for the pair.
//   - Effective read = overlay entry if present (kind=revoke hides the pair,
//     kind=follow materializes it with pending=true), else the confirmed edge.
//   - Confirmation of pin P consumes P's pin entry, and the pair entry only
//     when P still owns it (a newer unconfirmed mutation survives).
//   - Expiry (janitor): a pin entry older than the threshold whose pin never
//     confirmed is dropped; dropping the pair entry reveals the confirmed
//     state beneath — no rollback bookkeeping needed.
const (
	keyPendingPair = "edge:mp:pair:"
	keyPendingPin  = "edge:mp:pin:"

	pendingKindFollow = "follow"
	pendingKindRevoke = "revoke"
)

// pendingFollow is the newest unconfirmed mutation of one (follower, target)
// pair. Seq orders writes by local arrival (mempool delivery order); pins
// carry no height before confirmation, so arrival order is the only LWW key
// available — same policy the mempool itself exposes.
type pendingFollow struct {
	Kind        string `json:"kind"` // follow | revoke
	FollowPinId string `json:"followPinId"`
	FollowedAt  int64  `json:"followedAt"`
	FirstSeenAt int64  `json:"firstSeenAt"` // unix millis, local arrival
	Seq         uint64 `json:"seq"`
}

// pendingPinRef tracks every unconfirmed social pin so confirmation can
// consume it and a mempool revoke can resolve its target pair. Pair entries
// are always owned by exactly one live pin ref (the newest per pair).
type pendingPinRef struct {
	Pair        string `json:"pair"` // "<follower>:<target>"
	FirstSeenAt int64  `json:"firstSeenAt"`
	Seq         uint64 `json:"seq"`
}

func pendingPairKey(followerGlobalMetaId, targetGlobalMetaId string) []byte {
	return []byte(keyPendingPair + followerGlobalMetaId + ":" + targetGlobalMetaId)
}

func pendingPinKey(pinId string) []byte {
	return []byte(keyPendingPin + pinId)
}

func pendingPairPrefix() []byte {
	return []byte(keyPendingPair)
}

func pendingPinPrefix() []byte {
	return []byte(keyPendingPin)
}

// nextPendingSeq is the arrival-order counter backing overlay LWW.
func (a *Aggregator) nextPendingSeq() uint64 {
	return atomic.AddUint64(&a.pendingSeq, 1)
}

// applyPendingFollow writes one unconfirmed mutation for the pair (LWW by
// arrival seq) and registers the pin ref used at confirmation time. A pin
// already tracked is an idempotent re-delivery: keep the original firstSeenAt
// so expiry timing stays anchored to first arrival.
func (a *Aggregator) applyPendingFollow(pinId string, followerGlobalMetaId, targetGlobalMetaId, kind string, followedAt int64, now time.Time) error {
	if a == nil || a.store == nil || pinId == "" || followerGlobalMetaId == "" || targetGlobalMetaId == "" {
		return nil
	}
	if kind != pendingKindFollow && kind != pendingKindRevoke {
		return nil
	}
	if raw, err := a.store.Get(namespace, pendingPinKey(pinId)); err == nil && raw != nil {
		return nil // idempotent re-delivery
	}

	seq := a.nextPendingSeq()
	firstSeen := now.UnixMilli()
	pair := followerGlobalMetaId + ":" + targetGlobalMetaId
	ref, err := json.Marshal(pendingPinRef{Pair: pair, FirstSeenAt: firstSeen, Seq: seq})
	if err != nil {
		return err
	}
	if err := a.store.Set(namespace, pendingPinKey(pinId), ref); err != nil {
		return err
	}

	entry := pendingFollow{Kind: kind, FollowPinId: pinId, FollowedAt: followedAt, FirstSeenAt: firstSeen, Seq: seq}
	if existing := a.loadPendingFollow(followerGlobalMetaId, targetGlobalMetaId); existing != nil && existing.Seq >= seq {
		return nil // a newer unconfirmed mutation already owns the pair
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, pendingPairKey(followerGlobalMetaId, targetGlobalMetaId), raw)
}

func (a *Aggregator) loadPendingFollow(followerGlobalMetaId, targetGlobalMetaId string) *pendingFollow {
	if a == nil || a.store == nil || followerGlobalMetaId == "" || targetGlobalMetaId == "" {
		return nil
	}
	raw, err := a.store.Get(namespace, pendingPairKey(followerGlobalMetaId, targetGlobalMetaId))
	if err != nil || raw == nil {
		return nil
	}
	var entry pendingFollow
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil
	}
	return &entry
}

// loadEffectiveEdge is the read-side merge every public surface uses:
// the overlay wins when present (revoke hides the pair, follow materializes
// it as pending), otherwise the confirmed edge shows through.
func (a *Aggregator) loadEffectiveEdge(followerGlobalMetaId, targetGlobalMetaId string) (*FollowEdge, error) {
	if entry := a.loadPendingFollow(followerGlobalMetaId, targetGlobalMetaId); entry != nil {
		if entry.Kind == pendingKindRevoke {
			return nil, nil
		}
		return &FollowEdge{
			FollowerGlobalMetaId: followerGlobalMetaId,
			TargetGlobalMetaId:   targetGlobalMetaId,
			FollowPinId:          entry.FollowPinId,
			FollowedAt:           entry.FollowedAt,
			Active:               true,
			Pending:              true,
		}, nil
	}
	return a.loadActiveEdge(followerGlobalMetaId, targetGlobalMetaId)
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
	var ref pendingPinRef
	if err := json.Unmarshal(raw, &ref); err != nil {
		_ = a.store.Delete(namespace, pendingPinKey(pinId))
		return
	}
	_ = a.store.Delete(namespace, pendingPinKey(pinId))
	parts := strings.SplitN(ref.Pair, ":", 2)
	if len(parts) != 2 {
		return
	}
	if entry := a.loadPendingFollow(parts[0], parts[1]); entry != nil && entry.FollowPinId == pinId {
		_ = a.store.Delete(namespace, pendingPairKey(parts[0], parts[1]))
	}
}

// resolvePendingPairForPin finds the (follower, target) pair a pin id refers
// to, checking the unconfirmed overlay first, then the confirmed pin index.
func (a *Aggregator) resolvePendingPairForPin(pinId string) (string, string, bool) {
	if a == nil || a.store == nil || pinId == "" {
		return "", "", false
	}
	if raw, err := a.store.Get(namespace, pendingPinKey(pinId)); err == nil && raw != nil {
		var ref pendingPinRef
		if json.Unmarshal(raw, &ref) == nil {
			if parts := strings.SplitN(ref.Pair, ":", 2); len(parts) == 2 && parts[0] != "" && parts[1] != "" {
				return parts[0], parts[1], true
			}
		}
	}
	raw, err := a.store.Get(namespace, pinToPairKey(pinId))
	if err != nil || raw == nil {
		return "", "", false
	}
	var trail unresolvedPinRecord
	if json.Unmarshal(raw, &trail) == nil && trail.Unresolved {
		return "", "", false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// forEachPendingPair iterates every overlay pair entry. The pending set is
// bounded by the mempool window (entries expire via the janitor), so full
// prefix scans are cheap.
func (a *Aggregator) forEachPendingPair(fn func(followerGlobalMetaId, targetGlobalMetaId string, entry *pendingFollow) error) error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.ScanPrefix(namespace, pendingPairPrefix(), func(key, value []byte) error {
		keyText := strings.TrimPrefix(string(key), keyPendingPair)
		idx := strings.LastIndex(keyText, ":")
		if idx <= 0 || idx+1 >= len(keyText) {
			return nil
		}
		var entry pendingFollow
		if err := json.Unmarshal(value, &entry); err != nil {
			return nil
		}
		return fn(keyText[:idx], keyText[idx+1:], &entry)
	})
}

// pendingFollowsFor returns the peers whose effective edge for the subject
// comes from the overlay (kind=follow, correct direction). Peers whose
// confirmed edge already exists are included: the index scan surfaces them
// and the effective loader picks the overlay payload, but the caller needs
// this set to know which list items carry the pending marker.
func (a *Aggregator) pendingFollowPeers(subject string, outbound bool) map[string]pendingFollow {
	out := make(map[string]pendingFollow)
	if a == nil || a.store == nil {
		return out
	}
	_ = a.forEachPendingPair(func(follower, target string, entry *pendingFollow) error {
		if entry.Kind != pendingKindFollow {
			return nil
		}
		if outbound && follower == subject {
			out[target] = *entry
		} else if !outbound && target == subject {
			out[follower] = *entry
		}
		return nil
	})
	return out
}

// SweepExpiredMempoolOverlay is the janitor path (spec §4.4b): it walks the
// unconfirmed pin refs and
//   - consumes entries whose pin already folded into the confirmed model
//     (self-heal for a consumption missed across a restart/replay boundary),
//   - drops entries older than expiry whose pin never confirmed (dropped or
//     replaced mempool transactions); the pair entry goes with them, which
//     reveals the confirmed state beneath.
//
// Returns the number of pin entries removed.
func (a *Aggregator) SweepExpiredMempoolOverlay(now time.Time, expiry time.Duration) int {
	if a == nil || a.store == nil {
		return 0
	}
	removed := 0
	var expiredPins []string
	_ = a.store.ScanPrefix(namespace, pendingPinPrefix(), func(key, value []byte) error {
		pinId := strings.TrimPrefix(string(key), keyPendingPin)
		var ref pendingPinRef
		if err := json.Unmarshal(value, &ref); err != nil {
			expiredPins = append(expiredPins, pinId) // unreadable: drop
			return nil
		}
		parts := strings.SplitN(ref.Pair, ":", 2)
		if len(parts) == 2 {
			// Self-heal: the pin's own confirmation is already folded.
			if confirmed, _ := a.loadActiveEdgeByPin(pinId); confirmed != nil {
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
		log.Printf("[social] mempool overlay sweep removed %d entries", removed)
	}
	return removed
}

// pendingPeerEdge is one overlay follow edge keyed by its peer id.
type pendingPeerEdge struct {
	peer  string
	pinId string
	at    int64 // followedAt, for ordering
}

// pendingOnlyEdges returns the overlay follow peers of the subject that the
// confirmed index will never surface (their confirmed pair edge is nil),
// newest followedAt first, ties by peer id — the shared §4 ordering rule.
// Peers excluded by the unified filter are dropped here.
func (a *Aggregator) pendingOnlyEdges(subject string, outbound bool, excluded map[string]struct{}) []pendingPeerEdge {
	pending := a.pendingFollowPeers(subject, outbound)
	if len(pending) == 0 {
		return nil
	}
	edges := make([]pendingPeerEdge, 0, len(pending))
	for peer, entry := range pending {
		if _, blocked := excluded[peer]; blocked {
			continue
		}
		var confirmed *FollowEdge
		if outbound {
			confirmed, _ = a.loadActiveEdge(subject, peer)
		} else {
			confirmed, _ = a.loadActiveEdge(peer, subject)
		}
		if confirmed != nil {
			continue // surfaced by the confirmed index scan
		}
		edges = append(edges, pendingPeerEdge{peer: peer, pinId: entry.FollowPinId, at: entry.FollowedAt})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].at != edges[j].at {
			return edges[i].at > edges[j].at
		}
		return edges[i].peer < edges[j].peer
	})
	return edges
}

// StartMempoolSweeper runs the overlay janitor on a ticker until stop is
// closed. Wired from main.go; the interval and expiry come from config.
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
