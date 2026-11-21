package paybuzz

import (
	"fmt"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// SearchDocuments implements metaweb.DocumentSource (protocol = "paybuzz"),
// exposing encrypted paid articles' plaintext titles and 300-rune previews to
// unified MetaWeb search without ever indexing ciphertext.
func (a *Aggregator) SearchDocuments() []metawebdoc.Document {
	if a == nil || a.store == nil {
		return nil
	}

	a.searchMu.RLock()
	if !a.searchDirty && a.searchDocs != nil {
		cached := a.searchDocs
		a.searchMu.RUnlock()
		return cached
	}
	a.searchMu.RUnlock()

	records, err := a.listAllRecordsNewestFirst()
	if err != nil {
		return nil
	}

	docs := make([]metawebdoc.Document, 0, len(records))
	for _, rec := range records {
		if rec == nil {
			continue
		}
		preview300 := metawebdoc.CapRunes(rec.Preview, 300)
		tags := buildSearchTags(rec)
		publisherGID := rec.CreatorGlobalMetaId
		if publisherGID == "" && strings.HasPrefix(strings.ToLower(rec.AuthorMetaId), "idq1") {
			publisherGID = rec.AuthorMetaId
		}
		publisherMetaId := rec.CreatorMetaId
		if publisherMetaId == "" && publisherGID == "" {
			publisherMetaId = rec.AuthorMetaId
		}

		docs = append(docs, metawebdoc.Document{
			ProtocolKey:           metawebdoc.KeyPayBuzz,
			SourcePinId:           rec.SourcePinId,
			CurrentPinId:          firstNonEmpty(rec.CurrentPinId, rec.SourcePinId),
			Fingerprint:           fmt.Sprintf("%s#%d#%t", rec.CurrentPinId, rec.UpdatedAt, rec.Confirmed),
			ChainName:             rec.ChainName,
			Title:                 rec.Title,
			Summary:               preview300,
			Tags:                  tags,
			ContentExcerpt:        preview300,
			PublisherGlobalMetaId: publisherGID,
			PublisherMetaId:       publisherMetaId,
			CreatedAt:             rec.CreatedAt,
			UpdatedAt:             rec.UpdatedAt,
			Extra: map[string]any{
				"wordCount":        rec.WordCount,
				"contentType":      rec.ContentType,
				"authorMetaId":     rec.AuthorMetaId,
				"authorMvcAddress": rec.AuthorMvcAddress,
				"unlockRules":      rec.UnlockRules,
				"confirmed":        rec.Confirmed,
			},
		})
	}

	a.searchMu.Lock()
	a.searchDocs = docs
	a.searchDirty = false
	a.searchMu.Unlock()
	return docs
}

func buildSearchTags(rec *Record) []string {
	tags := []string{"paybuzz"}
	seen := map[string]struct{}{"paybuzz": {}}
	addTag := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		lower := strings.ToLower(trimmed)
		if _, dup := seen[lower]; dup {
			return
		}
		seen[lower] = struct{}{}
		tags = append(tags, trimmed)
	}
	for _, tok := range rec.UnlockRules.TokenPrices {
		addTag(tok.Symbol)
		addTag(tok.AssetKey)
	}
	for _, col := range rec.UnlockRules.VipMrc721Collections {
		addTag(col.CollectionName)
		addTag(col.CollectionPinId)
	}
	return tags
}
