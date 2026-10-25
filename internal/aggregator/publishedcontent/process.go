package publishedcontent

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

func (a *Aggregator) processPin(pin *aggregator.PinInscription, isMempool bool) error {
	if pin == nil || pin.Id == "" || pin.ChainName == "" {
		return nil
	}
	op := normaliseOperation(pin.Operation)
	protocolPath := protocolPathFromPinPath(pin.Path)
	if !isPublishedProtocol(protocolPath) {
		if isVersionOperation(op) {
			return a.processTargetedPublishedPin(pin, op, isMempool)
		}
		return nil
	}

	switch op {
	case OperationCreate:
		return a.processCreate(pin, protocolPath, isMempool)
	case OperationModify:
		return a.processModify(pin, protocolPath, isMempool)
	case OperationRevoke:
		return a.processRevoke(pin, protocolPath, isMempool)
	default:
		return nil
	}
}

func (a *Aggregator) processTargetedPublishedPin(pin *aggregator.PinInscription, op string, isMempool bool) error {
	targetPinId := targetPinID(pin)
	if targetPinId == "" {
		return nil
	}
	protocolPath, ok, err := a.protocolPathForTargetPin(pin.ChainName, targetPinId)
	if err != nil || !ok {
		return err
	}
	switch op {
	case OperationModify:
		return a.processModify(pin, protocolPath, isMempool)
	case OperationRevoke:
		return a.processRevoke(pin, protocolPath, isMempool)
	default:
		return nil
	}
}

func (a *Aggregator) protocolPathForTargetPin(chainName, targetPinId string) (string, bool, error) {
	for _, protocolPath := range publishedProtocolPaths {
		rec, err := a.loadRecordByAnyPinId(chainName, protocolPath, targetPinId)
		if err != nil {
			return "", false, err
		}
		if rec != nil {
			return protocolPath, true, nil
		}
	}
	return "", false, nil
}

func (a *Aggregator) processCreate(pin *aggregator.PinInscription, protocolPath string, isMempool bool) error {
	existing, err := a.loadRecord(pin.ChainName, protocolPath, pin.Id)
	if err != nil {
		return err
	}
	rec := newRecordFromPin(pin, protocolPath, pin.Id, isMempool)
	if existing != nil {
		if !isMempool && existing.IsMempool {
			mergeConfirmedReplayWithPendingCurrent(existing, rec, pin.Id)
			return a.saveRecord(rec, existing)
		}
		// Creator migration: records stored before the Creator* fields
		// existed get their creator backfilled from the replayed create pin
		// — the authoritative original author, which a folded record can
		// never recover on its own once a third party modified the app.
		// MetaApp-scoped: the creator identity is only consumed by the
		// preference author-conflict check.
		if !isMempool && protocolPath == PathMetaApp && existing.lacksCreatorIdentity() {
			migrated := *existing
			migrated.CreatorGlobalMetaId = rec.CreatorGlobalMetaId
			migrated.CreatorMetaId = rec.CreatorMetaId
			migrated.CreatorAddress = rec.CreatorAddress
			return a.saveRecord(&migrated, existing)
		}
		return nil
	}
	return a.saveRecord(rec, nil)
}

// lacksCreatorIdentity reports whether a record predates the Creator* fields
// (all three absent). A partially populated record counts as populated.
func (r *Record) lacksCreatorIdentity() bool {
	return r.CreatorGlobalMetaId == "" && r.CreatorMetaId == "" && r.CreatorAddress == ""
}

// migrateLegacyCreatorFields backfills creator identity on legacy metaapp
// records at startup. For create-only records (current == root) the folded
// publisher IS the original author, so creator := publisher is exact.
// Legacy records that were already modified by a third party cannot be
// recovered here — their creator is repopulated when the create pin replays
// through processCreate (metaapp backfill re-run); until then the
// author-conflict check degrades to the folded-publisher approximation.
// Non-fatal: a scan failure leaves the records untouched and the next
// startup retries.
func (a *Aggregator) migrateLegacyCreatorFields() error {
	if a.store == nil {
		return nil
	}
	migrated := 0
	err := a.store.ScanPrefix(Namespace, []byte(keyRecord), func(key, value []byte) error {
		_, protocolPath, _, ok := parseRecordKey(key)
		if !ok || protocolPath != PathMetaApp {
			return nil
		}
		var rec Record
		if err := json.Unmarshal(value, &rec); err != nil || !rec.lacksCreatorIdentity() {
			return nil // corrupt or already carries creator identity
		}
		if rec.CurrentPinId != "" && rec.CurrentPinId != rec.SourcePinId {
			return nil // modified legacy record: needs the create-pin replay
		}
		rec.CreatorGlobalMetaId = rec.PublisherGlobalMetaId
		rec.CreatorMetaId = rec.PublisherMetaId
		rec.CreatorAddress = rec.PublisherAddress
		raw, err := json.Marshal(rec)
		if err != nil {
			return nil
		}
		if err := a.store.Set(Namespace, key, raw); err != nil {
			return err
		}
		migrated++
		return nil
	})
	if err != nil {
		return err
	}
	if migrated > 0 {
		log.Printf("publishedcontent: migrated creator identity onto %d legacy create-only metaapp record(s)", migrated)
	}
	return nil
}

// parseRecordKey splits a `record:<chain>:<path>:<pin>` storage key.
func parseRecordKey(key []byte) (chainName, protocolPath, sourcePinId string, ok bool) {
	rest := strings.TrimPrefix(string(key), keyRecord)
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

func (a *Aggregator) processModify(pin *aggregator.PinInscription, protocolPath string, isMempool bool) error {
	targetPinId := targetPinID(pin)
	if targetPinId == "" {
		return nil
	}
	previous, err := a.loadRecordByAnyPinId(pin.ChainName, protocolPath, targetPinId)
	if err != nil || previous == nil {
		return err
	}

	// Registry requirement §4: a metaprotocol modify whose publisher identity
	// does not match the source record's publisher is forged — audit it and
	// leave the version chain untouched (no new version record, no
	// pin_to_source pointer).
	if protocolPath == PathMetaProtocol && a.ownerBoundModifiesEnabled() {
		sourceIdentity := PublisherIdentity{
			GlobalMetaId: previous.PublisherGlobalMetaId,
			MetaId:       previous.PublisherMetaId,
			Address:      previous.PublisherAddress,
		}
		if !publisherIdentityMatches(sourceIdentity, publisherIdentityOfPin(pin)) {
			return a.recordInvalidProtocolModify(pin, previous)
		}
	}

	updated := newRecordFromPin(pin, protocolPath, previous.SourcePinId, isMempool)
	updated.CreatedAt = previous.CreatedAt
	updated.SourceNumber = previous.SourceNumber
	updated.SourcePath = previous.SourcePath
	updated.SourceHost = previous.SourceHost
	if updated.PublisherGlobalMetaId == "" {
		updated.PublisherGlobalMetaId = previous.PublisherGlobalMetaId
	}
	if updated.PublisherMetaId == "" {
		updated.PublisherMetaId = previous.PublisherMetaId
	}
	if updated.PublisherAddress == "" {
		updated.PublisherAddress = previous.PublisherAddress
	}
	// The creator identity always belongs to the original create pin, never
	// to the modifier; inherit it across every modify/revoke fold.
	updated.CreatorGlobalMetaId = firstNonEmpty(updated.CreatorGlobalMetaId, previous.CreatorGlobalMetaId)
	updated.CreatorMetaId = firstNonEmpty(updated.CreatorMetaId, previous.CreatorMetaId)
	updated.CreatorAddress = firstNonEmpty(updated.CreatorAddress, previous.CreatorAddress)
	if !updated.PayloadExposed {
		updated.PayloadText = previous.PayloadText
		updated.PayloadJSON = previous.PayloadJSON
		updated.PayloadExposed = previous.PayloadExposed
	}
	if previous.Operation == OperationRevoke {
		updated.Operation = OperationRevoke
		updated.Hidden = true
	}
	mergeConfirmedReplayWithPendingCurrent(previous, updated, pin.Id)

	if err := a.saveRecord(updated, previous); err != nil {
		return err
	}
	return a.mapPinToSource(pin.ChainName, pin.Id, previous.SourcePinId)
}

func (a *Aggregator) processRevoke(pin *aggregator.PinInscription, protocolPath string, isMempool bool) error {
	targetPinId := targetPinID(pin)
	if targetPinId == "" {
		return nil
	}
	previous, err := a.loadRecordByAnyPinId(pin.ChainName, protocolPath, targetPinId)
	if err != nil || previous == nil {
		return err
	}

	updated := *previous
	updated.Operation = OperationRevoke
	updated.Hidden = true
	updated.IsMempool = isMempool
	updated.CurrentPinId = pin.Id
	updated.CurrentNumber = pin.Number
	updated.CurrentPath = pin.Path
	updated.CurrentHost = pin.Host
	if pin.ContentType != "" {
		updated.ContentType = pin.ContentType
	}
	if pin.Timestamp > 0 {
		updated.UpdatedAt = pin.Timestamp
	}
	mergeConfirmedReplayWithPendingCurrent(previous, &updated, pin.Id)
	if err := a.saveRecord(&updated, previous); err != nil {
		return err
	}
	return a.mapPinToSource(pin.ChainName, pin.Id, previous.SourcePinId)
}

func mergeConfirmedReplayWithPendingCurrent(previous, candidate *Record, confirmedPinID string) {
	if previous == nil || candidate == nil || candidate.IsMempool || !previous.IsMempool {
		return
	}
	if previous.CurrentPinId == "" || previous.CurrentPinId == previous.SourcePinId || previous.CurrentPinId == confirmedPinID {
		return
	}
	candidate.CurrentPinId = previous.CurrentPinId
	candidate.CurrentNumber = previous.CurrentNumber
	candidate.CurrentPath = previous.CurrentPath
	candidate.CurrentHost = previous.CurrentHost
	candidate.Operation = previous.Operation
	candidate.Hidden = previous.Hidden
	candidate.IsMempool = true
	candidate.ContentType = previous.ContentType
	candidate.PayloadText = previous.PayloadText
	candidate.PayloadJSON = previous.PayloadJSON
	candidate.PayloadExposed = previous.PayloadExposed
	candidate.UpdatedAt = previous.UpdatedAt
}

func newRecordFromPin(pin *aggregator.PinInscription, protocolPath, sourcePinId string, isMempool bool) *Record {
	payload := extractPayload(pin)
	ts := pin.Timestamp
	rec := &Record{
		SourcePinId:  sourcePinId,
		CurrentPinId: pin.Id,
		ChainName:    strings.ToLower(strings.TrimSpace(pin.ChainName)),
		ProtocolPath: protocolPath,

		PublisherGlobalMetaId: canonicalPublisherGlobalMetaId(pin),
		PublisherMetaId:       firstNonEmpty(pin.MetaId, pin.CreateMetaId),
		PublisherAddress:      firstNonEmpty(pin.Address, pin.CreateAddress),

		// On the create path (sourcePinId == pin.Id) the pin author is both
		// publisher and creator; processModify inherits these across folds.
		CreatorGlobalMetaId: creatorGlobalMetaId(pin, sourcePinId),
		CreatorMetaId:       creatorMetaId(pin, sourcePinId),
		CreatorAddress:      creatorAddress(pin, sourcePinId),

		Operation: normaliseOperation(pin.Operation),
		Hidden:    false,
		IsMempool: isMempool,

		ContentType:    pin.ContentType,
		PayloadText:    payload.text,
		PayloadJSON:    payload.jsonObject,
		PayloadExposed: payload.exposed,

		CreatedAt: ts,
		UpdatedAt: ts,

		SourceNumber:  pin.Number,
		CurrentNumber: pin.Number,
		SourcePath:    pin.Path,
		CurrentPath:   pin.Path,
		SourceHost:    pin.Host,
		CurrentHost:   pin.Host,
	}
	if rec.Operation == OperationRevoke {
		rec.Hidden = true
	}
	return rec
}

func canonicalPublisherGlobalMetaId(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	globalMetaId := strings.TrimSpace(pin.GlobalMetaId)
	address := firstNonEmpty(pin.Address, pin.CreateAddress)
	if address == "" {
		return globalMetaId
	}
	if globalMetaId != "" && !strings.EqualFold(globalMetaId, address) {
		return globalMetaId
	}
	if encoded := idaddress.EncodeGlobalMetaId(address, pin.ChainName); encoded != "" {
		return encoded
	}
	return globalMetaId
}

// creator* helpers stamp the creator identity only when the pin IS the
// create pin (its id equals the chain root); modify pins leave the fields
// empty so processModify inherits the original creator.
func creatorGlobalMetaId(pin *aggregator.PinInscription, sourcePinId string) string {
	if pin == nil || sourcePinId != pin.Id {
		return ""
	}
	return canonicalPublisherGlobalMetaId(pin)
}

func creatorMetaId(pin *aggregator.PinInscription, sourcePinId string) string {
	if pin == nil || sourcePinId != pin.Id {
		return ""
	}
	return firstNonEmpty(pin.MetaId, pin.CreateMetaId)
}

func creatorAddress(pin *aggregator.PinInscription, sourcePinId string) string {
	if pin == nil || sourcePinId != pin.Id {
		return ""
	}
	return firstNonEmpty(pin.Address, pin.CreateAddress)
}

type payloadResult struct {
	text       string
	jsonObject map[string]any
	exposed    bool
}

func extractPayload(pin *aggregator.PinInscription) payloadResult {
	if pin == nil {
		return payloadResult{}
	}
	raw := bytes.TrimSpace(pin.ContentBody)
	if len(raw) == 0 {
		raw = bytes.TrimSpace([]byte(pin.ContentSummary))
	}
	if len(raw) == 0 || isBinaryPayload(pin.ContentType, raw) {
		return payloadResult{}
	}
	if raw[0] == '{' {
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err == nil && obj != nil {
			return payloadResult{jsonObject: obj, exposed: true}
		}
	}
	return payloadResult{text: string(raw), exposed: true}
}

func isBinaryPayload(contentType string, raw []byte) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	for _, marker := range []string{"octet-stream", "zip", "image/", "audio/", "video/"} {
		if strings.Contains(ct, marker) {
			return true
		}
	}
	return bytes.IndexByte(raw, 0) >= 0
}

func protocolPathFromPinPath(path string) string {
	base := strings.TrimRight(strings.TrimSpace(path), "/")
	if at := strings.Index(base, "@"); at > 0 {
		base = base[:at]
	}
	if idx := strings.LastIndex(base, ":/"); idx >= 0 {
		if candidate := base[idx+1:]; strings.HasPrefix(candidate, "/protocols/") {
			base = candidate
		}
	}
	return strings.ToLower(base)
}

func targetPinID(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	if candidate := pinTargetFromPath(pin.Path); candidate != "" && candidate != pin.Id {
		return candidate
	}
	candidate := strings.TrimPrefix(strings.TrimSpace(pin.OriginalId), "@")
	if candidate != "" && candidate != pin.Id {
		return candidate
	}
	return ""
}

func pinTargetFromPath(path string) string {
	at := strings.Index(path, "@")
	if at < 0 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(path[at+1:]), "/")
}

func isPublishedProtocol(protocolPath string) bool {
	for _, supported := range publishedProtocolPaths {
		if protocolPath == supported {
			return true
		}
	}
	return false
}

var publishedProtocolPaths = []string{PathSimpleBuzz, PathMetaApp, PathMetaBotSkill, PathSimpleNote, PathMetaProtocol, PathSimpleQuestion, PathSimpleAnswer}

func isVersionOperation(operation string) bool {
	switch normaliseOperation(operation) {
	case OperationModify, OperationRevoke:
		return true
	default:
		return false
	}
}

func normaliseOperation(operation string) string {
	return strings.ToLower(strings.TrimSpace(operation))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
