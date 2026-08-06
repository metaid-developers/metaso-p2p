package socialcontent

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type Aggregator struct {
	store    *storage.PebbleStore
	cache    *cache.Cache[[]byte]
	notifyCh chan *aggregator.NotifyEvent
	mu       sync.Mutex
}

func (a *Aggregator) Name() string { return Namespace }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	if store == nil || cacheProvider == nil {
		return fmt.Errorf("socialcontent requires store and cache")
	}
	a.store = store
	a.cache = cacheProvider.Namespace(Namespace, 2000, 5*time.Minute)
	a.notifyCh = make(chan *aggregator.NotifyEvent, 64)
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent { return a.notifyCh }

func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if pin != nil && pin.ChainName != "" && pin.Id != "" {
		_ = a.store.Delete(Namespace, mempoolEventKey(pin.ChainName, pin.Id))
	}
	return nil, a.processPin(pin, false)
}

func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return nil, a.processPin(pin, true)
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) { registerRoutes(a, router) }

func (a *Aggregator) processPin(pin *aggregator.PinInscription, isMempool bool) error {
	if pin == nil || strings.TrimSpace(pin.Id) == "" || strings.TrimSpace(pin.ChainName) == "" {
		return nil
	}
	chain := strings.ToLower(strings.TrimSpace(pin.ChainName))
	if isMempool {
		return saveJSON(a.store, mempoolEventKey(chain, pin.Id), pin)
	}

	path := protocolPathFromPinPath(pin.Path)
	switch path {
	case PathSimpleBuzz:
		return a.processPost(pin, chain)
	case PathPayLike:
		return a.processLike(pin, chain)
	case PathPayComment:
		return a.processComment(pin, chain)
	default:
		return nil
	}
}

func (a *Aggregator) processPost(pin *aggregator.PinInscription, chain string) error {
	op := operation(pin)
	source := strings.TrimSpace(pin.Id)
	if op != OperationCreate {
		target := targetPinID(pin)
		if target == "" {
			return nil
		}
		resolved, err := a.sourcePinID(chain, target)
		if err != nil {
			return err
		}
		if resolved == "" {
			return nil
		}
		source = resolved
	}

	var current PostRecord
	if err := loadJSON(a.store, postRecordKey(chain, source), &current); err != nil {
		return err
	}
	var record *PostRecord
	if op == OperationRevoke {
		if current.SourcePinId == "" {
			return nil
		}
		record = &current
		record.CurrentPinId = pin.Id
		record.UpdatedAt = pin.Timestamp
		record.Hidden = true
	} else {
		if current.SourcePinId == "" && op != OperationCreate {
			return nil
		}
		record = postRecordFromPin(pin, source, func() *PostRecord {
			if current.SourcePinId == "" {
				return nil
			}
			return &current
		}())
		record.ChainName = chain
		record.Hidden = false
	}

	if err := a.removePostIndexes(&current); err != nil {
		return err
	}
	if err := saveJSON(a.store, postRecordKey(chain, source), record); err != nil {
		return err
	}
	if err := a.writePostIndexes(record); err != nil {
		return err
	}
	if err := a.store.Set(Namespace, postPinKey(chain, pin.Id), []byte(source)); err != nil {
		return err
	}
	return a.reconcilePendingInteractions(record)
}

func (a *Aggregator) processLike(pin *aggregator.PinInscription, chain string) error {
	event, err := parseLike(pin)
	if err != nil {
		return err
	}
	event.ChainName = chain
	canonicalTarget, err := a.canonicalTarget(chain, event.TargetPinId)
	if err != nil {
		return err
	}
	event.TargetPinId = canonicalTarget
	if err := saveJSON(a.store, likeEventKey(chain, pin.Id), event); err != nil {
		return err
	}
	actor := firstIdentity(event.ActorGlobalMetaId, event.ActorMetaId, event.ActorAddress)
	if actor == "" {
		return a.recomputeCounters(chain, event.TargetPinId)
	}
	if err := saveJSON(a.store, likeStateKey(chain, event.TargetPinId, actor), event); err != nil {
		return err
	}
	return a.recomputeCounters(chain, event.TargetPinId)
}

func (a *Aggregator) processComment(pin *aggregator.PinInscription, chain string) error {
	comment, err := parseComment(pin)
	if err != nil {
		return err
	}
	comment.ChainName = chain
	canonicalTarget, err := a.canonicalTarget(chain, comment.TargetPinId)
	if err != nil {
		return err
	}
	comment.TargetPinId = canonicalTarget
	if err := saveJSON(a.store, commentRecordKey(chain, pin.Id), comment); err != nil {
		return err
	}
	if err := a.store.Set(Namespace, commentTargetKey(chain, comment.TargetPinId, comment.Timestamp, comment.PinId), []byte(comment.PinId)); err != nil {
		return err
	}
	return a.recomputeCounters(chain, comment.TargetPinId)
}

func firstIdentity(values ...string) string {
	for _, value := range values {
		if trimmed := strings.ToLower(strings.TrimSpace(value)); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func (a *Aggregator) sourcePinID(chain, pinID string) (string, error) {
	raw, err := a.store.Get(Namespace, postPinKey(chain, pinID))
	if err != nil {
		if !errors.Is(err, pebble.ErrNotFound) {
			return "", err
		}
		raw = nil
	}
	source := string(raw)
	if source != "" {
		return source, nil
	}
	var found PostRecord
	err = a.store.ScanPrefix(Namespace, postTimePrefix(), func(key, _ []byte) error {
		candidateChain, candidateSource, ok := parsePostTimeKey(key)
		if !ok {
			return nil
		}
		if candidateChain != strings.ToLower(chain) {
			return nil
		}
		var record PostRecord
		if err := loadJSON(a.store, postRecordKey(chain, candidateSource), &record); err != nil {
			return err
		}
		if record.SourcePinId == pinID || record.CurrentPinId == pinID {
			found = record
			return errStop
		}
		return nil
	})
	if err != nil && err != errStop {
		return "", err
	}
	return found.SourcePinId, nil
}

var errStop = fmt.Errorf("socialcontent scan stop")

func (a *Aggregator) writePostIndexes(record *PostRecord) error {
	if record == nil || record.SourcePinId == "" || record.ChainName == "" || record.Hidden {
		return nil
	}
	if err := a.store.Set(Namespace, postTimeKey(record.CreatedAt, record.ChainName, record.SourcePinId), []byte(record.SourcePinId)); err != nil {
		return err
	}
	for _, identity := range []string{record.AuthorGlobalMetaId, record.AuthorMetaId, record.AuthorAddress} {
		if identity == "" {
			continue
		}
		if err := a.store.Set(Namespace, postAuthorKey(identity, record.CreatedAt, record.ChainName, record.SourcePinId), []byte(record.SourcePinId)); err != nil {
			return err
		}
	}
	return nil
}

func (a *Aggregator) removePostIndexes(record *PostRecord) error {
	if record == nil || record.SourcePinId == "" {
		return nil
	}
	_ = a.store.Delete(Namespace, postTimeKey(record.CreatedAt, record.ChainName, record.SourcePinId))
	for _, identity := range []string{record.AuthorGlobalMetaId, record.AuthorMetaId, record.AuthorAddress} {
		if identity != "" {
			_ = a.store.Delete(Namespace, postAuthorKey(identity, record.CreatedAt, record.ChainName, record.SourcePinId))
		}
	}
	return nil
}
