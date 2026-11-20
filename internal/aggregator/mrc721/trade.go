package mrc721

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
)

const (
	keyTrade        = "trade:"
	keyTradeTime    = "ttime:"
	keyPendingTrade = "pendingtrade:"
)

// TradeRecord is one verified MRC-721 settlement record (Contract C3.5).
type TradeRecord struct {
	TxId            string `json:"txId"`
	ItemPinId       string `json:"itemPinId"`
	ItemName        string `json:"itemName,omitempty"`
	Cover           string `json:"cover,omitempty"`
	CollectionPinId string `json:"collectionPinId,omitempty"`
	SellerAddress   string `json:"sellerAddress"`
	SellerMetaId    string `json:"sellerMetaId,omitempty"`
	BuyerAddress    string `json:"buyerAddress"`
	BuyerMetaId     string `json:"buyerMetaId,omitempty"`
	Currency        string `json:"currency"` // "me" | "fiat:CNY" | "space"
	AmountMilli     *int64 `json:"amountMilli,omitempty"`
	AmountCents     *int64 `json:"amountCents,omitempty"`
	PriceSats       *int64 `json:"priceSats,omitempty"`
	SettledAt       int64  `json:"settledAt"`
	VerifiedSource  string `json:"verifiedSource"` // "abs_escrow" | "escrow_bot" | "chain_offer"
}

// CollectionTradeStats is the aggregated trade summary for one collection (Contract C3.2).
type CollectionTradeStats struct {
	TotalTrades          int    `json:"totalTrades"`
	Trades24h            int    `json:"trades24h"`
	VolumeMeMilli24h     int64  `json:"volumeMeMilli24h"`
	LastSalePriceMeMilli *int64 `json:"lastSalePriceMeMilli"`
	LastSaleAt           *int64 `json:"lastSaleAt"`
}

type tradePayload struct {
	TxId            string
	ItemPinId       string
	CollectionPinId string
	SellerAddress   string
	SellerMetaId    string
	BuyerAddress    string
	BuyerMetaId     string
	Currency        string
	AmountMilli     *int64
	AmountCents     *int64
	PriceSats       *int64
	SettledAt       int64
	VerifiedSource  string
}

func tradeKey(itemPinId, txId string) []byte {
	return []byte(keyTrade + strings.TrimSpace(itemPinId) + ":" + strings.ToLower(strings.TrimSpace(txId)))
}

func pendingTradeKey(itemPinId, txId string) []byte {
	return []byte(keyPendingTrade + strings.TrimSpace(itemPinId) + ":" + strings.ToLower(strings.TrimSpace(txId)))
}

func tradeTimeKey(settledAt int64, itemPinId, txId string) []byte {
	var ts uint64
	if settledAt > 0 {
		ts = uint64(settledAt)
	}
	return []byte(keyTradeTime + invertedSeq(ts) + ":" + strings.TrimSpace(itemPinId) + ":" + strings.ToLower(strings.TrimSpace(txId)))
}

func parseTradePayload(raw []byte) (*tradePayload, error) {
	fields, err := decodePayloadObject(raw)
	if err != nil {
		return nil, err
	}
	p := &tradePayload{}
	var ok bool
	if p.TxId, ok = payloadString(fields, "txId"); !ok {
		if p.TxId, ok = payloadString(fields, "deliveryTxId"); !ok {
			return nil, fmt.Errorf("missing txId")
		}
	}
	p.TxId = strings.ToLower(strings.TrimSpace(p.TxId))
	if !hex64Pattern.MatchString(p.TxId) {
		return nil, fmt.Errorf("invalid txId %q", p.TxId)
	}
	if p.ItemPinId, ok = payloadString(fields, "itemPinId"); !ok {
		return nil, fmt.Errorf("missing itemPinId")
	}
	if !pinIdPattern.MatchString(p.ItemPinId) {
		return nil, fmt.Errorf("invalid itemPinId %q", p.ItemPinId)
	}
	p.CollectionPinId, _ = payloadString(fields, "collectionPinId")
	p.SellerAddress, _ = payloadString(fields, "sellerAddress")
	p.SellerMetaId, _ = payloadString(fields, "sellerMetaId")
	p.BuyerAddress, _ = payloadString(fields, "buyerAddress")
	p.BuyerMetaId, _ = payloadString(fields, "buyerMetaId")
	p.Currency, _ = payloadString(fields, "currency")
	p.VerifiedSource, _ = payloadString(fields, "verifiedSource")
	if v, ok := payloadInt(fields, "amountMilli"); ok && v > 0 {
		p.AmountMilli = &v
	}
	if v, ok := payloadInt(fields, "amountCents"); ok && v > 0 {
		p.AmountCents = &v
	}
	if v, ok := payloadInt(fields, "priceSats"); ok && v > 0 {
		p.PriceSats = &v
	}
	if p.Currency == "" {
		switch {
		case p.AmountMilli != nil:
			p.Currency = "me"
		case p.AmountCents != nil:
			p.Currency = "fiat:CNY"
		case p.PriceSats != nil:
			p.Currency = "space"
		default:
			p.Currency = "me"
		}
	}
	if p.VerifiedSource == "" {
		p.VerifiedSource = "escrow_bot"
	}
	p.SettledAt, _ = payloadInt(fields, "settledAt")
	if p.SettledAt > 100000000000 {
		p.SettledAt /= 1000
	}
	return p, nil
}

func (a *Aggregator) loadTrade(itemPinId, txId string) (*TradeRecord, error) {
	if a.store == nil {
		return nil, nil
	}
	raw, err := a.store.Get(Namespace, tradeKey(itemPinId, txId))
	if err != nil || len(raw) == 0 {
		return nil, nil
	}
	var rec TradeRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (a *Aggregator) saveTradeLocked(rec *TradeRecord) error {
	if rec == nil || a.store == nil {
		return nil
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := a.store.Set(Namespace, tradeKey(rec.ItemPinId, rec.TxId), raw); err != nil {
		return err
	}
	return a.store.Set(Namespace, tradeTimeKey(rec.SettledAt, rec.ItemPinId, rec.TxId), []byte("1"))
}

// ingestTradePin processes /protocols/mrc721trade pins (callers hold a.mu).
func (a *Aggregator) ingestTradePin(pin *aggregator.PinInscription) (bool, error) {
	if pin == nil || a.store == nil {
		return false, nil
	}
	if normalisedPinPath(pin.Path) != PathTrade {
		return false, nil
	}
	if strings.ToLower(strings.TrimSpace(pin.Operation)) != OperationCreate {
		return false, nil
	}
	payload, err := parseTradePayload(pinPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: mrc721 trade pin %s skipped: %v", strings.TrimSpace(pin.Id), err)
		return false, nil
	}
	if existing, _ := a.loadTrade(payload.ItemPinId, payload.TxId); existing != nil {
		return false, nil
	}
	settledAt := payload.SettledAt
	if settledAt <= 0 {
		settledAt = unixSeconds(pin.Timestamp)
	}
	rec := &TradeRecord{
		TxId:            payload.TxId,
		ItemPinId:       payload.ItemPinId,
		CollectionPinId: payload.CollectionPinId,
		SellerAddress:   payload.SellerAddress,
		SellerMetaId:    payload.SellerMetaId,
		BuyerAddress:    payload.BuyerAddress,
		BuyerMetaId:     payload.BuyerMetaId,
		Currency:        payload.Currency,
		AmountMilli:     payload.AmountMilli,
		AmountCents:     payload.AmountCents,
		PriceSats:       payload.PriceSats,
		SettledAt:       settledAt,
		VerifiedSource:  payload.VerifiedSource,
	}
	if a.nftReader != nil {
		tr, found := a.nftReader.ItemTransferByTx(payload.ItemPinId, payload.TxId)
		if !found {
			// Transfer may not have landed yet; stage until OnItemTransferred verifies it.
			raw, _ := json.Marshal(rec)
			if len(raw) > 0 {
				_ = a.store.Set(Namespace, pendingTradeKey(rec.ItemPinId, rec.TxId), raw)
			}
			return false, nil
		}
		if !a.tradeMatchesTransferLocked(rec, tr.FromAddress, tr.ToAddress) {
			log.Printf("WARNING: mrc721 trade pin %s rejected: transfer mismatch (%s->%s vs %s->%s)",
				pin.Id, tr.FromAddress, tr.ToAddress, rec.SellerAddress, rec.BuyerAddress)
			return false, nil
		}
		a.enrichTradeFromTransferLocked(rec, tr.FromAddress, tr.ToAddress)
	} else {
		a.enrichTradeMetadataLocked(rec)
	}
	if err := a.saveTradeLocked(rec); err != nil {
		return false, err
	}
	_ = a.store.Delete(Namespace, pendingTradeKey(rec.ItemPinId, rec.TxId))
	return true, nil
}

func (a *Aggregator) tradeMatchesTransferLocked(rec *TradeRecord, fromAddr, toAddr string) bool {
	if rec.SellerAddress != "" && fromAddr != "" && rec.SellerAddress != fromAddr {
		return false
	}
	if rec.BuyerAddress != "" && toAddr != "" && rec.BuyerAddress != toAddr {
		return false
	}
	return toAddr != ""
}

func (a *Aggregator) enrichTradeFromTransferLocked(rec *TradeRecord, fromAddr, toAddr string) {
	if rec.SellerAddress == "" {
		rec.SellerAddress = fromAddr
	}
	if rec.BuyerAddress == "" {
		rec.BuyerAddress = toAddr
	}
	a.enrichTradeMetadataLocked(rec)
}

func (a *Aggregator) enrichTradeMetadataLocked(rec *TradeRecord) {
	if a.nftReader != nil {
		if info := a.nftReader.ItemSummary(rec.ItemPinId, a.zeroConfTrade); info != nil {
			if rec.CollectionPinId == "" {
				rec.CollectionPinId = info.CollectionPinId
			}
			if rec.ItemName == "" {
				rec.ItemName = info.Name
			}
			if rec.Cover == "" {
				rec.Cover = info.Cover
			}
		}
	}
	if a.profileReader != nil {
		if rec.SellerMetaId == "" && rec.SellerAddress != "" {
			if p := a.profileReader.LookupProfile(rec.SellerAddress); p != nil {
				rec.SellerMetaId = firstNonEmpty(p.GlobalMetaId, p.MetaId)
			}
		}
		if rec.BuyerMetaId == "" && rec.BuyerAddress != "" {
			if p := a.profileReader.LookupProfile(rec.BuyerAddress); p != nil {
				rec.BuyerMetaId = firstNonEmpty(p.GlobalMetaId, p.MetaId)
			}
		}
	}
}

// OnItemTransferred handles mrc721nft transfer/burn events (REQ-B-03 + REQ-B-07).
// Must NOT be called while holding mrc721nft mutexes.
func (a *Aggregator) OnItemTransferred(ev mrc721nft.TransferEvent) {
	if a == nil || a.store == nil || strings.TrimSpace(ev.ItemPinId) == "" {
		return
	}
	a.mu.Lock()
	changed := false

	// 1. Auto-invalidate active sell intents on transfer to another address or burn.
	if ev.Confirmed {
		if rec, err := a.loadSellIntent(ev.ItemPinId); err == nil && rec != nil && rec.Active {
			if ev.Burned || ev.ToAddress != rec.SellerAddress {
				rec.Active = false
				rec.InactiveReason = "transferred"
				if err := a.saveSellIntent(rec); err != nil {
					log.Printf("WARNING: mrc721 invalidate sell intent %s on transfer: %v", ev.ItemPinId, err)
				} else {
					changed = true
				}
			}
		}
		delete(a.overlayInvalidated, ev.ItemPinId)
	} else {
		// Mempool transfer/burn: mark overlay sell intents and track confirmed invalidation in memory.
		for _, entry := range a.overlayDecls {
			if entry.sell != nil && entry.sell.ItemPinId == ev.ItemPinId && entry.sell.Active {
				if ev.Burned || ev.ToAddress != entry.sell.SellerAddress {
					entry.sell.Active = false
					entry.sell.InactiveReason = "transferred"
					changed = true
				}
			}
		}
		if rec, err := a.loadSellIntent(ev.ItemPinId); err == nil && rec != nil && rec.Active {
			if ev.Burned || ev.ToAddress != rec.SellerAddress {
				a.overlayInvalidated[ev.ItemPinId] = "transferred"
				changed = true
			}
		}
	}

	// 2. Promote any staged /protocols/mrc721trade awaiting this transfer tx,
	// or record an offer-based settlement if an active /protocols/mrc721offer existed.
	txId := strings.ToLower(strings.TrimSpace(ev.TxId))
	if txId != "" && !ev.Burned && ev.ToAddress != "" {
		if existing, _ := a.loadTrade(ev.ItemPinId, txId); existing == nil {
			if raw, err := a.store.Get(Namespace, pendingTradeKey(ev.ItemPinId, txId)); err == nil && len(raw) > 0 {
				var rec TradeRecord
				if jsonErr := json.Unmarshal(raw, &rec); jsonErr == nil && a.tradeMatchesTransferLocked(&rec, ev.FromAddress, ev.ToAddress) {
					a.enrichTradeFromTransferLocked(&rec, ev.FromAddress, ev.ToAddress)
					if rec.CollectionPinId == "" {
						rec.CollectionPinId = ev.CollectionPinId
					}
					if err := a.saveTradeLocked(&rec); err == nil {
						_ = a.store.Delete(Namespace, pendingTradeKey(ev.ItemPinId, txId))
						changed = true
					}
				}
			} else if ev.Confirmed && ev.FromAddress != ev.ToAddress {
				if offer, err := a.loadOffer(ev.ItemPinId); err == nil && offer != nil && offer.Active && offer.PriceSats > 0 {
					priceSats := offer.PriceSats
					settledAt := ev.Time
					if settledAt <= 0 {
						settledAt = a.nowUnix()
					}
					rec := &TradeRecord{
						TxId:            txId,
						ItemPinId:       ev.ItemPinId,
						CollectionPinId: ev.CollectionPinId,
						SellerAddress:   ev.FromAddress,
						BuyerAddress:    ev.ToAddress,
						Currency:        "space",
						PriceSats:       &priceSats,
						SettledAt:       settledAt,
						VerifiedSource:  "chain_offer",
					}
					a.enrichTradeMetadataLocked(rec)
					if err := a.saveTradeLocked(rec); err == nil {
						changed = true
					}
				}
			}
		}
	}
	a.mu.Unlock()

	if changed {
		a.invalidateMarketCache()
	}
}

func (a *Aggregator) listAllTrades() ([]*TradeRecord, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	trades := make([]*TradeRecord, 0)
	seen := make(map[string]struct{})
	err := a.store.ScanPrefix(Namespace, []byte(keyTrade), func(key, value []byte) error {
		var rec TradeRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		dedupKey := rec.ItemPinId + ":" + strings.ToLower(rec.TxId)
		if _, ok := seen[dedupKey]; ok {
			return nil
		}
		seen[dedupKey] = struct{}{}
		trades = append(trades, &rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(trades, func(i, j int) bool {
		if trades[i].SettledAt != trades[j].SettledAt {
			return trades[i].SettledAt > trades[j].SettledAt
		}
		return trades[i].TxId > trades[j].TxId
	})
	return trades, nil
}

func (a *Aggregator) collectionTradeStatsMap(nowSec int64) map[string]CollectionTradeStats {
	trades, err := a.listAllTrades()
	if err != nil || len(trades) == 0 {
		return map[string]CollectionTradeStats{}
	}
	cutoff24h := nowSec - 86400
	out := make(map[string]CollectionTradeStats)
	for _, tr := range trades {
		cid := strings.ToLower(strings.TrimSpace(tr.CollectionPinId))
		if cid == "" {
			continue
		}
		st := out[cid]
		st.TotalTrades++
		if tr.SettledAt >= cutoff24h {
			st.Trades24h++
			if tr.AmountMilli != nil && *tr.AmountMilli > 0 {
				st.VolumeMeMilli24h += *tr.AmountMilli
			}
		}
		if st.LastSaleAt == nil || tr.SettledAt > *st.LastSaleAt {
			ts := tr.SettledAt
			st.LastSaleAt = &ts
			if tr.AmountMilli != nil && *tr.AmountMilli > 0 {
				v := *tr.AmountMilli
				st.LastSalePriceMeMilli = &v
			}
		}
		out[cid] = st
	}
	return out
}

// handleMarketTrades serves GET /api/mrc721/market/trades (Contract C3.5).
func (a *Aggregator) handleMarketTrades(c *gin.Context) {
	if a == nil || a.store == nil {
		respondMarketErr(c, 50000, "mrc721 index unavailable")
		return
	}
	size, err := parseListSize(c.Query("size"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: size")
		return
	}
	offset := parseOffsetCursor(c.Query("cursor"))
	collectionPinId := strings.TrimSpace(c.Query("collectionPinId"))
	itemPinId := strings.TrimSpace(c.Query("itemPinId"))
	party := strings.TrimSpace(c.Query("party"))
	var partyAddrs map[string]struct{}
	if party != "" {
		partyAddrs = a.resolvePartyAddresses(party)
	}

	trades, err := a.listAllTrades()
	if err != nil {
		respondMarketErr(c, 50000, "failed to load trades")
		return
	}
	filtered := make([]*TradeRecord, 0, len(trades))
	for _, tr := range trades {
		if collectionPinId != "" && !strings.EqualFold(tr.CollectionPinId, collectionPinId) {
			continue
		}
		if itemPinId != "" && tr.ItemPinId != itemPinId {
			continue
		}
		if party != "" && !tradeMatchesParty(tr, party, partyAddrs) {
			continue
		}
		filtered = append(filtered, tr)
	}
	total := len(filtered)
	if offset > total {
		offset = total
	}
	end := offset + size
	if end > total {
		end = total
	}
	nextCursor := ""
	if end < total {
		nextCursor = strconv.Itoa(end)
	}
	respondMarketOK(c, gin.H{
		"list":   filtered[offset:end],
		"total":  total,
		"cursor": nextCursor,
	})
}

func (a *Aggregator) resolvePartyAddresses(party string) map[string]struct{} {
	addrs := map[string]struct{}{strings.ToLower(party): {}}
	a.mu.Lock()
	pr := a.profileReader
	a.mu.Unlock()
	if pr != nil {
		for _, addr := range pr.ResolveAddressesByGlobalMetaId(party) {
			if addr != "" {
				addrs[strings.ToLower(addr)] = struct{}{}
			}
		}
		if p := pr.LookupProfile(party); p != nil {
			if p.Address != "" {
				addrs[strings.ToLower(p.Address)] = struct{}{}
			}
			if p.GlobalMetaId != "" {
				addrs[strings.ToLower(p.GlobalMetaId)] = struct{}{}
			}
			if p.MetaId != "" {
				addrs[strings.ToLower(p.MetaId)] = struct{}{}
			}
		}
	}
	return addrs
}

func tradeMatchesParty(tr *TradeRecord, raw string, addrs map[string]struct{}) bool {
	candidates := []string{tr.SellerAddress, tr.SellerMetaId, tr.BuyerAddress, tr.BuyerMetaId}
	for _, cand := range candidates {
		if cand == "" {
			continue
		}
		if strings.EqualFold(cand, raw) {
			return true
		}
		if _, ok := addrs[strings.ToLower(cand)]; ok {
			return true
		}
	}
	return false
}

func respondMarketOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    data,
	})
}

func respondMarketErr(c *gin.Context, code int, message string) {
	c.JSON(http.StatusOK, gin.H{
		"code":    code,
		"message": message,
	})
}
