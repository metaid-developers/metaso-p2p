package privatechat

import (
	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// BlockPinIngestor is the narrow view of the block aggregator the
// compatibility wiring delegates to (duck-typed; implemented by
// block.Aggregator.IngestBlockPin). The block read model is the authority
// for chain-level block state (spec §4.6); privatechat only forwards the
// confirmed compatibility-path pins it receives, replay dedupe in the
// block aggregator keeps the double dispatch (registry + this forwarder)
// a no-op for the second arrival.
type BlockPinIngestor interface {
	IngestBlockPin(pin *aggregator.PinInscription) error
}

// SetBlockPinIngestor wires the block aggregator. Optional: without it the
// compatibility-path handler keeps its legacy log-only behavior.
func (a *Aggregator) SetBlockPinIngestor(ingestor BlockPinIngestor) {
	a.blockIngestor = ingestor
}
