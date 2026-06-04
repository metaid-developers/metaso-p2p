package skillservice

import (
	"errors"
	"fmt"
	"strings"
)

// DetailParams is the normalised lookup input for the detail endpoint.
type DetailParams struct {
	ServiceID string
	ChainName string // optional; disambiguates multi-chain collisions
	IDType    string // auto | currentPinId | sourceServicePinId
}

// ServiceDetail is the `service` block in the detail response. It mirrors
// the list item's core service fields but omits embedded provider profile
// and rating aggregates (v1 detail does not surface ratings).
type ServiceDetail struct {
	Id                 string `json:"id"`
	CurrentPinId       string `json:"currentPinId"`
	SourceServicePinId string `json:"sourceServicePinId"`

	ServiceName   string `json:"serviceName"`
	DisplayName   string `json:"displayName"`
	Description   string `json:"description"`
	ServiceIcon   string `json:"serviceIcon"`
	ProviderSkill string `json:"providerSkill"`
	OutputType    string `json:"outputType"`

	Price          string `json:"price"`
	Currency       string `json:"currency"`
	SettlementKind string `json:"settlementKind"`
	PaymentChain   string `json:"paymentChain"`
	MRC20Ticker    any    `json:"mrc20Ticker"`
	MRC20Id        any    `json:"mrc20Id"`
	PaymentAddress string `json:"paymentAddress"`

	Status    int    `json:"status"`
	Operation string `json:"operation"`
	Disabled  bool   `json:"disabled"`
	ChainName string `json:"chainName"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// DetailProvider is the `provider` block in the detail response.
type DetailProvider struct {
	MetaId       string  `json:"metaid"`
	GlobalMetaId string  `json:"globalMetaId"`
	Address      string  `json:"address"`
	Name         string  `json:"name"`
	Avatar       string  `json:"avatar"`
	AvatarId     string  `json:"avatarId,omitempty"`
	ChatPubkey   *string `json:"chatPubkey,omitempty"`
}

// DetailResult is the full `data` block of the detail response.
type DetailResult struct {
	Service       ServiceDetail  `json:"service"`
	Provider      DetailProvider `json:"provider"`
	AggregatedAt  int64          `json:"aggregatedAt"`
	SchemaVersion string         `json:"schemaVersion"`
}

var (
	errInvalidIDType   = errors.New("invalid idType")
	errAmbiguousLookup = errors.New("ambiguous serviceId")
)

// FindService resolves a service record by serviceId. When chainName is set
// the lookup is scoped to that chain; otherwise all chains are scanned and
// multiple matches return errAmbiguousLookup.
func (a *Aggregator) FindService(serviceID, chainName, idType string) (*ServiceRecord, error) {
	serviceID = strings.TrimSpace(serviceID)
	if serviceID == "" {
		return nil, nil
	}
	chainName = strings.ToLower(strings.TrimSpace(chainName))
	idType = normaliseIDType(idType)

	if chainName != "" {
		return a.findServiceOnChain(chainName, serviceID, idType)
	}

	records, err := a.listAllServices()
	if err != nil {
		return nil, err
	}
	var matches []*ServiceRecord
	for _, rec := range records {
		if recordMatchesID(rec, serviceID, idType) {
			matches = append(matches, rec)
		}
	}
	switch len(matches) {
	case 0:
		return nil, nil
	case 1:
		return matches[0], nil
	default:
		return nil, errAmbiguousLookup
	}
}

func (a *Aggregator) findServiceOnChain(chainName, serviceID, idType string) (*ServiceRecord, error) {
	switch idType {
	case "sourceServicePinId":
		return a.loadService(chainName, serviceID)
	case "currentPinId":
		return a.loadServiceByAnyPinId(chainName, serviceID)
	default: // auto
		if rec, err := a.loadService(chainName, serviceID); err != nil {
			return nil, err
		} else if rec != nil {
			return rec, nil
		}
		return a.loadServiceByAnyPinId(chainName, serviceID)
	}
}

func normaliseIDType(idType string) string {
	switch strings.ToLower(strings.TrimSpace(idType)) {
	case "", "auto":
		return "auto"
	case "currentpinid":
		return "currentPinId"
	case "sourceservicepinid":
		return "sourceServicePinId"
	default:
		return "invalid"
	}
}

func recordMatchesID(rec *ServiceRecord, serviceID, idType string) bool {
	if rec == nil {
		return false
	}
	switch idType {
	case "sourceServicePinId":
		return rec.SourceServicePinId == serviceID
	case "currentPinId":
		return rec.CurrentPinId == serviceID
	default:
		return rec.SourceServicePinId == serviceID || rec.CurrentPinId == serviceID
	}
}

// Detail builds the wire-level detail response for a resolved service.
func (a *Aggregator) Detail(p DetailParams) (*DetailResult, error) {
	idType := normaliseIDType(p.IDType)
	if idType == "invalid" {
		return nil, errInvalidIDType
	}

	rec, err := a.FindService(p.ServiceID, p.ChainName, idType)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, nil
	}

	profile := a.ResolveProvider(rec)
	return &DetailResult{
		Service:       a.toServiceDetail(rec),
		Provider:      a.toDetailProvider(rec, profile),
		AggregatedAt:  now(),
		SchemaVersion: "botHubSkillServiceDetail.v1",
	}, nil
}

func (a *Aggregator) toServiceDetail(rec *ServiceRecord) ServiceDetail {
	payment := normalisePaymentMetadata(rec)

	return ServiceDetail{
		Id:                 rec.CurrentPinId,
		CurrentPinId:       rec.CurrentPinId,
		SourceServicePinId: rec.SourceServicePinId,

		ServiceName:   rec.ServiceName,
		DisplayName:   rec.DisplayName,
		Description:   rec.Description,
		ServiceIcon:   a.ResolveAsset(rec.ServiceIcon),
		ProviderSkill: rec.ProviderSkill,
		OutputType:    rec.OutputType,

		Price:          rec.Price,
		Currency:       payment.currency,
		SettlementKind: payment.settlementKind,
		PaymentChain:   payment.paymentChain,
		MRC20Ticker:    payment.mrc20Ticker,
		MRC20Id:        payment.mrc20Id,
		PaymentAddress: payment.paymentAddress,

		Status:    rec.Status,
		Operation: rec.Operation,
		Disabled:  rec.Disabled,
		ChainName: rec.ChainName,
		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,
	}
}

func (a *Aggregator) toDetailProvider(rec *ServiceRecord, profile ProfileSnapshot) DetailProvider {
	out := DetailProvider{
		MetaId:       firstNonEmpty(profile.MetaId, rec.ProviderMetaId),
		GlobalMetaId: firstNonEmpty(profile.GlobalMetaId, rec.ProviderGlobalMetaId),
		Address:      firstNonEmpty(profile.Address, rec.ProviderAddress),
		Name:         profile.Name,
		Avatar:       a.ResolveAsset(profile.Avatar),
		AvatarId:     profile.AvatarId,
	}
	if pk := strings.TrimSpace(profile.ChatPublicKey); pk != "" {
		out.ChatPubkey = &pk
	}
	return out
}

// detailErr wraps lookup failures with stable messages for the HTTP layer.
func detailErr(err error) string {
	if errors.Is(err, errInvalidIDType) {
		return "invalid idType"
	}
	if errors.Is(err, errAmbiguousLookup) {
		return fmt.Sprintf("ambiguous serviceId: %v", err)
	}
	return "aggregation unavailable"
}
