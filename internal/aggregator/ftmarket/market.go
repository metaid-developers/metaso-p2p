package ftmarket

import (
	"context"
	"fmt"
	"math/big"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// MarketAssetDTO is the asset descriptor returned by Contract C2 endpoints.
type MarketAssetDTO struct {
	AssetKey            string  `json:"assetKey"`
	Chain               string  `json:"chain"`
	Standard            string  `json:"standard"`
	Mrc20Id             *string `json:"mrc20Id"`
	Codehash            *string `json:"codehash"`
	Genesis             *string `json:"genesis"`
	SensibleId          *string `json:"sensibleId,omitempty"`
	Symbol              string  `json:"symbol"`
	TokenName           string  `json:"tokenName"`
	Decimals            int     `json:"decimals"`
	Icon                string  `json:"icon"`
	CaVerified          bool    `json:"caVerified"`
	BridgedPeerAssetKey string  `json:"bridgedPeerAssetKey,omitempty"`
	BridgedDualChain    bool    `json:"bridgedDualChain,omitempty"`
}

// MarketFloorsDTO carries the three-rail unit floor prices (Contract C2.1).
type MarketFloorsDTO struct {
	BtcUnitSats   *string `json:"btcUnitSats"`
	SpaceUnitSats *string `json:"spaceUnitSats"`
	MeUnitMilli   *string `json:"meUnitMilli"`
}

// RecentGranularityDTO holds lot size quartiles for smart broker splitting (Contract C2.1).
type RecentGranularityDTO struct {
	MedianLotAmountDisplay *string `json:"medianLotAmountDisplay"`
	P25LotAmountDisplay    *string `json:"p25LotAmountDisplay"`
	P75LotAmountDisplay    *string `json:"p75LotAmountDisplay"`
}

// TradeStats24hDTO aggregates 24-hour trading metrics for one token (Contract C2.1).
type TradeStats24hDTO struct {
	TradeCount           int     `json:"tradeCount"`
	VolumeTokenDisplay   string  `json:"volumeTokenDisplay"`
	LastUnitPriceMeMilli *string `json:"lastUnitPriceMeMilli"`
	LastTradeAt          *int64  `json:"lastTradeAt"`
}

// PeerTokenSummaryDTO projects the bridged peer asset's market snapshot on Octopus dual-chain tokens.
type PeerTokenSummaryDTO struct {
	Asset                    MarketAssetDTO       `json:"asset"`
	HolderCount              int                  `json:"holderCount"`
	ListedLotCount           int                  `json:"listedLotCount"`
	ListedTotalAmountDisplay string               `json:"listedTotalAmountDisplay"`
	Floors                   MarketFloorsDTO      `json:"floors"`
	RecentGranularity        RecentGranularityDTO `json:"recentGranularity"`
	TradeStats24h            TradeStats24hDTO     `json:"tradeStats24h"`
}

// TokenMarketSummary is the aggregated single-token market view (Contract C2.1).
type TokenMarketSummary struct {
	Asset                    MarketAssetDTO       `json:"asset"`
	HolderCount              int                  `json:"holderCount"`
	ListedLotCount           int                  `json:"listedLotCount"`
	ListedTotalAmountDisplay string               `json:"listedTotalAmountDisplay"`
	Floors                   MarketFloorsDTO      `json:"floors"`
	RecentGranularity        RecentGranularityDTO `json:"recentGranularity"`
	TradeStats24h            TradeStats24hDTO     `json:"tradeStats24h"`
	PeerSummary              *PeerTokenSummaryDTO `json:"peerSummary,omitempty"`
	VolumeBase24h            *big.Int             `json:"-"`
}

// ListingUnitPrices carries computed per-display-token unit prices for a listing.
type ListingUnitPrices struct {
	BtcUnitSats   *string `json:"btcUnitSats,omitempty"`
	SpaceUnitSats *string `json:"spaceUnitSats,omitempty"`
	MeUnitMilli   *string `json:"meUnitMilli,omitempty"`
}

// SellerDTO carries enriched seller profile and live presence status.
type SellerDTO struct {
	Address            string `json:"address"`
	BtcAddress         string `json:"btcAddress,omitempty"`
	GlobalMetaId       string `json:"globalMetaId"`
	Name               string `json:"name"`
	Avatar             string `json:"avatar"`
	HasChatPubkey      bool   `json:"hasChatPubkey"`
	IsOnline           bool   `json:"isOnline"`
	LastSeenAgoSeconds *int64 `json:"lastSeenAgoSeconds,omitempty"`
}

// ListingDTO is one active listing returned by GET /api/ft/market/listings (Contract C2.2).
type ListingDTO struct {
	ListingKey      string             `json:"listingKey"`
	SellPinId       string             `json:"sellPinId"`
	EffectiveActive bool               `json:"effectiveActive"`
	Asset           MarketAssetDTO     `json:"asset"`
	Lot             LotInfo            `json:"lot"`
	Rails           *SellRails         `json:"rails"`
	AcceptedRails   []string           `json:"acceptedRails"`
	UnitPrices      *ListingUnitPrices `json:"unitPrices,omitempty"`
	BatchGroupId    string             `json:"batchGroupId,omitempty"`
	BatchIndex      int                `json:"batchIndex,omitempty"`
	BatchTotal      int                `json:"batchTotal,omitempty"`
	Note            string             `json:"note,omitempty"`
	Seller          SellerDTO          `json:"seller"`
	ListedAt        int64              `json:"listedAt"`
	CreatedAt       int64              `json:"createdAt"`
	ExpiresAt       int64              `json:"expiresAt"`
	Confirmed       bool               `json:"confirmed"`
}

// AmbiguousAssetInfo alerts callers when a symbol query matches multiple distinct assetKeys.
type AmbiguousAssetInfo struct {
	AssetKey            string `json:"assetKey"`
	Chain               string `json:"chain"`
	Standard            string `json:"standard"`
	Symbol              string `json:"symbol"`
	TokenName           string `json:"tokenName"`
	Decimals            int    `json:"decimals"`
	CaVerified          bool   `json:"caVerified"`
	ListedLotCount      int    `json:"listedLotCount"`
	BridgedPeerAssetKey string `json:"bridgedPeerAssetKey,omitempty"`
	BridgedDualChain    bool   `json:"bridgedDualChain,omitempty"`
}

type marketCacheSnapshot struct {
	builtAtSec int64
	summaries  []*TokenMarketSummary
	byAssetKey map[string]*TokenMarketSummary
}

const (
	marketCacheTTLSeconds = 5
	tokenMetaTTL          = 60 * time.Second
	granularitySampleCap  = 50
)

func (a *Aggregator) invalidateMarketCache() {
	if a == nil {
		return
	}
	a.marketMu.Lock()
	a.marketCache = nil
	a.marketMu.Unlock()
}

func (a *Aggregator) getOrBuildMarketSummaries(ctx context.Context) ([]*TokenMarketSummary, map[string]*TokenMarketSummary) {
	nowSec := a.nowUnix()
	a.marketMu.RLock()
	cached := a.marketCache
	a.marketMu.RUnlock()
	if cached != nil && nowSec-cached.builtAtSec < marketCacheTTLSeconds {
		return cached.summaries, cached.byAssetKey
	}

	summaries, byKey := a.computeAllTokenSummaries(ctx, nowSec, a.zeroConfTrade)
	a.marketMu.Lock()
	a.marketCache = &marketCacheSnapshot{
		builtAtSec: nowSec,
		summaries:  summaries,
		byAssetKey: byKey,
	}
	a.marketMu.Unlock()
	return summaries, byKey
}

func (a *Aggregator) computeAllTokenSummaries(ctx context.Context, nowSec int64, includeZeroConf bool) ([]*TokenMarketSummary, map[string]*TokenMarketSummary) {
	listings, _ := a.listAllEffectiveListings(includeZeroConf)
	a.refreshListingsLiveness(ctx, listings, false)
	trades, _ := a.listAllTrades(includeZeroConf)

	type lotSample struct {
		ts         int64
		key        string
		amountBase *big.Int
	}
	type assetAcc struct {
		asset       AssetInfo
		listedCount int
		listedBase  *big.Int
		btcFloor    *big.Rat
		spaceFloor  *big.Rat
		meFloor     *big.Rat
		samples     []lotSample
		trades24h   int
		volBase24h  *big.Int
		lastTradeAt *int64
		lastMeUnit  *string
	}

	accs := make(map[string]*assetAcc)
	ensureAcc := func(asset AssetInfo) *assetAcc {
		normalizeAssetInfoInPlace(&asset)
		key := NormalizeAssetKey(asset.AssetKey)
		if key == "" {
			return nil
		}
		if existing, ok := accs[key]; ok {
			if existing.asset.Symbol == "" && asset.Symbol != "" {
				existing.asset.Symbol = asset.Symbol
			}
			if existing.asset.TokenName == "" && asset.TokenName != "" {
				existing.asset.TokenName = asset.TokenName
			}
			if existing.asset.Icon == "" && asset.Icon != "" {
				existing.asset.Icon = asset.Icon
			}
			if existing.asset.Decimals == 0 && asset.Decimals > 0 {
				existing.asset.Decimals = asset.Decimals
			}
			if (existing.asset.SensibleId == nil || *existing.asset.SensibleId == "") && asset.SensibleId != nil && *asset.SensibleId != "" {
				s := *asset.SensibleId
				existing.asset.SensibleId = &s
			}
			return existing
		}
		asset.AssetKey = key
		acc := &assetAcc{
			asset:      asset,
			listedBase: big.NewInt(0),
			volBase24h: big.NewInt(0),
		}
		accs[key] = acc
		return acc
	}

	// Register CA sensibleId mappings first, then seed catalog + builtinCA + caReader.ListCertifiedTokens()
	// so official certified tokens remain discoverable even before any local listing or trade pin exists.
	a.mu.Lock()
	caReader := a.caReader
	builtins := make([]CertifiedTokenInfo, 0, len(a.builtinCA))
	for _, cert := range a.builtinCA {
		builtins = append(builtins, cert)
	}
	a.mu.Unlock()
	var caCerts []CertifiedTokenInfo
	if caReader != nil {
		caCerts = caReader.ListCertifiedTokens()
		for _, cert := range caCerts {
			registerCertSensibleMapping(cert)
		}
	}
	for _, cert := range builtins {
		ensureAcc(assetInfoFromCert(cert))
	}
	for _, cert := range caCerts {
		ensureAcc(assetInfoFromCert(cert))
	}
	for _, cat := range a.listTokenCatalog() {
		ensureAcc(cat.Asset)
	}

	for _, rec := range listings {
		if rec == nil || rec.Asset == nil || rec.Asset.AssetKey == "" {
			continue
		}
		acc := ensureAcc(*rec.Asset)
		if acc == nil || !rec.EffectiveActive || rec.Lot == nil {
			continue
		}
		amountInt, ok := new(big.Int).SetString(rec.Lot.AmountBase, 10)
		if !ok || amountInt.Sign() <= 0 {
			continue
		}
		acc.listedCount++
		acc.listedBase.Add(acc.listedBase, amountInt)

		ts := rec.UpdatedAt
		if ts == 0 {
			ts = rec.CreatedAt
		}
		acc.samples = append(acc.samples, lotSample{
			ts:         ts,
			key:        "L:" + rec.ListingKey,
			amountBase: amountInt,
		})

		if rec.Rails != nil {
			if rec.Rails.MvcTokenSell != nil && rec.Rails.MvcTokenSell.PriceSats > 0 {
				if r := computeUnitPriceRat(rec.Rails.MvcTokenSell.PriceSats, rec.Lot.AmountBase, acc.asset.Decimals); r != nil {
					if acc.spaceFloor == nil || r.Cmp(acc.spaceFloor) < 0 {
						acc.spaceFloor = r
					}
				}
			}
			if rec.Rails.BtcPsbt != nil && rec.Rails.BtcPsbt.PriceSats > 0 {
				if r := computeUnitPriceRat(rec.Rails.BtcPsbt.PriceSats, rec.Lot.AmountBase, acc.asset.Decimals); r != nil {
					if acc.btcFloor == nil || r.Cmp(acc.btcFloor) < 0 {
						acc.btcFloor = r
					}
				}
			}
			if rec.Rails.Me != nil && rec.Rails.Me.AmountMilli > 0 {
				if r := computeUnitPriceRat(rec.Rails.Me.AmountMilli, rec.Lot.AmountBase, acc.asset.Decimals); r != nil {
					if acc.meFloor == nil || r.Cmp(acc.meFloor) < 0 {
						acc.meFloor = r
					}
				}
			}
		}
	}

	cutoff24h := nowSec - 86400
	for _, tr := range trades {
		if tr == nil || tr.AssetKey == "" {
			continue
		}
		acc, ok := accs[strings.ToLower(tr.AssetKey)]
		if !ok {
			acc = ensureAcc(assetInfoFromTrade(tr))
		}
		if acc == nil {
			continue
		}
		var amtInt *big.Int
		if tr.AmountBase != "" {
			if parsed, valid := new(big.Int).SetString(tr.AmountBase, 10); valid && parsed.Sign() > 0 {
				amtInt = parsed
				acc.samples = append(acc.samples, lotSample{
					ts:         tr.SettledAt,
					key:        "T:" + tr.TxId,
					amountBase: parsed,
				})
			}
		}
		if tr.SettledAt >= cutoff24h {
			acc.trades24h++
			if amtInt != nil {
				acc.volBase24h.Add(acc.volBase24h, amtInt)
			}
		}
		if acc.lastTradeAt == nil || tr.SettledAt > *acc.lastTradeAt {
			ts := tr.SettledAt
			acc.lastTradeAt = &ts
			if tr.UnitMeMilli != nil {
				v := *tr.UnitMeMilli
				acc.lastMeUnit = &v
			} else if tr.AmountMilli != nil && *tr.AmountMilli > 0 && tr.AmountBase != "" {
				if r := computeUnitPriceRat(*tr.AmountMilli, tr.AmountBase, tr.Decimals); r != nil {
					s := formatRat(r, 6)
					acc.lastMeUnit = &s
				}
			}
		}
	}

	summaries := make([]*TokenMarketSummary, 0, len(accs))
	byKey := make(map[string]*TokenMarketSummary, len(accs))

	for key, acc := range accs {
		dtoAsset := a.buildMarketAssetDTO(acc.asset)
		holderCount := 0
		if meta := a.fetchTokenMetadataCached(ctx, acc.asset); meta != nil {
			holderCount = meta.HolderCount
			if dtoAsset.Symbol == "" && meta.Symbol != "" {
				dtoAsset.Symbol = meta.Symbol
			}
			if dtoAsset.TokenName == "" && meta.TokenName != "" {
				dtoAsset.TokenName = meta.TokenName
			}
			if dtoAsset.Decimals == 0 && meta.Decimals > 0 {
				dtoAsset.Decimals = meta.Decimals
			}
			if dtoAsset.Icon == "" && meta.Icon != "" {
				dtoAsset.Icon = a.resolveIconURL(meta.Icon)
			}
			if meta.SensibleId != "" {
				sid := strings.ToLower(strings.TrimSpace(meta.SensibleId))
				if dtoAsset.SensibleId == nil || *dtoAsset.SensibleId == "" {
					dtoAsset.SensibleId = &sid
				}
				if acc.asset.Chain == "mvc" && acc.asset.Codehash != nil && acc.asset.Genesis != nil && hex40Pattern.MatchString(*acc.asset.Genesis) {
					RegisterMvcSensibleMapping(*acc.asset.Codehash, *acc.asset.Genesis, sid)
				}
			}
		}

		// Optional external BTC floor snapshot (api.metaid.market) for MRC-20 assets.
		if acc.asset.Chain == "btc" && acc.asset.Mrc20Id != nil {
			a.mu.Lock()
			btcProv := a.btcFloorProv
			a.mu.Unlock()
			if btcProv != nil {
				if extFloor, ok := btcProv.FetchBtcUnitFloorSats(ctx, *acc.asset.Mrc20Id); ok && extFloor != nil {
					if extRat, okRat := new(big.Rat).SetString(*extFloor); okRat {
						if acc.btcFloor == nil || extRat.Cmp(acc.btcFloor) < 0 {
							acc.btcFloor = extRat
						}
					}
				}
			}
		}

		floors := MarketFloorsDTO{}
		if acc.btcFloor != nil {
			s := formatRat(acc.btcFloor, 6)
			floors.BtcUnitSats = &s
		}
		if acc.spaceFloor != nil {
			s := formatRat(acc.spaceFloor, 6)
			floors.SpaceUnitSats = &s
		}
		if acc.meFloor != nil {
			s := formatRat(acc.meFloor, 6)
			floors.MeUnitMilli = &s
		}

		// Sort samples newest-first, keep top 50, then compute quartiles.
		sort.Slice(acc.samples, func(i, j int) bool {
			if acc.samples[i].ts != acc.samples[j].ts {
				return acc.samples[i].ts > acc.samples[j].ts
			}
			return acc.samples[i].key > acc.samples[j].key
		})
		if len(acc.samples) > granularitySampleCap {
			acc.samples = acc.samples[:granularitySampleCap]
		}
		amounts := make([]*big.Int, 0, len(acc.samples))
		for _, smp := range acc.samples {
			amounts = append(amounts, smp.amountBase)
		}
		granularity := computeRecentGranularity(amounts, dtoAsset.Decimals)

		summary := &TokenMarketSummary{
			Asset:                    dtoAsset,
			HolderCount:              holderCount,
			ListedLotCount:           acc.listedCount,
			ListedTotalAmountDisplay: formatBaseAmount(acc.listedBase, dtoAsset.Decimals),
			Floors:                   floors,
			RecentGranularity:        granularity,
			TradeStats24h: TradeStats24hDTO{
				TradeCount:           acc.trades24h,
				VolumeTokenDisplay:   formatBaseAmount(acc.volBase24h, dtoAsset.Decimals),
				LastUnitPriceMeMilli: acc.lastMeUnit,
				LastTradeAt:          acc.lastTradeAt,
			},
			VolumeBase24h: acc.volBase24h,
		}
		summaries = append(summaries, summary)
		byKey[key] = summary
	}

	// Populate non-recursive PeerSummary for Octopus dual-chain bridged token pairs.
	for _, s := range summaries {
		if s == nil || s.Asset.BridgedPeerAssetKey == "" {
			continue
		}
		if peer := byKey[NormalizeAssetKey(s.Asset.BridgedPeerAssetKey)]; peer != nil && peer.Asset.AssetKey != s.Asset.AssetKey {
			s.PeerSummary = &PeerTokenSummaryDTO{
				Asset:                    peer.Asset,
				HolderCount:              peer.HolderCount,
				ListedLotCount:           peer.ListedLotCount,
				ListedTotalAmountDisplay: peer.ListedTotalAmountDisplay,
				Floors:                   peer.Floors,
				RecentGranularity:        peer.RecentGranularity,
				TradeStats24h:            peer.TradeStats24h,
			}
		}
	}

	return summaries, byKey
}

func computeRecentGranularity(amounts []*big.Int, decimals int) RecentGranularityDTO {
	if len(amounts) == 0 {
		return RecentGranularityDTO{}
	}
	sorted := make([]*big.Int, len(amounts))
	copy(sorted, amounts)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Cmp(sorted[j]) < 0
	})

	p25 := interpolateQuantile(sorted, 0.25, decimals)
	med := interpolateQuantile(sorted, 0.50, decimals)
	p75 := interpolateQuantile(sorted, 0.75, decimals)
	return RecentGranularityDTO{
		MedianLotAmountDisplay: &med,
		P25LotAmountDisplay:    &p25,
		P75LotAmountDisplay:    &p75,
	}
}

func interpolateQuantile(sorted []*big.Int, p float64, decimals int) string {
	n := len(sorted)
	if n == 0 {
		return "0"
	}
	if n == 1 {
		return formatBaseAmount(sorted[0], decimals)
	}
	// Linear interpolation over index [0, n-1] using exact rational weights.
	num := int64(p * 100)
	pos := new(big.Rat).SetFrac64(num*int64(n-1), 100)
	loFloat, _ := pos.Float64()
	lo := int(loFloat)
	if lo < 0 {
		lo = 0
	}
	if lo >= n-1 {
		return formatBaseAmount(sorted[n-1], decimals)
	}
	hi := lo + 1
	frac := new(big.Rat).Sub(pos, big.NewRat(int64(lo), 1))
	oneMinusFrac := new(big.Rat).Sub(big.NewRat(1, 1), frac)

	vLo := new(big.Rat).SetInt(sorted[lo])
	vHi := new(big.Rat).SetInt(sorted[hi])
	interp := new(big.Rat).Add(
		new(big.Rat).Mul(oneMinusFrac, vLo),
		new(big.Rat).Mul(frac, vHi),
	)
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	display := new(big.Rat).Quo(interp, new(big.Rat).SetInt(scale))
	maxFrac := decimals
	if maxFrac > 8 {
		maxFrac = 8
	}
	return formatRat(display, maxFrac)
}

func (a *Aggregator) buildMarketAssetDTO(asset AssetInfo) MarketAssetDTO {
	normalizeAssetInfoInPlace(&asset)
	caVerified, cert := a.lookupTokenCA(asset.AssetKey)
	symbol := asset.Symbol
	tokenName := asset.TokenName
	icon := asset.Icon
	decimals := asset.Decimals
	var sensibleIdPtr *string
	if asset.SensibleId != nil && *asset.SensibleId != "" {
		s := strings.ToLower(strings.TrimSpace(*asset.SensibleId))
		sensibleIdPtr = &s
	}
	bridgedPeer := ""
	bridgedDual := false
	if cert != nil {
		if cert.Symbol != "" {
			symbol = cert.Symbol
		}
		if cert.DisplayName != "" {
			tokenName = cert.DisplayName
		} else if tokenName == "" && cert.TokenName != "" {
			tokenName = cert.TokenName
		}
		if icon == "" && cert.Icon != "" {
			icon = cert.Icon
		}
		if decimals == 0 && cert.Decimals > 0 {
			decimals = cert.Decimals
		}
		if sensibleIdPtr == nil && cert.SensibleId != "" {
			s := strings.ToLower(strings.TrimSpace(cert.SensibleId))
			sensibleIdPtr = &s
		}
		if cert.BridgedPeerAssetKey != "" {
			bridgedPeer = NormalizeAssetKey(cert.BridgedPeerAssetKey)
			bridgedDual = true
		} else if cert.BridgedDualChain {
			bridgedDual = true
		}
	}
	if sensibleIdPtr == nil && asset.Chain == "mvc" && asset.Codehash != nil && asset.Genesis != nil {
		if _, sid := ResolveMvcGenesisAndSensibleId(*asset.Codehash, *asset.Genesis); sid != "" {
			sensibleIdPtr = &sid
		}
	}
	return MarketAssetDTO{
		AssetKey:            NormalizeAssetKey(asset.AssetKey),
		Chain:               asset.Chain,
		Standard:            asset.Standard,
		Mrc20Id:             asset.Mrc20Id,
		Codehash:            asset.Codehash,
		Genesis:             asset.Genesis,
		SensibleId:          sensibleIdPtr,
		Symbol:              symbol,
		TokenName:           tokenName,
		Decimals:            decimals,
		Icon:                a.resolveIconURL(icon),
		CaVerified:          caVerified,
		BridgedPeerAssetKey: bridgedPeer,
		BridgedDualChain:    bridgedDual,
	}
}

func (a *Aggregator) lookupTokenCA(assetKey string) (bool, *CertifiedTokenInfo) {
	key := NormalizeAssetKey(assetKey)
	if key == "" {
		return false, nil
	}
	a.mu.Lock()
	ca := a.caReader
	builtin, hasBuiltin := a.builtinCA[key]
	a.mu.Unlock()
	if ca != nil {
		if ok, info := ca.IsTokenCertified(key); ok {
			if info == nil {
				if hasBuiltin {
					cp := builtin
					return true, &cp
				}
				return true, nil
			}
			merged := *info
			if hasBuiltin {
				if merged.DisplayName == "" {
					merged.DisplayName = builtin.DisplayName
				}
				if merged.Symbol == "" {
					merged.Symbol = builtin.Symbol
				}
				if merged.TokenName == "" {
					merged.TokenName = builtin.TokenName
				}
				if merged.Decimals == 0 {
					merged.Decimals = builtin.Decimals
				}
				if merged.Icon == "" {
					merged.Icon = builtin.Icon
				}
				if merged.SensibleId == "" {
					merged.SensibleId = builtin.SensibleId
				}
				if merged.BridgedPeerAssetKey == "" && builtin.BridgedPeerAssetKey != "" {
					merged.BridgedPeerAssetKey = builtin.BridgedPeerAssetKey
					merged.BridgedDualChain = true
				}
			}
			if merged.BridgedPeerAssetKey != "" {
				merged.BridgedPeerAssetKey = NormalizeAssetKey(merged.BridgedPeerAssetKey)
				merged.BridgedDualChain = true
			}
			return true, &merged
		}
	}
	if hasBuiltin {
		cp := builtin
		return true, &cp
	}
	return false, nil
}

func (a *Aggregator) fetchTokenMetadataCached(ctx context.Context, asset AssetInfo) *TokenChainMetadata {
	a.mu.Lock()
	prov := a.metaProvider
	a.mu.Unlock()
	if prov == nil || asset.AssetKey == "" {
		return nil
	}
	key := strings.ToLower(strings.TrimSpace(asset.AssetKey))
	now := time.Now()
	a.tokenMetaMu.Lock()
	if cached, ok := a.tokenMetaCache[key]; ok && now.Sub(cached.fetchedAt) < tokenMetaTTL {
		a.tokenMetaMu.Unlock()
		return cached.meta
	}
	a.tokenMetaMu.Unlock()

	meta, ok := prov.FetchTokenMetadata(ctx, asset)
	if !ok {
		return nil
	}
	a.tokenMetaMu.Lock()
	a.tokenMetaCache[key] = cachedTokenMeta{fetchedAt: now, meta: meta}
	a.tokenMetaMu.Unlock()
	return meta
}

func (a *Aggregator) resolveIconURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if strings.HasPrefix(raw, "metafile://") {
		a.mu.Lock()
		base := a.assetBaseURL
		a.mu.Unlock()
		if base == "" {
			return raw
		}
		pin := strings.TrimPrefix(raw, "metafile://")
		return base + "/" + strings.TrimLeft(pin, "/")
	}
	return raw
}

func (a *Aggregator) buildSellerDTO(address, btcAddress, metaIdHint string) SellerDTO {
	dto := SellerDTO{
		Address:      address,
		BtcAddress:   btcAddress,
		GlobalMetaId: metaIdHint,
	}
	a.mu.Lock()
	pr := a.profileReader
	pres := a.presenceReader
	a.mu.Unlock()

	if pr != nil {
		var snap *SellerProfileSnapshot
		for _, id := range []string{metaIdHint, address, btcAddress} {
			if id == "" {
				continue
			}
			snap = pr.LookupProfile(id)
			if snap != nil {
				break
			}
		}
		if snap != nil {
			if dto.Address == "" && snap.Address != "" {
				dto.Address = snap.Address
			}
			if snap.GlobalMetaId != "" {
				dto.GlobalMetaId = snap.GlobalMetaId
			} else if dto.GlobalMetaId == "" {
				dto.GlobalMetaId = snap.MetaId
			}
			dto.Name = snap.Name
			dto.Avatar = a.resolveIconURL(snap.Avatar)
			dto.HasChatPubkey = snap.HasChatPubkey
		}
	}
	if pres != nil {
		for _, id := range []string{dto.GlobalMetaId, dto.Address, dto.BtcAddress} {
			if id == "" {
				continue
			}
			online, ago := pres.PresenceStatus(id)
			if online || ago != nil {
				dto.IsOnline = online
				dto.LastSeenAgoSeconds = ago
				break
			}
		}
	}
	return dto
}

func (a *Aggregator) listingDTOFromRecord(rec *SellListingRecord) *ListingDTO {
	if rec == nil || rec.Asset == nil || rec.Lot == nil {
		return nil
	}
	accepted := rec.AcceptedRails
	if accepted == nil {
		accepted = deriveAcceptedRails(rec.Rails)
	}
	unitPrices := computeListingUnitPrices(rec)
	listedAt := rec.UpdatedAt
	if listedAt == 0 {
		listedAt = rec.CreatedAt
	}
	return &ListingDTO{
		ListingKey:      rec.ListingKey,
		SellPinId:       rec.SellPinId,
		EffectiveActive: rec.EffectiveActive,
		Asset:           a.buildMarketAssetDTO(*rec.Asset),
		Lot:             *rec.Lot,
		Rails:           rec.Rails,
		AcceptedRails:   accepted,
		UnitPrices:      unitPrices,
		BatchGroupId:    rec.BatchGroupId,
		BatchIndex:      rec.BatchIndex,
		BatchTotal:      rec.BatchTotal,
		Note:            rec.Note,
		Seller:          a.buildSellerDTO(rec.SellerAddress, rec.SellerBtcAddress, rec.SellerMetaId),
		ListedAt:        listedAt,
		CreatedAt:       rec.CreatedAt,
		ExpiresAt:       rec.ExpiresAt,
		Confirmed:       !rec.Overlay,
	}
}

func computeListingUnitPrices(rec *SellListingRecord) *ListingUnitPrices {
	if rec == nil || rec.Rails == nil || rec.Lot == nil || rec.Asset == nil {
		return nil
	}
	var up ListingUnitPrices
	hasAny := false
	if rec.Rails.MvcTokenSell != nil && rec.Rails.MvcTokenSell.PriceSats > 0 {
		if r := computeUnitPriceRat(rec.Rails.MvcTokenSell.PriceSats, rec.Lot.AmountBase, rec.Asset.Decimals); r != nil {
			s := formatRat(r, 6)
			up.SpaceUnitSats = &s
			hasAny = true
		}
	}
	if rec.Rails.BtcPsbt != nil && rec.Rails.BtcPsbt.PriceSats > 0 {
		if r := computeUnitPriceRat(rec.Rails.BtcPsbt.PriceSats, rec.Lot.AmountBase, rec.Asset.Decimals); r != nil {
			s := formatRat(r, 6)
			up.BtcUnitSats = &s
			hasAny = true
		}
	}
	if rec.Rails.Me != nil && rec.Rails.Me.AmountMilli > 0 {
		if r := computeUnitPriceRat(rec.Rails.Me.AmountMilli, rec.Lot.AmountBase, rec.Asset.Decimals); r != nil {
			s := formatRat(r, 6)
			up.MeUnitMilli = &s
			hasAny = true
		}
	}
	if !hasAny {
		return nil
	}
	return &up
}

// handleMarketTokens serves GET /api/ft/market/tokens (Contract C2.1).
func (a *Aggregator) handleMarketTokens(c *gin.Context) {
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
	chain := strings.ToLower(strings.TrimSpace(c.DefaultQuery("chain", "all")))
	if chain != "" && chain != "all" && chain != "btc" && chain != "mvc" {
		respondMarketErr(c, 40000, "invalid parameter: chain")
		return
	}
	keyword := strings.ToLower(strings.TrimSpace(c.Query("keyword")))
	verifiedOnly := parseBoolQuery(c.Query("verifiedOnly"))
	sortBy := strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort", "listed_desc")))

	var all []*TokenMarketSummary
	if zeroConfOnly(c) {
		all, _ = a.computeAllTokenSummaries(c.Request.Context(), a.nowUnix(), false)
	} else {
		all, _ = a.getOrBuildMarketSummaries(c.Request.Context())
	}

	filtered := make([]*TokenMarketSummary, 0, len(all))
	for _, s := range all {
		if chain != "" && chain != "all" && !strings.EqualFold(s.Asset.Chain, chain) {
			continue
		}
		if verifiedOnly && !s.Asset.CaVerified {
			continue
		}
		if keyword != "" && !tokenMatchesKeyword(s, keyword) {
			continue
		}
		filtered = append(filtered, s)
	}

	sort.Slice(filtered, func(i, j int) bool {
		x, y := filtered[i], filtered[j]
		switch sortBy {
		case "volume_desc":
			if x.TradeStats24h.TradeCount != y.TradeStats24h.TradeCount {
				return x.TradeStats24h.TradeCount > y.TradeStats24h.TradeCount
			}
			if x.VolumeBase24h != nil && y.VolumeBase24h != nil {
				if cmp := x.VolumeBase24h.Cmp(y.VolumeBase24h); cmp != 0 {
					return cmp > 0
				}
			}
		case "holders_desc":
			if x.HolderCount != y.HolderCount {
				return x.HolderCount > y.HolderCount
			}
		}
		// listed_desc / tie-breaker
		if x.ListedLotCount != y.ListedLotCount {
			return x.ListedLotCount > y.ListedLotCount
		}
		if x.Asset.CaVerified != y.Asset.CaVerified {
			return x.Asset.CaVerified
		}
		return x.Asset.AssetKey < y.Asset.AssetKey
	})

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

// handleMarketTokenSummary serves GET /api/ft/market/token/:assetKey/summary (Contract C2.1).
func (a *Aggregator) handleMarketTokenSummary(c *gin.Context) {
	if a == nil || a.store == nil {
		respondMarketErr(c, 50000, "ftmarket index unavailable")
		return
	}
	rawKey := strings.TrimSpace(c.Param("assetKey"))
	if decoded, err := url.PathUnescape(rawKey); err == nil && decoded != "" {
		rawKey = decoded
	}
	assetKey := a.normalizeQueryAssetKey(c.Request.Context(), rawKey)
	if assetKey == "" {
		respondMarketErr(c, 40000, "assetKey required")
		return
	}

	var byKey map[string]*TokenMarketSummary
	if zeroConfOnly(c) {
		_, byKey = a.computeAllTokenSummaries(c.Request.Context(), a.nowUnix(), false)
	} else {
		_, byKey = a.getOrBuildMarketSummaries(c.Request.Context())
	}
	summary := byKey[assetKey]
	if summary == nil {
		respondMarketErr(c, 40400, "token not found")
		return
	}
	respondMarketOK(c, summary)
}

// handleMarketListings serves GET /api/ft/market/listings (Contract C2.2).
func (a *Aggregator) handleMarketListings(c *gin.Context) {
	if a == nil || a.store == nil {
		respondMarketErr(c, 50000, "ftmarket index unavailable")
		return
	}
	size, err := parseListSize(c.Query("size"), defaultListSize, maxListSize)
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: size")
		return
	}
	offset := parseOffsetCursor(c.Query("cursor"))

	assetKey := a.normalizeQueryAssetKey(c.Request.Context(), c.Query("assetKey"))
	symbol := strings.TrimSpace(c.Query("symbol"))
	rail := strings.ToLower(strings.TrimSpace(c.DefaultQuery("rail", "any")))
	if rail == "" {
		rail = "any"
	}
	sellerFilter := strings.TrimSpace(c.Query("seller"))
	onlineOnly := parseBoolQuery(c.Query("onlineOnly"))
	sortBy := strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort", "newest")))

	maxTotalMeMilli, err := parseOptionalNonNegInt(c.Query("maxTotalMeMilli"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: maxTotalMeMilli")
		return
	}
	maxTotalSats, err := parseOptionalNonNegInt(c.Query("maxTotalSats"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: maxTotalSats")
		return
	}

	var sellerAddrs map[string]struct{}
	if sellerFilter != "" {
		sellerAddrs = a.resolvePartyAddresses(sellerFilter)
	}

	includeOverlay := !zeroConfOnly(c)
	records, err := a.listAllEffectiveListings(includeOverlay)
	if err != nil {
		respondMarketErr(c, 50000, "failed to load listings")
		return
	}

	// Filter candidates before UTXO verification so we only verify relevant records.
	candidates := make([]*SellListingRecord, 0, len(records))
	for _, rec := range records {
		if rec == nil || !rec.EffectiveActive || rec.Asset == nil || rec.Lot == nil {
			continue
		}
		if assetKey != "" && !strings.EqualFold(rec.Asset.AssetKey, assetKey) {
			continue
		}
		if symbol != "" && !strings.EqualFold(rec.Asset.Symbol, symbol) {
			continue
		}
		if !listingMatchesRail(rec, rail) {
			continue
		}
		candidates = append(candidates, rec)
	}

	// Read-time cached UTXO liveness check eliminates stale/spent orders immediately.
	a.refreshListingsLiveness(c.Request.Context(), candidates, false)

	// Disambiguation: when symbol is queried without assetKey, detect multiple matching assetKeys.
	ambiguous := a.collectAmbiguousAssets(c.Request.Context(), symbol, assetKey, includeOverlay)

	listings := make([]*ListingDTO, 0, len(candidates))
	for _, rec := range candidates {
		if !rec.EffectiveActive {
			continue
		}
		if maxTotalMeMilli != nil {
			if rec.Rails == nil || rec.Rails.Me == nil || rec.Rails.Me.AmountMilli > *maxTotalMeMilli {
				continue
			}
		}
		if maxTotalSats != nil {
			sats, ok := listingTotalSats(rec, rail)
			if !ok || sats > *maxTotalSats {
				continue
			}
		}
		dto := a.listingDTOFromRecord(rec)
		if dto == nil {
			continue
		}
		if onlineOnly && !dto.Seller.IsOnline {
			continue
		}
		if sellerFilter != "" && !listingMatchesSeller(rec, dto, sellerFilter, sellerAddrs) {
			continue
		}
		listings = append(listings, dto)
	}

	sort.Slice(listings, func(i, j int) bool {
		x, y := listings[i], listings[j]
		switch sortBy {
		case "unit_price_asc":
			xr, xok := listingSortUnitPriceRat(x, rail)
			yr, yok := listingSortUnitPriceRat(y, rail)
			if xok != yok {
				return xok
			}
			if xok {
				if cmp := xr.Cmp(yr); cmp != 0 {
					return cmp < 0
				}
			}
		case "lot_amount_asc":
			xa, xok := new(big.Int).SetString(x.Lot.AmountBase, 10)
			ya, yok := new(big.Int).SetString(y.Lot.AmountBase, 10)
			if xok && yok {
				if cmp := xa.Cmp(ya); cmp != 0 {
					return cmp < 0
				}
			}
		}
		if x.ListedAt != y.ListedAt {
			return x.ListedAt > y.ListedAt
		}
		return x.ListingKey < y.ListingKey
	})

	total := len(listings)
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
		"list":            listings[offset:end],
		"total":           total,
		"cursor":          nextCursor,
		"ambiguousAssets": ambiguous,
	})
}

func (a *Aggregator) collectAmbiguousAssets(ctx context.Context, symbol, assetKey string, includeOverlay bool) []AmbiguousAssetInfo {
	if symbol == "" || assetKey != "" {
		return []AmbiguousAssetInfo{}
	}
	var summaries []*TokenMarketSummary
	if includeOverlay == a.zeroConfTrade {
		summaries, _ = a.getOrBuildMarketSummaries(ctx)
	} else {
		summaries, _ = a.computeAllTokenSummaries(ctx, a.nowUnix(), includeOverlay)
	}
	matches := make([]AmbiguousAssetInfo, 0)
	for _, s := range summaries {
		if s == nil {
			continue
		}
		if strings.EqualFold(s.Asset.Symbol, symbol) {
			matches = append(matches, AmbiguousAssetInfo{
				AssetKey:            s.Asset.AssetKey,
				Chain:               s.Asset.Chain,
				Standard:            s.Asset.Standard,
				Symbol:              s.Asset.Symbol,
				TokenName:           s.Asset.TokenName,
				Decimals:            s.Asset.Decimals,
				CaVerified:          s.Asset.CaVerified,
				ListedLotCount:      s.ListedLotCount,
				BridgedPeerAssetKey: s.Asset.BridgedPeerAssetKey,
				BridgedDualChain:    s.Asset.BridgedDualChain,
			})
		}
	}
	if len(matches) <= 1 {
		return []AmbiguousAssetInfo{}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].CaVerified != matches[j].CaVerified {
			return matches[i].CaVerified
		}
		if matches[i].ListedLotCount != matches[j].ListedLotCount {
			return matches[i].ListedLotCount > matches[j].ListedLotCount
		}
		return matches[i].AssetKey < matches[j].AssetKey
	})
	return matches
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

func listingMatchesSeller(rec *SellListingRecord, dto *ListingDTO, raw string, addrs map[string]struct{}) bool {
	candidates := []string{
		rec.SellerAddress,
		rec.SellerBtcAddress,
		rec.SellerMetaId,
		dto.Seller.Address,
		dto.Seller.BtcAddress,
		dto.Seller.GlobalMetaId,
	}
	for _, id := range candidates {
		if id == "" {
			continue
		}
		if strings.EqualFold(id, raw) {
			return true
		}
		if _, ok := addrs[strings.ToLower(id)]; ok {
			return true
		}
	}
	return false
}

func listingMatchesRail(rec *SellListingRecord, rail string) bool {
	if rail == "" || rail == "any" {
		return true
	}
	if rec.Rails != nil {
		switch rail {
		case "mvc-tokensell":
			return rec.Rails.MvcTokenSell != nil
		case "me-escrow", "me":
			return rec.Rails.Me != nil
		case "btc-psbt":
			return rec.Rails.BtcPsbt != nil
		case "fiat-escrow", "fiat":
			return rec.Rails.Fiat != nil
		}
	}
	for _, r := range rec.AcceptedRails {
		if strings.EqualFold(r, rail) {
			return true
		}
	}
	return false
}

func listingTotalSats(rec *SellListingRecord, rail string) (int64, bool) {
	if rec == nil || rec.Rails == nil {
		return 0, false
	}
	switch rail {
	case "mvc-tokensell":
		if rec.Rails.MvcTokenSell != nil && rec.Rails.MvcTokenSell.PriceSats > 0 {
			return rec.Rails.MvcTokenSell.PriceSats, true
		}
	case "btc-psbt":
		if rec.Rails.BtcPsbt != nil && rec.Rails.BtcPsbt.PriceSats > 0 {
			return rec.Rails.BtcPsbt.PriceSats, true
		}
	default:
		if rec.Rails.MvcTokenSell != nil && rec.Rails.MvcTokenSell.PriceSats > 0 {
			return rec.Rails.MvcTokenSell.PriceSats, true
		}
		if rec.Rails.BtcPsbt != nil && rec.Rails.BtcPsbt.PriceSats > 0 {
			return rec.Rails.BtcPsbt.PriceSats, true
		}
	}
	return 0, false
}

func listingSortUnitPriceRat(dto *ListingDTO, rail string) (*big.Rat, bool) {
	if dto == nil || dto.Rails == nil {
		return nil, false
	}
	switch rail {
	case "mvc-tokensell":
		if dto.Rails.MvcTokenSell != nil && dto.Rails.MvcTokenSell.PriceSats > 0 {
			r := computeUnitPriceRat(dto.Rails.MvcTokenSell.PriceSats, dto.Lot.AmountBase, dto.Asset.Decimals)
			return r, r != nil
		}
	case "btc-psbt":
		if dto.Rails.BtcPsbt != nil && dto.Rails.BtcPsbt.PriceSats > 0 {
			r := computeUnitPriceRat(dto.Rails.BtcPsbt.PriceSats, dto.Lot.AmountBase, dto.Asset.Decimals)
			return r, r != nil
		}
	case "me-escrow", "me":
		if dto.Rails.Me != nil && dto.Rails.Me.AmountMilli > 0 {
			r := computeUnitPriceRat(dto.Rails.Me.AmountMilli, dto.Lot.AmountBase, dto.Asset.Decimals)
			return r, r != nil
		}
	default:
		if dto.Rails.MvcTokenSell != nil && dto.Rails.MvcTokenSell.PriceSats > 0 {
			r := computeUnitPriceRat(dto.Rails.MvcTokenSell.PriceSats, dto.Lot.AmountBase, dto.Asset.Decimals)
			return r, r != nil
		}
		if dto.Rails.BtcPsbt != nil && dto.Rails.BtcPsbt.PriceSats > 0 {
			r := computeUnitPriceRat(dto.Rails.BtcPsbt.PriceSats, dto.Lot.AmountBase, dto.Asset.Decimals)
			return r, r != nil
		}
		if dto.Rails.Me != nil && dto.Rails.Me.AmountMilli > 0 {
			r := computeUnitPriceRat(dto.Rails.Me.AmountMilli, dto.Lot.AmountBase, dto.Asset.Decimals)
			return r, r != nil
		}
	}
	return nil, false
}

func tokenMatchesKeyword(s *TokenMarketSummary, kw string) bool {
	if s == nil {
		return false
	}
	if strings.Contains(strings.ToLower(s.Asset.Symbol), kw) ||
		strings.Contains(strings.ToLower(s.Asset.TokenName), kw) ||
		strings.Contains(strings.ToLower(s.Asset.AssetKey), kw) {
		return true
	}
	if s.Asset.Mrc20Id != nil && strings.Contains(strings.ToLower(*s.Asset.Mrc20Id), kw) {
		return true
	}
	if s.Asset.Codehash != nil && strings.Contains(strings.ToLower(*s.Asset.Codehash), kw) {
		return true
	}
	if s.Asset.Genesis != nil && strings.Contains(strings.ToLower(*s.Asset.Genesis), kw) {
		return true
	}
	if s.Asset.SensibleId != nil && strings.Contains(strings.ToLower(*s.Asset.SensibleId), kw) {
		return true
	}
	return false
}

func computeUnitPriceRat(totalPrice int64, amountBase string, decimals int) *big.Rat {
	if totalPrice <= 0 {
		return nil
	}
	amt, ok := new(big.Int).SetString(strings.TrimSpace(amountBase), 10)
	if !ok || amt.Sign() <= 0 {
		return nil
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	num := new(big.Int).Mul(big.NewInt(totalPrice), scale)
	return new(big.Rat).SetFrac(num, amt)
}

func formatBaseAmount(base *big.Int, decimals int) string {
	if base == nil || base.Sign() <= 0 {
		return "0"
	}
	if decimals <= 0 {
		return base.String()
	}
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	quot, rem := new(big.Int).QuoRem(base, scale, new(big.Int))
	if rem.Sign() == 0 {
		return quot.String()
	}
	remStr := fmt.Sprintf("%0*s", decimals, rem.String())
	remStr = strings.TrimRight(remStr, "0")
	return quot.String() + "." + remStr
}

func formatRat(r *big.Rat, maxFrac int) string {
	if r == nil {
		return "0"
	}
	if maxFrac < 0 {
		maxFrac = 0
	}
	s := r.FloatString(maxFrac)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	if s == "" || s == "-0" {
		return "0"
	}
	return s
}

func assetInfoFromCert(c CertifiedTokenInfo) AssetInfo {
	registerCertSensibleMapping(c)
	key := NormalizeAssetKey(c.AssetKey)
	a := AssetInfo{
		AssetKey:  key,
		Symbol:    c.Symbol,
		TokenName: firstNonEmpty(c.DisplayName, c.TokenName),
		Decimals:  c.Decimals,
		Icon:      c.Icon,
	}
	if strings.TrimSpace(c.SensibleId) != "" {
		sid := strings.ToLower(strings.TrimSpace(c.SensibleId))
		a.SensibleId = &sid
	}
	if strings.HasPrefix(key, "mrc20:") {
		id := strings.TrimPrefix(key, "mrc20:")
		a.Chain = "btc"
		a.Standard = "mrc20"
		a.Mrc20Id = &id
	} else if strings.HasPrefix(key, "mvc-ft:") {
		parts := strings.Split(strings.TrimPrefix(key, "mvc-ft:"), ":")
		a.Chain = "mvc"
		a.Standard = "metacontract-ft"
		if len(parts) == 2 {
			ch, gen := parts[0], parts[1]
			a.Codehash = &ch
			a.Genesis = &gen
		}
	}
	normalizeAssetInfoInPlace(&a)
	return a
}

func assetInfoFromTrade(tr *TradeRecord) AssetInfo {
	a := AssetInfo{
		AssetKey: strings.ToLower(strings.TrimSpace(tr.AssetKey)),
		Chain:    tr.Chain,
		Symbol:   tr.Symbol,
		Decimals: tr.Decimals,
	}
	if strings.HasPrefix(a.AssetKey, "mrc20:") {
		id := strings.TrimPrefix(a.AssetKey, "mrc20:")
		a.Chain = "btc"
		a.Standard = "mrc20"
		a.Mrc20Id = &id
	} else if strings.HasPrefix(a.AssetKey, "mvc-ft:") {
		parts := strings.Split(strings.TrimPrefix(a.AssetKey, "mvc-ft:"), ":")
		a.Chain = "mvc"
		a.Standard = "metacontract-ft"
		if len(parts) == 2 {
			ch, gen := parts[0], parts[1]
			a.Codehash = &ch
			a.Genesis = &gen
		}
	}
	return a
}

func zeroConfOnly(c *gin.Context) bool {
	return c.Query("confirmedOnly") == "1"
}

func parseListSize(raw string, defSize, maxSize int) (int, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return defSize, nil
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 1 {
		return 0, ErrInvalidParameter
	}
	if n > maxSize {
		n = maxSize
	}
	return n, nil
}

func parseOffsetCursor(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func parseBoolQuery(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func parseOptionalNonNegInt(raw string) (*int64, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, nil
	}
	v, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || v < 0 {
		return nil, ErrInvalidParameter
	}
	return &v, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
