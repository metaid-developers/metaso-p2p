# idchat Backend Compatibility Hardening Plan

## Objective

Make `https://socket.metaid.io` a drop-in replacement for the idchat chat
backend currently served from `https://api.idchat.io/chat-api`, without
requiring idchat source changes.

The target idchat config switch is:

- `api.metaSoBaseURL`: `https://socket.metaid.io`
- `api.paths.chatApi`: `/chat-api`
- `api.paths.chatNotify`: empty string
- `api.paths.chatWs`: empty string
- `api.paths.chatWsPath`: `/socket`

This document is the current migration gate. It supersedes any optimistic
readiness statements in older docs until every P0 item below is implemented and
verified against idchat.

## Scope

In scope:

- idchat HTTP chat API compatibility under `/chat-api/group-chat/*`.
- idchat push block-list compatibility under `/push-base/v1/push/*`.
- idchat Socket.IO compatibility under `/socket/socket.io` and `/socket.io`.
- Response shape compatibility with the old idchat backend for fields that the
  current idchat frontend reads.

Out of scope for this hardening round:

- Red packet, lucky bag, and MRC20 related endpoints.
- These deprecated community and identity endpoints, which idchat no longer
  needs in the migration target:
  - `/community/:id/auth/info`
  - `/community/:id/person/info`
  - `/community/:id/persons`
  - `/community/:id/announcements`
  - `/community/auths/:metaId`
  - `/community/metaname/:address`
  - `/community/ens/:address`
- Internal debug, sync, and maintenance APIs unless they are needed for rollout
  operations.

## Evidence Sources

- idchat frontend source:
  - `/Users/tusm/Documents/MetaID_Projects/idchat/src/api/talk.ts`
  - `/Users/tusm/Documents/MetaID_Projects/idchat/src/api/online-bots.ts`
  - `/Users/tusm/Documents/MetaID_Projects/idchat/src/api/chat-notify.ts`
  - `/Users/tusm/Documents/MetaID_Projects/idchat/src/stores/ws_new.ts`
  - `/Users/tusm/Documents/MetaID_Projects/idchat/src/stores/simple-talk.ts`
  - `/Users/tusm/Documents/MetaID_Projects/idchat/src/utils/taskQueue.ts`
- Old idchat backend source:
  - `/Users/tusm/Documents/MetaID_Projects/show-now-tmp/basicprotocols/group_chat/api/routes.go`
  - `/Users/tusm/Documents/MetaID_Projects/show-now-tmp/basicprotocols/group_chat/api/respond/group_response.go`
  - `/Users/tusm/Documents/MetaID_Projects/show-now-tmp/basicprotocols/group_chat/api/respond/socket_response.go`
  - `/Users/tusm/Documents/MetaID_Projects/show-now-tmp/basicprotocols/group_chat/service/group_service.go`
- Current meta-socket source:
  - `internal/api/router.go`
  - `internal/aggregator/groupchat/api.go`
  - `internal/aggregator/groupchat/db_chat.go`
  - `internal/aggregator/groupchat/db_group.go`
  - `internal/aggregator/privatechat/api.go`
  - `internal/aggregator/privatechat/db.go`
  - `internal/socket/server.go`
- Live probes comparing `https://api.idchat.io/chat-api` with
  `https://socket.metaid.io/chat-api` during this audit round.

## Current Verdict

`socket.metaid.io` is not yet a drop-in backend for idchat.

The basic API namespace exists, Socket.IO handshakes work, and push blocked-chat
routes are available. The blockers are in idchat's active chat surfaces:
the unified recent session list, historical group data coverage, missing
compatibility routes, stubbed subchannel/private-group endpoints, and group
message live-delivery semantics.

## Endpoint Gap Matrix

| Endpoint | idchat usage | Old backend contract | Current meta-socket state | Required action |
| --- | --- | --- | --- | --- |
| `/chat-api/group-chat/user/latest-chat-info-list` | Main session list. idchat hides private sessions without `userInfo.chatPublicKey`. | `data.total`, `data.list`; unified group and private sessions; private items include peer `userInfo`. | Implemented only from group membership, returns `data.list` only, live probe returned empty for a user with old sessions. | P0. Rebuild as unified group plus private session list with old item shape and `total`. |
| `/chat-api/group-chat/group-list` | Legacy channel list and fallback data. | `data.total`, `data.list`. | Route exists, but live probe returned empty where old backend returned groups. | P0. Fix group membership/index coverage and add old response fields. |
| `/chat-api/group-chat/group-info` | `getOneChannel` metadata lookup. | Full room metadata for historical group IDs. | Route exists, but live probe returned `group not found` for an old backend group. | P0. Ensure historical group create/join state is indexed and queryable. |
| `/chat-api/group-chat/group-chat-list` | Task queue polling path. | `data.total`, `data.nextTimestamp`, `data.list`. | Missing, live 404. | P0. Add compatibility route, preserving old response shape. |
| `/chat-api/group-chat/group-chat-list-v2` | Group history. | `data.total`, `data.nextTimestamp`, `data.list` with old message fields. | Route exists, but live probe returned fewer messages and a narrower item shape. | P0. Align history coverage and item fields used by idchat. |
| `/chat-api/group-chat/group-chat-list-by-index` | Indexed group history. | `data.total`, `data.lastIndex`, `data.list`. | Route exists, but returns cursor-style data and fewer rows than old backend for the same group. | P0. Preserve old index pagination fields. |
| `/chat-api/group-chat/channel-chat-list-v3` | Subchannel history. | Same message-list contract, filtered by channel. | Stub returns `{}`. | P0. Parse, index, and query channel messages. |
| `/chat-api/group-chat/channel-chat-list-by-index` | Indexed subchannel history. | Same indexed message-list contract, filtered by channel. | Stub returns `{}`. | P0. Implement channel indexed history. |
| `/chat-api/group-chat/private-chat-list` | Private history. | Old `PrivateChatItem` fields, including sender/receiver identity aliases and content fields. | Route exists. Shape and field parity still need idchat-contract tests. | P0. Audit and fill old fields that idchat renders or task flows read. |
| `/chat-api/group-chat/private-chat-list-by-index` | Indexed private history. | Old indexed private-list contract. | Route exists. Shape and field parity still need idchat-contract tests. | P0. Align old pagination and item fields. |
| `/chat-api/group-chat/private-group-paths` | Private group path lookup; idchat reads `res.data.list`. | `data.total`, `data.list` with `{path, groupId, pinId}`. | Route returns a raw array, so idchat sees no `data.list`. | P0. Return the old object shape and item fields. |
| `/chat-api/group-chat/group-channel-list` | Subchannel list lookup in session and message views. | `data.total`, `data.list` with channel metadata. | Stub returns `{}`. | P0. Parse and index group channel pins, including newest channel message fields. |
| `/chat-api/group-chat/group-join-control-list` | Join control state. | Includes metaId arrays and globalMetaId arrays. | Route exists but omits `joinBlockGlobalMetaIds` and `joinWhitelistGlobalMetaIds`. | P1. Add globalMetaId arrays and contract tests. |
| `/chat-api/group-chat/group-metaid-join-list` | Private group passcode recovery; idchat reads `data.items[*].k`. | `data.metaId`, `data.items` with join pin data and `k`. | Stub returns `{}`. | P0. Persist and query join records with `k`, referrer, chain, and by-user fields. |
| `/chat-api/group-chat/search-groups-and-users` | Direct contact search modal. | `data.total`, `data.list`, mixed group/user results. | Missing, live 404. | P0. Add combined search route. |
| `/chat-api/group-chat/search-users` | Member search helper. | `data.total`, `data.list`; honors query and size. | Route exists, but live probe returned only `list` and did not match old data coverage. | P1. Add `total`, honor size, and improve user index coverage. |
| `/chat-api/group-chat/group-member-list` | Member drawer and group metadata. | `data.total`, `data.list`. | Route exists. Needs parity test against old fields. | P1. Add field and pagination compatibility tests. |
| `/chat-api/group-chat/search-group-members` | Member search in group. | `data.total`, `data.list`. | Route exists. Needs parity test against old fields. | P1. Add field and pagination compatibility tests. |
| `/chat-api/group-chat/group-user-role` | Current user role in group. | Role response used by group UI controls. | Route exists. Needs old shape test. | P1. Add contract test. |
| `/chat-api/group-chat/group-person` | Current user group relation. | Person relation response. | Route exists. Needs old shape test. | P1. Add contract test. |
| `/chat-api/group-chat/socket/online-users` | Online bot/user list API. | `data.total`, `cursor`, `size`, `onlineWindowSeconds`, `list[*].userInfo`. | Missing under `/chat-api`, live 404. | P0. Add compatibility route mapped to presence snapshots. |
| `/chat-api/group-chat/socket/user-online` | Old presence compatibility. | Online status result. | Missing under `/chat-api`, live 404. | P1. Add if idchat or ops surface still calls it. |
| `/chat-api/group-chat/socket/stats` | Old presence stats/debug. | Stats object. | Missing under `/chat-api`, live 404. | P2. Add for rollout diagnostics. |
| `/push-base/v1/push/get_user_blocked_chats` | Blocked chat bootstrap. | `data.blockedChats`, `updatedAt`, `userId`. | Live route returns 200 with expected keys. | Keep covered by tests. |
| `/push-base/v1/push/add_blocked_chat` | Add blocked chat. | POST mutation. | Route exists. | Keep covered by tests. |
| `/push-base/v1/push/remove_blocked_chat` | Remove blocked chat. | POST mutation. | Route exists. | Keep covered by tests. |
| `/socket/socket.io` | idchat Socket.IO connection when `chatWsPath=/socket`. | Engine.IO v4 compatible handshake and notifications. | Handshake works. Private push has recent fixes. Group push currently broadcasts to `group:<groupId>` rooms, but idchat does not show a matching room join path. | P0. Add idchat-compatible group delivery, either member fanout or server-side auto room join. |

## P0 Work Packages

### 1. Add idchat contract tests first

Add a focused contract test layer before changing behavior. It should validate
response envelopes and the fields idchat reads, not every historical field in
the old backend.

Recommended files:

- `internal/api/router_test.go`
- A new `internal/api/idchat_contract_test.go` or
  `internal/compat/idchat_contract_test.go`
- Existing private and group websocket tests for live-notification payloads

Minimum assertions:

- Every P0 HTTP route returns `code: 0` and the old envelope shape.
- List endpoints return `data.total` when the old backend did.
- `user/latest-chat-info-list` returns both group and private-style items.
- Private session items include peer `userInfo.chatPublicKey` when available.
- `private-group-paths` returns `data.list`, not a raw array.
- Stubbed routes fail tests until they return real list objects.
- Socket private and group events include the fields idchat expects to render.

### 2. Rebuild `user/latest-chat-info-list`

This is the most important blocker because it drives the first screen after
login.

Required behavior:

- Resolve the input `metaId` to global identity aliases the same way the old
  backend accepted both local and global IDs.
- Build a unified session list containing:
  - group sessions from group membership and latest group message state;
  - private sessions from private conversations involving that identity.
- Sort sessions by latest message timestamp.
- Return old envelope shape: `data.total`, `data.list`.
- For group sessions, include old room fields that idchat uses or passes
  through: `type`, `groupId`, `roomName`, `roomAvatarUrl`, `roomJoinType`,
  `createMetaId`, `createGlobalMetaId`, `createAddress`, `createUserInfo`,
  `userCount`, `lastMessagePinId`, `timestamp`, `chain`, `blockHeight`,
  `index`, `version`, `content`, `chatType`, and `path`.
- For private sessions, include peer identity fields and `userInfo` with
  `chatPublicKey` and `chatPublicKeyId` when present.

Implementation note:

- Avoid making the groupchat package depend directly on privatechat internals
  in a way that creates a package cycle. Prefer a small shared compatibility
  helper or storage-level query helper that both aggregators can use.

Acceptance:

- A user with old backend private conversations sees those private sessions via
  `socket.metaid.io`.
- idchat no longer drops private sessions because `userInfo.chatPublicKey` is
  missing.

### 3. Restore historical group metadata and message coverage

The live probe showed an old backend group ID that `socket.metaid.io` could not
resolve through `group-info`, and group history returned fewer messages.

Required behavior:

- Historical group create, join, member, role, and message pins are indexed into
  the stores used by compatibility handlers.
- `group-info`, `group-list`, `group-member-list`, `group-chat-list-v2`, and
  `group-chat-list-by-index` can serve groups that idchat users already have in
  their old session lists.
- Old response fields are preserved even if meta-socket internally stores a
  narrower model.

Acceptance:

- For sampled production groups, old and new backends both return non-empty
  group metadata and comparable history counts.
- Pagination fields match idchat expectations: `nextTimestamp` for timestamp
  lists and `lastIndex` for index lists.

### 4. Implement missing and stubbed active routes

Implement these before any production cutover:

- `/chat-api/group-chat/group-chat-list`
- `/chat-api/group-chat/search-groups-and-users`
- `/chat-api/group-chat/group-channel-list`
- `/chat-api/group-chat/channel-chat-list-v3`
- `/chat-api/group-chat/channel-chat-list-by-index`
- `/chat-api/group-chat/group-metaid-join-list`
- `/chat-api/group-chat/socket/online-users`

Route details:

- `group-chat-list` can reuse the V2 storage path, but must return the old
  timestamp-list envelope that task queue code expects.
- `search-groups-and-users` should combine group and user search results into
  the old mixed result list. It must keep `data.total`.
- `group-channel-list` must return channel metadata and newest channel message
  fields, not `{}`.
- `channel-chat-list-v3` and `channel-chat-list-by-index` must filter by
  `channelId` and preserve old list envelopes.
- `group-metaid-join-list` must return the join records idchat uses to recover
  private group passcodes, especially `items[*].k`.
- `group-chat/socket/online-users` must preserve the old response shape:
  `total`, `cursor`, `size`, `onlineWindowSeconds`, and `list[*].userInfo`.

### 5. Fix private compatibility response shapes

Required changes:

- Change `/chat-api/group-chat/private-group-paths` from a raw array to:

```json
{
  "code": 0,
  "message": "success",
  "data": {
    "total": 0,
    "list": []
  }
}
```

- Return list items as objects with at least `path`, `groupId`, and `pinId`.
- Audit `private-chat-list` and `private-chat-list-by-index` against the old
  private message item shape and add any fields idchat renders or forwards.
- Update existing tests that currently lock in the raw-array behavior.

Acceptance:

- idchat code paths reading `res.data.list` work without special cases.
- Private history renders with the same sender and receiver identity metadata
  available from the old backend.

### 6. Add idchat-compatible group websocket delivery

Current private-chat push routing has recent fixes and should be preserved with
tests. Group chat still needs a migration-safe delivery path.

Problem:

- `internal/socket/server.go` broadcasts group chat events to
  `group:<groupId>` rooms.
- The current idchat socket client only connects with `metaid` and `type`
  query parameters; the audited frontend does not show a matching group-room
  join call.

Required behavior:

- A logged-in idchat client receives new group messages for groups it belongs
  to without frontend changes.

Recommended fastest path:

- On a group chat event, fan out to current group member identity aliases using
  the same target-identity delivery path used by private chat.
- Keep room broadcast as an optimization for clients that do join rooms.
- Add tests proving that a connected member socket receives the group event even
  without a room join.

Alternative path:

- Auto-join a socket to all current group rooms during connect based on the
  connecting identity's group membership. This can reduce fanout work, but it
  needs careful refresh behavior when group membership changes.

Acceptance:

- Two idchat clients connected only by `metaid` can send and receive group
  messages live through `socket.metaid.io`.
- Private message delivery remains covered by regression tests.

## P1 Work Packages

- Add `joinBlockGlobalMetaIds` and `joinWhitelistGlobalMetaIds` to
  `group-join-control-list`.
- Add `total`, `size` handling, and old field parity to `search-users`.
- Add old response fields and `total` to `group-list`.
- Add contract tests for `group-person`, `group-user-role`,
  `group-member-list`, and `search-group-members`.
- Add response-shape compatibility to private list endpoints where idchat reads
  duplicate convenience fields such as sender aliases, `chatType`, `data`,
  reply metadata, or user display fields.
- Add `/chat-api/group-chat/socket/user-online` if any idchat runtime or ops
  panel still uses it.

## P2 Work Packages

- Add `/chat-api/group-chat/socket/stats` for rollout diagnostics.
- Add live-probe scripts that compare selected old and new backend endpoints
  and print envelope and key differences.
- Update older docs after the P0 migration gate passes, especially docs that
  currently imply config-only readiness.

## Verification Plan

Local verification before merging hardening work:

```bash
CGO_ENABLED=0 go test ./...
```

Contract smoke against local or staging meta-socket:

```bash
curl -sS "$BASE/chat-api/group-chat/user/latest-chat-info-list?metaId=$META_ID"
curl -sS "$BASE/chat-api/group-chat/group-list?metaId=$META_ID"
curl -sS "$BASE/chat-api/group-chat/group-info?groupId=$GROUP_ID"
curl -sS "$BASE/chat-api/group-chat/group-chat-list?groupId=$GROUP_ID&size=3"
curl -sS "$BASE/chat-api/group-chat/group-chat-list-v2?groupId=$GROUP_ID&size=3"
curl -sS "$BASE/chat-api/group-chat/group-chat-list-by-index?groupId=$GROUP_ID&size=3"
curl -sS "$BASE/chat-api/group-chat/group-channel-list?groupId=$GROUP_ID"
curl -sS "$BASE/chat-api/group-chat/private-group-paths?metaId=$META_ID"
curl -sS "$BASE/chat-api/group-chat/group-metaid-join-list?groupId=$GROUP_ID&metaId=$META_ID"
curl -sS "$BASE/chat-api/group-chat/search-groups-and-users?query=$QUERY&size=5"
curl -sS "$BASE/chat-api/group-chat/search-users?query=$QUERY&size=5"
curl -sS "$BASE/chat-api/group-chat/socket/online-users?cursor=&size=20&withUserInfo=true"
curl -sS "$BASE/push-base/v1/push/get_user_blocked_chats?metaId=$META_ID"
curl -sS "$BASE/socket/socket.io/?EIO=4&transport=polling&metaid=$META_ID&type=pc"
```

Frontend acceptance with idchat config pointed at meta-socket:

- Session list loads with existing group and private conversations.
- Private sessions remain visible because peer `chatPublicKey` is present.
- Group history and private history paginate.
- Task queue polling through `group-chat-list` can find messages.
- Search modal returns both group and user results.
- Online users or bots list loads.
- Subchannel list and subchannel message history load.
- Private group passcode recovery path can read `group-metaid-join-list`.
- Blocked-chat settings load and mutate.
- Two connected users receive private and group messages live through Socket.IO.

Production rollout gate:

- Run old-vs-new live probes for a fixed sample of real idchat users and
  groups.
- Verify every P0 endpoint returns HTTP 200, `code: 0`, and the old envelope
  shape.
- Verify idchat can switch config to `socket.metaid.io` with no source change.
- Keep rollback as a config-only switch back to `https://api.idchat.io`.

