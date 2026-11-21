// Package paybuzz indexes /protocols/paybuzz encrypted paid-reading pins
// (Contract C6.1, REQ-5-02), folding owner-verified create/modify version
// chains in PebbleDB + L1/L2 cache, serving GET /api/paybuzz/info and
// GET /api/paybuzz/list, and exposing public titles and 300-char previews to
// unified MetaWeb search via metaweb.DocumentSource (protocol = "paybuzz").
package paybuzz

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	// Namespace is the PebbleDB namespace owned by the paybuzz aggregator.
	Namespace = "paybuzz"

	// PathPayBuzz is the canonical MetaID pin path for encrypted articles.
	PathPayBuzz = "/protocols/paybuzz"

	OperationCreate = "create"
	OperationModify = "modify"

	defaultListSize = 20
	maxListSize     = 100

	codeInvalidParam = 40000
	codeNotFound     = 40400
	codeUnavailable  = 50000

	infoCacheNamespace = "paybuzz_info"
	infoCacheMaxItems  = 2048
	infoCacheTTL       = 30 * time.Second
)

// EncryptionMeta carries the AES-256-GCM metadata declared on-chain (symmetric
// content key K is never on-chain).
type EncryptionMeta struct {
	Algo                  string `json:"algo"`
	IvHex                 string `json:"ivHex"`
	AuthTagHex            string `json:"authTagHex"`
	CipherSha256          string `json:"cipherSha256"`
	KeySaltHex            string `json:"keySaltHex,omitempty"`
	CiphertextPinOrBase64 string `json:"ciphertextPinOrBase64"`
}

// TokenPriceRule declares one FT micropayment unlock option (Contract C6.1).
type TokenPriceRule struct {
	AssetKey       string `json:"assetKey"`
	Chain          string `json:"chain"`
	Standard       string `json:"standard"`
	Mrc20Id        string `json:"mrc20Id,omitempty"`
	Codehash       string `json:"codehash,omitempty"`
	Genesis        string `json:"genesis,omitempty"`
	Symbol         string `json:"symbol"`
	Decimals       int    `json:"decimals"`
	AmountBase     string `json:"amountBase"`
	AmountDisplay  string `json:"amountDisplay"`
	ReceiveAddress string `json:"receiveAddress"`
}

// VipCollectionRule declares one MRC-721 membership collection whose holders
// can unlock the article at zero cost via signature verification.
type VipCollectionRule struct {
	CollectionPinId string `json:"collectionPinId"`
	CollectionName  string `json:"collectionName"`
}

// UnlockRules holds dual-track unlock conditions for a PayBuzz article.
type UnlockRules struct {
	MePriceMilli         *int64              `json:"mePriceMilli,omitempty"`
	TokenPrices          []TokenPriceRule    `json:"tokenPrices"`
	VipMrc721Collections []VipCollectionRule `json:"vipMrc721Collections"`
}

// AuthorProfileSnapshot carries userinfo identity enrichment fields.
type AuthorProfileSnapshot struct {
	GlobalMetaId string
	MetaId       string
	Address      string
	Name         string
	Avatar       string
}

// ProfileLookup resolves author display names, avatars, and cross-identity
// mappings from the userinfo aggregator.
type ProfileLookup interface {
	LookupByGlobalMetaId(globalMetaId string) (*AuthorProfileSnapshot, error)
	LookupByMetaId(metaId string) (*AuthorProfileSnapshot, error)
	LookupByAddress(address string) (*AuthorProfileSnapshot, error)
}

// Record is the folded latest-valid projection of one /protocols/paybuzz
// version chain persisted in PebbleDB.
type Record struct {
	SourcePinId      string         `json:"sourcePinId"`
	CurrentPinId     string         `json:"currentPinId"`
	ChainName        string         `json:"chainName"`
	Title            string         `json:"title"`
	Preview          string         `json:"preview"`
	ContentType      string         `json:"contentType"`
	WordCount        int            `json:"wordCount"`
	Encryption       EncryptionMeta `json:"encryption"`
	AuthorMetaId     string         `json:"authorMetaId"`
	AuthorMvcAddress string         `json:"authorMvcAddress"`
	ShopMetaAppPinId string         `json:"shopMetaAppPinId,omitempty"`
	UnlockRules      UnlockRules    `json:"unlockRules"`
	CreatedAt        int64          `json:"createdAt"`
	UpdatedAt        int64          `json:"updatedAt"`
	Height           int64          `json:"height"`
	Confirmed        bool           `json:"confirmed"`

	// Immutable creator identity stamped from the root create pin, used to
	// enforce owner-only modify folding.
	CreatorGlobalMetaId string `json:"creatorGlobalMetaId,omitempty"`
	CreatorMetaId       string `json:"creatorMetaId,omitempty"`
	CreatorAddress      string `json:"creatorAddress,omitempty"`
}

// Aggregator indexes /protocols/paybuzz and serves read/search models.
type Aggregator struct {
	store         *storage.PebbleStore
	infoCache     *cache.Cache[[]byte]
	notifyCh      chan *aggregator.NotifyEvent
	profileLookup ProfileLookup

	mu          sync.RWMutex
	searchMu    sync.RWMutex
	searchDocs  []metawebdoc.Document
	searchDirty bool
}

func (a *Aggregator) Name() string { return Namespace }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	if cacheProvider != nil {
		a.infoCache = cacheProvider.Namespace(infoCacheNamespace, infoCacheMaxItems, infoCacheTTL)
	}
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	a.searchDirty = true
	return nil
}

// SetProfileLookup wires the userinfo lookup adapter for author profile completion.
func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.mu.Lock()
	a.profileLookup = lookup
	a.mu.Unlock()
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, a.ingestPin(pin, false)
}

func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, a.ingestPin(pin, true)
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/paybuzz/info", a.handleInfo)
	router.GET("/paybuzz/list", a.handleList)
}
