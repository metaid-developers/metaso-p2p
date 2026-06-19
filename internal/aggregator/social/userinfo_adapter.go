package social

import "github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"

type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) ProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupByMetaId(metaId string) (*TargetRef, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByMetaId(metaId)
	return targetRefFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByGlobalMetaId(globalMetaId string) (*TargetRef, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByGlobalMetaId(globalMetaId)
	return targetRefFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByAddress(address string) (*TargetRef, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByAddress(address)
	return targetRefFromUserInfo(p), err
}

func targetRefFromUserInfo(p *userinfo.UserProfile) *TargetRef {
	if p == nil {
		return nil
	}
	return &TargetRef{
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
