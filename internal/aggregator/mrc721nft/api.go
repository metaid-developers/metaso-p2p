package mrc721nft

import (
	"net/http"
	"sort"
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
// Under the issuer-only protocol revision
// (docs/mrc721-protocol-revision-issuer-only.md) collection names are NOT
// unique — the album index key is the collectionid (collection_desc pinid).
// Every internal resolution therefore goes through the collectionid; a
// name-based query parameter resolves to the earliest-created album under
// that name (deterministic fallback; downstream should prefer pinId).
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

// resolveCollection resolves a collection for query parameters carrying
// both spellings. man-p2p precedence is kept (name wins when both are
// present); under non-unique names a name resolves to the earliest-created
// album under it — the deterministic disambiguation fallback, with the
// collectionid (pinId) as the exact-key alternative. Reports the wire-level
// no-data case as (nil, nil).
func (a *Aggregator) resolveCollection(pinId, collectionName string) (*Collection, error) {
	if collectionName != "" {
		if col, err := a.db.GetEarliestCollectionByName(collectionName); err != nil || col != nil {
			return col, err
		}
	}
	if pinId != "" {
		return a.db.GetCollectionByPinId(pinId)
	}
	return nil, nil
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
	opts := queryViewOptions(c)
	list, total, err := a.db.ListCollections(a.queryCursor(c), a.querySize(c))
	if err != nil {
		a.respondServiceError(c)
		return
	}
	dtos := make([]gin.H, 0, len(list))
	for _, col := range list {
		if a.zeroConfIssuanceActive(opts) {
			if view := a.albumViewByPair(col.CollectionName, col.CreateAddress, opts); view != nil {
				dtos = append(dtos, a.collectionViewDTO(view, opts))
				continue
			}
		}
		dtos = append(dtos, a.collectionDTO(col))
	}
	// Overlay-created albums join the page (R-F-2 acceptance: search and
	// page surfaces include the album within one poll), ordered after the
	// confirmed registry in provisional order.
	if a.zeroConfIssuanceActive(opts) {
		dtos = append(dtos, a.overlayCollectionDTOs(opts)...)
	}
	a.respondOK(c, gin.H{"list": dtos, "total": total + int64(len(dtos)-len(list))})
}

// overlayCollectionDTOs serializes the overlay-created albums (gate on).
func (a *Aggregator) overlayCollectionDTOs(opts viewOptions) []gin.H {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := a.overlaySnapshotLocked()
	var dtos []gin.H
	for _, entry := range overlayAlbumsLocked(snap) {
		view := a.albumViewByPairLocked(snap, entry.ColCreate.CollectionName, entry.ColCreate.CreateAddress, opts)
		if view != nil {
			dtos = append(dtos, a.collectionViewDTO(view, opts))
		}
	}
	return dtos
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
	opts := queryViewOptions(c)
	if a.zeroConfIssuanceActive(opts) {
		var view *albumView
		if name != "" {
			view = a.albumViewByName(name, opts)
		}
		if view == nil && pinId != "" {
			view = a.albumViewByPinId(pinId, opts)
		}
		if view != nil {
			a.respondOK(c, a.collectionViewDTO(view, opts))
			return
		}
	}
	col, err := a.resolveCollection(pinId, name)
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
	// Back-compat: pinid means the collectionid (the album index key);
	// name resolves to the earliest album under that name.
	pinId := c.Query("pinid")
	collectionName := c.Query("name")
	if pinId == "" && collectionName == "" {
		a.respondParamError(c)
		return
	}
	opts := queryViewOptions(c)
	var view *albumView
	if a.zeroConfIssuanceActive(opts) {
		if collectionName != "" {
			view = a.albumViewByName(collectionName, opts)
		}
		if view == nil && pinId != "" {
			view = a.albumViewByPinId(pinId, opts)
		}
	}
	if view != nil {
		// Zero-conf page: confirmed + staged + overlay items in serve order
		// with provisional numbers; total follows the overlay-inclusive
		// minted count (the mint precheck surface).
		ivs := a.itemViewsForAlbum(view, opts)
		cursor, size := a.queryCursor(c), a.querySize(c)
		total := int64(len(ivs))
		dtos := make([]gin.H, 0, len(ivs))
		start := cursor
		if start > total {
			start = total
		}
		end := cursor + size
		if end > total || end <= 0 {
			end = total
		}
		for _, iv := range ivs[start:end] {
			dtos = append(dtos, a.itemViewDTO(iv, opts))
		}
		a.respondOK(c, gin.H{"list": dtos, "total": total})
		return
	}
	col, err := a.resolveCollection(pinId, collectionName)
	if err != nil {
		a.respondServiceError(c)
		return
	}
	if col == nil {
		if pinId != "" {
			// Exact collectionid requested and not found: no-data error.
			a.respondNoData(c)
			return
		}
		// Unknown name: success with an empty list (same as man-p2p).
		a.respondOK(c, gin.H{"list": []gin.H{}, "total": int64(0)})
		return
	}
	cursor, size := a.queryCursor(c), a.querySize(c)
	var pinIds []string
	var pos int64
	err = a.db.IterateCollectionItems(col.PinId, func(seq int64, itemPinId string) bool {
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
	dtos := make([]gin.H, 0, len(pinIds))
	for _, itemPinId := range pinIds {
		item, err := a.db.GetItem(itemPinId)
		if err != nil || item == nil {
			continue
		}
		dtos = append(dtos, a.itemDTO(item))
	}
	a.respondOK(c, gin.H{"list": dtos, "total": col.TotalNum})
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
	// Dedup the held items into albums (keyed by collectionid — names are
	// not unique) in first-seen order, counting the per-album hold count
	// (ownnum).
	holdCounts := make(map[string]int64)
	var order []string
	err := a.db.IterateOwnerItems(address, "", func(collectionPinId, itemPinId string) bool {
		if _, seen := holdCounts[collectionPinId]; !seen {
			order = append(order, collectionPinId)
		}
		holdCounts[collectionPinId]++
		return true
	})
	if err != nil {
		a.respondServiceError(c)
		return
	}
	opts := queryViewOptions(c)
	tradeActive := a.zeroConfTradeActive(opts)
	if tradeActive {
		// Zero-conf holder view: fold the overlay ownership — hide albums
		// whose items all moved away, add albums holding overlay items
		// (overlay mints / staged mints / delivered spends).
		a.foldHolderCollections(address, &order, holdCounts, opts)
	}
	cursor, size := a.queryCursor(c), a.querySize(c)
	total := int64(len(order))
	dtos := make([]gin.H, 0)
	if cursor < total {
		end := cursor + size
		if end > total {
			end = total
		}
		for _, collectionPinId := range order[cursor:end] {
			if view := a.albumViewByPinId(collectionPinId, opts); view != nil && (tradeActive || a.zeroConfIssuanceActive(opts)) {
				dto := a.collectionViewDTO(view, opts)
				dto["ownnum"] = holdCounts[collectionPinId]
				dtos = append(dtos, dto)
				continue
			}
			col, err := a.db.GetCollectionByPinId(collectionPinId)
			if err != nil || col == nil {
				continue
			}
			dto := a.collectionDTO(col)
			dto["ownnum"] = holdCounts[collectionPinId]
			dtos = append(dtos, dto)
		}
	}
	a.respondOK(c, gin.H{"list": dtos, "total": total})
}

// foldHolderCollections rewrites the holder's album list under the trade
// overlay: confirmed albums whose items all moved away drop out, albums
// holding overlay items join in.
func (a *Aggregator) foldHolderCollections(address string, order *[]string, holdCounts map[string]int64, opts viewOptions) {
	a.mu.Lock()
	snap := a.overlaySnapshotLocked()
	// Overlay-owned items by album: overlay mints of the address resolve
	// through their album's collectionid (spend deliveries and staged mints
	// join below).
	overlayByCol := make(map[string]int64) // collection pinid → count
	for _, entry := range snap.pins {
		if entry.Kind == overlayEffectMint && entry.Mint != nil && entry.Mint.OwnerAddress == address {
			view := a.albumViewByPairLocked(snap, entry.Collection, entry.Mint.OwnerAddress, opts)
			if view != nil && view.collection != nil {
				overlayByCol[view.collection.PinId]++
			}
		}
	}
	a.mu.Unlock()
	staged, _ := a.db.PeekPendingAll()
	for _, sp := range staged {
		if sp.Entry.Kind == "mint" && sp.Entry.Mint != nil && sp.Entry.Mint.OwnerAddress == address {
			if view := a.albumViewByPair(sp.CollectionName, sp.IssuerAddress, opts); view != nil && view.collection != nil {
				overlayByCol[view.collection.PinId]++
			}
		}
	}
	for cid, count := range overlayByCol {
		if _, seen := holdCounts[cid]; !seen {
			*order = append(*order, cid)
		}
		holdCounts[cid] += count
	}
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
	// Optional album filter: pinId = collectionid (exact), name = earliest
	// album under that name.
	collectionFilter := c.Query("pinId")
	if collectionFilter == "" {
		collectionFilter = c.Query("name")
	}
	if collectionFilter != "" {
		col, err := a.resolveCollection(c.Query("pinId"), c.Query("name"))
		if err != nil {
			a.respondServiceError(c)
			return
		}
		if col == nil {
			a.respondNoData(c)
			return
		}
		collectionFilter = col.PinId
	}
	opts := queryViewOptions(c)
	if a.zeroConfTradeActive(opts) {
		// Zero-conf holder view: base items whose overlay owner moved away
		// hide, overlay-owned items (fresh mints, staged mints, delivered
		// spends) join — any chain depth (R-F-3.2/3.3).
		pinIds, total := a.holderItemPinIds(address, collectionFilter, opts)
		cursor, size := a.queryCursor(c), a.querySize(c)
		dtos := make([]gin.H, 0)
		start := cursor
		if start > total {
			start = total
		}
		end := cursor + size
		if end > total || end <= 0 {
			end = total
		}
		for _, pinId := range pinIds[start:end] {
			if view := a.itemViewByPinId(pinId, opts); view != nil {
				dtos = append(dtos, a.itemViewDTO(view, opts))
			}
		}
		a.respondOK(c, gin.H{"list": dtos, "total": total})
		return
	}
	cursor, size := a.queryCursor(c), a.querySize(c)
	dtos := make([]gin.H, 0)
	var total int64
	err := a.db.IterateOwnerItems(address, collectionFilter, func(collectionPinId, itemPinId string) bool {
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

// holderItemPinIds lists the address's visible items under the trade
// overlay, in serve order: confirmed holdings (overlay owner still the
// address), then overlay/staged items owned by the address.
func (a *Aggregator) holderItemPinIds(address, collectionFilter string, opts viewOptions) ([]string, int64) {
	a.mu.Lock()
	snap := a.overlaySnapshotLocked()

	currentOwner := func(itemPinId string) (owner string, burned bool) {
		if item, err := a.db.GetItem(itemPinId); err == nil && item != nil {
			iv := &itemView{item: &Item{ItemPinId: itemPinId, Output: item.Output, OwnerAddress: item.OwnerAddress}}
			iv.applyOwnershipFold(snap)
			return iv.item.OwnerAddress, iv.item.Burned
		}
		return "", false
	}

	var pinIds []string
	seen := make(map[string]struct{})
	_ = a.db.IterateOwnerItems(address, collectionFilter, func(collectionPinId, itemPinId string) bool {
		owner, burned := currentOwner(itemPinId)
		if burned || owner != address {
			return true // moved away or melted down in the mempool: hidden
		}
		if collectionFilter == "" || collectionPinId == collectionFilter {
			pinIds = append(pinIds, itemPinId)
			seen[itemPinId] = struct{}{}
		}
		return true
	})
	a.mu.Unlock()

	// Overlay deliveries to the address (chained spends land on overlay
	// outputs): the item's projected owner must be the address.
	a.mu.Lock()
	delivered := make(map[string]struct{})
	for _, sp := range snap.spends {
		if sp.ToAddress == address && !sp.Burned {
			delivered[sp.ItemPinId] = struct{}{}
		}
	}
	a.mu.Unlock()
	for itemPinId := range delivered {
		if _, listed := seen[itemPinId]; listed {
			continue
		}
		if view := a.itemViewByPinId(itemPinId, opts); view != nil &&
			view.item.OwnerAddress == address && !view.item.Burned &&
			(collectionFilter == "" || view.item.CollectionPinId == collectionFilter) {
			pinIds = append(pinIds, itemPinId)
			seen[itemPinId] = struct{}{}
		}
	}

	// Overlay + staged mints owned by the address.
	a.mu.Lock()
	snap = a.overlaySnapshotLocked()
	var ownedPinIds []string
	for _, entry := range snap.pins {
		if entry.Kind != overlayEffectMint || entry.Mint == nil || entry.Mint.OwnerAddress != address {
			continue
		}
		ownedPinIds = append(ownedPinIds, entry.Mint.ItemPinId)
	}
	a.mu.Unlock()
	for _, sp := range func() []StagedPin {
		staged, _ := a.db.PeekPendingAll()
		return staged
	}() {
		if sp.Entry.Kind == "mint" && sp.Entry.Mint != nil && sp.Entry.Mint.OwnerAddress == address {
			ownedPinIds = append(ownedPinIds, sp.Entry.Mint.ItemPinId)
		}
	}
	for _, itemPinId := range ownedPinIds {
		if _, listed := seen[itemPinId]; listed {
			continue
		}
		if view := a.itemViewByPinId(itemPinId, opts); view != nil &&
			!view.overSupply && !view.item.Burned &&
			(collectionFilter == "" || view.item.CollectionPinId == collectionFilter) {
			pinIds = append(pinIds, itemPinId)
			seen[itemPinId] = struct{}{}
		}
	}
	return pinIds, int64(len(pinIds))
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
	opts := queryViewOptions(c)
	if a.zeroConfIssuanceActive(opts) {
		if view := a.itemViewByPinId(pinId, opts); view != nil {
			a.respondOK(c, a.itemViewDTO(view, opts))
			return
		}
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
	opts := queryViewOptions(c)
	tradeActive := a.zeroConfTradeActive(opts)
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
	// Overlay provenance trails the confirmed history (ascending timeline
	// position — these are the most recent, unconfirmed ownership moves,
	// R-F-3.4); total includes them.
	var overlayRows []gin.H
	if tradeActive {
		for _, sp := range a.overlayTransfersForItem(pinId) {
			if total >= cursor && total < cursor+size {
				overlayRows = append(overlayRows, overlayTransferDTO(sp))
			}
			total++
		}
		list = append(list, overlayRows...)
	}
	// list holds confirmed history plus (gate on) overlay rows; the
	// separate pending array keeps its pre-zero-conf meaning so it never
	// causes a false "no transfers" answer.
	pendingList := make([]gin.H, 0)
	if pt, _ := a.db.GetPendingTransfer(pinId); pt != nil {
		pendingList = append(pendingList, a.pendingTransferDTO(pt))
	}
	a.respondOK(c, gin.H{"list": list, "total": total, "pending": pendingList})
}

// overlayTransfersForItem returns the item's overlay spends in provisional
// order (timeline order).
func (a *Aggregator) overlayTransfersForItem(itemPinId string) []*overlaySpendEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := a.overlaySnapshotLocked()
	var spends []*overlaySpendEntry
	for _, sp := range snap.spends {
		if sp.ItemPinId == itemPinId {
			spends = append(spends, sp)
		}
	}
	sort.Slice(spends, func(i, j int) bool {
		if spends[i].FirstSeenAt != spends[j].FirstSeenAt {
			return spends[i].FirstSeenAt < spends[j].FirstSeenAt
		}
		return spends[i].TxId < spends[j].TxId
	})
	return spends
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
	opts := queryViewOptions(c)
	if a.zeroConfIssuanceActive(opts) || a.zeroConfTradeActive(opts) {
		if view := a.itemViewByPinId(pinId, opts); view != nil {
			// Overlay facts report with confirmed:false; for an overlay
			// item, collected:true means provisionally collected (R-F-5).
			data := gin.H{
				"indexed":            true,
				"collected":          true,
				"confirmed":          view.confirmed,
				"pinId":              pinId,
				"itemPinId":          pinId,
				"collectionName":     view.item.CollectionName,
				"collectionid":       view.item.CollectionPinId,
				"collectionPinId":    view.item.CollectionPinId,
				"creatorMinted":      view.item.CreatorMinted,
				"burned":             view.item.Burned,
				"transferCount":      view.item.TransferCount,
				"numberFinal":        view.numberFinal,
				"ownershipConfirmed": view.ownershipConfirmed,
				"ownerAddress":       view.item.OwnerAddress,
				"ownerMetaId":        view.item.OwnerMetaId,
				"genesisChainName":   view.item.GenesisChainName,
				"genesisHeight":      view.item.GenesisHeight,
				"genesisTx":          view.item.GenesisTx,
				"itemGenesis": gin.H{
					"chainName": view.item.GenesisChainName,
					"height":    view.item.GenesisHeight,
					"tx":        view.item.GenesisTx,
				},
			}
			if col, _ := a.db.GetCollectionByPinId(view.item.CollectionPinId); col != nil {
				data["collectionGenesis"] = gin.H{
					"chainName": col.GenesisChainName,
					"height":    col.GenesisHeight,
					"tx":        col.GenesisTx,
				}
			} else if view.confirmedOwnerAddress == "" {
				data["collectionGenesis"] = gin.H{"chainName": "", "height": int64(0), "tx": ""}
			}
			a.respondOK(c, data)
			return
		}
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
	if col, _ := a.db.GetCollectionByPinId(item.CollectionPinId); col != nil {
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
