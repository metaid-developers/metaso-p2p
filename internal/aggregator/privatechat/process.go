package privatechat

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Protocol message structures parsed from Pin ContentBody.

// SimpleMsg is the JSON structure for /private/chat/simplemsg protocol.
type SimpleMsg struct {
	From         string   `json:"from"`
	To           string   `json:"to"`
	Content      string   `json:"content"`
	ContentType  string   `json:"contentType"`
	Encrypt      string   `json:"encryption"`
	EncryptAlias string   `json:"encrypt"`
	ReplyPin     string   `json:"replyPin"`
	Mention      []string `json:"mention"`
}

// SimplePrivateBlock is the JSON structure for /private/block/simpleprivateblock protocol.
type SimplePrivateBlock struct {
	To         string      `json:"to"`
	BlockState interface{} `json:"blockState"` // 1 = block, -1 = unblock
}

// dispatchPin dispatches pins to the appropriate handler based on pin.Path.
func (a *Aggregator) dispatchPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if pin == nil {
		return nil, nil
	}

	path := pin.Path
	if path == "" {
		return nil, nil
	}

	// Normalize path: remove leading/trailing slashes, convert to lowercase
	path = strings.Trim(path, "/")
	pathLower := strings.ToLower(path)

	switch {
	// Private chat message
	case strings.HasSuffix(pathLower, "simplemsg"):
		return a.handlePrivateChat(pin)

	// Private chat block/unblock — compatibility spelling
	// (/private/block/simpleprivateblock). Matched before the canonical
	// suffix so both dispatch correctly (spec §1.4).
	case strings.HasSuffix(pathLower, "simpleprivateblock"):
		return a.handlePrivateBlock(pin)

	// Block/unblock — canonical registered protocol /protocols/simpleblock.
	// "simpleprivateblock" does not end with "simpleblock", and group-block
	// paths (simplegroupblock etc.) end with "groupblock", so this case is
	// exclusive to the canonical protocol (spec §1.4/§5.2).
	case strings.HasSuffix(pathLower, "simpleblock"):
		return a.handlePrivateBlock(pin)

	default:
		return nil, nil
	}
}

// handlePrivateChat processes a /private/chat/simplemsg pin.
func (a *Aggregator) handlePrivateChat(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	var sm SimpleMsg
	if err := json.Unmarshal(pin.ContentBody, &sm); err != nil {
		log.Printf("[privatechat] failed to parse simplemsg: %v", err)
		return nil, nil
	}

	fromMetaId := sm.From
	if fromMetaId == "" {
		fromMetaId = pin.CreateMetaId
		if fromMetaId == "" {
			fromMetaId = pin.MetaId
		}
	}
	toMetaId := sm.To
	if toMetaId == "" {
		return nil, nil
	}

	fromGlobalMetaId := pin.GlobalMetaId
	if fromGlobalMetaId == "" {
		fromGlobalMetaId = pin.CreateMetaId
	}
	toGlobalMetaId := sm.To

	fromAddress := pin.CreateAddress
	if fromAddress == "" {
		fromAddress = pin.Address
	}

	txId := extractTxId(pin.Id)

	encryption := sm.Encrypt
	if encryption == "" {
		encryption = sm.EncryptAlias
	}
	contentType := sm.ContentType

	msg := &PrivateMessage{
		FromGlobalMetaId: fromGlobalMetaId,
		From:             fromMetaId,
		FromAddress:      fromAddress,
		ToGlobalMetaId:   toGlobalMetaId,
		To:               toMetaId,
		ToAddress:        "",
		TxId:             txId,
		PinId:            pin.Id,
		Protocol:         pin.Path,
		Content:          sm.Content,
		ContentType:      contentType,
		Encryption:       encryption,
		Timestamp:        pin.Timestamp,
		Chain:            pin.ChainName,
		BlockHeight:      pin.GenesisHeight,
		Confirmed:        pin.GenesisHeight > 0,
		Index:            -1,
	}
	if !a.canonicalizePrivateMessageForDelivery(msg) {
		log.Printf("[privatechat] skipped unresolved private message identities: pinId=%s from=%s to=%s", pin.Id, fromGlobalMetaId, toGlobalMetaId)
		return nil, nil
	}
	normalizePrivateMessageConfirmation(msg)

	writeResult, err := a.UpsertPrivateMessage(msg)
	if err != nil {
		return nil, err
	}
	if !writeResult.Created {
		if writeResult.ConfirmationUpdated {
			log.Printf("[privatechat] private message confirmed: pinId=%s height=%d", msg.PinId, msg.BlockHeight)
		}
		return nil, nil
	}

	notifyEvent := &aggregator.NotifyEvent{
		Type:         "WS_SERVER_NOTIFY_PRIVATE_CHAT",
		MetaId:       toMetaId,
		GlobalMetaId: msg.ToGlobalMetaId,
		TargetIds:    a.identityAliases(toMetaId),
		PinId:        msg.PinId,
		Payload:      msg,
	}

	log.Printf("[privatechat] private message saved: pinId=%s from=%s to=%s", msg.PinId, msg.FromGlobalMetaId, msg.ToGlobalMetaId)
	return notifyEvent, nil
}

// handlePrivateBlock processes block pins on both ingested paths
// (/private/block/simpleprivateblock compatibility and the canonical
// /protocols/simpleblock, spec §1.4). Payload parsing and validation stay
// here; the read-model fold is delegated to the block aggregator (the
// authority for chain-level block state, spec §4.6). Mempool pins are not
// materialized (confirmed-only, spec OQ-2) — the block aggregator's own
// registry dispatch ignores them too.
func (a *Aggregator) handlePrivateBlock(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if pin == nil {
		return nil, nil
	}

	var spb SimplePrivateBlock
	if err := json.Unmarshal(pin.ContentBody, &spb); err != nil {
		log.Printf("[privatechat] failed to parse block pin %s: %v", pin.Id, err)
		return nil, nil
	}
	if strings.TrimSpace(spb.To) == "" {
		log.Printf("[privatechat] block pin %s has empty to, ignored", pin.Id)
		return nil, nil
	}

	if a.blockIngestor == nil {
		// Legacy behavior kept as fallback: parse and log only.
		blockState := int64(1)
		if state, ok := spb.BlockState.(float64); ok {
			blockState = int64(state)
		}
		log.Printf("[privatechat] block observed (no block ingestor wired): to=%s blockState=%d pinId=%s", spb.To, blockState, pin.Id)
		return nil, nil
	}
	if pin.GenesisHeight <= 0 {
		log.Printf("[privatechat] block pin %s not confirmed yet, skipped (confirmed-only)", pin.Id)
		return nil, nil
	}
	if err := a.blockIngestor.IngestBlockPin(pin); err != nil {
		return nil, err
	}
	return nil, nil
}

// extractTxId extracts the transaction ID from BTC-style txId:iN and
// MVC-style 64-hex-txIdiN pin IDs.
func extractTxId(pinId string) string {
	pinId = strings.TrimSpace(pinId)
	idx := strings.LastIndex(pinId, ":i")
	if idx > 0 && isDecimal(pinId[idx+2:]) {
		return pinId[:idx]
	}
	if len(pinId) > 65 && pinId[64] == 'i' && isHex(pinId[:64]) && isDecimal(pinId[65:]) {
		return pinId[:64]
	}
	return pinId
}

func isDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func isHex(value string) bool {
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return value != ""
}

// sendNotifyEvent sends a notify event on the aggregator's channel if the channel is not full.
func (a *Aggregator) sendNotifyEvent(event *aggregator.NotifyEvent) bool {
	if event == nil {
		return false
	}

	select {
	case a.notifyCh <- event:
		return true
	default:
		log.Printf("[privatechat] notify channel full, dropping event type=%s", event.Type)
		return false
	}
}
