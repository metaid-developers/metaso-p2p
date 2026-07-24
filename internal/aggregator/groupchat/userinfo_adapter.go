package groupchat

import "github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"

// ProfileSnapshot is the immutable user-info shape placed directly in latest
// chat responses. Both chat-public-key spellings preserve IDChat compatibility.
type ProfileSnapshot struct {
	GlobalMetaId          string `json:"globalMetaId"`
	MetaId                string `json:"metaid"`
	Address               string `json:"address"`
	Name                  string `json:"name,omitempty"`
	NameId                string `json:"nameId,omitempty"`
	Avatar                string `json:"avatar,omitempty"`
	AvatarId              string `json:"avatarId,omitempty"`
	AvatarContentType     string `json:"avatarContentType,omitempty"`
	NftAvatar             string `json:"nftAvatar,omitempty"`
	Bio                   string `json:"bio,omitempty"`
	BioId                 string `json:"bioId,omitempty"`
	Role                  string `json:"role,omitempty"`
	RoleId                string `json:"roleId,omitempty"`
	Soul                  string `json:"soul,omitempty"`
	SoulId                string `json:"soulId,omitempty"`
	Goal                  string `json:"goal,omitempty"`
	GoalId                string `json:"goalId,omitempty"`
	ChatSkills            string `json:"chatSkills,omitempty"`
	ChatSkillsId          string `json:"chatSkillsId,omitempty"`
	LLM                   string `json:"llm,omitempty"`
	LLMId                 string `json:"llmId,omitempty"`
	Persona               string `json:"persona,omitempty"`
	PersonaId             string `json:"personaId,omitempty"`
	Homepage              string `json:"homepage,omitempty"`
	HomepageId            string `json:"homepageId,omitempty"`
	Background            string `json:"background,omitempty"`
	BackgroundId          string `json:"backgroundId,omitempty"`
	ChatPublicKey         string `json:"chatpubkey,omitempty"`
	ChatPublicKeyId       string `json:"chatpubkeyId,omitempty"`
	ChatPublicKeyCompat   string `json:"chatPublicKey,omitempty"`
	ChatPublicKeyIdCompat string `json:"chatPublicKeyId,omitempty"`
	ChainName             string `json:"chainName,omitempty"`
}

type ProfileLookup interface {
	LookupLocalByIdentity(identity string) (*ProfileSnapshot, error)
}

type userInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

func NewUserInfoLookupAdapter(ui *userinfo.Aggregator) ProfileLookup {
	return &userInfoLookupAdapter{ui: ui}
}

func (a *userInfoLookupAdapter) LookupLocalByIdentity(identity string) (*ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	profile, err := a.ui.LookupLocalByIdentity(identity)
	return groupChatProfileFromUserInfo(profile), err
}

func groupChatProfileFromUserInfo(profile *userinfo.UserProfile) *ProfileSnapshot {
	if profile == nil {
		return nil
	}
	return &ProfileSnapshot{
		GlobalMetaId:          profile.GlobalMetaID,
		MetaId:                profile.MetaID,
		Address:               profile.Address,
		Name:                  profile.Name,
		NameId:                profile.NameId,
		Avatar:                profile.Avatar,
		AvatarId:              profile.AvatarId,
		AvatarContentType:     profile.AvatarContentType,
		NftAvatar:             profile.NftAvatar,
		Bio:                   profile.Bio,
		BioId:                 profile.BioId,
		Role:                  profile.Role,
		RoleId:                profile.RoleId,
		Soul:                  profile.Soul,
		SoulId:                profile.SoulId,
		Goal:                  profile.Goal,
		GoalId:                profile.GoalId,
		ChatSkills:            profile.ChatSkills,
		ChatSkillsId:          profile.ChatSkillsId,
		LLM:                   profile.LLM,
		LLMId:                 profile.LLMId,
		Persona:               profile.Persona,
		PersonaId:             profile.PersonaId,
		Homepage:              profile.Homepage,
		HomepageId:            profile.HomepageId,
		Background:            profile.Background,
		BackgroundId:          profile.BackgroundId,
		ChatPublicKey:         profile.ChatPublicKey,
		ChatPublicKeyId:       profile.ChatPublicKeyId,
		ChatPublicKeyCompat:   profile.ChatPublicKey,
		ChatPublicKeyIdCompat: profile.ChatPublicKeyId,
		ChainName:             profile.ChainName,
	}
}
