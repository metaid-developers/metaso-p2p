package mrc721nft

import (
	"testing"
)

// Native collection search tests: the mrc721nft side of the IDBots search
// contract — keyword/issuer matching rules, row shape fields, offset cursor
// round-trip and the shared clamps.

func seedSearchAlbums(t *testing.T, a *Aggregator) {
	t.Helper()
	// Three collections from two issuers; metaId values derived.
	runPins(t, a,
		testPin("s1", "/nft/mrc721/Alpha Collection/collection_desc", "create",
			[]byte(`{"name":"Cool Pixels","desc":"pixel art","royaltyRate":5,"totalSupply":10}`), creator),
		testPin("s2", "/nft/mrc721/Beta Collection/collection_desc", "create",
			[]byte(`{"name":"Boring Pics","desc":"nothing here"}`), other),
		testPin("s3", "/nft/mrc721/Gamma Collection/collection_desc", "create",
			[]byte(`{"name":"Gamma"}`), creator),
		// A mint so Alpha's totalnum/mintedCount is non-zero.
		testPin("m1", "/nft/mrc721/Alpha Collection/one", "create", []byte(`"x"`), other),
	)
}

func TestSearchNativeCollectionsKeyword(t *testing.T) {
	a := newTestAggregator(t)
	seedSearchAlbums(t, a)

	// Keyword substring over name.
	rows, total, next, err := a.SearchNativeCollections("pixels", "", "", 20)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].CollectionName != "Alpha Collection" {
		t.Fatalf("keyword search wrong: total=%d rows=%+v", total, rows)
	}
	if rows[0].MintedCount != 1 || rows[0].Totalnum != 1 || rows[0].RoyaltyRate != 5 {
		t.Fatalf("row fields wrong: %+v", rows[0])
	}
	if rows[0].Pinid != "s1" {
		t.Fatalf("pinid wrong: %+v", rows[0])
	}

	// Keyword over desc; case-insensitive.
	rows, total, _, _ = a.SearchNativeCollections("NOTHING", "", "", 20)
	if total != 1 || rows[0].CollectionName != "Beta Collection" {
		t.Fatalf("desc keyword wrong: %+v", rows)
	}

	// Exact case-insensitive pinid alternative.
	rows, total, _, _ = a.SearchNativeCollections("S3", "", "", 20)
	if total != 1 || rows[0].CollectionName != "Gamma Collection" {
		t.Fatalf("pinid keyword wrong: %+v", rows)
	}

	// No match → empty page.
	rows, total, next, _ = a.SearchNativeCollections("zzz", "", "", 20)
	if total != 0 || len(rows) != 0 || next != "" {
		t.Fatalf("no-match wrong: %+v %+v", rows, next)
	}
}

func TestSearchNativeCollectionsIssuerAndPaging(t *testing.T) {
	a := newTestAggregator(t)
	seedSearchAlbums(t, a)

	// Issuer by createAddress (exact).
	rows, total, _, err := a.SearchNativeCollections("", creator, "", 20)
	if err != nil {
		t.Fatalf("issuer search: %v", err)
	}
	if total != 2 || rows[0].CollectionName != "Alpha Collection" || rows[1].CollectionName != "Gamma Collection" {
		t.Fatalf("issuer rows wrong: %+v", rows)
	}

	// Issuer by createMetaId (case-insensitive).
	rows, total, _, _ = a.SearchNativeCollections("", globalMetaIdByAddress(creator), "", 20)
	if total != 2 {
		t.Fatalf("metaId issuer wrong: %d", total)
	}

	// Conjunctive filters.
	_, total, _, _ = a.SearchNativeCollections("gamma", creator, "", 20)
	if total != 1 {
		t.Fatalf("conjunctive filters wrong: %d", total)
	}

	// Offset paging with the shared cursor format.
	rows, total, next, _ := a.SearchNativeCollections("", creator, "", 1)
	if total != 2 || len(rows) != 1 || next == "" {
		t.Fatalf("page 1 wrong: total=%d next=%q", total, next)
	}
	rows2, total2, next2, err := a.SearchNativeCollections("", creator, next, 1)
	if err != nil {
		t.Fatalf("page 2: %v", err)
	}
	if total2 != 2 || len(rows2) != 1 || rows2[0].CollectionName != "Gamma Collection" || next2 != "" {
		t.Fatalf("page 2 wrong: %+v next=%q", rows2, next2)
	}

	// Invalid cursor and empty filters are client errors.
	if _, _, _, err := a.SearchNativeCollections("", "", "!!!", 20); err == nil {
		t.Fatal("invalid cursor must error")
	}
	if _, _, _, err := a.SearchNativeCollections("", "", "", 20); err == nil {
		t.Fatal("empty filters must error")
	}
}

func TestNativeCollectionsIssued(t *testing.T) {
	a := newTestAggregator(t)
	seedSearchAlbums(t, a)

	rows, total, _, err := a.NativeCollectionsIssued(creator, "", 20)
	if err != nil {
		t.Fatalf("issued: %v", err)
	}
	if total != 2 {
		t.Fatalf("issued total wrong: %d", total)
	}
	if rows[0].CreateAddress != creator {
		t.Fatalf("issued rows wrong: %+v", rows)
	}

	// Empty address is a client error.
	if _, _, _, err := a.NativeCollectionsIssued("", "", 20); err == nil {
		t.Fatal("empty address must error")
	}
}
