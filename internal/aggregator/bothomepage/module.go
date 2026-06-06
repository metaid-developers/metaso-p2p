package bothomepage

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	namespace       = "bothomepage"
	cacheMaxEntries = 1000
	cacheTTL        = 30 * time.Second
)

// Aggregator implements the read-only Bot homepage aggregation module.
type Aggregator struct {
	store          *storage.PebbleStore
	cache          *cache.Cache[[]byte]
	notifyCh       chan *aggregator.NotifyEvent
	now            func() int64
	profileLookup  ProfileLookup
	serviceLister  ServiceLister
	localPresence  presence.LocalReader
	globalPresence presence.GlobalReader
	assetResolver  *skillservice.AssetResolver
	assetBaseURL   string
}

func (a *Aggregator) Name() string { return namespace }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	if cacheProvider != nil {
		a.cache = cacheProvider.Namespace(namespace, cacheMaxEntries, cacheTTL)
	}
	a.notifyCh = make(chan *aggregator.NotifyEvent, 1)
	if a.now == nil {
		a.now = func() int64 { return time.Now().UnixMilli() }
	}
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.profileLookup = lookup
}

func (a *Aggregator) SetServiceLister(lister ServiceLister) {
	a.serviceLister = lister
}

func (a *Aggregator) SetPresenceReaders(local presence.LocalReader, global presence.GlobalReader) {
	a.localPresence = local
	a.globalPresence = global
}

func (a *Aggregator) SetAssetBaseURL(baseURL string) {
	resolver := skillservice.NewAssetResolver(baseURL)
	a.assetResolver = resolver
	a.assetBaseURL = resolver.BaseURL()
}

func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/bot-homepage/globalmetaid/:globalMetaId", a.handleGlobalMetaID)
}
