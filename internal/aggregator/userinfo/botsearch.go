package userinfo

import "strings"

// BotSearchProfile is the per-profile snapshot consumed by the botsearch
// aggregator (POST /api/bots/search). It carries only the fields the bot
// search scoring and enrichment need; ChatSkills is pre-parsed with the same
// rules as /api/metaid/list. See docs/specs/2026-08-22-bot-search-api.md.
type BotSearchProfile struct {
	GlobalMetaID  string
	MetaID        string
	Address       string
	ChainName     string
	Name          string
	AvatarId      string
	Bio           string
	Role          string
	Goal          string
	ChatSkills    []string
	Homepage      string
	HasChatPubkey bool
}

// BotSearchProfiles returns a snapshot of every locally indexed profile that
// carries searchable /info content. The inclusion rule mirrors the MetaID
// search corpus (buildMetaIDSearchDoc): identities that never wrote any of
// name/bio/role/soul/goal/persona/llm/chatSkills are excluded. The snapshot
// reads the warm profilesByIdentity cache deduplicated by canonical metaId;
// every returned value is an independent copy, so callers never share state
// with the cache.
func (a *Aggregator) BotSearchProfiles() []BotSearchProfile {
	out := make([]BotSearchProfile, 0)
	if a == nil {
		return out
	}
	seen := make(map[string]struct{})
	a.profilesByIdentity.Range(func(_, value any) bool {
		profile, ok := value.(*UserProfile)
		if !ok || profile == nil {
			return true
		}
		key := metaIDSearchDocKey(profile.MetaID)
		if key == "" {
			return true
		}
		if _, dup := seen[key]; dup {
			return true
		}
		seen[key] = struct{}{}
		if !botSearchProfileSearchable(profile) {
			return true
		}
		out = append(out, BotSearchProfile{
			GlobalMetaID:  strings.TrimSpace(profile.GlobalMetaID),
			MetaID:        strings.TrimSpace(profile.MetaID),
			Address:       strings.TrimSpace(profile.Address),
			ChainName:     strings.TrimSpace(profile.ChainName),
			Name:          strings.TrimSpace(profile.Name),
			AvatarId:      strings.TrimSpace(profile.AvatarId),
			Bio:           strings.TrimSpace(profile.Bio),
			Role:          strings.TrimSpace(profile.Role),
			Goal:          strings.TrimSpace(profile.Goal),
			ChatSkills:    parseMetaIDChatSkills(profile.ChatSkills),
			Homepage:      strings.TrimSpace(profile.Homepage),
			HasChatPubkey: strings.TrimSpace(profile.ChatPublicKey) != "",
		})
		return true
	})
	return out
}

// botSearchProfileSearchable mirrors the corpus inclusion rule of
// buildMetaIDSearchDoc without materializing the doc itself.
func botSearchProfileSearchable(profile *UserProfile) bool {
	if profile == nil {
		return false
	}
	if strings.TrimSpace(profile.Name) != "" ||
		strings.TrimSpace(profile.Bio) != "" ||
		strings.TrimSpace(profile.Role) != "" ||
		strings.TrimSpace(profile.Soul) != "" ||
		strings.TrimSpace(profile.Goal) != "" ||
		strings.TrimSpace(profile.Persona) != "" ||
		strings.TrimSpace(profile.LLM) != "" {
		return true
	}
	return len(parseMetaIDChatSkills(profile.ChatSkills)) > 0
}
