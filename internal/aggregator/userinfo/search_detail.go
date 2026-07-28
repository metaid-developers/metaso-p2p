package userinfo

import (
	"encoding/json"
	"strings"
)

// MetaIDDetail is the /api/metaid/detail/:identity response: the list item
// fields plus the full profile payload (persona/homepage raw JSON, parsed
// llm, chat pubkey, background, per-field pin ids).
type MetaIDDetail struct {
	MetaIDItem
	AvatarContentType string            `json:"avatarContentType,omitempty"`
	Role              string            `json:"role,omitempty"`
	Soul              string            `json:"soul,omitempty"`
	Goal              string            `json:"goal,omitempty"`
	Persona           json.RawMessage   `json:"persona,omitempty"`
	LLM               *MetaIDLLMInfo    `json:"llm,omitempty"`
	Homepage          json.RawMessage   `json:"homepage,omitempty"`
	Background        string            `json:"background,omitempty"`
	ChatPublicKey     string            `json:"chatPubkey,omitempty"`
	FieldPins         map[string]string `json:"fieldPins,omitempty"`
}

// MetaIDDetail resolves any of globalMetaId / metaId / address through the
// local identity indexes and returns the full profile. (nil, nil) means "not
// found". Detail intentionally works for profiles excluded from the search
// corpus (no searchable content): they still exist and can be looked up.
func (a *Aggregator) MetaIDDetail(identity string) (*MetaIDDetail, error) {
	profile, err := a.LookupLocalByIdentity(identity)
	if err != nil || profile == nil {
		return nil, err
	}

	item := MetaIDItem{
		GlobalMetaID:  strings.TrimSpace(profile.GlobalMetaID),
		MetaID:        strings.TrimSpace(profile.MetaID),
		Address:       strings.TrimSpace(profile.Address),
		ChainName:     strings.TrimSpace(profile.ChainName),
		Name:          strings.TrimSpace(profile.Name),
		AvatarId:      strings.TrimSpace(profile.AvatarId),
		Bio:           strings.TrimSpace(profile.Bio),
		ChatSkills:    parseMetaIDChatSkills(profile.ChatSkills),
		HasChatPubkey: strings.TrimSpace(profile.ChatPublicKey) != "",
		HasHomepage:   strings.TrimSpace(profile.Homepage) != "",
	}
	if doc := a.getSearchDoc(profile.MetaID); doc != nil {
		item.CreatedAt = doc.createdAt
		item.UpdatedAt = doc.updatedAt
	}
	// Profiles outside the search corpus (or written before the doc existed)
	// still have their registration time in the creation records.
	if item.CreatedAt == 0 && a.store != nil && item.GlobalMetaID != "" {
		if raw, getErr := a.store.Get(namespace, globalMetaIDCreationKey(item.GlobalMetaID)); getErr == nil && len(raw) > 0 {
			var record globalMetaIDCreationRecord
			if json.Unmarshal(raw, &record) == nil && record.CreatedAt > 0 {
				item.CreatedAt = record.CreatedAt / 1000
			}
		}
	}

	detail := &MetaIDDetail{
		MetaIDItem:        item,
		AvatarContentType: strings.TrimSpace(profile.AvatarContentType),
		Role:              strings.TrimSpace(profile.Role),
		Soul:              strings.TrimSpace(profile.Soul),
		Goal:              strings.TrimSpace(profile.Goal),
		Background:        strings.TrimSpace(profile.Background),
		ChatPublicKey:     strings.TrimSpace(profile.ChatPublicKey),
	}
	// persona/homepage pass through as raw chain JSON; unpublishable values
	// (empty or invalid JSON) are omitted rather than normalised.
	if raw := strings.TrimSpace(profile.Persona); raw != "" && json.Valid([]byte(raw)) {
		detail.Persona = json.RawMessage(raw)
	}
	if raw := strings.TrimSpace(profile.Homepage); raw != "" && json.Valid([]byte(raw)) {
		detail.Homepage = json.RawMessage(raw)
	}
	if llm := parseMetaIDLLM(profile.LLM); llm.Provider != "" || llm.Model != "" || llm.Name != "" {
		detail.LLM = &llm
	}
	detail.FieldPins = metaIDFieldPins(profile)
	return detail, nil
}

func metaIDFieldPins(profile *UserProfile) map[string]string {
	if profile == nil {
		return nil
	}
	pins := make(map[string]string, 12)
	for _, entry := range []struct {
		field string
		pinID string
	}{
		{"name", profile.NameId},
		{"avatar", profile.AvatarId},
		{"bio", profile.BioId},
		{"role", profile.RoleId},
		{"soul", profile.SoulId},
		{"goal", profile.GoalId},
		{"chatSkills", profile.ChatSkillsId},
		{"llm", profile.LLMId},
		{"persona", profile.PersonaId},
		{"homepage", profile.HomepageId},
		{"background", profile.BackgroundId},
		{"chatpubkey", profile.ChatPublicKeyId},
	} {
		if pinID := strings.TrimSpace(entry.pinID); pinID != "" {
			pins[entry.field] = pinID
		}
	}
	if len(pins) == 0 {
		return nil
	}
	return pins
}
