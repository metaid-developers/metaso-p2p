package metaweb

import (
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
)

// UserInfoProfileNamer adapts the userinfo aggregator to the ProfileNamer
// interface. It uses the local reverse indexes only (LookupLocalByIdentity):
// enrichment must stay in-process and cheap, and unknown profiles simply
// yield empty strings.
type UserInfoProfileNamer struct {
	agg *userinfo.Aggregator
}

func NewUserInfoProfileNamer(agg *userinfo.Aggregator) *UserInfoProfileNamer {
	return &UserInfoProfileNamer{agg: agg}
}

func (n *UserInfoProfileNamer) ProfileNameAvatar(globalMetaId, metaId string) (string, string) {
	if n == nil || n.agg == nil {
		return "", ""
	}
	var profile *userinfo.UserProfile
	if identity := strings.TrimSpace(globalMetaId); identity != "" {
		profile, _ = n.agg.LookupLocalByIdentity(identity)
	}
	if profile == nil {
		if identity := strings.TrimSpace(metaId); identity != "" {
			profile, _ = n.agg.LookupLocalByIdentity(identity)
		}
	}
	if profile == nil {
		return "", ""
	}
	avatar := strings.TrimSpace(profile.Avatar)
	if avatar == "" {
		if avatarId := strings.TrimSpace(profile.AvatarId); avatarId != "" {
			// The contract keeps list avatars as raw metafile:// URIs in v1.
			avatar = "metafile://" + avatarId
		}
	}
	return strings.TrimSpace(profile.Name), avatar
}
