package indexer

import (
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Engine transfer-routing tests: processTransfers must collect the tracked
// outputs from TransferConsumer aggregators, run the chain adapter's
// TransferDetector and route the notices back — including for blocks that
// contain no pins at all (a transfer block can be pinless).

type detectorIndexer struct {
	mockIndexer
	notices map[string]aggregator.TransferNotice
}

func (d *detectorIndexer) DetectTransfers(idMap map[string]string) (map[string]aggregator.TransferNotice, error) {
	return d.notices, nil
}

type consumerAggregator struct {
	mu         sync.Mutex
	tracked    map[string]aggregator.TrackedOutput
	consumedAt []int64
	consumed   map[string]aggregator.TransferNotice
}

func (c *consumerAggregator) Name() string                                          { return "fakeconsumer" }
func (c *consumerAggregator) Init(*storage.PebbleStore, *cache.CacheProvider) error { return nil }
func (c *consumerAggregator) HandleBlockPin(*aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}
func (c *consumerAggregator) HandleMempoolPin(*aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}
func (c *consumerAggregator) RegisterRoutes(_ *gin.RouterGroup)             {}
func (c *consumerAggregator) NotifyChannel() <-chan *aggregator.NotifyEvent { return nil }
func (c *consumerAggregator) TrackedOutputs() map[string]aggregator.TrackedOutput {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tracked
}
func (c *consumerAggregator) HandleBlockTransfers(chainName string, height int64, transfers map[string]aggregator.TransferNotice) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.consumedAt = append(c.consumedAt, height)
	if c.consumed == nil {
		c.consumed = make(map[string]aggregator.TransferNotice, len(transfers))
	}
	for outpoint, notice := range transfers {
		c.consumed[outpoint] = notice
	}
}

func TestProcessTransfersRoutesNoticesToConsumers(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer func() { _ = store.Close() }()
	registry := aggregator.NewRegistry(store, cache.New(store))

	consumer := &consumerAggregator{tracked: map[string]aggregator.TrackedOutput{
		"aa:0": {Owner: "mvc1qowner", Ref: "item-1"},
		"bb:1": {Owner: "mvc1qowner2", Ref: "item-2"},
	}}
	if err := registry.Register(consumer); err != nil {
		t.Fatalf("register consumer: %v", err)
	}

	detector := &detectorIndexer{notices: map[string]aggregator.TransferNotice{
		"aa:0": {FromAddress: "mvc1qowner", ToAddress: "mvc1qnew", Output: "spend:0", OutputValue: 546, Location: "spend:0:0"},
		"cc:2": {FromAddress: "untracked", ToAddress: "x", Output: "spend:1"}, // untracked output
	}}
	engine := NewEngine(store, registry)
	engine.chains["testchain"] = &chainEntry{chain: &mockChain{name: "testchain"}, indexer: detector}

	engine.processTransfers(engine.chains["testchain"], 100)

	if len(consumer.consumedAt) != 1 || consumer.consumedAt[0] != 100 {
		t.Fatalf("consumer must receive one block batch: %+v", consumer.consumedAt)
	}
	notice, ok := consumer.consumed["aa:0"]
	if !ok || notice.ToAddress != "mvc1qnew" || notice.OutputValue != 546 {
		t.Fatalf("tracked notice not routed: %+v", consumer.consumed)
	}
	// The engine routes the detector result wholesale; consumers filter by
	// their own tracked set (mrc721nft does this in HandleBlockTransfers).
	if _, ok := consumer.consumed["cc:2"]; !ok {
		t.Fatal("untracked notice should still be routed to the consumer for its own filtering")
	}
}

func TestProcessTransfersSkipsWhenNoConsumersOrNoDetector(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer func() { _ = store.Close() }()

	// No consumers registered: no-op even with a detector.
	registry := aggregator.NewRegistry(store, cache.New(store))
	engine := NewEngine(store, registry)
	engine.chains["testchain"] = &chainEntry{chain: &mockChain{name: "testchain"}, indexer: &detectorIndexer{
		notices: map[string]aggregator.TransferNotice{"aa:0": {}},
	}}
	engine.processTransfers(engine.chains["testchain"], 100) // must not panic

	// Consumer registered but the chain indexer has no TransferDetector.
	consumer := &consumerAggregator{tracked: map[string]aggregator.TrackedOutput{
		"aa:0": {Owner: "mvc1qowner", Ref: "item-1"},
	}}
	if err := registry.Register(consumer); err != nil {
		t.Fatalf("register: %v", err)
	}
	engine.chains["nodetector"] = &chainEntry{chain: &mockChain{name: "nodetector"}, indexer: &mockIndexer{name: "nodetector"}}
	engine.processTransfers(engine.chains["nodetector"], 101)
	if len(consumer.consumedAt) != 0 {
		t.Fatalf("chains without a detector must be skipped: %+v", consumer.consumedAt)
	}
}

func TestScanRangeRunsTransfersOnPinlessBlocks(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer func() { _ = store.Close() }()
	registry := aggregator.NewRegistry(store, cache.New(store))

	consumer := &consumerAggregator{tracked: map[string]aggregator.TrackedOutput{
		"aa:0": {Owner: "mvc1qowner", Ref: "item-1"},
	}}
	if err := registry.Register(consumer); err != nil {
		t.Fatalf("register: %v", err)
	}

	// The block contains no pins, only a spend of the tracked output.
	detector := &detectorIndexer{notices: map[string]aggregator.TransferNotice{
		"aa:0": {FromAddress: "mvc1qowner", ToAddress: "mvc1qnew", Output: "spend:0", Location: "spend:0:0"},
	}}
	engine := NewEngine(store, registry)
	entry := &chainEntry{chain: &mockChain{name: "testchain"}, indexer: detector, lastHeight: 99}
	engine.chains["testchain"] = entry

	engine.scanRange(entry, 100, 100)

	if len(consumer.consumedAt) != 1 {
		t.Fatalf("pinless block must still run transfer detection: %+v", consumer.consumedAt)
	}
	if entry.lastHeight != 100 {
		t.Fatalf("height not persisted: %d", entry.lastHeight)
	}
}
