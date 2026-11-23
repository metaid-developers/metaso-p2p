package ftmarket

import (
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/idbotsca"
)

const (
	keyTrade     = "trade:"
	keyTradeTime = "ttime:"
)

// TradeRecord represents one verified FT trade settlement record (/protocols/ft-trade).
type TradeRecord struct {
	TxId           string  `json:"txId"`
	TradePinId     string  `json:"tradePinId,omitempty"`
	ListingKey     string  `json:"listingKey,omitempty"`
	AssetKey       string  `json:"assetKey"`
	Chain          string  `json:"chain"`
	Symbol         string  `json:"symbol,omitempty"`
	Decimals       int     `json:"decimals"`
	AmountBase     string  `json:"amountBase"`
	AmountDisplay  string  `json:"amountDisplay"`
	Rail           string  `json:"rail"` // "mvc-tokensell" | "me-escrow" | "btc-psbt" | "fiat-escrow"
	Currency       string  `json:"currency"`
	AmountMilli    *int64  `json:"amountMilli,omitempty"`
	AmountCents    *int64  `json:"amountCents,omitempty"`
	PriceSats      *int64  `json:"priceSats,omitempty"`
	UnitPriceLabel string  `json:"unitPriceLabel,omitempty"`
	SellerAddress  string  `json:"sellerAddress"`
	SellerMetaId   string  `json:"sellerMetaId,omitempty"`
	BuyerAddress   string  `json:"buyerAddress"`
	BuyerMetaId    string  `json:"buyerMetaId,omitempty"`
	SettledAt      int64   `json:"settledAt"`
	VerifiedSource string  `json:"verifiedSource"` // "abs_escrow" | "chain_tokensell" | "btc_psbt" | "escrow_bot"
	Confirmed      bool    `json:"confirmed"`
	UnitMeMilli    *string `json:"-"`
}

type tradePublisher struct {
	Address       string `json:"address,omitempty"`
	CreateAddress string `json:"createAddress,omitempty"`
	Creator       string `json:"creator,omitempty"`
	MetaId        string `json:"metaId,omitempty"`
	CreateMetaId  string `json:"createMetaId,omitempty"`
	GlobalMetaId  string `json:"globalMetaId,omitempty"`
}

type tradePayload struct {
	TxId           string
	ListingKey     string
	AssetKey       string
	Chain          string
	Symbol         string
	Decimals       int
	AmountBase     string
	AmountDisplay  string
	Rail           string
	Currency       string
	AmountMilli    *int64
	AmountCents    *int64
	PriceSats      *int64
	SellerAddress  string
	SellerMetaId   string
	BuyerAddress   string
	BuyerMetaId    string
	SettledAt      int64
	VerifiedSource string
}

func tradeStorageKey(txId, listingKey string) []byte {
	return []byte(keyTrade + strings.ToLower(strings.TrimSpace(txId)) + ":" + strings.TrimSpace(listingKey))
}

func tradeTimeKey(settledAt int64, txId, listingKey string) []byte {
	var ts uint64
	if settledAt > 0 {
		ts = uint64(settledAt)
	}
	return []byte(keyTradeTime + invertedSeq(ts) + ":" + strings.ToLower(strings.TrimSpace(txId)) + ":" + strings.TrimSpace(listingKey))
}

func parseTradePayload(raw []byte) (*tradePayload, error) {
	fields, err := decodePayloadObject(raw)
	if err != nil {
		return nil, err
	}
	p := &tradePayload{}
	var ok bool
	if p.TxId, ok = getStringField(fields, "txId"); !ok {
		if p.TxId, ok = getStringField(fields, "deliveryTxId"); !ok {
			return nil, fmt.Errorf("missing txId")
		}
	}
	p.TxId = strings.ToLower(strings.TrimSpace(p.TxId))
	if !hex64Pattern.MatchString(p.TxId) {
		return nil, fmt.Errorf("invalid txId %q", p.TxId)
	}

	p.ListingKey, _ = getStringField(fields, "listingKey")
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(p.ListingKey)), "mvc-ft:") {
		p.ListingKey = normalizeListingKeyAssetPrefix(p.ListingKey)
	}
	p.AssetKey, _ = getStringField(fields, "assetKey")
	p.AssetKey = NormalizeAssetKey(p.AssetKey)
	p.Chain, _ = getStringField(fields, "chain")
	p.Chain = strings.ToLower(strings.TrimSpace(p.Chain))
	p.Symbol, _ = getStringField(fields, "symbol")
	p.Decimals, _ = getIntField(fields, "decimals")

	if p.AssetKey == "" {
		return nil, fmt.Errorf("missing assetKey")
	}
	if p.Chain == "" {
		if strings.HasPrefix(p.AssetKey, "mrc20:") {
			p.Chain = "btc"
		} else if strings.HasPrefix(p.AssetKey, "mvc-ft:") {
			p.Chain = "mvc"
		}
	}

	p.AmountBase, _ = getStringField(fields, "amountBase")
	if p.AmountBase == "" {
		p.AmountBase, _ = getStringField(fields, "tokenAmountBase")
	}
	if p.AmountBase != "" {
		bi, valid := new(big.Int).SetString(p.AmountBase, 10)
		if !valid || bi.Sign() <= 0 {
			return nil, fmt.Errorf("invalid amountBase %q", p.AmountBase)
		}
		p.AmountBase = bi.String()
	}
	p.AmountDisplay, _ = getStringField(fields, "amountDisplay")
	if p.AmountDisplay == "" && p.AmountBase != "" {
		if bi, valid := new(big.Int).SetString(p.AmountBase, 10); valid {
			p.AmountDisplay = formatBaseAmount(bi, p.Decimals)
		}
	}

	p.SellerAddress, _ = getStringField(fields, "sellerAddress")
	p.SellerMetaId, _ = getStringField(fields, "sellerMetaId")
	p.BuyerAddress, _ = getStringField(fields, "buyerAddress")
	p.BuyerMetaId, _ = getStringField(fields, "buyerMetaId")
	p.Rail, _ = getStringField(fields, "rail")
	p.Rail = strings.ToLower(strings.TrimSpace(p.Rail))
	p.Currency, _ = getStringField(fields, "currency")
	p.VerifiedSource, _ = getStringField(fields, "verifiedSource")

	if v, ok := getInt64Field(fields, "amountMilli"); ok && v > 0 {
		p.AmountMilli = &v
	}
	if v, ok := getInt64Field(fields, "amountCents"); ok && v > 0 {
		p.AmountCents = &v
	}
	if v, ok := getInt64Field(fields, "priceSats"); ok && v > 0 {
		p.PriceSats = &v
	}
	if p.Currency == "" {
		switch {
		case p.AmountMilli != nil:
			p.Currency = "me"
		case p.AmountCents != nil:
			p.Currency = "fiat:CNY"
		case p.PriceSats != nil:
			if p.Chain == "btc" {
				p.Currency = "btc"
			} else {
				p.Currency = "space"
			}
		default:
			p.Currency = "me"
		}
	}
	if p.Rail == "" {
		switch {
		case p.AmountMilli != nil:
			p.Rail = "me-escrow"
		case p.AmountCents != nil:
			p.Rail = "fiat-escrow"
		case p.PriceSats != nil && p.Chain == "btc":
			p.Rail = "btc-psbt"
		case p.PriceSats != nil:
			p.Rail = "mvc-tokensell"
		}
	}
	if p.VerifiedSource == "" {
		switch p.Rail {
		case "mvc-tokensell":
			p.VerifiedSource = "chain_tokensell"
		case "btc-psbt":
			p.VerifiedSource = "btc_psbt"
		default:
			p.VerifiedSource = "abs_escrow"
		}
	}
	p.SettledAt, _ = getInt64Field(fields, "settledAt")
	p.SettledAt = unixSeconds(p.SettledAt)
	return p, nil
}

func extractTradePublisher(pin *aggregator.PinInscription) tradePublisher {
	if pin == nil {
		return tradePublisher{}
	}
	return tradePublisher{
		Address:       strings.TrimSpace(pin.Address),
		CreateAddress: strings.TrimSpace(pin.CreateAddress),
		Creator:       strings.TrimSpace(pin.Creator),
		MetaId:        strings.TrimSpace(pin.MetaId),
		CreateMetaId:  strings.TrimSpace(pin.CreateMetaId),
		GlobalMetaId:  strings.TrimSpace(pin.GlobalMetaId),
	}
}

func (a *Aggregator) isAuthorizedTradePublisherLocked(pub tradePublisher, sellerAddr, buyerAddr string) bool {
	if (pub.MetaId != "" && strings.EqualFold(pub.MetaId, idbotsca.OfficialMetaID)) ||
		(pub.CreateMetaId != "" && strings.EqualFold(pub.CreateMetaId, idbotsca.OfficialMetaID)) ||
		(pub.GlobalMetaId != "" && strings.EqualFold(pub.GlobalMetaId, idbotsca.OfficialGlobalMetaID)) ||
		pub.Address == idbotsca.OfficialMVCAddress ||
		pub.CreateAddress == idbotsca.OfficialMVCAddress {
		return true
	}
	if a.caReader != nil {
		gid := pub.GlobalMetaId
		if gid == "" && a.profileReader != nil {
			if id := firstNonEmpty(pub.MetaId, pub.CreateMetaId, pub.Address, pub.CreateAddress); id != "" {
				if p := a.profileReader.LookupProfile(id); p != nil {
					gid = p.GlobalMetaId
				}
			}
		}
		if gid != "" {
			if verified, role := a.caReader.GetOfficialBotRole(gid); verified &&
				(role == "escrow_guarantor" || role == "official_announcer") {
				return true
			}
		}
	}
	return a.pubMatchesAddressLocked(pub, sellerAddr) || a.pubMatchesAddressLocked(pub, buyerAddr)
}

func (a *Aggregator) pubMatchesAddressLocked(pub tradePublisher, addr string) bool {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return false
	}
	if sameSellerAddress(pub.Address, addr) || sameSellerAddress(pub.CreateAddress, addr) || sameSellerAddress(pub.Creator, addr) {
		return true
	}
	if a.profileReader == nil {
		return false
	}
	for _, gid := range []string{pub.GlobalMetaId, pub.MetaId, pub.CreateMetaId} {
		if gid == "" {
			continue
		}
		for _, resolved := range a.profileReader.ResolveAddressesByGlobalMetaId(gid) {
			if sameSellerAddress(resolved, addr) {
				return true
			}
		}
	}
	if p := a.profileReader.LookupProfile(addr); p != nil {
		if pub.GlobalMetaId != "" && p.GlobalMetaId != "" && strings.EqualFold(pub.GlobalMetaId, p.GlobalMetaId) {
			return true
		}
		if pub.MetaId != "" && p.MetaId != "" && strings.EqualFold(pub.MetaId, p.MetaId) {
			return true
		}
	}
	return false
}

func buildTradeRecordFromPayload(pin *aggregator.PinInscription, p *tradePayload, confirmed bool) *TradeRecord {
	settledAt := p.SettledAt
	if settledAt == 0 && pin != nil {
		settledAt = unixSeconds(pin.Timestamp)
	}
	var unitMeMilli *string
	if p.AmountMilli != nil && *p.AmountMilli > 0 && p.AmountBase != "" {
		if r := computeUnitPriceRat(*p.AmountMilli, p.AmountBase, p.Decimals); r != nil {
			s := formatRat(r, 6)
			unitMeMilli = &s
		}
	}
	return &TradeRecord{
		TxId:           p.TxId,
		TradePinId:     strings.TrimSpace(pin.Id),
		ListingKey:     p.ListingKey,
		AssetKey:       p.AssetKey,
		Chain:          p.Chain,
		Symbol:         p.Symbol,
		Decimals:       p.Decimals,
		AmountBase:     p.AmountBase,
		AmountDisplay:  p.AmountDisplay,
		Rail:           p.Rail,
		Currency:       p.Currency,
		AmountMilli:    p.AmountMilli,
		AmountCents:    p.AmountCents,
		PriceSats:      p.PriceSats,
		SellerAddress:  p.SellerAddress,
		SellerMetaId:   p.SellerMetaId,
		BuyerAddress:   p.BuyerAddress,
		BuyerMetaId:    p.BuyerMetaId,
		SettledAt:      settledAt,
		VerifiedSource: p.VerifiedSource,
		Confirmed:      confirmed,
		UnitMeMilli:    unitMeMilli,
	}
}

func (a *Aggregator) loadTrade(txId, listingKey string) (*TradeRecord, error) {
	if a.store == nil {
		return nil, nil
	}
	raw, err := a.store.Get(Namespace, tradeStorageKey(txId, listingKey))
	if err != nil || len(raw) == 0 {
		return nil, nil
	}
	var rec TradeRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	rec.Confirmed = true
	return &rec, nil
}

// ingestTradePin folds a confirmed /protocols/ft-trade receipt pin.
// Callers must hold a.mu.
func (a *Aggregator) ingestTradePin(pin *aggregator.PinInscription) (bool, error) {
	if pin == nil || a.store == nil {
		return false, nil
	}
	if normalisedPinPath(pin.Path) != PathFtTrade {
		return false, nil
	}
	if strings.ToLower(strings.TrimSpace(pin.Operation)) != OperationCreate {
		return false, nil
	}

	payload, err := parseTradePayload(pinPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: ftmarket trade pin %s skipped: %v", strings.TrimSpace(pin.Id), err)
		return false, nil
	}
	if strings.HasPrefix(payload.AssetKey, "mvc-ft:") {
		parts := strings.Split(strings.TrimPrefix(payload.AssetKey, "mvc-ft:"), ":")
		if len(parts) == 2 && hex40Pattern.MatchString(parts[0]) && !hex40Pattern.MatchString(parts[1]) {
			if canonGen, _ := a.resolveMvcSensibleIfNeededLocked(nil, parts[0], parts[1]); hex40Pattern.MatchString(canonGen) {
				payload.AssetKey = "mvc-ft:" + parts[0] + ":" + canonGen
				if strings.HasPrefix(strings.ToLower(payload.ListingKey), "mvc-ft:") {
					payload.ListingKey = normalizeListingKeyAssetPrefix(payload.ListingKey)
				}
			}
		}
	}

	pub := extractTradePublisher(pin)
	if !a.isAuthorizedTradePublisherLocked(pub, payload.SellerAddress, payload.BuyerAddress) {
		log.Printf("WARNING: ftmarket trade pin %s rejected: unauthorized publisher", strings.TrimSpace(pin.Id))
		return false, nil
	}

	if existing, _ := a.loadTrade(payload.TxId, payload.ListingKey); existing != nil {
		return false, nil
	}

	// Hydrate symbol/decimals from catalog or listing if missing in trade payload.
	if payload.Symbol == "" || payload.Decimals == 0 {
		if cat := a.loadTokenCatalog(payload.AssetKey); cat != nil {
			if payload.Symbol == "" {
				payload.Symbol = cat.Asset.Symbol
			}
			if payload.Decimals == 0 {
				payload.Decimals = cat.Asset.Decimals
			}
		}
	}
	if payload.AmountDisplay == "" && payload.AmountBase != "" {
		if bi, ok := new(big.Int).SetString(payload.AmountBase, 10); ok {
			payload.AmountDisplay = formatBaseAmount(bi, payload.Decimals)
		}
	}

	rec := buildTradeRecordFromPayload(pin, payload, true)
	raw, err := json.Marshal(rec)
	if err != nil {
		return false, err
	}
	if err := a.store.Set(Namespace, tradeStorageKey(rec.TxId, rec.ListingKey), raw); err != nil {
		return false, err
	}
	if err := a.store.Set(Namespace, tradeTimeKey(rec.SettledAt, rec.TxId, rec.ListingKey), []byte("1")); err != nil {
		return false, err
	}
	return true, nil
}

func (a *Aggregator) listAllTrades(includeOverlay bool) ([]*TradeRecord, error) {
	if a.store == nil {
		return nil, nil
	}
	var trades []*TradeRecord
	byKey := make(map[string]int)
	err := a.store.ScanPrefix(Namespace, []byte(keyTrade), func(_, val []byte) error {
		var rec TradeRecord
		if err := json.Unmarshal(val, &rec); err != nil {
			return nil
		}
		rec.Confirmed = true
		rec.AssetKey = NormalizeAssetKey(rec.AssetKey)
		if strings.HasPrefix(strings.ToLower(rec.ListingKey), "mvc-ft:") {
			rec.ListingKey = normalizeListingKeyAssetPrefix(rec.ListingKey)
		}
		k := strings.ToLower(rec.TxId) + ":" + rec.ListingKey
		byKey[k] = len(trades)
		trades = append(trades, &rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if includeOverlay && a.zeroConfTrade {
		a.mu.Lock()
		for _, entry := range a.overlayDecls {
			if entry != nil && entry.trade != nil {
				k := strings.ToLower(entry.trade.TxId) + ":" + entry.trade.ListingKey
				if _, exists := byKey[k]; !exists {
					cp := *entry.trade
					byKey[k] = len(trades)
					trades = append(trades, &cp)
				}
			}
		}
		a.mu.Unlock()
	}
	sort.Slice(trades, func(i, j int) bool {
		if trades[i].SettledAt != trades[j].SettledAt {
			return trades[i].SettledAt > trades[j].SettledAt
		}
		return trades[i].TxId > trades[j].TxId
	})
	return trades, nil
}

// handleMarketTrades serves GET /api/ft/market/trades (Contract C2.2).
func (a *Aggregator) handleMarketTrades(c *gin.Context) {
	if a == nil || a.store == nil {
		respondMarketErr(c, 50000, "ftmarket index unavailable")
		return
	}
	size, err := parseListSize(c.Query("size"), 20, maxListSize)
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: size")
		return
	}
	offset := parseOffsetCursor(c.Query("cursor"))
	assetKey := a.normalizeQueryAssetKey(c.Request.Context(), c.Query("assetKey"))
	rail := strings.ToLower(strings.TrimSpace(c.Query("rail")))
	party := strings.TrimSpace(c.Query("party"))
	var partyAddrs map[string]struct{}
	if party != "" {
		partyAddrs = a.resolvePartyAddresses(party)
	}

	trades, err := a.listAllTrades(!zeroConfOnly(c))
	if err != nil {
		respondMarketErr(c, 50000, "failed to load trades")
		return
	}
	filtered := make([]*TradeRecord, 0, len(trades))
	for _, tr := range trades {
		if assetKey != "" && !strings.EqualFold(tr.AssetKey, assetKey) {
			continue
		}
		if rail != "" && rail != "any" && !strings.EqualFold(tr.Rail, rail) {
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
