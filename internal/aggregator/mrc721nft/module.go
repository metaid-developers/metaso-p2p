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
type Aggregator struct {
	store    *storage.PebbleStore
	db       *nftStore
	notifyCh chan *aggregator.NotifyEvent
	mu       sync.Mutex // serialises ingest write paths (live + bootstrap replay)

	// tracked is the lazily rebuilt outpoint → (owner, itemPinId) cache for
	// the engine's transfer detection; invalidated by trackedDirty after
	// every write that can change an item's outpoint or owner.
	tracked      map[string]aggregator.TrackedOutput
	trackedDirty bool

	// replayClient is the upstream client the admin replay-pins endpoint
	// repairs entities from (set at startup, nil disables the endpoint).
	replayClient *UpstreamClient
}

// SetReplayClient wires the upstream client used by the admin replay-pins
// endpoint (called from the daemon startup, after Init).
func (a *Aggregator) SetReplayClient(client *UpstreamClient) {
	a.mu.Lock()
	defer a.mu.Unlock()
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
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// HandleBlockPin folds a confirmed pin into the NFT aggregate. Pins from
// other paths are ignored; malformed or invalid protocol pins are logged
// and skipped so they never fail the block pipeline.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ingestNftPin(pin)
	return nil, nil
}

// HandleMempoolPin is a deliberate no-op, mirroring man-p2p: only confirmed
// pins fold into the aggregate (unconfirmed mints/descs wait for their
// block; unconfirmed transfer visibility comes through the mempool pending
// channel, not through pin ingestion).
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
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
