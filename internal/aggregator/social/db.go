package social

import (
	"encoding/base64"
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
	return a.store.Set(namespace, pinToPairKey(edge.FollowPinId), []byte(edge.FollowerGlobalMetaId+":"+edge.TargetGlobalMetaId))
}

func (a *Aggregator) deleteActiveEdge(edge *FollowEdge) error {
	if a == nil || a.store == nil || edge == nil {
		return nil
	}
	_ = a.store.Delete(namespace, pairKey(edge.FollowerGlobalMetaId, edge.TargetGlobalMetaId))
	_ = a.store.Delete(namespace, followingIndexKey(edge.FollowerGlobalMetaId, edge.FollowedAt, edge.FollowPinId, edge.TargetGlobalMetaId))
	_ = a.store.Delete(namespace, followerIndexKey(edge.TargetGlobalMetaId, edge.FollowedAt, edge.FollowPinId, edge.FollowerGlobalMetaId))
	_ = a.store.Delete(namespace, pinToPairKey(edge.FollowPinId))
	return nil
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
