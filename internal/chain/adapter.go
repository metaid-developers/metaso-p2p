// Package chain defines the adapter interfaces for blockchain interaction.
// Each supported chain (BTC, MVC, DOGE, OPCAT) implements both Chain and Indexer.
//
// Design principles (synthesized from show-now-tmp, man-p2p, meta-file-system):
//   - Chain handles RPC communication and raw data retrieval.
//   - Indexer handles parsing of raw data into structured PinInscriptions.
//   - Minimal, focused interfaces — no unused methods.
//   - ZMQ is part of Chain (subscribe/close), processing is in the indexer engine.
package chain

import (
	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Chain provides RPC and ZMQ access to a blockchain node.
type Chain interface {
	// Name returns the chain identifier (e.g. "btc", "mvc").
	Name() string

	// Init initializes the RPC client and connection.
	Init() error

	// GetBlock returns the raw block at the given height.
	// The returned type is chain-specific (*wire.MsgBlock, etc.).
	GetBlock(height int64) (block any, err error)

	// GetBlockTime returns the timestamp of the block at the given height.
	GetBlockTime(height int64) (int64, error)

	// GetTransaction returns a raw transaction by txid.
	GetTransaction(txID string) (tx any, err error)

	// GetBestHeight returns the current chain tip height.
	GetBestHeight() int64

	// GetMempoolTransactionList returns all unconfirmed transaction IDs.
	GetMempoolTransactionList() ([]any, error)

	// BroadcastTx sends a raw transaction to the network.
	BroadcastTx(txRaw string) (txID string, err error)
}

// Indexer parses chain-specific raw data into structured PinInscriptions.
type Indexer interface {
	// Name returns the chain identifier.
	Name() string

	// Init initializes the indexer (chain parameters, etc.).
	Init() error

	// CatchPins parses all MetaID pins from the block at the given height.
	// Returns the parsed pins and the list of transaction IDs in the block (used for transfer detection).
	CatchPins(height int64) (pins []*aggregator.PinInscription, txIDs []string, err error)

	// CatchMempoolPins parses MetaID pins from a list of raw mempool transactions.
	CatchMempoolPins(txList []any) (pins []*aggregator.PinInscription, txIDs []string, err error)

	// CatchTransfer detects which previously-indexed pins have been spent in new transactions.
	// idMap maps output (txid:vout) → current owner address.
	// Returns a map of pin ID → transfer info (new location, new owner).
	CatchTransfer(idMap map[string]string) (transferMap map[string]any, err error)

	// GetAddress extracts a human-readable address from a pkScript.
	GetAddress(pkScript []byte) string

	// ZmqTopics returns the ZMQ topics this chain subscribes to (e.g. ["rawtx", "hashblock"]).
	ZmqTopics() []string
}

// ZMQHandler is called by the indexer engine when a ZMQ message arrives.
type ZMQHandler func(topic string, data []byte, sequence uint64)
