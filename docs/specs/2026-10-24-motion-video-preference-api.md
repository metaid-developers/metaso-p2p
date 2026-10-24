# Motion Video Playback Metadata and Open Preference Read Models

- **Status:** Implemented on branch `feat/motion-video-preference` (first delivery: R1–R6)
- **Applies to:** `/api/metaapp/list`, `/api/metaapp/detail/:pinId`, `/api/preference/comparisons`
- **Source requirement:** IDBots coordination package `docs/design/motion-video-preference/` (README, protocol-and-data-contract, metaso-requirements, acceptance-plan G4/E4/E5), version `119a0b86`
- **Scope guard:** open evidence only (`basis=open`). Validated (sealed-round) aggregation is R7 and deliberately unavailable; no MetaTask indexer changes, no video/source download, no byte hashing, no reward model.

## 1. Motion Video list projection (R1)

`MetaAppItem` gains an additive `motionVideo` block, emitted when the folded
manifest self-identifies as Motion Video (`metadata.series == "motion-video"`
or a `motion-video` tag). Values come from the current folded manifest only —
no second manifest index is maintained.

```json5
{
  "motionVideo": {
    "videoFile": "metafile://<pin>[.optional-extension]",
    "videoUrl": "https://<optional>",
    "videoSha256": "<64 lowercase hex>",
    "durationSec": 47.216,     // null when absent
    "fps": 24,                 // null when absent
    "frames": 1133,            // null when absent
    "resolution": "1920x1080",
    "hasAudio": true,          // null when unknown — never false by default
    "loudnessLufs": -14.6,     // null when absent
    "engine": "lemo-opuscar",
    "style": "silent-film",
    "learning": {
      "sourceIncluded": true,
      "hasPrompt": true,
      "status": "declared"    // or "incomplete"; "verified" is never emitted
    }
  }
}
```

Rules:

- `pinId`/`sourcePinId` stay the stable app root; `currentPinId` the exact
  latest folded version; `disabled` keeps its existing meaning. `detail`
  still resolves root or mid-chain version ids to the latest record.
- `learning.status = declared` exactly when the manifest carries every §2.2
  playback declaration (`series` equal to `motion-video`, well-formed
  `videoFile`/`videoSha256`, positive `durationSec`/`fps`, non-empty
  `resolution`/`engine`/`style`), `metadata.sourceIncluded === true`, and the
  root `prompt` is non-empty after trimming; otherwise `incomplete`. This is
  manifest completeness, not package verification.

## 2. Open comparison feedback aggregator (R2)

New module `internal/aggregator/preference` (storage namespace `preference`,
record keys under the `open:` prefix so any future validated projection is
physically separate):

- Consumes **create** pins on `/protocols/preference/feedback`; `modify` and
  `revoke` are ignored on chain.
- `comparison-batch` schema validation is atomic (§4.2 vectors): one invalid
  item marks the whole pin `malformed`; the pin is stored for audit only.
- Batches without a `motion-video` category are `unsupported` (outside the
  v1 projection, not malformed). Batches carrying any `test:` tag are
  diagnostic-only (`includeTest=1`).
- ArtifactRefs resolve through the publishedcontent metaapp store
  (`MetaAppResolver`): app root must resolve on the ref's chain, the version
  pin must belong to the same modify chain, and when the version is the
  current folded package the `video` (extension-insensitive) and
  `videoSha256` must match the manifest claim. Historical versions keep
  their evidence queryable when a new video hash starts a fresh aggregate.
  Resolution failures exclude that item (reasons: `app-not-found`,
  `version-outside-app`, `artifact-mismatch`, `chain-mismatch`) without
  discarding unrelated valid pairs in the batch.
- A signer who published either compared app version is recorded as a
  conflict exclusion (`author-conflict`).
- Only confirmed feedback changes scoring state. Mempool pins are stored
  pending (`isMempool`), surfaced only with `includeMempool=1`, and
  confirmation replaces the pending record. Duplicate deliveries are
  idempotent by pin id.
- Open scoring keeps only the latest valid comparison per `(chainName,
  signer, unordered videoSha256 pair)` by confirmed chain order
  (height, timestamp, pin id, item index).

## 3. Open preference score (R3) — `motion-video-open-pref-v1`

Per `(chainName, videoSha256)` from the latest-per-signer/pair set:

```text
n     = eligible comparisons involving the video
wins  = comparisons won; ties = comparisons tied
score = (wins + 0.5×ties + 1) / (n + 2)      // null when n = 0
state = unjudged (n=0) | emerging (<3 judges) | established (≥3 judges)
```

Unjudged identities return the exact zero-evidence summary
`{score:null, scoreVersion, state:"unjudged", comparisons:0, judges:0,
lastComparedAt:null}`. No Elo / Bradley–Terry / time decay / opponent
strength. Scores are a rebuildable read-model projection.

## 4. `/api/metaapp/list` preference mode (R4)

New parameters (all additive; calls without them keep current behavior):

| Parameter | Values | Notes |
|---|---|---|
| `sort` | `preference` | switches to score ordering |
| `order` | `desc` (default), `asc` | null scores last in both directions |
| `basis` | `open` (default) | `validated` → `code=40000`, message `preference_validated_basis_unavailable` (pre-R7) |
| `minJudges` | int ≥ 0 | filters by distinct judges; >0 excludes null scores |
| `learning` | `declared`, `incomplete` | filters `motionVideo.learning.status` |

- `sort=preference` requires explicit `chainName` (never blends chains).
- Deterministic order: score in `order`, comparisons descending, update
  recency, app root id ascending. Cursor is an offset bound to a fingerprint
  of chain+basis+order+filters+scoreVersion; changing any of them returns
  `40000 invalid cursor`.
- In preference mode every item carries both evidence summaries;
  `preference.validated` is exactly `{score:null, state:"unavailable"}`.

## 5. `GET /api/preference/comparisons` (R5)

Required: `chainName`, `category=motion-video`, `basis=open` (default).
Optional: `subject` (64-hex videoSha256), `outcome=preferred|rejected|all`
(default `all`; non-`all` requires `subject`; ties only in `all`),
`includeMempool`, `includeTest`, `includeExcluded` (all default 0), `size`
(20 default, 100 max), `cursor`.

Default view: scoring-eligible, confirmed, non-test items, newest confirmed
first. Items carry `feedbackPinId`, `chainName`, `basis`, `isMempool`,
`confirmedAt` (null while pending), `observedAt`, exact `a`/`b`
ArtifactRefs, `pick`, optional `reason`/`dims`, `signerGlobalMetaId`,
`client`; `subject`+subject-relative `outcome` when filtered;
`excluded`/`exclusionReason` only in the includeExcluded view;
`manifestUnverified: true` marks comparisons involving a historical
(non-current) package version whose manifest is not retained.

## 6. Backfill and replay (R6)

- `cmd/metaso-p2p-preference-backfill` replays the MANAPI history of
  `/protocols/preference/feedback` oldest-first through the read model
  (idempotent; per-chain fetched/applied/skipped/errors report). MetaTask
  indexer untouched; projections stay chain-rebuildable.
- A single pin replay failure is counted, skipped and retried on the next
  run (full idempotent rescan, no watermark to strand); the command exits
  non-zero with the failure list so operators see it.
- After each run, stored exclusions are re-evaluated against the current
  metaapp index (self-heal for feedback that arrived before its app) and
  the score snapshot rebuilds once for the whole batch (dirty-flag lazy
  rebuild — N confirmed pins cost one rebuild, not N).
- The same self-heal re-evaluation runs at service startup.

### Operational constraints

- **Service wiring:** `cmd/metaso-p2p/main.go` registers the preference
  aggregator after publishedcontent and wires both directions
  (`SetMetaAppResolver` before `Init`, `SetPreferenceLookup` after a
  successful register). `sort=preference` fails with a 50000 error if the
  score source is missing — it never silently degrades to unjudged items.
  `cmd/metaso-p2p/main_test.go` and the full-router fixture in
  `internal/api/router_test.go` guard this wiring.
- **Exclusive Pebble access:** the backfill command opens the same data
  directory, so it must run while the main service is stopped (maintenance
  window), same as the existing qa/metaweb backfill commands.
- **Ordering:** when bootstrapping, run the metaapp (publishedcontent /
  metaweb) backfill first — ArtifactRef validation resolves against the
  indexed metaapp records. Out-of-order runs self-heal at the next
  preference backfill or service restart.

- Fixture coverage: both real Motion Video manifests (Ukiyo-e #001 without
  extension/audio fields, Silent Film #002 with `.mp4`/audio fields), all
  §8 conformance vectors, mempool deferral, conflict exclusion, version
  boundaries, cross-chain isolation, validated-unavailable, cursor
  stability, replay determinism, exclusion self-heal, creator-conflict and
  lazy-rebuild regression guards.

## 7. Deviations / interpretations (for coordination review)

| # | Topic | MetaSo behaviour | Status for coordination |
|---|---|---|---|
| 1 | `client.artifact` | syntactic `metaapp://<pin>` check only; MetaSo does not promise the client root is published/indexed | Accepted by review; needs recording in the shared contract |
| 2 | ArtifactRef/manifest inconsistency | schema errors stay whole-pin atomic; artifact inconsistencies (app-not-found, version-outside-app, artifact-mismatch, chain-mismatch) exclude the item, unrelated pairs stay eligible. Switch point: `artifactInvalidatesBatch` in `internal/aggregator/preference/process.go` flips to whole-pin invalidation if coordination rules otherwise | **Needs a contract decision** — amendment requested |
| 3 | Historical version refs | recorded as stated, surfaced with `manifestUnverified: true`; never presented as manifest-verified | Implemented per review suggestion B; amendment requested |
| 4 | Missing current-manifest video declarations | treated as `artifact-mismatch` (not comparable ≠ implicit pass) | Implemented per review item C |
| 5 | Author conflict | checks folded publisher AND retained creator identity; intermediate historical modifiers are approximated by the folded publisher (per-version publisher history not retained) | Implemented per review item D; approximation declared |
| 6 | Pair chain scoping | a/b must share the feedback pin's chain; cross-chain pairs excluded as `chain-mismatch` | Accepted by review |
| 7 | Validated basis | fixed `40000 preference_validated_basis_unavailable` pre-R7 | Accepted by review |
