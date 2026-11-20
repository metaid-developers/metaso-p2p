package mrc721nft

import (
	"sort"
	"strings"
)

// TransferEvent reports a confirmed or zero-conf overlay ownership transfer or
// burn for an MRC-721 item. Fired outside mrc721nft mutexes so downstream hooks
// (e.g. mrc721 sell invalidation and trade settlement) can query read models safely.
type TransferEvent struct {
	ItemPinId       string
	CollectionPinId string
	FromAddress     string
	ToAddress       string
	NewOutpoint     string
	ChainName       string
	Height          int64
	TxId            string
	Time            int64
	Burned          bool
	Confirmed       bool
}

// AddressResolver resolves MVC/BTC addresses associated with a globalMetaId.
type AddressResolver interface {
	ResolveAddressesByGlobalMetaId(globalMetaId string) []string
}

// CollectionMarketFields carries optional market & CA fields injected into
// /api/mrc721/collection/* responses when wired.
type CollectionMarketFields struct {
	CaVerified          bool
	CaDisplayTitle      string
	HolderCount         int
	ListedCount         int
	FloorPriceMeMilli   *int64
	FloorPriceCnyCents  *int64
	MedianPriceMeMilli  *int64
	MedianPriceCnyCents *int64
}

// CellEnricherRef is the optional market & CA enricher interface for collection DTOs.
type CellEnricherRef interface {
	EnrichCollection(collectionPinId string) *CollectionMarketFields
}

// OwnerProfileDTO is the owner profile projected onto /api/mrc721/item/info.
type OwnerProfileDTO struct {
	Address            string `json:"address"`
	GlobalMetaId       string `json:"globalMetaId"`
	Name               string `json:"name"`
	Avatar             string `json:"avatar"`
	HasChatPubkey      bool   `json:"hasChatPubkey"`
	IsOnline           bool   `json:"isOnline"`
	LastSeenAgoSeconds *int64 `json:"lastSeenAgoSeconds,omitempty"`
}

// ItemDetailEnricher enriches /api/mrc721/item/info with ownerProfile and activeListing.
type ItemDetailEnricher interface {
	EnrichItemDetail(itemPinId, ownerAddress string) (ownerProfile *OwnerProfileDTO, activeListing any)
}

// ItemSummaryInfo is a read-only projection of an MRC-721 item for sibling aggregators.
type ItemSummaryInfo struct {
	ItemPinId       string
	CollectionPinId string
	CollectionName  string
	Name            string
	Desc            string
	Cover           string
	Metadata        string
	Number          int64
	OwnerAddress    string
	OwnerMetaId     string
	Output          string
	Burned          bool
	Confirmed       bool
}

// SetTransferEventHook registers a listener for item transfer/burn events.
func (a *Aggregator) SetTransferEventHook(hook func(TransferEvent)) {
	a.transferEventHook = hook
}

// SetOverlayReconciledHook registers a listener fired outside overlayMu when
// stale mempool entries are evicted during ReconcileMempoolRound.
func (a *Aggregator) SetOverlayReconciledHook(hook func()) {
	a.overlayMu.Lock()
	a.overlayReconciledHook = hook
	a.overlayMu.Unlock()
}

// SetAddressResolver wires globalMetaId -> addresses lookup for /address/* endpoints.
func (a *Aggregator) SetAddressResolver(resolver AddressResolver) {
	a.addressResolver = resolver
}

// SetCollectionMarketEnricher wires optional market/CA summary fields onto collection DTOs.
func (a *Aggregator) SetCollectionMarketEnricher(enricher CellEnricherRef) {
	a.collectionMarketEnrich = enricher
}

// SetItemDetailEnricher wires optional ownerProfile/activeListing enrichment onto /item/info.
func (a *Aggregator) SetItemDetailEnricher(enricher ItemDetailEnricher) {
	a.itemDetailEnrich = enricher
}

func (a *Aggregator) emitTransferEvents(events []TransferEvent) {
	if a == nil || a.transferEventHook == nil || len(events) == 0 {
		return
	}
	for _, ev := range events {
		a.transferEventHook(ev)
	}
}

func (a *Aggregator) resolveQueryAddresses(address, globalMetaId string) []string {
	address = strings.TrimSpace(address)
	if address != "" {
		return []string{address}
	}
	globalMetaId = strings.TrimSpace(globalMetaId)
	if globalMetaId == "" || a.addressResolver == nil {
		return nil
	}
	raw := a.addressResolver.ResolveAddressesByGlobalMetaId(globalMetaId)
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, addr := range raw {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, ok := seen[addr]; ok {
			continue
		}
		seen[addr] = struct{}{}
		out = append(out, addr)
	}
	return out
}

func (a *Aggregator) applyCollectionMarketFields(dto map[string]any, collectionPinId string, opts viewOptions) {
	if a == nil || a.collectionMarketEnrich == nil || opts.confirmedOnly || collectionPinId == "" {
		return
	}
	fields := a.collectionMarketEnrich.EnrichCollection(collectionPinId)
	if fields == nil {
		return
	}
	dto["caVerified"] = fields.CaVerified
	if fields.CaDisplayTitle != "" {
		dto["caDisplayTitle"] = fields.CaDisplayTitle
	}
	dto["holderCount"] = fields.HolderCount
	dto["listedCount"] = fields.ListedCount
	dto["floorPriceMeMilli"] = fields.FloorPriceMeMilli
	dto["floorPriceCnyCents"] = fields.FloorPriceCnyCents
	dto["medianPriceMeMilli"] = fields.MedianPriceMeMilli
	dto["medianPriceCnyCents"] = fields.MedianPriceCnyCents
}

func (a *Aggregator) applyItemDetailFields(dto map[string]any, itemPinId, ownerAddress string, opts viewOptions) {
	if a == nil || a.itemDetailEnrich == nil || opts.confirmedOnly || itemPinId == "" {
		return
	}
	ownerProfile, activeListing := a.itemDetailEnrich.EnrichItemDetail(itemPinId, ownerAddress)
	if ownerProfile != nil {
		dto["ownerProfile"] = ownerProfile
	}
	dto["activeListing"] = activeListing
}

// ItemOwnership returns the current effective owner, outpoint, and burned status of an item.
func (a *Aggregator) ItemOwnership(itemPinId string, zeroConf bool) (ownerAddress string, outpoint string, burned bool, found bool) {
	if a == nil || a.db == nil || strings.TrimSpace(itemPinId) == "" {
		return "", "", false, false
	}
	if zeroConf && (a.zeroConfIssuanceGate.Load() || a.zeroConfTradeGate.Load()) {
		opts := viewOptions{confirmedOnly: false}
		if view := a.itemViewByPinId(itemPinId, opts); view != nil && view.item != nil {
			return view.item.OwnerAddress, view.item.Output, view.item.Burned, true
		}
	}
	item, err := a.db.GetItem(itemPinId)
	if err != nil || item == nil {
		return "", "", false, false
	}
	return item.OwnerAddress, item.Output, item.Burned, true
}

// ItemSummary returns a read-only summary of an item (with optional zero-conf overlay).
func (a *Aggregator) ItemSummary(itemPinId string, zeroConf bool) *ItemSummaryInfo {
	if a == nil || a.db == nil || strings.TrimSpace(itemPinId) == "" {
		return nil
	}
	if zeroConf && (a.zeroConfIssuanceGate.Load() || a.zeroConfTradeGate.Load()) {
		if view := a.itemViewByPinId(itemPinId, viewOptions{}); view != nil && view.item != nil {
			it := view.item
			return &ItemSummaryInfo{
				ItemPinId:       it.ItemPinId,
				CollectionPinId: it.CollectionPinId,
				CollectionName:  it.CollectionName,
				Name:            it.Name,
				Desc:            it.Desc,
				Cover:           it.Cover,
				Metadata:        it.Metadata,
				Number:          it.Number,
				OwnerAddress:    it.OwnerAddress,
				OwnerMetaId:     it.OwnerMetaId,
				Output:          it.Output,
				Burned:          it.Burned,
				Confirmed:       view.confirmed,
			}
		}
	}
	item, err := a.db.GetItem(itemPinId)
	if err != nil || item == nil {
		return nil
	}
	return &ItemSummaryInfo{
		ItemPinId:       item.ItemPinId,
		CollectionPinId: item.CollectionPinId,
		CollectionName:  item.CollectionName,
		Name:            item.Name,
		Desc:            item.Desc,
		Cover:           item.Cover,
		Metadata:        item.Metadata,
		Number:          item.Number,
		OwnerAddress:    item.OwnerAddress,
		OwnerMetaId:     item.OwnerMetaId,
		Output:          item.Output,
		Burned:          item.Burned,
		Confirmed:       true,
	}
}

// ListAllCollections returns all confirmed (and overlay, when zeroConf is active) collections.
func (a *Aggregator) ListAllCollections(zeroConf bool) []*Collection {
	if a == nil || a.db == nil {
		return nil
	}
	var out []*Collection
	seen := make(map[string]struct{})
	var offset int64
	const pageSize = 200
	for {
		list, total, err := a.db.ListCollections(offset, pageSize)
		if err != nil || len(list) == 0 {
			break
		}
		for _, col := range list {
			if col == nil || col.PinId == "" {
				continue
			}
			c := cloneCollection(col)
			if zeroConf && a.zeroConfIssuanceGate.Load() {
				if view := a.albumViewByPair(col.CollectionName, col.CreateAddress, viewOptions{}); view != nil && view.collection != nil {
					c = cloneCollection(view.collection)
					c.TotalNum = view.confirmedMinted + view.stagedMintCount + view.overlayMintCount
				}
			}
			out = append(out, c)
			seen[strings.ToLower(c.PinId)] = struct{}{}
		}
		offset += int64(len(list))
		if offset >= total {
			break
		}
	}
	if zeroConf && a.zeroConfIssuanceGate.Load() {
		snap := a.overlaySnapshot()
		for _, entry := range overlayAlbums(snap) {
			view := a.albumViewByPairSnap(snap, entry.ColCreate.CollectionName, entry.ColCreate.CreateAddress, viewOptions{})
			if view == nil || view.collection == nil || view.collection.PinId == "" {
				continue
			}
			if _, ok := seen[strings.ToLower(view.collection.PinId)]; ok {
				continue
			}
			c := cloneCollection(view.collection)
			c.TotalNum = view.confirmedMinted + view.stagedMintCount + view.overlayMintCount
			out = append(out, c)
			seen[strings.ToLower(c.PinId)] = struct{}{}
		}
	}
	return out
}

// CollectionHolderStats computes the unique holder count and per-address item counts
// for non-burned items in collectionPinId.
func (a *Aggregator) CollectionHolderStats(collectionPinId string, zeroConf bool) (holderCount int, counts map[string]int, err error) {
	counts = make(map[string]int)
	if a == nil || a.db == nil || strings.TrimSpace(collectionPinId) == "" {
		return 0, counts, nil
	}
	if zeroConf && (a.zeroConfIssuanceGate.Load() || a.zeroConfTradeGate.Load()) {
		if view := a.albumViewByPinId(collectionPinId, viewOptions{}); view != nil {
			ivs := a.itemViewsForAlbum(view, viewOptions{})
			for _, iv := range ivs {
				if iv == nil || iv.item == nil || iv.item.Burned || iv.overSupply {
					continue
				}
				owner := strings.TrimSpace(iv.item.OwnerAddress)
				if owner == "" {
					continue
				}
				counts[owner]++
			}
			return len(counts), counts, nil
		}
	}
	err = a.db.IterateCollectionItems(collectionPinId, func(seq int64, itemPinId string) bool {
		item, getErr := a.db.GetItem(itemPinId)
		if getErr != nil || item == nil || item.Burned {
			return true
		}
		owner := strings.TrimSpace(item.OwnerAddress)
		if owner == "" {
			return true
		}
		counts[owner]++
		return true
	})
	return len(counts), counts, err
}

// CollectionBurnedCount counts burned items in collectionPinId.
func (a *Aggregator) CollectionBurnedCount(collectionPinId string, zeroConf bool) int {
	if a == nil || a.db == nil || strings.TrimSpace(collectionPinId) == "" {
		return 0
	}
	burned := 0
	if zeroConf && (a.zeroConfIssuanceGate.Load() || a.zeroConfTradeGate.Load()) {
		if view := a.albumViewByPinId(collectionPinId, viewOptions{}); view != nil {
			for _, iv := range a.itemViewsForAlbum(view, viewOptions{}) {
				if iv != nil && iv.item != nil && !iv.overSupply && iv.item.Burned {
					burned++
				}
			}
			return burned
		}
	}
	_ = a.db.IterateCollectionItems(collectionPinId, func(_ int64, itemPinId string) bool {
		item, getErr := a.db.GetItem(itemPinId)
		if getErr == nil && item != nil && item.Burned {
			burned++
		}
		return true
	})
	return burned
}

// ItemLatestConfirmedTransfer returns the latest confirmed transfer height,
// chain time, txId, and total confirmed transferCount for itemPinId.
func (a *Aggregator) ItemLatestConfirmedTransfer(itemPinId string) (height int64, txTime int64, txId string, transferCount int64, found bool) {
	if a == nil || a.db == nil || strings.TrimSpace(itemPinId) == "" {
		return 0, 0, "", 0, false
	}
	if item, err := a.db.GetItem(itemPinId); err == nil && item != nil {
		transferCount = item.TransferCount
	}
	_ = a.db.IterateItemTransfers(itemPinId, func(tr *ItemTransfer) bool {
		if tr == nil {
			return true
		}
		trTx := tr.TxId
		if idx := strings.Index(trTx, ":"); idx >= 0 {
			trTx = trTx[:idx]
		}
		if !found || tr.Height > height || (tr.Height == height && tr.Time >= txTime) {
			height = tr.Height
			txTime = tr.Time
			txId = trTx
			found = true
		}
		return true
	})
	return height, txTime, txId, transferCount, found
}

// ItemTransferByTx looks up a confirmed or (when zeroConf is true) overlay/pending transfer for (itemPinId, txId).
func (a *Aggregator) ItemTransferByTx(itemPinId, txId string, zeroConf bool) (*ItemTransfer, bool) {
	if a == nil || a.db == nil {
		return nil, false
	}
	itemPinId = strings.TrimSpace(itemPinId)
	txId = strings.ToLower(strings.TrimSpace(txId))
	if itemPinId == "" || txId == "" {
		return nil, false
	}
	var found *ItemTransfer
	_ = a.db.IterateItemTransfers(itemPinId, func(tr *ItemTransfer) bool {
		trTx := tr.TxId
		if idx := strings.Index(trTx, ":"); idx >= 0 {
			trTx = trTx[:idx]
		}
		if strings.EqualFold(trTx, txId) {
			cp := *tr
			cp.TxId = trTx
			found = &cp
			return false
		}
		return true
	})
	if found != nil {
		return found, true
	}
	if zeroConf && a.zeroConfTradeGate.Load() {
		spends := a.overlayTransfersForItem(itemPinId)
		sort.Slice(spends, func(i, j int) bool { return spends[i].FirstSeenAt > spends[j].FirstSeenAt })
		for _, sp := range spends {
			if strings.EqualFold(sp.TxId, txId) {
				return &ItemTransfer{
					ItemPinId:   sp.ItemPinId,
					FromAddress: sp.FromAddress,
					ToAddress:   sp.ToAddress,
					ChainName:   sp.ChainName,
					Height:      -1,
					TxId:        sp.TxId,
					Time:        sp.FirstSeenAt,
				}, true
			}
		}
		if pt, _ := a.db.GetPendingTransfer(itemPinId); pt != nil && strings.EqualFold(pt.TxId, txId) {
			return &ItemTransfer{
				ItemPinId:   pt.ItemPinId,
				FromAddress: pt.FromAddress,
				ToAddress:   pt.ToAddress,
				ChainName:   pt.ChainName,
				Height:      -1,
				TxId:        pt.TxId,
				Time:        pt.FirstSeenAt,
			}, true
		}
	}
	return nil, false
}
