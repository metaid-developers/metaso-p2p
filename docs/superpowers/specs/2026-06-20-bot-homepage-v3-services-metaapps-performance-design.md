# Bot Homepage v3 Services and MetaApps Performance Design

Date: 2026-06-20
Status: Internal design spec for performance-focused follow-up work
Target repo: `metaso-p2p`

## Goal

Reduce the response cost of:

```text
GET /api/bot-homepage/globalmetaid/:globalMetaId?version=v3
```

for the common case where a bot has a small number of homepage `services` and
`metaapps`, but the current implementation still pays read-time costs that
scale with legacy index gaps or redundant identity scans.

This design targets three concrete outcomes:

1. `processingTime` for homepage `services` should stop depending on
   `skillservice` full-catalog fallback scans in steady state.
2. `metaapps` should stop defaulting to three identity-index reads and merge
   work when canonical-global completeness has been established for historical
   homepage-visible records.
3. v3 section query flags should become exact enough to support isolated
   production timing checks for `services`, `metaapps`, `buzzes`, and `chats`.

## Non-Goals

- Do not add a cache layer to homepage aggregation.
- Do not redesign the v3 response shape.
- Do not add new public endpoints.
- Do not introduce a second parallel index family for homepage services or
  published content.
- Do not change default v1 or v2 behavior outside the shared query-parser
  semantics that v3 depends on.
- Do not change `buzzes` loading behavior in this task beyond the section-flag
  measurement support.

## Current Problem

Production verification after the `privatechat` sender-index work shows:

- `chats` is no longer the dominant cost.
- `services` remains the largest remaining section cost because homepage reads
  can still fall back to scanning the full `skillservice` catalog when
  provider-global indexes are incomplete for historical records.
- `metaapps` is smaller, but homepage aggregation still queries published
  content by `globalMetaId`, `metaId`, and `address` in sequence for every
  request, then merges and sorts the combined result even after canonical
  global identity has become the preferred long-term lookup key.
- v3 lacks a dedicated `includeChats` flag, so current production timing tests
  cannot isolate each section cleanly.

The result is that homepage latency is still higher than expected for bots with
few section records.

## Design Overview

This design keeps the work inside the existing owning modules:

- `internal/aggregator/bothomepage`
- `internal/aggregator/skillservice`
- `internal/aggregator/publishedcontent`

No homepage cache is added. Instead, the read paths are tightened so the
homepage aggregator can rely on stable indexes and avoid unnecessary alias
queries.

## Part 1: v3 Section Flag Semantics

### Intent

Make v3 section flags precise enough that each section can be enabled or
disabled independently for both behavior and measurement.

### Changes

Extend `bothomepage.Options` and the v3 parser to support:

- `includeSections`
- `includeServices`
- `includeMetaApps`
- `includeBuzzes`
- `includeChats`

### Semantics

For `version=v3` or `schemaVersion=botHomepage.v3`:

- `includeSections=false`
  - Do not build any sections.
  - Ignore the per-section booleans for execution purposes.
- `includeSections=true`
  - Build only the sections whose individual flags are `true`.
- Defaults:
  - `includeSections=true`
  - `includeServices=true`
  - `includeMetaApps=true`
  - `includeBuzzes=true`
  - `includeChats=true`

For non-v3 requests:

- Ignore `includeServices`, `includeMetaApps`, `includeBuzzes`, and
  `includeChats`.
- Do not change existing v1/v2 section behavior or error handling.

### Constraints

- Keep v1/v2 defaults unchanged except for shared parser cleanup that does not
  alter their existing response contracts.
- Preserve current validation behavior for malformed boolean query values.

## Part 2: Homepage Services Must Use Provider-Global Indexes as the Main Path

### Root Cause

`skillservice.ListHomepageByProvider` already prefers provider-global indexes,
but if the collected result count is below the requested size it can fall back
to `listAllServices()` or `listServicesByChain()`. For older records whose
provider-global homepage indexes were never written, that fallback turns a
homepage request for a handful of cards into a catalog scan.

### Design

#### 2.1 Keep the current write path

`saveService` continues to write the existing keys:

- `service_by_provider_global:*`
- `service_by_provider_global_chain:*`
- `service_by_provider_meta:*`

No new key families are introduced.

#### 2.2 Add one-time historical provider-global index backfill

At `skillservice` aggregator initialization:

1. Check a dedicated state marker key for this migration, for example:

```text
homepage_provider_global_index_state:v1
```

2. If the state key is absent:
   - scan existing `service:*` records
   - compute the canonical provider-global ids using the same logic already
     used by `homepageProviderGlobalMetaIds(rec)`
   - write any missing:
     - `service_by_provider_global:*`
     - `service_by_provider_global_chain:*`
3. Commit the batch and write the state marker.
4. On later starts, skip the full backfill when the state marker exists.

This mirrors the approach already used for the new homepage `privatechat`
sender indexes: historical repair is handled once at startup so production
requests do not need to pay that repair cost repeatedly.

Migration contract:

- The state marker means one full repair pass completed successfully for the
  local DB snapshot under the current canonicalization version. It does not
  prove that future imports, restores, or manual DB changes are automatically
  covered forever.
- The backfill must be idempotent. Re-running it must not duplicate records or
  corrupt ordering.
- Concurrent startup attempts must be safe. If two processes race, both may
  attempt repair, but the resulting indexes and state marker must still settle
  into the same valid final state.
- If canonicalization logic changes later, the state key version must bump and
  force a fresh repair pass.

#### 2.3 Tighten homepage service reads after backfill

`ListHomepageByProvider` becomes a layered lookup:

1. Read by provider-global indexes.
2. If a canonical profile exists with `providerMetaId`, read the
   provider-meta compatibility index.
3. Only allow the current full-scan fallback when:
   - the new state marker does not yet exist, or
   - the caller is still in a legacy transitional state where indexes are not
     guaranteed to be complete, or
   - the request hits an anomaly where both index paths miss records that a
     one-time correctness fallback for that provider can still discover.

Once the backfill state marker is present, the full-scan path should not be
part of steady-state homepage reads for correctly indexed records.

Legacy transitional state means one of:

- the state marker is absent
- a previous backfill attempt failed or did not finish cleanly
- the local DB was restored or imported without re-running this migration
- a request-path anomaly proves the indexes are still incomplete for the
  requested provider

### Behavior Requirements

- Stale or corrupt provider-global index entries must remain skippable.
- Duplicate entries from provider-global and provider-meta indexes must still
  dedupe to one logical service card.
- The newest service ordering must remain unchanged, using the same
  authoritative recency field and dedupe tie-break behavior as the current
  homepage service response.
- Existing compatibility for older `providerMetaId`-indexed records must be
  preserved.
- If a post-marker anomaly still requires full-scan fallback to preserve
  correctness, that path should be treated as a repair signal and logged as an
  unexpected steady-state miss. In this task, that fallback means at most one
  request-path call to the existing full-scan discovery logic for the affected
  provider, followed by normal dedupe and ordering.

## Part 3: MetaApps Should Prefer Canonical Global Identity Only After
Canonical Completeness Exists

### Root Cause

Homepage `metaapps` loading currently builds up to three published-content
queries for every request:

- `PublisherGlobalMetaId`
- `PublisherMetaId`
- `PublisherAddress`

It then merges, dedupes, and sorts the combined result. That is correct for
mixed historical data, but it means homepage requests keep paying the alias
query cost even after canonical global identity should become sufficient.

### Design

Keep `publishedcontent` key families unchanged, but establish a canonical
completeness contract before the fast path is allowed.

#### 3.1 Add one-time canonical-global repair for homepage-visible metaapps

At `publishedcontent` aggregator initialization:

1. Check a dedicated migration state marker for homepage `metaapps`, for
   example:

```text
homepage_metaapps_global_identity_state:v1
```

2. If the state key is absent:
   - scan homepage-visible `metaapps` records already stored in
     `publishedcontent`
   - compute the canonical `PublisherGlobalMetaId` using the same identity
     resolution logic as the current write path
   - populate any missing rows in the existing canonical-global
     `publishedcontent` index keys needed for homepage `metaapps` lookup
3. Commit the repair batch and write the state marker
4. On later starts, skip the repair when the state marker exists

Migration contract:

- The repair must be idempotent and safe under concurrent startup.
- The state marker means a full repair pass completed successfully for the
  local DB snapshot under the current identity-resolution version.
- If canonical identity resolution changes later, the state key version must
  bump and trigger a fresh repair pass.

#### 3.2 Use canonical-global fast path only when completeness is established

Once the `metaapps` canonical-global repair marker exists, homepage `metaapps`
loading may use this lookup strategy:

1. If canonical `globalMetaId` is present:
   - query by `PublisherGlobalMetaId` first
   - if the repair marker exists and the global-id query returns items, use
     that result directly and stop
2. Fall back to `PublisherMetaId` only if:
   - canonical `globalMetaId` is empty, or
   - the repair marker does not yet exist, or
   - the global-id query returns zero items
3. Fall back to `PublisherAddress` only if both previous identity forms produce
   no items

This avoids the incorrect rule of treating "non-empty global results" as proof
that alias queries are unnecessary. The fast path becomes valid only after the
historical canonical-global repair has established completeness for the local
homepage `metaapps` dataset.

This keeps the existing alias compatibility path for older data, but removes
the default three-query merge cost for the normal case after canonical-global
repair has completed.

### Scope

Apply this identity-preference behavior only to homepage `metaapps` in this
task. Do not change homepage `buzzes` behavior here. If the shared loader later
needs the same optimization, that should be proposed as a separate follow-up
with its own correctness contract.

## Testing Strategy

Follow test-first development for each behavior change.

### Required failing tests first

1. `bothomepage`
   - v3 `includeSections=false` omits all sections
   - v3 `includeChats=false` omits only chats while leaving other enabled
     sections intact
   - v3 per-section flags build only the explicitly enabled sections

2. `skillservice`
   - init backfills missing provider-global homepage indexes for historical
     records
   - once the backfill state exists, homepage provider reads succeed without
     needing the full-catalog fallback to discover those records
   - provider-meta compatibility still works for older alias-indexed records
   - stale or corrupt homepage provider index entries remain safely ignored

3. `publishedcontent` / `bothomepage`
   - init repair backfills missing canonical-global homepage `metaapps`
     indexes for historical records
   - homepage published-content loading prefers canonical `globalMetaId`
   - `metaId` and `address` fallback queries are only skipped after the repair
     marker establishes canonical completeness
   - merged ordering remains correct when fallback is actually needed
   - mixed-history data does not drop visible records when canonical-global
     results are non-empty but incomplete before repair
   - post-repair canonical-global fast path returns the same visible `metaapps`
     slice as the old alias-merge path for equivalent data

### Verification commands

Minimum local verification:

```text
CGO_ENABLED=0 go test ./internal/aggregator/skillservice ./internal/aggregator/publishedcontent ./internal/aggregator/bothomepage ./internal/api
```

If implementation touches shared helpers more broadly, extend verification to
the affected package set before deployment.

## Deployment and Production Verification

After merge-ready implementation:

1. Deploy the updated `metaso-p2p` binary to production.
2. Restart `metaso-p2p.service` so the one-time `skillservice` and
   `publishedcontent` startup repairs can run.
3. Verify:
   - `https://so.metaid.io/healthz`
   - `https://socket.metaid.io/healthz`
4. Re-measure:
   - full v3:
     - `/api/bot-homepage/globalmetaid/:globalMetaId?version=v3`
   - `services` only:
     - `/api/bot-homepage/globalmetaid/:globalMetaId?version=v3&includeSections=true&includeServices=true&includeMetaApps=false&includeBuzzes=false&includeChats=false`
   - `metaapps` only:
     - `/api/bot-homepage/globalmetaid/:globalMetaId?version=v3&includeSections=true&includeServices=false&includeMetaApps=true&includeBuzzes=false&includeChats=false`
   - `chats` only:
     - `/api/bot-homepage/globalmetaid/:globalMetaId?version=v3&includeSections=true&includeServices=false&includeMetaApps=false&includeBuzzes=false&includeChats=true`

Success means:

- `services` no longer behaves like a catalog scan for this bot
- `metaapps` no longer pays the default three-identity merge path when the
  canonical-global repair marker exists and the canonical global id resolves
  the section
- `processingTime` remains an elapsed duration, not a timestamp. This work must
  not regress the earlier response-timing fix.

## Risks and Guardrails

- Startup backfill increases initialization cost once per deployed state
  version. This is acceptable because it moves the cost off the request path.
- Provider-global index repair must use the same canonicalization logic as the
  existing write path, otherwise steady-state reads and backfilled records can
  diverge.
- The published-content canonical-global fast path must only activate after the
  dedicated repair marker establishes completeness for homepage `metaapps`;
  otherwise old visible records could disappear.
- Section-flag semantics must stay easy to reason about. `includeSections`
  remains the master switch; per-section flags only matter when sections are
  enabled.

## Out of Scope Follow-Ups

If full v3 remains slower than expected after this work lands, the next
investigation should focus on:

- `buzzes` query cost under published-content
- remaining cross-module profile resolution overhead inside `services`
- whether a later dedicated homepage cache is justified by real steady-state
  traffic, rather than by missing or weak indexes
