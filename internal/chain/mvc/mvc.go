package mvc

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire"

	"github.com/metaid-developers/metaso-p2p/internal/config"
)

// Chain implements chain.Chain for MicrovisionChain (MVC).
type Chain struct {
	client *rpcclient.Client
	cfg    config.ChainRPCConfig
}

func NewChain(cfg config.ChainRPCConfig) *Chain {
	return &Chain{cfg: cfg}
}

func (c *Chain) Name() string { return "mvc" }

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
		return fmt.Errorf("mvc rpc connect: %w", err)
	}
	log.Println("[mvc] RPC connected")
	return nil
}

func (c *Chain) GetBlock(height int64) (any, error) {
	hash, err := c.client.GetBlockHash(height)
	if err != nil {
		return nil, fmt.Errorf("get block hash %d: %w", height, err)
	}
	block, err := c.client.GetBlock(hash)
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
	txIDs, err := c.GetMempoolTransactionIDs()
	if err != nil {
		return nil, err
	}
	transactions, err := c.GetMempoolTransactions(txIDs)
	if err != nil {
		return nil, err
	}
	list := make([]any, 0, len(transactions))
	for _, txID := range txIDs {
		if tx, ok := transactions[txID]; ok {
			list = append(list, tx)
		}
	}
	return list, nil
}

func (c *Chain) GetMempoolTransactionIDs() ([]string, error) {
	hashes, err := c.client.GetRawMempool()
	if err != nil {
		return nil, err
	}
	txIDs := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		if hash != nil {
			txIDs = append(txIDs, hash.String())
		}
	}
	return txIDs, nil
}

func (c *Chain) GetMempoolTransactions(txIDs []string) (map[string]any, error) {
	transactions := make(map[string]any, len(txIDs))
	for _, txID := range txIDs {
		hash, err := chainhash.NewHashFromStr(txID)
		if err != nil {
			continue
		}
		tx, err := c.client.GetRawTransaction(hash)
		if err != nil {
			continue
		}
		transactions[txID] = tx.MsgTx()
	}
	return transactions, nil
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

// NetParams returns the MVC network parameters.
// MVC is a Bitcoin fork and uses the same address prefixes.
func NetParams(testNet string) *chaincfg.Params {
	switch testNet {
	case "1":
		return &chaincfg.TestNet3Params
	case "2":
		return &chaincfg.RegressionNetParams
	default:
		return &chaincfg.MainNetParams
	}
}
