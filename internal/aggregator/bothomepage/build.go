package bothomepage

import (
	"errors"
	"net/url"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
)

var (
	ErrInvalidParameter       = errors.New("invalid parameter")
	ErrNotFound               = errors.New("bot homepage not found")
	ErrAggregationUnavailable = errors.New("aggregation unavailable")
)

type ServiceLister interface {
	List(skillservice.ListParams) (*skillservice.ListResult, error)
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
	resolvedPresence := a.resolvePresence(*profile, opts.IncludePresence)
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
		SchemaVersion: "botHomepage.v1",
		ResolvedAt:    resolvedAt,
		GlobalMetaId:  requestGlobalMetaId,
		Canonical:     canonical,
		Profile:       outProfile,
		Homepage:      toDefaultHomepage(outProfile),
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
	out.Actions = buildActions(out.Profile.ChatPubkey, len(out.Services), canonical.GlobalMetaId)

	return out, nil
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

func toDefaultHomepage(profile Profile) Homepage {
	return Homepage{
		Mode:    "default",
		Title:   profile.Name,
		Summary: profile.Bio,
		Custom:  nil,
	}
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
	add("chatPubkey", "/info/chatpubkey", p.ChatPublicKeyId)

	if len(proofs.Profile) > 0 {
		proofs.VerificationState = "partial"
		return proofs, warnings
	}
	warnings = append(warnings, "profile proof metadata is unavailable")
	return proofs, warnings
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
