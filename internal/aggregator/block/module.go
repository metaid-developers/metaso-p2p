package block

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Aggregator folds Simpleblock pins (both the registered
// /protocols/simpleblock protocol and the /private/block/simpleprivateblock
// compatibility spelling) into a bidirectional block read model and serves
// the unified filter layer (spec §1.4/§4.4/§4.5).
type Aggregator struct {
	store           *storage.PebbleStore
	cache           *cache.Cache[[]byte]
	notifyCh        chan *aggregator.NotifyEvent
	profileLookup   ProfileLookup
	chatBlockBridge ChatBlockBridge
	pendingSeq      uint64 // mempool overlay arrival counter (LWW)
}

const (
	namespace       = "block"
	cacheMaxEntries = 2000
	cacheTTL        = 5 * time.Minute
)

// IdentityRef is the identity slice the block aggregator needs from the
// userinfo profile store (mirrors the social TargetRef shape).
type IdentityRef struct {
	MetaId       string
	GlobalMetaId string
	Address      string
	Name         string
	NameId       string
	AvatarId     string
	Bio          string
	BioId        string
}

// ProfileLookup resolves identity references to profiles; it mirrors the
// social/privatechat lookup surface so main.go can adapt userinfo once per
// aggregator.
type ProfileLookup interface {
	LookupByMetaId(metaId string) (*IdentityRef, error)
	LookupByGlobalMetaId(globalMetaId string) (*IdentityRef, error)
	LookupByAddress(address string) (*IdentityRef, error)
}

func (a *Aggregator) Name() string { return namespace }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	a.cache = cacheProvider.Namespace(namespace, cacheMaxEntries, cacheTTL)
	a.notifyCh = make(chan *aggregator.NotifyEvent, 64)
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// HandleBlockPin processes a confirmed (on-chain) pin; the confirmed read
// model remains the source of truth, and the fold consumes the pin's
// mempool overlay entry (spec §4.4b).
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if err := a.IngestBlockPin(pin); err != nil {
		return nil, err
	}
	return nil, nil
}

// HandleMempoolPin folds unconfirmed simpleblock pins into the mempool
// overlay so block effects take effect immediately (spec §4.4b, OQ-2
// reversed 2026-10-30). The notify session-cache bridge stays
// confirmed-only; chat filtering is mempool-fast through the read-side
// union, which consults the overlay on every read.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if err := a.IngestMempoolPin(pin); err != nil {
		return nil, err
	}
	return nil, nil
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	registerRoutes(a, router)
}

// SetProfileLookup wires the userinfo-backed identity resolver.
func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.profileLookup = lookup
}

// SetChatBlockBridge wires the notify event bridge (spec §4.6). Called from
// main.go after both aggregators are constructed; the bridge is optional —
// without it the read model still folds pins but the notify session cache
// is not mirrored.
func (a *Aggregator) SetChatBlockBridge(bridge ChatBlockBridge) {
	a.chatBlockBridge = bridge
}
