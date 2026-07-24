package groupchat

import (
	"context"
	"testing"

	"github.com/cockroachdb/pebble"

	privatechatagg "github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
)

func TestPrivateLatestChatInfoUsesMaterializedSnapshot(t *testing.T) {
	_, store, _ := setupTestAggregator(t)
	defer store.Close()
	privateAggregator := &privatechatagg.Aggregator{}
	if err := privateAggregator.Init(store, cache.New(store)); err != nil {
		t.Fatal(err)
	}
	message := &privatechatagg.PrivateMessage{
		From:             "alice",
		FromGlobalMetaId: "idq-alice",
		FromAddress:      "alice-address",
		FromUserInfo:     map[string]interface{}{"chatPublicKey": "alice-key"},
		To:               "bob",
		ToGlobalMetaId:   "idq-bob",
		ToAddress:        "bob-address",
		ToUserInfo:       map[string]interface{}{"chatPublicKey": "bob-key"},
		TxId:             "latest-tx",
		PinId:            "latest-pin:i0",
		Protocol:         "/private/chat/simplemsg",
		Content:          "latest content",
		Timestamp:        1_800_000_000_000,
		BlockHeight:      99,
		Confirmed:        true,
		Index:            -1,
	}
	if err := privateAggregator.SavePrivateMessage(message); err != nil {
		t.Fatal(err)
	}
	if _, err := privateAggregator.BackfillPrivateChatReadModel(context.Background()); err != nil {
		t.Fatal(err)
	}
	groupAggregator := &Aggregator{}
	if err := groupAggregator.Init(store, cache.New(store)); err != nil {
		t.Fatal(err)
	}

	db, _ := store.OpenDB("privatechat")
	var sourceKeys [][]byte
	if err := store.ScanPrefix("privatechat", []byte("pchat:"), func(key, _ []byte) error {
		sourceKeys = append(sourceKeys, append([]byte(nil), key...))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	batch := db.NewBatch()
	for _, key := range sourceKeys {
		if err := batch.Set(key, []byte("not-json"), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		t.Fatal(err)
	}
	batch.Close()

	got := groupAggregator.getPrivateLatestChatInfoList("idq-alice")
	if len(got) != 1 || got[0].MetaId != "bob" || got[0].LastMessagePinId != "latest-pin:i0" || got[0].Content != "latest content" {
		t.Fatalf("unexpected latest private chat info: %#v", got)
	}
}
