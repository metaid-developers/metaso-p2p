package privatechat

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/cache"
	privatechatread "github.com/metaid-developers/metaso-p2p/internal/readmodel/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func seedPrivateChatMessages(t testing.TB, agg *Aggregator, count int) [][]byte {
	t.Helper()
	db, err := agg.store.OpenDB(namespace)
	if err != nil {
		t.Fatalf("open privatechat DB: %v", err)
	}
	batch := db.NewBatch()
	defer batch.Close()
	keys := make([][]byte, 0, count)
	for i := 0; i < count; i++ {
		msg := &PrivateMessage{
			From:        "alice",
			To:          "bob",
			TxId:        fmt.Sprintf("tx-%06d", i),
			PinId:       fmt.Sprintf("pin-%06d:i0", i),
			Protocol:    "/private/chat/simplemsg",
			Content:     fmt.Sprintf("message-%06d", i),
			Timestamp:   int64(1_700_000_000_000 + i),
			BlockHeight: int64(i + 1),
			Confirmed:   true,
			Index:       -1,
		}
		key := pchatKey(msg.From, msg.To, msg.Timestamp, msg.TxId)
		raw, _ := json.Marshal(msg)
		if err := batch.Set(key, raw, nil); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
		keys = append(keys, append([]byte(nil), key...))
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		t.Fatalf("commit seeded messages: %v", err)
	}
	return keys
}

func TestPrivateChatReadModelBackfillServesBoundedIndexPages(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	keys := seedPrivateChatMessages(t, agg, 1000)

	report, err := agg.BackfillPrivateChatReadModel(context.Background())
	if err != nil {
		t.Fatalf("BackfillPrivateChatReadModel: %v", err)
	}
	if report.State.IndexedCount != 1000 || report.State.ConversationCount != 1 {
		t.Fatalf("unexpected backfill state: %+v", report.State)
	}

	db, _ := store.OpenDB(namespace)
	batch := db.NewBatch()
	for i, key := range keys {
		if i >= 500 && i < 520 {
			continue
		}
		if err := batch.Set(key, []byte("not-json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		t.Fatal(err)
	}
	batch.Close()

	got, err := agg.GetPrivateChatListByIndex("alice", "bob", 500, 20)
	if err != nil {
		t.Fatalf("GetPrivateChatListByIndex: %v", err)
	}
	if got.Total != 1000 || len(got.List) != 20 || got.List[0].Index != 500 || got.List[19].Index != 519 {
		t.Fatalf("unexpected bounded page: total=%d len=%d first=%d last=%d", got.Total, len(got.List), got.List[0].Index, got.List[19].Index)
	}
	if got.NextCursor != "520" || got.NextTimestamp != 519 {
		t.Fatalf("unexpected next position: cursor=%q timestamp=%d", got.NextCursor, got.NextTimestamp)
	}
}

func TestPrivateChatReadModelHTTPByIndexServesRawSnapshots(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()
	keys := seedPrivateChatMessages(t, agg, 100)
	if _, err := agg.BackfillPrivateChatReadModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, _ := store.OpenDB(namespace)
	batch := db.NewBatch()
	for i, key := range keys {
		if i >= 40 && i < 50 {
			continue
		}
		if err := batch.Set(key, []byte("not-json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		t.Fatal(err)
	}
	batch.Close()

	response := performRequest(t, router, "GET", "/api/private-chat/messages/by-index?metaId=alice&otherMetaId=bob&startIndex=40&size=10")
	var body struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != 0 || body.Data.Total != 100 || len(body.Data.List) != 10 || body.Data.List[0].Index != 40 || body.Data.List[9].Index != 49 {
		t.Fatalf("unexpected raw HTTP response: code=%d data=%+v", body.Code, body.Data)
	}
}

func TestPrivateChatReadyWriteUsesMetadataInsteadOfHistory(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	keys := seedPrivateChatMessages(t, agg, 200)
	if _, err := agg.BackfillPrivateChatReadModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	db, _ := store.OpenDB(namespace)
	batch := db.NewBatch()
	for _, key := range keys {
		if err := batch.Set(key, []byte("not-json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		t.Fatal(err)
	}
	batch.Close()

	msg := &PrivateMessage{From: "alice", To: "bob", TxId: "new-tx", PinId: "new-pin:i0", Timestamp: 1_800_000_000_000, Index: -1}
	result, err := agg.UpsertPrivateMessage(msg)
	if err != nil {
		t.Fatalf("UpsertPrivateMessage: %v", err)
	}
	if !result.Created || msg.Index != 200 {
		t.Fatalf("write result=%+v index=%d, want created index 200", result, msg.Index)
	}
	page, err := agg.GetPrivateChatListByIndex("alice", "bob", 200, 1)
	if err != nil || len(page.List) != 1 || page.List[0].PinId != "new-pin:i0" || page.Total != 201 {
		t.Fatalf("new indexed page=%+v err=%v", page, err)
	}
}

func TestPrivateChatConfirmationAfterBackfillKeepsIndexAndCount(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	mempool := &PrivateMessage{From: "alice", To: "bob", TxId: "same-tx", PinId: "same-pin:i0", Timestamp: 1_700_000_000_000, Index: -1}
	raw, _ := json.Marshal(mempool)
	if err := store.Set(namespace, pchatKey("alice", "bob", mempool.Timestamp, mempool.TxId), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := agg.BackfillPrivateChatReadModel(context.Background()); err != nil {
		t.Fatal(err)
	}

	confirmed := &PrivateMessage{From: "alice", To: "bob", TxId: "same-tx", PinId: "same-pin:i0", Timestamp: mempool.Timestamp + 500, BlockHeight: 99, Confirmed: true, Index: -1}
	result, err := agg.UpsertPrivateMessage(confirmed)
	if err != nil {
		t.Fatal(err)
	}
	if !result.ConfirmationUpdated || result.Created || confirmed.Index != 0 || confirmed.Timestamp != mempool.Timestamp {
		t.Fatalf("unexpected confirmation result=%+v message=%+v", result, confirmed)
	}
	page, err := agg.GetPrivateChatListByIndex("alice", "bob", 0, 20)
	if err != nil || page.Total != 1 || len(page.List) != 1 || !page.List[0].Confirmed || page.List[0].BlockHeight != 99 {
		t.Fatalf("unexpected confirmed page=%+v err=%v", page, err)
	}
}

func TestPrivateChatReadModelCanonicalAliasesHomesAndPaths(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	alice := &IdentityProfile{MetaId: "alice-meta", GlobalMetaId: "idq-alice", Address: "alice-address"}
	bob := &IdentityProfile{MetaId: "bob-meta", GlobalMetaId: "idq-bob", Address: "bob-address"}
	agg.SetProfileLookup(&localPrivateChatProfileLookup{profilesByID: map[string]*IdentityProfile{
		"alice-meta": alice, "idq-alice": alice, "alice-address": alice,
		"bob-meta": bob, "idq-bob": bob, "bob-address": bob,
	}})
	msg := &PrivateMessage{
		From: "alice-meta", FromGlobalMetaId: "idq-alice", FromAddress: "alice-address",
		To: "bob-meta", ToGlobalMetaId: "idq-bob", ToAddress: "bob-address",
		TxId: "alias-tx", PinId: "alias-pin:i0", Timestamp: 1_700_000_000_001, Index: -1,
	}
	raw, _ := json.Marshal(msg)
	if err := store.Set(namespace, pchatKey(msg.From, msg.To, msg.Timestamp, msg.TxId), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := agg.BackfillPrivateChatReadModel(context.Background()); err != nil {
		t.Fatal(err)
	}

	page, err := agg.GetPrivateChatListByIndex("alice-address", "idq-bob", 0, 20)
	if err != nil || page.Total != 1 || len(page.List) != 1 {
		t.Fatalf("alias page=%+v err=%v", page, err)
	}
	homes, err := agg.GetPrivateChatHomes("idq-alice")
	if err != nil || len(homes) != 1 || homes[0].MetaId != "bob-meta" || homes[0].GlobalMetaId != "idq-bob" {
		t.Fatalf("homes=%+v err=%v", homes, err)
	}
	paths, err := agg.GetPrivateGroupPaths("alice-address")
	if err != nil || len(paths) != 1 || paths[0].PinId != "alias-pin:i0" {
		t.Fatalf("paths=%+v err=%v", paths, err)
	}
}

func TestPrivateChatReadModelBackfillIsRepeatable(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	seedPrivateChatMessages(t, agg, 50)
	first, err := agg.BackfillPrivateChatReadModel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second, err := agg.BackfillPrivateChatReadModel(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.State.IndexedCount != second.State.IndexedCount || first.State.ConversationCount != second.State.ConversationCount {
		t.Fatalf("non-idempotent reports: first=%+v second=%+v", first.State, second.State)
	}
	verified, err := agg.VerifyPrivateChatReadModel(context.Background())
	if err != nil || verified.State.Status != privatechatread.StatusReady {
		t.Fatalf("VerifyPrivateChatReadModel=%+v err=%v", verified, err)
	}
}

func TestPrivateChatReadModelConcurrentWritesHaveContinuousUniqueIndices(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	if _, err := agg.BackfillPrivateChatReadModel(context.Background()); err != nil {
		t.Fatal(err)
	}

	const count = 100
	indices := make([]int, count)
	errs := make(chan error, count)
	var wait sync.WaitGroup
	for i := 0; i < count; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			msg := &PrivateMessage{
				From: "alice", To: "bob", TxId: fmt.Sprintf("concurrent-%03d", i),
				PinId: fmt.Sprintf("concurrent-%03d:i0", i), Timestamp: int64(1_800_000_000_000 + i), Index: -1,
			}
			if _, err := agg.UpsertPrivateMessage(msg); err != nil {
				errs <- err
				return
			}
			indices[i] = int(msg.Index)
		}(i)
	}
	wait.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	sort.Ints(indices)
	for i, index := range indices {
		if index != i {
			t.Fatalf("indices[%d]=%d, want %d; all=%v", i, index, i, indices)
		}
	}
}

func BenchmarkPrivateChatIndexedListByIndex100K(b *testing.B) {
	store := storage.NewPebbleStore(b.TempDir())
	defer store.Close()
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		b.Fatal(err)
	}
	seedPrivateChatMessages(b, agg, 100_000)
	if _, err := agg.BackfillPrivateChatReadModel(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := agg.GetPrivateChatListByIndex("alice", "bob", 50_000, 100)
		if err != nil || len(result.List) != 100 {
			b.Fatalf("query failed: len=%d err=%v", len(result.List), err)
		}
	}
}

func BenchmarkPrivateChatIndexedRawHTTPPayload100K(b *testing.B) {
	store := storage.NewPebbleStore(b.TempDir())
	defer store.Close()
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		b.Fatal(err)
	}
	seedPrivateChatMessages(b, agg, 100_000)
	if _, err := agg.BackfillPrivateChatReadModel(context.Background()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result, err := agg.getPrivateChatListByIndexRawJSON("alice", "bob", 50_000, 100)
		if err != nil || len(result) == 0 {
			b.Fatalf("query failed: bytes=%d err=%v", len(result), err)
		}
	}
}
