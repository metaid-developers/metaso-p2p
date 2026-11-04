package mrc721nft

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Replay-pins tests: the upstream-state repair path — item re-hydrate (with
// collection ensure), collection re-hydrate, not-found reporting, and the
// never-rewind freshness rule.

func replayUpstream(t *testing.T) (*httptest.Server, *map[string]func() any) {
	t.Helper()
	views := map[string]func() any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload any
		switch r.URL.Path {
		case "/api/mrc721/item/info":
			f, ok := views["item:"+r.URL.Query().Get("pinId")]
			if !ok {
				payload = map[string]any{"code": 100, "message": "no data found.", "data": []any{}}
			} else {
				payload = map[string]any{"code": 1, "message": "ok", "data": f()}
			}
		case "/api/mrc721/collection/info":
			f, ok := views["collection:"+r.URL.Query().Get("pinId")]
			if !ok {
				payload = map[string]any{"code": 100, "message": "no data found.", "data": []any{}}
			} else {
				payload = map[string]any{"code": 1, "message": "ok", "data": f()}
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	return server, &views
}

func TestReplayPinsRepairsItem(t *testing.T) {
	a := newTestAggregator(t)
	server, views := replayUpstream(t)
	defer server.Close()

	// Upstream knows item rp-item-1 whose collection is missing locally.
	(*views)["collection:rp-coll-1"] = func() any {
		return map[string]any{
			"collectionname": "RpAlbum", "name": "Repaired", "pinid": "rp-coll-1",
			"address": creator, "createAddress": creator,
			"totalnum": 1, "genesisChainName": "mvc", "genesisHeight": 700, "genesisTx": "rptx",
		}
	}
	(*views)["item:rp-item-1"] = func() any {
		return map[string]any{
			"collectionPinId": "rp-coll-1", "collectionName": "RpAlbum",
			"itemPinId": "rp-item-1", "number": 1, "name": "Pinned",
			"ownerAddress": other, "output": "rpspend:0", "outputValue": 546,
			"transferCount": 1, "genesisChainName": "mvc", "genesisHeight": 701,
			"genesisTx": "rptx1", "createTime": 1700001000,
		}
	}

	results := a.ReplayPins(context.Background(), NewUpstreamClient(server.URL, server.Client()), []string{"rp-item-1"})
	if len(results) != 1 || !results[0].Applied || results[0].Chain != "mvc" {
		t.Fatalf("item replay wrong: %+v", results)
	}

	col, _ := a.db.GetCollectionByPinId("rp-coll-1")
	if col == nil || col.CollectionName != "RpAlbum" {
		t.Fatalf("collection not ensured: %+v", col)
	}
	item, _ := a.db.GetItem("rp-item-1")
	if item == nil || item.OwnerAddress != other || item.CollectionName != "RpAlbum" || item.TransferCount != 1 {
		t.Fatalf("item not repaired: %+v", item)
	}
}

func TestReplayPinsRepairsCollection(t *testing.T) {
	a := newTestAggregator(t)
	server, views := replayUpstream(t)
	defer server.Close()

	(*views)["collection:rp-coll-2"] = func() any {
		return map[string]any{
			"collectionname": "RpColl", "name": "Coll Repair", "pinid": "rp-coll-2",
			"address": creator, "createAddress": creator, "totalnum": 0,
			"genesisChainName": "mvc", "genesisHeight": 800, "genesisTx": "rptxc",
		}
	}

	results := a.ReplayPins(context.Background(), NewUpstreamClient(server.URL, server.Client()), []string{"rp-coll-2"})
	if len(results) != 1 || !results[0].Applied || results[0].Chain != "mvc" {
		t.Fatalf("collection replay wrong: %+v", results)
	}
	col, _ := a.db.GetCollectionByName("RpColl")
	if col == nil || col.PinId != "rp-coll-2" {
		t.Fatalf("collection not repaired: %+v", col)
	}
}

func TestReplayPinsNotFoundAndFreshness(t *testing.T) {
	a := newTestAggregator(t)
	server, views := replayUpstream(t)
	defer server.Close()

	// Unknown id.
	results := a.ReplayPins(context.Background(), NewUpstreamClient(server.URL, server.Client()), []string{"missing-pin"})
	if len(results) != 1 || results[0].Applied || results[0].Reason != "pin not found" {
		t.Fatalf("not-found wrong: %+v", results)
	}

	// Freshness: a live-ahead local item must NOT be rewound by a stale
	// upstream row (transferCount strictly greater rule).
	runPins(t, a,
		testPin("lc1", "/nft/mrc721/LiveAlbum/collection_desc", "create", []byte(`{"name":"L"}`), creator),
		testPin("li1", "/nft/mrc721/LiveAlbum/a", "create", []byte(`"x"`), creator),
	)
	item, _ := a.db.GetItem("li1")
	item.OwnerAddress = "mvc1qlive"
	item.TransferCount = 3
	if err := a.db.SaveItem(item); err != nil {
		t.Fatalf("seed live state: %v", err)
	}
	(*views)["item:li1"] = func() any {
		return map[string]any{
			"collectionPinId": "lc1", "collectionName": "LiveAlbum",
			"itemPinId": "li1", "number": 1, "ownerAddress": creator,
			"transferCount": 1, "genesisChainName": "mvc", "genesisHeight": 100,
		}
	}
	results = a.ReplayPins(context.Background(), NewUpstreamClient(server.URL, server.Client()), []string{"li1"})
	if len(results) != 1 || !results[0].Applied {
		t.Fatalf("stale replay should still report applied (desc fill): %+v", results)
	}
	item, _ = a.db.GetItem("li1")
	if item.OwnerAddress != "mvc1qlive" || item.TransferCount != 3 {
		t.Fatalf("stale upstream must not rewind live state: %+v", item)
	}
}
