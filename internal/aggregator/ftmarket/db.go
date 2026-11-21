package ftmarket

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Pebble key prefixes in the ftmarket namespace:
//
//	meta:seq                                  global monotonic sequence (8-byte BE)
//	listing:<listingKey>                      latest SellListingRecord JSON
//	ltime:<invSeq16>:<listingKey>             recency index, newest first
//	idx:asset:<assetKey>:<listingKey>         secondary index by assetKey
//	idx:symbol:<UPPER_SYM>:<assetKey>:<key>   secondary index by uppercase symbol
//	idx:seller:<lower_addr>:<listingKey>      secondary index by sellerAddress
//	idx:utxo:<outpoint>:<listingKey>          secondary index by utxoOutpoint
//	asset:<assetKey>                          TokenCatalogRecord JSON
const (
	keySeq         = "meta:seq"
	keyListing     = "listing:"
	keyListingTime = "ltime:"
	keyIdxAsset    = "idx:asset:"
	keyIdxSymbol   = "idx:symbol:"
	keyIdxSeller   = "idx:seller:"
	keyIdxUtxo     = "idx:utxo:"
	keyAsset       = "asset:"
)

func listingStorageKey(listingKey string) []byte {
	return []byte(keyListing + listingKey)
}

func listingTimeKey(seq uint64, listingKey string) []byte {
	return []byte(keyListingTime + invertedSeq(seq) + ":" + listingKey)
}

func idxAssetKey(assetKey, listingKey string) []byte {
	return []byte(keyIdxAsset + strings.ToLower(assetKey) + ":" + listingKey)
}

func idxSymbolKey(symbol, assetKey, listingKey string) []byte {
	return []byte(keyIdxSymbol + strings.ToUpper(strings.TrimSpace(symbol)) + ":" + strings.ToLower(assetKey) + ":" + listingKey)
}

func idxSellerKey(sellerAddr, listingKey string) []byte {
	return []byte(keyIdxSeller + strings.ToLower(strings.TrimSpace(sellerAddr)) + ":" + listingKey)
}

func idxUtxoKey(outpoint, listingKey string) []byte {
	return []byte(keyIdxUtxo + strings.ToLower(strings.TrimSpace(outpoint)) + ":" + listingKey)
}

func assetStorageKey(assetKey string) []byte {
	return []byte(keyAsset + strings.ToLower(strings.TrimSpace(assetKey)))
}

func invertedSeq(seq uint64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, ^seq)
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf))
}

func (a *Aggregator) nextSeq() (uint64, error) {
	raw, err := a.store.Get(Namespace, []byte(keySeq))
	if err != nil || len(raw) == 0 {
		return 1, a.store.Set(Namespace, []byte(keySeq), encodeSeq(1))
	}
	next := binary.BigEndian.Uint64(raw) + 1
	return next, a.store.Set(Namespace, []byte(keySeq), encodeSeq(next))
}

func encodeSeq(seq uint64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, seq)
	return buf
}

func (a *Aggregator) loadListing(listingKey string) (*SellListingRecord, error) {
	if a.store == nil {
		return nil, nil
	}
	raw, err := a.store.Get(Namespace, listingStorageKey(listingKey))
	if err != nil || len(raw) == 0 {
		return nil, nil
	}
	var rec SellListingRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("corrupt ft listing %s: %w", listingKey, err)
	}
	return &rec, nil
}

func (a *Aggregator) saveListingLocked(rec *SellListingRecord, prev *SellListingRecord) error {
	if a.store == nil || rec == nil {
		return nil
	}
	if prev != nil {
		if prev.Seq > 0 {
			_ = a.store.Delete(Namespace, listingTimeKey(prev.Seq, prev.ListingKey))
		}
		a.deleteSecondaryIndexesLocked(prev)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := a.store.Set(Namespace, listingStorageKey(rec.ListingKey), raw); err != nil {
		return err
	}
	if err := a.store.Set(Namespace, listingTimeKey(rec.Seq, rec.ListingKey), []byte("1")); err != nil {
		return err
	}
	a.writeSecondaryIndexesLocked(rec)
	if rec.Asset != nil && rec.Asset.AssetKey != "" {
		a.saveTokenCatalogLocked(rec.Asset, rec.UpdatedAt)
	}
	return nil
}

func (a *Aggregator) writeSecondaryIndexesLocked(rec *SellListingRecord) {
	if rec == nil || a.store == nil {
		return
	}
	if rec.Asset != nil && rec.Asset.AssetKey != "" {
		_ = a.store.Set(Namespace, idxAssetKey(rec.Asset.AssetKey, rec.ListingKey), []byte("1"))
		if rec.Asset.Symbol != "" {
			_ = a.store.Set(Namespace, idxSymbolKey(rec.Asset.Symbol, rec.Asset.AssetKey, rec.ListingKey), []byte("1"))
		}
	}
	if rec.SellerAddress != "" {
		_ = a.store.Set(Namespace, idxSellerKey(rec.SellerAddress, rec.ListingKey), []byte("1"))
	}
	if rec.SellerBtcAddress != "" {
		_ = a.store.Set(Namespace, idxSellerKey(rec.SellerBtcAddress, rec.ListingKey), []byte("1"))
	}
	if rec.Lot != nil && rec.Lot.UtxoOutpoint != "" {
		_ = a.store.Set(Namespace, idxUtxoKey(rec.Lot.UtxoOutpoint, rec.ListingKey), []byte("1"))
	}
}

func (a *Aggregator) deleteSecondaryIndexesLocked(rec *SellListingRecord) {
	if rec == nil || a.store == nil {
		return
	}
	if rec.Asset != nil && rec.Asset.AssetKey != "" {
		_ = a.store.Delete(Namespace, idxAssetKey(rec.Asset.AssetKey, rec.ListingKey))
		if rec.Asset.Symbol != "" {
			_ = a.store.Delete(Namespace, idxSymbolKey(rec.Asset.Symbol, rec.Asset.AssetKey, rec.ListingKey))
		}
	}
	if rec.SellerAddress != "" {
		_ = a.store.Delete(Namespace, idxSellerKey(rec.SellerAddress, rec.ListingKey))
	}
	if rec.SellerBtcAddress != "" {
		_ = a.store.Delete(Namespace, idxSellerKey(rec.SellerBtcAddress, rec.ListingKey))
	}
	if rec.Lot != nil && rec.Lot.UtxoOutpoint != "" {
		_ = a.store.Delete(Namespace, idxUtxoKey(rec.Lot.UtxoOutpoint, rec.ListingKey))
	}
}

func (a *Aggregator) saveTokenCatalogLocked(asset *AssetInfo, ts int64) {
	if a.store == nil || asset == nil || asset.AssetKey == "" {
		return
	}
	existing := a.loadTokenCatalog(asset.AssetKey)
	merged := *asset
	if existing != nil {
		if merged.TokenName == "" {
			merged.TokenName = existing.Asset.TokenName
		}
		if merged.Icon == "" {
			merged.Icon = existing.Asset.Icon
		}
		if merged.Decimals == 0 && existing.Asset.Decimals > 0 {
			merged.Decimals = existing.Asset.Decimals
		}
	}
	raw, err := json.Marshal(TokenCatalogRecord{
		Asset:     merged,
		UpdatedAt: ts,
	})
	if err == nil {
		_ = a.store.Set(Namespace, assetStorageKey(merged.AssetKey), raw)
	}
}

func (a *Aggregator) loadTokenCatalog(assetKey string) *TokenCatalogRecord {
	if a.store == nil || assetKey == "" {
		return nil
	}
	raw, err := a.store.Get(Namespace, assetStorageKey(assetKey))
	if err != nil || len(raw) == 0 {
		return nil
	}
	var rec TokenCatalogRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil
	}
	return &rec
}

func (a *Aggregator) listTokenCatalog() []TokenCatalogRecord {
	if a.store == nil {
		return nil
	}
	var out []TokenCatalogRecord
	err := a.store.ScanPrefix(Namespace, []byte(keyAsset), func(_, val []byte) error {
		var rec TokenCatalogRecord
		if err := json.Unmarshal(val, &rec); err == nil && rec.Asset.AssetKey != "" {
			out = append(out, rec)
		}
		return nil
	})
	if err != nil {
		return nil
	}
	return out
}

// ingestSellPin folds one confirmed /protocols/ft-sell pin into PebbleDB.
// Callers must hold a.mu.
func (a *Aggregator) ingestSellPin(pin *aggregator.PinInscription) (bool, error) {
	if pin == nil || a.store == nil {
		return false, nil
	}
	if normalisedPinPath(pin.Path) != PathFtSell {
		return false, nil
	}
	if strings.ToLower(strings.TrimSpace(pin.Operation)) != OperationCreate {
		return false, nil
	}

	sellPinId := strings.TrimSpace(pin.Id)
	payload, err := parseSellPayload(pinPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: ftmarket sell pin %s skipped: %v", sellPinId, err)
		return false, nil
	}

	existing, err := a.loadListing(payload.ListingKey)
	if err != nil {
		log.Printf("WARNING: ftmarket sell pin %s skipped: %v", sellPinId, err)
		return false, nil
	}

	height := pin.GenesisHeight
	updatedAt := unixSeconds(pin.Timestamp)
	if existing != nil {
		if existing.SellPinId == sellPinId {
			return false, nil
		}
		if existing.SellerAddress != "" && !sameSellerAddress(existing.SellerAddress, payload.SellerAddress) {
			log.Printf("WARNING: ftmarket sell pin %s skipped: sellerAddress %q mismatches existing %q", sellPinId, payload.SellerAddress, existing.SellerAddress)
			return false, nil
		}
		if !chainOrderNewer(existing.Height, existing.UpdatedAt, existing.SellPinId, height, updatedAt, sellPinId) {
			return false, nil
		}
	}

	// Enforce cross-listing mutual exclusion on the same utxoOutpoint when active.
	if payload.Active && payload.Lot != nil && payload.Lot.UtxoOutpoint != "" {
		if conflict, reason := a.hasConflictingUTXORailLocked(payload.ListingKey, payload.Lot.UtxoOutpoint, payload.Rails); conflict {
			log.Printf("WARNING: ftmarket sell pin %s skipped: %s", sellPinId, reason)
			return false, nil
		}
	}

	seq, err := a.nextSeq()
	if err != nil {
		return false, err
	}
	rec := &SellListingRecord{
		ListingKey:       payload.ListingKey,
		SellPinId:        sellPinId,
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
		Height:           height,
		TxID:             txIdOfPinId(sellPinId),
		UpdatedAt:        updatedAt,
		Seq:              seq,
	}

	if !rec.Active {
		rec.EffectiveActive = false
		rec.InactiveReason = "canceled"
		// Preserve asset/lot/rails metadata from prior record when cancellation omits them.
		if existing != nil {
			if rec.Asset == nil {
				rec.Asset = existing.Asset
			}
			if rec.Lot == nil {
				rec.Lot = existing.Lot
			}
			if rec.Rails == nil {
				rec.Rails = existing.Rails
			}
			if len(rec.AcceptedRails) == 0 {
				rec.AcceptedRails = existing.AcceptedRails
			}
			if rec.BatchGroupId == "" {
				rec.BatchGroupId = existing.BatchGroupId
			}
			if rec.SellerBtcAddress == "" {
				rec.SellerBtcAddress = existing.SellerBtcAddress
			}
			if rec.SellerMetaId == "" {
				rec.SellerMetaId = existing.SellerMetaId
			}
		}
	} else {
		now := a.nowUnix()
		if rec.ExpiresAt > 0 && now >= rec.ExpiresAt {
			rec.EffectiveActive = false
			rec.InactiveReason = "expired"
		} else if a.utxoVerifier != nil {
			res := a.checkUTXOLivenessCachedLocked(context.Background(), rec)
			if res.Checked && !res.Alive {
				rec.EffectiveActive = false
				rec.InactiveReason = firstNonEmpty(res.Reason, "utxo_spent")
			}
		}
		if rec.EffectiveActive {
			delete(a.overlayInvalidated, rec.ListingKey)
		}
	}

	if err := a.saveListingLocked(rec, existing); err != nil {
		return false, err
	}
	return true, nil
}

// hasConflictingUTXORailLocked prevents two active listings on the same utxoOutpoint
// from declaring incompatible rails (mvcTokenSell vs me) per Contract C1.3.
func (a *Aggregator) hasConflictingUTXORailLocked(listingKey, outpoint string, rails *SellRails) (bool, string) {
	if a.store == nil || outpoint == "" || rails == nil {
		return false, ""
	}
	prefix := keyIdxUtxo + strings.ToLower(outpoint) + ":"
	var conflictReason string
	_ = a.store.ScanPrefix(Namespace, []byte(prefix), func(key, _ []byte) error {
		otherKey := strings.TrimPrefix(string(key), prefix)
		if otherKey == "" || otherKey == listingKey {
			return nil
		}
		other, err := a.loadListing(otherKey)
		if err != nil || other == nil || !other.EffectiveActive || other.Rails == nil {
			return nil
		}
		if (rails.MvcTokenSell != nil && other.Rails.Me != nil) || (rails.Me != nil && other.Rails.MvcTokenSell != nil) {
			conflictReason = fmt.Sprintf("utxoOutpoint %s already active on conflicting rail in listing %s", outpoint, otherKey)
		}
		return nil
	})
	if conflictReason != "" {
		return true, conflictReason
	}
	return false, ""
}

func (a *Aggregator) listConfirmedListings() ([]*SellListingRecord, error) {
	if a.store == nil {
		return nil, nil
	}
	var out []*SellListingRecord
	err := a.store.ScanPrefix(Namespace, []byte(keyListing), func(_, val []byte) error {
		var rec SellListingRecord
		if err := json.Unmarshal(val, &rec); err != nil {
			return nil
		}
		out = append(out, &rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (a *Aggregator) listAllEffectiveListings(includeOverlay bool) ([]*SellListingRecord, error) {
	confirmed, err := a.listConfirmedListings()
	if err != nil {
		return nil, err
	}
	byKey := make(map[string]*SellListingRecord, len(confirmed))
	for _, rec := range confirmed {
		if rec == nil {
			continue
		}
		byKey[rec.ListingKey] = rec
	}

	a.mu.Lock()
	if includeOverlay && a.zeroConfTrade {
		// Sort overlay entries deterministically in chain/arrival order before applying.
		overlays := a.sortedOverlaySellRecordsLocked()
		for _, ov := range overlays {
			if ov == nil {
				continue
			}
			cur := byKey[ov.ListingKey]
			if cur != nil && cur.SellerAddress != "" && !sameSellerAddress(cur.SellerAddress, ov.SellerAddress) {
				continue
			}
			if cur != nil && cur.SellPinId == ov.SellPinId {
				continue
			}
			cp := *ov
			if !cp.Active && cur != nil {
				if cp.Asset == nil {
					cp.Asset = cur.Asset
				}
				if cp.Lot == nil {
					cp.Lot = cur.Lot
				}
				if cp.Rails == nil {
					cp.Rails = cur.Rails
				}
			}
			if reason, bad := a.overlayInvalidated[cp.ListingKey]; bad {
				cp.EffectiveActive = false
				cp.InactiveReason = reason
			}
			byKey[cp.ListingKey] = &cp
		}
	}
	a.mu.Unlock()

	now := a.nowUnix()
	out := make([]*SellListingRecord, 0, len(byKey))
	for _, rec := range byKey {
		if rec.EffectiveActive && rec.ExpiresAt > 0 && now >= rec.ExpiresAt {
			cp := *rec
			cp.EffectiveActive = false
			cp.InactiveReason = "expired"
			out = append(out, &cp)
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// markListingUTXOSpent persists effectiveActive=false for a confirmed listing or records
// overlay invalidation when the listing's UTXO is spent/unavailable.
func (a *Aggregator) markListingUTXOSpent(listingKey string, overlay bool, reason string) bool {
	if reason == "" {
		reason = "utxo_spent"
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if overlay {
		if prev, exists := a.overlayInvalidated[listingKey]; exists && prev == reason {
			return false
		}
		a.overlayInvalidated[listingKey] = reason
		return true
	}
	rec, err := a.loadListing(listingKey)
	if err != nil || rec == nil || !rec.EffectiveActive {
		return false
	}
	rec.EffectiveActive = false
	rec.InactiveReason = reason
	raw, err := json.Marshal(rec)
	if err != nil {
		return false
	}
	if err := a.store.Set(Namespace, listingStorageKey(rec.ListingKey), raw); err != nil {
		return false
	}
	return true
}

func sameSellerAddress(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(a), "bc1") || strings.HasPrefix(strings.ToLower(a), "tb1") ||
		strings.HasPrefix(strings.ToLower(b), "bc1") || strings.HasPrefix(strings.ToLower(b), "tb1") {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func chainOrderNewer(curHeight, curTime int64, curPinId string, newHeight, newTime int64, newPinId string) bool {
	if newHeight < 0 && curHeight >= 0 {
		return false
	}
	if curHeight > 0 && newHeight > 0 && newHeight != curHeight {
		return newHeight > curHeight
	}
	if newTime != curTime {
		return newTime > curTime
	}
	if newHeight != curHeight {
		return newHeight > curHeight
	}
	return newPinId > curPinId
}

func pinPayloadBytes(pin *aggregator.PinInscription) []byte {
	if pin == nil {
		return nil
	}
	if len(pin.ContentBody) > 0 {
		return pin.ContentBody
	}
	return []byte(pin.ContentSummary)
}
