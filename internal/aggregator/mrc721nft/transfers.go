package mrc721nft

import (
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// TransferConsumer implementation: the engine feeds the detected spends of
// this aggregate's tracked outputs (the items' current outpoints) per
// confirmed block — the mirror of man-p2p's handleTransfer →
// mrc721TransferHook flow.

// markTrackedDirty invalidates the tracked-output cache; called after every
// write that can change an item's outpoint or owner.
func (a *Aggregator) markTrackedDirty() {
	a.trackedDirty = true
}

// TrackedOutputs returns the current outpoint → (owner, itemPinId) map for
// the engine's transfer detection, rebuilt lazily after writes.
func (a *Aggregator) TrackedOutputs() map[string]aggregator.TrackedOutput {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.trackedOutputsLocked()
}

func (a *Aggregator) trackedOutputsLocked() map[string]aggregator.TrackedOutput {
	if a.trackedDirty || a.tracked == nil {
		tracked := make(map[string]aggregator.TrackedOutput)
		_ = a.db.IterateItems(func(item *Item) bool {
			if item.Burned || item.Output == "" || item.OwnerAddress == "" {
				return true
			}
			tracked[item.Output] = aggregator.TrackedOutput{Owner: item.OwnerAddress, Ref: item.ItemPinId}
			return true
		})
		a.tracked = tracked
		a.trackedDirty = false
	}
	return a.tracked
}

// HandleBlockTransfers converts the engine's transfer notices into
// applyTransferHook input, filtered to the outputs this aggregate tracks.
// The hook's item lookup and (itemPinId, height, txid) idempotency do the
// rest.
func (a *Aggregator) HandleBlockTransfers(chainName string, height int64, transfers map[string]aggregator.TransferNotice) {
	a.mu.Lock()
	defer a.mu.Unlock()
	tracked := a.trackedOutputsLocked()
	transferMap := make(map[string]*TransferInfo, len(transfers))
	pinIdByOutput := make(map[string]string, len(transfers))
	for output, notice := range transfers {
		ref, ok := tracked[output]
		if !ok {
			continue // not an output this aggregate tracks
		}
		transferMap[output] = &TransferInfo{
			Address:     notice.ToAddress,
			Output:      notice.Output,
			OutputValue: notice.OutputValue,
			Offset:      notice.Offset,
			Location:    notice.Location,
			FromAddress: notice.FromAddress,
			IsMeltdown:  notice.Meltdown,
		}
		pinIdByOutput[output] = ref.Ref
	}
	if len(transferMap) == 0 {
		return
	}
	// man-p2p stamps the history rows with the processing time.
	a.applyTransferHook(chainName, height, transferMap, pinIdByOutput, time.Now().Unix())
}
