package privatechat

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func TestListOutgoingHomepageInteractionsFiltersSortsAndLimits(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	pins := []*aggregator.PinInscription{
		simpleMsgPinForHomepage(t, "out-1:i0", "global_bot", "bot_meta", "peer_a", 100),
		simpleMsgPinForHomepage(t, "out-2:i0", "global_bot", "bot_meta", "peer_b", 300),
		simpleMsgPinForHomepage(t, "out-3:i0", "global_bot", "bot_meta", "peer_c", 200),
		simpleMsgPinForHomepage(t, "out-4:i0", "global_bot", "bot_meta", "peer_d", 400),
		simpleMsgPinForHomepage(t, "out-5:i0", "global_bot", "bot_meta", "peer_e", 250),
		simpleMsgPinForHomepage(t, "out-6:i0", "global_bot", "bot_meta", "peer_f", 150),
		simpleMsgPinForHomepage(t, "inbound:i0", "global_peer", "peer_meta", "bot_meta", 500),
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}
	if err := agg.SavePrivateMessage(&PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		To:               "peer_duplicate",
		TxId:             "duplicate",
		PinId:            "out-2:i0",
		Protocol:         "/protocols/simplemsg",
		Timestamp:        999,
		Index:            -1,
	}); err != nil {
		t.Fatalf("save duplicate message: %v", err)
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		MetaId: "bot_meta",
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}

	if !got.HasMore {
		t.Fatal("HasMore = false, want true")
	}
	if len(got.Items) != 5 {
		t.Fatalf("len(Items) = %d, want 5", len(got.Items))
	}

	gotPinIDs := make([]string, 0, len(got.Items))
	for _, item := range got.Items {
		gotPinIDs = append(gotPinIDs, item.PinId)
		if item.ProtocolPath != HomepageSimpleMsgProtocolPath {
			t.Fatalf("ProtocolPath for %s = %q, want %q", item.PinId, item.ProtocolPath, HomepageSimpleMsgProtocolPath)
		}
		if item.InteractWith == "" {
			t.Fatalf("InteractWith for %s is empty", item.PinId)
		}
	}
	wantPinIDs := []string{"out-4:i0", "out-2:i0", "out-5:i0", "out-3:i0", "out-6:i0"}
	if !reflect.DeepEqual(gotPinIDs, wantPinIDs) {
		t.Fatalf("pin IDs = %#v, want %#v", gotPinIDs, wantPinIDs)
	}
}

func TestListOutgoingHomepageInteractionsUsesIdentityAliases(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	agg.SetProfileLookup(&fakePrivateChatProfileLookup{
		byGlobalMetaId: map[string]*IdentityProfile{
			"global_bot": {MetaId: "legacy_bot_meta", GlobalMetaId: "global_bot", Address: "1BotLegacyAddress"},
		},
	})

	if err := agg.SavePrivateMessage(&PrivateMessage{
		FromGlobalMetaId: "",
		From:             "legacy_bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "legacy-out",
		PinId:            "legacy-out:i0",
		Protocol:         "/protocols/simplemsg",
		Timestamp:        123,
		Index:            -1,
	}); err != nil {
		t.Fatalf("save legacy message: %v", err)
	}
	if err := agg.SavePrivateMessage(&PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "legacy_bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "private_legacy_peer",
		TxId:             "legacy-private",
		PinId:            "legacy-private:i0",
		Protocol:         "/private/chat/simplemsg",
		Timestamp:        999,
		Index:            -1,
	}); err != nil {
		t.Fatalf("save private-path message: %v", err)
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "global_bot",
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}

	if got.HasMore {
		t.Fatal("HasMore = true, want false")
	}
	if len(got.Items) != 1 {
		t.Fatalf("len(Items) = %d, want 1", len(got.Items))
	}
	if got.Items[0] != (HomepageInteraction{
		PinId:        "legacy-out:i0",
		ProtocolPath: HomepageSimpleMsgProtocolPath,
		Timestamp:    123,
		InteractWith: "peer_meta",
	}) {
		t.Fatalf("item = %#v", got.Items[0])
	}
}

func TestSavePrivateMessageWritesHomepageSenderIndexes(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	msg := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "indexed-out",
		PinId:            "indexed-out:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        321,
		Index:            -1,
	}
	if err := agg.SavePrivateMessage(msg); err != nil {
		t.Fatalf("SavePrivateMessage: %v", err)
	}

	for _, alias := range []string{"global_bot", "bot_meta", "1botlegacyaddress"} {
		count := 0
		err := store.ScanPrefix(namespace, homepageSenderIndexPrefix(alias), func(key, value []byte) error {
			count++
			var got PrivateMessage
			if err := json.Unmarshal(value, &got); err != nil {
				t.Fatalf("unmarshal index value: %v", err)
			}
			if got.PinId != "indexed-out:i0" {
				t.Fatalf("indexed pinId = %q, want indexed-out:i0", got.PinId)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("ScanPrefix(%s): %v", alias, err)
		}
		if count != 1 {
			t.Fatalf("index entries for %s = %d, want 1", alias, count)
		}
	}
}

func TestSavePrivateMessageWritesHomepageMaterializedChats(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	msg := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "materialized-out",
		PinId:            "materialized-out:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        654,
		Index:            -1,
	}
	if err := agg.SavePrivateMessage(msg); err != nil {
		t.Fatalf("SavePrivateMessage: %v", err)
	}

	for _, alias := range []string{"global_bot", "bot_meta", "1botlegacyaddress"} {
		raw, err := store.Get(namespace, homepageMaterializedChatsKeyForTest(alias))
		if err != nil {
			t.Fatalf("Get materialized list for %s: %v", alias, err)
		}
		var got []HomepageInteraction
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal materialized list for %s: %v", alias, err)
		}
		if len(got) != 1 {
			t.Fatalf("materialized list length for %s = %d, want 1", alias, len(got))
		}
		if got[0] != (HomepageInteraction{
			PinId:        "materialized-out:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    654,
			InteractWith: "peer_meta",
		}) {
			t.Fatalf("materialized list item for %s = %#v", alias, got[0])
		}
	}
}

func TestSavePrivateMessageAppendsHomepageMaterializedChats(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	first := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer_a",
		To:               "peer_a",
		TxId:             "materialized-append-1",
		PinId:            "materialized-append-1:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        100,
		Index:            -1,
	}
	second := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer_b",
		To:               "peer_b",
		TxId:             "materialized-append-2",
		PinId:            "materialized-append-2:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        200,
		Index:            -1,
	}
	if err := agg.SavePrivateMessage(first); err != nil {
		t.Fatalf("SavePrivateMessage(first): %v", err)
	}
	if err := agg.SavePrivateMessage(second); err != nil {
		t.Fatalf("SavePrivateMessage(second): %v", err)
	}

	raw, err := store.Get(namespace, homepageMaterializedChatsKeyForTest("bot_meta"))
	if err != nil {
		t.Fatalf("Get materialized list: %v", err)
	}

	var got []HomepageInteraction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal materialized list: %v", err)
	}
	if !reflect.DeepEqual(got, []HomepageInteraction{
		{
			PinId:        "materialized-append-2:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    200,
			InteractWith: "peer_b",
		},
		{
			PinId:        "materialized-append-1:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    100,
			InteractWith: "peer_a",
		},
	}) {
		t.Fatalf("materialized list = %#v", got)
	}
}

func TestListOutgoingHomepageInteractionsUsesMaterializedChats(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	for _, alias := range []string{"global_bot", "bot_meta", "1botlegacyaddress"} {
		if err := store.Set(namespace, homepageMaterializedChatsKeyForTest(alias), mustMarshal(t, []HomepageInteraction{
			{
				PinId:        "materialized-2:i0",
				ProtocolPath: HomepageSimpleMsgProtocolPath,
				Timestamp:    300,
				InteractWith: "peer_b",
			},
			{
				PinId:        "materialized-1:i0",
				ProtocolPath: HomepageSimpleMsgProtocolPath,
				Timestamp:    200,
				InteractWith: "peer_a",
			},
		})); err != nil {
			t.Fatalf("store.Set materialized list for %s: %v", alias, err)
		}
	}
	if err := store.Set(namespace, homepageMaterializedStateKeyForTest(), []byte("done")); err != nil {
		t.Fatalf("store.Set materialized state key: %v", err)
	}
	if err := store.DeleteByPrefix(namespace, homepageSenderIndexPrefix("global_bot")); err != nil {
		t.Fatalf("DeleteByPrefix(global_bot): %v", err)
	}
	if err := store.DeleteByPrefix(namespace, homepageSenderIndexPrefix("bot_meta")); err != nil {
		t.Fatalf("DeleteByPrefix(bot_meta): %v", err)
	}
	if err := store.DeleteByPrefix(namespace, homepageSenderIndexPrefix("1botlegacyaddress")); err != nil {
		t.Fatalf("DeleteByPrefix(1botlegacyaddress): %v", err)
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "global_bot",
		MetaId:       "bot_meta",
		Address:      "1BotLegacyAddress",
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}

	if got.HasMore {
		t.Fatal("HasMore = true, want false")
	}
	if !reflect.DeepEqual(got.Items, []HomepageInteraction{
		{
			PinId:        "materialized-2:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    300,
			InteractWith: "peer_b",
		},
		{
			PinId:        "materialized-1:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    200,
			InteractWith: "peer_a",
		},
	}) {
		t.Fatalf("Items = %#v", got.Items)
	}
}

func TestListOutgoingHomepageInteractionsFallsBackForLargePageRequests(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	msg := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "large-page-real",
		PinId:            "large-page-real:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        123,
		Index:            -1,
	}
	if err := agg.SavePrivateMessage(msg); err != nil {
		t.Fatalf("SavePrivateMessage: %v", err)
	}

	for _, alias := range []string{"global_bot", "bot_meta", "1botlegacyaddress"} {
		if err := store.Set(namespace, homepageMaterializedChatsKeyForTest(alias), mustMarshal(t, []HomepageInteraction{
			{
				PinId:        "wrong-materialized:i0",
				ProtocolPath: HomepageSimpleMsgProtocolPath,
				Timestamp:    999,
				InteractWith: "wrong_peer",
			},
		})); err != nil {
			t.Fatalf("store.Set wrong materialized list for %s: %v", alias, err)
		}
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "global_bot",
		MetaId:       "bot_meta",
		Address:      "1BotLegacyAddress",
		Size:         64,
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}

	if got.HasMore {
		t.Fatal("HasMore = true, want false")
	}
	if !reflect.DeepEqual(got.Items, []HomepageInteraction{
		{
			PinId:        "large-page-real:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    123,
			InteractWith: "peer_meta",
		},
	}) {
		t.Fatalf("Items = %#v", got.Items)
	}
}

func TestListOutgoingHomepageInteractionsFallsBackWhenMaterializedChatsCorrupted(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	msg := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "corrupt-fallback-real",
		PinId:            "corrupt-fallback-real:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        123,
		Index:            -1,
	}
	if err := agg.SavePrivateMessage(msg); err != nil {
		t.Fatalf("SavePrivateMessage: %v", err)
	}

	for _, alias := range []string{"global_bot", "bot_meta", "1botlegacyaddress"} {
		if err := store.Set(namespace, homepageMaterializedChatsKeyForTest(alias), []byte("{")); err != nil {
			t.Fatalf("store.Set corrupt materialized list for %s: %v", alias, err)
		}
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "global_bot",
		MetaId:       "bot_meta",
		Address:      "1BotLegacyAddress",
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}

	if got.HasMore {
		t.Fatal("HasMore = true, want false")
	}
	if !reflect.DeepEqual(got.Items, []HomepageInteraction{
		{
			PinId:        "corrupt-fallback-real:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    123,
			InteractWith: "peer_meta",
		},
	}) {
		t.Fatalf("Items = %#v", got.Items)
	}

	if _, err := store.Get(namespace, homepageMaterializedStateKeyForTest()); err == nil {
		t.Fatal("homepage materialized state key still exists after corruption fallback")
	}

	rebuilt, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "global_bot",
		MetaId:       "bot_meta",
		Address:      "1BotLegacyAddress",
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions after rebuild: %v", err)
	}
	if !reflect.DeepEqual(rebuilt.Items, got.Items) {
		t.Fatalf("rebuilt Items = %#v, want %#v", rebuilt.Items, got.Items)
	}
	if _, err := store.Get(namespace, homepageMaterializedStateKeyForTest()); err != nil {
		t.Fatalf("homepage materialized state key missing after rebuild: %v", err)
	}
}

func TestListOutgoingHomepageInteractionsBackfillsHomepageSenderIndexes(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	legacy := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "legacy-backfill",
		PinId:            "legacy-backfill:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        456,
		Index:            0,
	}
	raw := mustMarshal(t, legacy)
	if err := store.Set(namespace, pchatKey(legacy.From, legacy.To, legacy.Timestamp, legacy.TxId), raw); err != nil {
		t.Fatalf("store.Set legacy message: %v", err)
	}

	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		MetaId: "bot_meta",
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].PinId != "legacy-backfill:i0" {
		t.Fatalf("items = %#v, want legacy-backfill:i0", got.Items)
	}

	count := 0
	if err := store.ScanPrefix(namespace, homepageSenderIndexPrefix("bot_meta"), func(key, value []byte) error {
		count++
		return nil
	}); err != nil {
		t.Fatalf("ScanPrefix(bot_meta): %v", err)
	}
	if count != 1 {
		t.Fatalf("backfilled index entries = %d, want 1", count)
	}
	if _, err := store.Get(namespace, homepageSenderIndexStateKey()); err != nil {
		t.Fatalf("homepage sender index state key missing: %v", err)
	}
}

func TestInitBackfillsHomepageMaterializedChats(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	legacy := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "legacy-materialized",
		PinId:            "legacy-materialized:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        789,
		Index:            0,
	}
	raw := mustMarshal(t, legacy)
	if err := store.Set(namespace, pchatKey(legacy.From, legacy.To, legacy.Timestamp, legacy.TxId), raw); err != nil {
		t.Fatalf("store.Set legacy message: %v", err)
	}

	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, alias := range []string{"global_bot", "bot_meta", "1botlegacyaddress"} {
		raw, err := store.Get(namespace, homepageMaterializedChatsKeyForTest(alias))
		if err != nil {
			t.Fatalf("Get materialized list for %s after Init: %v", alias, err)
		}
		var got []HomepageInteraction
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal materialized list for %s after Init: %v", alias, err)
		}
		if len(got) != 1 || got[0].PinId != "legacy-materialized:i0" {
			t.Fatalf("materialized list for %s after Init = %#v", alias, got)
		}
	}
	if _, err := store.Get(namespace, homepageMaterializedStateKeyForTest()); err != nil {
		t.Fatalf("homepage materialized state key missing: %v", err)
	}
}

func TestSavePrivateMessageDoesNotWaitForHomepageReadLockAfterInit(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	msg := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "lock-scope",
		PinId:            "lock-scope:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        999,
		Index:            -1,
	}

	agg.homepageIndex.RLock()
	locked := true
	defer func() {
		if locked {
			agg.homepageIndex.RUnlock()
		}
	}()

	done := make(chan error, 1)
	go func() {
		done <- agg.SavePrivateMessage(msg)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SavePrivateMessage: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		agg.homepageIndex.RUnlock()
		locked = false
		if err := <-done; err != nil {
			t.Fatalf("SavePrivateMessage after releasing read lock: %v", err)
		}
		t.Fatal("SavePrivateMessage blocked on homepageIndex read lock")
	}
}

func TestSavePrivateMessageWaitsForConversationLockWhenAssigningIndex(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	msg := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "conversation-lock",
		PinId:            "conversation-lock:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        1000,
		Index:            -1,
	}

	lock := &sync.Mutex{}
	lock.Lock()
	agg.privateMessageLocks.Store(privateMessageConversationLockKey(msg), lock)
	locked := true
	defer func() {
		if locked {
			lock.Unlock()
		}
		agg.privateMessageLocks.Delete(privateMessageConversationLockKey(msg))
	}()

	done := make(chan error, 1)
	go func() {
		done <- agg.SavePrivateMessage(msg)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SavePrivateMessage: %v", err)
		}
		t.Fatal("SavePrivateMessage did not wait for conversation lock")
	case <-time.After(200 * time.Millisecond):
	}

	lock.Unlock()
	locked = false
	if err := <-done; err != nil {
		t.Fatalf("SavePrivateMessage after releasing conversation lock: %v", err)
	}
}

func TestSavePrivateMessageWaitsForSharedHomepageAliasLock(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	msg := &PrivateMessage{
		FromGlobalMetaId: "",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer",
		To:               "peer_meta",
		TxId:             "shared-alias-lock",
		PinId:            "shared-alias-lock:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        1001,
		Index:            -1,
	}

	shared := &sync.Mutex{}
	shared.Lock()
	agg.homepageMaterializedLock.Store(aliasKey("bot_meta"), shared)
	locked := true
	defer func() {
		if locked {
			shared.Unlock()
		}
		agg.homepageMaterializedLock.Delete(aliasKey("bot_meta"))
	}()

	done := make(chan error, 1)
	go func() {
		done <- agg.SavePrivateMessage(msg)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("SavePrivateMessage: %v", err)
		}
		t.Fatal("SavePrivateMessage did not wait for shared alias lock")
	case <-time.After(200 * time.Millisecond):
	}

	shared.Unlock()
	locked = false
	if err := <-done; err != nil {
		t.Fatalf("SavePrivateMessage after releasing shared alias lock: %v", err)
	}
}

func TestSavePrivateMessageFallsBackWhenMaterializedChatsCorrupted(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	first := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer_a",
		To:               "peer_a",
		TxId:             "corrupt-write-first",
		PinId:            "corrupt-write-first:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        100,
		Index:            -1,
	}
	if err := agg.SavePrivateMessage(first); err != nil {
		t.Fatalf("SavePrivateMessage(first): %v", err)
	}

	for _, alias := range []string{"global_bot", "bot_meta", "1botlegacyaddress"} {
		if err := store.Set(namespace, homepageMaterializedChatsKeyForTest(alias), []byte("{")); err != nil {
			t.Fatalf("store.Set corrupt materialized list for %s: %v", alias, err)
		}
	}

	second := &PrivateMessage{
		FromGlobalMetaId: "global_bot",
		From:             "bot_meta",
		FromAddress:      "1BotLegacyAddress",
		ToGlobalMetaId:   "global_peer_b",
		To:               "peer_b",
		TxId:             "corrupt-write-second",
		PinId:            "corrupt-write-second:i0",
		Protocol:         HomepageSimpleMsgProtocolPath,
		Timestamp:        200,
		Index:            -1,
	}
	if err := agg.SavePrivateMessage(second); err != nil {
		t.Fatalf("SavePrivateMessage(second): %v", err)
	}

	if _, err := store.Get(namespace, homepageMaterializedStateKeyForTest()); err == nil {
		t.Fatal("homepage materialized state key still exists after corrupt write fallback")
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "global_bot",
		MetaId:       "bot_meta",
		Address:      "1BotLegacyAddress",
		Size:         5,
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}
	if !reflect.DeepEqual(got.Items, []HomepageInteraction{
		{
			PinId:        "corrupt-write-second:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    200,
			InteractWith: "peer_b",
		},
		{
			PinId:        "corrupt-write-first:i0",
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    100,
			InteractWith: "peer_a",
		},
	}) {
		t.Fatalf("Items = %#v", got.Items)
	}
}

func TestSavePrivateMessageCapsHomepageMaterializedChats(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	for i := 1; i <= homepageMaterializedChatsLimit+6; i++ {
		msg := &PrivateMessage{
			FromGlobalMetaId: "global_bot",
			From:             "bot_meta",
			FromAddress:      "1BotLegacyAddress",
			ToGlobalMetaId:   "global_peer",
			To:               fmt.Sprintf("peer_%d", i),
			TxId:             fmt.Sprintf("materialized-cap-%d", i),
			PinId:            fmt.Sprintf("materialized-cap-%d:i0", i),
			Protocol:         HomepageSimpleMsgProtocolPath,
			Timestamp:        int64(i),
			Index:            -1,
		}
		if err := agg.SavePrivateMessage(msg); err != nil {
			t.Fatalf("SavePrivateMessage(%d): %v", i, err)
		}
	}

	raw, err := store.Get(namespace, homepageMaterializedChatsKeyForTest("bot_meta"))
	if err != nil {
		t.Fatalf("Get materialized list: %v", err)
	}

	var got []HomepageInteraction
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal materialized list: %v", err)
	}
	if len(got) != homepageMaterializedChatsLimit {
		t.Fatalf("materialized list length = %d, want %d", len(got), homepageMaterializedChatsLimit)
	}
	if got[0].Timestamp != int64(homepageMaterializedChatsLimit+6) {
		t.Fatalf("newest timestamp = %d, want %d", got[0].Timestamp, homepageMaterializedChatsLimit+6)
	}
	if got[len(got)-1].Timestamp != 7 {
		t.Fatalf("oldest retained timestamp = %d, want 7", got[len(got)-1].Timestamp)
	}

	result, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		MetaId: "bot_meta",
		Size:   5,
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions: %v", err)
	}
	if !result.HasMore {
		t.Fatal("HasMore = false, want true")
	}
	if len(result.Items) != 5 {
		t.Fatalf("len(Items) = %d, want 5", len(result.Items))
	}
	if result.Items[0].Timestamp != int64(homepageMaterializedChatsLimit+6) {
		t.Fatalf("first item timestamp = %d, want %d", result.Items[0].Timestamp, homepageMaterializedChatsLimit+6)
	}
}

func simpleMsgPinForHomepage(t *testing.T, pinID, fromGlobalMetaID, fromMetaID, toGlobalMetaID string, timestamp int64) *aggregator.PinInscription {
	t.Helper()

	return &aggregator.PinInscription{
		Id:           pinID,
		Path:         HomepageSimpleMsgProtocolPath,
		Operation:    "create",
		CreateMetaId: fromMetaID,
		GlobalMetaId: fromGlobalMetaID,
		Timestamp:    timestamp,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        fromMetaID,
			To:          toGlobalMetaID,
			Content:     "hello",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
}

func homepageMaterializedChatsKeyForTest(alias string) []byte {
	return []byte("hpchat:mat:" + aliasKey(alias))
}

func homepageMaterializedStateKeyForTest() []byte {
	return []byte("hpchat:mat-state:v1")
}
