package skillservice

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

func findServiceSearchDoc(docs []metawebdoc.Document, sourcePinId string) *metawebdoc.Document {
	for i := range docs {
		if docs[i].SourcePinId == sourcePinId {
			return &docs[i]
		}
	}
	return nil
}

func TestSearchDocuments_ServiceCreateModifyRevoke(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId:          "svc-create:i0",
		Operation:      OperationCreate,
		ChainName:      "mvc",
		ProviderMetaId: "provA",
		DisplayName:    "Fortune Teller",
		ServiceName:    "fortune",
		Description:    "Daily fortune reading",
		ProviderSkill:  "fortune-skill",
		Price:          "100",
		Currency:       "SPACE",
		Timestamp:      1755000000000,
	})); err != nil {
		t.Fatalf("create: %v", err)
	}

	docs := agg.SearchDocuments()
	if len(docs) != 1 {
		t.Fatalf("expected 1 search document, got %d", len(docs))
	}
	doc := docs[0]
	if doc.ProtocolKey != "skill-service" {
		t.Fatalf("protocolKey = %q", doc.ProtocolKey)
	}
	if doc.Title != "Fortune Teller" || doc.Summary != "Daily fortune reading" {
		t.Fatalf("title/summary = %q / %q", doc.Title, doc.Summary)
	}
	if len(doc.Tags) != 1 || doc.Tags[0] != "fortune-skill" {
		t.Fatalf("tags = %v", doc.Tags)
	}
	if doc.Extra["price"] != "100" || doc.Extra["currency"] != "SPACE" || doc.Extra["providerSkill"] != "fortune-skill" {
		t.Fatalf("extra = %v", doc.Extra)
	}
	// skillservice timestamps are milliseconds; the document carries seconds.
	if doc.CreatedAt != 1755000000 {
		t.Fatalf("createdAt = %d, want 1755000000", doc.CreatedAt)
	}
	if doc.PublisherMetaId != "provA" {
		t.Fatalf("publisherMetaId = %q", doc.PublisherMetaId)
	}

	// Modify advances currentPinId and refreshes the document.
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId:         "svc-modify:i0",
		Operation:     OperationModify,
		ChainName:     "mvc",
		OriginalId:    "svc-create:i0",
		DisplayName:   "Fortune Teller Pro",
		ServiceName:   "fortune",
		Description:   "Daily fortune reading, pro",
		ProviderSkill: "fortune-skill",
		Timestamp:     1755001000000,
	})); err != nil {
		t.Fatalf("modify: %v", err)
	}
	docs = agg.SearchDocuments()
	if len(docs) != 1 {
		t.Fatalf("expected 1 search document after modify, got %d", len(docs))
	}
	if docs[0].CurrentPinId != "svc-modify:i0" || docs[0].Title != "Fortune Teller Pro" {
		t.Fatalf("after modify: current=%q title=%q", docs[0].CurrentPinId, docs[0].Title)
	}
	if docs[0].CreatedAt != 1755000000 {
		t.Fatalf("createdAt changed across modify: %d", docs[0].CreatedAt)
	}

	// Revoke removes the document from search.
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId:       "svc-revoke:i0",
		Operation:   OperationRevoke,
		ChainName:   "mvc",
		OriginalId:  "svc-modify:i0",
		ServiceName: "fortune",
		DisplayName: "Fortune Teller Pro",
		Timestamp:   1755002000000,
	})); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if docs := agg.SearchDocuments(); len(docs) != 0 {
		t.Fatalf("expected empty snapshot after revoke, got %d", len(docs))
	}
}

func TestLookupServiceByAnyPinId_ResolvesVersionChain(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId:         "svc-src:i0",
		Operation:     OperationCreate,
		ChainName:     "mvc",
		ServiceName:   "fortune",
		DisplayName:   "Fortune Teller",
		ProviderSkill: "fortune-skill",
		Timestamp:     1755000000000,
	})); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId:         "svc-v2:i0",
		Operation:     OperationModify,
		ChainName:     "mvc",
		OriginalId:    "svc-src:i0",
		ServiceName:   "fortune",
		DisplayName:   "Fortune Teller v2",
		ProviderSkill: "fortune-skill",
		Timestamp:     1755001000000,
	})); err != nil {
		t.Fatalf("modify: %v", err)
	}

	for _, pinId := range []string{"svc-src:i0", "svc-v2:i0"} {
		rec, err := agg.LookupServiceByAnyPinId(pinId)
		if err != nil {
			t.Fatalf("LookupServiceByAnyPinId(%s): %v", pinId, err)
		}
		if rec == nil {
			t.Fatalf("LookupServiceByAnyPinId(%s): not found", pinId)
		}
		if rec.SourceServicePinId != "svc-src:i0" || rec.CurrentPinId != "svc-v2:i0" {
			t.Fatalf("LookupServiceByAnyPinId(%s) = source %q current %q", pinId, rec.SourceServicePinId, rec.CurrentPinId)
		}
	}

	rec, err := agg.LookupServiceByAnyPinId("unknown:i0")
	if err != nil || rec != nil {
		t.Fatalf("unknown pin = %v, %v", rec, err)
	}
}
