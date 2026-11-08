package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// The renumber tool's plan/apply logic, exercised against a seeded defect:
// an album whose staged mints drained in pinId key order (the R-F-10
// bug shape) and one where the corrected order also moves the supply cut.

func newTestAggregator(t *testing.T) *mrc721nft.Aggregator {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	a := &mrc721nft.Aggregator{}
	if err := a.Init(store, nil); err != nil {
		t.Fatalf("init aggregator: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return a
}

// creator is a valid mainnet base58 address (same fixture as the aggregate
// package tests).
const creator = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"

func hexPinId(prefix byte, outIdx int64) string {
	return fmt.Sprintf("%064ci%d", prefix, outIdx)
}

func hexTx(pinId string) string { return pinId[:64] }

// seedDefectAlbum writes the wrong state directly: items committed in pinId
// lexicographic order (m-a #1, m-b #2, m-c #3) while the chain order is
// m-b (earlier block), m-a, m-c. Uses the admin surface, no pin ingest —
// the current binary can no longer produce this shape on its own.
func seedDefectAlbum(t *testing.T, a *mrc721nft.Aggregator, name string, totalSupply int64, items []string, outIdxes []int64, heights []int64, txIndexes []int64) *mrc721nft.Collection {
	t.Helper()
	col := &mrc721nft.Collection{
		CollectionName: name,
		PinId:          name + "-cid",
		CreateAddress:  creator,
		TotalSupply:    totalSupply,
		TotalNum:       int64(len(items)),
	}
	if err := a.SaveCollection(col); err != nil {
		t.Fatalf("seed collection: %v", err)
	}
	for i, logical := range items {
		pinId := hexPinId('a', outIdxes[i])
		item := &mrc721nft.Item{
			ItemPinId:       pinId,
			CollectionName:  name,
			CollectionPinId: col.PinId,
			Number:          int64(i + 1), // wrong: pinId key order
			OwnerAddress:    creator,
			GenesisHeight:   heights[i],
			TxIndex:         txIndexes[i],
		}
		if err := a.SaveItem(item); err != nil {
			t.Fatalf("seed item: %v", err)
		}
		if err := a.RekeyCollectionItems(col.PinId, nil); err != nil {
			t.Fatalf("clear order: %v", err)
		}
		_ = logical
	}
	// ci: order as the buggy drain wrote it: item i at seq i+1.
	entries := make([]mrc721nft.CollectionItemEntry, 0, len(items))
	for i := range items {
		entries = append(entries, mrc721nft.CollectionItemEntry{Seq: int64(i + 1), ItemPinId: hexPinId('a', outIdxes[i])})
	}
	if err := a.RekeyCollectionItems(col.PinId, entries); err != nil {
		t.Fatalf("seed order: %v", err)
	}
	return col
}

func TestRenumberRepairsPinIdOrderDefect(t *testing.T) {
	a := newTestAggregator(t)
	// Chain order: m-b (#199) → m-a (200, tx1, out1) → m-c (200, tx2, out3).
	// pinId order (outIdx 1,2,3) is m-a, m-b, m-c — the defect.
	col := seedDefectAlbum(t, a, "Defect", -1,
		[]string{"m-a", "m-b", "m-c"},
		[]int64{1, 2, 3},
		[]int64{200, 199, 200},
		[]int64{1, 9, 2},
	)

	items, err := albumItems(a, col.PinId)
	if err != nil {
		t.Fatalf("albumItems: %v", err)
	}
	plan := buildPlan(col, items)
	if plan == nil {
		t.Fatalf("defect not detected")
	}
	if len(plan.renum) != 2 || len(plan.drops) != 0 {
		t.Fatalf("plan wrong: renum=%d drops=%d", len(plan.renum), len(plan.drops))
	}
	if err := applyPlan(a, col, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// Corrected numbering follows chain order.
	wantNumbers := map[string]int64{"m-b": 1, "m-a": 2, "m-c": 3}
	for logical, want := range wantNumbers {
		pinId := hexPinId('a', map[string]int64{"m-a": 1, "m-b": 2, "m-c": 3}[logical])
		item, err := a.GetItem(pinId)
		if err != nil || item == nil {
			t.Fatalf("item %s missing: %v", pinId, err)
		}
		if item.Number != want {
			t.Fatalf("item %s should be #%d, got #%d", logical, want, item.Number)
		}
	}
	// ci: order corrected to chain order.
	var order []string
	if err := a.IterateCollectionItems(col.PinId, func(seq int64, itemPinId string) bool {
		order = append(order, fmt.Sprintf("%d:%s", seq, itemPinId))
		return true
	}); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(order) != 3 || !strings.HasPrefix(order[0], "1:") || !strings.Contains(order[0], hexPinId('a', 2)) {
		t.Fatalf("corrected order wrong: %v", order)
	}
	if col.TotalNum != 3 {
		t.Fatalf("totalnum wrong: %d", col.TotalNum)
	}

	// Second run: healthy, no plan.
	items2, _ := albumItems(a, col.PinId)
	if plan2 := buildPlan(col, items2); plan2 != nil {
		t.Fatalf("repaired album must be a no-op on re-run: %+v", plan2)
	}
}

func TestRenumberSupplyCutMovesWithOrder(t *testing.T) {
	a := newTestAggregator(t)
	// totalSupply=2. Chain order: x-earliest (block 199) should keep its
	// slot; the lexicographic drain kept x-late and x-mid instead.
	col := seedDefectAlbum(t, a, "Cut", 2,
		[]string{"x-late", "x-mid"}, // committed: the wrong two
		[]int64{3, 2},
		[]int64{200, 200},
		[]int64{2, 1},
	)
	// The chain-first item exists beyond the cut in the stored state? No —
	// the buggy drain cut it entirely. Emulate: x-early never committed, so
	// repair can only renumber the kept two; totalnum stays at the cut.
	early := &mrc721nft.Item{
		ItemPinId:       hexPinId('a', 1),
		CollectionName:  "Cut",
		CollectionPinId: col.PinId,
		Number:          3, // was committed despite being chain-latest
		OwnerAddress:    creator,
		GenesisHeight:   199,
		TxIndex:         5,
	}
	if err := a.SaveItem(early); err != nil {
		t.Fatalf("seed early: %v", err)
	}
	entries := []mrc721nft.CollectionItemEntry{
		{Seq: 1, ItemPinId: hexPinId('a', 3)},
		{Seq: 2, ItemPinId: hexPinId('a', 2)},
		{Seq: 3, ItemPinId: hexPinId('a', 1)},
	}
	if err := a.RekeyCollectionItems(col.PinId, entries); err != nil {
		t.Fatalf("reseed order: %v", err)
	}
	col.TotalNum = 3

	items, err := albumItems(a, col.PinId)
	if err != nil {
		t.Fatalf("albumItems: %v", err)
	}
	plan := buildPlan(col, items)
	if plan == nil {
		t.Fatalf("defect not detected")
	}
	// Corrected order: early(199) #1, mid(tx1,out2) #2; late (tx2,out3) cut.
	if len(plan.drops) != 1 || plan.drops[0].item.ItemPinId != hexPinId('a', 3) {
		t.Fatalf("wrong drop: %+v", plan.drops)
	}
	if err := applyPlan(a, col, plan); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if item, _ := a.GetItem(hexPinId('a', 3)); item != nil {
		t.Fatalf("late item must be dropped")
	}
	if item, _ := a.GetItem(hexPinId('a', 1)); item == nil || item.Number != 1 {
		t.Fatalf("early item must be #1: %+v", item)
	}
	if col.TotalNum != 2 {
		t.Fatalf("totalnum must equal the cut: %d", col.TotalNum)
	}
}

func TestHealthyAlbumNeedsNoPlan(t *testing.T) {
	a := newTestAggregator(t)
	// Items committed in true chain order — the tool must leave them alone.
	col := seedDefectAlbum(t, a, "Healthy", -1,
		[]string{"h-1", "h-2", "h-3"},
		[]int64{7, 8, 9},
		[]int64{200, 200, 200},
		[]int64{1, 2, 3},
	)
	items, _ := albumItems(a, col.PinId)
	if plan := buildPlan(col, items); plan != nil {
		t.Fatalf("healthy album must produce no plan: %+v", plan)
	}
}
