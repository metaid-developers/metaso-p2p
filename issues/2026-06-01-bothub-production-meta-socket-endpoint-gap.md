# Bothub needs a first-party production/staging meta-socket endpoint

## Summary

Bothub should not depend on `https://api.idchat.io` or idchat `/chat-api/` for
release or production. Bothub is a pure frontend buyer app whose backend
contract is meta-socket. The local `http://127.0.0.1:18091` runtime now passes
Bothub smoke with real indexed data, but there is still no documented non-local
production/staging meta-socket base URL that Bothub can use for release
acceptance or hosted deployment.

Please provide a meta-socket-owned production/staging endpoint, or document the
deployment target Bothub should use, with the native routes listed below.

## Why this is not an idchat dependency

Earlier Bothub checks probed `https://api.idchat.io` only as a diagnostic while
the local meta-socket runtime was unavailable or catching up. That should not be
treated as a product dependency.

Known observations:

- `https://api.idchat.io/chat-api/` is a group/private chat compatibility
  surface.
- idchat `/chat-api/` does not expose BotHub skill-service routes.
- Bothub should not set `VITE_META_SOCKET_BASE_URL` to
  `https://api.idchat.io/chat-api` because it builds unmounted paths like
  `/chat-api/api/bot-hub/*`.
- Any idchat-like ability Bothub needs should be provided by meta-socket.

## Required Meta-Socket Surface

The assigned base URL should support:

- `GET /healthz`
- `GET /api/bot-hub/skill-service/list`
- `GET /api/bot-hub/skill-service/detail/:serviceId`
- `GET /api/group-chat/chat/homes/:metaId`
- `GET /api/group-chat/private-chat-list`
- `GET /api/group-chat/private-chat-list-by-index`
- Socket.IO at `/socket/socket.io`

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
META_SOCKET_BASE_URL=https://<meta-socket-host> pnpm smoke:meta-socket
```

For browser acceptance, Bothub should be able to run with:

```dotenv
VITE_META_SOCKET_BASE_URL=https://<meta-socket-host>
VITE_USE_AGGREGATOR_MOCK=false
VITE_USE_WS_MOCK=false
```

and load real service list/detail data plus Delivery private-chat history
without relying on idchat.

## Current Impact

- Local/private beta can continue against `http://127.0.0.1:18091`.
- Hosted production or staging release is blocked until meta-socket provides
  the assigned base URL or deployment/proxy instructions.
- This is a backend/runtime ownership gap, not a request to add a Bothub
  backend and not a request to point Bothub at idchat.
