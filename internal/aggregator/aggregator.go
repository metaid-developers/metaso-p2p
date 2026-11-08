package aggregator

import (
	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
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
	ContentSummary     string `json:"contentSummary,omitempty"`
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
	// TxIndex is the transaction's position within its block (0 = coinbase),
	// carried so out-of-order staging can replay mints in true chain order
	// (R-F-10). Mempool pins carry a fetch-order index that has no chain
	// meaning; their GenesisHeight is -1 and they never stage.
	TxIndex int64 `json:"txIndex,omitempty"`
	// Inputs lists the transaction's previous-output pointers ("txid:vout"),
	// one per input. Zero-conf consumers derive tx ancestry from it (the
	// parent→child mint chains behind numberFinal, R-F-5); the txids here
	// are the wire-hash convention of the raw transaction layer.
	Inputs    []string `json:"inputs,omitempty"`
	Timestamp int64    `json:"timestamp"`
	Output       string `json:"output"`
	OutputValue  int64  `json:"outputValue,omitempty"`
	IsTransfered bool   `json:"isTransfered"`
	OriginalId   string `json:"originalId,omitempty"`
	Host         string `json:"host"`
	// IsOpReturn marks pins parsed from an MVC OP_RETURN output. The MVC
	// witness envelope is a separate parse path that man-p2p's MVC adapter
	// never feeds into its pin store, so the mrc721nft aggregate consumes
	// only OP_RETURN-sourced MVC pins to keep its pin universe identical to
	// man-p2p's.
	IsOpReturn bool `json:"isOpReturn,omitempty"`
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
	PinId        string      // source pin for delivery tracing
	Payload      interface{} // notification body
}

// TransferNotice describes one detected spend of a tracked output, as
// derived by a chain adapter's transfer detection (mirror of man-p2p's
// pin.PinTransferInfo). Output/Location use the chain's ledger txid
// convention (for MVC version>=10 txs the reordered hash).
type TransferNotice struct {
	FromAddress string
	ToAddress   string // new owner address ("" on unresolvable burn)
	Output      string // "<txid>:<vout>" the NFT now sits at
	OutputValue int64  // sat value of that output
	Offset      uint64
	Location    string // spend tx pointer (ledger convention)
	Meltdown    bool
}

// TrackedOutput is one spendable output an aggregator currently tracks,
// keyed by outpoint "txid:vout" in the map returned by TransferConsumer.
type TrackedOutput struct {
	Owner string // current owning address
	Ref   string // aggregator-local reference (e.g. the item pin id)
}

// TransferConsumer is an optional Aggregator extension for modules that
// track spendable outputs (NFT-style UTXO ownership). The engine collects
// the tracked outputs to feed the chain adapter's transfer detection and
// routes the detected spends back per confirmed block.
type TransferConsumer interface {
	// TrackedOutputs returns the outputs currently tracked, keyed by
	// outpoint "txid:vout" → owning address.
	TrackedOutputs() map[string]TrackedOutput

	// HandleBlockTransfers consumes the spends detected in one confirmed
	// block (keyed by spent outpoint). Notices for outputs this consumer
	// does not track are ignored.
	HandleBlockTransfers(chainName string, height int64, transfers map[string]TransferNotice)

	// HandleMempoolSpends consumes one full mempool-scan round (engine
	// ticker). Each call is a complete round: the consumer records the
	// observed unconfirmed spends (advisory pending channel) and reconciles
	// away its pendings for this chain that the round no longer observes
	// (confirmed → settled by the block hook; dropped/RBF → cleaned).
	HandleMempoolSpends(chainName string, spends map[string]TransferNotice)
}

// MempoolOverlayConsumer is an optional TransferConsumer extension for
// zero-confirmation read surfaces (R-F): the engine feeds incremental
// mempool rounds' spend notices on every poll (no reconcile — a partial
// observation must never evict overlay state), and hands each full round's
// observed txid set to the overlay so entries whose transaction left the
// mempool without confirming are dropped (recomputed, never mutated — a
// dropped tx's effects vanish, revealing the confirmed state beneath).
type MempoolOverlayConsumer interface {
	// HandleMempoolSpendsIncremental consumes the spends detected in one
	// incremental mempool poll over the freshly fetched transactions.
	HandleMempoolSpendsIncremental(chainName string, spends map[string]TransferNotice)

	// ReconcileMempoolRound consumes one fully observed mempool round's
	// txid set (ledger convention). Overlay entries whose txid is absent
	// are evicted; consumers only ever receive sets from complete fetches.
	ReconcileMempoolRound(chainName string, observedTxIDs []string)
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

// TransferConsumers returns the registered aggregators that implement the
// optional TransferConsumer extension (spendable-output trackers).
func (r *Registry) TransferConsumers() []TransferConsumer {
	var consumers []TransferConsumer
	for _, a := range r.aggregators {
		if consumer, ok := a.(TransferConsumer); ok {
			consumers = append(consumers, consumer)
		}
	}
	return consumers
}
