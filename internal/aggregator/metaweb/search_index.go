package metaweb

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// The search hot path used to lowercase every document's text fields twice per
// request (once for the IDF scan, once for scoring) — the production baseline
// measured 1.7s p50 per /api/metaweb/search. Documents are immutable
// copy-on-write snapshot values, so their lowercased search text is stable for
// the lifetime of a snapshot: indexedDoc caches it once, and the per-source
// index is invalidated by a cheap fingerprint over the pin identities the
// index consumes. scoreDocument / tokenIDFWeights / documentMatchesAny remain
// the reference implementations of the scoring contract; the handler runs the
// indexed path, which produces identical scores (enforced by test).

// indexedDoc is one snapshot document with the lowercased text the scoring
// passes consume. union is the concatenated searchable text used for
// document-frequency counting; it matches documentTextUnion exactly.
type indexedDoc struct {
	doc     *metawebdoc.Document
	title   string
	summary string
	content string
	tags    []string
	union   string
}

// searchIndex is the precomputed text index of one source snapshot.
type searchIndex struct {
	fingerprint uint64
	docs        []indexedDoc
}

// indexedSources returns one searchIndex per source, rebuilding an entry only
// when that source's snapshot changed (fingerprint mismatch).
func (a *Aggregator) indexedSources() []*searchIndex {
	a.searchIndexMu.Lock()
	defer a.searchIndexMu.Unlock()
	if len(a.searchIndexes) != len(a.sources) {
		a.searchIndexes = make([]*searchIndex, len(a.sources))
	}
	for i, source := range a.sources {
		if source == nil {
			continue
		}
		docs := source.SearchDocuments()
		fingerprint := docsFingerprint(docs)
		if idx := a.searchIndexes[i]; idx != nil && idx.fingerprint == fingerprint {
			continue
		}
		a.searchIndexes[i] = &searchIndex{fingerprint: fingerprint, docs: buildIndexedDocs(docs)}
	}
	return a.searchIndexes
}

// buildIndexedDocs precomputes the lowercased search text of one snapshot.
func buildIndexedDocs(docs []metawebdoc.Document) []indexedDoc {
	indexed := make([]indexedDoc, len(docs))
	for i := range docs {
		doc := &docs[i]
		entry := indexedDoc{
			doc:     doc,
			title:   strings.ToLower(doc.Title),
			summary: strings.ToLower(doc.Summary),
			content: strings.ToLower(doc.ContentExcerpt),
			tags:    make([]string, len(doc.Tags)),
		}
		var sb strings.Builder
		sb.WriteString(entry.title)
		for j, tag := range doc.Tags {
			entry.tags[j] = strings.ToLower(tag)
			sb.WriteByte('\n')
			sb.WriteString(entry.tags[j])
		}
		sb.WriteByte('\n')
		sb.WriteString(entry.summary)
		sb.WriteByte('\n')
		sb.WriteString(entry.content)
		entry.union = sb.String()
		indexed[i] = entry
	}
	return indexed
}

// docsFingerprint hashes the fields the search index consumes. Any snapshot
// content change implies a new CurrentPinId (copy-on-write version chain), so
// pin ids plus the document count are sufficient to detect index-invalidating
// changes.
func docsFingerprint(docs []metawebdoc.Document) uint64 {
	h := fnv.New64a()
	var buf []byte
	for i := range docs {
		doc := &docs[i]
		buf = append(buf[:0], doc.SourcePinId...)
		buf = append(buf, 0)
		buf = append(buf, doc.CurrentPinId...)
		buf = append(buf, 0)
		buf = strconv.AppendUint(buf, uint64(doc.CreatedAt), 10)
		buf = append(buf, 0x1e)
		h.Write(buf)
	}
	return h.Sum64()
}
