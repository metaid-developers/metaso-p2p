# idchat Configuration Changes for meta-socket

This document lists every configuration change needed in **idchat** to switch from the legacy backend
to **meta-socket**. Only `config.json` needs to be modified — no `.ts`, `.tsx`, `.vue`, or `.js`
source changes are required.

## Overview

meta-socket is a drop-in replacement for the idchat backend. Every API path and Socket.IO event
is wire-compatible. The only change needed is pointing idchat's URLs to the meta-socket host.

## config.json Changes

Replace the old host values with your meta-socket host. The paths remain identical.

### Socket.IO

| Setting | Old Value | New Value |
|---|---|---|
| Socket.IO URL | `wss://old-host/socket/socket.io` | `ws://meta-socket-host:8080/socket/socket.io` |

Notes:
- Use `ws://` (not `wss://`) unless you have TLS in front of meta-socket (e.g. nginx reverse proxy).
- The query parameters `?metaid=<globalMetaId>&type=pc` (or `type=app`) are appended by the idchat client at runtime — do not hardcode them in the URL.

### API Base URL

| Setting | Old Value | New Value |
|---|---|---|
| API base URL | `https://old-host` | `http://meta-socket-host:8080` |

This base URL is prepended to all relative API paths below.

### User Info Endpoints

| Setting | Old Value | New Value |
|---|---|---|
| User info base path | `/api/info` | `/api/info` (unchanged) |

Full URL change: `https://old-host/api/info/*` -> `http://meta-socket-host:8080/api/info/*`

Affected calls:
- `GET /api/info/address/:address`
- `GET /api/info/metaid/:metaid`
- `GET /api/info/globalmetaid/:globalMetaId`

### Group Chat Endpoints

| Setting | Old Value | New Value |
|---|---|---|
| Group chat base path | `/group-chat` | `/group-chat` (unchanged) |

Full URL change: `https://old-host/group-chat/*` -> `http://meta-socket-host:8080/group-chat/*`

This covers all group and private chat endpoints including:
- Community: `/group-chat/community/*`
- Group info/roles/members: `/group-chat/group-*`
- Chat messages: `/group-chat/group-chat-list-*`, `/group-chat/channel-chat-list-*`
- Private chat: `/group-chat/private-chat-*`
- User search: `/group-chat/search-users`

### Push / Blocking Endpoints

| Setting | Old Value | New Value |
|---|---|---|
| Push base path | `/push-base/v1/push` | `/push-base/v1/push` (unchanged) |

Full URL change: `https://old-host/push-base/v1/push/*` -> `http://meta-socket-host:8080/push-base/v1/push/*`

Affected calls:
- `GET /push-base/v1/push/get_user_blocked_chats`
- `POST /push-base/v1/push/add_blocked_chat`
- `POST /push-base/v1/push/remove_blocked_chat`

### Health Check (idchat may use this for connectivity detection)

| Setting | Old Value | New Value |
|---|---|---|
| Health check path | `/healthz` | `/healthz` (unchanged) |

Full URL change: `https://old-host/healthz` -> `http://meta-socket-host:8080/healthz`

## Summary of Changes

The only values that change are:
1. **Protocol**: `https` -> `http` (unless TLS is added in front of meta-socket)
2. **Host**: `old-host` -> `meta-socket-host`
3. **Port**: old default port -> `8080` (meta-socket default)

**All API paths remain identical.** The idchat client application code does not need any modifications.

## Example: Complete config.json Diff

```diff
- "socketUrl": "wss://show-now-tmp.example.com/socket/socket.io",
+ "socketUrl": "ws://localhost:8080/socket/socket.io",

- "apiBaseUrl": "https://show-now-tmp.example.com",
+ "apiBaseUrl": "http://localhost:8080",
```

## Verification

After changing the config, restart idchat and verify:
1. The health check returns `{"code": 0, "data": {"status": "ok", ...}}`
2. User info loads correctly for known addresses
3. Group chat history loads
4. Real-time messages arrive via Socket.IO
5. Block/unblock operations work
