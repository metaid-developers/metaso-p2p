package mrc721

import (
	"log"
	"sort"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Zero-confirmation declaration overlay (R-F-4): mempool sell intents and
// passive offers take effect on top of the confirmed latest-wins records
// immediately. Same design as the mrc721nft ownership overlay (in-memory,
// recomputed): a confirmed pin folds on the base path first and the overlay
// entries of its txid drop in the same critical section; a full mempool
// round that no longer contains a txid evicts its entries — a dropped
// re-declaration automatically restores the confirmed record, which removes
// the "no recovery path" concern that kept this a no-op before R-F.

// overlayDeclEntry is one mempool declaration pin (sell or offer), with the
// payload already parsed (invalid payloads never enter the overlay).
type overlayDeclEntry struct {
	pinId       string
	txid        string
	chainName   string
	firstSeenAt int64
	sell        *SellIntentRecord
	offer       *OfferRecord
	trade       *TradeRecord
}

// SetZeroConfTrade wires the trade gate (R-F-6; called from the daemon
// startup before Register; default off keeps today's behavior).
func (a *Aggregator) SetZeroConfTrade(enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.zeroConfTrade = enabled
}

// overlayCap is the per-aggregator declaration overlay entry cap (same
// owner-confirmed figure as the NFT overlay).
const overlayCap = 50000

// recordMempoolDeclarationLocked records one mempool declaration pin.
// Idempotent by pin id.
func (a *Aggregator) recordMempoolDeclarationLocked(pin *aggregator.PinInscription) bool {
	if pin == nil || a.store == nil || !a.zeroConfTrade || len(pin.Id) == 0 {
		return false
	}
	if _, ok := a.overlayDecls[pin.Id]; ok {
		return false // idempotent re-delivery
	}
	path := normalisedPinPath(pin.Path)
	if strings.ToLower(strings.TrimSpace(pin.Operation)) != OperationCreate {
		return false // create-only protocols
	}
	entry := &overlayDeclEntry{
		pinId:       pin.Id,
		txid:        txIdOfPinId(pin.Id),
		chainName:   strings.TrimSpace(pin.ChainName),
		firstSeenAt: time.Now().Unix(),
	}
	switch path {
	case PathSell:
		payload, err := parseSellIntentPayload(pinPayloadBytes(pin))
		if err != nil {
			log.Printf("WARNING: mrc721 overlay sell pin %s skipped: %v", pin.Id, err)
			return false
		}
		entry.sell = a.synthesizeSellRecord(pin, payload)
		if entry.sell.Active {
			delete(a.overlayInvalidated, entry.sell.ItemPinId)
		}
	case PathOffer:
		if !a.offerIndexEnabled {
			return false // the offer index is off: no offer surface at all
		}
		payload, err := parseOfferPayload(pinPayloadBytes(pin))
		if err != nil {
			log.Printf("WARNING: mrc721 overlay offer pin %s skipped: %v", pin.Id, err)
			return false
		}
		entry.offer = a.synthesizeOfferRecord(pin, payload)
	case PathTrade:
		payload, err := parseTradePayload(pinPayloadBytes(pin))
		if err != nil {
			log.Printf("WARNING: mrc721 overlay trade pin %s skipped: %v", pin.Id, err)
			return false
		}
		if existing, _ := a.loadTrade(payload.ItemPinId, payload.TxId); existing != nil && !isImplicitTrade(existing) {
			return false
		}
		pub := extractTradePublisher(pin)
		rec := buildTradeRecordFromPayload(pin, payload, false)
		if a.nftReader != nil {
			tr, found := a.nftReader.ItemTransferByTx(payload.ItemPinId, payload.TxId, true)
			if !found {
				pendingKey := rec.ItemPinId + ":" + rec.TxId
				a.overlayPendingTrades[pendingKey] = &pendingTradeEnvelope{
					PinId:       entry.pinId,
					TxId:        entry.txid,
					FirstSeenAt: entry.firstSeenAt,
					Trade:       rec,
					Publisher:   pub,
				}
				return false
			}
			if !a.tradeMatchesTransferLocked(rec, tr.FromAddress, tr.ToAddress) {
				log.Printf("WARNING: mrc721 overlay trade pin %s rejected: transfer mismatch", pin.Id)
				return false
			}
			if !a.isAuthorizedTradePublisherLocked(pub, tr.FromAddress, tr.ToAddress) {
				log.Printf("WARNING: mrc721 overlay trade pin %s rejected: unauthorized publisher", pin.Id)
				return false
			}
			a.enrichTradeFromTransferLocked(rec, tr.FromAddress, tr.ToAddress)
		} else {
			if !a.isAuthorizedTradePublisherLocked(pub, rec.SellerAddress, rec.BuyerAddress) {
				log.Printf("WARNING: mrc721 overlay trade pin %s rejected: unauthorized publisher", pin.Id)
				return false
			}
			a.enrichTradeMetadataLocked(rec)
		}
		entry.trade = rec
	default:
		return false
	}
	if entry.sell != nil {
		entry.sell.Overlay = true
		entry.sell.OverlayFirstSeenAt = entry.firstSeenAt
	}
	if entry.offer != nil {
		entry.offer.Overlay = true
		entry.offer.OverlayFirstSeenAt = entry.firstSeenAt
	}
	a.overlayDecls[entry.pinId] = entry
	a.overlayTxids[entry.txid] = entry.firstSeenAt
	a.evictOverlayCapLocked()
	return true
}

// synthesizeSellRecord builds the provisional record of one mempool sell
// declaration (same field mapping as the confirmed ingest).
func (a *Aggregator) synthesizeSellRecord(pin *aggregator.PinInscription, payload *sellIntentPayload) *SellIntentRecord {
	sellPinId := strings.TrimSpace(pin.Id)
	rec := &SellIntentRecord{
		ItemPinId:          payload.ItemPinId,
		SellPinId:          sellPinId,
		Active:             payload.Active,
		CollectionName:     payload.CollectionName,
		CollectionPinId:    payload.CollectionPinId,
		ItemOutpoint:       payload.ItemOutpoint,
		SellerAddress:      payload.SellerAddress,
		SellerMetaId:       payload.SellerMetaId,
		PriceHintSats:      payload.PriceHintSats,
		Prices:             payload.Prices,
		AutoFulfillMe:      payload.AutoFulfillMe,
		Negotiable:         payload.Negotiable,
		ShopMetaAppPinId:   payload.ShopMetaAppPinId,
		AcceptedCurrencies: payload.AcceptedCurrencies,
		Settlement:         payload.Settlement,
		Note:               payload.Note,
		CreatedAt:          payload.CreatedAt,
		ExpiresAt:          payload.ExpiresAt,
		ChainName:          strings.TrimSpace(pin.ChainName),
		Height:             pin.GenesisHeight, // -1 on the mempool path
		TxID:               txIdOfPinId(sellPinId),
		UpdatedAt:          unixSeconds(pin.Timestamp),
		Seq:                0, // overlay rows carry no write sequence
	}
	if !rec.Active {
		rec.InactiveReason = "canceled"
	} else {
		a.enrichSellRecordNFTFieldsLocked(rec)
	}
	return rec
}

// synthesizeOfferRecord builds the provisional record of one mempool offer
// declaration (same field mapping as the confirmed ingest).
func (a *Aggregator) synthesizeOfferRecord(pin *aggregator.PinInscription, payload *offerPayload) *OfferRecord {
	offerPinId := strings.TrimSpace(pin.Id)
	return &OfferRecord{
		ItemPinId:    payload.ItemPinId,
		OfferPinId:   offerPinId,
		Active:       payload.Active,
		PriceSats:    payload.PriceSats,
		OfferTx:      payload.OfferTx,
		SellerMetaId: pinCreatorMetaId(pin),
		CreatedAt:    payload.CreatedAt,
		ChainName:    strings.TrimSpace(pin.ChainName),
		Height:       pin.GenesisHeight, // -1 on the mempool path
		TxID:         txIdOfPinId(offerPinId),
		UpdatedAt:    unixSeconds(pin.Timestamp),
		Seq:          0,
	}
}

// overlayBefore orders overlay declarations in provisional order.
func overlayBefore(x, y *overlayDeclEntry) bool {
	if x.firstSeenAt != y.firstSeenAt {
		return x.firstSeenAt < y.firstSeenAt
	}
	if x.txid != y.txid {
		return x.txid < y.txid
	}
	return x.pinId < y.pinId
}

// overlaySellForItem applies the item's overlay sell declarations onto the
// confirmed latest-wins record: the last overlay pin in provisional order
// wins (confirmed chain order first, then overlay — R-F-4). Reports the
// winning overlay entry, or nil when the confirmed record stands.
func (a *Aggregator) overlaySellForItem(itemPinId string) *overlayDeclEntry {
	var applied []*overlayDeclEntry
	for _, entry := range a.overlayDecls {
		if entry.sell != nil && entry.sell.ItemPinId == itemPinId {
			applied = append(applied, entry)
		}
	}
	if len(applied) == 0 {
		return nil
	}
	// Provisional order; the last overlay pin wins over everything before
	// it, and every overlay pin wins over the confirmed record.
	sort.Slice(applied, func(i, j int) bool { return overlayBefore(applied[i], applied[j]) })
	return applied[len(applied)-1]
}

// effectiveSellIntent is the merged latest-wins view of one item's sell
// declaration (overlay wins, confirmed shows through). overlay reports
// whether the winning row is provisional.
func (a *Aggregator) effectiveSellIntent(itemPinId string) (rec *SellIntentRecord, overlay *overlayDeclEntry, err error) {
	rec, err = a.loadSellIntent(itemPinId)
	if err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.overlaySellForItem(itemPinId)
	if entry != nil {
		return entry.sell, entry, nil
	}
	return rec, nil, nil
}

// overlayOfferForItem is overlaySellForItem for the offer log.
func (a *Aggregator) overlayOfferForItem(itemPinId string) *overlayDeclEntry {
	var applied []*overlayDeclEntry
	for _, entry := range a.overlayDecls {
		if entry.offer != nil && entry.offer.ItemPinId == itemPinId {
			applied = append(applied, entry)
		}
	}
	if len(applied) == 0 {
		return nil
	}
	sort.Slice(applied, func(i, j int) bool { return overlayBefore(applied[i], applied[j]) })
	return applied[len(applied)-1]
}

// effectiveOffer is the merged latest-wins view of one item's offer.
func (a *Aggregator) effectiveOffer(itemPinId string) (rec *OfferRecord, overlay *overlayDeclEntry, err error) {
	rec, err = a.loadOffer(itemPinId)
	if err != nil {
		return nil, nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	entry := a.overlayOfferForItem(itemPinId)
	if entry != nil {
		return entry.offer, entry, nil
	}
	return rec, nil, nil
}

// dropOverlayForTxLocked is the confirmation handoff for declaration pins.
func (a *Aggregator) dropOverlayForTxLocked(txid string) {
	if txid == "" {
		return
	}
	for pinId, entry := range a.overlayDecls {
		if entry.txid == txid {
			delete(a.overlayDecls, pinId)
		}
	}
	for k, env := range a.overlayPendingTrades {
		if env != nil && env.TxId == txid {
			delete(a.overlayPendingTrades, k)
		}
	}
	delete(a.overlayTxids, txid)
}

// ReconcileMempoolRound implements aggregator.MempoolOverlayReconciler
// (R-F-4 acceptance: evicting a mempool cancellation restores the intent on
// the next full round). A nil txid set never reconciles.
func (a *Aggregator) ReconcileMempoolRound(chainName string, observedTxIDs []string) {
	if observedTxIDs == nil {
		return
	}
	a.mu.Lock()
	if len(a.overlayTxids) == 0 && len(a.overlayPendingTrades) == 0 {
		a.mu.Unlock()
		return
	}
	observed := make(map[string]struct{}, len(observedTxIDs))
	for _, txid := range observedTxIDs {
		observed[txid] = struct{}{}
	}
	var stale []string
	for txid := range a.overlayTxids {
		if _, ok := observed[txid]; !ok {
			stale = append(stale, txid)
		}
	}
	for _, env := range a.overlayPendingTrades {
		if env != nil && env.TxId != "" {
			if _, ok := observed[env.TxId]; !ok {
				stale = append(stale, env.TxId)
			}
		}
	}
	for _, txid := range stale {
		a.dropOverlayForTxLocked(txid)
	}
	a.mu.Unlock()
	if len(stale) > 0 {
		log.Printf("[mrc721] declaration overlay reconcile (%s): evicted %d txid(s)", chainName, len(stale))
		a.invalidateMarketCache()
	}
}

// evictOverlayCapLocked enforces the overlay entry cap, oldest-first.
func (a *Aggregator) evictOverlayCapLocked() {
	if len(a.overlayDecls)+len(a.overlayTxids) <= overlayCap {
		return
	}
	type aged struct {
		firstSeenAt int64
		txid        string
	}
	ages := make([]aged, 0, len(a.overlayDecls))
	for _, entry := range a.overlayDecls {
		ages = append(ages, aged{entry.firstSeenAt, entry.txid})
	}
	sort.Slice(ages, func(i, j int) bool { return ages[i].firstSeenAt < ages[j].firstSeenAt })
	evicted := make(map[string]struct{})
	for _, age := range ages {
		if len(a.overlayDecls) <= overlayCap {
			break
		}
		if _, done := evicted[age.txid]; done {
			continue
		}
		a.dropOverlayForTxLocked(age.txid)
		evicted[age.txid] = struct{}{}
		log.Printf("WARNING: mrc721 declaration overlay cap %d exceeded — evicted txid %s (oldest first)", overlayCap, age.txid)
	}
}
