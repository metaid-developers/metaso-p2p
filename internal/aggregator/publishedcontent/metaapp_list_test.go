package publishedcontent

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func mustProcessMetaApp(t *testing.T, agg *Aggregator, pinId string, ts int64, payload string) {
	t.Helper()
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        pinId,
		Path:         PathMetaApp,
		Operation:    OperationCreate,
		Timestamp:    ts,
		ContentType:  "application/json",
		ContentBody:  []byte(payload),
		GlobalMetaId: "gid-" + pinId,
		MetaId:       "meta-" + pinId,
		Address:      "addr-" + pinId,
	}))
}

func TestMetaAppListDefaultOrdering(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMetaApp(t, agg, "app-1:i0", 1000, `{"title":"App One"}`)
	mustProcessMetaApp(t, agg, "app-2:i0", 3000, `{"title":"App Two"}`)
	mustProcessMetaApp(t, agg, "app-3:i0", 2000, `{"title":"App Three"}`)

	result, err := agg.ListMetaApps(MetaAppListParams{})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("len: got %d want 3", len(result.Items))
	}
	want := []string{"app-2:i0", "app-3:i0", "app-1:i0"}
	for i, item := range result.Items {
		if item.PinID != want[i] {
			t.Fatalf("item[%d].PinID: got %q want %q", i, item.PinID, want[i])
		}
		if item.IndexFile != "index.html" {
			t.Fatalf("item[%d].IndexFile: got %q want index.html", i, item.IndexFile)
		}
	}
}

func TestMetaAppListFieldNameVariants(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMetaApp(t, agg, "app-variant:i0", 1000, `{"name":"Variant App","description":"desc text","appname":"variant","forkedFrom":"parent:i0","disabled":"true","tags":["a",1]}`)

	result, err := agg.ListMetaApps(MetaAppListParams{IncludeDisabled: true})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("len: got %d want 1", len(result.Items))
	}
	item := result.Items[0]
	if item.Title != "Variant App" || item.Intro != "desc text" || item.AppName != "variant" {
		t.Fatalf("normalised fields: %+v", item)
	}
	if item.ForkedFrom != "parent:i0" {
		t.Fatalf("ForkedFrom: got %q", item.ForkedFrom)
	}
	if !item.Disabled {
		t.Fatal("Disabled should be true for string \"true\"")
	}
	if len(item.Tags) != 2 || item.Tags[0] != "a" || item.Tags[1] != "1" {
		t.Fatalf("Tags: %+v", item.Tags)
	}
}

func TestMetaAppListKeywordScoringAndAndSemantics(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	// B is newer but only hits the token in intro; A hits in title and must rank first.
	mustProcessMetaApp(t, agg, "app-a:i0", 1000, `{"title":"番茄钟工具","intro":"专注计时"}`)
	mustProcessMetaApp(t, agg, "app-b:i0", 2000, `{"title":"日历","intro":"一个番茄钟应用"}`)

	result, err := agg.ListMetaApps(MetaAppListParams{Keyword: "番茄钟"})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len: got %d want 2", len(result.Items))
	}
	if result.Items[0].PinID != "app-a:i0" {
		t.Fatalf("title hit should outrank intro hit: %+v", result.Items)
	}

	// AND semantics: both tokens must match; B lacks 工具 everywhere.
	result, err = agg.ListMetaApps(MetaAppListParams{Keyword: "番茄钟 工具"})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].PinID != "app-a:i0" {
		t.Fatalf("AND semantics: %+v", result.Items)
	}
}

func TestMetaAppListFilters(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMetaApp(t, agg, "app-game:i0", 1000, `{"title":"Game","tags":["game","puzzle"],"runtime":"browser/android"}`)
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "app-alice:i0",
		Path:         PathMetaApp,
		Timestamp:    2000,
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"Alice App","tags":["tool"],"runtime":"ios"}`),
		GlobalMetaId: "gid-alice",
		MetaId:       "meta-alice",
		Address:      "Addr-Alice",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "app-opcat:i0",
		Path:        PathMetaApp,
		ChainName:   "opcat",
		Timestamp:   3000,
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"Opcat App"}`),
	}))

	assertLen := func(params MetaAppListParams, want int) {
		t.Helper()
		result, err := agg.ListMetaApps(params)
		if err != nil {
			t.Fatalf("ListMetaApps(%+v): %v", params, err)
		}
		if len(result.Items) != want {
			t.Fatalf("ListMetaApps(%+v): got %d items want %d (%+v)", params, len(result.Items), want, result.Items)
		}
	}

	assertLen(MetaAppListParams{Tag: "game"}, 1)
	assertLen(MetaAppListParams{Tag: "tool,game"}, 2)
	assertLen(MetaAppListParams{Tag: "missing"}, 0)
	assertLen(MetaAppListParams{Since: 2000, Until: 3000}, 2)
	assertLen(MetaAppListParams{Since: 3001}, 0)
	assertLen(MetaAppListParams{Publisher: "gid-alice"}, 1)
	assertLen(MetaAppListParams{Publisher: "addr-alice"}, 1) // EqualFold on address
	assertLen(MetaAppListParams{Runtime: "browser"}, 1)     // contains match on browser/android
	assertLen(MetaAppListParams{Runtime: "ios"}, 1)
	assertLen(MetaAppListParams{ChainName: "opcat"}, 1)
	assertLen(MetaAppListParams{ChainName: "mvc"}, 2)
}

func TestMetaAppListDisabledAndRevoke(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMetaApp(t, agg, "app-disabled:i0", 1000, `{"title":"Disabled App","disabled":true}`)
	mustProcessMetaApp(t, agg, "app-live:i0", 2000, `{"title":"Live App"}`)

	result, err := agg.ListMetaApps(MetaAppListParams{})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].PinID != "app-live:i0" {
		t.Fatalf("disabled should be excluded by default: %+v", result.Items)
	}

	result, err = agg.ListMetaApps(MetaAppListParams{IncludeDisabled: true})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("includeDisabled should return both: %+v", result.Items)
	}

	// Revoke the live app: it must disappear from the list even with includeDisabled.
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:     "app-live-revoke:i0",
		Path:      PathMetaApp + "@app-live:i0",
		Operation: OperationRevoke,
		Timestamp: 3000,
	}))
	result, err = agg.ListMetaApps(MetaAppListParams{IncludeDisabled: true})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].PinID != "app-disabled:i0" {
		t.Fatalf("revoked app should be gone: %+v", result.Items)
	}
}

func TestMetaAppListModifyCollapsesToLatest(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMetaApp(t, agg, "app-1:i0", 1000, `{"title":"Old Title"}`)
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "app-1-b:i0",
		Path:         PathMetaApp + "@app-1:i0",
		Operation:    OperationModify,
		Timestamp:    2000,
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"New Title"}`),
		GlobalMetaId: "gid-app-1:i0",
		MetaId:       "meta-app-1:i0",
		Address:      "addr-app-1:i0",
	}))

	result, err := agg.ListMetaApps(MetaAppListParams{})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("modify should collapse to one item: %+v", result.Items)
	}
	item := result.Items[0]
	if item.PinID != "app-1-b:i0" || item.SourcePinID != "app-1:i0" {
		t.Fatalf("version fields: %+v", item)
	}
	if item.Title != "New Title" || item.UpdatedAt != 2000 || item.CreatedAt != 1000 {
		t.Fatalf("payload/timestamps: %+v", item)
	}
}

func TestMetaAppListCursorPagination(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMetaApp(t, agg, "app-1:i0", 1000, `{"title":"A1"}`)
	mustProcessMetaApp(t, agg, "app-2:i0", 2000, `{"title":"A2"}`)
	mustProcessMetaApp(t, agg, "app-3:i0", 3000, `{"title":"A3"}`)

	page1, err := agg.ListMetaApps(MetaAppListParams{Size: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Items) != 2 || !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("page1: %+v", page1)
	}
	page2, err := agg.ListMetaApps(MetaAppListParams{Size: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Items) != 1 || page2.HasMore {
		t.Fatalf("page2: %+v", page2)
	}
	if page1.Items[0].PinID != "app-3:i0" || page2.Items[0].PinID != "app-1:i0" {
		t.Fatalf("ordering across pages: %+v / %+v", page1.Items, page2.Items)
	}

	if _, err := agg.ListMetaApps(MetaAppListParams{Cursor: "!!!bad"}); err == nil || !strings.Contains(err.Error(), "invalid cursor") {
		t.Fatalf("bad cursor should error: %v", err)
	}
}

func TestMetaAppDetailResolvesAnyVersion(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMetaApp(t, agg, "app-1:i0", 1000, `{"title":"Old","prompt":"make it"}`)
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "app-1-b:i0",
		Path:         PathMetaApp + "@app-1:i0",
		Operation:    OperationModify,
		Timestamp:    2000,
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"New","prompt":"make it better"}`),
		GlobalMetaId: "gid-app-1:i0",
		MetaId:       "meta-app-1:i0",
		Address:      "addr-app-1:i0",
	}))

	// By source pinId, by current pinId, without chainName.
	for _, pinID := range []string{"app-1:i0", "app-1-b:i0"} {
		detail, err := agg.MetaAppDetail(pinID, "")
		if err != nil {
			t.Fatalf("MetaAppDetail(%s): %v", pinID, err)
		}
		if detail == nil {
			t.Fatalf("MetaAppDetail(%s): not found", pinID)
		}
		if detail.PinID != "app-1-b:i0" || detail.Title != "New" || detail.Prompt != "make it better" {
			t.Fatalf("MetaAppDetail(%s): %+v", pinID, detail)
		}
		if detail.Payload["title"] != "New" {
			t.Fatalf("raw payload missing: %+v", detail.Payload)
		}
	}

	missing, err := agg.MetaAppDetail("nope:i0", "")
	if err != nil || missing != nil {
		t.Fatalf("missing detail: %v %+v", err, missing)
	}
}

func TestMetaAppForksClustersAcrossParentVersions(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcessMetaApp(t, agg, "parent:i0", 1000, `{"title":"Parent"}`)
	mustProcessMetaApp(t, agg, "child-1:i0", 2000, `{"title":"Child 1","forkedfrom":"parent:i0"}`)
	// Parent gets a new version; child-2 references the newer version pinId.
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "parent-b:i0",
		Path:         PathMetaApp + "@parent:i0",
		Operation:    OperationModify,
		Timestamp:    3000,
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"Parent v2"}`),
		GlobalMetaId: "gid-parent:i0",
		MetaId:       "meta-parent:i0",
		Address:      "addr-parent:i0",
	}))
	mustProcessMetaApp(t, agg, "child-2:i0", 4000, `{"title":"Child 2","forkedfrom":"parent-b:i0"}`)
	mustProcessMetaApp(t, agg, "unrelated:i0", 5000, `{"title":"Unrelated"}`)

	for _, queryPin := range []string{"parent:i0", "parent-b:i0"} {
		result, found, err := agg.ListMetaAppForks(queryPin, "", 20, "")
		if err != nil {
			t.Fatalf("ListMetaAppForks(%s): %v", queryPin, err)
		}
		if !found {
			t.Fatalf("ListMetaAppForks(%s): parent not found", queryPin)
		}
		if len(result.Items) != 2 {
			t.Fatalf("ListMetaAppForks(%s): got %d items want 2 (%+v)", queryPin, len(result.Items), result.Items)
		}
		// createdAt desc
		if result.Items[0].PinID != "child-2:i0" || result.Items[1].PinID != "child-1:i0" {
			t.Fatalf("fork order: %+v", result.Items)
		}
	}

	empty, found, err := agg.ListMetaAppForks("unrelated:i0", "", 20, "")
	if err != nil || !found || len(empty.Items) != 0 {
		t.Fatalf("genesis app should have no forks: %v %v %+v", err, found, empty)
	}

	if _, found, err := agg.ListMetaAppForks("nope:i0", "", 20, ""); err != nil || found {
		t.Fatalf("missing parent: %v %v", err, found)
	}
}

func TestMetaAppTimeIndexBackfillOnInit(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()

	// Simulate a record indexed before the by_time index existed: write the
	// record JSON directly, bypassing saveRecord's index maintenance.
	rec := Record{
		SourcePinId:  "legacy-app:i0",
		CurrentPinId: "legacy-app:i0",
		ChainName:    "mvc",
		ProtocolPath: PathMetaApp,
		Operation:    OperationCreate,
		ContentType:  "application/json",
		PayloadJSON:  map[string]any{"title": "Legacy App"},
		CreatedAt:    1000,
		UpdatedAt:    1000,
	}
	raw, err := json.Marshal(&rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := store.Set(Namespace, recordKey(rec.ChainName, rec.ProtocolPath, rec.SourcePinId), raw); err != nil {
		t.Fatalf("store.Set: %v", err)
	}

	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("Init: %v", err)
	}

	result, err := agg.ListMetaApps(MetaAppListParams{})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].PinID != "legacy-app:i0" {
		t.Fatalf("backfilled record should be listed: %+v", result.Items)
	}
}
