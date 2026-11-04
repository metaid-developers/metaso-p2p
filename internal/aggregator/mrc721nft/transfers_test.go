package mrc721nft

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// TransferConsumer end-to-end tests through the public interface path:
// TrackedOutputs (engine idMap source) → HandleBlockTransfers → item
// ownership/history/holder-index updates + cache invalidation.

func TestTrackedOutputsReflectsMintedItems(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/TrkAlbum/collection_desc", "create", []byte(`{"name":"T"}`), creator),
		testPin("n1", "/nft/mrc721/TrkAlbum/a", "create", []byte(`"nft"`), creator),
		testPin("n2", "/nft/mrc721/TrkAlbum/b", "create", []byte(`"nft2"`), other),
	)

	tracked := a.TrackedOutputs()
	if len(tracked) != 2 {
		t.Fatalf("tracked outputs wrong: %+v", tracked)
	}
	if out := tracked["n1:0"]; out.Owner != creator || out.Ref != "n1" {
		t.Fatalf("n1 tracking wrong: %+v", out)
	}
}

func TestHandleBlockTransfersMovesOwnership(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/CsmAlbum/collection_desc", "create", []byte(`{"name":"C"}`), creator),
		testPin("n1", "/nft/mrc721/CsmAlbum/a", "create", []byte(`"nft"`), creator),
	)

	// The engine detects a spend of the item's genesis outpoint.
	notices := map[string]aggregator.TransferNotice{
		"n1:0": {
			FromAddress: creator,
			ToAddress:   other,
			Output:      "spendtx:1",
			OutputValue: 546,
			Location:    "spendtx:1:0",
		},
	}
	a.HandleBlockTransfers("mvc", 300, notices)

	item, _ := a.db.GetItem("n1")
	if item == nil || item.OwnerAddress != other || item.TransferCount != 1 ||
		item.Output != "spendtx:1" || item.OutputValue != 546 {
		t.Fatalf("ownership not moved via the consumer interface: %+v", item)
	}
	if item.OwnerMetaId != globalMetaIdByAddress(other) {
		t.Fatalf("owner metaId wrong: %q", item.OwnerMetaId)
	}
	// Provenance history written with the notice facts.
	histories := 0
	a.db.IterateItemTransfers("n1", func(tr *ItemTransfer) bool {
		histories++
		if tr.TxId != "spendtx:1:0" || tr.ToAddress != other || tr.Height != 300 {
			t.Fatalf("history row wrong: %+v", tr)
		}
		return true
	})
	if histories != 1 {
		t.Fatalf("history rows wrong: %d", histories)
	}
	// Holder index re-keyed.
	if n := countOwnerItems(t, a, other, "CsmAlbum"); n != 1 {
		t.Fatalf("new holder index wrong: %d", n)
	}
	if n := countOwnerItems(t, a, creator, "CsmAlbum"); n != 0 {
		t.Fatalf("old holder index not cleared: %d", n)
	}
	// The tracked cache must reflect the new outpoint for the next block.
	tracked := a.TrackedOutputs()
	if _, ok := tracked["n1:0"]; ok {
		t.Fatal("spent outpoint must leave the tracked set")
	}
	if out, ok := tracked["spendtx:1"]; !ok || out.Owner != other || out.Ref != "n1" {
		t.Fatalf("new outpoint must be tracked: %+v", tracked)
	}
}

func TestHandleBlockTransfersIgnoresForeignOutputs(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/ForAlbum/collection_desc", "create", []byte(`{"name":"F"}`), creator),
		testPin("n1", "/nft/mrc721/ForAlbum/a", "create", []byte(`"nft"`), creator),
	)

	// A spend of an output this aggregate does not track: no side effects.
	a.HandleBlockTransfers("mvc", 400, map[string]aggregator.TransferNotice{
		"foreign:0": {FromAddress: creator, ToAddress: other, Output: "x:1", Location: "x:1:0"},
	})

	item, _ := a.db.GetItem("n1")
	if item == nil || item.OwnerAddress != creator || item.TransferCount != 0 {
		t.Fatalf("foreign spend must not touch the aggregate: %+v", item)
	}
}

func TestHandleBlockTransfersMeltdownBurns(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/MltAlbum/collection_desc", "create", []byte(`{"name":"M"}`), creator),
		testPin("n1", "/nft/mrc721/MltAlbum/a", "create", []byte(`"nft"`), creator),
	)

	a.HandleBlockTransfers("mvc", 500, map[string]aggregator.TransferNotice{
		"n1:0": {
			FromAddress: creator,
			ToAddress:   "",
			Output:      "melttx:0",
			Location:    "melttx:0:0",
			Meltdown:    true,
		},
	})

	item, _ := a.db.GetItem("n1")
	if item == nil || !item.Burned {
		t.Fatalf("meltdown notice must burn the item: %+v", item)
	}
	if n := countOwnerItems(t, a, creator, "MltAlbum"); n != 0 {
		t.Fatalf("burned item must leave holder views: %d", n)
	}
}

func TestHandleMempoolSpendsPendingChannel(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/PndAlbum/collection_desc", "create", []byte(`{"name":"P"}`), creator),
		testPin("n1", "/nft/mrc721/PndAlbum/a", "create", []byte(`"nft"`), creator),
	)

	// One full round: the item's genesis outpoint is spent in the mempool.
	a.HandleMempoolSpends("mvc", map[string]aggregator.TransferNotice{
		"n1:0": {FromAddress: creator, ToAddress: other, Output: "spendtx:1", Location: "spendtx:1:0"},
	})

	pt, _ := a.db.GetPendingTransfer("n1")
	if pt == nil {
		t.Fatal("pending transfer not recorded")
	}
	if pt.SpentOutpoint != "n1:0" || pt.TxId != "spendtx" || pt.ToAddress != other || pt.ChainName != "mvc" {
		t.Fatalf("pending fields wrong: %+v", pt)
	}
	if pt.FirstSeenAt == 0 {
		t.Fatal("firstSeenAt must be stamped")
	}
	firstSeen := pt.FirstSeenAt

	// Re-scan of the same spend tx: firstSeenAt preserved.
	a.HandleMempoolSpends("mvc", map[string]aggregator.TransferNotice{
		"n1:0": {FromAddress: creator, ToAddress: other, Output: "spendtx:1", Location: "spendtx:1:0"},
	})
	pt, _ = a.db.GetPendingTransfer("n1")
	if pt == nil || pt.FirstSeenAt != firstSeen {
		t.Fatalf("same-tx re-scan must preserve firstSeenAt: %+v", pt)
	}

	// RBF replacement (new txid): latest wins, firstSeenAt resets. Backdate
	// the stored record first so the reset is observable even when the
	// rounds land in the same wall-clock second.
	pt, _ = a.db.GetPendingTransfer("n1")
	pt.FirstSeenAt = firstSeen - 1000
	if err := a.db.SavePendingTransfer(pt); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	a.HandleMempoolSpends("mvc", map[string]aggregator.TransferNotice{
		"n1:0": {FromAddress: creator, ToAddress: creator, Output: "rbftx:1", Location: "rbftx:1:0"},
	})
	pt, _ = a.db.GetPendingTransfer("n1")
	if pt == nil || pt.TxId != "rbftx" || pt.FirstSeenAt == firstSeen-1000 {
		t.Fatalf("RBF replacement must overwrite with a fresh firstSeenAt: %+v", pt)
	}

	// Confirmation settles the pending through the block hook.
	transferMap, pinIdByOutput := transferFor("n1", creator, creator, "rbftx:1", 546, false)
	a.applyTransferHook("mvc", 600, transferMap, pinIdByOutput, 1700000700)
	if pt, _ := a.db.GetPendingTransfer("n1"); pt != nil {
		t.Fatalf("confirmed spend must settle the pending: %+v", pt)
	}
}

func TestHandleMempoolSpendsReconcileEvictsUnseen(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/RcnAlbum/collection_desc", "create", []byte(`{"name":"R"}`), creator),
		testPin("n1", "/nft/mrc721/RcnAlbum/a", "create", []byte(`"nft"`), creator),
		testPin("n2", "/nft/mrc721/RcnAlbum/b", "create", []byte(`"nft2"`), other),
	)

	// Two pendings on different items.
	a.HandleMempoolSpends("mvc", map[string]aggregator.TransferNotice{
		"n1:0": {FromAddress: creator, ToAddress: other, Output: "tx1:1", Location: "tx1:1:0"},
		"n2:0": {FromAddress: other, ToAddress: creator, Output: "tx2:1", Location: "tx2:1:0"},
	})

	// Next round observes only n1's spend: n2's pending is evicted (the tx
	// was dropped or RBF-replaced).
	a.HandleMempoolSpends("mvc", map[string]aggregator.TransferNotice{
		"n1:0": {FromAddress: creator, ToAddress: other, Output: "tx1:1", Location: "tx1:1:0"},
	})
	if pt, _ := a.db.GetPendingTransfer("n1"); pt == nil {
		t.Fatal("observed pending must survive")
	}
	if pt, _ := a.db.GetPendingTransfer("n2"); pt != nil {
		t.Fatalf("unobserved pending must be evicted: %+v", pt)
	}

	// An empty round (mempool drained) evicts everything for the chain.
	a.HandleMempoolSpends("mvc", map[string]aggregator.TransferNotice{})
	if pt, _ := a.db.GetPendingTransfer("n1"); pt != nil {
		t.Fatalf("empty round must evict chain pendings: %+v", pt)
	}
}

func TestHandleMempoolSpendsChainScoped(t *testing.T) {
	a := newTestAggregator(t)

	// A pending on another chain is untouched by an mvc round that does not
	// observe it.
	if err := a.db.SavePendingTransfer(&PendingTransfer{
		ItemPinId: "foreign-item", ChainName: "btc", SpentOutpoint: "fx:0",
		FromAddress: "btc1q", ToAddress: "btc2q", TxId: "btctx", FirstSeenAt: 1,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	a.HandleMempoolSpends("mvc", map[string]aggregator.TransferNotice{})
	if pt, _ := a.db.GetPendingTransfer("foreign-item"); pt == nil {
		t.Fatal("other-chain pendings must survive an mvc reconcile round")
	}
}
