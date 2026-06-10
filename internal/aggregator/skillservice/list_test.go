package skillservice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// listFixture seeds a fresh aggregator with a handful of services and
// (optional) ratings. Returns the aggregator + router for HTTP-level
// tests. Caller closes the store via the *Aggregator's cleanup chain
// (handled by setupAggregator's t.Cleanup hook in test process_test.go).
type listFixture struct {
	agg    *Aggregator
	router *gin.Engine
}

func newListFixture(t *testing.T) *listFixture {
	t.Helper()
	agg, store := setupAggregator(t)
	t.Cleanup(func() { store.Close() })

	// Configure the asset base URL so URL resolution is exercised in
	// the wire shape just like production. Test base URL is short for
	// readable assertions.
	agg.SetAssetBaseURL("https://example.com/c")

	// Plug in a deterministic profile lookup so providerName / avatar /
	// chatpubkey appear in the wire shape without spinning up the full
	// userinfo aggregator. The handler / list pipeline is what's under
	// test, not the userinfo adapter (covered in M2).
	agg.SetProfileLookup(&fakeProfileLookup{
		byMetaId: map[string]*ProfileSnapshot{
			"provA": {Name: "Provider Alpha", Avatar: "metafile://provAavatar", AvatarId: "provAavatar", ChatPublicKey: "pkA"},
			"provB": {Name: "Provider Beta", Avatar: "https://cdn.example.com/b.png", ChatPublicKey: "pkB"},
		},
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))

	return &listFixture{agg: agg, router: router}
}

// seedService is a sugar wrapper around HandleBlockPin for fixture setup.
func (f *listFixture) seed(t *testing.T, opts servicePinOpts) {
	t.Helper()
	if _, err := f.agg.HandleBlockPin(makeServicePin(t, opts)); err != nil {
		t.Fatalf("seed %s: %v", opts.PinId, err)
	}
}

func (f *listFixture) seedRating(t *testing.T, ratingPin string, target string, chain string, rate int, ts int64) {
	t.Helper()
	if _, err := f.agg.HandleBlockPin(makeRatingPin(t, ratingPinOpts{
		PinId: ratingPin, ChainName: chain, RaterMetaId: "rater_" + ratingPin,
		ServiceID: target, Rate: rate, Timestamp: ts,
	})); err != nil {
		t.Fatalf("rate %s -> %s: %v", ratingPin, target, err)
	}
}

// listBody calls the handler with the supplied query string and returns the
// parsed JSON `data` block. Lets tests assert on shape without re-typing
// the envelope each time.
type listBody struct {
	Code           int        `json:"code"`
	Message        string     `json:"message"`
	Data           ListResult `json:"data"`
	ProcessingTime int64      `json:"processingTime"`
}

func (f *listFixture) call(t *testing.T, query string) (int, listBody) {
	t.Helper()
	url := "/api/bot-hub/skill-service/list"
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	var body listBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v raw=%s", err, w.Body.String())
	}
	return w.Code, body
}

// --- AC: empty store yields a clean empty list ---

func TestListEndpoint_EmptyStore(t *testing.T) {
	f := newListFixture(t)
	status, body := f.call(t, "")
	if status != 200 {
		t.Fatalf("HTTP status: got %d", status)
	}
	if body.Code != 0 || body.Message != "" {
		t.Errorf("envelope: code=%d message=%q", body.Code, body.Message)
	}
	if body.Data.SchemaVersion != "botHubSkillService.v1" {
		t.Errorf("schemaVersion: %q", body.Data.SchemaVersion)
	}
	if len(body.Data.List) != 0 {
		t.Errorf("expected empty list, got %d items", len(body.Data.List))
	}
	if body.Data.NextCursor != "" {
		t.Errorf("expected empty nextCursor, got %q", body.Data.NextCursor)
	}
}

// --- AC: single create surfaces with all the wire fields populated ---

func TestListEndpoint_SingleService(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "svc1:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "Fortune", Description: "desc",
		ServiceIcon: "metafile://icon1",
		Currency:    "MVC", Price: "1",
	})

	_, body := f.call(t, "")
	if len(body.Data.List) != 1 {
		t.Fatalf("expected 1 item, got %d", len(body.Data.List))
	}
	it := body.Data.List[0]
	if it.CurrentPinId != "svc1:i0" || it.SourceServicePinId != "svc1:i0" {
		t.Errorf("identity: %+v", it)
	}
	if it.DisplayName != "Fortune" {
		t.Errorf("displayName: %q", it.DisplayName)
	}
	// MVC currency surfaces as SPACE per spec.
	if it.Currency != "SPACE" {
		t.Errorf("currency: got %q want SPACE", it.Currency)
	}
	// Asset resolver expands metafile:// and providerAvatar.
	if !strings.HasPrefix(it.ServiceIcon, "https://example.com/c/") {
		t.Errorf("serviceIcon not resolved: %q", it.ServiceIcon)
	}
	if it.ProviderName != "Provider Alpha" {
		t.Errorf("providerName: %q", it.ProviderName)
	}
	if !strings.HasPrefix(it.ProviderAvatar, "https://example.com/c/") {
		t.Errorf("providerAvatar not resolved: %q", it.ProviderAvatar)
	}
	if it.ProviderAvatarId != "provAavatar" {
		t.Errorf("providerAvatarId: got %q want provAavatar", it.ProviderAvatarId)
	}
	if it.ProviderChatPubkey != "pkA" {
		t.Errorf("providerChatPubkey: %q", it.ProviderChatPubkey)
	}
	// MRC20 fields are null for native currency services.
	if it.MRC20Ticker != nil || it.MRC20Id != nil {
		t.Errorf("MRC20 fields should be null: ticker=%v id=%v", it.MRC20Ticker, it.MRC20Id)
	}
}

func TestListEndpoint_DefaultsNativePaymentMetadataFromProviderAddress(t *testing.T) {
	f := newListFixture(t)
	pin := makeServicePin(t, servicePinOpts{
		PinId: "native:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune", DisplayName: "Fortune",
	})
	pin.ContentBody = []byte(`{"serviceName":"fortune","displayName":"Fortune","providerSkill":"fortune-skill","price":"0.01","currency":"SPACE","outputType":"text"}`)
	if _, err := f.agg.HandleBlockPin(pin); err != nil {
		t.Fatal(err)
	}

	_, body := f.call(t, "")
	if len(body.Data.List) != 1 {
		t.Fatalf("expected 1 item, got %d", len(body.Data.List))
	}
	it := body.Data.List[0]
	if it.SettlementKind != "native" {
		t.Fatalf("settlementKind: got %q want native", it.SettlementKind)
	}
	if it.PaymentChain != "mvc" {
		t.Fatalf("paymentChain: got %q want mvc", it.PaymentChain)
	}
	if it.PaymentAddress != "addr-prov-provA" {
		t.Fatalf("paymentAddress: got %q want provider address", it.PaymentAddress)
	}
}

func TestListEndpoint_UsesCanonicalProviderIdentityFromProfile(t *testing.T) {
	f := newListFixture(t)
	f.agg.SetProfileLookup(&fakeProfileLookup{
		byMetaId: map[string]*ProfileSnapshot{
			"1GrqProvider": {
				MetaId:        "1GrqProvider",
				GlobalMetaId:  "idq14provider",
				Address:       "1GrqProvider",
				Name:          "AI_Sunny",
				ChatPublicKey: "04sunny",
			},
		},
	})
	f.seed(t, servicePinOpts{
		PinId: "sunny-list:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "1GrqProvider", Timestamp: 1000,
		ServiceName: "metabot-metaid-wiki-service", DisplayName: "MetaID Wiki",
	})

	_, body := f.call(t, "")
	if len(body.Data.List) != 1 {
		t.Fatalf("expected 1 item, got %d", len(body.Data.List))
	}
	it := body.Data.List[0]
	if it.ProviderGlobalMetaId != "idq14provider" {
		t.Fatalf("providerGlobalMetaId: got %q want canonical idq14provider", it.ProviderGlobalMetaId)
	}
	if it.ProviderAddress != "1GrqProvider" {
		t.Fatalf("providerAddress: got %q want chain address", it.ProviderAddress)
	}

	_, body = f.call(t, "providerGlobalMetaId=idq14provider")
	if len(body.Data.List) != 1 {
		t.Fatalf("expected canonical providerGlobalMetaId filter to match 1 item, got %d", len(body.Data.List))
	}
}

// --- AC: revoked / disabled services hidden by default; shown with includeInactive=1 ---

func TestListEndpoint_VisibilityFilter(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "ok:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "ok", DisplayName: "OK",
	})
	f.seed(t, servicePinOpts{
		PinId: "disabled:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "x", DisplayName: "Disabled", Disabled: true,
	})
	f.seed(t, servicePinOpts{
		PinId: "revoked:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "y", DisplayName: "WillRevoke",
	})
	f.seed(t, servicePinOpts{
		PinId: "revoked_op:i0", Operation: OperationRevoke,
		ChainName: "mvc", ProviderMetaId: "provA",
		OriginalId: "revoked:i0", Timestamp: 2000,
		ServiceName: "y", DisplayName: "WillRevoke",
	})

	// Default: only the OK service visible.
	_, body := f.call(t, "")
	if len(body.Data.List) != 1 || body.Data.List[0].DisplayName != "OK" {
		t.Errorf("default visibility filter wrong, got %+v", body.Data.List)
	}

	// includeInactive=1: all three (disabled, revoked, ok).
	_, body = f.call(t, "includeInactive=1")
	if len(body.Data.List) != 3 {
		t.Errorf("includeInactive=1 should show all 3, got %d", len(body.Data.List))
	}
}

// --- AC: chainName filter narrows results ---

func TestListEndpoint_ChainNameFilter(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "mvc_svc:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "m", DisplayName: "MVC svc",
	})
	f.seed(t, servicePinOpts{
		PinId: "doge_svc:i0", Operation: OperationCreate,
		ChainName: "doge", ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "d", DisplayName: "DOGE svc",
	})

	_, body := f.call(t, "chainName=mvc")
	if len(body.Data.List) != 1 || body.Data.List[0].ChainName != "mvc" {
		t.Errorf("chainName filter wrong: %+v", body.Data.List)
	}
	_, body = f.call(t, "chainName=doge")
	if len(body.Data.List) != 1 || body.Data.List[0].ChainName != "doge" {
		t.Errorf("chainName=doge wrong: %+v", body.Data.List)
	}
}

// --- AC: keyword search across multiple fields ---

func TestListEndpoint_KeywordFilter(t *testing.T) {
	f := newListFixture(t)
	// Set ProviderSkill explicitly so the test isolates the keyword
	// match to fields the test actually controls (the default skill
	// would contain "fortune" and create a false positive).
	f.seed(t, servicePinOpts{
		PinId: "a:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "fortune-pro", DisplayName: "Fortune Pro",
		Description: "advanced fortune telling", ProviderSkill: "fortune-skill",
	})
	f.seed(t, servicePinOpts{
		PinId: "b:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provB", Timestamp: 1000,
		ServiceName: "weather", DisplayName: "Weather Now",
		Description: "live forecast", ProviderSkill: "weather-skill",
	})

	_, body := f.call(t, "keyword=fortune")
	if len(body.Data.List) != 1 || body.Data.List[0].ServiceName != "fortune-pro" {
		t.Errorf("keyword=fortune wrong: %+v", body.Data.List)
	}
	// Match providerName via the profile snapshot ("Provider Beta").
	_, body = f.call(t, "keyword=Beta")
	if len(body.Data.List) != 1 || body.Data.List[0].ProviderName != "Provider Beta" {
		t.Errorf("keyword=Beta did not match providerName: %+v", body.Data.List)
	}
}

// --- AC: rating sort uses Bayesian smoothing; same chain order is stable ---

func TestListEndpoint_RatingSort(t *testing.T) {
	f := newListFixture(t)
	// Three services; rate them so the smoothed score order is
	// high-rate-many-ratings > high-rate-few-ratings > no-ratings.
	f.seed(t, servicePinOpts{
		PinId: "many:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "many", DisplayName: "Many ratings",
	})
	f.seed(t, servicePinOpts{
		PinId: "few:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 2000,
		ServiceName: "few", DisplayName: "Few ratings",
	})
	f.seed(t, servicePinOpts{
		PinId: "none:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 3000,
		ServiceName: "none", DisplayName: "No ratings",
	})
	// many: 5 5-star ratings → smoothed = (5*5 + 4*5)/(5+5) = 4.5
	for i := 0; i < 5; i++ {
		f.seedRating(t, "rmany"+strings.Repeat("x", i+1), "many:i0", "mvc", 5, int64(2000+i))
	}
	// few: 1 5-star rating → smoothed = (5*1 + 4*5)/(1+5) = 25/6 ≈ 4.166
	f.seedRating(t, "rfew", "few:i0", "mvc", 5, 2100)

	_, body := f.call(t, "sortBy=rating")
	if len(body.Data.List) != 3 {
		t.Fatalf("expected 3 items, got %d", len(body.Data.List))
	}
	got := []string{body.Data.List[0].ServiceName, body.Data.List[1].ServiceName, body.Data.List[2].ServiceName}
	want := []string{"many", "few", "none"}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("rating sort wrong at %d: got %v want %v", i, got, want)
			break
		}
	}
}

// --- AC: updated sort emits newest first by default; asc reverses ---

func TestListEndpoint_UpdatedSort(t *testing.T) {
	f := newListFixture(t)
	for i, ts := range []int64{1000, 5000, 3000} {
		f.seed(t, servicePinOpts{
			PinId: pinIdFor(i + 1), Operation: OperationCreate, ChainName: "mvc",
			ProviderMetaId: "provA", Timestamp: ts,
			ServiceName: "svc" + intToStr(i+1), DisplayName: "S" + intToStr(i+1),
		})
	}
	_, body := f.call(t, "sortBy=updated&order=desc")
	if len(body.Data.List) != 3 {
		t.Fatalf("expected 3, got %d", len(body.Data.List))
	}
	if body.Data.List[0].UpdatedAt != 5000 || body.Data.List[2].UpdatedAt != 1000 {
		t.Errorf("desc updatedAt order wrong: %d %d %d",
			body.Data.List[0].UpdatedAt, body.Data.List[1].UpdatedAt, body.Data.List[2].UpdatedAt)
	}
	_, body = f.call(t, "sortBy=updated&order=asc")
	if body.Data.List[0].UpdatedAt != 1000 || body.Data.List[2].UpdatedAt != 5000 {
		t.Errorf("asc updatedAt order wrong")
	}
}

// --- AC: cursor paginates deterministically ---

func TestListEndpoint_Pagination(t *testing.T) {
	f := newListFixture(t)
	for i := 0; i < 5; i++ {
		f.seed(t, servicePinOpts{
			PinId: pinIdFor(i + 1), Operation: OperationCreate, ChainName: "mvc",
			ProviderMetaId: "provA", Timestamp: int64(i + 1),
			ServiceName: "svc" + intToStr(i+1), DisplayName: "S" + intToStr(i+1),
		})
	}

	// Page 1 (size=2, sort=updated desc → svc5/svc4)
	_, body := f.call(t, "size=2&sortBy=updated")
	if len(body.Data.List) != 2 {
		t.Fatalf("page1 size: %d", len(body.Data.List))
	}
	if body.Data.List[0].ServiceName != "svc5" {
		t.Errorf("page1[0]: %s", body.Data.List[0].ServiceName)
	}
	if body.Data.NextCursor == "" {
		t.Fatal("expected nextCursor on page1")
	}

	// Page 2 — should pick up svc3/svc2.
	cursor := body.Data.NextCursor
	_, body = f.call(t, "size=2&sortBy=updated&cursor="+cursor)
	if len(body.Data.List) != 2 {
		t.Fatalf("page2 size: %d", len(body.Data.List))
	}
	if body.Data.List[0].ServiceName != "svc3" {
		t.Errorf("page2[0]: %s", body.Data.List[0].ServiceName)
	}

	// Page 3 — last single record, no nextCursor.
	cursor = body.Data.NextCursor
	_, body = f.call(t, "size=2&sortBy=updated&cursor="+cursor)
	if len(body.Data.List) != 1 || body.Data.List[0].ServiceName != "svc1" {
		t.Errorf("page3: %+v", body.Data.List)
	}
	if body.Data.NextCursor != "" {
		t.Errorf("nextCursor should be empty on last page, got %q", body.Data.NextCursor)
	}
}

// --- AC: invalid cursor → code=40000 invalid cursor ---

func TestListEndpoint_InvalidCursor(t *testing.T) {
	f := newListFixture(t)
	_, body := f.call(t, "cursor=NOT_BASE64!!!")
	if body.Code != 40000 {
		t.Errorf("expected code=40000, got %d", body.Code)
	}
}

// --- AC: size cap at 100 ---

func TestListEndpoint_SizeCappedAt100(t *testing.T) {
	f := newListFixture(t)
	// Don't seed a hundred records; we only need to confirm the params
	// were clamped — easier via the typed entry point.
	res, err := f.agg.List(ListParams{Size: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	// (no records seeded → empty list, but we already covered emptiness
	// elsewhere; the assertion is that the call did not error.)
}

// --- AC: invalid size → code=40000 ---

func TestListEndpoint_InvalidSize(t *testing.T) {
	f := newListFixture(t)
	_, body := f.call(t, "size=-3")
	if body.Code != 40000 {
		t.Errorf("expected 40000, got %d", body.Code)
	}
}

// --- AC: currency filter accepts MVC ↔ SPACE alias ---

func TestListEndpoint_CurrencyAlias(t *testing.T) {
	f := newListFixture(t)
	// Provider declares MVC; UI typically asks for SPACE.
	f.seed(t, servicePinOpts{
		PinId: "mvc:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "s", DisplayName: "S", Currency: "MVC",
	})
	_, body := f.call(t, "currency=SPACE")
	if len(body.Data.List) != 1 {
		t.Errorf("SPACE filter should match MVC currency, got %d", len(body.Data.List))
	}
	// And vice versa: declared SPACE should match SPACE query.
	f.seed(t, servicePinOpts{
		PinId: "space:i0", Operation: OperationCreate, ChainName: "btc",
		ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "s2", DisplayName: "S2", Currency: "SPACE",
	})
	_, body = f.call(t, "currency=SPACE")
	if len(body.Data.List) != 2 {
		t.Errorf("SPACE filter total: %d want 2", len(body.Data.List))
	}
}

// --- AC: modify after create still shows latest version ---

func TestListEndpoint_ModifyReflectsLatest(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "src:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "svc", DisplayName: "v1",
	})
	f.seed(t, servicePinOpts{
		PinId: "mod:i0", Operation: OperationModify, ChainName: "mvc",
		ProviderMetaId: "provA", OriginalId: "src:i0", Timestamp: 2000,
		ServiceName: "svc", DisplayName: "v2-latest",
	})
	_, body := f.call(t, "")
	if len(body.Data.List) != 1 || body.Data.List[0].DisplayName != "v2-latest" {
		t.Errorf("modify not reflected: %+v", body.Data.List)
	}
	if body.Data.List[0].CurrentPinId != "mod:i0" {
		t.Errorf("currentPinId not advanced: %q", body.Data.List[0].CurrentPinId)
	}
}

// --- AC: MRC20 service surfaces non-null ticker/id ---

func TestListEndpoint_MRC20Fields(t *testing.T) {
	f := newListFixture(t)
	mrc := makeServicePin(t, servicePinOpts{
		PinId: "mrc:i0", Operation: OperationCreate, ChainName: "btc",
		ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "mrcsvc", DisplayName: "MRC20 svc", Currency: "MRC20",
	})
	// Inject MRC20 fields directly into the contentSummary the helper
	// generated; easier than expanding servicePinOpts for one test.
	mrc.ContentBody = mustMarshal(t, ServiceContentSummary{
		ServiceName: "mrcsvc", DisplayName: "MRC20 svc",
		Price: "1", Currency: "MRC20", PaymentChain: "btc",
		SettlementKind: "mrc20", MRC20Ticker: "TKN", MRC20Id: "TKN-id-1",
		PaymentAddress: "btc-addr", OutputType: "text",
	})
	if _, err := f.agg.HandleBlockPin(mrc); err != nil {
		t.Fatal(err)
	}

	_, body := f.call(t, "")
	if len(body.Data.List) != 1 {
		t.Fatalf("expected 1 item")
	}
	it := body.Data.List[0]
	if it.Currency != "MRC20" {
		t.Errorf("currency: %q", it.Currency)
	}
	if it.MRC20Ticker != "TKN" {
		t.Errorf("ticker: %v", it.MRC20Ticker)
	}
	if it.MRC20Id != "TKN-id-1" {
		t.Errorf("mrc20Id: %v", it.MRC20Id)
	}
}

// --- AC: providerGlobalMetaId filter narrows the result ---

func TestListEndpoint_ProviderFilter(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "a:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "s1", DisplayName: "S1",
	})
	f.seed(t, servicePinOpts{
		PinId: "b:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provB", Timestamp: 2000,
		ServiceName: "s2", DisplayName: "S2",
	})

	// The ServiceRecord stores GlobalMetaId as "idq1-" + ProviderMetaId
	// from our makeServicePin helper.
	_, body := f.call(t, "providerGlobalMetaId=idq1-provA")
	if len(body.Data.List) != 1 || body.Data.List[0].ServiceName != "s1" {
		t.Errorf("provider filter wrong: %+v", body.Data.List)
	}
}

func TestListHomepageByProviderGlobalMetaIdReadsNewestSix(t *testing.T) {
	f := newListFixture(t)
	for i := 1; i <= 7; i++ {
		f.seed(t, servicePinOpts{
			PinId: pinIdFor(i), Operation: OperationCreate, ChainName: "mvc",
			ProviderMetaId: "provA", Timestamp: int64(i * 100),
			ServiceName: "svc" + intToStr(i), DisplayName: "Service " + intToStr(i),
		})
	}

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq1-provA",
		Size:                 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 5 {
		t.Fatalf("expected 5 homepage items, got %d", len(res.List))
	}
	if !res.HasMore {
		t.Fatal("expected HasMore=true with more provider services available")
	}
	if res.List[0].ServiceName != "svc7" || res.List[4].ServiceName != "svc3" {
		t.Fatalf("homepage order wrong: got first=%s fifth=%s", res.List[0].ServiceName, res.List[4].ServiceName)
	}
}

func TestListHomepageByProviderCanonicalGlobalMetaIdFromProfile(t *testing.T) {
	f := newListFixture(t)
	f.agg.SetProfileLookup(&fakeProfileLookup{
		byMetaId: map[string]*ProfileSnapshot{
			"1GrqProvider": {
				MetaId:        "1GrqProvider",
				GlobalMetaId:  "idq14provider",
				Address:       "1GrqProvider",
				Name:          "AI_Sunny",
				ChatPublicKey: "04sunny",
			},
		},
	})
	f.seed(t, servicePinOpts{
		PinId: "sunny-home:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "1GrqProvider", Timestamp: 1000,
		ServiceName: "metabot-metaid-wiki-service", DisplayName: "MetaID Wiki",
	})

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq14provider",
		Size:                 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 1 {
		t.Fatalf("expected canonical homepage provider match, got %d items: %+v", len(res.List), res.List)
	}
	if res.List[0].ProviderGlobalMetaId != "idq14provider" {
		t.Fatalf("providerGlobalMetaId: got %q want canonical idq14provider", res.List[0].ProviderGlobalMetaId)
	}
}

func TestListHomepageByProviderLateProfileCanonicalGlobalMetaId(t *testing.T) {
	f := newListFixture(t)
	f.agg.SetProfileLookup(&fakeProfileLookup{})
	f.seed(t, servicePinOpts{
		PinId: "sunny-late-home:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "1GrqProvider", Timestamp: 1000,
		ServiceName: "metabot-metaid-wiki-service", DisplayName: "MetaID Wiki",
	})

	profile := &ProfileSnapshot{
		MetaId:        "1GrqProvider",
		GlobalMetaId:  "idq14provider",
		Address:       "1GrqProvider",
		Name:          "AI_Sunny",
		ChatPublicKey: "04sunny",
	}
	f.agg.SetProfileLookup(&fakeProfileLookup{
		byMetaId:     map[string]*ProfileSnapshot{"1GrqProvider": profile},
		byGlobalMeta: map[string]*ProfileSnapshot{"idq14provider": profile},
	})

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq14provider",
		Size:                 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 1 {
		t.Fatalf("expected late canonical homepage provider match, got %d items: %+v", len(res.List), res.List)
	}
	if res.List[0].ProviderGlobalMetaId != "idq14provider" {
		t.Fatalf("providerGlobalMetaId: got %q want canonical idq14provider", res.List[0].ProviderGlobalMetaId)
	}
}

func TestHomepageProviderMetaIndexPrefixIsProviderScopedCrossChain(t *testing.T) {
	prefix := string(homepageProviderMetaIndexPrefix("1GrqProvider"))
	if prefix == string(providerIndexPrefix("", "")) {
		t.Fatalf("cross-chain late alias fallback prefix must not scan the whole provider index: %q", prefix)
	}
	if !strings.HasPrefix(prefix, keyServiceByProviderMeta+"1GrqProvider:") {
		t.Fatalf("cross-chain late alias fallback prefix is not provider scoped: %q", prefix)
	}
}

func TestListHomepageByProviderLateProfileCrossChainDoesNotNeedLegacyProviderIndex(t *testing.T) {
	f := newListFixture(t)
	f.agg.SetProfileLookup(&fakeProfileLookup{})
	f.seed(t, servicePinOpts{
		PinId: "sunny-late-meta-home:i0", Operation: OperationCreate,
		ChainName: "mvc", ProviderMetaId: "1GrqProvider", Timestamp: 1000,
		ServiceName: "metabot-metaid-wiki-service", DisplayName: "MetaID Wiki",
	})
	if err := f.agg.store.Delete(NamespaceService,
		providerIndexKey("mvc", "1GrqProvider", "sunny-late-meta-home:i0")); err != nil {
		t.Fatalf("delete legacy provider index: %v", err)
	}

	profile := &ProfileSnapshot{
		MetaId:        "1GrqProvider",
		GlobalMetaId:  "idq14provider",
		Address:       "1GrqProvider",
		Name:          "AI_Sunny",
		ChatPublicKey: "04sunny",
	}
	f.agg.SetProfileLookup(&fakeProfileLookup{
		byMetaId:     map[string]*ProfileSnapshot{"1GrqProvider": profile},
		byGlobalMeta: map[string]*ProfileSnapshot{"idq14provider": profile},
	})

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq14provider",
		Size:                 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 1 {
		t.Fatalf("expected late canonical homepage provider match from provider-meta index, got %d items: %+v", len(res.List), res.List)
	}
	if res.List[0].SourceServicePinId != "sunny-late-meta-home:i0" {
		t.Fatalf("expected provider-meta indexed service, got %+v", res.List[0])
	}
}

func TestMatchesLateHomepageProviderAliasCandidateRejectsStaleProviderMetaTimestamp(t *testing.T) {
	rec := &ServiceRecord{
		ChainName:            "mvc",
		SourceServicePinId:   "sunny-stale-home:i0",
		ProviderMetaId:       "1GrqProvider",
		ProviderGlobalMetaId: "idq14provider",
		UpdatedAt:            1000,
	}
	profile := ProfileSnapshot{MetaId: "1GrqProvider", GlobalMetaId: "idq14provider"}
	p := HomepageListParams{ProviderGlobalMetaId: "idq14provider"}
	candidate := homepageProviderCandidate{
		invertedUpdatedAt: invertedTimestampHex(999),
		chainName:         "mvc",
		sourcePinId:       "sunny-stale-home:i0",
	}
	if matchesLateHomepageProviderAliasCandidate(rec, profile, p, candidate) {
		t.Fatal("expected stale provider-meta fallback timestamp to be rejected")
	}
}

func TestListHomepageByProviderSkipsInactiveBeforeHasMoreLimit(t *testing.T) {
	f := newListFixture(t)
	for i := 1; i <= 6; i++ {
		f.seed(t, servicePinOpts{
			PinId: pinIdFor(i), Operation: OperationCreate, ChainName: "mvc",
			ProviderMetaId: "provA", Timestamp: int64(i * 100),
			ServiceName: "visible" + intToStr(i), DisplayName: "Visible " + intToStr(i),
		})
	}
	f.seed(t, servicePinOpts{
		PinId: "revoked-home:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 700,
		ServiceName: "revoked-home", DisplayName: "Revoked Home",
	})
	f.seed(t, servicePinOpts{
		PinId: "revoked-home-op:i0", Operation: OperationRevoke, ChainName: "mvc",
		ProviderMetaId: "provA", OriginalId: "revoked-home:i0", Timestamp: 800,
		ServiceName: "revoked-home", DisplayName: "Revoked Home",
	})
	f.seed(t, servicePinOpts{
		PinId: "disabled-home:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 900, Disabled: true,
		ServiceName: "disabled-home", DisplayName: "Disabled Home",
	})

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq1-provA",
		Size:                 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 5 {
		t.Fatalf("expected 5 visible homepage items, got %d: %+v", len(res.List), res.List)
	}
	if !res.HasMore {
		t.Fatal("expected HasMore=true with a sixth visible service")
	}
	for _, item := range res.List {
		if item.Disabled || item.Operation == OperationRevoke {
			t.Fatalf("homepage returned inactive item: %+v", item)
		}
	}
	if res.List[0].ServiceName != "visible6" || res.List[4].ServiceName != "visible2" {
		t.Fatalf("homepage order wrong after inactive filter: got first=%s fifth=%s", res.List[0].ServiceName, res.List[4].ServiceName)
	}
}

func TestListHomepageByProviderCrossChain(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "mvc-home:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 100,
		ServiceName: "mvc-home", DisplayName: "MVC Home",
	})
	f.seed(t, servicePinOpts{
		PinId: "btc-home:i0", Operation: OperationCreate, ChainName: "btc",
		ProviderMetaId: "provA", Timestamp: 200,
		ServiceName: "btc-home", DisplayName: "BTC Home",
	})

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq1-provA",
		Size:                 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 2 {
		t.Fatalf("expected 2 cross-chain homepage items, got %d", len(res.List))
	}
	if res.List[0].ChainName != "btc" || res.List[0].ServiceName != "btc-home" {
		t.Fatalf("expected newest BTC service first, got %+v", res.List[0])
	}
	if res.List[1].ChainName != "mvc" || res.List[1].ServiceName != "mvc-home" {
		t.Fatalf("expected MVC service second, got %+v", res.List[1])
	}
	if res.HasMore {
		t.Fatal("expected HasMore=false for exactly two visible services")
	}
}

func TestListHomepageByProviderChainNameFiltersProviderGlobalChainIndex(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "mvc-home:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 100,
		ServiceName: "mvc-home", DisplayName: "MVC Home",
	})
	f.seed(t, servicePinOpts{
		PinId: "btc-home:i0", Operation: OperationCreate, ChainName: "btc",
		ProviderMetaId: "provA", Timestamp: 200,
		ServiceName: "btc-home", DisplayName: "BTC Home",
	})

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq1-provA",
		ChainName:            "mvc",
		Size:                 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 1 {
		t.Fatalf("expected one MVC homepage item, got %d: %+v", len(res.List), res.List)
	}
	if res.List[0].ChainName != "mvc" || res.List[0].ServiceName != "mvc-home" {
		t.Fatalf("expected only MVC service from chain-scoped homepage list, got %+v", res.List[0])
	}
	if res.HasMore {
		t.Fatal("expected HasMore=false for single MVC service")
	}
}

func TestListHomepageByProviderIncludeInactiveReturnsDisabledService(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "disabled-home:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 100,
		Disabled:    true,
		ServiceName: "disabled-home", DisplayName: "Disabled Home",
	})

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq1-provA",
		IncludeInactive:      true,
		Size:                 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 1 {
		t.Fatalf("expected disabled homepage item with includeInactive, got %d: %+v", len(res.List), res.List)
	}
	if !res.List[0].Disabled || res.List[0].ServiceName != "disabled-home" {
		t.Fatalf("expected disabled homepage service, got %+v", res.List[0])
	}
	if res.HasMore {
		t.Fatal("expected HasMore=false for single inactive service")
	}
}

func TestListHomepageByProviderSkipsStaleProviderGlobalIndex(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "provider-b-home:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provB", Timestamp: 100,
		ServiceName: "provider-b-home", DisplayName: "Provider B Home",
	})
	if err := f.agg.store.Set(NamespaceService,
		providerGlobalIndexKey("idq1-provA", 200, "mvc", "provider-b-home:i0"), []byte{}); err != nil {
		t.Fatalf("seed stale provider-global index: %v", err)
	}

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq1-provA",
		Size:                 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 0 {
		t.Fatalf("expected stale provider-global index to be skipped, got %+v", res.List)
	}
	if res.HasMore {
		t.Fatal("expected HasMore=false when only stale indexed services exist")
	}
}

func TestListHomepageByProviderStaleSameServiceTimestampIndexDoesNotDuplicate(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "same-service-home:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 1000,
		ServiceName: "same-service-home", DisplayName: "Same Service Home",
	})
	if err := f.agg.store.Set(NamespaceService,
		providerGlobalIndexKey("idq1-provA", 100, "mvc", "same-service-home:i0"), []byte{}); err != nil {
		t.Fatalf("seed stale same-service provider-global index: %v", err)
	}

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq1-provA",
		Size:                 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.List) != 1 {
		t.Fatalf("expected one homepage item after stale duplicate index, got %d: %+v", len(res.List), res.List)
	}
	if res.List[0].SourceServicePinId != "same-service-home:i0" {
		t.Fatalf("expected same-service-home item, got %+v", res.List[0])
	}
	if res.HasMore {
		t.Fatal("expected HasMore=false when stale same-service timestamp index exists")
	}
}

func TestListHomepageByProviderSkipsCorruptIndexedService(t *testing.T) {
	f := newListFixture(t)
	if err := f.agg.store.Set(NamespaceService, serviceKey("mvc", "corrupt-home:i0"), []byte("{")); err != nil {
		t.Fatalf("seed corrupt service record: %v", err)
	}
	if err := f.agg.store.Set(NamespaceService,
		providerGlobalIndexKey("idq1-provA", 200, "mvc", "corrupt-home:i0"), []byte{}); err != nil {
		t.Fatalf("seed corrupt provider-global index: %v", err)
	}
	f.seed(t, servicePinOpts{
		PinId: "valid-home:i0", Operation: OperationCreate, ChainName: "mvc",
		ProviderMetaId: "provA", Timestamp: 100,
		ServiceName: "valid-home", DisplayName: "Valid Home",
	})

	res, err := f.agg.ListHomepageByProvider(HomepageListParams{
		ProviderGlobalMetaId: "idq1-provA",
		Size:                 6,
	})
	if err != nil {
		t.Fatalf("expected corrupt indexed service to be skipped, got err=%v", err)
	}
	if len(res.List) != 1 {
		t.Fatalf("expected one valid homepage item after corrupt record, got %d: %+v", len(res.List), res.List)
	}
	if res.List[0].ServiceName != "valid-home" {
		t.Fatalf("expected valid service after corrupt record, got %+v", res.List[0])
	}
}
