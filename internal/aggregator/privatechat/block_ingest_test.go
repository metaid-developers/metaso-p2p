package privatechat

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// recordedIngestor captures IngestBlockPin calls the privatechat wiring
// forwards.
type recordedIngestor struct {
	pins []*aggregator.PinInscription
	err  error
}

func (r *recordedIngestor) IngestBlockPin(pin *aggregator.PinInscription) error {
	if r.err != nil {
		return r.err
	}
	r.pins = append(r.pins, pin)
	return nil
}

func newBlockIngestFixture(t *testing.T) *Aggregator {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg
}

func blockIngestPin(id, path, to string, confirmed bool) *aggregator.PinInscription {
	pin := &aggregator.PinInscription{
		Id:          id,
		Path:        path,
		Operation:   "create",
		ContentBody: []byte(`{"to":"` + to + `","blockState":1}`),
		ChainName:   "mvc",
		Timestamp:   1000,
	}
	if confirmed {
		pin.GenesisHeight = 900
	}
	return pin
}

func TestHandlePrivateBlockDelegatesConfirmedPins(t *testing.T) {
	agg := newBlockIngestFixture(t)
	ingestor := &recordedIngestor{}
	agg.SetBlockPinIngestor(ingestor)

	// Compatibility path, confirmed.
	if _, err := agg.HandleBlockPin(blockIngestPin("blk-compat:i0", "/private/block/simpleprivateblock", "idq1target", true)); err != nil {
		t.Fatalf("compat dispatch: %v", err)
	}
	// Canonical path (registry would also deliver this directly; dedupe in
	// the block aggregator keeps the second arrival a no-op).
	if _, err := agg.HandleBlockPin(blockIngestPin("blk-canonical:i0", "/protocols/simpleblock", "idq1target2", true)); err != nil {
		t.Fatalf("canonical dispatch: %v", err)
	}
	if len(ingestor.pins) != 2 {
		t.Fatalf("forwarded pins = %d; want 2", len(ingestor.pins))
	}
}

func TestHandlePrivateBlockSkipsMempoolPins(t *testing.T) {
	agg := newBlockIngestFixture(t)
	ingestor := &recordedIngestor{}
	agg.SetBlockPinIngestor(ingestor)

	// Confirmed-only materialization (spec OQ-2): mempool pins are not
	// forwarded to the read model.
	if _, err := agg.HandleMempoolPin(blockIngestPin("blk-mem:i0", "/private/block/simpleprivateblock", "idq1target", false)); err != nil {
		t.Fatalf("mempool dispatch: %v", err)
	}
	if len(ingestor.pins) != 0 {
		t.Fatalf("mempool pin must not reach the block ingestor; got %d", len(ingestor.pins))
	}
}

func TestHandlePrivateBlockWithoutIngestorKeepsLogOnly(t *testing.T) {
	agg := newBlockIngestFixture(t)
	// No SetBlockPinIngestor wired: legacy log-only behavior, no panic.
	if _, err := agg.HandleBlockPin(blockIngestPin("blk-nobody:i0", "/private/block/simpleprivateblock", "idq1target", true)); err != nil {
		t.Fatalf("unwired dispatch: %v", err)
	}
}

func TestHandlePrivateBlockRejectsMalformedAndEmptyPayloads(t *testing.T) {
	agg := newBlockIngestFixture(t)
	ingestor := &recordedIngestor{}
	agg.SetBlockPinIngestor(ingestor)

	malformed := blockIngestPin("blk-bad:i0", "/protocols/simpleblock", "idq1target", true)
	malformed.ContentBody = []byte(`{not-json`)
	if _, err := agg.HandleBlockPin(malformed); err != nil {
		t.Fatalf("malformed payload must not error: %v", err)
	}

	empty := blockIngestPin("blk-empty:i0", "/protocols/simpleblock", "", true)
	empty.ContentBody = []byte(`{"to":"","blockState":1}`)
	if _, err := agg.HandleBlockPin(empty); err != nil {
		t.Fatalf("empty to must not error: %v", err)
	}

	if len(ingestor.pins) != 0 {
		t.Fatalf("malformed/empty payloads must not reach the ingestor; got %d", len(ingestor.pins))
	}
}

func TestHandlePrivateBlockPropagatesIngestorErrors(t *testing.T) {
	agg := newBlockIngestFixture(t)
	ingestor := &recordedIngestor{err: errInjected}
	agg.SetBlockPinIngestor(ingestor)

	if _, err := agg.HandleBlockPin(blockIngestPin("blk-err:i0", "/protocols/simpleblock", "idq1target", true)); err != errInjected {
		t.Fatalf("ingestor error = %v; want %v", err, errInjected)
	}
}

var errInjected = &injectedError{}

type injectedError struct{}

func (*injectedError) Error() string { return "injected" }
