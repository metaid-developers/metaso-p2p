// Package skillservice indexes /protocols/skill-service and
// /protocols/skill-service-rate PINs and serves the Bot Hub aggregation API
// (`/api/bot-hub/skill-service/*`).
//
// See docs/specs/2026-05-28-bot-hub-skill-service-aggregation-api.md for the
// full v1 contract. Key invariants enforced here:
//   - Same-chain version chain only: create / modify / revoke must share a
//     chainName. Cross-chain target references are intentionally NOT folded.
//   - sourceServicePinId is the canonical aggregation key; modify / revoke
//     PINs may point at the current version pin via `@pinId`, and the
//     aggregator maps that version back to the source create PIN.
//   - Declarative outputs only: the indexer stores chain-declared fields
//     (status, disabled, operation) verbatim and never synthesises action
//     verdicts like canOrder / available.
package skillservice

// PathSkillService is the MetaID protocol path for skill-service publish PINs.
const PathSkillService = "/protocols/skill-service"

// PathSkillServiceRate is the MetaID protocol path for skill-service rating PINs.
const PathSkillServiceRate = "/protocols/skill-service-rate"

// Pebble namespaces used by the skill-service aggregator. They are scoped to
// this module so they cannot collide with userinfo / groupchat / privatechat.
const (
	NamespaceService = "skillservice"
	NamespaceRate    = "skillservice_rate"
)

// ServiceContentSummary mirrors the `contentSummary` JSON payload published
// inside `/protocols/skill-service` PINs. Field names match the protocol
// specification, not the API response. The aggregator translates this into
// a ServiceRecord (chain metadata + parsed summary) before persisting.
type ServiceContentSummary struct {
	ServiceName     string `json:"serviceName"`
	DisplayName     string `json:"displayName"`
	Description     string `json:"description"`
	ServiceIcon     string `json:"serviceIcon"`
	ProviderMetaBot string `json:"providerMetaBot"`
	ProviderSkill   string `json:"providerSkill"`
	Price           string `json:"price"`
	Currency        string `json:"currency"`
	PaymentChain    string `json:"paymentChain"`
	SettlementKind  string `json:"settlementKind"`
	MRC20Ticker     string `json:"mrc20Ticker"`
	MRC20Id         string `json:"mrc20Id"`
	OutputType      string `json:"outputType"`
	PaymentAddress  string `json:"paymentAddress"`
	Disabled        bool   `json:"disabled"`
}

// RatingContentSummary mirrors the `contentSummary` JSON payload published
// inside `/protocols/skill-service-rate` PINs.
type RatingContentSummary struct {
	// ServiceID is the rating target. The spec requires this to be the
	// sourceServicePinId of the rated service, but historically clients have
	// also written the currentPinId or an arbitrary version pinId; the
	// aggregator must normalise during ingestion.
	ServiceID     string `json:"serviceID"`
	ServicePaidTx string `json:"servicePaidTx"`
	Rate          int    `json:"rate"`
	Comment       string `json:"comment"`
}

// ServiceRecord is the persisted aggregator-side view of a skill service.
// One ServiceRecord corresponds to one logical service (one source create
// PIN); modify / revoke PINs collapse into this record by updating its
// fields. Cross-chain folding is rejected: a record always lives on a
// single chainName.
type ServiceRecord struct {
	// Identity
	SourceServicePinId   string `json:"sourceServicePinId"`
	CurrentPinId         string `json:"currentPinId"`
	CurrentGenesisHeight int64  `json:"currentGenesisHeight,omitempty"`
	ChainName            string `json:"chainName"`

	// Provider chain identity (from PIN metadata, not contentSummary)
	ProviderMetaId       string `json:"providerMetaId"`
	ProviderGlobalMetaId string `json:"providerGlobalMetaId"`
	ProviderAddress      string `json:"providerAddress"`

	// Summary fields (from contentSummary)
	DeclarationPayload map[string]any `json:"declarationPayload,omitempty"`
	ServiceName        string         `json:"serviceName"`
	DisplayName        string         `json:"displayName"`
	Description        string         `json:"description"`
	ServiceIcon        string         `json:"serviceIcon"`
	ProviderMetaBot    string         `json:"providerMetaBot,omitempty"`
	ProviderSkill      string         `json:"providerSkill"`
	Price              string         `json:"price"`
	Currency           string         `json:"currency"`
	PaymentChain       string         `json:"paymentChain"`
	SettlementKind     string         `json:"settlementKind"`
	MRC20Ticker        string         `json:"mrc20Ticker,omitempty"`
	MRC20Id            string         `json:"mrc20Id,omitempty"`
	OutputType         string         `json:"outputType"`
	PaymentAddress     string         `json:"paymentAddress"`

	// Lifecycle state (chain-declared, not aggregator-decided)
	Status    int    `json:"status"`
	Operation string `json:"operation"`
	Disabled  bool   `json:"disabled"`

	// Timestamps (milliseconds)
	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
}

// IsVisibleDefault reports whether the record passes the default visibility
// filter described in the spec (latest != revoke, disabled != true,
// status in {0, 1}). It does NOT take action-permission decisions; it only
// applies the chain-declared visibility filter that the list endpoint uses
// when `includeInactive=0`.
func (r *ServiceRecord) IsVisibleDefault() bool {
	if r == nil {
		return false
	}
	if r.Operation == OperationRevoke {
		return false
	}
	if r.Disabled {
		return false
	}
	if r.Status != StatusConfirmed && r.Status != StatusPending {
		return false
	}
	return true
}

// Operation values that the aggregator understands. Anything outside this
// set causes the PIN to be skipped (logged as a no-op).
const (
	OperationCreate = "create"
	OperationModify = "modify"
	OperationRevoke = "revoke"
)

// Status values that the aggregator understands as "visible by default".
// Other values are treated as anomalous and only surface when the caller
// explicitly opts in via includeInactive=1.
const (
	StatusConfirmed = 0
	StatusPending   = 1
)
