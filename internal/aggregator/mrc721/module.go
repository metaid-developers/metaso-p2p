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
//   - R-E passive-offer index (flag-gated): ingests the /protocols/
//     mrc721offer declaration log (create-only, latest pin per itemPinId
//     wins, offerTx stored verbatim) from the live chain feed and serves
//     GET /api/mrc721/offers and GET /api/mrc721/offer/info.
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
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
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
// notifications: all read models are pure query surfaces.
type Aggregator struct {
	store    *storage.PebbleStore
	notifyCh chan *aggregator.NotifyEvent
	mu       sync.Mutex // serialises ingest write paths (live + backfill replay)

	// nativeSource, when set, is the mrc721nft aggregate the collection
	// search / issuer endpoints serve from instead of the upstream-refreshed
	// read model. Wired at startup only when the native aggregate is
	// enabled (i.e. its store has been bootstrapped); the wire contract is
	// unchanged either way.
	nativeSource NativeCollectionSource

	// itemCollectionSrc, when set, is the mrc721nft item read model the
	// offer rows join through for their item → collection identity
	// (best-effort; nil keeps the rows collection-free).
	itemCollectionSrc ItemCollectionSource

	// offerIndexEnabled gates the /protocols/mrc721offer passive-offer index
	// (ingest + routes); set at startup from METASO_P2P_MRC721_OFFER_ENABLED
	// before Register, default off.
	offerIndexEnabled bool
}

// NativeCollectionSource abstracts the native mrc721nft collection read
// model for the search / issuer endpoints. Rows carry the collectionRow
// wire shape.
type NativeCollectionSource interface {
	SearchNativeCollections(keyword, issuer, cursor string, size int, zeroConf bool) ([]*mrc721nft.SearchRow, int, string, error)
	NativeCollectionsIssued(address, cursor string, size int, zeroConf bool) ([]*mrc721nft.SearchRow, int, string, error)
}

// SetNativeCollectionSource hands the search / issuer endpoints to the
// native read model (called from the daemon startup after both aggregators
// registered).
func (a *Aggregator) SetNativeCollectionSource(src NativeCollectionSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nativeSource = src
}

// nativeCollectionSource snapshots the optional source for a request.
func (a *Aggregator) nativeCollectionSource() NativeCollectionSource {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.nativeSource
}

// SetOfferEnabled turns the /protocols/mrc721offer passive-offer index on
// or off (called from the daemon startup before Register; default off —
// both the ingest and the routes obey the gate).
func (a *Aggregator) SetOfferEnabled(enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.offerIndexEnabled = enabled
}

// nativeCollectionListData is the native-source variant of
// collectionListData (identical JSON shape; rows come pre-shaped from
// mrc721nft).
type nativeCollectionListData struct {
	List   []*mrc721nft.SearchRow `json:"list"`
	Total  int                    `json:"total"`
	Cursor string                 `json:"cursor,omitempty"`
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

// HandleBlockPin folds a confirmed pin into the sell-intent index and, when
// the offer gate is on, the passive-offer index. Pins from other paths are
// ignored; the registration runs in chain order, which is what the
// latest-pin-wins rules consume.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, err := a.ingestSellPin(pin); err != nil {
		return nil, err
	}
	if _, err := a.ingestOfferPin(pin); err != nil {
		return nil, err
	}
	return nil, nil
}

// HandleMempoolPin is a deliberate no-op. The sell and offer protocols are
// append-only declaration logs whose semantics are "latest pin by chain
// order wins"; folding unconfirmed pins into a latest-wins record would let
// a mempool re-declare overwrite a confirmed cancellation with no recovery
// path. Confirmed ingestion keeps the indexes honest; latency is one
// confirmation.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

// RegisterRoutes mounts the mrc721 query API on the given group (the /api
// group in the daemon router; /api prefix included by the caller). The
// passive-offer routes mount only when the offer gate is on. The gate is
// written once at startup (SetOfferEnabled before Register) and never
// mutated afterwards, so direct field reads are safe everywhere — the
// ingest callers already hold a.mu, and a locked getter here would
// dead-lock against them.
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/mrc721/sell/intents", a.handleSellIntents)
	router.GET("/mrc721/sell/intent/info", a.handleSellIntentInfo)
	router.GET("/mrc721/collection/search", a.handleCollectionSearch)
	router.GET("/mrc721/address/collections-issued", a.handleAddressCollectionsIssued)
	if a.offerIndexEnabled {
		router.GET("/mrc721/offers", a.handleOffers)
		router.GET("/mrc721/offer/info", a.handleOfferInfo)
	}
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
