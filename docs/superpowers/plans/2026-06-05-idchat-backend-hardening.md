# idchat Backend Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make metaso-p2p's idchat compatibility surface close the P0 gaps from `docs/IDCHAT_BACKEND_HARDENING_PLAN.md` so idchat can be pointed at `socket.metaid.io` without frontend code changes.

**Architecture:** Keep native metaso-p2p routes intact and harden only the compatibility layer exposed under `/chat-api/group-chat/*`, `/push-base/*`, and Socket.IO. Reuse existing groupchat, privatechat, and socket stores instead of adding a parallel compatibility database. Add contract tests around idchat-visible response envelopes before changing production handlers.

**Tech Stack:** Go, Gin, Pebble-backed aggregators, Socket.IO server, existing `CGO_ENABLED=0 go test ./...` verification.

---

## File Map

- Modify `internal/api/router_test.go`: add idchat P0 route-shape contract tests that work through the full router.
- Modify `internal/api/router.go`: mount idchat presence compatibility routes under `/chat-api/group-chat/socket/*`.
- Modify `internal/aggregator/groupchat/api.go`: add missing route registrations and route handlers.
- Modify `internal/aggregator/groupchat/db_chat.go`: add compatibility result structs, group timestamp/index aliases, channel filters, unified latest session list, and mixed search helpers.
- Modify `internal/aggregator/groupchat/db_group.go`: add old envelope totals and join-control global arrays.
- Modify `internal/aggregator/privatechat/api.go`: change `private-group-paths` response from raw array to old object envelope.
- Modify `internal/aggregator/privatechat/db.go`: return private path objects and expose a lightweight conversation scan for latest-session compatibility.
- Modify `internal/socket/presence.go`: add idchat-compatible online list and stats handlers.
- Modify `internal/socket/server.go`: route group chat events to target identities when aggregators provide `TargetIds`, while preserving room broadcast.
- Modify `internal/socket/server_test.go`: add group event routing regression tests.

## Task 1: Failing idchat P0 HTTP Contract Tests

**Files:**
- Modify: `internal/api/router_test.go`

- [ ] **Step 1: Add tests for idchat P0 envelopes**

Add tests that call the full router with empty stores and assert compatibility
shapes, not real production data:

```go
func TestRouter_IdchatP0RoutesReturnCompatibilityEnvelopes(t *testing.T) {
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
		if int(body["code"].(float64)) != 0 {
			t.Fatalf("%s: want code=0 body=%s", tc.path, w.Body.String())
		}
		data, ok := body["data"].(map[string]interface{})
		if !ok {
			t.Fatalf("%s: data should be object, got %T", tc.path, body["data"])
		}
		for _, key := range tc.dataKeys {
			if _, ok := data[key]; !ok {
				t.Fatalf("%s: missing data.%s in %v", tc.path, key, data)
			}
		}
	}
}
```

- [ ] **Step 2: Add test for idchat socket online route**

Assert `/chat-api/group-chat/socket/online-users` returns the old shape:

```go
func TestRouter_IdchatSocketOnlineUsersCompatibilityRoute(t *testing.T) {
	router := setupPresenceRouter(t, nil)
	w, body := get(t, router, "/chat-api/group-chat/socket/online-users?cursor=&size=20&withUserInfo=true")
	if w.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", w.Code, w.Body.String())
	}
	data := body["data"].(map[string]interface{})
	for _, key := range []string{"total", "cursor", "size", "onlineWindowSeconds", "list"} {
		if _, ok := data[key]; !ok {
			t.Fatalf("missing data.%s in %v", key, data)
		}
	}
}
```

- [ ] **Step 3: Run tests and verify failure**

Run:

```bash
CGO_ENABLED=0 go test ./internal/api -run 'IdchatP0|IdchatSocketOnline' -count=1
```

Expected: fail because several routes are 404 or still return stub/raw-array shapes.

## Task 2: HTTP Compatibility Handlers

**Files:**
- Modify: `internal/aggregator/groupchat/api.go`
- Modify: `internal/aggregator/groupchat/db_chat.go`
- Modify: `internal/aggregator/groupchat/db_group.go`
- Modify: `internal/aggregator/privatechat/api.go`
- Modify: `internal/aggregator/privatechat/db.go`

- [ ] **Step 1: Register missing routes**

In `groupchat.registerRoutes`, add:

```go
gc.GET("/group-chat-list", a.handleGroupChatList)
gc.GET("/group-chat-list-v3", a.handleGroupChatListV3)
gc.GET("/channel-chat-list-v3", a.handleChannelChatListV3)
gc.GET("/channel-chat-list-by-index", a.handleChannelChatListByIndex)
gc.GET("/group-channel-list", a.handleGroupChannelList)
gc.GET("/group-metaid-join-list", a.handleGroupMetaIdJoinList)
gc.GET("/search-groups-and-users", a.handleSearchGroupsAndUsers)
```

Remove those paths from stub registration.

- [ ] **Step 2: Return old list envelopes**

Add helper response structs with `LastIndex` for index endpoints and use
non-nil empty slices:

```go
type ChatListByIndexResult struct {
	Total         int64          `json:"total"`
	LastIndex     int64          `json:"lastIndex"`
	NextTimestamp int64          `json:"nextTimestamp,omitempty"`
	List          []*ChatMessage `json:"list"`
}
```

- [ ] **Step 3: Implement timestamp and index aliases**

Implement `handleGroupChatList` and `handleGroupChatListV3` by calling the same
storage path as V2 and returning `total`, `nextTimestamp`, and `list`.

Implement `GetChatListByIndexCompat` so `group-chat-list-by-index` returns
`lastIndex` equal to `startIndex + len(list)` when more items may exist.

- [ ] **Step 4: Implement channel history as filtered group history**

Implement channel timestamp and index handlers by filtering group messages where
`msg.ChannelId == channelId`. Return empty lists when no channel messages exist.

- [ ] **Step 5: Implement stub replacements with compatibility envelopes**

Return real empty envelopes first, then enrich from existing stores:

- `group-channel-list`: `{"total": 0, "list": []}` for now, with channel
  metadata added when indexed data exists.
- `group-metaid-join-list`: `{"metaId": metaId, "items": []}` until join-pin
  records are indexed.
- `search-groups-and-users`: combine existing group list/name search from
  stored groups plus `SearchUsers(query, size)`, returning `total` and `list`.

- [ ] **Step 6: Fix `private-group-paths`**

Change `GetPrivateGroupPaths` to return objects:

```go
type PrivateGroupPath struct {
	Path    string `json:"path"`
	GroupId string `json:"groupId"`
	PinId   string `json:"pinId"`
}
```

Change the handler to return `{"total": len(paths), "list": paths}`.

- [ ] **Step 7: Add totals and global arrays**

Add `total` to `group-list`, `group-member-list`, `search-group-members`, and
`search-users` responses. Add `joinBlockGlobalMetaIds` and
`joinWhitelistGlobalMetaIds` to `GroupJoinControlList`, deriving values from
stored member `GlobalMetaId` when available.

- [ ] **Step 8: Verify green**

Run:

```bash
CGO_ENABLED=0 go test ./internal/api ./internal/aggregator/groupchat ./internal/aggregator/privatechat -count=1
```

Expected: all tests pass.

## Task 3: Unified Latest Session Compatibility

**Files:**
- Modify: `internal/aggregator/groupchat/db_chat.go`
- Modify: `internal/aggregator/privatechat/db.go`
- Modify: `internal/api/router_test.go`

- [ ] **Step 1: Add failing seeded-data test**

Seed one group membership, one group message, and one private message. Assert
`/chat-api/group-chat/user/latest-chat-info-list?metaId=user-a` returns two
items sorted newest first, and the private item has `type == "2"` plus
`userInfo.chatPublicKey` when the message carries peer user info.

- [ ] **Step 2: Implement compatibility item type**

Replace the narrow `UserLatestChatInfo` response with an idchat-compatible item
that includes `type`, `groupId`, `metaId`, `globalMetaId`, `address`,
`timestamp`, `chatType`, `content`, `lastMessagePinId`, `roomName`,
`roomAvatarUrl`, `roomJoinType`, `createMetaId`, `createGlobalMetaId`,
`createAddress`, `createUserInfo`, `userCount`, `path`, and `userInfo`.

- [ ] **Step 3: Scan private conversations**

Add a privatechat storage helper or duplicate only the minimal pchat scan needed
to discover conversations involving the requested identity aliases. For each
conversation, keep the latest message and derive peer metadata from the message
fields and `FromUserInfo` / `ToUserInfo`.

- [ ] **Step 4: Sort and return old envelope**

Sort by timestamp descending and return `{"total": len(list), "list": list}`.

- [ ] **Step 5: Verify green**

Run:

```bash
CGO_ENABLED=0 go test ./internal/api ./internal/aggregator/groupchat ./internal/aggregator/privatechat -count=1
```

Expected: all tests pass.

## Task 4: idchat Presence Compatibility Routes

**Files:**
- Modify: `internal/socket/presence.go`
- Modify: `internal/api/router.go`
- Modify: `internal/api/router_test.go`

- [ ] **Step 1: Add compatibility route registration**

When a socket server exists, mount:

```go
router.GET("/chat-api/group-chat/socket/online-users", socketServer.HandleIdchatOnlineUsers)
router.GET("/chat-api/group-chat/socket/stats", socketServer.HandleOnlineStats)
router.GET("/chat-api/group-chat/socket/user-online", socketServer.HandleIdchatUserOnline)
```

- [ ] **Step 2: Implement old online-users shape**

Implement `HandleIdchatOnlineUsers` so it accepts `cursor`, `size`, `withUserInfo`
and returns:

```json
{
  "total": 0,
  "cursor": "",
  "size": 20,
  "onlineWindowSeconds": 35,
  "list": []
}
```

Rows should use `globalMetaId`, `lastSeenAt`, `lastSeenAgoSeconds`,
`deviceCount`, and `userInfo`.

- [ ] **Step 3: Implement user-online**

Return `{"online": true}` if the queried identity is currently connected,
otherwise `{"online": false}`.

- [ ] **Step 4: Verify green**

Run:

```bash
CGO_ENABLED=0 go test ./internal/api ./internal/socket -count=1
```

Expected: all tests pass.

## Task 5: idchat Group Websocket Delivery

**Files:**
- Modify: `internal/socket/server.go`
- Modify: `internal/socket/server_test.go`
- Modify: `internal/aggregator/groupchat/process.go`
- Modify: `internal/aggregator/groupchat/process_test.go`

- [ ] **Step 1: Add failing routing test**

Add a socket-layer unit test that creates a group notify event with
`TargetIds: []string{"member-a"}` and confirms `routeNotifyEvent` sends to that
target via `SendToUser` logic. Keep room broadcast behavior enabled when
`GroupId` is present.

- [ ] **Step 2: Add groupchat target IDs**

When groupchat emits `WS_SERVER_NOTIFY_GROUP_CHAT`, populate `TargetIds` from
current non-removed group members and include each member's `MetaId` and
`GlobalMetaId` if present.

- [ ] **Step 3: Route group target IDs**

In `routeNotifyEvent`, for `WS_SERVER_NOTIFY_GROUP_CHAT`, send to
`notifyEventTargetIds(evt)` when present, then broadcast to `group:<groupId>`.

- [ ] **Step 4: Verify green**

Run:

```bash
CGO_ENABLED=0 go test ./internal/socket ./internal/aggregator/groupchat -count=1
```

Expected: all tests pass.

## Task 6: Full Verification and Subagent Review

**Files:**
- Review only unless feedback requires fixes.

- [ ] **Step 1: Run full verification**

Run:

```bash
CGO_ENABLED=0 go test ./...
```

Expected: all tests pass.

- [ ] **Step 2: Commit implementation**

Stage only files changed for this hardening round and commit:

```bash
git add internal/api/router.go internal/api/router_test.go internal/aggregator/groupchat internal/aggregator/privatechat internal/socket
git commit -m "fix: harden idchat backend compatibility"
```

- [ ] **Step 3: Request subagent acceptance review**

Spawn one reviewer subagent with:

- Requirements: `docs/IDCHAT_BACKEND_HARDENING_PLAN.md`.
- Plan: this implementation plan.
- Diff range: base before implementation to current HEAD.
- Output required: explicit `ACCEPT`, `ACCEPT_WITH_FIXES`, or `REJECT`,
  plus concrete file/line findings.

- [ ] **Step 4: Apply feedback**

Fix all `REJECT` or `ACCEPT_WITH_FIXES` blocking items, rerun full tests, and
commit a follow-up fix if files change.

