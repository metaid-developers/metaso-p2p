package publishedcontent

import (
	"context"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

type replayMockIndexer struct {
	pinsByHeight map[int64][]*aggregator.PinInscription
}

func (m replayMockIndexer) CatchPins(height int64) ([]*aggregator.PinInscription, []string, error) {
	return m.pinsByHeight[height], nil, nil
}

func TestReplayBlocksIndexesOnlyRequestedProtocol(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	indexer := replayMockIndexer{pinsByHeight: map[int64][]*aggregator.PinInscription{
		10: {
			makeContentPin(contentPinOpts{
				PinId:       "buzz-replay:i0",
				Path:        PathSimpleBuzz,
				Timestamp:   1000,
				ContentBody: []byte("replayed buzz"),
			}),
			makeContentPin(contentPinOpts{
				PinId:       "metaapp-replay:i0",
				Path:        PathMetaApp,
				Timestamp:   1001,
				ContentBody: []byte(`{"name":"skip me"}`),
			}),
			makeContentPin(contentPinOpts{
				PinId:       "userinfo-replay:i0",
				Path:        "/info/name",
				Timestamp:   1002,
				ContentBody: []byte("skip userinfo"),
			}),
		},
	}}

	stats, err := agg.ReplayBlocks(ReplayOptions{
		Context:       context.Background(),
		Indexer:       indexer,
		FromHeight:    10,
		ToHeight:      10,
		ProtocolPaths: []string{PathSimpleBuzz},
	})
	if err != nil {
		t.Fatalf("ReplayBlocks: %v", err)
	}
	if stats.BlocksScanned != 1 || stats.PinsSeen != 3 || stats.PinsMatched != 1 || stats.PinsIndexed != 1 {
		t.Fatalf("ReplayBlocks stats = %+v, want one matched simplebuzz from three seen pins", stats)
	}

	buzzes, err := agg.List(ListParams{ProtocolPath: PathSimpleBuzz, PublisherGlobalMetaId: "gid-user", Size: 5})
	if err != nil {
		t.Fatalf("List simplebuzz: %v", err)
	}
	if len(buzzes.Items) != 1 || buzzes.Items[0].SourcePinId != "buzz-replay:i0" {
		t.Fatalf("simplebuzz items = %+v, want buzz-replay:i0", buzzes.Items)
	}

	metaapps, err := agg.List(ListParams{ProtocolPath: PathMetaApp, PublisherGlobalMetaId: "gid-user", Size: 5})
	if err != nil {
		t.Fatalf("List metaapps: %v", err)
	}
	if len(metaapps.Items) != 0 {
		t.Fatalf("metaapp items = %+v, want none", metaapps.Items)
	}
}
