# GlobalMetaID Prefix Query API Design

Status: implemented on `main`; not deployed.

## Decision

Add a local-only prefix resolver to the existing `userinfo` aggregator. Keep
the current exact GlobalMetaID reverse index unchanged and add one
creation-time-ordered secondary key per indexed GlobalMetaID.

The current Pebble architecture supports this without a database replacement,
a profile schema migration, or changes to the chain adapters. The required
work is limited to a small userinfo index, a resumable one-time backfill, the
read endpoint, and tests/benchmarks.

## API contract

### Request

```http
GET /api/info/globalmetaid?prefix=idq1w8ye
```

The compatibility mount is also available because the userinfo aggregator is
already mounted under both API prefixes:

```http
GET /metafile-indexer/api/info/globalmetaid?prefix=idq1w8ye
```

The existing exact endpoint remains unchanged:

```http
GET /api/info/globalmetaid/:globalMetaId
```

#### Query parameters

| Parameter | Required | Rules |
| --- | --- | --- |
| `prefix` | yes | Trimmed and normalized to lowercase; 8 to 90 ASCII characters; must start with a valid ID-address header (`idq1`, `idp1`, `idz1`, `idr1`, `idy1`, or `idt1`); characters after the header must belong to the ID-address base32 charset. |

Eight characters is the minimum. `idq1w8y` contains seven characters and is
rejected; `idq1w8ye` contains eight and is accepted. Prefixes longer than eight
characters are also accepted. Very broad scans such as `id` and `idq1` are
rejected.

### Successful response

The endpoint uses the existing `/info/*` response convention, where business
success is `code=1` and the HTTP status is 200.

```json
{
  "code": 1,
  "data": {
    "globalMetaId": "idq1w8ye5psdkqrn6ugxxwvf5p4kkeuzufa6n9tt47"
  },
  "message": "",
  "processingTime": 1
}
```

Only the canonical GlobalMetaID is returned. Profile hydration is deliberately
excluded from this endpoint so the hot path does not read profile JSON or call
a remote profile service. A client that needs profile data can pass the result
to the existing exact endpoint.

### Error responses

Errors retain the existing `/info/*` HTTP-200 envelope convention.

| Code | Message | Condition |
| --- | --- | --- |
| `40000` | `valid globalMetaId prefix is required` | Missing, shorter than 8, longer than 90, or invalid characters/header. |
| `40400` | `globalMetaId not found` | The ready local index has no matching GlobalMetaID. |
| `50000` | `globalMetaId lookup failed` | Pebble read/iterator failure. |
| `50300` | `globalMetaId prefix index is not ready` | Initial backfill has not completed. This prevents incomplete data from being presented as a real not-found result. |

## Result ordering

“Earliest created” means the earliest root MetaID `create` or `init` PIN
(`path=/`) for the canonical GlobalMetaID. Live indexing accepts this record
from confirmed block dispatch; historical indexing accepts it from MANAPI's
persisted path-list contract.

Creation order is determined by:

1. normalized positive PIN timestamp in milliseconds, ascending;
2. chain name, ascending, when timestamps are equal;
3. genesis height, ascending;
4. root PIN ID, ascending;
5. GlobalMetaID, ascending.

The fields after the timestamp are deterministic tie-breakers, not substitutes
for a missing timestamp. A root PIN without a positive timestamp is recorded
as a backfill/data-quality error and is not made queryable until repaired.
Mempool identities are not exposed by this resolver. This keeps results stable
across eviction, replacement, and later block confirmation.

The resolver is local-only. Unlike the existing exact profile endpoint, a
prefix miss must not fan out to `METASO_P2P_PROFILE_REMOTE_BASE_URL`, because
the current remote contract supports exact identity lookup only and cannot
prove that a returned record is globally the earliest prefix match.

## Existing architecture fit

`internal/aggregator/userinfo` already persists:

```text
profile:<metaid>                 -> UserProfile JSON
globalmetaid:<lower-global-id>   -> metaid
address:<lower-address>          -> metaid
```

`internal/storage.PebbleStore` already exposes bounded prefix iteration with
Pebble `LowerBound` and `UpperBound`. Therefore prefix seek is supported by the
current storage engine. A request-time scan of `profile:*` is neither required
nor acceptable.

The current `globalmetaid:*` keys are lexicographically ordered by GlobalMetaID,
not by creation time, and their values contain only `metaid`. Returning their
first prefix match would be incorrect. Scanning all matches and loading extra
metadata would be logically correct after adding metadata, but its latency
would grow with the number of collisions. The secondary index below makes the
minimum eight-character lookup stop at its first Pebble record.

## Secondary index

Use a fixed bucket width equal to the minimum accepted prefix length:

```text
globalmetaid-created:v1:<full-global-id>
  -> {globalMetaId, metaid, createdAt, chainName, genesisHeight, pinId}

globalmetaid-prefix:v1:<first-8-chars>:<created-at-sort>:<chain>:<height-sort>:<pin-id>:<full-global-id>
  -> {globalMetaId, metaid}

globalmetaid-prefix-state:v1
  -> {status, cursor, indexedCount, duplicateCount, replacedCount,
      invalidCount, missingTimestampCount, updatedAt}
```

`created-at-sort` is fixed-width unsigned hex. `height-sort` uses a signed-order
offset before fixed-width hex encoding. In both cases byte order equals
ascending numeric order. The value contains the identity fields so the resolver
does not need a second Pebble read or parse variable key suffixes.

There is one prefix key per GlobalMetaID, not one key per possible prefix.

Query algorithm:

1. normalize and validate `prefix`;
2. require index state `ready`;
3. seek the bucket `globalmetaid-prefix:v1:<prefix[0:8]>:`;
4. iterate in creation order;
5. return the first value whose `globalMetaId` starts with the complete input
   prefix;
6. return `40400` after the bucket is exhausted.

For an eight-character input, the first bucket record is the answer. Longer
inputs may inspect more records in the same eight-character bucket, but never
scan unrelated identities or profile values. Complexity is `O(log N + K)`,
where `K` is the number of chronologically earlier records in that one bucket.

## Write path and consistency

Confirmed `userinfo.HandleBlockPin` processing of a root PIN should upsert the
creation record after it has resolved the canonical GlobalMetaID. If an earlier
root record is replayed later, delete the old prefix key and write the earlier
one. A later duplicate must be ignored.

The creation record and its prefix key must be updated in one Pebble batch.
This requires only a narrow batch helper or direct use of the namespace DB
returned by `OpenDB`, a pattern already used by other aggregators in this
repository. The existing `globalmetaid:<full-id> -> metaid` key and exact query
behavior remain wire-compatible.

`HandleMempoolPin` may continue updating the live profile read model, but it
must not write the confirmed prefix index. Remote profile completion must also
not create prefix entries because its response does not contain authoritative
root-PIN creation evidence.

## Existing data backfill

The current userinfo backfill only requests `/info/*`; it does not request root
`/` PINs. Existing `UserProfile` values also do not store identity creation
time. Consequently, the new endpoint cannot be correct for historical users
until a one-time root-PIN backfill completes.

The dedicated `cmd/metaso-p2p-globalmetaid-prefix-backfill` command:

1. pages the configured MANAPI path-list source with `path=/`;
2. accepts root `create/init` PINs with canonical valid GlobalMetaIDs and positive
   timestamps;
3. upserts the earliest creation record idempotently;
4. persists the upstream cursor and counters after every committed batch;
5. resumes from the stored cursor after interruption;
6. marks `globalmetaid-prefix-state:v1` as `ready` only after the source is
   exhausted;
7. reports indexed, duplicate, replaced, invalid, and missing-timestamp counts.

The endpoint returns `50300`, not `40400`, while the state is absent or still
`building`. New confirmed root PINs can be indexed concurrently; the earliest
upsert rule makes replay order irrelevant.

The production MANAPI contract was sampled on 2026-07-20. It returned
`globalMetaId`, `metaid`, `address`, `timestamp`, `genesisHeight`, `chainName`,
`operation`, and `id`. The sampled MVC rows used `genesisHeight=-1`, so a
positive normalized timestamp is authoritative and height remains only a
deterministic tie-breaker. The response also contained `modify` and `read`
operations on `/`; these are rejected by the backfill. If a future row omits a
canonical GlobalMetaID, the implementation attempts to derive it from the root
owner address using `pkg/idaddress`; it never invents a creation timestamp.

## Performance and operational gates

The v1 endpoint should use Pebble only: no full profile scan, profile JSON
decode, remote HTTP call, or chain RPC. Do not add a response cache initially;
the ordered index is already the cache-friendly read model, while cached
prefixes would need invalidation whenever an earlier identity appears.

Release gates:

- benchmark 100 thousand and 1 million indexed identities;
- include random eight-character prefixes, longer prefixes, misses, and a
  deliberately dense eight-character bucket;
- proposed target on production-class storage: warm p95 at or below 5 ms and
  p99 at or below 20 ms under 100 concurrent readers;
- verify the request performs no reads under `profile:` and no outbound HTTP;
- expose indexed/skipped counts and keep the endpoint unavailable until the
  backfill state is `ready`;
- smoke-test that an intentionally lexicographically later but chronologically
  earlier GlobalMetaID wins.

The latency numbers are acceptance targets and must be validated by benchmark;
they are not claims about the current deployment.

Branch verification on 2026-07-20 used an intentionally dense single bucket
and the following command:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/userinfo \
  -run '^$' \
  -bench '^BenchmarkLookupGlobalMetaIDPrefix$' \
  -benchtime=100x \
  -count=1
```

On the local Apple M1 Pro host, the 100-thousand-record case measured
`32143 ns/op` and the 1-million-record case measured `28174 ns/op`. These are
average Go microbenchmark results for the minimum eight-character lookup, not
p95/p99 production measurements. Production concurrency and disk latency still
need to pass the deployment gate above.

## Tests

Required coverage:

- normalization and all valid ID-address version headers;
- missing, too-short, too-long, and invalid-character prefixes;
- single match and exact-full-ID query through the prefix endpoint;
- multiple matches return the earliest timestamp, independent of GlobalMetaID
  lexical order and ingestion order;
- deterministic equal-timestamp tie-breakers;
- longer prefix filters within an eight-character bucket;
- no match returns `40400` only when the index is ready;
- building/missing state returns `50300`;
- mempool and remote-only profiles are excluded;
- missing-timestamp roots are counted but not exposed;
- replay is idempotent and an earlier replay atomically replaces a later key;
- backfill cursor resumes without duplicate visible entries;
- both `/api` and `/metafile-indexer/api` mounts expose the same contract;
- Pebble benchmark and dense-bucket benchmark.

## Implementation scope

Touched areas:

- `internal/aggregator/userinfo`: validation, route/handler, index record,
  confirmed write path, lookup, and unit tests;
- `cmd/metaso-p2p-globalmetaid-prefix-backfill`: resumable historical build;
- `README.md` and API documentation.

No expected changes:

- chain RPC/indexer interfaces;
- existing exact GlobalMetaID API response;
- `UserProfile` public JSON schema;
- socket, federation, chat, skill-service, or Bot Homepage modules;
- database technology or deployment topology.

## Alternatives rejected

1. Scan all `profile:*` values on every request: linear, JSON-heavy, and already
   known to be unsuitable for hot paths.
2. Return the first existing `globalmetaid:<prefix>` key: fast but returns
   lexicographic order, not earliest creation.
3. Scan all existing GlobalMetaID matches and compare separate creation
   records: small code change, but collision-driven latency is avoidable.
4. Materialize every prefix length for every identity: constant-time reads but
   multiplies storage and write amplification by the GlobalMetaID length.
5. Ask the remote profile service at request time: its verified contract is an
   exact lookup, so it cannot determine the earliest prefix match.
