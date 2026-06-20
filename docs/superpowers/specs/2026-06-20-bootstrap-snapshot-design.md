# Bootstrap Snapshot Design

Date: 2026-06-20
Status: Approved design for implementation planning
Target repo: `metaso-p2p`

## Goal

Define a standard bootstrap snapshot artifact for `metaso-p2p` so one node can
build a fully indexed Pebble `dataDir` once, export it, and other nodes can
restore that snapshot and continue indexing from the stored chain heights
instead of replaying the full history again.

This round must ship:

- a stable bootstrap package layout;
- a pack script that exports a snapshot from an offline `dataDir`;
- a restore script that verifies and installs that snapshot into a target
  `dataDir`;
- deployment documentation that treats the snapshot as a standard operator
  artifact rather than an ad hoc backup copy.

## Non-Goals

- Do not implement live hot backup while the process is still writing Pebble.
- Do not implement cross-version database migrations.
- Do not implement partial merge restore into a non-empty target data
  directory.
- Do not add automatic remote distribution, upload, or registry publication for
  bootstrap artifacts.
- Do not change runtime indexing behavior; this is an operator workflow around
  the existing Pebble layout.

## Operator Problem

Today a new node can either:

1. start from an empty `dataDir` and replay chain history; or
2. receive a raw copied Pebble directory from another node.

Option 2 works in principle, but the repo lacks:

- a standard package structure;
- an explicit manifest that says what the snapshot contains;
- a safe restore flow;
- deployment documentation that explains when snapshot restore is valid and
  what compatibility checks operators must perform.

## Artifact Format

The bootstrap artifact is a compressed tarball:

```text
metaso-p2p-bootstrap-<network>-<timestamp>.tar.gz
```

The archive root contains:

```text
manifest.json
checksums.txt
namespaces/
  indexer_meta/
  userinfo/
  social/
  ...
```

### Why `tar.gz`

- available on standard Linux/macOS hosts without adding a new dependency;
- simple to inspect manually;
- easy to produce from shell scripts in the current repo.

Future work may add `tar.zst`, but this round deliberately keeps pack-side
artifact creation in standard shell tooling while accepting a restore-side
runtime dependency on `python3` for semantic manifest validation.

## Manifest Contract

`manifest.json` is required and must contain:

- `schemaVersion`: integer, starts at `1`;
- `metasoVersion`: string, from git commit or `dev` when unavailable;
- `gitCommit`: full commit sha when available, empty string otherwise;
- `builtAt`: UTC RFC3339 timestamp;
- `network`: operator-supplied filename-safe label matching
  `[A-Za-z0-9._-]+`, such as `mainnet`;
- `sourceNode`: free-form operator label for the node that produced the
  snapshot;
- `dataDirFormat`: fixed string `pebble-per-namespace`;
- `includedNamespaces`: ordered list of copied namespace directory names.

The manifest is descriptive metadata, not a schema migration mechanism.

Normal successful pack/restore output should also surface a stable
`manifest: {...}` summary line containing:

- `network`
- `sourceNode`
- `builtAt`
- `metasoVersion`
- `gitCommit`
- `includedNamespaces`

That keeps compatibility-relevant metadata visible in the operator workflow
without requiring manual archive inspection.

## Namespace Selection

The pack script must copy top-level Pebble namespace directories from the
source `dataDir`.

Default behavior:

- include all namespace directories except `cache_*`.

Optional behavior:

- `--include-cache` also includes `cache_*` namespaces.

Rationale:

- business/indexer namespaces are the durable bootstrap payload;
- cache namespaces are rebuildable and should stay optional to keep the default
  artifact smaller and less coupled to stale L2 cache state.

## Restore Semantics

Restore is defined only for an offline target node.

The restore script must:

1. unpack the archive to a temporary working directory;
2. require `manifest.json`, `checksums.txt`, and `namespaces/`;
3. require `python3` and use it to validate the manifest semantically after
   verifying the manifest checksum entry;
4. verify payload checksums before copying data into the target;
5. print the validated manifest summary in normal script output;
6. refuse to overwrite a non-empty target directory unless `--force` is set;
7. when `--force` is set, move the existing target directory to a timestamped
   backup sibling before installing the snapshot, but fail if that sibling path
   already exists so the reported backup path is the actual moved-aside root;
8. copy namespace directories exactly as packaged into the target `dataDir`.

This is a replace-style restore, not a merge.

## Compatibility Rules

Operators must treat a bootstrap snapshot as valid only when:

- source and target run compatible `metaso-p2p` code, ideally the same commit;
- source and target are indexing the same network semantics;
- source node was stopped, or the snapshot was produced from a filesystem-level
  offline snapshot.

The scripts should surface the manifest clearly, but hard compatibility policy
remains operational guidance for this round.

## Documentation Surface

The operator-facing workflow must live in deployment documentation, not only in
superpowers specs.

Ship:

- a dedicated bootstrap snapshot doc that defines the package and workflows;
- a concise section in `docs/DEPLOY.md` that links to that doc and explains
  when to use bootstrap restore vs plain backup copy.

## Verification

This round needs automated script-level coverage for:

- package creation with default namespace filtering;
- manifest generation;
- normal pack/restore manifest summary output;
- checksum verification during restore;
- semantic manifest rejection for unsupported `schemaVersion`;
- semantic manifest rejection for unsupported `dataDirFormat`;
- semantic manifest rejection for empty required non-empty metadata;
- semantic manifest rejection for malformed `includedNamespaces` entries;
- pack-side rejection for filename-unsafe `--network` labels;
- non-empty target refusal without `--force`;
- `--force` backup-and-replace restore behavior;
- forced-restore rejection when the timestamped backup sibling path already
  exists.

Shell-based tests are acceptable if they are deterministic and runnable from
this repo on the current machine.
