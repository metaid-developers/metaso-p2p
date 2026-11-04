package mrc721nft

import (
	"log"
	"strings"
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

// HandleMempoolSpends consumes one full mempool-scan round: spends of
// tracked outpoints land as advisory pending-transfer records (latest-wins
// per item, firstSeenAt preserved when the same spend tx is re-observed),
// then the round reconciles away pendings this round no longer observed for
// the chain — either the tx confirmed (the block hook settles the pending)
// or it was dropped/RBF-replaced. Meltdown is not judged here; the
// confirmed hook is authoritative.
func (a *Aggregator) HandleMempoolSpends(chainName string, spends map[string]aggregator.TransferNotice) {
	a.mu.Lock()
	defer a.mu.Unlock()
	tracked := a.trackedOutputsLocked()
	now := time.Now().Unix()
	seen := make(map[string]string) // itemPinId → spend txid observed this round
	for outpoint, notice := range spends {
		ref, ok := tracked[outpoint]
		if !ok {
			continue
		}
		// Spend txid = ledger-convention first segment of the location
		// pointer (same source as the confirmed history's txId).
		txId := notice.Location
		if idx := strings.Index(txId, ":"); idx >= 0 {
			txId = txId[:idx]
		}
		if txId == "" {
			continue
		}
		pt := &PendingTransfer{
			ItemPinId:     ref.Ref,
			ChainName:     chainName,
			SpentOutpoint: outpoint,
			FromAddress:   notice.FromAddress,
			ToAddress:     notice.ToAddress,
			TxId:          txId,
			FirstSeenAt:   now,
		}
		if existing, _ := a.db.GetPendingTransfer(ref.Ref); existing != nil && existing.TxId == txId {
			// Re-scan of the same spend tx: keep the first-seen time.
			// An RBF replacement is a new txid and resets it.
			pt.FirstSeenAt = existing.FirstSeenAt
		}
		if err := a.db.SavePendingTransfer(pt); err != nil {
			log.Printf("WARNING: mrc721nft save pending transfer %s: %v", ref.Ref, err)
			continue
		}
		seen[ref.Ref] = txId
	}
	a.mempoolReconcileLocked(chainName, seen)
}

// mempoolReconcileLocked deletes pendings for this chain that the round no
// longer observed. Never runs on a failed fetch — the engine skips the
// chain entirely in that case, so live pendings cannot be evicted by a
// partial observation.
func (a *Aggregator) mempoolReconcileLocked(chainName string, seen map[string]string) {
	var stale []*PendingTransfer
	if err := a.db.IteratePendingTransfers(func(pt *PendingTransfer) bool {
		if pt.ChainName == chainName && seen[pt.ItemPinId] != pt.TxId {
			stale = append(stale, pt)
		}
		return true
	}); err != nil {
		log.Printf("WARNING: mrc721nft pending transfer reconcile iterate: %v", err)
		return
	}
	for _, pt := range stale {
		if err := a.db.DeletePendingTransfer(pt.ItemPinId); err != nil {
			log.Printf("WARNING: mrc721nft pending transfer reconcile delete %s: %v", pt.ItemPinId, err)
		}
	}
}
