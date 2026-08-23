package publishedcontent

import (
	"encoding/json"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// searchLookupChains lists the chain names consulted by the cross-namespace
// pin lookup (LookupByAnyPinId) when the caller does not know the chain. It
// mirrors the chains the indexer engine can register (config.BlockIndex).
var searchLookupChains = []string{"btc", "mvc", "doge", "opcat"}

// SearchDocuments returns the shared, immutable search-document snapshot
// consumed by the metaweb unified search aggregator. The slice is swapped
// atomically on every record change (copy-on-write); callers must not mutate
// the returned documents.
func (a *Aggregator) SearchDocuments() []metawebdoc.Document {
	if snapshot, ok := a.searchDocs.Load().([]metawebdoc.Document); ok {
		return snapshot
	}
	return nil
}

// LookupByAnyPinId resolves a record by any pin id in its version chain
// (source or any modify/revoke version) across all published protocol paths
// and all known chains. Returns (nil, nil) when the pin is unknown. It
// generalises the chain-walk used by /api/metaapp/detail/:pinId for the
// cross-namespace dispatch of the metaweb pin-read endpoint.
func (a *Aggregator) LookupByAnyPinId(pinId string) (*Record, error) {
	pinId = strings.TrimSpace(pinId)
	if a == nil || a.store == nil || pinId == "" {
		return nil, nil
	}
	for _, chainName := range searchLookupChains {
		sourcePinId := a.sourcePinIdFor(chainName, pinId)
		if sourcePinId == "" {
			continue
		}
		for _, protocolPath := range publishedProtocolPaths {
			rec, err := a.loadRecord(chainName, protocolPath, sourcePinId)
			if err != nil {
				return nil, err
			}
			if rec != nil {
				return rec, nil
			}
		}
	}
	return nil, nil
}

// rebuildSearchDocuments builds the warm snapshot from the Pebble record
// store at Init. Synchronous by design: lookups tolerate an empty snapshot
// only until this completes, and Init runs before any pin is handled.
func (a *Aggregator) rebuildSearchDocuments() error {
	docs := make(map[string]metawebdoc.Document)
	err := a.store.ScanPrefix(Namespace, []byte(keyRecord), func(_, value []byte) error {
		var rec Record
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		if doc, ok := searchDocumentFromRecord(&rec); ok {
			docs[searchDocKey(doc.ChainName, doc.SourcePinId)] = doc
		}
		return nil
	})
	if err != nil {
		return err
	}
	a.swapSearchDocuments(docs)
	return nil
}

// refreshSearchDocument folds one committed record change into the snapshot
// (copy-on-write: clone the index map, swap the immutable slice). Called from
// saveRecord so every create/modify/revoke/mempool/backfill write path is
// covered exactly once.
func (a *Aggregator) refreshSearchDocument(rec *Record) {
	if a == nil || rec == nil {
		return
	}
	a.searchDocsMu.Lock()
	defer a.searchDocsMu.Unlock()
	index := make(map[string]metawebdoc.Document, len(a.searchDocsIndex)+1)
	for key, doc := range a.searchDocsIndex {
		index[key] = doc
	}
	key := searchDocKey(rec.ChainName, rec.SourcePinId)
	if doc, ok := searchDocumentFromRecord(rec); ok {
		index[key] = doc
	} else {
		delete(index, key)
	}
	a.swapSearchDocumentsLocked(index)
}

func (a *Aggregator) swapSearchDocuments(docs map[string]metawebdoc.Document) {
	a.searchDocsMu.Lock()
	defer a.searchDocsMu.Unlock()
	a.swapSearchDocumentsLocked(docs)
}

func (a *Aggregator) swapSearchDocumentsLocked(docs map[string]metawebdoc.Document) {
	a.searchDocsIndex = docs
	snapshot := make([]metawebdoc.Document, 0, len(docs))
	for _, doc := range docs {
		snapshot = append(snapshot, doc)
	}
	a.searchDocs.Store(snapshot)
}

func searchDocKey(chainName, sourcePinId string) string {
	return chainName + ":" + sourcePinId
}

// searchDocumentFromRecord projects a record onto its search document.
// Hidden/revoked records are excluded (ok=false); mempool versions are
// included per the freshness contract.
func searchDocumentFromRecord(rec *Record) (metawebdoc.Document, bool) {
	if rec == nil || rec.Hidden || rec.Operation == OperationRevoke {
		return metawebdoc.Document{}, false
	}
	extracted := metawebdoc.ExtractPublished(rec.ProtocolPath, rec.PayloadJSON, rec.PayloadText)
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return metawebdoc.Document{
		ProtocolKey:           metawebdoc.ProtocolKeyForPath(rec.ProtocolPath),
		SourcePinId:           rec.SourcePinId,
		CurrentPinId:          currentPinId,
		ChainName:             rec.ChainName,
		Title:                 extracted.Title,
		Summary:               extracted.Summary,
		Tags:                  extracted.Tags,
		ContentExcerpt:        extracted.ContentExcerpt,
		PublisherGlobalMetaId: rec.PublisherGlobalMetaId,
		PublisherMetaId:       rec.PublisherMetaId,
		CreatedAt:             metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
		UpdatedAt:             metawebdoc.NormalizeUnixSeconds(rec.UpdatedAt),
		Extra:                 extracted.Extra,
	}, true
}
