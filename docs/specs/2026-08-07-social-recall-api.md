# Social Recall API (Agent Downstream Contract)

Date: 2026-08-07
Status: Draft for review

## 1. Purpose

This document defines the stable downstream contract that MetaSo exposes to an
Agent host for social-content recall. The Agent host converts a user's natural
language into structured queries and selects the final candidates; MetaSo only
returns a broad, structured candidate set.

The contract is the Stage 2 completion surface of
`docs/specs/2026-08-06-social-content-aggregation.md` and is intentionally
kept free of ranking, recommendation, and semantic behavior.

## 2. Scope

In scope for v1:

- simplebuzz posts with create/modify/revoke folding;
- paylike like/unlike events folded into per-post counters;
- paycomment comments attached to their target post;
- newest, author, time-window, keyword, and recent-hot retrieval;
- post detail and comment retrieval;
- deterministic, opaque pagination.

Out of scope for v1 (explicit non-goals):

- natural-language intent parsing;
- relevance ranking, personalization, or recommendation;
- semantic/vector search or embeddings;
- follow-graph-based filtering;
- protocol write APIs;
- donation counters (reserved field, always zero until the adapter is
  verified and enabled).

## 3. Data state

The aggregated dataset is complete through the most recently completed
historical backfill and continues to receive newly confirmed pins through the
live indexer. As of 2026-08-07 the production dataset contains roughly 1.24
million posts and their likes/comments through 2026-08-07 13:50 CST.

Freshness caveats that downstream callers must accept:

- MetaSo can only index what MANAPI serves. MANAPI's path-list index has shown
  historical stalls, so "complete" means "complete as of the source snapshot".
- Backfills are long-running full cursor scans. The service-embedded backfill
  is unreliable in the current production environment; the supported path is
  the standalone `metaso-p2p-socialcontent-backfill` CLI against a stopped or
  staged data directory.
- Candidates returned by this API are a recall set. The Agent host owns final
  selection and user-facing presentation.

## 4. Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/social/feed` | Candidate post retrieval |
| GET | `/api/social/post/:pinId` | Post detail by any version PIN |
| GET | `/api/social/post/:pinId/comments` | Comments for a post |

All endpoints are public, read-only, and return HTTP 200 with a JSON envelope;
errors are carried in the envelope body.

## 5. Response envelope

Success:

```json
{
  "code": 0,
  "message": "",
  "data": { ... },
  "processingTime": 3
}
```

Error (HTTP 200, no `data` field):

```json
{
  "code": 40000,
  "message": "invalid size"
}
```

## 6. GET /api/social/feed

### 6.1 Query parameters

| Parameter | Type | Default | Limits | Description |
| --- | --- | --- | --- | --- |
| `size` | int | 20 | 1..100 | Items per page |
| `cursor` | string | - | - | Opaque continuation cursor from the previous response |
| `sort` | string | `newest` | `newest`, `hot` | Ordering |
| `publisher` | string | - | - | GlobalMetaID, MetaID, or address of the post author |
| `since` | int64 | - | unix seconds | Inclusive `createdAt` lower bound |
| `until` | int64 | - | unix seconds | Inclusive `createdAt` upper bound |
| `keyword` | string | - | - | Case-insensitive substring over post text |
| `chainName` | string | - | - | Restrict to one chain (e.g. `mvc`) |
| `protocol` | string | - | `simplebuzz` | Protocol path; only simplebuzz is supported in v1 |

Constraints:

- `since` and `until` must satisfy `since <= until` when both are present.
- `size` outside `1..100` is rejected.
- `sort`, `protocol`, and malformed `since`/`until`/`cursor` are rejected.
- All filters combine with AND semantics.

### 6.2 Ordering semantics

- `newest` (default): the global newest-first index. The scan is bounded and
  stops after one page plus a has-more probe, so latency is independent of the
  total dataset size.
- `hot`: top-N within a fixed recent window of the last 48 hours, ranked by
  the documented hot score (`engagement / (ageHours + 2)^1.5`, engagement =
  likes + 2*comments + 3*donations, ageHours floored at 1). Older posts are
  never returned by `hot`. The result is a top-N snapshot with no pagination.

### 6.3 Response data

```json
{
  "items": [
    {
      "pinId": "...",
      "sourcePinId": "...",
      "currentPinId": "...",
      "chainName": "mvc",
      "protocolPath": "/protocols/simplebuzz",
      "author": {
        "globalMetaId": "...",
        "metaId": "...",
        "address": "..."
      },
      "contentType": "application/json;utf-8",
      "payload": {
        "content": "...",
        "contentType": "text/plain;utf-8",
        "attachments": ["metafile://..."]
      },
      "createdAt": 1786081811,
      "updatedAt": 1786081811,
      "likeCount": 2,
      "commentCount": 0,
      "donateCount": 0,
      "hotScore": 0.0335
    }
  ],
  "nextCursor": "k:...",
  "hasMore": true
}
```

Notes:

- `payload` is the normalized post payload (object or raw text).
- `hotScore` is present only for `sort=hot`; zero scores are omitted.
- `nextCursor` is opaque, key-based, and only present for `newest` pages with
  more results. It must be passed back unchanged.

## 7. GET /api/social/post/:pinId

Returns the canonical post for any PIN in its version chain (create, modify,
or revoke PIN).

Query parameters:

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `chainName` | string | auto-resolved | Pass it when known for the fast path |

Response `data` is a single post item with the same shape as feed items.
Missing or hidden posts return `40400`.

## 8. GET /api/social/post/:pinId/comments

Returns comments attached to the canonical target post.

Query parameters:

| Parameter | Type | Default | Limits | Description |
| --- | --- | --- | --- | --- |
| `size` | int | 20 | 1..100 | Items per page |
| `cursor` | string | - | - | Offset-based opaque cursor |
| `chainName` | string | auto-resolved | - | Pass it when known for the fast path |

Response `data`:

```json
{
  "items": [
    {
      "pinId": "...",
      "chainName": "mvc",
      "targetPinId": "...",
      "authorGlobalMetaId": "...",
      "authorMetaId": "...",
      "authorAddress": "...",
      "content": "...",
      "contentType": "text/plain",
      "timestamp": 1772292925
    }
  ],
  "nextCursor": "...",
  "hasMore": true
}
```

## 9. Error codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `40000` | Invalid parameter or invalid cursor |
| `40400` | Post not found |
| `50000` | Aggregation unavailable (storage/index failure) |

All errors return HTTP 200 with the envelope `{code, message}` and no `data`.

## 10. Recommended downstream usage

- Request `size=20..50` for a broad candidate set; do not assume the first item
  is the best answer.
- For "latest N days" queries, pass `since` explicitly; do not rely on the
  default window (the default returns the newest posts regardless of age).
- For "hot in the last couple of days", use `sort=hot`; do not combine it with
  deep pagination (it is a top-N snapshot).
- Verify selected candidates with the detail endpoint before rendering.

## 11. Acceptance criteria

Before this contract is marked stable:

1. Counter cross-check: sample N posts and verify `likeCount`/`commentCount`
   against MANAPI raw events.
2. Idempotency: replay the same backfill window twice and confirm identical
   records, indexes, and counters.
3. Pagination: newest pages have no overlap or gaps and `hasMore` is accurate.
4. Freshness: a known recent post (published after the last backfill) appears
   in `newest` after live indexing.
5. Performance bounds: `newest`, keyword, and recent time-window requests stay
   bounded; `hot` scans only the 48-hour window; deep-past `since` windows are
   documented as full-walk.
6. Error contract: invalid inputs return `40000`, missing posts `40400`, and
   the envelope shape is stable.

## 12. Open decisions for review

- Whether the 48-hour `hot` window should become a query parameter in a later
  revision (recommended: keep it fixed for v1, add `hotWindow` only if a
  downstream need appears).
- Whether actor-like state ("did this identity like the post?") is needed by
  downstream agents; exposing it requires an identity parameter and is
  intentionally not part of v1.
- Whether a dedicated likes list endpoint is needed before Agent integration.
- Follow-up work for the backfill pipeline: incremental/resumable crawling and
  MANAPI-side index health are prerequisites for stronger freshness
  guarantees.
