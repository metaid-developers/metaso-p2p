package bothomepage

import (
	"encoding/json"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
)

func (a *Aggregator) BuildV3(requestGlobalMetaId string, opts Options) (*DataV3, error) {
	requestGlobalMetaId = strings.TrimSpace(requestGlobalMetaId)
	if requestGlobalMetaId == "" {
		return nil, ErrInvalidParameter
	}
	if a == nil || a.profileLookup == nil {
		return nil, ErrAggregationUnavailable
	}
	if cached, ok := a.loadV3FromCache(buildV3CacheKey(requestGlobalMetaId, opts), opts.IncludePresence); ok {
		return cached, nil
	}

	profile, err := a.profileLookup.LookupByGlobalMetaId(requestGlobalMetaId)
	if err != nil {
		return nil, ErrAggregationUnavailable
	}
	if profile == nil {
		return nil, ErrNotFound
	}

	canonical := CanonicalIdentity{
		GlobalMetaId: firstNonEmpty(profile.GlobalMetaId, requestGlobalMetaId),
		MetaId:       strings.TrimSpace(profile.MetaId),
		Address:      strings.TrimSpace(profile.Address),
		ChainName:    firstNonEmpty(strings.TrimSpace(profile.ChainName), strings.TrimSpace(opts.ChainName)),
	}
	outProfile, warnings := buildProfileV3(profile)
	sections := make([]SectionV3, 0)
	if opts.IncludeSections {
		var sectionWarnings []string
		sections, sectionWarnings = a.loadSectionsV3(canonical, opts)
		warnings = append(warnings, sectionWarnings...)
	}

	result := &DataV3{
		SchemaVersion: schemaVersionV3,
		Identity: IdentityV3{
			GlobalMetaId: canonical.GlobalMetaId,
			LegacyMetaId: canonical.MetaId,
			Display:      abbreviateGlobalMetaId(canonical.GlobalMetaId),
		},
		Profile:  outProfile,
		Presence: a.resolvePresence(*profile, opts.IncludePresence),
		Sections: sections,
		Warnings: warnings,
	}
	a.storeV3Cache(buildV3CacheKey(requestGlobalMetaId, opts), result, profile)
	return result, nil
}

func (a *Aggregator) loadSectionsV3(canonical CanonicalIdentity, opts Options) ([]SectionV3, []string) {
	sections := make([]SectionV3, 0, 4)
	warnings := make([]string, 0)

	if opts.IncludeServices {
		section, warning := a.loadServicesSectionV3(canonical, opts)
		sections = append(sections, section)
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	if opts.IncludeMetaApps {
		section, warning := a.loadPublishedContentSectionV3(canonical, opts, "metaapps", publishedcontent.PathMetaApp, "metaapps section source unavailable")
		sections = append(sections, section)
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	if opts.IncludeChats {
		section, warning := a.loadChatsSectionV3(canonical)
		sections = append(sections, section)
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	if opts.IncludeBuzzes {
		section, warning := a.loadPublishedContentSectionV3(canonical, opts, "buzzes", publishedcontent.PathSimpleBuzz, "buzzes section source unavailable")
		sections = append(sections, section)
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}

	return sections, warnings
}

func emptySectionV3(id, protocolPath string) SectionV3 {
	return SectionV3{
		ID:           id,
		ProtocolPath: protocolPath,
		Page: SectionPageV3{
			Limit:   homepageV3SectionLimit,
			Count:   0,
			HasMore: false,
		},
		Items: []SectionItemV3{},
	}
}

func sectionWithItemsV3(id, protocolPath string, items []SectionItemV3, hasMore bool) SectionV3 {
	if len(items) > homepageV3SectionLimit {
		hasMore = true
		items = items[:homepageV3SectionLimit]
	}
	return SectionV3{
		ID:           id,
		ProtocolPath: protocolPath,
		Page: SectionPageV3{
			Limit:   homepageV3SectionLimit,
			Count:   len(items),
			HasMore: hasMore,
		},
		Items: items,
	}
}

func (a *Aggregator) loadServicesSectionV3(canonical CanonicalIdentity, opts Options) (SectionV3, string) {
	if a == nil || a.homepageServiceLister == nil {
		return emptySectionV3("services", skillservice.PathSkillService), ""
	}

	result, err := a.homepageServiceLister.ListHomepageByProvider(skillservice.HomepageListParams{
		ProviderGlobalMetaId: canonical.GlobalMetaId,
		Size:                 homepageV3SectionReadSize,
		IncludeInactive:      opts.IncludeInactiveServices,
	})
	if err != nil {
		return emptySectionV3("services", skillservice.PathSkillService), "services section source unavailable"
	}
	if result == nil || len(result.List) == 0 {
		return emptySectionV3("services", skillservice.PathSkillService), ""
	}

	items := make([]SectionItemV3, 0, len(result.List))
	for _, item := range result.List {
		items = append(items, sectionItemFromHomepageServiceV3(item))
	}

	return sectionWithItemsV3("services", skillservice.PathSkillService, items, result.HasMore), ""
}

func (a *Aggregator) loadChatsSectionV3(canonical CanonicalIdentity) (SectionV3, string) {
	if a == nil || a.chatInteractionLister == nil {
		return emptySectionV3("chats", privatechat.HomepageSimpleMsgProtocolPath), ""
	}

	result, err := a.chatInteractionLister.ListOutgoingHomepageInteractions(privatechat.HomepageInteractionListParams{
		GlobalMetaId: canonical.GlobalMetaId,
		MetaId:       canonical.MetaId,
		Address:      canonical.Address,
		Size:         homepageV3SectionReadSize,
	})
	if err != nil {
		return emptySectionV3("chats", privatechat.HomepageSimpleMsgProtocolPath), "chats section source unavailable"
	}
	if result == nil || len(result.Items) == 0 {
		return emptySectionV3("chats", privatechat.HomepageSimpleMsgProtocolPath), ""
	}

	items := make([]SectionItemV3, 0, len(result.Items))
	interactWithProfiles := make(map[string]*InteractWithV3)
	for _, item := range result.Items {
		pinID := strings.TrimSpace(item.PinId)
		interactWith := strings.TrimSpace(item.InteractWith)
		if pinID == "" || interactWith == "" {
			continue
		}
		items = append(items, SectionItemV3{
			PinId:        pinID,
			ProtocolPath: privatechat.HomepageSimpleMsgProtocolPath,
			Timestamp:    item.Timestamp,
			Data: SectionItemDataV3{
				InteractWith: a.resolveChatInteractWithV3(interactWith, interactWithProfiles),
			},
		})
	}
	if len(items) == 0 {
		return emptySectionV3("chats", privatechat.HomepageSimpleMsgProtocolPath), ""
	}

	return sectionWithItemsV3("chats", privatechat.HomepageSimpleMsgProtocolPath, items, result.HasMore), ""
}

func (a *Aggregator) resolveChatInteractWithV3(globalMetaId string, cache map[string]*InteractWithV3) *InteractWithV3 {
	globalMetaId = strings.TrimSpace(globalMetaId)
	if globalMetaId == "" {
		return nil
	}
	cacheKey := strings.ToLower(globalMetaId)
	if cached := cache[cacheKey]; cached != nil {
		return cached
	}

	out := &InteractWithV3{GlobalMetaId: globalMetaId}
	if profile := a.lookupLocalChatPeerProfileV3(globalMetaId); profile != nil {
		out.GlobalMetaId = firstNonEmpty(profile.GlobalMetaId, globalMetaId)
		out.Name = strings.TrimSpace(profile.Name)
		out.AvatarId = strings.TrimSpace(profile.AvatarId)
	}
	cache[cacheKey] = out
	return out
}

func (a *Aggregator) lookupLocalChatPeerProfileV3(globalMetaId string) *ProfileSnapshot {
	if a == nil || a.profileLookup == nil {
		return nil
	}
	if localLookup, ok := a.profileLookup.(localIdentityProfileLookup); ok {
		profile, err := localLookup.LookupLocalByIdentity(globalMetaId)
		if err != nil {
			return nil
		}
		return profile
	}
	if localLookup, ok := a.profileLookup.(LocalProfileLookup); ok {
		profile, err := localLookup.LookupLocalByGlobalMetaId(globalMetaId)
		if err != nil {
			return nil
		}
		return profile
	}
	profile, err := a.profileLookup.LookupByGlobalMetaId(globalMetaId)
	if err != nil {
		return nil
	}
	return profile
}

func sectionItemFromHomepageServiceV3(item skillservice.ServiceListItem) SectionItemV3 {
	return SectionItemV3{
		PinId:        stableServicePinIDV3(item),
		ProtocolPath: skillservice.PathSkillService,
		Timestamp:    item.UpdatedAt,
		Data: SectionItemDataV3{
			Payload: serviceDeclarationPayloadV3(item),
		},
	}
}

func serviceDeclarationPayloadV3(item skillservice.ServiceListItem) map[string]any {
	if len(item.DeclarationPayload) > 0 {
		return cloneStringAnyMap(item.DeclarationPayload)
	}
	if len(item.FallbackDeclarationPayload) > 0 {
		return cloneStringAnyMap(item.FallbackDeclarationPayload)
	}
	payload := make(map[string]any)
	addNonEmptyPayloadValue(payload, "serviceName", item.ServiceName)
	addNonEmptyPayloadValue(payload, "displayName", item.DisplayName)
	addNonEmptyPayloadValue(payload, "description", item.Description)
	addNonEmptyPayloadValue(payload, "serviceIcon", item.ServiceIcon)
	addNonEmptyPayloadValue(payload, "providerSkill", item.ProviderSkill)
	addNonEmptyPayloadValue(payload, "outputType", item.OutputType)
	addNonEmptyPayloadValue(payload, "price", item.Price)
	addNonEmptyPayloadValue(payload, "currency", item.Currency)
	addNonEmptyPayloadValue(payload, "paymentChain", item.PaymentChain)
	addNonEmptyPayloadValue(payload, "settlementKind", item.SettlementKind)
	addNonEmptyPayloadValue(payload, "paymentAddress", item.PaymentAddress)
	addNonNilPayloadValue(payload, "mrc20Ticker", item.MRC20Ticker)
	addNonNilPayloadValue(payload, "mrc20Id", item.MRC20Id)
	if item.Disabled {
		payload["disabled"] = true
	}
	return payload
}

func addNonEmptyPayloadValue(payload map[string]any, key, value string) {
	value = strings.TrimSpace(value)
	if value != "" {
		payload[key] = value
	}
}

func addNonNilPayloadValue(payload map[string]any, key string, value any) {
	if value != nil {
		payload[key] = value
	}
}

func (a *Aggregator) loadPublishedContentSectionV3(canonical CanonicalIdentity, opts Options, id, protocolPath, warning string) (SectionV3, string) {
	if a == nil || a.publishedContentLister == nil {
		return emptySectionV3(id, protocolPath), ""
	}

	contentOpts := opts
	contentOpts.ChainName = ""

	result, err := a.loadPublishedContentByCanonicalIdentity(canonical, contentOpts, protocolPath)
	if err != nil {
		return emptySectionV3(id, protocolPath), warning
	}
	if result == nil || len(result.Items) == 0 {
		return emptySectionV3(id, protocolPath), ""
	}

	items := make([]SectionItemV3, 0, len(result.Items))
	for _, item := range result.Items {
		payload, ok := sectionItemPayloadV3(item)
		if !ok {
			continue
		}
		items = append(items, SectionItemV3{
			PinId:        stablePublishedContentPinIDV3(item),
			ProtocolPath: item.ProtocolPath,
			Timestamp:    publishedContentItemSortTimestamp(item),
			Data: SectionItemDataV3{
				Payload: payload,
			},
		})
	}
	if len(items) == 0 {
		return emptySectionV3(id, protocolPath), ""
	}

	return sectionWithItemsV3(id, protocolPath, items, result.HasMore), ""
}

func sectionItemPayloadV3(item publishedcontent.SectionItem) (any, bool) {
	data := sectionItemData(item)
	if len(data) == 0 {
		return nil, false
	}
	payload, ok := data["payload"]
	if !ok {
		return nil, false
	}
	return payload, true
}

func stableServicePinIDV3(item skillservice.ServiceListItem) string {
	return firstNonEmpty(item.SourceServicePinId, item.CurrentPinId)
}

func stablePublishedContentPinIDV3(item publishedcontent.SectionItem) string {
	return firstNonEmpty(item.SourcePinId, item.CurrentPinId)
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
