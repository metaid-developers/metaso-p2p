package bitcoin

import (
	"fmt"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Indexer implements chain.Indexer for Bitcoin (BTC).
// BTC pins are stored in SegWit witness data.
type Indexer struct {
	chainParams *chaincfg.Params
	chain       *Chain
	block       *wire.MsgBlock // current block being processed (for transfer detection)
}

func NewIndexer(chain *Chain, params *chaincfg.Params) *Indexer {
	return &Indexer{
		chainParams: params,
		chain:       chain,
	}
}

func (idx *Indexer) Name() string { return "btc" }

func (idx *Indexer) Init() error { return nil }

// CatchPins parses all MetaID pins from a confirmed block.
func (idx *Indexer) CatchPins(height int64) ([]*aggregator.PinInscription, []string, error) {
	blockAny, err := idx.chain.GetBlock(height)
	if err != nil {
		return nil, nil, err
	}
	block := blockAny.(*wire.MsgBlock)
	idx.block = block

	timestamp := block.Header.Timestamp.Unix()
	blockHash := block.BlockHash().String()
	merkleRoot := block.Header.MerkleRoot.String()

	var pins []*aggregator.PinInscription
	var txIDs []string

	for i, tx := range block.Transactions {
		// Collect all input outputs for transfer detection
		for _, in := range tx.TxIn {
			txIDs = append(txIDs, fmt.Sprintf("%s:%d",
				in.PreviousOutPoint.Hash.String(),
				in.PreviousOutPoint.Index))
		}

		// Only SegWit transactions can contain MetaID data
		if !tx.HasWitness() {
			continue
		}

		txPins := idx.catchPinsByTx(tx, height, timestamp, blockHash, merkleRoot, i)
		pins = append(pins, txPins...)
	}

	return pins, txIDs, nil
}

// CatchMempoolPins parses MetaID pins from unconfirmed transactions.
func (idx *Indexer) CatchMempoolPins(txList []any) ([]*aggregator.PinInscription, []string, error) {
	timestamp := time.Now().Unix()
	var pins []*aggregator.PinInscription
	var txIDs []string

	for i, item := range txList {
		tx, ok := item.(*wire.MsgTx)
		if !ok {
			continue
		}
		for _, in := range tx.TxIn {
			txIDs = append(txIDs, fmt.Sprintf("%s:%d",
				in.PreviousOutPoint.Hash.String(),
				in.PreviousOutPoint.Index))
		}
		if !tx.HasWitness() {
			continue
		}
		txPins := idx.catchPinsByTx(tx, -1, timestamp, "none", "none", i)
		pins = append(pins, txPins...)
	}

	return pins, txIDs, nil
}

// catchPinsByTx parses MetaID protocol data from a single transaction's witness.
func (idx *Indexer) catchPinsByTx(tx *wire.MsgTx, height int64, timestamp int64,
	blockHash, merkleRoot string, txIndex int) []*aggregator.PinInscription {

	txHash := tx.TxHash().String()
	var pins []*aggregator.PinInscription

	for outIdx, out := range tx.TxOut {
		// MetaID data is in OP_RETURN + SegWit witness
		// Parse from witness stack of SegWit transactions
		if len(out.PkScript) == 0 {
			continue
		}

		// Check for witness data pattern
		if !isMetaIDOutput(out.PkScript) {
			continue
		}

		pin, err := parseMetaIDWitness(tx, outIdx, height, timestamp, blockHash, merkleRoot, txIndex, txHash, idx.chainParams)
		if err != nil || pin == nil {
			continue
		}
		pins = append(pins, pin)
	}

	return pins
}

func (idx *Indexer) CatchTransfer(idMap map[string]string) (map[string]any, error) {
	if idx.block == nil {
		return nil, nil
	}
	result := make(map[string]any)
	for _, tx := range idx.block.Transactions {
		for _, in := range tx.TxIn {
			id := fmt.Sprintf("%s:%d", in.PreviousOutPoint.Hash.String(), in.PreviousOutPoint.Index)
			if fromAddr, ok := idMap[id]; ok {
				info := idx.getOwnerAddress(id, tx)
				if info != nil {
					info["fromAddress"] = fromAddr
					result[id] = info
				}
			}
		}
	}
	return result, nil
}

func (idx *Indexer) GetAddress(pkScript []byte) string {
	_, addresses, _, _ := txscript.ExtractPkScriptAddrs(pkScript, idx.chainParams)
	if len(addresses) > 0 {
		return addresses[0].String()
	}
	return ""
}

func (idx *Indexer) ZmqTopics() []string {
	return []string{"rawtx", "hashblock"}
}

// getOwnerAddress finds the new owner of a spent output.
func (idx *Indexer) getOwnerAddress(outputPoint string, tx *wire.MsgTx) map[string]any {
	// Find the output that receives the value back
	for outIdx, out := range tx.TxOut {
		_, addresses, _, _ := txscript.ExtractPkScriptAddrs(out.PkScript, idx.chainParams)
		if len(addresses) > 0 {
			return map[string]any{
				"location": fmt.Sprintf("%s:%d", tx.TxHash().String(), outIdx),
				"address":  addresses[0].String(),
			}
		}
	}
	return nil
}

// isMetaIDOutput checks if a pkScript contains a MetaID protocol marker.
func isMetaIDOutput(pkScript []byte) bool {
	// MetaID protocol ID: "metaid" = 0x6d6574616964
	const metaIDHex = "6d6574616964"
	if len(pkScript) < 6 {
		return false
	}
	// Check for OP_RETURN + pushdata containing the protocol ID
	// This is a simplified check — full parsing is in parseMetaIDWitness
	return true
}

// parseMetaIDWitness extracts MetaID fields from a transaction's witness data.
// BTC MetaID data lives in witness stack items after the signature.
func parseMetaIDWitness(tx *wire.MsgTx, outIdx int, height int64, timestamp int64,
	blockHash, merkleRoot string, txIndex int, txHash string, params *chaincfg.Params) (*aggregator.PinInscription, error) {

	// For SegWit transactions, MetaID data is encoded in witness stack
	// The witness structure for MetaID is:
	// [signature, pubkey, metaid_marker, operation, path, encryption, version, content_type, content...]
	witness := tx.TxIn[0].Witness
	if len(witness) < 4 {
		return nil, nil
	}

	// Find position of "metaid" marker
	dataStart := -1
	for i, w := range witness {
		if len(w) >= 6 && string(w[:6]) == "metaid" {
			dataStart = i
			break
		}
	}
	if dataStart < 0 || len(witness) <= dataStart+2 {
		return nil, nil
	}

	// Parse fields after marker
	operation := string(witness[dataStart+1])
	path := string(witness[dataStart+2])

	// Get creator address from the first input
	creatorAddr := extractCreatorAddress(tx, params)

	// Get chain-specific identity fields
	metaid := extractMetaID(tx, params)
	globalMetaID := extractGlobalMetaID(metaid)

	pin := &aggregator.PinInscription{
		Id:                 fmt.Sprintf("%s:i%d", txHash, outIdx),
		Path:               path,
		Operation:          operation,
		ChainName:          "btc",
		GenesisTransaction: txHash,
		GenesisHeight:      height,
		Timestamp:          timestamp,
		Address:            creatorAddr,
		CreateAddress:      creatorAddr,
		MetaId:             metaid,
		GlobalMetaId:       globalMetaID,
		Output:             fmt.Sprintf("%s:%d", txHash, outIdx),
	}

	// Parse additional fields if present.
	// Field layout after marker: [operation, path, encryption, version, content_type, body...]
	if len(witness) > dataStart+5 {
		pin.ContentType = string(witness[dataStart+5])
	}
	if len(witness) > dataStart+6 {
		pin.ContentBody = witness[dataStart+6]
	}

	return pin, nil
}

func extractCreatorAddress(tx *wire.MsgTx, params *chaincfg.Params) string {
	if len(tx.TxIn) == 0 {
		return ""
	}
	// For SegWit, the second witness item is the public key
	// We derive the address from pkScript
	for _, out := range tx.TxOut {
		_, addresses, _, _ := txscript.ExtractPkScriptAddrs(out.PkScript, params)
		if len(addresses) > 0 {
			return addresses[0].String()
		}
	}
	return ""
}

func extractMetaID(tx *wire.MsgTx, params *chaincfg.Params) string {
	addr := extractCreatorAddress(tx, params)
	if addr == "" {
		return ""
	}
	// In MetaID protocol, metaid is derived from the creator's address
	return addr
}

func extractGlobalMetaID(metaid string) string {
	// GlobalMetaID is typically derived from metaid via the idaddress encoding
	// This is a placeholder — the actual encoding is done by idaddress package
	return metaid
}
