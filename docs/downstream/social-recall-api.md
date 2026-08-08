# Social Recall API — Downstream Integration Guide

Version: v1 (2026-08-08)

This guide is for downstream teams (Agent hosts, scheduled digests, dashboards)
that consume MetaSo's on-chain social data. The design record and acceptance
criteria live in [`../specs/2026-08-07-social-recall-api.md`](../specs/2026-08-07-social-recall-api.md);
this document only describes how to call the API.

## 1. Basics

- Production base URL: `https://so.metaid.io`
- All endpoints are public and read-only.
- Responses are JSON with HTTP 200. Success: `code=0`; errors carry a
  non-zero `code` and `message` (no `data` field).
- Timestamps are Unix seconds.
- Pagination cursors are opaque strings; pass `nextCursor` back unchanged.

Envelope:

```json
{
  "code": 0,
  "message": "",
  "data": { ... },
  "processingTime": 3
}
```

## 2. Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/api/social/feed` | Candidate post retrieval |
| GET | `/api/social/post/:pinId` | Aggregated post detail by any version PIN |
| GET | `/api/social/post/:pinId/comments` | Comments on a post |

## 3. GET /api/social/feed

Returns a coarse candidate set. MetaSo does not rank by user preference;
ordering is by time (newest) or by engagement (hot).

### 3.1 Parameters

| Parameter | Type | Default | Limits | Meaning |
| --- | --- | --- | --- | --- |
| `size` | int | 20 | 1..100 | Items per page |
| `cursor` | string | - | - | Continuation cursor from the previous response |
| `sort` | string | `newest` | `newest`, `hot` | Ordering |
| `publisher` | string | - | - | One author: GlobalMetaID, MetaID, or address |
| `publishers` | string | - | - | Comma-separated authors, OR semantics |
| `since` | int64 | - | Unix seconds | Inclusive `createdAt` lower bound |
| `until` | int64 | - | Unix seconds | Inclusive `createdAt` upper bound |
| `keyword` | string | - | - | One case-insensitive substring term |
| `keywords` | string | - | - | Comma-separated terms, OR semantics |
| `chainName` | string | - | - | Restrict to one chain (e.g. `mvc`) |
| `protocol` | string | - | `simplebuzz` | Protocol path (only simplebuzz in v1) |
| `scope` | string | - | `following` | `following` = posts by authors the `user` follows |
| `user` | GlobalMetaID | - | - | Subject for `scope=following` |

Rules:

- `keyword` and `keywords` are mutually exclusive; `publisher` and
  `publishers` are mutually exclusive.
- All filters combine with AND; multi-value filters use OR within themselves.
- `scope=following` requires `user`; the followed set is resolved from the
  MetaSo follow graph.

### 3.2 Ordering

- `newest` (default): newest-first, bounded scan.
- `hot`: top-N within the last 48 hours, ranked by raw engagement
  (`likeCount + commentCount + donateCount`) descending, ties broken by
  newest first. Older posts never appear. Hot is a snapshot without
  pagination (`nextCursor` is empty).

### 3.3 Response

```json
{
  "items": [
    {
      "pinId": "b6b9449b...i0",
      "sourcePinId": "b6b9449b...i0",
      "currentPinId": "b6b9449b...i0",
      "chainName": "mvc",
      "protocolPath": "/protocols/simplebuzz",
      "author": {
        "globalMetaId": "idq...",
        "metaId": "...",
        "address": "1..."
      },
      "contentType": "application/json;utf-8",
      "payload": {
        "content": "...",
        "contentType": "text/plain;utf-8",
        "attachments": ["metafile://..."]
      },
      "createdAt": 1786122527,
      "updatedAt": 1786122527,
      "likeCount": 2,
      "commentCount": 1,
      "donateCount": 0,
      "quoteCount": 1
    }
  ],
  "nextCursor": "k:...",
  "hasMore": true
}
```

Field notes:

- `pinId` is the stable source PIN; `currentPinId` is the latest version
  (modify/revoke folded into the same record).
- `payload` is the normalized post payload (JSON object or raw text).
- `quoteCount` counts simplebuzz posts whose `quotePin` references this post
  (repost/quote semantics).
- `donateCount` is reserved and always `0` until the donation adapter is
  enabled.
- `hotScore` appears only for `sort=hot` and equals the raw engagement count.

## 4. GET /api/social/post/:pinId

Returns the canonical post for any PIN in its version chain.

Parameters:

| Parameter | Type | Meaning |
| --- | --- | --- |
| `chainName` | string | Pass when known for the fast path; otherwise auto-resolved |

Response `data` is a single post item (same schema as feed items). Missing or
hidden posts return `40400`.

## 5. GET /api/social/post/:pinId/comments

Comments attached to the canonical target post.

Parameters:

| Parameter | Type | Default | Limits | Meaning |
| --- | --- | --- | --- | --- |
| `size` | int | 20 | 1..100 | Items per page |
| `cursor` | string | - | - | Continuation cursor |
| `chainName` | string | - | - | Pass when known for the fast path |

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

## 6. Error codes

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `40000` | Invalid parameter or invalid cursor |
| `40400` | Post not found |
| `50000` | Aggregation unavailable |

## 7. Scenario cheat sheet

| Intent | Call |
| --- | --- |
| "Today's posts about AI" | `feed?keywords=AI&since=<today 00:00>&size=50` |
| Morning digest over several topics | `feed?keywords=AI,MVC,web3&since=<window start>&size=50` (one call, OR match) |
| "What did X say in the last two days" | `feed?publisher=<globalMetaId>&since=<now-2d>&size=50` |
| Latest hot posts | `feed?sort=hot&size=20` |
| Just the latest posts | `feed?size=20` |
| Check one post / "how many likes does my latest post have" | `post/:pinId`; for own latest: `feed?publisher=<me>&size=1` then detail |
| Posts by people I follow | `feed?scope=following&user=<my globalMetaId>&since=...` |

Notes for the Agent host:

- Resolve names to GlobalMetaIDs before calling; MetaSo only accepts IDs.
- Compute calendar windows ("today", "last two days") on the host side.
- Treat the result as a broad candidate set (20-50 items); the host ranks and
  selects 3-5 items for the user.
- Verify selected candidates with the detail endpoint before rendering.

## 8. Data scope and freshness

- Indexed protocols: simplebuzz (posts with modify/revoke folding), paylike
  (like/unlike), paycomment (comments). Quote/repost counts derive from
  simplebuzz `quotePin`.
- Data is updated by live indexing as new confirmed pins arrive, and
  historically backfilled from MANAPI. Freshness is therefore bounded by the
  source indexer; a post published minutes ago may not appear immediately.
- Default `size` is 20; use `since` explicitly for "latest N days" semantics.
- Deep-past `since` windows require a full index walk and can be slow;
  recent-window queries are fast.

## 9. Versioning

- v1 (2026-08-08): current surface as documented above.
- Planned/deferred (not part of v1): actor-like state ("did this identity
  like the post?"), a dedicated likes list endpoint, a `hotWindow` parameter,
  and donation counters.
