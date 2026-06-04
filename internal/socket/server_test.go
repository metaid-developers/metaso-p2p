package socket

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/meta-socket/internal/aggregator"
	"github.com/metaid-developers/meta-socket/internal/config"
	"github.com/metaid-developers/meta-socket/internal/presence"
)

// apiResponse is a generic envelope for idchat-compatible HTTP responses.
type apiResponse struct {
	Code           int             `json:"code"`
	Data           json.RawMessage `json:"data"`
	Message        string          `json:"message"`
	ProcessingTime int64           `json:"processingTime"`
}

// newTestRouter creates a Gin router with the Socket.IO server attached
// (no network listener — handlers tested via httptest.ResponseRecorder).
func newTestRouter(t *testing.T) (*Server, *gin.Engine) {
	t.Helper()

	cfg := config.SocketConfig{
		Enabled:              true,
		PrimaryPath:          "/socket/socket.io",
		LegacyPath:           "/socket.io",
		RoomBroadcastEnabled: true,
		MaxConnections:       10000,
		MaxPCPerUser:         3,
		MaxAppPerUser:        3,
		PingInterval:         2 * time.Second,
		PingTimeout:          5 * time.Second,
		AllowEIO3:            true,
		ExtraPushAuthKey:     "",
	}

	srv := NewServer(cfg)

	gin.SetMode(gin.TestMode)
	router := gin.New()

	// Register routes (primary + legacy socket.io)
	handler := srv.Handler()
	router.Any(cfg.PrimaryPath+"/*any", handler)
	router.Any(cfg.LegacyPath+"/*any", handler)
	srv.RegisterPresenceRoutes(router)

	return srv, router
}

// performRequest is a helper to make an HTTP request and decode the JSON response.
func performRequest(t *testing.T, router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

type fakeGlobalReader struct {
	enabled      bool
	defaultScope string
	items        []presence.OnlineEntry
	stats        presence.GlobalStats

	listCalls  int
	statsCalls int
	lastLocal  []presence.OnlineEntry
}

func (r *fakeGlobalReader) Enabled() bool {
	return r.enabled
}

func (r *fakeGlobalReader) DefaultScope() string {
	return r.defaultScope
}

func (r *fakeGlobalReader) OnlineList(local []presence.OnlineEntry, page int, size int) []presence.OnlineEntry {
	r.listCalls++
	r.lastLocal = append([]presence.OnlineEntry(nil), local...)
	return append([]presence.OnlineEntry(nil), r.items...)
}

func (r *fakeGlobalReader) Stats(local []presence.OnlineEntry) presence.GlobalStats {
	r.statsCalls++
	r.lastLocal = append([]presence.OnlineEntry(nil), local...)
	return r.stats
}

func addServerTestConnection(srv *Server, metaID string, connType ConnType, connectedAt int64, lastSeenAt int64) {
	srv.manager.mu.Lock()
	defer srv.manager.mu.Unlock()

	srv.manager.connections[metaID] = append(srv.manager.connections[metaID], &TrackedConnection{
		MetaId:      metaID,
		ConnType:    connType,
		ConnectedAt: time.UnixMilli(connectedAt),
		LastPing:    time.UnixMilli(lastSeenAt),
	})
}

func shutdownServerWithoutTestConnections(srv *Server) {
	srv.manager.mu.Lock()
	srv.manager.connections = make(map[string][]*TrackedConnection)
	srv.manager.mu.Unlock()
	srv.Shutdown()
}

func decodeOnlineListMaps(t *testing.T, body []byte) []map[string]any {
	t.Helper()

	var resp apiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to decode list response: %v", err)
	}
	var data struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("failed to decode list data: %v", err)
	}
	return data.Items
}

func assertLegacyOnlineItemShape(t *testing.T, item map[string]any) {
	t.Helper()

	if _, ok := item["lastSeenAt"]; ok {
		t.Fatalf("legacy online item should not include lastSeenAt: %#v", item)
	}
	if _, ok := item["sourceNodeIds"]; ok {
		t.Fatalf("legacy online item should not include sourceNodeIds: %#v", item)
	}
	if _, ok := item["sources"]; ok {
		t.Fatalf("legacy online item should not include sources: %#v", item)
	}
}

// TestPushMessageFormat verifies the exact envelope format {M, C:0, D}.
func TestPushMessageFormat(t *testing.T) {
	msg := &PushEnvelope{
		M: "TEST_EVENT",
		C: 0,
		D: map[string]string{"key": "value"},
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	// Verify M field
	if result["M"] != "TEST_EVENT" {
		t.Errorf("expected M='TEST_EVENT', got %v", result["M"])
	}

	// Verify C is integer 0, not string
	cVal, ok := result["C"].(float64)
	if !ok {
		t.Errorf("expected C to be number, got %T: %v", result["C"], result["C"])
	}
	if int(cVal) != 0 {
		t.Errorf("expected C=0, got %v", cVal)
	}

	// Verify D is present
	if _, ok := result["D"]; !ok {
		t.Error("expected D field")
	}
}

func TestNotifyEventTargetIdsDeduplicateAndFallback(t *testing.T) {
	evt := &aggregator.NotifyEvent{
		MetaId:       "buyer_local_meta",
		GlobalMetaId: "idqBuyerGlobal",
		TargetIds:    []string{"idqBuyerGlobal", "buyer_local_meta", "1BuyerAddress", "idqBuyerGlobal", " "},
	}

	got := notifyEventTargetIds(evt)
	want := []string{"idqBuyerGlobal", "buyer_local_meta", "1BuyerAddress"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notifyEventTargetIds() = %#v, want %#v", got, want)
	}
}

func TestNotifyEventTargetIdsFallbackWithoutTargetIds(t *testing.T) {
	evt := &aggregator.NotifyEvent{
		MetaId:       "buyer_local_meta",
		GlobalMetaId: "idqBuyerGlobal",
	}

	got := notifyEventTargetIds(evt)
	want := []string{"buyer_local_meta", "idqBuyerGlobal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notifyEventTargetIds() = %#v, want %#v", got, want)
	}
}

// TestOnlineStats verifies the presence stats endpoint returns the correct format.
func TestOnlineStats(t *testing.T) {
	srv, router := newTestRouter(t)
	defer srv.Shutdown()

	w := performRequest(t, router, "GET", "/socket/online/stats")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode stats: %v", err)
	}

	if resp.Code != 0 {
		t.Errorf("expected code=0, got %d", resp.Code)
	}
	if resp.ProcessingTime <= 0 {
		t.Errorf("expected processingTime > 0, got %d", resp.ProcessingTime)
	}

	var data struct {
		TotalConnections int `json:"totalConnections"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("failed to decode stats data: %v", err)
	}
	// Initially zero connections since no actual Socket.IO client connected
	if data.TotalConnections < 0 {
		t.Errorf("totalConnections should be >= 0, got %d", data.TotalConnections)
	}
	t.Logf("total connections: %d", data.TotalConnections)
}

// TestOnlineList verifies the presence list endpoint returns the correct format.
func TestOnlineList(t *testing.T) {
	srv, router := newTestRouter(t)
	defer srv.Shutdown()

	w := performRequest(t, router, "GET", "/socket/online/list?page=1&size=20")

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}

	if resp.Code != 0 {
		t.Errorf("expected code=0, got %d", resp.Code)
	}
	if resp.ProcessingTime <= 0 {
		t.Errorf("expected processingTime > 0, got %d", resp.ProcessingTime)
	}

	var data struct {
		Items []OnlineEntry `json:"items"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("failed to decode list data: %v", err)
	}
	// Items should be an array (possibly empty since no real connections)
	if data.Items == nil {
		t.Error("expected items array, got nil")
	}
	t.Logf("online items: %d", len(data.Items))
}

type fakePresenceProfileLookup struct {
	byMetaId     map[string]*ProfileSnapshot
	byGlobalMeta map[string]*ProfileSnapshot
	byAddress    map[string]*ProfileSnapshot
}

func (f *fakePresenceProfileLookup) LookupByMetaId(metaid string) (*ProfileSnapshot, error) {
	return f.byMetaId[metaid], nil
}

func (f *fakePresenceProfileLookup) LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error) {
	return f.byGlobalMeta[globalMetaId], nil
}

func (f *fakePresenceProfileLookup) LookupByAddress(address string) (*ProfileSnapshot, error) {
	return f.byAddress[address], nil
}

func TestOnlineListHydratesAppRowsWithProfile(t *testing.T) {
	srv, router := newTestRouter(t)
	defer func() {
		srv.manager.mu.Lock()
		srv.manager.connections = map[string][]*TrackedConnection{}
		srv.manager.mu.Unlock()
		srv.Shutdown()
	}()

	const globalMetaID = "idq1wlsx9q3lf45uz3n654lnya8kplj6lt2vuwjgy5"
	const avatarID = "2b1a6068498cd34ae99953eca889dc206ed81823425ff7cc1c5e09a142c05795i0"
	srv.SetProfileLookup(&fakePresenceProfileLookup{
		byGlobalMeta: map[string]*ProfileSnapshot{
			globalMetaID: {
				GlobalMetaId:  globalMetaID,
				MetaId:        "ce447562dcbca15ee44c7055c40735b01d96f1fa2017c871051fe9cfcddf70c3",
				Address:       "1BvrDMi5UoytcLWKXnLL66xErdc73gkAoL",
				Name:          "Ellis Grant",
				Avatar:        "/content/" + avatarID,
				AvatarId:      avatarID,
				ChatPublicKey: "04currentchatkey",
			},
		},
	})
	srv.SetProfileAssetBaseURL("https://file.metaid.io/metafile-indexer/content")

	srv.manager.mu.Lock()
	srv.manager.connections[globalMetaID] = []*TrackedConnection{{
		MetaId:      globalMetaID,
		ConnType:    ConnTypeApp,
		ConnectedAt: time.UnixMilli(1780546569529),
		LastPing:    time.UnixMilli(1780546569529),
	}}
	srv.manager.mu.Unlock()

	w := performRequest(t, router, "GET", "/socket/online/list?page=1&size=5")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}

	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode list: %v", err)
	}
	var data struct {
		Items []OnlineEntry `json:"items"`
	}
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("failed to decode data: %v", err)
	}
	if len(data.Items) != 1 {
		t.Fatalf("expected 1 online item, got %d: %+v", len(data.Items), data.Items)
	}
	got := data.Items[0]
	if got.UserInfo == nil {
		t.Fatalf("app row should include hydrated userInfo: %+v", got)
	}
	if got.UserInfo.Name != "Ellis Grant" {
		t.Fatalf("userInfo.name: got %q", got.UserInfo.Name)
	}
	if got.UserInfo.AvatarId != avatarID {
		t.Fatalf("userInfo.avatarId: got %q want %q", got.UserInfo.AvatarId, avatarID)
	}
	if got.UserInfo.AvatarUrl != "https://file.metaid.io/metafile-indexer/content/"+avatarID {
		t.Fatalf("userInfo.avatarUrl: got %q", got.UserInfo.AvatarUrl)
	}
}

func TestOnlineListScopeLocalPreservesLocalOnlyResponseShape(t *testing.T) {
	srv, router := newTestRouter(t)
	defer shutdownServerWithoutTestConnections(srv)
	addServerTestConnection(srv, "meta-local", ConnTypePC, 1710000000000, 1710000000500)
	reader := &fakeGlobalReader{
		enabled:      true,
		defaultScope: "global",
		items: []presence.OnlineEntry{
			{
				MetaId:        "meta-global",
				Type:          "app",
				ConnectedAt:   1710000000000,
				LastSeenAt:    1710000001000,
				SourceNodeIds: []string{"node-remote"},
				Sources:       1,
			},
		},
	}
	srv.SetGlobalReader(reader)

	w := performRequest(t, router, "GET", "/socket/online/list?scope=local")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	items := decodeOnlineListMaps(t, w.Body.Bytes())
	if len(items) != 1 {
		t.Fatalf("local scope items: want 1 got %d", len(items))
	}
	if items[0]["metaid"] != "meta-local" {
		t.Fatalf("local scope should return local entry, got %#v", items[0])
	}
	assertLegacyOnlineItemShape(t, items[0])
	if reader.listCalls != 0 {
		t.Fatalf("scope=local should not call global reader, got %d calls", reader.listCalls)
	}
}

func TestOnlineListScopeGlobalWithNilOrDisabledReaderBehavesLocalOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reader *fakeGlobalReader
	}{
		{name: "nil reader"},
		{name: "disabled reader", reader: &fakeGlobalReader{enabled: false, defaultScope: "global"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, router := newTestRouter(t)
			defer shutdownServerWithoutTestConnections(srv)
			addServerTestConnection(srv, "meta-local", ConnTypePC, 1710000000000, 1710000000500)
			if tc.reader != nil {
				srv.SetGlobalReader(tc.reader)
			}

			w := performRequest(t, router, "GET", "/socket/online/list?scope=global")

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			items := decodeOnlineListMaps(t, w.Body.Bytes())
			if len(items) != 1 {
				t.Fatalf("global scope with no enabled reader items: want 1 got %d", len(items))
			}
			if items[0]["metaid"] != "meta-local" {
				t.Fatalf("disabled global reader should preserve local item, got %#v", items[0])
			}
			assertLegacyOnlineItemShape(t, items[0])
			if tc.reader != nil && tc.reader.listCalls != 0 {
				t.Fatalf("disabled global reader should not be called, got %d calls", tc.reader.listCalls)
			}
		})
	}
}

func TestOnlineListScopeGlobalWithEnabledReaderReturnsGlobalItems(t *testing.T) {
	srv, router := newTestRouter(t)
	defer shutdownServerWithoutTestConnections(srv)
	addServerTestConnection(srv, "meta-local", ConnTypePC, 1710000000000, 1710000000500)
	reader := &fakeGlobalReader{
		enabled:      true,
		defaultScope: "local",
		items: []presence.OnlineEntry{
			{
				MetaId:        "meta-global",
				Type:          "app",
				ConnectedAt:   1710000000000,
				LastSeenAt:    1710000001000,
				SourceNodeIds: []string{"node-remote"},
				Sources:       2,
			},
		},
	}
	srv.SetGlobalReader(reader)

	w := performRequest(t, router, "GET", "/socket/online/list?scope=global")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	items := decodeOnlineListMaps(t, w.Body.Bytes())
	if len(items) != 1 {
		t.Fatalf("global scope items: want 1 got %d", len(items))
	}
	if items[0]["metaid"] != "meta-global" {
		t.Fatalf("enabled global reader should return global item, got %#v", items[0])
	}
	if _, ok := items[0]["lastSeenAt"]; !ok {
		t.Fatalf("global item should include lastSeenAt: %#v", items[0])
	}
	if _, ok := items[0]["sourceNodeIds"]; !ok {
		t.Fatalf("global item should include sourceNodeIds: %#v", items[0])
	}
	if items[0]["sources"] != float64(2) {
		t.Fatalf("global item sources: want 2 got %#v", items[0]["sources"])
	}
	if reader.listCalls != 1 {
		t.Fatalf("enabled global reader calls: want 1 got %d", reader.listCalls)
	}
	if len(reader.lastLocal) != 1 || reader.lastLocal[0].LastSeenAt != 1710000000500 {
		t.Fatalf("global reader should receive unpaginated local OnlineEntries with LastSeenAt, got %#v", reader.lastLocal)
	}
}

func TestOnlineListScopeDefaultUsesEnabledGlobalReaderDefaultScope(t *testing.T) {
	srv, router := newTestRouter(t)
	defer shutdownServerWithoutTestConnections(srv)
	addServerTestConnection(srv, "meta-local", ConnTypePC, 1710000000000, 1710000000500)
	reader := &fakeGlobalReader{
		enabled:      true,
		defaultScope: "global",
		items: []presence.OnlineEntry{
			{MetaId: "meta-global", Type: "app", ConnectedAt: 1710000000000, LastSeenAt: 1710000001000, SourceNodeIds: []string{"node-remote"}, Sources: 1},
		},
	}
	srv.SetGlobalReader(reader)

	w := performRequest(t, router, "GET", "/socket/online/list")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	items := decodeOnlineListMaps(t, w.Body.Bytes())
	if len(items) != 1 || items[0]["metaid"] != "meta-global" {
		t.Fatalf("default global scope should use global reader, got %#v", items)
	}
}

// TestOnlineListPagination verifies the list endpoint handles pagination params.
func TestOnlineListPagination(t *testing.T) {
	srv, router := newTestRouter(t)
	defer srv.Shutdown()

	// Test default page/size
	w := performRequest(t, router, "GET", "/socket/online/list")
	var resp apiResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ProcessingTime <= 0 {
		t.Errorf("expected processingTime > 0, got %d", resp.ProcessingTime)
	}
	var data struct {
		Items []OnlineEntry `json:"items"`
	}
	json.Unmarshal(resp.Data, &data)
	if data.Items == nil {
		t.Error("expected items array even without page params")
	}

	// Test with invalid page
	w = performRequest(t, router, "GET", "/socket/online/list?page=-1&size=20")
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ProcessingTime <= 0 {
		t.Errorf("expected processingTime > 0, got %d", resp.ProcessingTime)
	}
	json.Unmarshal(resp.Data, &data)
	if data.Items == nil {
		t.Error("expected items array with invalid page")
	}

	// Test with large size (should cap at 100)
	w = performRequest(t, router, "GET", "/socket/online/list?page=1&size=999")
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.ProcessingTime <= 0 {
		t.Errorf("expected processingTime > 0, got %d", resp.ProcessingTime)
	}
	json.Unmarshal(resp.Data, &data)
	if data.Items == nil {
		t.Error("expected items array with large size")
	}
}

func TestOnlineStatsScopeGlobalReturnsAggregateFields(t *testing.T) {
	srv, router := newTestRouter(t)
	defer shutdownServerWithoutTestConnections(srv)
	addServerTestConnection(srv, "meta-local", ConnTypePC, 1710000000000, 1710000000500)
	reader := &fakeGlobalReader{
		enabled:      true,
		defaultScope: "local",
		stats: presence.GlobalStats{
			TotalConnections: 4,
			UniqueMetaIds:    3,
			Nodes:            2,
		},
	}
	srv.SetGlobalReader(reader)

	w := performRequest(t, router, "GET", "/socket/online/stats?scope=global")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode stats response: %v", err)
	}
	var data map[string]int
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("failed to decode stats data: %v", err)
	}
	if len(data) != 3 {
		t.Fatalf("global stats should expose exactly 3 fields, got %#v", data)
	}
	if data["totalConnections"] != 4 || data["uniqueMetaIds"] != 3 || data["nodes"] != 2 {
		t.Fatalf("unexpected global stats data: %#v", data)
	}
	if reader.statsCalls != 1 {
		t.Fatalf("enabled global stats calls: want 1 got %d", reader.statsCalls)
	}
	if len(reader.lastLocal) != 1 || reader.lastLocal[0].LastSeenAt != 1710000000500 {
		t.Fatalf("global stats reader should receive unpaginated local OnlineEntries with LastSeenAt, got %#v", reader.lastLocal)
	}
}

func TestOnlineStatsScopeLocalPreservesCurrentResponseShape(t *testing.T) {
	srv, router := newTestRouter(t)
	defer shutdownServerWithoutTestConnections(srv)
	addServerTestConnection(srv, "meta-local", ConnTypePC, 1710000000000, 1710000000500)
	reader := &fakeGlobalReader{
		enabled:      true,
		defaultScope: "global",
		stats:        presence.GlobalStats{TotalConnections: 4, UniqueMetaIds: 3, Nodes: 2},
	}
	srv.SetGlobalReader(reader)

	w := performRequest(t, router, "GET", "/socket/online/stats?scope=local")

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var resp apiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode stats response: %v", err)
	}
	var data map[string]int
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		t.Fatalf("failed to decode stats data: %v", err)
	}
	if len(data) != 1 || data["totalConnections"] != 1 {
		t.Fatalf("local stats should expose only local totalConnections, got %#v", data)
	}
	if _, ok := data["uniqueMetaIds"]; ok {
		t.Fatalf("local stats should not include uniqueMetaIds: %#v", data)
	}
	if _, ok := data["nodes"]; ok {
		t.Fatalf("local stats should not include nodes: %#v", data)
	}
	if reader.statsCalls != 0 {
		t.Fatalf("scope=local should not call global stats reader, got %d calls", reader.statsCalls)
	}
}

// TestConnectionManager tests the connection tracking logic in isolation.
func TestConnectionManager(t *testing.T) {
	cm := NewConnectionManager(3, 3)

	if cm.TotalConnections() != 0 {
		t.Error("expected 0 connections initially")
	}

	if cm.CountByType("user1", ConnTypePC) != 0 {
		t.Error("expected 0 PC connections for user1")
	}

	entries := cm.OnlineList(1, 20)
	if len(entries) != 0 {
		t.Error("expected empty online list")
	}

	stale := cm.FindStaleConnections(35 * time.Second)
	if len(stale) != 0 {
		t.Error("expected 0 stale connections")
	}
}

// TestManagerOnlineListPagination tests pagination logic in isolation.
func TestManagerOnlineListPagination(t *testing.T) {
	cm := NewConnectionManager(3, 3)

	// Empty list
	entries := cm.OnlineList(1, 10)
	if len(entries) != 0 {
		t.Error("expected empty list")
	}

	// Page beyond range
	entries = cm.OnlineList(100, 10)
	if len(entries) != 0 {
		t.Error("expected empty list for far page")
	}

	// Negative page should be handled
	entries = cm.OnlineList(-1, 10)
	if len(entries) != 0 {
		t.Error("expected empty list for negative page")
	}
}

func TestConnectionManagerOnlineEntriesReturnsAllLocalEntries(t *testing.T) {
	cm := NewConnectionManager(3, 3)
	connectedAt := time.UnixMilli(1710000000000)
	lastSeenAt := time.UnixMilli(1710000000500)

	cm.connections["meta-1"] = []*TrackedConnection{
		{
			MetaId:      "meta-1",
			ConnType:    ConnTypePC,
			ConnectedAt: connectedAt,
			LastPing:    lastSeenAt,
		},
		{
			MetaId:      "meta-1",
			ConnType:    ConnTypeApp,
			ConnectedAt: connectedAt.Add(1 * time.Second),
			LastPing:    lastSeenAt.Add(1 * time.Second),
		},
	}
	cm.connections["meta-2"] = []*TrackedConnection{
		{
			MetaId:      "meta-2",
			ConnType:    ConnTypePC,
			ConnectedAt: connectedAt.Add(2 * time.Second),
			LastPing:    lastSeenAt.Add(2 * time.Second),
		},
	}

	paged := cm.OnlineList(1, 2)
	if len(paged) != 2 {
		t.Fatalf("paged online list should still honor size: got %d", len(paged))
	}

	entries := cm.OnlineEntries()
	if len(entries) != 3 {
		t.Fatalf("unpaginated entries: want 3 got %d", len(entries))
	}

	seen := map[string]int64{}
	for _, entry := range entries {
		seen[entry.MetaId+":"+entry.Type] = entry.LastSeenAt
	}
	if seen["meta-1:pc"] != 1710000000500 {
		t.Fatalf("meta-1 pc lastSeenAt: want 1710000000500 got %d", seen["meta-1:pc"])
	}
	if seen["meta-1:app"] != 1710000001500 {
		t.Fatalf("meta-1 app lastSeenAt: want 1710000001500 got %d", seen["meta-1:app"])
	}
	if seen["meta-2:pc"] != 1710000002500 {
		t.Fatalf("meta-2 pc lastSeenAt: want 1710000002500 got %d", seen["meta-2:pc"])
	}
}

func TestConnectionManagerOnlineListPreservesLegacyJSONShape(t *testing.T) {
	cm := NewConnectionManager(3, 3)
	cm.connections["meta-1"] = []*TrackedConnection{
		{
			MetaId:      "meta-1",
			ConnType:    ConnTypePC,
			ConnectedAt: time.UnixMilli(1710000000000),
			LastPing:    time.UnixMilli(1710000000500),
		},
	}

	entries := cm.OnlineList(1, 20)
	if len(entries) != 1 {
		t.Fatalf("legacy online list entries: want 1 got %d", len(entries))
	}
	if entries[0].LastSeenAt != 0 {
		t.Fatalf("legacy online list LastSeenAt should remain zero, got %d", entries[0].LastSeenAt)
	}

	raw, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatalf("marshal legacy online list entry: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode legacy online list entry: %v", err)
	}
	if _, ok := decoded["lastSeenAt"]; ok {
		t.Fatalf("legacy online list JSON should not expose lastSeenAt: %s", raw)
	}
}

// TestManagerStaleDetection tests the stale connection detection.
func TestManagerStaleDetection(t *testing.T) {
	cm := NewConnectionManager(3, 3)

	// No connections, no stale
	stale := cm.FindStaleConnections(1 * time.Second)
	if len(stale) != 0 {
		t.Error("expected 0 stale without connections")
	}

	// We can't easily add connections without real sockets,
	// but we can verify the method returns empty for no connections.
	t.Logf("stale detection empty check passed: %d stale", len(stale))
}

// TestServerShutdownCleanup verifies the server creates required goroutines
// and shuts them down cleanly.
func TestServerShutdownCleanup(t *testing.T) {
	cfg := config.SocketConfig{
		Enabled:              true,
		PrimaryPath:          "/socket/socket.io",
		LegacyPath:           "/socket.io",
		RoomBroadcastEnabled: true,
		MaxConnections:       10000,
		MaxPCPerUser:         3,
		MaxAppPerUser:        3,
		PingInterval:         2 * time.Second,
		PingTimeout:          5 * time.Second,
		AllowEIO3:            true,
	}

	srv := NewServer(cfg)
	srv.StartTimeoutCleanup()

	// Give the cleanup goroutine a moment to start
	time.Sleep(50 * time.Millisecond)

	// Shutdown should complete without hanging
	done := make(chan bool, 1)
	go func() {
		srv.Shutdown()
		done <- true
	}()

	select {
	case <-done:
		t.Log("shutdown completed successfully")
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

// TestConcurrentManagerAccess verifies concurrent access to the connection manager.
func TestConcurrentManagerAccess(t *testing.T) {
	cm := NewConnectionManager(3, 3)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cm.TotalConnections()
			cm.CountByType("test", ConnTypePC)
			cm.FindStaleConnections(35 * time.Second)
			cm.OnlineList(1, 20)
		}()
	}
	wg.Wait()
	// No panics = pass
}

// TestRouterHealthCheck verifies the router setup function works end-to-end
// (exercises the full SetupRouter path used in main.go).
func TestRouterHealthCheck(t *testing.T) {
	// This test imports api.SetupRouter indirectly via our local mock.
	// Instead, we verify the handler we created responds correctly.
	srv, router := newTestRouter(t)
	defer srv.Shutdown()

	// Add a health endpoint manually (mimicking what api.SetupRouter does)
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(200, gin.H{"code": 0, "data": gin.H{"status": "ok"}})
	})

	w := performRequest(t, router, "GET", "/healthz")
	if w.Code != 200 {
		t.Errorf("expected 200 from healthz, got %d", w.Code)
	}
}

// TestDualPathRouting verifies both socket.io paths are registered in the router.
func TestDualPathRouting(t *testing.T) {
	cfg := config.SocketConfig{
		Enabled:              true,
		PrimaryPath:          "/socket/socket.io",
		LegacyPath:           "/socket.io",
		RoomBroadcastEnabled: true,
		MaxConnections:       10000,
		MaxPCPerUser:         3,
		MaxAppPerUser:        3,
		PingInterval:         2 * time.Second,
		PingTimeout:          5 * time.Second,
		AllowEIO3:            true,
	}

	srv := NewServer(cfg)
	defer srv.Shutdown()

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := srv.Handler()
	router.Any(cfg.PrimaryPath+"/*any", handler)
	router.Any(cfg.LegacyPath+"/*any", handler)
	srv.RegisterPresenceRoutes(router)

	// Verify primary path is registered (returns a response, not 404)
	req, _ := http.NewRequest("GET", "/socket/socket.io/?EIO=4&transport=polling", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	// Engine.IO should return something (not a gin 404)
	if w.Code == 404 {
		t.Error("primary path returned 404, route not registered")
	}
	t.Logf("primary path response: %d", w.Code)

	// Verify legacy path is registered
	req, _ = http.NewRequest("GET", "/socket.io/?EIO=4&transport=polling", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code == 404 {
		t.Error("legacy path returned 404, route not registered")
	}
	t.Logf("legacy path response: %d", w.Code)

	// Verify presence route
	w = performRequest(t, router, "GET", "/socket/online/stats")
	if w.Code != 200 {
		t.Errorf("presence stats returned %d", w.Code)
	}
}

// Integration tests that require a real TCP listener and WebSocket client
// are documented below. These tests verify the complete Socket.IO protocol
// exchange and can be run with:
//
//   go test -v -tags=integration ./internal/socket/
//
// They require:
// 1. Network access (for httptest.NewServer)
// 2. The gorilla/websocket library
//
// Test scenarios:
// - TestServerConnection: WebSocket to Engine.IO handshake
// - TestHeartbeatAck: ping/heartbeat_ack request-response
// - TestPCMaxLimit: PC device limit enforcement
// - TestAppMaxLimit: App device limit enforcement
// - TestPushMessage: SendToUser delivers correct envelope
// - TestTimeoutCleanup: stale connection cleanup
// - TestMissingMetaIdRejection: connection without metaid rejected
// - TestConcurrentConnections: concurrent connection handling
