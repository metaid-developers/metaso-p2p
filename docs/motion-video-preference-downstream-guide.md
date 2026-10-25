# Motion Video Preference — Downstream Integration Guide

- **Audience:** Arena / feed clients, IDBots retrieval, and bots consuming the
  Motion Video open-preference read models on production `so.metaid.io`
- **Contract source of truth:** [docs/specs/2026-10-24-motion-video-preference-api.md](specs/2026-10-24-motion-video-preference-api.md)
- **Live since:** 2026-10-25, release `079f9d2` (merge of `feat/motion-video-preference`)
- **Evidence basis:** open only (`basis=open`). Validated (sealed-round)
  aggregation is R7 and deliberately unavailable — see the error contract below.

## 1. Discovery and playback metadata (R1)

`GET /api/metaapp/list?chainName=mvc&tag=motion-video` and
`GET /api/metaapp/detail/:pinId?chainName=mvc` now carry an additive
`motionVideo` block on every Motion Video item (identified by
`metadata.series == "motion-video"` or a `motion-video` tag):

```json
{
  "pinId": "22ce6ca2…i0",
  "sourcePinId": "22ce6ca2…i0",
  "currentPinId": "22ce6ca2…i0",
  "chainName": "mvc",
  "motionVideo": {
    "videoFile": "metafile://726ba9b…i0",
    "videoSha256": "b8113ccc…f81674",
    "durationSec": 10, "fps": 24,
    "frames": null, "hasAudio": null, "loudnessLufs": null,
    "resolution": "1920x1080", "engine": "lemo-opuscar", "style": "ukiyoe",
    "learning": { "sourceIncluded": true, "hasPrompt": true, "status": "declared" }
  }
}
```

Rules clients must respect:

- `pinId`/`sourcePinId` is the stable app root — build `app` as
  `metaapp://<pinId>`; `currentPinId` is the exact latest folded package —
  build `version` as `metaapp://<currentPinId>`. The manifest display
  `version` string (e.g. `v1.0.0`) is NOT a pin id.
- `detail/:pinId` accepts a root or mid-chain version id and resolves the
  latest record — use it for deep-linked candidates.
- `null` fields mean **unknown**, never false/zero (`hasAudio: null` is not
  "silent"). Absent `motionVideo` means the app is not a Motion Video.
- `learning.status = declared` is a manifest-completeness claim only. Never
  present it as "source verified"; a `verified` status does not exist yet.
- `videoFile` may carry an optional filename extension (`.mp4`); strip or
  ignore the extension when comparing video identity — compare
  `videoSha256` instead.

## 2. Writing feedback (client-side obligations)

Feedback pins are ordinary `metaid.pin.write` creates on
`/protocols/preference/feedback` with `kind=comparison-batch` (see the shared
contract §4 for the payload). MetaSo-side behaviours that matter to clients:

- Only **confirmed** pins affect scores; mempool pins appear only with
  `includeMempool=1` and never change ordering while pending.
- Batches containing any `test:` category tag are diagnostic-only — verify
  with `includeTest=1` that a smoke batch landed, and never expect it in
  production retrieval.
- A signer who published (created or last-modified) either compared app is
  recorded as an `author-conflict` exclusion — Arena owners should expect
  their own judgments of their own works to be excluded from scoring.
- Schema errors invalidate the whole pin; an ArtifactRef that cannot be
  resolved against the indexed manifest excludes only that item (contract
  ruling A).

## 3. Preference-sorted listing (R4)

```
GET /api/metaapp/list
    ?chainName=mvc            # REQUIRED with sort=preference
    &tag=motion-video
    &sort=preference
    &basis=open               # default; validated is unavailable pre-R7
    &order=desc               # desc (default) | asc; null scores last both ways
    &minJudges=3              # optional; >0 excludes unjudged items
    &learning=declared        # optional: declared | incomplete
    &size=20&cursor=<opaque>
```

Every returned item carries both evidence summaries:

```json
"preference": {
  "open": {
    "score": 0.73, "scoreVersion": "motion-video-open-pref-v1",
    "state": "established", "comparisons": 18, "judges": 5,
    "lastComparedAt": 1792832839
  },
  "validated": { "score": null, "state": "unavailable" }
}
```

- `score` is `(wins + 0.5×ties + 1)/(n+2)` per `(chainName, videoSha256)`;
  `null` + `state:"unjudged"` means no evidence yet — treat as "explore
  me", never as low quality (decision D12).
- `state`: `emerging` (<3 distinct judges) / `established` (≥3).
- Deterministic order: score in `order`, then comparisons desc, update
  recency, app root id asc. The cursor is bound to the whole query
  fingerprint — changing any parameter (including `order` or `minJudges`)
  invalidates it with `40000 invalid cursor`.
- Calls without the new parameters behave exactly as before (no
  `preference` block, existing order).

## 4. Comparison evidence API (R5)

```
GET /api/preference/comparisons
    ?chainName=mvc            # required
    &category=motion-video    # required (only projection in v1)
    &basis=open               # required semantics; default open
    &subject=<videoSha256>    # optional 64-hex
    &outcome=preferred|rejected|all   # default all; non-all requires subject
    &includeMempool=0         # pending evidence only
    &includeTest=0            # test: batches only
    &includeExcluded=0        # conflict/artifact exclusions only, with reasons
    &size=20&cursor=<opaque>
```

Default view: scoring-eligible, confirmed, non-test items, newest first.
Items carry `feedbackPinId`, `isMempool`, `confirmedAt` (null while
pending), `observedAt`, exact `a`/`b` ArtifactRefs, `pick`, optional
`reason`/`dims`, `signerGlobalMetaId`, `client{artifact,mode}`; with a
subject filter each item adds `subject` + subject-relative `outcome`
(`preferred`/`rejected`; ties only appear under `outcome=all`).
`manifestUnverified: true` marks comparisons against a historical
(non-current) package version whose manifest MetaSo does not retain — bots
must fetch and validate the actual package before learning use (ruling B).

Bots retrieve positive examples with `outcome=preferred` and rejected
contrasts with `outcome=rejected`, both with `subject=<sha>`; reasons come
inline. This endpoint is not a global worst-list — do not use it to turn
unjudged works into negative examples.

## 5. Error contract

| Case | code | message |
|---|---|---|
| `basis=validated` (list or comparisons, pre-R7) | 40000 | `preference_validated_basis_unavailable` (stable; IDBots maps this to its readable unavailable state) |
| `sort=preference` without `chainName` | 40000 | `sort=preference requires chainName` |
| Invalid `sort`/`order`/`basis`/`minJudges`/`learning` values | 40000 | `invalid …` |
| Cursor from a different query fingerprint or garbage | 40000 | `invalid cursor` |
| Comparisons: missing `chainName`/`category`, bad `subject`, `outcome≠all` without subject | 40000 | specific message |
| Preference source not wired (server misconfig) | 50000 | `aggregation unavailable` — never silent null summaries |

## 6. Production verification commands

```sh
# playback projection on the two reference fixtures
curl -s "https://so.metaid.io/api/metaapp/detail/22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0?chainName=mvc" | jq .data.motionVideo
curl -s "https://so.metaid.io/api/metaapp/detail/5686db0dd51ac19ec60c4a02ff66bc51adcf0149b8484141b38e730c734d9bb4i0?chainName=mvc" | jq .data.motionVideo

# preference-sorted candidates
curl -s "https://so.metaid.io/api/metaapp/list?chainName=mvc&tag=motion-video&sort=preference&basis=open&order=desc" | jq '.data.items[] | {pinId, preference}'

# open evidence (empty until the first confirmed feedback pin lands)
curl -s "https://so.metaid.io/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open" | jq .

# stable pre-R7 validated error (both endpoints)
curl -s "https://so.metaid.io/api/preference/comparisons?chainName=mvc&category=motion-video&basis=validated"
curl -s "https://so.metaid.io/api/metaapp/list?chainName=mvc&sort=preference&basis=validated"

# after submitting a smoke batch (category contains test:motion-video-smoke)
curl -s "https://so.metaid.io/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&includeTest=1" | jq .
```

## 7. Operational notes

- `/protocols/preference/feedback` had zero on-chain history at release;
  live indexing covers everything from deploy time, no backfill needed for
  new pins. Re-running `metaso-p2p-preference-backfill` is idempotent if
  history ever needs materializing.
- Scores are rebuildable read-model values (`scoreVersion`
  `motion-video-open-pref-v1`); they are not chain truth and carry no
  humanity guarantee (decision D11).
