package federation

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"github.com/bitcoinsv/bsvd/bsvec"
	"github.com/bitcoinsv/bsvd/chaincfg"
	"github.com/bitcoinsv/bsvd/chaincfg/chainhash"
	"github.com/bitcoinsv/bsvd/txscript"
	"github.com/bitcoinsv/bsvd/wire"
	"github.com/bitcoinsv/bsvutil"
)

const (
	mvcRegistryTxVersion   = 10
	mvcRegistryContentType = "application/json"
	mvcRegistryEncryption  = "0"
	mvcRegistryMaxPushSize = 520
	mvcRegistryDefaultDust = int64(1)
)

// MVCRegistryUTXO is an MVC P2PKH output available for registry transaction funding.
type MVCRegistryUTXO struct {
	TxID     string
	OutIndex uint32
	Value    int64
	Address  string
	PkScript string
}

// MVCRegistryTxRequest describes one MetaID registry pin transaction to build.
type MVCRegistryTxRequest struct {
	Net           string
	PrivateKeyHex string
	UTXOs         []MVCRegistryUTXO
	Operation     string
	Path          string
	Version       string
	Content       []byte
	FeeRate       int64
	DustAmount    int64
}

// MVCRegistryTxResult contains the signed MVC registry transaction and key metadata.
type MVCRegistryTxResult struct {
	RawTx             string
	Tx                *wire.MsgTx
	Address           string
	PublicKey         string
	OwnerOutputIndex  int
	ChangeOutputIndex int
	ChangeAmount      int64
	Fee               int64
}

// MVCIdentityFromPrivateKey derives the compressed public key and P2PKH address for an MVC key.
func MVCIdentityFromPrivateKey(privateKeyHex string, net string) (address, publicKeyHex string, err error) {
	privateKey, params, err := mvcRegistryPrivateKeyAndParams(privateKeyHex, net)
	if err != nil {
		return "", "", err
	}
	return mvcRegistryIdentity(privateKey, params)
}

// BuildMVCRegistryTx builds and signs an MVC MetaID registry transaction.
func BuildMVCRegistryTx(req MVCRegistryTxRequest) (*MVCRegistryTxResult, error) {
	privateKey, params, err := mvcRegistryPrivateKeyAndParams(req.PrivateKeyHex, req.Net)
	if err != nil {
		return nil, err
	}
	address, publicKeyHex, err := mvcRegistryIdentity(privateKey, params)
	if err != nil {
		return nil, err
	}

	operation, err := mvcRegistryOperation(req.Operation)
	if err != nil {
		return nil, err
	}
	if (operation == "create" || operation == "modify") && len(req.Content) == 0 {
		return nil, fmt.Errorf("%s registry content is required", operation)
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = RegistryPath
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = Version
	}
	if req.FeeRate <= 0 {
		return nil, errors.New("fee rate must be positive")
	}
	dustAmount := req.DustAmount
	if dustAmount <= 0 {
		dustAmount = mvcRegistryDefaultDust
	}
	if len(req.UTXOs) == 0 {
		return nil, errors.New("at least one UTXO is required")
	}

	tx := wire.NewMsgTx(mvcRegistryTxVersion)
	var inputValue int64
	inputScripts := make([][]byte, 0, len(req.UTXOs))
	inputValues := make([]int64, 0, len(req.UTXOs))
	for i, utxo := range req.UTXOs {
		if utxo.Value <= 0 {
			return nil, fmt.Errorf("utxo %d value must be positive", i)
		}
		hash, err := chainhash.NewHashFromStr(strings.TrimSpace(utxo.TxID))
		if err != nil {
			return nil, fmt.Errorf("utxo %d txid: %w", i, err)
		}
		pkScript, err := mvcRegistryUTXOPkScript(utxo, address, params)
		if err != nil {
			return nil, fmt.Errorf("utxo %d pkScript: %w", i, err)
		}

		tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(hash, utxo.OutIndex), nil))
		inputScripts = append(inputScripts, pkScript)
		inputValues = append(inputValues, utxo.Value)
		inputValue += utxo.Value
	}

	ownerScript, err := mvcRegistryAddressScript(address, params)
	if err != nil {
		return nil, err
	}
	registryScript, err := mvcRegistryOPReturnScript(operation, path, version, req.Content)
	if err != nil {
		return nil, err
	}

	ownerOutputIndex := len(tx.TxOut)
	tx.AddTxOut(wire.NewTxOut(dustAmount, ownerScript))
	tx.AddTxOut(wire.NewTxOut(0, registryScript))
	changeOutputIndex := len(tx.TxOut)
	tx.AddTxOut(wire.NewTxOut(0, ownerScript))

	var changeAmount int64
	var fee int64
	requiredFee := int64(0)
	for attempt := 0; attempt < 20; attempt++ {
		changeAmount = inputValue - dustAmount - requiredFee
		if changeAmount < dustAmount {
			return nil, errors.New("insufficient UTXO value for owner output, change output, and fee")
		}

		tx.TxOut[changeOutputIndex].Value = changeAmount
		if err := mvcRegistrySign(tx, inputScripts, inputValues, privateKey); err != nil {
			return nil, err
		}

		fee = inputValue - dustAmount - changeAmount
		minFee := int64(tx.SerializeSize()) * req.FeeRate
		if fee >= minFee {
			rawTx, err := mvcRegistryRawTx(tx)
			if err != nil {
				return nil, err
			}
			return &MVCRegistryTxResult{
				RawTx:             rawTx,
				Tx:                tx,
				Address:           address,
				PublicKey:         publicKeyHex,
				OwnerOutputIndex:  ownerOutputIndex,
				ChangeOutputIndex: changeOutputIndex,
				ChangeAmount:      changeAmount,
				Fee:               fee,
			}, nil
		}
		requiredFee += minFee - fee
	}

	return nil, errors.New("could not satisfy requested fee rate")
}

func mvcRegistryPrivateKeyAndParams(privateKeyHex string, net string) (*bsvec.PrivateKey, *chaincfg.Params, error) {
	params, err := mvcRegistryNetParams(net)
	if err != nil {
		return nil, nil, err
	}
	privateKey, err := mvcRegistryPrivateKeyFromHex(privateKeyHex)
	if err != nil {
		return nil, nil, err
	}
	return privateKey, params, nil
}

func mvcRegistryPrivateKeyFromHex(privateKeyHex string) (*bsvec.PrivateKey, error) {
	decoded, err := decodeHexField(privateKeyHex, "private key")
	if err != nil {
		return nil, err
	}
	if len(decoded) != bsvec.PrivKeyBytesLen {
		return nil, fmt.Errorf("private key must be %d bytes, got %d", bsvec.PrivKeyBytesLen, len(decoded))
	}

	keyValue := new(big.Int).SetBytes(decoded)
	if keyValue.Sign() <= 0 {
		return nil, errors.New("private key must be greater than zero")
	}
	if keyValue.Cmp(bsvec.S256().N) >= 0 {
		return nil, errors.New("private key must be less than the secp256k1 group order")
	}

	privateKey, _ := bsvec.PrivKeyFromBytes(bsvec.S256(), decoded)
	return privateKey, nil
}

func mvcRegistryIdentity(privateKey *bsvec.PrivateKey, params *chaincfg.Params) (address, publicKeyHex string, err error) {
	publicKey := privateKey.PubKey().SerializeCompressed()
	pubKeyHash := bsvutil.Hash160(publicKey)
	addr, err := bsvutil.NewLegacyAddressPubKeyHash(pubKeyHash, params)
	if err != nil {
		return "", "", fmt.Errorf("derive MVC address: %w", err)
	}
	return addr.String(), hex.EncodeToString(publicKey), nil
}

func mvcRegistryNetParams(net string) (*chaincfg.Params, error) {
	switch strings.ToLower(strings.TrimSpace(net)) {
	case "", "mvc-mainnet", "mainnet", "mvc", "0":
		return &chaincfg.MainNetParams, nil
	case "mvc-testnet", "testnet", "testnet3", "1":
		return &chaincfg.TestNet3Params, nil
	case "mvc-regtest", "regtest", "regression", "2":
		return &chaincfg.RegressionNetParams, nil
	default:
		return nil, fmt.Errorf("unsupported MVC network %q", net)
	}
}

func mvcRegistryOperation(operation string) (string, error) {
	switch normalized := strings.ToLower(strings.TrimSpace(operation)); normalized {
	case "create", "modify", "revoke":
		return normalized, nil
	default:
		return "", fmt.Errorf("unsupported registry operation %q", operation)
	}
}

func mvcRegistryUTXOPkScript(utxo MVCRegistryUTXO, derivedAddress string, params *chaincfg.Params) ([]byte, error) {
	if scriptHex := strings.TrimSpace(utxo.PkScript); scriptHex != "" {
		script, err := hex.DecodeString(scriptHex)
		if err != nil {
			return nil, fmt.Errorf("decode hex: %w", err)
		}
		if len(script) == 0 {
			return nil, errors.New("decoded pkScript is empty")
		}
		return script, nil
	}

	address := strings.TrimSpace(utxo.Address)
	if address == "" {
		address = derivedAddress
	}
	return mvcRegistryAddressScript(address, params)
}

func mvcRegistryAddressScript(address string, params *chaincfg.Params) ([]byte, error) {
	decoded, err := bsvutil.DecodeAddress(strings.TrimSpace(address), params)
	if err != nil {
		return nil, fmt.Errorf("decode address: %w", err)
	}
	if !decoded.IsForNet(params) {
		return nil, fmt.Errorf("address %q is not for selected MVC network", address)
	}
	script, err := txscript.PayToAddrScript(decoded)
	if err != nil {
		return nil, fmt.Errorf("address script: %w", err)
	}
	return script, nil
}

func mvcRegistryOPReturnScript(operation string, path string, version string, content []byte) ([]byte, error) {
	builder := txscript.NewScriptBuilder().
		AddOp(txscript.OP_0).
		AddOp(txscript.OP_RETURN).
		AddData([]byte("metaid")).
		AddData([]byte(operation)).
		AddData([]byte(path)).
		AddData([]byte(mvcRegistryEncryption)).
		AddData([]byte(version)).
		AddData([]byte(mvcRegistryContentType))

	for start := 0; start < len(content); start += mvcRegistryMaxPushSize {
		end := start + mvcRegistryMaxPushSize
		if end > len(content) {
			end = len(content)
		}
		builder.AddData(content[start:end])
	}

	script, err := builder.Script()
	if err != nil {
		return nil, fmt.Errorf("build registry OP_RETURN script: %w", err)
	}
	return script, nil
}

func mvcRegistrySign(tx *wire.MsgTx, inputScripts [][]byte, inputValues []int64, privateKey *bsvec.PrivateKey) error {
	for _, input := range tx.TxIn {
		input.SignatureScript = nil
	}
	for i, pkScript := range inputScripts {
		signatureScript, err := txscript.SignatureScript(tx, i, inputValues[i], pkScript, txscript.SigHashAll, privateKey, true)
		if err != nil {
			return fmt.Errorf("sign input %d: %w", i, err)
		}
		tx.TxIn[i].SignatureScript = signatureScript
	}
	return nil
}

func mvcRegistryRawTx(tx *wire.MsgTx) (string, error) {
	var buf bytes.Buffer
	if err := tx.Serialize(&buf); err != nil {
		return "", fmt.Errorf("serialize tx: %w", err)
	}
	return hex.EncodeToString(buf.Bytes()), nil
}
