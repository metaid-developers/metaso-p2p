// Package mrc721nft is the MetaSo-P2P native MRC-721 NFT aggregate,
// re-implementing man-p2p's MRC-721 indexing (collections, items, transfer
// provenance) with the same API contract so downstream consumers can switch
// their base URL from man-p2p to MetaSo-P2P without any other change.
//
// Division of labour (docs/specs/2026-11-04-mrc721-sell-collection-api.md
// §0): man-p2p stays a pure raw-pin indexer; MetaSo-P2P owns the NFT
// business indexing. This package ingests confirmed /nft/mrc721 pins from
// the live chain feed and (bootstrap) the manapi replay, and serves the
// manapi-compatible /api/mrc721 collection/item query surface. The
// sibling mrc721 package keeps serving the sell-intent index.
package mrc721nft

import (
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	// Namespace is the Pebble namespace of the NFT aggregate.
	Namespace = "mrc721nft"

	// nftPathPrefix is the MRC-721 protocol path prefix.
	nftPathPrefix = "/nft/mrc721"

	// fallbackChainName matches man-p2p's mrc721ChainName default for pins
	// that somehow arrive without a chain name.
	fallbackChainName = "mvc"
)

// Aggregator is the native MRC-721 NFT aggregate. It never produces socket
// notifications: the aggregate is a pure query surface.
//
// Locking (2026-11-15 production incident fix): two independent locks plus
// atomic gates. mu serialises the confirmed ingest write paths across their
// Pebble writes and guards the tracked-output cache; overlayMu (RWMutex)
// guards the in-memory overlay maps and is only ever held for memory-only
// operations — never across Pebble writes; the zero-conf gates are atomic
// bools set once at startup. A stalled Pebble write (the incident held the
// ingest lock for 2h11m) therefore stalls the ingest pipeline but never
// blocks a read: gates load atomically and readers copy an overlay snapshot
// under overlayMu.RLock, then work lock-free.
type Aggregator struct {
	store    *storage.PebbleStore
	db       *nftStore
	notifyCh chan *aggregator.NotifyEvent

	// mu serialises the confirmed ingest write paths (live block loop,
	// mempool spend rounds, bootstrap replay). Guards the Pebble writes and
	// the tracked cache below. Deliberately NOT held by any read path.
	mu sync.Mutex

	// tracked is the lazily rebuilt outpoint → (owner, itemPinId) cache for
	// the engine's transfer detection; invalidated by trackedDirty after
	// every write that can change an item's outpoint or owner.
	tracked      map[string]aggregator.TrackedOutput
	trackedDirty bool

	// overlayMu guards the overlay maps. Writers take it for the map
	// mutation only; readers take RLock to copy a snapshot and then work
	// lock-free. Never held across Pebble writes.
	overlayMu sync.RWMutex

	// Zero-confirmation overlay state (R-F-1): in-memory only, rebuilt from
	// the node mempool after a restart within one poll plus one full scan.
	// Never persisted, never replayed by backfill. All fields below are
	// guarded by overlayMu except the gates, which are set once at startup.
	zeroConfIssuanceGate atomic.Bool
	zeroConfTradeGate    atomic.Bool
	overlayPins          map[string]*overlayPinEntry   // pinId → entry
	overlaySpends        map[string]*overlaySpendEntry // spent outpoint → entry
	overlayTxids         map[string]int64              // live overlay txid → firstSeenAt
	overlayCap           int

	// replayClient is the upstream client the admin replay-pins endpoint
	// repairs entities from (set once at startup before serving, nil
	// disables the endpoint; unsynchronized by design).
	replayClient *UpstreamClient
}

// SetReplayClient wires the upstream client used by the admin replay-pins
// endpoint (called from the daemon startup, after Init, before serving).
func (a *Aggregator) SetReplayClient(client *UpstreamClient) {
	a.replayClient = client
}

func (a *Aggregator) Name() string { return Namespace }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	if _, err := store.OpenDB(Namespace); err != nil {
		return err
	}
	a.db = &nftStore{store: store}
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	a.overlayPins = make(map[string]*overlayPinEntry)
	a.overlaySpends = make(map[string]*overlaySpendEntry)
	a.overlayTxids = make(map[string]int64)
	a.overlayCap = overlayDefaultCap
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// HandleBlockPin folds a confirmed pin into the NFT aggregate. Pins from
// other paths are ignored; malformed or invalid protocol pins are logged
// and skipped so they never fail the block pipeline. Confirmation handoff
// (R-F-8.1): the pin's overlay entries are dropped right after the
// confirmed facts are folded — the fold runs under the ingest mutex (the
// Pebble writes), the drop under overlayMu (memory only), in that order. A
// read racing that handoff window may transiently still apply the overlay
// effect of the just-folded tx (spend folds self-skip on the moved
// outpoint; mint folds self-correct once the entry drops) — the previous
// single-critical-section design prevented that only by blocking every
// read for the entire duration of a stalled Pebble write.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	a.ingestNftPin(pin)
	a.mu.Unlock()
	if pin != nil {
		a.dropOverlayForTx(txidOfPin(pin))
	}
	return nil, nil
}

// HandleMempoolPin records the pin's zero-conf overlay effect (R-F-1) when
// the issuance gate is on; the confirmed aggregate stays untouched until
// the block folds the pin in. Memory only — no ingest lock involved.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.recordMempoolPin(pin)
	return nil, nil
}

// RegisterRoutes mounts the manapi-compatible MRC-721 collection/item query
// API on the given group (the /api group in the daemon router; the /api
// prefix is added by the caller). Paths/params/response shapes mirror
// man-p2p's /api/mrc721 surface; the sibling mrc721 package's sell/search
// routes coexist (disjoint static children).
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/mrc721")
	group.GET("/collection/pageList", a.handleCollectionPageList)
	group.GET("/collection/info", a.handleCollectionInfo)
	group.GET("/collection/items/pageList", a.handleCollectionItemsPageList)
	group.GET("/address/collection", a.handleAddressCollection)
	group.GET("/address/item", a.handleAddressItem)
	group.GET("/item/info", a.handleItemInfo)
	group.GET("/item/transfers", a.handleItemTransfers)
	group.GET("/item/verify", a.handleItemVerify)
	// Admin repair: re-apply entities by id from the upstream state (same
	// surface as man-p2p's admin endpoint, incl. its no-auth posture).
	group.GET("/admin/replay-pins", a.handleAdminReplayPins)
}
