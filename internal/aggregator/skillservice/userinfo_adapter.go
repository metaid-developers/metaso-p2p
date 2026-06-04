package skillservice

import (
	"github.com/metaid-developers/meta-socket/internal/aggregator/userinfo"
)

// userInfoLookupAdapter is the thin bridge between the userinfo aggregator
// (a sibling package) and the ProfileLookup interface this package
// consumes. Keeping the adapter inside skillservice rather than userinfo
// means userinfo stays oblivious to Bot Hub concepts; if another consumer
// shows up later it can write its own adapter without touching userinfo.
//
// The adapter copies only the fields the Bot Hub UI cares about. The rest of
// userinfo.UserProfile (nameId, bio, …) stays unused so future userinfo schema
// changes have no chance of leaking into Bot Hub responses.
type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

// NewUserInfoLookupAdapter wraps a userinfo.Aggregator as a ProfileLookup.
// main.go calls this once after both aggregators are registered.
func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) ProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupByMetaId(metaid string) (*ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByMetaId(metaid)
	return snapshotFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByGlobalMetaId(globalMetaId)
	return snapshotFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByAddress(address string) (*ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByAddress(address)
	return snapshotFromUserInfo(p), err
}

// snapshotFromUserInfo collapses a userinfo.UserProfile to a Bot Hub-shaped
// ProfileSnapshot. Returns nil when the input is nil so the caller can
// distinguish "not found" from "found with empty fields".
func snapshotFromUserInfo(p *userinfo.UserProfile) *ProfileSnapshot {
	if p == nil {
		return nil
	}
	return &ProfileSnapshot{
		MetaId:        p.MetaID,
		GlobalMetaId:  p.GlobalMetaID,
		Address:       p.Address,
		Name:          p.Name,
		Avatar:        p.Avatar,
		AvatarId:      p.AvatarId,
		ChatPublicKey: p.ChatPublicKey,
	}
}
