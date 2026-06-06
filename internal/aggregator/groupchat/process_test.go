package groupchat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// setupTestAggregator creates a test-ready groupchat aggregator.
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

func performRequest(t *testing.T, router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req, _ := http.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// --- AC1: Build passes (verified by go build) ---

// --- AC2: Group creation ---

func TestGroupCreation(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Process a simplegroupcreate pin
	pin := &aggregator.PinInscription{
		Id:            "test_tx1:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa",
		CreateMetaId:  "group_creator_1",
		GlobalMetaId:  "global_group_creator_1",
		ChainName:     "btc",
		Timestamp:     time.Now().Unix(),
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   "group_test_001",
			GroupName: "Test Group",
			GroupIcon: "icon_hash_123",
			JoinType:  "0",
		}),
	}

	_, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Fatalf("HandleBlockPin(group create) failed: %v", err)
	}

	// Verify via HTTP endpoint
	w := performRequest(t, router, "GET", "/api/group-chat/group-info?groupId=group_test_001")

	var resp struct {
		Code    int    `json:"code"`
		Data    Group  `json:"data"`
		Message string `json:"message"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Errorf("expected code=0, got %d: %s", resp.Code, w.Body.String())
	}
	if resp.Data.GroupName != "Test Group" {
		t.Errorf("expected groupName='Test Group', got %q", resp.Data.GroupName)
	}
	if resp.Data.CreatorMetaId != "group_creator_1" {
		t.Errorf("expected creator='group_creator_1', got %q", resp.Data.CreatorMetaId)
	}
	if resp.Data.MemberCount != 1 {
		t.Errorf("expected memberCount=1, got %d", resp.Data.MemberCount)
	}
	t.Logf("Group creation OK: groupName=%s creator=%s memberCount=%d",
		resp.Data.GroupName, resp.Data.CreatorMetaId, resp.Data.MemberCount)
}

// --- AC3: Chat message persistence ---

func TestChatMessagePersistence(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// First create a group
	createPin := &aggregator.PinInscription{
		Id:            "create_tx:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "creator1",
		GlobalMetaId:  "global_creator1",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   "chat_test_group",
			GroupName: "Chat Test Group",
		}),
	}
	agg.HandleBlockPin(createPin)

	// Process a chat message pin
	chatPin := &aggregator.PinInscription{
		Id:            "chat_tx:i0",
		Path:          "/protocols/simplegroupchat",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "sender1",
		GlobalMetaId:  "global_sender1",
		ChainName:     "btc",
		Timestamp:     2000,
		GenesisHeight: 100,
		ContentBody: mustMarshal(t, SimpleGroupChat{
			GroupId:     "chat_test_group",
			Content:     "Hello, world!",
			ContentType: "text/plain",
			Encryption:  "none",
		}),
	}

	_, err := agg.HandleBlockPin(chatPin)
	if err != nil {
		t.Fatalf("HandleBlockPin(chat) failed: %v", err)
	}

	// Verify via HTTP
	w := performRequest(t, router, "GET", "/api/group-chat/group-chat-list-v2?groupId=chat_test_group&cursor=&size=20")

	var resp struct {
		Code int            `json:"code"`
		Data ChatListResult `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %d: %s", resp.Code, w.Body.String())
	}
	if len(resp.Data.List) == 0 {
		t.Fatal("expected at least one message")
	}

	msg := resp.Data.List[0]
	if msg.Content != "Hello, world!" {
		t.Errorf("expected content='Hello, world!', got %q", msg.Content)
	}
	if msg.GroupId != "chat_test_group" {
		t.Errorf("expected groupId='chat_test_group', got %q", msg.GroupId)
	}
	if msg.MetaId != "sender1" {
		t.Errorf("expected metaId='sender1', got %q", msg.MetaId)
	}
	if msg.GlobalMetaId != "global_sender1" {
		t.Errorf("expected globalMetaId='global_sender1', got %q", msg.GlobalMetaId)
	}
	if msg.ContentType != "text/plain" {
		t.Errorf("expected contentType='text/plain', got %q", msg.ContentType)
	}
	if msg.Encryption != "none" {
		t.Errorf("expected encryption='none', got %q", msg.Encryption)
	}
	if msg.ChatType != "msg" {
		t.Errorf("expected chatType='msg', got %q", msg.ChatType)
	}
	if msg.BlockHeight != 100 {
		t.Errorf("expected blockHeight=100, got %d", msg.BlockHeight)
	}
	if msg.Chain != "btc" {
		t.Errorf("expected chain='btc', got %q", msg.Chain)
	}

	t.Logf("Chat message OK: content=%s metaId=%s globalMetaId=%s blockHeight=%d",
		msg.Content, msg.MetaId, msg.GlobalMetaId, msg.BlockHeight)
}

// --- AC4: Pagination ---

func TestChatPagination(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Create a group
	createPin := &aggregator.PinInscription{
		Id:            "page_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "creator1",
		GlobalMetaId:  "global_creator1",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   "page_test_group",
			GroupName: "Pagination Test",
		}),
	}
	agg.HandleBlockPin(createPin)

	// Insert 100 messages with distinct timestamps
	for i := 0; i < 100; i++ {
		chatPin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("page_tx_%d:i0", i),
			Path:          "/protocols/simplegroupchat",
			Operation:     "create",
			CreateAddress: "1Addr1",
			CreateMetaId:  fmt.Sprintf("sender_%d", i),
			GlobalMetaId:  fmt.Sprintf("global_sender_%d", i),
			ChainName:     "btc",
			Timestamp:     2000 + int64(i),
			GenesisHeight: int64(i),
			ContentBody: mustMarshal(t, SimpleGroupChat{
				GroupId:     "page_test_group",
				Content:     fmt.Sprintf("Message %d", i),
				ContentType: "text/plain",
			}),
		}
		agg.HandleBlockPin(chatPin)
	}

	// Query page 1 (first 20, newest first)
	w1 := performRequest(t, router, "GET", "/api/group-chat/group-chat-list-v2?groupId=page_test_group&cursor=&size=20")
	var resp1 struct {
		Code int            `json:"code"`
		Data ChatListResult `json:"data"`
	}
	json.Unmarshal(w1.Body.Bytes(), &resp1)

	if resp1.Code != 0 {
		t.Fatalf("page 1 failed: code=%d", resp1.Code)
	}
	if len(resp1.Data.List) != 20 {
		t.Errorf("page 1: expected 20 messages, got %d", len(resp1.Data.List))
	}
	if resp1.Data.Total != 100 {
		t.Errorf("expected total=100, got %d", resp1.Data.Total)
	}
	// First message (index 0) should be the newest: "Message 99"
	if resp1.Data.List[0].Content != "Message 99" {
		t.Errorf("page 1 first msg: expected 'Message 99', got %q", resp1.Data.List[0].Content)
	}
	// Last message on page 1 should be "Message 80"
	if resp1.Data.List[19].Content != "Message 80" {
		t.Errorf("page 1 last msg: expected 'Message 80', got %q", resp1.Data.List[19].Content)
	}
	t.Logf("Page 1: total=%d nextCursor=%q first=%q last=%q",
		resp1.Data.Total, resp1.Data.NextCursor,
		resp1.Data.List[0].Content, resp1.Data.List[19].Content)

	// Verify pages 2-4 have non-empty nextCursor, page 5 has empty nextCursor
	// We'll test by iterating through pages
	cursor := ""
	page := 1
	seenMessages := make(map[string]bool)
	for {
		w := performRequest(t, router, "GET",
			fmt.Sprintf("/api/group-chat/group-chat-list-v2?groupId=page_test_group&cursor=%s&size=20", cursor))
		var resp struct {
			Code int            `json:"code"`
			Data ChatListResult `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)

		if resp.Code != 0 {
			t.Fatalf("page %d failed: code=%d", page, resp.Code)
		}

		for _, msg := range resp.Data.List {
			if seenMessages[msg.Content] {
				t.Errorf("page %d: duplicate message %q", page, msg.Content)
			}
			seenMessages[msg.Content] = true
		}

		if page < 5 && resp.Data.NextCursor == "" {
			t.Errorf("page %d: expected non-empty nextCursor", page)
		}
		if page == 5 && resp.Data.NextCursor != "" {
			t.Errorf("page %d: expected empty nextCursor, got %q", page, resp.Data.NextCursor)
		}

		cursor = resp.Data.NextCursor
		page++
		if cursor == "" {
			break
		}
	}

	if page != 6 { // 5 pages + 1 final iteration
		t.Errorf("expected 5 pages, got %d", page-1)
	}
	if len(seenMessages) != 100 {
		t.Errorf("expected 100 unique messages, got %d", len(seenMessages))
	}

	t.Logf("Pagination OK: %d pages, %d unique messages", page-1, len(seenMessages))
}

// --- AC5: Index query ---

func TestChatListByIndex(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Create group
	createPin := &aggregator.PinInscription{
		Id:            "idx_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "creator1",
		GlobalMetaId:  "global_creator1",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   "idx_test_group",
			GroupName: "Index Test",
		}),
	}
	agg.HandleBlockPin(createPin)

	// Insert 50 messages
	for i := 0; i < 50; i++ {
		chatPin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("idx_tx_%d:i0", i),
			Path:          "/protocols/simplegroupchat",
			Operation:     "create",
			CreateAddress: "1Addr1",
			CreateMetaId:  fmt.Sprintf("sender_%d", i),
			GlobalMetaId:  fmt.Sprintf("global_sender_%d", i),
			ChainName:     "btc",
			Timestamp:     2000 + int64(i),
			ContentBody: mustMarshal(t, SimpleGroupChat{
				GroupId:     "idx_test_group",
				Content:     fmt.Sprintf("IndexMessage %d", i),
				ContentType: "text/plain",
			}),
		}
		agg.HandleBlockPin(chatPin)
	}

	// Query by index: startIndex=0, size=20 (idchat expects ascending message indexes 0..19)
	w := performRequest(t, router, "GET", "/api/group-chat/group-chat-list-by-index?groupId=idx_test_group&startIndex=0&size=20")
	var resp struct {
		Code int                   `json:"code"`
		Data ChatListByIndexResult `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("index query failed: code=%d", resp.Code)
	}
	if len(resp.Data.List) != 20 {
		t.Errorf("expected 20 messages, got %d", len(resp.Data.List))
	}
	if resp.Data.LastIndex != 19 {
		t.Errorf("expected lastIndex=19, got %d", resp.Data.LastIndex)
	}
	if resp.Data.List[0].Content != "IndexMessage 0" || resp.Data.List[0].Index != 0 {
		t.Errorf("expected index 0 as first, got content=%q index=%d", resp.Data.List[0].Content, resp.Data.List[0].Index)
	}
	if resp.Data.List[19].Content != "IndexMessage 19" || resp.Data.List[19].Index != 19 {
		t.Errorf("expected index 19 as 20th, got content=%q index=%d", resp.Data.List[19].Content, resp.Data.List[19].Index)
	}

	// Query next page: startIndex=20, size=20
	w2 := performRequest(t, router, "GET", "/api/group-chat/group-chat-list-by-index?groupId=idx_test_group&startIndex=20&size=20")
	var resp2 struct {
		Code int                   `json:"code"`
		Data ChatListByIndexResult `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &resp2)

	if len(resp2.Data.List) != 20 {
		t.Errorf("page 2: expected 20 messages, got %d", len(resp2.Data.List))
	}
	if resp2.Data.LastIndex != 39 {
		t.Errorf("page 2: expected lastIndex=39, got %d", resp2.Data.LastIndex)
	}
	if resp2.Data.List[0].Content != "IndexMessage 20" || resp2.Data.List[0].Index != 20 {
		t.Errorf("page 2 first: expected index 20, got content=%q index=%d", resp2.Data.List[0].Content, resp2.Data.List[0].Index)
	}

	t.Logf("Index query OK: page1=%d msgs, page2=%d msgs",
		len(resp.Data.List), len(resp2.Data.List))
}

// --- AC6: Socket push notification ---

func TestSocketPushNotification(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	// Create group
	createPin := &aggregator.PinInscription{
		Id:            "push_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "creator1",
		GlobalMetaId:  "global_creator1",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   "push_test_group",
			GroupName: "Push Test",
		}),
	}
	agg.HandleBlockPin(createPin)

	// Process a chat message and check for NotifyEvent
	chatPin := &aggregator.PinInscription{
		Id:            "push_chat:i0",
		Path:          "/protocols/simplegroupchat",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "sender1",
		GlobalMetaId:  "global_sender1",
		ChainName:     "btc",
		Timestamp:     2000,
		ContentBody: mustMarshal(t, SimpleGroupChat{
			GroupId:     "push_test_group",
			Content:     "Push message!",
			ContentType: "text/plain",
		}),
	}

	evt, err := agg.HandleBlockPin(chatPin)
	if err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}

	// Verify the event is sent on notifyCh
	if evt == nil {
		t.Fatal("expected NotifyEvent, got nil")
	}
	if evt.Type != "WS_SERVER_NOTIFY_GROUP_CHAT" {
		t.Errorf("expected Type='WS_SERVER_NOTIFY_GROUP_CHAT', got %q", evt.Type)
	}
	if evt.GroupId != "push_test_group" {
		t.Errorf("expected GroupId='push_test_group', got %q", evt.GroupId)
	}
	if !containsString(evt.TargetIds, "creator1") {
		t.Fatalf("expected group chat TargetIds to include creator metaId, got %#v", evt.TargetIds)
	}
	if !containsString(evt.TargetIds, "global_creator1") {
		t.Fatalf("expected group chat TargetIds to include creator globalMetaId, got %#v", evt.TargetIds)
	}

	// Also check the notify channel
	select {
	case chEvent := <-agg.NotifyChannel():
		if chEvent.Type != "WS_SERVER_NOTIFY_GROUP_CHAT" {
			t.Errorf("channel event: expected Type='WS_SERVER_NOTIFY_GROUP_CHAT', got %q", chEvent.Type)
		}
		t.Logf("Socket push OK: type=%s groupId=%s", chEvent.Type, chEvent.GroupId)
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for notify event on channel")
	}
}

// --- AC7: Role change push ---

func TestRoleChangePush(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	// Create group
	createPin := &aggregator.PinInscription{
		Id:            "role_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "creator1",
		GlobalMetaId:  "global_creator1",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   "role_test_group",
			GroupName: "Role Test",
		}),
	}
	agg.HandleBlockPin(createPin)

	// Process an admin assignment
	adminPin := &aggregator.PinInscription{
		Id:            "role_admin:i0",
		Path:          "/protocols/simplegroupadmin",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "creator1",
		GlobalMetaId:  "global_creator1",
		ChainName:     "btc",
		Timestamp:     2000,
		ContentBody: mustMarshal(t, SimpleGroupAdmin{
			GroupId: "role_test_group",
			Admins:  []string{"new_admin_1", "new_admin_2"},
		}),
	}

	evt, err := agg.HandleBlockPin(adminPin)
	if err != nil {
		t.Fatalf("HandleBlockPin(admin) failed: %v", err)
	}

	if evt == nil {
		t.Fatal("expected NotifyEvent for admin role, got nil")
	}
	if evt.Type != "WS_SERVER_NOTIFY_GROUP_ROLE" {
		t.Errorf("expected Type='WS_SERVER_NOTIFY_GROUP_ROLE', got %q", evt.Type)
	}
	if evt.GroupId != "role_test_group" {
		t.Errorf("expected GroupId='role_test_group', got %q", evt.GroupId)
	}

	// Verify the payload has isAdmin=true
	payload, ok := evt.Payload.(map[string]interface{})
	if !ok {
		t.Fatal("expected payload to be map[string]interface{}")
	}
	if isAdmin, _ := payload["isAdmin"].(bool); !isAdmin {
		t.Error("expected isAdmin=true in payload")
	}

	// Verify the role was persisted
	role, err := agg.GetGroupUserRole("role_test_group", "new_admin_1")
	if err != nil {
		t.Fatalf("GetGroupUserRole failed: %v", err)
	}
	if role == nil || !role.IsAdmin {
		t.Error("expected new_admin_1 to be admin in persisted role")
	}

	t.Logf("Role change OK: type=%s groupId=%s isAdmin=%v", evt.Type, evt.GroupId, payload["isAdmin"])
}

// --- AC8: User search ---

func TestSearchUsers(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	// Pre-populate userinfo namespace with some profiles
	userNs := "userinfo"
	profiles := []map[string]interface{}{
		{"metaid": "alice123", "name": "Alice", "avatar": "/content/av1", "address": "1AddrAlice"},
		{"metaid": "bob456", "name": "Bob Smith", "avatar": "/content/av2", "address": "1AddrBob"},
		{"metaid": "charlie789", "name": "Charlie", "avatar": "/content/av3", "address": "1AddrCharlie"},
		{"metaid": "alice_work", "name": "Alice Work", "avatar": "/content/av4", "address": "1AddrAliceWork"},
	}

	for _, p := range profiles {
		raw, _ := json.Marshal(p)
		metaid, _ := p["metaid"].(string)
		store.Set(userNs, []byte("profile:"+metaid), raw)
	}

	// Search for "Alice" - should find alice123 and alice_work
	results, err := agg.SearchUsers("Alice", 10)
	if err != nil {
		t.Fatalf("SearchUsers failed: %v", err)
	}

	if len(results) < 2 {
		t.Errorf("expected at least 2 results for 'Alice', got %d", len(results))
	}

	foundAlice := false
	foundAliceWork := false
	for _, r := range results {
		metaid, _ := r["metaid"].(string)
		name, _ := r["name"].(string)
		if metaid == "alice123" && name == "Alice" {
			foundAlice = true
		}
		if metaid == "alice_work" && name == "Alice Work" {
			foundAliceWork = true
		}
	}

	if !foundAlice {
		t.Error("expected to find alice123")
	}
	if !foundAliceWork {
		t.Error("expected to find alice_work")
	}

	// Search via HTTP endpoint
	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))

	w := performRequest(t, router, "GET", "/api/group-chat/search-users?query=Bob")
	var resp struct {
		Code int                    `json:"code"`
		Data map[string]interface{} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Errorf("HTTP search-users: expected code=0, got %d", resp.Code)
	}

	t.Logf("User search OK: %d results for 'Alice', %v results for 'Bob'", len(results), resp.Code == 0)
}

// --- AC9: Group member query ---

func TestGroupMemberQuery(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Create a group
	createPin := &aggregator.PinInscription{
		Id:            "member_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "1AddrCreator",
		CreateMetaId:  "creator99",
		GlobalMetaId:  "global_creator99",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   "member_test_group",
			GroupName: "Member Test Group",
		}),
	}
	agg.HandleBlockPin(createPin)

	// Add members via join
	for i := 0; i < 5; i++ {
		joinPin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("member_join_%d:i0", i),
			Path:          "/protocols/simplegroupjoin",
			Operation:     "create",
			CreateAddress: fmt.Sprintf("1Addr%d", i),
			CreateMetaId:  fmt.Sprintf("member_%d", i),
			GlobalMetaId:  fmt.Sprintf("global_member_%d", i),
			ChainName:     "btc",
			Timestamp:     2000,
			ContentBody: mustMarshal(t, SimpleGroupJoin{
				GroupId: "member_test_group",
				State:   float64(1), // join
			}),
		}
		agg.HandleBlockPin(joinPin)
	}

	// Make some admins
	adminPin := &aggregator.PinInscription{
		Id:            "member_admin:i0",
		Path:          "/protocols/simplegroupadmin",
		Operation:     "create",
		CreateAddress: "1AddrCreator",
		CreateMetaId:  "creator99",
		GlobalMetaId:  "global_creator99",
		ChainName:     "btc",
		Timestamp:     3000,
		ContentBody: mustMarshal(t, SimpleGroupAdmin{
			GroupId: "member_test_group",
			Admins:  []string{"member_0", "member_1"},
		}),
	}
	agg.HandleBlockPin(adminPin)

	// Query member list via HTTP
	w := performRequest(t, router, "GET", "/api/group-chat/group-member-list?groupId=member_test_group&cursor=&size=20")
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Admins    []GroupMember `json:"admins"`
			Creator   string        `json:"creator"`
			List      []GroupMember `json:"list"`
			WhiteList []GroupMember `json:"whiteList"`
			BlockList []GroupMember `json:"blockList"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("member list failed: code=%d %s", resp.Code, w.Body.String())
	}
	if resp.Data.Creator != "creator99" {
		t.Errorf("expected creator='creator99', got %q", resp.Data.Creator)
	}
	if len(resp.Data.Admins) < 2 {
		t.Errorf("expected at least 2 admins, got %d", len(resp.Data.Admins))
	}

	// Creator + 5 joined members = 6 members total
	if len(resp.Data.List) < 6 {
		t.Errorf("expected at least 6 members, got %d", len(resp.Data.List))
	}

	// Check admin flag on member_0
	foundAdmin := false
	for _, m := range resp.Data.List {
		if m.MetaId == "member_0" && m.IsAdmin {
			foundAdmin = true
		}
	}
	if !foundAdmin {
		t.Error("expected member_0 to be admin")
	}

	t.Logf("Member query OK: creator=%s admins=%d members=%d",
		resp.Data.Creator, len(resp.Data.Admins), len(resp.Data.List))
}

func TestGroupMetaIdJoinListPreservesPrivateGroupKey(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	createPin := &aggregator.PinInscription{
		Id:            "private_group_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "addr_creator",
		CreateMetaId:  "creator_local",
		GlobalMetaId:  "creator_global",
		ChainName:     "mvc",
		Timestamp:     1000,
		GenesisHeight: 100,
		ContentBody: mustMarshal(t, map[string]interface{}{
			"groupId":   "private_group",
			"groupName": "Private Group",
			"type":      "100",
		}),
	}
	if _, err := agg.HandleBlockPin(createPin); err != nil {
		t.Fatalf("HandleBlockPin(group create): %v", err)
	}

	joinPin := &aggregator.PinInscription{
		Id:            "private_group_join:i0",
		Path:          "/protocols/simplegroupjoin",
		Operation:     "create",
		CreateAddress: "addr_member",
		CreateMetaId:  "member_local",
		GlobalMetaId:  "member_global",
		ChainName:     "mvc",
		Timestamp:     2000,
		GenesisHeight: 200,
		ContentBody: mustMarshal(t, map[string]interface{}{
			"groupId":  "private_group",
			"state":    1,
			"referrer": "creator_global",
			"k":        "private-pass-key",
		}),
	}
	if _, err := agg.HandleBlockPin(joinPin); err != nil {
		t.Fatalf("HandleBlockPin(group join): %v", err)
	}

	w := performRequest(t, router, "GET", "/api/group-chat/group-metaid-join-list?groupId=private_group&metaId=member_global")
	var resp struct {
		Code int `json:"code"`
		Data struct {
			MetaId string `json:"metaId"`
			Items  []struct {
				JoinPinId      string `json:"joinPinId"`
				JoinType       string `json:"joinType"`
				JoinTimestamp  int64  `json:"joinTimestamp"`
				GroupState     int64  `json:"groupState"`
				Address        string `json:"address"`
				Referrer       string `json:"referrer"`
				K              string `json:"k"`
				BlockHeight    int64  `json:"blockHeight"`
				Chain          string `json:"chain"`
				ByGlobalMetaId string `json:"byGlobalMetaId"`
				ByMetaId       string `json:"byMetaId"`
				ByAddress      string `json:"byAddress"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %d body=%s", resp.Code, w.Body.String())
	}
	if resp.Data.MetaId != "member_global" {
		t.Fatalf("expected metaId=member_global, got %q", resp.Data.MetaId)
	}

	var joinItemFound bool
	for _, item := range resp.Data.Items {
		if item.JoinPinId != joinPin.Id {
			continue
		}
		joinItemFound = true
		if item.K != "private-pass-key" {
			t.Fatalf("expected k to be preserved, got %q in %#v", item.K, item)
		}
		if item.Referrer != "creator_global" {
			t.Fatalf("expected referrer=creator_global, got %q", item.Referrer)
		}
		if item.JoinType != "join" {
			t.Fatalf("expected joinType=join, got %q", item.JoinType)
		}
		if item.GroupState != 1 {
			t.Fatalf("expected groupState=1, got %d", item.GroupState)
		}
		if item.ByGlobalMetaId != "member_global" || item.ByMetaId != "member_local" || item.ByAddress != "addr_member" {
			t.Fatalf("expected by-user aliases to be stored, got %#v", item)
		}
	}
	if !joinItemFound {
		t.Fatalf("expected join item %s in %#v", joinPin.Id, resp.Data.Items)
	}
}

func TestGroupListAndLatestChatInfoUseMemberIdentityAliases(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	if err := agg.SaveGroup(&Group{
		GroupId:       "alias_group",
		GroupName:     "Alias Group",
		Avatar:        "alias_icon",
		Creator:       "addr_creator",
		CreatorMetaId: "creator_local",
		MemberCount:   1,
		JoinType:      "0",
		CreatedAt:     1000,
		Chain:         "mvc",
		BlockHeight:   100,
	}); err != nil {
		t.Fatalf("SaveGroup: %v", err)
	}
	if err := agg.SaveGroupMember("alias_group", "member_local", &GroupMember{
		MetaId:       "member_local",
		GlobalMetaId: "member_global",
		Address:      "addr_member",
		Timestamp:    1000,
	}); err != nil {
		t.Fatalf("SaveGroupMember: %v", err)
	}
	if err := agg.SaveChatMessage(&ChatMessage{
		TxId:         "alias_chat",
		PinId:        "alias_chati0",
		GroupId:      "alias_group",
		MetaId:       "member_local",
		GlobalMetaId: "member_global",
		Address:      "addr_member",
		Protocol:     "/protocols/simplegroupchat",
		Content:      "alias message",
		ContentType:  "text/plain",
		ChatType:     "msg",
		Timestamp:    2000,
		Chain:        "mvc",
		BlockHeight:  200,
		Index:        1,
	}); err != nil {
		t.Fatalf("SaveChatMessage: %v", err)
	}

	for _, identity := range []string{"member_local", "member_global", "addr_member"} {
		groups, _, total, err := agg.GetGroupList(identity, "", 20)
		if err != nil {
			t.Fatalf("GetGroupList(%s): %v", identity, err)
		}
		if total != 1 || len(groups) != 1 || groups[0].GroupId != "alias_group" {
			t.Fatalf("GetGroupList(%s) = total %d groups %#v, want alias_group", identity, total, groups)
		}

		latest, err := agg.GetUserLatestChatInfoList(identity)
		if err != nil {
			t.Fatalf("GetUserLatestChatInfoList(%s): %v", identity, err)
		}
		var found bool
		for _, item := range latest {
			if item.Type == "1" && item.GroupId == "alias_group" {
				found = true
				if item.Content != "alias message" {
					t.Fatalf("latest content for %s = %q, want alias message", identity, item.Content)
				}
			}
		}
		if !found {
			t.Fatalf("GetUserLatestChatInfoList(%s) missing alias_group in %#v", identity, latest)
		}
	}
}

func TestGroupChatPinsAssignContinuousIndexes(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	createPin := &aggregator.PinInscription{
		Id:            "index_group_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "addr_creator",
		CreateMetaId:  "creator_local",
		GlobalMetaId:  "creator_global",
		ChainName:     "mvc",
		Timestamp:     1000,
		GenesisHeight: 100,
		ContentBody: mustMarshal(t, map[string]interface{}{
			"groupId":   "index_group",
			"groupName": "Index Group",
		}),
	}
	if _, err := agg.HandleBlockPin(createPin); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	for i, content := range []string{"first group message", "second group message"} {
		pin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("index_group_msg_%d:i0", i+1),
			Path:          "/protocols/simplegroupchat",
			Operation:     "create",
			CreateAddress: "addr_creator",
			CreateMetaId:  "creator_local",
			GlobalMetaId:  "creator_global",
			ChainName:     "mvc",
			Timestamp:     int64(2000 + i),
			GenesisHeight: int64(200 + i),
			ContentBody: mustMarshal(t, map[string]interface{}{
				"groupId":     "index_group",
				"content":     content,
				"contentType": "text/plain",
			}),
		}
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(group chat %d): %v", i+1, err)
		}
	}

	groupResult, err := agg.GetChatListByIndexCompat("index_group", 0, 10)
	if err != nil {
		t.Fatalf("GetChatListByIndexCompat: %v", err)
	}
	if len(groupResult.List) != 2 || groupResult.LastIndex != 1 {
		t.Fatalf("group by-index should return two indexed messages and lastIndex=1, got lastIndex=%d list=%#v", groupResult.LastIndex, groupResult.List)
	}
	for i, msg := range groupResult.List {
		if msg.Index != int64(i) {
			t.Fatalf("group message %d index=%d, want %d; list=%#v", i, msg.Index, i, groupResult.List)
		}
	}

	for i, content := range []string{"first channel message", "second channel message"} {
		pin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("index_channel_msg_%d:i0", i+1),
			Path:          "/protocols/simplegroupchat",
			Operation:     "create",
			CreateAddress: "addr_creator",
			CreateMetaId:  "creator_local",
			GlobalMetaId:  "creator_global",
			ChainName:     "mvc",
			Timestamp:     int64(3000 + i),
			GenesisHeight: int64(300 + i),
			ContentBody: mustMarshal(t, map[string]interface{}{
				"groupId":     "index_group",
				"channelId":   "index_channel",
				"content":     content,
				"contentType": "text/plain",
			}),
		}
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(channel chat %d): %v", i+1, err)
		}
	}

	channelResult, err := agg.GetChannelChatListByIndex("", "index_channel", 0, 10)
	if err != nil {
		t.Fatalf("GetChannelChatListByIndex: %v", err)
	}
	if len(channelResult.List) != 2 || channelResult.LastIndex != 1 {
		t.Fatalf("channel by-index should return two indexed messages and lastIndex=1, got lastIndex=%d list=%#v", channelResult.LastIndex, channelResult.List)
	}
	for i, msg := range channelResult.List {
		if msg.Index != int64(i) {
			t.Fatalf("channel message %d index=%d, want %d; list=%#v", i, msg.Index, i, channelResult.List)
		}
	}
}

func TestGroupChannelListUsesChannelPinMetadataAndNewestMessage(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	createPin := &aggregator.PinInscription{
		Id:            "channel_group_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "addr_creator",
		CreateMetaId:  "creator_local",
		GlobalMetaId:  "creator_global",
		ChainName:     "mvc",
		Timestamp:     1000,
		GenesisHeight: 100,
		ContentBody: mustMarshal(t, map[string]interface{}{
			"groupId":   "channel_group",
			"groupName": "Channel Group",
			"type":      "0",
		}),
	}
	if _, err := agg.HandleBlockPin(createPin); err != nil {
		t.Fatalf("HandleBlockPin(group create): %v", err)
	}

	channelPin := &aggregator.PinInscription{
		Id:            "channel_create:i0",
		Path:          "/protocols/simplegroupchannel",
		Operation:     "create",
		CreateAddress: "addr_creator",
		CreateMetaId:  "creator_local",
		GlobalMetaId:  "creator_global",
		ChainName:     "mvc",
		Timestamp:     1500,
		GenesisHeight: 150,
		ContentBody: mustMarshal(t, map[string]interface{}{
			"groupId":     "channel_group",
			"channelId":   "",
			"channelName": "Announcements",
			"channelIcon": "metafile://channel-icon",
			"channelNote": "Pinned channel note",
			"channelType": 1,
		}),
	}
	if _, err := agg.HandleBlockPin(channelPin); err != nil {
		t.Fatalf("HandleBlockPin(channel create): %v", err)
	}

	chatPin := &aggregator.PinInscription{
		Id:            "channel_chat:i0",
		Path:          "/protocols/simplegroupchat",
		Operation:     "create",
		CreateAddress: "addr_member",
		CreateMetaId:  "member_local",
		GlobalMetaId:  "member_global",
		ChainName:     "mvc",
		Timestamp:     2000,
		GenesisHeight: 200,
		ContentBody: mustMarshal(t, SimpleGroupChat{
			GroupId:     "channel_group",
			ChannelId:   channelPin.Id,
			Content:     "channel latest message",
			ContentType: "text/plain",
			Encryption:  "none",
		}),
	}
	if _, err := agg.HandleBlockPin(chatPin); err != nil {
		t.Fatalf("HandleBlockPin(channel chat): %v", err)
	}

	w := performRequest(t, router, "GET", "/api/group-chat/group-channel-list?groupId=channel_group")
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Total int64                    `json:"total"`
			List  []map[string]interface{} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("expected code=0, got %d body=%s", resp.Code, w.Body.String())
	}
	if resp.Data.Total != 1 || len(resp.Data.List) != 1 {
		t.Fatalf("expected one channel, got total=%d list=%#v", resp.Data.Total, resp.Data.List)
	}
	channel := resp.Data.List[0]
	if channel["channelId"] != channelPin.Id {
		t.Fatalf("channelId = %v, want %s", channel["channelId"], channelPin.Id)
	}
	if channel["channelName"] != "Announcements" || channel["channelIcon"] != "metafile://channel-icon" || channel["channelNote"] != "Pinned channel note" {
		t.Fatalf("channel metadata not preserved: %#v", channel)
	}
	if channelType, _ := channel["channelType"].(float64); int64(channelType) != 1 {
		t.Fatalf("channelType = %v, want 1 in %#v", channel["channelType"], channel)
	}
	if channel["channelNewestContent"] != "channel latest message" || channel["channelNewestPinId"] != chatPin.Id {
		t.Fatalf("channel newest message not populated: %#v", channel)
	}
	if channel["createUserMetaId"] != "creator_local" || channel["createUserGlobalMetaId"] != "creator_global" || channel["createUserAddress"] != "addr_creator" {
		t.Fatalf("channel creator aliases not populated: %#v", channel)
	}
}

// --- AC10: Community query ---

func TestCommunityQuery(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Create communities
	for i := 0; i < 3; i++ {
		commPin := &aggregator.PinInscription{
			Id:            fmt.Sprintf("comm_create_%d:i0", i),
			Path:          "/protocols/simplecommunity",
			Operation:     "create",
			CreateAddress: "1AddrCreator",
			CreateMetaId:  "creator_comm",
			GlobalMetaId:  "global_creator_comm",
			ChainName:     "btc",
			Timestamp:     1000 + int64(i),
			ContentBody: mustMarshal(t, SimpleCommunity{
				Name:        fmt.Sprintf("Community %d", i),
				Description: fmt.Sprintf("Description %d", i),
				Icon:        fmt.Sprintf("icon_%d", i),
			}),
		}
		agg.HandleBlockPin(commPin)
	}

	// Query community list via HTTP
	w := performRequest(t, router, "GET", "/api/group-chat/community/list?page=1&pageSize=20")
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Results struct {
				Items []Community `json:"items"`
			} `json:"results"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)

	if resp.Code != 0 {
		t.Fatalf("community list failed: code=%d %s", resp.Code, w.Body.String())
	}
	if len(resp.Data.Results.Items) < 3 {
		t.Errorf("expected at least 3 communities, got %d", len(resp.Data.Results.Items))
	}

	// Query single community
	communityId := resp.Data.Results.Items[0].CommunityId
	w2 := performRequest(t, router, "GET", fmt.Sprintf("/api/group-chat/community/%s", communityId))
	var resp2 struct {
		Code int       `json:"code"`
		Data Community `json:"data"`
	}
	json.Unmarshal(w2.Body.Bytes(), &resp2)

	if resp2.Code != 0 {
		t.Fatalf("community detail failed: code=%d", resp2.Code)
	}
	if resp2.Data.CommunityId != communityId {
		t.Errorf("expected communityId=%s, got %s", communityId, resp2.Data.CommunityId)
	}

	t.Logf("Community query OK: %d communities, detail communityId=%s",
		len(resp.Data.Results.Items), communityId)
}

// --- Additional tests ---

func TestNameMethod(t *testing.T) {
	agg := &Aggregator{}
	if agg.Name() != "groupchat" {
		t.Errorf("expected Name()='groupchat', got %q", agg.Name())
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

func TestChatListByIndex_OutOfRange(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()

	// Create empty group
	createPin := &aggregator.PinInscription{
		Id:            "oor_create:i0",
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: "1Addr1",
		CreateMetaId:  "creator1",
		GlobalMetaId:  "global_creator1",
		ChainName:     "btc",
		Timestamp:     1000,
		ContentBody: mustMarshal(t, SimpleGroupCreate{
			GroupId:   "oor_group",
			GroupName: "OutOfRange",
		}),
	}
	agg.HandleBlockPin(createPin)

	w := performRequest(t, router, "GET", "/api/group-chat/group-chat-list-by-index?groupId=oor_group&startIndex=999&size=20")
	var resp struct {
		Code int            `json:"code"`
		Data ChatListResult `json:"data"`
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

// mustMarshal is a test helper that marshals to JSON bytes.
func mustMarshal(t *testing.T, v interface{}) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return b
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
