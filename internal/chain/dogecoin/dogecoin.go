package dogecoin

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire"

	"github.com/metaid-developers/metaso-p2p/internal/config"
)

// Chain implements chain.Chain for Dogecoin (DOGE).
type Chain struct {
	client *rpcclient.Client
	cfg    config.ChainRPCConfig
}

func NewChain(cfg config.ChainRPCConfig) *Chain {
	return &Chain{cfg: cfg}
}

func (c *Chain) Name() string { return "doge" }

func (c *Chain) Init() error {
	rpcCfg := &rpcclient.ConnConfig{
		Host:                 c.cfg.RPCHost,
		User:                 c.cfg.RPCUser,
		Pass:                 c.cfg.RPCPass,
		HTTPPostMode:         c.cfg.RPCHTTPPostMode,
		DisableTLS:           c.cfg.RPCDisableTLS,
		DisableAutoReconnect: true,
		DisableConnectOnNew:  true,
	}
	var err error
	c.client, err = rpcclient.New(rpcCfg, nil)
	if err != nil {
		return fmt.Errorf("doge rpc connect: %w", err)
	}
	log.Println("[doge] RPC connected")
	return nil
}

func (c *Chain) GetBlock(height int64) (any, error) {
	hash, err := c.client.GetBlockHash(height)
	if err != nil {
		return nil, fmt.Errorf("get block hash %d: %w", height, err)
	}
	block, err := c.getBlockByRPC(hash)
	if err != nil {
		return nil, fmt.Errorf("get block %s: %w", hash, err)
	}
	return block, nil
}

func (c *Chain) GetBlockTime(height int64) (int64, error) {
	block, err := c.GetBlock(height)
	if err != nil {
		return 0, err
	}
	return block.(*wire.MsgBlock).Header.Timestamp.Unix(), nil
}

func (c *Chain) GetTransaction(txID string) (any, error) {
	hash, err := chainhash.NewHashFromStr(txID)
	if err != nil {
		return nil, err
	}
	return c.client.GetRawTransaction(hash)
}

func (c *Chain) GetBestHeight() int64 {
	info, err := c.client.GetBlockChainInfo()
	if err == nil {
		return int64(info.Blocks)
	}
	count, err := c.client.GetBlockCount()
	if err == nil {
		return count
	}
	return 0
}

func (c *Chain) GetMempoolTransactionList() ([]any, error) {
	txIDs, err := c.client.GetRawMempool()
	if err != nil {
		return nil, err
	}
	var list []any
	for _, hash := range txIDs {
		tx, err := c.client.GetRawTransaction(hash)
		if err != nil {
			continue
		}
		list = append(list, tx.MsgTx())
	}
	return list, nil
}

func (c *Chain) BroadcastTx(txRaw string) (string, error) {
	txBytes, err := hex.DecodeString(txRaw)
	if err != nil {
		return "", fmt.Errorf("decode tx: %w", err)
	}
	tx := wire.NewMsgTx(2)
	if err := tx.Deserialize(bytes.NewReader(txBytes)); err != nil {
		return "", fmt.Errorf("deserialize tx: %w", err)
	}
	hash, err := c.client.SendRawTransaction(tx, true)
	if err != nil {
		return "", err
	}
	return hash.String(), nil
}

// NetParams returns the Dogecoin network parameters.
func NetParams(testNet string) *chaincfg.Params {
	switch testNet {
	case "1":
		return &dogeTestNetParams
	case "2":
		return &dogeRegTestParams
	default:
		return &dogeMainNetParams
	}
}

var dogeMainNetParams = chaincfg.Params{
	Name:             "dogecoin-mainnet",
	Net:              wire.BitcoinNet(0xc0c0c0c0),
	DefaultPort:      "22556",
	PubKeyHashAddrID: 0x1e,
	ScriptHashAddrID: 0x16,
	PrivateKeyID:     0x9e,
	Bech32HRPSegwit:  "doge",
	HDCoinType:       3,
}

var dogeTestNetParams = chaincfg.Params{
	Name:             "dogecoin-testnet",
	Net:              wire.BitcoinNet(0xfcc1b7dc),
	DefaultPort:      "44556",
	PubKeyHashAddrID: 0x71,
	ScriptHashAddrID: 0xc4,
	PrivateKeyID:     0xf1,
	Bech32HRPSegwit:  "tdoge",
	HDCoinType:       1,
}

var dogeRegTestParams = chaincfg.Params{
	Name:             "dogecoin-regtest",
	Net:              wire.BitcoinNet(0xfabfb5da),
	DefaultPort:      "18444",
	PubKeyHashAddrID: 0x6f,
	ScriptHashAddrID: 0xc4,
	PrivateKeyID:     0xef,
	Bech32HRPSegwit:  "rdoge",
	HDCoinType:       1,
}

func (c *Chain) getBlockByRPC(hash *chainhash.Hash) (*wire.MsgBlock, error) {
	blockVerbose, err := c.client.GetBlockVerbose(hash)
	if err != nil {
		return nil, fmt.Errorf("get block verbose: %w", err)
	}

	bits, err := parseDogeBits(blockVerbose.Bits)
	if err != nil {
		return nil, err
	}
	prevHash, err := parseDogeHash(blockVerbose.PreviousHash)
	if err != nil {
		return nil, fmt.Errorf("parse previous hash: %w", err)
	}
	merkleRoot, err := parseDogeHash(blockVerbose.MerkleRoot)
	if err != nil {
		return nil, fmt.Errorf("parse merkle root: %w", err)
	}

	rawBlock, err := c.rawBlockHex(hash)
	if err != nil {
		return nil, err
	}
	blockBytes, err := hex.DecodeString(rawBlock)
	if err != nil {
		return nil, fmt.Errorf("decode raw block: %w", err)
	}
	txs, err := parseDogeBlockTransactions(blockBytes, len(blockVerbose.Tx))
	if err != nil {
		return nil, err
	}

	return &wire.MsgBlock{
		Header: wire.BlockHeader{
			Version:    blockVerbose.Version,
			PrevBlock:  prevHash,
			MerkleRoot: merkleRoot,
			Timestamp:  time.Unix(blockVerbose.Time, 0),
			Bits:       bits,
			Nonce:      blockVerbose.Nonce,
		},
		Transactions: txs,
	}, nil
}

func (c *Chain) rawBlockHex(hash *chainhash.Hash) (string, error) {
	result, err := c.client.RawRequest("getblock", []json.RawMessage{
		json.RawMessage(strconv.Quote(hash.String())),
		json.RawMessage("0"),
	})
	if err != nil {
		return "", fmt.Errorf("get raw block: %w", err)
	}
	var raw string
	if err := json.Unmarshal(result, &raw); err != nil {
		return "", fmt.Errorf("decode raw block response: %w", err)
	}
	return raw, nil
}

func parseDogeBlockTransactions(blockBytes []byte, expectedTxCount int) ([]*wire.MsgTx, error) {
	if len(blockBytes) < 80 {
		return nil, fmt.Errorf("block too short: %d bytes", len(blockBytes))
	}

	version := int32(binary.LittleEndian.Uint32(blockBytes[0:4]))
	offset := 80
	if version&0x100 != 0 {
		next, err := skipDogeAuxPoW(blockBytes, offset)
		if err != nil {
			return nil, err
		}
		offset = next
	}

	txCount, n, err := readDogeVarInt(blockBytes[offset:])
	if err != nil {
		return nil, fmt.Errorf("read tx count: %w", err)
	}
	offset += n
	if expectedTxCount >= 0 && int(txCount) != expectedTxCount {
		log.Printf("[doge] parsed tx count %d differs from verbose tx count %d", txCount, expectedTxCount)
	}

	txs := make([]*wire.MsgTx, 0, txCount)
	reader := bytes.NewReader(blockBytes[offset:])
	for i := uint64(0); i < txCount; i++ {
		tx := wire.NewMsgTx(1)
		if err := tx.BtcDecode(reader, 0, wire.BaseEncoding); err != nil {
			return nil, fmt.Errorf("parse tx %d: %w", i, err)
		}
		txs = append(txs, tx)
	}
	return txs, nil
}

func skipDogeAuxPoW(blockBytes []byte, offset int) (int, error) {
	reader := bytes.NewReader(blockBytes[offset:])
	coinbaseTx := wire.NewMsgTx(1)
	if err := coinbaseTx.BtcDecode(reader, 0, wire.WitnessEncoding); err != nil {
		reader = bytes.NewReader(blockBytes[offset:])
		if baseErr := coinbaseTx.BtcDecode(reader, 0, wire.BaseEncoding); baseErr != nil {
			return 0, fmt.Errorf("parse auxpow coinbase: witness=%v base=%v", err, baseErr)
		}
	}
	offset += len(blockBytes[offset:]) - reader.Len()

	if offset+32 > len(blockBytes) {
		return 0, fmt.Errorf("auxpow block too short for block hash at offset %d", offset)
	}
	offset += 32

	next, err := skipDogeHashBranch(blockBytes, offset, "merkle")
	if err != nil {
		return 0, err
	}
	offset = next
	if offset+4 > len(blockBytes) {
		return 0, fmt.Errorf("auxpow block too short for merkle index at offset %d", offset)
	}
	offset += 4

	next, err = skipDogeHashBranch(blockBytes, offset, "aux merkle")
	if err != nil {
		return 0, err
	}
	offset = next
	if offset+4 > len(blockBytes) {
		return 0, fmt.Errorf("auxpow block too short for aux merkle index at offset %d", offset)
	}
	offset += 4

	if offset+80 > len(blockBytes) {
		return 0, fmt.Errorf("auxpow block too short for parent header at offset %d", offset)
	}
	return offset + 80, nil
}

func skipDogeHashBranch(blockBytes []byte, offset int, name string) (int, error) {
	count, n, err := readDogeVarInt(blockBytes[offset:])
	if err != nil {
		return 0, fmt.Errorf("read %s branch count: %w", name, err)
	}
	if count > 256 {
		return 0, fmt.Errorf("%s branch count implausibly large: %d", name, count)
	}
	offset += n
	bytesToSkip := int(count) * 32
	if offset+bytesToSkip > len(blockBytes) {
		return 0, fmt.Errorf("auxpow block too short for %s branch at offset %d", name, offset)
	}
	return offset + bytesToSkip, nil
}

func readDogeVarInt(data []byte) (uint64, int, error) {
	if len(data) == 0 {
		return 0, 0, fmt.Errorf("empty varint")
	}
	switch data[0] {
	case 0xff:
		if len(data) < 9 {
			return 0, 0, fmt.Errorf("short uint64 varint")
		}
		return binary.LittleEndian.Uint64(data[1:9]), 9, nil
	case 0xfe:
		if len(data) < 5 {
			return 0, 0, fmt.Errorf("short uint32 varint")
		}
		return uint64(binary.LittleEndian.Uint32(data[1:5])), 5, nil
	case 0xfd:
		if len(data) < 3 {
			return 0, 0, fmt.Errorf("short uint16 varint")
		}
		return uint64(binary.LittleEndian.Uint16(data[1:3])), 3, nil
	default:
		return uint64(data[0]), 1, nil
	}
}

func parseDogeBits(bits string) (uint32, error) {
	parsed, err := strconv.ParseUint(bits, 16, 32)
	if err != nil {
		return 0, fmt.Errorf("parse bits: %w", err)
	}
	return uint32(parsed), nil
}

func parseDogeHash(value string) (chainhash.Hash, error) {
	if value == "" {
		return chainhash.Hash{}, nil
	}
	hash, err := chainhash.NewHashFromStr(value)
	if err != nil {
		return chainhash.Hash{}, err
	}
	return *hash, nil
}
