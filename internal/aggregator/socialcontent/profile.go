package socialcontent

import (
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
)

// AuthorProfileSnapshot carries the userinfo fields surfaced next to a post
// or comment author. Keep it minimal: name and avatar pin id are what
// downstream renders next to each author.
type AuthorProfileSnapshot struct {
	Name     string
	AvatarId string
}

// AuthorProfileLookup resolves author profiles from the userinfo aggregator.
// It follows the same per-package interface + adapter pattern as
// skillservice/publishedcontent so userinfo stays oblivious to consumers.
type AuthorProfileLookup interface {
	LookupLocalByIdentity(identity string) (*AuthorProfileSnapshot, error)
}

// userInfoLookupAdapter bridges the userinfo aggregator to the
// AuthorProfileLookup interface. Only name and avatar pin id are copied over.
type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

// NewUserInfoLookupAdapter wraps a userinfo.Aggregator as an
// AuthorProfileLookup. main.go wires it before route serving.
func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) AuthorProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupLocalByIdentity(identity string) (*AuthorProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupLocalByIdentity(identity)
	return authorSnapshotFromUserInfo(p), err
}

func authorSnapshotFromUserInfo(p *userinfo.UserProfile) *AuthorProfileSnapshot {
	if p == nil {
		return nil
	}
	return &AuthorProfileSnapshot{
		Name:     p.Name,
		AvatarId: p.AvatarId,
	}
}
