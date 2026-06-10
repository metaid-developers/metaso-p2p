package indexer

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"

	"github.com/gin-gonic/gin"
)

// mockChain is a minimal chain.Chain for engine tests.
type mockChain struct {
	name           string
	bestHeight     int64
	initErr        error
	mempoolTxList  []any
	mempoolListErr error
}

func (m *mockChain) Name() string                             { return m.name }
func (m *mockChain) Init() error                              { return m.initErr }
func (m *mockChain) GetBlock(height int64) (any, error)       { return nil, nil }
func (m *mockChain) GetBlockTime(height int64) (int64, error) { return 0, nil }
func (m *mockChain) GetTransaction(txID string) (any, error)  { return nil, nil }
func (m *mockChain) GetBestHeight() int64                     { return m.bestHeight }
func (m *mockChain) GetMempoolTransactionList() ([]any, error) {
	return m.mempoolTxList, m.mempoolListErr
}
func (m *mockChain) BroadcastTx(txRaw string) (string, error) { return "", nil }

// mockIndexer is a minimal chain.Indexer for engine tests.
type mockIndexer struct {
	name         string
	mempoolPins  []*aggregator.PinInscription
	mempoolTxIDs []string
	mempoolErr   error
	lastTxList   []any
	mempoolCalls int
}

func (m *mockIndexer) Name() string { return m.name }
func (m *mockIndexer) Init() error  { return nil }
func (m *mockIndexer) CatchPins(height int64) ([]*aggregator.PinInscription, []string, error) {
	return nil, nil, nil
}
func (m *mockIndexer) CatchMempoolPins(txList []any) ([]*aggregator.PinInscription, []string, error) {
	m.lastTxList = txList
	m.mempoolCalls++
	return m.mempoolPins, m.mempoolTxIDs, m.mempoolErr
}
func (m *mockIndexer) CatchTransfer(idMap map[string]string) (map[string]any, error) {
	return nil, nil
}
func (m *mockIndexer) GetAddress(pkScript []byte) string { return "" }
func (m *mockIndexer) ZmqTopics() []string               { return nil }

type recordingRegistryAggregator struct {
	mu         sync.Mutex
	mempoolPin []*aggregator.PinInscription
}

func (r *recordingRegistryAggregator) Name() string { return "recording" }
func (r *recordingRegistryAggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	return nil
}
func (r *recordingRegistryAggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}
func (r *recordingRegistryAggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mempoolPin = append(r.mempoolPin, pin)
	return nil, nil
}
func (r *recordingRegistryAggregator) RegisterRoutes(router *gin.RouterGroup) {}
func (r *recordingRegistryAggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return nil
}
func (r *recordingRegistryAggregator) MempoolPins() []*aggregator.PinInscription {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*aggregator.PinInscription, len(r.mempoolPin))
	copy(out, r.mempoolPin)
	return out
}

func TestEnginePollsMempoolAndRoutesPins(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	recorder := &recordingRegistryAggregator{}
	if err := registry.Register(recorder); err != nil {
		t.Fatalf("Register recorder failed: %v", err)
	}
	engine := NewEngine(store, registry)

	chain := &mockChain{name: "mvc", mempoolTxList: []any{"tx1"}}
	pin := &aggregator.PinInscription{
		Id:           "tx1i0",
		Path:         "/protocols/simplebuzz",
		Operation:    "create",
		ChainName:    "mvc",
		GlobalMetaId: "idq1",
		MetaId:       "meta1",
		Address:      "addr1",
		ContentBody:  []byte(`{"content":"hello"}`),
	}
	idx := &mockIndexer{
		name:        "mvc",
		mempoolPins: []*aggregator.PinInscription{pin},
		mempoolTxIDs: []string{
			"tx1",
		},
	}
	if err := engine.RegisterChain(chain, idx, 0); err != nil {
		t.Fatalf("RegisterChain failed: %v", err)
	}

	engine.pollMempoolOnce()

	got := recorder.MempoolPins()
	if len(got) != 1 {
		t.Fatalf("expected one routed mempool pin, got %d", len(got))
	}
	if got[0] != pin {
		t.Fatalf("expected routed pin pointer %p, got %p", pin, got[0])
	}
}

func TestEngineMempoolPollDeduplicatesTransactionIDs(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	recorder := &recordingRegistryAggregator{}
	if err := registry.Register(recorder); err != nil {
		t.Fatalf("Register recorder failed: %v", err)
	}
	engine := NewEngine(store, registry)

	chain := &mockChain{name: "mvc", mempoolTxList: []any{"tx1"}}
	idx := &mockIndexer{
		name: "mvc",
		mempoolPins: []*aggregator.PinInscription{{
			Id:           "tx1i0",
			Path:         "/protocols/simplebuzz",
			Operation:    "create",
			ChainName:    "mvc",
			GlobalMetaId: "idq1",
			MetaId:       "meta1",
			Address:      "addr1",
			ContentBody:  []byte(`{"content":"hello"}`),
		}},
		mempoolTxIDs: []string{"tx1"},
	}
	if err := engine.RegisterChain(chain, idx, 0); err != nil {
		t.Fatalf("RegisterChain failed: %v", err)
	}

	engine.pollMempoolOnce()
	engine.pollMempoolOnce()

	got := recorder.MempoolPins()
	if len(got) != 1 {
		t.Fatalf("expected duplicate txID to route once, got %d pins", len(got))
	}
}

func TestEngineMempoolDeduplicatesDuplicatePinsInSamePoll(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	recorder := &recordingRegistryAggregator{}
	if err := registry.Register(recorder); err != nil {
		t.Fatalf("Register recorder failed: %v", err)
	}
	engine := NewEngine(store, registry)

	chain := &mockChain{name: "mvc", mempoolTxList: []any{"tx1", "tx1-duplicate"}}
	pinA := &aggregator.PinInscription{
		Id:                 "tx1i0",
		GenesisTransaction: "tx1",
		Path:               "/protocols/simplebuzz",
		Operation:          "create",
		ChainName:          "mvc",
		GlobalMetaId:       "idq1",
		MetaId:             "meta1",
		Address:            "addr1",
		ContentBody:        []byte(`{"content":"hello"}`),
	}
	pinB := &aggregator.PinInscription{
		Id:                 "tx1i0",
		GenesisTransaction: "tx1",
		Path:               "/protocols/simplebuzz",
		Operation:          "create",
		ChainName:          "mvc",
		GlobalMetaId:       "idq1",
		MetaId:             "meta1",
		Address:            "addr1",
		ContentBody:        []byte(`{"content":"hello duplicate"}`),
	}
	idx := &mockIndexer{
		name:         "mvc",
		mempoolPins:  []*aggregator.PinInscription{pinA, pinB},
		mempoolTxIDs: []string{"tx1", "tx1"},
	}
	if err := engine.RegisterChain(chain, idx, 0); err != nil {
		t.Fatalf("RegisterChain failed: %v", err)
	}

	engine.pollMempoolOnce()

	got := recorder.MempoolPins()
	if len(got) != 1 {
		t.Fatalf("expected duplicate stable pin identity in one poll to route once, got %d pins", len(got))
	}
	if got[0] != pinA {
		t.Fatalf("expected first duplicate pin pointer %p to route, got %p", pinA, got[0])
	}
}

func TestEngineMempoolDedupesByPinIdentityWhenTxIDsAreUnaligned(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	recorder := &recordingRegistryAggregator{}
	if err := registry.Register(recorder); err != nil {
		t.Fatalf("Register recorder failed: %v", err)
	}
	engine := NewEngine(store, registry)

	chain := &mockChain{name: "mvc", mempoolTxList: []any{"raw1", "raw2"}}
	pinA := &aggregator.PinInscription{
		Id:           "txAi0",
		Path:         "/protocols/simplebuzz",
		Operation:    "create",
		ChainName:    "mvc",
		GlobalMetaId: "idq1",
		MetaId:       "meta1",
		Address:      "addr1",
		ContentBody:  []byte(`{"content":"hello a"}`),
	}
	pinB := &aggregator.PinInscription{
		Id:           "txBi0",
		Path:         "/protocols/simplebuzz",
		Operation:    "create",
		ChainName:    "mvc",
		GlobalMetaId: "idq2",
		MetaId:       "meta2",
		Address:      "addr2",
		ContentBody:  []byte(`{"content":"hello b"}`),
	}
	idx := &mockIndexer{
		name:         "mvc",
		mempoolPins:  []*aggregator.PinInscription{pinA, pinB},
		mempoolTxIDs: []string{"spent1:0", "spent2:0"},
	}
	if err := engine.RegisterChain(chain, idx, 0); err != nil {
		t.Fatalf("RegisterChain failed: %v", err)
	}

	engine.pollMempoolOnce()
	idx.mempoolTxIDs = []string{"spent3:0"}
	engine.pollMempoolOnce()

	got := recorder.MempoolPins()
	if len(got) != 2 {
		t.Fatalf("expected each stable pin identity to route once, got %d pins", len(got))
	}
	if got[0] != pinA || got[1] != pinB {
		t.Fatalf("expected routed pins [pinA pinB], got [%p %p]", got[0], got[1])
	}
}

func TestEngineMempoolDeduplicatesWithoutReturnedTxIDs(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	recorder := &recordingRegistryAggregator{}
	if err := registry.Register(recorder); err != nil {
		t.Fatalf("Register recorder failed: %v", err)
	}
	engine := NewEngine(store, registry)

	chain := &mockChain{name: "mvc", mempoolTxList: []any{"raw1"}}
	pin := &aggregator.PinInscription{
		Id:           "tx1i0",
		Path:         "/protocols/simplebuzz",
		Operation:    "create",
		ChainName:    "mvc",
		GlobalMetaId: "idq1",
		MetaId:       "meta1",
		Address:      "addr1",
		ContentBody:  []byte(`{"content":"hello"}`),
	}
	idx := &mockIndexer{
		name:         "mvc",
		mempoolPins:  []*aggregator.PinInscription{pin},
		mempoolTxIDs: nil,
	}
	if err := engine.RegisterChain(chain, idx, 0); err != nil {
		t.Fatalf("RegisterChain failed: %v", err)
	}

	engine.pollMempoolOnce()
	engine.pollMempoolOnce()

	got := recorder.MempoolPins()
	if len(got) != 1 {
		t.Fatalf("expected pin without returned txIDs to route once, got %d pins", len(got))
	}
	if got[0] != pin {
		t.Fatalf("expected routed pin pointer %p, got %p", pin, got[0])
	}
}

func TestEngineMempoolDedupeTTLAllowsRefresh(t *testing.T) {
	engine := NewEngine(nil, nil)
	engine.ConfigureMempoolPolling(true, time.Second, time.Second)
	pin := &aggregator.PinInscription{
		Id:        "tx1i0",
		ChainName: "mvc",
	}

	first := engine.filterSeenMempoolPins("mvc", []*aggregator.PinInscription{pin}, nil, time.Unix(100, 0))
	if len(first) != 1 {
		t.Fatalf("expected first pin to pass dedupe, got %d pins", len(first))
	}

	withinTTL := engine.filterSeenMempoolPins("mvc", []*aggregator.PinInscription{pin}, nil, time.Unix(100, int64(500*time.Millisecond)))
	if len(withinTTL) != 0 {
		t.Fatalf("expected duplicate within TTL to be suppressed, got %d pins", len(withinTTL))
	}

	afterTTL := engine.filterSeenMempoolPins("mvc", []*aggregator.PinInscription{pin}, nil, time.Unix(102, 0))
	if len(afterTTL) != 1 {
		t.Fatalf("expected pin after TTL to pass dedupe, got %d pins", len(afterTTL))
	}
}

func TestPersistAndRestoreHeight(t *testing.T) {
	// Use a real PebbleStore with a temporary directory.
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	engine := NewEngine(store, registry)

	chain := &mockChain{name: "btc", bestHeight: 0}
	idx := &mockIndexer{name: "btc"}

	if err := engine.RegisterChain(chain, idx, 0); err != nil {
		t.Fatalf("RegisterChain failed: %v", err)
	}

	// Persist height for "btc" chain.
	engine.persistHeight("btc", 100)

	// Verify the height was stored.
	key := []byte("btc_lastheight")
	raw, err := store.Get("indexer_meta", key)
	if err != nil {
		t.Fatalf("Get persisted height failed: %v", err)
	}
	if raw == nil {
		t.Fatal("persisted height not found in store")
	}

	var storedHeight int64
	fmt.Sscanf(string(raw), "%d", &storedHeight)
	if storedHeight != 100 {
		t.Errorf("expected persisted height 100, got %d", storedHeight)
	}
	t.Logf("persisted height: %d", storedHeight)

	// Reset the engine's entry and restore.
	engine.chains["btc"].lastHeight = 0
	engine.restoreHeights()

	restoredHeight := engine.chains["btc"].lastHeight
	if restoredHeight != 100 {
		t.Errorf("expected restored height 100, got %d", restoredHeight)
	}
	t.Logf("restored height: %d", restoredHeight)
}

func TestRestoreHeightAfterStoreReopen(t *testing.T) {
	dir := t.TempDir()
	store := storage.NewPebbleStore(dir)

	registry := aggregator.NewRegistry(store, nil)
	engine := NewEngine(store, registry)

	chain := &mockChain{name: "mvc", bestHeight: 0}
	idx := &mockIndexer{name: "mvc"}
	if err := engine.RegisterChain(chain, idx, 0); err != nil {
		t.Fatalf("RegisterChain failed: %v", err)
	}
	engine.persistHeight("mvc", 175610)
	if err := store.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	reopened := storage.NewPebbleStore(dir)
	defer reopened.Close()
	reopenedRegistry := aggregator.NewRegistry(reopened, nil)
	reopenedEngine := NewEngine(reopened, reopenedRegistry)
	if err := reopenedEngine.RegisterChain(chain, idx, 0); err != nil {
		t.Fatalf("RegisterChain reopened failed: %v", err)
	}
	reopenedEngine.restoreHeights()

	if got := reopenedEngine.chains["mvc"].lastHeight; got != 175610 {
		t.Fatalf("expected restored height 175610 after reopening store, got %d", got)
	}
}

func TestPersistAndRestoreMultipleHeights(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	engine := NewEngine(store, registry)

	// Register BTC and DOGE chains.
	btcChain := &mockChain{name: "btc"}
	btcIdx := &mockIndexer{name: "btc"}
	dogeChain := &mockChain{name: "doge"}
	dogeIdx := &mockIndexer{name: "doge"}

	engine.RegisterChain(btcChain, btcIdx, 0)
	engine.RegisterChain(dogeChain, dogeIdx, 0)

	// Persist different heights for each.
	engine.persistHeight("btc", 500)
	engine.persistHeight("doge", 3000)

	// Reset and restore.
	engine.chains["btc"].lastHeight = 0
	engine.chains["doge"].lastHeight = 0
	engine.restoreHeights()

	if btcH := engine.chains["btc"].lastHeight; btcH != 500 {
		t.Errorf("expected btc height 500, got %d", btcH)
	}
	if dogeH := engine.chains["doge"].lastHeight; dogeH != 3000 {
		t.Errorf("expected doge height 3000, got %d", dogeH)
	}
	t.Logf("btc=%d doge=%d", engine.chains["btc"].lastHeight, engine.chains["doge"].lastHeight)
}

func TestPersistHeight_EmptyStore(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	engine := NewEngine(store, registry)

	// Should not panic with no chain entries.
	engine.persistHeight("nonexistent", 42)
}

func TestRegisterChain(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	engine := NewEngine(store, registry)

	chain := &mockChain{name: "btc"}
	idx := &mockIndexer{name: "btc"}

	if err := engine.RegisterChain(chain, idx, 10); err != nil {
		t.Fatalf("RegisterChain failed: %v", err)
	}

	entry := engine.chains["btc"]
	if entry == nil {
		t.Fatal("chain entry not found")
	}
	if entry.lastHeight != 10 {
		t.Errorf("expected startHeight 10, got %d", entry.lastHeight)
	}
	t.Logf("registered btc at height %d", entry.lastHeight)
}

func TestEngineStop(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	registry := aggregator.NewRegistry(store, nil)
	engine := NewEngine(store, registry)

	engine.mu.Lock()
	engine.running = true
	engine.mu.Unlock()

	engine.Stop()

	if engine.running {
		t.Error("expected engine to be stopped")
	}
}
