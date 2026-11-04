// Package mrc721 is the MetaSo-P2P business-index layer for MRC-721 NFT
// reads (docs/specs/2026-11-04-mrc721-sell-collection-api.md):
//
//   - R-C sell-intent index: ingests the /protocols/mrc721sell declaration
//     log (create-only, latest pin per itemPinId wins) from the live chain
//     feed and the MANAPI backfill, and serves
//     GET /api/mrc721/sell/intents and GET /api/mrc721/sell/intent/info.
//   - R-A/R-B collection views: a read model that periodically refreshes the
//     manapi-compatible MRC-721 collection page list, serving
//     GET /api/mrc721/collection/search and
//     GET /api/mrc721/address/collections-issued.
//
// man-p2p stays a pure raw-pin indexer; this package consumes pins through
// the same aggregator / MANAPI-source patterns every other metaso feature
// uses and never re-implements the raw /nft/* indexing.
package mrc721

import (
	"errors"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	// Namespace is the Pebble namespace of the mrc721 read models.
	Namespace = "mrc721"

	// PathSell is the fixed MRC-721 sell-intent declaration path
	// (docs/nft-sell-protocol-draft.md §2.1). Pins on this path are
	// ordinary raw pins; this index only subscribes to them.
	PathSell = "/protocols/mrc721sell"

	// OperationCreate is the only operation the sell protocol uses: every
	// declaration (first, re-declare, post-cancel re-list) is a CREATE pin,
	// and cancellation is a new declaration with active=false.
	OperationCreate = "create"

	// defaultListSize / maxListSize mirror the house list-page clamps.
	defaultListSize = 20
	maxListSize     = 100
)

// Sentinel errors mapped onto the mrc721 wire codes by the HTTP layer.
var (
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrNotFound         = errors.New("not found")
)

// ItemOutpoint is the advisory item location declared in a sell-intent
// payload (docs/nft-sell-protocol-draft.md §2.2).
type ItemOutpoint struct {
	TxID     string `json:"txid,omitempty"`
	Vout     int64  `json:"vout,omitempty"`
	Satoshis int64  `json:"satoshis,omitempty"`
}

// SellIntentRecord is the latest-wins projection of one itemPinId's
// declaration log entry. Seq is the local monotonic write counter used for
// the recency index and the pagination cursor.
type SellIntentRecord struct {
	ItemPinId          string        `json:"itemPinId"`
	SellPinId          string        `json:"sellPinId"`
	Active             bool          `json:"active"`
	CollectionName     string        `json:"collectionName,omitempty"`
	CollectionPinId    string        `json:"collectionPinId,omitempty"`
	ItemOutpoint       *ItemOutpoint `json:"itemOutpoint,omitempty"`
	SellerAddress      string        `json:"sellerAddress"`
	SellerMetaId       string        `json:"sellerMetaId,omitempty"`
	PriceHintSats      int64         `json:"priceHintSats,omitempty"`
	AcceptedCurrencies []string      `json:"acceptedCurrencies,omitempty"`
	Settlement         []string      `json:"settlement,omitempty"`
	Note               string        `json:"note,omitempty"`
	CreatedAt          int64         `json:"createdAt,omitempty"`
	ExpiresAt          int64         `json:"expiresAt,omitempty"`

	// Chain facts of the winning sell pin.
	ChainName string `json:"chainName,omitempty"`
	Height    int64  `json:"height,omitempty"`
	TxID      string `json:"txId,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"` // chain time, unix seconds

	Seq uint64 `json:"seq"`
}

// Aggregator is the mrc721 business-index module. It never produces socket
// notifications: both read models are pure query surfaces.
type Aggregator struct {
	store    *storage.PebbleStore
	notifyCh chan *aggregator.NotifyEvent
	mu       sync.Mutex // serialises ingest write paths (live + backfill replay)
}

func (a *Aggregator) Name() string { return Namespace }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// HandleBlockPin folds a confirmed pin into the sell-intent index. Pins from
// other paths are ignored; the registration runs in chain order, which is
// what the latest-pin-wins rule consumes.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.ingestSellPin(pin); err != nil {
		return nil, err
	}
	return nil, nil
}

// HandleMempoolPin is a deliberate no-op. The sell protocol is an
// append-only declaration log whose semantics are "latest pin by chain order
// wins"; folding unconfirmed pins into the latest-wins record would let a
// mempool re-declare overwrite a confirmed cancellation with no recovery
// path. Confirmed ingestion plus the MANAPI replay keep the index honest;
// latency is one confirmation.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

// RegisterRoutes mounts the mrc721 query API on the given group (the /api
// group in the daemon router; /api prefix included by the caller).
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/mrc721/sell/intents", a.handleSellIntents)
	router.GET("/mrc721/sell/intent/info", a.handleSellIntentInfo)
}

// normalisedPinPath lowercases and strips the version suffix (@pinId) and
// trailing slash so "/protocols/mrc721sell@<pinid>" and "/protocols/mrc721sell"
// both match PathSell (same house rule as the social aggregator).
func normalisedPinPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	if idx := strings.Index(path, "@"); idx >= 0 {
		path = path[:idx]
	}
	return strings.TrimRight(path, "/")
}

// unixSeconds normalises chain timestamps that arrive in mixed second /
// millisecond granularity into unix seconds.
func unixSeconds(timestamp int64) int64 {
	if timestamp > 100000000000 {
		return timestamp / 1000
	}
	return timestamp
}
