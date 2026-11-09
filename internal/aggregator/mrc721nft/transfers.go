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
// mrc721TransferHook flow — plus the zero-conf spend channels (R-F-3/R-F-7):
// incremental detection on every mempool poll, full-mempool rounds for the
// pending reconcile.

// markTrackedDirty invalidates the tracked-output cache; called after every
// write that can change an item's outpoint or owner.
func (a *Aggregator) markTrackedDirty() {
	a.trackedDirty = true
}

// TrackedOutputs returns the current outpoint → (owner, itemPinId) map for
// the engine's transfer detection, rebuilt lazily after writes. With the
// trade gate on it also covers overlay outputs (R-F-3.2): freshly minted
// and freshly delivered items exist only in the overlay, and a resale or
// re-transfer before the first confirmation must stay visible at any depth.
func (a *Aggregator) TrackedOutputs() map[string]aggregator.TrackedOutput {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.trackedOutputsLocked()
}

func (a *Aggregator) trackedOutputsLocked() map[string]aggregator.TrackedOutput {
	base := a.baseTrackedLocked()
	if !a.zeroConfTrade || (len(a.overlayPins) == 0 && len(a.overlaySpends) == 0) {
		return base
	}
	// Overlay extension: work on a copy (never mutate the lazy cache) — an
	// outpoint spent in the mempool is no longer spendable, every overlay
	// delivery outpoint becomes tracked.
	tracked := make(map[string]aggregator.TrackedOutput, len(base)+len(a.overlayPins)+len(a.overlaySpends))
	for k, v := range base {
		tracked[k] = v
	}
	for outpoint := range a.overlaySpends {
		delete(tracked, outpoint)
	}
	for _, entry := range a.overlayPins {
		if entry.Kind != overlayEffectMint || entry.Mint == nil {
			continue
		}
		if entry.Mint.Output == "" || entry.Mint.OwnerAddress == "" {
			continue
		}
		// A delivery outpoint another mempool tx already spends is no
		// longer spendable (chained unconfirmed spends).
		if _, spentAgain := a.overlaySpends[entry.Mint.Output]; spentAgain {
			continue
		}
		tracked[entry.Mint.Output] = aggregator.TrackedOutput{Owner: entry.Mint.OwnerAddress, Ref: entry.Mint.ItemPinId}
	}
	for _, sp := range a.overlaySpends {
		if sp.Burned || sp.Output == "" || sp.ToAddress == "" {
			continue
		}
		if _, spentAgain := a.overlaySpends[sp.Output]; spentAgain {
			continue
		}
		tracked[sp.Output] = aggregator.TrackedOutput{Owner: sp.ToAddress, Ref: sp.ItemPinId}
	}
	return tracked
}

// BaseTrackedOutputs implements aggregator.BaseTrackedOutputsProvider: the
// confirmed-only tracked set the engine's block spend path detects against
// (the overlay-aware TrackedOutputs would hide an outpoint whose spend is
// in flight in the mempool, and its confirmation block must still settle).
func (a *Aggregator) BaseTrackedOutputs() map[string]aggregator.TrackedOutput {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.baseTrackedLocked()
}

// HandleBlockTransfers converts the engine's transfer notices into
// applyTransferHook input, filtered to the CONFIRMED tracked outputs. The
// confirmed hook is authoritative: a spend first seen in the mempool (its
// outpoint absent from the overlay-aware view) must still settle here when
// it confirms, so this filter deliberately uses the base set. The hook's
// item lookup and (itemPinId, height, txid) idempotency do the rest.
// Confirmation handoff (R-F-8.1): once the confirmed ownership facts
// folded, the spending tx's overlay entries go — same critical section.
// A conflicting mempool spend (different txid that lost the block) stays
// overlay-live until eviction, where the ownership fold skips it: its spent
// outpoint no longer matches the confirmed output.
func (a *Aggregator) HandleBlockTransfers(chainName string, height int64, transfers map[string]aggregator.TransferNotice) {
	a.mu.Lock()
	defer a.mu.Unlock()
	tracked := a.baseTrackedLocked()
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
	for _, info := range transferMap {
		// Location is the "<ledgerTxid>:<vout>:<satOffset>" pointer; the
		// overlay entries are keyed by the bare spending txid.
		txid := info.Location
		if idx := strings.Index(txid, ":"); idx >= 0 {
			txid = txid[:idx]
		}
		a.dropOverlayForTxLocked(txid)
	}
}

// baseTrackedLocked rebuilds the confirmed-only tracked set (the lazy cache).
func (a *Aggregator) baseTrackedLocked() map[string]aggregator.TrackedOutput {
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

// HandleMempoolSpends consumes one full mempool-scan round: spends of
// tracked outpoints land as advisory pending-transfer records (latest-wins
// per item, firstSeenAt preserved when the same spend tx is re-observed),
// then the round reconciles away pendings this round no longer observed for
// the chain — either the tx confirmed (the block hook settles the pending)
// or it was dropped/RBF-replaced. Meltdown is not judged here; the
// confirmed hook is authoritative. With the trade gate on the same notices
// also fold into the zero-conf ownership overlay.
func (a *Aggregator) HandleMempoolSpends(chainName string, spends map[string]aggregator.TransferNotice) {
	a.mu.Lock()
	defer a.mu.Unlock()
	tracked := a.trackedOutputsLocked()
	now := time.Now().Unix()
	seen := a.recordPendingTransfersLocked(chainName, tracked, spends, now)
	if a.zeroConfTrade {
		a.recordOverlaySpendsLocked(chainName, tracked, spends, now)
	}
	a.mempoolReconcileLocked(chainName, seen)
}

// HandleMempoolSpendsIncremental implements aggregator.MempoolOverlayConsumer
// (R-F-7): spend detection on every mempool poll, so an ownership change
// surfaces within one poll instead of waiting for the full-scan cadence.
// No reconcile — an incremental round is a partial observation and must
// never evict anything. With the trade gate off this is a strict no-op
// (today's trading behavior, exactly).
func (a *Aggregator) HandleMempoolSpendsIncremental(chainName string, spends map[string]aggregator.TransferNotice) {
	if !a.zeroConfTrade {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tracked := a.trackedOutputsLocked()
	now := time.Now().Unix()
	a.recordPendingTransfersLocked(chainName, tracked, spends, now)
	a.recordOverlaySpendsLocked(chainName, tracked, spends, now)
}

// recordPendingTransfersLocked writes the advisory pending-transfer records
// for one spend round and returns the itemPinId → spend txid observation map
// the full-round reconcile consumes.
func (a *Aggregator) recordPendingTransfersLocked(chainName string, tracked map[string]aggregator.TrackedOutput, spends map[string]aggregator.TransferNotice, now int64) map[string]string {
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
	return seen
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
