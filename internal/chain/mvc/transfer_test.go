package mvc

import (
	"errors"
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

func TestDetectMempoolSpends(t *testing.T) {
	idx := newTransferTestIndexer(t)
	params := idx.chainParams
	scriptA := p2pkhScript(t, params, 0x09)

	// One mempool tx spends the tracked outpoint, another is unrelated.
	from := outpoint(0xac, 0)
	spendTx := buildTx([]wire.OutPoint{from}, []int64{546}, [][]byte{scriptA})
	unrelatedTx := buildTx([]wire.OutPoint{outpoint(0xad, 2)}, []int64{546}, [][]byte{scriptA})

	notices, err := idx.DetectMempoolSpends(
		map[string]string{from.Hash.String() + ":0": "mvc1qowner"},
		[]any{spendTx, unrelatedTx, "not-a-tx"},
	)
	if err != nil {
		t.Fatalf("DetectMempoolSpends: %v", err)
	}
	notice, ok := notices[from.Hash.String()+":0"]
	if !ok {
		t.Fatalf("mempool spend not detected: %+v", notices)
	}
	if notice.Output != mustLedgerTxid(t, spendTx)+":0" || notice.ToAddress == "" {
		t.Fatalf("mempool notice fields wrong: %+v", notice)
	}
	if notice.Meltdown {
		t.Fatal("meltdown is not evaluated at the mempool stage")
	}

	// Empty idMap: empty result.
	notices, err = idx.DetectMempoolSpends(nil, []any{spendTx})
	if err != nil || len(notices) != 0 {
		t.Fatalf("empty idMap must yield empty notices: %+v %v", notices, err)
	}
}

// ---------- R-E-3: the passive-offer settlement delivery layout ----------

// offerSettlementTx builds the R-E-3 layout: the item UTXO (546 sats) at
// input0, the signed price payment at output0, the delivery output (546
// sats) at output1, optionally followed by buyer change.
func offerSettlementTx(t *testing.T, itemValue, priceSats, deliveryValue int64, withChange bool) (*wire.MsgTx, wire.OutPoint, []byte, []byte) {
	t.Helper()
	params := NetParams("")
	scriptSeller := p2pkhScript(t, params, 0x21)
	scriptBuyer := p2pkhScript(t, params, 0x22)

	item := outpoint(0xda, 0)
	funding := outpoint(0xdb, 1)
	outputValues := []int64{priceSats, deliveryValue}
	outputScripts := [][]byte{scriptSeller, scriptBuyer}
	if withChange {
		outputValues = append(outputValues, 12345)
		outputScripts = append(outputScripts, p2pkhScript(t, params, 0x23))
	}
	return buildTx([]wire.OutPoint{item, funding}, outputValues, outputScripts), item, scriptSeller, scriptBuyer
}

func offerSettlementValues(item wire.OutPoint) func(txid string, vout int) (int64, error) {
	return func(txid string, vout int) (int64, error) {
		if txid == item.Hash.String() && vout == 0 {
			return 546, nil
		}
		return 100000, nil // buyer funding
	}
}

func TestDetectTransfersOfferSettlementLayout(t *testing.T) {
	idx := newTransferTestIndexer(t)

	// The exact settlement layout: input0 = item (546), output0 = price
	// (30000 → seller), output1 = delivery (546 → buyer).
	tx, item, _, scriptBuyer := offerSettlementTx(t, 546, 30000, 546, false)
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}
	idx.valueOfSpent = offerSettlementValues(item)

	spent := item.Hash.String() + ":0"
	notices, err := idx.DetectTransfers(map[string]string{spent: "mvc1qowner"})
	if err != nil {
		t.Fatalf("DetectTransfers: %v", err)
	}
	notice, ok := notices[spent]
	if !ok {
		t.Fatalf("settlement spend not detected: %+v", notices)
	}
	ledger := mustLedgerTxid(t, tx)
	if notice.Output != ledger+":1" {
		t.Fatalf("delivery must be read from output1: %+v", notice)
	}
	if notice.Location != ledger+":1:0" {
		t.Fatalf("delivery location wrong: %+v", notice)
	}
	_, addresses, _, _ := txscript.ExtractPkScriptAddrs(scriptBuyer, idx.chainParams)
	if notice.ToAddress != addresses[0].String() {
		t.Fatalf("new owner must be the buyer: %+v", notice)
	}
	if notice.OutputValue != 546 || notice.FromAddress != "mvc1qowner" || notice.Meltdown {
		t.Fatalf("notice fields wrong: %+v", notice)
	}
}

func TestDetectTransfersOfferSettlementWithChange(t *testing.T) {
	idx := newTransferTestIndexer(t)

	// Buyer change outputs after the delivery output must not disturb the
	// rule: delivery stays output1.
	tx, item, _, scriptBuyer := offerSettlementTx(t, 546, 30000, 546, true)
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}
	idx.valueOfSpent = offerSettlementValues(item)

	spent := item.Hash.String() + ":0"
	notices, _ := idx.DetectTransfers(map[string]string{spent: "mvc1qowner"})
	notice, ok := notices[spent]
	if !ok || notice.Output != mustLedgerTxid(t, tx)+":1" {
		t.Fatalf("delivery must stay output1 with change outputs: %+v", notice)
	}
	_, addresses, _, _ := txscript.ExtractPkScriptAddrs(scriptBuyer, idx.chainParams)
	if notice.ToAddress != addresses[0].String() {
		t.Fatalf("new owner must be the buyer: %+v", notice)
	}
}

func TestDetectTransfersOfferAmbiguousShapeKeepsLegacy(t *testing.T) {
	idx := newTransferTestIndexer(t)

	// output1 does NOT carry the item value: the shape is not an offer
	// settlement — the legacy output-0 read applies.
	tx, item, scriptSeller, _ := offerSettlementTx(t, 546, 30000, 600, false)
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}
	idx.valueOfSpent = offerSettlementValues(item)

	spent := item.Hash.String() + ":0"
	notices, _ := idx.DetectTransfers(map[string]string{spent: "mvc1qowner"})
	notice, ok := notices[spent]
	if !ok || notice.Output != mustLedgerTxid(t, tx)+":0" {
		t.Fatalf("ambiguous shape must keep the legacy output0 read: %+v", notice)
	}
	_, addresses, _, _ := txscript.ExtractPkScriptAddrs(scriptSeller, idx.chainParams)
	if notice.ToAddress != addresses[0].String() {
		t.Fatalf("legacy read must resolve output0: %+v", notice)
	}
}

func TestDetectTransfersOfferValueLookupFailureKeepsLegacy(t *testing.T) {
	idx := newTransferTestIndexer(t)

	// Spent-value lookup failure: fall back to the legacy read (no
	// behaviour change on RPC hiccups).
	tx, item, _, _ := offerSettlementTx(t, 546, 30000, 546, false)
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}
	idx.valueOfSpent = func(txid string, vout int) (int64, error) {
		return 0, errors.New("rpc down")
	}

	spent := item.Hash.String() + ":0"
	notices, _ := idx.DetectTransfers(map[string]string{spent: "mvc1qowner"})
	notice, ok := notices[spent]
	if !ok || notice.Output != mustLedgerTxid(t, tx)+":0" {
		t.Fatalf("lookup failure must keep the legacy output0 read: %+v", notice)
	}
}

func TestDetectTransfersPlainTransferStaysOutputZero(t *testing.T) {
	idx := newTransferTestIndexer(t)

	// Plain transfer with the item value at output0: no diversion even with
	// a successful value lookup (output0 IS the delivery output).
	item := outpoint(0xdc, 0)
	funding := outpoint(0xdd, 1)
	scriptBuyer := p2pkhScript(t, idx.chainParams, 0x22)
	tx := buildTx([]wire.OutPoint{item, funding}, []int64{546, 100000}, [][]byte{scriptBuyer, scriptBuyer})
	idx.block = &wire.MsgBlock{Transactions: []*wire.MsgTx{tx}}
	idx.valueOfSpent = offerSettlementValues(item)

	spent := item.Hash.String() + ":0"
	notices, _ := idx.DetectTransfers(map[string]string{spent: "mvc1qowner"})
	notice, ok := notices[spent]
	if !ok || notice.Output != mustLedgerTxid(t, tx)+":0" {
		t.Fatalf("plain transfer must stay on output0: %+v", notice)
	}
}

func TestDetectMempoolSpendsOfferSettlementLayout(t *testing.T) {
	idx := newTransferTestIndexer(t)

	// The same rule drives the advisory mempool pendings.
	tx, item, _, scriptBuyer := offerSettlementTx(t, 546, 30000, 546, false)
	idx.valueOfSpent = offerSettlementValues(item)

	spent := item.Hash.String() + ":0"
	notices, err := idx.DetectMempoolSpends(map[string]string{spent: "mvc1qowner"}, []any{tx})
	if err != nil {
		t.Fatalf("DetectMempoolSpends: %v", err)
	}
	notice, ok := notices[spent]
	if !ok || notice.Output != mustLedgerTxid(t, tx)+":1" {
		t.Fatalf("mempool settlement must read output1: %+v", notice)
	}
	_, addresses, _, _ := txscript.ExtractPkScriptAddrs(scriptBuyer, idx.chainParams)
	if notice.ToAddress != addresses[0].String() {
		t.Fatalf("mempool new owner must be the buyer: %+v", notice)
	}
}
