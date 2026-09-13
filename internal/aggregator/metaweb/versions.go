package metaweb

// Version-chain resolution (R4): GET /api/metaweb/pin/:pinId/versions returns
// the authoritative version chain of a pin — exactly the chain projection's
// modify_history (oldest → newest, including the create pin; verified against
// production MANAPI). Chain and per-version metadata are cached in memory:
// version pins are immutable once confirmed, so only the chain list needs a
// short TTL (a new modify extends it).

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	lru "github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

const (
	// versionChainCacheTTL is short because a fresh modify extends the chain.
	versionChainCacheTTL = 60 * time.Second
	// versionMetaCacheTTL is long because confirmed version pins never change.
	versionMetaCacheTTL = 10 * time.Minute
	versionCacheMax     = 4096
	// versionFetchConcurrency bounds the per-version MANAPI fan-out.
	versionFetchConcurrency = 4
)

// ErrInvalidPinId marks a pinId that fails the shape check.
var ErrInvalidPinId = errors.New("metaweb: malformed pinId")

// chainEntry is one version-chain member with its attributed metadata.
type chainEntry struct {
	PinId        string
	Operation    string // create / modify / revoke (lowercase)
	CreatedAt    int64  // unix seconds
	GlobalMetaId string
	MetaId       string
	Address      string
}

// versionInfo is the version block of pin-read data (R2): the current pin id
// plus the attributed version count (omitted when unknown).
type versionInfo struct {
	Latest string `json:"latest"`
	Count  *int   `json:"count"`
}

type versionCache struct {
	chain *lru.LRU[string, []chainEntry]
	meta  *lru.LRU[string, chainEntry]
	once  sync.Once
}

func (a *Aggregator) versionCache() *versionCache {
	a.versionCaches.once.Do(func() {
		a.versionCaches.chain = lru.NewLRU[string, []chainEntry](versionCacheMax, nil, versionChainCacheTTL)
		a.versionCaches.meta = lru.NewLRU[string, chainEntry](versionCacheMax, nil, versionMetaCacheTTL)
	})
	return &a.versionCaches
}

// versionItem is one row of the versions response.
type versionItem struct {
	PinId     string      `json:"pinId"`
	Version   int         `json:"version"`
	CreatedAt int64       `json:"createdAt"`
	Operation string      `json:"operation"`
	Author    creatorInfo `json:"author"`
}

type versionsData struct {
	PinId    string        `json:"pinId"`
	Latest   string        `json:"latest"`
	Versions []versionItem `json:"versions"`
}

func (a *Aggregator) handlePinVersions(c *gin.Context) {
	pinId := strings.TrimSpace(c.Param("pinId"))
	if !pinIDPattern.MatchString(pinId) {
		api.RespErr(c, codeInvalidParam, "malformed pinId")
		return
	}
	chain, err := a.VersionChain(pinId)
	if err != nil {
		if errors.Is(err, ErrRemotePinNotFound) {
			api.RespErr(c, codeNotFound, "pin not found")
			return
		}
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	items := make([]versionItem, len(chain))
	for i, entry := range chain {
		items[i] = versionItem{
			PinId:     entry.PinId,
			Version:   i + 1,
			CreatedAt: entry.CreatedAt,
			Operation: entry.Operation,
			Author: creatorInfo{
				GlobalMetaId: entry.GlobalMetaId,
				MetaId:       entry.MetaId,
				Name:         a.creatorName(entry.GlobalMetaId, entry.MetaId),
				Address:      entry.Address,
			},
		}
	}
	latest := pinId
	if len(chain) > 0 {
		latest = chain[len(chain)-1].PinId
	}
	api.RespSuccess(c, versionsData{PinId: pinId, Latest: latest, Versions: items})
}

// VersionChain returns the version chain of any pin in the chain, oldest
// first, with per-version metadata attributed. Used by the versions endpoint
// and the batch pin-read version block.
func (a *Aggregator) VersionChain(pinId string) ([]chainEntry, error) {
	pinId = strings.TrimSpace(pinId)
	if !pinIDPattern.MatchString(pinId) {
		return nil, ErrInvalidPinId
	}
	cache := a.versionCache()
	if chain, ok := cache.chain.Get(pinId); ok {
		return chain, nil
	}

	chain, err := a.resolveVersionChain(pinId)
	if err != nil {
		return nil, err
	}
	cache.chain.Add(pinId, chain)
	return chain, nil
}

func (a *Aggregator) resolveVersionChain(pinId string) ([]chainEntry, error) {
	var local *publishedcontent.Record
	if a.pinLookup != nil {
		rec, err := a.pinLookup.LookupByAnyPinId(pinId)
		if err != nil {
			return nil, err
		}
		local = rec
	}

	if local != nil {
		return a.versionChainFromLocal(pinId, local)
	}
	return a.versionChainFromRemote(pinId)
}

// versionChainFromLocal resolves the chain for a locally indexed record: the
// current pin's chain projection is authoritative; when MANAPI is reachable
// it also supplies per-version metadata. If MANAPI fails, the chain
// degrades to the locally known source/current pair rather than erroring.
func (a *Aggregator) versionChainFromLocal(pinId string, local *publishedcontent.Record) ([]chainEntry, error) {
	current := firstNonEmptyString(local.CurrentPinId, local.SourcePinId)
	sourceEntry := chainEntry{
		PinId:        local.SourcePinId,
		Operation:    publishedcontent.OperationCreate,
		CreatedAt:    metawebdoc.NormalizeUnixSeconds(local.CreatedAt),
		GlobalMetaId: local.PublisherGlobalMetaId,
		MetaId:       local.PublisherMetaId,
		Address:      local.PublisherAddress,
	}

	// Single-version fast path: no observed modify exists locally, and the
	// chain is answered from the local record alone (documented local
	// attribution — the versions endpoint remains the authority for chains
	// with a known modify).
	if current == local.SourcePinId {
		return []chainEntry{sourceEntry}, nil
	}
	if a.remoteFetcher != nil {
		if pin, err := a.remoteFetcher.FetchPin(current); err == nil && pin != nil {
			if chain := chainFromHistory(pin.ModifyHistory); len(chain) > 0 {
				return a.fillVersionMetadata(chain, sourceEntry, currentEntryFromLocal(local))
			}
		}
	}
	// Degraded mode: MANAPI unreachable — the locally known chain members
	// only. documentCurrent carries the newest observed operation.
	chain := []chainEntry{sourceEntry}
	if current != local.SourcePinId {
		chain = append(chain, chainEntry{
			PinId:        current,
			Operation:    localChainOperation(local),
			CreatedAt:    metawebdoc.NormalizeUnixSeconds(local.UpdatedAt),
			GlobalMetaId: local.PublisherGlobalMetaId,
			MetaId:       local.PublisherMetaId,
			Address:      local.PublisherAddress,
		})
	}
	return chain, nil
}

func currentEntryFromLocal(local *publishedcontent.Record) chainEntry {
	current := firstNonEmptyString(local.CurrentPinId, local.SourcePinId)
	return chainEntry{
		PinId:        current,
		Operation:    localChainOperation(local),
		CreatedAt:    metawebdoc.NormalizeUnixSeconds(local.UpdatedAt),
		GlobalMetaId: local.PublisherGlobalMetaId,
		MetaId:       local.PublisherMetaId,
		Address:      local.PublisherAddress,
	}
}

// localChainOperation maps the record's last operation onto the chain
// vocabulary (records keep "revoke" once revoked).
func localChainOperation(local *publishedcontent.Record) string {
	if local.Operation == publishedcontent.OperationRevoke {
		return publishedcontent.OperationRevoke
	}
	if firstNonEmptyString(local.CurrentPinId, local.SourcePinId) != local.SourcePinId {
		return publishedcontent.OperationModify
	}
	return publishedcontent.OperationCreate
}

// versionChainFromRemote resolves the chain for a pin metaso has not indexed:
// the chain projection supplies modify_history for any member of the chain.
func (a *Aggregator) versionChainFromRemote(pinId string) ([]chainEntry, error) {
	pin, err := a.remoteFetcher.FetchPin(pinId)
	if err != nil {
		return nil, err
	}
	chain := chainFromHistory(pin.ModifyHistory)
	if len(chain) == 0 {
		// Single-version pin: the fetched pin is the whole chain.
		chain = []chainEntry{chainEntry{
			PinId:        firstNonEmptyString(pin.PinId, pinId),
			Operation:    strings.ToLower(strings.TrimSpace(pin.Operation)),
			CreatedAt:    metawebdoc.NormalizeUnixSeconds(pin.Timestamp),
			GlobalMetaId: pin.GlobalMetaId,
			MetaId:       firstNonEmptyString(pin.MetaId, pin.CreateMetaId),
			Address:      firstNonEmptyString(pin.Address, pin.CreateAddress),
		}}
		if chain[0].Operation == "" {
			chain[0].Operation = publishedcontent.OperationCreate
		}
		return chain, nil
	}
	return a.fillVersionMetadata(chain, chainEntry{}, chainEntry{})
}

// chainFromHistory converts a modify_history projection into chain entries
// (ids only; metadata is filled by fillVersionMetadata).
func chainFromHistory(history []string) []chainEntry {
	chain := make([]chainEntry, 0, len(history))
	for _, raw := range history {
		entry := strings.Trim(strings.TrimSpace(raw), "@/") // legacy "@/" prefix
		if !pinIDPattern.MatchString(entry) {
			continue
		}
		chain = append(chain, chainEntry{PinId: entry, Operation: publishedcontent.OperationModify})
	}
	return chain
}

// fillVersionMetadata attributes per-version metadata to a chain of pin ids.
// Locally known entries (source and current of an indexed record) are used
// as-is; every other version pin is fetched (immutable — cached 10 min).
func (a *Aggregator) fillVersionMetadata(chain []chainEntry, source, current chainEntry) ([]chainEntry, error) {
	cache := a.versionCache()
	known := map[string]chainEntry{}
	if source.PinId != "" {
		known[source.PinId] = source
	}
	if current.PinId != "" && current.PinId != source.PinId {
		known[current.PinId] = current
	}

	var (
		pendingIdx []int
		wg         sync.WaitGroup
		mu         sync.Mutex
		firstErr   error
	)
	sem := make(chan struct{}, versionFetchConcurrency)
	for i := range chain {
		if entry, ok := known[chain[i].PinId]; ok {
			chain[i].Operation = entry.Operation
			chain[i].CreatedAt = entry.CreatedAt
			chain[i].GlobalMetaId = entry.GlobalMetaId
			chain[i].MetaId = entry.MetaId
			chain[i].Address = entry.Address
			continue
		}
		if cached, ok := cache.meta.Get(chain[i].PinId); ok {
			chain[i] = cached
			continue
		}
		pendingIdx = append(pendingIdx, i)
	}
	for _, i := range pendingIdx {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			pin, err := a.remoteFetcher.FetchPin(chain[idx].PinId)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if firstErr == nil {
					// Not wrapped: a chain member whose metadata cannot be
					// attributed is an availability failure, never a
					// "pin not found" for the whole chain.
					firstErr = fmt.Errorf("version %s metadata: %v", chain[idx].PinId, err)
				}
				return
			}
			operation := strings.ToLower(strings.TrimSpace(pin.Operation))
			if operation == "" {
				operation = publishedcontent.OperationModify
			}
			entry := chainEntry{
				PinId:        firstNonEmptyString(pin.PinId, chain[idx].PinId),
				Operation:    operation,
				CreatedAt:    metawebdoc.NormalizeUnixSeconds(pin.Timestamp),
				GlobalMetaId: pin.GlobalMetaId,
				MetaId:       firstNonEmptyString(pin.MetaId, pin.CreateMetaId),
				Address:      firstNonEmptyString(pin.Address, pin.CreateAddress),
			}
			chain[idx] = entry
			cache.meta.Add(entry.PinId, entry)
		}(i)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return chain, nil
}
