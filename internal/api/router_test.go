package api_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/bothomepage"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/groupchat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/notify"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/api"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
	"github.com/metaid-developers/metaso-p2p/internal/socket"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// setupFullRouter mirrors cmd/metaso-p2p/main.go's aggregator wiring so the
// tests below catch real-world routing regressions (e.g. forgetting to register
// privatechat, which causes its routes to fall back to groupchat stubs).
func setupFullRouter(t *testing.T) *gin.Engine {
	t.Helper()

	fixture := setupFullRouterFixture(t)
	return fixture.router
}

type fullRouterFixture struct {
	router         *gin.Engine
	store          *storage.PebbleStore
	userAgg        *userinfo.Aggregator
	groupAgg       *groupchat.Aggregator
	privateAgg     *privatechat.Aggregator
	botHomepageAgg *bothomepage.Aggregator
	skillAgg       *skillservice.Aggregator
	publishedAgg   *publishedcontent.Aggregator
}

func setupFullRouterFixture(t *testing.T) *fullRouterFixture {
	t.Helper()

	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })
	cacheProvider := cache.New(store)

	reg := aggregator.NewRegistry(store, cacheProvider)
	if err := reg.Register(&notify.Aggregator{}); err != nil {
		t.Fatalf("register notify: %v", err)
	}
	userAgg := &userinfo.Aggregator{}
	if err := reg.Register(userAgg); err != nil {
		t.Fatalf("register userinfo: %v", err)
	}
	groupAgg := &groupchat.Aggregator{}
	if err := reg.Register(groupAgg); err != nil {
		t.Fatalf("register groupchat: %v", err)
	}
	privateAgg := &privatechat.Aggregator{}
	if err := reg.Register(privateAgg); err != nil {
		t.Fatalf("register privatechat: %v", err)
	}
	skillAgg := &skillservice.Aggregator{}
	if err := reg.Register(skillAgg); err != nil {
		t.Fatalf("register skillservice: %v", err)
	}
	publishedAgg := &publishedcontent.Aggregator{}
	if err := reg.Register(publishedAgg); err != nil {
		t.Fatalf("register publishedcontent: %v", err)
	}
	botHomepageAgg := &bothomepage.Aggregator{}
	if err := reg.Register(botHomepageAgg); err != nil {
		t.Fatalf("register bothomepage: %v", err)
	}

	skillAgg.SetProfileLookup(skillservice.NewUserInfoLookupAdapter(userAgg))
	skillAgg.SetAssetBaseURL("https://file.metaid.io/metafile-indexer/content")
	botHomepageAgg.SetProfileLookup(bothomepage.NewUserInfoLookupAdapter(userAgg))
	botHomepageAgg.SetServiceLister(skillAgg)
	botHomepageAgg.SetHomepageServiceLister(skillAgg)
	botHomepageAgg.SetPublishedContentLister(publishedAgg)
	botHomepageAgg.SetAssetBaseURL("https://file.metaid.io/metafile-indexer/content")
	privateAgg.SetProfileLookup(privatechat.NewUserInfoLookupAdapter(userAgg))

	cfg := config.Default()
	// SetupRouter handles nil socketServer gracefully (Socket.IO routes skipped).
	return &fullRouterFixture{
		router:         api.SetupRouter(cfg, store, cacheProvider, reg, nil, "test"),
		store:          store,
		userAgg:        userAgg,
		groupAgg:       groupAgg,
		privateAgg:     privateAgg,
		botHomepageAgg: botHomepageAgg,
		skillAgg:       skillAgg,
		publishedAgg:   publishedAgg,
	}
}

func seedBotProfile(t *testing.T, fixture *fullRouterFixture, globalMetaId string) {
	t.Helper()

	profile := userinfo.UserProfile{
		GlobalMetaID:    globalMetaId,
		MetaID:          "meta-" + globalMetaId,
		Address:         "addr-" + globalMetaId,
		Name:            "Homepage Bot",
		NameId:          "name-" + globalMetaId + ":i0",
		Bio:             "Bot homepage fixture",
		BioId:           "bio-" + globalMetaId + ":i0",
		Role:            "Router acceptance bot",
		RoleId:          "role-" + globalMetaId + ":i0",
		ChatPublicKey:   "04" + strings.Repeat("a", 64),
		ChatPublicKeyId: "chat-key-" + globalMetaId + ":i0",
		ChainName:       "mvc",
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	metaID := profile.MetaID
	if err := fixture.store.Set("userinfo", []byte("profile:"+metaID), raw); err != nil {
		t.Fatalf("seed userinfo profile: %v", err)
	}
	if err := fixture.store.Set("userinfo", []byte("globalmetaid:"+strings.ToLower(globalMetaId)), []byte(metaID)); err != nil {
		t.Fatalf("seed userinfo globalMetaId index: %v", err)
	}
}

func seedPublishedContent(t *testing.T, agg *publishedcontent.Aggregator, globalMetaId string) {
	t.Helper()

	pins := []*aggregator.PinInscription{
		{
			Id:           "buzz-" + globalMetaId + ":i0",
			Path:         publishedcontent.PathSimpleBuzz,
			Operation:    publishedcontent.OperationCreate,
			ContentBody:  []byte("router buzz"),
			ContentType:  "text/plain",
			ChainName:    "mvc",
			GlobalMetaId: globalMetaId,
			MetaId:       "meta-" + globalMetaId,
			Address:      "addr-" + globalMetaId,
			Timestamp:    3000,
			Number:       30,
		},
		{
			Id:           "metaapp-" + globalMetaId + ":i0",
			Path:         publishedcontent.PathMetaApp,
			Operation:    publishedcontent.OperationCreate,
			ContentBody:  mustMarshalJSON(t, map[string]interface{}{"title": "Router MetaAPP", "description": "MetaAPP fixture"}),
			ContentType:  "application/json",
			ChainName:    "mvc",
			GlobalMetaId: globalMetaId,
			MetaId:       "meta-" + globalMetaId,
			Address:      "addr-" + globalMetaId,
			Timestamp:    2000,
			Number:       20,
		},
		{
			Id:           "skill-" + globalMetaId + ":i0",
			Path:         publishedcontent.PathMetaBotSkill,
			Operation:    publishedcontent.OperationCreate,
			ContentBody:  mustMarshalJSON(t, map[string]interface{}{"name": "Router Skill", "summary": "Skill fixture"}),
			ContentType:  "application/json",
			ChainName:    "mvc",
			GlobalMetaId: globalMetaId,
			MetaId:       "meta-" + globalMetaId,
			Address:      "addr-" + globalMetaId,
			Timestamp:    1000,
			Number:       10,
		},
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("seed published content %s: %v", pin.Id, err)
		}
	}
}

func get(t *testing.T, router *gin.Engine, path string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequest("GET", path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w, body
}

func postJSON(t *testing.T, router *gin.Engine, path string, body string) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	req, _ := http.NewRequest("POST", path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var decoded map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &decoded)
	return w, decoded
}

func mustMarshalJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return raw
}

func assertResponseDataKeys(t *testing.T, body map[string]interface{}, keys ...string) map[string]interface{} {
	t.Helper()

	code, _ := body["code"].(float64)
	if int(code) != 0 {
		t.Fatalf("expected code=0, got %v body=%v", body["code"], body)
	}

	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data should be an object, got %T %v", body["data"], body["data"])
	}
	for _, key := range keys {
		if _, ok := data[key]; !ok {
			t.Fatalf("missing data.%s in %v", key, data)
		}
	}
	return data
}

type fakePresenceSnapshotProvider struct {
	snapshot *presence.Snapshot
	err      error
}

func (p fakePresenceSnapshotProvider) Snapshot() (*presence.Snapshot, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.snapshot, nil
}

func setupPresenceRouter(t *testing.T, provider presence.SnapshotProvider) *gin.Engine {
	t.Helper()

	cfg := config.Default()
	router, _ := setupPresenceRouterWithConfig(t, cfg, provider)
	return router
}

func setupPresenceRouterWithConfig(t *testing.T, cfg config.Config, provider presence.SnapshotProvider) (*gin.Engine, *socket.Server) {
	t.Helper()

	socketServer := socket.NewServer(cfg.Socket)
	t.Cleanup(socketServer.Shutdown)
	socketServer.SetSnapshotProvider(provider)

	return api.SetupRouter(cfg, nil, nil, nil, socketServer, "test"), socketServer
}

func fakePresenceSnapshot() *presence.Snapshot {
	return &presence.Snapshot{
		Protocol:    "metaso-p2p-presence",
		Version:     "1.0.0",
		NodeID:      "node-a",
		GeneratedAt: 1710000001000,
		TTLSeconds:  30,
		Sequence:    7,
		Items: []presence.OnlineEntry{
			{MetaId: "meta-1", Type: "pc", ConnectedAt: 1710000000000, LastSeenAt: 1710000000500},
		},
		Signature: "sig",
	}
}

func TestRouter_WellKnownPresenceEndpointDisabledWithoutProvider(t *testing.T) {
	router := setupPresenceRouter(t, nil)

	w, _ := get(t, router, "/.well-known/metaso-p2p/presence")
	if w.Code != http.StatusNotFound {
		t.Fatalf("presence endpoint without provider: want 404 got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointReturnsSnapshotFromProvider(t *testing.T) {
	router := setupPresenceRouter(t, fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()})

	w, body := get(t, router, "/.well-known/metaso-p2p/presence")
	if w.Code != http.StatusOK {
		t.Fatalf("presence endpoint: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if body["protocol"] != "metaso-p2p-presence" {
		t.Fatalf("presence endpoint protocol: want metaso-p2p-presence got %v body=%s", body["protocol"], w.Body.String())
	}
	if _, ok := body["code"]; ok {
		t.Fatalf("presence endpoint should return the snapshot directly, got enveloped body=%s", w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointUsesConfiguredPath(t *testing.T) {
	cfg := config.Default()
	cfg.Federation.PresencePath = "/custom/presence"
	router, _ := setupPresenceRouterWithConfig(t, cfg, fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()})

	w, body := get(t, router, "/custom/presence")
	if w.Code != http.StatusOK {
		t.Fatalf("configured presence endpoint: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if body["protocol"] != "metaso-p2p-presence" {
		t.Fatalf("configured presence endpoint protocol: want metaso-p2p-presence got %v body=%s", body["protocol"], w.Body.String())
	}

	w, _ = get(t, router, "/.well-known/metaso-p2p/presence")
	if w.Code != http.StatusNotFound {
		t.Fatalf("default presence endpoint should not be mounted when custom path is configured: got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointEmptyConfiguredPathFallsBackToDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Federation.PresencePath = ""
	router, _ := setupPresenceRouterWithConfig(t, cfg, fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()})

	w, body := get(t, router, "/.well-known/metaso-p2p/presence")
	if w.Code != http.StatusOK {
		t.Fatalf("fallback presence endpoint: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if body["protocol"] != "metaso-p2p-presence" {
		t.Fatalf("fallback presence endpoint protocol: want metaso-p2p-presence got %v body=%s", body["protocol"], w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointProviderErrorReturns503(t *testing.T) {
	router := setupPresenceRouter(t, fakePresenceSnapshotProvider{err: errors.New("signing failed")})

	w, _ := get(t, router, "/.well-known/metaso-p2p/presence")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("presence endpoint provider error: want 503 got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointDoesNotExposeSocketIDs(t *testing.T) {
	router := setupPresenceRouter(t, fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()})

	w, body := get(t, router, "/.well-known/metaso-p2p/presence")
	if w.Code != http.StatusOK {
		t.Fatalf("presence endpoint: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	assertNoJSONKey(t, body, "socketId")
	assertNoJSONKey(t, body, "socketID")
}

func TestRouter_WellKnownPresenceEndpointProviderAccessIsRaceFree(t *testing.T) {
	router, socketServer := setupPresenceRouterWithConfig(
		t,
		config.Default(),
		fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()},
	)

	providers := []presence.SnapshotProvider{
		nil,
		fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()},
	}

	start := make(chan struct{})
	var wg sync.WaitGroup

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			<-start
			for j := 0; j < 1000; j++ {
				socketServer.SetSnapshotProvider(providers[(j+offset)%len(providers)])
			}
		}(i)
	}

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 1000; j++ {
				w, _ := get(t, router, "/.well-known/metaso-p2p/presence")
				if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
					t.Errorf("presence endpoint during provider update: got %d body=%s", w.Code, w.Body.String())
				}
			}
		}()
	}

	close(start)
	wg.Wait()
}

func assertNoJSONKey(t *testing.T, value any, forbidden string) {
	t.Helper()

	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if key == forbidden {
				t.Fatalf("presence endpoint exposed forbidden key %q", forbidden)
			}
			assertNoJSONKey(t, child, forbidden)
		}
	case []interface{}:
		for _, child := range typed {
			assertNoJSONKey(t, child, forbidden)
		}
	}
}

// TestRouter_AggregatorRegistrationDoesNotPanic ensures all four aggregators
// can be registered together without gin panicking on duplicate routes. This
// is a regression test for the previous state where private-chat routes were
// registered as stubs by groupchat AND by privatechat, which made registering
// both impossible.
func TestRouter_AggregatorRegistrationDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("router setup panicked (likely duplicate route): %v", r)
		}
	}()
	_ = setupFullRouter(t)
}

func TestRouter_BotHomepageGlobalMetaIDAcceptance(t *testing.T) {
	fixture := setupFullRouterFixture(t)
	const botAddress = "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"

	if _, err := fixture.botHomepageAgg.HandleBlockPin(nil); err != nil {
		t.Fatalf("HandleBlockPin(nil): %v", err)
	}

	if _, err := fixture.userAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:        "init-bot:i0",
		Path:      "/",
		MetaId:    "bot-meta",
		Address:   botAddress,
		ChainName: "mvc",
	}); err != nil {
		t.Fatalf("HandleBlockPin(user init): %v", err)
	}
	if _, err := fixture.userAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:          "name-bot:i0",
		Path:        "/info/name",
		MetaId:      "bot-meta",
		Address:     botAddress,
		ChainName:   "mvc",
		ContentBody: []byte("Homepage Bot"),
	}); err != nil {
		t.Fatalf("HandleBlockPin(user name): %v", err)
	}

	profile, err := fixture.userAgg.LookupByMetaId("bot-meta")
	if err != nil {
		t.Fatalf("LookupByMetaId: %v", err)
	}
	if profile == nil {
		t.Fatalf("LookupByMetaId returned nil")
	}
	if profile.GlobalMetaID == "" {
		t.Fatalf("userinfo should generate globalMetaId from %s", botAddress)
	}

	w, body := get(t, fixture.router, "/api/bot-homepage/globalmetaid/"+profile.GlobalMetaID)
	if w.Code != http.StatusOK {
		t.Fatalf("want HTTP 200 got %d body=%s", w.Code, w.Body.String())
	}

	data := assertResponseDataKeys(t, body, "schemaVersion", "canonical", "profile", "homepage", "services", "actions", "proofs", "source", "warnings")
	if data["schemaVersion"] != "botHomepage.v1" {
		t.Fatalf("schemaVersion: want botHomepage.v1 got %v data=%v", data["schemaVersion"], data)
	}
}

func TestRouterBotHomepageV2IncludesSections(t *testing.T) {
	fixture := setupFullRouterFixture(t)
	seedBotProfile(t, fixture, "idq-bot")
	seedPublishedContent(t, fixture.publishedAgg, "idq-bot")

	w, body := get(t, fixture.router, "/api/bot-homepage/globalmetaid/idq-bot?version=v2")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if body["code"] != float64(0) {
		t.Fatalf("code = %#v body=%s", body["code"], w.Body.String())
	}
	data := body["data"].(map[string]interface{})
	if data["schemaVersion"] != "botHomepage.v2" {
		t.Fatalf("schema = %#v", data["schemaVersion"])
	}
	sections, ok := data["sections"].([]interface{})
	if !ok {
		t.Fatalf("sections missing: %#v", data)
	}
	if len(sections) != 4 {
		t.Fatalf("sections length = %d, want 4: %#v", len(sections), sections)
	}

	sectionsByID := make(map[string]map[string]interface{}, len(sections))
	for _, section := range sections {
		typed, ok := section.(map[string]interface{})
		if !ok {
			t.Fatalf("section should be object: %T %#v", section, section)
		}
		id, _ := typed["id"].(string)
		sectionsByID[id] = typed
	}
	for _, id := range []string{"buzzes", "metaapps", "skills"} {
		section := sectionsByID[id]
		if section == nil {
			t.Fatalf("section %s missing from %#v", id, sections)
		}
		items, ok := section["items"].([]interface{})
		if !ok || len(items) != 1 {
			t.Fatalf("section %s items = %T %#v, want one seeded item", id, section["items"], section["items"])
		}
	}
}

func TestRouterBotHomepageDefaultStillV1(t *testing.T) {
	fixture := setupFullRouterFixture(t)
	seedBotProfile(t, fixture, "idq-bot")

	w, body := get(t, fixture.router, "/api/bot-homepage/globalmetaid/idq-bot")
	if w.Code != http.StatusOK || body["code"] != float64(0) {
		t.Fatalf("bad response status=%d body=%s", w.Code, w.Body.String())
	}
	data := body["data"].(map[string]interface{})
	if data["schemaVersion"] != "botHomepage.v1" {
		t.Fatalf("default schema = %#v", data["schemaVersion"])
	}
	if _, ok := data["sections"]; ok {
		t.Fatalf("default v1 should not return sections: %#v", data["sections"])
	}
}

// TestRouter_PrivateChatRoutesHandledByPrivateChat verifies the four
// /api/group-chat/private-* routes are handled by the privatechat aggregator
// (which returns a real payload) rather than the groupchat handleStub (which
// returns an empty object `{}`).
//
// Each case picks a discriminator that the privatechat handler exposes but the
// stub does not:
//   - "list"   = data is an object with a "list" field.
//   - "object" = data is a plain object that must be non-empty (has at least
//     one of the privatechat fields total/nextCursor/list).
//   - "object_with_list" = data is an object with a "list" field.
func TestRouter_PrivateChatRoutesHandledByPrivateChat(t *testing.T) {
	router := setupFullRouter(t)

	cases := []struct {
		path  string
		shape string // "object_with_list"
	}{
		{"/api/group-chat/private-chat-list?metaId=a&otherMetaId=b", "object_with_list"},
		{"/api/group-chat/private-chat-list-by-index?metaId=a&otherMetaId=b", "object_with_list"},
		{"/api/group-chat/private-group-paths?metaId=a", "object_with_list"},
		{"/api/group-chat/chat/homes/some_metaid", "object_with_list"},
	}

	for _, tc := range cases {
		w, body := get(t, router, tc.path)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d body=%s", tc.path, w.Code, w.Body.String())
			continue
		}
		code, _ := body["code"].(float64)
		if int(code) != 0 {
			t.Errorf("%s: expected code=0, got %v body=%s", tc.path, body["code"], w.Body.String())
			continue
		}

		switch tc.shape {
		case "object_with_list":
			data, ok := body["data"].(map[string]interface{})
			if !ok {
				t.Errorf("%s: data is not an object (groupchat stub would be {}): %v",
					tc.path, body["data"])
				continue
			}
			if _, present := data["list"]; !present {
				t.Errorf("%s: expected privatechat field 'list' in data (groupchat stub would have returned empty {}); got data=%v",
					tc.path, data)
			}
		}
	}
}

func TestRouter_CanonicalPrivateChatRoutesHandledByPrivateChat(t *testing.T) {
	router := setupFullRouter(t)

	cases := []struct {
		path  string
		shape string // "object_with_list"
	}{
		{"/api/private-chat/messages?metaId=a&otherMetaId=b", "object_with_list"},
		{"/api/private-chat/messages/by-index?metaId=a&otherMetaId=b", "object_with_list"},
		{"/api/private-chat/paths?metaId=a", "object_with_list"},
		{"/api/private-chat/homes/some_metaid", "object_with_list"},
	}

	for _, tc := range cases {
		w, body := get(t, router, tc.path)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d body=%s", tc.path, w.Code, w.Body.String())
			continue
		}
		code, _ := body["code"].(float64)
		if int(code) != 0 {
			t.Errorf("%s: expected code=0, got %v body=%s", tc.path, body["code"], w.Body.String())
			continue
		}

		switch tc.shape {
		case "object_with_list":
			data, ok := body["data"].(map[string]interface{})
			if !ok {
				t.Errorf("%s: data is not an object: %v", tc.path, body["data"])
				continue
			}
			if _, present := data["list"]; !present {
				t.Errorf("%s: expected privatechat field 'list' in data; got data=%v", tc.path, data)
			}
		}
	}
}

// TestRouter_GroupChatRoutesStillWork verifies the surgical change to
// groupchat/api.go (removing the four private-chat stubs) didn't accidentally
// break other group-chat endpoints.
func TestRouter_GroupChatRoutesStillWork(t *testing.T) {
	router := setupFullRouter(t)

	// Pick three representative group-chat endpoints (community / group / chat).
	paths := []string{
		"/api/group-chat/community/list",
		"/api/group-chat/group-list?metaId=test",
		"/api/group-chat/group-chat-list-v2?groupId=test",
	}
	for _, p := range paths {
		w, body := get(t, router, p)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", p, w.Code)
		}
		if _, ok := body["code"]; !ok {
			t.Errorf("%s: missing code field in response: %s", p, w.Body.String())
		}
	}
}

func TestRouter_IDChatChatAPICompatRoutes(t *testing.T) {
	router := setupFullRouter(t)

	cases := []struct {
		path  string
		shape string
	}{
		{"/chat-api/group-chat/community/list", "object_with_results"},
		{"/chat-api/group-chat/group-list?metaId=test", "object_with_list"},
		{"/chat-api/group-chat/private-chat-list?metaId=a&otherMetaId=b", "object_with_list"},
	}

	for _, tc := range cases {
		w, body := get(t, router, tc.path)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d body=%s", tc.path, w.Code, w.Body.String())
			continue
		}
		code, _ := body["code"].(float64)
		if int(code) != 0 {
			t.Errorf("%s: expected code=0, got %v body=%s", tc.path, body["code"], w.Body.String())
			continue
		}
		data, ok := body["data"].(map[string]interface{})
		if !ok {
			t.Errorf("%s: expected object data, got %T %v", tc.path, body["data"], body["data"])
			continue
		}

		switch tc.shape {
		case "object_with_results":
			if _, present := data["results"]; !present {
				t.Errorf("%s: expected data.results, got data=%v", tc.path, data)
			}
		case "object_with_list":
			if _, present := data["list"]; !present {
				t.Errorf("%s: expected data.list, got data=%v", tc.path, data)
			}
		}
	}
}

func TestRouter_IDChatP0RoutesReturnCompatibilityEnvelopes(t *testing.T) {
	router := setupFullRouter(t)

	cases := []struct {
		path     string
		dataKeys []string
	}{
		{"/chat-api/group-chat/group-chat-list?groupId=g1", []string{"total", "nextTimestamp", "list"}},
		{"/chat-api/group-chat/group-chat-list-v2?groupId=g1", []string{"total", "nextTimestamp", "list"}},
		{"/chat-api/group-chat/group-chat-list-by-index?groupId=g1", []string{"total", "lastIndex", "list"}},
		{"/chat-api/group-chat/channel-chat-list-v3?groupId=g1&channelId=c1", []string{"total", "nextTimestamp", "list"}},
		{"/chat-api/group-chat/channel-chat-list-by-index?groupId=g1&channelId=c1", []string{"total", "lastIndex", "list"}},
		{"/chat-api/group-chat/group-channel-list?groupId=g1", []string{"total", "list"}},
		{"/chat-api/group-chat/group-metaid-join-list?groupId=g1&metaId=m1", []string{"metaId", "items"}},
		{"/chat-api/group-chat/private-group-paths?metaId=m1", []string{"total", "list"}},
		{"/chat-api/group-chat/search-groups-and-users?query=m&size=5", []string{"total", "list"}},
		{"/chat-api/group-chat/user/latest-chat-info-list?metaId=m1", []string{"total", "list"}},
	}

	for _, tc := range cases {
		w, body := get(t, router, tc.path)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: want 200 got %d body=%s", tc.path, w.Code, w.Body.String())
		}
		assertResponseDataKeys(t, body, tc.dataKeys...)
	}
}

func TestRouter_IDChatLatestChatInfoListIncludesPrivateSessions(t *testing.T) {
	fixture := setupFullRouterFixture(t)

	if err := fixture.groupAgg.SaveGroup(&groupchat.Group{
		GroupId:       "group-1",
		GroupName:     "General",
		Avatar:        "avatar-1",
		Creator:       "addr-creator",
		CreatorMetaId: "creator-meta",
		MemberCount:   2,
		JoinType:      "public",
		CreatedAt:     1000,
		Chain:         "mvc",
		BlockHeight:   10,
	}); err != nil {
		t.Fatalf("save group: %v", err)
	}
	if err := fixture.groupAgg.SaveGroupMember("group-1", "user-a", &groupchat.GroupMember{
		MetaId:       "user-a",
		GlobalMetaId: "global-a",
		Address:      "addr-a",
		Timestamp:    1000,
	}); err != nil {
		t.Fatalf("save member: %v", err)
	}
	if err := fixture.groupAgg.SaveChatMessage(&groupchat.ChatMessage{
		TxId:         "tx-group",
		PinId:        "tx-groupi0",
		GroupId:      "group-1",
		MetaId:       "user-a",
		GlobalMetaId: "global-a",
		Address:      "addr-a",
		Protocol:     "/protocols/simplegroupchat",
		Content:      "group message",
		ContentType:  "text/plain",
		ChatType:     "msg",
		Timestamp:    1500,
		Chain:        "mvc",
		BlockHeight:  11,
		Index:        1,
	}); err != nil {
		t.Fatalf("save group message: %v", err)
	}
	if err := fixture.privateAgg.SavePrivateMessage(&privatechat.PrivateMessage{
		FromGlobalMetaId: "global-b",
		From:             "user-b",
		FromAddress:      "addr-b",
		FromUserInfo: map[string]interface{}{
			"metaId":        "user-b",
			"globalMetaId":  "global-b",
			"chatPublicKey": "pub-b",
		},
		ToGlobalMetaId: "global-a",
		To:             "user-a",
		ToAddress:      "addr-a",
		TxId:           "tx-private",
		PinId:          "tx-privatei0",
		Protocol:       "/protocols/simplemsg",
		Content:        "private message",
		ContentType:    "text/plain",
		Timestamp:      2000,
		Chain:          "mvc",
		BlockHeight:    12,
		Index:          2,
	}); err != nil {
		t.Fatalf("save private message: %v", err)
	}

	w, body := get(t, fixture.router, "/chat-api/group-chat/user/latest-chat-info-list?metaId=user-a")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data := assertResponseDataKeys(t, body, "total", "list")
	if data["total"] != float64(2) {
		t.Fatalf("total: want 2 got %v body=%s", data["total"], w.Body.String())
	}
	list, ok := data["list"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("list: want two items got %T %v body=%s", data["list"], data["list"], w.Body.String())
	}
	privateItem, ok := list[0].(map[string]interface{})
	if !ok {
		t.Fatalf("first list item should be object: %T %v", list[0], list[0])
	}
	if privateItem["type"] != "2" {
		t.Fatalf("newest item should be private type=2, got %v item=%v", privateItem["type"], privateItem)
	}
	userInfo, ok := privateItem["userInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("private item should include userInfo, got %T %v", privateItem["userInfo"], privateItem["userInfo"])
	}
	if userInfo["chatPublicKey"] != "pub-b" {
		t.Fatalf("private userInfo.chatPublicKey: want pub-b got %v userInfo=%v", userInfo["chatPublicKey"], userInfo)
	}
}

func TestRouter_IDChatLatestChatInfoListHydratesPrivateUserInfoFromProfiles(t *testing.T) {
	fixture := setupFullRouterFixture(t)

	peerProfile := map[string]interface{}{
		"metaid":       "user-b",
		"globalMetaId": "global-b",
		"address":      "addr-b",
		"name":         "User B",
		"avatar":       "avatar-b",
		"chatpubkey":   "pub-b-from-profile",
		"chatpubkeyId": "pub-b-pin",
	}
	rawProfile, _ := json.Marshal(peerProfile)
	if err := fixture.store.Set("userinfo", []byte("profile:user-b"), rawProfile); err != nil {
		t.Fatalf("seed userinfo profile: %v", err)
	}

	if err := fixture.privateAgg.SavePrivateMessage(&privatechat.PrivateMessage{
		FromGlobalMetaId: "global-b",
		From:             "user-b",
		FromAddress:      "addr-b",
		ToGlobalMetaId:   "global-a",
		To:               "user-a",
		ToAddress:        "addr-a",
		TxId:             "tx-private-profile",
		PinId:            "tx-private-profilei0",
		Protocol:         "/protocols/simplemsg",
		Content:          "private message without embedded user info",
		ContentType:      "text/plain",
		Timestamp:        2000,
		Chain:            "mvc",
		BlockHeight:      12,
		Index:            2,
	}); err != nil {
		t.Fatalf("save private message: %v", err)
	}

	w, body := get(t, fixture.router, "/chat-api/group-chat/user/latest-chat-info-list?metaId=user-a")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data := assertResponseDataKeys(t, body, "total", "list")
	list, ok := data["list"].([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("list: want one item got %T %v body=%s", data["list"], data["list"], w.Body.String())
	}
	privateItem, ok := list[0].(map[string]interface{})
	if !ok {
		t.Fatalf("first list item should be object: %T %v", list[0], list[0])
	}
	userInfo, ok := privateItem["userInfo"].(map[string]interface{})
	if !ok {
		t.Fatalf("private item should hydrate userInfo from profile, got %T %v", privateItem["userInfo"], privateItem["userInfo"])
	}
	if userInfo["chatPublicKey"] != "pub-b-from-profile" || userInfo["chatPublicKeyId"] != "pub-b-pin" {
		t.Fatalf("private userInfo chat key mismatch: %v", userInfo)
	}
}

func TestRouter_IDChatGroupInfoAndListReturnOldRoomFields(t *testing.T) {
	fixture := setupFullRouterFixture(t)

	createPin := &aggregator.PinInscription{
		Id:            "compatgrouptxi0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "addr-creator",
		CreateMetaId:  "creator-meta",
		GlobalMetaId:  "creator-global",
		ChainName:     "mvc",
		Timestamp:     1000,
		GenesisHeight: 10,
		ContentBody: mustMarshalJSON(t, map[string]interface{}{
			"groupId":   "compat-group",
			"groupName": "Compatibility Room",
			"groupIcon": "room-avatar",
			"groupNote": "room note",
			"type":      "100",
		}),
	}
	if _, err := fixture.groupAgg.HandleBlockPin(createPin); err != nil {
		t.Fatalf("HandleBlockPin(group create): %v", err)
	}

	w, body := get(t, fixture.router, "/chat-api/group-chat/group-info?groupId=compat-group")
	if w.Code != http.StatusOK {
		t.Fatalf("group-info: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	info := assertResponseDataKeys(t, body, "groupId", "txId", "pinId", "roomName", "roomAvatarUrl", "roomJoinType", "createUserMetaId", "createUserGlobalMetaId", "createUserAddress", "userCount")
	if info["roomName"] != "Compatibility Room" || info["roomAvatarUrl"] != "room-avatar" || info["roomJoinType"] != "100" {
		t.Fatalf("group-info old room fields mismatch: %v", info)
	}
	if info["txId"] != "compatgrouptx" || info["pinId"] != createPin.Id {
		t.Fatalf("group-info tx/pin mismatch: %v", info)
	}

	w, body = get(t, fixture.router, "/chat-api/group-chat/group-list?metaId=creator-global")
	if w.Code != http.StatusOK {
		t.Fatalf("group-list: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data := assertResponseDataKeys(t, body, "total", "list")
	list, ok := data["list"].([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("group-list: want one item got %T %v body=%s", data["list"], data["list"], w.Body.String())
	}
	item, ok := list[0].(map[string]interface{})
	if !ok {
		t.Fatalf("group-list item should be object: %T %v", list[0], list[0])
	}
	for _, key := range []string{"roomName", "roomAvatarUrl", "txId", "pinId", "createUserMetaId", "createUserGlobalMetaId", "createUserAddress", "userCount"} {
		if _, ok := item[key]; !ok {
			t.Fatalf("group-list item missing %s: %v", key, item)
		}
	}
	if item["roomName"] != "Compatibility Room" || item["txId"] != "compatgrouptx" {
		t.Fatalf("group-list old fields mismatch: %v", item)
	}
}

func TestRouter_IDChatChannelHistoryAcceptsChannelIDOnlyAndTimestamp(t *testing.T) {
	fixture := setupFullRouterFixture(t)

	if err := fixture.groupAgg.SaveChatMessage(&groupchat.ChatMessage{
		TxId:         "sub-old",
		PinId:        "sub-oldi0",
		GroupId:      "parent-group",
		ChannelId:    "sub-channel",
		MetaId:       "user-a",
		GlobalMetaId: "global-a",
		Protocol:     "/protocols/simplegroupchat",
		Content:      "old sub message",
		ContentType:  "text/plain",
		ChatType:     "msg",
		Timestamp:    1000,
		Chain:        "mvc",
		BlockHeight:  10,
		Index:        0,
	}); err != nil {
		t.Fatalf("save old message: %v", err)
	}
	if err := fixture.groupAgg.SaveChatMessage(&groupchat.ChatMessage{
		TxId:         "sub-new",
		PinId:        "sub-newi0",
		GroupId:      "parent-group",
		ChannelId:    "sub-channel",
		MetaId:       "user-a",
		GlobalMetaId: "global-a",
		Protocol:     "/protocols/simplegroupchat",
		Content:      "new sub message",
		ContentType:  "text/plain",
		ChatType:     "msg",
		Timestamp:    2000,
		Chain:        "mvc",
		BlockHeight:  20,
		Index:        1,
	}); err != nil {
		t.Fatalf("save new message: %v", err)
	}

	w, body := get(t, fixture.router, "/chat-api/group-chat/channel-chat-list-v3?channelId=sub-channel&timestamp=0&size=20")
	if w.Code != http.StatusOK {
		t.Fatalf("channel-chat-list-v3 channelId-only: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data := assertResponseDataKeys(t, body, "total", "nextTimestamp", "list")
	list, ok := data["list"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("timestamp=0 list: want two items got %T %v body=%s", data["list"], data["list"], w.Body.String())
	}
	first := list[0].(map[string]interface{})
	if first["content"] != "new sub message" {
		t.Fatalf("timestamp=0 should return newest first, got %v", first)
	}

	w, body = get(t, fixture.router, "/chat-api/group-chat/channel-chat-list-v3?channelId=sub-channel&timestamp=1500&size=20")
	if w.Code != http.StatusOK {
		t.Fatalf("channel-chat-list-v3 timestamp: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data = assertResponseDataKeys(t, body, "total", "nextTimestamp", "list")
	list, ok = data["list"].([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("timestamp=1500 list: want one older item got %T %v body=%s", data["list"], data["list"], w.Body.String())
	}
	older := list[0].(map[string]interface{})
	if older["content"] != "old sub message" {
		t.Fatalf("timestamp=1500 should return older message, got %v", older)
	}

	w, body = get(t, fixture.router, "/chat-api/group-chat/channel-chat-list-by-index?channelId=sub-channel&startIndex=0&size=20")
	if w.Code != http.StatusOK {
		t.Fatalf("channel-chat-list-by-index channelId-only: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data = assertResponseDataKeys(t, body, "total", "lastIndex", "list")
	if data["total"] != float64(2) {
		t.Fatalf("by-index total mismatch: %v body=%s", data["total"], w.Body.String())
	}
	if data["lastIndex"] != float64(1) {
		t.Fatalf("by-index lastIndex should be the max message index, got %v body=%s", data["lastIndex"], w.Body.String())
	}
	list, ok = data["list"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("by-index list: want two items got %T %v body=%s", data["list"], data["list"], w.Body.String())
	}
	first = list[0].(map[string]interface{})
	if first["content"] != "old sub message" || first["index"] != float64(0) {
		t.Fatalf("by-index should return ascending continuous index order, got %v", first)
	}
}

func TestRouter_IDChatGroupHistoryHonorsTimestamp(t *testing.T) {
	fixture := setupFullRouterFixture(t)

	for _, msg := range []*groupchat.ChatMessage{
		{TxId: "g-old", PinId: "g-oldi0", GroupId: "group-history", MetaId: "user-a", GlobalMetaId: "global-a", Protocol: "/protocols/simplegroupchat", Content: "old group message", ContentType: "text/plain", ChatType: "msg", Timestamp: 1000, Chain: "mvc", BlockHeight: 10, Index: 0},
		{TxId: "g-new", PinId: "g-newi0", GroupId: "group-history", MetaId: "user-a", GlobalMetaId: "global-a", Protocol: "/protocols/simplegroupchat", Content: "new group message", ContentType: "text/plain", ChatType: "msg", Timestamp: 2000, Chain: "mvc", BlockHeight: 20, Index: 1},
	} {
		if err := fixture.groupAgg.SaveChatMessage(msg); err != nil {
			t.Fatalf("save group message: %v", err)
		}
	}

	w, body := get(t, fixture.router, "/chat-api/group-chat/group-chat-list-v2?groupId=group-history&timestamp=1500&size=20")
	if w.Code != http.StatusOK {
		t.Fatalf("group-chat-list-v2 timestamp: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data := assertResponseDataKeys(t, body, "total", "nextTimestamp", "list")
	list, ok := data["list"].([]interface{})
	if !ok || len(list) != 1 {
		t.Fatalf("timestamp=1500 list: want one older item got %T %v body=%s", data["list"], data["list"], w.Body.String())
	}
	item := list[0].(map[string]interface{})
	if item["content"] != "old group message" {
		t.Fatalf("timestamp=1500 should return older message, got %v", item)
	}
}

func TestRouter_IDChatSocketOnlineUsersCompatibilityRoute(t *testing.T) {
	router := setupPresenceRouter(t, nil)

	w, body := get(t, router, "/chat-api/group-chat/socket/online-users?cursor=&size=20&withUserInfo=true")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data := assertResponseDataKeys(t, body, "total", "cursor", "size", "onlineWindowSeconds", "list")
	if _, ok := data["list"].([]interface{}); !ok {
		t.Fatalf("data.list should be an array, got %T %v", data["list"], data["list"])
	}
}

func TestRouter_IDChatPushBaseCompatRoutes(t *testing.T) {
	router := setupFullRouter(t)

	w, body := get(t, router, "/push-base/v1/push/get_user_blocked_chats?metaId=test")
	if w.Code != http.StatusOK {
		t.Fatalf("get_user_blocked_chats: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	code, _ := body["code"].(float64)
	if int(code) != 0 {
		t.Fatalf("get_user_blocked_chats: expected code=0, got %v body=%s", body["code"], w.Body.String())
	}
	data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("get_user_blocked_chats: expected object data, got %T %v", body["data"], body["data"])
	}
	if _, present := data["blockedChats"]; !present {
		t.Fatalf("get_user_blocked_chats: expected blockedChats in data, got %v", data)
	}

	w, body = postJSON(t, router, "/push-base/v1/push/add_blocked_chat", `{"chatId":"group1","chatType":"group","metaId":"test","reason":"muted"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("add_blocked_chat: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	code, _ = body["code"].(float64)
	if int(code) != 0 {
		t.Fatalf("add_blocked_chat: expected code=0, got %v body=%s", body["code"], w.Body.String())
	}
}

func TestRouter_IDChatCORSCompat(t *testing.T) {
	router := setupFullRouter(t)

	req, _ := http.NewRequest("GET", "/chat-api/group-chat/group-list?metaId=test", nil)
	req.Header.Set("Origin", "https://idchat.io")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /chat-api/group-chat/group-list: expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("GET /chat-api/group-chat/group-list: expected Access-Control-Allow-Origin *, got %q", got)
	}

	req, _ = http.NewRequest("OPTIONS", "/push-base/v1/push/add_blocked_chat", nil)
	req.Header.Set("Origin", "https://idchat.io")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "X-Signature,X-Public-Key,Content-Type")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS /push-base/v1/push/add_blocked_chat: expected 204, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("OPTIONS /push-base/v1/push/add_blocked_chat: expected Access-Control-Allow-Origin *, got %q", got)
	}
	allowHeaders := w.Header().Get("Access-Control-Allow-Headers")
	for _, header := range []string{"Content-Type", "X-Signature", "X-Public-Key"} {
		if !strings.Contains(allowHeaders, header) {
			t.Fatalf("OPTIONS /push-base/v1/push/add_blocked_chat: expected Access-Control-Allow-Headers to include %s, got %q", header, allowHeaders)
		}
	}
	allowMethods := w.Header().Get("Access-Control-Allow-Methods")
	for _, method := range []string{"GET", "POST", "OPTIONS"} {
		if !strings.Contains(allowMethods, method) {
			t.Fatalf("OPTIONS /push-base/v1/push/add_blocked_chat: expected Access-Control-Allow-Methods to include %s, got %q", method, allowMethods)
		}
	}
}

// TestRouter_UserInfoMetaFileCompat verifies userinfo still hits both prefixes
// and uses the meta-file-system code convention (40400 not_found), so the
// privatechat fix did not regress the meta-file-system compatibility commit.
func TestRouter_UserInfoMetaFileCompat(t *testing.T) {
	router := setupFullRouter(t)

	for _, p := range []string{
		"/api/info/metaid/nonexistent",
		"/metafile-indexer/api/info/metaid/nonexistent",
	} {
		w, body := get(t, router, p)
		if w.Code != http.StatusOK {
			t.Errorf("%s: expected 200, got %d", p, w.Code)
		}
		code, _ := body["code"].(float64)
		if int(code) != 40400 {
			t.Errorf("%s: expected code=40400, got %v", p, body["code"])
		}
	}
}
