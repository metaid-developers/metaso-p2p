package bothomepage

import (
	"errors"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
)

var errUserInfoLookupUnavailable = errors.New("userinfo lookup unavailable")

// ProfileSnapshot is the profile subset needed to assemble a bot homepage.
type ProfileSnapshot struct {
	GlobalMetaId    string
	MetaId          string
	Address         string
	Name            string
	NameId          string
	Avatar          string
	AvatarId        string
	Background      string
	BackgroundId    string
	NftAvatar       string
	Bio             string
	BioId           string
	ChatPublicKey   string
	ChatPublicKeyId string
	ChainName       string
}

type ProfileLookup interface {
	LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error)
}

type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) ProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, errUserInfoLookupUnavailable
	}
	p, err := a.ui.LookupByGlobalMetaId(globalMetaId)
	return profileFromUserInfo(p), err
}

func profileFromUserInfo(p *userinfo.UserProfile) *ProfileSnapshot {
	if p == nil {
		return nil
	}
	return &ProfileSnapshot{
		GlobalMetaId:    p.GlobalMetaID,
		MetaId:          p.MetaID,
		Address:         p.Address,
		Name:            p.Name,
		NameId:          p.NameId,
		Avatar:          p.Avatar,
		AvatarId:        p.AvatarId,
		Background:      p.Background,
		BackgroundId:    p.BackgroundId,
		NftAvatar:       p.NftAvatar,
		Bio:             p.Bio,
		BioId:           p.BioId,
		ChatPublicKey:   p.ChatPublicKey,
		ChatPublicKeyId: p.ChatPublicKeyId,
		ChainName:       p.ChainName,
	}
}
