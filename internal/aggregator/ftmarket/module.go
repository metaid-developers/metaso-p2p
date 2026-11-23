// Package ftmarket indexes /protocols/ft-sell and /protocols/ft-trade pins,
// verifies UTXO liveness against higun (MVC) and man-p2p (BTC), projects
// official CA token verification, and serves the Contract C2 HTTP endpoints
// (/api/ft/market/*) plus MetaWeb search documents ("ft-token", "ft-listing").
package ftmarket

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	// Namespace is the Pebble namespace of the ftmarket read models.
	Namespace = "ftmarket"

	// PathFtSell is the fixed FT sell listing protocol path (Contract C1.1).
	PathFtSell = "/protocols/ft-sell"

	// PathFtTrade is the verified FT trade receipt protocol path (Contract C2.2).
	PathFtTrade = "/protocols/ft-trade"

	// OperationCreate is the create-only operation used by ft-sell and ft-trade.
	OperationCreate = "create"

	defaultVerifyCacheTTL = 5 * time.Second
	defaultListSize       = 50
	maxListSize           = 200
)

var (
	// ErrInvalidParameter indicates an invalid HTTP query or path parameter.
	ErrInvalidParameter = errors.New("invalid parameter")
	// ErrNotFound indicates a requested asset or record was not found.
	ErrNotFound = errors.New("not found")
)

// AssetInfo describes the fungible token asset in a listing or summary.
type AssetInfo struct {
	AssetKey   string  `json:"assetKey"`
	Chain      string  `json:"chain"`
	Standard   string  `json:"standard"`
	Codehash   *string `json:"codehash"`
	Genesis    *string `json:"genesis"`
	SensibleId *string `json:"sensibleId,omitempty"`
	Mrc20Id    *string `json:"mrc20Id"`
	Symbol     string  `json:"symbol"`
	TokenName  string  `json:"tokenName"`
	Decimals   int     `json:"decimals"`
	Icon       string  `json:"icon,omitempty"`
}

// LotInfo describes the exact token UTXO lot declared by a listing.
type LotInfo struct {
	AmountBase    string `json:"amountBase"`
	AmountDisplay string `json:"amountDisplay"`
	UtxoOutpoint  string `json:"utxoOutpoint"`
}

// MvcTokenSellRail describes the sCrypt TokenSell contract rail.
type MvcTokenSellRail struct {
	SellTxid             string `json:"sellTxid"`
	TokenListTxid        string `json:"tokenListTxid"`
	TokenListOutputIndex int64  `json:"tokenListOutputIndex"`
	PriceSats            int64  `json:"priceSats"`
	SellerAddress        string `json:"sellerAddress"`
	SeedTxid             string `json:"seedTxid,omitempty"`
	SeedVout             int64  `json:"seedVout"`
	PrevTokenTxid        string `json:"prevTokenTxid,omitempty"`
	ContractAddress      string `json:"contractAddress,omitempty"`
	TokenSellJson        string `json:"tokenSellJson,omitempty"`
}

// BtcPsbtRail describes an optional mirrored metaid.market PSBT rail.
type BtcPsbtRail struct {
	OrderId       string `json:"orderId,omitempty"`
	PriceSats     int64  `json:"priceSats"`
	UnitPriceSats string `json:"unitPriceSats,omitempty"`
	SellerAddress string `json:"sellerAddress,omitempty"`
	Psbt          string `json:"psbt,omitempty"`
}

// MeRail describes the ABS ME points escrow rail.
type MeRail struct {
	AmountMilli int64 `json:"amountMilli"`
	AutoFulfill bool  `json:"autoFulfill"`
}

// FiatRail describes an optional fiat escrow rail.
type FiatRail struct {
	Currency    string `json:"currency"`
	AmountCents int64  `json:"amountCents"`
}

// SellRails groups the settlement rails declared on a listing.
type SellRails struct {
	MvcTokenSell *MvcTokenSellRail `json:"mvcTokenSell"`
	BtcPsbt      *BtcPsbtRail      `json:"btcPsbt"`
	Me           *MeRail           `json:"me"`
	Fiat         *FiatRail         `json:"fiat"`
}

// SellListingRecord is the latest-wins persisted record for one listingKey.
type SellListingRecord struct {
	ListingKey       string     `json:"listingKey"`
	SellPinId        string     `json:"sellPinId"`
	Active           bool       `json:"active"`
	EffectiveActive  bool       `json:"effectiveActive"`
	InactiveReason   string     `json:"inactiveReason,omitempty"`
	Asset            *AssetInfo `json:"asset,omitempty"`
	Lot              *LotInfo   `json:"lot,omitempty"`
	SellerAddress    string     `json:"sellerAddress"`
	SellerBtcAddress string     `json:"sellerBtcAddress,omitempty"`
	SellerMetaId     string     `json:"sellerMetaId,omitempty"`
	Rails            *SellRails `json:"rails,omitempty"`
	AcceptedRails    []string   `json:"acceptedRails,omitempty"`
	BatchGroupId     string     `json:"batchGroupId,omitempty"`
	BatchIndex       int        `json:"batchIndex,omitempty"`
	BatchTotal       int        `json:"batchTotal,omitempty"`
	Note             string     `json:"note,omitempty"`
	CreatedAt        int64      `json:"createdAt,omitempty"`
	ExpiresAt        int64      `json:"expiresAt,omitempty"`

	// Chain provenance of the winning pin.
	ChainName string `json:"chainName,omitempty"`
	Height    int64  `json:"height,omitempty"`
	TxID      string `json:"txId,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Seq       uint64 `json:"seq"`

	// Overlay markers (mempool-only; not persisted).
	Overlay            bool  `json:"-"`
	OverlayFirstSeenAt int64 `json:"-"`
}

// TokenCatalogRecord stores the latest known metadata for an assetKey.
type TokenCatalogRecord struct {
	Asset     AssetInfo `json:"asset"`
	UpdatedAt int64     `json:"updatedAt"`
}

// CertifiedTokenInfo carries CA certification details for a token assetKey.
type CertifiedTokenInfo struct {
	AssetKey            string
	DisplayName         string
	Symbol              string
	TokenName           string
	Decimals            int
	Icon                string
	SensibleId          string
	BridgedPeerAssetKey string
	BridgedDualChain    bool
}

// CAReader resolves official CA certification for FT assets and official bots.
type CAReader interface {
	IsTokenCertified(assetKey string) (certified bool, info *CertifiedTokenInfo)
	ListCertifiedTokens() []CertifiedTokenInfo
	GetOfficialBotRole(globalMetaId string) (verified bool, role string)
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

// TokenChainMetadata holds optional holder/display metadata fetched from upstream indexers.
type TokenChainMetadata struct {
	HolderCount int
	Symbol      string
	TokenName   string
	Decimals    int
	Icon        string
	SensibleId  string
}

// TokenMetadataProvider optionally resolves holder counts and token metadata from higun/man-p2p.
type TokenMetadataProvider interface {
	FetchTokenMetadata(ctx context.Context, asset AssetInfo) (*TokenChainMetadata, bool)
}

// BtcFloorProvider optionally fetches cached BTC PSBT floor price (in sats per display token)
// from api.metaid.market for a given mrc20Id.
type BtcFloorProvider interface {
	FetchBtcUnitFloorSats(ctx context.Context, mrc20Id string) (*string, bool)
}

// Aggregator indexes /protocols/ft-sell and /protocols/ft-trade and serves /api/ft/market/*.
type Aggregator struct {
	store    *storage.PebbleStore
	notifyCh chan *aggregator.NotifyEvent
	mu       sync.Mutex

	zeroConfTrade        bool
	overlayDecls         map[string]*overlayDeclEntry
	overlayTxids         map[string]int64
	overlayInvalidated   map[string]string // listingKey -> reason
	overlayPendingTrades map[string]*TradeRecord

	caReader       CAReader
	profileReader  ProfileReader
	presenceReader PresenceReader
	utxoVerifier   UTXOVerifier
	metaProvider   TokenMetadataProvider
	btcFloorProv   BtcFloorProvider
	assetBaseURL   string
	builtinCA      map[string]CertifiedTokenInfo

	verifyCacheMu  sync.Mutex
	verifyCache    map[string]cachedUTXOCheck
	verifyCacheTTL time.Duration

	tokenMetaMu    sync.Mutex
	tokenMetaCache map[string]cachedTokenMeta

	marketMu    sync.RWMutex
	marketCache *marketCacheSnapshot

	nowFn func() int64
}

type cachedUTXOCheck struct {
	checkedAt time.Time
	result    UTXOLivenessResult
}

type cachedTokenMeta struct {
	fetchedAt time.Time
	meta      *TokenChainMetadata
}

// Name implements aggregator.Aggregator.
func (a *Aggregator) Name() string { return Namespace }

// Init implements aggregator.Aggregator.
func (a *Aggregator) Init(store *storage.PebbleStore, _ *cache.CacheProvider) error {
	a.store = store
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	a.overlayDecls = make(map[string]*overlayDeclEntry)
	a.overlayTxids = make(map[string]int64)
	a.overlayInvalidated = make(map[string]string)
	a.overlayPendingTrades = make(map[string]*TradeRecord)
	a.verifyCache = make(map[string]cachedUTXOCheck)
	a.tokenMetaCache = make(map[string]cachedTokenMeta)
	if a.verifyCacheTTL <= 0 {
		a.verifyCacheTTL = defaultVerifyCacheTTL
	}
	if a.nowFn == nil {
		a.nowFn = func() int64 { return time.Now().Unix() }
	}
	if a.store != nil {
		_, _ = a.store.OpenDB(Namespace)
		a.mu.Lock()
		a.migrateLegacyMvcSensibleKeysLocked()
		a.mu.Unlock()
	}
	return nil
}

// SetZeroConfTrade enables or disables zero-confirmation mempool overlay indexing.
func (a *Aggregator) SetZeroConfTrade(enabled bool) {
	a.mu.Lock()
	a.zeroConfTrade = enabled
	a.mu.Unlock()
	a.invalidateMarketCache()
}

// SetCAReader wires the official CA registry reader.
func (a *Aggregator) SetCAReader(reader CAReader) {
	a.mu.Lock()
	a.caReader = reader
	a.mu.Unlock()
	a.invalidateMarketCache()
}

// SetBuiltinCertifiedTokens configures built-in well-known certified token mappings.
func (a *Aggregator) SetBuiltinCertifiedTokens(tokens map[string]CertifiedTokenInfo) {
	a.mu.Lock()
	cp := make(map[string]CertifiedTokenInfo, len(tokens))
	for k, v := range tokens {
		rawKey := strings.ToLower(strings.TrimSpace(k))
		if rawKey == "" {
			rawKey = strings.ToLower(strings.TrimSpace(v.AssetKey))
		}
		if rawKey == "" {
			continue
		}
		v.AssetKey = rawKey
		v.SensibleId = strings.ToLower(strings.TrimSpace(v.SensibleId))
		registerCertSensibleMapping(v)
		key := NormalizeAssetKey(rawKey)
		v.AssetKey = key
		if v.BridgedPeerAssetKey != "" {
			v.BridgedPeerAssetKey = NormalizeAssetKey(v.BridgedPeerAssetKey)
			v.BridgedDualChain = true
		}
		cp[key] = v
	}
	a.builtinCA = cp
	a.migrateLegacyMvcSensibleKeysLocked()
	a.mu.Unlock()
	a.invalidateMarketCache()
}

// SetProfileReader wires the userinfo profile reader.
func (a *Aggregator) SetProfileReader(reader ProfileReader) {
	a.mu.Lock()
	a.profileReader = reader
	a.mu.Unlock()
}

// SetPresenceReader wires the presence reader.
func (a *Aggregator) SetPresenceReader(reader PresenceReader) {
	a.mu.Lock()
	a.presenceReader = reader
	a.mu.Unlock()
}

// SetUTXOVerifier wires the chain UTXO liveness verifier.
func (a *Aggregator) SetUTXOVerifier(verifier UTXOVerifier) {
	a.mu.Lock()
	a.utxoVerifier = verifier
	a.mu.Unlock()
	a.verifyCacheMu.Lock()
	a.verifyCache = make(map[string]cachedUTXOCheck)
	a.verifyCacheMu.Unlock()
	a.invalidateMarketCache()
}

// SetVerifyCacheTTL configures the read-through UTXO verification cache TTL.
func (a *Aggregator) SetVerifyCacheTTL(ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	a.mu.Lock()
	a.verifyCacheTTL = ttl
	a.mu.Unlock()
}

// SetTokenMetadataProvider wires optional upstream token metadata / holder count lookups.
func (a *Aggregator) SetTokenMetadataProvider(provider TokenMetadataProvider) {
	a.mu.Lock()
	a.metaProvider = provider
	a.mu.Unlock()
	a.invalidateMarketCache()
}

// SetBtcFloorProvider wires optional BTC PSBT floor snapshot lookups.
func (a *Aggregator) SetBtcFloorProvider(provider BtcFloorProvider) {
	a.mu.Lock()
	a.btcFloorProv = provider
	a.mu.Unlock()
	a.invalidateMarketCache()
}

// SetAssetBaseURL sets the base URL for resolving metafile:// asset icons.
func (a *Aggregator) SetAssetBaseURL(baseURL string) {
	a.mu.Lock()
	a.assetBaseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	a.mu.Unlock()
	a.invalidateMarketCache()
}

// RefreshMarketCache invalidates cached token market summaries.
func (a *Aggregator) RefreshMarketCache() {
	a.invalidateMarketCache()
}

func (a *Aggregator) nowUnix() int64 {
	if a != nil && a.nowFn != nil {
		return a.nowFn()
	}
	return time.Now().Unix()
}

// NotifyChannel implements aggregator.Aggregator.
func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// HandleBlockPin implements aggregator.Aggregator.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.preResolvePinSensibleMapping(pin)
	a.mu.Lock()
	sellChanged, err := a.ingestSellPin(pin)
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
	if sellChanged || tradeChanged {
		a.invalidateMarketCache()
	}
	return nil, nil
}

// HandleMempoolPin implements aggregator.Aggregator.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.preResolvePinSensibleMapping(pin)
	a.mu.Lock()
	changed := a.recordMempoolDeclarationLocked(pin)
	a.mu.Unlock()
	if changed {
		a.invalidateMarketCache()
	}
	return nil, nil
}

// RegisterRoutes mounts the Contract C2 FT market HTTP routes under /api.
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/ft/market/tokens", a.handleMarketTokens)
	router.GET("/ft/market/token/:assetKey/summary", a.handleMarketTokenSummary)
	router.GET("/ft/market/listings", a.handleMarketListings)
	router.GET("/ft/market/trades", a.handleMarketTrades)
}

func normalisedPinPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	if idx := strings.Index(path, "@"); idx >= 0 {
		path = path[:idx]
	}
	return strings.TrimRight(path, "/")
}

func unixSeconds(ts int64) int64 {
	if ts > 100000000000 {
		return ts / 1000
	}
	return ts
}

func txIdOfPinId(pinId string) string {
	pinId = strings.TrimSpace(pinId)
	if idx := strings.Index(pinId, "i"); idx == 64 {
		return strings.ToLower(pinId[:64])
	}
	return strings.ToLower(pinId)
}
