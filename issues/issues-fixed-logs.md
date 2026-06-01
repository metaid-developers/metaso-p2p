# Downstream Issue Handling Log

This file records how downstream-reported issues under `issues/` were handled
in this repository. Keep the original issue files unchanged as the reporter's
evidence, and add maintainer-side resolution notes here.

## 2026-06-01 - Bothub paid service payment metadata gap

- Issue: `2026-06-01-bothub-paid-service-payment-metadata-gap.md`
- Status: Fixed.
- Maintainer check:
  - The issue was reproduced against local `127.0.0.1:18091`.
  - Detail for
    `09d5b9dc05b816d0d6f0641d03f8d42235cb162f9f76e3329805a0c4ca376669i0`
    returned `price="0.01"` and `currency="SPACE"` but empty
    `settlementKind`, `paymentChain`, and `paymentAddress`.
  - The list endpoint had the same missing payment metadata for the same
    service.
  - The provider chain address was present on the record, so the native
    receiver was derivable without inventing a new receiver.
  - The OAC skill-service compatibility rules state that missing
    `settlementKind` defaults to `native`, `MVC` aliases normalize to `SPACE`,
    and provider identity is the payment recipient for native services.
- Fix:
  - Added shared payment metadata normalization for BotHub list and detail
    responses.
  - Missing or unknown non-MRC20 settlement now defaults to `native`.
  - Missing native payment chain is derived from display currency
    (`SPACE`/`MVC` -> `mvc`, `BTC` -> `btc`, `DOGE` -> `doge`).
  - Paid native services with no explicit `paymentAddress` fall back to the
    provider chain address.
  - MRC20 services still require explicit payment metadata and do not fall back
    to provider address.
  - Fixed Pebble read-on-restart so the indexer can restore
    `indexer_meta/*_lastheight` after a process restart instead of starting
    from the configured initial height.
- Verification:
  - Added red/green regression coverage for list and detail native payment
    fallback.
  - Added red/green regression coverage for restoring indexer height after
    reopening the Pebble store.
  - `CGO_ENABLED=0 go test ./internal/aggregator/skillservice ./internal/aggregator/userinfo ./internal/indexer ./internal/storage -count=1`
  - Rebuilt and restarted the local `com.metaid.meta-socket.mvc30d.18091`
    service.
  - The tested service detail now returns `settlementKind="native"`,
    `paymentChain="mvc"`, and
    `paymentAddress="125DQu9dBCXksYWg7HnmnmU3TpBNqnMsZF"`.

## 2026-06-01 - Bothub skill-service availability gap

- Issue: `2026-06-01-bothub-skill-service-availability-gap.md`
- Status: Runtime restored after triage; valid downstream acceptance blocker,
  but not a new skill-service JSON contract or handler bug in this checkout.
- Maintainer check:
  - Current repo state was clean on `main`, with this issue file already present
    in the latest docs commit.
  - Local launchd meta-socket was running on `127.0.0.1:18091`, and `/healthz`
    returned `code=0`.
  - The same local service returned `code=0` and
    `schemaVersion=botHubSkillService.v1` for
    `/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true`,
    but `data.list` was empty.
  - The local service was launched with
    `META_SOCKET_BLOCK_INDEX_ENABLED=false` and a temporary empty Pebble data
    dir, so it cannot discover real `/protocols/skill-service` pins.
  - `META_SOCKET_BASE_URL=http://127.0.0.1:18091 pnpm smoke:meta-socket` in
    Bothub reproduced the downstream blocker: `skill-service list returned an
    empty list`.
  - The previously verified 30-day MVC real-data instance used
    `/tmp/meta-socket-mvc-30d-pebble` and
    `com.codex.meta-socket.mvc30d.18091`; that temporary data dir and old
    launchd job were no longer present on this Mac.
  - `https://api.idchat.io/api/bot-hub/skill-service/list?...` still returned
    nginx `502 Bad Gateway`.
  - `https://api.idchat.io/chat-api/` was healthy for the group-chat service,
    while `https://api.idchat.io/chat-api/bot-hub/skill-service/list?...`
    returned `404 page not found`.
- Decision:
  - The issue is reasonable because Bothub mock-disabled marketplace/order
    acceptance requires at least one real, orderable BotHub skill-service list
    item and a detail payload with provider/payment fields.
  - No code change is recommended for this repo solely to satisfy the current
    empty-list symptom. Returning fake or seeded services from the production
    aggregator would hide the real readiness gap.
  - Keep Bothub pointed at native meta-socket shape:
    `<base>/api/bot-hub/skill-service/*`. The `/chat-api` compatibility prefix
    remains the idchat group/private chat surface; using it as
    `META_SOCKET_BASE_URL` for Bothub builds `/chat-api/api/bot-hub/*`, which is
    not a mounted BotHub route.
  - Remaining action is runtime/deployment readiness: run an acceptance or
    production meta-socket instance with MVC block indexing enabled and real RPC
    credentials, or publish a documented staging/production base URL where
    native `/api/bot-hub/*` routes are healthy and backed by indexed
    `/protocols/skill-service` data.
- Runtime follow-up:
  - Replaced the empty local launchd job with
    `com.metaid.meta-socket.mvc30d.18091` on `127.0.0.1:18091`.
  - Built the current binary to `/Users/tusm/.local/bin/meta-socket`.
  - Moved the MVC Pebble data dir out of `/tmp` to
    `/Users/tusm/.local/var/meta-socket/mvc-30d-pebble`.
  - Enabled MVC block indexing from height `170725`; logs show real pins being
    parsed and `groupchat`, `privatechat`, `userinfo`, and `skillservice`
    Pebble stores populated.
- Verification:
  - Local curl for `/healthz`, local BotHub list, and local nonexistent detail.
  - Public curl for `https://api.idchat.io/api/bot-hub/skill-service/list?...`,
    `https://api.idchat.io/chat-api/`, and
    `https://api.idchat.io/chat-api/bot-hub/skill-service/list?...`.
  - `META_SOCKET_BASE_URL=http://127.0.0.1:18091 pnpm smoke:meta-socket`
    reproduced the empty-list failure in Bothub.
  - After runtime restoration, local BotHub list returned a real
    `botHubSkillService.v1` item (`seedance-service`), detail returned
    `botHubSkillServiceDetail.v1` with provider data, and the documented group
    chat sample returned real chain rows.
  - After runtime restoration,
    `META_SOCKET_BASE_URL=http://127.0.0.1:18091 pnpm smoke:meta-socket`
    passed in Bothub.

## 2026-05-31 - Bothub aggregator readiness

- Issue: `2026-05-31-bothub-aggregator-readiness.md`
- Status: Triaged; still blocked by runtime/deployment readiness, not by a new
  Bot Hub API code gap in this checkout.
- Related commits already present:
  - `5fa62ec fix: fill provider chat key from profile fallback`
  - `dfe28c4 fix: recover global profile chat key`
  - `53073af docs: record bothub aggregator readiness blocker`
- Maintainer check:
  - No process was listening on `127.0.0.1:18091` during triage.
  - `https://api.idchat.io/api/bot-hub/skill-service/list?...` still returned
    nginx `502 Bad Gateway`.
  - Starting the current local binary with
    `META_SOCKET_HTTP_ADDR=127.0.0.1:18091` and a temporary Pebble data dir made
    `/healthz` return JSON `code=0`.
  - The same local run made
    `/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true`
    return parseable JSON with schema version `botHubSkillService.v1`.
  - The same local run made
    `/api/bot-hub/skill-service/detail/nonexistent?chainName=mvc` return a
    parseable JSON `40400` envelope.
- Decision:
  - The issue is reasonable as a release-readiness blocker because Bothub cannot
    verify real live services while no expected local service is running and the
    public endpoint returns 502.
  - No additional code change is needed for health/list/detail JSON availability
    in this checkout.
  - Remaining action is deployment/runtime: run a current meta-socket instance
    for Bothub acceptance, or publish a replacement base URL. The repo default
    port remains `:8080`; Bothub's `127.0.0.1:18091` expectation is an
    environment-specific acceptance port and must be set via
    `META_SOCKET_HTTP_ADDR=127.0.0.1:18091`.
- Verification:
  - `CGO_ENABLED=0 go test ./internal/aggregator/skillservice ./internal/aggregator/userinfo`
  - Local temporary `127.0.0.1:18091` health/list/detail curl checks.

## 2026-05-30 - Bothub paid service detail missing provider chat public key

- Issue: `2026-05-30-bothub-paid-service-missing-provider-chatpubkey.md`
- Status: Fixed.
- Fix commit: `5fa62ec fix: fill provider chat key from profile fallback`
- Summary:
  - Wired the skill-service aggregator to the userinfo aggregator in-process.
  - Added provider profile fallback so paid service detail can expose
    `data.provider.chatPubkey` when the provider has a published chat key.
  - Added regression coverage for detail hydration through the real userinfo
    adapter.
- Verification:
  - `CGO_ENABLED=0 go test ./internal/aggregator/skillservice`
  - `CGO_ENABLED=0 go test ./internal/aggregator/userinfo`

## 2026-05-30 - `/api/info/globalmetaid/:globalMetaId` missing chat public key

- Issue: `2026-05-30-info-globalmetaid-missing-chatpubkey.md`
- Status: Fixed.
- Fix commit: `dfe28c4 fix: recover global profile chat key`
- Summary:
  - Continued profile fallback when a legacy `globalMetaId` lookup returns an
    incomplete shell profile.
  - Added address/metaid fallback and merge-until-complete behavior so
    `chatpubkey`, `chatpubkeyId`, canonical `globalMetaId`, address, and avatar
    can be recovered.
  - Treated `/content/` placeholder avatars as replaceable during profile merge.
- Verification:
  - `CGO_ENABLED=0 go test ./internal/aggregator/userinfo`
  - `CGO_ENABLED=0 go test ./internal/aggregator/skillservice`
