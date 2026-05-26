package dogecoin

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// buildMetaIDWitnessTx creates a mock SegWit transaction with MetaID witness data.
func buildMetaIDWitnessTx(t *testing.T) *wire.MsgTx {
	t.Helper()

	// Build a witness script: OP_FALSE OP_IF <protocol_id> OP_ENDIF
	builder := txscript.NewScriptBuilder()
	builder.AddOp(txscript.OP_FALSE)
	builder.AddOp(txscript.OP_IF)
	builder.AddData([]byte("metaid"))
	builder.AddOp(txscript.OP_ENDIF)

	witnessScript, err := builder.Script()
	if err != nil {
		t.Fatalf("failed to build witness script: %v", err)
	}

	// Build a valid P2WSH output script (OP_0 + SHA256 of witness script).
	witnessProg := sha256Sum(witnessScript)
	pkScript := make([]byte, 2+len(witnessProg))
	pkScript[0] = txscript.OP_0
	pkScript[1] = byte(len(witnessProg))
	copy(pkScript[2:], witnessProg)

	tx := wire.NewMsgTx(2)

	outpoint := wire.NewOutPoint(&chainhash.Hash{}, 0)
	txIn := wire.NewTxIn(outpoint, nil, nil)
	txIn.Sequence = wire.MaxTxInSequenceNum
	tx.AddTxIn(txIn)

	witnessData := [][]byte{
		[]byte("dummy-sig"),
		[]byte("dummy-pubkey"),
		[]byte("metaid"),
		[]byte("init"),
		[]byte("/"),
		[]byte("0"),
		[]byte("0"),
		[]byte("text/plain"),
		[]byte("hello metaid doge world"),
	}
	fullWitness := append([][]byte{witnessScript}, witnessData...)
	tx.TxIn[0].Witness = fullWitness

	txOut := wire.NewTxOut(0, pkScript)
	tx.AddTxOut(txOut)

	return tx
}

func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

func TestCatchPinsByTx_BasicMetaID_DOGE(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	tx := buildMetaIDWitnessTx(t)

	pins := idx.catchPinsByTx(tx, 100, 1234567890,
		"0000000000000000000000000000000000000000000000000000000000000000",
		"0000000000000000000000000000000000000000000000000000000000000000",
		0)

	if len(pins) == 0 {
		t.Fatal("catchPinsByTx returned no pins for MetaID witness transaction")
	}

	pin := pins[0]
	if pin.ChainName != "doge" {
		t.Errorf("expected chainName 'doge', got %q", pin.ChainName)
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

func TestGetAddress_DOGE(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	// A valid P2PKH output for a well-known address.
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

func TestCatchPinsByTx_InitPin_DOGE(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	builder := txscript.NewScriptBuilder()
	builder.AddOp(txscript.OP_FALSE)
	builder.AddOp(txscript.OP_IF)
	builder.AddData([]byte("metaid"))
	builder.AddOp(txscript.OP_ENDIF)
	witnessScript, _ := builder.Script()

	tx := wire.NewMsgTx(2)
	outpoint := wire.NewOutPoint(&chainhash.Hash{}, 0)
	txIn := wire.NewTxIn(outpoint, nil, nil)
	txIn.Sequence = wire.MaxTxInSequenceNum
	tx.AddTxIn(txIn)

	witnessData := [][]byte{
		[]byte("sig"), []byte("pubkey"),
		[]byte("metaid"),
		[]byte("init"),
		[]byte("/"),
		[]byte("0"), []byte("0"),
		[]byte("text/plain"),
		[]byte(""),
	}
	tx.TxIn[0].Witness = append([][]byte{witnessScript}, witnessData...)

	witnessProg := sha256Sum(witnessScript)
	pkScript := make([]byte, 2+len(witnessProg))
	pkScript[0] = txscript.OP_0
	pkScript[1] = byte(len(witnessProg))
	copy(pkScript[2:], witnessProg)
	tx.AddTxOut(wire.NewTxOut(0, pkScript))

	pins := idx.catchPinsByTx(tx, 300, 1234567890, "00", "00", 0)
	if len(pins) == 0 {
		t.Fatal("catchPinsByTx returned no pins for init path '/'")
	}

	pin := pins[0]
	if pin.ChainName != "doge" {
		t.Errorf("expected chainName 'doge', got %q", pin.ChainName)
	}
	if pin.Path != "/" {
		t.Errorf("expected path '/', got %q", pin.Path)
	}
	if pin.Operation != "init" {
		t.Errorf("expected operation 'init', got %q", pin.Operation)
	}
	t.Logf("init pin: chain=%s path=%s op=%s", pin.ChainName, pin.Path, pin.Operation)
}

func TestCatchPinsByTx_NonWitness_DOGE(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	tx := wire.NewMsgTx(1) // version 1 = no SegWit
	outpoint := wire.NewOutPoint(&chainhash.Hash{}, 0)
	tx.AddTxIn(wire.NewTxIn(outpoint, nil, nil))
	tx.AddTxOut(wire.NewTxOut(0, []byte{}))

	pins := idx.catchPinsByTx(tx, 100, 1234567890, "00", "00", 0)
	if len(pins) != 0 {
		t.Errorf("expected 0 pins for non-witness tx, got %d", len(pins))
	}
}
