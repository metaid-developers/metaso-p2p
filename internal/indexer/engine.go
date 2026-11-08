// Package indexer implements the blockchain indexing engine.
// It orchestrates chain adapters for block scanning and ZMQ mempool monitoring,
// emitting parsed PinEvents to the aggregator registry.
//
// Architecture:
//   - BlockScanner polls chain RPC for new blocks.
//   - ZMQListener receives mempool transactions in real time.
//   - MultiChainCoordinator ensures cross-chain ordering by timestamp.
//   - All pins flow through the aggregator Registry.
package indexer

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/chain"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Engine is the top-level indexing engine.
type Engine struct {
	mu                      sync.RWMutex
	chains                  map[string]*chainEntry
	registry                *aggregator.Registry
	store                   *storage.PebbleStore
	scanInterval            time.Duration
	mempoolPollingEnabled   bool
	mempoolPollInterval     time.Duration
	mempoolDedupeTTL        time.Duration
	mempoolFullScanInterval time.Duration
	mempoolSeen             map[string]time.Time
	mempoolTxSeen           map[string]time.Time
	mempoolMu               sync.Mutex
	running                 bool
	cancel                  context.CancelFunc
}

type chainEntry struct {
	chain      chain.Chain
	indexer    chain.Indexer
	lastHeight int64
}

// NewEngine creates a new indexing engine.
func NewEngine(store *storage.PebbleStore, registry *aggregator.Registry) *Engine {
	return &Engine{
		chains:                  make(map[string]*chainEntry),
		registry:                registry,
		store:                   store,
		scanInterval:            10 * time.Second,
		mempoolPollingEnabled:   true,
		mempoolPollInterval:     10 * time.Second,
		mempoolDedupeTTL:        30 * time.Minute,
		mempoolFullScanInterval: mempoolFullScanIntervalDefault,
		mempoolSeen:             make(map[string]time.Time),
		mempoolTxSeen:           make(map[string]time.Time),
	}
}

// ConfigureMempoolPolling updates RPC mempool polling behavior.
func (e *Engine) ConfigureMempoolPolling(enabled bool, pollInterval, dedupeTTL time.Duration) {
	e.mempoolMu.Lock()
	defer e.mempoolMu.Unlock()

	e.mempoolPollingEnabled = enabled
	if pollInterval > 0 {
		e.mempoolPollInterval = pollInterval
	}
	if dedupeTTL > 0 {
		e.mempoolDedupeTTL = dedupeTTL
	}
}

// Chains returns the number of registered chains.
func (e *Engine) Chains() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.chains)
}

// RegisterChain adds a chain+indexer pair to the engine.
func (e *Engine) RegisterChain(c chain.Chain, idx chain.Indexer, startHeight int64) error {
	if err := c.Init(); err != nil {
		return err
	}
	if err := idx.Init(); err != nil {
		return err
	}

	e.mu.Lock()
	e.chains[c.Name()] = &chainEntry{
		chain:      c,
		indexer:    idx,
		lastHeight: startHeight,
	}
	e.mu.Unlock()

	log.Printf("[indexer] registered chain %s at height %d", c.Name(), startHeight)
	return nil
}

// Start begins block scanning and ZMQ monitoring.
func (e *Engine) Start(ctx context.Context) {
	runCtx, cancel := context.WithCancel(ctx)
	e.cancel = cancel

	e.mu.Lock()
	e.running = true
	e.mu.Unlock()

	log.Printf("[indexer] engine started, scanInterval=%s", e.scanInterval)

	// Restore last known heights from Pebble
	e.restoreHeights()

	// Start block scanning loop
	go e.scanLoop(runCtx)

	// Start ZMQ listeners
	go e.zmqLoop(runCtx)

	// Start the periodic full-mempool transfer scan (unconfirmed NFT spend
	// visibility for TransferConsumer aggregators).
	go e.mempoolTransferLoop(runCtx)
}

// Stop gracefully stops the engine.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.mu.Lock()
	e.running = false
	e.mu.Unlock()
	log.Printf("[indexer] engine stopped")
}

// scanLoop periodically polls for new blocks.
func (e *Engine) scanLoop(ctx context.Context) {
	ticker := time.NewTicker(e.scanInterval)
	defer ticker.Stop()

	// Do an immediate catch-up scan on startup
	e.catchUpAll()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.scanNewBlocks()
		}
	}
}

// catchUpAll processes blocks from stored height to current chain tip.
func (e *Engine) catchUpAll() {
	e.mu.RLock()
	entries := make(map[string]*chainEntry, len(e.chains))
	for name, entry := range e.chains {
		entries[name] = entry
	}
	e.mu.RUnlock()

	for _, entry := range entries {
		bestHeight := entry.chain.GetBestHeight()
		if bestHeight <= 0 {
			continue
		}

		from := entry.lastHeight + 1
		if from <= 0 {
			from = 1
		}

		log.Printf("[indexer] %s: catching up blocks %d → %d", entry.chain.Name(), from, bestHeight)
		e.scanRange(entry, from, bestHeight)
	}
}

// scanNewBlocks checks each chain for a single new block.
func (e *Engine) scanNewBlocks() {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, entry := range e.chains {
		bestHeight := entry.chain.GetBestHeight()
		if bestHeight > entry.lastHeight {
			e.scanRange(entry, entry.lastHeight+1, bestHeight)
		}
	}
}

// scanRange processes blocks from fromHeight to toHeight (inclusive).
func (e *Engine) scanRange(entry *chainEntry, fromHeight, toHeight int64) {
	chainName := entry.chain.Name()

	for h := fromHeight; h <= toHeight; h++ {
		pins, _, err := entry.indexer.CatchPins(h)
		if err != nil {
			log.Printf("[indexer] %s block %d: CatchPins error: %v", chainName, h, err)
			continue
		}

		var events []*aggregator.NotifyEvent
		if len(pins) > 0 {
			log.Printf("[indexer] %s block %d: %d pins parsed", chainName, h, len(pins))

			// Route confirmed pins to aggregators
			for _, p := range pins {
				evt := e.registry.RouteBlockPin(p)
				events = append(events, evt...)
			}
		}

		// Detect spends of tracked outputs every block — a transfer block
		// can contain no new pins at all (plain UTXO spends).
		e.processTransfers(entry, h)

		entry.lastHeight = h
		e.persistHeight(chainName, h)

		// Log notify events — the socket layer reads from each aggregator's
		// NotifyChannel directly via StartPushConsumer (Phase 2), so the engine
		// does not write to aggregator channels.
		if len(events) > 0 {
			log.Printf("[indexer] %s block %d: %d notify events", chainName, h, len(events))
		}
	}
}

// processTransfers feeds the block's spends of tracked outputs to the
// TransferConsumer aggregators. The chain adapter performs the detection
// (chain.TransferDetector, optional per chain); the tracked-output set
// comes from the consumers themselves. Chains without a transfer detector
// and runs without consumers are no-ops.
func (e *Engine) processTransfers(entry *chainEntry, height int64) {
	consumers := e.registry.TransferConsumers()
	if len(consumers) == 0 {
		return
	}
	detector, ok := entry.indexer.(chain.TransferDetector)
	if !ok {
		return
	}
	idMap := make(map[string]string)
	for _, consumer := range consumers {
		for outpoint, tracked := range consumer.TrackedOutputs() {
			if tracked.Owner != "" {
				idMap[outpoint] = tracked.Owner
			}
		}
	}
	if len(idMap) == 0 {
		return
	}
	notices, err := detector.DetectTransfers(idMap)
	if err != nil {
		log.Printf("[indexer] %s block %d: DetectTransfers error: %v", entry.chain.Name(), height, err)
		return
	}
	if len(notices) == 0 {
		return
	}
	chainName := entry.chain.Name()
	log.Printf("[indexer] %s block %d: %d tracked-output spends", chainName, height, len(notices))
	for _, consumer := range consumers {
		consumer.HandleBlockTransfers(chainName, height, notices)
	}
}

// mempoolFullScanIntervalDefault mirrors man-p2p's periodic full-mempool
// sync cadence for unconfirmed NFT spend visibility. Since the incremental
// mempool poll also runs spend detection (R-F-7), the full scan stays for
// eviction reconcile only; the interval is configurable, clamped 30–60s.
const (
	mempoolFullScanIntervalDefault = 30 * time.Second
	mempoolFullScanIntervalMin     = 30 * time.Second
	mempoolFullScanIntervalMax     = 60 * time.Second
)

// ConfigureMempoolFullScanInterval sets the full-mempool spend-scan cadence,
// clamped to [30s, 60s] (R-F-7).
func (e *Engine) ConfigureMempoolFullScanInterval(interval time.Duration) {
	if interval <= 0 {
		interval = mempoolFullScanIntervalDefault
	}
	if interval < mempoolFullScanIntervalMin {
		interval = mempoolFullScanIntervalMin
	}
	if interval > mempoolFullScanIntervalMax {
		interval = mempoolFullScanIntervalMax
	}
	e.mu.Lock()
	e.mempoolFullScanInterval = interval
	e.mu.Unlock()
}

// mempoolTransferLoop periodically scans the FULL mempool of each chain
// with a MempoolTransferDetector for spends of tracked outputs. Full-list
// sync (not incremental) is what the consumers' reconcile correctness
// relies on: a pending spend not observed this round is settled/dropped.
// New spends are caught by the incremental poll's spend detection; this
// loop's remaining job is the reconcile that evicts dropped transactions.
func (e *Engine) mempoolTransferLoop(ctx context.Context) {
	e.mu.RLock()
	interval := e.mempoolFullScanInterval
	e.mu.RUnlock()
	if interval <= 0 {
		interval = mempoolFullScanIntervalDefault
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.processMempoolTransfers()
		}
	}
}

// processMempoolTransfers runs one full-mempool spend-scan round for every
// chain with a MempoolTransferDetector. A failed mempool fetch skips the
// chain entirely — reconcile must never run on a round whose tx list was
// not fully observed (it would evict live pendings).
func (e *Engine) processMempoolTransfers() {
	consumers := e.registry.TransferConsumers()
	if len(consumers) == 0 {
		return
	}
	var overlayConsumers []aggregator.MempoolOverlayConsumer
	for _, consumer := range consumers {
		if oc, ok := consumer.(aggregator.MempoolOverlayConsumer); ok {
			overlayConsumers = append(overlayConsumers, oc)
		}
	}
	e.mu.RLock()
	entries := make([]*chainEntry, 0, len(e.chains))
	for _, entry := range e.chains {
		entries = append(entries, entry)
	}
	e.mu.RUnlock()

	for _, entry := range entries {
		detector, ok := entry.indexer.(chain.MempoolTransferDetector)
		if !ok {
			continue
		}
		idMap := make(map[string]string)
		for _, consumer := range consumers {
			for outpoint, tracked := range consumer.TrackedOutputs() {
				if tracked.Owner != "" {
					idMap[outpoint] = tracked.Owner
				}
			}
		}
		txList, err := entry.chain.GetMempoolTransactionList()
		if err != nil {
			log.Printf("[indexer] %s mempool transfer scan: fetch error: %v", entry.chain.Name(), err)
			continue
		}
		notices, err := detector.DetectMempoolSpends(idMap, txList)
		if err != nil {
			log.Printf("[indexer] %s mempool transfer scan: detect error: %v", entry.chain.Name(), err)
			continue
		}
		chainName := entry.chain.Name()
		for _, consumer := range consumers {
			consumer.HandleMempoolSpends(chainName, notices)
		}
		if len(overlayConsumers) > 0 {
			// Complete round: hand the observed txid set (ledger convention)
			// to the overlay consumers for eviction reconcile.
			var txIDs []string
			if lister, ok := entry.chain.(chain.MempoolTxIDLister); ok {
				txIDs = lister.MempoolTxIDs(txList)
			}
			for _, consumer := range overlayConsumers {
				consumer.ReconcileMempoolRound(chainName, txIDs)
			}
		}
	}
}

// pollMempoolOnce fetches raw mempool transactions from each registered chain
// over RPC, parses pins, and routes newly-seen mempool pins to aggregators.
func (e *Engine) pollMempoolOnce() {
	e.mu.RLock()
	entries := make([]*chainEntry, 0, len(e.chains))
	for _, entry := range e.chains {
		entries = append(entries, entry)
	}
	e.mu.RUnlock()

	now := time.Now()
	for _, entry := range entries {
		chainName := entry.chain.Name()
		txList, fetchedTxIDs, err := e.getMempoolTransactionList(entry.chain, chainName, now)
		if err != nil {
			log.Printf("[indexer] %s mempool: GetMempoolTransactionList error: %v", chainName, err)
			continue
		}
		if len(txList) == 0 {
			continue
		}

		pins, txIDs, err := entry.indexer.CatchMempoolPins(txList)
		if err != nil {
			log.Printf("[indexer] %s mempool: CatchMempoolPins error: %v", chainName, err)
			continue
		}
		pins = e.filterSeenMempoolPins(chainName, pins, txIDs, now)

		for _, pin := range pins {
			e.registry.RouteMempoolPin(pin)
		}
		// Incremental spend detection (R-F-7): every poll scans the freshly
		// fetched transactions for spends of tracked outputs, so an
		// ownership change surfaces within one poll instead of waiting for
		// the full-scan cadence. No reconcile here — an incremental round is
		// a partial observation. The loop chains spends: each applied notice
		// can surface new overlay-tracked outputs (mint → list → buy →
		// resell, all unconfirmed) that later transactions in the same batch
		// spend, so detection repeats until it stops finding new ones.
		e.detectIncrementalMempoolSpends(entry, txList)
		e.markMempoolTxIDsSeen(chainName, fetchedTxIDs, now)
	}
}

// detectIncrementalMempoolSpends runs the incremental spend detection for
// one poll round, chaining until no new notices surface (max depth cap).
func (e *Engine) detectIncrementalMempoolSpends(entry *chainEntry, txList []any) {
	consumers := e.registry.TransferConsumers()
	if len(consumers) == 0 || len(txList) == 0 {
		return
	}
	detector, ok := entry.indexer.(chain.MempoolTransferDetector)
	if !ok {
		return
	}
	chainName := entry.chain.Name()
	notified := make(map[string]struct{}) // spent outpoints already handed over
	const maxChainDepth = 8
	for round := 0; round < maxChainDepth; round++ {
		idMap := make(map[string]string)
		for _, consumer := range consumers {
			for outpoint, tracked := range consumer.TrackedOutputs() {
				if tracked.Owner != "" {
					idMap[outpoint] = tracked.Owner
				}
			}
		}
		notices, err := detector.DetectMempoolSpends(idMap, txList)
		if err != nil {
			log.Printf("[indexer] %s mempool incremental spend scan: detect error: %v", chainName, err)
			return
		}
		fresh := make(map[string]aggregator.TransferNotice, len(notices))
		for outpoint, notice := range notices {
			if _, done := notified[outpoint]; !done {
				fresh[outpoint] = notice
			}
		}
		if len(fresh) == 0 {
			return
		}
		for outpoint := range fresh {
			notified[outpoint] = struct{}{}
		}
		for _, consumer := range consumers {
			if oc, ok := consumer.(aggregator.MempoolOverlayConsumer); ok {
				oc.HandleMempoolSpendsIncremental(chainName, fresh)
			}
		}
	}
}

func (e *Engine) getMempoolTransactionList(c chain.Chain, chainName string, now time.Time) ([]any, []string, error) {
	source, ok := c.(chain.IncrementalMempoolChain)
	if !ok {
		list, err := c.GetMempoolTransactionList()
		return list, nil, err
	}

	txIDs, err := source.GetMempoolTransactionIDs()
	if err != nil {
		return nil, nil, err
	}
	pending := e.filterUnseenMempoolTxIDs(chainName, txIDs, now)
	if len(pending) == 0 {
		return nil, nil, nil
	}
	transactions, err := source.GetMempoolTransactions(pending)
	if err != nil {
		return nil, nil, err
	}

	list := make([]any, 0, len(transactions))
	succeeded := make([]string, 0, len(transactions))
	for _, txID := range pending {
		if tx, ok := transactions[txID]; ok && tx != nil {
			list = append(list, tx)
			succeeded = append(succeeded, txID)
		}
	}
	return list, succeeded, nil
}

func (e *Engine) filterUnseenMempoolTxIDs(chainName string, txIDs []string, now time.Time) []string {
	e.mempoolMu.Lock()
	defer e.mempoolMu.Unlock()

	for key, seenAt := range e.mempoolTxSeen {
		if e.mempoolDedupeTTL > 0 && now.Sub(seenAt) > e.mempoolDedupeTTL {
			delete(e.mempoolTxSeen, key)
		}
	}

	pending := make([]string, 0, len(txIDs))
	added := make(map[string]struct{}, len(txIDs))
	for _, txID := range txIDs {
		txID = strings.TrimSpace(txID)
		if txID == "" {
			continue
		}
		key := chainName + ":" + txID
		if _, ok := added[key]; ok {
			continue
		}
		if seenAt, ok := e.mempoolTxSeen[key]; ok && (e.mempoolDedupeTTL <= 0 || now.Sub(seenAt) <= e.mempoolDedupeTTL) {
			continue
		}
		added[key] = struct{}{}
		pending = append(pending, txID)
	}
	return pending
}

func (e *Engine) markMempoolTxIDsSeen(chainName string, txIDs []string, now time.Time) {
	e.mempoolMu.Lock()
	defer e.mempoolMu.Unlock()
	for _, txID := range txIDs {
		txID = strings.TrimSpace(txID)
		if txID != "" {
			e.mempoolTxSeen[chainName+":"+txID] = now
		}
	}
}

func (e *Engine) filterSeenMempoolPins(chainName string, pins []*aggregator.PinInscription, txIDs []string, now time.Time) []*aggregator.PinInscription {
	e.mempoolMu.Lock()
	defer e.mempoolMu.Unlock()

	for key, seenAt := range e.mempoolSeen {
		if e.mempoolDedupeTTL > 0 && now.Sub(seenAt) > e.mempoolDedupeTTL {
			delete(e.mempoolSeen, key)
		}
	}

	filtered := make([]*aggregator.PinInscription, 0, len(pins))
	newKeys := make(map[string]time.Time)
	for _, pin := range pins {
		if pin == nil {
			continue
		}
		stableID := mempoolStableIDForPin(pin, txIDs)
		if stableID == "" {
			filtered = append(filtered, pin)
			continue
		}

		key := chainName + ":" + stableID
		if seenAt, ok := e.mempoolSeen[key]; ok && (e.mempoolDedupeTTL <= 0 || now.Sub(seenAt) <= e.mempoolDedupeTTL) {
			continue
		}
		if _, ok := newKeys[key]; ok {
			continue
		}
		newKeys[key] = now
		filtered = append(filtered, pin)
	}
	for key, seenAt := range newKeys {
		e.mempoolSeen[key] = seenAt
	}
	return filtered
}

func mempoolStableIDForPin(pin *aggregator.PinInscription, txIDs []string) string {
	if pin == nil {
		return ""
	}
	if genesisTx := strings.TrimSpace(pin.GenesisTransaction); genesisTx != "" {
		return genesisTx
	}
	pinID := strings.TrimSpace(pin.Id)
	if txID := mempoolTxIDFromPinID(pinID); txID != "" {
		return txID
	}
	for _, txID := range txIDs {
		txID = strings.TrimSpace(txID)
		if txID != "" && (pinID == txID || strings.HasPrefix(pinID, txID+"i") || strings.HasPrefix(pinID, txID+":")) {
			return txID
		}
	}
	return pinID
}

func mempoolTxIDFromPinID(pinID string) string {
	if pinID == "" {
		return ""
	}
	if separator := strings.Index(pinID, ":"); separator > 0 {
		return strings.TrimSpace(pinID[:separator])
	}
	suffixStart := strings.LastIndex(pinID, "i")
	if suffixStart <= 0 || suffixStart == len(pinID)-1 {
		return ""
	}
	for _, r := range pinID[suffixStart+1:] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return strings.TrimSpace(pinID[:suffixStart])
}

// persistHeight saves the last processed height to Pebble.
func (e *Engine) persistHeight(chainName string, height int64) {
	key := []byte(chainName + "_lastheight")
	val := []byte(fmt.Sprintf("%d", height))
	if err := e.store.Set("indexer_meta", key, val); err != nil {
		log.Printf("[indexer] failed to persist height for %s: %v", chainName, err)
	}
}

// restoreHeights loads last processed heights from Pebble.
func (e *Engine) restoreHeights() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for name, entry := range e.chains {
		key := []byte(name + "_lastheight")
		raw, err := e.store.Get("indexer_meta", key)
		if err != nil || raw == nil {
			continue
		}
		var height int64
		fmt.Sscanf(string(raw), "%d", &height)
		if height > 0 {
			entry.lastHeight = height
			log.Printf("[indexer] %s: restored height %d", name, height)
		}
	}
}

// zmqLoop manages ZMQ listeners for all registered chains.
func (e *Engine) zmqLoop(ctx context.Context) {
	e.mempoolMu.Lock()
	enabled := e.mempoolPollingEnabled
	interval := e.mempoolPollInterval
	e.mempoolMu.Unlock()

	if !enabled {
		log.Printf("[indexer] mempool polling disabled")
		return
	}
	if interval <= 0 {
		interval = 10 * time.Second
	}

	log.Printf("[indexer] mempool RPC polling started, interval=%s", interval)
	e.pollMempoolOnce()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.pollMempoolOnce()
		}
	}
}

// TransactionStatus resolves the confirmation facts of one transaction on
// the named chain (the data source behind GET /api/tx/status). Unknown
// chains and chains without a TxStatusProvider report ok=false; RPC errors
// surface as errors and the API layer answers found:false/unknown.
func (e *Engine) TransactionStatus(chainName, txid string) (ok bool, confirmed bool, confirmations int64, blockHash string, height int64, err error) {
	if e == nil {
		return false, false, 0, "", 0, nil
	}
	e.mu.RLock()
	entry := e.chains[chainName]
	e.mu.RUnlock()
	if entry == nil {
		return false, false, 0, "", 0, nil
	}
	provider, ok := entry.chain.(chain.TxStatusProvider)
	if !ok {
		return false, false, 0, "", 0, nil
	}
	confirmed, confirmations, blockHash, height, err = provider.GetTransactionStatus(txid)
	return true, confirmed, confirmations, blockHash, height, err
}
