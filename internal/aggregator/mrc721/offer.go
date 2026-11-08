// The /protocols/mrc721offer passive-offer index (R-E-1,
// docs/nft-offer-protocol.md): a hard MRC-721 listing whose pin wraps the
// seller's signed settlement tx. The ingest mirrors the sell-intent log
// exactly — create-only, latest pin per itemPinId wins by chain order,
// cancellations are ordinary rows with active=false — and stores offerTx
// verbatim (the client's signature verification depends on byte identity;
// this indexer never parses or validates the tx).

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

// PathOffer is the fixed MRC-721 passive-offer declaration path
// (docs/nft-offer-protocol.md §2). Pins on this path are ordinary raw pins;
// this index only subscribes to them.
const PathOffer = "/protocols/mrc721offer"

// OfferRecord is the latest-wins projection of one itemPinId's offer log
// entry. Seq is the local monotonic write counter used for the recency index
// and the pagination cursor (same scheme as the sell-intent records).
type OfferRecord struct {
	ItemPinId  string `json:"itemPinId"`
	OfferPinId string `json:"offerPinId"`
	Active     bool   `json:"active"`
	PriceSats  int64  `json:"priceSats"`
	// OfferTx is the base64 TxComposer JSON envelope exactly as written on
	// chain — never parsed, validated, or normalised (R-E-1).
	OfferTx string `json:"offerTx"`
	// SellerMetaId is the offer pin creator (the payload deliberately
	// carries no seller fields; the seller identity is the pin creator).
	SellerMetaId string `json:"sellerMetaId,omitempty"`
	CreatedAt    int64  `json:"createdAt,omitempty"`

	// Chain facts of the winning offer pin.
	ChainName string `json:"chainName,omitempty"`
	Height    int64  `json:"height,omitempty"`
	TxID      string `json:"txId,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"` // chain time, unix seconds

	Seq uint64 `json:"seq"`
}

// offerPayload is the validated view of one /protocols/mrc721offer pin
// payload (docs/nft-offer-protocol.md §3). Key resolution reuses the
// sell-intent tolerant lookup (exact camelCase spelling wins, then
// case-insensitive / underscore-folded fallbacks). Unknown fields are
// ignored, never rejected.
type offerPayload struct {
	ItemPinId string
	Active    bool
	PriceSats int64
	OfferTx   string
	CreatedAt int64
}

// parseOfferPayload parses and validates one offer content body. Required
// fields: itemPinId, active, priceSats, offerTx (the frozen schema; the
// client's cancel pins carry the same shape with active=false). Any
// violation returns an error — the ingest path logs and skips such pins.
func parseOfferPayload(raw []byte) (*offerPayload, error) {
	fields, err := decodePayloadObject(raw)
	if err != nil {
		return nil, err
	}

	payload := &offerPayload{}
	var ok bool

	if payload.ItemPinId, ok = payloadString(fields, "itemPinId"); !ok {
		return nil, fmt.Errorf("missing itemPinId")
	}
	if !pinIdPattern.MatchString(payload.ItemPinId) {
		return nil, fmt.Errorf("itemPinId %q is not a <64hex>i<n> pin id", payload.ItemPinId)
	}
	activeValue, ok := payloadLookup(fields, "active")
	if !ok {
		return nil, fmt.Errorf("missing active")
	}
	if payload.Active, ok = payloadBool(activeValue); !ok {
		return nil, fmt.Errorf("active is not a boolean")
	}
	if payload.PriceSats, ok = payloadInt(fields, "priceSats"); !ok {
		return nil, fmt.Errorf("missing or invalid priceSats")
	}
	// offerTx is stored verbatim: read the JSON string without trimming or
	// normalising so the client's byte-exact signature verification holds.
	if payload.OfferTx, ok = payloadVerbatimString(fields, "offerTx"); !ok {
		return nil, fmt.Errorf("missing offerTx")
	}
	payload.CreatedAt, _ = payloadInt(fields, "createdAt")

	// Timestamps are epoch seconds; tolerate millisecond values the same
	// way the sell-intent parse does.
	if payload.CreatedAt > 100000000000 {
		payload.CreatedAt /= 1000
	}
	return payload, nil
}

// payloadVerbatimString reads a JSON string field with its bytes preserved
// exactly as written (no trimming / normalisation) — offerTx storage
// depends on byte identity (R-E-1).
func payloadVerbatimString(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := payloadLookup(fields, key)
	if !ok {
		return "", false
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return text, text != ""
}

// Pebble key families of the offer read model (same inverted-seq recency
// scheme as the sell-intent index):
//
//	offer:<itemPinId>               latest offer record per item (JSON)
//	otime:<invSeq16>:<itemPinId>    offer recency index, newest first
const (
	keyOffer     = "offer:"
	keyOfferTime = "otime:"
)

func offerKey(itemPinId string) []byte {
	return []byte(keyOffer + itemPinId)
}

func offerTimeKey(seq uint64, itemPinId string) []byte {
	return []byte(keyOfferTime + invertedSeq(seq) + ":" + itemPinId)
}

func offerTimePrefix() []byte {
	return []byte(keyOfferTime)
}

// parseOfferTimeKey extracts (seq, itemPinId) from an otime key.
func parseOfferTimeKey(key []byte) (uint64, string, bool) {
	rest := strings.TrimPrefix(string(key), keyOfferTime)
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

func (a *Aggregator) loadOffer(itemPinId string) (*OfferRecord, error) {
	raw, err := a.store.Get(Namespace, offerKey(itemPinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rec OfferRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("corrupt offer record %s: %w", itemPinId, err)
	}
	return &rec, nil
}

// saveOffer writes the latest-wins record plus its recency-index entry.
// The caller deletes the item's previous index position first so queries
// never see the same item twice. Callers hold a.mu.
func (a *Aggregator) saveOffer(rec *OfferRecord) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := a.store.Set(Namespace, offerKey(rec.ItemPinId), raw); err != nil {
		return err
	}
	return a.store.Set(Namespace, offerTimeKey(rec.Seq, rec.ItemPinId), []byte("1"))
}

// deleteOfferIndexEntry removes a stale recency-index entry observed during
// scans (key seq differs from the record's current seq).
func (a *Aggregator) deleteOfferIndexEntry(seq uint64, itemPinId string) {
	_ = a.store.Delete(Namespace, offerTimeKey(seq, itemPinId))
}

// ingestOfferPin folds one confirmed pin into the offer index. It returns
// whether the pin changed the read model. Non-offer-path pins, non-create
// operations, and disabled-flag runs are ignored (create-only protocol);
// unparseable or invalid payloads are logged and skipped (never fail the
// block pipeline); a pin older than the item's stored offer is a no-op
// (latest-wins).
func (a *Aggregator) ingestOfferPin(pin *aggregator.PinInscription) (bool, error) {
	// The gate is startup-set-once (SetOfferEnabled before Register); the
	// ingest callers already hold a.mu, so a direct read is the only
	// deadlock-free shape here.
	if pin == nil || a.store == nil || !a.offerIndexEnabled {
		return false, nil
	}
	if normalisedPinPath(pin.Path) != PathOffer {
		return false, nil
	}
	if strings.ToLower(strings.TrimSpace(pin.Operation)) != OperationCreate {
		// The protocol is create-only: modify/revoke pins on the path carry
		// no declaration semantics.
		return false, nil
	}

	offerPinId := strings.TrimSpace(pin.Id)
	payload, err := parseOfferPayload(pinPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: mrc721 offer pin %s skipped: %v", offerPinId, err)
		return false, nil
	}

	itemPinId := payload.ItemPinId
	existing, err := a.loadOffer(itemPinId)
	if err != nil {
		log.Printf("WARNING: mrc721 offer pin %s skipped: load existing offer: %v", offerPinId, err)
		return false, nil
	}

	height := pin.GenesisHeight
	updatedAt := unixSeconds(pin.Timestamp)
	if existing != nil {
		if existing.OfferPinId == offerPinId {
			return false, nil // idempotent re-apply (live + replay overlap)
		}
		if !chainOrderNewer(existing.Height, existing.UpdatedAt, existing.OfferPinId, height, updatedAt, offerPinId) {
			return false, nil // older declaration re-arriving; latest stays
		}
	}

	seq, err := a.nextSeq()
	if err != nil {
		return false, err
	}
	rec := &OfferRecord{
		ItemPinId:    itemPinId,
		OfferPinId:   offerPinId,
		Active:       payload.Active,
		PriceSats:    payload.PriceSats,
		OfferTx:      payload.OfferTx,
		SellerMetaId: pinCreatorMetaId(pin),
		CreatedAt:    payload.CreatedAt,
		ChainName:    strings.TrimSpace(pin.ChainName),
		Height:       height,
		TxID:         txIdOfPinId(offerPinId),
		UpdatedAt:    updatedAt,
		Seq:          seq,
	}
	if existing != nil && existing.Seq > 0 {
		// Overwrite: clear the previous index position before writing the
		// new one (saveOffer re-keys).
		if err := a.store.Delete(Namespace, offerTimeKey(existing.Seq, itemPinId)); err != nil {
			log.Printf("WARNING: mrc721 offer %s index re-key failed: %v", itemPinId, err)
		}
	}
	if err := a.saveOffer(rec); err != nil {
		return false, err
	}
	return true, nil
}

// pinCreatorMetaId resolves the pin creator identity for the offer row's
// sellerMetaId (the payload carries no seller fields by design — the seller
// identity is the pin creator). Same cascade as the publishedcontent
// publisher resolution.
func pinCreatorMetaId(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	if id := strings.TrimSpace(pin.MetaId); id != "" {
		return id
	}
	return strings.TrimSpace(pin.CreateMetaId)
}
