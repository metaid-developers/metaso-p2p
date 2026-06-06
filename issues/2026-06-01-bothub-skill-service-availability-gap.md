# Bothub needs an available non-empty BotHub skill-service aggregator

## Summary

Bothub release acceptance is blocked by the current BotHub skill-service
aggregator availability, not by the idchat group-chat API health surface.

As of 2026-06-01 10:19 CST / 2026-06-01 02:19 UTC:

- Local `http://127.0.0.1:18091/healthz` is healthy.
- Local `http://127.0.0.1:18091/api/bot-hub/skill-service/list` returns a
  valid `botHubSkillService.v1` envelope, but `data.list` is empty.
- Public `https://api.idchat.io/chat-api/` is healthy for idchat group-chat.
- Public `https://api.idchat.io/api/bot-hub/skill-service/list` returns nginx
  `502 Bad Gateway`.
- BotHub paths under the public idchat prefix, such as
  `https://api.idchat.io/chat-api/api/bot-hub/skill-service/list`, return 404.

Bothub cannot complete mock-disabled free or paid order acceptance until a
local or public BotHub skill-service list/detail endpoint returns at least one
real orderable service.

## Business Requirement From Bothub

Bothub is a pure frontend buyer app. It depends on metaso-p2p for:

- service discovery: `GET /api/bot-hub/skill-service/list`
- service detail/orderability: `GET /api/bot-hub/skill-service/detail/:serviceId`
- provider payment and chat-key fields needed before buyer checkout

For release acceptance, Bothub needs one of these to be true:

1. The local acceptance metaso-p2p at `127.0.0.1:18091` indexes and returns at
   least one real skill-service item plus a detail payload.
2. A public/staging metaso-p2p base URL is documented and returns the native
   BotHub `/api/bot-hub/*` routes.
3. If `https://api.idchat.io/chat-api/` is intended to be the public base for
   Bothub too, it must expose/alias the BotHub aggregator routes, not only
   group/private chat routes.

## Environment

- Date checked: 2026-06-01 10:19 CST / 2026-06-01 02:19 UTC
- Bothub revision checked: `50de390`
- metaso-p2p checkout revision: `31db7ea`
- metaso-p2p branch: `main`
- Local expected base URL: `http://127.0.0.1:18091`
- Public idchat chat API checked: `https://api.idchat.io/chat-api/`
- Public native BotHub base checked: `https://api.idchat.io`

## Evidence

Listener scan:

```bash
lsof -nP -iTCP -sTCP:LISTEN | rg '(18091|5176|5177|vite|metaso-p2p)' || true
```

Result:

```text
meta-sock  3962 tusm    6u  IPv4  ...  TCP 127.0.0.1:18091 (LISTEN)
node      27845 tusm   13u  IPv6  ...  TCP [::1]:5176 (LISTEN)
```

Local health:

```bash
curl -sS -m 8 -D - http://127.0.0.1:18091/healthz
```

Result:

```text
HTTP/1.1 200 OK

{"code":0,"data":{"service":"metaso-p2p","status":"ok","version":"dev"},"message":"","processingTime":1780280385264}
```

Local BotHub service list:

```bash
curl -sS -m 8 -D - 'http://127.0.0.1:18091/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true'
```

Result:

```text
HTTP/1.1 200 OK

{"code":0,"data":{"list":[],"nextCursor":"","total":null,"aggregatedAt":1780280385332,"schemaVersion":"botHubSkillService.v1"},"message":"","processingTime":1780280385332}
```

Bothub smoke against local metaso-p2p:

```bash
METASO_P2P_BASE_URL=http://127.0.0.1:18091 pnpm --dir /Users/tusm/.config/superpowers/worktrees/bothub/codex-delivery-workspace-release-hardening smoke:metaso-p2p
```

Result:

```text
[smoke:metaso-p2p] smoke failed: skill-service list returned an empty list
[ELIFECYCLE] Command failed with exit code 1.
```

Public idchat chat API:

```bash
curl -sS -m 12 -D - https://api.idchat.io/chat-api/
curl -sS -m 12 -D - https://api.idchat.io/chat-api/health
curl -sS -m 12 -D - https://api.idchat.io/chat-api/status
```

Results:

```text
HTTP/1.1 200 OK
{"docs":"/group-chat/docs/index.html","health":"/health","service":"group-chat","status":"/status","version":"1.0.0"}

HTTP/1.1 200 OK
{"service":"group-chat","status":"ok"}

HTTP/1.1 200 OK
{"service":"group-chat","stats":{"indexer":"running","initialized":true}}
```

Public native BotHub service list:

```bash
curl -sS -m 12 -D - 'https://api.idchat.io/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true'
```

Result:

```text
HTTP/1.1 502 Bad Gateway
Server: nginx/1.29.1
Content-Type: text/html

<html>
<head><title>502 Bad Gateway</title></head>
...
```

BotHub routes under the idchat prefix:

```bash
curl -sS -m 12 -D - 'https://api.idchat.io/chat-api/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true'
curl -sS -m 12 -D - 'https://api.idchat.io/chat-api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true'
```

Both returned:

```text
HTTP/1.1 404 Not Found

404 page not found
```

Bothub smoke with the idchat prefix:

```bash
METASO_P2P_BASE_URL=https://api.idchat.io/chat-api pnpm --dir /Users/tusm/.config/superpowers/worktrees/bothub/codex-delivery-workspace-release-hardening smoke:metaso-p2p
```

Result:

```text
[smoke:metaso-p2p] smoke failed: healthz request failed (https://api.idchat.io/chat-api/healthz): healthz returned HTTP 404
[ELIFECYCLE] Command failed with exit code 1.
```

## Current Interpretation

The public `/chat-api/` prefix is healthy, but it is the group-chat compatibility
surface. It does not currently satisfy Bothub's BotHub marketplace dependency.

Bothub should keep using the native aggregator path shape:

```text
<metaSocketBaseUrl>/api/bot-hub/skill-service/list
<metaSocketBaseUrl>/api/bot-hub/skill-service/detail/:serviceId
```

Pointing Bothub at `https://api.idchat.io/chat-api` would build
`/chat-api/api/bot-hub/*`, which currently returns 404.

## Product Impact

- Bothub cannot load real services with aggregator mocks disabled.
- Bothub cannot select a real free service to complete the free-order path.
- Bothub cannot select a paid service to inspect payment amount, receiver, and
  provider chat public key.
- Chrome + Metalet acceptance cannot proceed to meaningful checkout prompts
  without a real list/detail payload.
- Frontend normalization changes would be speculative while no live orderable
  service payload is available.

## Acceptance Criteria

- `GET http://127.0.0.1:18091/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true`
  returns `code: 0` and at least one real orderable service in `data.list` for
  the local Bothub acceptance environment.
- For at least one returned service id, `GET /api/bot-hub/skill-service/detail/:serviceId?chainName=mvc`
  returns a detail payload with orderability fields required by Bothub.
- A public/staging base URL for Bothub is documented. That base URL must serve
  native BotHub `/api/bot-hub/*` routes, or `/chat-api/` must explicitly alias
  those routes if it is the intended shared public prefix.
- `METASO_P2P_BASE_URL=<documented-base> pnpm smoke:metaso-p2p` from the
  Bothub repo passes the list/detail portions of the smoke script.
- Bothub with `VITE_USE_AGGREGATOR_MOCK=false` can load at least one real
  service card from metaso-p2p.

## Related Context

- Previous issue with older outage evidence:
  `issues/2026-05-31-bothub-aggregator-readiness.md`
- Maintainer-side log:
  `issues/issues-fixed-logs.md`
- Bothub acceptance correction:
  `docs/superpowers/acceptance/2026-05-31-delivery-workspace-v1.md` in the
  Bothub release-hardening worktree
