package mrc721nft

import (
	"log"
	"sort"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Zero-confirmation overlay (R-F-1, docs/nft-zero-conf-indexer-requirements.md):
// mempool pins and mempool spends of tracked outputs take effect in every
// read view immediately, before any block. Design principles (normative):
//
//   - Overlay, never mutation. The confirmed Pebble aggregates stay exactly
//     as they are; the overlay is in-memory state that is RECOMPUTED — a
//     dropped transaction's effects vanish, revealing the confirmed state
//     beneath. Nothing here is ever persisted, replayed, or backfilled.
//   - Confirmed wins. When a pin or spend confirms, the block path ingests
//     it first and the overlay entries of that txid are dropped in the same
//     critical section (no gap, no duplicate rows).
//   - Eviction. A fully observed mempool round that no longer contains a
//     transaction removes all of its overlay effects (ReconcileMempoolRound).
//     A failed fetch never reconciles — the engine skips the chain.
//   - Gates. zeroConfIssuance governs the pin effects (R-F-2), zeroConfTrade
//     governs ownership and the incremental spend channel (R-F-3); turning
//     trade off restores the pre-zero-conf behavior exactly.
//
// After a restart the overlay is empty and rebuilds from the node mempool
// within one poll plus one full scan.

const (
	// overlayDefaultCap is the per-aggregator overlay entry cap (owner
	// decision 2026-11-09, requirement §5 Q2): oldest-first eviction with a
	// log line when exceeded.
	overlayDefaultCap = 50000

	overlayEffectMint      = "mint"
	overlayEffectColCreate = "col_create"
	overlayEffectColModify = "col_modify"
	overlayEffectItemDesc  = "item_desc"
)

// overlayPinEntry is one mempool pin's parsed effect. The parsed payload
// shapes are the staging structs (PendingMint / PendingDesc / PendingColMod)
// so projections treat staged-confirmed and overlay pins uniformly; a mint
// carries its tx Inputs for the ancestry rule behind numberFinal (R-F-5).
type overlayPinEntry struct {
	PinId       string
	TxId        string
	ChainName   string
	FirstSeenAt int64
	Collection  string
	Kind        string // overlayEffect*
	Mint        *PendingMint
	ColCreate   *Collection
	ColModify   *PendingColMod
	Desc        *PendingDesc
}

// overlaySpendEntry is one detected mempool spend of a tracked item outpoint
// (base or overlay). Keyed by the spent outpoint, latest observation wins.
type overlaySpendEntry struct {
	ItemPinId     string
	SpentOutpoint string
	TxId          string
	ChainName     string
	FromAddress   string
	ToAddress     string // empty on unresolvable burn / meltdown
	Output        string
	OutputValue   int64
	Burned        bool
	FirstSeenAt   int64
}

// SetZeroConfGates wires the R-F-6 config gates (called from the daemon
// startup before the engine starts; default off keeps today's behavior).
func (a *Aggregator) SetZeroConfGates(issuance, trade bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.zeroConfIssuance = issuance
	a.zeroConfTrade = trade
}

// SetOverlayCap overrides the overlay entry cap (tests).
func (a *Aggregator) SetOverlayCap(cap int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cap > 0 {
		a.overlayCap = cap
	}
}

// classifyNftPin resolves which aggregate effect a pin would have once
// confirmed (same dispatch rules as ingestNftPin). effect "" = not ours.
func classifyNftPin(pin *aggregator.PinInscription) (collectionName, effect string) {
	path := effectivePath(pin.Path)
	if !strings.HasPrefix(strings.ToLower(path), nftPathPrefix) {
		return "", ""
	}
	segments := strings.Split(path, "/")
	if len(segments) < 4 || segments[3] == "" {
		return "", ""
	}
	collectionName = segments[3]
	op := ""
	if len(segments) > 4 {
		op = strings.ToLower(segments[4])
	}
	operation := strings.ToLower(strings.TrimSpace(pin.Operation))
	switch op {
	case "collection_desc":
		switch operation {
		case "create":
			return collectionName, overlayEffectColCreate
		case "modify":
			return collectionName, overlayEffectColModify
		}
	case "item_desc":
		if operation == "create" {
			return collectionName, overlayEffectItemDesc
		}
	default:
		if operation == "create" {
			return collectionName, overlayEffectMint
		}
	}
	return collectionName, ""
}

// txidOfPin resolves a mempool pin's transaction id (ledger convention —
// the same string the confirmed path stamps into GenesisTransaction).
func txidOfPin(pin *aggregator.PinInscription) string {
	if tx := strings.TrimSpace(pin.GenesisTransaction); tx != "" {
		return tx
	}
	if id := pin.Id; len(id) > 64 && id[64] == 'i' {
		return id[:64]
	}
	return pin.Id
}

// recordMempoolPin records one mempool pin's overlay effect (R-F-1).
// Idempotent by pin id: an engine re-delivery keeps the original firstSeenAt.
// Caller holds a.mu.
func (a *Aggregator) recordMempoolPinLocked(pin *aggregator.PinInscription) {
	if pin == nil || a.db == nil || len(pin.Id) == 0 || !a.zeroConfIssuance {
		return
	}
	// Same pin universe as the confirmed path: MVC witness-format pins are
	// a separate parse path man-p2p never indexes.
	if pin.ChainName == "mvc" && !pin.IsOpReturn {
		return
	}
	collectionName, effect := classifyNftPin(pin)
	if effect == "" {
		return
	}
	if _, ok := a.overlayPins[pin.Id]; ok {
		return // idempotent re-delivery
	}
	entry := &overlayPinEntry{
		PinId:       pin.Id,
		TxId:        txidOfPin(pin),
		ChainName:   nftChainName(pin),
		FirstSeenAt: time.Now().Unix(),
		Collection:  collectionName,
		Kind:        effect,
	}
	switch effect {
	case overlayEffectMint:
		entry.Mint = &PendingMint{
			ItemPinId:        pin.Id,
			OwnerAddress:     pin.Address,
			OwnerMetaId:      globalMetaIdByAddress(pin.Address),
			ContentType:      pin.ContentType,
			ContentSummary:   pinListSummary(pin.ContentBody),
			GenesisChainName: nftChainName(pin),
			GenesisHeight:    pin.GenesisHeight, // -1 on the mempool path (MAN convention)
			TxIndex:          pin.TxIndex,
			OutputIndex:      pinOutputIndex(pin.Id),
			GenesisTx:        pin.GenesisTransaction,
			GenesisFee:       0,
			Output:           pin.Output,
			OutputValue:      pin.OutputValue,
			CreateTime:       pin.Timestamp,
			Inputs:           pin.Inputs,
		}
	case overlayEffectColCreate:
		col, err := parseOverlayCollection(collectionName, pin)
		if err != nil {
			log.Printf("WARNING: mrc721nft overlay collection_desc %s skipped: %v", pin.Id, err)
			return
		}
		entry.ColCreate = col
	case overlayEffectColModify:
		entry.ColModify = &PendingColMod{
			ModifyPinId:   pin.Id,
			ModifyAddress: pin.Address,
			Content:       pinListSummary(pin.ContentBody),
			Timestamp:     pin.Timestamp,
		}
	case overlayEffectItemDesc:
		refs, ok := parseItemDescRefs(pin.ContentBody)
		if !ok {
			log.Printf("WARNING: mrc721nft overlay item_desc %s skipped: unreadable payload", pin.Id)
			return
		}
		entry.Desc = &PendingDesc{DescPinId: pin.Id, Items: refs}
	}
	a.overlayPins[entry.PinId] = entry
	a.overlayTxids[entry.TxId] = entry.FirstSeenAt
	a.evictOverlayCapLocked()
}

// parseOverlayCollection builds the provisional album record for an overlay
// collection_desc create — same parsing and validation as
// collectionRegister, minus persistence (first-registration identity is
// resolved at read time: a confirmed album for the pair always wins).
func parseOverlayCollection(collectionName string, pin *aggregator.PinInscription) (*Collection, error) {
	content, err := parseContent(pin.ContentBody)
	if err != nil {
		return nil, err
	}
	col := &Collection{
		CollectionName:   collectionName,
		PinId:            pin.Id,
		CreateAddress:    pin.Address,
		CreateMetaId:     globalMetaIdByAddress(pin.Address),
		CreateTime:       pin.Timestamp,
		GenesisChainName: nftChainName(pin),
		GenesisHeight:    pin.GenesisHeight, // -1 (MAN convention for overlay rows)
		GenesisTx:        pin.GenesisTransaction,
	}
	col.Name = stringField(content, "name")
	col.Desc = stringField(content, "desc")
	col.Website = stringField(content, "website")
	col.Cover = stringField(content, "cover")
	col.Metadata = stringField(content, "metadata")
	col.TotalSupply = -1
	if raw, ok := lookupCI(content, "totalsupply"); ok {
		v, err := parseInt64(raw)
		if err != nil {
			return nil, err
		}
		col.TotalSupply = v
	}
	if raw, ok := lookupCI(content, "royaltyrate"); ok {
		v, err := parseInt64(raw)
		if err != nil {
			return nil, err
		}
		col.RoyaltyRate = int(v)
	}
	if col.TotalSupply < -1 || col.TotalSupply > 1000000000000 {
		return nil, errOverlayInvalidField{"totalSupply out of range"}
	}
	if col.RoyaltyRate < 0 || col.RoyaltyRate > 20 {
		return nil, errOverlayInvalidField{"royaltyRate out of range"}
	}
	return col, nil
}

type errOverlayInvalidField struct{ msg string }

func (e errOverlayInvalidField) Error() string { return e.msg }

// recordOverlaySpendsLocked folds mempool spend notices into the overlay
// (R-F-3): one entry per spent tracked outpoint, latest observation wins,
// firstSeenAt preserved when the same spending tx is re-observed.
func (a *Aggregator) recordOverlaySpendsLocked(chainName string, tracked map[string]aggregator.TrackedOutput, spends map[string]aggregator.TransferNotice, now int64) {
	for outpoint, notice := range spends {
		ref, ok := tracked[outpoint]
		if !ok {
			continue
		}
		txId := notice.Location
		if idx := strings.Index(txId, ":"); idx >= 0 {
			txId = txId[:idx]
		}
		if txId == "" {
			continue
		}
		entry := &overlaySpendEntry{
			ItemPinId:     ref.Ref,
			SpentOutpoint: outpoint,
			TxId:          txId,
			ChainName:     chainName,
			FromAddress:   notice.FromAddress,
			ToAddress:     notice.ToAddress,
			Output:        notice.Output,
			OutputValue:   notice.OutputValue,
			Burned:        notice.Meltdown || notice.ToAddress == "",
			FirstSeenAt:   now,
		}
		if existing := a.overlaySpends[outpoint]; existing != nil && existing.TxId == txId {
			entry.FirstSeenAt = existing.FirstSeenAt // re-observation keeps first arrival
		}
		a.overlaySpends[outpoint] = entry
		a.overlayTxids[txId] = entry.FirstSeenAt
	}
	a.evictOverlayCapLocked()
}

// dropOverlayForTxLocked is the confirmation handoff (R-F-8.1): after the
// block path ingested a txid's facts, its overlay entries go — same lock,
// so no read can observe the gap or a duplicate row.
func (a *Aggregator) dropOverlayForTxLocked(txid string) {
	if txid == "" {
		return
	}
	for pinId, entry := range a.overlayPins {
		if entry.TxId == txid {
			delete(a.overlayPins, pinId)
		}
	}
	for outpoint, entry := range a.overlaySpends {
		if entry.TxId == txid {
			delete(a.overlaySpends, outpoint)
		}
	}
	delete(a.overlayTxids, txid)
}

// ReconcileMempoolRound implements aggregator.MempoolOverlayConsumer
// (R-F-1 eviction): a transaction absent from a fully observed mempool
// round loses all of its overlay effects — it either confirmed (the block
// hook already dropped its entries) or left the mempool. Chain-scoped like
// the pending-transfer reconcile; a nil txid set (chain without a
// MempoolTxIDLister, or a failed fetch — the engine skips those) never
// reconciles.
func (a *Aggregator) ReconcileMempoolRound(chainName string, observedTxIDs []string) {
	if observedTxIDs == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	observed := make(map[string]struct{}, len(observedTxIDs))
	for _, txid := range observedTxIDs {
		observed[txid] = struct{}{}
	}
	var staleTxs []string
	for txid := range a.overlayTxids {
		if _, ok := observed[txid]; !ok {
			staleTxs = append(staleTxs, txid)
		}
	}
	for _, txid := range staleTxs {
		a.dropOverlayForTxLocked(txid)
	}
	if len(staleTxs) > 0 {
		log.Printf("[mrc721nft] overlay reconcile (%s): evicted %d txid(s)", chainName, len(staleTxs))
	}
}

// evictOverlayCapLocked enforces the overlay entry cap, oldest-first, with
// a log line (requirement §5 Q2, owner-confirmed figure).
func (a *Aggregator) evictOverlayCapLocked() {
	cap := a.overlayCap
	if cap <= 0 {
		cap = overlayDefaultCap
	}
	count := len(a.overlayPins) + len(a.overlaySpends)
	if count <= cap {
		return
	}
	type aged struct {
		firstSeenAt int64
		txid        string
	}
	ages := make([]aged, 0, count)
	for _, entry := range a.overlayPins {
		ages = append(ages, aged{entry.FirstSeenAt, entry.TxId})
	}
	for _, entry := range a.overlaySpends {
		ages = append(ages, aged{entry.FirstSeenAt, entry.TxId})
	}
	sort.Slice(ages, func(i, j int) bool { return ages[i].firstSeenAt < ages[j].firstSeenAt })
	evicted := make(map[string]struct{})
	for _, age := range ages {
		if len(a.overlayPins)+len(a.overlaySpends) <= cap {
			break
		}
		if _, done := evicted[age.txid]; done {
			continue
		}
		a.dropOverlayForTxLocked(age.txid)
		evicted[age.txid] = struct{}{}
		log.Printf("WARNING: mrc721nft overlay cap %d exceeded — evicted txid %s (oldest first)", cap, age.txid)
	}
}

// overlaySnapshot is a consistent copy of the overlay state for read-side
// projections (taken under a.mu; projections never touch the live maps).
type overlaySnapshot struct {
	pins   map[string]*overlayPinEntry
	spends map[string]*overlaySpendEntry
}

func (a *Aggregator) overlaySnapshotLocked() *overlaySnapshot {
	snap := &overlaySnapshot{
		pins:   make(map[string]*overlayPinEntry, len(a.overlayPins)),
		spends: make(map[string]*overlaySpendEntry, len(a.overlaySpends)),
	}
	for k, v := range a.overlayPins {
		snap.pins[k] = v
	}
	for k, v := range a.overlaySpends {
		snap.spends[k] = v
	}
	return snap
}
