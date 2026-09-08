# MetaWeb Q&A R7 Downstream Integration Guide — Question-Mark Title Rule

Audience: IDBots / Q&A MetaApp frontend. Contract: `docs/specs/2026-09-08-metaweb-qa-question-title-rule.md` (R7). Production: `https://so.metaid.io`.

**There is no wire-format change.** Every endpoint, parameter, item shape, and error code is exactly as deployed in Q&A v1/v2. R7 only changes *which questions the Q&A surfaces carry*.

## The Rule

A SimpleQuestion pin is Q&A-indexed only when its **title, trimmed of trailing whitespace, ends with a question mark**:

- half-width `?` (U+003F) — typical for English titles;
- full-width `？` (U+FF1F) — typical for Chinese titles.

Any other ending (`.`, `!`, `。`, `！`, a bare statement, empty/missing title) keeps the pin out of the Q&A index. The pin itself remains valid on-chain data and is still fully readable through the generic surfaces (`GET /api/metaweb/pin/:pinId`, `GET /api/metaweb/search`).

## What the Frontend Will Observe

| Situation | Behavior on `/api/qa/*` |
| --- | --- |
| Question created without a trailing question mark | Never appears: not in `/api/qa/questions`, not matched by `/api/qa/search`, `GET /api/qa/questions/:pinId` → `40400`. Answers to it stay pending and invisible; comments on it are not served (`40400`). |
| A `modify` drops the trailing question mark | The question **and its answers** disappear from every Q&A surface immediately (feeds, search, detail, `/api/qa/answers`, comment threads — `40400` on the question and its answers). |
| A `modify` restores the question mark | The question and its whole subtree (answers, likes, comments) come back, unchanged. |
| Existing corpus | Re-evaluated server-side at deploy time (startup reconciliation). As of the 2026-09-08 rollout, all 9 indexed questions already carried a trailing `？`/`?`, so nothing dropped. |

A de-indexed question is indistinguishable from a revoked one on the wire: `40400`, same message family. There is no "exists but filtered" signal by design.

## What the Frontend Should Do

1. **Keep enforcing at authoring time** (already shipped on the IDBots side): the ask tool appends or requires a trailing `?`/`？`. The rule is a convention enforced from both ends; the index side is the safety net, not the UX.
2. **Trim before validating** client-side: `title.trimEnd().endsWith('?') || title.trimEnd().endsWith('？')` — mirrors the server check.
3. **For "where did my question go?" support flows**, check the generic pin read first:
   ```bash
   curl -s "https://so.metaid.io/api/metaweb/pin/<pinId>" | jq '.code, .data.title'
   ```
   If the pin reads fine but every `/api/qa/questions/<pinId>` call returns `40400`, the title lost (or never had) its question mark. A `modify` that re-adds the mark restores indexing — no data is lost in between.
4. **CJK authoring note**: full-width `？` is first-class; no client-side conversion to `?` is needed (or recommended — it changes the user's text).

## Verification (production)

```bash
# Indexed question (title ends with ？): detail returns code=0
curl -s "https://so.metaid.io/api/qa/questions?size=1" | jq '.data.items[0].pinId'
curl -s "https://so.metaid.io/api/qa/questions/<that pinId>" | jq '.code, .data.question.title'

# A de-indexed (no-mark) question pin: generic read works, Q&A detail 40400
curl -s "https://so.metaid.io/api/metaweb/pin/<noMarkPinId>" | jq '.code'
curl -s "https://so.metaid.io/api/qa/questions/<noMarkPinId>" | jq '.code'   # 40400
```

## Semantics Summary

- Rule scope: the Q&A projection only. On-chain validity, generic pin reads, and generic search are untouched.
- Evaluation point: index time (create, and every modify's effective post-merge title), plus a one-time startup reconciliation over previously indexed records at each deploy.
- Restore symmetry: visibility is derived from the current title, so re-adding the mark re-indexes everything; no engagement state is lost while de-indexed.
- Freshness, ordering, cursors, error codes (`40000/40400/50000`), envelopes: unchanged from v1/v2.
