package mrc721nft

import (
	"github.com/gin-gonic/gin"
)

// Zero-conf DTO layer (R-F-5/R-F-6): the merged-view serializers plus the
// confirmedOnly/includeOverSupply query options. Wire rule: the new fields
// appear only when the relevant gate is on AND the request did not set
// confirmedOnly=1 — a confirmedOnly response is byte-identical to the
// pre-zero-conf shape, and overlay-free rows keep today's exact bytes too.

// queryViewOptions parses the R-F-6 query parameters.
func queryViewOptions(c *gin.Context) viewOptions {
	return viewOptions{
		confirmedOnly:     c.Query("confirmedOnly") == "1",
		includeOverSupply: c.Query("includeOverSupply") == "1",
	}
}

// zeroConfIssuanceActive reports whether the issuance overlay serves this
// request (gate on, not confirmedOnly).
func (a *Aggregator) zeroConfIssuanceActive(opts viewOptions) bool {
	if opts.confirmedOnly {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.zeroConfIssuance
}

// zeroConfTradeActive reports whether the trading overlay serves this
// request (gate on, not confirmedOnly).
func (a *Aggregator) zeroConfTradeActive(opts viewOptions) bool {
	if opts.confirmedOnly {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.zeroConfTrade
}

// collectionViewDTO serializes the merged album view. The base fields are
// byte-compatible with collectionDTO; the R-F-5 fields join when the gate
// is active for the request.
func (a *Aggregator) collectionViewDTO(view *albumView, opts viewOptions) gin.H {
	col := view.collection
	dto := gin.H{
		"collectionname":   col.CollectionName,
		"collectionName":   col.CollectionName,
		"name":             col.Name,
		"totalsupply":      col.TotalSupply,
		"totalSupply":      col.TotalSupply,
		"royaltyrate":      col.RoyaltyRate,
		"royaltyRate":      col.RoyaltyRate,
		"desc":             col.Desc,
		"website":          col.Website,
		"cover":            col.Cover,
		"metadata":         col.Metadata,
		"pinid":            col.PinId,
		"address":          col.CreateAddress,
		"metaid":           col.CreateMetaId,
		"createAddress":    col.CreateAddress,
		"createMetaId":     col.CreateMetaId,
		"createtime":       col.CreateTime,
		"createTime":       col.CreateTime,
		"totalnum":         view.confirmedMinted,
		"modifyHistory":    col.ModifyHistory,
		"modify_history":   col.ModifyHistory,
		"chainName":        col.GenesisChainName,
		"genesisChainName": col.GenesisChainName,
		"genesisHeight":    col.GenesisHeight,
		"genesisTx":        col.GenesisTx,
	}
	if !a.zeroConfIssuanceActive(opts) {
		return dto
	}
	dto["confirmed"] = view.confirmed
	dto["contentConfirmed"] = view.contentConfirmed
	dto["mintedCount"] = view.confirmedMinted + view.stagedMintCount + view.overlayMintCount
	dto["confirmedMintedCount"] = view.confirmedMinted
	if !view.confirmed {
		dto["firstSeenAt"] = view.firstSeenAt
	}
	return dto
}

// itemViewDTO serializes the merged item view. The base fields are
// byte-compatible with itemDTO; the R-F-5 fields join when a gate is
// active for the request (the ownership fields follow the trade gate,
// the row fields the issuance gate).
func (a *Aggregator) itemViewDTO(view *itemView, opts viewOptions) gin.H {
	item := view.item
	issuanceActive := a.zeroConfIssuanceActive(opts)
	tradeActive := a.zeroConfTradeActive(opts)
	dto := gin.H{
		"collectionPinId":  item.CollectionPinId,
		"collectionName":   item.CollectionName,
		"itemPinId":        item.ItemPinId,
		"itemPinNumber":    item.Number,
		"number":           item.Number,
		"name":             item.Name,
		"desc":             item.Desc,
		"cover":            item.Cover,
		"metaData":         item.Metadata,
		"metadata":         item.Metadata,
		"createTime":       item.CreateTime,
		"address":          item.OwnerAddress,
		"metaId":           item.OwnerMetaId,
		"ownerAddress":     item.OwnerAddress,
		"ownerMetaId":      item.OwnerMetaId,
		"creatorMinted":    item.CreatorMinted,
		"contentType":      item.ContentType,
		"contentString":    item.ContentSummary,
		"contentSummary":   item.ContentSummary,
		"txOutput":         item.Output,
		"txValue":          item.OutputValue,
		"output":           item.Output,
		"outputValue":      item.OutputValue,
		"burned":           item.Burned,
		"transferCount":    item.TransferCount,
		"chainName":        item.GenesisChainName,
		"genesisChainName": item.GenesisChainName,
		"genesisHeight":    item.GenesisHeight,
		"genesisTx":        item.GenesisTx,
	}
	if !issuanceActive && !tradeActive {
		return dto
	}
	dto["confirmed"] = view.confirmed
	dto["numberFinal"] = view.numberFinal
	dto["descConfirmed"] = view.descConfirmed
	dto["ownershipConfirmed"] = view.ownershipConfirmed
	dto["confirmedOwnerAddress"] = view.confirmedOwnerAddress
	if !view.confirmed {
		dto["firstSeenAt"] = view.firstSeenAt
	}
	if view.overSupply && opts.includeOverSupply {
		dto["overSupply"] = true
	}
	return dto
}

// overlayTransferDTO is one overlay provenance row (R-F-3.4).
func overlayTransferDTO(sp *overlaySpendEntry) gin.H {
	return gin.H{
		"itemPinId":   sp.ItemPinId,
		"fromAddress": sp.FromAddress,
		"toAddress":   sp.ToAddress,
		"chainName":   sp.ChainName,
		"height":      int64(-1),
		"txId":        sp.TxId,
		"time":        sp.FirstSeenAt,
		"confirmed":   false,
		"firstSeenAt": sp.FirstSeenAt,
	}
}
