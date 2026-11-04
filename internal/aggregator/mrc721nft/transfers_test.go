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
	if item.OwnerMetaId != metaIdByAddress(other) {
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
