package social

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

const (
	pathFollow      = "/follow"
	operationCreate = "create"
	operationRevoke = "revoke"
)

func (a *Aggregator) processPin(pin *aggregator.PinInscription) error {
	if pin == nil {
		return nil
	}
	switch normalisedPinPath(pin.Path) {
	case pathFollow:
		switch strings.ToLower(strings.TrimSpace(pin.Operation)) {
		case operationCreate:
			return a.processCreate(pin)
		case operationRevoke:
			return a.processRevoke(pin)
		}
	}
	return nil
}

func (a *Aggregator) processCreate(pin *aggregator.PinInscription) error {
	followerGlobalMetaId, err := a.resolveFollowerGlobalMetaId(pin)
	if err != nil {
		return err
	}
	targetRef := decodeTargetRef(pin)
	targetGlobalMetaId, ok, err := a.normalizeIdentity(targetRef)
	if err != nil {
		return err
	}
	if followerGlobalMetaId == "" || !ok {
		// §2.5: an unresolved reference creates no edge, but the pin-level
		// index records it so backfill replay can re-normalize later.
		return a.recordUnresolvedPin(pin, followerGlobalMetaId, targetRef)
	}
	return a.saveActiveEdge(&FollowEdge{
		FollowerGlobalMetaId: followerGlobalMetaId,
		TargetGlobalMetaId:   targetGlobalMetaId,
		FollowPinId:          strings.TrimSpace(pin.Id),
		FollowedAt:           pin.Timestamp,
	})
}

// unresolvedPinRecord is the edge:pin trail entry written when a follow pin's
// target (or follower) reference did not resolve through normalization
// (spec 2026-10-29 §2.5). It keeps the pin auditable and re-normalizable by
// replay without materializing an edge.
type unresolvedPinRecord struct {
	Unresolved   bool   `json:"unresolved"`
	Follower     string `json:"follower,omitempty"`
	RawFollower  string `json:"rawFollower,omitempty"`
	RawTargetRef string `json:"rawTargetRef,omitempty"`
	Timestamp    int64  `json:"timestamp"`
}

func (a *Aggregator) recordUnresolvedPin(pin *aggregator.PinInscription, followerGlobalMetaId, targetRef string) error {
	if a == nil || a.store == nil || pin == nil {
		return nil
	}
	pinId := strings.TrimSpace(pin.Id)
	if pinId == "" {
		return nil
	}
	raw, err := json.Marshal(unresolvedPinRecord{
		Unresolved:   true,
		Follower:     followerGlobalMetaId,
		RawFollower:  firstNonEmpty(pin.MetaId, pin.CreateMetaId, pin.Address, pin.CreateAddress),
		RawTargetRef: targetRef,
		Timestamp:    pin.Timestamp,
	})
	if err != nil {
		return err
	}
	return a.store.Set(namespace, pinToPairKey(pinId), raw)
}

func (a *Aggregator) processRevoke(pin *aggregator.PinInscription) error {
	edge, err := a.loadActiveEdgeByPin(resolveTargetPinId(pin))
	if err != nil || edge == nil {
		return err
	}
	return a.deleteActiveEdge(edge)
}

func (a *Aggregator) Relationship(params RelationshipParams) (*RelationshipResult, error) {
	source, err := a.lookupRequestSubject(params.SourceGlobalMetaId)
	if err != nil {
		return nil, err
	}
	target, err := a.lookupRequestSubject(params.TargetGlobalMetaId)
	if err != nil {
		return nil, err
	}

	sourceEdge, err := a.loadActiveEdge(source.GlobalMetaId, target.GlobalMetaId)
	if err != nil {
		return nil, err
	}
	targetEdge, err := a.loadActiveEdge(target.GlobalMetaId, source.GlobalMetaId)
	if err != nil {
		return nil, err
	}

	out := &RelationshipResult{
		Source: RelationshipSide{GlobalMetaId: source.GlobalMetaId},
		Target: RelationshipSide{GlobalMetaId: target.GlobalMetaId},
	}
	if sourceEdge != nil {
		out.Source.FollowsTarget = true
		out.Source.FollowPinId = sourceEdge.FollowPinId
		out.Source.FollowedAt = sourceEdge.FollowedAt
	}
	if targetEdge != nil {
		out.Target.FollowsSource = true
		out.Target.FollowPinId = targetEdge.FollowPinId
		out.Target.FollowedAt = targetEdge.FollowedAt
	}
	out.Mutual = out.Source.FollowsTarget && out.Target.FollowsSource
	return out, nil
}

func (a *Aggregator) ListFollowing(params ListParams) (*ListResult, error) {
	view, err := normaliseListView(params.View)
	if err != nil {
		return nil, err
	}
	params.View = view

	subject, err := a.lookupRequestSubject(params.GlobalMetaId)
	if err != nil {
		return nil, err
	}
	excluded, err := a.blockedPeerSet(subject.GlobalMetaId)
	if err != nil {
		return nil, err
	}
	return a.listEdges(followingIndexPrefix(subject.GlobalMetaId), params, excluded, func(key []byte) (string, bool) {
		keyText := string(key)
		idx := strings.LastIndex(keyText, ":")
		if idx < 0 || idx+1 >= len(keyText) {
			return "", false
		}
		return keyText[idx+1:], true
	}, func(peerGlobalMetaId string) (*FollowEdge, error) {
		return a.loadActiveEdge(subject.GlobalMetaId, peerGlobalMetaId)
	})
}

func (a *Aggregator) ListFollowers(params ListParams) (*ListResult, error) {
	view, err := normaliseListView(params.View)
	if err != nil {
		return nil, err
	}
	params.View = view

	subject, err := a.lookupRequestSubject(params.GlobalMetaId)
	if err != nil {
		return nil, err
	}
	excluded, err := a.blockedPeerSet(subject.GlobalMetaId)
	if err != nil {
		return nil, err
	}
	return a.listEdges(followerIndexPrefix(subject.GlobalMetaId), params, excluded, func(key []byte) (string, bool) {
		keyText := string(key)
		idx := strings.LastIndex(keyText, ":")
		if idx < 0 || idx+1 >= len(keyText) {
			return "", false
		}
		return keyText[idx+1:], true
	}, func(peerGlobalMetaId string) (*FollowEdge, error) {
		return a.loadActiveEdge(peerGlobalMetaId, subject.GlobalMetaId)
	})
}

func (a *Aggregator) listEdges(prefix []byte, params ListParams, excludedPeers map[string]struct{}, parsePeer func([]byte) (string, bool), loadEdge func(string) (*FollowEdge, error)) (*ListResult, error) {
	view, err := normaliseListView(params.View)
	if err != nil {
		return nil, err
	}

	size := clampListSize(params.Size)
	cursorKey, err := decodeCursor(params.Cursor)
	if err != nil {
		return nil, err
	}
	if len(cursorKey) > 0 && !bytes.HasPrefix(cursorKey, prefix) {
		return nil, ErrInvalidParameter
	}

	items := make([]ListItem, 0, size)
	nextCursor := ""
	lastReturnedKey := []byte(nil)
	skipUntilCursor := len(cursorKey) > 0
	foundCursor := len(cursorKey) == 0

	err = a.store.ScanPrefix(namespace, prefix, func(key, _ []byte) error {
		if skipUntilCursor {
			if bytes.Equal(key, cursorKey) {
				skipUntilCursor = false
				foundCursor = true
			}
			return nil
		}
		peerGlobalMetaId, ok := parsePeer(key)
		if !ok || peerGlobalMetaId == "" {
			return nil
		}
		// §4.5 unified filter: blocked pairs never appear in list reads.
		if _, excluded := excludedPeers[peerGlobalMetaId]; excluded {
			return nil
		}

		edge, err := loadEdge(peerGlobalMetaId)
		if err != nil || edge == nil {
			return err
		}

		profile, err := a.lookupListProfile(peerGlobalMetaId)
		if err != nil {
			return err
		}

		items = append(items, buildListItem(view, profile, edge))
		if len(items) <= size {
			lastReturnedKey = append(lastReturnedKey[:0], key...)
		}
		if len(items) == size+1 {
			nextCursor = encodeCursor(lastReturnedKey)
			return errStopScan
		}
		return nil
	})
	if err == errStopScan {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if !foundCursor {
		return nil, ErrInvalidParameter
	}
	if len(items) > size {
		items = items[:size]
	}
	return &ListResult{List: items, NextCursor: nextCursor, Size: size}, nil
}

func normaliseListView(view string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "", ViewCompact:
		return ViewCompact, nil
	case ViewProfile:
		return ViewProfile, nil
	default:
		return "", ErrInvalidParameter
	}
}

func (a *Aggregator) lookupListProfile(globalMetaId string) (*TargetRef, error) {
	profile, err := a.lookupRequestSubject(globalMetaId)
	if err == nil {
		return profile, nil
	}
	if errors.Is(err, ErrNotFound) {
		return &TargetRef{GlobalMetaId: globalMetaId}, nil
	}
	return nil, err
}

func buildListItem(view string, profile *TargetRef, edge *FollowEdge) ListItem {
	item := ListItem{
		GlobalMetaId: profile.GlobalMetaId,
		Name:         profile.Name,
		NameId:       profile.NameId,
		AvatarId:     profile.AvatarId,
	}
	if view == ViewProfile {
		item.Bio = profile.Bio
		item.BioId = profile.BioId
		item.FollowedAt = edge.FollowedAt
		item.FollowPinId = edge.FollowPinId
	}
	return item
}

func (a *Aggregator) lookupRequestSubject(globalMetaId string) (*TargetRef, error) {
	globalMetaId = strings.TrimSpace(globalMetaId)
	if globalMetaId == "" {
		return nil, ErrInvalidParameter
	}
	if a == nil || a.profileLookup == nil {
		return nil, ErrUnavailable
	}
	target, err := a.profileLookup.LookupByGlobalMetaId(globalMetaId)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if target == nil || strings.TrimSpace(target.GlobalMetaId) == "" {
		return nil, ErrNotFound
	}
	return target, nil
}

func (a *Aggregator) resolveFollowerGlobalMetaId(pin *aggregator.PinInscription) (string, error) {
	if pin == nil {
		return "", nil
	}
	if globalMetaId := strings.TrimSpace(pin.GlobalMetaId); globalMetaId != "" {
		return globalMetaId, nil
	}
	ref := firstNonEmpty(pin.MetaId, pin.CreateMetaId, pin.Address, pin.CreateAddress)
	canonical, ok, err := a.normalizeIdentity(ref)
	if err != nil || !ok {
		return "", err
	}
	return canonical, nil
}

func resolveTargetPinId(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	if candidate := strings.Trim(strings.TrimSpace(pin.OriginalId), "@/"); candidate != "" && candidate != pin.Id {
		return candidate
	}
	if idx := strings.Index(pin.Path, "@"); idx >= 0 {
		candidate := strings.Trim(strings.TrimSpace(pin.Path[idx+1:]), "/")
		if candidate != "" && candidate != pin.Id {
			return candidate
		}
	}
	return ""
}

func normalisedPinPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	if idx := strings.Index(path, "@"); idx >= 0 {
		path = path[:idx]
	}
	return strings.TrimRight(path, "/")
}

func decodeTargetRef(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	ref := strings.TrimSpace(string(pin.ContentBody))
	ref = strings.Trim(ref, "\"")
	if ref != "" {
		return ref
	}
	return strings.Trim(strings.TrimSpace(pin.ContentSummary), "\"")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

type stopScanError struct{}

func (stopScanError) Error() string { return "stop scan" }

var errStopScan error = stopScanError{}
