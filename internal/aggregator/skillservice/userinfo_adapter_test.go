package skillservice

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// TestUserInfoLookupAdapter_EndToEnd exercises the real userinfo
// aggregator: index a /info/name + /info/chatpubkey pin pair, then verify
// the adapter pulls them through ProfileLookup the same way main.go would.
// This catches regressions in either side of the contract (skillservice's
// ProfileSnapshot shape and userinfo's UserProfile JSON layout).
func TestUserInfoLookupAdapter_EndToEnd(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()
	cacheProvider := cache.New(store)

	ui := &userinfo.Aggregator{}
	if err := ui.Init(store, cacheProvider); err != nil {
		t.Fatalf("userinfo.Init: %v", err)
	}

	// Seed userinfo: init + /info/name + /info/chatpubkey
	for _, pin := range []*aggregator.PinInscription{
		{
			Path: "/", Operation: "init",
			MetaId: "providerA", Address: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
			ChainName: "btc", Id: "init:i0",
		},
		{
			Path: "/info/name", Operation: "create",
			MetaId: "providerA", Address: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
			ChainName: "btc", Id: "name:i0",
			ContentBody: []byte("Fortune Bot"),
		},
		{
			Path: "/info/chatpubkey", Operation: "create",
			MetaId: "providerA", Address: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
			ChainName: "btc", Id: "chatpk:i0",
			ContentBody: []byte("02deadbeefdeadbeef"),
		},
	} {
		if _, err := ui.HandleBlockPin(pin); err != nil {
			t.Fatalf("userinfo.HandleBlockPin: %v", err)
		}
	}

	adapter := NewUserInfoLookupAdapter(ui)

	// Lookup by MetaId (primary path)
	snap, err := adapter.LookupByMetaId("providerA")
	if err != nil {
		t.Fatalf("LookupByMetaId err: %v", err)
	}
	if snap == nil {
		t.Fatal("LookupByMetaId returned nil snapshot")
	}
	if snap.Name != "Fortune Bot" {
		t.Errorf("Name: %q want Fortune Bot", snap.Name)
	}
	if snap.ChatPublicKey != "02deadbeefdeadbeef" {
		t.Errorf("ChatPublicKey: %q", snap.ChatPublicKey)
	}

	// Lookup by Address (fallback tier)
	snap, err = adapter.LookupByAddress("1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa")
	if err != nil {
		t.Fatalf("LookupByAddress err: %v", err)
	}
	if snap == nil || snap.Name != "Fortune Bot" {
		t.Errorf("LookupByAddress did not resolve: %+v", snap)
	}

	// Unknown metaid → (nil, nil), NOT an error
	snap, err = adapter.LookupByMetaId("does_not_exist")
	if err != nil {
		t.Errorf("unknown metaid should not error: %v", err)
	}
	if snap != nil {
		t.Errorf("unknown metaid should return nil snapshot, got %+v", snap)
	}

	// Plug the adapter into the skillservice aggregator and resolve a
	// record that points at providerA. This is the real wiring main.go
	// uses, so it catches integration regressions end-to-end.
	ssAgg := &Aggregator{}
	if err := ssAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("skillservice.Init: %v", err)
	}
	ssAgg.SetProfileLookup(adapter)

	got := ssAgg.ResolveProvider(&ServiceRecord{
		ProviderMetaId:       "providerA",
		ProviderGlobalMetaId: "",
		ProviderAddress:      "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
	})
	if got.Name != "Fortune Bot" || got.ChatPublicKey != "02deadbeefdeadbeef" {
		t.Errorf("ResolveProvider end-to-end miss: %+v", got)
	}
}

// TestUserInfoLookupAdapter_NilHandling exercises the safety net for the
// case where userinfo is somehow nil. main.go currently always wires it,
// but this guards against future refactors that might omit the adapter.
func TestUserInfoLookupAdapter_NilHandling(t *testing.T) {
	adapter := NewUserInfoLookupAdapter(nil)
	if snap, err := adapter.LookupByMetaId("x"); snap != nil || err != nil {
		t.Errorf("nil ui adapter should return (nil, nil), got %v %v", snap, err)
	}
	if snap, err := adapter.LookupByGlobalMetaId("x"); snap != nil || err != nil {
		t.Errorf("nil ui adapter should return (nil, nil), got %v %v", snap, err)
	}
	if snap, err := adapter.LookupByAddress("x"); snap != nil || err != nil {
		t.Errorf("nil ui adapter should return (nil, nil), got %v %v", snap, err)
	}
}

func TestUserInfoLookupAdapter_RemoteFallbackSuppliesDetailChatKey(t *testing.T) {
	const providerAddress = "1PaidProviderAddress11111111111111111"

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/info/metaid/" + providerAddress:
			_, _ = w.Write([]byte(`{"code":40400,"message":"user not found"}`))
		case "/info/address/" + providerAddress:
			_, _ = w.Write([]byte(`{"code":1,"message":"success","data":{"metaid":"paid_provider_metaid","globalMetaId":"idq1paidprovider","address":"` + providerAddress + `","name":"Paid Provider","chatpubkey":"04paidproviderkey","chatpubkeyId":"paid_key:i0"}}`))
		default:
			t.Fatalf("unexpected remote profile path: %s", r.URL.Path)
		}
	}))
	defer remote.Close()

	t.Setenv("METASO_P2P_PROFILE_REMOTE_BASE_URL", remote.URL)
	t.Setenv("METASO_P2P_PROFILE_MODE", "local-first")
	t.Setenv("METASO_P2P_PROFILE_ALLOW_REMOTE_FALLBACK", "true")

	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()
	cacheProvider := cache.New(store)

	ui := &userinfo.Aggregator{}
	if err := ui.Init(store, cacheProvider); err != nil {
		t.Fatalf("userinfo.Init: %v", err)
	}
	if _, err := ui.HandleBlockPin(&aggregator.PinInscription{
		Path:        "/info/name",
		Operation:   "create",
		MetaId:      providerAddress,
		Address:     providerAddress,
		ChainName:   "mvc",
		Id:          "paid_name:i0",
		ContentBody: []byte("Local Paid Provider"),
	}); err != nil {
		t.Fatalf("seed local userinfo: %v", err)
	}

	ssAgg := &Aggregator{}
	if err := ssAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("skillservice.Init: %v", err)
	}
	ssAgg.SetProfileLookup(NewUserInfoLookupAdapter(ui))
	servicePin := makeServicePin(t, servicePinOpts{
		PinId: "paid_service:i0", ChainName: "mvc", Operation: OperationCreate,
		ProviderMetaId: providerAddress,
		ServiceName:    "paid", DisplayName: "Paid Service", ProviderSkill: "paid-skill",
		Price: "0.0001", Currency: "SPACE",
	})
	servicePin.Address = providerAddress
	servicePin.CreateAddress = providerAddress
	servicePin.GlobalMetaId = providerAddress
	if _, err := ssAgg.HandleBlockPin(servicePin); err != nil {
		t.Fatalf("seed service: %v", err)
	}

	detail, err := ssAgg.Detail(DetailParams{ServiceID: "paid_service:i0", ChainName: "mvc"})
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail == nil {
		t.Fatal("detail not found")
	}
	if detail.Provider.ChatPubkey == nil || *detail.Provider.ChatPubkey != "04paidproviderkey" {
		t.Fatalf("provider chatPubkey missing from detail: %+v", detail.Provider)
	}
}
