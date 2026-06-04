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

	"github.com/metaid-developers/meta-socket/internal/aggregator"
	"github.com/metaid-developers/meta-socket/internal/aggregator/groupchat"
	"github.com/metaid-developers/meta-socket/internal/aggregator/notify"
	"github.com/metaid-developers/meta-socket/internal/aggregator/privatechat"
	"github.com/metaid-developers/meta-socket/internal/aggregator/userinfo"
	"github.com/metaid-developers/meta-socket/internal/api"
	"github.com/metaid-developers/meta-socket/internal/cache"
	"github.com/metaid-developers/meta-socket/internal/config"
	"github.com/metaid-developers/meta-socket/internal/presence"
	"github.com/metaid-developers/meta-socket/internal/socket"
	"github.com/metaid-developers/meta-socket/internal/storage"
)

// setupFullRouter mirrors cmd/meta-socket/main.go's aggregator wiring so the
// tests below catch real-world routing regressions (e.g. forgetting to register
// privatechat, which causes its routes to fall back to groupchat stubs).
func setupFullRouter(t *testing.T) *gin.Engine {
	t.Helper()

	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })
	cacheProvider := cache.New(store)

	reg := aggregator.NewRegistry(store, cacheProvider)
	if err := reg.Register(&notify.Aggregator{}); err != nil {
		t.Fatalf("register notify: %v", err)
	}
	if err := reg.Register(&userinfo.Aggregator{}); err != nil {
		t.Fatalf("register userinfo: %v", err)
	}
	if err := reg.Register(&groupchat.Aggregator{}); err != nil {
		t.Fatalf("register groupchat: %v", err)
	}
	if err := reg.Register(&privatechat.Aggregator{}); err != nil {
		t.Fatalf("register privatechat: %v", err)
	}

	cfg := config.Default()
	// SetupRouter handles nil socketServer gracefully (Socket.IO routes skipped).
	return api.SetupRouter(cfg, store, cacheProvider, reg, nil, "test")
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
		Protocol:    "metasocket-presence",
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

	w, _ := get(t, router, "/.well-known/metasocket/presence")
	if w.Code != http.StatusNotFound {
		t.Fatalf("presence endpoint without provider: want 404 got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointReturnsSnapshotFromProvider(t *testing.T) {
	router := setupPresenceRouter(t, fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()})

	w, body := get(t, router, "/.well-known/metasocket/presence")
	if w.Code != http.StatusOK {
		t.Fatalf("presence endpoint: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if body["protocol"] != "metasocket-presence" {
		t.Fatalf("presence endpoint protocol: want metasocket-presence got %v body=%s", body["protocol"], w.Body.String())
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
	if body["protocol"] != "metasocket-presence" {
		t.Fatalf("configured presence endpoint protocol: want metasocket-presence got %v body=%s", body["protocol"], w.Body.String())
	}

	w, _ = get(t, router, "/.well-known/metasocket/presence")
	if w.Code != http.StatusNotFound {
		t.Fatalf("default presence endpoint should not be mounted when custom path is configured: got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointEmptyConfiguredPathFallsBackToDefault(t *testing.T) {
	cfg := config.Default()
	cfg.Federation.PresencePath = ""
	router, _ := setupPresenceRouterWithConfig(t, cfg, fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()})

	w, body := get(t, router, "/.well-known/metasocket/presence")
	if w.Code != http.StatusOK {
		t.Fatalf("fallback presence endpoint: want 200 got %d body=%s", w.Code, w.Body.String())
	}
	if body["protocol"] != "metasocket-presence" {
		t.Fatalf("fallback presence endpoint protocol: want metasocket-presence got %v body=%s", body["protocol"], w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointProviderErrorReturns503(t *testing.T) {
	router := setupPresenceRouter(t, fakePresenceSnapshotProvider{err: errors.New("signing failed")})

	w, _ := get(t, router, "/.well-known/metasocket/presence")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("presence endpoint provider error: want 503 got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRouter_WellKnownPresenceEndpointDoesNotExposeSocketIDs(t *testing.T) {
	router := setupPresenceRouter(t, fakePresenceSnapshotProvider{snapshot: fakePresenceSnapshot()})

	w, body := get(t, router, "/.well-known/metasocket/presence")
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
				w, _ := get(t, router, "/.well-known/metasocket/presence")
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
//   - "array"  = data is an array; the stub would have returned {}.
func TestRouter_PrivateChatRoutesHandledByPrivateChat(t *testing.T) {
	router := setupFullRouter(t)

	cases := []struct {
		path  string
		shape string // "object_with_list" | "array"
	}{
		{"/api/group-chat/private-chat-list?metaId=a&otherMetaId=b", "object_with_list"},
		{"/api/group-chat/private-chat-list-by-index?metaId=a&otherMetaId=b", "object_with_list"},
		{"/api/group-chat/private-group-paths?metaId=a", "array"},
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
		case "array":
			if _, ok := body["data"].([]interface{}); !ok {
				t.Errorf("%s: expected data to be array (privatechat returns []string); groupchat stub would have returned {}; got %T %v",
					tc.path, body["data"], body["data"])
			}
		}
	}
}

func TestRouter_CanonicalPrivateChatRoutesHandledByPrivateChat(t *testing.T) {
	router := setupFullRouter(t)

	cases := []struct {
		path  string
		shape string // "object_with_list" | "array"
	}{
		{"/api/private-chat/messages?metaId=a&otherMetaId=b", "object_with_list"},
		{"/api/private-chat/messages/by-index?metaId=a&otherMetaId=b", "object_with_list"},
		{"/api/private-chat/paths?metaId=a", "array"},
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
		case "array":
			if _, ok := body["data"].([]interface{}); !ok {
				t.Errorf("%s: expected data to be array; got %T %v", tc.path, body["data"], body["data"])
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
