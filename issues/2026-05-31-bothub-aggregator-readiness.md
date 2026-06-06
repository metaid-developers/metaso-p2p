# Bothub release hardening cannot verify aggregator readiness

## Summary

Bothub Task 4 release-hardening checks could not verify real metaso-p2p service readiness on 2026-05-31. The local metaso-p2p HTTP service was not listening on `127.0.0.1:18091`, and the public `api.idchat.io` Bot Hub service list endpoint returned `502 Bad Gateway`.

This blocks Bothub from proving that mock-disabled service list, service detail, provider orderability, and provider chat-key hydration work against current live data.

## Environment

- Date checked: 2026-05-31 22:11 CST / 2026-05-31 14:11 UTC
- Bothub revision: `ac5a113`
- metaso-p2p revision: `dfe28c4`
- Local expected base URL: `http://127.0.0.1:18091`
- Public base URL checked: `https://api.idchat.io`

## Evidence

Listener scan from the Bothub release-hardening worktree:

```bash
lsof -nP -iTCP -sTCP:LISTEN | rg "(18091|5176|vite|metaso-p2p)" || true
```

Result:

```text
node      27845 tusm   13u  IPv6 0x9f6b9bc4c256f443      0t0  TCP [::1]:5176 (LISTEN)
```

Local health check:

```bash
curl -sS -i http://127.0.0.1:18091/healthz || true
```

Result:

```text
curl: (7) Failed to connect to 127.0.0.1 port 18091 after 0 ms: Couldn't connect to server
```

Public Bot Hub service list check:

```bash
curl -sS -i 'https://api.idchat.io/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true' || true
```

Result:

```text
HTTP/1.1 502 Bad Gateway
Server: nginx/1.29.1
Date: Sun, 31 May 2026 14:09:28 GMT
Content-Type: text/html
Content-Length: 157
Connection: keep-alive

<html>
<head><title>502 Bad Gateway</title></head>
<body>
<center><h1>502 Bad Gateway</h1></center>
<hr><center>nginx/1.29.1</center>
</body>
</html>
```

Local smoke check:

```bash
METASO_P2P_BASE_URL=http://127.0.0.1:18091 pnpm smoke:metaso-p2p
```

Result:

```text
$ node scripts/smoke-metaso-p2p.mjs
[smoke:metaso-p2p] smoke failed: healthz request failed (http://127.0.0.1:18091/healthz): fetch failed
[ELIFECYCLE] Command failed with exit code 1.
```

Provider chat-key probe against both candidate endpoints:

```text
BASE http://127.0.0.1:18091
LIST {"ok":false,"error":"fetch failed"}
BASE https://api.idchat.io
LIST {"ok":true,"status":502,"text":"<html>\r\n<head><title>502 Bad Gateway</title></head>\r\n<body>\r\n<center><h1>502 Bad Gateway</h1></center>\r\n<hr><center>nginx/1.29.1</center>\r\n</body>\r\n</html>\r\n"}
```

Mock-disabled Bothub browser check:

```bash
VITE_METASO_P2P_BASE_URL=/metaso-p2p VITE_USE_AGGREGATOR_MOCK=false VITE_USE_WS_MOCK=false pnpm dev -- --host 127.0.0.1
```

Vite selected `http://localhost:5177/` because `5176` was already in use. The page showed `Could not load services` with detail `Failed to execute 'json' on 'Response': Unexpected end of JSON input`.

Vite proxy logged:

```text
10:10:56 PM [vite] http proxy error: /api/bot-hub/skill-service/list?sortBy=rating&order=desc
Error: connect ECONNREFUSED 127.0.0.1:18091
    at TCPConnectWrap.afterConnect [as oncomplete] (node:net:1705:16)
```

## Expected API Contract

For Bothub release acceptance, at least one live endpoint should be available and return parseable JSON for:

```text
GET /healthz
GET /api/bot-hub/skill-service/list?size=20&chainName=mvc&sortBy=updated&order=desc&includeInactive=true
GET /api/bot-hub/skill-service/detail/:serviceId?chainName=mvc
```

Service detail data should expose enough orderability information for Bothub to validate a buyer request before payment or broadcast. For paid encrypted orders, Bothub needs a provider chat public key from either service detail or a compatible profile endpoint, for example:

```ts
data.provider = {
  metaid: string
  globalMetaId: string
  name: string
  avatar: string
  chatPubkey: string
}
```

## Actual Behavior

- No local service responded on `127.0.0.1:18091`.
- Public `https://api.idchat.io/api/bot-hub/skill-service/list` returned nginx `502 Bad Gateway`.
- Bothub with aggregator mocks disabled showed a service-loading error rather than real services.
- No current list/detail/profile payload was available to verify provider field spellings or frontend normalization.

## Product Impact

- BotHub cannot verify real service discovery for release hardening.
- BotHub cannot verify service detail orderability against current live data.
- BotHub cannot verify whether provider chat public keys are currently present or missing.
- BotHub should not change frontend normalization based on stale payload notes while both live endpoints are unavailable.

## Reproduction

From the Bothub repo:

```bash
lsof -nP -iTCP -sTCP:LISTEN | rg "(18091|5176|vite|metaso-p2p)" || true
curl -sS -i http://127.0.0.1:18091/healthz || true
curl -sS -i 'https://api.idchat.io/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true' || true
METASO_P2P_BASE_URL=http://127.0.0.1:18091 pnpm smoke:metaso-p2p
VITE_METASO_P2P_BASE_URL=/metaso-p2p VITE_USE_AGGREGATOR_MOCK=false VITE_USE_WS_MOCK=false pnpm dev -- --host 127.0.0.1
```

Then open the served Bothub URL and confirm whether real services load. In this run, the app showed an honest service-loading error and Vite logged `ECONNREFUSED 127.0.0.1:18091`.

## Acceptance Criteria

- `http://127.0.0.1:18091/healthz` returns a healthy JSON response in the expected local acceptance environment, or a current replacement base URL is documented.
- The public or local Bot Hub service list endpoint returns parseable JSON with real services.
- At least one service detail endpoint can be sampled for orderability fields.
- Paid-service samples expose a provider chat public key through service detail or a documented profile fallback when the provider has one.
- Bothub with aggregator mocks disabled loads real services, or the release checklist explicitly treats the unavailable service as a blocking backend readiness issue.
