package social

import (
	"errors"
)

// TargetRef is the canonical follow-target identity shape used internally by
// the social aggregator.
type TargetRef struct {
	MetaId       string
	GlobalMetaId string
	Address      string
	Name         string
	NameId       string
	AvatarId     string
	Bio          string
	BioId        string
}

type FollowEdge struct {
	FollowerGlobalMetaId string `json:"followerGlobalMetaId"`
	TargetGlobalMetaId   string `json:"targetGlobalMetaId"`
	FollowPinId          string `json:"followPinId"`
	FollowedAt           int64  `json:"followedAt"`
	Active               bool   `json:"active"`
	// Pending marks an edge materialized from the mempool overlay (unconfirmed
	// pin, spec §4.4b). Confirmed edges serialize it away (omitempty), so the
	// stored confirmed-model JSON is unchanged.
	Pending bool `json:"pending,omitempty"`
}

const (
	ViewCompact = "compact"
	ViewProfile = "profile"
)

type RelationshipParams struct {
	SourceGlobalMetaId string
	TargetGlobalMetaId string
}

type RelationshipSide struct {
	GlobalMetaId  string `json:"globalMetaId"`
	FollowsTarget bool   `json:"followsTarget"`
	FollowsSource bool   `json:"followsSource"`
	FollowPinId   string `json:"followPinId"`
	FollowedAt    int64  `json:"followedAt"`
	// Pending is true when this side's edge comes from the mempool overlay
	// (spec §4.4b).
	Pending bool `json:"pending,omitempty"`
}

type RelationshipResult struct {
	Source RelationshipSide `json:"source"`
	Target RelationshipSide `json:"target"`
	Mutual bool             `json:"mutual"`
}

type ListParams struct {
	GlobalMetaId string
	Cursor       string
	Size         int
	View         string
}

type ListItem struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name,omitempty"`
	NameId       string `json:"nameId,omitempty"`
	AvatarId     string `json:"avatarId,omitempty"`
	Bio          string `json:"bio,omitempty"`
	BioId        string `json:"bioId,omitempty"`
	FollowedAt   int64  `json:"followedAt"`
	FollowPinId  string `json:"followPinId"`
	// Pending is true when the edge comes from the mempool overlay (the
	// show.now "Mempool:1" marker precedent, spec §4.4b).
	Pending bool `json:"pending,omitempty"`
}

type ListResult struct {
	List       []ListItem `json:"list"`
	NextCursor string     `json:"nextCursor"`
	Size       int        `json:"size"`
}

var (
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrNotFound         = errors.New("subject not found")
	ErrUnavailable      = errors.New("aggregation unavailable")
)

const (
	defaultListSize = 20
	maxListSize     = 100
)

// clampListSize applies the shared list-size rule of the §4 pagination
// contract: unset → default, out-of-range → capped.
func clampListSize(size int) int {
	if size <= 0 {
		return defaultListSize
	}
	if size > maxListSize {
		return maxListSize
	}
	return size
}

type ProfileLookup interface {
	LookupByMetaId(metaId string) (*TargetRef, error)
	LookupByGlobalMetaId(globalMetaId string) (*TargetRef, error)
	LookupByAddress(address string) (*TargetRef, error)
}

func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.profileLookup = lookup
}
