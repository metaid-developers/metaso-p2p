// HTTP surface of the passive-offer index (R-E-2): GET /api/mrc721/offers
// and GET /api/mrc721/offer/info — the sell-intent endpoints' envelope and
// row conventions exactly ({code,message,data}, {list,total,cursor},
// camelCase rows plus the lowercase duplicate spellings the IDBots client
// tolerates, public read without authentication).

package mrc721

import (
	"strings"

	"github.com/gin-gonic/gin"
)

// offerRow is the wire shape of one passive offer (camelCase keys plus the
// lowercase duplicate spellings the IDBots client tolerates — same
// convention as the sell-intent rows). Both spellings always carry the same
// value.
type offerRow struct {
	ItemPinId string `json:"itemPinId"`
	Itempinid string `json:"itempinid"`

	OfferPinId string `json:"offerPinId,omitempty"`
	Offerpinid string `json:"offerpinid,omitempty"`

	Active bool `json:"active"`

	PriceSats int64 `json:"priceSats,omitempty"`
	Pricesats int64 `json:"pricesats,omitempty"`

	// offerTx is served verbatim (byte-identical to the on-chain payload).
	OfferTx string `json:"offerTx,omitempty"`
	Offertx string `json:"offertx,omitempty"`

	SellerMetaId string `json:"sellerMetaId,omitempty"`
	Sellermetaid string `json:"sellermetaid,omitempty"`

	CollectionPinId string `json:"collectionPinId,omitempty"`
	Collectionpinid string `json:"collectionpinid,omitempty"`

	CollectionName string `json:"collectionName,omitempty"`
	Collectionname string `json:"collectionname,omitempty"`

	CreatedAt int64 `json:"createdAt,omitempty"`
	Createdat int64 `json:"createdat,omitempty"`

	UpdatedAt int64 `json:"updatedAt,omitempty"`
	Updatedat int64 `json:"updatedat,omitempty"`

	// Chain facts (present when the winning offer pin carried them).
	ChainName string `json:"chainName,omitempty"`
	Chainname string `json:"chainname,omitempty"`
	Height    int64  `json:"height,omitempty"`
	TxId      string `json:"txId,omitempty"`
	Txid      string `json:"txid,omitempty"`

	// Zero-conf fields (R-F-5): present only on overlay-synthesized rows.
	Confirmed   *bool `json:"confirmed,omitempty"`
	FirstSeenAt int64 `json:"firstSeenAt,omitempty"`
}

// offerRowFrom shapes one record; the collection join fields are filled
// only when the native item read model knows the item.
func offerRowFrom(rec *OfferRecord, collectionPinId, collectionName string) *offerRow {
	if rec == nil {
		return nil
	}
	row := &offerRow{
		ItemPinId:       rec.ItemPinId,
		Itempinid:       rec.ItemPinId,
		OfferPinId:      rec.OfferPinId,
		Offerpinid:      rec.OfferPinId,
		Active:          rec.Active,
		PriceSats:       rec.PriceSats,
		Pricesats:       rec.PriceSats,
		OfferTx:         rec.OfferTx,
		Offertx:         rec.OfferTx,
		SellerMetaId:    rec.SellerMetaId,
		Sellermetaid:    rec.SellerMetaId,
		CollectionPinId: collectionPinId,
		Collectionpinid: collectionPinId,
		CollectionName:  collectionName,
		Collectionname:  collectionName,
		CreatedAt:       rec.CreatedAt,
		Createdat:       rec.CreatedAt,
		UpdatedAt:       rec.UpdatedAt,
		Updatedat:       rec.UpdatedAt,
		ChainName:       rec.ChainName,
		Chainname:       rec.ChainName,
		Height:          rec.Height,
		TxId:            rec.TxID,
		Txid:            rec.TxID,
	}
	if rec.Overlay {
		confirmed := false
		row.Confirmed = &confirmed
		row.FirstSeenAt = rec.OverlayFirstSeenAt
	}
	return row
}

type offerListData struct {
	List   []*offerRow `json:"list"`
	Total  int         `json:"total"`
	Cursor string      `json:"cursor,omitempty"`
}

func (a *Aggregator) handleOffers(c *gin.Context) {
	filter := OfferFilter{
		ItemPinId:       strings.TrimSpace(c.Query("itemPinId")),
		SellerMetaId:    strings.TrimSpace(c.Query("sellerMetaId")),
		CollectionPinId: strings.TrimSpace(c.Query("collectionPinId")),
	}
	// The endpoint serves open offers (R-E-2): active defaults to true;
	// active=false lists the cancellation declarations (rows are kept
	// queryable, never deleted).
	active := true
	if activeRaw := strings.TrimSpace(c.Query("active")); activeRaw != "" {
		switch strings.ToLower(activeRaw) {
		case "true", "1":
			active = true
		case "false", "0":
			active = false
		default:
			respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter: active")
			return
		}
	}
	filter.Active = &active
	size, err := parseListSize(c.Query("size"))
	if err != nil {
		respondMrc721Error(c, err)
		return
	}

	page, err := a.ListOffers(filter, strings.TrimSpace(c.Query("cursor")), size)
	if err != nil {
		respondMrc721Error(c, err)
		return
	}
	rows := make([]*offerRow, 0, len(page.List))
	for _, rec := range page.List {
		collectionPinId, collectionName := a.offerCollectionRef(rec.ItemPinId)
		rows = append(rows, offerRowFrom(rec, collectionPinId, collectionName))
	}
	respondMrc721OK(c, offerListData{List: rows, Total: page.Total, Cursor: page.NextCursor})
}

func (a *Aggregator) handleOfferInfo(c *gin.Context) {
	itemPinId := strings.TrimSpace(c.Query("itemPinId"))
	if itemPinId == "" {
		respondMrc721Err(c, mrc721InvalidParamCode, "invalid parameter: itemPinId")
		return
	}
	rec, err := a.OfferInfo(itemPinId)
	if err != nil {
		respondMrc721Error(c, err)
		return
	}
	if rec == nil {
		respondMrc721Err(c, mrc721NotFoundCode, "offer not found")
		return
	}
	collectionPinId, collectionName := a.offerCollectionRef(rec.ItemPinId)
	respondMrc721OK(c, offerRowFrom(rec, collectionPinId, collectionName))
}

// respondMrc721 helpers live in api.go; the offer handlers reuse them (the
// envelope is the family contract).
