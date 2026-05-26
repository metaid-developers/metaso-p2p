package dogecoin

import (
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	"github.com/metaid-developers/meta-socket/internal/aggregator"
)

// Indexer implements chain.Indexer for Dogecoin (DOGE).
// DOGE pins use both SegWit witness data and OP_RETURN outputs.
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

func (idx *Indexer) Name() string { return "doge" }

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
		for _, in := range tx.TxIn {
			txIDs = append(txIDs, fmt.Sprintf("%s:%d",
				in.PreviousOutPoint.Hash.String(),
				in.PreviousOutPoint.Index))
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
		txPins := idx.catchPinsByTx(tx, -1, timestamp, "none", "none", i)
		pins = append(pins, txPins...)
	}

	return pins, txIDs, nil
}

// catchPinsByTx parses MetaID protocol data from a single transaction.
// DOGE supports two inscription formats:
//  1. OP_RETURN + metaid marker (nonstandard outputs)
//  2. SegWit witness envelope: OP_FALSE OP_IF "metaid" ... OP_ENDIF
func (idx *Indexer) catchPinsByTx(tx *wire.MsgTx, height int64, timestamp int64,
	blockHash, merkleRoot string, txIndex int) []*aggregator.PinInscription {

	txHash := tx.TxHash().String()
	var pins []*aggregator.PinInscription

	// Check OP_RETURN and nonstandard outputs first
	for outIdx, out := range tx.TxOut {
		class, _, _, _ := txscript.ExtractPkScriptAddrs(out.PkScript, idx.chainParams)
		if class.String() == "nonstandard" || class.String() == "nulldata" {
			pin := idx.parseOpReturnPin(out.PkScript)
			if pin == nil {
				continue
			}

			address, ownerOutIdx, _ := idx.getPinOwner(tx)
			pin.Id = fmt.Sprintf("%si%d", txHash, ownerOutIdx)
			pin.ChainName = "doge"
			pin.GenesisTransaction = txHash
			pin.GenesisHeight = height
			pin.Timestamp = timestamp
			pin.Address = address
			pin.CreateAddress = address
			pin.MetaId = address
			pin.GlobalMetaId = address
			pin.Output = fmt.Sprintf("%s:%d", txHash, ownerOutIdx)
			_ = outIdx

			pins = append(pins, pin)
		}
	}

	if len(pins) > 0 {
		return pins
	}

	// Fall back to SegWit witness parsing (same as BTC)
	if !tx.HasWitness() {
		return nil
	}

	for outIdx, out := range tx.TxOut {
		if len(out.PkScript) == 0 {
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

// parseOpReturnPin parses DOGE OP_RETURN/nonstandard outputs for MetaID data.
// Format: OP_RETURN "metaid" <operation> <path> <encryption> <version> <contentType> <contentBody>
func (idx *Indexer) parseOpReturnPin(pkScript []byte) *aggregator.PinInscription {
	tokenizer := txscript.MakeScriptTokenizer(0, pkScript)
	for tokenizer.Next() {
		if tokenizer.Opcode() == txscript.OP_RETURN {
			if !tokenizer.Next() {
				return nil
			}
			if hex.EncodeToString(tokenizer.Data()) != "6d6574616964" { // "metaid"
				return nil
			}
			return idx.parseOpReturnFields(&tokenizer)
		}
	}
	return nil
}

// parseOpReturnFields parses the data fields following the metaid marker in OP_RETURN.
func (idx *Indexer) parseOpReturnFields(tokenizer *txscript.ScriptTokenizer) *aggregator.PinInscription {
	var infoList [][]byte
	for tokenizer.Next() {
		infoList = append(infoList, tokenizer.Data())
	}
	if err := tokenizer.Err(); err != nil {
		return nil
	}
	if len(infoList) < 1 {
		return nil
	}

	operation := strings.ToLower(string(infoList[0]))
	if operation == "init" {
		return &aggregator.PinInscription{
			Path:      "/",
			Operation: "init",
		}
	}
	if len(infoList) < 6 && operation != "revoke" {
		return nil
	}
	if operation == "revoke" && len(infoList) < 5 {
		return nil
	}

	encryption := "0"
	if len(infoList) > 2 && infoList[2] != nil {
		encryption = string(infoList[2])
	}
	version := "0"
	if len(infoList) > 3 && infoList[3] != nil {
		version = string(infoList[3])
	}
	contentType := "application/json"
	if len(infoList) > 4 && infoList[4] != nil {
		contentType = strings.ToLower(string(infoList[4]))
	}
	var body []byte
	for i := 5; i < len(infoList); i++ {
		body = append(body, infoList[i]...)
	}

	pin := &aggregator.PinInscription{
		Path:        strings.ToLower(string(infoList[1])),
		Operation:   operation,
		ContentType: contentType,
		ContentBody: body,
	}
	// Encryption and version are embedded in the content encoding;
	// not stored as separate fields in PinInscription.
	_ = encryption
	_ = version
	return pin
}

// getPinOwner finds the first non-data output to use as the pin owner address.
func (idx *Indexer) getPinOwner(tx *wire.MsgTx) (address string, outIdx int, locationIdx int64) {
	for i, out := range tx.TxOut {
		class, addresses, _, _ := txscript.ExtractPkScriptAddrs(out.PkScript, idx.chainParams)
		if class.String() != "nulldata" && class.String() != "nonstandard" && len(addresses) > 0 {
			address = addresses[0].String()
			outIdx = i
			locationIdx = 0
			break
		}
	}
	return
}

// parseMetaIDWitness extracts MetaID fields from SegWit witness data.
func parseMetaIDWitness(tx *wire.MsgTx, outIdx int, height int64, timestamp int64,
	blockHash, merkleRoot string, txIndex int, txHash string, params *chaincfg.Params) (*aggregator.PinInscription, error) {

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

	// Get creator address
	creatorAddr := extractCreatorAddress(tx, params)

	pin := &aggregator.PinInscription{
		Id:                 fmt.Sprintf("%s:i%d", txHash, outIdx),
		Path:               path,
		Operation:          operation,
		ChainName:          "doge",
		GenesisTransaction: txHash,
		GenesisHeight:      height,
		Timestamp:          timestamp,
		Address:            creatorAddr,
		CreateAddress:      creatorAddr,
		MetaId:             creatorAddr,
		GlobalMetaId:       creatorAddr,
		Output:             fmt.Sprintf("%s:%d", txHash, outIdx),
	}

	// Parse additional fields if present
	if len(witness) > dataStart+5 {
		pin.ContentType = string(witness[dataStart+5])
	}
	if len(witness) > dataStart+6 {
		pin.ContentBody = witness[dataStart+6]
	}

	return pin, nil
}

func extractCreatorAddress(tx *wire.MsgTx, params *chaincfg.Params) string {
	for _, out := range tx.TxOut {
		_, addresses, _, _ := txscript.ExtractPkScriptAddrs(out.PkScript, params)
		if len(addresses) > 0 {
			return addresses[0].String()
		}
	}
	return ""
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
