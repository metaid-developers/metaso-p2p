package mrc721

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
)

// The mrc721 endpoints speak the IDBots MRC-721 client contract exactly:
// success is {"code":1,"message":"ok","data":...}; failures carry any other
// code with an explanatory message (the client treats code!==1 as a
// structured miss/error and never requires HTTP status semantics).
const (
	mrc721SuccessCode      = 1
	mrc721InvalidParamCode = 40000
	mrc721NotFoundCode     = 40400
)

func respondMrc721OK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{
		"code":    mrc721SuccessCode,
		"message": "ok",
		"data":    data,
	})
}

func respondMrc721Err(c *gin.Context, code int, message string) {
	c.JSON(http.StatusOK, gin.H{
		"code":    code,
		"message": message,
	})
}

func respondMrc721Error(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidParameter):
		respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter")
	case errors.Is(err, ErrNotFound):
		respondMrc721Err(c, mrc721NotFoundCode, "not found")
	default:
		respondMrc721Err(c, 50000, "mrc721 index unavailable")
	}
}

// sellIntentRow is the wire shape of one sell intent: camelCase keys plus
// the lowercase duplicate spellings the IDBots client tolerates
// (itemPinId/itempinid, ...). Both spellings always carry the same value.
type sellIntentRow struct {
	ItemPinId string `json:"itemPinId"`
	Itempinid string `json:"itempinid"`

	SellPinId string `json:"sellPinId,omitempty"`
	Sellpinid string `json:"sellpinid,omitempty"`

	Active bool `json:"active"`

	CollectionName string `json:"collectionName,omitempty"`
	Collectionname string `json:"collectionname,omitempty"`

	CollectionPinId string `json:"collectionPinId,omitempty"`
	Collectionpinid string `json:"collectionpinid,omitempty"`

	ItemOutpoint *ItemOutpoint `json:"itemOutpoint,omitempty"`

	SellerAddress string `json:"sellerAddress,omitempty"`
	Selleraddress string `json:"selleraddress,omitempty"`

	SellerMetaId string `json:"sellerMetaId,omitempty"`
	Sellermetaid string `json:"sellermetaid,omitempty"`

	PriceHintSats int64 `json:"priceHintSats,omitempty"`
	Pricehintsats int64 `json:"pricehintsats,omitempty"`

	AcceptedCurrencies []string `json:"acceptedCurrencies,omitempty"`
	Acceptedcurrencies []string `json:"acceptedcurrencies,omitempty"`

	Settlement []string `json:"settlement,omitempty"`

	Note string `json:"note,omitempty"`

	CreatedAt int64 `json:"createdAt,omitempty"`
	Createdat int64 `json:"createdat,omitempty"`

	ExpiresAt int64 `json:"expiresAt,omitempty"`
	Expiresat int64 `json:"expiresat,omitempty"`

	UpdatedAt int64 `json:"updatedAt,omitempty"`
	Updatedat int64 `json:"updatedat,omitempty"`

	// Chain facts (present when the winning sell pin carried them).
	ChainName string `json:"chainName,omitempty"`
	Chainname string `json:"chainname,omitempty"`
	Height    int64  `json:"height,omitempty"`
	TxId      string `json:"txId,omitempty"`
	Txid      string `json:"txid,omitempty"`
}

func sellIntentRowFrom(rec *SellIntentRecord) *sellIntentRow {
	if rec == nil {
		return nil
	}
	return &sellIntentRow{
		ItemPinId:          rec.ItemPinId,
		Itempinid:          rec.ItemPinId,
		SellPinId:          rec.SellPinId,
		Sellpinid:          rec.SellPinId,
		Active:             rec.Active,
		CollectionName:     rec.CollectionName,
		Collectionname:     rec.CollectionName,
		CollectionPinId:    rec.CollectionPinId,
		Collectionpinid:    rec.CollectionPinId,
		ItemOutpoint:       rec.ItemOutpoint,
		SellerAddress:      rec.SellerAddress,
		Selleraddress:      rec.SellerAddress,
		SellerMetaId:       rec.SellerMetaId,
		Sellermetaid:       rec.SellerMetaId,
		PriceHintSats:      rec.PriceHintSats,
		Pricehintsats:      rec.PriceHintSats,
		AcceptedCurrencies: rec.AcceptedCurrencies,
		Acceptedcurrencies: rec.AcceptedCurrencies,
		Settlement:         rec.Settlement,
		Note:               rec.Note,
		CreatedAt:          rec.CreatedAt,
		Createdat:          rec.CreatedAt,
		ExpiresAt:          rec.ExpiresAt,
		Expiresat:          rec.ExpiresAt,
		UpdatedAt:          rec.UpdatedAt,
		Updatedat:          rec.UpdatedAt,
		ChainName:          rec.ChainName,
		Chainname:          rec.ChainName,
		Height:             rec.Height,
		TxId:               rec.TxID,
		Txid:               rec.TxID,
	}
}

type sellIntentListData struct {
	List   []*sellIntentRow `json:"list"`
	Total  int              `json:"total"`
	Cursor string           `json:"cursor,omitempty"`
}

func (a *Aggregator) handleSellIntents(c *gin.Context) {
	filter := SellIntentFilter{
		CollectionPinId: strings.TrimSpace(c.Query("collectionPinId")),
		CollectionName:  strings.TrimSpace(c.Query("collectionName")),
		ItemPinId:       strings.TrimSpace(c.Query("itemPinId")),
		SellerAddress:   strings.TrimSpace(c.Query("sellerAddress")),
	}
	if activeRaw := strings.TrimSpace(c.Query("active")); activeRaw != "" {
		switch strings.ToLower(activeRaw) {
		case "true", "1":
			active := true
			filter.Active = &active
		case "false", "0":
			active := false
			filter.Active = &active
		default:
			respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter: active")
			return
		}
	}
	size, err := parseListSize(c.Query("size"))
	if err != nil {
		respondMrc721Error(c, err)
		return
	}

	page, err := a.ListSellIntents(filter, strings.TrimSpace(c.Query("cursor")), size)
	if err != nil {
		respondMrc721Error(c, err)
		return
	}
	rows := make([]*sellIntentRow, 0, len(page.List))
	for _, rec := range page.List {
		rows = append(rows, sellIntentRowFrom(rec))
	}
	respondMrc721OK(c, sellIntentListData{List: rows, Total: page.Total, Cursor: page.NextCursor})
}

func (a *Aggregator) handleSellIntentInfo(c *gin.Context) {
	itemPinId := strings.TrimSpace(c.Query("itemPinId"))
	if itemPinId == "" {
		respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter: itemPinId")
		return
	}
	rec, err := a.SellIntentInfo(itemPinId)
	if err != nil {
		respondMrc721Error(c, err)
		return
	}
	if rec == nil {
		respondMrc721Err(c, mrc721NotFoundCode, "sell intent not found")
		return
	}
	respondMrc721OK(c, sellIntentRowFrom(rec))
}

// parseListSize mirrors the house clamps: default 20, max 100, positive.
func parseListSize(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultListSize, nil
	}
	size, err := strconv.Atoi(raw)
	if err != nil || size <= 0 || size > maxListSize {
		return 0, ErrInvalidParameter
	}
	return size, nil
}

// collectionRow is the wire shape of one collection row (the MRC-721
// collection summary the IDBots client reads), served by
// /api/mrc721/collection/search and /api/mrc721/address/collections-issued.
type collectionRow struct {
	CollectionName   string `json:"collectionName"`
	Name             string `json:"name,omitempty"`
	Desc             string `json:"desc,omitempty"`
	TotalSupply      int64  `json:"totalSupply,omitempty"`
	Totalnum         int64  `json:"totalnum,omitempty"`
	RoyaltyRate      int64  `json:"royaltyRate,omitempty"`
	Pinid            string `json:"pinid,omitempty"`
	CreateAddress    string `json:"createAddress,omitempty"`
	CreateMetaId     string `json:"createMetaId,omitempty"`
	CreateTime       int64  `json:"createTime,omitempty"`
	Cover            string `json:"cover,omitempty"`
	GenesisChainName string `json:"genesisChainName,omitempty"`
	MintedCount      int64  `json:"mintedCount,omitempty"`
}

func collectionRowFrom(rec *CollectionRecord) *collectionRow {
	if rec == nil {
		return nil
	}
	return &collectionRow{
		CollectionName:   rec.CollectionName,
		Name:             rec.Name,
		Desc:             rec.Desc,
		TotalSupply:      rec.TotalSupply,
		Totalnum:         rec.Totalnum,
		RoyaltyRate:      rec.RoyaltyRate,
		Pinid:            rec.PinId,
		CreateAddress:    rec.CreateAddress,
		CreateMetaId:     rec.CreateMetaId,
		CreateTime:       rec.CreateTime,
		Cover:            rec.Cover,
		GenesisChainName: rec.GenesisChainName,
		MintedCount:      rec.MintedCount,
	}
}

type collectionListData struct {
	List   []*collectionRow `json:"list"`
	Total  int              `json:"total"`
	Cursor string           `json:"cursor,omitempty"`
}

func (a *Aggregator) handleCollectionSearch(c *gin.Context) {
	size, err := parseListSize(c.Query("size"))
	if err != nil {
		respondMrc721Error(c, err)
		return
	}
	keyword := c.Query("keyword")
	issuer := strings.TrimSpace(c.Query("issuer"))
	cursor := strings.TrimSpace(c.Query("cursor"))
	if src := a.nativeCollectionSource(); src != nil {
		// Native read model (mrc721nft aggregate): same wire contract, the
		// registry serves from its own store instead of the upstream sweep.
		rows, total, next, err := src.SearchNativeCollections(keyword, issuer, cursor, size)
		if err != nil {
			respondMrc721Error(c, err)
			return
		}
		respondMrc721OK(c, nativeCollectionPageData(rows, total, next))
		return
	}
	page, err := a.SearchCollections(keyword, issuer, cursor, size)
	if err != nil {
		respondMrc721Error(c, err)
		return
	}
	respondMrc721OK(c, collectionPageData(page))
}

func (a *Aggregator) handleAddressCollectionsIssued(c *gin.Context) {
	size, err := parseListSize(c.Query("size"))
	if err != nil {
		respondMrc721Error(c, err)
		return
	}
	address := strings.TrimSpace(c.Query("address"))
	cursor := strings.TrimSpace(c.Query("cursor"))
	if src := a.nativeCollectionSource(); src != nil {
		rows, total, next, err := src.NativeCollectionsIssued(address, cursor, size)
		if err != nil {
			if errors.Is(err, mrc721nft.ErrInvalidSearch) {
				respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter: address")
				return
			}
			respondMrc721Error(c, err)
			return
		}
		respondMrc721OK(c, nativeCollectionPageData(rows, total, next))
		return
	}
	page, err := a.CollectionsIssuedByAddress(address, cursor, size)
	if err != nil {
		if errors.Is(err, ErrInvalidParameter) {
			respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter: address")
			return
		}
		respondMrc721Error(c, err)
		return
	}
	respondMrc721OK(c, collectionPageData(page))
}

// nativeCollectionPageData wraps native search rows in the same wire shape
// as collectionPageData ({list,total,cursor} with cursor omitted at end).
func nativeCollectionPageData(rows []*mrc721nft.SearchRow, total int, next string) nativeCollectionListData {
	if rows == nil {
		rows = []*mrc721nft.SearchRow{}
	}
	return nativeCollectionListData{List: rows, Total: total, Cursor: next}
}

func collectionPageData(page *CollectionPage) collectionListData {
	data := collectionListData{List: []*collectionRow{}, Total: 0}
	if page == nil {
		return data
	}
	data.Total = page.Total
	data.Cursor = page.NextCursor
	for _, rec := range page.List {
		data.List = append(data.List, collectionRowFrom(rec))
	}
	return data
}
