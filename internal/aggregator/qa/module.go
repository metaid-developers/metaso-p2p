package qa

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

// ProfileNamer resolves publisher name/avatar for the returned page only
// (userinfo aggregator; empty strings when the profile is unknown). Same
// structural interface as metaweb.ProfileNamer.
type ProfileNamer interface {
	ProfileNameAvatar(globalMetaId, metaId string) (name, avatar string)
}

// questionDoc is the warm search-document projection of one question
// (copy-on-write snapshot consumed by /api/qa/search). Derived fields are
// computed at index time so search and list views can never drift.
type questionDoc struct {
	SourcePinId           string
	CurrentPinId          string
	ChainName             string
	Title                 string
	Summary               string
	Tags                  []string
	ContentExcerpt        string
	AnswersExcerpt        string
	PublisherGlobalMetaId string
	PublisherMetaId       string
	CreatedAt             int64
	AnswerCount           int
	TopAnswer             *TopAnswerInfo
}

type Aggregator struct {
	store    *storage.PebbleStore
	notifyCh chan *aggregator.NotifyEvent
	mu       sync.Mutex // serialises processPin write paths

	profileNamer ProfileNamer
	now          func() int64 // unix seconds; test hook for the hot window

	// searchDocs holds the warm, copy-on-write question snapshot consumed by
	// /api/qa/search (see search.go).
	searchDocs      atomic.Value // []questionDoc, immutable once stored
	searchDocsMu    sync.Mutex   // guards searchDocsIndex copy-on-write updates
	searchDocsIndex map[string]questionDoc
}

func (a *Aggregator) Name() string { return Namespace }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	if store == nil {
		return nil
	}
	a.store = store
	a.notifyCh = make(chan *aggregator.NotifyEvent, 16)
	if a.now == nil {
		a.now = func() int64 { return time.Now().Unix() }
	}
	// Warm the question snapshot from the record store. A scan failure is
	// non-fatal: every subsequent question write re-folds its document.
	if err := a.rebuildSearchDocs(); err != nil {
		log.Printf("WARNING: qa search document snapshot build failed: %v", err)
	}
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
	router.GET("/qa/search", a.handleSearch)
	router.GET("/qa/questions", a.handleQuestions)
	router.GET("/qa/questions/:pinId", a.handleQuestionDetail)
	router.GET("/qa/questions/:pinId/answers", a.handleQuestionAnswers)
}

// SetProfileNamer wires the userinfo-backed publisher profile resolver.
// Without it names stay empty and responses are unaffected.
func (a *Aggregator) SetProfileNamer(namer ProfileNamer) {
	a.profileNamer = namer
}

// SetNow installs the clock used for the hot ranking window (test hook).
func (a *Aggregator) SetNow(now func() int64) {
	a.now = now
}

// questionDocFromRecord projects a question onto its search document.
// Hidden/revoked questions are excluded (ok=false); mempool questions are
// included per the freshness contract.
func questionDocFromRecord(rec *QuestionRecord) (questionDoc, bool) {
	if rec == nil || rec.Hidden || rec.Title == "" {
		return questionDoc{}, false
	}
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return questionDoc{
		SourcePinId:           rec.SourcePinId,
		CurrentPinId:          currentPinId,
		ChainName:             rec.ChainName,
		Title:                 rec.Title,
		Summary:               rec.Summary,
		Tags:                  rec.Tags,
		ContentExcerpt:        metawebdoc.CapRunes(metawebdoc.StripMarkdown(rec.Content), metawebdoc.ContentMaxRunes),
		AnswersExcerpt:        rec.AnswersText,
		PublisherGlobalMetaId: rec.Publisher.GlobalMetaId,
		PublisherMetaId:       rec.Publisher.MetaId,
		CreatedAt:             rec.CreatedAt,
		AnswerCount:           rec.AnswerCount,
		TopAnswer:             rec.TopAnswer,
	}, true
}

func questionDocKey(chainName, sourcePinId string) string {
	return chainName + ":" + sourcePinId
}

// rebuildSearchDocs builds the warm snapshot from the Pebble question store.
func (a *Aggregator) rebuildSearchDocs() error {
	if a.store == nil {
		return nil
	}
	docs := make(map[string]questionDoc)
	err := a.store.ScanPrefix(Namespace, []byte(keyQuestion), func(_, value []byte) error {
		var rec QuestionRecord
		if err := unmarshalStrict(value, &rec); err != nil {
			return nil
		}
		if doc, ok := questionDocFromRecord(&rec); ok {
			docs[questionDocKey(doc.ChainName, doc.SourcePinId)] = doc
		}
		return nil
	})
	if err != nil {
		return err
	}
	a.swapSearchDocs(docs)
	return nil
}

// refreshQuestionDoc folds one committed question change into the snapshot
// (copy-on-write: clone the index map, swap the immutable slice).
func (a *Aggregator) refreshQuestionDoc(rec *QuestionRecord) {
	if a == nil || rec == nil {
		return
	}
	a.searchDocsMu.Lock()
	defer a.searchDocsMu.Unlock()
	index := make(map[string]questionDoc, len(a.searchDocsIndex)+1)
	for key, doc := range a.searchDocsIndex {
		index[key] = doc
	}
	key := questionDocKey(rec.ChainName, rec.SourcePinId)
	if doc, ok := questionDocFromRecord(rec); ok {
		index[key] = doc
	} else {
		delete(index, key)
	}
	a.swapSearchDocsLocked(index)
}

func (a *Aggregator) swapSearchDocs(docs map[string]questionDoc) {
	a.searchDocsMu.Lock()
	defer a.searchDocsMu.Unlock()
	a.swapSearchDocsLocked(docs)
}

func (a *Aggregator) swapSearchDocsLocked(docs map[string]questionDoc) {
	a.searchDocsIndex = docs
	snapshot := make([]questionDoc, 0, len(docs))
	for _, doc := range docs {
		snapshot = append(snapshot, doc)
	}
	a.searchDocs.Store(snapshot)
}

// searchDocSnapshot returns the shared, immutable question snapshot.
func (a *Aggregator) searchDocSnapshot() []questionDoc {
	if snapshot, ok := a.searchDocs.Load().([]questionDoc); ok {
		return snapshot
	}
	return nil
}
