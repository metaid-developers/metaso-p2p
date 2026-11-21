package mrc721

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
)

// FloorSellerDTO identifies the floor-listing seller on CollectionMarketSummary.
type FloorSellerDTO struct {
	GlobalMetaId     string `json:"globalMetaId"`
	Address          string `json:"address"`
	Name             string `json:"name"`
	Avatar           string `json:"avatar"`
	IsOnline         bool   `json:"isOnline"`
	ShopMetaAppPinId string `json:"shopMetaAppPinId,omitempty"`
	ItemPinId        string `json:"itemPinId"`
	ItemName         string `json:"itemName"`
}

// CollectionMarketSummary is the aggregated collection market summary (Contract C3.2).
type CollectionMarketSummary struct {
	CollectionPinId     string                `json:"collectionPinId"`
	CollectionName      string                `json:"collectionName"`
	DisplayTitle        string                `json:"displayTitle"`
	Cover               string                `json:"cover"`
	Desc                string                `json:"desc"`
	CaVerified          bool                  `json:"caVerified"`
	TotalSupply         int64                 `json:"totalSupply"`
	MintedCount         int64                 `json:"mintedCount"`
	BurnedCount         int                   `json:"burnedCount"`
	HolderCount         int                   `json:"holderCount"`
	ListedCount         int                   `json:"listedCount"`
	ListedMeCount       int                   `json:"listedMeCount"`
	ListedCnyCount      int                   `json:"listedCnyCount"`
	FloorPriceMeMilli   *int64                `json:"floorPriceMeMilli"`
	MedianPriceMeMilli  *int64                `json:"medianPriceMeMilli"`
	FloorPriceCnyCents  *int64                `json:"floorPriceCnyCents"`
	MedianPriceCnyCents *int64                `json:"medianPriceCnyCents"`
	FloorSeller         *FloorSellerDTO       `json:"floorSeller"`
	TradeStats          CollectionTradeStats  `json:"tradeStats"`
	CreateTime          int64                 `json:"-"`
}

// SellerDTO is the enriched seller sub-object on ListingDTO (Contract C3.3).
type SellerDTO struct {
	Address            string `json:"address"`
	GlobalMetaId       string `json:"globalMetaId"`
	Name               string `json:"name"`
	Avatar             string `json:"avatar"`
	HasChatPubkey      bool   `json:"hasChatPubkey"`
	IsOnline           bool   `json:"isOnline"`
	LastSeenAgoSeconds *int64 `json:"lastSeenAgoSeconds,omitempty"`
}

// ListingDTO is the enriched market listing shape (Contract C3.3).
type ListingDTO struct {
	IntentPinId        string        `json:"intentPinId"`
	ItemPinId          string        `json:"itemPinId"`
	CollectionPinId    string        `json:"collectionPinId"`
	CollectionName     string        `json:"collectionName"`
	CaVerified         bool          `json:"caVerified"`
	ItemName           string        `json:"itemName"`
	ItemNumber         int64         `json:"itemNumber,omitempty"`
	Cover              string        `json:"cover"`
	Metadata           string        `json:"metadata"`
	Prices             *SellPrices   `json:"prices"`
	AutoFulfillMe      bool          `json:"autoFulfillMe"`
	AutoFulfillTokens  bool          `json:"autoFulfillTokens"`
	Negotiable         bool          `json:"negotiable"`
	AcceptedCurrencies []string      `json:"acceptedCurrencies"`
	Settlement         []string      `json:"settlement"`
	Note               string        `json:"note"`
	ShopMetaAppPinId   string        `json:"shopMetaAppPinId"`
	Seller             SellerDTO     `json:"seller"`
	ListedAt           int64         `json:"listedAt"`
	ExpiresAt          int64         `json:"expiresAt"`
	Confirmed          bool          `json:"confirmed"`
	PriceHintSats      int64         `json:"priceHintSats,omitempty"`
	Desc               string        `json:"-"`
}

// HolderDTO is one row of GET /api/mrc721/collection/holders (Contract C3.4).
type HolderDTO struct {
	Address         string `json:"address"`
	GlobalMetaId    string `json:"globalMetaId"`
	Name            string `json:"name"`
	Avatar          string `json:"avatar"`
	IsOnline        bool   `json:"isOnline"`
	HasChatPubkey   bool   `json:"hasChatPubkey"`
	ItemCount       int    `json:"itemCount"`
	ListedItemCount int    `json:"listedItemCount"`
}

type marketCacheSnapshot struct {
	builtAtSec int64
	summaries  []*CollectionMarketSummary
	byPinId    map[string]*CollectionMarketSummary
}

func (a *Aggregator) invalidateMarketCache() {
	if a == nil {
		return
	}
	a.marketMu.Lock()
	a.marketCache = nil
	a.marketMu.Unlock()
}

// RefreshMarketCache invalidates cached market summaries (e.g. when CA changes).
func (a *Aggregator) RefreshMarketCache() {
	a.invalidateMarketCache()
}

const marketCacheTTLSeconds = 5

func (a *Aggregator) getOrBuildMarketSummaries() ([]*CollectionMarketSummary, map[string]*CollectionMarketSummary) {
	nowSec := a.nowUnix()
	a.marketMu.RLock()
	cached := a.marketCache
	a.marketMu.RUnlock()
	if cached != nil && nowSec-cached.builtAtSec < marketCacheTTLSeconds {
		return cached.summaries, cached.byPinId
	}

	summaries, byPinId := a.computeAllCollectionSummaries(nowSec, a.zeroConfTrade)
	a.marketMu.Lock()
	a.marketCache = &marketCacheSnapshot{
		builtAtSec: nowSec,
		summaries:  summaries,
		byPinId:    byPinId,
	}
	a.marketMu.Unlock()
	return summaries, byPinId
}

func (a *Aggregator) computeAllCollectionSummaries(nowSec int64, includeZeroConf bool) ([]*CollectionMarketSummary, map[string]*CollectionMarketSummary) {
	a.mu.Lock()
	nftReader := a.nftReader
	caReader := a.caReader
	zeroConf := includeZeroConf && a.zeroConfTrade
	a.mu.Unlock()

	summaries := make([]*CollectionMarketSummary, 0)
	byPinId := make(map[string]*CollectionMarketSummary)

	ensureCollection := func(pinId, colName, displayTitle, cover, desc string, supply, minted, createTime int64) *CollectionMarketSummary {
		key := strings.ToLower(strings.TrimSpace(pinId))
		if key == "" {
			return nil
		}
		if existing, ok := byPinId[key]; ok {
			return existing
		}
		caVerified := false
		caTitle := ""
		if caReader != nil {
			caVerified, caTitle = caReader.IsCollectionCertified(pinId)
		}
		if caTitle != "" {
			displayTitle = caTitle
		}
		if displayTitle == "" {
			displayTitle = colName
		}
		s := &CollectionMarketSummary{
			CollectionPinId: pinId,
			CollectionName:  colName,
			DisplayTitle:    displayTitle,
			Cover:           cover,
			Desc:            desc,
			CaVerified:      caVerified,
			TotalSupply:     supply,
			MintedCount:     minted,
			CreateTime:      createTime,
		}
		byPinId[key] = s
		summaries = append(summaries, s)
		return s
	}

	if nftReader != nil {
		for _, col := range nftReader.ListAllCollections(zeroConf) {
			if col == nil || col.PinId == "" {
				continue
			}
			title := col.Name
			if title == "" {
				title = col.CollectionName
			}
			s := ensureCollection(col.PinId, col.CollectionName, title, col.Cover, col.Desc, col.TotalSupply, col.TotalNum, col.CreateTime)
			if s != nil {
				if hc, _, err := nftReader.CollectionHolderStats(col.PinId, zeroConf); err == nil {
					s.HolderCount = hc
				}
				s.BurnedCount = nftReader.CollectionBurnedCount(col.PinId, zeroConf)
			}
		}
	}

	// Group active listings by collectionPinId.
	intents, _ := a.listAllEffectiveSellIntents(zeroConf)
	mePricesByCol := make(map[string][]int64)
	cnyPricesByCol := make(map[string][]int64)
	floorListingByCol := make(map[string]*SellIntentRecord)
	floorListingCnyByCol := make(map[string]*SellIntentRecord)
	floorListingAnyByCol := make(map[string]*SellIntentRecord)

	for _, rec := range intents {
		if rec == nil || !rec.Active {
			continue
		}
		cid := strings.TrimSpace(rec.CollectionPinId)
		colName := strings.TrimSpace(rec.CollectionName)
		if cid == "" && nftReader != nil {
			if info := nftReader.ItemSummary(rec.ItemPinId, zeroConf); info != nil {
				cid = info.CollectionPinId
				if colName == "" {
					colName = info.CollectionName
				}
			}
		}
		if cid == "" {
			continue
		}
		key := strings.ToLower(cid)
		s := ensureCollection(cid, colName, colName, "", "", -1, 0, rec.CreatedAt)
		if s == nil {
			continue
		}
		s.ListedCount++
		if floorListingAnyByCol[key] == nil {
			floorListingAnyByCol[key] = rec
		}
		if rec.Prices != nil && rec.Prices.Me != nil && rec.Prices.Me.AmountMilli > 0 {
			p := rec.Prices.Me.AmountMilli
			s.ListedMeCount++
			mePricesByCol[key] = append(mePricesByCol[key], p)
			cur := floorListingByCol[key]
			if cur == nil || p < cur.Prices.Me.AmountMilli {
				floorListingByCol[key] = rec
			}
		}
		if rec.Prices != nil && rec.Prices.Fiat != nil && rec.Prices.Fiat.AmountCents > 0 && strings.EqualFold(rec.Prices.Fiat.Currency, "CNY") {
			p := rec.Prices.Fiat.AmountCents
			s.ListedCnyCount++
			cnyPricesByCol[key] = append(cnyPricesByCol[key], p)
			cur := floorListingCnyByCol[key]
			if cur == nil || p < cur.Prices.Fiat.AmountCents {
				floorListingCnyByCol[key] = rec
			}
		}
	}

	tradeStatsMap := a.collectionTradeStatsMap(nowSec, zeroConf)

	for key, s := range byPinId {
		if prices := mePricesByCol[key]; len(prices) > 0 {
			sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })
			floor := prices[0]
			med := medianMeMilli(prices)
			s.FloorPriceMeMilli = &floor
			s.MedianPriceMeMilli = &med
		}
		if prices := cnyPricesByCol[key]; len(prices) > 0 {
			sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })
			floor := prices[0]
			med := medianCnyCents(prices)
			s.FloorPriceCnyCents = &floor
			s.MedianPriceCnyCents = &med
		}
		floorRec := floorListingByCol[key]
		if floorRec == nil {
			floorRec = floorListingCnyByCol[key]
		}
		if floorRec == nil {
			floorRec = floorListingAnyByCol[key]
		}
		if floorRec != nil {
			s.FloorSeller = a.buildFloorSellerDTO(floorRec)
		}
		if st, ok := tradeStatsMap[key]; ok {
			s.TradeStats = st
		}
	}
	return summaries, byPinId
}

// medianMeMilli returns the median of sorted ME prices; for even counts it averages
// the two middle prices and floors to the nearest 100 milli-ME (REQ-B-04).
func medianMeMilli(sorted []int64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	avg := (sorted[n/2-1] + sorted[n/2]) / 2
	return (avg / 100) * 100
}

func medianCnyCents(sorted []int64) int64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func (a *Aggregator) buildFloorSellerDTO(rec *SellIntentRecord) *FloorSellerDTO {
	seller := a.buildSellerDTO(rec.SellerAddress, rec.SellerMetaId)
	itemName := rec.ItemPinId
	a.mu.Lock()
	nftReader := a.nftReader
	zeroConf := a.zeroConfTrade
	a.mu.Unlock()
	if nftReader != nil {
		if info := nftReader.ItemSummary(rec.ItemPinId, zeroConf); info != nil && info.Name != "" {
			itemName = info.Name
		}
	}
	return &FloorSellerDTO{
		GlobalMetaId:     seller.GlobalMetaId,
		Address:          seller.Address,
		Name:             seller.Name,
		Avatar:           seller.Avatar,
		IsOnline:         seller.IsOnline,
		ShopMetaAppPinId: rec.ShopMetaAppPinId,
		ItemPinId:        rec.ItemPinId,
		ItemName:         itemName,
	}
}

func (a *Aggregator) buildSellerDTO(address, metaIdHint string) SellerDTO {
	dto := SellerDTO{
		Address:      address,
		GlobalMetaId: metaIdHint,
	}
	a.mu.Lock()
	pr := a.profileReader
	pres := a.presenceReader
	a.mu.Unlock()

	if pr != nil {
		var snap *SellerProfileSnapshot
		if address != "" {
			snap = pr.LookupProfile(address)
		}
		if snap == nil && metaIdHint != "" {
			snap = pr.LookupProfile(metaIdHint)
		}
		if snap != nil {
			if snap.Address != "" {
				dto.Address = snap.Address
			}
			if snap.GlobalMetaId != "" {
				dto.GlobalMetaId = snap.GlobalMetaId
			} else if dto.GlobalMetaId == "" {
				dto.GlobalMetaId = snap.MetaId
			}
			dto.Name = snap.Name
			dto.Avatar = snap.Avatar
			dto.HasChatPubkey = snap.HasChatPubkey
		}
	}
	if pres != nil {
		id := dto.GlobalMetaId
		if id == "" {
			id = dto.Address
		}
		if id != "" {
			dto.IsOnline, dto.LastSeenAgoSeconds = pres.PresenceStatus(id)
		}
	}
	return dto
}

func (a *Aggregator) withLiveFloorSellerPresence(s *CollectionMarketSummary) *CollectionMarketSummary {
	if s == nil {
		return nil
	}
	cp := *s
	if s.FloorSeller != nil {
		fs := *s.FloorSeller
		a.mu.Lock()
		pres := a.presenceReader
		a.mu.Unlock()
		if pres != nil {
			id := fs.GlobalMetaId
			if id == "" {
				id = fs.Address
			}
			if id != "" {
				fs.IsOnline, _ = pres.PresenceStatus(id)
			}
		}
		cp.FloorSeller = &fs
	}
	return &cp
}

// EnrichCollection implements mrc721nft.CellEnricherRef (REQ-B-04).
func (a *Aggregator) EnrichCollection(collectionPinId string) *mrc721nft.CollectionMarketFields {
	if a == nil || strings.TrimSpace(collectionPinId) == "" {
		return nil
	}
	_, byPinId := a.getOrBuildMarketSummaries()
	s := byPinId[strings.ToLower(strings.TrimSpace(collectionPinId))]
	if s == nil {
		caVerified := false
		caTitle := ""
		a.mu.Lock()
		ca := a.caReader
		a.mu.Unlock()
		if ca != nil {
			caVerified, caTitle = ca.IsCollectionCertified(collectionPinId)
		}
		return &mrc721nft.CollectionMarketFields{
			CaVerified:     caVerified,
			CaDisplayTitle: caTitle,
		}
	}
	return &mrc721nft.CollectionMarketFields{
		CaVerified:          s.CaVerified,
		CaDisplayTitle:      s.DisplayTitle,
		HolderCount:         s.HolderCount,
		ListedCount:         s.ListedCount,
		FloorPriceMeMilli:   s.FloorPriceMeMilli,
		FloorPriceCnyCents:  s.FloorPriceCnyCents,
		MedianPriceMeMilli:  s.MedianPriceMeMilli,
		MedianPriceCnyCents: s.MedianPriceCnyCents,
	}
}

// EnrichItemDetail implements mrc721nft.ItemDetailEnricher (REQ-B-06).
func (a *Aggregator) EnrichItemDetail(itemPinId, ownerAddress string) (*mrc721nft.OwnerProfileDTO, any) {
	if a == nil {
		return nil, nil
	}
	seller := a.buildSellerDTO(ownerAddress, "")
	ownerProfile := &mrc721nft.OwnerProfileDTO{
		Address:            seller.Address,
		GlobalMetaId:       seller.GlobalMetaId,
		Name:               seller.Name,
		Avatar:             seller.Avatar,
		HasChatPubkey:      seller.HasChatPubkey,
		IsOnline:           seller.IsOnline,
		LastSeenAgoSeconds: seller.LastSeenAgoSeconds,
	}
	var activeListing any
	if rec, err := a.SellIntentInfo(itemPinId); err == nil && rec != nil && rec.Active {
		activeListing = a.listingDTOFromRecord(rec)
	}
	return ownerProfile, activeListing
}

func (a *Aggregator) listingDTOFromRecord(rec *SellIntentRecord) *ListingDTO {
	if rec == nil {
		return nil
	}
	a.mu.Lock()
	nftReader := a.nftReader
	caReader := a.caReader
	zeroConf := a.zeroConfTrade
	a.mu.Unlock()

	colPinId := rec.CollectionPinId
	colName := rec.CollectionName
	itemName := ""
	cover := ""
	metadata := ""
	desc := ""
	var itemNum int64
	if nftReader != nil {
		if info := nftReader.ItemSummary(rec.ItemPinId, zeroConf); info != nil {
			if colPinId == "" {
				colPinId = info.CollectionPinId
			}
			if colName == "" {
				colName = info.CollectionName
			}
			itemName = info.Name
			cover = info.Cover
			metadata = info.Metadata
			desc = info.Desc
			itemNum = info.Number
		}
	}
	caVerified := false
	if caReader != nil && colPinId != "" {
		caVerified, _ = caReader.IsCollectionCertified(colPinId)
	}
	accepted := mergeAcceptedCurrenciesWithTokens(rec.AcceptedCurrencies, rec.Prices)
	settlement := rec.Settlement
	if settlement == nil {
		settlement = []string{}
	}
	return &ListingDTO{
		IntentPinId:        rec.SellPinId,
		ItemPinId:          rec.ItemPinId,
		CollectionPinId:    colPinId,
		CollectionName:     colName,
		CaVerified:         caVerified,
		ItemName:           itemName,
		ItemNumber:         itemNum,
		Cover:              cover,
		Metadata:           metadata,
		Prices:             rec.Prices,
		AutoFulfillMe:      rec.EffectiveAutoFulfillMe(),
		AutoFulfillTokens:  rec.EffectiveAutoFulfillTokens(),
		Negotiable:         rec.Negotiable,
		AcceptedCurrencies: accepted,
		Settlement:         settlement,
		Note:               rec.Note,
		ShopMetaAppPinId:   rec.ShopMetaAppPinId,
		Seller:             a.buildSellerDTO(rec.SellerAddress, rec.SellerMetaId),
		ListedAt:           rec.UpdatedAt,
		ExpiresAt:          rec.ExpiresAt,
		Confirmed:          !rec.Overlay,
		PriceHintSats:      rec.PriceHintSats,
		Desc:               desc,
	}
}

// handleMarketCollections serves GET /api/mrc721/market/collections (REQ-B-04, Contract C3.2).
func (a *Aggregator) handleMarketCollections(c *gin.Context) {
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
	caOnly := parseBoolQuery(c.Query("caOnly"))
	keyword := strings.ToLower(strings.TrimSpace(c.Query("keyword")))
	sortBy := strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort", "listed_desc")))

	var allSummaries []*CollectionMarketSummary
	if zeroConfOnly(c) {
		allSummaries, _ = a.computeAllCollectionSummaries(a.nowUnix(), false)
	} else {
		allSummaries, _ = a.getOrBuildMarketSummaries()
	}
	filtered := make([]*CollectionMarketSummary, 0, len(allSummaries))
	for _, s := range allSummaries {
		if caOnly && !s.CaVerified {
			continue
		}
		if keyword != "" &&
			!strings.Contains(strings.ToLower(s.CollectionName), keyword) &&
			!strings.Contains(strings.ToLower(s.DisplayTitle), keyword) &&
			!strings.Contains(strings.ToLower(s.Desc), keyword) &&
			!strings.Contains(strings.ToLower(s.CollectionPinId), keyword) {
			continue
		}
		filtered = append(filtered, a.withLiveFloorSellerPresence(s))
	}

	sort.Slice(filtered, func(i, j int) bool {
		x, y := filtered[i], filtered[j]
		switch sortBy {
		case "volume_desc":
			if x.TradeStats.VolumeMeMilli24h != y.TradeStats.VolumeMeMilli24h {
				return x.TradeStats.VolumeMeMilli24h > y.TradeStats.VolumeMeMilli24h
			}
		case "floor_me_asc":
			if x.FloorPriceMeMilli != nil && y.FloorPriceMeMilli != nil {
				if *x.FloorPriceMeMilli != *y.FloorPriceMeMilli {
					return *x.FloorPriceMeMilli < *y.FloorPriceMeMilli
				}
			} else if x.FloorPriceMeMilli != nil {
				return true
			} else if y.FloorPriceMeMilli != nil {
				return false
			}
		case "newest":
			if x.CreateTime != y.CreateTime {
				return x.CreateTime > y.CreateTime
			}
		default: // listed_desc
			if x.ListedCount != y.ListedCount {
				return x.ListedCount > y.ListedCount
			}
			if x.CaVerified != y.CaVerified {
				return x.CaVerified
			}
		}
		if x.ListedCount != y.ListedCount {
			return x.ListedCount > y.ListedCount
		}
		return x.CollectionPinId < y.CollectionPinId
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

// handleMarketCollectionSummary serves GET /api/mrc721/market/collection/:collectionPinId/summary.
func (a *Aggregator) handleMarketCollectionSummary(c *gin.Context) {
	if a == nil || a.store == nil {
		respondMarketErr(c, 50000, "mrc721 index unavailable")
		return
	}
	collectionPinId := strings.TrimSpace(c.Param("collectionPinId"))
	if collectionPinId == "" {
		respondMarketErr(c, 40000, "collectionPinId required")
		return
	}
	var byPinId map[string]*CollectionMarketSummary
	if zeroConfOnly(c) {
		_, byPinId = a.computeAllCollectionSummaries(a.nowUnix(), false)
	} else {
		_, byPinId = a.getOrBuildMarketSummaries()
	}
	s := byPinId[strings.ToLower(collectionPinId)]
	if s == nil {
		respondMarketErr(c, 40400, "collection not found")
		return
	}
	respondMarketOK(c, a.withLiveFloorSellerPresence(s))
}

// handleMarketListings serves GET /api/mrc721/market/listings (REQ-B-05, Contract C3.3).
func (a *Aggregator) handleMarketListings(c *gin.Context) {
	if a == nil || a.store == nil {
		respondMarketErr(c, 50000, "mrc721 index unavailable")
		return
	}
	sizeRaw := strings.TrimSpace(c.Query("size"))
	if sizeRaw == "" {
		sizeRaw = strings.TrimSpace(c.Query("limit"))
	}
	size, err := parseListSize(sizeRaw)
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: size")
		return
	}
	offset := parseOffsetCursor(c.Query("cursor"))

	collectionPinId := strings.TrimSpace(c.Query("collectionPinId"))
	sellerFilter := strings.TrimSpace(c.Query("seller"))
	if sellerFilter == "" {
		sellerFilter = strings.TrimSpace(c.Query("sellerGlobalMetaId"))
	}
	shopMetaAppPinId := strings.TrimSpace(c.Query("shopMetaAppPinId"))
	tokenAssetKey := normalizeTokenAssetKey(c.Query("tokenAssetKey"))
	currency := strings.ToLower(strings.TrimSpace(c.DefaultQuery("currency", "any")))
	if currency == "cny" {
		currency = "fiat:cny"
	} else if strings.HasPrefix(currency, "ft:") && tokenAssetKey == "" {
		tokenAssetKey = normalizeTokenAssetKey(strings.TrimPrefix(currency, "ft:"))
	}
	onlineOnly := parseBoolQuery(c.Query("onlineOnly"))
	caOnly := parseBoolQuery(c.Query("caOnly"))
	keyword := strings.ToLower(strings.TrimSpace(c.Query("keyword")))
	sortBy := strings.ToLower(strings.TrimSpace(c.DefaultQuery("sort", "newest")))
	switch sortBy {
	case "recent":
		sortBy = "newest"
	case "price_asc":
		if currency == "fiat:cny" {
			sortBy = "price_cny_asc"
		} else {
			sortBy = "price_me_asc"
		}
	case "price_desc":
		if currency == "fiat:cny" {
			sortBy = "price_cny_desc"
		} else {
			sortBy = "price_me_desc"
		}
	}

	autoFulfillFilter, err := parseOptionalBoolQuery(c.Query("autoFulfillMe"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: autoFulfillMe")
		return
	}
	autoFulfillTokensFilter, err := parseOptionalBoolQuery(c.Query("autoFulfillTokens"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: autoFulfillTokens")
		return
	}
	negotiableFilter, err := parseOptionalBoolQuery(c.Query("negotiable"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: negotiable")
		return
	}

	minPrice, err := parseOptionalNonNegInt(c.Query("minPrice"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: minPrice")
		return
	}
	maxPrice, err := parseOptionalNonNegInt(c.Query("maxPrice"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: maxPrice")
		return
	}
	minPriceMeMilli, err := parseOptionalNonNegInt(c.Query("minPriceMeMilli"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: minPriceMeMilli")
		return
	}
	maxPriceMeMilli, err := parseOptionalNonNegInt(c.Query("maxPriceMeMilli"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: maxPriceMeMilli")
		return
	}
	minPriceCnyCents, err := parseOptionalNonNegInt(c.Query("minPriceCnyCents"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: minPriceCnyCents")
		return
	}
	maxPriceCnyCents, err := parseOptionalNonNegInt(c.Query("maxPriceCnyCents"))
	if err != nil {
		respondMarketErr(c, 40000, "invalid parameter: maxPriceCnyCents")
		return
	}

	var sellerAddrs map[string]struct{}
	if sellerFilter != "" {
		sellerAddrs = a.resolvePartyAddresses(sellerFilter)
	}

	var tokenIndexedItems map[string]struct{}
	if tokenAssetKey != "" {
		tokenIndexedItems, _ = a.listTokenIndexedItemPinIds(tokenAssetKey)
	}

	confirmedOnly := zeroConfOnly(c)
	intents, err := a.listAllEffectiveSellIntents(!confirmedOnly)
	if err != nil {
		respondMarketErr(c, 50000, "failed to load listings")
		return
	}
	listings := make([]*ListingDTO, 0)
	for _, rec := range intents {
		if rec == nil || !rec.Active {
			continue
		}
		if confirmedOnly && rec.Overlay {
			continue
		}
		if tokenAssetKey != "" {
			_, inIndex := tokenIndexedItems[rec.ItemPinId]
			if !inIndex && !recordHasTokenAssetKey(rec, tokenAssetKey) {
				continue
			}
			if !recordHasTokenAssetKey(rec, tokenAssetKey) {
				continue
			}
		}
		dto := a.listingDTOFromRecord(rec)
		if collectionPinId != "" && !strings.EqualFold(dto.CollectionPinId, collectionPinId) {
			continue
		}
		if shopMetaAppPinId != "" && !strings.EqualFold(dto.ShopMetaAppPinId, shopMetaAppPinId) {
			continue
		}
		if autoFulfillFilter != nil && dto.AutoFulfillMe != *autoFulfillFilter {
			continue
		}
		if autoFulfillTokensFilter != nil && dto.AutoFulfillTokens != *autoFulfillTokensFilter {
			continue
		}
		if negotiableFilter != nil && dto.Negotiable != *negotiableFilter {
			continue
		}
		if caOnly && !dto.CaVerified {
			continue
		}
		if onlineOnly && !dto.Seller.IsOnline {
			continue
		}
		if sellerFilter != "" && !listingMatchesSeller(rec, dto, sellerFilter, sellerAddrs) {
			continue
		}
		if !listingMatchesPriceFilter(dto, currency, minPrice, maxPrice) {
			continue
		}
		if minPriceMeMilli != nil || maxPriceMeMilli != nil {
			p, ok := listingMePrice(dto)
			if !ok || (minPriceMeMilli != nil && p < *minPriceMeMilli) || (maxPriceMeMilli != nil && p > *maxPriceMeMilli) {
				continue
			}
		}
		if minPriceCnyCents != nil || maxPriceCnyCents != nil {
			p, ok := listingCnyPrice(dto)
			if !ok || (minPriceCnyCents != nil && p < *minPriceCnyCents) || (maxPriceCnyCents != nil && p > *maxPriceCnyCents) {
				continue
			}
		}
		if keyword != "" && !listingMatchesKeyword(dto, keyword) {
			continue
		}
		listings = append(listings, dto)
	}

	sort.Slice(listings, func(i, j int) bool {
		x, y := listings[i], listings[j]
		switch sortBy {
		case "price_me_asc":
			xm, xok := listingMePrice(x)
			ym, yok := listingMePrice(y)
			if xok != yok {
				return xok
			}
			if xok && xm != ym {
				return xm < ym
			}
		case "price_me_desc":
			xm, xok := listingMePrice(x)
			ym, yok := listingMePrice(y)
			if xok != yok {
				return xok
			}
			if xok && xm != ym {
				return xm > ym
			}
		case "price_cny_asc":
			xc, xok := listingCnyPrice(x)
			yc, yok := listingCnyPrice(y)
			if xok != yok {
				return xok
			}
			if xok && xc != yc {
				return xc < yc
			}
		case "price_cny_desc":
			xc, xok := listingCnyPrice(x)
			yc, yok := listingCnyPrice(y)
			if xok != yok {
				return xok
			}
			if xok && xc != yc {
				return xc > yc
			}
		}
		if x.ListedAt != y.ListedAt {
			return x.ListedAt > y.ListedAt
		}
		return x.IntentPinId > y.IntentPinId
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
		"list":   listings[offset:end],
		"total":  total,
		"cursor": nextCursor,
	})
}

func listingMatchesSeller(rec *SellIntentRecord, dto *ListingDTO, raw string, addrs map[string]struct{}) bool {
	for _, id := range []string{rec.SellerAddress, rec.SellerMetaId, dto.Seller.Address, dto.Seller.GlobalMetaId} {
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

func listingMePrice(dto *ListingDTO) (int64, bool) {
	if dto != nil && dto.Prices != nil && dto.Prices.Me != nil && dto.Prices.Me.AmountMilli > 0 {
		return dto.Prices.Me.AmountMilli, true
	}
	return 0, false
}

func listingCnyPrice(dto *ListingDTO) (int64, bool) {
	if dto != nil && dto.Prices != nil && dto.Prices.Fiat != nil && dto.Prices.Fiat.AmountCents > 0 && strings.EqualFold(dto.Prices.Fiat.Currency, "CNY") {
		return dto.Prices.Fiat.AmountCents, true
	}
	return 0, false
}

func listingMatchesPriceFilter(dto *ListingDTO, currency string, minPrice, maxPrice *int64) bool {
	inRange := func(val int64) bool {
		if minPrice != nil && val < *minPrice {
			return false
		}
		if maxPrice != nil && val > *maxPrice {
			return false
		}
		return true
	}
	switch strings.ToLower(currency) {
	case "me":
		p, ok := listingMePrice(dto)
		return ok && inRange(p)
	case "fiat:cny":
		p, ok := listingCnyPrice(dto)
		return ok && inRange(p)
	default: // "any" or "ft:<assetKey>"
		if strings.HasPrefix(strings.ToLower(currency), "ft:") {
			target := normalizeTokenAssetKey(strings.TrimPrefix(currency, "ft:"))
			return listingHasTokenAssetKey(dto, target)
		}
		if minPrice == nil && maxPrice == nil {
			return true
		}
		if p, ok := listingMePrice(dto); ok && inRange(p) {
			return true
		}
		if p, ok := listingCnyPrice(dto); ok && inRange(p) {
			return true
		}
		return false
	}
}

func listingMatchesKeyword(dto *ListingDTO, keyword string) bool {
	if strings.Contains(strings.ToLower(dto.ItemName), keyword) ||
		strings.Contains(strings.ToLower(dto.Desc), keyword) ||
		strings.Contains(strings.ToLower(dto.Note), keyword) ||
		strings.Contains(strings.ToLower(dto.CollectionName), keyword) ||
		strings.Contains(strings.ToLower(dto.Seller.Name), keyword) ||
		strings.Contains(strings.ToLower(dto.ItemPinId), keyword) {
		return true
	}
	if dto.ItemNumber > 0 {
		numStr := strconv.FormatInt(dto.ItemNumber, 10)
		if keyword == numStr || keyword == "#"+numStr || strings.Contains("#"+numStr, keyword) {
			return true
		}
	}
	return false
}

func mergeAcceptedCurrenciesWithTokens(raw []string, prices *SellPrices) []string {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, entry := range raw {
		trimmed := strings.TrimSpace(entry)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if _, dup := seen[lower]; dup {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, trimmed)
	}
	if prices != nil {
		for _, tok := range prices.Tokens {
			key := normalizeTokenAssetKey(tok.AssetKey)
			if key == "" {
				continue
			}
			cur := "ft:" + key
			if _, dup := seen[cur]; dup {
				continue
			}
			seen[cur] = struct{}{}
			out = append(out, cur)
		}
	}
	return out
}

func listingHasTokenAssetKey(dto *ListingDTO, assetKey string) bool {
	target := normalizeTokenAssetKey(assetKey)
	if dto == nil || dto.Prices == nil || target == "" {
		return false
	}
	for _, tok := range dto.Prices.Tokens {
		if normalizeTokenAssetKey(tok.AssetKey) == target {
			return true
		}
	}
	return false
}

// handleCollectionHolders serves GET /api/mrc721/collection/holders (REQ-B-06, Contract C3.4).
func (a *Aggregator) handleCollectionHolders(c *gin.Context) {
	if a == nil || a.store == nil {
		respondMrc721Err(c, 50000, "mrc721 index unavailable")
		return
	}
	collectionPinId := strings.TrimSpace(c.Query("pinId"))
	if collectionPinId == "" {
		collectionPinId = strings.TrimSpace(c.Query("collectionPinId"))
	}
	if collectionPinId == "" {
		respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter: pinId")
		return
	}
	size := 50
	if rawSize := strings.TrimSpace(c.Query("size")); rawSize != "" {
		parsed, err := parseListSize(rawSize)
		if err != nil {
			respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter: size")
			return
		}
		size = parsed
	}
	offset := parseOffsetCursor(c.Query("cursor"))

	a.mu.Lock()
	nftReader := a.nftReader
	zeroConf := a.zeroConfTrade
	a.mu.Unlock()
	if nftReader == nil {
		respondMrc721Err(c, 50000, "mrc721nft reader unavailable")
		return
	}

	holderCount, counts, err := nftReader.CollectionHolderStats(collectionPinId, zeroConf)
	if err != nil {
		respondMrc721Err(c, 50000, "failed to load collection holders")
		return
	}

	listedBySeller := make(map[string]int)
	if intents, err := a.listAllEffectiveSellIntents(true); err == nil {
		for _, rec := range intents {
			if rec == nil || !rec.Active {
				continue
			}
			cid := rec.CollectionPinId
			if cid == "" {
				if info := nftReader.ItemSummary(rec.ItemPinId, zeroConf); info != nil {
					cid = info.CollectionPinId
				}
			}
			if strings.EqualFold(cid, collectionPinId) {
				listedBySeller[rec.SellerAddress]++
			}
		}
	}

	holders := make([]HolderDTO, 0, len(counts))
	for addr, itemCount := range counts {
		seller := a.buildSellerDTO(addr, "")
		holders = append(holders, HolderDTO{
			Address:         addr,
			GlobalMetaId:    seller.GlobalMetaId,
			Name:            seller.Name,
			Avatar:          seller.Avatar,
			IsOnline:        seller.IsOnline,
			HasChatPubkey:   seller.HasChatPubkey,
			ItemCount:       itemCount,
			ListedItemCount: listedBySeller[addr],
		})
	}
	sort.Slice(holders, func(i, j int) bool {
		if holders[i].ItemCount != holders[j].ItemCount {
			return holders[i].ItemCount > holders[j].ItemCount
		}
		if holders[i].ListedItemCount != holders[j].ListedItemCount {
			return holders[i].ListedItemCount > holders[j].ListedItemCount
		}
		return holders[i].Address < holders[j].Address
	})

	total := len(holders)
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
	respondMrc721OK(c, gin.H{
		"holderCount": holderCount,
		"list":        holders[offset:end],
		"total":       total,
		"cursor":      nextCursor,
	})
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

func parseOptionalBoolQuery(raw string) (*bool, error) {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return nil, nil
	}
	switch trimmed {
	case "true", "1", "yes":
		v := true
		return &v, nil
	case "false", "0", "no":
		v := false
		return &v, nil
	default:
		return nil, ErrInvalidParameter
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
			return v
		}
	}
	return ""
}

func ptrValOrNeg(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func formatMarketVersionToken(s *CollectionMarketSummary) string {
	if s == nil {
		return ""
	}
	sellerOnline := false
	if s.FloorSeller != nil {
		sellerOnline = s.FloorSeller.IsOnline
	}
	return fmt.Sprintf("%s#%d#%d#%d#%d#%d#%t#%t#%s",
		s.CollectionPinId,
		s.ListedCount,
		ptrValOrNeg(s.FloorPriceMeMilli),
		ptrValOrNeg(s.FloorPriceCnyCents),
		s.HolderCount,
		s.BurnedCount,
		s.CaVerified,
		sellerOnline,
		s.DisplayTitle,
	)
}
