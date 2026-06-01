package skillservice

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type detailBody struct {
	Code    int          `json:"code"`
	Message string       `json:"message"`
	Data    DetailResult `json:"data"`
}

func (f *listFixture) callDetail(t *testing.T, serviceID, query string) (int, detailBody) {
	t.Helper()
	url := "/api/bot-hub/skill-service/detail/" + serviceID
	if query != "" {
		url += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, url, nil)
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	var body detailBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v raw=%s", err, w.Body.String())
	}
	return w.Code, body
}

func TestDetailEndpoint_NotFound(t *testing.T) {
	f := newListFixture(t)
	status, body := f.callDetail(t, "missing:i0", "")
	if status != 200 || body.Code != 40400 {
		t.Fatalf("want HTTP 200 code 40400, got status=%d code=%d msg=%s", status, body.Code, body.Message)
	}
}

func TestDetailEndpoint_BySourcePinId(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "src:i0", ChainName: "mvc", Operation: OperationCreate,
		DisplayName: "Fortune", ServiceName: "fortune", ProviderSkill: "fortune-skill",
		ProviderMetaId: "provA", Price: "1", Currency: "MVC",
	})
	f.seed(t, servicePinOpts{
		PinId: "mod:i0", ChainName: "mvc", Operation: OperationModify,
		OriginalId: "src:i0", DisplayName: "Fortune v2",
	})

	status, body := f.callDetail(t, "src:i0", "idType=sourceServicePinId&chainName=mvc")
	if status != 200 || body.Code != 0 {
		t.Fatalf("want success, got status=%d code=%d", status, body.Code)
	}
	if body.Data.Service.CurrentPinId != "mod:i0" {
		t.Fatalf("currentPinId: got %q want mod:i0", body.Data.Service.CurrentPinId)
	}
	if body.Data.Service.DisplayName != "Fortune v2" {
		t.Fatalf("displayName: got %q", body.Data.Service.DisplayName)
	}
	if body.Data.Service.Currency != "SPACE" {
		t.Fatalf("currency normalised to SPACE, got %q", body.Data.Service.Currency)
	}
	if body.Data.Provider.Name != "Provider Alpha" {
		t.Fatalf("provider name: got %q", body.Data.Provider.Name)
	}
	if body.Data.Provider.ChatPubkey == nil || *body.Data.Provider.ChatPubkey != "pkA" {
		t.Fatalf("chatPubkey: got %v", body.Data.Provider.ChatPubkey)
	}
	if body.Data.SchemaVersion != "botHubSkillServiceDetail.v1" {
		t.Fatalf("schema: %q", body.Data.SchemaVersion)
	}
}

func TestDetailEndpoint_ByCurrentPinIdAuto(t *testing.T) {
	f := newListFixture(t)
	f.seed(t, servicePinOpts{
		PinId: "src:i1", ChainName: "mvc", Operation: OperationCreate,
		DisplayName: "Alpha", ServiceName: "alpha", ProviderSkill: "alpha-skill",
	})
	f.seed(t, servicePinOpts{
		PinId: "mod:i1", ChainName: "mvc", Operation: OperationModify,
		OriginalId: "src:i1", DisplayName: "Alpha v2",
	})

	status, body := f.callDetail(t, "mod:i1", "chainName=mvc")
	if status != 200 || body.Code != 0 {
		t.Fatalf("want success, got status=%d code=%d", status, body.Code)
	}
	if body.Data.Service.SourceServicePinId != "src:i1" {
		t.Fatalf("source: got %q", body.Data.Service.SourceServicePinId)
	}
}

func TestDetailEndpoint_MRC20Fields(t *testing.T) {
	f := newListFixture(t)
	pin := makeServicePin(t, servicePinOpts{
		PinId: "mrc:i0", ChainName: "mvc", Operation: OperationCreate,
		DisplayName: "Token svc", ServiceName: "token", ProviderSkill: "tok",
	})
	pin.ContentBody = []byte(`{"serviceName":"token","displayName":"Token svc","providerSkill":"tok","price":"1","currency":"SPACE","paymentChain":"mvc","settlementKind":"mrc20","outputType":"text","paymentAddress":"addr","mrc20Ticker":"FOO","mrc20Id":"0xabc"}`)
	if _, err := f.agg.HandleBlockPin(pin); err != nil {
		t.Fatal(err)
	}

	_, body := f.callDetail(t, "mrc:i0", "chainName=mvc")
	if body.Code != 0 {
		t.Fatalf("code=%d", body.Code)
	}
	if body.Data.Service.MRC20Ticker != "FOO" || body.Data.Service.MRC20Id != "0xabc" {
		t.Fatalf("mrc20 fields: ticker=%v id=%v", body.Data.Service.MRC20Ticker, body.Data.Service.MRC20Id)
	}
}

func TestDetailEndpoint_DefaultsNativePaymentMetadataFromProviderAddress(t *testing.T) {
	f := newListFixture(t)
	pin := makeServicePin(t, servicePinOpts{
		PinId: "native-detail:i0", ChainName: "mvc", Operation: OperationCreate,
		ProviderMetaId: "provA", DisplayName: "Fortune", ServiceName: "fortune",
	})
	pin.ContentBody = []byte(`{"serviceName":"fortune","displayName":"Fortune","providerSkill":"fortune-skill","price":"0.01","currency":"SPACE","outputType":"text"}`)
	if _, err := f.agg.HandleBlockPin(pin); err != nil {
		t.Fatal(err)
	}

	_, body := f.callDetail(t, "native-detail:i0", "chainName=mvc")
	if body.Code != 0 {
		t.Fatalf("code=%d", body.Code)
	}
	if body.Data.Service.SettlementKind != "native" {
		t.Fatalf("settlementKind: got %q want native", body.Data.Service.SettlementKind)
	}
	if body.Data.Service.PaymentChain != "mvc" {
		t.Fatalf("paymentChain: got %q want mvc", body.Data.Service.PaymentChain)
	}
	if body.Data.Service.PaymentAddress != "addr-prov-provA" {
		t.Fatalf("paymentAddress: got %q want provider address", body.Data.Service.PaymentAddress)
	}
}

func TestDetailEndpoint_InvalidIDType(t *testing.T) {
	f := newListFixture(t)
	_, body := f.callDetail(t, "x", "idType=bad")
	if body.Code != 40000 {
		t.Fatalf("want 40000, got %d", body.Code)
	}
}

func TestFindService_AmbiguousWithoutChain(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	// Same pin id on two chains — caller must pass chainName to disambiguate.
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "same:i0", ChainName: "btc", Operation: OperationCreate,
		DisplayName: "btc", ServiceName: "s", ProviderSkill: "s",
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := agg.HandleBlockPin(makeServicePin(t, servicePinOpts{
		PinId: "same:i0", ChainName: "mvc", Operation: OperationCreate,
		DisplayName: "mvc", ServiceName: "s", ProviderSkill: "s",
	})); err != nil {
		t.Fatal(err)
	}

	_, err := agg.FindService("same:i0", "", "auto")
	if err != errAmbiguousLookup {
		t.Fatalf("want ambiguous, got %v", err)
	}
	rec, err := agg.FindService("same:i0", "mvc", "auto")
	if err != nil || rec == nil || rec.DisplayName != "mvc" {
		t.Fatalf("scoped lookup: rec=%v err=%v", rec, err)
	}
}
