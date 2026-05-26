package opcat

import (
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// buildOpReturnMetaIDTx creates a mock OPCAT transaction with OP_RETURN MetaID data.
// The OP_RETURN output encodes: OP_RETURN "metaid" "create" "/info/test" "0" "0" "text/plain" "hello metaid world"
func buildOpReturnMetaIDTx(t *testing.T) *wire.MsgTx {
	t.Helper()

	tx := wire.NewMsgTx(2)

	// Add a dummy input.
	outpoint := wire.NewOutPoint(&chainhash.Hash{}, 0)
	txIn := wire.NewTxIn(outpoint, nil, nil)
	txIn.Sequence = wire.MaxTxInSequenceNum
	tx.AddTxIn(txIn)

	// Build OP_RETURN output with MetaID data.
	builder := txscript.NewScriptBuilder()
	builder.AddOp(txscript.OP_RETURN)
	builder.AddData([]byte("metaid"))
	builder.AddData([]byte("create"))
	builder.AddData([]byte("/info/test"))
	builder.AddData([]byte("0"))
	builder.AddData([]byte("0"))
	builder.AddData([]byte("text/plain"))
	builder.AddData([]byte("hello metaid world"))
	opReturnScript, err := builder.Script()
	if err != nil {
		t.Fatalf("failed to build OP_RETURN script: %v", err)
	}
	tx.AddTxOut(wire.NewTxOut(0, opReturnScript))

	// Add a P2PKH output for address extraction (dummy script with valid address pattern).
	p2pkhBuilder := txscript.NewScriptBuilder()
	p2pkhBuilder.AddOp(txscript.OP_DUP)
	p2pkhBuilder.AddOp(txscript.OP_HASH160)
	// Push 20-byte dummy hash for address extraction
	dummyHash := make([]byte, 20)
	p2pkhBuilder.AddData(dummyHash)
	p2pkhBuilder.AddOp(txscript.OP_EQUALVERIFY)
	p2pkhBuilder.AddOp(txscript.OP_CHECKSIG)
	p2pkhScript, err := p2pkhBuilder.Script()
	if err != nil {
		t.Fatalf("failed to build P2PKH script: %v", err)
	}
	tx.AddTxOut(wire.NewTxOut(546, p2pkhScript))

	return tx
}

// buildOpReturnCreateTx creates a mock OPCAT transaction with /info/name MetaID data.
func buildOpReturnCreateTx(t *testing.T) *wire.MsgTx {
	t.Helper()

	tx := wire.NewMsgTx(2)
	outpoint := wire.NewOutPoint(&chainhash.Hash{}, 0)
	txIn := wire.NewTxIn(outpoint, nil, nil)
	txIn.Sequence = wire.MaxTxInSequenceNum
	tx.AddTxIn(txIn)

	builder := txscript.NewScriptBuilder()
	builder.AddOp(txscript.OP_RETURN)
	builder.AddData([]byte("metaid"))
	builder.AddData([]byte("create"))
	builder.AddData([]byte("/info/name"))
	builder.AddData([]byte("0"))
	builder.AddData([]byte("0"))
	builder.AddData([]byte("text/plain"))
	builder.AddData([]byte("Alice"))
	opReturnScript, err := builder.Script()
	if err != nil {
		t.Fatalf("failed to build OP_RETURN script: %v", err)
	}
	tx.AddTxOut(wire.NewTxOut(0, opReturnScript))

	// P2PKH output
	p2pkhBuilder := txscript.NewScriptBuilder()
	p2pkhBuilder.AddOp(txscript.OP_DUP)
	p2pkhBuilder.AddOp(txscript.OP_HASH160)
	dummyHash := make([]byte, 20)
	p2pkhBuilder.AddData(dummyHash)
	p2pkhBuilder.AddOp(txscript.OP_EQUALVERIFY)
	p2pkhBuilder.AddOp(txscript.OP_CHECKSIG)
	p2pkhScript, _ := p2pkhBuilder.Script()
	tx.AddTxOut(wire.NewTxOut(546, p2pkhScript))

	return tx
}

func TestCatchPinsByTx_BasicMetaID_OPCAT(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	tx := buildOpReturnMetaIDTx(t)

	pins := idx.catchPinsByTx(tx, 100, 1234567890,
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0000000000000000000000000000000000000000000000000000000000000000",
		0)

	if len(pins) == 0 {
		t.Fatal("catchPinsByTx returned no pins for OP_RETURN MetaID transaction")
	}

	pin := pins[0]
	if pin.ChainName != "opcat" {
		t.Errorf("expected chainName 'opcat', got %q", pin.ChainName)
	}
	if pin.Operation == "" {
		t.Error("expected non-empty operation")
	}
	if pin.Path == "" {
		t.Error("expected non-empty path")
	}
	if pin.ContentBody == nil || len(pin.ContentBody) == 0 {
		t.Error("expected non-empty content body")
	}
	t.Logf("chain=%s op=%s path=%q body=%q",
		pin.ChainName, pin.Operation, pin.Path, string(pin.ContentBody))
}

func TestCatchPinsByTx_InfoNamePin_OPCAT(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	tx := buildOpReturnCreateTx(t)

	pins := idx.catchPinsByTx(tx, 200, 1234567890, "00", "00", 0)

	if len(pins) == 0 {
		t.Fatal("catchPinsByTx returned no pins for /info/name path")
	}

	pin := pins[0]
	if pin.Path != "/info/name" {
		t.Errorf("expected path '/info/name', got %q", pin.Path)
	}
	if pin.Operation != "create" {
		t.Errorf("expected operation 'create', got %q", pin.Operation)
	}
	if string(pin.ContentBody) != "Alice" {
		t.Errorf("expected contentBody 'Alice', got %q", string(pin.ContentBody))
	}
	t.Logf("path=%s op=%s body=%s", pin.Path, pin.Operation, string(pin.ContentBody))
}

func TestCatchPinsByTx_NonOpReturn_OPCAT(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	// Transaction with no OP_RETURN output.
	tx := wire.NewMsgTx(2)
	outpoint := wire.NewOutPoint(&chainhash.Hash{}, 0)
	txIn := wire.NewTxIn(outpoint, nil, nil)
	txIn.Sequence = wire.MaxTxInSequenceNum
	tx.AddTxIn(txIn)

	// Only a regular P2PKH output, no OP_RETURN.
	p2pkhBuilder := txscript.NewScriptBuilder()
	p2pkhBuilder.AddOp(txscript.OP_DUP)
	p2pkhBuilder.AddOp(txscript.OP_HASH160)
	dummyHash := make([]byte, 20)
	p2pkhBuilder.AddData(dummyHash)
	p2pkhBuilder.AddOp(txscript.OP_EQUALVERIFY)
	p2pkhBuilder.AddOp(txscript.OP_CHECKSIG)
	p2pkhScript, _ := p2pkhBuilder.Script()
	tx.AddTxOut(wire.NewTxOut(546, p2pkhScript))

	pins := idx.catchPinsByTx(tx, 100, 1234567890, "00", "00", 0)
	if len(pins) != 0 {
		t.Errorf("expected 0 pins for non-OP_RETURN tx, got %d", len(pins))
	}
}

func TestGetAddress_OPCAT(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	// A valid P2PK output script for a well-known Bitcoin address.
	scriptHex := "4104678afdb0fe5548271967f1a67130b7105cd6a828e03909a67962e0ea1f61deb649f6bc3f4cef38c4f35504e51ec112de5c384df7ba0b8d578a4c702b6bf11d5fac"
	scriptBytes, err := hex.DecodeString(scriptHex)
	if err != nil {
		t.Fatalf("failed to decode hex script: %v", err)
	}

	addr := idx.GetAddress(scriptBytes)
	if addr == "" {
		t.Error("GetAddress returned empty for valid P2PK script")
	}
	t.Logf("address from P2PK script: %s", addr)
}
