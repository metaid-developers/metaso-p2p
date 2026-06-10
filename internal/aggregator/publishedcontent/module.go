package publishedcontent

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type Aggregator struct {
	store    *storage.PebbleStore
	cache    *cache.Cache[[]byte]
	notifyCh chan *aggregator.NotifyEvent
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

func (a *Aggregator) RegisterRoutes(_ *gin.RouterGroup) {
	// Task 3 only builds the internal aggregation surface. Task 7 wires public
	// router exposure.
}
