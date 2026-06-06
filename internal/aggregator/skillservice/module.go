package skillservice

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Aggregator implements aggregator.Aggregator for the Bot Hub skill-service
// API. It indexes /protocols/skill-service and /protocols/skill-service-rate
// PINs, folds version chains by originalId within a single chain, and serves
// `/api/bot-hub/skill-service/*`.
//
// The aggregator never emits NotifyEvents: v1 does not push skill-service
// changes over Socket.IO. The Bot Hub frontend polls the HTTP API and the
// 30s p95 fresh-view target is met by the indexer scan loop cadence.
type Aggregator struct {
	store         *storage.PebbleStore
	cache         *cache.Cache[[]byte]
	notifyCh      chan *aggregator.NotifyEvent
	profileLookup ProfileLookup // nil-safe; see ResolveProvider
	assetResolver *AssetResolver
}

const (
	cacheMaxEntries = 2000
	// cacheTTL is a real time.Duration. Five minutes is plenty for the
	// list / detail read path, and any update is small (a single record
	// rewrite) so invalidating on write keeps things fresh.
	cacheTTL = 5 * time.Minute
)

// Name returns the aggregator name. Must remain stable: it is used as the
// Pebble namespace and as a route ownership marker in internal/api/router.go.
func (a *Aggregator) Name() string { return "skillservice" }

// Init wires in the shared Pebble store and cache provider. The aggregator
// does not need its own goroutines beyond the standard NotifyChannel; the
// indexer engine calls HandleBlockPin / HandleMempoolPin synchronously.
func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	a.cache = cacheProvider.Namespace(NamespaceService, cacheMaxEntries, cacheTTL)
	// Buffer 1 so we satisfy the Aggregator interface without ever
	// actually sending; closed-channel semantics are not required.
	a.notifyCh = make(chan *aggregator.NotifyEvent, 1)
	return nil
}

// NotifyChannel is required by the Aggregator interface. The skill-service
// aggregator does not push over Socket.IO in v1, so this channel never
// receives events; the socket consumer waits on it harmlessly.
func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// HandleBlockPin processes a confirmed pin. processPin classifies the pin
// by Path and routes to service / rating handling. We always return nil for
// the NotifyEvent because v1 does not push.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if err := a.processPin(pin); err != nil {
		return nil, err
	}
	return nil, nil
}

// HandleMempoolPin treats unconfirmed pins exactly like block pins for v1:
// we want pending services to appear in the list (status=1) within the 30s
// p95 target. The indexer engine still drives whether a mempool pin is
// promoted to confirmed; that promotion just re-enters processPin.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if err := a.processPin(pin); err != nil {
		return nil, err
	}
	return nil, nil
}

// RegisterRoutes mounts /bot-hub/skill-service/* on the supplied router
// group. The router in internal/api/router.go calls this with the /api
// group, so the externally visible paths are
//
//	GET /api/bot-hub/skill-service/list
//	GET /api/bot-hub/skill-service/detail/:serviceId
//
// M1 ships placeholder handlers that return code=0 with an empty payload;
// real handlers land in M5 (list) and M6 (detail).
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	registerRoutes(a, router)
}

// SetAssetBaseURL configures the base URL used to expand on-chain pin id /
// metafile references into HTTP URLs the frontend can load directly. It is
// safe to leave unset: ResolveAsset will return the raw input unchanged,
// which is convenient during isolated unit tests but never desirable in
// production. main.go wires this from config.BotHubConfig.AssetBaseURL.
func (a *Aggregator) SetAssetBaseURL(baseURL string) {
	a.assetResolver = NewAssetResolver(baseURL)
}

// ResolveAsset converts a chain-declared asset reference into a load-ready
// URL. Pass-through when no resolver is set so behaviour stays predictable
// in tests that exercise the aggregator without going through main.go.
func (a *Aggregator) ResolveAsset(asset string) string {
	if a.assetResolver == nil {
		return asset
	}
	return a.assetResolver.Resolve(asset)
}
