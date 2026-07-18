package groupchat

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Aggregator implements the aggregator.Aggregator interface for group chat.
// It provides PebbleDB persistence, HTTP query APIs, and Socket.IO push integration.
type Aggregator struct {
	store    *storage.PebbleStore
	cache    *cache.Cache[[]byte]
	notifyCh chan *aggregator.NotifyEvent
}

const (
	namespace       = "groupchat"
	cacheMaxEntries = 2000
	cacheTTL        = 5 * time.Minute
)

func (a *Aggregator) Name() string { return "groupchat" }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	a.cache = cacheProvider.Namespace(namespace, cacheMaxEntries, cacheTTL)
	a.notifyCh = make(chan *aggregator.NotifyEvent, 256)
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// HandleBlockPin processes a confirmed (on-chain) pin.
// Dispatches to the appropriate handler and returns a NotifyEvent for socket push.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	event, err := a.dispatchPin(pin)
	if err != nil {
		return nil, err
	}
	// If dispatchPin returned an event, send it to the notify channel
	if event != nil {
		a.sendNotifyEvent(event)
	}
	return event, err
}

// HandleMempoolPin processes a mempool (unconfirmed) pin.
// Dispatches to the appropriate handler for real-time push.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	event, err := a.dispatchPin(pin)
	if err != nil {
		return nil, err
	}
	if event != nil {
		a.sendNotifyEvent(event)
	}
	return event, err
}

// RegisterRoutes mounts all group-chat HTTP endpoints on the router.
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	registerRoutes(a, router)
}
