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

// legacyProfileBlob is the pre-v3 IDBots client shape: the whole profile was
// written as a single /info/bio JSON object instead of the v3 split pins
// (/info/bio plain text, /info/persona {role,soul,goal}, /info/chatSkills
// {allowPrivateChatSkills,allowGroupChatSkills}). RawMessage fields keep the
// parse tolerant of unexpected value types.
type legacyProfileBlob struct {
	Role                 string          `json:"role"`
	Soul                 string          `json:"soul"`
	Goal                 string          `json:"goal"`
	Background           string          `json:"background"`
	Bio                  string          `json:"bio"`
	LLM                  json.RawMessage `json:"llm"`
	AllowChatSkills      json.RawMessage `json:"allowChatSkills"`
	AllowChatSkillsSnake json.RawMessage `json:"allow_chat_skills"`
}

// parseLegacyBioBlob parses a legacy whole-profile /info/bio JSON object. It
// only recognizes objects carrying at least one known profile key, so a bio
// that happens to be arbitrary JSON is left untouched.
func parseLegacyBioBlob(raw string) (*legacyProfileBlob, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") || !json.Valid([]byte(trimmed)) {
		return nil, false
	}
	var blob legacyProfileBlob
	if err := json.Unmarshal([]byte(trimmed), &blob); err != nil {
		return nil, false
	}
	if blob.Role == "" && blob.Soul == "" && blob.Goal == "" &&
		blob.Background == "" && blob.Bio == "" &&
		len(blob.LLM) == 0 && len(blob.AllowChatSkills) == 0 && len(blob.AllowChatSkillsSnake) == 0 {
		return nil, false
	}
	return &blob, true
}

// legacyBlobChatSkills extracts the skill list from a legacy blob:
// allowChatSkills, falling back to the snake_case allow_chat_skills.
func legacyBlobChatSkills(blob *legacyProfileBlob) []string {
	for _, raw := range []json.RawMessage{blob.AllowChatSkills, blob.AllowChatSkillsSnake} {
		if len(raw) == 0 {
			continue
		}
		var skills []string
		if err := json.Unmarshal(raw, &skills); err != nil {
			continue
		}
		out := make([]string, 0, len(skills))
		for _, skill := range skills {
			if trimmed := strings.TrimSpace(skill); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
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
// persona). Profiles whose /info/bio is a legacy pre-v3 whole-profile JSON
// object get the IDBots-restore-aligned fallback — bio ← background||bio,
// role/goal ← the object, chatSkills ← allowChatSkills||allow_chat_skills —
// but only when no /info/persona and no /info/chatSkills pin exists, and
// never overriding dedicated /info/role|goal pins. The snapshot reads the
// warm profilesByIdentity cache deduplicated by canonical metaId; every
// returned value is an independent copy, so callers never share state with
// the cache.
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
		bio := strings.TrimSpace(profile.Bio)
		chatSkills := parseMetaIDChatSkills(profile.ChatSkills)
		// Legacy pre-v3 whole-profile /info/bio JSON. Gated on both v3 pins
		// being absent so new-style pins always win; dedicated /info/role|goal
		// pins are never overridden either.
		if strings.TrimSpace(profile.Persona) == "" && len(chatSkills) == 0 {
			if blob, ok := parseLegacyBioBlob(profile.Bio); ok {
				if background := strings.TrimSpace(blob.Background); background != "" {
					bio = background
				} else {
					bio = strings.TrimSpace(blob.Bio)
				}
				if role == "" {
					role = strings.TrimSpace(blob.Role)
				}
				if goal == "" {
					goal = strings.TrimSpace(blob.Goal)
				}
				chatSkills = legacyBlobChatSkills(blob)
			}
		}
		out = append(out, BotSearchProfile{
			GlobalMetaID:  strings.TrimSpace(profile.GlobalMetaID),
			MetaID:        strings.TrimSpace(profile.MetaID),
			Address:       strings.TrimSpace(profile.Address),
			ChainName:     strings.TrimSpace(profile.ChainName),
			Name:          strings.TrimSpace(profile.Name),
			AvatarId:      strings.TrimSpace(profile.AvatarId),
			Bio:           bio,
			Role:          role,
			Goal:          goal,
			ChatSkills:    chatSkills,
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
