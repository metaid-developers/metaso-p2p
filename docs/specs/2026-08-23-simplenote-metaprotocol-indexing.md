# SimpleNote and Metaprotocol Indexing

## Background

Two knowledge-bearing protocols are not indexed by metaso-p2p today: `/protocols/simplenote` (long-form markdown notes — the primary corpus for IDBots "bots learn from MetaWeb") and `/protocols/metaprotocol` (protocol descriptors). Both must become searchable via the unified search API ([`2026-08-23-metaweb-search-api.md`](2026-08-23-metaweb-search-api.md)) and fetchable via the generic pin-read API ([`2026-08-23-metaweb-pin-read-api.md`](2026-08-23-metaweb-pin-read-api.md)). Requirements source: IDBots `docs/metaweb-search-backend-requirements.md` (R2, plus the metaprotocol open question).

Both protocols are carried by the existing **`publishedcontent`** aggregator's generic record pipeline — the same one that already indexes `/protocols/simplebuzz`, `/protocols/metaapp`, and `/protocols/metabot-skill`. No new Pebble namespace and no new record type is introduced.

## On-Chain Payload Shape (observed)

SimpleNote create pin, `path=/protocols/simplenote`, `contentType=application/json`:

```json
{
  "title": "MetaID / Agent Internet 生态理解",
  "subtitle": "WuFenGBot 认知快照 v1 · 2026-08-09",
  "contentType": "text/markdown",
  "content": "# MetaID / Agent Internet 生态理解\n\n…",
  "tags": ["metaid", "agent-internet"]
}
```

- `title`, `subtitle`, `content` are strings; `tags` is an optional string array; payload-level `contentType` (e.g. `text/markdown`) describes `content`.
- Modify/revoke follow the standard MetaID convention: operation `modify`/`revoke` with `originalId` (or a `@<pinId>` path suffix) pointing at the previous version — the same semantics `publishedcontent` already handles for simplebuzz/metaapp via `processModify` / `processRevoke` and the `pin_to_source` chain-walk.

Metaprotocol create pin, `path=/protocols/metaprotocol`, `contentType=application/json`: descriptor object with (best-effort) `title` / `protocolName` / `intro` fields. Indexed generically; no special semantics in v1.

## Changes to `publishedcontent`

1. Add path constants `PathSimpleNote = "/protocols/simplenote"` and `PathMetaProtocol = "/protocols/metaprotocol"` to `publishedProtocolPaths` (`process.go`).
2. From then on, live indexing (block + mempool), modify/revoke version chains, and hidden-state handling work unchanged — the pipeline is protocol-agnostic over `Record`.
3. Add the two paths to the default backfill path list (`backfill.go`).
4. Expose the warm **search-document snapshot** consumed by the `metawebsearch` aggregator (field derivations per the search spec's extraction table), plus a **record lookup by any pin id in a version chain** (`loadRecordByAnyPinId`, already used by `/api/metaapp/detail/:pinId`) generalized for the pin-read API across all published protocol paths.

No changes to existing publishedcontent APIs (`/api/metaapp/*`) or storage keys; existing records are untouched.

## Historical Backfill

New offline command **`cmd/metaso-p2p-metaweb-backfill`**, following the established `cmd/metaso-p2p-*-backfill` skeleton (open Pebble directly, construct the aggregator, replay MANAPI pins through the same handlers as live indexing — idempotent, safe to re-run):

```
go run ./cmd/metaso-p2p-metaweb-backfill \
  --data-dir <pebble dir> \
  --manapi-base-url https://manapi.metaid.io \
  --paths /protocols/simplenote,/protocols/metaprotocol \
  --since 2025-08-01 \
  --page-size 100 \
  --timeout 8h
```

- `--paths` defaults to the two new protocol paths; `--since`/`--lookback`, `--page-size`, `--timeout`, `--manapi-base-url` mirror the skillservice/socialcontent commands (MANAPI default `https://manapi.metaid.io`, page size 100).
- Fetch: `GET {base}/pin/path/list?path=&cursor=&size=`, cursor loop with repeated-cursor guard and 3× backoff retry, version chains re-fetched via `modify_history` (`@<pinId>` paths) and replayed oldest-first — reusing the existing `publishedcontent.Backfill` machinery.
- **Completion report** (new vs. the older commands, required by the IDBots contract): on completion the command logs and prints a per-chain table — pins fetched, records created/updated, version-chain replays, errors — grouped by `chainName` (`btc`, `mvc`, `doge`, `opcat`), e.g. `[metaweb-backfill] done path=/protocols/simplenote chain=mvc fetched=5123 upserted=5119 modified=402 revoked=17 errors=0`.

## Freshness

New SimpleNote/metaprotocol pins become searchable within one confirmed block plus mempool relay: the block engine already routes every scanned pin to `publishedcontent.HandleBlockPin` / `HandleMempoolPin`; adding the path constants is the only switch. Mempool versions appear in search immediately with their seen-time as `createdAt` fallback, and are superseded by the confirmed version per the existing merge logic.

## Acceptance

- After backfill, `GET /api/metaweb/search?q=<keyword>&protocols=simplenote` returns SimpleNote documents with correct title/summary/tags.
- Modify a note on-chain → `currentPinId` advances, old versions stop appearing in search; revoke → document disappears from search and pin-read returns the record with `operation: "revoke"` / hidden semantics as documented in the pin-read spec.
- Backfill completion report shows per-chain counts; re-running the command is a no-op diff (idempotent).

## Explicitly Out of Scope (v1)

- A dedicated `/api/simplenote/list` endpoint (unified search + pin-read fully cover SimpleNote for clients).
- Full-content storage beyond the generic `Record.PayloadJSON`/`PayloadText` capture (payloads are already stored whole).
- `/file/*` markdown pins and other phase-2 protocols.
