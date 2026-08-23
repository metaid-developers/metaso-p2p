# MetaWeb Unified Search API

## Background

IDBots is building "bots learn from MetaWeb": a bot issues one keyword search across all knowledge-bearing MetaWeb protocols, gets 5–10 candidates (`title`/`summary`/`pinId`), then opens chosen pins for full content via the generic pin-read API ([`2026-08-23-metaweb-pin-read-api.md`](2026-08-23-metaweb-pin-read-api.md)). Today metaso-p2p only offers per-protocol keyword search (`/api/social/feed`, `/api/metaapp/list`, `/api/bot-hub/skill-service/list`, `/api/metaid/list`). This spec adds `GET /api/metaweb/search`, a read-only cross-protocol search over data the node already indexes (plus SimpleNote / metaprotocol, newly indexed per [`2026-08-23-simplenote-metaprotocol-indexing.md`](2026-08-23-simplenote-metaprotocol-indexing.md)).

Requirements source: IDBots `docs/metaweb-search-backend-requirements.md` (R1). The API style follows the established aggregation conventions: `{code, data, message, processingTime}` envelope, HTTP status always 200, opaque base64url(JSON) cursor, no auth, permissive CORS.

## General Principles

- The search aggregator is **read-only**: it indexes nothing itself and owns no Pebble data. Searchable documents are injected from source aggregators via setters, mirroring the `botsearch` composition pattern (`internal/aggregator/botsearch`).
- Each source aggregator maintains a warm in-memory **search-document snapshot** (derived fields only: title/summary/tags/content excerpt), built from its Pebble store at startup and updated on every block/mempool pin it already processes. `SearchDocuments()` returns a **shared, immutable snapshot** (copy-on-write: the source swaps in a new slice/map on update; callers hold the previous reference). Callers must not mutate the returned documents. There is no per-request deep copy of the corpus — scoring reads the shared snapshot in place.
- Matching is **weighted partial match** with CJK-aware tokenization (same rules as `botsearch`), not hard AND.
- Hidden/revoked records are excluded. Mempool versions are included (freshness: searchable within one confirmed block + mempool relay).
- v1 covers six protocol keys; the list is extensible without wire-format changes.

## API: MetaWeb Unified Search

### Endpoint

`GET /api/metaweb/search`

Idempotent. No auth.

### Request Parameters

| Param | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `q` | string | Yes | — | Keyword query; CJK-aware tokenization (below). Empty/whitespace after trim → `40000` |
| `protocols` | CSV string | No | all indexed | Filter by protocol key, e.g. `simplenote,simplebuzz`. Unknown key → `40000` |
| `publisher` | string | No | — | Filter by publisher `globalMetaId` **or** `metaid`, case-insensitive exact match |
| `since` | unix seconds | No | — | `createdAt >= since`. Non-numeric → `40000` |
| `until` | unix seconds | No | — | `createdAt <= until`. Non-numeric → `40000`. `since > until` → `40000` |
| `sort` | `relevance` \| `newest` | No | `relevance` | `newest` = `createdAt` desc, scoring bypassed (`score` reported as `0`) |
| `size` | int | No | `10` | Page size. Non-numeric or `< 1` → `40000`; `> 50` clamps to `50` (same convention as the skill-service list) |
| `cursor` | string | No | — | Opaque cursor from a previous `nextCursor`; invalid → `40000` |

### Protocol Keys

| Key | Protocol path | Source aggregator |
| --- | --- | --- |
| `simplenote` | `/protocols/simplenote` | publishedcontent (new, phase 1) |
| `simplebuzz` | `/protocols/simplebuzz` | publishedcontent |
| `metaapp` | `/protocols/metaapp` | publishedcontent |
| `metabot-skill` | `/protocols/metabot-skill` | publishedcontent |
| `skill-service` | `/protocols/skill-service` | skillservice |
| `metaprotocol` | `/protocols/metaprotocol` | publishedcontent (new, phase 1) |

Phase 2 (not in this contract, may lag): `/file/remote-skill` and other markdown `/file` pins, `/protocols/loom-task`, `/protocols/skill-service-rate`.

### Tokenization

Whitespace-split `q`, then per segment (identical to `botsearch`):

- If the segment contains CJK runes (Han, Hiragana, Katakana, Hangul): emit the whole segment (lowercased) **and** every CJK bigram within each maximal run of consecutive CJK runes; a single-CJK-char run yields that char.
- Otherwise (latin/digit): emit the lowercased segment as one whole-word token.

The token set is deduplicated, preserving first-occurrence order.

**Stopword exclusion** (ranking hardening, 2026-08-23): latin/digit tokens that are English function words (stopword table in `internal/aggregator/metaweb/stopwords.go` — `is`, `what`, `the`, `of`, …) are removed from the scoring token set. CJK tokens are never stopwords. If every token is a stopword (e.g. `q="what is"`), the result set is empty (`code 0`, `items: []`) for both `sort=relevance` and `sort=newest`.

### Search Document Fields

Each indexed record projects to one search document (derived **at index time**, i.e. when the snapshot entry is built/updated — see "Open-Question Decisions"):

| Field | Derivation |
| --- | --- |
| `title` | Per-protocol extraction table below |
| `summary` | Per-protocol extraction table below; max ~200 runes |
| `tags` | Per-protocol extraction table below |
| `content` | Plain-text/markdown body excerpt, capped at 1024 runes, used only for weight-1 matching; never returned in the response |
| `publisherGlobalMetaId` / `publisherMetaId` | From the record |
| `createdAt` | Source pin `createdAt` (unix seconds); preserved across modify versions |
| `extra` | Protocol-specific highlights (below) |

Title/summary/tags extraction (markdown-stripped where noted; "markdown-stripped" = heading/emphasis markers removed, links/images reduced to their text):

| Protocol | title | summary | tags |
| --- | --- | --- | --- |
| simplenote | `title` | `subtitle`, else first ~200 runes of markdown-stripped `content` | `tags` (payload array, else `[]`) |
| simplebuzz | First line/heading of body, ≤60 runes, markdown-stripped | First ~200 runes of body after the title line, markdown-stripped | `[]` |
| metaapp | `appName` \|\| `title` | `intro` | `tags` |
| metabot-skill | `name` | `description` | `[]` |
| skill-service | `displayName` \|\| `serviceName` | `description` | `[providerSkill]` (when non-empty) |
| metaprotocol | `title` \|\| `protocolName` | `intro` | `[]` |

`extra` per protocol (may be empty object):

| Protocol | extra |
| --- | --- |
| metaapp | `{runtime, version, icon}` |
| skill-service | `{price, currency, providerSkill, settlementKind}` |
| metabot-skill | `{version}` |
| simplenote | `{contentType}` (payload-declared content type, e.g. `text/markdown`) |
| simplebuzz, metaprotocol | `{}` |

### Scoring

```
score = 5*titleHit + 3*tagsHit + 2*summaryHit + 1*contentHit
```

- Each hit sums token weights; token weight = `min(runeCount(token), 4)`.
- Each (field, token) pair is counted once. `tagsHit`: a token hitting any tag counts once.
- **Hit test** (ranking hardening, 2026-08-23): latin/digit tokens hit a field only on word boundaries — bounded by non-`[a-z0-9]` runes or string boundaries (case-insensitive; e.g. `is` does not hit `this`/`history`). CJK tokens and bigrams keep plain case-insensitive substring matching. The same hit test applies in the `sort=newest` admission filter.
- **Stopwords score zero** (2026-08-23): stopword tokens (see Tokenization) are excluded from scoring; a document scores only through non-stopword tokens and the exact-phrase boost.
- **IDF-lite** (2026-08-23): per request, the document frequency of each scoring token is counted over the merged snapshot (all sources, unfiltered; hit test over title+tags+summary+content). If `df[token]/totalDocs > 0.30`, that token's weight is multiplied by `0.5` — halved, not zeroed, so a direct query for a ubiquitous term (e.g. `metaid`) still works. In-memory only, two-pass scoring (one corpus scan for df, then the filtered scoring pass).
- **Exact-phrase boost**: when the whole trimmed `q` (case-insensitive) is a substring of `title`, a flat `+100`; when it is a substring of `summary`, any tag, or `content`, a flat `+10` (at most one +10 per document). Requires at least one non-stopword token in `q` — an all-stopword query never scores.
- Documents with score 0 are excluded.
- `sort=relevance`: score descending, tie-break `createdAt` descending (recency tiebreak), then `pinId` ascending. `sort=newest`: `createdAt` descending, then `pinId` ascending; `score` is reported as `0`.

### Pagination

Offset cursor: base64url(JSON `{"o": offset}`) — the same wire format as the MetaApp / MetaID / skill-service lists. Empty cursor → offset 0; base64/JSON/negative-offset failure → `40000`. Offset past the result end yields an empty page with `hasMore: false`. `nextCursor` is `null` on the last page.

### Response

```json
{
  "code": 0,
  "message": "",
  "data": {
    "items": [
      {
        "protocol": "simplenote",
        "pinId": "92ec…fb4i0",
        "currentPinId": "92ec…fb4i0",
        "chainName": "mvc",
        "title": "IDBots Beginner Tutorial",
        "summary": "One-line abstract of the article…",
        "tags": ["idbots", "tutorial", "zh"],
        "publisher": {
          "globalMetaId": "idq1…",
          "metaid": "…",
          "name": "WuFenGBot",
          "avatar": "metafile://…i0"
        },
        "createdAt": 1755000000,
        "score": 14,
        "links": { "pin": "/api/metaweb/pin/92ec…fb4i0" },
        "extra": { "contentType": "text/markdown" }
      }
    ],
    "nextCursor": "eyJvIjoyMH0",
    "hasMore": true
  },
  "processingTime": 37
}
```

- `pinId` is the **source** pin id of the record; `currentPinId` is the latest version in its modify chain (equal when never modified).
- `links.pin` always points at `currentPinId` so the caller opens the latest version via the pin-read API.
- `publisher.name` / `publisher.avatar` are best-effort enrichment from the userinfo aggregator (warm profile cache); empty strings when the profile is unknown. `publisher.avatar` stays a raw `metafile://` URI in v1 (list clients already handle it; the pin-read API resolves attachment URLs server-side instead).
- `createdAt` is unix seconds. `tags` is always an array (never `null`).
- `hasMore` mirrors whether `nextCursor` is non-null.

### Error Codes

| code | Meaning |
| --- | --- |
| 0 | OK |
| 40000 | Invalid parameter or cursor (empty `q`, unknown protocol key, malformed `since`/`until`, `since > until`, bad `size`, invalid cursor) |
| 50000 | Search unavailable (a required document source not wired) |

HTTP status is always 200. Error envelopes carry no `data` field (standard `api.RespErr`).

## Performance and Observability

- Target: search p95 < 500 ms at the current corpus (order of 10⁵–10⁶ pins per protocol worst case; realistic phase-1 corpus is far smaller — measured after backfill).
- `processingTime` (milliseconds) is populated on every success response by the standard timing middleware.
- **Slow-query log**: the handler logs a warn line when elapsed > 300 ms, including `q`, active filters, result count, and elapsed ms.
- Measured numbers:

| Corpus (searchable docs) | p50 | p95 | Note |
| --- | --- | --- | --- |
| 115 docs (86 simplenote + 29 metaprotocol, local dev node after full-history backfill) | ~1 ms | ~2 ms | `processingTime` observed on single queries; staging numbers to be re-measured after production backfill |

## Open-Question Decisions

Answers to §7 of the requirements doc:

1. **Inverted index: later.** v1 scores over an in-memory search-document snapshot with substring matching — the same approach `botsearch` uses for the profile corpus, with bounded per-doc memory (summary ≤ 200 runes, content excerpt ≤ 1024 runes). Trigger to build a dedicated Pebble inverted index: any of (a) search p95 > 400 ms sustained over 24 h, (b) searchable documents > 300 k, (c) snapshot heap contribution > 512 MB.
2. **Summary at index time.** Derived when the snapshot entry is built/updated. Consistent between list and detail, zero per-query derivation cost, and the extraction rules live in exactly one place shared with the pin-read API.
3. **Parallel coexistence.** `/api/metaweb/search` is an additional surface; the per-protocol list endpoints (`/api/social/feed`, `/api/metaapp/list`, `/api/bot-hub/skill-service/list`, `/api/metaid/list`) keep their `keyword` params unchanged — IDBots' specialized UIs keep using them. Revisit convergence only after M1 usage data.
4. **Path family `/api/metaweb/*`: accepted.** No conflicts with existing routes; the family has room to grow (`/api/metaweb/search`, `/api/metaweb/pin/:pinId`, later `/api/metaweb/…`).
5. **metaprotocol stays in phase 1.** It rides the generic `publishedcontent` indexing (one path constant + one backfill path — see the indexing spec), so coverage is near-free. No special payload semantics in v1: a metaprotocol pin is searchable by its `title`/`protocolName`/`intro` like any other document.

## Implementation Notes

- New package `internal/aggregator/metawebsearch` (read-only; `HandleBlockPin`/`HandleMempoolPin` are no-ops). Route mounted as `GET /api/metaweb/search` via the standard `RegisterRoutes(router.Group("/api"))`.
- Consumer-side dependency interfaces declared in the package: `DocumentSource.SearchDocuments() []SearchDocument` (one implementation each from `publishedcontent` and `skillservice`) and `ProfileNamer` (userinfo lookup for `publisher.name`/`avatar` enrichment of the returned page only). Injected via `Set…` setters in `cmd/metaso-p2p/main.go`, WARNING-and-continue on init failure like the other aggregators.
- `publishedcontent` builds snapshot entries from its existing generic `Record` store (covers simplenote/simplebuzz/metaapp/metabot-skill/metaprotocol); `skillservice` builds them from its service records. Snapshot entries are keyed by source pin id and replaced on modify/revoke/mempool events the source aggregator already handles.
- Scoring runs over one merged snapshot per request; filters apply before scoring; publisher/profile enrichment happens only for the returned page.
- Cursor, size clamping, envelope, and timing reuse the conventions cited above; no new shared helpers are introduced.

## Explicitly Out of Scope (v1)

- Semantic/vector search; encrypted group/private-chat content; any write API; auth/rate limiting; reputation-based ranking.
- Phase-2 protocol keys (`/file/*`, `loom-task`, `skill-service-rate`).
- Server-side resolution of `publisher.avatar` metafile URIs in list responses (pin-read resolves attachments; list keeps raw URIs).
