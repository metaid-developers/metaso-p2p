package skillservice

import (
	"encoding/json"
	"testing"

	"github.com/metaid-developers/meta-socket/internal/aggregator"
	"github.com/metaid-developers/meta-socket/internal/cache"
	"github.com/metaid-developers/meta-socket/internal/storage"
)

// setupAggregator builds an isolated aggregator backed by a temp PebbleStore.
// Tests must Close() the returned store at the end; the helper returns it so
// callers can defer cleanup.
func setupAggregator(t *testing.T) (*Aggregator, *storage.PebbleStore) {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	cacheProvider := cache.New(store)
	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg, store
}

// mustMarshal is a JSON helper for crafting contentSummary bodies.
func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// makeServicePin constructs a /protocols/skill-service PIN for tests. The
// operation, originalId and timestamp are kept as explicit args so each test
// can dial them independently.
func makeServicePin(t *testing.T, opts servicePinOpts) *aggregator.PinInscription {
	t.Helper()
	if opts.Path == "" {
		opts.Path = PathSkillService
	}
	summary := ServiceContentSummary{
		ServiceName:    opts.ServiceName,
		DisplayName:    opts.DisplayName,
		Description:    opts.Description,
		ServiceIcon:    opts.ServiceIcon,
		ProviderSkill:  "zhuwei-fortune",
		Price:          opts.Price,
		Currency:       opts.Currency,
		PaymentChain:   opts.ChainName,
		SettlementKind: "native",
		OutputType:     "text",
		PaymentAddress: opts.PaymentAddress,
		Disabled:       opts.Disabled,
	}
	if opts.Currency == "" {
		summary.Currency = "SPACE"
	}
	if opts.Price == "" {
		summary.Price = "1"
	}
	if opts.PaymentAddress == "" {
		summary.PaymentAddress = "addr-" + opts.PinId
	}

	return &aggregator.PinInscription{
		Id:            opts.PinId,
		Path:          opts.Path,
		Operation:     opts.Operation,
		ContentBody:   mustMarshal(t, summary),
		ChainName:     opts.ChainName,
		CreateMetaId:  opts.ProviderMetaId,
		MetaId:        opts.ProviderMetaId,
		CreateAddress: "addr-prov-" + opts.ProviderMetaId,
		GlobalMetaId:  "idq1-" + opts.ProviderMetaId,
		Timestamp:     opts.Timestamp,
		OriginalId:    opts.OriginalId,
	}
}

type servicePinOpts struct {
	PinId          string
	Path           string
	Operation      string
	ChainName      string
	ProviderMetaId string
	OriginalId     string // empty for create
	Timestamp      int64
	Disabled       bool
	ServiceName    string
	DisplayName    string
	Description    string
	ServiceIcon    string
	Price          string
	Currency       string
	PaymentAddress string
}

// --- AC #1: a single create lands in the persisted view ---

func TestProcessServiceCreate_LandsInPebble(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	pin := makeServicePin(t, servicePinOpts{
		PinId:          "tx_create:i0",
		Operation:      OperationCreate,
		ChainName:      "mvc",
		ProviderMetaId: "provA",
		Timestamp:      1000,
		ServiceName:    "fortune",
		DisplayName:    "紫微斗数",
		Description:    "desc",
		ServiceIcon:    "icon.png",
	})
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	rec, err := agg.loadService("mvc", "tx_create:i0")
	if err != nil {
		t.Fatalf("loadService: %v", err)
	}
	if rec == nil {
		t.Fatal("expected record after create, got nil")
	}
	if rec.CurrentPinId != "tx_create:i0" || rec.SourceServicePinId != "tx_create:i0" {
		t.Errorf("identity wrong: current=%s source=%s", rec.CurrentPinId, rec.SourceServicePinId)
	}
	if rec.Operation != OperationCreate {
		t.Errorf("Operation: got %s want create", rec.Operation)
	}
	if rec.DisplayName != "紫微斗数" {
		t.Errorf("DisplayName: got %q", rec.DisplayName)
	}
	if rec.CreatedAt != 1000 || rec.UpdatedAt != 1000 {
		t.Errorf("timestamps: createdAt=%d updatedAt=%d", rec.CreatedAt, rec.UpdatedAt)
	}
}

// --- AC #2: modify on same chain folds onto the create ---

func TestProcessServiceModify_FoldsToSource(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	create := makeServicePin(t, servicePinOpts{
		PinId: "tx_create:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "紫微斗数 v1",
	})
	if _, err := agg.HandleBlockPin(create); err != nil {
		t.Fatalf("create: %v", err)
	}

	modify := makeServicePin(t, servicePinOpts{
		PinId: "tx_modify:i0", Operation: OperationModify,
		ChainName: "mvc", ProviderMetaId: "provA",
		OriginalId: "tx_create:i0", Timestamp: 2000,
		ServiceName: "fortune", DisplayName: "紫微斗数 v2",
		Description: "更详细",
	})
	if _, err := agg.HandleBlockPin(modify); err != nil {
		t.Fatalf("modify: %v", err)
	}

	all, _ := agg.listServicesByChain("mvc")
	if len(all) != 1 {
		t.Fatalf("expected 1 folded record, got %d", len(all))
	}
	rec := all[0]
	if rec.SourceServicePinId != "tx_create:i0" {
		t.Errorf("source: got %s", rec.SourceServicePinId)
	}
	if rec.CurrentPinId != "tx_modify:i0" {
		t.Errorf("current: got %s", rec.CurrentPinId)
	}
	if rec.DisplayName != "紫微斗数 v2" {
		t.Errorf("displayName not updated: %s", rec.DisplayName)
	}
	if rec.Description != "更详细" {
		t.Errorf("description not updated: %s", rec.Description)
	}
	if rec.CreatedAt != 1000 {
		t.Errorf("createdAt should stay 1000, got %d", rec.CreatedAt)
	}
	if rec.UpdatedAt != 2000 {
		t.Errorf("updatedAt should be 2000, got %d", rec.UpdatedAt)
	}
	if rec.Operation != OperationModify {
		t.Errorf("operation: got %s want modify", rec.Operation)
	}

	// pin_to_source index lets later lookups by the modify pin id resolve
	// back to the source record.
	resolved, err := agg.loadServiceByAnyPinId("mvc", "tx_modify:i0")
	if err != nil || resolved == nil {
		t.Fatalf("loadServiceByAnyPinId(modify): %v rec=%v", err, resolved)
	}
	if resolved.SourceServicePinId != "tx_create:i0" {
		t.Errorf("pin_to_source mapping wrong: %s", resolved.SourceServicePinId)
	}
}

// --- AC #3: revoke makes the service hidden by default ---

func TestProcessServiceRevoke_HidesByDefault(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "tx_create:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "紫微斗数",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "tx_revoke:i0", Operation: OperationRevoke,
		ChainName: "mvc", ProviderMetaId: "provA",
		OriginalId: "tx_create:i0", Timestamp: 3000,
		ServiceName: "fortune", DisplayName: "紫微斗数",
	})); err != nil {
		t.Fatal(err)
	}

	rec, _ := agg.loadService("mvc", "tx_create:i0")
	if rec == nil {
		t.Fatal("record should still exist after revoke (chain history is preserved)")
	}
	if rec.Operation != OperationRevoke {
		t.Errorf("operation: got %s want revoke", rec.Operation)
	}
	if rec.IsVisibleDefault() {
		t.Errorf("revoked record must be hidden from default list")
	}
	if rec.CurrentPinId != "tx_revoke:i0" {
		t.Errorf("currentPinId should advance to the revoke pin: got %s", rec.CurrentPinId)
	}
}

// --- AC #4: disabled=true hides the service even without revoke ---

func TestProcessServiceCreate_DisabledHidden(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "tx_create:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "停用中", Disabled: true,
	})); err != nil {
		t.Fatal(err)
	}

	rec, _ := agg.loadService("mvc", "tx_create:i0")
	if rec == nil || !rec.Disabled {
		t.Fatalf("disabled flag should propagate to the record: %+v", rec)
	}
	if rec.IsVisibleDefault() {
		t.Errorf("disabled record must be hidden from default list")
	}
}

// --- AC #5: same-chain invariant — cross-chain modify is dropped ---

func TestProcessServiceModify_CrossChainDropped(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	// create on MVC
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "tx_create:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "MVC create",
	})); err != nil {
		t.Fatal(err)
	}

	// modify on DOGE referencing the MVC create. v1 must not fold across
	// chains; the modify should leave the MVC record untouched and no
	// DOGE record should be created.
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "tx_modify:i0", Operation: OperationModify,
		ChainName: "doge", ProviderMetaId: "provA",
		OriginalId: "tx_create:i0", Timestamp: 2000,
		ServiceName: "fortune", DisplayName: "DOGE modify (rejected)",
	})); err != nil {
		t.Fatal(err)
	}

	mvc, _ := agg.loadService("mvc", "tx_create:i0")
	if mvc == nil {
		t.Fatal("MVC record disappeared")
	}
	if mvc.DisplayName != "MVC create" {
		t.Errorf("MVC record was mutated by cross-chain modify: %q", mvc.DisplayName)
	}
	if mvc.UpdatedAt != 1000 {
		t.Errorf("MVC updatedAt should not advance from cross-chain modify: %d", mvc.UpdatedAt)
	}

	doge, _ := agg.loadService("doge", "tx_create:i0")
	if doge != nil {
		t.Errorf("no DOGE record should exist: %+v", doge)
	}
}

// --- AC #6: originalId missing → path @pinId fallback (one hop, logged) ---

func TestProcessServiceModify_OriginalIdFallback(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "tx_create:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "v1",
	})); err != nil {
		t.Fatal(err)
	}

	// Construct a legacy-style modify: OriginalId empty, path carries
	// "@<sourcePinId>". The fallback must extract the source id and fold
	// the modify in.
	legacyModify := makeServicePin(t, servicePinOpts{
		PinId:          "tx_modify_legacy:i0",
		Path:           PathSkillService + "@tx_create:i0",
		Operation:      OperationModify,
		ChainName:      "mvc",
		ProviderMetaId: "provA",
		Timestamp:      2000,
		ServiceName:    "fortune",
		DisplayName:    "v2 via fallback",
	})
	legacyModify.OriginalId = ""

	if _, err := agg.HandleBlockPin(legacyModify); err != nil {
		t.Fatal(err)
	}

	rec, _ := agg.loadService("mvc", "tx_create:i0")
	if rec == nil {
		t.Fatal("record missing after fallback modify")
	}
	if rec.DisplayName != "v2 via fallback" {
		t.Errorf("fallback modify did not fold: %s", rec.DisplayName)
	}
}

// --- AC #7: malformed pins are skipped without panic ---

func TestProcessPin_MalformedSkipped(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	cases := []*aggregator.PinInscription{
		nil,
		{}, // empty path
		{Path: "/protocols/unknown", Id: "x"},
		{Path: PathSkillService, Id: "tx:i0", ChainName: "mvc", Operation: OperationCreate, ContentBody: []byte("not json")},
		{Path: PathSkillService, Id: "tx:i0", ChainName: "mvc", Operation: OperationCreate, ContentBody: []byte(`{"price":"1"}`)}, // no name/displayName
		{Path: PathSkillService, Id: "tx:i0", ChainName: "mvc", Operation: "weird"},                                                   // unknown op
		{Path: PathSkillService, Id: "tx_mod:i0", ChainName: "mvc", Operation: OperationModify, OriginalId: ""},                       // no originalId, no @ in path
	}
	for i, pin := range cases {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Errorf("case %d returned error: %v", i, err)
		}
	}

	all, err := agg.listAllServices()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 0 {
		t.Errorf("expected no records after malformed inputs, got %d", len(all))
	}
}

// --- AC #8: out-of-order modify (source unknown) is dropped, not stored ---

func TestProcessServiceModify_SourceMissingDropped(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "tx_mod:i0", Operation: OperationModify,
		ChainName: "mvc", ProviderMetaId: "provA",
		OriginalId: "tx_create_never_seen:i0", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "orphan",
	})); err != nil {
		t.Fatal(err)
	}

	all, _ := agg.listAllServices()
	if len(all) != 0 {
		t.Errorf("orphan modify should not create a record: got %d", len(all))
	}
}

// --- AC #9: idempotent duplicate create is a no-op ---

func TestProcessServiceCreate_DuplicateIgnored(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	first := makeServicePin(t, servicePinOpts{
		PinId: "tx_create:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "first",
	})
	dup := makeServicePin(t, servicePinOpts{
		PinId: "tx_create:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 2000,
		ServiceName: "fortune", DisplayName: "DUPLICATE (should be ignored)",
	})
	if _, err := agg.HandleBlockPin(first); err != nil {
		t.Fatal(err)
	}
	if _, err := agg.HandleBlockPin(dup); err != nil {
		t.Fatal(err)
	}

	rec, _ := agg.loadService("mvc", "tx_create:i0")
	if rec == nil || rec.DisplayName != "first" {
		t.Errorf("duplicate create overwrote the first: %+v", rec)
	}
	if rec.UpdatedAt != 1000 {
		t.Errorf("duplicate create advanced UpdatedAt: %d", rec.UpdatedAt)
	}
}

// --- AC #10: HandleMempoolPin reuses the same pipeline (no panics) ---

func TestHandleMempoolPin_SameAsBlock(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	pin := makeServicePin(t, servicePinOpts{
		PinId: "tx_mempool:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provM", Timestamp: 5000,
		ServiceName: "mempool-svc", DisplayName: "Pending publish",
	})
	if _, err := agg.HandleMempoolPin(pin); err != nil {
		t.Fatal(err)
	}
	rec, _ := agg.loadService("mvc", "tx_mempool:i0")
	if rec == nil {
		t.Fatal("mempool pin should also land in the persisted view")
	}
}

// --- AC #11: same-chain folding survives multiple modifies in order ---

func TestProcessServiceModify_MultipleVersionsKeepLatest(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "src:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "svc", DisplayName: "v1",
	})); err != nil {
		t.Fatal(err)
	}
	for i, ts := range []int64{2000, 3000, 4000} {
		if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
			PinId: pinIdFor(i + 2), Operation: OperationModify,
			ChainName: "mvc", ProviderMetaId: "provA",
			OriginalId: "src:i0", Timestamp: ts,
			ServiceName: "svc", DisplayName: displayFor(i + 2),
		})); err != nil {
			t.Fatal(err)
		}
	}

	all, _ := agg.listServicesByChain("mvc")
	if len(all) != 1 {
		t.Fatalf("expected single folded record, got %d", len(all))
	}
	rec := all[0]
	if rec.DisplayName != "v4" {
		t.Errorf("expected latest displayName=v4, got %s", rec.DisplayName)
	}
	if rec.UpdatedAt != 4000 {
		t.Errorf("UpdatedAt should follow latest modify: %d", rec.UpdatedAt)
	}
}

func pinIdFor(version int) string {
	return "mod_v" + intToStr(version) + ":i0"
}

func displayFor(version int) string {
	return "v" + intToStr(version)
}

func intToStr(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// --- AC #12: visibility flag matches spec wording exactly ---

func TestIsVisibleDefault_Cases(t *testing.T) {
	cases := []struct {
		name string
		rec  *ServiceRecord
		want bool
	}{
		{"nil", nil, false},
		{"create published", &ServiceRecord{Operation: OperationCreate, Status: StatusConfirmed}, true},
		{"create pending", &ServiceRecord{Operation: OperationCreate, Status: StatusPending}, true},
		{"modify published", &ServiceRecord{Operation: OperationModify, Status: StatusConfirmed}, true},
		{"revoked", &ServiceRecord{Operation: OperationRevoke, Status: StatusConfirmed}, false},
		{"disabled", &ServiceRecord{Operation: OperationCreate, Disabled: true}, false},
		{"unknown status", &ServiceRecord{Operation: OperationCreate, Status: 9}, false},
	}
	for _, tc := range cases {
		if got := tc.rec.IsVisibleDefault(); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
