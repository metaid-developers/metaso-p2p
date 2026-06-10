package bothomepage

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
)

const (
	schemaVersionV1         = "botHomepage.v1"
	schemaVersionV2         = "botHomepage.v2"
	homepageSectionLimit    = 5
	homepageSectionReadSize = homepageSectionLimit + 1
)

var (
	ErrInvalidParameter       = errors.New("invalid parameter")
	ErrNotFound               = errors.New("bot homepage not found")
	ErrAggregationUnavailable = errors.New("aggregation unavailable")
)

type ServiceLister interface {
	List(skillservice.ListParams) (*skillservice.ListResult, error)
}

type HomepageServiceLister interface {
	ListHomepageByProvider(skillservice.HomepageListParams) (*skillservice.HomepageListResult, error)
}

type PublishedContentLister interface {
	List(publishedcontent.ListParams) (*publishedcontent.ListResult, error)
}

func (a *Aggregator) Build(requestGlobalMetaId string, opts Options) (*Data, error) {
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

	resolvedAt := a.currentTime()
	canonical := CanonicalIdentity{
		GlobalMetaId: firstNonEmpty(profile.GlobalMetaId, requestGlobalMetaId),
		MetaId:       strings.TrimSpace(profile.MetaId),
		Address:      strings.TrimSpace(profile.Address),
		ChainName:    strings.TrimSpace(profile.ChainName),
	}
	outProfile := a.toProfile(profile, canonical.GlobalMetaId)
	var persona *Persona
	if opts.Version == "v2" {
		persona = buildPersona(profile)
		outProfile.Bio = profileBioForVersion(profile, outProfile.Bio, opts.Version)
	}
	resolvedPresence := a.resolvePresence(*profile, opts.IncludePresence)
	homepage := toDefaultHomepage(outProfile, persona)
	if opts.Version == "v2" {
		homepage = toHomepage(outProfile, persona, profile)
	}
	services := make([]Service, 0)
	proofs := Proofs{
		VerificationState: "unverified",
		Identity:          nil,
		Profile:           make([]ProfileProof, 0),
		Homepage:          nil,
		Services:          make([]ServiceProof, 0),
	}
	warnings := make([]string, 0)
	if opts.IncludeProofs {
		proofs, warnings = buildProfileProofs(profile, canonical.GlobalMetaId)
	}

	out := &Data{
		SchemaVersion: schemaVersion(opts),
		ResolvedAt:    resolvedAt,
		GlobalMetaId:  requestGlobalMetaId,
		Canonical:     canonical,
		Profile:       outProfile,
		Persona:       persona,
		Homepage:      homepage,
		Presence:      resolvedPresence,
		Services:      services,
		Proofs:        proofs,
		Source:        a.source(resolvedAt),
		Warnings:      warnings,
	}
	if opts.IncludeServices {
		services, serviceProofs, warnings, err := a.loadServices(canonical.GlobalMetaId, opts, opts.IncludeProofs, out.Warnings)
		if err != nil {
			return nil, ErrAggregationUnavailable
		}
		out.Services = services
		out.Proofs.Services = serviceProofs
		if len(serviceProofs) > 0 && out.Proofs.VerificationState != "partial" {
			out.Proofs.VerificationState = "partial"
		}
		out.Warnings = warnings
	}
	if opts.Version == "v2" && opts.IncludeSections {
		out.Sections, out.Warnings = a.loadSections(canonical.GlobalMetaId, opts, out.Warnings)
	}
	out.Actions = buildActions(out.Profile.ChatPubkey, serviceActionCount(out), canonical.GlobalMetaId)

	return out, nil
}

func schemaVersion(opts Options) string {
	if opts.Version == "v2" {
		return schemaVersionV2
	}
	return schemaVersionV1
}

func (a *Aggregator) currentTime() int64 {
	if a == nil || a.now == nil {
		return 0
	}
	return a.now()
}

func (a *Aggregator) toProfile(p *ProfileSnapshot, canonicalGlobalMetaId string) Profile {
	if p == nil {
		return Profile{DisplayGlobalId: abbreviateGlobalMetaId(canonicalGlobalMetaId)}
	}
	return Profile{
		Name:            strings.TrimSpace(p.Name),
		Avatar:          a.resolveAsset(p.Avatar),
		AvatarPinId:     strings.TrimSpace(p.AvatarId),
		Background:      a.resolveAsset(p.Background),
		BackgroundPinId: strings.TrimSpace(p.BackgroundId),
		Bio:             strings.TrimSpace(p.Bio),
		BioPinId:        strings.TrimSpace(p.BioId),
		ChatPubkey:      strings.TrimSpace(p.ChatPublicKey),
		ChatPubkeyPinId: strings.TrimSpace(p.ChatPublicKeyId),
		NftAvatar:       a.resolveAsset(p.NftAvatar),
		DisplayGlobalId: abbreviateGlobalMetaId(canonicalGlobalMetaId),
	}
}

func toDefaultHomepage(profile Profile, persona *Persona) Homepage {
	return Homepage{
		Mode:    "default",
		Title:   profile.Name,
		Summary: homepageSummary(profile, persona),
		Custom:  nil,
	}
}

func toHomepage(profile Profile, persona *Persona, snapshot *ProfileSnapshot) Homepage {
	homepage := toDefaultHomepage(profile, persona)
	if snapshot == nil {
		return homepage
	}
	custom, ok := parseCustomHomepage(snapshot.Homepage, snapshot.HomepageId)
	if !ok {
		return homepage
	}
	homepage.Mode = "custom"
	homepage.Custom = custom
	return homepage
}

func parseCustomHomepage(raw, sourcePinID string) (*CustomHomepage, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}

	custom := CustomHomepage{
		PinId:        strings.TrimSpace(sourcePinID),
		ProtocolPath: "/info/homepage",
	}
	if startsJSONContainer(raw) {
		var decoded map[string]any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil, false
		}
		custom.URI = stringValue(decoded["uri"])
		custom.ContentType = stringValue(decoded["contentType"])
		custom.Renderer = stringValue(decoded["renderer"])
		custom.Txid = stringValue(decoded["txid"])
		custom.PinId = firstNonEmpty(custom.PinId, stringValue(decoded["pinId"]))
	} else {
		custom.URI = raw
	}

	if strings.TrimSpace(custom.URI) == "" {
		return nil, false
	}
	return &custom, true
}

func homepageSummary(profile Profile, persona *Persona) string {
	if strings.TrimSpace(profile.Bio) != "" {
		return strings.TrimSpace(profile.Bio)
	}
	if persona != nil && strings.TrimSpace(persona.Role) != "" {
		return strings.TrimSpace(persona.Role)
	}
	if persona != nil && strings.TrimSpace(persona.Goal) != "" {
		return strings.TrimSpace(persona.Goal)
	}
	return ""
}

func (a *Aggregator) source(fetchedAt int64) Source {
	baseURL := ""
	if a != nil {
		baseURL = a.assetBaseURL
	}
	return Source{
		Resolver:        "metaso-p2p",
		Node:            contentOrigin(baseURL),
		ProfileEndpoint: "/api/info/globalmetaid/:globalMetaId",
		ServiceEndpoint: "/api/bot-hub/skill-service/list",
		ContentBaseURL:  baseURL,
		FetchedAt:       fetchedAt,
		Stale:           false,
	}
}

func contentOrigin(baseURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func buildActions(chatPubkey string, serviceCount int, canonicalGlobalMetaId string) []Action {
	return []Action{
		{
			Id:                    "message",
			Label:                 "Message",
			Kind:                  "private-chat",
			Enabled:               strings.TrimSpace(chatPubkey) != "",
			RequiresUsingIdentity: true,
		},
		{
			Id:                    "services",
			Label:                 "Services",
			Kind:                  "service-list",
			Enabled:               serviceCount > 0,
			RequiresUsingIdentity: true,
		},
		{
			Id:                    "copy-uri",
			Label:                 "Copy URI",
			Kind:                  "copy",
			Enabled:               true,
			RequiresUsingIdentity: false,
			URI:                   "metaid://" + strings.TrimSpace(canonicalGlobalMetaId),
		},
	}
}

func buildProfileProofs(p *ProfileSnapshot, canonicalGlobalMetaId string) (Proofs, []string) {
	proofs := Proofs{
		VerificationState: "unverified",
		Identity:          nil,
		Profile:           make([]ProfileProof, 0),
		Homepage:          nil,
		Services:          make([]ServiceProof, 0),
	}
	warnings := make([]string, 0)
	if p == nil {
		warnings = append(warnings, "profile proof metadata is unavailable")
		return proofs, warnings
	}

	add := func(field, path, pinID string) {
		pinID = strings.TrimSpace(pinID)
		if pinID == "" {
			return
		}
		proofs.Profile = append(proofs.Profile, ProfileProof{
			Field:                 field,
			ProtocolPath:          path,
			PinId:                 pinID,
			PublisherGlobalMetaId: canonicalGlobalMetaId,
		})
		warnings = append(warnings, field+" proof txid/contentHash metadata is missing")
	}
	add("name", "/info/name", p.NameId)
	add("avatar", "/info/avatar", p.AvatarId)
	add("background", "/info/background", p.BackgroundId)
	add("bio", "/info/bio", p.BioId)
	add("role", "/info/role", p.RoleId)
	add("soul", "/info/soul", p.SoulId)
	add("goal", "/info/goal", p.GoalId)
	add("chatSkills", "/info/chatSkills", p.ChatSkillsId)
	add("llm", "/info/LLM", p.LLMId)
	add("chatPubkey", "/info/chatpubkey", p.ChatPublicKeyId)

	if len(proofs.Profile) > 0 {
		proofs.VerificationState = "partial"
		return proofs, warnings
	}
	warnings = append(warnings, "profile proof metadata is unavailable")
	return proofs, warnings
}

func buildPersona(p *ProfileSnapshot) *Persona {
	persona := &Persona{}
	if p == nil {
		return persona
	}

	if bioPersona, ok := personaFromLegacyBio(p.Bio); ok {
		*persona = bioPersona
	}
	if role := strings.TrimSpace(p.Role); role != "" {
		persona.Role = role
		persona.RolePinId = strings.TrimSpace(p.RoleId)
	}
	if soul := strings.TrimSpace(p.Soul); soul != "" {
		persona.Soul = soul
		persona.SoulPinId = strings.TrimSpace(p.SoulId)
	}
	if goal := strings.TrimSpace(p.Goal); goal != "" {
		persona.Goal = goal
		persona.GoalPinId = strings.TrimSpace(p.GoalId)
	}
	if chatSkills, ok := parseChatSkills(p.ChatSkills); ok {
		persona.ChatSkills = chatSkills
		persona.ChatSkills.PinId = strings.TrimSpace(p.ChatSkillsId)
	}
	if llm, ok := parseLLM(p.LLM); ok {
		persona.LLM = llm
		persona.LLM.PinId = strings.TrimSpace(p.LLMId)
	}
	return persona
}

func profileBioForVersion(p *ProfileSnapshot, bio, version string) string {
	if version != "v2" || p == nil {
		return bio
	}
	if _, ok := personaFromLegacyBio(p.Bio); ok {
		return ""
	}
	return bio
}

func personaFromLegacyBio(raw string) (Persona, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || !strings.HasPrefix(raw, "{") {
		return Persona{}, false
	}
	var legacy map[string]any
	if err := json.Unmarshal([]byte(raw), &legacy); err != nil {
		return Persona{}, false
	}
	if !hasLegacyPersonaKey(legacy) {
		return Persona{}, false
	}

	role := stringValue(legacy["role"])
	soul := stringValue(legacy["soul"])
	goal := stringValue(legacy["goal"])
	allow := firstNonEmptyStringSlice(
		stringSliceValue(legacy["allowChatSkills"]),
		stringSliceValue(legacy["chatSkills"]),
		stringSliceValue(legacy["skills"]),
		stringSliceValue(legacy["tools"]),
	)
	llm := llmValue(firstNonNil(legacy["llm"], legacy["LLM"]))

	return Persona{
		Role: role,
		Soul: soul,
		Goal: goal,
		ChatSkills: ChatSkills{
			Allow: allow,
		},
		LLM: llm,
	}, true
}

func parseChatSkills(raw string) (ChatSkills, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ChatSkills{}, false
	}
	if startsJSONContainer(raw) {
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return ChatSkills{}, false
		}
		return ChatSkills{Allow: stringSliceValue(decoded)}, true
	}
	return ChatSkills{Allow: []string{raw}}, true
}

func parseLLM(raw string) (LLM, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return LLM{}, false
	}
	if startsJSONContainer(raw) {
		var decoded any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			return LLM{}, false
		}
		return llmValue(decoded), true
	}
	return LLM{Provider: raw}, true
}

func startsJSONContainer(raw string) bool {
	return strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[")
}

func hasLegacyPersonaKey(values map[string]any) bool {
	for _, key := range []string{"role", "soul", "goal", "llm", "LLM", "allowChatSkills", "chatSkills", "skills", "tools"} {
		if _, ok := values[key]; ok {
			return true
		}
	}
	return false
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func stringSliceValue(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		return trimStringSlice([]string{typed})
	case []string:
		return trimStringSlice(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	case map[string]any:
		return firstNonEmptyStringSlice(
			stringSliceValue(typed["allowChatSkills"]),
			stringSliceValue(typed["allow"]),
			stringSliceValue(typed["chatSkills"]),
			stringSliceValue(typed["skills"]),
			stringSliceValue(typed["tools"]),
		)
	default:
		return nil
	}
}

func firstNonEmptyStringSlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return value
		}
	}
	return nil
}

func llmValue(value any) LLM {
	switch typed := value.(type) {
	case string:
		return LLM{Provider: strings.TrimSpace(typed)}
	case map[string]any:
		return LLM{
			Provider: firstNonEmpty(stringValue(typed["provider"]), stringValue(typed["primaryProvider"])),
			Model:    stringValue(typed["model"]),
			Name:     firstNonEmpty(stringValue(typed["name"]), stringValue(typed["displayName"])),
		}
	default:
		return LLM{}
	}
}

func trimStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func serviceActionCount(out *Data) int {
	if out == nil {
		return 0
	}
	if len(out.Services) > 0 {
		return len(out.Services)
	}
	for _, section := range out.Sections {
		if section.Id != "services" {
			continue
		}
		if section.Returned > 0 {
			return section.Returned
		}
		return len(section.Items)
	}
	return 0
}

func (a *Aggregator) loadSections(canonicalGlobalMetaId string, opts Options, warnings []string) ([]Section, []string) {
	sections := []Section{
		emptySection("services", "Services", "services"),
		emptySection("metaapps", "MetaAPPs", "metaapps"),
		emptySection("skills", "Skills", "skills"),
		emptySection("buzzes", "Buzzes", "buzzes"),
	}

	section, warning := a.loadServicesSection(canonicalGlobalMetaId, opts)
	sections[0] = section
	if warning != "" {
		warnings = append(warnings, warning)
	}

	publishedSpecs := []struct {
		index        int
		id           string
		title        string
		kind         string
		protocolPath string
		warning      string
	}{
		{index: 1, id: "metaapps", title: "MetaAPPs", kind: "metaapps", protocolPath: publishedcontent.PathMetaApp, warning: "metaapps section unavailable"},
		{index: 2, id: "skills", title: "Skills", kind: "skills", protocolPath: publishedcontent.PathMetaBotSkill, warning: "skills section unavailable"},
		{index: 3, id: "buzzes", title: "Buzzes", kind: "buzzes", protocolPath: publishedcontent.PathSimpleBuzz, warning: "buzzes section unavailable"},
	}
	for _, spec := range publishedSpecs {
		section, warning := a.loadPublishedContentSection(canonicalGlobalMetaId, opts, spec.id, spec.title, spec.kind, spec.protocolPath, spec.warning)
		sections[spec.index] = section
		if warning != "" {
			warnings = append(warnings, warning)
		}
	}
	return sections, warnings
}

func emptySection(id, title, kind string) Section {
	return Section{
		Id:       id,
		Title:    title,
		Kind:     kind,
		Items:    []SectionItem{},
		Limit:    homepageSectionLimit,
		Returned: 0,
		HasMore:  false,
		More:     MoreLink{Label: "More", Enabled: false},
	}
}

func sectionWithItems(id, title, kind string, items []SectionItem, hasMore bool) Section {
	if len(items) > homepageSectionLimit {
		hasMore = true
		items = items[:homepageSectionLimit]
	}
	return Section{
		Id:       id,
		Title:    title,
		Kind:     kind,
		Items:    items,
		Limit:    homepageSectionLimit,
		Returned: len(items),
		HasMore:  hasMore,
		More:     MoreLink{Label: "More", Enabled: false},
	}
}

func (a *Aggregator) loadServicesSection(canonicalGlobalMetaId string, opts Options) (Section, string) {
	if a == nil || a.homepageServiceLister == nil {
		return emptySection("services", "Services", "services"), ""
	}
	result, err := a.homepageServiceLister.ListHomepageByProvider(skillservice.HomepageListParams{
		ProviderGlobalMetaId: canonicalGlobalMetaId,
		ChainName:            opts.ChainName,
		Size:                 homepageSectionReadSize,
		IncludeInactive:      opts.IncludeInactiveServices,
	})
	if err != nil {
		return emptySection("services", "Services", "services"), "services section unavailable"
	}
	if result == nil || len(result.List) == 0 {
		return emptySection("services", "Services", "services"), ""
	}
	items := make([]SectionItem, 0, len(result.List))
	for _, item := range result.List {
		service := serviceFromListItem(item, nil)
		items = append(items, sectionItemFromService(service))
	}
	return sectionWithItems("services", "Services", "services", items, result.HasMore), ""
}

func (a *Aggregator) loadPublishedContentSection(canonicalGlobalMetaId string, opts Options, id, title, kind, protocolPath, warningText string) (Section, string) {
	if a == nil || a.publishedContentLister == nil {
		return emptySection(id, title, kind), ""
	}
	result, err := a.publishedContentLister.List(publishedcontent.ListParams{
		ProtocolPath:          protocolPath,
		PublisherGlobalMetaId: canonicalGlobalMetaId,
		ChainName:             opts.ChainName,
		Size:                  homepageSectionReadSize,
	})
	if err != nil {
		return emptySection(id, title, kind), warningText
	}
	if result == nil || len(result.Items) == 0 {
		return emptySection(id, title, kind), ""
	}
	items := make([]SectionItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, sectionItemFromPublishedContent(item))
	}
	return sectionWithItems(id, title, kind, items, result.HasMore), ""
}

func sectionItemFromService(service Service) SectionItem {
	return SectionItem{
		Id:           service.Id,
		SourcePinId:  service.SourceServicePinId,
		CurrentPinId: service.CurrentPinId,
		ChainName:    service.ChainName,
		ProtocolPath: skillservice.PathSkillService,
		Title:        firstNonEmpty(service.DisplayName, service.ServiceName),
		Description:  service.Description,
		CreatedAt:    service.CreatedAt,
		UpdatedAt:    service.UpdatedAt,
		Service:      &service,
	}
}

func sectionItemFromPublishedContent(item publishedcontent.SectionItem) SectionItem {
	return SectionItem{
		Id:             firstNonEmpty(item.CurrentPinId, item.SourcePinId),
		SourcePinId:    item.SourcePinId,
		CurrentPinId:   item.CurrentPinId,
		ChainName:      item.ChainName,
		ProtocolPath:   item.ProtocolPath,
		Title:          sectionItemTitle(item),
		Description:    sectionItemDescription(item),
		ContentType:    item.ContentType,
		PayloadText:    item.PayloadText,
		PayloadJSON:    item.PayloadJSON,
		PayloadExposed: item.PayloadExposed,
		IsMempool:      item.IsMempool,
		CreatedAt:      item.CreatedAt,
		UpdatedAt:      item.UpdatedAt,
	}
}

func sectionItemTitle(item publishedcontent.SectionItem) string {
	for _, key := range []string{"title", "name", "displayName"} {
		if value, ok := item.PayloadJSON[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(item.PayloadText)
}

func sectionItemDescription(item publishedcontent.SectionItem) string {
	for _, key := range []string{"description", "summary"} {
		if value, ok := item.PayloadJSON[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (a *Aggregator) loadServices(canonicalGlobalMetaId string, opts Options, includeProofs bool, warnings []string) ([]Service, []ServiceProof, []string, error) {
	services := make([]Service, 0)
	proofs := make([]ServiceProof, 0)
	if a == nil || a.serviceLister == nil {
		return services, proofs, warnings, nil
	}

	result, err := a.serviceLister.List(skillservice.ListParams{
		Size:                 opts.ServiceSize,
		ProviderGlobalMetaId: canonicalGlobalMetaId,
		ChainName:            opts.ChainName,
		SortBy:               "updated",
		Order:                "desc",
		IncludeInactive:      opts.IncludeInactiveServices,
	})
	if err != nil {
		return nil, nil, warnings, err
	}
	if result == nil || len(result.List) == 0 {
		return services, proofs, warnings, nil
	}

	for _, item := range result.List {
		var proof *ServiceProof
		if includeProofs {
			serviceProof := ServiceProof{
				ServiceId:             item.Id,
				PinId:                 item.CurrentPinId,
				SourceServicePinId:    item.SourceServicePinId,
				ProtocolPath:          skillservice.PathSkillService,
				PublisherGlobalMetaId: canonicalGlobalMetaId,
			}
			proof = &serviceProof
			proofs = append(proofs, serviceProof)
			warnings = append(warnings, "service proof for "+item.Id+" is missing txid/contentHash metadata")
		}
		services = append(services, serviceFromListItem(item, proof))
	}

	return services, proofs, warnings, nil
}

func serviceFromListItem(item skillservice.ServiceListItem, proof *ServiceProof) Service {
	return Service{
		Id:                 item.Id,
		CurrentPinId:       item.CurrentPinId,
		SourceServicePinId: item.SourceServicePinId,
		DisplayName:        item.DisplayName,
		ServiceName:        item.ServiceName,
		Description:        item.Description,
		ServiceIcon:        item.ServiceIcon,
		ProviderSkill:      item.ProviderSkill,
		OutputType:         item.OutputType,
		Price:              item.Price,
		Currency:           item.Currency,
		SettlementKind:     item.SettlementKind,
		PaymentChain:       item.PaymentChain,
		MRC20Ticker:        item.MRC20Ticker,
		MRC20Id:            item.MRC20Id,
		PaymentAddress:     item.PaymentAddress,
		RatingAvg:          item.RatingAvg,
		RatingCount:        item.RatingCount,
		Status:             item.Status,
		Operation:          item.Operation,
		Disabled:           item.Disabled,
		ChainName:          item.ChainName,
		CreatedAt:          item.CreatedAt,
		UpdatedAt:          item.UpdatedAt,
		Proof:              proof,
	}
}

func (a *Aggregator) resolveAsset(asset string) string {
	if a == nil || a.assetResolver == nil {
		return strings.TrimSpace(asset)
	}
	return a.assetResolver.Resolve(asset)
}

func unknownPresence() Presence {
	return Presence{State: "unknown", UpdatedAt: nil, Source: ""}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func abbreviateGlobalMetaId(globalMetaId string) string {
	globalMetaId = strings.TrimSpace(globalMetaId)
	if len(globalMetaId) <= 16 {
		return globalMetaId
	}
	return globalMetaId[:8] + "..." + globalMetaId[len(globalMetaId)-6:]
}
