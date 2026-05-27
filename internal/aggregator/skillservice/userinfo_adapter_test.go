package skillservice

import (
	"testing"

	"github.com/metaid-developers/meta-socket/internal/aggregator"
	"github.com/metaid-developers/meta-socket/internal/aggregator/userinfo"
	"github.com/metaid-developers/meta-socket/internal/cache"
	"github.com/metaid-developers/meta-socket/internal/storage"
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
