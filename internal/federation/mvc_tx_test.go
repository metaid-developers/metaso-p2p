package federation

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/bitcoinsv/bsvd/chaincfg"
	"github.com/bitcoinsv/bsvd/txscript"
	"github.com/bitcoinsv/bsvd/wire"
	"github.com/bitcoinsv/bsvutil"
)

const (
	mvcTestPrivateKeyHex = "0000000000000000000000000000000000000000000000000000000000000001"
	mvcTestPublicKeyHex  = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	mvcTestAddress       = "1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH"
	mvcTestPkScriptHex   = "76a914751e76e8199196d454941c45d1b3a323f1433bd688ac"
)

func TestMVCIdentityFromPrivateKeyDerivesExpectedMainnetAddress(t *testing.T) {
	address, publicKey, err := MVCIdentityFromPrivateKey(mvcTestPrivateKeyHex, "mvc-mainnet")
	if err != nil {
		t.Fatalf("MVCIdentityFromPrivateKey returned error: %v", err)
	}

	if address != mvcTestAddress {
		t.Fatalf("address: want %q got %q", mvcTestAddress, address)
	}
	if publicKey != mvcTestPublicKeyHex {
		t.Fatalf("public key: want %q got %q", mvcTestPublicKeyHex, publicKey)
	}
}

func TestBuildMVCRegistryTxBuildsSignedMetaIDRegistryPin(t *testing.T) {
	content := []byte(`{"nodeId":"node-a","payload":"` + strings.Repeat("a", 600) + `"}`)
	utxo := mvcRegistryTestUTXO(t, 50_000)

	result, err := BuildMVCRegistryTx(MVCRegistryTxRequest{
		Net:           "mvc-mainnet",
		PrivateKeyHex: mvcTestPrivateKeyHex,
		UTXOs:         []MVCRegistryUTXO{utxo},
		Operation:     "modify",
		Content:       content,
		FeeRate:       2,
		DustAmount:    1,
	})
	if err != nil {
		t.Fatalf("BuildMVCRegistryTx returned error: %v", err)
	}

	if result.Address != mvcTestAddress {
		t.Fatalf("result address: want %q got %q", mvcTestAddress, result.Address)
	}
	if result.PublicKey != mvcTestPublicKeyHex {
		t.Fatalf("result public key: want %q got %q", mvcTestPublicKeyHex, result.PublicKey)
	}
	if result.Tx == nil {
		t.Fatal("result Tx is nil")
	}
	if result.Tx.Version != 10 {
		t.Fatalf("tx version: want 10 got %d", result.Tx.Version)
	}
	if result.OwnerOutputIndex != 0 {
		t.Fatalf("owner output index: want 0 got %d", result.OwnerOutputIndex)
	}
	if result.ChangeOutputIndex != 2 {
		t.Fatalf("change output index: want 2 got %d", result.ChangeOutputIndex)
	}
	if len(result.Tx.TxOut) != 3 {
		t.Fatalf("tx outputs: want owner, op_return, change; got %d", len(result.Tx.TxOut))
	}
	if result.Tx.TxOut[result.OwnerOutputIndex].Value != 1 {
		t.Fatalf("owner output amount: want 1 got %d", result.Tx.TxOut[result.OwnerOutputIndex].Value)
	}
	if result.ChangeAmount <= 0 {
		t.Fatalf("change amount should be positive, got %d", result.ChangeAmount)
	}
	if result.Tx.TxOut[result.ChangeOutputIndex].Value != result.ChangeAmount {
		t.Fatalf("change output amount: want %d got %d", result.ChangeAmount, result.Tx.TxOut[result.ChangeOutputIndex].Value)
	}

	ownerAddress := extractOnlyAddress(t, result.Tx.TxOut[result.OwnerOutputIndex].PkScript)
	if ownerAddress != mvcTestAddress {
		t.Fatalf("owner output address: want %q got %q", mvcTestAddress, ownerAddress)
	}
	changeAddress := extractOnlyAddress(t, result.Tx.TxOut[result.ChangeOutputIndex].PkScript)
	if changeAddress != mvcTestAddress {
		t.Fatalf("change output address: want %q got %q", mvcTestAddress, changeAddress)
	}

	fields := parseRegistryOpReturnFields(t, result.Tx.TxOut[1].PkScript)
	wantFields := []string{"metaid", "modify", RegistryPath, "0", Version, "application/json"}
	if len(fields) < len(wantFields) {
		t.Fatalf("op_return fields: want at least %d got %d", len(wantFields), len(fields))
	}
	for i, want := range wantFields {
		if got := string(fields[i]); got != want {
			t.Fatalf("op_return field %d: want %q got %q", i, want, got)
		}
	}
	if got := bytes.Join(fields[6:], nil); !bytes.Equal(got, content) {
		t.Fatalf("op_return content mismatch: want %d bytes got %d", len(content), len(got))
	}
	if len(fields[6:]) < 2 {
		t.Fatalf("content should be chunked across multiple pushes, got %d chunk(s)", len(fields[6:]))
	}
	for i, chunk := range fields[6:] {
		if len(chunk) > 520 {
			t.Fatalf("content chunk %d exceeds 520 bytes: %d", i, len(chunk))
		}
	}

	fee := utxo.Value - result.Tx.TxOut[result.OwnerOutputIndex].Value - result.Tx.TxOut[result.ChangeOutputIndex].Value
	if result.Fee != fee {
		t.Fatalf("result fee: want %d got %d", fee, result.Fee)
	}
	minFee := int64(result.Tx.SerializeSize()) * 2
	if result.Fee < minFee {
		t.Fatalf("fee below requested rate after signing: fee=%d min=%d size=%d", result.Fee, minFee, result.Tx.SerializeSize())
	}

	if len(result.Tx.TxIn) != 1 || len(result.Tx.TxIn[0].SignatureScript) == 0 {
		t.Fatal("transaction input is not signed")
	}
	assertInputUsesForkIDSignature(t, result.Tx, 0)
	pkScript, err := hex.DecodeString(utxo.PkScript)
	if err != nil {
		t.Fatalf("decode test pkScript: %v", err)
	}
	engine, err := txscript.NewEngine(pkScript, result.Tx, 0, txscript.StandardVerifyFlags, nil, nil, utxo.Value)
	if err != nil {
		t.Fatalf("create script engine: %v", err)
	}
	if err := engine.Execute(); err != nil {
		t.Fatalf("signature script does not verify: %v", err)
	}

	rawBytes, err := hex.DecodeString(result.RawTx)
	if err != nil {
		t.Fatalf("raw tx is not hex: %v", err)
	}
	var decoded wire.MsgTx
	if err := decoded.Deserialize(bytes.NewReader(rawBytes)); err != nil {
		t.Fatalf("raw tx does not deserialize: %v", err)
	}
	if decoded.SerializeSize() != result.Tx.SerializeSize() {
		t.Fatalf("raw tx size: want %d got %d", result.Tx.SerializeSize(), decoded.SerializeSize())
	}
}

func TestBuildMVCRegistryTxAcceptsCreateModifyAndRevoke(t *testing.T) {
	for _, operation := range []string{"create", "modify", "revoke"} {
		t.Run(operation, func(t *testing.T) {
			result, err := BuildMVCRegistryTx(MVCRegistryTxRequest{
				Net:           "mvc-mainnet",
				PrivateKeyHex: mvcTestPrivateKeyHex,
				UTXOs:         []MVCRegistryUTXO{mvcRegistryTestUTXO(t, 20_000)},
				Operation:     operation,
				Path:          "/protocols/custom-node",
				Version:       "2.0.0",
				Content:       []byte(`{"ok":true}`),
				FeeRate:       1,
				DustAmount:    1,
			})
			if err != nil {
				t.Fatalf("BuildMVCRegistryTx returned error: %v", err)
			}

			fields := parseRegistryOpReturnFields(t, result.Tx.TxOut[1].PkScript)
			if got := string(fields[1]); got != operation {
				t.Fatalf("operation: want %q got %q", operation, got)
			}
			if got := string(fields[2]); got != "/protocols/custom-node" {
				t.Fatalf("path: want custom path got %q", got)
			}
			if got := string(fields[4]); got != "2.0.0" {
				t.Fatalf("version: want 2.0.0 got %q", got)
			}
			if got := string(fields[5]); got != "application/json" {
				t.Fatalf("content type: want application/json got %q", got)
			}
		})
	}
}

func TestBuildMVCRegistryTxRejectsEmptyCreateModifyContent(t *testing.T) {
	for _, operation := range []string{"create", "modify"} {
		t.Run(operation, func(t *testing.T) {
			_, err := BuildMVCRegistryTx(MVCRegistryTxRequest{
				Net:           "mvc-mainnet",
				PrivateKeyHex: mvcTestPrivateKeyHex,
				UTXOs:         []MVCRegistryUTXO{mvcRegistryTestUTXO(t, 20_000)},
				Operation:     operation,
				Content:       nil,
				FeeRate:       1,
				DustAmount:    1,
			})
			if err == nil {
				t.Fatal("expected empty content error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), "content") {
				t.Fatalf("error should mention content, got %v", err)
			}
		})
	}
}

func TestBuildMVCRegistryTxRejectsInvalidOperation(t *testing.T) {
	_, err := BuildMVCRegistryTx(MVCRegistryTxRequest{
		Net:           "mvc-mainnet",
		PrivateKeyHex: mvcTestPrivateKeyHex,
		UTXOs:         []MVCRegistryUTXO{mvcRegistryTestUTXO(t, 20_000)},
		Operation:     "delete",
		Content:       []byte(`{}`),
		FeeRate:       1,
		DustAmount:    1,
	})
	if err == nil {
		t.Fatal("expected invalid operation error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "operation") {
		t.Fatalf("error should mention operation, got %v", err)
	}
}

func TestBuildMVCRegistryTxReturnsErrorWhenUTXOIsInsufficient(t *testing.T) {
	_, err := BuildMVCRegistryTx(MVCRegistryTxRequest{
		Net:           "mvc-mainnet",
		PrivateKeyHex: mvcTestPrivateKeyHex,
		UTXOs:         []MVCRegistryUTXO{mvcRegistryTestUTXO(t, 2)},
		Operation:     "create",
		Content:       []byte(`{}`),
		FeeRate:       10,
		DustAmount:    1,
	})
	if err == nil {
		t.Fatal("expected insufficient UTXO error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "insufficient") {
		t.Fatalf("error should mention insufficient funds, got %v", err)
	}
}

func mvcRegistryTestUTXO(t *testing.T, value int64) MVCRegistryUTXO {
	t.Helper()

	return MVCRegistryUTXO{
		TxID:     "1111111111111111111111111111111111111111111111111111111111111111",
		OutIndex: 0,
		Value:    value,
		Address:  mvcTestAddress,
		PkScript: mvcTestPkScriptHex,
	}
}

func parseRegistryOpReturnFields(t *testing.T, pkScript []byte) [][]byte {
	t.Helper()

	if len(pkScript) < 2 {
		t.Fatalf("op_return script too short: %d bytes", len(pkScript))
	}
	if pkScript[0] != txscript.OP_0 {
		t.Fatalf("first opcode: want OP_0 got 0x%x", pkScript[0])
	}
	if pkScript[1] != txscript.OP_RETURN {
		t.Fatalf("second opcode: want OP_RETURN got 0x%x", pkScript[1])
	}

	var fields [][]byte
	for offset := 2; offset < len(pkScript); {
		data, next, err := readScriptDataPush(pkScript, offset)
		if err != nil {
			t.Fatalf("field %d is not a valid data push: %v", len(fields), err)
		}
		if len(data) == 0 {
			t.Fatalf("field %d is not a data push", len(fields))
		}
		if len(data) > 520 {
			t.Fatalf("field %d exceeds max push size: %d", len(fields), len(data))
		}
		fields = append(fields, append([]byte(nil), data...))
		offset = next
	}
	return fields
}

func assertInputUsesForkIDSignature(t *testing.T, tx *wire.MsgTx, inputIndex int) {
	t.Helper()

	pushes, err := txscript.PushedData(tx.TxIn[inputIndex].SignatureScript)
	if err != nil {
		t.Fatalf("parse input signature script: %v", err)
	}
	if len(pushes) < 1 {
		t.Fatalf("signature script has no pushed signature: %x", tx.TxIn[inputIndex].SignatureScript)
	}
	signature := pushes[0]
	if len(signature) == 0 {
		t.Fatal("input signature push is empty")
	}
	wantHashType := byte(txscript.SigHashAll | txscript.SigHashForkID)
	if got := signature[len(signature)-1]; got != wantHashType {
		t.Fatalf("signature sighash byte: want 0x%x (SigHashAll|SigHashForkID) got 0x%x", wantHashType, got)
	}
}

func readScriptDataPush(script []byte, offset int) ([]byte, int, error) {
	if offset >= len(script) {
		return nil, offset, fmt.Errorf("offset %d past script length %d", offset, len(script))
	}

	opcode := script[offset]
	offset++

	var size uint64
	switch {
	case opcode > txscript.OP_0 && opcode < txscript.OP_PUSHDATA1:
		size = uint64(opcode)
	case opcode == txscript.OP_PUSHDATA1:
		if offset+1 > len(script) {
			return nil, offset, errors.New("truncated OP_PUSHDATA1 length")
		}
		size = uint64(script[offset])
		offset++
	case opcode == txscript.OP_PUSHDATA2:
		if offset+2 > len(script) {
			return nil, offset, errors.New("truncated OP_PUSHDATA2 length")
		}
		size = uint64(binary.LittleEndian.Uint16(script[offset : offset+2]))
		offset += 2
	case opcode == txscript.OP_PUSHDATA4:
		if offset+4 > len(script) {
			return nil, offset, errors.New("truncated OP_PUSHDATA4 length")
		}
		size = uint64(binary.LittleEndian.Uint32(script[offset : offset+4]))
		offset += 4
	default:
		return nil, offset, fmt.Errorf("opcode 0x%x is not a data push", opcode)
	}

	if size > uint64(len(script)-offset) {
		return nil, offset, fmt.Errorf("declared data size %d exceeds remaining script bytes %d", size, len(script)-offset)
	}
	next := offset + int(size)
	return script[offset:next], next, nil
}

func extractOnlyAddress(t *testing.T, pkScript []byte) string {
	t.Helper()

	class, addresses, _, err := txscript.ExtractPkScriptAddrs(pkScript, &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("extract output address: %v", err)
	}
	if class.String() != "pubkeyhash" {
		t.Fatalf("output class: want pubkeyhash got %s", class)
	}
	if len(addresses) != 1 {
		t.Fatalf("output addresses: want 1 got %d", len(addresses))
	}
	legacy, err := bsvutil.NewLegacyAddressPubKeyHash(addresses[0].ScriptAddress(), &chaincfg.MainNetParams)
	if err != nil {
		t.Fatalf("convert output address to legacy form: %v", err)
	}
	return legacy.String()
}
