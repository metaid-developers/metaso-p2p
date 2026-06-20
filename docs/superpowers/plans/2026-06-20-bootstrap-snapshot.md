# Bootstrap Snapshot Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a standard bootstrap snapshot artifact for `metaso-p2p`, plus pack/restore scripts and deployment docs that operators can use to seed new nodes from a prebuilt Pebble `dataDir`.

**Architecture:** Keep the implementation operator-side and file-based. Ship two bash scripts under `scripts/` that package namespace directories from an offline Pebble `dataDir` into a `tar.gz` archive with `manifest.json` and `checksums.txt`, then restore that archive into a target `dataDir` with checksum verification and guarded overwrite semantics. Document the artifact as a first-class deployment input in `docs/`.

**Tech Stack:** Bash, GNU/BSD userland tools (`tar`, `find`, `cp`, `mv`, `shasum`), `python3` for restore-side semantic manifest validation, existing Pebble namespace layout, markdown docs.

---

## Review Status

This file is the approved execution guide for the bootstrap snapshot feature in
the `codex/social-follow-apis` worktree.

Final acceptance for this plan also treats two details as part of the operator
contract:

- restore hosts must provide `python3` because semantic manifest validation is
  deliberate runtime behavior, not an incidental local convenience;
- successful pack/restore runs must surface a stable `manifest: {...}` summary
  line containing `network`, `sourceNode`, `builtAt`, `metasoVersion`,
  `gitCommit`, and `includedNamespaces`.

## File Structure

- Create `scripts/bootstrap-pack.sh`
  - Export a bootstrap artifact from an offline Pebble `dataDir`.
- Create `scripts/bootstrap-restore.sh`
  - Verify and install a bootstrap artifact into a target `dataDir`.
- Create `scripts/bootstrap_test.sh`
  - Script-level regression coverage for pack/restore behavior.
- Create `docs/BOOTSTRAP.md`
  - Operator contract for artifact shape, generation, restore, and compatibility.
- Modify `docs/DEPLOY.md`
  - Link bootstrap snapshots into normal deployment and recovery workflows.

## Task 1: Ship Pack And Restore Scripts

**Files:**
- Create: `scripts/bootstrap-pack.sh`
- Create: `scripts/bootstrap-restore.sh`
- Create: `scripts/bootstrap_test.sh`

- [ ] Write the failing shell tests first for:
  - default exclusion of `cache_*`;
  - `manifest.json` creation;
  - restore checksum verification;
  - refusal to restore into a non-empty target without `--force`;
  - backup-and-replace behavior with `--force`.
- [ ] Run `bash scripts/bootstrap_test.sh` and confirm the initial failure is due
      to missing scripts/behavior.
- [ ] Implement `bootstrap-pack.sh` with:
  - `--data-dir`, `--output-dir`, `--network`, `--source-node`,
    `--include-cache`;
  - archive name `metaso-p2p-bootstrap-<network>-<timestamp>.tar.gz`;
  - `manifest.json`, `checksums.txt`, and `namespaces/` layout;
  - stable `manifest: {...}` and `archive: ...` success output.
- [ ] Implement `bootstrap-restore.sh` with:
  - `--archive`, `--target-dir`, `--force`;
  - checksum verification before restore;
  - semantic manifest validation with `python3`;
  - stable `manifest: {...}` success output before target mutation;
  - fail-on-non-empty-target unless `--force`;
  - timestamped backup of the previous target directory when forced.
- [ ] Re-run `bash scripts/bootstrap_test.sh` and confirm all cases pass.
- [ ] Commit:

```bash
git add scripts/bootstrap-pack.sh scripts/bootstrap-restore.sh scripts/bootstrap_test.sh
git commit -m "feat: add bootstrap snapshot scripts"
```

## Task 2: Document The Bootstrap Artifact Workflow

**Files:**
- Create: `docs/BOOTSTRAP.md`
- Modify: `docs/DEPLOY.md`

- [ ] Write docs that define:
  - what the bootstrap artifact is;
  - when to use it vs plain backup copy;
  - pack workflow from an offline source node;
  - restore workflow on a new node;
  - compatibility constraints and caveats.
- [ ] Update `docs/DEPLOY.md` so bootstrap snapshots appear in the main
      deployment/recovery flow rather than as an isolated note.
- [ ] Verify docs references and commands match the shipped script names and
      flags exactly.
- [ ] Commit:

```bash
git add docs/BOOTSTRAP.md docs/DEPLOY.md
git commit -m "docs: add bootstrap snapshot workflow"
```

## Task 3: Final Verification

**Files:**
- Verify only

- [ ] Run the bootstrap regression script:

```bash
bash scripts/bootstrap_test.sh
```

- [ ] Confirm script coverage includes semantic manifest rejection for:
  - unsupported `schemaVersion`;
  - unsupported `dataDirFormat`;
  - empty-string values for required non-empty metadata;
  - malformed `includedNamespaces` entries.

- [ ] Run the repo go tests most likely to catch unrelated breakage:

```bash
CGO_ENABLED=0 go test ./internal/indexer ./internal/storage ./internal/cache -count=1
```

- [ ] Check worktree cleanliness:

```bash
git status --short --branch
```

- [ ] Dispatch one fresh subagent for final holistic acceptance review over the
      branch after Tasks 1-2 are complete.
