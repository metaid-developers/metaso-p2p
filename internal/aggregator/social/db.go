package social

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	keyEdgeByPair     = "edge:pair:"
	keyFollowingIndex = "edge:following:"
	keyFollowerIndex  = "edge:followers:"
	keyPinToPair      = "edge:pin:"
	keyMutualIndex    = "edge:mutual:"
)

func pairKey(followerGlobalMetaId, targetGlobalMetaId string) []byte {
	return []byte(keyEdgeByPair + followerGlobalMetaId + ":" + targetGlobalMetaId)
}

func followingIndexKey(followerGlobalMetaId string, followedAt int64, followPinId, targetGlobalMetaId string) []byte {
	return []byte(keyFollowingIndex + followerGlobalMetaId + ":" + invertedTimestampHex(followedAt) + ":" + followPinId + ":" + targetGlobalMetaId)
}

func followerIndexKey(targetGlobalMetaId string, followedAt int64, followPinId, followerGlobalMetaId string) []byte {
	return []byte(keyFollowerIndex + targetGlobalMetaId + ":" + invertedTimestampHex(followedAt) + ":" + followPinId + ":" + followerGlobalMetaId)
}

func pinToPairKey(followPinId string) []byte {
	return []byte(keyPinToPair + followPinId)
}

// mutualRecord is the value of the edge:mutual reverse index entries
// (spec 2026-10-29 §4.1). FriendSince is the moment the mutual state came
// into existence — the later of the two direction edges.
type mutualRecord struct {
	FriendSince  int64  `json:"friendSince"`
	SubjectPinId string `json:"subjectPinId"`
	SubjectAt    int64  `json:"subjectAt"`
	PeerPinId    string `json:"peerPinId"`
	PeerAt       int64  `json:"peerAt"`
}

func mutualIndexKey(subject, peer string) []byte {
	return []byte(keyMutualIndex + subject + ":" + peer)
}

func mutualIndexPrefix(subject string) []byte {
	return []byte(keyMutualIndex + subject + ":")
}

// maintainMutualForPair keeps the edge:mutual reverse index in sync with the
// directional edges of a pair: when both directions are currently active the
// pair is mutual (friend) and both per-subject entries are (re)written;
// otherwise both entries are removed. It runs on every edge save/delete, so
// the second directional edge write materializes the friendship.
func (a *Aggregator) maintainMutualForPair(followerGlobalMetaId, targetGlobalMetaId string) error {
	if a == nil || a.store == nil || followerGlobalMetaId == "" || targetGlobalMetaId == "" {
		return nil
	}
	forward, err := a.loadActiveEdge(followerGlobalMetaId, targetGlobalMetaId)
	if err != nil {
		return err
	}
	reverse, err := a.loadActiveEdge(targetGlobalMetaId, followerGlobalMetaId)
	if err != nil {
		return err
	}
	if forward == nil || reverse == nil {
		_ = a.store.Delete(namespace, mutualIndexKey(followerGlobalMetaId, targetGlobalMetaId))
		_ = a.store.Delete(namespace, mutualIndexKey(targetGlobalMetaId, followerGlobalMetaId))
		return nil
	}

	friendSince := max(forward.FollowedAt, reverse.FollowedAt)
	forwardRecord := mutualRecord{
		FriendSince:  friendSince,
		SubjectPinId: forward.FollowPinId,
		SubjectAt:    forward.FollowedAt,
		PeerPinId:    reverse.FollowPinId,
		PeerAt:       reverse.FollowedAt,
	}
	reverseRecord := mutualRecord{
		FriendSince:  friendSince,
		SubjectPinId: reverse.FollowPinId,
		SubjectAt:    reverse.FollowedAt,
		PeerPinId:    forward.FollowPinId,
		PeerAt:       forward.FollowedAt,
	}
	if raw, err := json.Marshal(forwardRecord); err != nil {
		return err
	} else if err := a.store.Set(namespace, mutualIndexKey(followerGlobalMetaId, targetGlobalMetaId), raw); err != nil {
		return err
	}
	if raw, err := json.Marshal(reverseRecord); err != nil {
		return err
	} else if err := a.store.Set(namespace, mutualIndexKey(targetGlobalMetaId, followerGlobalMetaId), raw); err != nil {
		return err
	}
	return nil
}

func followingIndexPrefix(followerGlobalMetaId string) []byte {
	return []byte(keyFollowingIndex + followerGlobalMetaId + ":")
}

func followerIndexPrefix(targetGlobalMetaId string) []byte {
	return []byte(keyFollowerIndex + targetGlobalMetaId + ":")
}

func invertedTimestampHex(ts int64) string {
	inverted := ^uint64(ts)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, inverted)
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf))
}

func (a *Aggregator) loadActiveEdge(followerGlobalMetaId, targetGlobalMetaId string) (*FollowEdge, error) {
	if a == nil || a.store == nil || followerGlobalMetaId == "" || targetGlobalMetaId == "" {
		return nil, nil
	}
	raw, err := a.store.Get(namespace, pairKey(followerGlobalMetaId, targetGlobalMetaId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var edge FollowEdge
	if err := json.Unmarshal(raw, &edge); err != nil {
		return nil, fmt.Errorf("loadActiveEdge: %w", err)
	}
	if !edge.Active {
		return nil, nil
	}
	return &edge, nil
}

func (a *Aggregator) loadActiveEdgeByPin(followPinId string) (*FollowEdge, error) {
	if a == nil || a.store == nil || followPinId == "" {
		return nil, nil
	}
	raw, err := a.store.Get(namespace, pinToPairKey(followPinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	// §2.5 audit trail: unresolved pins are recorded at the pin level without
	// materializing an edge; they carry no active pair to load.
	var trail unresolvedPinRecord
	if json.Unmarshal(raw, &trail) == nil && trail.Unresolved {
		return nil, nil
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return nil, nil
	}
	return a.loadActiveEdge(parts[0], parts[1])
}

func (a *Aggregator) saveActiveEdge(edge *FollowEdge) error {
	if a == nil || a.store == nil {
		return ErrUnavailable
	}
	if edge == nil || edge.FollowerGlobalMetaId == "" || edge.TargetGlobalMetaId == "" || edge.FollowPinId == "" {
		return errors.New("saveActiveEdge: incomplete edge")
	}
	edge.Active = true

	previous, err := a.loadActiveEdge(edge.FollowerGlobalMetaId, edge.TargetGlobalMetaId)
	if err != nil {
		return err
	}
	if previous != nil {
		if err := a.deleteActiveEdge(previous); err != nil {
			return err
		}
	}

	raw, err := json.Marshal(edge)
	if err != nil {
		return err
	}
	if err := a.store.Set(namespace, pairKey(edge.FollowerGlobalMetaId, edge.TargetGlobalMetaId), raw); err != nil {
		return err
	}
	if err := a.store.Set(namespace, followingIndexKey(edge.FollowerGlobalMetaId, edge.FollowedAt, edge.FollowPinId, edge.TargetGlobalMetaId), []byte{}); err != nil {
		return err
	}
	if err := a.store.Set(namespace, followerIndexKey(edge.TargetGlobalMetaId, edge.FollowedAt, edge.FollowPinId, edge.FollowerGlobalMetaId), []byte{}); err != nil {
		return err
	}
	if err := a.store.Set(namespace, pinToPairKey(edge.FollowPinId), []byte(edge.FollowerGlobalMetaId+":"+edge.TargetGlobalMetaId)); err != nil {
		return err
	}
	return a.maintainMutualForPair(edge.FollowerGlobalMetaId, edge.TargetGlobalMetaId)
}

func (a *Aggregator) deleteActiveEdge(edge *FollowEdge) error {
	if a == nil || a.store == nil || edge == nil {
		return nil
	}
	_ = a.store.Delete(namespace, pairKey(edge.FollowerGlobalMetaId, edge.TargetGlobalMetaId))
	_ = a.store.Delete(namespace, followingIndexKey(edge.FollowerGlobalMetaId, edge.FollowedAt, edge.FollowPinId, edge.TargetGlobalMetaId))
	_ = a.store.Delete(namespace, followerIndexKey(edge.TargetGlobalMetaId, edge.FollowedAt, edge.FollowPinId, edge.FollowerGlobalMetaId))
	_ = a.store.Delete(namespace, pinToPairKey(edge.FollowPinId))
	return a.maintainMutualForPair(edge.FollowerGlobalMetaId, edge.TargetGlobalMetaId)
}
