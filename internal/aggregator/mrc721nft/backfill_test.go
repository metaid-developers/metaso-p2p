package mrc721nft

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

// Backfill unit tests: an httptest server plays the manapi-compatible
// upstream (collection pageList / collection items pageList / item
// transfers) and the hydrate must rebuild the aggregate identically —
// including offset pagination, duplicate spellings in the DTOs, holder-index
// reconciliation on re-runs and transfer-history dedup.

type fakeUpstream struct {
	collections []upstreamCollection
	items       map[string][]upstreamItem // collectionPinId → items
	transfers   map[string][]upstreamTransfer
	pageSize    int
}

func pageOf[T any](rows []T, offset int64, size int) []T {
	if offset >= int64(len(rows)) {
		return nil
	}
	end := offset + int64(size)
	if end > int64(len(rows)) {
		end = int64(len(rows))
	}
	return rows[offset:end]
}

func respondList(w http.ResponseWriter, rows any, total int64) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code":    1,
		"message": "ok",
		"data":    map[string]any{"list": rows, "total": total},
	})
}

func (f *fakeUpstream) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		t.Helper()
		q := r.URL.Query()
		cursor, _ := strconv.ParseInt(q.Get("cursor"), 10, 64)
		size := f.pageSize
		if size <= 0 {
			size = 2
		}
		switch r.URL.Path {
		case "/api/mrc721/collection/pageList":
			respondList(w, pageOf(f.collections, cursor, size), int64(len(f.collections)))
		case "/api/mrc721/collection/items/pageList":
			items := f.items[q.Get("pinid")]
			respondList(w, pageOf(items, cursor, size), int64(len(items)))
		case "/api/mrc721/item/transfers":
			trs := f.transfers[q.Get("pinId")]
			respondList(w, pageOf(trs, cursor, size), int64(len(trs)))
		default:
			t.Fatalf("unexpected upstream path: %s", r.URL.Path)
		}
	}
}

func fixtureUpstream() *fakeUpstream {
	return &fakeUpstream{
		collections: []upstreamCollection{
			{
				CollectionName: "BootAlbum", Name: "Boot Name", TotalSupply: 10, RoyaltyRate: 3,
				Desc: "booted", Website: "https://boot.example", Cover: "metafile://bootcover",
				Metadata: `{"boot":true}`, PinId: "bootcoll-1",
				Address: "mvc1qissuer", CreateAddress: "mvc1qissuer",
				MetaId: "idissuer", CreateMetaId: "idissuer",
				CreateTime: 1700000001, TotalNum: 3,
				ModifyHistory:    []string{"modpin-1"},
				GenesisChainName: "mvc", GenesisHeight: 500, GenesisTx: "colltx",
			},
		},
		items: map[string][]upstreamItem{
			"bootcoll-1": {
				{
					CollectionPinId: "bootcoll-1", CollectionName: "BootAlbum",
					ItemPinId: "item-1", Number: 1, Name: "One", Desc: "first",
					Cover: "metafile://one", Metadata: `{"k":1}`,
					OwnerAddress: "mvc1qholder1", OwnerMetaId: "idholder1",
					CreatorMinted: true, ContentType: "application/json",
					ContentSummary: `"raw one"`, Output: "origtx:1", OutputValue: 546,
					TransferCount: 1, GenesisChainName: "mvc", GenesisHeight: 501,
					GenesisTx: "itemtx-1", CreateTime: 1700000011,
				},
				{
					CollectionPinId: "bootcoll-1", CollectionName: "BootAlbum",
					ItemPinId: "item-2", Number: 2, Name: "Two",
					OwnerAddress: "mvc1qholder2", OwnerMetaId: "idholder2",
					ContentSummary: `"raw two"`, Output: "origtx:2", OutputValue: 546,
					TransferCount: 0, GenesisChainName: "mvc", GenesisHeight: 502,
					GenesisTx: "itemtx-2", CreateTime: 1700000012,
				},
				{
					CollectionPinId: "bootcoll-1", CollectionName: "BootAlbum",
					ItemPinId: "item-3", Number: 3, Name: "Three",
					OwnerAddress: "", OwnerMetaId: "",
					ContentSummary: `"raw three"`, Output: "origtx:3", OutputValue: 546,
					Burned: true, TransferCount: 2, GenesisChainName: "mvc",
					GenesisHeight: 503, GenesisTx: "itemtx-3", CreateTime: 1700000013,
				},
			},
		},
		transfers: map[string][]upstreamTransfer{
			"item-1": {
				{ItemPinId: "item-1", FromAddress: "mvc1qissuer", ToAddress: "mvc1qholder1", ChainName: "mvc", Height: 600, TxId: "spendtx-1", Time: 1700000200},
			},
			"item-3": {
				{ItemPinId: "item-3", FromAddress: "mvc1qissuer", ToAddress: "mvc1qmid", ChainName: "mvc", Height: 700, TxId: "spendtx-3a", Time: 1700000300},
				{ItemPinId: "item-3", FromAddress: "mvc1qmid", ToAddress: "", ChainName: "mvc", Height: 701, TxId: "spendtx-3b", Time: 1700000301},
			},
		},
	}
}

func TestBackfillNftHydrates(t *testing.T) {
	a := newTestAggregator(t)
	fake := fixtureUpstream()
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	stats := &BackfillStats{}
	if err := a.BackfillNft(BackfillOptions{
		Context:  context.Background(),
		Client:   NewUpstreamClient(server.URL, server.Client()),
		PageSize: 2, // force multi-page walks
		Stats:    stats,
	}); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if stats.Collections != 1 || stats.Items != 3 || stats.Transfers != 3 {
		t.Fatalf("stats wrong: %+v", stats)
	}

	col, _ := a.db.GetCollectionByName("BootAlbum")
	if col == nil || col.PinId != "bootcoll-1" || col.TotalNum != 3 || col.TotalSupply != 10 || col.RoyaltyRate != 3 {
		t.Fatalf("collection not hydrated: %+v", col)
	}
	if len(col.ModifyHistory) != 1 || col.ModifyHistory[0] != "modpin-1" {
		t.Fatalf("modify history not hydrated: %v", col.ModifyHistory)
	}
	if col.CreateMetaId != "idissuer" {
		t.Fatalf("createMetaId not taken from upstream: %q", col.CreateMetaId)
	}
	if _, err := a.db.GetCollectionByPinId("bootcoll-1"); err != nil {
		t.Fatalf("collection by pinid missing after hydrate: %v", err)
	}

	item1, _ := a.db.GetItem("item-1")
	if item1 == nil || item1.OwnerAddress != "mvc1qholder1" || item1.Number != 1 ||
		item1.Output != "origtx:1" || item1.OutputValue != 546 || item1.TransferCount != 1 ||
		item1.Name != "One" || !item1.CreatorMinted {
		t.Fatalf("item-1 not hydrated: %+v", item1)
	}
	item3, _ := a.db.GetItem("item-3")
	if item3 == nil || !item3.Burned || item3.OwnerAddress != "" || item3.TransferCount != 2 {
		t.Fatalf("item-3 not hydrated: %+v", item3)
	}

	// Holder index: burned item stays out, holders land in.
	holderCount := 0
	a.db.IterateOwnerItems("mvc1qholder1", "BootAlbum", func(cn, ip string) bool { holderCount++; return true })
	if holderCount != 1 {
		t.Fatalf("holder index missing item-1: %d", holderCount)
	}
	burnedCount := 0
	a.db.IterateOwnerItems("", "", func(cn, ip string) bool {
		if ip == "item-3" {
			burnedCount++
		}
		return true
	})
	if burnedCount != 0 {
		t.Fatal("burned item must not appear in the holder index")
	}

	// Provenance history.
	trs := 0
	a.db.IterateItemTransfers("item-1", func(tr *ItemTransfer) bool { trs++; return true })
	if trs != 1 {
		t.Fatalf("item-1 history wrong: %d", trs)
	}
	trs = 0
	a.db.IterateItemTransfers("item-3", func(tr *ItemTransfer) bool { trs++; return true })
	if trs != 2 {
		t.Fatalf("item-3 history wrong: %d", trs)
	}
}

func TestBackfillNftIdempotentRerun(t *testing.T) {
	a := newTestAggregator(t)
	fake := fixtureUpstream()
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	opts := BackfillOptions{
		Context:  context.Background(),
		Client:   NewUpstreamClient(server.URL, server.Client()),
		PageSize: 2,
	}
	if err := a.BackfillNft(opts); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Plain re-run: everything already exists at the same freshness, so
	// nothing gets rewritten and the transfer history dedups.
	stats := &BackfillStats{}
	opts.Stats = stats
	if err := a.BackfillNft(opts); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if stats.Items != 0 || stats.Transfers != 0 || stats.SkippedTransfers != 3 || stats.OwnerMoves != 0 {
		t.Fatalf("plain re-run must be a no-op: %+v", stats)
	}
	if item, _ := a.db.GetItem("item-2"); item.OwnerAddress != "mvc1qholder2" {
		t.Fatalf("re-run must not touch live state: %+v", item)
	}

	// Upstream strictly ahead (item-2 transferred while the daemon was
	// down): the row's transferCount beats the stored one, so owner,
	// transferCount and the holder index follow the upstream state.
	fake.items["bootcoll-1"][1].OwnerAddress = "mvc1qholder1"
	fake.items["bootcoll-1"][1].OwnerMetaId = "idholder1"
	fake.items["bootcoll-1"][1].TransferCount = 1
	fake.transfers["item-2"] = []upstreamTransfer{
		{ItemPinId: "item-2", FromAddress: "mvc1qholder2", ToAddress: "mvc1qholder1", ChainName: "mvc", Height: 800, TxId: "spendtx-2", Time: 1700000400},
	}

	stats = &BackfillStats{}
	opts.Stats = stats
	if err := a.BackfillNft(opts); err != nil {
		t.Fatalf("ahead re-run: %v", err)
	}
	if stats.Items != 1 || stats.OwnerMoves != 1 || stats.Transfers != 1 {
		t.Fatalf("ahead re-run should apply the newer row: %+v", stats)
	}
	item2, _ := a.db.GetItem("item-2")
	if item2.OwnerAddress != "mvc1qholder1" || item2.TransferCount != 1 {
		t.Fatalf("upstream-newer item not applied: %+v", item2)
	}
	if cnt := countOwnerItems(t, a, "mvc1qholder1", "BootAlbum"); cnt != 2 {
		t.Fatalf("holder1 should hold item-1 + item-2: %d", cnt)
	}
	if cnt := countOwnerItems(t, a, "mvc1qholder2", "BootAlbum"); cnt != 0 {
		t.Fatalf("holder2 should hold nothing: %d", cnt)
	}
	trs := 0
	a.db.IterateItemTransfers("item-2", func(tr *ItemTransfer) bool { trs++; return true })
	if trs != 1 {
		t.Fatalf("item-2 history wrong: %d", trs)
	}

	// Rewind protection: the local record is live-ahead of the stale
	// upstream row, so a re-run must keep the local state.
	item2, _ = a.db.GetItem("item-2")
	item2.TransferCount = 5
	item2.OwnerAddress = "mvc1qliveowner"
	if err := a.db.DeleteOwnerItem("mvc1qholder1", "BootAlbum", "item-2"); err != nil {
		t.Fatalf("re-key: %v", err)
	}
	if err := a.db.PutOwnerItem("mvc1qliveowner", "BootAlbum", "item-2"); err != nil {
		t.Fatalf("re-key: %v", err)
	}
	if err := a.db.SaveItem(item2); err != nil {
		t.Fatalf("save: %v", err)
	}
	stats = &BackfillStats{}
	if err := a.BackfillNft(opts); err != nil {
		t.Fatalf("stale re-run: %v", err)
	}
	if stats.OwnerMoves != 0 {
		t.Fatalf("stale upstream must not move owners: %+v", stats)
	}
	item2, _ = a.db.GetItem("item-2")
	if item2.OwnerAddress != "mvc1qliveowner" || item2.TransferCount != 5 {
		t.Fatalf("stale upstream must not rewind live state: %+v", item2)
	}
	if cnt := countOwnerItems(t, a, "mvc1qliveowner", "BootAlbum"); cnt != 1 {
		t.Fatalf("live owner key must survive: %d", cnt)
	}
}

func countOwnerItems(t *testing.T, a *Aggregator, owner, collection string) int {
	t.Helper()
	n := 0
	a.db.IterateOwnerItems(owner, collection, func(cn, ip string) bool { n++; return true })
	return n
}

func TestUpstreamDecodeNoDataCode(t *testing.T) {
	// code 100 (no data found) must decode to an empty page, not an error.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":100,"message":"no data found.","data":[]}`))
	}))
	defer server.Close()
	client := NewUpstreamClient(server.URL, server.Client())
	rows, total, err := client.ListCollections(context.Background(), 0, 10)
	if err != nil || len(rows) != 0 || total != 0 {
		t.Fatalf("code-100 page should be empty-success: rows=%d total=%d err=%v", len(rows), total, err)
	}
}

func TestUpstreamDecodeFlatEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":1,"message":"ok","list":[{"collectionname":"Flat","pinid":"flatpin","totalnum":0,"address":"mvc1qx"}],"total":1}`))
	}))
	defer server.Close()
	client := NewUpstreamClient(server.URL, server.Client())
	rows, total, err := client.ListCollections(context.Background(), 0, 10)
	if err != nil || total != 1 || len(rows) != 1 || rows[0].CollectionName != "Flat" || rows[0].PinId != "flatpin" {
		t.Fatalf("flat envelope decode failed: total=%d rows=%+v err=%v", total, rows, err)
	}
}

func TestUpstreamPageSizeParam(t *testing.T) {
	var seen url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query()
		respondList(w, []upstreamCollection{}, 0)
	}))
	defer server.Close()
	client := NewUpstreamClient(server.URL, server.Client())
	if _, _, err := client.ListCollections(context.Background(), 42, 7); err != nil {
		t.Fatalf("list: %v", err)
	}
	if seen.Get("cursor") != "42" || seen.Get("size") != "7" {
		t.Fatalf("offset pagination params wrong: %v", seen)
	}
}
