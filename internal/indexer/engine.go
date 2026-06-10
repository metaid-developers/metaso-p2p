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
	mu                    sync.RWMutex
	chains                map[string]*chainEntry
	registry              *aggregator.Registry
	store                 *storage.PebbleStore
	scanInterval          time.Duration
	mempoolPollingEnabled bool
	mempoolPollInterval   time.Duration
	mempoolDedupeTTL      time.Duration
	mempoolSeen           map[string]time.Time
	mempoolMu             sync.Mutex
	running               bool
	cancel                context.CancelFunc
}

type chainEntry struct {
	chain      chain.Chain
	indexer    chain.Indexer
	lastHeight int64
}

// NewEngine creates a new indexing engine.
func NewEngine(store *storage.PebbleStore, registry *aggregator.Registry) *Engine {
	return &Engine{
		chains:                make(map[string]*chainEntry),
		registry:              registry,
		store:                 store,
		scanInterval:          10 * time.Second,
		mempoolPollingEnabled: true,
		mempoolPollInterval:   10 * time.Second,
		mempoolDedupeTTL:      30 * time.Minute,
		mempoolSeen:           make(map[string]time.Time),
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
		pins, txIDs, err := entry.indexer.CatchPins(h)
		if err != nil {
			log.Printf("[indexer] %s block %d: CatchPins error: %v", chainName, h, err)
			continue
		}

		if len(pins) == 0 {
			entry.lastHeight = h
			e.persistHeight(chainName, h)
			continue
		}

		log.Printf("[indexer] %s block %d: %d pins parsed", chainName, h, len(pins))

		// Route confirmed pins to aggregators
		var events []*aggregator.NotifyEvent
		for _, p := range pins {
			evt := e.registry.RouteBlockPin(p)
			events = append(events, evt...)
		}

		// Handle transfers if we have txIDs
		if len(txIDs) > 0 {
			e.processTransfers(entry, txIDs)
		}

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

// processTransfers detects spent pins and notifies aggregators.
func (e *Engine) processTransfers(entry *chainEntry, txIDs []string) {
	// Build idMap from stored pins that reference these outputs
	// In a full implementation, this queries the aggregator's Pebble DB
	_ = txIDs
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
		txList, err := entry.chain.GetMempoolTransactionList()
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
