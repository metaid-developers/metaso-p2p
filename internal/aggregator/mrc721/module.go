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
	"time"

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

	// PathTrade is the verified MRC-721 trade settlement receipt path (REQ-B-07).
	PathTrade = "/protocols/mrc721trade"

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

// SellPriceMe declares the ME fixed-price leg of a v2.1 sell intent (Contract C2.2).
type SellPriceMe struct {
	AmountMilli int64 `json:"amountMilli"`
}

// SellPriceFiat declares the fiat leg of a v2.1 sell intent (Contract C2.2).
type SellPriceFiat struct {
	Currency    string `json:"currency"`
	AmountCents int64  `json:"amountCents"`
}

// SellPrices carries optional multi-currency prices on /protocols/mrc721sell v2.1.
type SellPrices struct {
	Me   *SellPriceMe   `json:"me,omitempty"`
	Fiat *SellPriceFiat `json:"fiat,omitempty"`
}

// SellIntentRecord is the latest-wins projection of one itemPinId's
// declaration log entry. Seq is the local monotonic write counter used for
// the recency index and the pagination cursor.
type SellIntentRecord struct {
	ItemPinId          string        `json:"itemPinId"`
	SellPinId          string        `json:"sellPinId"`
	Active             bool          `json:"active"`
	InactiveReason     string        `json:"inactiveReason,omitempty"`
	CollectionName     string        `json:"collectionName,omitempty"`
	CollectionPinId    string        `json:"collectionPinId,omitempty"`
	ItemOutpoint       *ItemOutpoint `json:"itemOutpoint,omitempty"`
	SellerAddress      string        `json:"sellerAddress"`
	SellerMetaId       string        `json:"sellerMetaId,omitempty"`
	PriceHintSats      int64         `json:"priceHintSats,omitempty"`
	Prices             *SellPrices   `json:"prices,omitempty"`
	AutoFulfillMe      *bool         `json:"autoFulfillMe,omitempty"`
	Negotiable         bool          `json:"negotiable,omitempty"`
	ShopMetaAppPinId   string        `json:"shopMetaAppPinId,omitempty"`
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

	// Zero-conf projection markers (R-F-4): set on overlay-synthesized
	// records only, never persisted (the confirmed records keep their
	// stored JSON bytes unchanged).
	Overlay            bool  `json:"-"`
	OverlayFirstSeenAt int64 `json:"-"`
}

// EffectiveAutoFulfillMe returns true unless explicitly set to false (Contract C2.3).
func (r *SellIntentRecord) EffectiveAutoFulfillMe() bool {
	if r == nil || r.AutoFulfillMe == nil {
		return true
	}
	return *r.AutoFulfillMe
}

// CAReader resolves official CA certification for collections.
type CAReader interface {
	IsCollectionCertified(collectionPinId string) (certified bool, displayTitle string)
}

// SellerProfileSnapshot carries userinfo identity fields for market DTOs.
type SellerProfileSnapshot struct {
	Address       string
	GlobalMetaId  string
	MetaId        string
	Name          string
	Avatar        string
	HasChatPubkey bool
}

// ProfileReader resolves user profiles and globalMetaId -> addresses.
type ProfileReader interface {
	LookupProfile(identity string) *SellerProfileSnapshot
	ResolveAddressesByGlobalMetaId(globalMetaId string) []string
}

// PresenceReader resolves online status for an identity.
type PresenceReader interface {
	PresenceStatus(identity string) (online bool, lastSeenAgoSeconds *int64)
}

// NFTReader abstracts read-only queries against mrc721nft.
type NFTReader interface {
	ItemOwnership(itemPinId string, zeroConf bool) (ownerAddress string, outpoint string, burned bool, found bool)
	ItemSummary(itemPinId string, zeroConf bool) *mrc721nft.ItemSummaryInfo
	ListAllCollections(zeroConf bool) []*mrc721nft.Collection
	CollectionHolderStats(collectionPinId string, zeroConf bool) (holderCount int, counts map[string]int, err error)
	ItemTransferByTx(itemPinId, txId string) (*mrc721nft.ItemTransfer, bool)
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

	// Zero-confirmation declaration overlay state (R-F-4): in-memory only,
	// guarded by a.mu, rebuilt from the node mempool after a restart.
	zeroConfTrade      bool
	overlayDecls       map[string]*overlayDeclEntry // pinId → entry
	overlayTxids       map[string]int64             // live overlay txid → firstSeenAt
	overlayInvalidated map[string]string            // itemPinId → inactiveReason (mempool transfer/burn)

	// Cross-module readers wired at startup in main.go.
	caReader       CAReader
	profileReader  ProfileReader
	presenceReader PresenceReader
	nftReader      NFTReader
	nowFn          func() int64

	// Cached collection market summaries (invalidated on sell/transfer/trade writes).
	marketMu    sync.RWMutex
	marketCache *marketCacheSnapshot
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
	a.overlayDecls = make(map[string]*overlayDeclEntry)
	a.overlayTxids = make(map[string]int64)
	a.overlayInvalidated = make(map[string]string)
	if a.nowFn == nil {
		a.nowFn = func() int64 { return time.Now().Unix() }
	}
	return nil
}

// SetCAReader wires the official CA certification reader.
func (a *Aggregator) SetCAReader(reader CAReader) {
	a.mu.Lock()
	a.caReader = reader
	a.mu.Unlock()
	a.invalidateMarketCache()
}

// SetProfileReader wires the userinfo profile reader.
func (a *Aggregator) SetProfileReader(reader ProfileReader) {
	a.mu.Lock()
	a.profileReader = reader
	a.mu.Unlock()
}

// SetPresenceReader wires the presence status reader.
func (a *Aggregator) SetPresenceReader(reader PresenceReader) {
	a.mu.Lock()
	a.presenceReader = reader
	a.mu.Unlock()
}

// SetNFTReader wires the mrc721nft read model.
func (a *Aggregator) SetNFTReader(reader NFTReader) {
	a.mu.Lock()
	a.nftReader = reader
	a.mu.Unlock()
	a.invalidateMarketCache()
}

func (a *Aggregator) nowUnix() int64 {
	if a != nil && a.nowFn != nil {
		return a.nowFn()
	}
	return time.Now().Unix()
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
	sellChanged, err := a.ingestSellPin(pin)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	offerChanged, err := a.ingestOfferPin(pin)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	tradeChanged, err := a.ingestTradePin(pin)
	if err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if pin != nil {
		a.dropOverlayForTxLocked(txIdOfPinId(pin.Id))
	}
	a.mu.Unlock()
	if sellChanged || offerChanged || tradeChanged {
		a.invalidateMarketCache()
	}
	return nil, nil
}

// HandleMempoolPin records zero-conf declaration overlay effects.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	changed := a.recordMempoolDeclarationLocked(pin)
	a.mu.Unlock()
	if changed {
		a.invalidateMarketCache()
	}
	return nil, nil
}

// RegisterRoutes mounts the mrc721 query API on the given group (the /api
// group in the daemon router; /api prefix included by the caller).
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/mrc721/sell/intents", a.handleSellIntents)
	router.GET("/mrc721/sell/intent/info", a.handleSellIntentInfo)
	router.GET("/mrc721/collection/search", a.handleCollectionSearch)
	router.GET("/mrc721/collection/holders", a.handleCollectionHolders)
	router.GET("/mrc721/address/collections-issued", a.handleAddressCollectionsIssued)
	router.GET("/mrc721/market/collections", a.handleMarketCollections)
	router.GET("/mrc721/market/collection/:collectionPinId/summary", a.handleMarketCollectionSummary)
	router.GET("/mrc721/market/listings", a.handleMarketListings)
	router.GET("/mrc721/market/trades", a.handleMarketTrades)
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
