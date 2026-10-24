package preference

import (
	"log"
	"sync"
	"sync/atomic"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// MetaAppResolver validates feedback ArtifactRefs against the indexed
// MetaApp manifests; implemented by the publishedcontent aggregator.
type MetaAppResolver interface {
	ResolveMetaAppSnapshot(chainName, anyPinId string) (*publishedcontent.MetaAppSnapshot, bool)
}

type Aggregator struct {
	store    *storage.PebbleStore
	notifyCh chan *aggregator.NotifyEvent
	mu       sync.Mutex // serialises processPin write paths

	metaAppResolver MetaAppResolver

	// openScores holds the warm open-preference snapshot, rebuilt from the
	// record store at Init and after every confirmed write (score.go).
	openScores atomic.Value // map[string]OpenSummary, immutable once stored
	// openScoresDirty defers snapshot rebuilds to the next score read, so a
	// replay/backfill burst of N confirmed pins pays one rebuild, not N.
	openScoresDirty   atomic.Bool
	rebuildMu         sync.Mutex // serialises snapshot rebuilds
	openScoreRebuilds int        // guarded by rebuildMu; test hook
}

func (a *Aggregator) Name() string { return Namespace }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	if store == nil {
		return nil
	}
	a.store = store
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	// Self-heal resolutions recorded before their target apps were indexed
	// (mempool ordering, backfill order), then warm the snapshot once. The
	// resolver must therefore be wired before Init; production main.go does.
	if err := a.reevaluateResolutions(); err != nil {
		log.Printf("WARNING: preference resolution re-evaluation failed: %v", err)
	}
	a.markOpenScoresDirty()
	a.ensureFreshOpenScores()
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.processPin(pin, false); err != nil {
		return nil, err
	}
	return nil, nil
}

func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.processPin(pin, true); err != nil {
		return nil, err
	}
	return nil, nil
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/preference/comparisons", a.handleComparisons)
}

// SetMetaAppResolver wires the MetaApp manifest resolver (publishedcontent
// aggregator). Without it refs are recorded as stated and never excluded at
// write time; production wiring always installs it.
func (a *Aggregator) SetMetaAppResolver(resolver MetaAppResolver) {
	a.metaAppResolver = resolver
}

// OpenPreferenceSummary implements publishedcontent.PreferenceLookup so the
// MetaApp list projection can attach open summaries to preference-sorted
// items. Unjudged identities get the exact zero-evidence summary.
func (a *Aggregator) OpenPreferenceSummary(chainName, videoSha256 string) publishedcontent.MetaAppPreferenceOpen {
	summary := a.OpenSummaryFor(chainName, videoSha256)
	return publishedcontent.MetaAppPreferenceOpen{
		Score:          summary.Score,
		ScoreVersion:   summary.ScoreVersion,
		State:          summary.State,
		Comparisons:    summary.Comparisons,
		Judges:         summary.Judges,
		LastComparedAt: summary.LastComparedAt,
	}
}

// NewMetaAppResolverAdapter adapts the publishedcontent aggregator to the
// MetaAppResolver interface.
func NewMetaAppResolverAdapter(agg *publishedcontent.Aggregator) MetaAppResolver {
	return resolverAdapter{agg: agg}
}

type resolverAdapter struct {
	agg *publishedcontent.Aggregator
}

func (r resolverAdapter) ResolveMetaAppSnapshot(chainName, anyPinId string) (*publishedcontent.MetaAppSnapshot, bool) {
	if r.agg == nil {
		return nil, false
	}
	return r.agg.ResolveMetaAppSnapshot(chainName, anyPinId)
}
