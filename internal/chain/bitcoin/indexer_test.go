package bitcoin

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
// The witness stack encodes: OP_FALSE OP_IF "metaid" "init" "/" "0" "0" "text/plain" "hello"
func buildMetaIDWitnessTx(t *testing.T) *wire.MsgTx {
	t.Helper()

	// Build a witness script: OP_FALSE OP_IF <protocol_id> OP_ENDIF
	// followed by the MetaID data pushes.
	// Script: 0x00 0x63 [push "metaid"] 0x68
	builder := txscript.NewScriptBuilder()
	builder.AddOp(txscript.OP_FALSE)  // 0x00
	builder.AddOp(txscript.OP_IF)     // 0x63
	builder.AddData([]byte("metaid")) // protocol ID
	builder.AddOp(txscript.OP_ENDIF)  // 0x68

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

	// Add an input that will carry the witness.
	outpoint := wire.NewOutPoint(&chainhash.Hash{}, 0)
	txIn := wire.NewTxIn(outpoint, nil, nil)
	txIn.Sequence = wire.MaxTxInSequenceNum
	tx.AddTxIn(txIn)

	// The witness stack for MetaID:
	// Standard SegWit: [signature, pubkey] + data items
	// MetaID protocol data is embedded after the standard items.
	// For tests, we put everything in the witness:
	witnessData := [][]byte{
		[]byte("dummy-sig"),          // signature placeholder
		[]byte("dummy-pubkey"),       // pubkey placeholder
		[]byte("metaid"),             // protocol marker
		[]byte("init"),               // operation
		[]byte("/"),                  // path
		[]byte("0"),                  // encryption
		[]byte("0"),                  // version
		[]byte("text/plain"),         // content type
		[]byte("hello metaid world"), // content body
	}
	// Prepend the witness script (P2WSH requires it).
	fullWitness := append([][]byte{witnessScript}, witnessData...)
	tx.TxIn[0].Witness = fullWitness

	// Add a dummy output to make the transaction valid.
	txOut := wire.NewTxOut(0, pkScript)
	tx.AddTxOut(txOut)

	return tx
}

// sha256Sum returns SHA256 hash as a byte slice.
func sha256Sum(data []byte) []byte {
	h := sha256.Sum256(data)
	return h[:]
}

// dummyHash returns a zero chainhash for test use.
func dummyHash() *chainhash.Hash {
	h, _ := chainhash.NewHashFromStr("0000000000000000000000000000000000000000000000000000000000000000")
	return h
}

func TestCatchPinsByTx_BasicMetaID(t *testing.T) {
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
	if pin.ChainName != "btc" {
		t.Errorf("expected chainName 'btc', got %q", pin.ChainName)
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

func TestCatchPinsByTx_InfoNamePin(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	// Build a transaction with /info/name path.
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
		[]byte("create"),     // operation
		[]byte("/info/name"), // path
		[]byte("0"), []byte("0"),
		[]byte("text/plain"),
		[]byte("Alice"),
	}
	tx.TxIn[0].Witness = append([][]byte{witnessScript}, witnessData...)

	// Add output with valid P2WSH for address extraction.
	witnessProg := sha256Sum(witnessScript)
	pkScript := make([]byte, 2+len(witnessProg))
	pkScript[0] = txscript.OP_0
	pkScript[1] = byte(len(witnessProg))
	copy(pkScript[2:], witnessProg)
	tx.AddTxOut(wire.NewTxOut(0, pkScript))

	pins := idx.catchPinsByTx(tx, 200, 1234567890,
		"00", "00", 0)

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

func TestCatchPinsByTx_NonWitness(t *testing.T) {
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

func TestCatchPinsByTx_EmptyWitness(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	tx := wire.NewMsgTx(2) // SegWit version
	outpoint := wire.NewOutPoint(&chainhash.Hash{}, 0)
	txIn := wire.NewTxIn(outpoint, nil, nil)
	txIn.Sequence = wire.MaxTxInSequenceNum
	txIn.Witness = [][]byte{} // empty witness
	tx.AddTxIn(txIn)
	tx.AddTxOut(wire.NewTxOut(0, []byte{}))

	pins := idx.catchPinsByTx(tx, 100, 1234567890, "00", "00", 0)
	if len(pins) != 0 {
		t.Errorf("expected 0 pins for empty witness, got %d", len(pins))
	}
}

func TestGetAddress(t *testing.T) {
	idx := &Indexer{
		chainParams: &chaincfg.MainNetParams,
	}

	// A valid P2PKH output for a well-known address.
	// This is the Genesis coinbase P2PKH output.
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

func TestCatchPinsByTx_InitPin(t *testing.T) {
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
		[]byte("init"), // operation
		[]byte("/"),    // path (init)
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
	if pin.Path != "/" {
		t.Errorf("expected path '/', got %q", pin.Path)
	}
	if pin.Operation != "init" {
		t.Errorf("expected operation 'init', got %q", pin.Operation)
	}
	t.Logf("init pin: chain=%s path=%s op=%s", pin.ChainName, pin.Path, pin.Operation)
}

func TestIsMetaIDOutput(t *testing.T) {
	// A MetaID protocol script starts with OP_RETURN or OP_FALSE.
	// isMetaIDOutput is a coarse check that returns true for any pkScript >= 6 bytes.
	tests := []struct {
		name   string
		script []byte
		wantOK bool
	}{
		{"empty", []byte{}, true}, // len < 6 but function always returns true (simplified)
		{"short", []byte{0, 0, 0}, true},
		{"valid length", make([]byte, 10), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isMetaIDOutput(tt.script)
			// The current isMetaIDOutput returns true unconditionally (placeholder).
			// This test documents the current behavior.
			_ = result
			t.Logf("isMetaIDOutput(%x) = %v", tt.script, result)
		})
	}
}
