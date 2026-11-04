package mvc

import (
	"strings"
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"

	"github.com/btcsuite/btcd/btcutil"
)

// Transfer-detection tests: the DetectTransfers semantics ported from
// man-p2p's MVC adapter — ledger-txid pointers, the input-0 → output-0 fast
// path, the cumulative-value slow path, and the meltdown signature.

func newTransferTestIndexer(t *testing.T) *Indexer {
	t.Helper()
	return NewIndexer(nil, NetParams(""))
}

func p2pkhScript(t *testing.T, params *chaincfg.Params, seed byte) []byte {
	t.Helper()
	var hash [20]byte
	for i := range hash {
		hash[i] = seed
	}
	addr, err := btcutil.NewAddressPubKeyHash(hash[:], params)
	if err != nil {
		t.Fatalf("address: %v", err)
	}
	script, err := txscript.PayToAddrScript(addr)
	if err != nil {
		t.Fatalf("script: %v", err)
	}
	return script
}

func outpoint(txidSeed byte, vout uint32) wire.OutPoint {
	var hash chainhash.Hash
	for i := range hash {
		hash[i] = txidSeed
	}
	return wire.OutPoint{Hash: hash, Index: vout}
}

func outpointString(op wire.OutPoint) string {
	return op.Hash.String() + ":" + string(rune('0'+int(op.Index)))
}

func buildTx(inputs []wire.OutPoint, outputValues []int64, outputScripts [][]byte) *wire.MsgTx {
	tx := wire.NewMsgTx(1) // version 1: ledger txid == sha256d TxHash
	for i := range inputs {
		tx.AddTxIn(wire.NewTxIn(&inputs[i], nil, nil))
	}
	for i := range outputScripts {
		tx.AddTxOut(wire.NewTxOut(outputValues[i], outputScripts[i]))
	}
	return tx
}

func TestDetectTransfersFastPathSingleInput(t *testing.T) {
	idx := newTransferTestIndexer(t)
	params := idx.chainParams
	scriptA := p2pkhScript(t, params, 0x01)

	from := outpoint(0xaa, 0)
	tx := buildTx([]wire.OutPoint{from}, []int64{546}, [][]byte{scriptA})
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}

	spent := from.Hash.String() + ":0"
	notices, err := idx.DetectTransfers(map[string]string{spent: "mvc1qowner"})
	if err != nil {
		t.Fatalf("DetectTransfers: %v", err)
	}
	notice, ok := notices[spent]
	if !ok {
		t.Fatalf("no notice for %s: %+v", spent, notices)
	}
	wantTxid, err := mvcMetaIDTxHash(tx)
	if err != nil {
		t.Fatalf("ledger txid: %v", err)
	}
	if notice.Output != wantTxid+":0" {
		t.Fatalf("output pointer must use the ledger txid: %s", notice.Output)
	}
	if notice.Location != wantTxid+":0:0" {
		t.Fatalf("fast-path location must be <txid>:0:0: %s", notice.Location)
	}
	_, addresses, _, _ := txscript.ExtractPkScriptAddrs(scriptA, params)
	if notice.ToAddress != addresses[0].String() {
		t.Fatalf("to address wrong: %s", notice.ToAddress)
	}
	if notice.OutputValue != 546 || notice.FromAddress != "mvc1qowner" || notice.Meltdown {
		t.Fatalf("notice fields wrong: %+v", notice)
	}
}

func TestDetectTransfersFastPathTrackedInputIsInputZero(t *testing.T) {
	idx := newTransferTestIndexer(t)
	params := idx.chainParams
	scriptA := p2pkhScript(t, params, 0x02)
	scriptB := p2pkhScript(t, params, 0x03)

	// Tracked outpoint is input 0 of a two-input tx: still the fast path
	// (output 0), even though output 1 is bigger.
	from := outpoint(0xbb, 0)
	other := outpoint(0xcc, 1)
	tx := buildTx([]wire.OutPoint{from, other}, []int64{546, 100000}, [][]byte{scriptA, scriptB})
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}

	spent := from.Hash.String() + ":0"
	notices, _ := idx.DetectTransfers(map[string]string{spent: "mvc1qowner"})
	notice, ok := notices[spent]
	if !ok || notice.Output != mustLedgerTxid(t, tx)+":0" {
		t.Fatalf("input-0 fast path broken: %+v", notice)
	}
}

func TestDetectTransfersSlowPathCumulativeValues(t *testing.T) {
	idx := newTransferTestIndexer(t)
	params := idx.chainParams
	scriptA := p2pkhScript(t, params, 0x04)
	scriptB := p2pkhScript(t, params, 0x05)

	// Tracked NFT outpoint is input 1; input 0 carries 1000 sats. Outputs
	// 500 + 1200: the running output total passes the 1000-sat input total
	// at output 1, so the NFT carries through output 1.
	from0 := outpoint(0xdd, 0)
	from1 := outpoint(0xee, 1)
	tx := buildTx([]wire.OutPoint{from0, from1}, []int64{500, 1200}, [][]byte{scriptA, scriptB})
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}
	idx.valueOfSpent = func(txid string, vout int) (int64, error) {
		if vout == 0 {
			return 1000, nil
		}
		return 0, nil
	}

	spent := from1.Hash.String() + ":1"
	notices, _ := idx.DetectTransfers(map[string]string{spent: "mvc1qowner"})
	notice, ok := notices[spent]
	if !ok {
		t.Fatalf("slow path produced no notice: %+v", notices)
	}
	ledger := mustLedgerTxid(t, tx)
	if notice.Output != ledger+":1" {
		t.Fatalf("slow path output wrong: %s", notice.Output)
	}
	if notice.Location != ledger+":1:500" {
		t.Fatalf("slow path location (sat offset) wrong: %s", notice.Location)
	}
	_, addresses, _, _ := txscript.ExtractPkScriptAddrs(scriptB, params)
	if notice.ToAddress != addresses[0].String() || notice.OutputValue != 1200 {
		t.Fatalf("slow path fields wrong: %+v", notice)
	}
}

func TestDetectTransfersMeltdownSignature(t *testing.T) {
	idx := newTransferTestIndexer(t)
	params := idx.chainParams
	scriptSelf := p2pkhScript(t, params, 0x06)

	// 3 tracked 546-sat inputs, one output, all to the same address:
	// the man-p2p meltdown signature.
	_, selfAddrs, _, _ := txscript.ExtractPkScriptAddrs(scriptSelf, params)
	selfAddr := selfAddrs[0].String()
	var inputs []wire.OutPoint
	idMap := map[string]string{}
	values := map[string]int64{}
	for i := 0; i < 3; i++ {
		op := outpoint(byte(0xf0+i), 0)
		inputs = append(inputs, op)
		key := op.Hash.String() + ":0"
		idMap[key] = selfAddr
		values[key] = 546
	}
	tx := buildTx(inputs, []int64{1638}, [][]byte{scriptSelf})
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}
	idx.valueOfSpent = func(txid string, vout int) (int64, error) {
		return values[txid+":0"], nil
	}

	notices, _ := idx.DetectTransfers(idMap)
	if len(notices) != 3 {
		t.Fatalf("meltdown tx should notice every spent input: %+v", notices)
	}
	for key, notice := range notices {
		if !notice.Meltdown {
			t.Fatalf("input %s must be marked meltdown: %+v", key, notice)
		}
	}

	// Two outputs: not a meltdown.
	tx2 := buildTx(inputs, []int64{1638, 546}, [][]byte{scriptSelf, scriptSelf})
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx2}}
	notices2, _ := idx.DetectTransfers(idMap)
	for key, notice := range notices2 {
		if notice.Meltdown {
			t.Fatalf("two-output tx must not be meltdown: %s %+v", key, notice)
		}
	}
}

func TestDetectTransfersNilBlockAndUntracked(t *testing.T) {
	idx := newTransferTestIndexer(t)

	// Nil block (abnormal call order): empty result, no panic.
	notices, err := idx.DetectTransfers(map[string]string{"aa:0": "x"})
	if err != nil || len(notices) != 0 {
		t.Fatalf("nil block must yield empty notices: %+v %v", notices, err)
	}

	// Block without tracked spends.
	tx := buildTx([]wire.OutPoint{outpoint(0x99, 0)}, []int64{546}, [][]byte{p2pkhScript(t, idx.chainParams, 0x07)})
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}
	notices, _ = idx.DetectTransfers(map[string]string{"unrelated:0": "mvc1qowner"})
	if len(notices) != 0 {
		t.Fatalf("untracked spends must produce nothing: %+v", notices)
	}
}

func TestCatchTransferLegacyShape(t *testing.T) {
	idx := newTransferTestIndexer(t)
	params := idx.chainParams
	scriptA := p2pkhScript(t, params, 0x08)

	from := outpoint(0xab, 0)
	tx := buildTx([]wire.OutPoint{from}, []int64{546}, [][]byte{scriptA})
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}

	spent := from.Hash.String() + ":0"
	result, err := idx.CatchTransfer(map[string]string{spent: "mvc1qowner"})
	if err != nil {
		t.Fatalf("CatchTransfer: %v", err)
	}
	info, ok := result[spent].(map[string]any)
	if !ok {
		t.Fatalf("legacy shape wrong: %+v", result)
	}
	if info["fromAddress"] != "mvc1qowner" || info["isMeltdown"] != false {
		t.Fatalf("legacy info fields wrong: %+v", info)
	}
	if !strings.Contains(info["location"].(string), ":0:0") {
		t.Fatalf("legacy location wrong: %v", info["location"])
	}
}

func mustLedgerTxid(t *testing.T, tx *wire.MsgTx) string {
	t.Helper()
	txid, err := mvcMetaIDTxHash(tx)
	if err != nil {
		t.Fatalf("ledger txid: %v", err)
	}
	return txid
}
