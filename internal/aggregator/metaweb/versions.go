package metaweb

// Version-chain resolution (R4): GET /api/metaweb/pin/:pinId/versions returns
// the authoritative version chain of a pin — exactly the chain projection's
// modify_history (oldest → newest, including the create pin; verified against
// production MANAPI). Chain and per-version metadata are cached in memory:
// version pins are immutable once confirmed, so only the chain list needs a
// short TTL (a new modify extends it).

import (
	"bytes"
	"encoding/json"
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
	// PayloadVersion is the payload's `version` field (protocol descriptors);
	// filled from fetched pins and the local current record, best-effort for
	// the registry detail endpoint. Empty when unattributed.
	PayloadVersion string
}

// versionInfo is the version block of pin-read data (R2): the current pin id
// plus the attributed version count. Count is omitted when the chain cannot
// be attributed (contract: the key is absent, never null).
type versionInfo struct {
	Latest string `json:"latest"`
	Count  *int   `json:"count,omitempty"`
}

// Version attribution markers of the versions endpoint (R4).
const (
	// versionAttributionChain = resolved from the chain projection's
	// modify_history; matches the projection exactly.
	versionAttributionChain = "chain"
	// versionAttributionLocal = answered from the local index alone (the
	// single-version fast path, or the degraded mode when the projection is
	// unreachable). Exact when the indexer has observed every pin of the
	// chain; may be partial or stale after indexer gaps — documented in the
	// spec so evidence-grade consumers can distinguish the two.
	versionAttributionLocal = "local"
)

// resolvedChain is a version chain plus how it was attributed.
type resolvedChain struct {
	entries     []chainEntry
	attribution string
}

type versionCache struct {
	chain *lru.LRU[string, resolvedChain]
	meta  *lru.LRU[string, chainEntry]
	once  sync.Once
}

func (a *Aggregator) versionCache() *versionCache {
	a.versionCaches.once.Do(func() {
		a.versionCaches.chain = lru.NewLRU[string, resolvedChain](versionCacheMax, nil, versionChainCacheTTL)
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
	PinId       string        `json:"pinId"`
	Latest      string        `json:"latest"`
	Attribution string        `json:"attribution"`
	Versions    []versionItem `json:"versions"`
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
	items := make([]versionItem, len(chain.entries))
	for i, entry := range chain.entries {
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
	if len(chain.entries) > 0 {
		latest = chain.entries[len(chain.entries)-1].PinId
	}
	api.RespSuccess(c, versionsData{PinId: pinId, Latest: latest, Attribution: chain.attribution, Versions: items})
}

// VersionChain returns the version chain of any pin in the chain, oldest
// first, with per-version metadata attributed and the attribution marker
// ("chain" = matches the chain projection's modify_history exactly; "local" =
// local-index attribution, which can be partial after indexer gaps). Used by
// the versions endpoint and the batch pin-read version block.
func (a *Aggregator) VersionChain(pinId string) (resolvedChain, error) {
	pinId = strings.TrimSpace(pinId)
	if !pinIDPattern.MatchString(pinId) {
		return resolvedChain{}, ErrInvalidPinId
	}
	cache := a.versionCache()
	if chain, ok := cache.chain.Get(pinId); ok {
		return chain, nil
	}

	chain, err := a.resolveVersionChain(pinId)
	if err != nil {
		return resolvedChain{}, err
	}
	cache.chain.Add(pinId, chain)
	return chain, nil
}

func (a *Aggregator) resolveVersionChain(pinId string) (resolvedChain, error) {
	var local *publishedcontent.Record
	if a.pinLookup != nil {
		rec, err := a.pinLookup.LookupByAnyPinId(pinId)
		if err != nil {
			return resolvedChain{}, err
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
func (a *Aggregator) versionChainFromLocal(pinId string, local *publishedcontent.Record) (resolvedChain, error) {
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
		sourceEntry.PayloadVersion = payloadVersionOf(local.PayloadJSON)
		return resolvedChain{entries: []chainEntry{sourceEntry}, attribution: versionAttributionLocal}, nil
	}
	if a.remoteFetcher != nil {
		if pin, err := a.remoteFetcher.FetchPin(current); err == nil && pin != nil {
			if entries := chainFromHistory(pin.ModifyHistory); len(entries) > 0 {
				filled, err := a.fillVersionMetadata(entries, sourceEntry, currentEntryFromLocal(local))
				if err != nil {
					return resolvedChain{}, err
				}
				return resolvedChain{entries: filled, attribution: versionAttributionChain}, nil
			}
		}
	}
	// Degraded mode: the chain projection is unreachable — the locally known
	// chain members only, marked local (may be partial for chains with more
	// versions than the indexer observed).
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
	return resolvedChain{entries: chain, attribution: versionAttributionLocal}, nil
}

func currentEntryFromLocal(local *publishedcontent.Record) chainEntry {
	current := firstNonEmptyString(local.CurrentPinId, local.SourcePinId)
	return chainEntry{
		PinId:          current,
		Operation:      localChainOperation(local),
		CreatedAt:      metawebdoc.NormalizeUnixSeconds(local.UpdatedAt),
		GlobalMetaId:   local.PublisherGlobalMetaId,
		MetaId:         local.PublisherMetaId,
		Address:        local.PublisherAddress,
		PayloadVersion: payloadVersionOf(local.PayloadJSON),
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
func (a *Aggregator) versionChainFromRemote(pinId string) (resolvedChain, error) {
	pin, err := a.remoteFetcher.FetchPin(pinId)
	if err != nil {
		return resolvedChain{}, err
	}
	chain := chainFromHistory(pin.ModifyHistory)
	if len(chain) == 0 {
		// Single-version pin: the fetched pin is the whole chain.
		chain = []chainEntry{chainEntry{
			PinId:          firstNonEmptyString(pin.PinId, pinId),
			Operation:      strings.ToLower(strings.TrimSpace(pin.Operation)),
			CreatedAt:      metawebdoc.NormalizeUnixSeconds(pin.Timestamp),
			GlobalMetaId:   pin.GlobalMetaId,
			MetaId:         firstNonEmptyString(pin.MetaId, pin.CreateMetaId),
			Address:        firstNonEmptyString(pin.Address, pin.CreateAddress),
			PayloadVersion: payloadVersionFromBody(pin.ContentBody),
		}}
		if chain[0].Operation == "" {
			chain[0].Operation = publishedcontent.OperationCreate
		}
		return resolvedChain{entries: chain, attribution: versionAttributionChain}, nil
	}
	filled, err := a.fillVersionMetadata(chain, chainEntry{}, chainEntry{})
	if err != nil {
		return resolvedChain{}, err
	}
	return resolvedChain{entries: filled, attribution: versionAttributionChain}, nil
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

// payloadVersionOf reads the payload `version` field of a decoded payload
// object ("" when absent or not a string).
func payloadVersionOf(payload map[string]any) string {
	return stringFieldOf(payload, "version")
}

// payloadVersionFromBody extracts the payload `version` of a raw pin body;
// "" for non-JSON bodies. Used for version pins fetched from MANAPI.
func payloadVersionFromBody(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return ""
	}
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return ""
	}
	return payloadVersionOf(obj)
}

// fillPayloadVersions best-effort backfills PayloadVersion for chain entries
// the local index cannot attribute (e.g. the original create pin of a
// modified record — the local record only retains the latest payload).
// Entries are fetched from MANAPI through the version metadata cache;
// failures leave the field empty rather than failing the read.
func (a *Aggregator) fillPayloadVersions(entries []chainEntry) []chainEntry {
	if a.remoteFetcher == nil {
		return entries
	}
	cache := a.versionCache()
	for i := range entries {
		if entries[i].PayloadVersion != "" {
			continue
		}
		if cached, ok := cache.meta.Get(entries[i].PinId); ok && cached.PayloadVersion != "" {
			entries[i].PayloadVersion = cached.PayloadVersion
			continue
		}
		pin, err := a.remoteFetcher.FetchPin(entries[i].PinId)
		if err != nil || pin == nil {
			continue
		}
		entries[i].PayloadVersion = payloadVersionFromBody(pin.ContentBody)
		cache.meta.Add(entries[i].PinId, entries[i])
	}
	return entries
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
			chain[i].PayloadVersion = entry.PayloadVersion
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
				PinId:          firstNonEmptyString(pin.PinId, chain[idx].PinId),
				Operation:      operation,
				CreatedAt:      metawebdoc.NormalizeUnixSeconds(pin.Timestamp),
				GlobalMetaId:   pin.GlobalMetaId,
				MetaId:         firstNonEmptyString(pin.MetaId, pin.CreateMetaId),
				Address:        firstNonEmptyString(pin.Address, pin.CreateAddress),
				PayloadVersion: payloadVersionFromBody(pin.ContentBody),
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
