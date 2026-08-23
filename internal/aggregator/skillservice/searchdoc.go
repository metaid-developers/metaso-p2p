package skillservice

import (
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// serviceLookupChains lists the chain names consulted by the cross-namespace
// pin lookup (LookupServiceByAnyPinId) when the caller does not know the
// chain. It mirrors the chains the indexer engine can register
// (config.BlockIndex).
var serviceLookupChains = []string{"btc", "mvc", "doge", "opcat"}

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

// LookupServiceByAnyPinId resolves a service record by any pin id in its
// version chain (source or any modify/revoke version) across all known
// chains, for the cross-namespace dispatch of the metaweb pin-read endpoint.
// Returns (nil, nil) when the pin is unknown.
func (a *Aggregator) LookupServiceByAnyPinId(pinId string) (*ServiceRecord, error) {
	pinId = strings.TrimSpace(pinId)
	if a == nil || a.store == nil || pinId == "" {
		return nil, nil
	}
	for _, chainName := range serviceLookupChains {
		rec, err := a.loadServiceByAnyPinId(chainName, pinId)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			return rec, nil
		}
	}
	return nil, nil
}

// rebuildSearchDocuments builds the warm snapshot from the Pebble service
// store at Init. Synchronous by design: Init runs before any pin is handled.
func (a *Aggregator) rebuildSearchDocuments() error {
	records, err := a.listAllServices()
	if err != nil {
		return err
	}
	docs := make(map[string]metawebdoc.Document, len(records))
	for _, rec := range records {
		if doc, ok := searchDocumentFromService(rec); ok {
			docs[searchDocKey(doc.ChainName, doc.SourcePinId)] = doc
		}
	}
	a.swapSearchDocuments(docs)
	return nil
}

// refreshSearchDocument folds one committed service change into the snapshot
// (copy-on-write: clone the index map, swap the immutable slice). Called from
// saveService so every create/modify/revoke write path is covered once.
func (a *Aggregator) refreshSearchDocument(rec *ServiceRecord) {
	if a == nil || rec == nil {
		return
	}
	a.searchDocsMu.Lock()
	defer a.searchDocsMu.Unlock()
	index := make(map[string]metawebdoc.Document, len(a.searchDocsIndex)+1)
	for key, doc := range a.searchDocsIndex {
		index[key] = doc
	}
	key := searchDocKey(rec.ChainName, rec.SourceServicePinId)
	if doc, ok := searchDocumentFromService(rec); ok {
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

// searchDocumentFromService projects a service record onto its search
// document. Revoked records are excluded (ok=false) per the search spec's
// hidden/revoked rule.
func searchDocumentFromService(rec *ServiceRecord) (metawebdoc.Document, bool) {
	if rec == nil || rec.Operation == OperationRevoke {
		return metawebdoc.Document{}, false
	}
	extracted := metawebdoc.ExtractSkillService(metawebdoc.SkillServiceFields{
		DisplayName:    rec.DisplayName,
		ServiceName:    rec.ServiceName,
		Description:    rec.Description,
		ProviderSkill:  rec.ProviderSkill,
		Price:          rec.Price,
		Currency:       rec.Currency,
		SettlementKind: rec.SettlementKind,
	})
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourceServicePinId
	}
	return metawebdoc.Document{
		ProtocolKey:           metawebdoc.KeySkillService,
		SourcePinId:           rec.SourceServicePinId,
		CurrentPinId:          currentPinId,
		ChainName:             rec.ChainName,
		Title:                 extracted.Title,
		Summary:               extracted.Summary,
		Tags:                  extracted.Tags,
		ContentExcerpt:        extracted.ContentExcerpt,
		PublisherGlobalMetaId: rec.ProviderGlobalMetaId,
		PublisherMetaId:       rec.ProviderMetaId,
		CreatedAt:             metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
		UpdatedAt:             metawebdoc.NormalizeUnixSeconds(rec.UpdatedAt),
		Extra:                 extracted.Extra,
	}, true
}
