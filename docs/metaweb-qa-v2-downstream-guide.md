# MetaWeb On-chain Q&A — Downstream Integration Guide (v2: Comment Threads & Author Pages)

Audience: frontend teams building the human-facing Q&A MetaApp on top of `https://so.metaid.io`. This guide covers the **v2 additions** deployed 2026-09-07 (version `3ed380b`):

- **R5** `GET /api/qa/pins/:pinId/comments` — readable comment threads on questions and answers
- **R6** `GET /api/qa/answers` — answers across questions (author pages / newest-answers feed)
- **R6.2** `GET /api/qa/questions?publisher=` — an author's questions, keyword-free

The v1 endpoints (`/api/qa/search`, `/api/qa/questions`, `/api/qa/questions/:pinId`, `/api/qa/questions/:pinId/answers`) are unchanged. Full normative contract: [`docs/specs/2026-09-07-metaweb-qa-comments-author-api.md`](specs/2026-09-07-metaweb-qa-comments-author-api.md) (v2) and [`docs/specs/2026-09-07-metaweb-qa-api.md`](specs/2026-09-07-metaweb-qa-api.md) (v1).

## Conventions (unchanged from v1)

- **Base URL**: `https://so.metaid.io` (alias `https://socket.metaid.io`).
- **Envelope**: every response is HTTP 200 with `{code, data, message, processingTime}`. `code=0` is success; `40000` bad parameter/cursor, `40400` not found (or hidden), `50000` aggregation unavailable. Match on `code`, never on HTTP status.
- **No auth**, permissive CORS — direct browser calls are fine.
- **Cursors**: opaque `base64url` strings. Pass `nextCursor` back verbatim as `?cursor=`. When `hasMore=false`, `nextCursor` is `null` and paging is done. Cursors are offset-based: rows can shift by one across pages if new content arrives mid-pagination (same trade-off as all list endpoints in this family).
- **Time**: `createdAt` is unix seconds (block time). Rows with `isMempool: true` arrived via mempool relay and will be replaced by the confirmed pin — same pin id, possibly a slightly different `createdAt`. Treat pin id as the stable key; re-render rows on refetch.
- **Visibility**: revoked (hidden) questions, answers, and comments disappear from all surfaces; a revoked question hides its answers and their comment threads too (`40400`). There are no "deleted but visible" states.

## 1. Comment Threads — `GET /api/qa/pins/:pinId/comments`

Renders the flat comment list under a question **or** an answer. This is the only place Q&A comment content is readable (v1 exposed only a `commentCount`).

### Request

| Param | Type | Required | Default | Notes |
| --- | --- | --- | --- | --- |
| `:pinId` | path | Yes | — | Any version of a question, or an answer pin. `<64 hex>i<n>` |
| `sort` | query | No | `newest` | `newest` = createdAt desc, tie pinId asc; `oldest` = exact reverse (chronological reading) |
| `size` | query | No | `20` | 1–50; `< 1` or non-numeric → `40000`, `> 50` clamps to `50` |
| `cursor` | query | No | — | Invalid → `40000` |

### Response `data`

```json
{
  "items": [
    {
      "protocol": "paycomment",
      "pinId": "a1b2…i0",
      "currentPinId": "a1b2…i0",
      "targetPinId": "c3d4…i0",
      "chainName": "mvc",
      "content": "Full comment body (markdown, capped at 2000 runes at index time).",
      "contentType": "text/markdown",
      "publisher": { "globalMetaId": "idq1…", "metaid": "…", "name": "Show" },
      "createdAt": 1755000000,
      "isMempool": false
    }
  ],
  "nextCursor": null,
  "hasMore": false
}
```

Field notes:

- `content` is the **full stored body** — render it as markdown. No summary field exists for comments. Bodies longer than 2000 runes were truncated server-side at index time; nothing for the client to trim.
- `targetPinId` is the stable source pin id of the commented question/answer. You may pass **any version** of the target in the path — it normalizes to the same thread.
- `publisher.name` is best-effort profile enrichment of the returned page (empty string when unknown). `avatar` is not included on comment rows.
- Comment replies (a comment targeting another comment) are **not** part of this product round — don't build reply threading UI against this endpoint; it returns a flat list.

### Errors

| code | when |
| --- | --- |
| `40000` | `:pinId` not `<64 hex>i<n>`; unknown `sort`; bad `size`; invalid `cursor` |
| `40400` | Pin is not a Q&A pin (e.g. a simplebuzz post), or it is hidden (revoked question/answer, or an answer whose question was revoked) |

### UI recipe

- Question page: load answers (v1 detail), render `commentCount` badge, fetch comments lazily when the user expands the thread (`?sort=newest`); a "view chronologically" toggle refetches `?sort=oldest`.
- Same call for the comment section under each answer — pass the answer's `pinId`.
- Polling for live updates: re-request page 1 without cursor; reconcile by `pinId`. `isMempool` rows may shift `createdAt` on confirmation.

## 2. Answers Across Questions — `GET /api/qa/answers`

One endpoint powers three surfaces: the site-wide "newest answers" feed, an author page's answers tab, and its best-answers tab.

### Request

| Param | Type | Required | Default | Notes |
| --- | --- | --- | --- | --- |
| `publisher` | query | No | — | Answer publisher's `globalMetaId` **or** `metaid`, case-insensitive exact match. Omit = all publishers |
| `sort` | query | No | `newest` | `newest` = createdAt desc, tie pinId asc. `top` = `score` desc, tie newer first, then pinId asc |
| `size` | query | No | `10` | 1–50, clamped as usual |
| `cursor` | query | No | — | Invalid → `40000` |

### Response `data`

Each item is the v1 answer item **plus an embedded `question` block**:

```json
{
  "items": [
    {
      "protocol": "simpleanswer",
      "pinId": "e5f6…i0",
      "currentPinId": "e5f6…i0",
      "questionPinId": "c3d4…i0",
      "question": {
        "pinId": "c3d4…i0",
        "title": "How to recover a wallet when the mnemonic is lost?",
        "chainName": "mvc",
        "createdAt": 1755000000
      },
      "chainName": "mvc",
      "summary": "First ~200 runes of the answer…",
      "tags": ["wallet"],
      "publisher": { "globalMetaId": "idq1…", "metaid": "…", "name": "Show" },
      "createdAt": 1755000100,
      "isMempool": false,
      "likeCount": 5,
      "dislikeCount": 1,
      "commentCount": 0,
      "score": 4
    }
  ],
  "nextCursor": null,
  "hasMore": false
}
```

Field notes:

- `question` is the row's parent-question identity — enough to render "answered *<title>*" without a per-row detail call. Click through to `/api/qa/questions/:questionPinId` for the full page. The `question` embed exists **only** on this endpoint; per-question answer lists (v1 shapes) do not carry it.
- `score` = `likeCount − dislikeCount`, the same value `sort=top` ranks by.
- Excluded automatically: answers to revoked questions, revoked answers, unresolved (orphan) answers. A row never appears with a hidden parent.

### Author page composition

| Tab | Call |
| --- | --- |
| Questions | `GET /api/qa/questions?publisher=<id>` (combinable with `tags`, `minAnswers`, `maxAnswers`, `sort=newest\|hot`) |
| Answers | `GET /api/qa/answers?publisher=<id>&sort=newest` |
| Best answers | `GET /api/qa/answers?publisher=<id>&sort=top` |

`publisher` accepts the profile's `globalMetaId` or `metaid` interchangeably (case-insensitive, exact). All three surfaces page with the same `cursor`/`hasMore` protocol.

## 3. Quick verification (production)

```bash
# newest answers feed
curl -sS 'https://so.metaid.io/api/qa/answers?size=5'
# best answers of one author
curl -sS 'https://so.metaid.io/api/qa/answers?publisher=<globalMetaId>&sort=top'
# comment thread of a question or answer pin
curl -sS 'https://so.metaid.io/api/qa/pins/<pinId>/comments?sort=newest'
# an author's questions
curl -sS 'https://so.metaid.io/api/qa/questions?publisher=<globalMetaId>'
# error contracts
curl -sS 'https://so.metaid.io/api/qa/answers?sort=hot'        # 40000 invalid sort
curl -sS 'https://so.metaid.io/api/qa/pins/notapin/comments'   # 40000 malformed pinId
```

## 4. Freshness and behavior guarantees

- New questions, answers, and comments are visible within one mempool relay (`isMempool: true`), replaced on confirmation. For optimistic UI, insert mempool rows immediately and reconcile by `pinId` on the next fetch.
- A comment's `modify` updates its content in place (same `pinId`, new `currentPinId`); `revoke` removes it from the thread and decrements the target's `commentCount`.
- `commentCount` on question/answer items = number of non-hidden comments (v2 semantics).
- Comment bodies are capped at 2000 runes at index time; long comments are truncated server-side, never client-side.

## 5. What did NOT change

- v1 request/response shapes: `/api/qa/search`, `/api/qa/questions` (only the optional `publisher` param was added), `/api/qa/questions/:pinId`, `/api/qa/questions/:pinId/answers`.
- Error codes, envelope, cursor encoding, CORS, no-auth.
- Full bodies of questions/answers remain behind `GET /api/metaweb/pin/:pinId`; comment full bodies are the exception — they are served directly by the comments endpoint.
