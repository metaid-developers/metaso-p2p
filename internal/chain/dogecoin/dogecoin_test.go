package dogecoin

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

func TestParseDogeBlockTransactionsSkipsAuxPoW(t *testing.T) {
	coinbase := wire.NewMsgTx(1)
	coinbase.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&chainhash.Hash{}, 0xffffffff), []byte{0x01}, nil))
	coinbase.AddTxOut(wire.NewTxOut(1, []byte{0x51}))

	dogeTx := wire.NewMsgTx(1)
	dogeTx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&chainhash.Hash{}, 0), []byte{0x02}, nil))
	dogeTx.AddTxOut(wire.NewTxOut(2, []byte{0x51}))

	block := buildAuxPoWBlockBytes(t, coinbase, dogeTx)
	txs, err := parseDogeBlockTransactions(block, 1)
	if err != nil {
		t.Fatalf("parseDogeBlockTransactions returned error: %v", err)
	}
	if len(txs) != 1 {
		t.Fatalf("expected 1 Dogecoin transaction, got %d", len(txs))
	}
	if got, want := txs[0].TxHash(), dogeTx.TxHash(); got != want {
		t.Fatalf("parsed tx hash = %s, want %s", got, want)
	}
}

func TestNetParamsReturnsDogeAddressPrefixes(t *testing.T) {
	params := NetParams("")
	if params.PubKeyHashAddrID != 0x1e {
		t.Fatalf("expected DOGE P2PKH prefix 0x1e, got 0x%x", params.PubKeyHashAddrID)
	}
	if params.ScriptHashAddrID != 0x16 {
		t.Fatalf("expected DOGE P2SH prefix 0x16, got 0x%x", params.ScriptHashAddrID)
	}
}

func buildAuxPoWBlockBytes(t *testing.T, coinbaseTx, dogeTx *wire.MsgTx) []byte {
	t.Helper()

	var block bytes.Buffer
	header := make([]byte, 80)
	binary.LittleEndian.PutUint32(header[0:4], 0x101)
	block.Write(header)
	writeTx(t, &block, coinbaseTx)
	block.Write(make([]byte, 32))
	block.WriteByte(0)
	block.Write(make([]byte, 4))
	block.WriteByte(0)
	block.Write(make([]byte, 4))
	block.Write(make([]byte, 80))
	block.WriteByte(1)
	writeTx(t, &block, dogeTx)
	return block.Bytes()
}

func writeTx(t *testing.T, dst *bytes.Buffer, tx *wire.MsgTx) {
	t.Helper()
	if err := tx.BtcEncode(dst, 0, wire.BaseEncoding); err != nil {
		t.Fatalf("encode tx: %v", err)
	}
}
