package publishedcontent

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
)

func simpleNoteBody(title, subtitle, content string, tags ...string) string {
	body := `{"title":"` + title + `","subtitle":"` + subtitle + `","contentType":"text/markdown","content":"` + content + `","tags":[`
	for i, tag := range tags {
		if i > 0 {
			body += ","
		}
		body += `"` + tag + `"`
	}
	return body + `]}`
}

func findSearchDoc(docs []metawebdoc.Document, sourcePinId string) *metawebdoc.Document {
	for i := range docs {
		if docs[i].SourcePinId == sourcePinId {
			return &docs[i]
		}
	}
	return nil
}

func TestSearchDocuments_CreateModifyRevoke(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	create := makeContentPin(contentPinOpts{
		PinId:       "note-create:i0",
		Path:        PathSimpleNote,
		Operation:   OperationCreate,
		ChainName:   "mvc",
		Timestamp:   1755000000,
		ContentType: "application/json",
		ContentBody: []byte(simpleNoteBody("IDBots Beginner Tutorial", "One-line abstract", "# IDBots Beginner Tutorial\\n\\nBody text", "idbots", "tutorial")),
	})
	if _, err := agg.HandleBlockPin(create); err != nil {
		t.Fatalf("create: %v", err)
	}

	docs := agg.SearchDocuments()
	if len(docs) != 1 {
		t.Fatalf("expected 1 search document, got %d", len(docs))
	}
	doc := docs[0]
	if doc.ProtocolKey != "simplenote" {
		t.Fatalf("protocolKey = %q", doc.ProtocolKey)
	}
	if doc.Title != "IDBots Beginner Tutorial" || doc.Summary != "One-line abstract" {
		t.Fatalf("title/summary = %q / %q", doc.Title, doc.Summary)
	}
	if len(doc.Tags) != 2 || doc.Tags[0] != "idbots" {
		t.Fatalf("tags = %v", doc.Tags)
	}
	if doc.Extra["contentType"] != "text/markdown" {
		t.Fatalf("extra = %v", doc.Extra)
	}
	if doc.CreatedAt != 1755000000 || doc.CurrentPinId != "note-create:i0" {
		t.Fatalf("createdAt/currentPinId = %d / %q", doc.CreatedAt, doc.CurrentPinId)
	}
	// The content excerpt is markdown-stripped plain text for matching.
	if doc.ContentExcerpt == "" || doc.ContentExcerpt[0] == '#' {
		t.Fatalf("contentExcerpt = %q", doc.ContentExcerpt)
	}

	// Modify: the snapshot entry advances currentPinId, shows the new
	// version's content, and keeps the source createdAt.
	modify := makeContentPin(contentPinOpts{
		PinId:       "note-modify:i0",
		Path:        PathSimpleNote,
		Operation:   OperationModify,
		ChainName:   "mvc",
		OriginalId:  "note-create:i0",
		Timestamp:   1755001000,
		ContentType: "application/json",
		ContentBody: []byte(simpleNoteBody("IDBots Beginner Tutorial v2", "Revised abstract", "new body")),
	})
	if _, err := agg.HandleBlockPin(modify); err != nil {
		t.Fatalf("modify: %v", err)
	}
	docs = agg.SearchDocuments()
	if len(docs) != 1 {
		t.Fatalf("expected 1 search document after modify, got %d", len(docs))
	}
	doc = docs[0]
	if doc.CurrentPinId != "note-modify:i0" {
		t.Fatalf("currentPinId did not advance: %q", doc.CurrentPinId)
	}
	if doc.Title != "IDBots Beginner Tutorial v2" {
		t.Fatalf("title after modify = %q", doc.Title)
	}
	if doc.CreatedAt != 1755000000 {
		t.Fatalf("createdAt changed across modify: %d", doc.CreatedAt)
	}

	// Revoke: the document disappears from the snapshot.
	revoke := makeContentPin(contentPinOpts{
		PinId:      "note-revoke:i0",
		Path:       PathSimpleNote,
		Operation:  OperationRevoke,
		ChainName:  "mvc",
		OriginalId: "note-modify:i0",
		Timestamp:  1755002000,
	})
	if _, err := agg.HandleBlockPin(revoke); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if docs := agg.SearchDocuments(); len(docs) != 0 {
		t.Fatalf("expected empty snapshot after revoke, got %d", len(docs))
	}
}

func TestSearchDocuments_MempoolVersionIncluded(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	if _, err := agg.HandleMempoolPin(makeContentPin(contentPinOpts{
		PinId:       "note-mempool:i0",
		Path:        PathSimpleNote,
		Operation:   OperationCreate,
		ChainName:   "doge",
		Timestamp:   1755000000,
		ContentType: "application/json",
		ContentBody: []byte(simpleNoteBody("Mempool Note", "", "body")),
	})); err != nil {
		t.Fatalf("mempool create: %v", err)
	}
	if doc := findSearchDoc(agg.SearchDocuments(), "note-mempool:i0"); doc == nil {
		t.Fatalf("mempool note missing from snapshot")
	}
}

func TestSearchDocuments_WarmedAtInit(t *testing.T) {
	agg, store := setupTestAggregator(t)

	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId:       "note-warm:i0",
		Path:        PathMetaProtocol,
		Operation:   OperationCreate,
		ChainName:   "btc",
		Timestamp:   1755000000,
		ContentType: "application/json",
		ContentBody: []byte(`{"protocolName":"simplenote","intro":"Long-form notes"}`),
	})); err != nil {
		t.Fatalf("create: %v", err)
	}

	// A fresh aggregator over the same store warms its snapshot from Pebble.
	reloaded := &Aggregator{}
	if err := reloaded.Init(store, cache.New(store)); err != nil {
		t.Fatalf("reloaded Init: %v", err)
	}
	doc := findSearchDoc(reloaded.SearchDocuments(), "note-warm:i0")
	if doc == nil {
		t.Fatalf("snapshot not warmed at Init")
	}
	if doc.ProtocolKey != "metaprotocol" || doc.Title != "simplenote" || doc.Summary != "Long-form notes" {
		t.Fatalf("unexpected metaprotocol doc: %+v", doc)
	}
}

func TestLookupByAnyPinId_ResolvesAcrossChainsAndVersions(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId:       "note-src:i0",
		Path:        PathSimpleNote,
		Operation:   OperationCreate,
		ChainName:   "mvc",
		Timestamp:   1755000000,
		ContentType: "application/json",
		ContentBody: []byte(simpleNoteBody("Chain Walk", "", "v1")),
	})); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
		PinId:       "note-v2:i0",
		Path:        PathSimpleNote + "@note-src:i0",
		Operation:   OperationModify,
		ChainName:   "mvc",
		Timestamp:   1755001000,
		ContentType: "application/json",
		ContentBody: []byte(simpleNoteBody("Chain Walk v2", "", "v2")),
	})); err != nil {
		t.Fatalf("modify: %v", err)
	}

	// Source id and any version id both resolve to the same record.
	for _, pinId := range []string{"note-src:i0", "note-v2:i0"} {
		rec, err := agg.LookupByAnyPinId(pinId)
		if err != nil {
			t.Fatalf("LookupByAnyPinId(%s): %v", pinId, err)
		}
		if rec == nil {
			t.Fatalf("LookupByAnyPinId(%s): not found", pinId)
		}
		if rec.SourcePinId != "note-src:i0" || rec.CurrentPinId != "note-v2:i0" {
			t.Fatalf("LookupByAnyPinId(%s) = source %q current %q", pinId, rec.SourcePinId, rec.CurrentPinId)
		}
	}

	rec, err := agg.LookupByAnyPinId("unknown-pin:i0")
	if err != nil || rec != nil {
		t.Fatalf("unknown pin = %v, %v", rec, err)
	}
}
