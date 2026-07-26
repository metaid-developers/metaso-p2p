package publishedcontent

import (
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
)

// userInfoLookupAdapter bridges the userinfo aggregator to the
// MetaAppProfileLookup interface, mirroring the skillservice adapter.
// Only name and avatar pin id are copied over.
type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

// NewUserInfoLookupAdapter wraps a userinfo.Aggregator as a
// MetaAppProfileLookup. main.go wires it before route serving.
func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) MetaAppProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupByGlobalMetaId(globalMetaId string) (*MetaAppProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByGlobalMetaId(globalMetaId)
	return metaAppSnapshotFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByMetaId(metaid string) (*MetaAppProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByMetaId(metaid)
	return metaAppSnapshotFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByAddress(address string) (*MetaAppProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByAddress(address)
	return metaAppSnapshotFromUserInfo(p), err
}

func metaAppSnapshotFromUserInfo(p *userinfo.UserProfile) *MetaAppProfileSnapshot {
	if p == nil {
		return nil
	}
	return &MetaAppProfileSnapshot{
		Name:     p.Name,
		AvatarId: p.AvatarId,
	}
}
