package paybuzz

import (
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
)

type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

// NewUserInfoLookupAdapter bridges userinfo.Aggregator onto paybuzz.ProfileLookup.
func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) ProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupByGlobalMetaId(globalMetaId string) (*AuthorProfileSnapshot, error) {
	if a == nil || a.ui == nil || globalMetaId == "" {
		return nil, nil
	}
	p, err := a.ui.LookupByGlobalMetaId(globalMetaId)
	return snapshotFromUserProfile(p), err
}

func (a *userInfoLookupAdapter) LookupByMetaId(metaId string) (*AuthorProfileSnapshot, error) {
	if a == nil || a.ui == nil || metaId == "" {
		return nil, nil
	}
	p, err := a.ui.LookupByMetaId(metaId)
	return snapshotFromUserProfile(p), err
}

func (a *userInfoLookupAdapter) LookupByAddress(address string) (*AuthorProfileSnapshot, error) {
	if a == nil || a.ui == nil || address == "" {
		return nil, nil
	}
	p, err := a.ui.LookupByAddress(address)
	return snapshotFromUserProfile(p), err
}

func snapshotFromUserProfile(p *userinfo.UserProfile) *AuthorProfileSnapshot {
	if p == nil {
		return nil
	}
	return &AuthorProfileSnapshot{
		GlobalMetaId: p.GlobalMetaID,
		MetaId:       p.MetaID,
		Address:      p.Address,
		Name:         p.Name,
		Avatar:       p.AvatarId,
	}
}

func (a *Aggregator) lookupProfileLocked(globalMetaId, metaId, address string) *AuthorProfileSnapshot {
	if a.profileLookup == nil {
		return nil
	}
	if globalMetaId != "" {
		if snap, _ := a.profileLookup.LookupByGlobalMetaId(globalMetaId); snap != nil {
			return snap
		}
	}
	if metaId != "" {
		if snap, _ := a.profileLookup.LookupByMetaId(metaId); snap != nil {
			return snap
		}
	}
	if address != "" {
		if snap, _ := a.profileLookup.LookupByAddress(address); snap != nil {
			return snap
		}
	}
	return nil
}

func (a *Aggregator) resolveAuthorProfile(rec *Record) AuthorDTO {
	out := AuthorDTO{
		GlobalMetaId: firstNonEmpty(rec.CreatorGlobalMetaId, rec.AuthorMetaId),
		MetaId:       rec.CreatorMetaId,
		Address:      firstNonEmpty(rec.AuthorMvcAddress, rec.CreatorAddress),
	}
	a.mu.RLock()
	lookup := a.profileLookup
	a.mu.RUnlock()
	if lookup == nil {
		return out
	}

	var snap *AuthorProfileSnapshot
	for _, gid := range []string{rec.CreatorGlobalMetaId, rec.AuthorMetaId} {
		if gid != "" {
			if s, _ := lookup.LookupByGlobalMetaId(gid); s != nil {
				snap = s
				break
			}
		}
	}
	if snap == nil && rec.CreatorMetaId != "" {
		snap, _ = lookup.LookupByMetaId(rec.CreatorMetaId)
	}
	if snap == nil {
		for _, addr := range []string{rec.AuthorMvcAddress, rec.CreatorAddress} {
			if addr != "" {
				if s, _ := lookup.LookupByAddress(addr); s != nil {
					snap = s
					break
				}
			}
		}
	}
	if snap != nil {
		if snap.GlobalMetaId != "" {
			out.GlobalMetaId = snap.GlobalMetaId
		}
		if snap.MetaId != "" && out.MetaId == "" {
			out.MetaId = snap.MetaId
		}
		if snap.Address != "" && out.Address == "" {
			out.Address = snap.Address
		}
		out.Name = snap.Name
		out.Avatar = snap.Avatar
	}
	return out
}
