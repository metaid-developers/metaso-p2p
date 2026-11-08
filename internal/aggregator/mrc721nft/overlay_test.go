package mrc721nft

import (
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// Zero-conf overlay state machine tests (R-F-1/R-F-8): record → confirm
// handoff, eviction reconcile, chained spend tracking, gates.

func newZeroConfAggregator(t *testing.T) *Aggregator {
	t.Helper()
	a := newTestAggregator(t)
	a.SetZeroConfGates(true, true)
	return a
}

// mempoolPin builds a pin as the engine's CatchMempoolPins produces it:
// height -1, fetch-order tx index, wire-hash input pointers.
func mempoolPin(id, path, op string, content []byte, address string) *aggregator.PinInscription {
	pin := testPin(id, path, op, content, address)
	pin.GenesisHeight = -1
	pin.TxIndex = 0
	pin.Inputs = []string{"fundingtxid:1"}
	return pin
}

func TestOverlayPinRecordedAndDroppedOnConfirm(t *testing.T) {
	a := newZeroConfAggregator(t)

	// The album exists in the confirmed aggregate, so the overlay mint
	// attributes to it and its confirmation commits an item.
	runPins(t, a, testPin("zc1", "/nft/mrc721/ZcAlbum/collection_desc", "create", []byte(`{"name":"ZC"}`), creator))

	pin := mempoolPin("om1", "/nft/mrc721/ZcAlbum/nft-1", "create", []byte(`"zc"`), creator)
	if _, err := a.HandleMempoolPin(pin); err != nil {
		t.Fatalf("mempool pin: %v", err)
	}
	entry := a.overlayPins["om1"]
	if entry == nil || entry.Kind != overlayEffectMint || entry.Collection != "ZcAlbum" {
		t.Fatalf("overlay mint not recorded: %+v", entry)
	}
	if entry.Mint == nil || entry.Mint.OutputIndex != 0 || len(entry.Mint.Inputs) != 1 {
		t.Fatalf("overlay mint facts wrong: %+v", entry.Mint)
	}
	if _, live := a.overlayTxids[entry.TxId]; !live {
		t.Fatalf("overlay txid not tracked")
	}
	// Confirmed aggregate untouched.
	if item, _ := a.db.GetItem("om1"); item != nil {
		t.Fatalf("overlay must not mutate the confirmed aggregate")
	}

	// Confirmation handoff: the same pin folding through the block path
	// drops the overlay entry in the same critical section.
	if _, err := a.HandleBlockPin(pin); err != nil {
		t.Fatalf("block pin: %v", err)
	}
	if a.overlayPins["om1"] != nil {
		t.Fatalf("overlay entry not dropped on confirmation")
	}
	if _, live := a.overlayTxids[entry.TxId]; live {
		t.Fatalf("overlay txid not dropped on confirmation")
	}
	if item, _ := a.db.GetItem("om1"); item == nil {
		t.Fatalf("confirmed item missing after handoff")
	}
}

func TestOverlayPinIdempotentRedelivery(t *testing.T) {
	a := newZeroConfAggregator(t)
	pin := mempoolPin("om2", "/nft/mrc721/ZcAlbum/nft-2", "create", []byte(`"z2"`), creator)
	if _, err := a.HandleMempoolPin(pin); err != nil {
		t.Fatalf("mempool pin: %v", err)
	}
	first := a.overlayPins["om2"].FirstSeenAt
	time.Sleep(1100 * time.Millisecond)
	if _, err := a.HandleMempoolPin(pin); err != nil {
		t.Fatalf("re-delivery: %v", err)
	}
	if got := a.overlayPins["om2"].FirstSeenAt; got != first {
		t.Fatalf("re-delivery must keep firstSeenAt: %d != %d", got, first)
	}
}

func TestOverlayReconcileEvictsDroppedTx(t *testing.T) {
	a := newZeroConfAggregator(t)
	pin := mempoolPin("om3", "/nft/mrc721/ZcAlbum/nft-3", "create", []byte(`"z3"`), creator)
	_, _ = a.HandleMempoolPin(pin)
	txid := a.overlayPins["om3"].TxId

	// A failed fetch never reconciles (nil set).
	a.ReconcileMempoolRound("mvc", nil)
	if a.overlayPins["om3"] == nil {
		t.Fatalf("nil observation must not reconcile")
	}
	// A complete round without the txid evicts it.
	a.ReconcileMempoolRound("mvc", []string{"some-other-txid"})
	if a.overlayPins["om3"] != nil {
		t.Fatalf("evicted tx's overlay entry must be gone")
	}
	if _, live := a.overlayTxids[txid]; live {
		t.Fatalf("evicted txid must be gone")
	}
}

func TestOverlaySpendChainAndTrackedOutputs(t *testing.T) {
	a := newZeroConfAggregator(t)

	// Confirmed base item owned by creator at outpoint O1.
	runPins(t, a, testPin("ci1", "/nft/mrc721/ChainAlbum/collection_desc", "create", []byte(`{"name":"CH"}`), creator))
	runPins(t, a, testPin("mi1", "/nft/mrc721/ChainAlbum/x", "create", []byte(`"x"`), creator))

	O1 := "basetxid:0"
	item, _ := a.db.GetItem("mi1")
	item.Output = O1
	if err := a.db.SaveItem(item); err != nil {
		t.Fatalf("seed output: %v", err)
	}
	a.markTrackedDirty()

	// Incremental round 1: creator spends O1 → O2 to holder.
	O2 := "buytxid:0"
	a.HandleMempoolSpendsIncremental("mvc", map[string]aggregator.TransferNotice{
		O1: {FromAddress: creator, ToAddress: holder, Output: O2, OutputValue: 1000, Location: O2 + ":0"},
	})
	if sp := a.overlaySpends[O1]; sp == nil || sp.ToAddress != holder || sp.ItemPinId != "mi1" {
		t.Fatalf("overlay spend not recorded: %+v", sp)
	}
	// The overlay delivery outpoint is now tracked; the spent one is not.
	tracked := a.TrackedOutputs()
	if _, still := tracked[O1]; still {
		t.Fatalf("spent outpoint must leave the tracked set")
	}
	if got := tracked[O2]; got.Owner != holder || got.Ref != "mi1" {
		t.Fatalf("overlay delivery outpoint not tracked: %+v", got)
	}

	// Incremental round 2 (chained, before any confirmation): holder spends
	// O2 → O3 to impostor.
	O3 := "reselltxid:0"
	a.HandleMempoolSpendsIncremental("mvc", map[string]aggregator.TransferNotice{
		O2: {FromAddress: holder, ToAddress: impostor, Output: O3, OutputValue: 1000, Location: O3 + ":0"},
	})
	if sp := a.overlaySpends[O2]; sp == nil || sp.ToAddress != impostor {
		t.Fatalf("chained overlay spend not recorded: %+v", sp)
	}
	tracked = a.TrackedOutputs()
	if _, still := tracked[O2]; still {
		t.Fatalf("chained-spent outpoint must leave the tracked set")
	}
	if got := tracked[O3]; got.Owner != impostor {
		t.Fatalf("chained delivery outpoint not tracked: %+v", got)
	}

	// Confirmation handoff: the confirmed block settles ownership and drops
	// the overlay entries of the spending txids.
	a.HandleBlockTransfers("mvc", 300, map[string]aggregator.TransferNotice{
		O1: {FromAddress: creator, ToAddress: holder, Output: O2, OutputValue: 1000, Location: "buytxid:0:0"},
	})
	if a.overlaySpends[O1] != nil {
		t.Fatalf("confirmed spend's overlay entry not dropped")
	}
	confirmed, _ := a.db.GetItem("mi1")
	if confirmed.OwnerAddress != holder || confirmed.Output != O2 {
		t.Fatalf("confirmed ownership wrong: %+v", confirmed)
	}
	// The chained (still unconfirmed) spend survives until its own tx confirms.
	if a.overlaySpends[O2] == nil {
		t.Fatalf("chained overlay spend must survive the first confirmation")
	}
}

func TestOverlayGatesOffRestoresToday(t *testing.T) {
	// Gates off (zero values): mempool pins are not recorded, the
	// incremental spend channel is a strict no-op, tracked outputs stay
	// confirmed-only — exactly today's behavior.
	a := newTestAggregator(t)
	runPins(t, a, testPin("cg1", "/nft/mrc721/GateOff/collection_desc", "create", []byte(`{"name":"GO"}`), creator))
	runPins(t, a, testPin("gm1", "/nft/mrc721/GateOff/x", "create", []byte(`"x"`), creator))

	if _, err := a.HandleMempoolPin(mempoolPin("gz1", "/nft/mrc721/GateOff/y", "create", []byte(`"y"`), creator)); err != nil {
		t.Fatalf("mempool pin: %v", err)
	}
	if len(a.overlayPins) != 0 {
		t.Fatalf("issuance gate off must not record overlay pins")
	}
	a.HandleMempoolSpendsIncremental("mvc", map[string]aggregator.TransferNotice{
		"gm1:0": {FromAddress: creator, ToAddress: holder, Output: "x:1", OutputValue: 1, Location: "x:1:0"},
	})
	if len(a.overlaySpends) != 0 {
		t.Fatalf("trade gate off must not record overlay spends")
	}
	a.ReconcileMempoolRound("mvc", []string{}) // must not panic with gates off
}

func TestOverlayCapEvictsOldest(t *testing.T) {
	a := newZeroConfAggregator(t)
	a.SetOverlayCap(2)
	ids := []string{"cap1", "cap2", "cap3"}
	for i, id := range ids {
		pin := mempoolPin(id, "/nft/mrc721/CapOff/n", "create", []byte(`"n"`), creator)
		pin.Timestamp = int64(1700000000 + i)
		if _, err := a.HandleMempoolPin(pin); err != nil {
			t.Fatalf("pin %s: %v", id, err)
		}
		if i < len(ids)-1 {
			time.Sleep(1100 * time.Millisecond) // distinct firstSeenAt seconds
		}
	}
	if len(a.overlayPins) != 2 {
		t.Fatalf("cap not enforced: %d entries", len(a.overlayPins))
	}
	if a.overlayPins["cap1"] != nil {
		t.Fatalf("oldest entry must be evicted first")
	}
	if a.overlayPins["cap3"] == nil {
		t.Fatalf("newest entry must survive")
	}
}

func TestOverlayColCreateAndDescRecording(t *testing.T) {
	a := newZeroConfAggregator(t)

	colPin := mempoolPin("oc1", "/nft/mrc721/ZcReg/collection_desc", "create",
		[]byte(`{"name":"ZCR","totalSupply":5,"royaltyRate":3}`), creator)
	if _, err := a.HandleMempoolPin(colPin); err != nil {
		t.Fatalf("col pin: %v", err)
	}
	entry := a.overlayPins["oc1"]
	if entry == nil || entry.Kind != overlayEffectColCreate || entry.ColCreate == nil {
		t.Fatalf("overlay col_create not recorded: %+v", entry)
	}
	if entry.ColCreate.TotalSupply != 5 || entry.ColCreate.RoyaltyRate != 3 || entry.ColCreate.GenesisHeight != -1 {
		t.Fatalf("overlay col facts wrong: %+v", entry.ColCreate)
	}

	// Invalid range is rejected at record time, like the confirmed path.
	badPin := mempoolPin("oc2", "/nft/mrc721/ZcReg/collection_desc", "create",
		[]byte(`{"name":"BAD","totalSupply":-7}`), creator)
	if _, err := a.HandleMempoolPin(badPin); err != nil {
		t.Fatalf("bad col pin: %v", err)
	}
	if a.overlayPins["oc2"] != nil {
		t.Fatalf("out-of-range overlay collection_desc must be skipped")
	}

	descPin := mempoolPin("od1", "/nft/mrc721/ZcReg/item_desc", "create",
		[]byte(`{"items":[{"pinid":"om9","name":"Z Nine","metadata":{"g":"x"}}]}`), creator)
	if _, err := a.HandleMempoolPin(descPin); err != nil {
		t.Fatalf("desc pin: %v", err)
	}
	descEntry := a.overlayPins["od1"]
	if descEntry == nil || descEntry.Desc == nil || len(descEntry.Desc.Items) != 1 {
		t.Fatalf("overlay item_desc not recorded: %+v", descEntry)
	}
	if got := descEntry.Desc.Items[0].MetadataText(); got != `{"g":"x"}` {
		t.Fatalf("overlay desc metadata wrong: %q", got)
	}
}
