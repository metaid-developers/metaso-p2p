package social

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
			if err := a.processCreate(pin); err != nil {
				return err
			}
		case operationRevoke:
			if err := a.processRevoke(pin); err != nil {
				return err
			}
		default:
			return nil
		}
		// The confirmed fold consumes this pin's mempool overlay entry so
		// the transition is seamless (spec §4.4b).
		a.consumePendingPin(strings.TrimSpace(pin.Id))
	}
	return nil
}

// processMempoolPin folds unconfirmed /follow pins into the overlay: creates
// materialize the edge immediately (pending), revokes hide it, both LWW per
// pair by arrival (spec §4.4b).
func (a *Aggregator) processMempoolPin(pin *aggregator.PinInscription) error {
	if pin == nil {
		return nil
	}
	switch normalisedPinPath(pin.Path) {
	case pathFollow:
		switch strings.ToLower(strings.TrimSpace(pin.Operation)) {
		case operationCreate:
			return a.processMempoolCreate(pin)
		case operationRevoke:
			return a.processMempoolRevoke(pin)
		}
	}
	return nil
}

func (a *Aggregator) processMempoolCreate(pin *aggregator.PinInscription) error {
	pinId := strings.TrimSpace(pin.Id)
	if pinId != "" {
		// Already folded into the confirmed model (e.g. replay ran first):
		// never resurrect the overlay for a confirmed pin.
		if confirmed, _ := a.loadActiveEdgeByPin(pinId); confirmed != nil {
			return nil
		}
	}
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
		// Unresolved references fold nothing pre-confirmation; the
		// confirmed path records the §2.5 audit trail when the pin lands.
		return nil
	}
	return a.applyPendingFollow(pinId, followerGlobalMetaId, targetGlobalMetaId, pendingKindFollow, pin.Timestamp, time.Now())
}

func (a *Aggregator) processMempoolRevoke(pin *aggregator.PinInscription) error {
	follower, target, ok := a.resolvePendingPairForPin(resolveTargetPinId(pin))
	if !ok {
		// The revoked follow pin is unknown to both layers yet; the
		// confirmed folds apply it in chain order when the pins land.
		return nil
	}
	return a.applyPendingFollow(strings.TrimSpace(pin.Id), follower, target, pendingKindRevoke, pin.Timestamp, time.Now())
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
	// §4.4c ingest heal: drop pre-fix twin rows of this pair (the same
	// identity keyed by chain address / legacy hex) before the canonical
	// write, so the idempotent backfill replay rewrites legacy data and
	// pinToPair always ends up pointing at the canonical form.
	a.sweepPairTwins(pinFollowerForms(pin), followerGlobalMetaId, targetGlobalMetaId)
	return a.saveActiveEdge(&FollowEdge{
		FollowerGlobalMetaId: followerGlobalMetaId,
		TargetGlobalMetaId:   targetGlobalMetaId,
		FollowPinId:          strings.TrimSpace(pin.Id),
		FollowedAt:           pin.Timestamp,
	})
}

// pinFollowerForms lists the raw identity strings a pin carries for its
// publisher — the exact key forms historical ingest paths may have used
// for the same identity (§4.4c sweep input).
func pinFollowerForms(pin *aggregator.PinInscription) []string {
	if pin == nil {
		return nil
	}
	return []string{pin.GlobalMetaId, pin.MetaId, pin.CreateMetaId, pin.Address, pin.CreateAddress}
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
	if err := a.deleteActiveEdge(edge); err != nil {
		return err
	}
	// §4.4c ingest heal: a pre-fix twin row of the same pair under another
	// key form would otherwise resurface as a ghost follower once this
	// direction is revoked.
	a.sweepPairTwins([]string{edge.FollowerGlobalMetaId}, a.canonicalPeerId(edge.FollowerGlobalMetaId), edge.TargetGlobalMetaId)
	return nil
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

	sourceEdge, err := a.effectiveEdgeAcrossForms(source.GlobalMetaId, target.GlobalMetaId)
	if err != nil {
		return nil, err
	}
	targetEdge, err := a.effectiveEdgeAcrossForms(target.GlobalMetaId, source.GlobalMetaId)
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
		out.Source.Pending = sourceEdge.Pending
	}
	if targetEdge != nil {
		out.Target.FollowsSource = true
		out.Target.FollowPinId = targetEdge.FollowPinId
		out.Target.FollowedAt = targetEdge.FollowedAt
		out.Target.Pending = targetEdge.Pending
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
	cursor, err := decodeEdgeListCursor(edgeCursorKindFollowing, params.Cursor)
	if err != nil {
		return nil, ErrInvalidParameter
	}
	excluded, err := a.blockedPeerSet(subject.GlobalMetaId)
	if err != nil {
		return nil, err
	}
	views, err := a.mergedPeerEdges(subject.GlobalMetaId, true, excluded)
	if err != nil {
		return nil, err
	}
	return a.buildEdgeListResult(views, cursor, params, edgeCursorKindFollowing)
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
	cursor, err := decodeEdgeListCursor(edgeCursorKindFollowers, params.Cursor)
	if err != nil {
		return nil, ErrInvalidParameter
	}
	excluded, err := a.blockedPeerSet(subject.GlobalMetaId)
	if err != nil {
		return nil, err
	}
	views, err := a.mergedPeerEdges(subject.GlobalMetaId, false, excluded)
	if err != nil {
		return nil, err
	}
	return a.buildEdgeListResult(views, cursor, params, edgeCursorKindFollowers)
}

const (
	edgeCursorKindFollowing = "following"
	edgeCursorKindFollowers = "followers"
)

// buildEdgeListResult pages the merged view with the tuple cursor
// (§4.4c): rows sort pending bucket first, then newest followedAt, ties by
// canonical peer; the cursor anchors at the last returned row and a stale
// anchor (row no longer present) rejects with 40000, same external
// contract as before.
func (a *Aggregator) buildEdgeListResult(views []peerEdgeView, cursor edgeListCursor, params ListParams, kind string) (*ListResult, error) {
	start := 0
	if cursor.Peer != "" {
		start = -1
		for i, view := range views {
			if view.edge.Pending == cursor.Pending &&
				view.edge.FollowedAt == cursor.FollowedAt &&
				view.peer == cursor.Peer {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, ErrInvalidParameter
		}
	}

	size := clampListSize(params.Size)
	end := start + size
	if end > len(views) {
		end = len(views)
	}
	items := make([]ListItem, 0, end-start)
	for _, view := range views[start:end] {
		profile, err := a.lookupListProfile(view.peer)
		if err != nil {
			return nil, err
		}
		items = append(items, buildListItem(params.View, profile, view.edge))
	}

	nextCursor := ""
	if end < len(views) && end > start {
		last := views[end-1]
		encoded, err := encodeEdgeListCursor(edgeListCursor{
			Kind:       kind,
			Pending:    last.edge.Pending,
			FollowedAt: last.edge.FollowedAt,
			Peer:       last.peer,
		})
		if err != nil {
			return nil, err
		}
		nextCursor = encoded
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
		Pending:      edge.Pending,
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

// resolveFollowerGlobalMetaId normalizes the pin publisher identity to the
// canonical GlobalMetaID through the §2.3 pipeline. The live MVC indexer
// supplies the wallet chain address in pin.GlobalMetaId (and pin.MetaId),
// so the field is never trusted verbatim: GlobalMetaIDs pass through,
// chain addresses convert locally via the IDAddress algorithm, legacy hex
// resolves through the profile archive (§4.4c root-cause fix).
func (a *Aggregator) resolveFollowerGlobalMetaId(pin *aggregator.PinInscription) (string, error) {
	if pin == nil {
		return "", nil
	}
	ref := firstNonEmpty(pin.GlobalMetaId, pin.MetaId, pin.CreateMetaId, pin.Address, pin.CreateAddress)
	if ref == "" {
		return "", nil
	}
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
