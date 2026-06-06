# idchat Configuration Changes for metaso-p2p

This document lists every configuration change needed in **idchat** to switch from the legacy backend
to **metaso-p2p**. Only `config.json` needs to be modified — no `.ts`, `.tsx`, `.vue`, or `.js`
source changes are required.

## Overview

metaso-p2p is a drop-in replacement for the idchat backend. Every API path and Socket.IO event
is wire-compatible. The only change needed is pointing idchat's URLs to the metaso-p2p host.

## config.json Changes

Replace the old host values with your metaso-p2p host. The paths remain identical.

### Socket.IO

| Setting | Old Value | New Value |
|---|---|---|
| Socket.IO URL | `wss://old-host/socket/socket.io` | `ws://metaso-p2p-host:8080/socket/socket.io` |

Notes:
- Use `ws://` (not `wss://`) unless you have TLS in front of metaso-p2p (e.g. nginx reverse proxy).
- The query parameters `?metaid=<globalMetaId>&type=pc` (or `type=app`) are appended by the idchat client at runtime — do not hardcode them in the URL.

### API Base URL

| Setting | Old Value | New Value |
|---|---|---|
| API base URL | `https://old-host` | `http://metaso-p2p-host:8080` |

This base URL is prepended to all relative API paths below.

### User Info Endpoints

| Setting | Old Value | New Value |
|---|---|---|
| User info base path | `/api/info` | `/api/info` (unchanged) |

Full URL change: `https://old-host/api/info/*` -> `http://metaso-p2p-host:8080/api/info/*`

Affected calls:
- `GET /api/info/address/:address`
- `GET /api/info/metaid/:metaid`
- `GET /api/info/globalmetaid/:globalMetaId`

### meta-file-system replacement (`metafileIndexerApi`)

idchat reads user info today from meta-file-system through the `metafileIndexerApi` client at `${metaFSBaseURL}/metafile-indexer/api/info/*`. metaso-p2p exposes the same three routes under `/metafile-indexer/api/info/*` with byte-for-byte matching response (`code: 1` on success, `40400` not_found, `40000` invalid_param, fields `chatpubkey` / `chatpubkeyId` in lowercase), so the migration is a single config line:

```diff
- api.metafileIndexerApi = 'https://file.metaid.io/metafile-indexer/api'
+ api.metafileIndexerApi = 'http://metaso-p2p-host:8080/metafile-indexer/api'
```

The other meta-file-system endpoints (`fileApi`, `avatarContentApi`) keep pointing at meta-file-system — metaso-p2p does not yet provide file upload or `/content/<pinId>` static serving.

### Browser CORS

idchat normally calls the chat API from a browser origin such as `https://idchat.io` to an API origin such as `https://api.idchat.io`. metaso-p2p therefore sends public CORS headers (`Access-Control-Allow-Origin: *`) and answers `OPTIONS` preflight requests for the compatibility HTTP APIs, including signed `/push-base` POST calls that send `X-Signature` and `X-Public-Key`.

### Group Chat Endpoints

| Setting | Old Value | New Value |
|---|---|---|
| idchat `paths.chatApi` | `/chat-api` | `/chat-api` (unchanged) |
| metaso-p2p native path | — | `/api/group-chat/*` |
| idchat compatibility path | `/chat-api/group-chat/*` | `/chat-api/group-chat/*` |

Full URL change: `https://old-host/chat-api/group-chat/*` -> `http://metaso-p2p-host:8080/chat-api/group-chat/*`

The `/chat-api/group-chat/*` prefix is an idchat compatibility alias for the existing `/api/group-chat/*` handlers. This covers the lightweight group/private chat routes already implemented by metaso-p2p, including:
- Community list/detail: `/chat-api/group-chat/community/list`, `/chat-api/group-chat/community/:communityId`
- Group info/roles/members: `/chat-api/group-chat/group-*`
- Chat messages: `/chat-api/group-chat/group-chat-list-v2`, `/chat-api/group-chat/group-chat-list-by-index`
- Private chat: `/chat-api/group-chat/private-chat-*`, `/chat-api/group-chat/chat/homes/:metaId`
- User search: `/chat-api/group-chat/search-users`

Not covered in this compatibility pass:
- Red packet / lucky bag routes such as `/lucky-bag-info`, `/grab-lucky-bag`, and `/generate-lucky-bag-code`.
- MetaName / ENS resolution routes are still stubs and should not be treated as production-complete.

### Push / Blocking Endpoints

| Setting | Old Value | New Value |
|---|---|---|
| Push base path | `/push-base/v1/push` | `/push-base/v1/push` (unchanged) |
| metaso-p2p native path | — | `/api/push-base/v1/push/*` |
| idchat compatibility path | `/push-base/v1/push/*` | `/push-base/v1/push/*` |

Full URL change: `https://old-host/push-base/v1/push/*` -> `http://metaso-p2p-host:8080/push-base/v1/push/*`

Affected calls:
- `GET /push-base/v1/push/get_user_blocked_chats`
- `POST /push-base/v1/push/add_blocked_chat`
- `POST /push-base/v1/push/remove_blocked_chat`

### Health Check (idchat may use this for connectivity detection)

| Setting | Old Value | New Value |
|---|---|---|
| Health check path | `/healthz` | `/healthz` (unchanged) |

Full URL change: `https://old-host/healthz` -> `http://metaso-p2p-host:8080/healthz`

## Summary of Changes

The only values that change are:
1. **Protocol**: `https` -> `http` (unless TLS is added in front of metaso-p2p)
2. **Host**: `old-host` -> `metaso-p2p-host`
3. **Port**: old default port -> `8080` (metaso-p2p default)

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
