# Downstream Issue Handling Log

This file records how downstream-reported issues under `issues/` were handled
in this repository. Keep the original issue files unchanged as the reporter's
evidence, and add maintainer-side resolution notes here.

## 2026-06-01 - Bothub production metaso-p2p endpoint gap

- Issue: `2026-06-01-bothub-production-metaso-p2p-endpoint-gap.md`
- Status: Code/API fixed; deployment host assignment remains an ops/runtime
  action.
- Maintainer check:
  - The request is reasonable. Bothub should use a metaso-p2p root base URL
    and should not depend on `https://api.idchat.io/chat-api`.
  - Existing code already exposed CORS globally, `/healthz`, BotHub
    skill-service routes, and Socket.IO at `/socket/socket.io`.
  - Existing private-chat routes were only canonical under historical
    `/api/group-chat/...` paths, with `/chat-api/group-chat/...` retained for
    idchat compatibility.
  - A repository code change cannot mint a public production/staging hostname,
    but the native route surface and reverse-proxy/base-URL contract can be
    made explicit here for deployment.
- Fix:
  - Added canonical native private-chat routes:
    - `GET /api/private-chat/homes/:metaId`
    - `GET /api/private-chat/messages`
    - `GET /api/private-chat/messages/by-index`
    - `GET /api/private-chat/paths`
  - These routes reuse the existing private-chat handlers, so response
    envelopes and query semantics stay identical to the historical
    `/api/group-chat/...` compatibility routes.
  - Kept old `/api/group-chat/...` and `/chat-api/group-chat/...` routes
    working.
  - Documented Bothub's endpoint contract in
    `docs/BOTHUB_METASO_P2P_ENDPOINT.md`.
  - Updated `docs/DEPLOY.md` and `docs/IDCHAT_API_CONTRACT.md` with the root
    base URL rule, canonical private-chat routes, reverse-proxy shape, and
    acceptance commands.
- Verification:
  - Added red/green route coverage for canonical private-chat aliases in
    `internal/aggregator/privatechat` and full router registration in
    `internal/api`.
  - `CGO_ENABLED=0 go test ./internal/aggregator/privatechat ./internal/api ./internal/aggregator/skillservice -count=1`
  - `git diff --check`
  - `CGO_ENABLED=0 go build -o /Users/tusm/.local/bin/metaso-p2p ./cmd/metaso-p2p`
  - Restarted local launch agent
    `com.metaid.metaso-p2p.mvc30d.18091`.
  - Local canonical private-chat checks on `127.0.0.1:18091`:
    - `/api/private-chat/messages?...` returned `code=0`, `total=57`.
    - `/api/private-chat/messages/by-index?...` returned `code=0`,
      `total=57`.
    - `/api/private-chat/homes/<metaId>` returned `code=0`, `count=6`.
    - `/api/private-chat/paths?metaId=<metaId>` returned `code=0`,
      `count=6`.
  - Canonical route CORS preflight returned HTTP `204` with
    `Access-Control-Allow-Origin: *`.
  - Bothub smoke passed:
    `METASO_P2P_BASE_URL=http://127.0.0.1:18091 pnpm smoke:metaso-p2p`.

## 2026-06-01 - Bothub AI_Sunny provider chat identity gap

- Issue: `2026-06-01-bothub-ai-sunny-provider-chat-identity-gap.md`
- Status: Fixed.
- Maintainer check:
  - The issue was reasonable and reproduced against local `127.0.0.1:18091`.
  - BotHub detail for
    `e9a7064693dfdcbea381c8355c3c91c0ba3947abee816287774729c432378e61i0`
    returned AI_Sunny provider identity as the legacy chain address
    `1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za`.
  - `userinfo` already resolved that provider to canonical globalMetaId
    `idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz`, but BotHub list/detail
    did not preserve the profile identity fields.
  - Private chat persisted messages under the simplemsg `from/to` identity
    pair, so querying with the canonical provider peer had to resolve aliases
    to see messages stored under the legacy address.
  - After restart, persisted private-chat data was also not visible until a
    namespace write reopened the Pebble DB; the read path needed to open the
    namespace on demand.
- Fix:
  - Extended BotHub provider profile snapshots to carry canonical
    MetaID/globalMetaId/address from `userinfo`.
  - BotHub list/detail now prefer canonical provider identity from profile and
    keep `providerAddress` as the chain/payment address.
  - BotHub `providerGlobalMetaId` list filtering now matches the resolved
    provider identity set, so canonical IDs and legacy aliases both work.
  - Private-chat list/list-by-index resolve both participants through
    `userinfo` aliases and scan all alias pair prefixes with de-duplication.
  - `main` wires private-chat to the in-process `userinfo` lookup.
  - Pebble `ScanPrefix` now opens a namespace on demand, restoring historical
    private-chat reads immediately after process restart.
- Verification:
  - `CGO_ENABLED=0 go test ./internal/aggregator/skillservice ./internal/aggregator/privatechat ./internal/aggregator/userinfo ./internal/api -count=1`
  - `git diff --check`
  - `CGO_ENABLED=0 go build -o /Users/tusm/.local/bin/metaso-p2p ./cmd/metaso-p2p`
  - Restarted local launch agent
    `com.metaid.metaso-p2p.mvc30d.18091`.
  - Local detail now returns `provider.globalMetaId` as
    `idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz` and `provider.address` as
    `1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za`.
  - Local list filtered by AI_Sunny canonical `providerGlobalMetaId` returns
    the wiki service.
  - Local private-chat query with `otherMetaId=idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz`
    returns `total=57`, including AI_Sunny reply pins
    `42c3f0ab816e06e749e9394caa7bebdf7cdf98125984a024be0a75cb74fef022i0`,
    `2f26c872f019bc65036e79eb05e8af795d52c69afe0f34e52cd1c73bb5511ac2i0`,
    and `0299ef93ada6171276aaa218a5accc2e5bcd567538e61a0e3b1f58c1aa5d537ei0`
    at block height `175638`.
  - Local indexer was caught up: local height `175640`, remote height
    `175640`, lag `0`.
  - Bothub smoke passed:
    `METASO_P2P_BASE_URL=http://127.0.0.1:18091 pnpm smoke:metaso-p2p`.

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
  - Rebuilt and restarted the local `com.metaid.metaso-p2p.mvc30d.18091`
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
  - Local launchd metaso-p2p was running on `127.0.0.1:18091`, and `/healthz`
    returned `code=0`.
  - The same local service returned `code=0` and
    `schemaVersion=botHubSkillService.v1` for
    `/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc&includeInactive=true`,
    but `data.list` was empty.
  - The local service was launched with
    `METASO_P2P_BLOCK_INDEX_ENABLED=false` and a temporary empty Pebble data
    dir, so it cannot discover real `/protocols/skill-service` pins.
  - `METASO_P2P_BASE_URL=http://127.0.0.1:18091 pnpm smoke:metaso-p2p` in
    Bothub reproduced the downstream blocker: `skill-service list returned an
    empty list`.
  - The previously verified 30-day MVC real-data instance used
    `/tmp/metaso-p2p-mvc-30d-pebble` and
    `com.codex.metaso-p2p.mvc30d.18091`; that temporary data dir and old
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
  - Keep Bothub pointed at native metaso-p2p shape:
    `<base>/api/bot-hub/skill-service/*`. The `/chat-api` compatibility prefix
    remains the idchat group/private chat surface; using it as
    `METASO_P2P_BASE_URL` for Bothub builds `/chat-api/api/bot-hub/*`, which is
    not a mounted BotHub route.
  - Remaining action is runtime/deployment readiness: run an acceptance or
    production metaso-p2p instance with MVC block indexing enabled and real RPC
    credentials, or publish a documented staging/production base URL where
    native `/api/bot-hub/*` routes are healthy and backed by indexed
    `/protocols/skill-service` data.
- Runtime follow-up:
  - Replaced the empty local launchd job with
    `com.metaid.metaso-p2p.mvc30d.18091` on `127.0.0.1:18091`.
  - Built the current binary to `/Users/tusm/.local/bin/metaso-p2p`.
  - Moved the MVC Pebble data dir out of `/tmp` to
    `/Users/tusm/.local/var/metaso-p2p/mvc-30d-pebble`.
  - Enabled MVC block indexing from height `170725`; logs show real pins being
    parsed and `groupchat`, `privatechat`, `userinfo`, and `skillservice`
    Pebble stores populated.
- Verification:
  - Local curl for `/healthz`, local BotHub list, and local nonexistent detail.
  - Public curl for `https://api.idchat.io/api/bot-hub/skill-service/list?...`,
    `https://api.idchat.io/chat-api/`, and
    `https://api.idchat.io/chat-api/bot-hub/skill-service/list?...`.
  - `METASO_P2P_BASE_URL=http://127.0.0.1:18091 pnpm smoke:metaso-p2p`
    reproduced the empty-list failure in Bothub.
  - After runtime restoration, local BotHub list returned a real
    `botHubSkillService.v1` item (`seedance-service`), detail returned
    `botHubSkillServiceDetail.v1` with provider data, and the documented group
    chat sample returned real chain rows.
  - After runtime restoration,
    `METASO_P2P_BASE_URL=http://127.0.0.1:18091 pnpm smoke:metaso-p2p`
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
    `METASO_P2P_HTTP_ADDR=127.0.0.1:18091` and a temporary Pebble data dir made
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
  - Remaining action is deployment/runtime: run a current metaso-p2p instance
    for Bothub acceptance, or publish a replacement base URL. The repo default
    port remains `:8080`; Bothub's `127.0.0.1:18091` expectation is an
    environment-specific acceptance port and must be set via
    `METASO_P2P_HTTP_ADDR=127.0.0.1:18091`.
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
