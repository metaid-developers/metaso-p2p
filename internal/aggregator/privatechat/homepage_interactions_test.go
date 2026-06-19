package privatechat

import (
	"encoding/json"
	"reflect"
	"testing"

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
