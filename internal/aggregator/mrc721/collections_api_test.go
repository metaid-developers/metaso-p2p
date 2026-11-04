package mrc721

import (
	"testing"
)

func seedCollections(t *testing.T, agg *Aggregator) {
	t.Helper()
	rows := []*CollectionRecord{
		{CollectionName: "pixel-cats", Name: "Pixel Cats", Desc: "retro pixel cats on MVC", PinId: hexOf("coll-cats") + "i0", CreateAddress: testSeller1, CreateMetaId: hexOf("meta-cats"), TotalSupply: 100, Totalnum: 100, RoyaltyRate: 5, CreateTime: 1793868000, MintedCount: 42},
		{CollectionName: "meta-dogs", Name: "Meta Dogs", Desc: "loyal dogs", PinId: hexOf("coll-dogs") + "i0", CreateAddress: testSeller2, CreateMetaId: hexOf("meta-dogs"), TotalSupply: 200, MintedCount: 7},
		{CollectionName: "art-deco", Name: "Art Deco", Desc: "geometric art", PinId: hexOf("coll-art") + "i0", CreateAddress: testSeller1, CreateMetaId: hexOf("meta-art")},
	}
	for _, row := range rows {
		if upserted, err := agg.upsertCollection(row); err != nil || !upserted {
			t.Fatalf("upsert %s: upserted=%v err=%v", row.CollectionName, upserted, err)
		}
	}
}

func TestCollectionSearchEndpointKeywordAndIssuer(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)
	seedCollections(t, agg)

	// Keyword substring over name (case-insensitive).
	_, env := doMrc721Request(t, router, "/api/mrc721/collection/search?keyword=PIXEL")
	data := decodeCollectionList(t, env)
	if env.Code != 1 || data.Total != 1 || data.List[0]["collectionName"] != "pixel-cats" {
		t.Fatalf("keyword PIXEL: env=%+v data=%+v", env, data)
	}

	// Keyword over desc.
	_, env = doMrc721Request(t, router, "/api/mrc721/collection/search?keyword=loyal")
	data = decodeCollectionList(t, env)
	if data.Total != 1 || data.List[0]["collectionName"] != "meta-dogs" {
		t.Fatalf("keyword loyal: %+v", data)
	}

	// Exact pinid keyword.
	_, env = doMrc721Request(t, router, "/api/mrc721/collection/search?keyword="+hexOf("coll-art")+"i0")
	data = decodeCollectionList(t, env)
	if data.Total != 1 || data.List[0]["collectionName"] != "art-deco" {
		t.Fatalf("keyword pinid: %+v", data)
	}

	// Issuer by createAddress.
	_, env = doMrc721Request(t, router, "/api/mrc721/collection/search?issuer="+testSeller1)
	data = decodeCollectionList(t, env)
	if data.Total != 2 {
		t.Fatalf("issuer address: %+v", data)
	}

	// Issuer by createMetaId.
	_, env = doMrc721Request(t, router, "/api/mrc721/collection/search?issuer="+hexOf("meta-dogs"))
	data = decodeCollectionList(t, env)
	if data.Total != 1 || data.List[0]["collectionName"] != "meta-dogs" {
		t.Fatalf("issuer metaid: %+v", data)
	}

	// Combined filters intersect.
	_, env = doMrc721Request(t, router, "/api/mrc721/collection/search?keyword=deco&issuer="+testSeller1)
	data = decodeCollectionList(t, env)
	if data.Total != 1 || data.List[0]["collectionName"] != "art-deco" {
		t.Fatalf("combined: %+v", data)
	}

	// Neither keyword nor issuer is invalid.
	_, env = doMrc721Request(t, router, "/api/mrc721/collection/search")
	if env.Code != 40000 {
		t.Fatalf("empty search: %+v", env)
	}

	// Row shape mirrors the MRC-721 collection summary.
	_, env = doMrc721Request(t, router, "/api/mrc721/collection/search?keyword=pixel")
	data = decodeCollectionList(t, env)
	row := data.List[0]
	if num(row, "totalSupply") != 100 || num(row, "totalnum") != 100 || num(row, "royaltyRate") != 5 ||
		num(row, "mintedCount") != 42 || num(row, "createTime") != 1793868000 ||
		str(row, "pinid") != hexOf("coll-cats")+"i0" || str(row, "createAddress") != testSeller1 ||
		str(row, "createMetaId") != hexOf("meta-cats") {
		t.Fatalf("collection row shape: %v", row)
	}
}

func TestCollectionIssuedEndpoint(t *testing.T) {
	agg := newTestAggregator(t)
	router := newTestRouter(agg)
	seedCollections(t, agg)

	_, env := doMrc721Request(t, router, "/api/mrc721/address/collections-issued?address="+testSeller1)
	if env.Code != 1 {
		t.Fatalf("envelope: %+v", env)
	}
	data := decodeCollectionList(t, env)
	if data.Total != 2 || len(data.List) != 2 {
		t.Fatalf("issued: %+v", data)
	}
	for _, row := range data.List {
		if str(row, "createAddress") != testSeller1 {
			t.Fatalf("row from another issuer: %v", row)
		}
	}

	// Pagination over the issuer view.
	_, env = doMrc721Request(t, router, "/api/mrc721/address/collections-issued?address="+testSeller1+"&size=1")
	data = decodeCollectionList(t, env)
	if data.Total != 2 || len(data.List) != 1 || data.Cursor == "" {
		t.Fatalf("issued page 1: %+v", data)
	}
	_, env = doMrc721Request(t, router, "/api/mrc721/address/collections-issued?address="+testSeller1+"&size=1&cursor="+data.Cursor)
	data = decodeCollectionList(t, env)
	if len(data.List) != 1 || data.Cursor != "" {
		t.Fatalf("issued page 2: %+v", data)
	}

	// Missing address.
	_, env = doMrc721Request(t, router, "/api/mrc721/address/collections-issued")
	if env.Code != 40000 {
		t.Fatalf("missing address: %+v", env)
	}
}
