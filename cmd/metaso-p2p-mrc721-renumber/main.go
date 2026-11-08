// Command metaso-p2p-mrc721-renumber repairs MRC-721 albums whose staged
// (mint-before-register) mints drained in Pebble's lexicographic pinId key
// order instead of chain order — the R-F-10 defect. It re-derives every
// affected album's numbering (and supply cut) from the chain positions
// (height, block tx index, pin output index) and rewrites the item records,
// the in-album mint order keys and the album totals.
//
// Detection is direct: a healthy album numbers its items 1..n exactly in
// chain order, so any album whose items sort to a different sequence is
// affected. Items minted before Item.TxIndex existed get their tx index
// hydrated from the MVC node (one CatchPins call per distinct block height).
//
// Run with the metaso-p2p daemon STOPPED (same Pebble namespace), as the
// service user (a root-run tool leaves the namespace root-owned and the
// daemon fails Init on the LOCK). Dry-run by default; --apply executes.
// The command prints the affected collectionids — publish the list.
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/chain/mvc"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("mrc721-renumber: %v", err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("metaso-p2p-mrc721-renumber", flag.ContinueOnError)
	dataDir := fs.String("data-dir", cfg.Pebble.DataDir, "Pebble data directory of the metaso-p2p daemon")
	apply := fs.Bool("apply", false, "execute the repair (default is a dry-run plan)")
	noRPC := fs.Bool("no-rpc", false, "skip the MVC node hydration of missing tx indexes (their order falls back to the pin output index)")
	if err := fs.Parse(flag.Args()); err != nil {
		return err
	}

	store := storage.NewPebbleStore(*dataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close pebble store: %v", err)
		}
	}()
	agg := &mrc721nft.Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		return err
	}

	// Optional MVC node access for tx-index hydration (missing on items
	// minted before Item.TxIndex existed). Failure is soft: those items then
	// order by (height, 0, pin output index) with a warning.
	var hydration *txIndexHydrator
	if chainCfg := cfg.BlockIndex.MVC; chainCfg.Enabled && !*noRPC {
		chain := mvc.NewChain(chainCfg)
		if err := chain.Init(); err != nil {
			log.Printf("WARNING: mvc rpc unavailable (%v); missing tx indexes stay approximate", err)
		} else {
			hydration = newTxIndexHydrator(mvc.NewIndexer(chain, mvc.NetParams("")))
		}
	}

	cols, _, err := agg.ListCollections()
	if err != nil {
		return err
	}

	affected := 0
	for _, col := range cols {
		items, err := albumItems(agg, col.PinId)
		if err != nil {
			return fmt.Errorf("collect items for %s: %w", col.PinId, err)
		}
		if hydration != nil {
			if err := hydration.fill(items); err != nil {
				log.Printf("WARNING: tx-index hydration for %s: %v", col.PinId, err)
			}
		}
		plan := buildPlan(col, items)
		if plan == nil {
			continue
		}
		affected++
		printPlan(col, plan)
		if !*apply {
			continue
		}
		if err := applyPlan(agg, col, plan); err != nil {
			return fmt.Errorf("apply plan for %s: %w", col.PinId, err)
		}
		log.Printf("REPAIRED %s (%s / %s)", col.PinId, col.CollectionName, col.CreateAddress)
	}

	if affected == 0 {
		log.Printf("no album needs renumbering — nothing to do")
		return nil
	}
	if *apply {
		log.Printf("repaired %d album(s) — affected collectionids are listed above, publish them", affected)
	} else {
		log.Printf("dry-run: %d album(s) need renumbering — re-run with --apply to execute (daemon stopped)", affected)
	}
	return nil
}

type albumItem struct {
	seq  int64
	item *mrc721nft.Item
}

// albumItems returns the album's items joined with their in-album sequence.
func albumItems(agg *mrc721nft.Aggregator, collectionPinId string) ([]*albumItem, error) {
	var items []*albumItem
	err := agg.IterateCollectionItems(collectionPinId, func(seq int64, itemPinId string) bool {
		item, err := agg.GetItem(itemPinId)
		if err != nil || item == nil {
			log.Printf("WARNING: album %s seq %d: item %s missing: %v", collectionPinId, seq, itemPinId, err)
			return true
		}
		items = append(items, &albumItem{seq: seq, item: item})
		return true
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].seq < items[j].seq })
	return items, nil
}

// renumberPlan is one album's repair: survivors keep densely renumbered in
// chain order; drops leave the album entirely (the supply cut re-applied on
// the corrected order).
type renumberPlan struct {
	items  []*albumItem // chain order, cut applied
	renum  []*albumItem // subset of items whose number changes
	drops  []*albumItem
	needCI bool
}

// buildPlan sorts the album's items into chain order and reports nil when
// the stored numbering already matches (healthy album, no writes).
func buildPlan(col *mrc721nft.Collection, items []*albumItem) *renumberPlan {
	sorted := make([]*albumItem, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return chainBefore(sorted[i].item, sorted[j].item) })

	// Supply cut on the corrected order (the drain applied it on the wrong one).
	kept := sorted
	if col.TotalSupply != -1 && col.TotalSupply < int64(len(sorted)) {
		kept = sorted[:col.TotalSupply]
	}

	plan := &renumberPlan{items: kept}
	for want, ai := range kept {
		if ai.item.Number != int64(want)+1 {
			plan.renum = append(plan.renum, ai)
		}
		if ai.item.Number != int64(want)+1 || ai.seq != int64(want)+1 {
			plan.needCI = true
		}
	}
	if col.TotalSupply != -1 && col.TotalSupply < int64(len(sorted)) {
		plan.drops = sorted[col.TotalSupply:]
	}
	if len(plan.renum) == 0 && len(plan.drops) == 0 {
		return nil
	}
	return plan
}

func printPlan(col *mrc721nft.Collection, plan *renumberPlan) {
	fmt.Printf("album %s (%s / %s)\n", col.PinId, col.CollectionName, col.CreateAddress)
	for _, ai := range plan.renum {
		fmt.Printf("  renumber %s: #%d -> #%d\n", ai.item.ItemPinId, ai.item.Number, numberIn(plan.items, ai))
	}
	for _, ai := range plan.drops {
		fmt.Printf("  drop     %s: #%d (beyond totalSupply %d on the corrected order)\n", ai.item.ItemPinId, ai.item.Number, col.TotalSupply)
	}
}

func numberIn(items []*albumItem, target *albumItem) int64 {
	for i, ai := range items {
		if ai == target {
			return int64(i) + 1
		}
	}
	return 0
}

func applyPlan(agg *mrc721nft.Aggregator, col *mrc721nft.Collection, plan *renumberPlan) error {
	for _, ai := range plan.drops {
		if err := agg.DropItem(ai.item); err != nil {
			return fmt.Errorf("drop item %s: %w", ai.item.ItemPinId, err)
		}
	}
	if plan.needCI {
		entries := make([]mrc721nft.CollectionItemEntry, 0, len(plan.items))
		for i, ai := range plan.items {
			entries = append(entries, mrc721nft.CollectionItemEntry{Seq: int64(i) + 1, ItemPinId: ai.item.ItemPinId})
		}
		if err := agg.RekeyCollectionItems(col.PinId, entries); err != nil {
			return fmt.Errorf("re-key album order for %s: %w", col.PinId, err)
		}
	}
	for _, ai := range plan.items {
		number := numberIn(plan.items, ai)
		if ai.item.Number == number {
			continue
		}
		ai.item.Number = number
		if err := agg.SaveItem(ai.item); err != nil {
			return fmt.Errorf("save item %s: %w", ai.item.ItemPinId, err)
		}
	}
	col.TotalNum = int64(len(plan.items))
	return agg.SaveCollection(col)
}

// chainBefore orders two items the way the chain committed their mint pins:
// block height, then tx index within the block, then the pin output index;
// the pin id breaks still-undecidable pairs deterministically.
func chainBefore(x, y *mrc721nft.Item) bool {
	if x.GenesisHeight != y.GenesisHeight {
		return x.GenesisHeight < y.GenesisHeight
	}
	if x.TxIndex != y.TxIndex {
		return x.TxIndex < y.TxIndex
	}
	xOut, yOut := pinOutputIndex(x.ItemPinId), pinOutputIndex(y.ItemPinId)
	if xOut != yOut {
		return xOut < yOut
	}
	return x.ItemPinId < y.ItemPinId
}

func pinOutputIndex(pinId string) int64 {
	idx := strings.LastIndex(pinId, "i")
	if idx < 0 || idx == len(pinId)-1 {
		return 0
	}
	v, err := strconv.ParseInt(pinId[idx+1:], 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// txIndexHydrator fills Item.TxIndex for items minted before the field was
// recorded, from the block's parsed pins (one CatchPins call per height,
// cached). Pin ids are ledger-hash based, exactly what CatchPins produces.
type txIndexHydrator struct {
	indexer *mvc.Indexer
	cache   map[int64]map[string]int64 // height → pinId → txIndex
}

func newTxIndexHydrator(indexer *mvc.Indexer) *txIndexHydrator {
	return &txIndexHydrator{indexer: indexer, cache: make(map[int64]map[string]int64)}
}

func (h *txIndexHydrator) fill(items []*albumItem) error {
	for _, ai := range items {
		if ai.item.TxIndex != 0 || ai.item.GenesisHeight <= 0 {
			continue
		}
		byPinId, err := h.blockPinIndexes(ai.item.GenesisHeight)
		if err != nil {
			log.Printf("WARNING: hydrate tx index for %s from block %d: %v", ai.item.ItemPinId, ai.item.GenesisHeight, err)
			continue
		}
		if txIndex, ok := byPinId[ai.item.ItemPinId]; ok {
			ai.item.TxIndex = txIndex
		}
	}
	return nil
}

func (h *txIndexHydrator) blockPinIndexes(height int64) (map[string]int64, error) {
	if byPinId, ok := h.cache[height]; ok {
		return byPinId, nil
	}
	pins, _, err := h.indexer.CatchPins(height)
	if err != nil {
		return nil, err
	}
	byPinId := make(map[string]int64, len(pins))
	for _, pin := range pins {
		if pin != nil {
			byPinId[pin.Id] = pin.TxIndex
		}
	}
	h.cache[height] = byPinId
	return byPinId, nil
}
