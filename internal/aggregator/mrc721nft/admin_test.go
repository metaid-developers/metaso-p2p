package mrc721nft

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Tests for the offline repair tools' surface (admin.go): re-keying an
// album's mint order, dropping a supply-cut item, and the replay entry
// point's idempotency.

func TestRekeyCollectionItemsAndDropItem(t *testing.T) {
	a := newTestAggregator(t)
	runPins(t, a,
		testPin("c1", "/nft/mrc721/RKAlbum/collection_desc", "create", []byte(`{"name":"RK"}`), creator),
		testPin("r1", "/nft/mrc721/RKAlbum/a", "create", []byte(`"1"`), creator),
		testPin("r2", "/nft/mrc721/RKAlbum/b", "create", []byte(`"2"`), creator),
		testPin("r3", "/nft/mrc721/RKAlbum/c", "create", []byte(`"3"`), creator),
	)

	// Re-key the album order reversed: r3, r2, r1.
	err := a.RekeyCollectionItems("c1", []CollectionItemEntry{
		{Seq: 1, ItemPinId: "r3"},
		{Seq: 2, ItemPinId: "r2"},
		{Seq: 3, ItemPinId: "r1"},
	})
	if err != nil {
		t.Fatalf("rekey: %v", err)
	}
	var order []string
	if err := a.IterateCollectionItems("c1", func(seq int64, itemPinId string) bool {
		order = append(order, itemPinId)
		return true
	}); err != nil {
		t.Fatalf("iterate: %v", err)
	}
	if len(order) != 3 || order[0] != "r3" || order[2] != "r1" {
		t.Fatalf("rekeyed order wrong: %v", order)
	}

	// DropItem removes the record and its holder entries.
	item, _ := a.GetItem("r1")
	if item == nil {
		t.Fatalf("r1 missing before drop")
	}
	if err := a.DropItem(item); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if got, _ := a.GetItem("r1"); got != nil {
		t.Fatalf("r1 still present after drop")
	}
}

func TestIngestConfirmedPinIdempotent(t *testing.T) {
	a := newTestAggregator(t)
	runPins(t, a,
		testPin("c1", "/nft/mrc721/RPAlbum/collection_desc", "create", []byte(`{"name":"RP"}`), creator),
		testPin("p1", "/nft/mrc721/RPAlbum/x", "create", []byte(`"p1"`), creator),
	)

	// Re-ingesting the same confirmed pin never double-counts.
	pin := testPin("p1", "/nft/mrc721/RPAlbum/x", "create", []byte(`"p1"`), creator)
	for i := 0; i < 3; i++ {
		if err := a.IngestConfirmedPin(pin); err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
	}
	col := mustCol(t, a, "RPAlbum", creator)
	if col.TotalNum != 1 {
		t.Fatalf("replay must stay idempotent: totalnum %d", col.TotalNum)
	}

	// A previously-dropped item_desc replays through the same entry point:
	// first-write-wins fills the empty display fields.
	desc := testPin("d1", "/nft/mrc721/RPAlbum/item_desc", "create",
		[]byte(`{"items":[{"pinid":"p1","name":"Replayed Name","metadata":{"k":"v"}}]}`), creator)
	if _, err := a.HandleBlockPin(desc); err != nil {
		t.Fatalf("desc: %v", err)
	}
	if err := a.IngestConfirmedPin(desc); err != nil {
		t.Fatalf("desc replay: %v", err)
	}
	item, _ := a.GetItem("p1")
	if item == nil || item.Name != "Replayed Name" {
		t.Fatalf("replayed desc not applied: %+v", item)
	}
	if item.Metadata != `{"k":"v"}` {
		t.Fatalf("replayed metadata wrong: %q", item.Metadata)
	}
}

func TestListCollectionsAdminSurface(t *testing.T) {
	a := newTestAggregator(t)
	runPins(t, a,
		testPin("k1", "/nft/mrc721/AdminA/collection_desc", "create", []byte(`{"name":"A"}`), creator),
		testPin("k2", "/nft/mrc721/AdminB/collection_desc", "create", []byte(`{"name":"B"}`), other),
	)
	cols, total, err := a.ListCollections()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if total != 2 || len(cols) != 2 {
		t.Fatalf("list wrong: total=%d len=%d", total, len(cols))
	}
}

// Compile-time shape check of the replay input type.
var _ = aggregator.PinInscription{}
