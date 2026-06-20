# Bootstrap Snapshot Guide

`metaso-p2p` bootstrap snapshots are the standard way to move a fully indexed
Pebble `dataDir` from one node to another without replaying the full history.
Use them when you want a portable, checked, operator-facing artifact instead of
an ad hoc directory copy.

## When To Use It

Use a bootstrap snapshot when you need to:

- seed a new node from an already indexed source node;
- hand off indexed Pebble state between hosts or operators;
- keep a standard artifact with a manifest and checksum verification.

Use a plain backup copy instead when you need a direct backup/rollback of the
same node's Pebble directory and you do not need packaging or manifest checks.

## Operator Rules

- The source node must be offline before you package its `dataDir`.
- Restore is replace-style, not merge-style.
- The host running `scripts/bootstrap-restore.sh` must have `python3` available.
- `--target-dir` must be a real directory path, not a symlink.
- Source and target should run the same `metaso-p2p` commit, or at least a
  known-compatible build.
- Source and target must be for the same network semantics.
- `--force` is only for replacing an existing non-empty target `dataDir`. It
  moves that directory aside before installing the snapshot.

## Artifact Contract

Pack output is a gzip tarball named like:

```text
metaso-p2p-bootstrap-<network>-<timestamp>.tar.gz
```

The archive root contains:

```text
manifest.json
checksums.txt
namespaces/
  indexer_meta/
  social/
  ...
```

`manifest.json` is parsed semantically with `python3` during restore. The
current restore contract requires:

- `schemaVersion`: integer, currently must be `1`
- `metasoVersion`: non-empty string
- `gitCommit`: string, must be empty or a full 40-character hex SHA (`[0-9a-fA-F]{40}`)
- `builtAt`: non-empty UTC RFC3339 timestamp in pack format `YYYY-MM-DDTHH:MM:SSZ` (for example `2026-06-20T12:34:56Z`)
- `network`: non-empty string
- `sourceNode`: non-empty string
- `dataDirFormat`: string, currently must be `pebble-per-namespace`
- `includedNamespaces`: non-empty list of namespace strings

By default `scripts/bootstrap-pack.sh` includes all top-level namespace
directories under the source `dataDir` except `cache_*`. Add `--include-cache`
only when you intentionally want cache namespaces inside the artifact.

## Pack A Snapshot

Run this on the stopped source node:

```bash
mkdir -p ./artifacts

scripts/bootstrap-pack.sh \
  --data-dir ./data/pebble \
  --output-dir ./artifacts \
  --network mainnet \
  --source-node prod-node-a
```

If you also need `cache_*` namespaces:

```bash
scripts/bootstrap-pack.sh \
  --data-dir ./data/pebble \
  --output-dir ./artifacts \
  --network mainnet \
  --source-node prod-node-a \
  --include-cache
```

The script prints the archive path on success.

Optional quick inspection:

```bash
tar -tzf ./artifacts/metaso-p2p-bootstrap-mainnet-<timestamp>.tar.gz
```

## Restore A Snapshot

Run this on the target node before starting `metaso-p2p`:

```bash
scripts/bootstrap-restore.sh \
  --archive ./artifacts/metaso-p2p-bootstrap-mainnet-<timestamp>.tar.gz \
  --target-dir ./data/pebble
```

What restore does:

1. unpacks the archive to a temporary directory;
2. requires `manifest.json`, `checksums.txt`, and `namespaces/`;
3. rejects unexpected archive-root entries, archive symlinks, and symlink
   targets before touching the target path;
4. verifies the `manifest.json` checksum entry, then validates the manifest
   contract semantically with `python3`;
5. verifies checksums and rejects undeclared or extra payload directories
   before touching the target path;
6. refuses a non-empty target directory unless `--force` is set;
7. copies the packaged namespace directories into the target `dataDir`.

To replace an existing non-empty target directory:

```bash
scripts/bootstrap-restore.sh \
  --archive ./artifacts/metaso-p2p-bootstrap-mainnet-<timestamp>.tar.gz \
  --target-dir ./data/pebble \
  --force
```

With `--force`, the current non-empty target directory is moved to a sibling
backup path before restore:

```text
<target-dir>.backup-<timestamp>
```

The script prints `backup: ...` when it creates that backup, followed by
`restored: ...` for the installed target path.

## Bootstrap Restore Vs Plain Backup Copy

Choose based on the job:

| Situation | Use |
|---|---|
| New host or new node needs a prebuilt indexed `dataDir` | Bootstrap snapshot |
| You want a standard artifact with `manifest.json` and checksum verification | Bootstrap snapshot |
| Same node needs a quick offline backup copy for rollback | Plain backup copy |
| You already control both source and target directories and do not need packaging metadata | Plain backup copy |

Plain backup example:

```bash
cp -r ./data/pebble ./data/pebble-backup-$(date +%Y%m%d)
```

Bootstrap restore does more checking and gives you an explicit artifact
boundary. Plain copy is simpler, but it is just a raw directory copy.

## Deployment Sequence

For a new node that should start from a prebuilt index:

1. Stop the source node.
2. Run `scripts/bootstrap-pack.sh` on the source host.
3. Move the tarball to the target host.
4. Run `scripts/bootstrap-restore.sh` on the target host.
5. Start `metaso-p2p` with `METASO_P2P_PEBBLE_DATA_DIR` pointing at that
   restored directory.
6. Verify `/healthz` and normal indexing progress.
