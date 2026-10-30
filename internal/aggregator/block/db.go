package block

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Pebble key layout (mirrors internal/aggregator/social/db.go):
//
//	blk:pair:<from>:<to>                  -> BlockEdge JSON (current folded state)
//	blk:out:<from>:<invTs>:<pinId>:<to>   -> empty (outbound listing index, newest first)
//	blk:in:<to>:<invTs>:<pinId>:<from>    -> empty (inbound listing index, newest first)
//	blk:pin:<pinId>                       -> "<from>:<to>" (pinId reverse lookup + replay dedupe)
const (
	keyEdgeByPair = "blk:pair:"
	keyOutIndex   = "blk:out:"
	keyInIndex    = "blk:in:"
	keyPinToPair  = "blk:pin:"
)

func pairKey(fromGlobalMetaId, toGlobalMetaId string) []byte {
	return []byte(keyEdgeByPair + fromGlobalMetaId + ":" + toGlobalMetaId)
}

func outIndexKey(fromGlobalMetaId string, blockedAt int64, blockPinId, toGlobalMetaId string) []byte {
	return []byte(keyOutIndex + fromGlobalMetaId + ":" + invertedTimestampHex(blockedAt) + ":" + blockPinId + ":" + toGlobalMetaId)
}

func inIndexKey(toGlobalMetaId string, blockedAt int64, blockPinId, fromGlobalMetaId string) []byte {
	return []byte(keyInIndex + toGlobalMetaId + ":" + invertedTimestampHex(blockedAt) + ":" + blockPinId + ":" + fromGlobalMetaId)
}

func pinToPairKey(blockPinId string) []byte {
	return []byte(keyPinToPair + blockPinId)
}

func outIndexPrefix(fromGlobalMetaId string) []byte {
	return []byte(keyOutIndex + fromGlobalMetaId + ":")
}

func inIndexPrefix(toGlobalMetaId string) []byte {
	return []byte(keyInIndex + toGlobalMetaId + ":")
}

func invertedTimestampHex(ts int64) string {
	inverted := ^uint64(ts)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, inverted)
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf))
}

func (a *Aggregator) loadActiveEdge(fromGlobalMetaId, toGlobalMetaId string) (*BlockEdge, error) {
	if a == nil || a.store == nil || fromGlobalMetaId == "" || toGlobalMetaId == "" {
		return nil, nil
	}
	raw, err := a.store.Get(namespace, pairKey(fromGlobalMetaId, toGlobalMetaId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var edge BlockEdge
	if err := json.Unmarshal(raw, &edge); err != nil {
		return nil, fmt.Errorf("loadActiveEdge: %w", err)
	}
	if !edge.Active {
		return nil, nil
	}
	return &edge, nil
}

// loadPairByPin resolves the (from, to) pair recorded for a pin, if any.
func (a *Aggregator) loadPairByPin(blockPinId string) (string, string, bool) {
	if a == nil || a.store == nil || blockPinId == "" {
		return "", "", false
	}
	raw, err := a.store.Get(namespace, pinToPairKey(blockPinId))
	if err != nil || raw == nil {
		return "", "", false
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// saveActiveBlock folds a blockState = 1 pin into the read model: the pair
// key holds the newest state and both directional indexes point at the
// active pin. Any previous index entries for the pair are removed first, so
// the listing always shows exactly the current active pin (last write wins).
func (a *Aggregator) saveActiveBlock(edge *BlockEdge) error {
	if a == nil || a.store == nil {
		return ErrUnavailable
	}
	if edge == nil || edge.FromGlobalMetaId == "" || edge.ToGlobalMetaId == "" || edge.BlockPinId == "" {
		return errors.New("saveActiveBlock: incomplete edge")
	}
	edge.Active = true

	previous, err := a.loadActiveEdge(edge.FromGlobalMetaId, edge.ToGlobalMetaId)
	if err != nil {
		return err
	}
	if previous != nil {
		a.deletePairIndexes(previous)
	}

	raw, err := json.Marshal(edge)
	if err != nil {
		return err
	}
	if err := a.store.Set(namespace, pairKey(edge.FromGlobalMetaId, edge.ToGlobalMetaId), raw); err != nil {
		return err
	}
	if err := a.store.Set(namespace, outIndexKey(edge.FromGlobalMetaId, edge.BlockedAt, edge.BlockPinId, edge.ToGlobalMetaId), []byte{}); err != nil {
		return err
	}
	return a.store.Set(namespace, inIndexKey(edge.ToGlobalMetaId, edge.BlockedAt, edge.BlockPinId, edge.FromGlobalMetaId), []byte{})
}

// removeActiveBlock folds a blockState = -1 pin: the pair leaves the active
// read model entirely; counters and listings drop it (spec §4.4).
func (a *Aggregator) removeActiveBlock(fromGlobalMetaId, toGlobalMetaId, blockPinId string) error {
	if a == nil || a.store == nil {
		return ErrUnavailable
	}
	previous, err := a.loadActiveEdge(fromGlobalMetaId, toGlobalMetaId)
	if err != nil {
		return err
	}
	if previous == nil {
		// Unblock without an active block: still record the pin so replays
		// stay deduplicated, nothing else to fold.
		return a.recordPinPair(blockPinId, fromGlobalMetaId, toGlobalMetaId)
	}
	a.deletePairIndexes(previous)
	_ = a.store.Delete(namespace, pairKey(fromGlobalMetaId, toGlobalMetaId))
	return a.recordPinPair(blockPinId, fromGlobalMetaId, toGlobalMetaId)
}

func (a *Aggregator) deletePairIndexes(edge *BlockEdge) {
	if a == nil || a.store == nil || edge == nil {
		return
	}
	_ = a.store.Delete(namespace, outIndexKey(edge.FromGlobalMetaId, edge.BlockedAt, edge.BlockPinId, edge.ToGlobalMetaId))
	_ = a.store.Delete(namespace, inIndexKey(edge.ToGlobalMetaId, edge.BlockedAt, edge.BlockPinId, edge.FromGlobalMetaId))
}

// recordPinPair persists the pin-level index used both for pinId reverse
// lookup and replay dedupe (a repeated pin is a no-op, which keeps the
// last-write-wins fold correct when live ingest and backfill replay mix).
func (a *Aggregator) recordPinPair(blockPinId, fromGlobalMetaId, toGlobalMetaId string) error {
	if blockPinId == "" {
		return nil
	}
	return a.store.Set(namespace, pinToPairKey(blockPinId), []byte(fromGlobalMetaId+":"+toGlobalMetaId))
}

// activeEdgesFrom lists peers the subject currently blocks, newest first
// (index: blk:out:<from>:<invTs>:<pinId>:<to>).
func (a *Aggregator) activeEdgesFrom(subjectGlobalMetaId string) ([]BlockPeer, error) {
	return a.collectEdges(outIndexPrefix(subjectGlobalMetaId), subjectGlobalMetaId, true)
}

// activeEdgesTo lists peers currently blocking the subject, newest first
// (index: blk:in:<to>:<invTs>:<pinId>:<from>).
func (a *Aggregator) activeEdgesTo(subjectGlobalMetaId string) ([]BlockPeer, error) {
	return a.collectEdges(inIndexPrefix(subjectGlobalMetaId), subjectGlobalMetaId, false)
}

// collectEdges lists the subject's effective block peers in one direction
// (spec §4.4b): the confirmed index scan serves effective edges (a pending
// unblock hides its pair, a pending re-block supplies the overlay payload),
// then pending-only pairs (overlay block with no confirmed edge) are
// appended. ListBlocks re-sorts the merged set, so append order is fine.
func (a *Aggregator) collectEdges(prefix []byte, subjectGlobalMetaId string, outbound bool) ([]BlockPeer, error) {
	peers := make([]BlockPeer, 0, 8)
	err := a.store.ScanPrefix(namespace, prefix, func(key, _ []byte) error {
		keyText := string(key)
		idx := strings.LastIndex(keyText, ":")
		if idx < 0 || idx+1 >= len(keyText) {
			return nil
		}
		peerGlobalMetaId := keyText[idx+1:]
		if peerGlobalMetaId == "" {
			return nil
		}

		var edge *BlockEdge
		var err error
		if outbound {
			edge, err = a.loadEffectiveBlockEdge(subjectGlobalMetaId, peerGlobalMetaId)
		} else {
			edge, err = a.loadEffectiveBlockEdge(peerGlobalMetaId, subjectGlobalMetaId)
		}
		if err != nil {
			return err
		}
		if edge == nil {
			// Stale index entry, or hidden by a pending unblock: skip.
			return nil
		}
		peers = append(peers, BlockPeer{
			GlobalMetaId: peerGlobalMetaId,
			BlockPinId:   edge.BlockPinId,
			BlockedAt:    edge.BlockedAt,
			Pending:      edge.Pending,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Pending-only blocks (no confirmed index entry) join the listing.
	seen := make(map[string]struct{}, len(peers))
	for _, peer := range peers {
		seen[peer.GlobalMetaId] = struct{}{}
	}
	var pendingOnly []BlockPeer
	for peer, entry := range a.pendingBlocksInvolving(subjectGlobalMetaId, outbound) {
		if entry.BlockState != blockStateBlocked {
			continue
		}
		if _, dup := seen[peer]; dup {
			continue
		}
		var confirmed *BlockEdge
		if outbound {
			confirmed, _ = a.loadActiveEdge(subjectGlobalMetaId, peer)
		} else {
			confirmed, _ = a.loadActiveEdge(peer, subjectGlobalMetaId)
		}
		if confirmed != nil {
			continue // surfaced by the confirmed index scan above
		}
		pendingOnly = append(pendingOnly, BlockPeer{
			GlobalMetaId: peer,
			BlockPinId:   entry.BlockPinId,
			BlockedAt:    entry.BlockedAt,
			Pending:      true,
		})
	}
	sortPendingBlockPeers(pendingOnly)
	return append(peers, pendingOnly...), nil
}

func encodeCursor(key []byte) string {
	if len(key) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(key)
}

func decodeCursor(cursor string) ([]byte, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("invalid cursor: %w", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("invalid cursor: empty")
	}
	return key, nil
}
