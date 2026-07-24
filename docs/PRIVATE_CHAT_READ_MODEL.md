# Private-chat materialized read model

The `pchat:` records remain the source of truth. The v1 read model adds flattened, conversation-local records that trade disk space for predictable CPU and latency:

- `pchat-idx:v1:` stores a complete message snapshot by canonical conversation and continuous ordinal index.
- `pchat-time:v1:` stores the same snapshot by timestamp for reverse chronological pagination.
- `pchat-meta:v1:` stores `count`, `nextIndex`, and the latest timestamp for O(1) totals and write allocation.
- `pchat-loc:v1:` maps a pin ID to its source row, canonical conversation, timestamp, and index.
- `pchat-home:v1:` stores one complete latest-message snapshot per user alias and conversation.
- `pchat-read-model-state:v1` is the durable feature gate. Readers use the new keys only when version 1 is `ready`.

The model stores one canonical conversation index rather than separate A-to-B and B-to-A copies. The complete message is deliberately duplicated in the ordinal and time indexes: a 100-message response performs one bounded sequential iterator read and no per-message source lookup.

## Write invariants

After the read model is ready, a private-message write holds a canonical conversation lock and commits the source row, pin locator, conversation metadata, ordinal snapshot, time snapshot, home snapshots, and homepage indexes in one Pebble batch. `nextIndex` is read from the per-conversation metadata; existing history is never scanned.

A mempool message and its later confirmation remain one logical row. Confirmation preserves the original timestamp and index, updates the source row and both snapshots, and does not increment `count` or `nextIndex`.

## Backfill and verification

Build the maintenance command with the service binary:

```sh
CGO_ENABLED=0 go build ./cmd/metaso-p2p-privatechat-index-backfill
```

Stop every writer before running a production rebuild:

```sh
metaso-p2p-privatechat-index-backfill --data-dir /path/to/pebble --timeout 60m
metaso-p2p-privatechat-index-backfill --data-dir /path/to/pebble --timeout 60m --verify-only
```

The rebuild first marks the state `building`, clears only v1 derived prefixes, scans `pchat:` once, deduplicates messages, sorts each canonical conversation once, writes bounded batches, and verifies source accounting plus index/time/locator/home/conversation counts. It marks the state `ready` only after all checks pass.

The command is repeatable. A crash or failed check leaves `building`; a restarted service then keeps using the legacy records. The source `pchat:` rows are never rewritten or deleted by the backfill.

## Deployment and rollback

Rehearse against a consistent copy of the production `privatechat` and `userinfo` namespaces first. For the actual cutover, stop the service, run the verified backfill against the live data directory, install the new `main` binary, and restart it.

Rolling the binary back is sufficient because old binaries ignore all v1 keys and continue reading `pchat:`. The v1 keys can be retained for investigation or replaced by a later repeatable rebuild.
