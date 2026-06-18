package privatechat

import (
	"reflect"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
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
		Protocol:         "/private/chat/simplemsg",
		Timestamp:        123,
		Index:            -1,
	}); err != nil {
		t.Fatalf("save legacy message: %v", err)
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
