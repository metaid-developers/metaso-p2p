# Bot Search API (Group-Task Staffing)

## Background

The IDBots Twin Bot staffs on-chain Group Tasks: for each coarse seat (content / design / engineering / promotion / domain) it needs a ranked, explained, online-aware page of remote bot candidates. The existing `GET /api/metaid/list` ([`2026-07-28-metaid-search-api.md`](2026-07-28-metaid-search-api.md)) is a keyword/skill feed — it lacks role-oriented priors, match explanations, presence in one shot, and any on-chain collaboration history. This requirement adds `POST /api/bots/search` on top of data the node already indexes: userinfo profiles, SimpleGroup create/join pins, skill-service pins, and idchat presence.

The API style aligns with the other aggregation endpoints: a `{code, data, message, processingTime}` envelope, `code=0` on success, HTTP status always 200, and an opaque `nextCursor` for pagination. Business error codes are `1001/1002/1003` (new contract for the IDBots Twin consumer; see "Error Codes").

## General Principles

- The aggregator only does declarative data aggregation plus a documented, stable scoring function. Hiring decisions, local-vs-remote policy, and trust/temperature judgments are host-side and stay out of the response.
- Matching is **weighted partial match**, not hard AND: a query `占卜 塔罗 命运` must recall a bot whose bio only contains `占卜塔罗牌`.
- Group-task history is **facts only** (create/join/leave pins). No verdicts, no reliability scoring. Empty history is valid, not an error.
- The botsearch aggregator is read-only: it indexes nothing itself and owns no Pebble data. Profiles come from userinfo (`BotSearchProfiles` snapshot), history from groupchat (`GroupHistoryForIdentity`), published skills from skillservice (`PublishedServiceNames`), and presence from the socket manager / federation global reader — all injected via setters in `cmd/metaso-p2p/main.go`.
- The profile corpus inclusion rule mirrors the MetaID search corpus: identities that never wrote any searchable `/info` field (name/bio/role/soul/goal/persona/llm/chatSkills) are excluded. Candidate `role`/`goal` fall back to the `/info/persona` JSON `{"role","goal"}` when the separate `/info/role|goal` pins were never written (IDBots Bot edit only writes persona), so persona-only bots still score on role/goal and roleHint. Profiles whose `/info/bio` is a legacy pre-v3 whole-profile JSON object (`{"role","soul","goal","background","bio","allowChatSkills"/"allow_chat_skills","llm"}`, written by old IDBots clients) get the IDBots-restore-aligned fallback — `bio` ← `background` || `bio`, `role`/`goal` ← the object, `chatSkills` ← `allowChatSkills` || `allow_chat_skills` — applied only when neither a `/info/persona` nor a `/info/chatSkills` pin exists, and never overriding dedicated `/info/role|goal` pins. A `/info/bio` value that parses as JSON but carries no known profile key passes through untouched. The v3 `/info/chatSkills` pin shape `{allowPrivateChatSkills, allowGroupChatSkills}` is parsed as the union of both lists (this also fixes chatSkills for v3 bots in `/api/metaid/list`).

## API: Bot Search

### Endpoint

`POST /api/bots/search`

Idempotent. No auth beyond the existing public aggregation auth.

### Request Body

| Field | Type | Required | Default | Description |
| --- | --- | --- | --- | --- |
| `query` | string | recommended | - | Natural-language seat query; CJK-aware tokenization (below) |
| `roleHint` | string | No | - | One of `content`, `design`, `engineering`, `promotion`, `domain`. Soft prior, not a hard filter; any other value returns `1001` |
| `skills` | string[] | No | - | Extra skill tokens, OR-ed with query tokens |
| `language` | `zh` \| `en` | No | - | Accepted for forward compatibility; currently does not change behavior |
| `onlineOnly` | boolean | No | `true` | When true, candidates not online per presence are dropped before paging |
| `hasChatPubkey` | boolean | No | `true` | When true, only bots with a chat pubkey (group-task invite requires it) |
| `excludeGlobalMetaIds` | string[] | No | - | Case-insensitive exclusion (host sends its own local Twin/worker GMIDs) |
| `limit` | number | No | `10` | Page size, max `50`; out of `[1,50]` returns `1001` |
| `cursor` | string | No | - | Opaque cursor from a previous `nextCursor`; invalid returns `1001` |

`query` empty after trim AND no `skills` AND no `roleHint` → `1001`. A `roleHint`-only or `skills`-only query is valid.

### Tokenization

Whitespace-split `query` and each `skills` entry into segments, then per segment:

- If the segment contains CJK runes (Han, Hiragana, Katakana, Hangul): emit the whole segment (lowercased) **and** every CJK bigram within it — bigrams are taken over maximal runs of consecutive CJK runes; a 2-char segment thereby yields itself once, and a run of a single CJK char yields that char.
- Otherwise (latin/digit): emit the lowercased segment as one whole-word token.

The resulting token set is deduplicated, preserving first-occurrence order.

### Scoring

```
score = 4*nameHit + 2*skillHit + 1*bioHit + 0.5*roleHintHit + 1*groupTaskHit
```

- Each hit sums token weights; token weight = `min(runeCount(token), 4)`.
- Each (field-category, token) pair is counted once. The field-categories are: `name`, `chatSkills` (a token hitting any skill counts once), `bio`/`role`/`goal` (one shared category), and `groupTaskTitle`/`groupTaskNote` (one shared category over recent group history).
- Hit test: case-insensitive substring against the field text.
- `roleHintHit`: a static en+zh synonym map per roleHint, substring-matched against `role + bio + chatSkills`; at most one flat `0.5` hit per candidate. Synonyms: `content → [content, 内容, 文案, 写作, writer]`, `design → [design, 设计, 视觉, ui]`, `engineering → [engineering, 开发, 工程, 代码, code]`, `promotion → [promotion, 推广, 营销, 运营]`, `domain → [domain, 领域, 专家, 顾问]`.
- **Exact-name boost**: when `name` exactly equals the whole trimmed `query` (case-insensitive), a flat `+1000` is added so exact hits never sort below fuzzy-only hits.
- A candidate with score 0 is excluded. There is intentionally **no** flat "has any history" bonus — that would bury specialists who have never been in a Group Task.

`matchReasons` carries one entry per contributing (field, token): `{field, token, weight}` where weight = coefficient × token weight (`name` 4×, `chatSkills` 2×, `bio`/`role`/`goal` 1×, `groupTaskTitle`/`groupTaskNote` 1×, `roleHint` flat 0.5; the exact-name boost appears as `{field:"name", token:<query>, weight:1000}`). For the shared bio/role/goal and title/note categories the entry names the first field that contained the token. Entries are ordered by weight descending.

### Sort and Pagination

Score descending; stable tie-break by lowercased `name`, then `globalMetaId`. The cursor is base64url(JSON `{"o": offset}`) — the same wire format as the MetaApp / MetaID / skill-service lists; `nextCursor` is `null` when the page is the last one. Presence, published-skills, and history enrichment of the response rows happens after paging (history is fetched during scoring because groupTask title/note hits feed the score; presence is matched in memory against a single snapshot taken per request).

### Response

```json
{
  "code": 0,
  "message": "",
  "data": {
    "candidates": [
      {
        "globalMetaId": "idq1…",
        "metaId": "…",
        "name": "Counsel-bot",
        "avatarId": "…i0",
        "bio": "商业合同与合规审查",
        "role": "法律顾问",
        "goal": "帮客户审查合同",
        "chatSkills": ["legal-review", "contract"],
        "publishedSkills": ["legal-review-service"],
        "chainName": "mvc",
        "hasChatPubkey": true,
        "hasHomepage": true,
        "homepage": "metaapp://…i0",
        "isOnline": true,
        "lastSeenAgoSeconds": 42,
        "groupTaskCount": 3,
        "recentGroupTasks": [
          {
            "groupId": "aaaa…i0",
            "title": "技能介绍 MetaApp",
            "goal": "写出介绍、出图出视频并上链发布",
            "joinedAs": "member",
            "joinedAt": 1780000000,
            "joinPinId": "bbbb…i0",
            "stillMember": true,
            "messageCount": 0,
            "kind": "group"
          }
        ],
        "score": 18.5,
        "matchReasons": [
          { "field": "chatSkills", "token": "legal", "weight": 8 },
          { "field": "bio", "token": "合同", "weight": 2 },
          { "field": "groupTaskTitle", "token": "合同", "weight": 2 }
        ]
      }
    ],
    "nextCursor": null,
    "queriedAt": 1780000000000
  }
}
```

- `lastSeenAgoSeconds` is `null` when the candidate is offline or presence is unknown; otherwise derived from the presence entry's `lastSeenAt` (falling back to `connectedAt`).
- `queriedAt` is unix milliseconds; `joinedAt` is unix seconds.
- `publishedSkills` is the deduplicated names of the provider's currently visible skill services (default visibility filter), newest first; `[]` when the bot publishes none. It is a bonus field — Twin ranks on `chatSkills` + bio.
- `chatSkills`, `publishedSkills`, `recentGroupTasks`, and `matchReasons` are always arrays (never `null`).

### Group-Task History

History is folded in from the existing SimpleGroup index — no new client publish and no new Pebble index:

- Source pins: `/protocols/simplegroupcreate` (the create pin **is** `groupId`; `groupName` → `title`, `groupNote` → `goal`) and `/protocols/simplegroupjoin` (`state: 1` join, otherwise leave).
- `joinedAs` is `chair` when the identity created the group (joinPinId = create pin), else `member` (joinPinId = join pin, `joinedAt` = join pin timestamp).
- `stillMember` comes from the member record (`!isRemoved`), so a leave pin flips it to `false` while the group stays in history.
- `groupTaskCount` is the full count; `recentGroupTasks` returns at most 5, newest first.
- Reverse lookup identity → groups scans the existing per-identity `groupjoin:<identity>:<groupId>:<ts>:<pinId>` index with all of the candidate's aliases (metaId / globalMetaId / address). No backfill is required; an identity with no indexed pins simply yields `groupTaskCount: 0` and `recentGroupTasks: []`.

**`kind: "group"` caveat:** `simplegroupchat` content is AES-encrypted, so the indexer cannot detect the `[GROUP TASK]` kickoff tag and cannot distinguish Group Tasks from casual social groups yet. Every row is therefore `kind: "group"` until the message index allows `kind: "group_task"`. For the same reason `messageCount` is always `0` (chat sender counts are not indexed).

## Presence Behavior

Presence reuses the idchat presence backends: the local socket connection manager, and the federation global reader when enabled (same wiring as the bot-homepage aggregator). Identity matching is case-insensitive over globalMetaId / metaId / address.

- Presence is **unavailable** when no local reader is wired AND (no global reader OR global reader disabled).
- If `onlineOnly` is true while presence is unavailable: HTTP 200, `code: 1002`, `message: "presence_unavailable"`, and empty `data.candidates` — the node must not return stale "maybe online" rows.
- If `onlineOnly` is false in that state, the query proceeds with `isOnline: false` and `lastSeenAgoSeconds: null` on every row.
- When presence is available and `onlineOnly` is true, offline candidates are dropped **before** paging, so a page never comes back short because of post-filtering.

## Error Codes

| code | Meaning |
| --- | --- |
| 0 | OK |
| 1001 | Invalid query (empty after trim with no skills/roleHint, unknown roleHint, limit out of `[1,50]`, invalid cursor, malformed body) |
| 1002 | Presence backend unavailable (only when `onlineOnly` is true) |
| 1003 | Internal failure (profile source not wired, group-history lookup error) |

HTTP status is always 200, matching the envelope convention of the other aggregation endpoints.

## Implementation Notes

- New package `internal/aggregator/botsearch` (read-only aggregator; `HandleBlockPin`/`HandleMempoolPin` are no-ops). Route mounted as `POST /api/bots/search` via the standard `RegisterRoutes(router.Group("/api"))`.
- userinfo gains one exported accessor: `BotSearchProfiles()` returns independent per-profile snapshots (name/avatarId/bio/role/goal/chatSkills parsed/homepage/hasChatPubkey + identities) from the warm `profilesByIdentity` cache, applying the same corpus inclusion rule as the MetaID search documents.
- groupchat gains one exported accessor: `GroupHistoryForIdentity(identities ...string)` scanning the existing `groupjoin:` identity prefixes; groupId is extracted from the key by anchoring on the fixed-width timestamp because pin ids themselves contain `:`.
- skillservice gains one exported accessor: `PublishedServiceNames(providerGlobalMetaId)` scanning the existing `service_by_provider_global:` index and applying `IsVisibleDefault()`.
- Scoring runs over the in-memory profile snapshot; history is fetched per candidate during scoring (groupTask title/note hits feed the score), presence is matched from one snapshot per request, and published skills are resolved only for the returned page.

## Explicitly Out of Scope (v1)

- `[GROUP TASK]` detection and per-sender `messageCount` (encrypted chat; see the `kind` caveat).
- Hiring, inviting, impressions, trust/temperature scoring — host-side.
- Local-bot search; the host merges local workers itself.
- MetaApp package search and Gig Square service listings (unchanged, separate endpoints).
