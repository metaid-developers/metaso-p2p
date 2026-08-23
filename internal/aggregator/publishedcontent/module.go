package publishedcontent

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type Aggregator struct {
	store         *storage.PebbleStore
	cache         *cache.Cache[[]byte]
	notifyCh      chan *aggregator.NotifyEvent
	indexMu       sync.Mutex
	profileLookup MetaAppProfileLookup

	// searchDocs holds the warm, copy-on-write search-document snapshot
	// consumed by the metaweb unified search aggregator (see searchdoc.go).
	searchDocs      atomic.Value // []metawebdoc.Document, immutable once stored
	searchDocsMu    sync.Mutex   // guards searchDocsIndex copy-on-write updates
	searchDocsIndex map[string]metawebdoc.Document
}

const (
	cacheMaxEntries = 1000
	cacheTTL        = 5 * time.Minute
)

func (a *Aggregator) Name() string { return "publishedcontent" }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	a.cache = cacheProvider.Namespace(Namespace, cacheMaxEntries, cacheTTL)
	a.notifyCh = make(chan *aggregator.NotifyEvent, 1)
	if err := a.ensureHomepageMetaAppGlobalIndexes(); err != nil {
		return err
	}
	if err := a.ensureMetaAppTimeIndexes(); err != nil {
		return err
	}
	// Warm the search-document snapshot from the record store. A scan failure
	// is non-fatal: every subsequent record write re-folds its document.
	if err := a.rebuildSearchDocuments(); err != nil {
		log.Printf("WARNING: publishedcontent search document snapshot build failed: %v", err)
	}
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if err := a.processPin(pin, false); err != nil {
		return nil, err
	}
	return nil, nil
}

func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if err := a.processPin(pin, true); err != nil {
		return nil, err
	}
	return nil, nil
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	registerMetaAppRoutes(a, router)
}

func (a *Aggregator) HomepageMetaAppsCanonicalGlobalReady() (bool, error) {
	return a.homepageMetaAppsGlobalIdentityStateReady()
}
