// Package botsearch implements the read-only bot search aggregator behind
// POST /api/bots/search, the staffing query consumed by the IDBots Twin Bot.
// It indexes nothing itself: profiles come from the userinfo aggregator,
// group-task history from groupchat, published skills from skillservice, and
// online state from the presence readers — all injected via setters.
// See docs/specs/2026-08-22-bot-search-api.md for the contract.
package botsearch

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/groupchat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Business error codes of the /api/bots/search contract. HTTP stays 200 for
// envelope compatibility with the other aggregation endpoints.
const (
	codeInvalidQuery        = 1001
	codePresenceUnavailable = 1002
	codeInternalFailure     = 1003
)

// ProfileSource supplies the searchable profile corpus (userinfo aggregator).
type ProfileSource interface {
	BotSearchProfiles() []userinfo.BotSearchProfile
}

// GroupHistorySource supplies per-identity group create/join history
// (groupchat aggregator).
type GroupHistorySource interface {
	GroupHistoryForIdentity(identities ...string) ([]groupchat.GroupHistoryItem, error)
}

// SkillLister supplies a provider's visible published skill-service names
// (skillservice aggregator).
type SkillLister interface {
	PublishedServiceNames(providerGlobalMetaId string) ([]string, error)
}

// Aggregator is a read-only aggregator: HandleBlockPin/HandleMempoolPin are
// no-ops and it owns no Pebble data.
type Aggregator struct {
	notifyCh       chan *aggregator.NotifyEvent
	profiles       ProfileSource
	history        GroupHistorySource
	skills         SkillLister
	localPresence  presence.LocalReader
	globalPresence presence.GlobalReader
	now            func() int64 // unix milliseconds; test hook
}

func (a *Aggregator) Name() string { return "botsearch" }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	if a.now == nil {
		a.now = func() int64 { return time.Now().UnixMilli() }
	}
	return nil
}

// HandleBlockPin is a no-op: botsearch aggregates nothing of its own.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

// HandleMempoolPin is a no-op: botsearch aggregates nothing of its own.
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.POST("/bots/search", a.handleSearch)
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

func (a *Aggregator) SetProfileSource(source ProfileSource) {
	a.profiles = source
}

func (a *Aggregator) SetGroupHistorySource(source GroupHistorySource) {
	a.history = source
}

func (a *Aggregator) SetSkillLister(lister SkillLister) {
	a.skills = lister
}

// SetPresenceReaders injects the presence backends, mirroring the bothomepage
// aggregator wiring (local socket manager + optional federation global reader).
func (a *Aggregator) SetPresenceReaders(local presence.LocalReader, global presence.GlobalReader) {
	a.localPresence = local
	a.globalPresence = global
}
