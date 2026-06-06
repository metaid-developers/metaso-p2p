# idchat API Contract

Every HTTP endpoint and Socket.IO event idchat expects. metaso-p2p v1 must match this exactly.

Use `show-now-tmp` commit `1643a1a` as reference for any ambiguity in this doc.

## Response Format

**All endpoints:**
```json
// Success — code MUST be 0
{"code": 0, "data": <payload>, "message": "", "processingTime": <unix_ms>}

// Error — code MUST be non-zero
{"code": 1, "message": "<description>"}

// Not found — use code: 1 with descriptive message
{"code": 1, "message": "not found"}
```

HTTP status is always 200 (even for errors). The `code` field distinguishes success/error.

## Pagination

### Cursor-based
Query: `?cursor=&size=20`. Response `data` contains `nextCursor` (string). Empty `nextCursor` or `""` = end of results. First request uses `cursor=` (empty).

**Cursor encoding**: Since metaso-p2p uses PebbleDB (not MongoDB), cursor is a base64-encoded Pebble key or an opaque string. The client treats it as opaque — it must not be parsed. It must survive round-trips and be valid for subsequent `cursor=<value>` calls.

### Index-based
Query: `?startIndex=0&size=20`. Response `data` contains the slice starting at that index. No cursor needed.

### Timestamp-based
Query: `?timestamp=0`. Response `data` contains `nextTimestamp` (int64 unix seconds). Empty or 0 = no more data.

## 1. User Info (`/api/info/*`)

| Method | Path | Response `.data` shape |
|--------|------|----------------------|
| GET | `/api/info/address/:address` | `{metaid, globalMetaId, name, nameId, address, avatar, avatarId, nftAvatar, bio, bioId, background, chatpubkey, chatpubkeyId, chainName}` |
| GET | `/api/info/metaid/:metaid` | Same |
| GET | `/api/info/globalmetaid/:globalMetaId` | Same |

All fields are strings. `avatar`, `nftAvatar`, `background` values are content paths like `/content/<pinId>`. Chat public key fields are intentionally all-lowercase (`chatpubkey` / `chatpubkeyId`) to match meta-file-system.

### meta-file-system drop-in compatibility

These three `/info/*` endpoints are an exception to the universal `code: 0 = success` envelope used by every other metaso-p2p endpoint. idchat's `metafileIndexerApi` client treats `code === 1` as success and any other value as failure (`idchat/src/api/man.ts`). To let idchat point its `metafileIndexerApi` URL at metaso-p2p without any TypeScript changes, the user-info handlers mirror meta-file-system's response shape exactly:

| Result | code | body |
|---|---|---|
| Success | `1` | `{code: 1, data: <UserProfile>, message: "", processingTime: <unix_ms>}` |
| Not found | `40400` | `{code: 40400, message: "user not found"}` |
| Invalid parameter | `40000` | `{code: 40000, message: "<param> is required"}` |

The same three handlers are also mounted under `/metafile-indexer/api/info/*` so idchat can point its existing `metafileIndexerApi` URL (`<host>/metafile-indexer/api`) directly at metaso-p2p. The native `/api/info/*` paths are kept for new clients that follow metaso-p2p's standard prefix.

All **other** metaso-p2p endpoints use `code: 0 = success`, matching idchat's `TalkApi` / `chat-notify` / `pushBase` clients. Native metaso-p2p clients use the `/api` prefix (`/api/group-chat/*`, `/api/push-base/*`); idchat compatibility aliases expose the same implemented handlers at `/chat-api/group-chat/*` and `/push-base/*`.

## 2. Group Chat (`/group-chat/*`)

Native prefix: `/api/group-chat/*`

idchat compatibility prefix: `/chat-api/group-chat/*`

The path table below omits the deployment prefix for readability.

### Community

| Method | Path | Query/Body | Response `.data` |
|--------|------|-----------|------------------|
| GET | `/group-chat/community/list` | `page`, `pageSize` | `{results: {items: [Community]}}` |
| GET | `/group-chat/community/{communityId}` | — | Community object |
| GET | `/group-chat/community/{communityId}/auth/info` | — | `{communityId, authType, ...}` |
| GET | `/group-chat/community/auths/{metaId}` | `page`, `pageSize` | `{results: {items: [Metaname]}}` |
| GET | `/group-chat/community/metaname/{address}` | — | `{total, nextFlag, results: {items}}` |
| GET | `/group-chat/community/ens/{address}` | — | Same as metaname |
| GET | `/group-chat/community/{communityId}/person/info` | `metaId` | `{communityState}` |
| GET | `/group-chat/community/{communityId}/persons` | `pageSize, page` | `{results: {items: [Member]}}` |
| GET | `/group-chat/community/{communityId}/announcements` | — | `{total, results: {items: [Announcement]}}` |

### Group

| Method | Path | Query | Response `.data` |
|--------|------|-------|------------------|
| GET | `/group-chat/group-info` | `groupId` | Group/Channel object: `{groupId, groupName, avatar, creator, memberCount, ...}` |
| GET | `/group-chat/group-person` | `metaId, groupId` | `{isInGroup: bool}` |
| GET | `/group-chat/group-user-role` | `groupId, metaId` | `{isCreator, isAdmin, isBlocked, isWhitelist, isRemoved}` |
| GET | `/group-chat/group-member-list` | `groupId, cursor, size, timestamp` | `{admins: [], blockList: [], creator: "", list: [Member], whiteList: []}` |
| GET | `/group-chat/search-group-members` | `groupId, size, query` | `{list: [Member]}` |
| GET | `/group-chat/group-list` | `metaId, cursor, size` | `{list: [Channel]}` |
| GET | `/group-chat/group-channel-list` | `groupId` | `{total, list: [SubChannel]}` |
| GET | `/group-chat/group-join-control-list` | `groupId` | `{groupId, joinBlockMetaIds: [], joinWhitelistMetaIds: []}` |
| GET | `/group-chat/group-metaid-join-list` | `metaId, groupId` | `{items: [{k: encryptedPasscode}]}` |

### Chat Messages

| Method | Path | Query | Response `.data` |
|--------|------|-------|------------------|
| GET | `/group-chat/group-chat-list-v2` | `groupId, metaId, cursor, size, timestamp` | `{total, nextTimestamp, list: [ChatMessage]}` |
| GET | `/group-chat/group-chat-list-by-index` | `groupId, startIndex, size` | Same as v2 |
| GET | `/group-chat/channel-chat-list-v3` | `channelId, metaId, cursor, size, timestamp` | Same shape |
| GET | `/group-chat/channel-chat-list-by-index` | `channelId, startIndex, size` | Same shape |
| GET | `/group-chat/user/latest-chat-info-list` | `metaId, cursor, size, timestamp` | `{list: [Channel]}` |

**ChatMessage shape**: `{txId, pinId, groupId, channelId, metaId, globalMetaId, address, userInfo: {UserInfo}, nickName, protocol, content, contentType, encryption, chatType, replyPin, replyInfo: {...}, replyMetaId, replyGlobalMetaId, mention: [...], timestamp, chain, blockHeight, index}`

### Search

| Method | Path | Query | Response `.data` |
|--------|------|-------|------------------|
| GET | `/group-chat/search-users` | `query` | `{list: [{metaId, globalMetaId, address, userName, avatar, avatarId, chatPublicKey, timestamp}]}` |

## 3. Private Chat

Native canonical prefix for new metaso-p2p clients: `/api/private-chat/*`

Historical group-chat compatibility prefix: `/api/group-chat/*`

idchat compatibility prefix: `/chat-api/group-chat/*`

| Method | Path | Query | Response `.data` |
|--------|------|-------|------------------|
| GET | `/group-chat/private-chat-list` | `otherMetaId, metaId, cursor, size, timestamp` | `{total, nextTimestamp, list: [PrivateMessage]}` |
| GET | `/group-chat/private-chat-list-by-index` | `otherMetaId, metaId, startIndex, size` | Same shape |
| GET | `/group-chat/private-group-paths` | `metaId` | `{list: [path]}` |
| GET | `/group-chat/chat/homes/{metaId}` | — (data as JSON body) | `{data: [HomeEntry]}` |

Canonical private-chat aliases return the same response envelopes and use the
same query semantics:

| Method | Canonical path | Existing compatibility path |
|--------|----------------|-----------------------------|
| GET | `/private-chat/messages` | `/group-chat/private-chat-list` |
| GET | `/private-chat/messages/by-index` | `/group-chat/private-chat-list-by-index` |
| GET | `/private-chat/paths` | `/group-chat/private-group-paths` |
| GET | `/private-chat/homes/{metaId}` | `/group-chat/chat/homes/{metaId}` |

**PrivateMessage shape**: `{fromGlobalMetaId, from, fromAddress, fromUserInfo, toGlobalMetaId, to, toAddress, toUserInfo, txId, pinId, protocol, content, contentType, encryption, timestamp, chain, blockHeight, index}`

## 4. Chat Blocking (`/push-base/v1/push/*`)

Native prefix: `/api/push-base/v1/push/*`

idchat compatibility prefix: `/push-base/v1/push/*`

| Method | Path | Auth Headers | Request Body | Response `.data` |
|--------|------|-------------|-------------|------------------|
| GET | `/push-base/v1/push/get_user_blocked_chats` | — | `?metaId=<id>` | `{userId, blockedChats: [{chatId, chatType, metaId, reason}], updatedAt}` |
| POST | `/push-base/v1/push/add_blocked_chat` | `X-Signature`, `X-Public-Key` | JSON: `{chatId, chatType, metaId, reason}` | Same as get — returns updated full list |
| POST | `/push-base/v1/push/remove_blocked_chat` | `X-Signature`, `X-Public-Key` | JSON: `{chatId, metaId}` | Same — returns updated list |

**Signature verification**: `SHA256("metaso.network")` → hex encode → compare with `X-Signature` header. `X-Public-Key` must match configured public key. If no public key configured, skip verification (test mode).

## 5. Socket.IO

### Connection
```
URL:  wss://<host>/socket/socket.io
Query: ?metaid=<globalMetaId>&type=pc|app
Library: socket.io-client v4.7.5 (client), zishang520/socket.io v2 (server)
```

### Heartbeat
Client sends `socket.emit('ping')` every 30s. Server responds with `heartbeat_ack` event. This is request-response, not server-push. If client sends no ping for 35s, server disconnects.

### Push Events

All delivered via `message` event with envelope:
```json
{"M": "<EVENT_TYPE>", "C": 0, "D": <payload>}
```
`C` is always integer 0 for push events. `WS_CODE_SEND_SUCCESS = 200` for wrapped success responses.

| M value | Trigger |
|---------|---------|
| `WS_SERVER_NOTIFY_GROUP_CHAT` | New group message confirmed on-chain |
| `WS_SERVER_NOTIFY_PRIVATE_CHAT` | New private message confirmed on-chain |
| `WS_SERVER_NOTIFY_GROUP_ROLE` | Role change (join, leave, admin, block, whitelist, remove) |

### Event Payloads

**GroupChatItem**: `{groupId, globalMetaId, channelId, metanetId (=groupId), txId, pinId, metaId, address, userInfo: {globalMetaId, metaid, address, name, avatar, avatarImage, chatPublicKey, chatPublicKeyId}, nickName, protocol, domain, content, contentType, encryption, chatType, data, replyPin, replyInfo: {channelId, pinId, globalMetaId, metaId, address, userInfo, nickName, protocol, content, contentType, encryption, chatType, mention, timestamp, chain, blockHeight, index}, replyMetaId, replyGlobalMetaId, mention: [metaid...], timestamp, params, chain, blockHeight, index}`

**PrivateChatItem**: `{fromGlobalMetaId, from, fromUserInfo: UserInfo, toGlobalMetaId, to, toUserInfo: UserInfo, txId, pinId, globalMetaId, metaId, address, userInfo: UserInfo, nickName, protocol, content, contentType, encryption, chatType, data, replyPin, replyInfo, replyGlobalMetaId, replyMetaId, timestamp, params, chain, blockHeight, index}`

Private-chat Socket.IO pushes use the same `PrivateChatItem`-compatible object
as `D` in the `{M, C, D}` envelope. Clients should use `M` for event dispatch
and treat `D.from`, `D.to`, `D.fromGlobalMetaId`, `D.toGlobalMetaId`, `D.pinId`,
and `D.txId` as the stable identity and de-duplication fields.

**GroupUserRoleInfo**: `{globalMetaId, metaId, address, userInfo: UserInfo, groupId, channelId, isCreator, isAdmin, isBlocked, isWhitelist, isRemoved}`

## 6. Health Check
```
GET /healthz
→ {"code": 0, "data": {"status": "ok", "service": "metaso-p2p", "version": "v1.0.0"}}
```
