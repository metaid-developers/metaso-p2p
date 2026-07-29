# MetaID Aggregated Search API Requirements

## Background

The AI agent browser (host: IDBots) has integrated the MetaApp aggregated query API ([`2026-07-26-metaapp-query-api.md`](2026-07-26-metaapp-query-api.md)): the downstream LLM translates intents like "find mini-games from the last seven days" into structured query parameters for `/api/metaapp/list` and picks a candidate to open — a flow that has validated well. This requirement is its "find people" counterpart. Three typical downstream LLM intents:

- "View <someone>'s bot page" → search by name, take the best match's `globalMetaId`, and open the bot page;
- "View <someone>'s details" → return the specified identity's basic profile (globalMetaId, name, avatar, bio, etc.) for the downstream LLM to present;
- "Find cheerful users/bots to chat with" → search persona/bio corpora, return candidates that can receive private messages (chatpubkey set), and let the host open a bot page or start a private chat.

The userinfo aggregator already indexes `/info/*` in real time (name/avatar/bio/role/soul/goal/chatskills/llm/persona/homepage/background/chatpubkey; four-chain scanning + MAN historical backfill + per-path latest-wins revisions), maintains reverse indexes for the three identity fields (globalMetaId/metaId/address), and warms all profiles into memory at startup. This requirement adds the public search API on top. The indexing layer gains only two additions: profile-level `updatedAt` tracking (max of the per-`/info`-path revision timestamps) and an in-memory search-document registry. No new configuration items.

The API style aligns completely with MetaApp (downstream learns one convention): a `{code, data, message, processingTime}` envelope, `code=0` on success, business error codes limited to `40000/40400/50000`, HTTP status always 200, and an opaque `nextCursor` for list pagination.

## General Principles

- The aggregator only does declarative data aggregation: indexing, field normalization, filtering, sorting. "Which person best matches the intent" is decided by the host LLM from the candidates; the aggregator makes no subjective judgments.
- The search corpus is the text of `name/bio/role/soul/goal/persona/chatSkills/llm`; it excludes binary references such as avatar/background and non-readable fields such as chatpubkey and homepage URIs (those are only returned by detail).
- CJK-friendly substring matching (case-insensitive contains); no tokenization, no synonym/semantic expansion. Insufficient recall is compensated by the host LLM retrying with near-synonym tokens (the same degradation strategy as the MetaApp guide).
- "Empty profiles" — identities that never wrote any searchable `/info` field after registration — are excluded from the search corpus: they cannot be hit by keyword and should not appear in the unfiltered feed either.
- No vector/semantic search in v1. If keyword recall becomes insufficient as the user base grows, extend later (same direction as the MetaApp spec: configurable external embedding HTTP service + in-memory cosine similarity, as an optional module).

## API 1: MetaID List / Search

### Endpoint

`GET /api/metaid/list`

### Purpose

The global MetaID user feed and intent search. With no filters it is the "recently updated users" list (by `updatedAt` descending); with parameters it covers scenarios such as "view <someone>" (keyword with exact-name boost), "find people with personality X" (keyword hitting persona/bio), "find bots with skill Y" (skill), and "find people who can receive private messages" (hasChatPubkey).

### Query Parameters

| Parameter | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `keyword` | string | No | - | Whitespace-tokenized, AND semantics, case-insensitive substring match; corpus under "Search Corpus and Scoring" |
| `skill` | string | No | - | Contains match against parsed chatSkills skill names (case-insensitive) |
| `chainName` | string | No | - | Filter by the chain the user registered on, e.g. `mvc`, `btc`, `doge`, `opcat` |
| `hasChatPubkey` | number | No | `0` | When `1`, only users with chatpubkey set (able to receive private messages) |
| `hasHomepage` | number | No | `0` | When `1`, only users with a declared custom homepage (`/info/homepage` non-empty) |
| `since` | number | No | - | Unix seconds; only users with `updatedAt >= since` |
| `until` | number | No | - | Unix seconds; only users with `updatedAt <= until` |
| `size` | number | No | `20` | Page size, max 100 |
| `cursor` | string | No | - | Opaque cursor; an invalid cursor returns 40000 |

### Search Corpus and Scoring

Each user's search corpus (precomputed, lowercased) has three tiers:

| Corpus tier | Fields | Score per token hit |
| --- | --- | --- |
| Name | `name` | 3 |
| Skills | skill names parsed from `chatSkills` | 2 |
| Profile text | `bio`, `role`, `soul`, `goal`, `persona` (raw JSON), `llm` (provider/model/name) | 1 |

Rules (same shape as MetaApp):

- The keyword is whitespace-tokenized with **AND semantics**: every token must hit at least one corpus tier, otherwise the user is out; the score accumulates each token's best-hit tier.
- **Exact-name boost**: when `name` exactly equals the whole keyword (after lowercasing and removing whitespace), a large bonus is added, guaranteeing the person ranks first for "view <someone>" intents.
- With `keyword`: relevance score descending → `updatedAt` descending; without `keyword`: `updatedAt` descending. Tie-breakers: `chainName`, then `globalMetaId` lexicographic order.

### Response

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "items": [
      {
        "globalMetaId": "...",
        "metaId": "...",
        "address": "...",
        "chainName": "mvc",
        "name": "Alice",
        "avatarId": "<avatar-pin-id>i0",
        "bio": "链上生活记录者",
        "chatSkills": ["translate", "draw"],
        "hasChatPubkey": true,
        "hasHomepage": true,
        "createdAt": 1768284841,
        "updatedAt": 1768284841
      }
    ],
    "nextCursor": "eyJvIjoyMH0",
    "hasMore": true
  }
}
```

- The three identity fields are returned as-is; downstream uses `globalMetaId` to open the bot page (`/api/bot-homepage/globalmetaid/:globalMetaId`) or start a private chat.
- `avatarId` is the avatar pinId; callers resolve and download it via the existing metafile chain (file.metaid.io, etc.).
- `chatSkills` is the parsed skill-name array (parsing rules match bothomepage: a JSON array/object yields its allow list, a plain string is a single skill); absent when unset or invalid JSON.
- `persona/llm/homepage/chatpubkey` are excluded from list items (size and readability) and returned by detail.
- `createdAt` is the MetaID registration time (globalMetaId creation); `updatedAt` is the on-chain timestamp of the user's most recent `/info/*` update. Both are unix seconds.

## API 2: MetaID Detail

### Endpoint

`GET /api/metaid/detail/:identity`

### Purpose

View a user's full profile after picking a candidate. `:identity` accepts any of `globalMetaId / metaId / address` (resolved internally); callers do not need to know which identity type they hold.

### Path Parameters

| Parameter | Description |
| --- | --- |
| `:identity` | Any of `globalMetaId` / `metaId` / `address` |

### Response

`data` is a superset of the list-item fields, plus:

```json
{
  "code": 0,
  "message": "ok",
  "data": {
    "globalMetaId": "...",
    "...": "(all list-item fields)",
    "avatarContentType": "image/png",
    "role": "...",
    "soul": "...",
    "goal": "...",
    "persona": { "(raw /info/persona JSON)" },
    "llm": { "provider": "...", "model": "...", "name": "..." },
    "homepage": { "(raw /info/homepage JSON)" },
    "background": "/content/<pinId>",
    "chatPubkey": "...",
    "fieldPins": {
      "name": "<pinId>",
      "avatar": "<pinId>",
      "bio": "<pinId>",
      "...": "current version pinId of each /info field"
    }
  }
}
```

- `persona/homepage` are raw on-chain JSON (absent when unset or invalid JSON); `llm` is parsed into provider/model/name by the existing bothomepage rules, with a plain string treated as the provider.
- A non-existent user (none of the three identity forms resolves) returns `code=40400`.

## Error Codes

| code | Scenario |
| --- | --- |
| 40000 | Invalid parameters (size/since/until/hasChatPubkey/hasHomepage unparseable, invalid cursor) |
| 40400 | Target identity of detail does not exist |
| 50000 | Internal aggregation error |

## Implementation Notes (Performance Design)

- **Approach**: in-memory precomputed search documents + per-query scan-and-score — the same philosophy as MetaApp's "time-index scan + in-memory filtering". A lowercased corpus document (per-tier text + updatedAt + flags) is pre-built for every non-empty profile and held in an in-memory registry inside the userinfo module.
- **Update timing**: search documents are rebuilt on the profile write path (hooked at the same point as `profilesByIdentity` updates) and built during the startup warm; reads take an RWMutex read lock and never re-lowercase/re-concatenate at query time.
- **updatedAt tracking**: the profile write path records the max of the per-`/info`-path revision timestamps into the search document; `createdAt` reuses the existing globalMetaId creation records.
- **Scale assumptions**: all profiles already live in memory (the `profilesByIdentity` startup warm), so the search documents' incremental memory is manageable (a few hundred bytes per user). At tens of thousands to ~100k+ users with downstream LLM tool-call QPS, a single query takes milliseconds to tens of milliseconds. If volume or QPS grows significantly, re-evaluate inverted indexes (CJK needs n-grams) or result caching.
- The cursor uses the same format as MetaApp: base64url(JSON `{"o":offset}`), only issued when `hasMore`; offset drift under concurrent writes is accepted, same as MetaApp.

## Explicitly Out of Scope (v1)

- Synonym/semantic/vector search and pinyin matching (e.g. "kaixin" matching 「开心」). Insufficient recall is compensated by host LLM near-synonym retries.
- Inverted indexes or FTS engines for name/skills (in-memory scan suffices at current volumes; CJK substring matching needs no tokenizer).
- Including empty profiles (registered identities that never wrote searchable `/info` fields) in the corpus.
- Changing the existing `GET /api/group-chat/search-users` (kept as-is, no interaction) and the legacy `GET /info/*` endpoints.
- Rewriting or deeply normalizing JSON fields such as persona/homepage (passed through raw; llm/chatSkills parsed by the existing bothomepage rules).
- New configuration items.
