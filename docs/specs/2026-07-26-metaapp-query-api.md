# MetaApp Aggregated Query API Requirements

## Background

The AI agent browser (host: IDBots) needs to support "finding on-chain MetaApps in natural language" in its left-hand AI chat rail: a user says "find mini-games from the last seven days", "open the latest MetaApp by a given MetaID", or "show the apps derived from this one". The host LLM translates the intent into structured query parameters, calls the aggregated API to get 5–10 candidates, then picks one and opens it as `metaapp://<pinId>`.

metaso-p2p's `publishedcontent` aggregator already indexes `/protocols/metaapp` in real time (four-chain scanning + MAN historical backfill + create/modify/revoke version folding + publisher identity indexes), with the full payload stored in `Record.PayloadJSON`. This requirement adds the public query API on top of it (the "Task 7 wires public router exposure" item in the publishedcontent plan). Zero indexing-layer changes; no new configuration items.

The API style follows the metaso-p2p house convention: a `{code, data, message, processingTime}` envelope, `code=0` on success, business error codes limited to `40000/40400/50000`, HTTP status always 200. List pagination uses an opaque `nextCursor`.

## General Principles

- The aggregator only does declarative data aggregation: indexing, folding, field normalization, filtering, sorting. It makes no subjective judgments such as "is this app good/safe".
- Semantic understanding lives in the host LLM layer: the aggregator provides structured retrieval by keyword/tag/time/publisher/derivation; "which app best matches the intent" is decided by the host LLM from the candidates.
- The default list only returns latest, non-revoked, `disabled != true` apps; this is a filter over on-chain declared state.
- The search corpus is `title/appName/intro/tags` and **excludes `prompt`** (AI generation prompts are too long and noisy; only returned by detail).
- No vector/semantic search in v1. If keyword recall becomes insufficient as the app count grows, extend later (direction: embeddings via a configurable external HTTP service, vectors in a Pebble namespace, in-memory cosine similarity, as an optional module with no heavy external dependencies).

## API 1: MetaApp List / Search

### Endpoint

`GET /api/metaapp/list`

### Purpose

The global MetaApp feed and intent search. With no filters it is the "latest apps" list; with parameters it covers scenarios such as "category X apps from the last N days" (keyword+since), "apps published by a MetaID/address" (publisher), and "apps supporting a protocol" (tag).

### Query Parameters

| Parameter | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `keyword` | string | No | - | Whitespace-tokenized, AND semantics, case-insensitive substring match; corpus `title/appName/intro/tags` |
| `tag` | string | No | - | Comma-separated tags, any hit; exact match against payload `tags` (case-insensitive) |
| `chainName` | string | No | - | Filter by the chain hosting the app, e.g. `mvc`, `btc`, `doge`, `opcat` |
| `runtime` | string | No | - | Contains match (case-insensitive); `browser` matches `browser/android` |
| `publisher` | string | No | - | Matches any of the publisher's `globalMetaId/metaId/address` (case-insensitive); with `size=1` it means "this user's latest app" |
| `since` | number | No | - | Unix seconds; only apps with `updatedAt >= since` |
| `until` | number | No | - | Unix seconds; only apps with `updatedAt <= until` |
| `includeDisabled` | number | No | `0` | When `1`, include apps whose payload declares `disabled=true` (revoked apps are never returned) |
| `size` | number | No | `20` | Page size, max 100 |
| `cursor` | string | No | - | Opaque cursor; an invalid cursor returns 40000 |

### Sorting

- With `keyword`: relevance score descending first (tag hit ×3 + title/appName hit ×2 + intro hit ×1, accumulated per matched token), then `updatedAt` descending.
- Without `keyword`: `updatedAt` descending.
- Tie-breakers: `chainName`, then `pinId` lexicographic order.

### Response

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "pinId": "source-pin-id",
        "sourcePinId": "source-pin-id",
        "currentPinId": "current-pin-id",
        "chainName": "mvc",
        "title": "番茄钟",
        "appName": "pomodoro",
        "intro": "极简风格的番茄钟工具",
        "tags": ["tool", "timer"],
        "icon": "metafile://<pinId>.png",
        "coverImg": "metafile://<pinId>.jpg",
        "runtime": "browser",
        "version": "1.0.0",
        "content": "metafile://<pinId>.zip",
        "indexFile": "index.html",
        "forkedFrom": "",
        "disabled": false,
        "publisherGlobalMetaId": "...",
        "publisherMetaId": "...",
        "publisherAddress": "...",
        "publisherName": "Alice",
        "publisherAvatarId": "<avatar-pin-id>i0",
        "createdAt": 1768284841,
        "updatedAt": 1768284841
      }
    ],
    "nextCursor": "eyJvIjoyMH0",
    "hasMore": true
  }
}
```

- `pinId` is the version chain's **stable root pin** (source pin, identical to `sourcePinId`) — MetaID modify/revoke operations anchor to the original pin, so hosts should build open URLs as `metaapp://<pinId>` (the original pin). This matches the `pinId` semantics of Bot Homepage v3 section items. `currentPinId` is the latest version pin and can be used to detect app updates; for never-modified apps all three are equal.
- List-item payload fields (title/intro/tags, etc.) come from the version chain's **latest** record, combined with the stable pinId.
- `icon/coverImg/content` are returned as raw `metafile://` URIs; callers resolve and download them via the existing metafile chain.
- `publisherName` / `publisherAvatarId` come from userinfo profile enrichment (avatar pinId, downloadable via the metafile chain); both fields are absent when the publisher has no profile.
- `createdAt/updatedAt` are unix seconds.

## API 2: MetaApp Detail

### Endpoint

`GET /api/metaapp/detail/:pinId`

### Purpose

Fetch the complete manifest before opening an app. `:pinId` accepts any version pinId on the chain (automatically resolved to the latest record).

### Query Parameters

| Parameter | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `chainName` | string | No | - | Direct lookup when provided; otherwise resolved via a cross-chain scan |

### Response

`data` is a superset of the list-item fields, plus:

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "pinId": "...",
    "...": "(all list-item fields)",
    "prompt": "You are an AI...",
    "payload": { "(raw on-chain payload JSON)" }
  }
}
```

A non-existent app returns `code=40400`.

## API 3: MetaApp Forks

### Endpoint

`GET /api/metaapp/forks/:pinId`

### Purpose

"Show the apps derived from this MetaApp": returns direct children whose payload `forkedfrom`/`forkedFrom` points at this app's version chain. A child referencing any version pinId of the parent clusters into the same version chain. Forks of forks are not recursed in v1; callers may call this endpoint again on a child.

### Query Parameters

| Parameter | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `size` | number | No | `20` | Page size, max 100 |
| `cursor` | string | No | - | Opaque cursor |

Sorting: `createdAt` descending. A non-existent parent returns `code=40400`. The response shape is the same as API 1 (`items/nextCursor/hasMore`).

## MetaAppItem Field Normalization Rules

On-chain payload field names come in several spellings; the aggregator normalizes them as follows (first non-empty string wins by priority):

| Output field | Payload keys |
| --- | --- |
| `title` | `title` → `name` → `displayName` |
| `appName` | `appName` → `appname` |
| `intro` | `intro` → `description` → `summary` |
| `tags` | `tags` (array elements stringified) |
| `icon` / `coverImg` / `runtime` / `version` / `content` | same-name key |
| `indexFile` | `indexFile`, defaults to `index.html` |
| `forkedFrom` | `forkedfrom` → `forkedFrom` |
| `disabled` | `disabled` (tolerates `true` and `"true"`) |

## Error Codes

| code | Scenario |
| --- | --- |
| 40000 | Invalid parameters (size/since/until/includeDisabled unparseable, invalid cursor) |
| 40400 | Target app of detail/forks does not exist |
| 50000 | Internal aggregation error |

## Protocol Capability Declaration Convention (Publisher-Side Cooperation)

Capability searches like "a MetaApp that can display simplebuzz" or "an app that can publish on-chain notes" depend on publishers declaring capability tags in payload `tags` (recommended: use protocol names directly, e.g. `simplebuzz`, `simplenote`). The aggregator does not infer capabilities from payload content; undeclared capabilities can only fall back to keyword hits in `intro` text. When IDBots' publish tool (the `bot_browser_publish_app` chain) generates a metaapp payload, it should write the protocols the app supports into `tags`.

## Explicitly Out of Scope (v1)

- Vector/semantic search (see "General Principles").
- Secondary indexes for tag/forkedFrom (at the current app volume, time-index scan + in-memory filtering is sufficient; add when the volume grows).
- URL rewriting for assets such as icon/cover (returned as raw `metafile://`).
- Recursing the full derivation tree in forks.
- New configuration items.
