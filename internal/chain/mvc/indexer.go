package mvc

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Indexer implements chain.Indexer for MicrovisionChain (MVC).
// MVC pins use both SegWit witness data and OP_RETURN outputs.
type Indexer struct {
	chainParams *chaincfg.Params
	chain       *Chain
	block       *wire.MsgBlock // current block being processed (for transfer detection)

	// valueOfSpent overrides the RPC spent-output value lookup (test seam).
	valueOfSpent func(txid string, vout int) (int64, error)
}

// Transfer-detection constants, mirroring man-p2p's pin package
// (StandardPinUtxoValue / MeltdownMinPinCount).
const (
	standardPinUtxoValue = 546
	meltdownMinPinCount  = 3
)

func NewIndexer(chain *Chain, params *chaincfg.Params) *Indexer {
	return &Indexer{
		chainParams: params,
		chain:       chain,
	}
}

func (idx *Indexer) Name() string { return "mvc" }

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
// MVC supports two inscription formats:
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
			pinHash, err := mvcMetaIDTxHash(tx)
			if err != nil {
				pinHash = txHash
			}
			pin.Id = fmt.Sprintf("%si%d", pinHash, ownerOutIdx)
			pin.ChainName = "mvc"
			pin.GenesisTransaction = pinHash
			pin.GenesisHeight = height
			pin.Timestamp = timestamp
			pin.Address = address
			pin.CreateAddress = address
			pin.MetaId = address
			pin.GlobalMetaId = address
			pin.Output = fmt.Sprintf("%s:%d", pinHash, ownerOutIdx)
			if ownerOutIdx >= 0 && ownerOutIdx < len(tx.TxOut) {
				pin.OutputValue = tx.TxOut[ownerOutIdx].Value
			}
			pin.IsOpReturn = true
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

// parseOpReturnPin parses MVC OP_RETURN/nonstandard outputs for MetaID data.
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

func mvcMetaIDTxHash(tx *wire.MsgTx) (string, error) {
	if tx == nil {
		return "", errors.New("nil tx")
	}
	if tx.Version < 10 {
		return tx.TxHash().String(), nil
	}

	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return "", err
	}
	parsed, err := parseMVCMetaIDRawTx(buf.Bytes())
	if err != nil {
		return "", err
	}

	var inputBytes, inputScriptHashes, outputBytes []byte
	for _, in := range parsed.inputs {
		inputBytes = append(inputBytes, in.txid...)
		inputBytes = append(inputBytes, in.vout...)
		inputBytes = append(inputBytes, in.sequence...)
		inputScriptHashes = append(inputScriptHashes, sha256Bytes(in.scriptSig)...)
	}
	for _, out := range parsed.outputs {
		outputBytes = append(outputBytes, out.amount...)
		outputBytes = append(outputBytes, sha256Bytes(out.script)...)
	}

	var manRaw []byte
	manRaw = append(manRaw, parsed.version...)
	manRaw = append(manRaw, parsed.lockTime...)
	manRaw = append(manRaw, uint32LittleEndian(uint32(len(parsed.inputs)))...)
	manRaw = append(manRaw, uint32LittleEndian(uint32(len(parsed.outputs)))...)
	manRaw = append(manRaw, sha256Bytes(inputBytes)...)
	manRaw = append(manRaw, sha256Bytes(inputScriptHashes)...)
	manRaw = append(manRaw, sha256Bytes(outputBytes)...)
	return doubleSHA256ReversedHex(manRaw), nil
}

type mvcRawTx struct {
	version  []byte
	inputs   []mvcRawTxIn
	outputs  []mvcRawTxOut
	lockTime []byte
}

type mvcRawTxIn struct {
	txid      []byte
	vout      []byte
	scriptSig []byte
	sequence  []byte
}

type mvcRawTxOut struct {
	amount []byte
	script []byte
}

func parseMVCMetaIDRawTx(raw []byte) (*mvcRawTx, error) {
	if len(raw) < 10 {
		return nil, errors.New("invalid transaction data")
	}

	index := 0
	parsed := &mvcRawTx{}
	parsed.version = raw[index : index+4]
	index += 4

	inCount, n, err := decodeMVCVarInt(raw[index:])
	if err != nil {
		return nil, err
	}
	index += n
	if inCount == 0 {
		return nil, errors.New("invalid transaction data")
	}

	for i := 0; i < inCount; i++ {
		if index+36 > len(raw) {
			return nil, errors.New("invalid transaction data length")
		}
		in := mvcRawTxIn{
			txid: raw[index : index+32],
			vout: raw[index+32 : index+36],
		}
		index += 36

		scriptLen, size, err := decodeMVCVarInt(raw[index:])
		if err != nil {
			return nil, err
		}
		index += size
		if index+scriptLen+4 > len(raw) {
			return nil, errors.New("invalid transaction data length")
		}
		in.scriptSig = raw[index : index+scriptLen]
		index += scriptLen
		in.sequence = raw[index : index+4]
		index += 4
		parsed.inputs = append(parsed.inputs, in)
	}

	outCount, n, err := decodeMVCVarInt(raw[index:])
	if err != nil {
		return nil, err
	}
	index += n
	if outCount == 0 {
		return nil, errors.New("invalid transaction data")
	}

	for i := 0; i < outCount; i++ {
		if index+8 > len(raw) {
			return nil, errors.New("invalid transaction data length")
		}
		out := mvcRawTxOut{amount: raw[index : index+8]}
		index += 8

		scriptLen, size, err := decodeMVCVarInt(raw[index:])
		if err != nil {
			return nil, err
		}
		index += size
		if index+scriptLen > len(raw) {
			return nil, errors.New("invalid transaction data length")
		}
		out.script = raw[index : index+scriptLen]
		index += scriptLen
		parsed.outputs = append(parsed.outputs, out)
	}

	if index+4 != len(raw) {
		return nil, errors.New("invalid transaction data length")
	}
	parsed.lockTime = raw[index : index+4]
	return parsed, nil
}

func decodeMVCVarInt(buf []byte) (int, int, error) {
	if len(buf) == 0 {
		return 0, 0, errors.New("invalid transaction data length")
	}
	switch buf[0] {
	case 0xfd:
		if len(buf) < 3 {
			return 0, 0, errors.New("invalid transaction data length")
		}
		return int(binary.LittleEndian.Uint16(buf[1:3])), 3, nil
	case 0xfe:
		if len(buf) < 5 {
			return 0, 0, errors.New("invalid transaction data length")
		}
		return int(binary.LittleEndian.Uint32(buf[1:5])), 5, nil
	case 0xff:
		if len(buf) < 9 {
			return 0, 0, errors.New("invalid transaction data length")
		}
		value := binary.LittleEndian.Uint64(buf[1:9])
		if value > uint64(^uint(0)>>1) {
			return 0, 0, errors.New("varint too large")
		}
		return int(value), 9, nil
	default:
		return int(buf[0]), 1, nil
	}
}

func uint32LittleEndian(v uint32) []byte {
	out := make([]byte, 4)
	binary.LittleEndian.PutUint32(out, v)
	return out
}

func sha256Bytes(data []byte) []byte {
	sum := sha256.Sum256(data)
	return sum[:]
}

func doubleSHA256ReversedHex(data []byte) string {
	first := sha256.Sum256(data)
	second := sha256.Sum256(first[:])
	out := make([]byte, len(second))
	copy(out, second[:])
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return hex.EncodeToString(out)
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
		ChainName:          "mvc",
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

// DetectTransfers scans the block last fetched by CatchPins for spends of
// the tracked outputs (chain.TransferDetector). Semantics mirror man-p2p's
// MVC CatchTransfer:
//
//   - meltdown detection per transaction (≥3 tracked 546-sat inputs, one
//     output, same addresses),
//   - the NFT lands at output 0 when the spent outpoint is input 0 (or the
//     tx is single-input), otherwise a cumulative input-value walk locates
//     the carrying output,
//   - the passive-offer settlement layout (R-E-3) diverts to output 1 when
//     input0's output0 is the signed price payment (value differs) and
//     output1 carries exactly the item value — see offerDeliveryOutput,
//   - all pointers use the ledger txid (mvcMetaIDTxHash) — sha256d pointers
//     are unresolvable on-chain for version>=10 txs and break downstream
//     transfer construction with [-25] Missing inputs.
func (idx *Indexer) DetectTransfers(idMap map[string]string) (map[string]aggregator.TransferNotice, error) {
	notices := make(map[string]aggregator.TransferNotice)
	if idx.block == nil {
		// CatchPins assigns the block on its normal path; a nil block means
		// an abnormal call order — skip this block's transfer detection
		// instead of nil-panicking.
		return notices, nil
	}
	for _, tx := range idx.block.Transactions {
		isMeltdown := idx.isMeltdownTransaction(tx, idMap, idx.spentOutputValue)
		for _, in := range tx.TxIn {
			id := fmt.Sprintf("%s:%d", in.PreviousOutPoint.Hash.String(), in.PreviousOutPoint.Index)
			fromAddress, ok := idMap[id]
			if !ok {
				continue
			}
			notice, err := idx.transferOwnerAddress(id, tx)
			if err != nil || notice == nil {
				continue
			}
			notice.FromAddress = fromAddress
			notice.Meltdown = isMeltdown
			notices[id] = *notice
		}
	}
	return notices, nil
}

// CatchTransfer implements the legacy Indexer interface shape on top of
// DetectTransfers (kept for interface compatibility; the engine consumes
// the typed DetectTransfers).
func (idx *Indexer) CatchTransfer(idMap map[string]string) (map[string]any, error) {
	notices, err := idx.DetectTransfers(idMap)
	if err != nil || notices == nil {
		return nil, err
	}
	result := make(map[string]any, len(notices))
	for id, notice := range notices {
		result[id] = map[string]any{
			"fromAddress": notice.FromAddress,
			"address":     notice.ToAddress,
			"location":    notice.Location,
			"output":      notice.Output,
			"outputValue": notice.OutputValue,
			"offset":      notice.Offset,
			"isMeltdown":  notice.Meltdown,
		}
	}
	return result, nil
}

// DetectMempoolSpends implements chain.MempoolTransferDetector: scan the
// current mempool transaction list for spends of the tracked outputs
// (mirror of man-p2p's MempoolSpends). Same derivation as the block path
// but no meltdown evaluation — the confirmed hook decides authoritatively.
func (idx *Indexer) DetectMempoolSpends(idMap map[string]string, txList []any) (map[string]aggregator.TransferNotice, error) {
	notices := make(map[string]aggregator.TransferNotice)
	if len(idMap) == 0 {
		return notices, nil
	}
	for _, item := range txList {
		tx, ok := item.(*wire.MsgTx)
		if !ok {
			continue
		}
		for _, in := range tx.TxIn {
			id := fmt.Sprintf("%s:%d", in.PreviousOutPoint.Hash.String(), in.PreviousOutPoint.Index)
			fromAddress, ok := idMap[id]
			if !ok {
				continue
			}
			notice, err := idx.transferOwnerAddress(id, tx)
			if err != nil || notice == nil {
				continue
			}
			notice.FromAddress = fromAddress
			notices[id] = *notice
		}
	}
	return notices, nil
}

// transferOwnerAddress derives the new owner/output of a spent outpoint
// (mirror of man-p2p's GetOWnerAddress). Location is the three-segment
// "<ledgerTxid>:<vout>:<satOffset>" pointer and Output the spendable
// "<ledgerTxid>:<vout>".
func (idx *Indexer) transferOwnerAddress(inputId string, tx *wire.MsgTx) (*aggregator.TransferNotice, error) {
	ledgerTxid, err := mvcMetaIDTxHash(tx)
	if err != nil {
		return nil, err
	}
	notice := &aggregator.TransferNotice{}
	firstInputId := fmt.Sprintf("%s:%d", tx.TxIn[0].PreviousOutPoint.Hash, tx.TxIn[0].PreviousOutPoint.Index)
	// Fast path: single-input txs or the tracked outpoint IS input 0 — the
	// NFT lands at output 0, unless the tx is a passive-offer settlement
	// (R-E-3): output0 is then the signed PRICE payment and the delivery
	// output sits at index 1.
	if len(tx.TxIn) == 1 || firstInputId == inputId {
		if delivery := idx.offerDeliveryOutput(inputId, tx); delivery != nil {
			class, addresses, _, _ := txscript.ExtractPkScriptAddrs(delivery.PkScript, idx.chainParams)
			if len(addresses) > 0 {
				notice.ToAddress = addresses[0].String()
			} else if class.String() == "nulldata" {
				notice.ToAddress = hex.EncodeToString(delivery.PkScript)
			}
			notice.Location = fmt.Sprintf("%s:%d:%d", ledgerTxid, 1, 0)
			notice.Offset = 0
			notice.Output = fmt.Sprintf("%s:%d", ledgerTxid, 1)
			notice.OutputValue = delivery.Value
			return notice, nil
		}
		class, addresses, _, _ := txscript.ExtractPkScriptAddrs(tx.TxOut[0].PkScript, idx.chainParams)
		if len(addresses) > 0 {
			notice.ToAddress = addresses[0].String()
		} else if class.String() == "nulldata" {
			notice.ToAddress = hex.EncodeToString(tx.TxOut[0].PkScript)
		}
		notice.Location = fmt.Sprintf("%s:%d:%d", ledgerTxid, 0, 0)
		notice.Offset = 0
		notice.Output = fmt.Sprintf("%s:%d", ledgerTxid, 0)
		notice.OutputValue = tx.TxOut[0].Value
		return notice, nil
	}
	// Slow path: walk the inputs accumulating their values (RPC lookups)
	// until the running total exceeds the outputs — the output where that
	// happens carries the tracked value through.
	totalOutputValue := int64(0)
	for _, out := range tx.TxOut {
		totalOutputValue += out.Value
	}
	inputValue := int64(0)
	for _, in := range tx.TxIn {
		id := fmt.Sprintf("%s:%d", in.PreviousOutPoint.Hash.String(), in.PreviousOutPoint.Index)
		if id == inputId {
			break
		}
		value, err := idx.spentOutputValue(in.PreviousOutPoint.Hash.String(), int(in.PreviousOutPoint.Index))
		if err != nil {
			return nil, errors.New("get value error")
		}
		inputValue += value
		if inputValue > totalOutputValue {
			return nil, nil
		}
	}
	outputValue := int64(0)
	for i, out := range tx.TxOut {
		outputValue += out.Value
		if outputValue > inputValue {
			class, addresses, _, _ := txscript.ExtractPkScriptAddrs(out.PkScript, idx.chainParams)
			if len(addresses) > 0 {
				notice.ToAddress = addresses[0].String()
			} else if class.String() == "nulldata" {
				notice.ToAddress = hex.EncodeToString(out.PkScript)
			}
			notice.Output = fmt.Sprintf("%s:%d", ledgerTxid, i)
			notice.Location = fmt.Sprintf("%s:%d", notice.Output, out.Value-(outputValue-inputValue))
			notice.Offset = uint64(i)
			notice.OutputValue = out.Value
			break
		}
	}
	return notice, nil
}

// offerDeliveryOutput applies the passive-offer delivery layout rule (R-E-3,
// docs/nft-offer-protocol.md §5 / nft-offer-indexer-requirements.md R-E-3):
// when the tracked item UTXO is input0 and output0 is NOT its delivery
// output — its value differs from the spent item's value, i.e. it is the
// price payment — the delivery output is output1. The client guarantees
// output1 carries exactly the item value (standard P2PKH), so the value
// double-check keeps every other shape (plain transfers, ambiguous layouts)
// on the legacy output-0 read, as does a spent-value lookup failure.
func (idx *Indexer) offerDeliveryOutput(inputId string, tx *wire.MsgTx) *wire.TxOut {
	if len(tx.TxOut) < 2 || len(tx.TxIn) == 0 {
		return nil
	}
	firstInputId := fmt.Sprintf("%s:%d", tx.TxIn[0].PreviousOutPoint.Hash, tx.TxIn[0].PreviousOutPoint.Index)
	if firstInputId != inputId {
		return nil
	}
	itemValue, err := idx.spentOutputValue(tx.TxIn[0].PreviousOutPoint.Hash.String(), int(tx.TxIn[0].PreviousOutPoint.Index))
	if err != nil || itemValue <= 0 {
		return nil
	}
	if tx.TxOut[0].Value == itemValue {
		return nil // output0 is the delivery output (plain transfer layout)
	}
	if tx.TxOut[1].Value != itemValue {
		return nil // ambiguous shape; keep the legacy output-0 read
	}
	return tx.TxOut[1]
}

// isMeltdownTransaction mirrors man-p2p's IsMeltdownTransaction: ≥3 tracked
// 546-sat PIN inputs, exactly one output, and all tracked PIN-input
// addresses equal to the output address. valueOf is the spent-output value
// lookup (RPC in production, in-memory in tests).
func (idx *Indexer) isMeltdownTransaction(tx *wire.MsgTx, idMap map[string]string, valueOf func(txid string, vout int) (int64, error)) bool {
	// Condition 2: exactly one output.
	if len(tx.TxOut) != 1 {
		return false
	}
	_, addresses, _, _ := txscript.ExtractPkScriptAddrs(tx.TxOut[0].PkScript, idx.chainParams)
	if len(addresses) == 0 {
		return false
	}
	outAddress := addresses[0].String()
	if outAddress == "" {
		return false
	}
	pinUtxoCount := 0
	allSameAddress := true
	for _, in := range tx.TxIn {
		id := fmt.Sprintf("%s:%d", in.PreviousOutPoint.Hash.String(), in.PreviousOutPoint.Index)
		fromAddress, ok := idMap[id]
		if !ok {
			continue
		}
		value, err := valueOf(in.PreviousOutPoint.Hash.String(), int(in.PreviousOutPoint.Index))
		if err == nil && value == standardPinUtxoValue {
			pinUtxoCount++
			if fromAddress != outAddress {
				allSameAddress = false
			}
		}
	}
	return pinUtxoCount >= meltdownMinPinCount && allSameAddress
}

// spentOutputValue looks up the sat value of a spent output via the node
// RPC (mirror of man-p2p's GetValueByTx).
func (idx *Indexer) spentOutputValue(txid string, vout int) (int64, error) {
	if idx.valueOfSpent != nil {
		return idx.valueOfSpent(txid, vout)
	}
	if idx.chain == nil {
		return 0, errors.New("mvc chain client not set")
	}
	rawTx, err := idx.chain.GetTransaction(txid)
	if err != nil {
		return 0, err
	}
	var tx *wire.MsgTx
	switch t := rawTx.(type) {
	case *wire.MsgTx:
		tx = t
	case interface{ MsgTx() *wire.MsgTx }:
		tx = t.MsgTx()
	default:
		return 0, errors.New("unexpected transaction type")
	}
	if vout < 0 || vout >= len(tx.TxOut) {
		return 0, errors.New("vout out of range")
	}
	return tx.TxOut[vout].Value, nil
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
