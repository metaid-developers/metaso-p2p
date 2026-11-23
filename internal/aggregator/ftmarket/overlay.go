package ftmarket

import (
	"context"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

const overlayCap = 50000

type overlayDeclEntry struct {
	pinId       string
	txid        string
	chainName   string
	firstSeenAt int64
	sell        *SellListingRecord
	trade       *TradeRecord
}

// ReconcileMempoolRound implements aggregator.MempoolOverlayReconciler.
// Entries whose txid is no longer in the observed mempool round are dropped.
func (a *Aggregator) ReconcileMempoolRound(chainName string, observedTxIDs []string) {
	if a == nil || a.store == nil {
		return
	}
	observed := make(map[string]struct{}, len(observedTxIDs))
	for _, txid := range observedTxIDs {
		t := strings.ToLower(strings.TrimSpace(txid))
		if t != "" {
			observed[t] = struct{}{}
		}
	}
	a.mu.Lock()
	changed := false
	for pinId, entry := range a.overlayDecls {
		if chainName != "" && entry.chainName != "" && !strings.EqualFold(entry.chainName, chainName) {
			continue
		}
		if _, ok := observed[entry.txid]; !ok {
			if entry.sell != nil {
				delete(a.overlayInvalidated, entry.sell.ListingKey)
			}
			delete(a.overlayDecls, pinId)
			delete(a.overlayTxids, entry.txid)
			changed = true
		}
	}
	for k, tr := range a.overlayPendingTrades {
		if chainName != "" && tr.Chain != "" && !strings.EqualFold(tr.Chain, chainName) {
			continue
		}
		if _, ok := observed[strings.ToLower(tr.TxId)]; !ok {
			delete(a.overlayPendingTrades, k)
		}
	}
	a.mu.Unlock()
	if changed {
		a.invalidateMarketCache()
	}
}

func (a *Aggregator) dropOverlayForTxLocked(txid string) {
	txid = strings.ToLower(strings.TrimSpace(txid))
	if txid == "" {
		return
	}
	delete(a.overlayTxids, txid)
	for pinId, entry := range a.overlayDecls {
		if entry.txid == txid {
			delete(a.overlayDecls, pinId)
		}
	}
}

func (a *Aggregator) recordMempoolDeclarationLocked(pin *aggregator.PinInscription) bool {
	if pin == nil || a.store == nil || !a.zeroConfTrade || len(pin.Id) == 0 {
		return false
	}
	if _, ok := a.overlayDecls[pin.Id]; ok {
		return false
	}
	path := normalisedPinPath(pin.Path)
	if strings.ToLower(strings.TrimSpace(pin.Operation)) != OperationCreate {
		return false
	}
	now := a.nowUnix()
	entry := &overlayDeclEntry{
		pinId:       pin.Id,
		txid:        txIdOfPinId(pin.Id),
		chainName:   strings.TrimSpace(pin.ChainName),
		firstSeenAt: now,
	}
	switch path {
	case PathFtSell:
		payload, err := parseSellPayload(pinPayloadBytes(pin))
		if err != nil {
			log.Printf("WARNING: ftmarket overlay sell pin %s skipped: %v", pin.Id, err)
			return false
		}
		if payload.Asset != nil && payload.Asset.Chain == "mvc" && payload.Asset.Codehash != nil && payload.Asset.Genesis != nil && !hex40Pattern.MatchString(*payload.Asset.Genesis) {
			canonGen, sid := a.resolveMvcSensibleIfNeededLocked(context.Background(), *payload.Asset.Codehash, *payload.Asset.Genesis)
			if canonGen != *payload.Asset.Genesis {
				payload.Asset.Genesis = &canonGen
				payload.Asset.AssetKey = "mvc-ft:" + *payload.Asset.Codehash + ":" + canonGen
				if sid != "" {
					payload.Asset.SensibleId = &sid
				}
				payload.ListingKey = normalizeListingKeyAssetPrefix(payload.ListingKey)
				if payload.Lot != nil {
					payload.ListingKey = canonicalizeListingKey(payload.ListingKey, payload.Asset.AssetKey, payload.Lot.UtxoOutpoint)
				}
			}
		}
		if existing, _ := a.loadListing(payload.ListingKey); existing != nil && existing.SellerAddress != "" {
			if !sameSellerAddress(existing.SellerAddress, payload.SellerAddress) {
				log.Printf("WARNING: ftmarket overlay sell pin %s skipped: seller mismatch", pin.Id)
				return false
			}
		}
		if payload.Active && payload.Lot != nil && payload.Lot.UtxoOutpoint != "" {
			if conflict, reason := a.hasConflictingUTXORailLocked(payload.ListingKey, payload.Lot.UtxoOutpoint, payload.Rails); conflict {
				log.Printf("WARNING: ftmarket overlay sell pin %s skipped: %s", pin.Id, reason)
				return false
			}
		}
		rec := &SellListingRecord{
			ListingKey:       payload.ListingKey,
			SellPinId:        strings.TrimSpace(pin.Id),
			Active:           payload.Active,
			EffectiveActive:  payload.Active,
			Asset:            payload.Asset,
			Lot:              payload.Lot,
			SellerAddress:    payload.SellerAddress,
			SellerBtcAddress: payload.SellerBtcAddress,
			SellerMetaId:     payload.SellerMetaId,
			Rails:            payload.Rails,
			AcceptedRails:    payload.AcceptedRails,
			BatchGroupId:     payload.BatchGroupId,
			BatchIndex:       payload.BatchIndex,
			BatchTotal:       payload.BatchTotal,
			Note:             payload.Note,
			CreatedAt:        payload.CreatedAt,
			ExpiresAt:        payload.ExpiresAt,
			ChainName:        strings.TrimSpace(pin.ChainName),
			Height:           0,
			TxID:             txIdOfPinId(pin.Id),
			UpdatedAt:        unixSeconds(pin.Timestamp),
			Overlay:          true,
			OverlayFirstSeenAt: now,
		}
		if rec.UpdatedAt == 0 {
			rec.UpdatedAt = now
		}
		if !rec.Active {
			rec.EffectiveActive = false
			rec.InactiveReason = "canceled"
		} else if rec.ExpiresAt > 0 && now >= rec.ExpiresAt {
			rec.EffectiveActive = false
			rec.InactiveReason = "expired"
		} else if a.utxoVerifier != nil {
			res := a.checkUTXOLivenessCachedLocked(context.Background(), rec)
			if res.Checked && !res.Alive {
				rec.EffectiveActive = false
				rec.InactiveReason = firstNonEmpty(res.Reason, "utxo_spent")
				a.overlayInvalidated[rec.ListingKey] = rec.InactiveReason
			} else {
				delete(a.overlayInvalidated, rec.ListingKey)
			}
		} else {
			delete(a.overlayInvalidated, rec.ListingKey)
		}
		entry.sell = rec

	case PathFtTrade:
		payload, err := parseTradePayload(pinPayloadBytes(pin))
		if err != nil {
			log.Printf("WARNING: ftmarket overlay trade pin %s skipped: %v", pin.Id, err)
			return false
		}
		if existing, _ := a.loadTrade(payload.TxId, payload.ListingKey); existing != nil {
			return false
		}
		pub := extractTradePublisher(pin)
		if !a.isAuthorizedTradePublisherLocked(pub, payload.SellerAddress, payload.BuyerAddress) {
			log.Printf("WARNING: ftmarket overlay trade pin %s rejected: unauthorized publisher", pin.Id)
			return false
		}
		rec := buildTradeRecordFromPayload(pin, payload, false)
		entry.trade = rec

	default:
		return false
	}

	a.overlayDecls[entry.pinId] = entry
	a.overlayTxids[entry.txid] = entry.firstSeenAt
	a.evictOverlayCapLocked()
	return true
}

func (a *Aggregator) sortedOverlaySellRecordsLocked() []*SellListingRecord {
	if len(a.overlayDecls) == 0 {
		return nil
	}
	list := make([]*SellListingRecord, 0, len(a.overlayDecls))
	for _, entry := range a.overlayDecls {
		if entry != nil && entry.sell != nil {
			list = append(list, entry.sell)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].UpdatedAt != list[j].UpdatedAt {
			return list[i].UpdatedAt < list[j].UpdatedAt
		}
		if list[i].OverlayFirstSeenAt != list[j].OverlayFirstSeenAt {
			return list[i].OverlayFirstSeenAt < list[j].OverlayFirstSeenAt
		}
		return list[i].SellPinId < list[j].SellPinId
	})
	return list
}

func (a *Aggregator) evictOverlayCapLocked() {
	if len(a.overlayDecls) <= overlayCap {
		return
	}
	entries := make([]*overlayDeclEntry, 0, len(a.overlayDecls))
	for _, e := range a.overlayDecls {
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].firstSeenAt != entries[j].firstSeenAt {
			return entries[i].firstSeenAt < entries[j].firstSeenAt
		}
		return entries[i].pinId < entries[j].pinId
	})
	excess := len(entries) - overlayCap
	for i := 0; i < excess; i++ {
		delete(a.overlayDecls, entries[i].pinId)
		delete(a.overlayTxids, entries[i].txid)
	}
}

func (a *Aggregator) hasOverlayPending(txid string) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.overlayTxids[strings.ToLower(strings.TrimSpace(txid))]
	return ok
}

// StartUTXOSweeper runs periodic background liveness verification on active listings
// until stop is closed.
func (a *Aggregator) StartUTXOSweeper(interval time.Duration, stop <-chan struct{}) {
	if a == nil || interval <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				a.VerifyActiveListings(ctx)
				cancel()
			}
		}
	}()
}
