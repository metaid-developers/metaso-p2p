package mrc721nft

import (
	"context"
	"errors"
	"strings"
)

// ReplayPins repairs individual entities by id, the MetaSo counterpart of
// man-p2p's GET /api/mrc721/admin/replay-pins. man-p2p re-runs local pin
// records through its aggregation hook; MetaSo keeps no raw-pin store, so
// the repair re-applies the entity's upstream state through the same
// idempotent upsert paths the bootstrap uses:
//
//   - an item pin id is re-hydrated from the upstream item/info view (its
//     collection record is ensured first),
//   - a collection_desc pin id is re-hydrated from the collection/info view,
//   - anything else reports "pin not found".
//
// The freshness rules of the bootstrap apply unchanged, so replaying never
// rewinds live state.
type ReplayResult struct {
	PinId   string `json:"pinId"`
	Chain   string `json:"chain,omitempty"`
	Applied bool   `json:"applied"`
	Reason  string `json:"reason,omitempty"`
}

// ReplayPins repairs each id in order; the per-id result mirrors man-p2p's
// man.Mrc721ReplayResult wire shape.
func (a *Aggregator) ReplayPins(ctx context.Context, client *UpstreamClient, ids []string) []ReplayResult {
	results := make([]ReplayResult, 0, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		results = append(results, a.replayOne(ctx, client, id))
	}
	return results
}

func (a *Aggregator) replayOne(ctx context.Context, client *UpstreamClient, pinId string) ReplayResult {
	if item, err := client.ItemInfo(ctx, pinId); err == nil && item != nil && item.ItemPinId == pinId {
		col, err := a.ensureCollection(ctx, client, item.CollectionPinId)
		if err != nil {
			return ReplayResult{PinId: pinId, Applied: false, Reason: "upstream error"}
		}
		a.mu.Lock()
		err = a.applyUpstreamItem(col, item, &BackfillStats{})
		a.mu.Unlock()
		if err != nil {
			return ReplayResult{PinId: pinId, Applied: false, Reason: "apply failed"}
		}
		return ReplayResult{PinId: pinId, Chain: item.GenesisChainName, Applied: true}
	} else if err != nil && !errors.Is(err, ErrUpstreamNotFound) {
		return ReplayResult{PinId: pinId, Applied: false, Reason: "upstream error"}
	}

	if row, err := client.CollectionInfo(ctx, pinId); err == nil && row != nil && row.PinId == pinId {
		a.mu.Lock()
		err := a.db.SaveCollection(collectionFromUpstream(row))
		a.mu.Unlock()
		if err != nil {
			return ReplayResult{PinId: pinId, Applied: false, Reason: "apply failed"}
		}
		return ReplayResult{PinId: pinId, Chain: row.GenesisChainName, Applied: true}
	} else if err != nil && !errors.Is(err, ErrUpstreamNotFound) {
		return ReplayResult{PinId: pinId, Applied: false, Reason: "upstream error"}
	}

	return ReplayResult{PinId: pinId, Applied: false, Reason: "pin not found"}
}

// ensureCollection returns the locally stored collection for a collectionid,
// hydrating it from the upstream when missing.
func (a *Aggregator) ensureCollection(ctx context.Context, client *UpstreamClient, collectionPinId string) (*Collection, error) {
	if col, _ := a.db.GetCollectionByPinId(collectionPinId); col != nil {
		return col, nil
	}
	row, err := client.CollectionInfo(ctx, collectionPinId)
	if err != nil {
		return nil, err
	}
	col := collectionFromUpstream(row)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.db.SaveCollection(col); err != nil {
		return nil, err
	}
	return col, nil
}
