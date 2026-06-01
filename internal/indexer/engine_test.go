package indexer

import (
	"fmt"
	"testing"

	"github.com/metaid-developers/meta-socket/internal/aggregator"
	"github.com/metaid-developers/meta-socket/internal/storage"
)

// mockChain is a minimal chain.Chain for engine tests.
type mockChain struct {
	name       string
	bestHeight int64
	initErr    error
}

func (m *mockChain) Name() string                              { return m.name }
func (m *mockChain) Init() error                               { return m.initErr }
func (m *mockChain) GetBlock(height int64) (any, error)        { return nil, nil }
func (m *mockChain) GetBlockTime(height int64) (int64, error)  { return 0, nil }
func (m *mockChain) GetTransaction(txID string) (any, error)   { return nil, nil }
func (m *mockChain) GetBestHeight() int64                      { return m.bestHeight }
func (m *mockChain) GetMempoolTransactionList() ([]any, error) { return nil, nil }
func (m *mockChain) BroadcastTx(txRaw string) (string, error)  { return "", nil }

// mockIndexer is a minimal chain.Indexer for engine tests.
type mockIndexer struct {
	name string
}

func (m *mockIndexer) Name() string { return m.name }
func (m *mockIndexer) Init() error  { return nil }
func (m *mockIndexer) CatchPins(height int64) ([]*aggregator.PinInscription, []string, error) {
	return nil, nil, nil
}
func (m *mockIndexer) CatchMempoolPins(txList []any) ([]*aggregator.PinInscription, []string, error) {
	return nil, nil, nil
}
func (m *mockIndexer) CatchTransfer(idMap map[string]string) (map[string]any, error) {
	return nil, nil
}
func (m *mockIndexer) GetAddress(pkScript []byte) string { return "" }
func (m *mockIndexer) ZmqTopics() []string               { return nil }

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
