package publishedcontent

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

func (a *Aggregator) processPin(pin *aggregator.PinInscription, isMempool bool) error {
	if pin == nil || pin.Id == "" || pin.ChainName == "" {
		return nil
	}
	protocolPath := protocolPathFromPinPath(pin.Path)
	if !isPublishedProtocol(protocolPath) {
		return nil
	}

	switch normaliseOperation(pin.Operation) {
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
		return nil
	}
	return a.saveRecord(rec, nil)
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

		PublisherGlobalMetaId: strings.TrimSpace(pin.GlobalMetaId),
		PublisherMetaId:       firstNonEmpty(pin.MetaId, pin.CreateMetaId),
		PublisherAddress:      firstNonEmpty(pin.Address, pin.CreateAddress),

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
	switch protocolPath {
	case PathSimpleBuzz, PathMetaApp, PathMetaBotSkill:
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
