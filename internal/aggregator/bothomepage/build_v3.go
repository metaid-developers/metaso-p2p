package bothomepage

import (
	"encoding/json"
	"strings"
)

func (a *Aggregator) BuildV3(requestGlobalMetaId string, opts Options) (*DataV3, error) {
	requestGlobalMetaId = strings.TrimSpace(requestGlobalMetaId)
	if requestGlobalMetaId == "" {
		return nil, ErrInvalidParameter
	}
	if a == nil || a.profileLookup == nil {
		return nil, ErrAggregationUnavailable
	}

	profile, err := a.profileLookup.LookupByGlobalMetaId(requestGlobalMetaId)
	if err != nil {
		return nil, ErrAggregationUnavailable
	}
	if profile == nil {
		return nil, ErrNotFound
	}

	canonicalGlobalMetaId := firstNonEmpty(profile.GlobalMetaId, requestGlobalMetaId)
	outProfile, warnings := buildProfileV3(profile)

	return &DataV3{
		SchemaVersion: schemaVersionV3,
		Identity: IdentityV3{
			GlobalMetaId: canonicalGlobalMetaId,
			LegacyMetaId: strings.TrimSpace(profile.MetaId),
			Display:      abbreviateGlobalMetaId(canonicalGlobalMetaId),
		},
		Profile:  outProfile,
		Presence: a.resolvePresence(*profile, opts.IncludePresence),
		Sections: []SectionV3{},
		Warnings: warnings,
	}, nil
}

func buildProfileV3(profile *ProfileSnapshot) (ProfileV3, []string) {
	if profile == nil {
		return ProfileV3{}, nil
	}

	warnings := make([]string, 0)
	llm, llmWarnings := parseJSONBlockV3(profile.LLM, profile.LLMId, "/info/llm")
	warnings = append(warnings, llmWarnings...)
	persona, personaWarnings := parseJSONBlockV3(profile.Persona, profile.PersonaId, "/info/persona")
	warnings = append(warnings, personaWarnings...)
	homepage, homepageWarnings := parseJSONBlockV3(profile.Homepage, profile.HomepageId, "/info/homepage")
	warnings = append(warnings, homepageWarnings...)

	return ProfileV3{
		Name:       strings.TrimSpace(profile.Name),
		Avatar:     avatarV3(profile),
		Bio:        strings.TrimSpace(profile.Bio),
		ChatPubkey: strings.TrimSpace(profile.ChatPublicKey),
		LLM:        llm,
		Persona:    persona,
		Homepage:   homepage,
		Pins: ProfilePinsV3{
			Name:       strings.TrimSpace(profile.NameId),
			Bio:        strings.TrimSpace(profile.BioId),
			ChatPubkey: strings.TrimSpace(profile.ChatPublicKeyId),
		},
	}, warnings
}

func avatarV3(profile *ProfileSnapshot) *AvatarV3 {
	if profile == nil {
		return nil
	}
	pinID := strings.TrimSpace(profile.AvatarId)
	if pinID == "" {
		return nil
	}
	return &AvatarV3{
		PinId:       pinID,
		ContentType: avatarContentTypeV3(profile.AvatarContentType),
	}
}

func parseJSONBlockV3(raw, pinID, path string) (*JSONBlockV3, []string) {
	raw = strings.TrimSpace(raw)
	pinID = strings.TrimSpace(pinID)
	if raw == "" || pinID == "" {
		return nil, nil
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, []string{"invalid JSON in " + path}
	}

	return &JSONBlockV3{
		PinId:   pinID,
		Payload: payload,
	}, nil
}

func avatarContentTypeV3(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	contentType = strings.TrimSuffix(contentType, ";binary")
	return strings.TrimSpace(contentType)
}
