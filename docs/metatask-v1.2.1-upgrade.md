# MetaTask replay engine upgrade to /protocols/metatask v1.2.1 (Go side)

Date: 2026-09-27 · Repo scope: `internal/metatask` + `cmd/metaso-p2p-metatask-indexer`

## Summary

The metaso-p2p MetaTask replay engine is upgraded from the pre-v1.2 freeze
semantics (v1.1-holdover calibration against the on-chain skill engine read
path) to the **v1.2.1 registration body** semantics — the Go side of the
three-engine consistency contract (Python skill / metaso Go indexer / IDBots
TS engine).

Authority chain (逐字以此为准):

- Registration body v1.2.1: `pin://cbae49e09697182b076652ae9bded629388a0dafc9048b01c6a7c8766247fc55i0`
- Conformance vector set (shared by all three engines):
  `internal/metatask/testdata/metatask-conformance-vectors.v1.2.1.json`,
  canonical-JSON sha256 `106aa1f3bee8ebd48339ceb65f97a12e54247e1e831296a394a974c9cb22f2c4`
- TS reference implementation (semantic reference for the port):
  `src/main/services/metatask/engine.ts` (IDBots worktree)

Activation constants: `H_ACT = 190000` (unchanged, already passed), `H_ACT2 =
191500` (v1.2.1 lowered it from 192000; expected to arrive 2026-09-29~30).

## Acceptance evidence (mechanically verified)

- `TestV121ConformanceVectors` — the shared 16-vector set (2 hash + 14
  replay) runs **16/16 green**. The vendored fixture is byte-checked
  (raw-file sha256) and its canonical-JSON sha256 is asserted against the
  announced value before any vector runs.
- `TestV121TSParityEventSetHashAndManifest` — for every replay vector, this
  engine and the compiled TS engine produce **byte-identical eventSetHash and
  settlement-manifest canonical bytes** (golden generated from the TS engine
  over the same fixture, stored in `testdata/v121-ts-golden.json`).
- The pre-existing calibrations still pass: skill D8 guard fixtures 13/13,
  engine guard fixtures 9/9, pool settlement vectors, HTTP endpoint tests.
- Full repo `go test ./...` green.

## What changed

### New engine: `internal/metatask/v121.go`

A faithful port of the TS reference engine (`replayMetaTask`), driving
everything else:

- **Unified ordered walk** — claims, releases, submissions and last-valid
  votes interleave in the total order `(height, txIndex, timestampMs,
  pinId)`. Ballot resolution: #8/#9 gates (`semantic_check`,
  `failreason`) filter BEFORE the last-valid-per-(target, bot) collapse, so a
  malformed ballot never withdraws the same bot's earlier valid one; the
  surviving ballot acts in the walk. An acting valid fail reopens the node
  immediately (ruling three), undoing a provisional quorum.
- **Supersede** (`paths.submission.supersedeRule`) — six predicates: target
  exists, same pin author, same taskid/node/claimid (same cycle), the new pin
  is the later chain tip, target not already superseded, target not
  verified; plus the height gate: both ends ≥ H_ACT2. Superseded submissions
  leave the effective pool (no contribution, no aggregation hash match, no
  vote counting, recorded in `unpaidHistory`).
- **/amend** (`paths.amend`) — publisher-only, H_ACT2-gated; `bases` version
  chain (stale vs conflict: two amends on the same bases → the on-chain
  earliest wins, others `amend_conflict`); point-in-time freeze on each
  node's first effective claim (`firstClaimOrder`); fold invariants (acyclic,
  single root, Σ weight = 10000, no orphan children) checked per fold, any
  failure ignores the whole amend; terminal ban once the task root is
  verified; `remove_node`/`reweight`/`retitle`/`respec` never touch frozen or
  disputed nodes. The effective amend head is carried in projections
  (`amendHead`).
- **/challenge** (`paths.challenge`) — H_ACT2-gated; validity gates (target
  is the current-cycle effective submission, author ≠ submitter/root author,
  non-empty evidence, at most one open challenge per author/target);
  `withdraw` resolves the author's open challenge; TTL
  (`policy.challenge_ttl_days`, default 14) expiry = withdrawal (derived
  from the evaluation clock, never a chain event); closed loop: a challenge
  stands only while its target still anchors a finally-verified node — an
  overturn via the fail path resolves it permanently. Open challenges mark
  nodes disputed and block settlement.
- **Aggregation precondition** (`paths.aggregationPrecondition`) — a parent
  is finally verified only when ALL children are finally verified AND its own
  submission passed AND the aggregate's canonical `childids`
  (`result.childids`, mirrored by top-level `childids`) correspond exactly
  to the children's effective verified submissions. Enforced when the
  parent's effective submission anchors at height ≥ H_ACT2; below it the
  recorded vote-level verdict stands (grandfather — pilot #01's root keeps
  `verified`; the divergence is disclosed in the registration body).
  **Task completion = root node verified.**
- **Settlement** (`settlement`) — pure replay output, produced only when the
  task completes with no open challenge:
  `shareBP(s) = floor(w × σ / 10000)`, `pool = w − shareBP(s)` (subtraction,
  no double rounding), Laplace reviewer accuracy
  `a(r) = clamp(floor(10000 × (correct+1)/(terminal+2)), 2500, 10000)`
  (floor 2500 is a protocol constant), reviewer split
  `floor(pool × a(r) / Σ a(r'))`, integer fixed-point with residue
  discarded; σ = `policy.split.submitterShareBP` clamped to [6000, 9000],
  default 8000; empty R(n) pool → submitter; legacy trees without weights
  settle uniformly at `floor(10000/N)` with the residue deliberately
  discarded (r14: 3 × 3333 = 9999). The manifest
  (`{taskid, boundaryBlock, eventSetHash, engineAlgoVersion, shares,
  unpaidHistory, disputed, weightsTableHash}`) carries the shared
  three-engine algorithm version `idbots-metatask-engine/1.2.1` and passes
  the self-check `shareBP = submittedBP + reviewedBP` before emission.
- **eventSetHash** (`settlement.eventSetHash`) — fixed path order
  [task, tree, spec, claim, release, submission, verify, amend, challenge];
  membership per the registry table (task = root pin; tree/spec only via
  `task.treeid`/`task.specid`; claim/release/submission/amend by `taskid`;
  verify/challenge by `targetid` → that submission's task; broken reference
  chains never enter the set); mempool excluded; in-path ordering by
  `(height, txIndex, pinId)`; ALL facts enter — ignored/invalid included;
  `eventSetHash = sha256(canonJ(E))`, `boundaryBlock = max height`.
- **Canonical JSON + double hash (Appendix A)** — `canonJ` reproduces
  Python's `json.dumps(ensure_ascii=False, sort_keys=True,
  separators=(',',':'))` byte-for-byte (integer literals verbatim, raw
  non-ASCII, Python escape set); `innerHash` deletes the top-level `hash`
  key shallowly, `outerHash` embeds it.

### Renderer: `internal/metatask/replay.go`

`Replay()` keeps the frozen §5 projection shape (field names are the API
contract) and now renders it from the v1.2.1 conclusion. `ReplayResult`
gains the additive v1.2.1 block (`taskComplete`, `eventSetHash`,
`boundaryBlock`, `settlement`, `ignoredEvents`, `disputedNodes`,
`amendHead`, `hAct2`); nodes gain `weight` and `disputed`. Protocol anchors:
`protocolVersion` 1.2.1, `replayAlgoVersion`
`metatask-replay-indexer/2.0.0+v1.2.1`.

Old-calibration realignments (v1.2.1 wins over the freeze draft):

| Freeze (pre-upgrade) behavior | v1.2.1 behavior |
| --- | --- |
| `verdict=invalid` sent the node back to open | occupies no fail slot, never triggers the send-back |
| below-H_ACT fail without evidence neither rejected nor blocked | any valid fail rejects immediately (ruling three) |
| review-window close ignored late votes | a late ballot that completes the quorum verifies; the window only reopens unmet-quorum cycles |
| release after quorum ended the cycle open/released | quorum is decisive — a concluded verified cycle survives a later release |
| aggregation gated parents at all heights | gated only when the parent's submission anchors ≥ H_ACT2; grandfathered below |
| second in-cycle submission ignored as `not_earliest_in_cycle` | `duplicate_without_supersede`, or a valid supersede chain at ≥ H_ACT2 |
| submission author had to equal the claim holder | submission validity rides the claimid (reference walk) |
| CycleSeq counted at claim | kept (rendered from effective claims) |

The old ignore-reason constants remain defined for history; the live set is
the reference engine's closed set plus the freeze names that still apply
(`superseded_by_earlier_claim`, `claimid_mismatch`, `state_no_op`,
`target_not_active`, `unresolvable_target`, `duplicate_vote_superseded`,
`voter_is_submitter`, `voter_is_task_author`, `verdict_invalid`,
`unknown_verdict`).

### Paths and endpoints

`EventPaths` now enumerates all nine protocol paths (amend + challenge
added); the HTTP server, the CLI and the projections pick them up
automatically (`/api/metatask/tasks`,
`/api/metatask/tasks/{rootPinId}`, `/api/metatask/tasks/{rootPinId}/nodes/{nodeId}`,
`/api/metatask/replay/{rootPinId}`). `replayMeta.semanticGate` echoes both
`hAct` and `hAct2`. The pool aggregation read path
(`aggregate.go`/`settle.go`, the vendored skill guard parity) is intentionally
untouched — it is a separate calibration module, not the v1.2.1 replay.

### Documented reference readings (kept for three-engine parity)

- `remove_node` deletes the named node only; descendants keep a dangling
  parent (the TS reference reading; the registry text says whole-subtree
  removal — no vector exercises the difference).
- A conflicted fail (voter = submitter / root author) still acts in the walk
  (identity filtering applies to pass votes only — reference parity).
- Challenge validity checks the target as the current-cycle effective
  submission (the `target_not_active_verified` reason name notwithstanding).
- ~~Roster same-side filtering requires the roster pin bodies via
  `V121Options.RosterPins` (not yet wired in the indexer's live collection;
  no roster task exists on chain yet).~~ **Wired 2026-09-29 (wave-1 parity):
  the indexer sweeps `/protocols/metatask-roster` as the tenth pool
  (`CollectionPaths`) and passes the decoded bodies into replay.**

## Wave-1 parity fixes (IDBots pre-launch audit, 2026-09-29)

Mirrored from the IDBots TS engine (already landed there) so the three
engines stay byte-identical on settlement manifests. Regression coverage:
`internal/metatask/v121_wave1_parity_test.go`.

- **B1 ghost-claim weight poisoning (critical).** A claim/release/submission
  pin whose node id is not in the task's effective (post-amend) tree is
  ignored with reason `unknown_node`: no node state, no `openNodes` entry, no
  `progress.total` inflation, no reach into the settlement weight table.
  Unfixed, one stray claim de-validated the tree weights (`len(effectiveTree)
  != progress.total`) and silently flipped a weighted task to the legacy
  uniform fallback. The engine walk now runs in two passes: pass 1
  (unfiltered) derives the amend-fold inputs — ghost events only ever touch
  ghost-keyed state, so the fold is identical either way — and pass 2 applies
  the membership filter and drives every projection. Legacy treeless tasks
  (no resolved tree pin) keep implicit node state, and the ghost pins stay in
  `eventSetHash` (they are facts of the event set; ignored ≠ dropped).
- **B2 reviewer-accuracy identity filter.** `reviewCorrect`/`reviewTerminal`
  (the Laplace accuracy `a(r)` feeding the reviewer pool) exclude votes where
  the voter is the submission author or the task root author — exactly as the
  settlement reviewer set R(n) already does. `reviewVotes` (participation
  count, never hashed) still counts them.
- **B3 same-side roster wiring.** The roster pool `/protocols/metatask-roster`
  (a flat sibling path, NOT under `/protocols/metatask/*`) is swept as the
  tenth pool; confirmed, payload-accepted, in-boundary roster bodies
  (`{groups: string[][]}`) are passed into replay. Votes at height ≥ H_ACT2
  from a voter sharing a roster group with the submitter or the root author
  are ignored (`same_side_roster`); pre-H_ACT2 votes are untouched, so
  pilot-task projections are unchanged. Replay results echo `rosterId` /
  `rosterResolved` (additive) so a declared-but-missing roster is visible
  rather than silently unfiltered. `SnapshotLister` serves frozen material
  that predates the pool as an honest empty page.
- `replayAlgoVersion` bumped to `metatask-replay-indexer/2.1.0+v1.2.1`
  (semantic change, D10 full replay). The manifest's `engineAlgoVersion`
  stays `idbots-metatask-engine/1.2.1` — it names the shared algorithm, not
  the implementation, and must stay byte-identical across engines.

The 16-vector conformance set and the TS golden parity harness remain green
unchanged: no vector exercises a ghost node, an identity-conflicted terminal
vote, or a roster pin, so the pinned eventSetHash/manifest bytes are
identical before and after the fixes.

## Files

- `internal/metatask/v121.go` — new: the v1.2.1 engine, canonJ/hashes,
  eventSetHash, settlement manifest.
- `internal/metatask/replay.go` — rewritten as the frozen-projection renderer
  over `ReplayV121`.
- `internal/metatask/types.go` — nine protocol paths, pool names.
- `internal/metatask/project.go` — `semanticGate.hAct2` echo.
- `internal/metatask/v121_conformance_test.go` — the 16-vector conformance
  harness (fixture sha256 pre-checks).
- `internal/metatask/v121_ts_parity_test.go` — TS-engine byte-parity harness.
- `internal/metatask/testdata/metatask-conformance-vectors.v1.2.1.json` —
  verbatim copy of the shared fixture (raw sha256
  `f8396fe28ccea2c20f075b845d662237127ce299fecad279f4d2e097b9df4c0c`).
- `internal/metatask/testdata/v121-ts-golden.json` — golden outputs from the
  compiled TS engine over the same fixture.
- `internal/metatask/replay_test.go` — conflicting freeze expectations
  realigned to v1.2.1 (see table above).
- `internal/metatask/http_test.go`, `engine_parity_test.go` — fixture loaders
  tolerate the two new (possibly absent) pools.

## Boundary

No protocol registration, vector-set pin or announcement was published from
this repo — the registry body and its announcements belong to the publisher.
This upgrade only brings the write-side indexer to the pre-announced
algorithm ("写入端先行" precondition of `activation.processRequirements`).

## v1.2.2 rulings (registration-author, approved 2026-09-29 — recorded for the future H_ACT3 implementation; NOT implemented here)

Wave-1 proceeds under v1.2.1 with the B-parity fixes (commit `6d7cdda`). The
following rulings land only with the future v1.2.1→v1.2.2 registration update
and its H_ACT3 announcement; this engine stays on v1.2.1 semantics until then:

1. **Amend fold time-anchor** — ADOPT the point-in-time reading: an amend
   published before root verification counts, later ones are ignored; the
   fold must also enforce the no-orphaned-children invariant. Effective from
   H_ACT3; no retroactive changes.
2. **Fail-vote identity gate** — ADOPT: §10.4 identity rules (voter ≠
   submission author AND ≠ task root author) apply to fail votes from H_ACT3.
   Historical tasks (incl. pilot #01's fail-then-pass) are never rewritten.
3. **§12 item 6 (submitter ≠ task root author)** — enforce READER-side from
   H_ACT2 (the published text already says so), closing the hole writer-side
   refusal cannot cover: existing on-chain self-submits.
4. **Mempool** — settlement manifests are produced only once every scoped
   event is confirmed (no height<0 pins in scope); the live walk keeps
   mempool events for display. (This engine currently excludes mempool pins
   from E entirely and counts them in `excludedMempool`; the display-side
   mempool walk is a v1.2.2 delta.)
5. **Settlement restriction** — manifests only for tasks published at/after
   H_ACT2; pilot #01's legacy uniform manifest is a historical artifact.
6. **engineAlgoVersion** — pinned per protocol version in the registration
   text with an implementation-neutral value (`metatask-engine/1.2.2` for
   v1.2.2); for v1.2.1 the de-facto pinned `idbots-metatask-engine/1.2.1`
   stays (this engine emits exactly that).
7. **Ghost-submission vote trace** — accepted into the v1.2.2 discussion
   list: verify pins targeting ignored ghost submissions currently leave no
   vote-level trace (they remain in EventCount/eventSetHash); extending
   `unresolved` semantics is a v1.2.2 drafting-thread topic.

Noted display-field divergence (no action, no hash/manifest impact): this
engine's `reviewVotes` stays terminal-only while the TS engine counts every
identity-valid vote for UI participation display.
