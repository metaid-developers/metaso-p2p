package block

import "github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"

type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

// NewUserInfoLookupAdapter adapts the userinfo aggregator to the block
// package's ProfileLookup (same pattern as social/privatechat).
func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) ProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupByMetaId(metaId string) (*IdentityRef, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByMetaId(metaId)
	return identityRefFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByGlobalMetaId(globalMetaId string) (*IdentityRef, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByGlobalMetaId(globalMetaId)
	return identityRefFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByAddress(address string) (*IdentityRef, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByAddress(address)
	return identityRefFromUserInfo(p), err
}

func identityRefFromUserInfo(p *userinfo.UserProfile) *IdentityRef {
	if p == nil {
		return nil
	}
	return &IdentityRef{
		MetaId:       p.MetaID,
		GlobalMetaId: p.GlobalMetaID,
		Address:      p.Address,
		Name:         p.Name,
		NameId:       p.NameId,
		AvatarId:     p.AvatarId,
		Bio:          p.Bio,
		BioId:        p.BioId,
	}
}
