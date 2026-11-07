package mrc721nft

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"
)

// BackfillNft bootstraps the native aggregate from the manapi-compatible
// upstream (man-p2p) read views. Since the issuer-only protocol revision
// (docs/mrc721-protocol-revision-issuer-only.md) the upstream mirror is no
// longer diff-identical to this surface: upstream indexes open-mint items,
// while the revised semantics index issuer-minted items only — so the
// hydrate filters on the upstream rows' creatorMinted flag and renumbers
// the surviving items densely per album (the upstream numbers count
// open-mint slots the revised aggregate does not recognise):
//
//	phase 1: collection/pageList      → c:/cp: album records (TotalNum
//	                                    deliberately zeroed; phase 2 rebuilds it)
//	phase 2: collection/items/pageList → i:/ci:/oi: item state (owner,
//	                                     output, burned, transferCount, descs)
//	phase 3: item/transfers           → th: provenance history
//
// Every phase is idempotent and safe to re-run (record-level dedup), and the
// three phases are independently re-runnable via the command's --skip-*
// flags. Run it with the daemon stopped: the command writes the same Pebble
// namespace the live ingest uses.
type BackfillStats struct {
	Collections      int64 // upstream collection rows applied
	Items            int64 // upstream item rows applied
	OwnerMoves       int64 // items whose holder changed vs the stored record (re-runs)
	Transfers        int64 // upstream transfer rows applied
	SkippedTransfers int64 // transfer rows already present (idempotent skips)
	SkippedNonIssuer int64 // item rows dropped: not minted by the album issuer (issuer-only gate)
}

// BackfillOptions carries the run dependencies.
type BackfillOptions struct {
	Context  context.Context
	Client   *UpstreamClient
	PageSize int
	Stats    *BackfillStats
}

// BackfillNft runs the three-phase hydrate. The context bounds the whole
// run; upstream errors abort with the stats gathered so far.
func (a *Aggregator) BackfillNft(opts BackfillOptions) error {
	ctx := opts.Context
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 8*time.Hour)
		defer cancel()
	}
	client := opts.Client
	if client == nil {
		return errors.New("mrc721nft backfill: upstream client is required")
	}
	size := opts.PageSize
	if size <= 0 {
		size = 100
	}
	stats := opts.Stats
	if stats == nil {
		stats = &BackfillStats{}
	}

	if err := a.backfillCollections(ctx, client, size, stats); err != nil {
		return err
	}
	if err := a.backfillItems(ctx, client, size, stats); err != nil {
		return err
	}
	return a.backfillTransfers(ctx, client, size, stats)
}

// backfillCollections sweeps the upstream collection registry. The DTO is a
// complete record (incl. genesis fields and the modify version chain), so
// rows are applied directly — no pin replay involved.
func (a *Aggregator) backfillCollections(ctx context.Context, client *UpstreamClient, size int, stats *BackfillStats) error {
	log.Printf("[mrc721nft-backfill] phase 1: collections pageSize=%d", size)
	offset := int64(0)
	page := 0
	for {
		rows, total, err := client.ListCollections(ctx, offset, size)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		a.mu.Lock()
		for i := range rows {
			if err := a.db.SaveCollection(collectionFromUpstream(&rows[i])); err != nil {
				a.mu.Unlock()
				return err
			}
			stats.Collections++
		}
		a.mu.Unlock()
		page++
		if page%50 == 0 {
			log.Printf("[mrc721nft-backfill] collections: page=%d applied=%d total=%d", page, stats.Collections, total)
		}
		offset += int64(len(rows))
		if total > 0 && offset >= total || len(rows) < size {
			break
		}
	}
	log.Printf("[mrc721nft-backfill] phase 1 done: collections=%d", stats.Collections)
	return nil
}

// collectionFromUpstream maps an upstream collection DTO row onto the native
// record. createMetaId is always derived from the create address: the
// upstream metaid fields carry man-p2p's legacy sha256(address) hex, while
// this surface serves global metaids (IDAddress format). TotalNum is
// deliberately NOT taken from the upstream row: it counts open-mint items
// the revised issuer-only aggregate does not index, and phase 2 rebuilds
// the count from the rows it actually applies.
func collectionFromUpstream(row *upstreamCollection) *Collection {
	col := &Collection{
		CollectionName:   row.CollectionName,
		Name:             row.Name,
		TotalSupply:      row.TotalSupply,
		RoyaltyRate:      row.RoyaltyRate,
		Desc:             row.Desc,
		Website:          row.Website,
		Cover:            row.Cover,
		Metadata:         row.Metadata,
		PinId:            row.PinId,
		CreateAddress:    row.CreateAddress,
		CreateTime:       row.CreateTime,
		GenesisChainName: row.GenesisChainName,
		GenesisHeight:    row.GenesisHeight,
		GenesisTx:        row.GenesisTx,
		ModifyHistory:    row.ModifyHistory,
	}
	if col.CreateAddress == "" {
		col.CreateAddress = row.Address
	}
	col.CreateMetaId = globalMetaIdByAddress(col.CreateAddress)
	return col
}

// backfillItems sweeps each known collection's items through the upstream
// item pageList and reconciles the native item records (owner, output,
// burned, transferCount and the desc fields) plus the holder index.
func (a *Aggregator) backfillItems(ctx context.Context, client *UpstreamClient, size int, stats *BackfillStats) error {
	log.Printf("[mrc721nft-backfill] phase 2: items pageSize=%d", size)
	// Iterate our own registry so re-runs pick up collections hydrated by
	// earlier runs even when the upstream sweep fails halfway.
	var cols []*Collection
	err := a.store.ScanPrefix(Namespace, []byte(keyCollection), func(_, value []byte) error {
		var col Collection
		if err := json.Unmarshal(value, &col); err == nil {
			cols = append(cols, &col)
		}
		return nil
	})
	if err != nil {
		return err
	}
	done := 0
	for _, col := range cols {
		itemOffset := int64(0)
		for {
			rows, total, err := client.ListCollectionItems(ctx, col.PinId, itemOffset, size)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			a.mu.Lock()
			for i := range rows {
				if err := a.applyUpstreamItem(col, &rows[i], stats); err != nil {
					a.mu.Unlock()
					return err
				}
			}
			a.mu.Unlock()
			itemOffset += int64(len(rows))
			if total > 0 && itemOffset >= total || len(rows) < size {
				break
			}
		}
		done++
		if done%50 == 0 {
			log.Printf("[mrc721nft-backfill] items: collections=%d/%d applied=%d", done, len(cols), stats.Items)
		}
	}
	log.Printf("[mrc721nft-backfill] phase 2 done: items=%d ownerMoves=%d", stats.Items, stats.OwnerMoves)
	return nil
}

// applyUpstreamItem upserts one upstream item row into the native records.
// Issuer-only gate: upstream indexes open-mint items, and only rows minted
// by the album issuer (creatorMinted) are indexable under the revised
// semantics — the rest are counted and skipped. Freshness rule: the stored
// item is only overwritten when the upstream row is strictly ahead on
// transferCount (the upstream saw transfers the local live path has not
// processed); otherwise the live state is kept and only empty desc fields
// get filled (mirroring the first-per-field item_desc semantics). This makes
// re-runs safe against a stale upstream — they can fill gaps but never
// rewind live ownership.
//
// Numbering: upstream numbers count open-mint slots the revised aggregate
// does not recognise, so applied items are renumbered densely per album in
// sweep order (Number = TotalNum+1) and the album's TotalNum is maintained
// alongside; a refresh of an already-stored item keeps its stored number.
func (a *Aggregator) applyUpstreamItem(col *Collection, row *upstreamItem, stats *BackfillStats) error {
	if row.ItemPinId == "" {
		return nil
	}
	if !row.CreatorMinted {
		stats.SkippedNonIssuer++
		return nil
	}
	existing, _ := a.db.GetItem(row.ItemPinId)
	if existing != nil && row.TransferCount <= existing.TransferCount {
		changed := false
		if existing.Name == "" && row.Name != "" {
			existing.Name, changed = row.Name, true
		}
		if existing.Desc == "" && row.Desc != "" {
			existing.Desc, changed = row.Desc, true
		}
		if existing.Cover == "" && row.Cover != "" {
			existing.Cover, changed = row.Cover, true
		}
		if existing.Metadata == "" && row.Metadata != "" {
			existing.Metadata, changed = row.Metadata, true
		}
		if existing.ContentSummary == "" && row.ContentSummary != "" {
			existing.ContentSummary, changed = row.ContentSummary, true
		}
		if changed {
			return a.db.SaveItem(existing)
		}
		return nil
	}
	item := &Item{
		ItemPinId:        row.ItemPinId,
		CollectionName:   col.CollectionName,
		CollectionPinId:  row.CollectionPinId,
		Name:             row.Name,
		Desc:             row.Desc,
		Cover:            row.Cover,
		Metadata:         row.Metadata,
		OwnerAddress:     row.OwnerAddress,
		CreatorMinted:    row.CreatorMinted,
		Burned:           row.Burned,
		ContentType:      row.ContentType,
		ContentSummary:   row.ContentSummary,
		GenesisChainName: row.GenesisChainName,
		GenesisHeight:    row.GenesisHeight,
		GenesisTx:        row.GenesisTx,
		Output:           row.Output,
		OutputValue:      row.OutputValue,
		CreateTime:       row.CreateTime,
		TransferCount:    row.TransferCount,
	}
	if item.CollectionPinId == "" {
		item.CollectionPinId = col.PinId
	}
	if item.GenesisChainName == "" {
		item.GenesisChainName = col.GenesisChainName
	}
	// ownerMetaId is derived from the owner address (global metaid), never
	// taken from the upstream row's legacy sha256 hex value.
	item.OwnerMetaId = globalMetaIdByAddress(item.OwnerAddress)
	if existing != nil {
		// Upstream is strictly ahead: refresh state but keep the stored
		// dense number; re-key the holder index from the stored owner to
		// the upstream one.
		item.Number = existing.Number
		if existing.OwnerAddress != item.OwnerAddress && existing.OwnerAddress != "" {
			a.db.DeleteOwnerItem(existing.OwnerAddress, existing.CollectionPinId, existing.ItemPinId)
			stats.OwnerMoves++
		}
	} else {
		col.TotalNum++
		item.Number = col.TotalNum
	}
	if err := a.db.SaveItem(item); err != nil {
		return err
	}
	if existing == nil {
		// Persist the renumber-driven TotalNum bump alongside the item.
		if err := a.db.SaveCollection(col); err != nil {
			return err
		}
	}
	if !item.Burned && item.OwnerAddress != "" {
		if err := a.db.PutOwnerItem(item.OwnerAddress, item.CollectionPinId, item.ItemPinId); err != nil {
			return err
		}
	}
	if err := a.db.AppendCollectionItem(item.CollectionPinId, item.Number, item.ItemPinId); err != nil {
		return err
	}
	a.markTrackedDirty()
	stats.Items++
	return nil
}

// backfillTransfers sweeps each known item's confirmed transfer history into
// th: (dedup by (itemPinId, height, txId) — the same idempotency key the
// live transfer hook uses). Items with transferCount 0 are skipped: their
// history is empty by definition.
func (a *Aggregator) backfillTransfers(ctx context.Context, client *UpstreamClient, size int, stats *BackfillStats) error {
	log.Printf("[mrc721nft-backfill] phase 3: transfers pageSize=%d", size)
	var items []*Item
	if err := a.db.IterateItems(func(item *Item) bool {
		items = append(items, item)
		return true
	}); err != nil {
		return err
	}
	processed := 0
	for _, item := range items {
		if item.TransferCount <= 0 {
			continue
		}
		offset := int64(0)
		for {
			rows, total, err := client.ListItemTransfers(ctx, item.ItemPinId, offset, size)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				break
			}
			a.mu.Lock()
			for i := range rows {
				row := &rows[i]
				if existing, _ := a.db.GetItemTransfer(row.ItemPinId, row.Height, row.TxId); existing != nil {
					stats.SkippedTransfers++
					continue
				}
				if err := a.db.SaveItemTransfer(&ItemTransfer{
					ItemPinId:   row.ItemPinId,
					FromAddress: row.FromAddress,
					ToAddress:   row.ToAddress,
					ChainName:   row.ChainName,
					Height:      row.Height,
					TxId:        row.TxId,
					Time:        row.Time,
				}); err != nil {
					a.mu.Unlock()
					return err
				}
				stats.Transfers++
			}
			a.mu.Unlock()
			offset += int64(len(rows))
			if total > 0 && offset >= total || len(rows) < size {
				break
			}
		}
		processed++
		if processed%200 == 0 {
			log.Printf("[mrc721nft-backfill] transfers: items=%d/%d applied=%d skipped=%d", processed, len(items), stats.Transfers, stats.SkippedTransfers)
		}
	}
	log.Printf("[mrc721nft-backfill] phase 3 done: transfers=%d skipped=%d itemsScanned=%d", stats.Transfers, stats.SkippedTransfers, len(items))
	return nil
}
