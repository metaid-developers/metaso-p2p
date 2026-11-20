// Package botsearch implements the read-only bot search aggregator behind
// POST /api/bots/search, the staffing query consumed by the IDBots Twin Bot.
// It indexes nothing itself: profiles come from the userinfo aggregator,
// group-task history from groupchat, published skills from skillservice, and
// online state from the presence readers — all injected via setters.
// See docs/specs/2026-08-22-bot-search-api.md for the contract.
package botsearch

import (
	"strings"
	"sync"
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

// OfficialBadge is the CA-verified official bot badge projected onto bot search results.
type OfficialBadge struct {
	Verified   bool   `json:"verified"`
	Role       string `json:"role"`
	BadgeType  string `json:"badgeType"`
	BadgeLabel string `json:"badgeLabel"`
}

// OfficialBadgeProvider resolves CA-verified official bot badges by globalMetaId.
type OfficialBadgeProvider interface {
	GetOfficialBotBadge(globalMetaId string) *OfficialBadge
}

// Aggregator is a read-only aggregator: HandleBlockPin/HandleMempoolPin are
// no-ops and it owns no Pebble data.
type Aggregator struct {
	notifyCh              chan *aggregator.NotifyEvent
	profiles              ProfileSource
	history               GroupHistorySource
	skills                SkillLister
	officialBadgeProvider OfficialBadgeProvider
	localPresence         presence.LocalReader
	globalPresence        presence.GlobalReader
	now                   func() int64 // unix milliseconds; test hook

	// Search-input caches. A single /api/bots/search request previously paid
	// the full corpus rebuild plus one group-history prefix scan per profile
	// (three identity aliases each); production baseline measured ~2s per
	// request. Corpus, per-identity history, and per-provider published skills
	// all change slowly, so they are cached behind short TTLs. Presence stays
	// uncached: it is in-memory and drives the onlineOnly gate.
	corpusMu     sync.Mutex
	corpus       []userinfo.BotSearchProfile
	corpusAtMs   int64
	historyMu    sync.Mutex
	historyCache map[string]groupHistoryCacheEntry
	skillsMu     sync.Mutex
	skillsCache  map[string]skillsCacheEntry
}

// Cache TTLs for the search inputs. They bound staleness for ranking data;
// the wire contract treats search as advisory, so 30s staleness is acceptable
// in exchange for not rescanning the corpus per request.
const (
	searchCorpusTTL  = 30 * time.Second
	searchHistoryTTL = 30 * time.Second
	searchSkillsTTL  = 30 * time.Second

	// searchCacheMaxEntries bounds the per-identity caches; the corpus size
	// naturally bounds the working set, so hitting this cap means identities
	// rotated — drop the map and start fresh.
	searchCacheMaxEntries = 8192
)

type groupHistoryCacheEntry struct {
	items  []groupchat.GroupHistoryItem
	atMs   int64
}

type skillsCacheEntry struct {
	names []string
	atMs  int64
}

func (a *Aggregator) Name() string { return "botsearch" }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	if a.now == nil {
		a.now = func() int64 { return time.Now().UnixMilli() }
	}
	a.historyCache = make(map[string]groupHistoryCacheEntry)
	a.skillsCache = make(map[string]skillsCacheEntry)
	return nil
}

func (a *Aggregator) nowMs() int64 {
	if a.now != nil {
		return a.now()
	}
	return time.Now().UnixMilli()
}

// searchCorpusSnapshot returns the searchable profile corpus, cached for
// searchCorpusTTL. The upstream builder already returns independent copies,
// so sharing the cached slice between requests is safe as long as handlers
// treat it as read-only.
func (a *Aggregator) searchCorpusSnapshot() []userinfo.BotSearchProfile {
	nowMs := a.nowMs()
	a.corpusMu.Lock()
	defer a.corpusMu.Unlock()
	if a.corpus != nil && nowMs-a.corpusAtMs < searchCorpusTTL.Milliseconds() {
		return a.corpus
	}
	corpus := a.profiles.BotSearchProfiles()
	a.corpus = corpus
	a.corpusAtMs = nowMs
	return corpus
}

// groupHistoryForIdentityCached returns the group history for one profile's
// identity aliases, cached per identity tuple for searchHistoryTTL. Lookup
// errors are not cached: a transient history failure must not pin stale
// results, and handleSearch surfaces it to the caller as before.
func (a *Aggregator) groupHistoryForIdentityCached(metaId, globalMetaId, address string) ([]groupchat.GroupHistoryItem, error) {
	if a.history == nil {
		return []groupchat.GroupHistoryItem{}, nil
	}
	key := strings.ToLower(metaId) + "|" + strings.ToLower(globalMetaId) + "|" + strings.ToLower(address)
	nowMs := a.nowMs()

	a.historyMu.Lock()
	defer a.historyMu.Unlock()
	if entry, ok := a.historyCache[key]; ok && nowMs-entry.atMs < searchHistoryTTL.Milliseconds() {
		return entry.items, nil
	}
	items, err := a.history.GroupHistoryForIdentity(metaId, globalMetaId, address)
	if err != nil {
		return nil, err
	}
	if len(a.historyCache) >= searchCacheMaxEntries {
		a.historyCache = make(map[string]groupHistoryCacheEntry)
	}
	a.historyCache[key] = groupHistoryCacheEntry{items: items, atMs: nowMs}
	return items, nil
}

// publishedSkillsCached returns a provider's published skill-service names,
// cached per provider for searchSkillsTTL. Errors fall back to the empty list
// exactly like the uncached call site.
func (a *Aggregator) publishedSkillsCached(globalMetaId string) []string {
	if a.skills == nil || globalMetaId == "" {
		return []string{}
	}
	key := strings.ToLower(globalMetaId)
	nowMs := a.nowMs()

	a.skillsMu.Lock()
	defer a.skillsMu.Unlock()
	if entry, ok := a.skillsCache[key]; ok && nowMs-entry.atMs < searchSkillsTTL.Milliseconds() {
		return entry.names
	}
	names := []string{}
	if list, err := a.skills.PublishedServiceNames(globalMetaId); err == nil && list != nil {
		names = list
	}
	if len(a.skillsCache) >= searchCacheMaxEntries {
		a.skillsCache = make(map[string]skillsCacheEntry)
	}
	a.skillsCache[key] = skillsCacheEntry{names: names, atMs: nowMs}
	return names
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

// SetOfficialBadgeProvider wires the CA official-bot badge lookup.
func (a *Aggregator) SetOfficialBadgeProvider(provider OfficialBadgeProvider) {
	a.officialBadgeProvider = provider
}

// SetPresenceReaders injects the presence backends, mirroring the bothomepage
// aggregator wiring (local socket manager + optional federation global reader).
func (a *Aggregator) SetPresenceReaders(local presence.LocalReader, global presence.GlobalReader) {
	a.localPresence = local
	a.globalPresence = global
}
