package mrc721

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Pebble key families of the mrc721 namespace:
//
//	meta:seq                        global write-sequence counter (8-byte BE)
//	intent:<itemPinId>              latest sell-intent record per item (JSON)
//	itime:<invSeq16>:<itemPinId>    sell-intent recency index, newest first
//	coll:<collectionPinId>          collection read-model row per pinid (JSON)
//	ctime:<invSeq16>:<collectionPinId> collection recency index, newest first
const (
	keySeq        = "meta:seq"
	keyIntent     = "intent:"
	keyIntentTime = "itime:"
	keyColl       = "coll:"
	keyCollTime   = "ctime:"
)

func intentKey(itemPinId string) []byte {
	return []byte(keyIntent + itemPinId)
}

func intentTimeKey(seq uint64, itemPinId string) []byte {
	return []byte(keyIntentTime + invertedSeq(seq) + ":" + itemPinId)
}

func intentTimePrefix() []byte {
	return []byte(keyIntentTime)
}

func collectionKey(pinId string) []byte {
	return []byte(keyColl + pinId)
}

func collectionTimeKey(seq uint64, pinId string) []byte {
	return []byte(keyCollTime + invertedSeq(seq) + ":" + pinId)
}

func collectionTimePrefix() []byte {
	return []byte(keyCollTime)
}

// invertedSeq renders the write sequence as a big-endian hex complement so
// ascending key order is newest-write-first (same scheme as the qa /
// publishedcontent read models).
func invertedSeq(seq uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, ^seq)
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf))
}

// parseIntentTimeKey extracts (seq, itemPinId) from an itime key.
func parseIntentTimeKey(key []byte) (uint64, string, bool) {
	rest := strings.TrimPrefix(string(key), keyIntentTime)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || len(parts[0]) != 16 {
		return 0, "", false
	}
	buf, err := hex.DecodeString(parts[0])
	if err != nil || len(buf) != 8 {
		return 0, "", false
	}
	return ^binary.BigEndian.Uint64(buf), parts[1], true
}

// parseCollectionTimeKey extracts (seq, collectionPinId) from a ctime key.
func parseCollectionTimeKey(key []byte) (uint64, string, bool) {
	rest := strings.TrimPrefix(string(key), keyCollTime)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 || len(parts[0]) != 16 {
		return 0, "", false
	}
	buf, err := hex.DecodeString(parts[0])
	if err != nil || len(buf) != 8 {
		return 0, "", false
	}
	return ^binary.BigEndian.Uint64(buf), parts[1], true
}

// nextSeq allocates the next write-sequence value.
func (a *Aggregator) nextSeq() (uint64, error) {
	raw, err := a.store.Get(Namespace, []byte(keySeq))
	if err != nil || len(raw) == 0 {
		return 1, a.store.Set(Namespace, []byte(keySeq), encodeSeq(1))
	}
	return binary.BigEndian.Uint64(raw) + 1, a.store.Set(Namespace, []byte(keySeq), encodeSeq(binary.BigEndian.Uint64(raw)+1))
}

func encodeSeq(seq uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, seq)
	return buf
}

func (a *Aggregator) loadSellIntent(itemPinId string) (*SellIntentRecord, error) {
	raw, err := a.store.Get(Namespace, intentKey(itemPinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rec SellIntentRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("corrupt sell intent record %s: %w", itemPinId, err)
	}
	return &rec, nil
}

// saveSellIntent writes the latest-wins record plus its recency-index
// entry. The caller deletes the item's previous index position first so
// queries never see the same item twice. Callers hold a.mu.
func (a *Aggregator) saveSellIntent(rec *SellIntentRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := a.store.Set(Namespace, intentKey(rec.ItemPinId), raw); err != nil {
		return err
	}
	return a.store.Set(Namespace, intentTimeKey(rec.Seq, rec.ItemPinId), []byte("1"))
}

// deleteSellIntentIndexEntry removes a stale recency-index entry observed
// during scans (key seq differs from the record's current seq).
func (a *Aggregator) deleteSellIntentIndexEntry(seq uint64, itemPinId string) {
	_ = a.store.Delete(Namespace, intentTimeKey(seq, itemPinId))
}

// ingestSellPin folds one confirmed pin into the sell-intent index. It
// returns whether the pin changed the read model. Non-sell-path pins and
// non-create operations are ignored (create-only protocol); unparseable or
// invalid payloads are logged and skipped (never fail the block pipeline);
// a pin older than the item's stored declaration is a no-op (latest-wins).
func (a *Aggregator) ingestSellPin(pin *aggregator.PinInscription) (bool, error) {
	if pin == nil || a.store == nil {
		return false, nil
	}
	if normalisedPinPath(pin.Path) != PathSell {
		return false, nil
	}
	if strings.ToLower(strings.TrimSpace(pin.Operation)) != OperationCreate {
		// The protocol is create-only: modify/revoke pins on the path carry
		// no declaration semantics.
		return false, nil
	}

	sellPinId := strings.TrimSpace(pin.Id)
	payload, err := parseSellIntentPayload(sellPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: mrc721 sell pin %s skipped: %v", sellPinId, err)
		return false, nil
	}

	itemPinId := payload.ItemPinId
	existing, err := a.loadSellIntent(itemPinId)
	if err != nil {
		log.Printf("WARNING: mrc721 sell pin %s skipped: load existing intent: %v", sellPinId, err)
		return false, nil
	}

	height := pin.GenesisHeight
	updatedAt := unixSeconds(pin.Timestamp)
	if existing != nil {
		if existing.SellPinId == sellPinId {
			return false, nil // idempotent re-apply (live + backfill overlap)
		}
		if !chainOrderNewer(existing.Height, existing.UpdatedAt, existing.SellPinId, height, updatedAt, sellPinId) {
			return false, nil // older declaration re-arriving; latest stays
		}
	}

	seq, err := a.nextSeq()
	if err != nil {
		return false, err
	}
	rec := &SellIntentRecord{
		ItemPinId:          itemPinId,
		SellPinId:          sellPinId,
		Active:             payload.Active,
		CollectionName:     payload.CollectionName,
		CollectionPinId:    payload.CollectionPinId,
		ItemOutpoint:       payload.ItemOutpoint,
		SellerAddress:      payload.SellerAddress,
		SellerMetaId:       payload.SellerMetaId,
		PriceHintSats:      payload.PriceHintSats,
		AcceptedCurrencies: payload.AcceptedCurrencies,
		Settlement:         payload.Settlement,
		Note:               payload.Note,
		CreatedAt:          payload.CreatedAt,
		ExpiresAt:          payload.ExpiresAt,
		ChainName:          strings.TrimSpace(pin.ChainName),
		Height:             height,
		TxID:               txIdOfPinId(sellPinId),
		UpdatedAt:          updatedAt,
		Seq:                seq,
	}
	if existing != nil && existing.Seq > 0 {
		// Overwrite: clear the previous index position before writing the
		// new one (saveSellIntent re-keys).
		if err := a.store.Delete(Namespace, intentTimeKey(existing.Seq, itemPinId)); err != nil {
			log.Printf("WARNING: mrc721 sell intent %s index re-key failed: %v", itemPinId, err)
		}
	}
	if err := a.saveSellIntent(rec); err != nil {
		return false, err
	}
	return true, nil
}

// chainOrderNewer reports whether the (height, chainTime, pinId) triple of a
// candidate declaration is strictly later than the stored one. Chain height
// is the primary key; chain time breaks same-height ties; the pin id breaks
// pathological same-tx ties deterministically.
func chainOrderNewer(curHeight, curTime int64, curPinId string, newHeight, newTime int64, newPinId string) bool {
	if newHeight != curHeight {
		return newHeight > curHeight
	}
	if newTime != curTime {
		return newTime > curTime
	}
	return newPinId > curPinId
}

// sellPayloadBytes prefers the parsed content body and falls back to the
// content summary (single-line payloads are summarised there verbatim).
func sellPayloadBytes(pin *aggregator.PinInscription) []byte {
	if pin == nil {
		return nil
	}
	if len(pin.ContentBody) > 0 {
		return pin.ContentBody
	}
	return []byte(pin.ContentSummary)
}

// txIdOfPinId extracts the 64-hex transaction id from a `<64hex>i<n>` pin
// id; unknown shapes return the input trimmed.
func txIdOfPinId(pinId string) string {
	pinId = strings.TrimSpace(pinId)
	if idx := strings.Index(pinId, "i"); idx == 64 {
		return strings.ToLower(pinId[:64])
	}
	return pinId
}
