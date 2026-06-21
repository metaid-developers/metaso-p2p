package userinfo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

type stubRemoteProfileLookup struct {
	lookupByMetaId       func(context.Context, string) (*UserProfile, error)
	lookupByAddress      func(context.Context, string) (*UserProfile, error)
	lookupByGlobalMetaId func(context.Context, string) (*UserProfile, error)
}

func (s stubRemoteProfileLookup) LookupByMetaId(ctx context.Context, metaid string) (*UserProfile, error) {
	if s.lookupByMetaId == nil {
		return nil, nil
	}
	return s.lookupByMetaId(ctx, metaid)
}

func (s stubRemoteProfileLookup) LookupByAddress(ctx context.Context, address string) (*UserProfile, error) {
	if s.lookupByAddress == nil {
		return nil, nil
	}
	return s.lookupByAddress(ctx, address)
}

func (s stubRemoteProfileLookup) LookupByGlobalMetaId(ctx context.Context, globalMetaId string) (*UserProfile, error) {
	if s.lookupByGlobalMetaId == nil {
		return nil, nil
	}
	return s.lookupByGlobalMetaId(ctx, globalMetaId)
}

// setupTestAggregator creates a test-ready userinfo aggregator with a real Pebble store and cache.
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

// performRequest is a test helper.
func performRequest(t *testing.T, router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// --- Acceptance Criteria #4: UserInfo database storage ---

func TestHandleBlockPin_InitAndName(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	// Simulate an init pin (path="/").
	initPin := &aggregator.PinInscription{
		Path:      "/",
		Operation: "init",
		MetaId:    "testuser123",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "tx1:i0",
	}

	_, err := agg.HandleBlockPin(initPin)
	if err != nil {
		t.Fatalf("HandleBlockPin(init) failed: %v", err)
	}

	// Simulate a /info/name pin.
	namePin := &aggregator.PinInscription{
		Path:        "/info/name",
		Operation:   "create",
		MetaId:      "testuser123",
		Address:     "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName:   "btc",
		ContentBody: []byte("Alice"),
		Id:          "tx2:i0",
	}

	_, err = agg.HandleBlockPin(namePin)
	if err != nil {
		t.Fatalf("HandleBlockPin(name) failed: %v", err)
	}

	// Verify the profile was stored in Pebble with the name field.
	raw, err := store.Get(namespace, profileKey("testuser123"))
	if err != nil {
		t.Fatalf("store.Get failed: %v", err)
	}
	if raw == nil {
		t.Fatal("profile not found in store")
	}

	var profile UserProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		t.Fatalf("failed to unmarshal profile: %v", err)
	}

	if profile.Name != "Alice" {
		t.Errorf("expected name 'Alice', got %q", profile.Name)
	}
	if profile.MetaID != "testuser123" {
		t.Errorf("expected metaid 'testuser123', got %q", profile.MetaID)
	}
	t.Logf("stored profile: name=%s metaid=%s globalMetaId=%s", profile.Name, profile.MetaID, profile.GlobalMetaID)
}

// --- Acceptance Criteria #5: UserInfo HTTP response ---

func TestHandleMetaIdInfo_ReturnsCorrectFormat(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// First, store a profile via HandleBlockPin.
	initPin := &aggregator.PinInscription{
		Path:      "/",
		Operation: "init",
		MetaId:    "testuser456",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "tx3:i0",
	}
	agg.HandleBlockPin(initPin)

	// Hit the HTTP endpoint.
	w := performRequest(t, router, "GET", "/api/info/metaid/testuser456")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Decode response.
	var resp struct {
		Code           int         `json:"code"`
		Data           UserProfile `json:"data"`
		Message        string      `json:"message"`
		ProcessingTime int64       `json:"processingTime"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Code != 1 {
		t.Errorf("expected code=1 (meta-file-system success), got %d", resp.Code)
	}
	if resp.ProcessingTime <= 0 {
		t.Errorf("expected processingTime > 0, got %d", resp.ProcessingTime)
	}
	if resp.Data.MetaID != "testuser456" {
		t.Errorf("expected metaid 'testuser456', got %q", resp.Data.MetaID)
	}
	t.Logf("HTTP response: code=%d metaid=%s processingTime=%d", resp.Code, resp.Data.MetaID, resp.ProcessingTime)
}

// TestHandleMetaIdInfo_NotFound tests error response for unknown user.
// The /info/* endpoints use code=40400 not_found to stay wire-compatible with
// meta-file-system (idchat's metafileIndexerApi client rejects any non-1 code).
func TestHandleMetaIdInfo_NotFound(t *testing.T) {
	_, store, router := setupTestAggregator(t)
	defer store.Close()

	w := performRequest(t, router, "GET", "/api/info/metaid/nonexistent")

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 40400 {
		t.Errorf("expected code=40400 (meta-file-system not_found), got %d", resp.Code)
	}
	t.Logf("not found response: code=%d message=%s", resp.Code, resp.Message)
}

// --- Acceptance Criteria #7: GlobalMetaId ---

func TestHandleBlockPin_GeneratesGlobalMetaId(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	// A valid BTC P2PKH address should generate an "id"-prefixed GlobalMetaId.
	initPin := &aggregator.PinInscription{
		Path:      "/",
		Operation: "init",
		MetaId:    "testuser_globalid",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "tx_global:i0",
	}

	_, err := agg.HandleBlockPin(initPin)
	if err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}

	raw, err := store.Get(namespace, profileKey("testuser_globalid"))
	if err != nil || raw == nil {
		t.Fatal("profile not found in store")
	}

	var profile UserProfile
	json.Unmarshal(raw, &profile)

	if profile.GlobalMetaID == "" {
		t.Error("GlobalMetaID should not be empty")
	}
	if len(profile.GlobalMetaID) < 3 || profile.GlobalMetaID[:2] != "id" {
		t.Errorf("GlobalMetaID should start with 'id', got %q", profile.GlobalMetaID)
	}
	t.Logf("GlobalMetaID: %s", profile.GlobalMetaID)
}

func TestHandleBlockPin_StoresPersonaInfoPaths(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	metaid := "meta_persona"
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	global := idaddress.EncodeGlobalMetaId(address, "mvc")

	pins := []*aggregator.PinInscription{
		{Id: "init:i0", Path: "/", Operation: "init", MetaId: metaid, Address: address, ChainName: "mvc"},
		{Id: "role:i0", Path: "/info/role", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte("Public role")},
		{Id: "soul:i0", Path: "/info/soul", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte("Calm soul")},
		{Id: "goal:i0", Path: "/info/goal", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte("Help users")},
		{Id: "skills:i0", Path: "/info/chatSkills", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"allowChatSkills":["metabot-post-buzz"]}`)},
		{Id: "llm:i0", Path: "/info/LLM", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"primaryProvider":"deepseek","displayName":"DeepSeek"}`)},
		{Id: "home:i0", Path: "/info/homepage", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"uri":"metaapp://abc","renderer":"html","contentType":"text/html"}`)},
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Path, err)
		}
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile.Role != "Public role" || profile.RoleId != "role:i0" {
		t.Fatalf("role not stored: %#v", profile)
	}
	if profile.Soul != "Calm soul" || profile.SoulId != "soul:i0" || profile.Goal != "Help users" || profile.GoalId != "goal:i0" {
		t.Fatalf("persona text not stored: %#v", profile)
	}
	if profile.ChatSkills != `{"allowChatSkills":["metabot-post-buzz"]}` || profile.ChatSkillsId != "skills:i0" {
		t.Fatalf("chatSkills not stored: %#v", profile)
	}
	if profile.LLM != `{"primaryProvider":"deepseek","displayName":"DeepSeek"}` || profile.LLMId != "llm:i0" {
		t.Fatalf("llm not stored: %#v", profile)
	}
	if profile.Homepage != `{"uri":"metaapp://abc","renderer":"html","contentType":"text/html"}` || profile.HomepageId != "home:i0" {
		t.Fatalf("homepage not stored: %#v", profile)
	}
}

func TestHandleBlockPin_StoresV3BotInfoPathsAndClears(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	metaid := "meta_v3_bot"
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"

	pins := []*aggregator.PinInscription{
		{Id: "init-v3:i0", Path: "/", Operation: "init", MetaId: metaid, Address: address, ChainName: "mvc"},
		{Id: "avatar-v3:i0", Path: "/info/avatar", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentType: "image/png", ContentBody: []byte{0x89, 'P', 'N', 'G'}},
		{Id: "llm-v3:i0", Path: "/info/llm", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"provider":"deepseek","model":"v3"}`)},
		{Id: "persona-v3:i0", Path: "/info/persona", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"style":"direct","language":"zh-CN"}`)},
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Path, err)
		}
	}

	profile, err := agg.LookupByMetaId(metaid)
	if err != nil {
		t.Fatalf("LookupByMetaId: %v", err)
	}
	if profile.Avatar != "/content/avatar-v3:i0" || profile.AvatarId != "avatar-v3:i0" || profile.AvatarContentType != "image/png" {
		t.Fatalf("avatar fields not stored: %#v", profile)
	}
	if profile.LLM != `{"provider":"deepseek","model":"v3"}` || profile.LLMId != "llm-v3:i0" {
		t.Fatalf("canonical lowercase llm not stored: %#v", profile)
	}
	if profile.Persona != `{"style":"direct","language":"zh-CN"}` || profile.PersonaId != "persona-v3:i0" {
		t.Fatalf("persona raw JSON not stored: %#v", profile)
	}

	clearPins := []*aggregator.PinInscription{
		{Id: "avatar-clear:i0", Path: "/info/avatar", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc"},
		{Id: "llm-clear:i0", Path: "/info/LLM", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc"},
		{Id: "persona-clear:i0", Path: "/info/persona", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc"},
	}
	for _, pin := range clearPins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(clear %s): %v", pin.Path, err)
		}
	}

	profile, err = agg.LookupByMetaId(metaid)
	if err != nil {
		t.Fatalf("LookupByMetaId after clears: %v", err)
	}
	if profile.Avatar != "" || profile.AvatarId != "" || profile.AvatarContentType != "" {
		t.Fatalf("empty avatar body should clear avatar fields: %#v", profile)
	}
	if profile.LLM != "" || profile.LLMId != "" {
		t.Fatalf("empty LLM body should clear llm fields: %#v", profile)
	}
	if profile.Persona != "" || profile.PersonaId != "" {
		t.Fatalf("empty persona body should clear persona fields: %#v", profile)
	}
}

func TestHandleMempoolPin_StoresPersona(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	metaid := "mempool_persona_user"
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	global := idaddress.EncodeGlobalMetaId(address, "mvc")

	if _, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Id:        "mempool-persona-init:i0",
		Path:      "/",
		Operation: "init",
		MetaId:    metaid,
		Address:   address,
		ChainName: "mvc",
	}); err != nil {
		t.Fatalf("HandleBlockPin(init): %v", err)
	}

	evt, err := agg.HandleMempoolPin(&aggregator.PinInscription{
		Id:           "mempool-persona:i0",
		Path:         "/info/persona",
		Operation:    "create",
		MetaId:       metaid,
		Address:      address,
		GlobalMetaId: global,
		ContentBody:  []byte(`{"style":"pending"}`),
		ChainName:    "mvc",
	})
	if err != nil {
		t.Fatalf("HandleMempoolPin(persona): %v", err)
	}
	if evt != nil {
		t.Fatal("HandleMempoolPin should return nil event for userinfo")
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile.Persona != `{"style":"pending"}` || profile.PersonaId != "mempool-persona:i0" {
		t.Fatalf("mempool persona not stored: %#v", profile)
	}
}

func TestDefaultBackfillPaths_IncludesV3BotInfoPaths(t *testing.T) {
	paths := DefaultBackfillPaths()
	for _, want := range []string{"/info/LLM", "/info/llm", "/info/persona"} {
		found := false
		for _, got := range paths {
			if got == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("DefaultBackfillPaths() missing %q: %v", want, paths)
		}
	}
}

func TestHandleBlockPin_PreservesEmptyChatPubkeyPinId(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	metaid := "meta_empty_chatpubkey"
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	if _, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Id:        "empty-chatpubkey-init:i0",
		Path:      "/",
		Operation: "init",
		MetaId:    metaid,
		Address:   address,
		ChainName: "mvc",
	}); err != nil {
		t.Fatalf("HandleBlockPin(init): %v", err)
	}

	if _, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Id:        "empty-chatpubkey:i0",
		Path:      "/info/chatpubkey",
		Operation: "create",
		MetaId:    metaid,
		Address:   address,
		ChainName: "mvc",
	}); err != nil {
		t.Fatalf("HandleBlockPin(chatpubkey): %v", err)
	}

	profile, err := agg.LookupByMetaId(metaid)
	if err != nil {
		t.Fatalf("LookupByMetaId: %v", err)
	}
	if profile.ChatPublicKey != "" || profile.ChatPublicKeyId != "empty-chatpubkey:i0" {
		t.Fatalf("empty chatpubkey body should preserve pin id behavior: %#v", profile)
	}
}

func TestLookupByGlobalMetaId_UsesReverseIndex(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	global := idaddress.EncodeGlobalMetaId(address, "mvc")
	if _, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Id: "init:i0", Path: "/", Operation: "init", MetaId: "meta_reverse", Address: address, ChainName: "mvc",
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := store.Get(namespace, globalMetaIdKey(global))
	if err != nil {
		t.Fatalf("reverse globalMetaId index missing: %v", err)
	}
	if string(raw) != "meta_reverse" {
		t.Fatalf("reverse globalMetaId index = %q, want meta_reverse", raw)
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile == nil || profile.MetaID != "meta_reverse" {
		t.Fatalf("reverse lookup returned %#v", profile)
	}
}

func TestLookupByGlobalMetaId_ReturnsErrorWhenRemoteLookupFailsWithoutLocalProfile(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	remoteErr := errors.New("remote lookup unavailable")
	agg.profileMode = "remote-only"
	agg.remoteLookup = stubRemoteProfileLookup{
		lookupByGlobalMetaId: func(context.Context, string) (*UserProfile, error) {
			return nil, remoteErr
		},
	}

	profile, err := agg.LookupByGlobalMetaId("idq1remoteonly")
	if !errors.Is(err, remoteErr) {
		t.Fatalf("LookupByGlobalMetaId error = %v, want %v", err, remoteErr)
	}
	if profile != nil {
		t.Fatalf("LookupByGlobalMetaId profile = %#v, want nil", profile)
	}
}

func TestLookupByGlobalMetaId_ReturnsLocalProfileWhenRemoteLookupFails(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	local := &UserProfile{
		MetaID:       "meta_local_remote_error",
		GlobalMetaID: "idq1localremoteerror",
		Address:      "address_local_remote_error",
		Name:         "Local Remote Error",
	}
	if err := agg.saveProfile(local); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	remoteErr := errors.New("remote lookup unavailable")
	agg.profileMode = "local-first"
	agg.allowRemoteFallback = true
	agg.remoteLookup = stubRemoteProfileLookup{
		lookupByGlobalMetaId: func(context.Context, string) (*UserProfile, error) {
			return nil, remoteErr
		},
	}

	profile, err := agg.LookupByGlobalMetaId(local.GlobalMetaID)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId error = %v, want nil", err)
	}
	if profile == nil {
		t.Fatal("LookupByGlobalMetaId returned nil profile")
	}
	if profile.MetaID != local.MetaID || profile.GlobalMetaID != local.GlobalMetaID || profile.Address != local.Address || profile.Name != local.Name {
		t.Fatalf("LookupByGlobalMetaId profile = %#v, want %#v", profile, local)
	}
}

func TestLookupLocalByGlobalMetaIdDoesNotFetchRemote(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	local := &UserProfile{
		MetaID:       "meta_local_only_global",
		GlobalMetaID: "idq1localonlyglobal",
		Address:      "address_local_only_global",
		Name:         "Local Only Global",
		AvatarId:     "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdefi0",
	}
	if err := agg.saveProfile(local); err != nil {
		t.Fatalf("seed profile: %v", err)
	}

	remoteCalls := 0
	agg.profileMode = "remote-only"
	agg.remoteLookup = stubRemoteProfileLookup{
		lookupByGlobalMetaId: func(context.Context, string) (*UserProfile, error) {
			remoteCalls++
			return &UserProfile{Name: "Remote Should Not Be Used"}, nil
		},
	}

	profile, err := agg.LookupLocalByGlobalMetaId(local.GlobalMetaID)
	if err != nil {
		t.Fatalf("LookupLocalByGlobalMetaId: %v", err)
	}
	if profile == nil {
		t.Fatal("LookupLocalByGlobalMetaId returned nil")
	}
	if profile.MetaID != local.MetaID || profile.Name != local.Name || profile.AvatarId != local.AvatarId {
		t.Fatalf("LookupLocalByGlobalMetaId profile = %#v, want local %#v", profile, local)
	}
	if remoteCalls != 0 {
		t.Fatalf("remote lookup calls = %d, want 0", remoteCalls)
	}
}

func TestLookupByGlobalMetaId_UsesReverseIndexWithoutScanMatch(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	indexedGlobal := "idq1indexedglobal"
	if err := agg.saveProfile(&UserProfile{
		MetaID:       "meta_index_only_global",
		GlobalMetaID: indexedGlobal,
		Address:      "address_index_only_global",
		Name:         "Index Only Global",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := store.Set(namespace, globalMetaIdKey(indexedGlobal), []byte("meta_index_only_global")); err != nil {
		t.Fatalf("seed globalMetaId index: %v", err)
	}
	agg.scanProfiles = func(match func(*UserProfile) bool) (*UserProfile, error) {
		t.Fatal("valid globalMetaId reverse index lookup should not scan profiles")
		return nil, nil
	}

	profile, err := agg.LookupByGlobalMetaId("  " + indexedGlobal + "  ")
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile == nil || profile.MetaID != "meta_index_only_global" {
		t.Fatalf("reverse index lookup returned %#v", profile)
	}
}

func TestLookupByAddress_UsesReverseIndexWithoutScanMatch(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	indexedAddress := "address_indexed_only"
	if err := agg.saveProfile(&UserProfile{
		MetaID:       "meta_index_only_address",
		GlobalMetaID: "idq1indexonlyaddress",
		Address:      indexedAddress,
		Name:         "Index Only Address",
	}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if err := store.Set(namespace, addressKey(indexedAddress), []byte("meta_index_only_address")); err != nil {
		t.Fatalf("seed address index: %v", err)
	}
	agg.scanProfiles = func(match func(*UserProfile) bool) (*UserProfile, error) {
		t.Fatal("valid address reverse index lookup should not scan profiles")
		return nil, nil
	}

	profile, err := agg.LookupByAddress("  " + indexedAddress + "  ")
	if err != nil {
		t.Fatalf("LookupByAddress: %v", err)
	}
	if profile == nil || profile.MetaID != "meta_index_only_address" {
		t.Fatalf("reverse index lookup returned %#v", profile)
	}
}

func TestLookupByGlobalMetaId_FallsBackToScanWhenIndexedProfileMismatches(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	global := "idq1mismatchedglobalfallback"
	if err := agg.saveProfile(&UserProfile{
		MetaID:       "meta_wrong_global",
		GlobalMetaID: "idq1wrongglobal",
		Address:      "wrong_global_address",
		Name:         "Wrong Global",
	}); err != nil {
		t.Fatalf("seed wrong indexed profile: %v", err)
	}
	if err := store.Set(namespace, globalMetaIdKey(global), []byte("meta_wrong_global")); err != nil {
		t.Fatalf("seed mismatched globalMetaId index: %v", err)
	}
	if err := store.Set(namespace, profileKey("meta_scan_global_match"), mustMarshalProfile(t, &UserProfile{
		MetaID:       "meta_scan_global_match",
		GlobalMetaID: global,
		Address:      "scan_global_match_address",
		Name:         "Scan Global Match",
	})); err != nil {
		t.Fatalf("seed scan profile: %v", err)
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile == nil || profile.MetaID != "meta_scan_global_match" {
		t.Fatalf("scan fallback returned %#v", profile)
	}
}

func TestLookupByAddress_FallsBackToScanWhenIndexedProfileMismatches(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	address := "address_mismatched_fallback"
	if err := agg.saveProfile(&UserProfile{
		MetaID:       "meta_wrong_address",
		GlobalMetaID: "idq1wrongaddress",
		Address:      "wrong_address",
		Name:         "Wrong Address",
	}); err != nil {
		t.Fatalf("seed wrong indexed profile: %v", err)
	}
	if err := store.Set(namespace, addressKey(address), []byte("meta_wrong_address")); err != nil {
		t.Fatalf("seed mismatched address index: %v", err)
	}
	if err := store.Set(namespace, profileKey("meta_scan_address_match"), mustMarshalProfile(t, &UserProfile{
		MetaID:       "meta_scan_address_match",
		GlobalMetaID: "idq1scanaddressmatch",
		Address:      address,
		Name:         "Scan Address Match",
	})); err != nil {
		t.Fatalf("seed scan profile: %v", err)
	}

	profile, err := agg.LookupByAddress(address)
	if err != nil {
		t.Fatalf("LookupByAddress: %v", err)
	}
	if profile == nil || profile.MetaID != "meta_scan_address_match" {
		t.Fatalf("scan fallback returned %#v", profile)
	}
}

func TestLookupByGlobalMetaId_FallsBackToScanWhenIndexedProfileIsCorrupt(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	global := "idq1corruptglobalfallback"
	if err := store.Set(namespace, profileKey("meta_corrupt_global"), []byte("{not-json")); err != nil {
		t.Fatalf("seed corrupt profile: %v", err)
	}
	if err := store.Set(namespace, globalMetaIdKey(global), []byte("meta_corrupt_global")); err != nil {
		t.Fatalf("seed corrupt globalMetaId index: %v", err)
	}
	if err := store.Set(namespace, profileKey("meta_scan_global"), mustMarshalProfile(t, &UserProfile{
		MetaID:       "meta_scan_global",
		GlobalMetaID: global,
		Address:      "scan_global_address",
		Name:         "Scan Global",
	})); err != nil {
		t.Fatalf("seed scan profile: %v", err)
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile == nil || profile.MetaID != "meta_scan_global" {
		t.Fatalf("scan fallback returned %#v", profile)
	}
}

func TestLookupByAddress_FallsBackToScanWhenIndexedProfileIsCorrupt(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	address := "address_corrupt_fallback"
	if err := store.Set(namespace, profileKey("meta_corrupt_address"), []byte("{not-json")); err != nil {
		t.Fatalf("seed corrupt profile: %v", err)
	}
	if err := store.Set(namespace, addressKey(address), []byte("meta_corrupt_address")); err != nil {
		t.Fatalf("seed corrupt address index: %v", err)
	}
	if err := store.Set(namespace, profileKey("meta_scan_address"), mustMarshalProfile(t, &UserProfile{
		MetaID:       "meta_scan_address",
		GlobalMetaID: "idq1scanaddress",
		Address:      address,
		Name:         "Scan Address",
	})); err != nil {
		t.Fatalf("seed scan profile: %v", err)
	}

	profile, err := agg.LookupByAddress(address)
	if err != nil {
		t.Fatalf("LookupByAddress: %v", err)
	}
	if profile == nil || profile.MetaID != "meta_scan_address" {
		t.Fatalf("scan fallback returned %#v", profile)
	}
}

func mustMarshalProfile(t *testing.T, profile *UserProfile) []byte {
	t.Helper()
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	return raw
}

// --- Acceptance Criteria #8: Cache hit ---

func TestHandleMetaIdInfo_CacheHit(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Store a profile.
	initPin := &aggregator.PinInscription{
		Path:      "/",
		Operation: "init",
		MetaId:    "cache_test_user",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "cache_init:i0",
	}
	agg.HandleBlockPin(initPin)

	// First call: should populate cache.
	w1 := performRequest(t, router, "GET", "/api/info/metaid/cache_test_user")
	var resp1 struct {
		Code           int         `json:"code"`
		Data           UserProfile `json:"data"`
		ProcessingTime int64       `json:"processingTime"`
	}
	json.Unmarshal(w1.Body.Bytes(), &resp1)
	if resp1.Code != 1 {
		t.Fatalf("first call failed: code=%d", resp1.Code)
	}
	pt1 := resp1.ProcessingTime

	// Second call: should hit cache and return processingTime > 0.
	w2 := performRequest(t, router, "GET", "/api/info/metaid/cache_test_user")
	var resp2 struct {
		Code           int         `json:"code"`
		Data           UserProfile `json:"data"`
		ProcessingTime int64       `json:"processingTime"`
	}
	json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Code != 1 {
		t.Fatalf("second call failed: code=%d", resp2.Code)
	}
	pt2 := resp2.ProcessingTime

	if pt1 <= 0 || pt2 <= 0 {
		t.Errorf("processingTime should be > 0: pt1=%d pt2=%d", pt1, pt2)
	}
	if resp2.Data.MetaID != "cache_test_user" {
		t.Errorf("expected cached metaid, got %q", resp2.Data.MetaID)
	}
	t.Logf("processingTime: call1=%d call2=%d", pt1, pt2)
}

// --- Acceptance Criteria #9: Cache invalidation ---

func TestHandleBlockPin_InvalidatesCache(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Store initial profile.
	initPin := &aggregator.PinInscription{
		Path:      "/",
		Operation: "init",
		MetaId:    "invalidate_test_user",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "inv_init:i0",
	}
	agg.HandleBlockPin(initPin)

	// Set initial name.
	namePin1 := &aggregator.PinInscription{
		Path:        "/info/name",
		Operation:   "create",
		MetaId:      "invalidate_test_user",
		Address:     "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName:   "btc",
		ContentBody: []byte("OldName"),
		Id:          "inv_name1:i0",
	}
	agg.HandleBlockPin(namePin1)

	// Prime cache.
	w1 := performRequest(t, router, "GET", "/api/info/metaid/invalidate_test_user")
	var resp1 struct {
		Code int         `json:"code"`
		Data UserProfile `json:"data"`
	}
	json.Unmarshal(w1.Body.Bytes(), &resp1)
	if resp1.Data.Name != "OldName" {
		t.Fatalf("expected initial name 'OldName', got %q", resp1.Data.Name)
	}

	// Process an updated name pin — this should invalidate the cache.
	namePin2 := &aggregator.PinInscription{
		Path:        "/info/name",
		Operation:   "modify",
		MetaId:      "invalidate_test_user",
		Address:     "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName:   "btc",
		ContentBody: []byte("NewName"),
		Id:          "inv_name2:i0",
	}
	_, err := agg.HandleBlockPin(namePin2)
	if err != nil {
		t.Fatalf("HandleBlockPin(modify name) failed: %v", err)
	}

	// Verify the name was updated in the store.
	raw, _ := store.Get(namespace, profileKey("invalidate_test_user"))
	var stored UserProfile
	json.Unmarshal(raw, &stored)
	if stored.Name != "NewName" {
		t.Errorf("expected stored name 'NewName', got %q", stored.Name)
	}

	// Fetch via HTTP — should return the updated name (cache was invalidated).
	w2 := performRequest(t, router, "GET", "/api/info/metaid/invalidate_test_user")
	var resp2 struct {
		Code int         `json:"code"`
		Data UserProfile `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &resp2)
	if resp2.Data.Name != "NewName" {
		t.Errorf("expected updated name 'NewName' via HTTP, got %q", resp2.Data.Name)
	}
	t.Logf("cache invalidation test: old=%q new=%q", resp1.Data.Name, resp2.Data.Name)
}

func TestHandleBlockPin_BackgroundStoresPinId(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	initPin := &aggregator.PinInscription{
		Path:      "/",
		Operation: "init",
		MetaId:    "bg-user",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "background-init:i0",
	}
	if _, err := agg.HandleBlockPin(initPin); err != nil {
		t.Fatalf("HandleBlockPin(init) failed: %v", err)
	}

	backgroundPin := &aggregator.PinInscription{
		Path:      "/info/background",
		Operation: "create",
		MetaId:    "bg-user",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "background-pin:i0",
	}
	if _, err := agg.HandleBlockPin(backgroundPin); err != nil {
		t.Fatalf("HandleBlockPin(background) failed: %v", err)
	}

	profile, err := agg.LookupByMetaId("bg-user")
	if err != nil {
		t.Fatalf("LookupByMetaId returned error: %v", err)
	}
	if profile == nil {
		t.Fatal("LookupByMetaId returned nil profile")
	}
	if profile.Background != "/content/background-pin:i0" {
		t.Fatalf("Background = %q, want /content/background-pin:i0", profile.Background)
	}
	if profile.BackgroundId != "background-pin:i0" {
		t.Fatalf("BackgroundId = %q, want background-pin:i0", profile.BackgroundId)
	}
}

// TestHandleAddressInfo tests the /api/info/address/:address endpoint.
func TestHandleAddressInfo(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Store a profile with a known address.
	initPin := &aggregator.PinInscription{
		Path:      "/",
		Operation: "init",
		MetaId:    "addr_test_user",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "addr_init:i0",
	}
	agg.HandleBlockPin(initPin)

	w := performRequest(t, router, "GET", "/api/info/address/1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa")

	var resp struct {
		Code           int         `json:"code"`
		Data           UserProfile `json:"data"`
		ProcessingTime int64       `json:"processingTime"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 1 {
		t.Errorf("expected code=1 (meta-file-system success), got %d", resp.Code)
	}
	if resp.ProcessingTime <= 0 {
		t.Errorf("expected processingTime > 0, got %d", resp.ProcessingTime)
	}
	t.Logf("address info: code=%d metaid=%s globalMetaId=%s", resp.Code, resp.Data.MetaID, resp.Data.GlobalMetaID)
}

func TestHandleMempoolPin_StoresHomepage(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	metaid := "mempool_user"
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	global := idaddress.EncodeGlobalMetaId(address, "mvc")

	if _, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Id:        "init:i0",
		Path:      "/",
		Operation: "init",
		MetaId:    metaid,
		Address:   address,
		ChainName: "mvc",
	}); err != nil {
		t.Fatalf("HandleBlockPin(init): %v", err)
	}

	pin := &aggregator.PinInscription{
		Id:           "mempool-home:i0",
		Path:         "/info/homepage",
		Operation:    "create",
		MetaId:       metaid,
		Address:      address,
		GlobalMetaId: global,
		ContentBody:  []byte(`{"uri":"metaapp://pending","renderer":"metaapp","contentType":"application/vnd.metaapp"}`),
		ChainName:    "mvc",
	}

	evt, err := agg.HandleMempoolPin(pin)
	if err != nil {
		t.Fatalf("HandleMempoolPin should not error: %v", err)
	}
	if evt != nil {
		t.Fatal("HandleMempoolPin should return nil event for userinfo")
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile.HomepageId != "mempool-home:i0" {
		t.Fatalf("HomepageId = %q, want mempool-home:i0; profile=%#v", profile.HomepageId, profile)
	}
	if profile.Homepage != `{"uri":"metaapp://pending","renderer":"metaapp","contentType":"application/vnd.metaapp"}` {
		t.Fatalf("Homepage = %q, want pending custom homepage", profile.Homepage)
	}
}

// TestNameMethod verifies Aggregator.Name returns "userinfo".
func TestNameMethod(t *testing.T) {
	agg := &Aggregator{}
	if agg.Name() != "userinfo" {
		t.Errorf("expected Name()='userinfo', got %q", agg.Name())
	}
}

// --- Acceptance Criteria #11: idchat / meta-file-system wire compatibility ---

// TestUserProfile_JSONFieldNames locks in the exact JSON field names idchat's
// metafileIndexerApi client expects. In particular, chat public key fields must
// be all-lowercase (chatpubkey / chatpubkeyId) to match meta-file-system, not
// camelCase. Any regression here breaks idchat's normalizeUserInfo() silently.
func TestUserProfile_JSONFieldNames(t *testing.T) {
	profile := UserProfile{
		GlobalMetaID:    "idq1test",
		MetaID:          "metaid_test",
		Address:         "addr_test",
		Name:            "Alice",
		NameId:          "tx_name:i0",
		Avatar:          "/content/tx_avatar:i0",
		AvatarId:        "tx_avatar:i0",
		ChatPublicKey:   "02deadbeef",
		ChatPublicKeyId: "tx_key:i0",
		ChainName:       "btc",
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var asMap map[string]interface{}
	if err := json.Unmarshal(raw, &asMap); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	// Required fields (must exist with the lowercase names).
	requiredFields := []string{"chatpubkey", "chatpubkeyId", "metaid", "globalMetaId", "avatar", "avatarId"}
	for _, f := range requiredFields {
		if _, ok := asMap[f]; !ok {
			t.Errorf("expected JSON field %q to be present, raw=%s", f, raw)
		}
	}

	// Forbidden fields (camelCase variants that meta-file-system does NOT use
	// and that idchat's normalizeUserInfo() reads as undefined).
	forbiddenFields := []string{"chatPublicKey", "chatPublicKeyId"}
	for _, f := range forbiddenFields {
		if _, ok := asMap[f]; ok {
			t.Errorf("JSON field %q must not be present (raw=%s)", f, raw)
		}
	}

	// Spot-check the values landed on the correct keys.
	if asMap["chatpubkey"] != "02deadbeef" {
		t.Errorf("chatpubkey value mismatch: %v", asMap["chatpubkey"])
	}
	if asMap["chatpubkeyId"] != "tx_key:i0" {
		t.Errorf("chatpubkeyId value mismatch: %v", asMap["chatpubkeyId"])
	}
}

// TestHandleMetaIdInfo_MetafileIndexerPrefix verifies the userinfo handlers
// respond on both `/api/info/...` (native metaso-p2p prefix) and
// `/metafile-indexer/api/info/...` (meta-file-system drop-in prefix) so idchat
// can flip `metafileIndexerApi` to metaso-p2p with zero TypeScript changes.
func TestHandleMetaIdInfo_MetafileIndexerPrefix(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	// Register the same routes under the meta-file-system prefix.
	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))
	agg.RegisterRoutes(router.Group("/metafile-indexer/api"))

	initPin := &aggregator.PinInscription{
		Path:      "/",
		Operation: "init",
		MetaId:    "drop_in_user",
		Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		ChainName: "btc",
		Id:        "drop_in_init:i0",
	}
	agg.HandleBlockPin(initPin)

	paths := []string{
		"/api/info/metaid/drop_in_user",
		"/metafile-indexer/api/info/metaid/drop_in_user",
	}
	for _, p := range paths {
		w := performRequest(t, router, "GET", p)
		var resp struct {
			Code int         `json:"code"`
			Data UserProfile `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("%s: decode failed: %v body=%s", p, err, w.Body.String())
		}
		if resp.Code != 1 {
			t.Errorf("%s: expected code=1, got %d body=%s", p, resp.Code, w.Body.String())
		}
		if resp.Data.MetaID != "drop_in_user" {
			t.Errorf("%s: metaid mismatch: %q", p, resp.Data.MetaID)
		}
	}
}

func TestHandleMetaIdInfo_RemoteFallbackFillsMissingChatKey(t *testing.T) {
	const providerAddress = "1ProviderAddress11111111111111111111"
	const providerMetaID = "823548f91509cc2318f2b2e9205c86d6f4502762a78959e8cb2f026486606ab0"

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/info/metaid/" + providerAddress:
			_, _ = w.Write([]byte(`{"code":40400,"message":"user not found"}`))
		case "/info/address/" + providerAddress:
			_, _ = w.Write([]byte(`{"code":1,"message":"success","data":{"metaid":"` + providerMetaID + `","globalMetaId":"idq1provider","address":"` + providerAddress + `","name":"Remote Bot","chatpubkey":"04remotechatkey","chatpubkeyId":"remote_key:i0"}}`))
		default:
			t.Fatalf("unexpected remote profile path: %s", r.URL.Path)
		}
	}))
	defer remote.Close()

	t.Setenv("METASO_P2P_PROFILE_REMOTE_BASE_URL", remote.URL)
	t.Setenv("METASO_P2P_PROFILE_MODE", "local-first")
	t.Setenv("METASO_P2P_PROFILE_ALLOW_REMOTE_FALLBACK", "true")

	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Mirrors the local 30-day MVC index shape from the Bothub issue: a
	// profile exists under the provider address and has display fields, but
	// the older /info/chatpubkey pin is outside the local index window.
	_, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Path:        "/info/name",
		Operation:   "create",
		MetaId:      providerAddress,
		Address:     providerAddress,
		ChainName:   "mvc",
		ContentBody: []byte("Local Bot"),
		Id:          "local_name:i0",
	})
	if err != nil {
		t.Fatalf("seed local profile: %v", err)
	}

	w := performRequest(t, router, "GET", "/api/info/metaid/"+providerAddress)
	var resp struct {
		Code int         `json:"code"`
		Data UserProfile `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v body=%s", err, w.Body.String())
	}
	if resp.Code != 1 {
		t.Fatalf("expected success code=1, got %d body=%s", resp.Code, w.Body.String())
	}
	if resp.Data.ChatPublicKey != "04remotechatkey" {
		t.Fatalf("chatpubkey was not filled from remote fallback: %+v", resp.Data)
	}
	if resp.Data.ChatPublicKeyId != "remote_key:i0" {
		t.Fatalf("chatpubkeyId mismatch: %+v", resp.Data)
	}
	if resp.Data.GlobalMetaID != "idq1provider" {
		t.Fatalf("globalMetaId should be filled from remote fallback: %+v", resp.Data)
	}

	raw, err := store.Get(namespace, profileKey(providerAddress))
	if err != nil || raw == nil {
		t.Fatalf("stored merged profile missing: raw=%s err=%v", raw, err)
	}
	var stored UserProfile
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatalf("decode stored profile: %v", err)
	}
	if stored.ChatPublicKey != "04remotechatkey" {
		t.Fatalf("remote chat key was not persisted: %+v", stored)
	}
}

func TestHandleGlobalMetaIdInfo_LegacyAddressFallbackFillsMissingChatKey(t *testing.T) {
	const providerAddress = "1LegacyGlobalAddress1111111111111111"

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/info/globalmetaid/" + providerAddress:
			_, _ = w.Write([]byte(`{"code":1,"message":"success","data":{"globalMetaId":"` + providerAddress + `","metaid":"","address":"","avatar":"/content/","chatpubkey":"","chatpubkeyId":""}}`))
		case "/info/address/" + providerAddress:
			_, _ = w.Write([]byte(`{"code":1,"message":"success","data":{"metaid":"` + providerAddress + `","globalMetaId":"idq1legacyprovider","address":"` + providerAddress + `","name":"Legacy Provider","avatar":"/content/legacy_avatar:i0","chatpubkey":"04legacychatkey","chatpubkeyId":"legacy_key:i0"}}`))
		default:
			t.Fatalf("unexpected remote profile path: %s", r.URL.Path)
		}
	}))
	defer remote.Close()

	t.Setenv("METASO_P2P_PROFILE_REMOTE_BASE_URL", remote.URL)
	t.Setenv("METASO_P2P_PROFILE_MODE", "local-first")
	t.Setenv("METASO_P2P_PROFILE_ALLOW_REMOTE_FALLBACK", "true")

	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Mirrors legacy Bot Hub records that exposed a provider address in the
	// providerGlobalMetaId field. Delivery may later call
	// /info/globalmetaid/<that-address>, so this path must recover through the
	// address profile route instead of returning the incomplete local shell.
	if err := agg.saveProfile(&UserProfile{
		GlobalMetaID: providerAddress,
		MetaID:       "local_shell",
		Avatar:       "/content/",
	}); err != nil {
		t.Fatalf("seed local shell profile: %v", err)
	}

	w := performRequest(t, router, "GET", "/api/info/globalmetaid/"+providerAddress)
	var resp struct {
		Code int         `json:"code"`
		Data UserProfile `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v body=%s", err, w.Body.String())
	}
	if resp.Code != 1 {
		t.Fatalf("expected success code=1, got %d body=%s", resp.Code, w.Body.String())
	}
	if resp.Data.ChatPublicKey != "04legacychatkey" {
		t.Fatalf("chatpubkey was not filled from address fallback: %+v", resp.Data)
	}
	if resp.Data.ChatPublicKeyId != "legacy_key:i0" {
		t.Fatalf("chatpubkeyId mismatch: %+v", resp.Data)
	}
	if resp.Data.Address != providerAddress {
		t.Fatalf("address should be filled from address fallback: %+v", resp.Data)
	}
	if resp.Data.Avatar != "/content/legacy_avatar:i0" {
		t.Fatalf("avatar placeholder should be replaced from address fallback: %+v", resp.Data)
	}
	if resp.Data.GlobalMetaID != "idq1legacyprovider" {
		t.Fatalf("canonical globalMetaId should be filled from remote fallback: %+v", resp.Data)
	}
}

func TestHandleGlobalMetaIdInfo_RemoteFallbackRefreshesLegacyAvatar(t *testing.T) {
	const globalMetaID = "idq1avatarrefresh"
	const providerMetaID = "ce447562dcbca15ee44c7055c40735b01d96f1fa2017c871051fe9cfcddf70c3"
	const providerAddress = "1BvrDMi5UoytcLWKXnLL66xErdc73gkAoL"
	const staleAvatarID = "d20f8dc9512b55223e67ea6e6df7b664f24d788ef1f594cfc57cd43c557f5e8ci0"
	const currentAvatarID = "2b1a6068498cd34ae99953eca889dc206ed81823425ff7cc1c5e09a142c05795i0"

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/info/globalmetaid/" + globalMetaID:
			_, _ = w.Write([]byte(`{"code":1,"message":"success","data":{"metaid":"` + providerMetaID + `","globalMetaId":"` + globalMetaID + `","address":"` + providerAddress + `","name":"Ellis Grant","avatar":"/content/` + currentAvatarID + `","avatarId":"` + currentAvatarID + `","chatpubkey":"04currentchatkey","chatpubkeyId":"current_key:i0"}}`))
		default:
			t.Fatalf("unexpected remote profile path: %s", r.URL.Path)
		}
	}))
	defer remote.Close()

	t.Setenv("METASO_P2P_PROFILE_REMOTE_BASE_URL", remote.URL)
	t.Setenv("METASO_P2P_PROFILE_MODE", "local-first")
	t.Setenv("METASO_P2P_PROFILE_ALLOW_REMOTE_FALLBACK", "true")

	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	if err := agg.saveProfile(&UserProfile{
		GlobalMetaID:    globalMetaID,
		MetaID:          providerMetaID,
		Address:         providerAddress,
		Name:            "Ellis Grant",
		Avatar:          "https://manapi.metaid.io/content/" + staleAvatarID,
		AvatarId:        staleAvatarID,
		ChatPublicKey:   "04currentchatkey",
		ChatPublicKeyId: "current_key:i0",
	}); err != nil {
		t.Fatalf("seed stale local profile: %v", err)
	}

	w := performRequest(t, router, "GET", "/api/info/globalmetaid/"+globalMetaID)
	var resp struct {
		Code int         `json:"code"`
		Data UserProfile `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v body=%s", err, w.Body.String())
	}
	if resp.Code != 1 {
		t.Fatalf("expected success code=1, got %d body=%s", resp.Code, w.Body.String())
	}
	if resp.Data.Avatar != "/content/"+currentAvatarID {
		t.Fatalf("avatar should be refreshed from current remote profile, got %+v", resp.Data)
	}
	if resp.Data.AvatarId != currentAvatarID {
		t.Fatalf("avatarId should be current remote id, got %+v", resp.Data)
	}
}

func TestHandleGlobalMetaIdInfo_RemoteFallbackPreservesExistingLocalHomepage(t *testing.T) {
	const globalMetaID = "idq1homepagepreserve"
	const providerMetaID = "3f8121c0c277f80c8edf7e36b9f64e2ac13b58bf39da7a6f32ec006365c14297"
	const providerAddress = "1EX5NN6npyCp3X6Sv4Yahv6DrBNKRtq4Gw"
	const homepagePayload = `{"uri":"metaapp://c06b7a2db6efa241560a2356e9966cf9758dae3ec9c795f614a652b113e30329i0","renderer":"metaapp","contentType":"application/vnd.metaapp"}`

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/info/globalmetaid/" + globalMetaID:
			_, _ = w.Write([]byte(`{"code":1,"message":"success","data":{"metaid":"` + providerMetaID + `","globalMetaId":"` + globalMetaID + `","address":"` + providerAddress + `","name":"Eric","avatar":"/content/avatar:i0","avatarId":"avatar:i0","chatpubkey":"04remotechatkey","chatpubkeyId":"remote_key:i0"}}`))
		default:
			t.Fatalf("unexpected remote profile path: %s", r.URL.Path)
		}
	}))
	defer remote.Close()

	t.Setenv("METASO_P2P_PROFILE_REMOTE_BASE_URL", remote.URL)
	t.Setenv("METASO_P2P_PROFILE_MODE", "local-first")
	t.Setenv("METASO_P2P_PROFILE_ALLOW_REMOTE_FALLBACK", "true")

	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Seed the canonical local profile directly without the reverse index to
	// mirror a stale lookup path. The local-only homepage must survive remote
	// completion instead of being overwritten by the narrower remote schema.
	raw := mustMarshalProfile(t, &UserProfile{
		MetaID:     providerMetaID,
		Address:    providerAddress,
		Homepage:   homepagePayload,
		HomepageId: "homepage:i0",
		ChainName:  "mvc",
	})
	if err := store.Set(namespace, profileKey(providerMetaID), raw); err != nil {
		t.Fatalf("seed canonical local profile: %v", err)
	}

	w := performRequest(t, router, "GET", "/api/info/globalmetaid/"+globalMetaID)
	var resp struct {
		Code int         `json:"code"`
		Data UserProfile `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v body=%s", err, w.Body.String())
	}
	if resp.Code != 1 {
		t.Fatalf("expected success code=1, got %d body=%s", resp.Code, w.Body.String())
	}
	if resp.Data.Homepage != homepagePayload || resp.Data.HomepageId != "homepage:i0" {
		t.Fatalf("homepage should survive remote fallback: %+v", resp.Data)
	}
	if resp.Data.GlobalMetaID != globalMetaID {
		t.Fatalf("globalMetaId should still be filled from remote fallback: %+v", resp.Data)
	}

	stored, err := agg.LookupByMetaId(providerMetaID)
	if err != nil {
		t.Fatalf("LookupByMetaId: %v", err)
	}
	if stored == nil {
		t.Fatal("LookupByMetaId returned nil profile")
	}
	if stored.Homepage != homepagePayload || stored.HomepageId != "homepage:i0" {
		t.Fatalf("persisted profile lost homepage after remote fallback: %+v", stored)
	}
}

// TestHandleMetaIdInfo_FullWireFormat asserts the whole on-the-wire response
// exactly matches what idchat's metafileIndexerApi client expects after a
// realistic init + name + chatpubkey indexing sequence.
func TestHandleMetaIdInfo_FullWireFormat(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	pins := []*aggregator.PinInscription{
		{
			Path:      "/",
			Operation: "init",
			MetaId:    "wire_user",
			Address:   "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
			ChainName: "btc",
			Id:        "wire_init:i0",
		},
		{
			Path:        "/info/name",
			Operation:   "create",
			MetaId:      "wire_user",
			Address:     "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
			ChainName:   "btc",
			ContentBody: []byte("WireAlice"),
			Id:          "wire_name:i0",
		},
		{
			Path:        "/info/chatpubkey",
			Operation:   "create",
			MetaId:      "wire_user",
			Address:     "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
			ChainName:   "btc",
			ContentBody: []byte("02wireBeef"),
			Id:          "wire_key:i0",
		},
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s) failed: %v", pin.Path, err)
		}
	}

	w := performRequest(t, router, "GET", "/api/info/metaid/wire_user")
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode failed: %v body=%s", err, w.Body.String())
	}

	// Envelope shape (code=1 success, processingTime present).
	if code, _ := resp["code"].(float64); int(code) != 1 {
		t.Errorf("expected code=1, got %v", resp["code"])
	}
	if _, ok := resp["processingTime"]; !ok {
		t.Errorf("expected processingTime field")
	}

	data, ok := resp["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data is not an object: %v", resp["data"])
	}
	if data["metaid"] != "wire_user" {
		t.Errorf("metaid mismatch: %v", data["metaid"])
	}
	if data["name"] != "WireAlice" {
		t.Errorf("name mismatch: %v", data["name"])
	}
	if data["chatpubkey"] != "02wireBeef" {
		t.Errorf("chatpubkey mismatch: %v (full data=%v)", data["chatpubkey"], data)
	}
	if data["chatpubkeyId"] != "wire_key:i0" {
		t.Errorf("chatpubkeyId mismatch: %v", data["chatpubkeyId"])
	}
	// Make sure the legacy camelCase keys are NOT present.
	if _, present := data["chatPublicKey"]; present {
		t.Error("legacy chatPublicKey key must not be in response (idchat reads chatpubkey)")
	}
}

// TestNotifyChannel verifies the notify channel has correct type and capacity.
func TestNotifyChannel(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	ch := agg.NotifyChannel()
	if ch == nil {
		t.Fatal("NotifyChannel() returned nil")
	}

	// Try to read from channel (should not block since nothing is sent).
	select {
	case <-ch:
		t.Error("unexpected event on notify channel")
	case <-time.After(10 * time.Millisecond):
		// Expected: no event.
	}
}
