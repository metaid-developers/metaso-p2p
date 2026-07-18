package privatechat

import "github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"

type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) ProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupByMetaId(metaid string) (*IdentityProfile, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByMetaId(metaid)
	return identityFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByGlobalMetaId(globalMetaId string) (*IdentityProfile, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByGlobalMetaId(globalMetaId)
	return identityFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupByAddress(address string) (*IdentityProfile, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByAddress(address)
	return identityFromUserInfo(p), err
}

func (a *userInfoLookupAdapter) LookupLocalByIdentity(identity string) (*IdentityProfile, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupLocalByIdentity(identity)
	return identityFromUserInfo(p), err
}

func identityFromUserInfo(p *userinfo.UserProfile) *IdentityProfile {
	if p == nil {
		return nil
	}
	return &IdentityProfile{
		MetaId:       p.MetaID,
		GlobalMetaId: p.GlobalMetaID,
		Address:      p.Address,
	}
}
