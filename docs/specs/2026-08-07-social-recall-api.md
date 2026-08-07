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

## 2.1 Scenario-driven requirements

The contract is derived from concrete downstream scenarios. Each scenario
must be satisfiable with the endpoints in this document; the backend always
returns a coarse, time-ordered (or engagement-ordered) candidate set and never
personalizes or ranks by user preference.

| # | Scenario | Structured query | Backend behavior |
| --- | --- | --- | --- |
| S1 | "Today's posts about AI" / scheduled morning digest over several topics | `keyword` or `keywords`, `since`/`until` | Coarse candidates matching any keyword in the time window |
| S2 | "What did X say in the last two days" | `publisher`, `since`/`until` | Coarse candidates from that author in the window |
| S3 | "What are the latest hot posts" | `sort=hot` | Top-N by engagement within the recent hot window |
| S4 | "Just show me the latest posts" | no conditions | Newest-first feed |
| S5 | "Check post P / how many likes does my latest post have" | detail by `pinId`; `publisher=self&size=1` for own latest | Aggregated post info with like/comment/repost counts |
| S6 | "What have the people I follow posted" | `scope=following&user=<globalMetaId>`, `since` | Posts by the user's followed authors, newest first |

The Agent host is responsible for: resolving names to GlobalMetaIDs, computing
calendar windows ("today", "last two days"), issuing one or more calls, and
selecting/presenting 3-5 final items.

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
| `keyword` | string | - | - | Case-insensitive substring over post text (single term) |
| `keywords` | string | - | - | Comma-separated terms, OR semantics; matches if any term matches |
| `publishers` | string | - | - | Comma-separated GlobalMetaIDs/MetaIDs/addresses, OR semantics |
| `scope` | string | - | `following` | Restrict to posts by authors followed by `user` |
| `user` | string | - | - | GlobalMetaID used by `scope=following` |
| `chainName` | string | - | - | Restrict to one chain (e.g. `mvc`) |
| `protocol` | string | - | `simplebuzz` | Protocol path; only simplebuzz is supported in v1 |

Constraints:

- `since` and `until` must satisfy `since <= until` when both are present.
- `size` outside `1..100` is rejected.
- `sort`, `protocol`, and malformed `since`/`until`/`cursor` are rejected.
- All filters combine with AND semantics.
- `keyword` and `keywords` are mutually exclusive; `publisher` and
  `publishers` are mutually exclusive.
- `scope=following` requires `user`; the followed set is resolved from the
  MetaSo follow graph and the author filter is applied as an OR over that set.

### 6.2 Ordering semantics

- `newest` (default): the global newest-first index. The scan is bounded and
  stops after one page plus a has-more probe, so latency is independent of the
  total dataset size.
- `hot`: top-N within a fixed recent window of the last 48 hours, ranked by
  engagement descending, aligned with the legacy MetaSo hot semantics
  (sort by raw engagement count). Engagement = `likeCount + commentCount +
  donateCount`. Ties are broken by newest first. Older posts are never
  returned by `hot`. The result is a top-N snapshot with no pagination.

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
      "quoteCount": 0
    }
  ],
  "nextCursor": "k:...",
  "hasMore": true
}
```

Notes:

- `payload` is the normalized post payload (object or raw text).
- `hotScore` is present only for `sort=hot`; zero scores are omitted.
- `quoteCount` counts simplebuzz posts whose payload `quotePin` references the
  canonical source of this post (repost/quote semantics from the legacy
  MetaSo protocol). It is a proposed field; see Open decisions.
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
7. Scenario coverage: S1-S6 above each return real candidates in production
   with the documented ordering.

## 12. Open decisions for review

- Whether the 48-hour `hot` window should become a query parameter in a later
  revision (recommended: keep it fixed for v1, add `hotWindow` only if a
  downstream need appears).
- Whether actor-like state ("did this identity like the post?") is needed by
  downstream agents; exposing it requires an identity parameter and is
  intentionally not part of v1.
- `quoteCount` (repost/quote) requires verifying real `quotePin` samples from
  MANAPI and adding the counter; until then the field is absent or zero.
- Whether a dedicated likes list endpoint is needed before Agent integration.
- Whether `scope=following` should be its own endpoint
  (`/api/social/following-feed`) instead of a feed parameter; both are
  functionally equivalent in v1.
- Follow-up work for the backfill pipeline: incremental/resumable crawling and
  MANAPI-side index health are prerequisites for stronger freshness
  guarantees.
