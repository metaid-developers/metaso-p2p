# MetaWeb On-chain Q&A v2: Comment Threads and Author Listing

## Background

IDBots' human-facing Q&A MetaApp needs two capabilities the deployed v1 contract ([`2026-09-07-metaweb-qa-api.md`](2026-09-07-metaweb-qa-api.md)) deliberately left out: readable comment threads on Q&A pins (v1 stores a bare `commentCount`; the social comments endpoint is simplebuzz-only, so comment content on Q&A pins is unreadable anywhere) and cross-question answer listing (author pages / newest-answers feeds; v1 lists answers per question only).

Requirements source: IDBots `docs/metaweb-qa-backend-requirements-v2.md` (R5–R6). Everything here is additive — no change to any existing endpoint or item shape. Conventions (envelope, opaque cursors, error codes `40000/40400/50000`, no auth, permissive CORS, block time authoritative, mempool freshness) carry over from v1 unchanged.

Non-goals (per the requirements): write APIs, comment threading (flat comment lists only — replies to comments are not indexed), like/reaction lists on comments, accepted-answer semantics, moderation.

## Decisions on the Requirements' Open Questions (§6)

1. **Comment content cap**: 2000 runes at index time, as suggested. Longer bodies are truncated when the comment record is built; the wire always returns the stored (capped) body.
2. **Question detail stays comment-free**: `GET /api/qa/questions/:pinId` does not embed comments; R5 is strictly separate (the question page already embeds answers; comments load lazily in the UI).
3. **R6 `sort=top`**: paged scan over the answer time index with in-memory score sort in v1 (same shape as the v1 hot feed). Per-publisher precomputed ranking is deferred until a scan measurably degrades; the wire format will not change when that happens.
4. **Path shape**: `/api/qa/pins/:pinId/comments` is adopted as proposed — it intentionally serves both questions and answers.

## R5 — Comment Threads

### Indexing (R5.1)

PayComment pins whose `commentTo` resolves (via the qa pin map, any version) to a Q&A-indexed pin — question or answer — are stored as full comment records in the `qa` namespace:

- `pinId` / `currentPinId`: source and newest version of the comment pin.
- `targetPinId`: the resolved **stable source pin id** of the target question/answer (any version of the target normalises to it).
- `content`: the full payload body (markdown), capped at **2000 runes** at index time.
- `contentType`: the payload's `contentType` when present.
- Publisher identity, chain, block/relay timestamp — same extraction rules as questions/answers.

Lifecycle rules mirror the content protocols:

- A `create` whose `commentTo` does not resolve to a Q&A pin is ignored (simplebuzz comments stay with socialcontent; this endpoint never serves them). Comments targeting another **comment** are ignored too — flat lists only in this round.
- `modify` updates content in place (version-target resolution via `@<pinId>` suffix or `originalId`, same as questions/answers); `revoke` hides the comment — it leaves comment lists and stops counting in the target's `commentCount`.
- Mempool comments are indexed immediately (`isMempool: true`) and replaced when the confirmed pin arrives.
- `commentCount` on questions and answers becomes the count of **non-hidden** comment records targeting the pin (v1 counted bare markers; the qa backfill replays `/protocols/paycomment` so existing counts converge).

### Endpoint (R5.2)

`GET /api/qa/pins/:pinId/comments` — idempotent, no auth. `pinId` may be any version of a question, or any answer pin.

| Param | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `sort` | `newest` \| `oldest` | No | `newest` | `newest` = createdAt desc, tie pinId asc; `oldest` = the exact reverse (chronological reading) |
| `size` | int | No | `20` | 1–50; non-numeric or `< 1` → `40000`, `> 50` clamps to `50` |
| `cursor` | string | No | — | Opaque offset cursor; invalid → `40000` |

- Malformed `pinId` (not `<64 hex>i<n>`) → `40000`. Target not Q&A-indexed, or hidden (revoked question, revoked answer, or an answer whose question is revoked) → `40400`.
- `data` shape: `{items: [comment item], nextCursor, hasMore}`.

Comment item:

```json
{
  "protocol": "paycomment",
  "pinId": "<txid>i0",
  "currentPinId": "<txid>i0",
  "targetPinId": "<txid>i0",
  "chainName": "mvc",
  "content": "Full comment body (markdown, capped at 2000 runes at index time).",
  "contentType": "text/markdown",
  "publisher": { "globalMetaId": "…", "metaid": "…", "name": "…" },
  "createdAt": 1755000000,
  "isMempool": false
}
```

- `content` is the full stored body — this endpoint is the only place comments are readable, so no summary is derived.
- `name` is best-effort userinfo enrichment of the returned page only.
- Likes on comment pins remain out of scope (v1's "likes target only Q&A pins" rule now means questions and answers, not comments).

## R6 — Cross-question Answer Listing

### `GET /api/qa/answers` (R6.1)

Idempotent, no auth.

| Param | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `publisher` | string | No | — | Filter by answer publisher `globalMetaId` **or** `metaid`, case-insensitive exact match (same semantics as the per-question filter). Omitted = all publishers ("newest answers on MetaWeb" feed) |
| `sort` | `newest` \| `top` | No | `newest` | `newest` = createdAt desc, tie pinId asc. `top` = `score` desc (likeCount − dislikeCount), tie newer first, then pinId asc — the "best answers" tab of an author page |
| `size` | int | No | `10` | 1–50, clamped as usual |
| `cursor` | string | No | — | Opaque offset cursor; invalid → `40000` |

- Answers to hidden (revoked) questions are excluded, and hidden answers are excluded, matching the v1 visibility rules; orphan (unresolved) answers cannot appear.
- `data` shape: `{items: [answer item], nextCursor, hasMore}`.

Answer item: **the v1 answer item shape unchanged, plus an embedded parent question**:

```json
{
  "protocol": "simpleanswer",
  "pinId": "<txid>i0",
  "currentPinId": "<txid>i0",
  "questionPinId": "<txid>i0",
  "question": { "pinId": "<txid>i0", "title": "How to recover a wallet…", "chainName": "mvc", "createdAt": 1755000000 },
  "chainName": "mvc",
  "summary": "First ~200 runes of the answer…",
  "tags": ["wallet"],
  "publisher": { "globalMetaId": "…", "metaid": "…", "name": "…" },
  "createdAt": 1755000100,
  "isMempool": false,
  "likeCount": 5, "dislikeCount": 1, "commentCount": 0,
  "score": 4
}
```

`question` is a light embed (identity of the parent question: `pinId`, `title`, `chainName`, `createdAt`). It appears on this endpoint only — existing per-question surfaces are unchanged. The embedded question carries the same visibility guarantee as the row itself: a row is never served with a hidden parent.

### Author questions feed (R6.2)

`GET /api/qa/questions` gains an optional `publisher` param (globalMetaId or `metaid`, case-insensitive exact match, same semantics as everywhere else), combinable with the existing `tags` / `minAnswers` / `maxAnswers` / `sort` params. No shape change.

With R6.1 + R6.2 an author page is fully server-supported: questions tab `/api/qa/questions?publisher=`, answers tab `/api/qa/answers?publisher=`, best-answers tab `/api/qa/answers?publisher=&sort=top`.

## Indexing Internals (v2 additions)

- `c:<chain>:<commentSourcePinId>` comment record; `ct:<targetSrcPinId>:<invTs>:<commentSrcPinId>` per-target comment list (newest first); `atime:<invTs>:<chain>:<answerSrcPinId>` global visible-answer list (newest first). Comment pins map into the shared pin map as kind `c` (never a valid `commentTo`/`likeTo` target for this aggregator).
- The answer time index carries exactly the answers that are non-hidden, question-resolved, and whose question is non-hidden; it is maintained on both the answer write path and the question visibility path (a revoked question removes all its answers from the index).
- Mempool→confirmed replacement can change a pin's `createdAt` (relay time → block time); all time-ordered index entries now re-key on that transition so no surface ever serves a duplicate row (this also hardens the pre-existing v1 question/answer index maintenance).

## Errors

| code | when |
| --- | --- |
| `40000` | Unknown `sort`; non-numeric/`< 1` size; malformed `pinId`; invalid cursor |
| `40400` | `:pinId` resolves to no indexed Q&A pin, or the target (or its parent question) is hidden |
| `50000` | Pebble/store failure while serving the request |

## Backfill

`metaso-p2p-qa-backfill` already replays `/protocols/paycomment` through the qa aggregator; with v2 the same replay materialises comment records and comment lists (idempotent, safe to re-run). The completion report keeps its per-path/per-chain counters. A production rollout runs the backfill once after deploy so historical comments become readable and `commentCount` converges to the non-hidden-record semantics.

## Performance and Freshness

- Both endpoints p95 < 300 ms at the projected 12-month corpus (reverse-time index scans; `sort=top` sorts the scanned page set in memory, v1 hot-feed shape).
- Comments and answers visible within one mempool relay; confirmed pins replace mempool records.
