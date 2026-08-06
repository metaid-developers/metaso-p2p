package socialcontent

import (
	"encoding/json"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func setupTestAggregator(t *testing.T) (*Aggregator, *storage.PebbleStore) {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return agg, store
}

func testPin(id, path, op, chain string, ts int64, body []byte) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:            id,
		Path:          path,
		Operation:     op,
		ContentBody:   body,
		ContentType:   "application/json",
		ChainName:     chain,
		GlobalMetaId:  "idq-author",
		MetaId:        "meta-author",
		Address:       "address-author",
		CreateMetaId:  "meta-author",
		CreateAddress: "address-author",
		Timestamp:     ts,
	}
}

func TestSimpleBuzzCreateModifyRevokeAndQueries(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	create := testPin("buzz-create:i0", "/protocols/simplebuzz", OperationCreate, "mvc", 100, []byte(`{"text":"hello MetaSo"}`))
	if _, err := agg.HandleBlockPin(create); err != nil {
		t.Fatalf("create: %v", err)
	}
	modify := testPin("buzz-modify:i0", "/protocols/simplebuzz@buzz-create:i0", OperationModify, "mvc", 110, []byte(`{"text":"edited MetaSo"}`))
	modify.OriginalId = create.Id
	if _, err := agg.HandleBlockPin(modify); err != nil {
		t.Fatalf("modify: %v", err)
	}

	result, err := agg.List(FeedParams{Size: 10, Keyword: "edited"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].SourcePinId != create.Id || result.Items[0].CurrentPinId != modify.Id {
		t.Fatalf("list result = %+v", result)
	}

	detail, err := agg.FindPost(modify.Id, "mvc")
	if err != nil || detail == nil || detail.PayloadJSON["text"] != "edited MetaSo" {
		t.Fatalf("detail = %+v, err=%v", detail, err)
	}

	revoke := testPin("buzz-revoke:i0", "/protocols/simplebuzz@buzz-modify:i0", OperationRevoke, "mvc", 120, nil)
	revoke.OriginalId = modify.Id
	if _, err := agg.HandleBlockPin(revoke); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	result, err = agg.List(FeedParams{Size: 10})
	if err != nil {
		t.Fatalf("list after revoke: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("revoked post remained visible: %+v", result.Items)
	}
}

func TestInteractionsAndAuthorTimeFilter(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	post := testPin("buzz-target:i0", "host:/protocols/simplebuzz", OperationCreate, "mvc", 200, []byte(`{"text":"target"}`))
	if _, err := agg.HandleBlockPin(post); err != nil {
		t.Fatalf("post: %v", err)
	}
	like := testPin("like-1:i0", PathPayLike, OperationCreate, "mvc", 210, mustJSON(t, map[string]any{"isLike": true, "likeTo": post.Id}))
	if _, err := agg.HandleBlockPin(like); err != nil {
		t.Fatalf("like: %v", err)
	}
	comment := testPin("comment-1:i0", PathPayComment, OperationCreate, "mvc", 220, mustJSON(t, map[string]any{"commentTo": post.Id, "content": "nice post", "contentType": "text/plain"}))
	if _, err := agg.HandleBlockPin(comment); err != nil {
		t.Fatalf("comment: %v", err)
	}

	comments, err := agg.ListComments(CommentParams{PinId: post.Id, ChainName: "mvc", Size: 10})
	if err != nil || len(comments.Items) != 1 || comments.Items[0].Content != "nice post" {
		t.Fatalf("comments = %+v, err=%v", comments, err)
	}
	result, err := agg.List(FeedParams{Publisher: "idq-author", Since: 190, Until: 205, Size: 10})
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("author/time result = %+v, err=%v", result, err)
	}
}

func TestMempoolDoesNotBecomePublicUntilConfirmed(t *testing.T) {
	agg, store := setupTestAggregator(t)
	pin := testPin("mempool-buzz:i0", PathSimpleBuzz, OperationCreate, "mvc", 300, []byte(`{"text":"pending"}`))
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatalf("mempool: %v", err)
	}
	result, err := agg.List(FeedParams{Size: 10})
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("mempool post was public: %+v, err=%v", result, err)
	}
	if _, err := store.Get(Namespace, mempoolEventKey("mvc", pin.Id)); err != nil {
		t.Fatalf("mempool event missing: %v", err)
	}
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	result, err = agg.List(FeedParams{Size: 10})
	if err != nil || len(result.Items) != 1 {
		t.Fatalf("confirmed post missing: %+v, err=%v", result, err)
	}
}

func TestMalformedInteractionPayloadIsRejected(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	bad := testPin("bad-like:i0", PathPayLike, OperationCreate, "mvc", 400, []byte(`{"isLike":true}`))
	if _, err := agg.HandleBlockPin(bad); err == nil {
		t.Fatal("malformed like unexpectedly succeeded")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return raw
}
