# Bothub needs a first-party production/staging metaso-p2p endpoint

## Summary

Bothub should not depend on `https://api.idchat.io` or idchat `/chat-api/` for
release or production. Bothub is a pure frontend buyer app whose backend
contract is metaso-p2p. The local `http://127.0.0.1:18091` runtime now passes
Bothub smoke with real indexed data, but there is still no documented non-local
production/staging metaso-p2p base URL that Bothub can use for release
acceptance or hosted deployment.

Please provide a metaso-p2p-owned production/staging endpoint, or document the
deployment target Bothub should use, with the native routes listed below. While
doing so, please also promote private chat to its own canonical API namespace so
new Bothub code does not need to depend on the historical `/api/group-chat/...`
private-chat paths.

## Why this is not an idchat dependency

Earlier Bothub checks probed `https://api.idchat.io` only as a diagnostic while
the local metaso-p2p runtime was unavailable or catching up. That should not be
treated as a product dependency.

Known observations:

- `https://api.idchat.io/chat-api/` is a group/private chat compatibility
  surface.
- idchat `/chat-api/` does not expose BotHub skill-service routes.
- Bothub should not set `VITE_METASO_P2P_BASE_URL` to
  `https://api.idchat.io/chat-api` because it builds unmounted paths like
  `/chat-api/api/bot-hub/*`.
- Any idchat-like ability Bothub needs should be provided by metaso-p2p.

## Required Meta-Socket Surface

The assigned base URL should support:

- `GET /healthz`
- `GET /api/bot-hub/skill-service/list`
- `GET /api/bot-hub/skill-service/detail/:serviceId`
- `GET /api/private-chat/homes/:metaId`
- `GET /api/private-chat/messages`
- `GET /api/private-chat/messages/by-index`
- `GET /api/private-chat/paths`
- Socket.IO at `/socket/socket.io`

## Private-Chat API Migration Requirement

Bothub needs private-chat history for Delivery recovery. It does not need
group/channel/community chat product features.

The current usable metaso-p2p routes are mounted under the historical
`/api/group-chat/...` namespace:

- `GET /api/group-chat/chat/homes/:metaId`
- `GET /api/group-chat/private-chat-list`
- `GET /api/group-chat/private-chat-list-by-index`
- `GET /api/group-chat/private-group-paths`

Please add canonical private-chat aliases with the same response envelopes and
query semantics:

| Canonical route | Existing compatibility route |
| --- | --- |
| `GET /api/private-chat/homes/:metaId` | `GET /api/group-chat/chat/homes/:metaId` |
| `GET /api/private-chat/messages?metaId=&otherMetaId=&cursor=&size=&timestamp=` | `GET /api/group-chat/private-chat-list?...` |
| `GET /api/private-chat/messages/by-index?metaId=&otherMetaId=&startIndex=&size=` | `GET /api/group-chat/private-chat-list-by-index?...` |
| `GET /api/private-chat/paths?metaId=` | `GET /api/group-chat/private-group-paths?metaId=` |

Keep the old `/api/group-chat/...` routes and `/chat-api/group-chat/...`
compatibility routes working for existing clients. Bothub will switch to the
canonical `/api/private-chat/*` routes once they are available.

The deployment should also:

- return real indexed MVC skill-service data, not an empty temporary Pebble;
- include provider canonical chat identity and payment metadata fixes already
  verified locally;
- support browser CORS for Bothub's production/staging origins, or provide the
  intended reverse-proxy shape;
- keep response envelopes compatible with the current local
  `botHubSkillService.v1` and private-chat contracts.

## Bothub Acceptance Commands

From the Bothub repo, this should pass against the assigned endpoint:

```bash
METASO_P2P_BASE_URL=https://<metaso-p2p-host> pnpm smoke:metaso-p2p
```

For browser acceptance, Bothub should be able to run with:

```dotenv
VITE_METASO_P2P_BASE_URL=https://<metaso-p2p-host>
VITE_USE_AGGREGATOR_MOCK=false
VITE_USE_WS_MOCK=false
```

and load real service list/detail data plus Delivery private-chat history
without relying on idchat.

Additional route checks expected before Bothub switches over:

```bash
curl 'https://<metaso-p2p-host>/api/private-chat/homes/<buyer-metaId>'
curl 'https://<metaso-p2p-host>/api/private-chat/messages?metaId=<buyer-metaId>&otherMetaId=<provider-metaId>&cursor=&size=5'
curl 'https://<metaso-p2p-host>/api/private-chat/messages/by-index?metaId=<buyer-metaId>&otherMetaId=<provider-metaId>&startIndex=0&size=5'
```

Each canonical route should return the same envelope/data shape as its existing
`/api/group-chat/...` compatibility route.

## Current Impact

- Local/private beta can continue against `http://127.0.0.1:18091`.
- Hosted production or staging release is blocked until metaso-p2p provides
  the assigned base URL or deployment/proxy instructions.
- This is a backend/runtime ownership gap, not a request to add a Bothub
  backend and not a request to point Bothub at idchat.
