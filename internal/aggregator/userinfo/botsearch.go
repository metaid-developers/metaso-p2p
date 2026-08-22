package userinfo

import (
	"encoding/json"
	"strings"
)

// personaRoleGoal extracts role/goal from the raw /info/persona JSON. IDBots
// Bot edit writes a single persona object {"role","soul","goal"} and does NOT
// write separate /info/role or /info/goal pins, so the persona fields are the
// fallback source for BotSearchProfile.Role/Goal.
func personaRoleGoal(raw string) (role, goal string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || !json.Valid([]byte(trimmed)) {
		return "", ""
	}
	var persona struct {
		Role string `json:"role"`
		Goal string `json:"goal"`
	}
	if err := json.Unmarshal([]byte(trimmed), &persona); err != nil {
		return "", ""
	}
	return strings.TrimSpace(persona.Role), strings.TrimSpace(persona.Goal)
}

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
// name/bio/role/soul/goal/persona/llm/chatSkills are excluded. Role/Goal fall
// back to the /info/persona JSON {"role","goal"} when the separate
// /info/role|goal pins were never written (IDBots Bot edit only writes
// persona). The snapshot reads the warm profilesByIdentity cache deduplicated
// by canonical metaId; every returned value is an independent copy, so
// callers never share state with the cache.
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
		role := strings.TrimSpace(profile.Role)
		goal := strings.TrimSpace(profile.Goal)
		if role == "" || goal == "" {
			personaRole, personaGoal := personaRoleGoal(profile.Persona)
			if role == "" {
				role = personaRole
			}
			if goal == "" {
				goal = personaGoal
			}
		}
		out = append(out, BotSearchProfile{
			GlobalMetaID:  strings.TrimSpace(profile.GlobalMetaID),
			MetaID:        strings.TrimSpace(profile.MetaID),
			Address:       strings.TrimSpace(profile.Address),
			ChainName:     strings.TrimSpace(profile.ChainName),
			Name:          strings.TrimSpace(profile.Name),
			AvatarId:      strings.TrimSpace(profile.AvatarId),
			Bio:           strings.TrimSpace(profile.Bio),
			Role:          role,
			Goal:          goal,
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
