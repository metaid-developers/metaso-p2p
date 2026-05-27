package skillservice

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/metaid-developers/meta-socket/internal/aggregator"
)

// processPin is the core dispatch entry. It accepts both block (confirmed)
// and mempool pins and updates the persisted service view accordingly.
//
// Routing rules:
//   - path == /protocols/skill-service     → dispatch by operation
//   - path == /protocols/skill-service-rate → handled in rating.go (M3)
//   - anything else                          → ignored
//
// processPin never produces a NotifyEvent: skill-service updates do not need
// socket push in v1. The Bot Hub UI polls the list/detail endpoints, and the
// 30s p95 fresh-view target is already met by the scan loop cadence.
func (a *Aggregator) processPin(pin *aggregator.PinInscription) error {
	if pin == nil || pin.Path == "" {
		return nil
	}
	// MetaID modify / revoke PINs commonly publish the path as
	// `<base-path>@<originalPinId>`. Strip the `@…` suffix before routing
	// so the legacy fallback path (relied on by resolveOriginalId when
	// OriginalId is missing) reaches the correct handler. Anything to the
	// right of `@` is still inspected later for the source pin id; here
	// we only need the protocol identifier.
	base := strings.ToLower(strings.TrimRight(pin.Path, "/"))
	if at := strings.Index(base, "@"); at >= 0 {
		base = base[:at]
	}
	switch base {
	case PathSkillService:
		return a.processServicePin(pin)
	case PathSkillServiceRate:
		return a.processRatingPin(pin)
	default:
		return nil
	}
}

// processServicePin handles a single /protocols/skill-service PIN. It folds
// create / modify / revoke into a single ServiceRecord keyed by
// (chainName, sourceServicePinId).
//
// Same-chain invariant: if a modify or revoke pin's originalId resolves to a
// create pin on a different chainName, the pin is rejected as a compatibility
// fallback (logged, not stored). Cross-chain version chains are intentionally
// out of scope for v1; see docs/specs/2026-05-28-bot-hub-skill-service-...
func (a *Aggregator) processServicePin(pin *aggregator.PinInscription) error {
	if pin.ChainName == "" || pin.Id == "" {
		return nil
	}

	op := strings.ToLower(strings.TrimSpace(pin.Operation))
	switch op {
	case OperationCreate:
		return a.processServiceCreate(pin)
	case OperationModify:
		return a.processServiceModify(pin)
	case OperationRevoke:
		return a.processServiceRevoke(pin)
	default:
		// Unknown operation; ignored.
		return nil
	}
}

// processServiceCreate inserts a brand-new service record. The
// sourceServicePinId is the pin's own id. If a record with the same source
// id already exists on the same chain we treat the older one as canonical
// and ignore the duplicate create — protocol-level dedup is the publisher's
// responsibility, not the aggregator's.
func (a *Aggregator) processServiceCreate(pin *aggregator.PinInscription) error {
	summary, ok := decodeServiceSummary(pin)
	if !ok {
		// contentSummary missing or malformed; cannot build a record.
		return nil
	}

	existing, err := a.loadService(pin.ChainName, pin.Id)
	if err != nil {
		return err
	}
	if existing != nil {
		// First create wins; duplicate creates are no-ops.
		return nil
	}

	rec := newServiceRecord(pin, summary, pin.Id /* sourcePinId == own id */)
	if err := a.saveService(rec, nil); err != nil {
		return err
	}
	// A create's pin id IS the source; the pin_to_source index is only
	// needed for modify / revoke ids that differ from the source.
	return nil
}

// processServiceModify resolves the source pin via originalId (one hop),
// rejects cross-chain references, and overwrites the current ServiceRecord
// while keeping CreatedAt stable.
func (a *Aggregator) processServiceModify(pin *aggregator.PinInscription) error {
	sourcePinId, ok := resolveOriginalId(pin)
	if !ok {
		return nil
	}

	previous, err := a.loadService(pin.ChainName, sourcePinId)
	if err != nil {
		return err
	}
	if previous == nil {
		// No source on this chain to fold into; could be cross-chain
		// originalId or simply out-of-order delivery. Drop the modify
		// silently — the indexer engine does not replay later, so out-of-
		// order is expected to be rare; log it so we can tell apart cross-
		// chain pollution.
		log.Printf("[skillservice] modify pin %s: source %s/%s not found (cross-chain or out-of-order); skipped",
			pin.Id, pin.ChainName, sourcePinId)
		return nil
	}

	summary, ok := decodeServiceSummary(pin)
	if !ok {
		return nil
	}

	updated := newServiceRecord(pin, summary, sourcePinId)
	// Preserve the original CreatedAt; modifies only move UpdatedAt forward.
	updated.CreatedAt = previous.CreatedAt
	// Modify of an already-revoked service: keep operation=revoke (revoke
	// is terminal in v1). The chain-declared status can still update.
	if previous.Operation == OperationRevoke {
		updated.Operation = OperationRevoke
	}

	if err := a.saveService(updated, previous); err != nil {
		return err
	}
	return a.mapPinToSource(pin.ChainName, pin.Id, sourcePinId)
}

// processServiceRevoke flips the persisted record's Operation to "revoke"
// and stamps UpdatedAt. The summary fields stay (so the detail endpoint can
// still return the last known declaration if the product chooses to surface
// revoked services).
func (a *Aggregator) processServiceRevoke(pin *aggregator.PinInscription) error {
	sourcePinId, ok := resolveOriginalId(pin)
	if !ok {
		return nil
	}

	previous, err := a.loadService(pin.ChainName, sourcePinId)
	if err != nil {
		return err
	}
	if previous == nil {
		log.Printf("[skillservice] revoke pin %s: source %s/%s not found (cross-chain or out-of-order); skipped",
			pin.Id, pin.ChainName, sourcePinId)
		return nil
	}

	updated := *previous // shallow copy
	updated.Operation = OperationRevoke
	updated.CurrentPinId = pin.Id
	if pin.Timestamp > 0 {
		updated.UpdatedAt = pin.Timestamp
	}

	if err := a.saveService(&updated, previous); err != nil {
		return err
	}
	return a.mapPinToSource(pin.ChainName, pin.Id, sourcePinId)
}

// --- helpers -----------------------------------------------------------------

// resolveOriginalId picks the canonical source pin id for a modify/revoke.
// It prefers PinInscription.OriginalId (the metaid protocol's standard
// one-hop reference). When OriginalId is missing we fall back to extracting
// an @pinId from pin.Path; the fallback is bounded (one hop only — we do
// NOT recursively resolve "previous version" pin chains) and is logged so
// upstream telemetry can spot stragglers from the legacy publish path.
func resolveOriginalId(pin *aggregator.PinInscription) (string, bool) {
	if pin.OriginalId != "" {
		return pin.OriginalId, true
	}

	// Compatibility fallback: a few legacy publishers encode the original
	// pin id as `@<pinId>` inside the path. Extract that, but only once;
	// chain-resolution loops are not supported in v1.
	if at := strings.Index(pin.Path, "@"); at >= 0 {
		candidate := strings.TrimSpace(pin.Path[at+1:])
		if candidate != "" && candidate != pin.Id {
			log.Printf("[skillservice] originalId compatibility fallback for pin %s: extracted %s from path", pin.Id, candidate)
			return candidate, true
		}
	}
	log.Printf("[skillservice] modify/revoke pin %s missing originalId and no @pinId in path; skipped", pin.Id)
	return "", false
}

// decodeServiceSummary unmarshals the JSON content of a skill-service PIN
// into a ServiceContentSummary. Returns ok=false when the body is missing
// or malformed; the caller is expected to skip the pin in that case rather
// than emit a half-filled record.
func decodeServiceSummary(pin *aggregator.PinInscription) (ServiceContentSummary, bool) {
	var s ServiceContentSummary
	if len(pin.ContentBody) == 0 {
		return s, false
	}
	if err := json.Unmarshal(pin.ContentBody, &s); err != nil {
		return s, false
	}
	// ServiceName / DisplayName must be present; without them the card has
	// nothing to render. Other fields can be empty (e.g. free service).
	if strings.TrimSpace(s.ServiceName) == "" && strings.TrimSpace(s.DisplayName) == "" {
		return s, false
	}
	return s, true
}

// newServiceRecord builds a ServiceRecord from a fresh skill-service PIN
// plus its parsed contentSummary. CreatedAt and UpdatedAt are seeded from
// pin.Timestamp; later modifies/revokes update them via processServiceModify
// / processServiceRevoke.
func newServiceRecord(pin *aggregator.PinInscription, summary ServiceContentSummary, sourcePinId string) *ServiceRecord {
	rec := &ServiceRecord{
		SourceServicePinId: sourcePinId,
		CurrentPinId:       pin.Id,
		ChainName:          pin.ChainName,

		ProviderMetaId:       firstNonEmpty(pin.MetaId, pin.CreateMetaId),
		ProviderGlobalMetaId: pin.GlobalMetaId,
		ProviderAddress:      firstNonEmpty(pin.Address, pin.CreateAddress),

		ServiceName:     summary.ServiceName,
		DisplayName:     summary.DisplayName,
		Description:     summary.Description,
		ServiceIcon:     summary.ServiceIcon,
		ProviderMetaBot: summary.ProviderMetaBot,
		ProviderSkill:   summary.ProviderSkill,
		Price:           summary.Price,
		Currency:        summary.Currency,
		PaymentChain:    summary.PaymentChain,
		SettlementKind:  summary.SettlementKind,
		MRC20Ticker:     summary.MRC20Ticker,
		MRC20Id:         summary.MRC20Id,
		OutputType:      summary.OutputType,
		PaymentAddress:  summary.PaymentAddress,

		Status:    statusFromPin(pin),
		Operation: strings.ToLower(strings.TrimSpace(pin.Operation)),
		Disabled:  summary.Disabled,

		CreatedAt: pin.Timestamp,
		UpdatedAt: pin.Timestamp,
	}
	return rec
}

// statusFromPin returns the chain-declared status. PinInscription does not
// carry a numeric status field — it carries IsTransfered as the closest
// hint — so for now we default to StatusConfirmed (0). A future PIN schema
// update can plumb the real value through; the aggregator already stores
// it verbatim so no API change will be needed.
func statusFromPin(_ *aggregator.PinInscription) int {
	return StatusConfirmed
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// String renders the record briefly for log lines. We do not put this in
// types.go because it is purely a debug helper for process.go.
func recordSummary(r *ServiceRecord) string {
	if r == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%s/%s op=%s disabled=%v", r.ChainName, r.SourceServicePinId, r.Operation, r.Disabled)
}

// processRatingPin moved to rating.go (M3).
