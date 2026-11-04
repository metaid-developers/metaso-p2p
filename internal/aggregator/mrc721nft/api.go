package mrc721nft

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// manapi-compatible MRC-721 query API (ported from man-p2p's
// api/mrc721_jsonapi.go). Paths, parameters, pagination and the response
// envelope are byte-compatible with man-p2p so downstream consumers switch
// base URLs without any other change:
//
//	GET /api/mrc721/collection/pageList
//	GET /api/mrc721/collection/info
//	GET /api/mrc721/collection/items/pageList
//	GET /api/mrc721/address/collection
//	GET /api/mrc721/address/item
//	GET /api/mrc721/item/info
//	GET /api/mrc721/item/transfers
//	GET /api/mrc721/item/verify
//
// Envelope (man-p2p api/respond.ApiResponse): success {"code":1,"message":
// "ok","data":...}; parameter/service errors carry code 404; no-data
// carries code 100 with data []. Pagination reads the legacy misspelled
// `cousor` first (same precedence as man-p2p) then `cursor`, as an int64
// offset; size defaults to 20 and clamps at 200.

// apiEnvelope mirrors man-p2p's respond.ApiResponse JSON shape; a nil Data
// marshals as "data":null exactly like the upstream error responses.
type apiEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func (a *Aggregator) respondOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, apiEnvelope{Code: 1, Message: "ok", Data: data})
}

func (a *Aggregator) respondParamError(c *gin.Context) {
	c.JSON(http.StatusOK, apiEnvelope{Code: 404, Message: "Parameter error."})
}

func (a *Aggregator) respondServiceError(c *gin.Context) {
	c.JSON(http.StatusOK, apiEnvelope{Code: 404, Message: "service exception."})
}

func (a *Aggregator) respondNoData(c *gin.Context) {
	c.JSON(http.StatusOK, apiEnvelope{Code: 100, Message: "no data found.", Data: []string{}})
}

func (a *Aggregator) queryCursor(c *gin.Context) int64 {
	v := c.Query("cousor")
	if v == "" {
		v = c.Query("cursor")
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

func (a *Aggregator) querySize(c *gin.Context) int64 {
	n, err := strconv.ParseInt(c.Query("size"), 10, 64)
	if err != nil || n <= 0 {
		return 20
	}
	if n > 200 {
		return 200
	}
	return n
}

// collectionDTO field names mirror man-p2p's mrc721CollectionDTO: legacy
// lowercase spellings plus camelCase aliases (IDBots contract), with
// address/metaid being the issuer create address/metaId.
func (a *Aggregator) collectionDTO(col *Collection) gin.H {
	return gin.H{
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
		"totalnum":         col.TotalNum,
		"modifyHistory":    col.ModifyHistory,
		"modify_history":   col.ModifyHistory,
		"chainName":        col.GenesisChainName,
		"genesisChainName": col.GenesisChainName,
		"genesisHeight":    col.GenesisHeight,
		"genesisTx":        col.GenesisTx,
	}
}

// itemDTO field names mirror man-p2p's mrc721ItemDTO: address/metaId are the
// current holder; txOutput/txValue alias output/outputValue (the current
// spendable outpoint the downstream transfer tooling gates on).
func (a *Aggregator) itemDTO(item *Item) gin.H {
	return gin.H{
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
}

// pendingTransferDTO mirrors man-p2p's mrc721PendingTransferDTO: txStatus is
// the literal "unconfirmed"; the record disappears once the spend confirms
// (or is evicted by the mempool reconcile).
func (a *Aggregator) pendingTransferDTO(pt *PendingTransfer) gin.H {
	return gin.H{
		"itemPinId":     pt.ItemPinId,
		"chainName":     pt.ChainName,
		"spentOutpoint": pt.SpentOutpoint,
		"fromAddress":   pt.FromAddress,
		"toAddress":     pt.ToAddress,
		"to":            pt.ToAddress,
		"txId":          pt.TxId,
		"txid":          pt.TxId,
		"firstSeenAt":   pt.FirstSeenAt,
		"txStatus":      "unconfirmed",
	}
}

func (a *Aggregator) handleCollectionPageList(c *gin.Context) {
	if a.db == nil {
		a.respondServiceError(c)
		return
	}
	list, total, err := a.db.ListCollections(a.queryCursor(c), a.querySize(c))
	if err != nil {
		a.respondServiceError(c)
		return
	}
	dtos := make([]gin.H, 0, len(list))
	for _, col := range list {
		dtos = append(dtos, a.collectionDTO(col))
	}
	a.respondOK(c, gin.H{"list": dtos, "total": total})
}

func (a *Aggregator) handleCollectionInfo(c *gin.Context) {
	if a.db == nil {
		a.respondServiceError(c)
		return
	}
	name := c.Query("name")
	pinId := c.Query("pinId")
	if name == "" && pinId == "" {
		a.respondParamError(c)
		return
	}
	var col *Collection
	var err error
	if name != "" {
		col, err = a.db.GetCollectionByName(name)
	} else {
		col, err = a.db.GetCollectionByPinId(pinId)
	}
	if err != nil {
		a.respondServiceError(c)
		return
	}
	if col == nil {
		a.respondNoData(c)
		return
	}
	a.respondOK(c, a.collectionDTO(col))
}

func (a *Aggregator) handleCollectionItemsPageList(c *gin.Context) {
	if a.db == nil {
		a.respondServiceError(c)
		return
	}
	// Back-compat: pinid means the collectionid (legacy MetaSo semantics);
	// name means the collection name.
	pinId := c.Query("pinid")
	collectionName := c.Query("name")
	if pinId == "" && collectionName == "" {
		a.respondParamError(c)
		return
	}
	if collectionName == "" {
		col, err := a.db.GetCollectionByPinId(pinId)
		if err != nil {
			a.respondServiceError(c)
			return
		}
		if col == nil {
			a.respondNoData(c)
			return
		}
		collectionName = col.CollectionName
	}
	cursor, size := a.queryCursor(c), a.querySize(c)
	var pinIds []string
	var pos int64
	err := a.db.IterateCollectionItems(collectionName, func(seq int64, itemPinId string) bool {
		if pos >= cursor+size {
			return false
		}
		if pos >= cursor {
			pinIds = append(pinIds, itemPinId)
		}
		pos++
		return true
	})
	if err != nil {
		a.respondServiceError(c)
		return
	}
	col, _ := a.db.GetCollectionByName(collectionName)
	var total int64
	if col != nil {
		total = col.TotalNum
	}
	dtos := make([]gin.H, 0, len(pinIds))
	for _, itemPinId := range pinIds {
		item, err := a.db.GetItem(itemPinId)
		if err != nil || item == nil {
			continue
		}
		dtos = append(dtos, a.itemDTO(item))
	}
	a.respondOK(c, gin.H{"list": dtos, "total": total})
}

func (a *Aggregator) handleAddressCollection(c *gin.Context) {
	if a.db == nil {
		a.respondServiceError(c)
		return
	}
	address := c.Query("address")
	if address == "" {
		a.respondParamError(c)
		return
	}
	// Dedup the held items into collections in first-seen order, counting
	// the per-collection hold count (ownnum).
	holdCounts := make(map[string]int64)
	var order []string
	err := a.db.IterateOwnerItems(address, "", func(collectionName, itemPinId string) bool {
		if _, seen := holdCounts[collectionName]; !seen {
			order = append(order, collectionName)
		}
		holdCounts[collectionName]++
		return true
	})
	if err != nil {
		a.respondServiceError(c)
		return
	}
	cursor, size := a.queryCursor(c), a.querySize(c)
	total := int64(len(order))
	dtos := make([]gin.H, 0)
	if cursor < total {
		end := cursor + size
		if end > total {
			end = total
		}
		for _, name := range order[cursor:end] {
			col, err := a.db.GetCollectionByName(name)
			if err != nil || col == nil {
				continue
			}
			dto := a.collectionDTO(col)
			dto["ownnum"] = holdCounts[name]
			dtos = append(dtos, dto)
		}
	}
	a.respondOK(c, gin.H{"list": dtos, "total": total})
}

func (a *Aggregator) handleAddressItem(c *gin.Context) {
	if a.db == nil {
		a.respondServiceError(c)
		return
	}
	address := c.Query("address")
	if address == "" {
		a.respondParamError(c)
		return
	}
	// Optional collection filter: pinId = collectionid, name = collection name.
	collectionFilter := c.Query("pinId")
	if collectionFilter != "" {
		col, err := a.db.GetCollectionByPinId(collectionFilter)
		if err != nil {
			a.respondServiceError(c)
			return
		}
		if col == nil {
			a.respondNoData(c)
			return
		}
		collectionFilter = col.CollectionName
	} else {
		collectionFilter = c.Query("name")
	}
	cursor, size := a.queryCursor(c), a.querySize(c)
	dtos := make([]gin.H, 0)
	var total int64
	err := a.db.IterateOwnerItems(address, collectionFilter, func(collectionName, itemPinId string) bool {
		if total < cursor || total >= cursor+size {
			total++
			return true
		}
		item, err := a.db.GetItem(itemPinId)
		if err != nil || item == nil {
			total++
			return true
		}
		dtos = append(dtos, a.itemDTO(item))
		total++
		return true
	})
	if err != nil {
		a.respondServiceError(c)
		return
	}
	a.respondOK(c, gin.H{"list": dtos, "total": total})
}

func (a *Aggregator) handleItemInfo(c *gin.Context) {
	if a.db == nil {
		a.respondServiceError(c)
		return
	}
	pinId := c.Query("pinId")
	if pinId == "" {
		a.respondParamError(c)
		return
	}
	item, err := a.db.GetItem(pinId)
	if err != nil {
		a.respondServiceError(c)
		return
	}
	if item == nil {
		a.respondNoData(c)
		return
	}
	data := a.itemDTO(item)
	// Unconfirmed-transfer channel: the current outpoint already spent in
	// the mempool. Absent (not null) when there is no pending spend — read
	// side distinguishes "sent, unconfirmed" from "not sent / confirmed".
	if pt, _ := a.db.GetPendingTransfer(pinId); pt != nil {
		data["pending"] = a.pendingTransferDTO(pt)
	}
	a.respondOK(c, data)
}

func (a *Aggregator) handleItemTransfers(c *gin.Context) {
	if a.db == nil {
		a.respondServiceError(c)
		return
	}
	pinId := c.Query("pinId")
	if pinId == "" {
		a.respondParamError(c)
		return
	}
	cursor, size := a.queryCursor(c), a.querySize(c)
	list := make([]gin.H, 0)
	var total int64
	err := a.db.IterateItemTransfers(pinId, func(tr *ItemTransfer) bool {
		if total >= cursor && total < cursor+size {
			list = append(list, gin.H{
				"itemPinId":   tr.ItemPinId,
				"fromAddress": tr.FromAddress,
				"toAddress":   tr.ToAddress,
				"chainName":   tr.ChainName,
				"height":      tr.Height,
				"txId":        tr.TxId,
				"time":        tr.Time,
			})
		}
		total++
		return true
	})
	if err != nil {
		a.respondServiceError(c)
		return
	}
	// list holds confirmed history only; unconfirmed spends are a separate
	// pending array (not merged into list/total) so they never cause a
	// false "no transfers" answer.
	pendingList := make([]gin.H, 0)
	if pt, _ := a.db.GetPendingTransfer(pinId); pt != nil {
		pendingList = append(pendingList, a.pendingTransferDTO(pt))
	}
	a.respondOK(c, gin.H{"list": list, "total": total, "pending": pendingList})
}

func (a *Aggregator) handleAdminReplayPins(c *gin.Context) {
	if a.db == nil || a.replayClient == nil {
		a.respondServiceError(c)
		return
	}
	var ids []string
	for _, id := range strings.Split(c.Query("pinIds"), ",") {
		if strings.TrimSpace(id) != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		a.respondParamError(c)
		return
	}
	if len(ids) > 100 {
		ids = ids[:100]
	}
	a.respondOK(c, gin.H{"results": a.ReplayPins(c.Request.Context(), a.replayClient, ids)})
}

func (a *Aggregator) handleItemVerify(c *gin.Context) {
	if a.db == nil {
		a.respondServiceError(c)
		return
	}
	pinId := c.Query("pinId")
	if pinId == "" {
		a.respondParamError(c)
		return
	}
	item, err := a.db.GetItem(pinId)
	if err != nil {
		a.respondServiceError(c)
		return
	}
	if item == nil {
		// Not indexed: return the fact set, presentation decides
		// (collected is the IDBots contract alias).
		a.respondOK(c, gin.H{"indexed": false, "collected": false, "pinId": pinId, "itemPinId": pinId})
		return
	}
	genesisChainName, genesisHeight, genesisTx := "", int64(0), ""
	if col, _ := a.db.GetCollectionByName(item.CollectionName); col != nil {
		genesisChainName, genesisHeight, genesisTx = col.GenesisChainName, col.GenesisHeight, col.GenesisTx
	}
	a.respondOK(c, gin.H{
		"indexed":          true,
		"collected":        true,
		"pinId":            pinId,
		"itemPinId":        pinId,
		"collectionName":   item.CollectionName,
		"collectionid":     item.CollectionPinId,
		"collectionPinId":  item.CollectionPinId,
		"creatorMinted":    item.CreatorMinted,
		"burned":           item.Burned,
		"transferCount":    item.TransferCount,
		"ownerAddress":     item.OwnerAddress,
		"ownerMetaId":      item.OwnerMetaId,
		"genesisChainName": item.GenesisChainName,
		"genesisHeight":    item.GenesisHeight,
		"genesisTx":        item.GenesisTx,
		"itemGenesis": gin.H{
			"chainName": item.GenesisChainName,
			"height":    item.GenesisHeight,
			"tx":        item.GenesisTx,
		},
		"collectionGenesis": gin.H{
			"chainName": genesisChainName,
			"height":    genesisHeight,
			"tx":        genesisTx,
		},
	})
}
