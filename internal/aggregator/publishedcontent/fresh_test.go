package publishedcontent

import (
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func TestFresh_CursorStableUnderConcurrentInsert(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	baseTs := int64(1755000000)
	seed := func(fill byte, ts int64, content string) string {
		pinId := strings.Repeat(string(fill), 64) + "i0"
		if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
			PinId:       pinId,
			Path:        PathSimpleBuzz,
			Operation:   OperationCreate,
			ChainName:   "mvc",
			MetaId:      "meta-user",
			Address:     "addr-user",
			Timestamp:   ts,
			ContentType: "application/json",
			ContentBody: []byte(`{"content":"` + content + `"}`),
		})); err != nil {
			t.Fatalf("seed %s: %v", pinId, err)
		}
		return pinId
	}
	a := seed('a', baseTs, "one")
	b := seed('b', baseTs+10, "two")
	c := seed('c', baseTs+20, "three")

	page1, err := agg.Fresh(FreshParams{Size: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Records) != 2 || !page1.HasMore {
		t.Fatalf("page1 = %d records hasMore=%v, want 2/true", len(page1.Records), page1.HasMore)
	}
	if page1.Records[0].SourcePinId != c || page1.Records[1].SourcePinId != b {
		t.Fatalf("page1 order = [%s %s], want [c b]", page1.Records[0].SourcePinId, page1.Records[1].SourcePinId)
	}

	// A concurrent insert lands between page1 and page2: it must appear
	// exactly once on a fresh walk, never inside the pinned cursor window.
	d := seed('d', baseTs+30, "four")

	page2, err := agg.Fresh(FreshParams{Size: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Records) != 1 || page2.HasMore {
		t.Fatalf("page2 = %d records hasMore=%v, want 1/false", len(page2.Records), page2.HasMore)
	}
	if page2.Records[0].SourcePinId != a {
		t.Fatalf("page2 = %s, want a (d is newer than the pinned window)", page2.Records[0].SourcePinId)
	}

	// Walking from scratch sees all four exactly once, newest first.
	full, err := agg.Fresh(FreshParams{Size: 10})
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	want := []string{d, c, b, a}
	if len(full.Records) != len(want) {
		t.Fatalf("full = %d records, want %d", len(full.Records), len(want))
	}
	for i, rec := range full.Records {
		if rec.SourcePinId != want[i] {
			t.Errorf("full[%d] = %s, want %s", i, rec.SourcePinId, want[i])
		}
	}
}

func TestFresh_SinceInclusiveBoundary(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	baseTs := int64(1755000000)
	for _, s := range []struct {
		fill byte
		ts   int64
	}{
		{'a', baseTs - 1},
		{'b', baseTs},
		{'c', baseTs + 1},
	} {
		if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
			PinId:       strings.Repeat(string(s.fill), 64) + "i0",
			Path:        PathSimpleBuzz,
			Operation:   OperationCreate,
			ChainName:   "mvc",
			MetaId:      "meta-user",
			Address:     "addr-user",
			Timestamp:   s.ts,
			ContentType: "application/json",
			ContentBody: []byte(`{"content":"x"}`),
		})); err != nil {
			t.Fatalf("seed %c: %v", s.fill, err)
		}
	}

	page, err := agg.Fresh(FreshParams{Size: 10, SinceSec: baseTs})
	if err != nil {
		t.Fatalf("Fresh: %v", err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("records = %d, want 2 (inclusive since)", len(page.Records))
	}
	for _, rec := range page.Records {
		if metawebdoc.NormalizeUnixSeconds(rec.CreatedAt) < baseTs {
			t.Fatalf("since filter leaked record with createdAt %d", rec.CreatedAt)
		}
	}
}

func TestFresh_RebuildsIndexFromExistingRecords(t *testing.T) {
	dir := t.TempDir()

	// First boot: the index is built empty, then records arrive through the
	// normal write path.
	store := storage.NewPebbleStore(dir)
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}
	baseTs := int64(1755000000)
	for _, fill := range []byte{'a', 'b'} {
		if _, err := agg.HandleBlockPin(makeContentPin(contentPinOpts{
			PinId:       strings.Repeat(string(fill), 64) + "i0",
			Path:        PathSimpleNote,
			Operation:   OperationCreate,
			ChainName:   "mvc",
			MetaId:      "meta-user",
			Address:     "addr-user",
			Timestamp:   baseTs + int64(fill),
			ContentType: "application/json",
			ContentBody: []byte(`{"title":"n","content":"c"}`),
		})); err != nil {
			t.Fatalf("seed %c: %v", fill, err)
		}
	}
	store.Close()

	// Simulate a legacy store: records exist but the fresh index and its
	// state marker do not.
	store2 := storage.NewPebbleStore(dir)
	if err := store2.DeleteByPrefix(Namespace, []byte(keyFreshTime)); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if err := store2.Delete(Namespace, freshTimeIndexStateKey()); err != nil {
		t.Fatalf("drop state: %v", err)
	}
	agg2 := &Aggregator{}
	if err := agg2.Init(store2, cache.New(store2)); err != nil {
		t.Fatalf("rebuild Init: %v", err)
	}
	defer store2.Close()

	page, err := agg2.Fresh(FreshParams{Size: 10})
	if err != nil {
		t.Fatalf("Fresh after rebuild: %v", err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("records after rebuild = %d, want 2", len(page.Records))
	}
}
