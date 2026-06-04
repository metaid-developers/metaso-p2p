package aggregator

import (
	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/meta-socket/internal/cache"
	"github.com/metaid-developers/meta-socket/internal/storage"
)

// PinInscription is a parsed MetaID protocol entry from a blockchain transaction.
// This mirrors the core data type from the indexing pipeline.
type PinInscription struct {
	Id                 string `json:"id"`
	Number             int64  `json:"number"`
	Path               string `json:"path"`
	OriginalPath       string `json:"originalPath,omitempty"`
	Operation          string `json:"operation"` // init, create, modify, revoke
	ContentBody        []byte `json:"contentBody"`
	ContentType        string `json:"contentType"`
	Creator            string `json:"creator"`
	CreateAddress      string `json:"createAddress"`
	CreateMetaId       string `json:"createMetaId"`
	MetaId             string `json:"metaId"`
	GlobalMetaId       string `json:"globalMetaId"`
	Address            string `json:"address"`
	ChainName          string `json:"chainName"`
	GenesisTransaction string `json:"genesisTransaction"`
	GenesisHeight      int64  `json:"genesisHeight"`
	Timestamp          int64  `json:"timestamp"`
	Output             string `json:"output"`
	IsTransfered       bool   `json:"isTransfered"`
	OriginalId         string `json:"originalId,omitempty"`
	Host               string `json:"host"`
}

// PinEvent represents a parsed pin emitted by the indexer engine.
type PinEvent struct {
	Pin       *PinInscription
	ChainName string
	Height    int64 // 0 = mempool
	IsMempool bool
	Timestamp int64
}

// NotifyEvent is emitted by aggregators to trigger socket push.
type NotifyEvent struct {
	Type         string      // WS_SERVER_NOTIFY_GROUP_CHAT, etc.
	MetaId       string      // target user MetaId
	GlobalMetaId string      // target user GlobalMetaId
	TargetIds    []string    // all known target identities/aliases for user-directed pushes
	GroupId      string      // target group (for room broadcast)
	Payload      interface{} // notification body
}

// Aggregator is the interface each business module must implement.
// The indexer calls HandleBlockPin/HandleMempoolPin for each parsed pin.
// The HTTP layer calls RegisterRoutes to mount the module's API.
type Aggregator interface {
	// Name returns a unique name for this aggregator (e.g. "groupchat").
	Name() string

	// Init initializes the aggregator with its Pebble store and cache provider.
	Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error

	// HandleBlockPin processes a confirmed (on-chain) pin.
	// Returns a NotifyEvent if this pin should trigger a socket push.
	HandleBlockPin(pin *PinInscription) (*NotifyEvent, error)

	// HandleMempoolPin processes a mempool (unconfirmed) pin.
	HandleMempoolPin(pin *PinInscription) (*NotifyEvent, error)

	// RegisterRoutes mounts the aggregator's HTTP API on the given router group.
	RegisterRoutes(router *gin.RouterGroup)

	// NotifyChannel returns a channel the aggregator sends NotifyEvents on.
	// The socket layer reads from this to push to clients.
	NotifyChannel() <-chan *NotifyEvent
}

// Registry manages the set of registered aggregators.
type Registry struct {
	aggregators []Aggregator
	store       *storage.PebbleStore
	cache       *cache.CacheProvider
}

// NewRegistry creates an empty aggregator registry.
func NewRegistry(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) *Registry {
	return &Registry{
		store: store,
		cache: cacheProvider,
	}
}

// Register adds an aggregator to the registry and initializes it.
func (r *Registry) Register(a Aggregator) error {
	if err := a.Init(r.store, r.cache); err != nil {
		return err
	}
	r.aggregators = append(r.aggregators, a)
	return nil
}

// All returns all registered aggregators.
func (r *Registry) All() []Aggregator {
	return r.aggregators
}

// RouteBlockPin dispatches a confirmed pin to all registered aggregators.
func (r *Registry) RouteBlockPin(pin *PinInscription) []*NotifyEvent {
	var events []*NotifyEvent
	for _, a := range r.aggregators {
		evt, err := a.HandleBlockPin(pin)
		if err != nil {
			continue
		}
		if evt != nil {
			events = append(events, evt)
		}
	}
	return events
}

// RouteMempoolPin dispatches a mempool pin to all registered aggregators.
func (r *Registry) RouteMempoolPin(pin *PinInscription) []*NotifyEvent {
	var events []*NotifyEvent
	for _, a := range r.aggregators {
		evt, err := a.HandleMempoolPin(pin)
		if err != nil {
			continue
		}
		if evt != nil {
			events = append(events, evt)
		}
	}
	return events
}
