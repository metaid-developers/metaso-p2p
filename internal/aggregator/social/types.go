package social

import (
	"errors"
	"strings"
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

type ProfileLookup interface {
	LookupByMetaId(metaId string) (*TargetRef, error)
	LookupByGlobalMetaId(globalMetaId string) (*TargetRef, error)
	LookupByAddress(address string) (*TargetRef, error)
}

func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.profileLookup = lookup
}

func (a *Aggregator) lookupTargetRef(ref string) (*TargetRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || a == nil || a.profileLookup == nil {
		return nil, nil
	}

	for _, lookup := range []func(string) (*TargetRef, error){
		a.profileLookup.LookupByGlobalMetaId,
		a.profileLookup.LookupByMetaId,
		a.profileLookup.LookupByAddress,
	} {
		target, err := lookup(ref)
		if err != nil {
			return nil, err
		}
		if target != nil {
			return target, nil
		}
	}

	return nil, nil
}
