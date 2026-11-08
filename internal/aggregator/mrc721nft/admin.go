package mrc721nft

import (
	"fmt"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Public surface for the offline repair tools (cmd/metaso-p2p-mrc721-renumber,
// cmd/metaso-p2p-mrc721-replay). The daemon's API layer never calls these —
// the tools run against the Pebble namespace with the service stopped, so the
// methods skip the ingest mutex entirely.

// ListCollections pages the album main table (key order).
func (a *Aggregator) ListCollections() ([]*Collection, int64, error) {
	return a.db.ListCollections(0, -1)
}

// GetItem fetches one indexed item (nil when unknown).
func (a *Aggregator) GetItem(itemPinId string) (*Item, error) {
	return a.db.GetItem(itemPinId)
}

// SaveItem writes one item record (renumbered Number included).
func (a *Aggregator) SaveItem(item *Item) error {
	return a.db.SaveItem(item)
}

// SaveCollection writes one album record (repaired TotalNum included).
func (a *Aggregator) SaveCollection(col *Collection) error {
	return a.db.SaveCollection(col)
}

// IterateCollectionItems walks an album's mint order, yielding (seq,
// itemPinId).
func (a *Aggregator) IterateCollectionItems(collectionPinId string, fn func(seq int64, itemPinId string) bool) error {
	return a.db.IterateCollectionItems(collectionPinId, fn)
}

// DropItem removes an item that falls outside the corrected supply cut: the
// main record and its holder-index entries (the in-album order entry is
// re-keyed wholesale by RekeyCollectionItems).
func (a *Aggregator) DropItem(item *Item) error {
	if item.OwnerAddress != "" {
		if err := a.db.DeleteOwnerItem(item.OwnerAddress, item.CollectionPinId, item.ItemPinId); err != nil {
			return fmt.Errorf("delete holder index: %w", err)
		}
	}
	return a.store.Delete(Namespace, []byte(itemKey(item.ItemPinId)))
}

// CollectionItemEntry is one (seq, itemPinId) pair of a corrected album
// order, handed to RekeyCollectionItems.
type CollectionItemEntry struct {
	Seq       int64
	ItemPinId string
}

// RekeyCollectionItems replaces an album's whole ci: order with the given
// entries: the old keys are removed first, the corrected order is appended
// under its sequence numbers.
func (a *Aggregator) RekeyCollectionItems(collectionPinId string, entries []CollectionItemEntry) error {
	type oldKey struct {
		seq       int64
		itemPinId string
	}
	var olds []oldKey
	if err := a.db.IterateCollectionItems(collectionPinId, func(seq int64, itemPinId string) bool {
		olds = append(olds, oldKey{seq, itemPinId})
		return true
	}); err != nil {
		return err
	}
	for _, o := range olds {
		if err := a.store.Delete(Namespace, []byte(collItemKey(collectionPinId, o.seq, o.itemPinId))); err != nil {
			return err
		}
	}
	for _, e := range entries {
		if err := a.db.AppendCollectionItem(collectionPinId, e.Seq, e.ItemPinId); err != nil {
			return err
		}
	}
	return nil
}

// IngestConfirmedPin folds one re-parsed confirmed pin through the same
// block-ingest path the engine uses (the replay tool's entry point).
// Ingest idempotency (first-write-wins, per-pinId guards) makes re-application safe.
func (a *Aggregator) IngestConfirmedPin(pin *aggregator.PinInscription) error {
	_, err := a.HandleBlockPin(pin)
	return err
}
