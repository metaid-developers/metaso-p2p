package mrc721

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

// collectionPageListServer serves the manapi-compatible
// /api/mrc721/collection/pageList with cursor paging.
func collectionPageListServer(t *testing.T, pages [][]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mrc721/collection/pageList" {
			t.Errorf("request path: got %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cursor := r.URL.Query().Get("cursor")
		pageIndex := 0
		if cursor != "" {
			parsed, err := strconv.Atoi(cursor)
			if err != nil {
				t.Errorf("cursor: %v", err)
			}
			pageIndex = parsed
		}
		w.Header().Set("Content-Type", "application/json")
		nextCursor := ""
		if pageIndex+1 < len(pages) {
			nextCursor = strconv.Itoa(pageIndex + 1)
		}
		list := [][]map[string]any(nil)
		if pageIndex < len(pages) {
			list = append(list, pages[pageIndex])
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code": 1,
			"data": map[string]any{
				"list":       list[0],
				"nextCursor": nextCursor,
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
}

func collectionRowFor(name, pinId, address string) map[string]any {
	return map[string]any{
		"collectionName":   name,
		"name":             name + " display",
		"desc":             "desc of " + name,
		"totalSupply":      100,
		"totalnum":         100,
		"royaltyRate":      5,
		"pinid":            pinId,
		"createAddress":    address,
		"createMetaId":     hexOf("meta-" + name),
		"createTime":       1793868000,
		"cover":            "metafile://" + hexOf("cover-"+name),
		"genesisChainName": "mvc",
		"mintedCount":      10,
	}
}

func TestCollectionClientDecodeAndSyncSweep(t *testing.T) {
	agg := newTestAggregator(t)

	page1 := []map[string]any{
		collectionRowFor("pixel-cats", hexOf("c1")+"i0", testSeller1),
		collectionRowFor("meta-dogs", hexOf("c2")+"i0", testSeller2),
	}
	// Second page: snake_case duplicate spellings tolerated.
	page2 := []map[string]any{
		{
			"collectionname": "art-deco",
			"desc":           "geometric",
			"pinid":          hexOf("c3") + "i0",
			"createaddress":  testSeller1,
			"total_supply":   5,
		},
	}
	server := collectionPageListServer(t, [][]map[string]any{page1, page2})
	defer server.Close()

	client := NewCollectionClient(server.URL, server.Client())
	stats, err := agg.SyncCollections(context.Background(), client, 2)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if stats.Pages != 2 || stats.Fetched != 3 || stats.Upserted != 3 {
		t.Fatalf("stats = %+v", stats)
	}

	page, err := agg.SearchCollections("pixel", "", "", 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if page.Total != 1 || page.List[0].CollectionName != "pixel-cats" {
		t.Fatalf("search result: %+v", page)
	}

	// Snake-case row landed in the read model with its fields folded.
	issued, err := agg.CollectionsIssuedByAddress(testSeller1, "", 100)
	if err != nil {
		t.Fatalf("issued: %v", err)
	}
	if issued.Total != 2 {
		t.Fatalf("issued for seller1: %+v", issued)
	}
	foundArt := false
	for _, rec := range issued.List {
		if rec.CollectionName == "art-deco" {
			foundArt = true
			if rec.TotalSupply != 5 || rec.CreateAddress != testSeller1 {
				t.Fatalf("snake-case fold: %+v", rec)
			}
		}
	}
	if !foundArt {
		t.Fatal("art-deco row missing from the read model")
	}
}

// A second sweep with unchanged rows keeps the original positions
// (no seq churn); changed rows refresh.
func TestCollectionSyncUpsertDeduplicates(t *testing.T) {
	agg := newTestAggregator(t)

	row := collectionRowFor("pixel-cats", hexOf("c1")+"i0", testSeller1)
	server := collectionPageListServer(t, [][]map[string]any{{row}})
	defer server.Close()
	client := NewCollectionClient(server.URL, server.Client())

	if _, err := agg.SyncCollections(context.Background(), client, 100); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	first, err := agg.loadCollection(hexOf("c1") + "i0")
	if err != nil || first == nil {
		t.Fatalf("load: %v %v", first, err)
	}

	stats, err := agg.SyncCollections(context.Background(), client, 100)
	if err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if stats.Upserted != 0 || stats.Skipped != 1 {
		t.Fatalf("unchanged row must be skipped: %+v", stats)
	}
	second, _ := agg.loadCollection(hexOf("c1") + "i0")
	if second.Seq != first.Seq {
		t.Fatalf("unchanged row changed seq: %d -> %d", first.Seq, second.Seq)
	}

	// Changed row refreshes.
	row["mintedCount"] = 11
	if _, err := agg.SyncCollections(context.Background(), client, 100); err != nil {
		t.Fatalf("sync 3: %v", err)
	}
	third, _ := agg.loadCollection(hexOf("c1") + "i0")
	if third.MintedCount != 11 || third.Seq == first.Seq {
		t.Fatalf("changed row did not refresh: %+v", third)
	}
}

func TestCollectionSearchPagination(t *testing.T) {
	agg := newTestAggregator(t)
	for i := 0; i < 5; i++ {
		row := collectionRowFor("coll-"+strconv.Itoa(i), hexOf("p")+strconv.Itoa(i)+"i0", testSeller1)
		if upserted, err := agg.upsertCollection(&CollectionRecord{
			CollectionName: row["collectionName"].(string),
			PinId:          row["pinid"].(string),
			CreateAddress:  testSeller1,
			Desc:           "batch " + strconv.Itoa(i),
		}); err != nil || !upserted {
			t.Fatalf("upsert %d: %v %v", i, upserted, err)
		}
	}

	// Keyword "batch" matches all five; walk two pages of two.
	page1, err := agg.SearchCollections("batch", "", "", 2)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if page1.Total != 5 || len(page1.List) != 2 || page1.NextCursor == "" {
		t.Fatalf("page1: %+v", page1)
	}
	page2, err := agg.SearchCollections("batch", "", page1.NextCursor, 2)
	if err != nil {
		t.Fatalf("search page2: %v", err)
	}
	if page2.Total != 5 || len(page2.List) != 2 {
		t.Fatalf("page2: %+v", page2)
	}
	page3, err := agg.SearchCollections("batch", "", page2.NextCursor, 2)
	if err != nil {
		t.Fatalf("search page3: %v", err)
	}
	if len(page3.List) != 1 || page3.NextCursor != "" {
		t.Fatalf("page3: %+v", page3)
	}
	distinct := map[string]bool{}
	for _, page := range []*CollectionPage{page1, page2, page3} {
		for _, rec := range page.List {
			distinct[rec.PinId] = true
		}
	}
	if len(distinct) != 5 {
		t.Fatalf("walked %d distinct rows, want 5", len(distinct))
	}
}

func TestCollectionPageDecodeEnvelopeVariants(t *testing.T) {
	// Flat envelope (no data wrapper).
	raw := []byte(`{"code":0,"list":[{"collectionName":"flat","pinid":"` + hexOf("flat") + `i0"}],"cursor":""}`)
	page, err := decodeCollectionPage(raw)
	if err != nil {
		t.Fatalf("decode flat: %v", err)
	}
	if len(page.Records) != 1 || page.Records[0].CollectionName != "flat" {
		t.Fatalf("flat page: %+v", page)
	}

	// Error code surfaces.
	raw = []byte(`{"code":500,"message":"boom"}`)
	if _, err := decodeCollectionPage(raw); err == nil {
		t.Fatal("error code not surfaced")
	}

	// A malformed row is skipped, not fatal.
	raw = []byte(`{"code":1,"data":{"list":["garbage",{"pinid":"` + hexOf("ok") + `i0"}],"nextCursor":""}}`)
	page, err = decodeCollectionPage(raw)
	if err != nil {
		t.Fatalf("decode mixed: %v", err)
	}
	if len(page.Records) != 1 {
		t.Fatalf("mixed page: %+v", page)
	}
}

func TestRunCollectionSyncLoopStopsAndSyncs(t *testing.T) {
	agg := newTestAggregator(t)
	sweeps := make(chan int, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 1,
			"data": map[string]any{
				"list": []map[string]any{collectionRowFor("loop-coll", hexOf("loop")+"i0", testSeller1)},
			},
		})
		sweeps <- 1
	}))
	defer server.Close()

	stop := make(chan struct{})
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		RunCollectionSyncLoop(ctx, agg, NewCollectionClient(server.URL, server.Client()), 20*time.Millisecond, 500*time.Millisecond, 100, stop, nil)
		close(done)
	}()

	select {
	case <-sweeps:
	case <-time.After(2 * time.Second):
		t.Fatal("initial sweep never ran")
	}
	select {
	case <-sweeps:
	case <-time.After(2 * time.Second):
		t.Fatal("tick sweep never ran")
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop")
	}
	page, err := agg.SearchCollections("loop", "", "", 0)
	if err != nil || page.Total != 1 {
		t.Fatalf("loop collection not indexed: %+v err=%v", page, err)
	}
}

// Regression (found by live smoke): NewCollectionClient with a nil http
// client must default to http.DefaultClient instead of panicking on use —
// the daemon and the backfill cmd both construct it that way.
func TestCollectionClientNilHTTPClientDefaults(t *testing.T) {
	agg := newTestAggregator(t)
	server := collectionPageListServer(t, [][]map[string]any{{
		collectionRowFor("pixel-cats", hexOf("nilc")+"i0", testSeller1),
	}})
	defer server.Close()

	client := NewCollectionClient(server.URL, nil)
	stats, err := agg.SyncCollections(context.Background(), client, 100)
	if err != nil {
		t.Fatalf("sync with default client: %v", err)
	}
	if stats.Upserted != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	page, err := agg.SearchCollections("pixel", "", "", 0)
	if err != nil || page.Total != 1 {
		t.Fatalf("row not served: %+v err=%v", page, err)
	}
}
