package social

import (
	"strings"

	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// Five-state relationship fold (spec 2026-10-29 §4.3).
const (
	RelationBlocked   = "blocked"
	RelationFriend    = "friend"
	RelationFollowing = "following"
	RelationFollower  = "follower"
	RelationStranger  = "stranger"
)

const maxBatchTargets = 100

type BatchRelationshipParams struct {
	SourceGlobalMetaId string
	Targets            []string
}

// BatchRelationshipItem reports both the single-value relation fold and the
// structured booleans (always present, never folded away).
type BatchRelationshipItem struct {
	GlobalMetaId string `json:"globalMetaId"`
	Relation     string `json:"relation"`
	IsFriend     bool   `json:"isFriend"`
	FollowsMe    bool   `json:"followsMe"`
	FollowedByMe bool   `json:"followedByMe"`
	Blocking     bool   `json:"blocking"`
	BlockedBy    bool   `json:"blockedBy"`
}

type BatchRelationshipResult struct {
	List []BatchRelationshipItem `json:"list"`
}

// BatchRelationships answers "what is my relation to each of these peers" in
// one call (spec §4.3). It is the deliberate exception to the §4.5 filter: it
// reports block state instead of filtering. Response order matches request
// order; each item echoes the canonical GlobalMetaID of the target. A target
// that is format-valid but has no indexed presence is not an error — batch
// lookups are intentionally total and answer "stranger". A legacy-hex target
// that does not resolve through the profile archive echoes the raw input
// (there is no canonical form to report) with all-false booleans.
func (a *Aggregator) BatchRelationships(params BatchRelationshipParams) (*BatchRelationshipResult, error) {
	sourceInput := strings.TrimSpace(params.SourceGlobalMetaId)
	targets := params.Targets
	if len(targets) == 0 || len(targets) > maxBatchTargets {
		return nil, ErrInvalidParameter
	}
	normalisedTargets := make([]string, 0, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if !isBatchTargetForm(target) {
			return nil, ErrInvalidParameter
		}
		normalisedTargets = append(normalisedTargets, target)
	}

	sourceId, ok, err := a.normalizeIdentity(sourceInput)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}

	blockingSet, blockedBySet, err := a.blockPeerSets(sourceId)
	if err != nil {
		return nil, err
	}

	list := make([]BatchRelationshipItem, 0, len(normalisedTargets))
	for _, target := range normalisedTargets {
		canonical := target
		if idaddress.ValidateIDAddress(strings.ToLower(target)) {
			canonical = strings.ToLower(target)
		} else {
			// Legacy 64-hex target: resolve through the profile archive.
			resolved := false
			if a.profileLookup != nil {
				ref, lookupErr := a.profileLookup.LookupByMetaId(strings.ToLower(target))
				if lookupErr != nil {
					return nil, unavailableWrap(lookupErr)
				}
				if ref != nil && strings.TrimSpace(ref.GlobalMetaId) != "" {
					canonical = strings.TrimSpace(ref.GlobalMetaId)
					resolved = true
				}
			}
			if !resolved {
				// Total semantics: no canonical form exists, report the raw
				// input with no active relation.
				list = append(list, BatchRelationshipItem{
					GlobalMetaId: canonical,
					Relation:     RelationStranger,
				})
				continue
			}
		}

		forwardEdge, err := a.loadActiveEdge(sourceId, canonical)
		if err != nil {
			return nil, err
		}
		reverseEdge, err := a.loadActiveEdge(canonical, sourceId)
		if err != nil {
			return nil, err
		}
		item := BatchRelationshipItem{
			GlobalMetaId: canonical,
			FollowsMe:    reverseEdge != nil,
			FollowedByMe: forwardEdge != nil,
			Blocking:     peerSetContains(blockingSet, canonical),
			BlockedBy:    peerSetContains(blockedBySet, canonical),
		}
		item.IsFriend = item.FollowsMe && item.FollowedByMe
		switch {
		case item.Blocking || item.BlockedBy:
			item.Relation = RelationBlocked
		case item.IsFriend:
			item.Relation = RelationFriend
		case item.FollowedByMe:
			item.Relation = RelationFollowing
		case item.FollowsMe:
			item.Relation = RelationFollower
		default:
			item.Relation = RelationStranger
		}
		list = append(list, item)
	}
	return &BatchRelationshipResult{List: list}, nil
}

// isBatchTargetForm matches the §4.3 request rule: each entry must be a valid
// GlobalMetaID or a legacy 64-hex metaid (anything else is a 40000).
func isBatchTargetForm(target string) bool {
	if target == "" {
		return false
	}
	if idaddress.ValidateIDAddress(strings.ToLower(target)) {
		return true
	}
	return isLegacyHexMetaId(target)
}

func peerSetContains(set map[string]struct{}, peer string) bool {
	_, ok := set[peer]
	return ok
}
