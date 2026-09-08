# MetaWeb Q&A: Question-Mark Title Rule (R7)

## Background

IDBots is turning "a question's title must be a question" into a de-facto convention for the on-chain Q&A layer (the ZhiHu/Quora house style): the IDBots client-side asking tool already enforces a trailing question mark, and the index side now applies the same rule so the two halves reinforce each other and the Q&A surfaces cannot degrade into a mixed feed.

Requirements source: IDBots `docs/metaweb-qa-backend-requirements-v2.md` §3b (R7). R7 is deliberately small and independent of R5/R6; it ships on top of the deployed v1/v2 Q&A contract (`docs/specs/2026-09-07-metaweb-qa-api.md`, `docs/specs/2026-09-07-metaweb-qa-comments-author-api.md`).

**No wire format, endpoint, or error-code changes.** R7 is purely an indexing/visibility rule.

## The Rule

Only SimpleQuestion pins whose **title, after trim, ends with a question mark** enter the Q&A index:

- half-width `?` (U+003F), or
- full-width `？` (U+FF1F).

Both ASCII-authored and CJK-authored titles are first-class. A title ending in other punctuation (`.`, `!`, `。`, `！`, …), a statement, or an empty/missing title does not qualify.

## Indexing Semantics

- **create** — a question whose trimmed title does not end with a question mark is **skipped** from the Q&A index, exactly like the existing missing/empty-title rule: no question record, no pin-map entry, invisible on every `/api/qa/*` surface. The pin itself stays valid on-chain data: the generic pipeline keeps it readable via `GET /api/metaweb/pin/:pinId` and searchable via `GET /api/metaweb/search`.
  - Consequence (same as empty titles today): answers targeting such a question stay in the pending index and never surface; likes/comments on it are not Q&A-indexed.
- **modify** — the rule is re-evaluated on the effective (post-merge) title:
  - A modify that **removes** the trailing question mark **de-indexes** the question: it leaves the newest/hot feeds (`qtime`), the search snapshot, and the question detail (`40400`), and — as with revoke — its answers leave the global answer listing (`atime`) and the comment-thread endpoint returns `40400` for the question and its answers. The question/answer records are retained.
  - A modify that **restores** the trailing question mark re-indexes the question and its whole subtree (answers, engagement, comments) — no state is lost while de-indexed.
  - A modify whose payload omits `title` keeps the previous title (existing rule), so visibility is unchanged.
- **revoke** — unchanged: revoke hides the question and its answers; the title rule is orthogonal.

Visibility is derived, not stored: a question is Q&A-visible iff it is not revoked **and** its current title ends with a question mark. All surfaces (feeds, hot, search, detail, per-question answers, comment threads, the cross-question answer listing and its embedded question) gate on this single predicate, driven by the same time-index maintenance that revoke already uses.

## Existing Corpus (upgrade path)

The corpus is small, so the chosen mechanism is a **startup reconciliation**: on boot (before serving), the aggregator re-evaluates every visible-questions index entry; entries whose record no longer qualifies — a pre-R7 question whose title lacks a question mark — are dropped together with their answers' global-list entries, and the warm search snapshot is rebuilt under the rule anyway. This runs inside the service at startup (no Pebble-lock contention with a running process) and is idempotent.

`metaso-p2p-qa-backfill` remains the belt-and-braces alternative (it replays the full protocol history through the same read model and converges to the same state); it is only needed if history is suspected to be missing from the local store, not for the title rule itself.

## Surface Impact

| Surface | Effect of a non-question-mark title |
| --- | --- |
| `GET /api/qa/questions` (newest/hot) | question absent |
| `GET /api/qa/questions/:pinId` (+ `/answers`) | `40400` (any version pin id) |
| `GET /api/qa/search` | never matched |
| `GET /api/qa/answers` (incl. `publisher=`, `sort=top`) | answers of the question absent; no embedded question rows |
| `GET /api/qa/pins/:pinId/comments` | `40400` for the question and its answers |
| `GET /api/metaweb/pin/:pinId` | unaffected — the pin stays readable (generic pipeline) |
| `GET /api/metaweb/search` | unaffected — generic search still finds the pin |

Error codes stay `40000/40400/50000`; a de-indexed question is indistinguishable from a revoked one on the wire.

## Non-goals

- No title rewriting, trimming beyond trailing whitespace, or suggestion/validation write APIs — the IDBots client enforces the convention at authoring time; the index only filters.
- No new query parameter, item field, or error code.
- No retroactive on-chain invalidation — the pin is only filtered from the Q&A projection.
