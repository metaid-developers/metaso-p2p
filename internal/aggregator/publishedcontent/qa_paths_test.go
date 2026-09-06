package publishedcontent

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

func TestSearchDocuments_QAProtocols(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	question := makeContentPin(contentPinOpts{
		PinId:       "question-create:i0",
		Path:        PathSimpleQuestion,
		Operation:   OperationCreate,
		ChainName:   "mvc",
		Timestamp:   1755000000,
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"How to recover a wallet when the mnemonic is lost?","content":"The userData directory survives.","tags":["wallet","recovery"],"contentType":"text/markdown"}`),
	})
	if _, err := agg.HandleBlockPin(question); err != nil {
		t.Fatalf("question create: %v", err)
	}
	answer := makeContentPin(contentPinOpts{
		PinId:       "answer-create:i0",
		Path:        PathSimpleAnswer,
		Operation:   OperationCreate,
		ChainName:   "mvc",
		Timestamp:   1755000100,
		ContentType: "application/json",
		ContentBody: []byte(`{"answerTo":"question-create:i0","content":"Keys are never stored in plaintext.","tags":["wallet"]}`),
	})
	if _, err := agg.HandleBlockPin(answer); err != nil {
		t.Fatalf("answer create: %v", err)
	}

	docs := agg.SearchDocuments()
	if len(docs) != 2 {
		t.Fatalf("expected 2 search documents, got %d", len(docs))
	}

	qDoc := findSearchDoc(docs, "question-create:i0")
	if qDoc == nil {
		t.Fatal("question document missing")
	}
	if qDoc.ProtocolKey != metawebdoc.KeySimpleQuestion {
		t.Fatalf("question protocolKey = %q", qDoc.ProtocolKey)
	}
	if qDoc.Title != "How to recover a wallet when the mnemonic is lost?" {
		t.Fatalf("question title = %q", qDoc.Title)
	}
	if qDoc.Summary != "The userData directory survives." {
		t.Fatalf("question summary = %q", qDoc.Summary)
	}
	if len(qDoc.Tags) != 2 || qDoc.Tags[0] != "wallet" {
		t.Fatalf("question tags = %v", qDoc.Tags)
	}

	aDoc := findSearchDoc(docs, "answer-create:i0")
	if aDoc == nil {
		t.Fatal("answer document missing")
	}
	if aDoc.ProtocolKey != metawebdoc.KeySimpleAnswer {
		t.Fatalf("answer protocolKey = %q", aDoc.ProtocolKey)
	}
	if aDoc.Title != "Keys are never stored in plaintext." {
		t.Fatalf("answer title = %q", aDoc.Title)
	}
	if len(aDoc.Tags) != 1 || aDoc.Tags[0] != "wallet" {
		t.Fatalf("answer tags = %v", aDoc.Tags)
	}

	// Modify the question through a targeted version pin; the document must
	// reflect the new title while keeping the source pin id.
	modify := makeContentPin(contentPinOpts{
		PinId:       "question-modify:i0",
		Path:        PathSimpleQuestion,
		Operation:   OperationModify,
		ChainName:   "mvc",
		OriginalId:  "@question-create:i0",
		Timestamp:   1755000200,
		ContentBody: []byte(`{"title":"Updated question title","content":"Updated body"}`),
	})
	if _, err := agg.HandleBlockPin(modify); err != nil {
		t.Fatalf("question modify: %v", err)
	}
	docs = agg.SearchDocuments()
	qDoc = findSearchDoc(docs, "question-create:i0")
	if qDoc == nil {
		t.Fatal("question document missing after modify")
	}
	if qDoc.Title != "Updated question title" || qDoc.CurrentPinId != "question-modify:i0" {
		t.Fatalf("modified question doc = %+v", qDoc)
	}

	// Revoke collapses the answer document out of the snapshot.
	revoke := makeContentPin(contentPinOpts{
		PinId:      "answer-revoke:i0",
		Path:       PathSimpleAnswer,
		Operation:  OperationRevoke,
		ChainName:  "mvc",
		OriginalId: "@answer-create:i0",
		Timestamp:  1755000300,
	})
	if _, err := agg.HandleBlockPin(revoke); err != nil {
		t.Fatalf("answer revoke: %v", err)
	}
	docs = agg.SearchDocuments()
	if findSearchDoc(docs, "answer-create:i0") != nil {
		t.Fatal("revoked answer still present in search documents")
	}
}
