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
	base := protocolPathFromPinPath(pin.Path)
	switch base {
	case PathSkillService:
		return a.processServicePin(pin)
	case PathSkillServiceRate:
		return a.processRatingPin(pin)
	default:
		if isVersionOperation(pin.Operation) {
			return a.processTargetedServicePin(pin)
		}
		return nil
	}
}

// processServicePin handles a single /protocols/skill-service PIN. It folds
// create / modify / revoke into a single ServiceRecord keyed by
// (chainName, sourceServicePinId).
//
// Same-chain invariant: if a modify or revoke target resolves to a service
// on a different chainName, the pin is rejected as a compatibility fallback
// (logged, not stored). Cross-chain version chains are intentionally out of
// scope for v1; see docs/specs/2026-05-28-bot-hub-skill-service-...
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

// processServiceModify resolves the MetaID target pin back to the source
// service, rejects stale or cross-chain references, and overwrites the current
// ServiceRecord while keeping CreatedAt stable.
func (a *Aggregator) processServiceModify(pin *aggregator.PinInscription) error {
	targetPinId, ok := resolveTargetPinId(pin)
	if !ok {
		return nil
	}

	previous, err := a.loadServiceByAnyPinId(pin.ChainName, targetPinId)
	if err != nil {
		return err
	}
	if previous == nil {
		// No target on this chain to fold into; could be cross-chain
		// target id or simply out-of-order delivery. Drop the modify
		// silently — the indexer engine does not replay later, so out-of-
		// order is expected to be rare; log it so we can tell apart cross-
		// chain pollution.
		log.Printf("[skillservice] modify pin %s: target %s/%s not found (cross-chain or out-of-order); skipped",
			pin.Id, pin.ChainName, targetPinId)
		return nil
	}
	if !isCurrentVersionTarget(previous, targetPinId) {
		log.Printf("[skillservice] modify pin %s: target %s is not current version %s for source %s; skipped",
			pin.Id, targetPinId, previous.CurrentPinId, previous.SourceServicePinId)
		return nil
	}

	summary, ok := decodeServiceSummary(pin)
	if !ok {
		return nil
	}

	sourcePinId := previous.SourceServicePinId
	updated := newServiceRecord(pin, summary, sourcePinId)
	// Preserve the original CreatedAt; modifies only move UpdatedAt forward.
	updated.CreatedAt = previous.CreatedAt
	// Modify PINs often omit provider identity metadata; keep the create
	// record's provider keys so list/detail can still resolve profiles.
	if updated.ProviderMetaId == "" {
		updated.ProviderMetaId = previous.ProviderMetaId
	}
	if updated.ProviderGlobalMetaId == "" {
		updated.ProviderGlobalMetaId = previous.ProviderGlobalMetaId
	}
	if updated.ProviderAddress == "" {
		updated.ProviderAddress = previous.ProviderAddress
	}
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
	targetPinId, ok := resolveTargetPinId(pin)
	if !ok {
		return nil
	}

	previous, err := a.loadServiceByAnyPinId(pin.ChainName, targetPinId)
	if err != nil {
		return err
	}
	if previous == nil {
		log.Printf("[skillservice] revoke pin %s: target %s/%s not found (cross-chain or out-of-order); skipped",
			pin.Id, pin.ChainName, targetPinId)
		return nil
	}
	if !isCurrentVersionTarget(previous, targetPinId) {
		log.Printf("[skillservice] revoke pin %s: target %s is not current version %s for source %s; skipped",
			pin.Id, targetPinId, previous.CurrentPinId, previous.SourceServicePinId)
		return nil
	}

	sourcePinId := previous.SourceServicePinId
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

func protocolPathFromPinPath(path string) string {
	base := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	if at := strings.Index(base, "@"); at > 0 {
		return base[:at]
	}
	return base
}

func isVersionOperation(operation string) bool {
	switch strings.ToLower(strings.TrimSpace(operation)) {
	case OperationModify, OperationRevoke:
		return true
	default:
		return false
	}
}

func (a *Aggregator) processTargetedServicePin(pin *aggregator.PinInscription) error {
	targetPinId := pinTargetFromPath(pin.Path)
	if targetPinId == "" || pin.ChainName == "" {
		return nil
	}
	rec, err := a.loadServiceByAnyPinId(pin.ChainName, targetPinId)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	return a.processServicePin(pin)
}

// resolveTargetPinId picks the target pin id for a modify/revoke operation.
// MetaID convention writes the target in path as `@<pinId>`; legacy test
// fixtures and older publishers may also provide OriginalId directly.
func resolveTargetPinId(pin *aggregator.PinInscription) (string, bool) {
	if candidate := pinTargetFromPath(pin.Path); candidate != "" && candidate != pin.Id {
		return candidate, true
	}
	if candidate := strings.TrimPrefix(strings.TrimSpace(pin.OriginalId), "@"); candidate != "" && candidate != pin.Id {
		return candidate, true
	}
	log.Printf("[skillservice] modify/revoke pin %s missing target pin id; skipped", pin.Id)
	return "", false
}

func pinTargetFromPath(path string) string {
	at := strings.Index(path, "@")
	if at < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(path[at+1:]), "/")
}

func isCurrentVersionTarget(rec *ServiceRecord, targetPinId string) bool {
	if rec == nil {
		return false
	}
	return targetPinId == rec.CurrentPinId
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
