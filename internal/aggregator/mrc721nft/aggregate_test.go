package mrc721nft

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Aggregate unit tests, ported from man-p2p's man/mrc721_test.go and
// man/mrc721_colmod_test.go: registration / first-wins / supply caps /
// out-of-order self-healing / case handling / the full collection_desc
// modify matrix / the transfer hook. They drive HandleBlockPin (the block
// ingest path) and the transfer hook directly — no chain adapter needed.

func newTestAggregator(t *testing.T) *Aggregator {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	a := &Aggregator{}
	if err := a.Init(store, nil); err != nil {
		t.Fatalf("init aggregator: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return a
}

func testPin(id, path, op string, content []byte, address string) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:                 id,
		Path:               path,
		Operation:          op,
		ContentBody:        content,
		Address:            address,
		ChainName:          "mvc",
		GenesisHeight:      100,
		GenesisTransaction: "tx-" + id,
		Timestamp:          1700000000,
		Output:             id + ":0",
		OutputValue:        1000,
		ContentType:        "application/json",
		IsOpReturn:         true,
	}
}

func runPins(t *testing.T, a *Aggregator, pins ...*aggregator.PinInscription) {
	t.Helper()
	for _, pin := range pins {
		if _, err := a.HandleBlockPin(pin); err != nil {
			t.Fatalf("ingest pin %s: %v", pin.Id, err)
		}
	}
}

func modifyPin(id, collectionName, address, content string) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:                 id,
		Path:               "/nft/mrc721/" + collectionName + "/collection_desc",
		Operation:          "modify",
		ContentBody:        []byte(content),
		Address:            address,
		ChainName:          "mvc",
		GenesisHeight:      300,
		GenesisTransaction: "tx-" + id,
		Timestamp:          1700000500,
		IsOpReturn:         true,
	}
}

func transferFor(itemPinId, to, from, output string, value int64, meltdown bool) (map[string]*TransferInfo, map[string]string) {
	transferMap := map[string]*TransferInfo{
		itemPinId + ":0": {
			Address:     to,
			FromAddress: from,
			Output:      output,
			OutputValue: value,
			Location:    "spendtx-" + itemPinId,
			IsMeltdown:  meltdown,
		},
	}
	pinIdByOutput := map[string]string{itemPinId + ":0": itemPinId}
	return transferMap, pinIdByOutput
}

const (
	creator  = "mvc1qcreator"
	other    = "mvc1qother"
	holder   = "mvc1qholder"
	impostor = "mvc1qimpostor"
)

func TestCollectionRegisterFirstWins(t *testing.T) {
	a := newTestAggregator(t)

	descA := `{"name":"My Cool Album","totalSupply":100,"royaltyRate":5,"desc":"D","website":"https://Example.COM","cover":"metafile://coverpin","metadata":{"x":1}}`
	descB := `{"name":"Imposter Album","totalSupply":1}`
	pinA := testPin("collA", "/nft/mrc721/TestAlbum/collection_desc", "create", []byte(descA), creator)
	pinB := testPin("collB", "/nft/mrc721/TestAlbum/collection_desc", "create", []byte(descB), other)

	runPins(t, a, pinA, pinB)

	col, err := a.db.GetCollectionByName("TestAlbum")
	if err != nil || col == nil {
		t.Fatalf("collection not registered: %v", err)
	}
	if col.PinId != "collA" {
		t.Fatalf("first collection_desc should win, got pinid=%s", col.PinId)
	}
	if col.Name != "My Cool Album" || col.Website != "https://Example.COM" {
		t.Fatalf("value case not preserved: name=%q website=%q", col.Name, col.Website)
	}
	if col.TotalSupply != 100 || col.RoyaltyRate != 5 {
		t.Fatalf("fields wrong: totalSupply=%d royaltyRate=%d", col.TotalSupply, col.RoyaltyRate)
	}
	if col.CreateAddress != creator || col.CreateMetaId != metaIdByAddress(creator) || col.TotalNum != 0 {
		t.Fatalf("creator/totalnum wrong: %s %s %d", col.CreateAddress, col.CreateMetaId, col.TotalNum)
	}
	byPinId, err := a.db.GetCollectionByPinId("collA")
	if err != nil || byPinId == nil || byPinId.CollectionName != "TestAlbum" {
		t.Fatalf("collection by pinid lookup failed: %v", err)
	}
}

func TestDefaultsAndCIReturn(t *testing.T) {
	a := newTestAggregator(t)

	// JSON keys are case-insensitive (camelCase TotalSupply), royaltyRate
	// defaults to 0.
	desc := `{"Name":"Camel Keys","TotalSupply":10}`
	runPins(t, a, testPin("collC", "/nft/mrc721/CIAlbum/collection_desc", "create", []byte(desc), creator))

	col, _ := a.db.GetCollectionByName("CIAlbum")
	if col == nil {
		t.Fatal("collection not registered")
	}
	if col.Name != "Camel Keys" || col.TotalSupply != 10 {
		t.Fatalf("case-insensitive keys failed: %+v", col)
	}
	if col.RoyaltyRate != 0 {
		t.Fatalf("royaltyRate default should be 0, got %d", col.RoyaltyRate)
	}
}

func TestMintSeqAndCreatorMinted(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/SeqAlbum/collection_desc", "create", []byte(`{"name":"S","totalSupply":-1}`), creator),
		testPin("i1", "/nft/mrc721/SeqAlbum/item-one", "create", []byte(`"content 1"`), creator),
		testPin("i2", "/nft/mrc721/SeqAlbum/item-two", "create", []byte(`"content 2"`), other),
	)

	col, _ := a.db.GetCollectionByName("SeqAlbum")
	if col == nil || col.TotalNum != 2 {
		t.Fatalf("totalnum wrong: %+v", col)
	}
	item1, _ := a.db.GetItem("i1")
	item2, _ := a.db.GetItem("i2")
	if item1 == nil || item1.Number != 1 || !item1.CreatorMinted {
		t.Fatalf("item1 wrong: %+v", item1)
	}
	if item2 == nil || item2.Number != 2 || item2.CreatorMinted {
		t.Fatalf("item2 wrong: %+v", item2)
	}
	if item2.OwnerAddress != other || item2.OwnerMetaId != metaIdByAddress(other) {
		t.Fatalf("owner wrong: %+v", item2)
	}
	if item1.CollectionPinId != "c1" {
		t.Fatalf("collectionPinId wrong: %s", item1.CollectionPinId)
	}
	if item1.Output != "i1:0" || item1.OutputValue != 1000 {
		t.Fatalf("mint outpoint not recorded: %s %d", item1.Output, item1.OutputValue)
	}
	// In-collection ingest order.
	var seqs []int64
	a.db.IterateCollectionItems("SeqAlbum", func(seq int64, itemPinId string) bool {
		seqs = append(seqs, seq)
		return true
	})
	if len(seqs) != 2 || seqs[0] != 1 || seqs[1] != 2 {
		t.Fatalf("coll item order wrong: %v", seqs)
	}
	// Holder index.
	ownerCount := 0
	a.db.IterateOwnerItems(other, "SeqAlbum", func(cn, itemPinId string) bool {
		ownerCount++
		return true
	})
	if ownerCount != 1 {
		t.Fatalf("owner index count wrong: %d", ownerCount)
	}
}

func TestSupplyCap(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c", "/nft/mrc721/CapAlbum/collection_desc", "create", []byte(`{"name":"C","totalSupply":2}`), creator),
		testPin("m1", "/nft/mrc721/CapAlbum/a", "create", []byte(`"1"`), creator),
		testPin("m2", "/nft/mrc721/CapAlbum/b", "create", []byte(`"2"`), creator),
		testPin("m3", "/nft/mrc721/CapAlbum/c", "create", []byte(`"3"`), creator),
	)

	col, _ := a.db.GetCollectionByName("CapAlbum")
	if col == nil || col.TotalNum != 2 {
		t.Fatalf("totalnum should cap at 2: %+v", col)
	}
	if item, _ := a.db.GetItem("m3"); item != nil {
		t.Fatalf("m3 should be rejected beyond totalSupply")
	}
}

func TestOutOfOrderPending(t *testing.T) {
	a := newTestAggregator(t)

	// Mint and item_desc arrive before the collection_desc.
	runPins(t, a,
		testPin("m1", "/nft/mrc721/OooAlbum/nft-1", "create", []byte(`"raw nft"`), other),
		testPin("d1", "/nft/mrc721/OooAlbum/item_desc", "create",
			[]byte(`{"items":[{"pinid":"m1","name":"Named Later","cover":"metafile://x"}]}`), creator),
	)
	if item, _ := a.db.GetItem("m1"); item != nil {
		t.Fatalf("m1 should stay pending before collection register")
	}

	// Registration replays: mint commits + desc backfills.
	runPins(t, a, testPin("c1", "/nft/mrc721/OooAlbum/collection_desc", "create",
		[]byte(`{"name":"OOO","totalSupply":10}`), creator))

	item, _ := a.db.GetItem("m1")
	if item == nil || item.Number != 1 {
		t.Fatalf("pending mint not replayed: %+v", item)
	}
	if item.Name != "Named Later" || item.Cover != "metafile://x" {
		t.Fatalf("pending desc not applied: %+v", item)
	}
	col, _ := a.db.GetCollectionByName("OooAlbum")
	if col == nil || col.TotalNum != 1 {
		t.Fatalf("totalnum after replay wrong: %+v", col)
	}
	// Pending drained.
	left, _ := a.db.PopPending("OooAlbum")
	if len(left) != 0 {
		t.Fatalf("pending should be drained, left %d", len(left))
	}
}

func TestDescBeforeMint(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/DescFirstAlbum/collection_desc", "create", []byte(`{"name":"DF"}`), creator),
		testPin("d1", "/nft/mrc721/DescFirstAlbum/item_desc", "create",
			[]byte(`{"items":[{"pinid":"late-1","name":"Late Name","desc":"Late Desc"}]}`), creator),
	)
	if item, _ := a.db.GetItem("late-1"); item != nil {
		t.Fatalf("item should not exist before mint")
	}

	// Minting replays the staged desc.
	runPins(t, a, testPin("late-1", "/nft/mrc721/DescFirstAlbum/x", "create", []byte(`"late"`), other))

	item, _ := a.db.GetItem("late-1")
	if item == nil || item.Name != "Late Name" || item.Desc != "Late Desc" {
		t.Fatalf("desc not replayed after mint: %+v", item)
	}
}

func TestDescFirstWinsPerField(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/FWAlbum/collection_desc", "create", []byte(`{"name":"FW"}`), creator),
		testPin("i1", "/nft/mrc721/FWAlbum/a", "create", []byte(`"x"`), creator),
		testPin("d1", "/nft/mrc721/FWAlbum/item_desc", "create",
			[]byte(`{"items":[{"pinid":"i1","name":"First","desc":"D1"}]}`), creator),
		testPin("d2", "/nft/mrc721/FWAlbum/item_desc", "create",
			[]byte(`{"items":[{"pinid":"i1","name":"Second","cover":"metafile://c"}]}`), creator),
	)

	item, _ := a.db.GetItem("i1")
	if item.Name != "First" {
		t.Fatalf("first desc should win per field: %q", item.Name)
	}
	if item.Desc != "D1" {
		t.Fatalf("desc field wrong: %q", item.Desc)
	}
	if item.Cover != "metafile://c" {
		t.Fatalf("second desc should fill empty field: %q", item.Cover)
	}
}

func TestInvalidIgnored(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		// royaltyRate out of range
		testPin("bad1", "/nft/mrc721/BadAlbum/collection_desc", "create", []byte(`{"name":"B","royaltyRate":25}`), creator),
		// totalSupply out of range
		testPin("bad2", "/nft/mrc721/BadAlbum2/collection_desc", "create", []byte(`{"name":"B","totalSupply":-5}`), creator),
		// invalid JSON
		testPin("bad3", "/nft/mrc721/BadAlbum3/collection_desc", "create", []byte(`not-json`), creator),
		// non-create operation
		testPin("bad4", "/nft/mrc721/BadAlbum/collection_desc", "modify", []byte(`{"name":"B"}`), creator),
		// non-/nft/mrc721 path
		testPin("bad5", "/info/file/x", "create", []byte(`{"name":"B"}`), creator),
		// insufficient path segments
		testPin("bad6", "/nft/mrc721", "create", []byte(`{"name":"B"}`), creator),
		// witness-sourced MVC pin (outside man-p2p's MVC pin universe)
		func() *aggregator.PinInscription {
			p := testPin("bad7", "/nft/mrc721/WitnessAlbum/collection_desc", "create", []byte(`{"name":"W"}`), creator)
			p.IsOpReturn = false
			return p
		}(),
	)

	for _, name := range []string{"BadAlbum", "BadAlbum2", "BadAlbum3", "WitnessAlbum"} {
		if col, _ := a.db.GetCollectionByName(name); col != nil {
			t.Fatalf("collection %s should not be registered", name)
		}
	}
}

func TestHostPrefixedPathIndexed(t *testing.T) {
	a := newTestAggregator(t)

	// man-p2p indexes "<host>:/nft/mrc721/..." pins under the post-colon
	// path (ValidHostPath projection).
	runPins(t, a, testPin("hc1", "abcd1234:/nft/mrc721/HostAlbum/collection_desc", "create",
		[]byte(`{"name":"Hosted"}`), creator))

	col, _ := a.db.GetCollectionByName("HostAlbum")
	if col == nil || col.Name != "Hosted" {
		t.Fatalf("host-prefixed path should register the collection: %+v", col)
	}
}

func TestMempoolPinNotAggregated(t *testing.T) {
	a := newTestAggregator(t)

	mempoolPin := testPin("mp1", "/nft/mrc721/MemAlbum/a", "create", []byte(`"x"`), creator)
	if _, err := a.HandleMempoolPin(mempoolPin); err != nil {
		t.Fatalf("mempool ingest: %v", err)
	}
	if item, _ := a.db.GetItem("mp1"); item != nil {
		t.Fatal("mempool pin must not be aggregated")
	}
	if pending, _ := a.db.PopPending("MemAlbum"); len(pending) != 0 {
		t.Fatalf("mempool pin must not stage pending, got %d", len(pending))
	}
}

// ---------- transfer hook ----------

func TestTransferUpdatesOwner(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/TrAlbum/collection_desc", "create", []byte(`{"name":"T"}`), creator),
		testPin("n1", "/nft/mrc721/TrAlbum/a", "create", []byte(`"nft"`), other),
	)

	transferMap, pinIdByOutput := transferFor("n1", creator, other, "spendtx-n1:1", 546, false)
	a.applyTransferHook("mvc", 200, transferMap, pinIdByOutput, 1700000100)

	item, _ := a.db.GetItem("n1")
	if item == nil || item.OwnerAddress != creator || item.TransferCount != 1 {
		t.Fatalf("owner not updated: %+v", item)
	}
	if item.OwnerMetaId != metaIdByAddress(creator) {
		t.Fatalf("owner metaid wrong: %q", item.OwnerMetaId)
	}
	if item.Output != "spendtx-n1:1" || item.OutputValue != 546 {
		t.Fatalf("current outpoint not updated: %s %d", item.Output, item.OutputValue)
	}
	// Holder index re-keyed.
	newCount, oldCount := 0, 0
	a.db.IterateOwnerItems(creator, "TrAlbum", func(cn, ip string) bool { newCount++; return true })
	a.db.IterateOwnerItems(other, "TrAlbum", func(cn, ip string) bool { oldCount++; return true })
	if newCount != 1 || oldCount != 0 {
		t.Fatalf("owner index not swapped: new=%d old=%d", newCount, oldCount)
	}
	// Provenance history.
	histories := 0
	var first *ItemTransfer
	err := a.db.IterateItemTransfers("n1", func(tr *ItemTransfer) bool {
		histories++
		if first == nil {
			first = tr
		}
		return true
	})
	if err != nil || histories != 1 {
		t.Fatalf("transfer history not recorded: %v %d", err, histories)
	}
	if first.FromAddress != other || first.ToAddress != creator || first.Height != 200 {
		t.Fatalf("transfer history fields wrong: %+v", first)
	}
}

func TestMeltdownBurns(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/BurnAlbum/collection_desc", "create", []byte(`{"name":"B"}`), creator),
		testPin("n1", "/nft/mrc721/BurnAlbum/a", "create", []byte(`"nft"`), creator),
	)

	transferMap, pinIdByOutput := transferFor("n1", creator, creator, "melttx:0", 546, true)
	a.applyTransferHook("mvc", 201, transferMap, pinIdByOutput, 1700000200)

	item, _ := a.db.GetItem("n1")
	if item == nil || !item.Burned {
		t.Fatalf("meltdown should mark burned: %+v", item)
	}
	// Removed from holder views.
	ownerCount := 0
	a.db.IterateOwnerItems(creator, "BurnAlbum", func(cn, ip string) bool { ownerCount++; return true })
	if ownerCount != 0 {
		t.Fatalf("burned item should leave owner view: %d", ownerCount)
	}
	// Genesis outpoint stays (a meltdown does not rewrite Output).
	if item.Output != "n1:0" {
		t.Fatalf("burned item output should stay genesis: %s", item.Output)
	}
}

func TestTransferNonItemNoop(t *testing.T) {
	a := newTestAggregator(t)

	// Transfers of non-MRC-721 pins: the hook must be side-effect free.
	transferMap, pinIdByOutput := transferFor("plain-pin", creator, other, "x:1", 1000, false)
	a.applyTransferHook("mvc", 202, transferMap, pinIdByOutput, 1700000300)

	if item, _ := a.db.GetItem("plain-pin"); item != nil {
		t.Fatalf("non-item pin should not be indexed")
	}
}

func TestTransferSettlesPendingChannel(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/SettleAlbum/collection_desc", "create", []byte(`{"name":"S"}`), creator),
		testPin("n1", "/nft/mrc721/SettleAlbum/a", "create", []byte(`"nft"`), creator),
	)
	if err := a.db.SavePendingTransfer(&PendingTransfer{
		ItemPinId:     "n1",
		ChainName:     "mvc",
		SpentOutpoint: "n1:0",
		FromAddress:   creator,
		ToAddress:     other,
		TxId:          "spendtx-n1",
		FirstSeenAt:   1700000150,
	}); err != nil {
		t.Fatalf("seed pending transfer: %v", err)
	}

	transferMap, pinIdByOutput := transferFor("n1", other, creator, "spendtx-n1:1", 546, false)
	a.applyTransferHook("mvc", 210, transferMap, pinIdByOutput, 1700000200)

	if pt, _ := a.db.GetPendingTransfer("n1"); pt != nil {
		t.Fatalf("confirmed spend should settle the pending channel: %+v", pt)
	}
}

// ---------- idempotency (dual-path delivery / backfill re-runs) ----------

func TestDoubleMintIdempotent(t *testing.T) {
	a := newTestAggregator(t)

	pin1 := testPin("c1", "/nft/mrc721/IdemAlbum/collection_desc", "create", []byte(`{"name":"I"}`), creator)
	item1 := testPin("i1", "/nft/mrc721/IdemAlbum/a", "create", []byte(`"x"`), creator)
	// Duplicate delivery within one batch and across batches.
	runPins(t, a, pin1, item1, item1)
	runPins(t, a, item1)

	col, _ := a.db.GetCollectionByName("IdemAlbum")
	if col == nil || col.TotalNum != 1 {
		t.Fatalf("double mint should not double count: %+v", col)
	}
	item, _ := a.db.GetItem("i1")
	if item == nil || item.Number != 1 {
		t.Fatalf("item number should stay 1: %+v", item)
	}
	listed := 0
	a.db.IterateCollectionItems("IdemAlbum", func(seq int64, id string) bool { listed++; return true })
	if listed != 1 {
		t.Fatalf("coll item list should have 1 entry: %d", listed)
	}
}

func TestDoubleTransferIdempotent(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("c1", "/nft/mrc721/IdemTrAlbum/collection_desc", "create", []byte(`{"name":"IT"}`), creator),
		testPin("i1", "/nft/mrc721/IdemTrAlbum/a", "create", []byte(`"x"`), creator),
	)
	transferMap, pinIdByOutput := transferFor("i1", other, creator, "spend:1", 546, false)
	a.applyTransferHook("mvc", 210, transferMap, pinIdByOutput, 1700000400)
	a.applyTransferHook("mvc", 210, transferMap, pinIdByOutput, 1700000400)

	item, _ := a.db.GetItem("i1")
	if item == nil || item.TransferCount != 1 || item.OwnerAddress != other {
		t.Fatalf("double transfer should be idempotent: %+v", item)
	}
	historyCount := 0
	a.db.IterateItemTransfers("i1", func(tr *ItemTransfer) bool { historyCount++; return true })
	if historyCount != 1 {
		t.Fatalf("transfer history should have 1 entry: %d", historyCount)
	}
}

// ---------- collection_desc modify (issuer-only version chain) ----------

func TestCollectionModifyByIssuer(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("mc1", "/nft/mrc721/ModAlbum/collection_desc", "create",
			[]byte(`{"name":"V1","desc":"old","cover":"metafile://old","totalSupply":10,"royaltyRate":5}`), creator),
	)

	// Issuer modify: display fields overwritten wholesale; totalSupply /
	// royaltyRate pass through.
	mod1 := modifyPin("mm1", "ModAlbum", creator,
		`{"name":"V2","desc":"new","website":"https://New.Example","cover":"metafile://newcover","metadata":"m2","totalSupply":10,"royaltyRate":5}`)
	runPins(t, a, mod1)

	col, _ := a.db.GetCollectionByName("ModAlbum")
	if col == nil || col.Name != "V2" || col.Desc != "new" || col.Website != "https://New.Example" || col.Cover != "metafile://newcover" || col.Metadata != "m2" {
		t.Fatalf("modify fields not applied: %+v", col)
	}
	if col.TotalSupply != 10 || col.RoyaltyRate != 5 {
		t.Fatalf("supply/royalty should stay: %+v", col)
	}
	if len(col.ModifyHistory) != 1 || col.ModifyHistory[0] != "mm1" {
		t.Fatalf("history wrong: %v", col.ModifyHistory)
	}

	// Two modifies: the latest wins.
	mod2 := modifyPin("mm2", "ModAlbum", creator, `{"name":"V3"}`)
	runPins(t, a, mod2)
	col, _ = a.db.GetCollectionByName("ModAlbum")
	if col.Name != "V3" || col.Desc != "new" {
		t.Fatalf("second modify should win name but keep rest: %+v", col)
	}
	if len(col.ModifyHistory) != 2 || col.ModifyHistory[1] != "mm2" {
		t.Fatalf("history order wrong: %v", col.ModifyHistory)
	}

	// Supply gate: totalnum=0, lower supply to 1, mint 2 → the second one
	// must be rejected.
	mod3 := modifyPin("mm3", "ModAlbum", creator, `{"name":"V4","totalSupply":1}`)
	runPins(t, a, mod3)
	runPins(t, a,
		testPin("mi1", "/nft/mrc721/ModAlbum/a", "create", []byte(`"1"`), creator),
		testPin("mi2", "/nft/mrc721/ModAlbum/b", "create", []byte(`"2"`), creator),
	)
	col, _ = a.db.GetCollectionByName("ModAlbum")
	if col.TotalNum != 1 {
		t.Fatalf("lowered totalSupply should gate future mints: %+v", col)
	}
	if item, _ := a.db.GetItem("mi2"); item != nil {
		t.Fatal("mi2 should be rejected beyond lowered supply")
	}
}

func TestCollectionModifyPermissions(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a, testPin("mc1", "/nft/mrc721/PermAlbum/collection_desc", "create",
		[]byte(`{"name":"V1"}`), creator))

	// Non-issuer modify is ignored.
	runPins(t, a, modifyPin("bad1", "PermAlbum", impostor, `{"name":"HACKED"}`))
	col, _ := a.db.GetCollectionByName("PermAlbum")
	if col.Name != "V1" || len(col.ModifyHistory) != 0 {
		t.Fatalf("non-issuer modify must be ignored: %+v", col)
	}

	// Invalid JSON / out-of-range totalSupply / out-of-range royaltyRate are
	// ignored entirely.
	runPins(t, a,
		modifyPin("bad2", "PermAlbum", creator, `not-json`),
		modifyPin("bad3", "PermAlbum", creator, `{"name":"X","totalSupply":-5}`),
		modifyPin("bad4", "PermAlbum", creator, `{"name":"X","royaltyRate":25}`),
	)
	col, _ = a.db.GetCollectionByName("PermAlbum")
	if col.Name != "V1" || len(col.ModifyHistory) != 0 {
		t.Fatalf("invalid modifies must be ignored entirely: %+v", col)
	}
}

func TestCollectionModifyIdempotent(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a, testPin("mc1", "/nft/mrc721/IdemModAlbum/collection_desc", "create",
		[]byte(`{"name":"V1"}`), creator))
	mod := modifyPin("im1", "IdemModAlbum", creator, `{"name":"V2"}`)
	// The same modify pin delivered multiple times (chain + replay).
	runPins(t, a, mod, mod)
	runPins(t, a, mod)

	col, _ := a.db.GetCollectionByName("IdemModAlbum")
	if col.Name != "V2" || len(col.ModifyHistory) != 1 || col.ModifyHistory[0] != "im1" {
		t.Fatalf("duplicate modify ingest must be idempotent: %+v", col)
	}
}

func TestCollectionModifyPendingReplay(t *testing.T) {
	a := newTestAggregator(t)

	// Modifies arrive before the collection registers: staged, replayed
	// chronologically after registration.
	early1 := modifyPin("pm1", "PendModAlbum", creator, `{"name":"MOD1"}`)
	early1.Timestamp = 1700000100
	early2 := modifyPin("pm2", "PendModAlbum", creator, `{"name":"MOD2"}`)
	early2.Timestamp = 1700000200
	runPins(t, a, early2, early1) // out-of-order delivery

	if col, _ := a.db.GetCollectionByName("PendModAlbum"); col != nil {
		t.Fatal("collection should not exist yet")
	}

	runPins(t, a, testPin("pc1", "/nft/mrc721/PendModAlbum/collection_desc", "create",
		[]byte(`{"name":"BASE"}`), creator))

	col, _ := a.db.GetCollectionByName("PendModAlbum")
	if col == nil || col.Name != "MOD2" {
		t.Fatalf("pending modifies should replay chronologically, latest wins: %+v", col)
	}
	if len(col.ModifyHistory) != 2 {
		t.Fatalf("both pending modifies should be in history: %v", col.ModifyHistory)
	}
	left, _ := a.db.PopPending("PendModAlbum")
	if len(left) != 0 {
		t.Fatalf("pending should be drained: %d", len(left))
	}
}

func TestItemPathModifyIgnored(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("ic1", "/nft/mrc721/ItmModAlbum/collection_desc", "create", []byte(`{"name":"I"}`), creator),
		testPin("ii1", "/nft/mrc721/ItmModAlbum/a", "create", []byte(`"x"`), other),
	)
	// A modify on an item path has no aggregate effect.
	itemMod := testPin("badmod", "/nft/mrc721/ItmModAlbum/a", "modify", []byte(`"hacked"`), other)
	runPins(t, a, itemMod)

	item, _ := a.db.GetItem("ii1")
	if item == nil || item.OwnerAddress != other || item.TransferCount != 0 {
		t.Fatalf("item modify must not affect item: %+v", item)
	}
	// revoke has no effect under /nft/mrc721.
	revoke := testPin("rv1", "/nft/mrc721/ItmModAlbum/collection_desc", "revoke", []byte(`{"name":"GONE"}`), creator)
	runPins(t, a, revoke)
	col, _ := a.db.GetCollectionByName("ItmModAlbum")
	if col == nil || col.Name != "I" {
		t.Fatalf("revoke must be ignored: %+v", col)
	}
}

func TestModifyTransferAndVerifyUntouched(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("tc1", "/nft/mrc721/TrModAlbum/collection_desc", "create", []byte(`{"name":"T"}`), creator),
		testPin("ti1", "/nft/mrc721/TrModAlbum/a", "create", []byte(`"x"`), creator),
	)
	runPins(t, a, modifyPin("tm1", "TrModAlbum", creator, `{"name":"T2"}`))

	// A modify is an independent pin; ownership/transfers are unaffected.
	transferMap, pinIdByOutput := transferFor("ti1", other, creator, "spend:1", 546, false)
	a.applyTransferHook("mvc", 400, transferMap, pinIdByOutput, 1700000600)

	item, _ := a.db.GetItem("ti1")
	if item == nil || item.OwnerAddress != other || item.TransferCount != 1 {
		t.Fatalf("transfer after modify broken: %+v", item)
	}
	col, _ := a.db.GetCollectionByName("TrModAlbum")
	if col == nil || col.Name != "T2" || len(col.ModifyHistory) != 1 {
		t.Fatalf("collection state after transfer should be intact: %+v", col)
	}
	// creatorMinted is a mint-time fact and must survive collection modifies.
	if !item.CreatorMinted {
		t.Fatal("creatorMinted must survive collection modify")
	}
}

func TestListCollectionsPagination(t *testing.T) {
	a := newTestAggregator(t)

	runPins(t, a,
		testPin("k1", "/nft/mrc721/Alpha/collection_desc", "create", []byte(`{"name":"A"}`), creator),
		testPin("k2", "/nft/mrc721/Beta/collection_desc", "create", []byte(`{"name":"B"}`), creator),
		testPin("k3", "/nft/mrc721/Gamma/collection_desc", "create", []byte(`{"name":"G"}`), creator),
	)

	page, total, err := a.db.ListCollections(0, 2)
	if err != nil {
		t.Fatalf("list collections: %v", err)
	}
	if total != 3 || len(page) != 2 || page[0].CollectionName != "Alpha" || page[1].CollectionName != "Beta" {
		t.Fatalf("page 1 wrong: total=%d page=%+v", total, page)
	}
	page2, total2, err := a.db.ListCollections(2, 2)
	if err != nil {
		t.Fatalf("list collections page 2: %v", err)
	}
	if total2 != 3 || len(page2) != 1 || page2[0].CollectionName != "Gamma" {
		t.Fatalf("page 2 wrong: total=%d page=%+v", total2, page2)
	}
	empty, total3, err := a.db.ListCollections(10, 2)
	if err != nil || total3 != 3 || len(empty) != 0 {
		t.Fatalf("offset beyond end wrong: total=%d len=%d err=%v", total3, len(empty), err)
	}
}
