package privatechat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// setupTestAggregator creates a test-ready privatechat aggregator.
func setupTestAggregator(t *testing.T) (*Aggregator, *storage.PebbleStore, *gin.Engine) {
	t.Helper()

	store := storage.NewPebbleStore(t.TempDir())
	cacheProvider := cache.New(store)

	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("failed to init aggregator: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))

	return agg, store, router
}

func TestCacheTTLUsesDurationUnits(t *testing.T) {
	if cacheTTL != 5*time.Minute {
		t.Fatalf("cacheTTL = %s, want 5m", cacheTTL)
	}
}

func performRequest(t *testing.T, router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// mustMarshal is a test helper that marshals to JSON bytes.
func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return b
}

type fakePrivateChatProfileLookup struct {
	byMetaId       map[string]*IdentityProfile
	byGlobalMetaId map[string]*IdentityProfile
	byAddress      map[string]*IdentityProfile
}

type localPrivateChatProfileLookup struct {
	fakePrivateChatProfileLookup
	profile    *IdentityProfile
	localCalls int
}

func (f *localPrivateChatProfileLookup) LookupLocalByIdentity(identity string) (*IdentityProfile, error) {
	f.localCalls++
	return f.profile, nil
}

func (f *fakePrivateChatProfileLookup) LookupByMetaId(metaid string) (*IdentityProfile, error) {
	return f.byMetaId[metaid], nil
}

func (f *fakePrivateChatProfileLookup) LookupByGlobalMetaId(globalMetaId string) (*IdentityProfile, error) {
	return f.byGlobalMetaId[globalMetaId], nil
}

func (f *fakePrivateChatProfileLookup) LookupByAddress(address string) (*IdentityProfile, error) {
	return f.byAddress[address], nil
}

// --- AC2: Message persistence ---

func TestPrivateMessagePersistence(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Process a private chat simplemsg pin
	pin := &aggregator.PinInscription{
		Id:            "priv_tx1:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1AliceSender",
		CreateMetaId:  "alice_meta_id",
		GlobalMetaId:  "global_alice_meta_id",
		ChainName:     "btc",
		Timestamp:     1700000000000,
		GenesisHeight: 100,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_meta_id",
			To:          "bob_meta_id",
			Content:     "Hello Bob!",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}
	if evt == nil {
		t.Fatal("expected NotifyEvent, got nil")
	}

	// Query via HTTP endpoint (Alice queries her chat with Bob)
	w := performRequest(t, router, "GET",
		"/api/group-chat/private-chat-list?metaId=alice_meta_id&otherMetaId=bob_meta_id&cursor=&size=20")

	var resp struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %d: %s", resp.Code, w.Body.String())
	}
	if len(resp.Data.List) == 0 {
		t.Fatal("expected at least one message")
	}

	msg := resp.Data.List[0]
	if msg.From != "alice_meta_id" {
		t.Errorf("expected from='alice_meta_id', got %q", msg.From)
	}
	if msg.To != "bob_meta_id" {
		t.Errorf("expected to='bob_meta_id', got %q", msg.To)
	}
	if msg.Content != "Hello Bob!" {
		t.Errorf("expected content='Hello Bob!', got %q", msg.Content)
	}
	if msg.ContentType != "text/plain" {
		t.Errorf("expected contentType='text/plain', got %q", msg.ContentType)
	}
	if msg.Encryption != "none" {
		t.Errorf("expected encryption='none', got %q", msg.Encryption)
	}
	if msg.TxId != "priv_tx1" {
		t.Errorf("expected txId='priv_tx1', got %q", msg.TxId)
	}
	if msg.PinId != "priv_tx1:i0" {
		t.Errorf("expected pinId='priv_tx1:i0', got %q", msg.PinId)
	}
	if msg.BlockHeight != 100 {
		t.Errorf("expected blockHeight=100, got %d", msg.BlockHeight)
	}
	if msg.Chain != "btc" {
		t.Errorf("expected chain='btc', got %q", msg.Chain)
	}
	if msg.Protocol != "/private/chat/simplemsg" {
		t.Errorf("expected protocol='/private/chat/simplemsg', got %q", msg.Protocol)
	}
	if msg.FromGlobalMetaId != "global_alice_meta_id" {
		t.Errorf("expected fromGlobalMetaId='global_alice_meta_id', got %q", msg.FromGlobalMetaId)
	}

	t.Logf("Message persistence OK: from=%s to=%s content=%s txId=%s pinId=%s",
		msg.From, msg.To, msg.Content, msg.TxId, msg.PinId)
}

func TestSimpleMsgEncryptAliasIsPersisted(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Id:            "encrypt_alias:i0",
		Path:          "/protocols/simplemsg",
		Operation:     "create",
		CreateAddress: "1AliceSender",
		CreateMetaId:  "alice_encrypt",
		GlobalMetaId:  "global_alice_encrypt",
		ChainName:     "mvc",
		Timestamp:     1700000000000,
		ContentBody: []byte(`{
			"to":"bob_encrypt",
			"content":"U2FsdGVkX1encrypted",
			"contentType":"text/plain",
			"encrypt":"ecdh",
			"replyPin":""
		}`),
	}

	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}

	w := performRequest(t, router, "GET",
		"/api/private-chat/messages?metaId=alice_encrypt&otherMetaId=bob_encrypt&cursor=&size=20")
	var resp struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %d: %s", resp.Code, w.Body.String())
	}
	if len(resp.Data.List) != 1 {
		t.Fatalf("expected 1 message, got %d: %s", len(resp.Data.List), w.Body.String())
	}
	if resp.Data.List[0].Encryption != "ecdh" {
		t.Fatalf("encryption: got %q want ecdh", resp.Data.List[0].Encryption)
	}
}

func TestPrivateChatList_ReadsPersistedMessagesAfterRestart(t *testing.T) {
	dataDir := t.TempDir()
	store1 := storage.NewPebbleStore(dataDir)
	agg1 := &Aggregator{}
	if err := agg1.Init(store1, cache.New(store1)); err != nil {
		t.Fatalf("init first aggregator: %v", err)
	}

	if err := agg1.SavePrivateMessage(&PrivateMessage{
		From:      "1GrqProvider",
		To:        "idqBuyer",
		TxId:      "restart-tx",
		PinId:     "restart-pin:i0",
		Content:   "persisted",
		Timestamp: 1780313024,
	}); err != nil {
		t.Fatalf("save message: %v", err)
	}
	if err := store1.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	store2 := storage.NewPebbleStore(dataDir)
	defer store2.Close()
	agg2 := &Aggregator{}
	if err := agg2.Init(store2, cache.New(store2)); err != nil {
		t.Fatalf("init restarted aggregator: %v", err)
	}

	got, err := agg2.GetPrivateChatList("idqBuyer", "1GrqProvider", "", 20, 0)
	if err != nil {
		t.Fatalf("GetPrivateChatList after restart: %v", err)
	}
	if got.Total != 1 || len(got.List) != 1 {
		t.Fatalf("expected persisted message after restart, total=%d len=%d", got.Total, len(got.List))
	}
	if got.List[0].PinId != "restart-pin:i0" {
		t.Fatalf("pinId: got %q want restart-pin:i0", got.List[0].PinId)
	}
}

func TestPrivateChatList_ResolvesCanonicalPeerAlias(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()
	agg.SetProfileLookup(&fakePrivateChatProfileLookup{
		byGlobalMetaId: map[string]*IdentityProfile{
			"idq14provider": {MetaId: "1GrqProvider", GlobalMetaId: "idq14provider", Address: "1GrqProvider"},
		},
	})

	pin := &aggregator.PinInscription{
		Id:            "provider_reply:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1GrqProvider",
		CreateMetaId:  "1GrqProvider",
		GlobalMetaId:  "1GrqProvider",
		ChainName:     "mvc",
		Timestamp:     1780313024,
		GenesisHeight: 175637,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "1GrqProvider",
			To:          "idqBuyer",
			Content:     "[ORDER_STATUS:buyer_pin] done",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}

	w := performRequest(t, router, "GET",
		"/api/group-chat/private-chat-list?metaId=idqBuyer&otherMetaId=idq14provider&cursor=&size=20")

	var resp struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v raw=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %d: %s", resp.Code, w.Body.String())
	}
	if resp.Data.Total != 1 || len(resp.Data.List) != 1 {
		t.Fatalf("expected aliased message, total=%d len=%d body=%s", resp.Data.Total, len(resp.Data.List), w.Body.String())
	}
	if resp.Data.List[0].PinId != "provider_reply:i0" {
		t.Fatalf("pinId: got %q want provider_reply:i0", resp.Data.List[0].PinId)
	}
}

func TestPrivateChatNotifyEventTargetsRecipientAliases(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	agg.SetProfileLookup(&fakePrivateChatProfileLookup{
		byGlobalMetaId: map[string]*IdentityProfile{
			"idqBuyerGlobal": {
				MetaId:       "buyer_local_meta",
				GlobalMetaId: "idqBuyerGlobal",
				Address:      "1BuyerAddress",
			},
		},
	})

	pin := &aggregator.PinInscription{
		Id:            "alias_push:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1ProviderAddress",
		CreateMetaId:  "provider_meta",
		GlobalMetaId:  "idqProviderGlobal",
		ChainName:     "mvc",
		Timestamp:     1780562000,
		GenesisHeight: 176100,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "provider_meta",
			To:          "idqBuyerGlobal",
			Content:     "alias route",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}
	if evt == nil {
		t.Fatal("expected NotifyEvent")
	}

	want := []string{"idqBuyerGlobal", "buyer_local_meta", "1BuyerAddress"}
	if !reflect.DeepEqual(evt.TargetIds, want) {
		t.Fatalf("TargetIds = %#v, want %#v", evt.TargetIds, want)
	}
	if evt.MetaId != "idqBuyerGlobal" {
		t.Fatalf("MetaId fallback = %q, want idqBuyerGlobal", evt.MetaId)
	}
}

func TestIdentityAliasesPreferSingleLocalIdentityLookup(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	lookup := &localPrivateChatProfileLookup{profile: &IdentityProfile{
		MetaId:       "buyer_local_meta",
		GlobalMetaId: "idqBuyerGlobal",
		Address:      "1BuyerAddress",
	}}
	agg.SetProfileLookup(lookup)

	got := agg.identityAliases("1BuyerAddress")
	want := []string{"1BuyerAddress", "buyer_local_meta", "idqBuyerGlobal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("identityAliases = %#v, want %#v", got, want)
	}
	if lookup.localCalls != 1 {
		t.Fatalf("local identity lookup calls = %d, want 1", lookup.localCalls)
	}
}

func TestPrivateChatNotifyPayloadUsesCanonicalMessageShape(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Id:            "canonical_payload:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1ProviderAddress",
		CreateMetaId:  "provider_meta",
		GlobalMetaId:  "idqProviderGlobal",
		ChainName:     "mvc",
		Timestamp:     1780562100,
		GenesisHeight: 176101,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "provider_meta",
			To:          "idqBuyerGlobal",
			Content:     "canonical payload",
			ContentType: "text/markdown",
			Encrypt:     "ecdh",
		}),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}
	if evt == nil {
		t.Fatal("expected NotifyEvent")
	}

	payload, ok := evt.Payload.(*PrivateMessage)
	if !ok {
		t.Fatalf("Payload = %T, want *PrivateMessage", evt.Payload)
	}
	if payload.FromGlobalMetaId != "idqProviderGlobal" {
		t.Fatalf("FromGlobalMetaId = %q, want idqProviderGlobal", payload.FromGlobalMetaId)
	}
	if payload.ToGlobalMetaId != "idqBuyerGlobal" {
		t.Fatalf("ToGlobalMetaId = %q, want idqBuyerGlobal", payload.ToGlobalMetaId)
	}
	if payload.Protocol != "/private/chat/simplemsg" {
		t.Fatalf("Protocol = %q, want /private/chat/simplemsg", payload.Protocol)
	}
	if payload.Chain != "mvc" {
		t.Fatalf("Chain = %q, want mvc", payload.Chain)
	}
	if payload.BlockHeight != 176101 {
		t.Fatalf("BlockHeight = %d, want 176101", payload.BlockHeight)
	}
	if payload.Index != 0 {
		t.Fatalf("Index = %d, want 0", payload.Index)
	}
	if payload.ContentType != "text/markdown" || payload.Encryption != "ecdh" {
		t.Fatalf("ContentType/Encryption = %q/%q, want text/markdown/ecdh", payload.ContentType, payload.Encryption)
	}
}

func TestCanonicalPrivateChatRoutesMirrorGroupChatCompatibilityRoutes(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Id:            "canonical_tx:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1CanonicalAlice",
		CreateMetaId:  "alice_canonical",
		GlobalMetaId:  "global_alice_canonical",
		ChainName:     "mvc",
		Timestamp:     1780315000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_canonical",
			To:          "bob_canonical",
			Content:     "canonical route parity",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}

	cases := []struct {
		name      string
		canonical string
		compat    string
	}{
		{
			name:      "messages",
			canonical: "/api/private-chat/messages?metaId=alice_canonical&otherMetaId=bob_canonical&cursor=&size=20&timestamp=1780315000",
			compat:    "/api/group-chat/private-chat-list?metaId=alice_canonical&otherMetaId=bob_canonical&cursor=&size=20&timestamp=1780315000",
		},
		{
			name:      "messages by index",
			canonical: "/api/private-chat/messages/by-index?metaId=alice_canonical&otherMetaId=bob_canonical&startIndex=0&size=20",
			compat:    "/api/group-chat/private-chat-list-by-index?metaId=alice_canonical&otherMetaId=bob_canonical&startIndex=0&size=20",
		},
		{
			name:      "paths",
			canonical: "/api/private-chat/paths?metaId=alice_canonical",
			compat:    "/api/group-chat/private-group-paths?metaId=alice_canonical",
		},
		{
			name:      "homes",
			canonical: "/api/private-chat/homes/alice_canonical",
			compat:    "/api/group-chat/chat/homes/alice_canonical",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			canonical := performRequest(t, router, "GET", tc.canonical)
			compat := performRequest(t, router, "GET", tc.compat)
			if canonical.Code != compat.Code {
				t.Fatalf("status mismatch: canonical=%d compat=%d canonicalBody=%s compatBody=%s",
					canonical.Code, compat.Code, canonical.Body.String(), compat.Body.String())
			}
			var canonicalBody map[string]interface{}
			var compatBody map[string]interface{}
			if err := json.Unmarshal(canonical.Body.Bytes(), &canonicalBody); err != nil {
				t.Fatalf("decode canonical body: %v raw=%s", err, canonical.Body.String())
			}
			if err := json.Unmarshal(compat.Body.Bytes(), &compatBody); err != nil {
				t.Fatalf("decode compat body: %v raw=%s", err, compat.Body.String())
			}
			delete(canonicalBody, "processingTime")
			delete(compatBody, "processingTime")
			if !reflect.DeepEqual(canonicalBody, compatBody) {
				t.Fatalf("body mismatch:\ncanonical=%v\ncompat=%v", canonicalBody, compatBody)
			}
		})
	}
}

// --- AC3: Bidirectional query and cursor pagination ---

func TestBidirectionalPagination(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Insert 50 bidirectional messages (A↔B)
	// Alice sends 25 messages to Bob, Bob sends 25 messages to Alice
	for i := 0; i < 25; i++ {
		// Alice → Bob
		pin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("ab_tx_%d:i0", i),
			Path:          "/private/chat/simplemsg",
			Operation:     "create",
			CreateAddress: "1AliceAddr",
			CreateMetaId:  "alice_pag",
			GlobalMetaId:  "global_alice_pag",
			ChainName:     "btc",
			Timestamp:     1700000000000 + int64(i),
			ContentBody: mustMarshal(t, SimpleMsg{
				From:        "alice_pag",
				To:          "bob_pag",
				Content:     fmt.Sprintf("Alice message %d", i),
				ContentType: "text/plain",
				Encrypt:     "none",
			}),
		}
		agg.HandleBlockPin(pin)

		// Bob → Alice
		pin2 := &aggregator.PinInscription{
			Id:            fmt.Sprintf("ba_tx_%d:i0", i),
			Path:          "/private/chat/simplemsg",
			Operation:     "create",
			CreateAddress: "1BobAddr",
			CreateMetaId:  "bob_pag",
			GlobalMetaId:  "global_bob_pag",
			ChainName:     "btc",
			Timestamp:     1700000100000 + int64(i),
			ContentBody: mustMarshal(t, SimpleMsg{
				From:        "bob_pag",
				To:          "alice_pag",
				Content:     fmt.Sprintf("Bob message %d", i),
				ContentType: "text/plain",
				Encrypt:     "none",
			}),
		}
		agg.HandleBlockPin(pin2)
	}

	// Alice queries her chat with Bob — should see 50 messages (25 from Alice, 25 from Bob)
	w := performRequest(t, router, "GET",
		"/api/group-chat/private-chat-list?metaId=alice_pag&otherMetaId=bob_pag&cursor=&size=20")
	var resp struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %d: %s", resp.Code, w.Body.String())
	}
	if resp.Data.Total != 50 {
		t.Errorf("expected total=50, got %d", resp.Data.Total)
	}
	if len(resp.Data.List) != 20 {
		t.Errorf("page 1: expected 20 messages, got %d", len(resp.Data.List))
	}

	// Verify no C's messages leaked
	for _, msg := range resp.Data.List {
		if msg.From != "alice_pag" && msg.From != "bob_pag" {
			t.Errorf("unexpected sender %q leaked into A-B chat", msg.From)
		}
		if msg.To != "alice_pag" && msg.To != "bob_pag" {
			t.Errorf("unexpected receiver %q leaked into A-B chat", msg.To)
		}
	}

	// Iterate through all pages and verify cursor behavior
	cursor := resp.Data.NextCursor
	page := 1
	seenCount := len(resp.Data.List)

	for cursor != "" && page < 5 {
		w2 := performRequest(t, router, "GET",
			fmt.Sprintf("/api/group-chat/private-chat-list?metaId=alice_pag&otherMetaId=bob_pag&cursor=%s&size=20", cursor))
		var pageResp struct {
			Code int                   `json:"code"`
			Data PrivateChatListResult `json:"data"`
		}
		json.Unmarshal(w2.Body.Bytes(), &pageResp)

		if pageResp.Code != 0 {
			t.Fatalf("page %d failed: code=%d", page+1, pageResp.Code)
		}

		seenCount += len(pageResp.Data.List)
		cursor = pageResp.Data.NextCursor
		page++
	}

	if seenCount != 50 {
		t.Errorf("expected 50 total messages across pages, got %d", seenCount)
	}

	t.Logf("Bidirectional pagination OK: total=%d, %d pages, %d messages seen",
		resp.Data.Total, page, seenCount)
}

func TestPrivateChatListTimestampPagination(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	for i, ts := range []int64{1000, 2000, 3000} {
		pin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("timestamp_page_%d:i0", i),
			Path:          "/private/chat/simplemsg",
			Operation:     "create",
			CreateAddress: "1AliceAddr",
			CreateMetaId:  "alice_ts",
			GlobalMetaId:  "global_alice_ts",
			ChainName:     "mvc",
			Timestamp:     ts,
			ContentBody: mustMarshal(t, SimpleMsg{
				From:        "alice_ts",
				To:          "bob_ts",
				Content:     fmt.Sprintf("message %d", ts),
				ContentType: "text/plain",
				Encrypt:     "none",
			}),
		}
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin %d failed: %v", i, err)
		}
	}

	w := performRequest(t, router, "GET",
		"/api/private-chat/messages?metaId=alice_ts&otherMetaId=bob_ts&timestamp=2000&size=20")
	var resp struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %d: %s", resp.Code, w.Body.String())
	}
	if len(resp.Data.List) != 1 {
		t.Fatalf("expected only messages older than timestamp, got %d: %s", len(resp.Data.List), w.Body.String())
	}
	if resp.Data.List[0].Content != "message 1000" {
		t.Fatalf("content: got %q want message 1000", resp.Data.List[0].Content)
	}
}

// --- AC4: Index-based query ---

func TestPrivateChatListByIndex(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Insert 50 messages from Alice to Bob
	for i := 0; i < 50; i++ {
		pin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("idx_tx_%d:i0", i),
			Path:          "/private/chat/simplemsg",
			Operation:     "create",
			CreateAddress: "1AliceAddr",
			CreateMetaId:  "alice_idx",
			GlobalMetaId:  "global_alice_idx",
			ChainName:     "btc",
			Timestamp:     1700000000000 + int64(i),
			ContentBody: mustMarshal(t, SimpleMsg{
				From:        "alice_idx",
				To:          "bob_idx",
				Content:     fmt.Sprintf("IndexMessage %d", i),
				ContentType: "text/plain",
				Encrypt:     "none",
			}),
		}
		agg.HandleBlockPin(pin)
	}

	// Query by index: startIndex=0, size=20 (idchat expects ascending message indexes 0..19)
	w := performRequest(t, router, "GET",
		"/api/group-chat/private-chat-list-by-index?metaId=alice_idx&otherMetaId=bob_idx&startIndex=0&size=20")
	var resp struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("index query failed: code=%d", resp.Code)
	}
	if len(resp.Data.List) != 20 {
		t.Errorf("expected 20 messages, got %d", len(resp.Data.List))
	}
	if resp.Data.NextTimestamp != 19 {
		t.Errorf("expected nextTimestamp(last index)=19, got %d", resp.Data.NextTimestamp)
	}
	if resp.Data.List[0].Content != "IndexMessage 0" || resp.Data.List[0].Index != 0 {
		t.Errorf("expected index 0 as first, got content=%q index=%d", resp.Data.List[0].Content, resp.Data.List[0].Index)
	}
	if resp.Data.List[19].Content != "IndexMessage 19" || resp.Data.List[19].Index != 19 {
		t.Errorf("expected index 19 as 20th, got content=%q index=%d", resp.Data.List[19].Content, resp.Data.List[19].Index)
	}

	// Query next page: startIndex=20, size=20
	w2 := performRequest(t, router, "GET",
		"/api/group-chat/private-chat-list-by-index?metaId=alice_idx&otherMetaId=bob_idx&startIndex=20&size=20")
	var resp2 struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &resp2)

	if len(resp2.Data.List) != 20 {
		t.Errorf("page 2: expected 20 messages, got %d", len(resp2.Data.List))
	}
	if resp2.Data.NextTimestamp != 39 {
		t.Errorf("page 2: expected nextTimestamp(last index)=39, got %d", resp2.Data.NextTimestamp)
	}
	if resp2.Data.List[0].Content != "IndexMessage 20" || resp2.Data.List[0].Index != 20 {
		t.Errorf("page 2 first: expected index 20, got content=%q index=%d", resp2.Data.List[0].Content, resp2.Data.List[0].Index)
	}

	t.Logf("Index query OK: page1=%d msgs, page2=%d msgs",
		len(resp.Data.List), len(resp2.Data.List))
}

// --- AC5: Chat homes (conversation list) ---

func TestPrivateChatHomes(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Alice chats with Bob
	pin1 := &aggregator.PinInscription{
		Id:            "home_tx1:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1AliceAddr",
		CreateMetaId:  "alice_home",
		GlobalMetaId:  "global_alice_home",
		ChainName:     "btc",
		Timestamp:     1700000000000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_home",
			To:          "bob_home",
			Content:     "Hi Bob",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	agg.HandleBlockPin(pin1)

	// Alice chats with Charlie
	pin2 := &aggregator.PinInscription{
		Id:            "home_tx2:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1AliceAddr",
		CreateMetaId:  "alice_home",
		GlobalMetaId:  "global_alice_home",
		ChainName:     "btc",
		Timestamp:     1700000010000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_home",
			To:          "charlie_home",
			Content:     "Hey Charlie",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	agg.HandleBlockPin(pin2)

	// Bob replies to Alice (this should update Bob's last message in Alice's home)
	pin3 := &aggregator.PinInscription{
		Id:            "home_tx3:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1BobAddr",
		CreateMetaId:  "bob_home",
		GlobalMetaId:  "global_bob_home",
		ChainName:     "btc",
		Timestamp:     1700000020000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "bob_home",
			To:          "alice_home",
			Content:     "Hi Alice!",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	agg.HandleBlockPin(pin3)

	// Query Alice's homes
	w := performRequest(t, router, "GET", "/api/group-chat/chat/homes/alice_home")
	var resp struct {
		Code int `json:"code"`
		Data struct {
			List []PrivateChatHome `json:"list"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("chat homes failed: code=%d: %s", resp.Code, w.Body.String())
	}
	if len(resp.Data.List) != 2 {
		t.Errorf("expected 2 conversation partners, got %d", len(resp.Data.List))
	}

	// Verify both partners present
	foundBob := false
	foundCharlie := false
	for _, home := range resp.Data.List {
		if home.MetaId == "bob_home" {
			foundBob = true
			if home.LastMessage.Content != "Hi Alice!" {
				t.Errorf("expected Bob's last message='Hi Alice!', got %q", home.LastMessage.Content)
			}
		}
		if home.MetaId == "charlie_home" {
			foundCharlie = true
			if home.LastMessage.Content != "Hey Charlie" {
				t.Errorf("expected Charlie's last message='Hey Charlie', got %q", home.LastMessage.Content)
			}
		}
	}
	if !foundBob {
		t.Error("expected bob_home in homes")
	}
	if !foundCharlie {
		t.Error("expected charlie_home in homes")
	}

	t.Logf("Chat homes OK: %d partners, bob=%v charlie=%v",
		len(resp.Data.List), foundBob, foundCharlie)

	// Also verify Bob's homes (Bob only has Alice)
	w2 := performRequest(t, router, "GET", "/api/group-chat/chat/homes/bob_home")
	var resp2 struct {
		Code int `json:"code"`
		Data struct {
			List []PrivateChatHome `json:"list"`
		} `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &resp2)

	if resp2.Code != 0 {
		t.Fatalf("bob homes failed: code=%d", resp2.Code)
	}
	if len(resp2.Data.List) != 1 {
		t.Errorf("expected 1 partner for Bob, got %d", len(resp2.Data.List))
	}
}

// --- AC6: Private group paths ---

func TestPrivateGroupPaths(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Alice chats with Bob
	pin1 := &aggregator.PinInscription{
		Id:            "path_tx1:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1AliceAddr",
		CreateMetaId:  "alice_path",
		GlobalMetaId:  "global_alice_path",
		ChainName:     "btc",
		Timestamp:     1700000000000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_path",
			To:          "bob_path",
			Content:     "Hello",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	agg.HandleBlockPin(pin1)

	// Alice chats with Charlie
	pin2 := &aggregator.PinInscription{
		Id:            "path_tx2:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1AliceAddr",
		CreateMetaId:  "alice_path",
		GlobalMetaId:  "global_alice_path",
		ChainName:     "btc",
		Timestamp:     1700000010000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_path",
			To:          "charlie_path",
			Content:     "Hey",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	agg.HandleBlockPin(pin2)

	// Query Alice's paths
	w := performRequest(t, router, "GET", "/api/group-chat/private-group-paths?metaId=alice_path")
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Total int                 `json:"total"`
			List  []*PrivateGroupPath `json:"list"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("paths query failed: code=%d: %s", resp.Code, w.Body.String())
	}
	if resp.Data.Total != 2 || len(resp.Data.List) != 2 {
		t.Errorf("expected 2 paths for Alice, got total=%d len=%d: %v", resp.Data.Total, len(resp.Data.List), resp.Data.List)
	}

	t.Logf("Private group paths OK: %d paths %v", len(resp.Data.List), resp.Data.List)
}

// --- AC7: Socket push notification ---

func TestSocketPushNotification(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	// Process a private chat pin and verify NotifyEvent
	pin := &aggregator.PinInscription{
		Id:            "push_priv:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1AliceSender",
		CreateMetaId:  "alice_push",
		GlobalMetaId:  "global_alice_push",
		ChainName:     "btc",
		Timestamp:     1700000000000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_push",
			To:          "bob_push",
			Content:     "Push notification test",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}

	if evt == nil {
		t.Fatal("expected NotifyEvent, got nil")
	}
	if evt.Type != "WS_SERVER_NOTIFY_PRIVATE_CHAT" {
		t.Errorf("expected Type='WS_SERVER_NOTIFY_PRIVATE_CHAT', got %q", evt.Type)
	}
	if evt.MetaId != "bob_push" {
		t.Errorf("expected MetaId='bob_push', got %q", evt.MetaId)
	}

	// Verify payload contains expected fields
	payload, ok := evt.Payload.(*PrivateMessage)
	if !ok {
		t.Fatal("expected payload to be *PrivateMessage")
	}
	if payload.From != "alice_push" {
		t.Errorf("expected payload.From='alice_push', got %q", payload.From)
	}
	if payload.Content != "Push notification test" {
		t.Errorf("expected payload.Content='Push notification test', got %q", payload.Content)
	}
	if payload.ToGlobalMetaId != "bob_push" {
		t.Errorf("expected payload.ToGlobalMetaId='bob_push', got %q", payload.ToGlobalMetaId)
	}

	// Check that event was delivered to notify channel
	select {
	case chEvent := <-agg.NotifyChannel():
		if chEvent.Type != "WS_SERVER_NOTIFY_PRIVATE_CHAT" {
			t.Errorf("channel event: expected Type='WS_SERVER_NOTIFY_PRIVATE_CHAT', got %q", chEvent.Type)
		}
		t.Logf("Socket push OK: type=%s metaId=%s", chEvent.Type, chEvent.MetaId)
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for notify event on channel")
	}
}

// --- Additional edge-case tests ---

func TestNameMethod(t *testing.T) {
	agg := &Aggregator{}
	if agg.Name() != "privatechat" {
		t.Errorf("expected Name()='privatechat', got %q", agg.Name())
	}
}

func TestNotifyChannel(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	ch := agg.NotifyChannel()
	if ch == nil {
		t.Fatal("NotifyChannel() returned nil")
	}

	// Channel should be empty initially
	select {
	case <-ch:
		t.Error("unexpected event on notify channel")
	case <-time.After(10 * time.Millisecond):
		// Expected: no event
	}
}

func TestHandleBlockPin_Nil(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	evt, err := agg.HandleBlockPin(nil)
	if err != nil {
		t.Errorf("HandleBlockPin(nil) should not error: %v", err)
	}
	if evt != nil {
		t.Error("HandleBlockPin(nil) should return nil event")
	}
}

func TestHandleBlockPin_UnknownPath(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Path:        "/unknown/path",
		Operation:   "create",
		ContentBody: []byte(`{}`),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Errorf("HandleBlockPin(unknown) should not error: %v", err)
	}
	if evt != nil {
		t.Error("HandleBlockPin(unknown) should return nil event")
	}
}

func TestHandleBlockPin_EmptyPath(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Path:        "",
		Operation:   "create",
		ContentBody: []byte(`{}`),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Errorf("HandleBlockPin(empty path) should not error: %v", err)
	}
	if evt != nil {
		t.Error("HandleBlockPin(empty path) should return nil event")
	}
}

func TestHandleBlockPin_InvalidJSON(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Id:            "badjson:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1Addr",
		CreateMetaId:  "alice",
		GlobalMetaId:  "global_alice",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody:   []byte(`not valid json`),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Errorf("HandleBlockPin(bad JSON) should not error: %v", err)
	}
	if evt != nil {
		t.Error("HandleBlockPin(bad JSON) should return nil event (malformed input)")
	}
}

func TestHandleBlockPin_EmptyContentBody(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Id:            "empty_body:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1Addr",
		CreateMetaId:  "alice",
		GlobalMetaId:  "global_alice",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody:   []byte(`{}`),
	}

	// Should gracefully handle empty To field
	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Errorf("HandleBlockPin(empty body) should not error: %v", err)
	}
	if evt != nil {
		t.Errorf("HandleBlockPin(empty body) should return nil (no To field), got event type=%s", evt.Type)
	}
}

func TestHandleMempoolPin(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Id:            "mempool_tx:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1Alice",
		CreateMetaId:  "alice_mem",
		GlobalMetaId:  "global_alice_mem",
		ChainName:     "btc",
		Timestamp:     1700000000000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_mem",
			To:          "bob_mem",
			Content:     "Mempool message",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}

	evt, err := agg.HandleMempoolPin(pin)
	if err != nil {
		t.Fatalf("HandleMempoolPin failed: %v", err)
	}
	if evt == nil {
		t.Fatal("expected NotifyEvent from mempool pin, got nil")
	}
	if evt.Type != "WS_SERVER_NOTIFY_PRIVATE_CHAT" {
		t.Errorf("expected Type='WS_SERVER_NOTIFY_PRIVATE_CHAT', got %q", evt.Type)
	}
}

func TestPrivateChatListByIndex_OutOfRange(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Single message
	pin := &aggregator.PinInscription{
		Id:            "oor_tx:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1Alice",
		CreateMetaId:  "alice_oor",
		GlobalMetaId:  "global_alice_oor",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_oor",
			To:          "bob_oor",
			Content:     "test",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	agg.HandleBlockPin(pin)

	// Query out-of-range
	w := performRequest(t, router, "GET",
		"/api/group-chat/private-chat-list-by-index?metaId=alice_oor&otherMetaId=bob_oor&startIndex=999&size=20")
	var resp struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Errorf("expected code=0 for out-of-range, got %d", resp.Code)
	}
	if len(resp.Data.List) != 0 {
		t.Errorf("expected empty list, got %d items", len(resp.Data.List))
	}
	t.Logf("Out-of-range index OK: %d items", len(resp.Data.List))
}

func TestPrivateChatList_MissingParams(t *testing.T) {
	_, _, router := setupTestAggregator(t)

	// Missing metaId
	w := performRequest(t, router, "GET", "/api/group-chat/private-chat-list?otherMetaId=bob&cursor=&size=20")
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != 1 {
		t.Errorf("expected code=1 for missing metaId, got %d", resp.Code)
	}

	// Missing otherMetaId
	w2 := performRequest(t, router, "GET", "/api/group-chat/private-chat-list?metaId=alice&cursor=&size=20")
	var resp2 struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Code != 1 {
		t.Errorf("expected code=1 for missing otherMetaId, got %d", resp2.Code)
	}
}

func TestBidirectionalQuery_DirectionInvariance(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Alice sends to Bob
	pin := &aggregator.PinInscription{
		Id:            "dir_tx:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1AliceAddr",
		CreateMetaId:  "alice_dir",
		GlobalMetaId:  "global_alice_dir",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "alice_dir",
			To:          "bob_dir",
			Content:     "Direction test",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
	agg.HandleBlockPin(pin)

	// Query as Alice (metaId=alice_dir, otherMetaId=bob_dir)
	w1 := performRequest(t, router, "GET",
		"/api/group-chat/private-chat-list?metaId=alice_dir&otherMetaId=bob_dir&cursor=&size=20")
	var resp1 struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w1.Body.Bytes(), &resp1)

	// Query as Bob (metaId=bob_dir, otherMetaId=alice_dir) — should see the same message
	w2 := performRequest(t, router, "GET",
		"/api/group-chat/private-chat-list?metaId=bob_dir&otherMetaId=alice_dir&cursor=&size=20")
	var resp2 struct {
		Code int                   `json:"code"`
		Data PrivateChatListResult `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &resp2)

	if resp1.Data.Total != 1 || resp2.Data.Total != 1 {
		t.Errorf("direction invariance failed: alice sees %d, bob sees %d",
			resp1.Data.Total, resp2.Data.Total)
	}

	if len(resp1.Data.List) > 0 && len(resp2.Data.List) > 0 {
		if resp1.Data.List[0].Content != resp2.Data.List[0].Content {
			t.Error("Alice and Bob should see the same message content")
		}
	}

	t.Logf("Direction invariance OK: alice=%d msgs, bob=%d msgs",
		resp1.Data.Total, resp2.Data.Total)
}

func TestPrivateChatHomes_EmptyUser(t *testing.T) {
	_, _, router := setupTestAggregator(t)

	w := performRequest(t, router, "GET", "/api/group-chat/chat/homes/no_one")
	var resp struct {
		Code int `json:"code"`
		Data struct {
			List []PrivateChatHome `json:"list"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Errorf("expected code=0 for empty user, got %d", resp.Code)
	}
	if len(resp.Data.List) != 0 {
		t.Errorf("expected empty homes list, got %d items", len(resp.Data.List))
	}
}

func TestPrivateGroupPaths_EmptyUser(t *testing.T) {
	_, _, router := setupTestAggregator(t)

	w := performRequest(t, router, "GET", "/api/group-chat/private-group-paths?metaId=no_one")
	var resp struct {
		Code int      `json:"code"`
		Data []string `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Errorf("expected code=0 for empty user, got %d", resp.Code)
	}
	if len(resp.Data) != 0 {
		t.Errorf("expected empty paths, got %d items", len(resp.Data))
	}
}
