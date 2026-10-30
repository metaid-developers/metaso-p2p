# Social Friend & Block API Specification

Date: 2026-10-29
Status: Draft specification — the single implementation basis for the friend
aggregation work (S2) and the Simpleblock block aggregation work (S3).
Style and transport conventions follow `2026-06-20-social-follow-api.md`.
This document does not change any shipped endpoint except where explicitly
noted (Section 4.7).

Author note: all on-chain findings and code references in Sections 1–3 were
verified against the repository at main `ed735cd` (2026-10-29) and against
live MANAPI queries on 2026-10-29. Nothing in this document is speculative
on-chain data. All identifier strings used in the examples of this document
(`idq123`, `idq456`, `idqSource`, `idqTarget`, the repeating-hex strings) are
obvious placeholders in the style of `2026-06-20-social-follow-api.md` —
they are not valid IDAddresses or metaids and must never be copied into
implementations or tests as if they were real. The only real-format samples
in this document are quoted from MAN's `GLOBALMETAID.md` in Section 2.1.

## 0. Document Role

This is the pre-development basis document. S2 (friend aggregation:
friends list, counts, batch relationship, normalization) and S3 (Simpleblock
aggregation and the unified filter layer) must be implemented exactly as
specified here. Where this document is silent, follow the shipped conventions
of `2026-06-20-social-follow-api.md`.

Sections:

1. Block protocol on-chain conventions (verified findings and the
   compatibility decision)
2. Dual-identifier normalization rules (legacy 64-hex metaid and
   GlobalMetaID)
3. New `/follow` writes use GlobalMetaID — convention and legacy-client
   compatibility
4. New API contracts (routes, parameters, response examples, error codes,
   pagination)
5. In scope / out of scope
6. Open questions for the owner

## 1. Block Protocol: On-chain Conventions (Verified)

### 1.1 The registered protocol

`/protocols/simpleblock` is registered in the MetaID protocol registry:

| Field | Value |
| --- | --- |
| Path | `/protocols/simpleblock` |
| Registrant | era |
| Registered | 2026-01-15 |
| Registration pin | `f7eb6b69e2c56b433eecd03077ee30789094c5eeaed38b61f593f504ccfa55dbi0` |
| Payload | `{"to":"{metaid}","blockState":1}` / `{"to":"{metaid}","blockState":-1}` |

Semantics:

- `to` — the blocked peer identity string.
- `blockState = 1` — block (or re-affirm a block).
- `blockState = -1` — unblock (lift an existing block).

An unblock is expressed as a **new `create` pin with `blockState = -1` on the
same path**, not as a `revoke` operation. This differs from `/follow`, where
revocation points at the original pin. Implementations must fold
`simpleblock` pins by (from, to) pair in pin order, last write wins.

### 1.2 The in-code compatibility path

`internal/aggregator/privatechat/process.go` already recognizes a second
path spelling:

- `internal/aggregator/privatechat/process.go:26-29` defines
  `SimplePrivateBlock{To string; BlockState interface{}}` — the same payload
  shape as the registered protocol.
- `internal/aggregator/privatechat/process.go:148-168`
  (`handlePrivateBlock`) parses `/private/block/simpleprivateblock` pins but
  currently only logs and returns (an empty shell; no read model is written).

### 1.3 Zero on-chain inventory on both paths

Live MANAPI checks on 2026-10-29 returned `total = 0` for both
`/protocols/simpleblock` and `/private/block/simpleprivateblock`. There is no
historical data to migrate. This is the factual basis for the compatibility
decision below: we can standardize on the registered protocol now, at zero
migration cost.

### 1.4 Compatibility decision (binding for S3)

- The **canonical ingest path is the registered protocol
  `/protocols/simpleblock`**.
- The code spelling `/private/block/simpleprivateblock` is retained as a
  **compatibility parse path**: the aggregator dispatches both paths to the
  same block handler with the same payload schema
  (`{"to": string, "blockState": 1 | -1}`).
- Only `create` operations are ingested. Other operations on these paths are
  ignored (consistent with the follow read model, which folds only
  create/revoke pairs for `/follow` and would ignore unknown operations);
  unconfirmed creates flow through the Section 4.4b overlay.
- New block pins written by our own clients (IDBots) must use the registered
  path `/protocols/simpleblock` and SHOULD write `to` as a GlobalMetaID (see
  Section 2.4).

### 1.5 Group block protocols are out of scope

`simplegroupblock` / `simplegroupjoinblock` (group-level block protocols) are
explicitly **not** implemented in this round. See Section 5.2.

## 2. Dual-Identifier Normalization Rules

### 2.1 The two identifiers

| Identifier | Form | Origin |
| --- | --- | --- |
| Legacy metaid | 64 lowercase hex chars, `sha256(chain address)` | Original MetaID identity |
| GlobalMetaID | IDAddress format, bech32-style with checksum, `id…1…` prefix (`idq1…` for P2PKH) | Cross-chain unified representation derived from the chain address |

Real-format samples quoted verbatim from MAN's `GLOBALMETAID.md`:

```text
idq1sv98gg8x84mzgnlhe0guyj8ffs2yvvjenmhn0j       // P2PKH
idp1qyq8s4zcvwyx9qvuqw8zrx3qcxqwqyujnkdxjm4tc    // P2SH
```

Verified upstream facts (MAN repository, `man-p2p`):

- `common/idaddress_util.go` — `ConvertToGlobalMetaId(address)` =
  `idaddress.ConvertFromBitcoin(address)`; returns `""` on unsupported
  formats or checksum failure. `ValidateGlobalMetaId` =
  `idaddress.ValidateIDAddress`.
- `GLOBALMETAID.md` (repo root of `man-p2p`) — GlobalMetaID is the IDAddress
  rendering of the creator's chain address; the same public key on different
  chains maps to the same GlobalMetaID; legacy `metaid` remains
  `sha256(createAddress)`.
- MANAPI input compatibility (verified): `/api/metaid/pin/list/:metaid`
  accepts a GlobalMetaID as the path parameter; `/api/info/metaid/:metaId`
  does not. Backfill and on-chain queries must use the pin-list endpoint
  family for GlobalMetaID-keyed lookups.
- metaso-p2p already contains a hex↔global precedent in the userinfo
  aggregation: `internal/aggregator/social/userinfo_adapter.go`
  (`LookupByGlobalMetaId`), used by the follow indexer to resolve follower
  and target identities.

### 2.2 On-chain inventory reality

Live MANAPI check on 2026-10-29: ~2465 `/follow` pins exist on-chain and
**every `contentBody` is a legacy 64-hex metaid**. There is no GlobalMetaID
precedent in follow payloads today. "Unify on GlobalMetaID" is therefore a
forward-looking write convention only — the read model must normalize, not
assume a single identifier.

### 2.3 The normalization pipeline (binding for S2)

All ingestion and all query parameters flow through one normalization
function before touching the edge read model:

```text
normalizeIdentity(input string) -> (canonicalGlobalMetaId, ok)

1. If input validates as an IDAddress (id-prefixed, checksum passes,
   ValidateGlobalMetaId)  -> canonical = input, ok = true.
2. Else if input is 64-hex (legacy metaid):
     resolve through the indexed profile lookup (metaid -> GlobalMetaID).
     Hit  -> canonical = resolved GlobalMetaID, ok = true.
     Miss -> ok = false (treated as unresolved; see 2.5).
3. Else if input is a chain address:
     convert via the IDAddress algorithm (ConvertFromBitcoin equivalent).
     Hit  -> canonical = converted GlobalMetaID, ok = true.
     Miss -> ok = false.
4. Else -> ok = false.
```

Binding rules:

- **Internal canonical form is always the GlobalMetaID.** Pebble pair keys
  (`edge:pair:<f>:<t>`, and the new `edge:mutual:<id>:…` reverse index) store
  canonical GlobalMetaIDs only. A legacy-hex edge and a GlobalMetaID edge for
  the same two humans are therefore the *same* edge — normalization happens
  *before* keying, so duplicates collapse by construction.
- **Public query inputs accept both forms** on all new endpoints in Section 4
  (`:globalMetaId` path parameters and body/query identity fields). Inputs
  are normalized with the same pipeline; an unresolvable input is a `40400`.
- This is an explicit, limited revision of the shipped contract note in
  `2026-06-20-social-follow-api.md` ("The contract does not accept legacy
  `metaId` or address as public API parameters") — see Section 4.7 and
  Section 6 (open question OQ-1).

### 2.4 Write-side convention (binding for new clients)

New follow pins and new block pins written by our own clients (IDBots and
future tooling) MUST write the peer identity as the GlobalMetaID, as a plain
string `contentBody` / `to` field — never a JSON object, never an address.

Legacy clients (e.g. show.now-era clients) continue to write legacy hex.
The read model absorbs both via Section 2.3; nothing needs to change for
them (see Section 3).

### 2.5 Unresolved legacy references

The shipped follow indexer ignores a non-GlobalMetaID target reference that
does not resolve through profile lookup, and creates no edge. We keep that
behavior. Two additions, binding for S2:

- The pin-level index (`edge:pin:<followPinId>`) continues to record every
  ingested pin regardless of resolution success, so unresolved references
  remain auditable and re-normalizable.
- Backfill may be replayed for a path window: when profile coverage improves,
  previously unresolved hex references can be re-normalized by replaying the
  same MANAPI pages through the normalizer. This is a maintenance path, not
  a hot-path requirement.

## 3. New `/follow` Writes Use GlobalMetaID

### 3.1 The convention

From this specification forward, the target reference of every new follow
pin is the target's GlobalMetaID:

| PIN Field | Required Value |
| --- | --- |
| `path` | `/follow` |
| `operation` | `create` |
| `contentBody` | Target GlobalMetaID (plain string, e.g. `idq123` in examples below; the real format is shown in Section 2.1) |
| publisher identity | The follower account |

Revoke semantics are unchanged from the shipped contract
(`/follow@<originalFollowPinId>` + `revoke` + `originalId`).

### 3.2 Legacy-client compatibility (read path)

- Legacy hex follow pins remain fully indexable. The normalizer resolves
  them through profile lookup to the canonical GlobalMetaID (Section 2.3).
- Existing hex-built edges keep working; queries by GlobalMetaID hit them
  because the canonical key is the GlobalMetaID (S2 acceptance test: replay
  real legacy hex inventory, query by GlobalMetaID, edges must resolve).
- The shipped "unresolved non-GlobalMetaID reference is ignored" behavior is
  unchanged (Section 2.5).
- Old clients are read-compatible too: they consume the same public API
  surfaces as today. New endpoints are additive; no shipped endpoint changes
  its response schema (Section 4.7).

### 3.3 Mixed-identity edge case

If the same pair has one hex-written edge (legacy client) and one
GlobalMetaID-written edge (new client) — for example because each side uses
a different client generation — normalization collapses them into one pair
edge before keying (Section 2.3). The newest active create pin wins for
`followPinId` / `followedAt`, exactly as the shipped contract specifies.

## 4. New API Contracts

All new endpoints follow the shipped transport conventions of
`2026-06-20-social-follow-api.md`:

- gin routes registered in the social aggregator's `RegisterRoutes`
  (pattern: `internal/aggregator/social/module.go:50` →
  `internal/aggregator/social/api.go:65-67`).
- Native metaso-p2p envelope, HTTP 200 always; clients branch on `code`.

Success envelope:

```json
{
  "code": 0,
  "message": "",
  "processingTime": 9,
  "data": {}
}
```

Error envelope (`code` + `message` only):

```json
{
  "code": 40000,
  "message": "invalid parameter"
}
```

Shared error codes (unchanged):

| Code | Message | Meaning |
| --- | --- | --- |
| `40000` | `invalid parameter` | Invalid or missing public parameters. |
| `40400` | `subject not found` | An identity input does not normalize to an indexed GlobalMetaID. |
| `50000` | `aggregation unavailable` | Read model or dependency temporarily unavailable. |

Shared pagination contract (all list endpoints below):

| Parameter | Type | Required | Default | Rules |
| --- | --- | --- | --- | --- |
| `cursor` | string | No | empty | Opaque pagination cursor. |
| `size` | number | No | `20` | Must be in `1..100`. |
| `view` | string | No | `compact` | Only `compact` or `profile`. |

Cursor rules are identical to the shipped contract: `nextCursor=""` means no
next page; cursors are opaque; cursors are valid only for the matching
endpoint and subject; a stale cursor whose referenced entry disappeared
returns `40000`. List ordering is newest first by the entry's on-chain
timestamp, ties resolved deterministically by internal index order.

Identity inputs: every identity field on every new endpoint accepts either a
GlobalMetaID or a legacy 64-hex metaid and normalizes per Section 2.3.
Unless stated otherwise, identity fields that fail normalization yield
`40400`.

### 4.1 GET /api/social/globalmetaid/:globalMetaId/friends

Returns the friend list of the subject. **Friend = mutual follow**: both
direction edges are currently active.

```text
GET /api/social/globalmetaid/idq123/friends
GET /api/social/globalmetaid/<id>/friends?cursor=...&size=20&view=profile
```

Response (`view=compact`):

```json
{
  "code": 0,
  "message": "",
  "processingTime": 11,
  "data": {
    "list": [
      {
        "globalMetaId": "idq456",
        "name": "Alice",
        "nameId": "name-alice:i0",
        "avatarId": "avatar-alice:i0"
      }
    ],
    "nextCursor": "",
    "size": 20
  }
}
```

Response (`view=profile`) adds per item:

```json
{
  "bio": "short bio",
  "bioId": "bio-alice:i0",
  "followedAt": 1760800000,
  "followPinId": "follow-alice-to-bob:i0",
  "friendSince": 1760800100
}
```

Item field notes:

- `followedAt` / `followPinId` — the subject→peer active edge (same
  semantics as the shipped `following` list).
- `friendSince` — Unix seconds of the later of the two direction edges, i.e.
  the moment the mutual state came into existence.
- Blocked peers are excluded by the unified filter layer (Section 4.5) and
  never appear in this list.

### 4.2 GET /api/social/globalmetaid/:globalMetaId/counts

Returns all relationship counters for the subject in one call. Counts use
the **same filtered view** as the list endpoints (blocked peers excluded),
so counts always agree with what the lists return.

```text
GET /api/social/globalmetaid/idq123/counts
```

Response:

```json
{
  "code": 0,
  "message": "",
  "processingTime": 6,
  "data": {
    "followers": 12,
    "following": 8,
    "friends": 5,
    "blockedCount": 2,
    "blockedByCount": 0
  }
}
```

Field notes:

- `followers` / `following` — active incoming / outgoing follow edges after
  the block filter (Section 4.5).
- `friends` — mutual edges after the block filter.
- `blockedCount` — distinct peers the subject currently blocks
  (`blockState = 1`).
- `blockedByCount` — distinct peers currently blocking the subject.
- This endpoint is additive; the shipped list endpoints continue to omit
  counts (their contract and the `Excluded Fields` section of
  `2026-06-20-social-follow-api.md` stay untouched).

### 4.3 POST /api/social/relationships/batch

Batch relationship lookup. One call answers "what is my relation to each of
these peers".

```text
POST /api/social/relationships/batch
Content-Type: application/json

{
  "sourceGlobalMetaId": "idqSource",
  "targets": [
    "idq456",
    "a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4"
  ]
}
```

Request rules:

| Field | Type | Required | Rules |
| --- | --- | --- | --- |
| `sourceGlobalMetaId` | string | Yes | GlobalMetaID or legacy hex; normalized per Section 2.3. |
| `targets` | string[] | Yes | 1..100 entries; each entry GlobalMetaID or legacy hex. More than 100 entries → `40000`. |

Response:

```json
{
  "code": 0,
  "message": "",
  "processingTime": 14,
  "data": {
    "list": [
      {
        "globalMetaId": "idq456",
        "relation": "friend",
        "isFriend": true,
        "followsMe": true,
        "followedByMe": true,
        "blocking": false,
        "blockedBy": false
      },
      {
        "globalMetaId": "a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4a1b2c3d4",
        "relation": "stranger",
        "isFriend": false,
        "followsMe": false,
        "followedByMe": false,
        "blocking": false,
        "blockedBy": false
      }
    ]
  }
}
```

`relation` is the single-value fold with this precedence:

```text
blocked > friend > following > follower > stranger
```

- `blocked` — `blocking || blockedBy` is true (see below).
- `friend` — both follow directions active.
- `following` — only the source→target edge active.
- `follower` — only the target→source edge active.
- `stranger` — no active edges in either direction.

Structured booleans (always present, never folded away):

- `followsMe` — target→source active edge.
- `followedByMe` — source→target active edge.
- `isFriend` — both.
- `blocking` — source currently blocks target (`blockState = 1`).
- `blockedBy` — target currently blocks source.

Callers who only need the five-state fold read `relation`; callers who need
direction detail read the booleans. This shape serves both the S2
acceptance wording (friend/follower/following/stranger) and the IDBots-side
five-state requirement (including blocked) without a second endpoint.

Error behavior:

- Empty or unresolvable `sourceGlobalMetaId` → `40400`.
- Empty `targets`, > 100 entries, or any entry that is neither a valid
  GlobalMetaID nor 64-hex → `40000`.
- A target that normalizes but has no indexed presence is **not** an error:
  it returns `relation: "stranger"` with all booleans `false` (batch lookups
  are intentionally total).
- Response order matches request order; each item echoes the canonical
  GlobalMetaID of the target.

### 4.4 GET /api/social/globalmetaid/:globalMetaId/blocks

Block aggregation read model. Returns the current block relationships of the
subject, derived from ingested `simpleblock` pins (both canonical and
compatibility paths, Section 1.4), folded last-write-wins per pair.

```text
GET /api/social/globalmetaid/idq123/blocks
GET /api/social/globalmetaid/<id>/blocks?type=blocking&cursor=...&size=20&view=compact
```

| Parameter | Type | Required | Default | Rules |
| --- | --- | --- | --- | --- |
| `type` | string | No | `all` | One of `all`, `blocking` (peers the subject blocks), `blockedBy` (peers blocking the subject). |
| `cursor` / `size` / `view` | | No | | Shared pagination contract. |

Response:

```json
{
  "code": 0,
  "message": "",
  "processingTime": 7,
  "data": {
    "list": [
      {
        "globalMetaId": "idq456",
        "name": "Mallory",
        "nameId": "name-mallory:i0",
        "avatarId": "avatar-mallory:i0",
        "direction": "blocking",
        "blockedAt": 1793240000,
        "blockPinId": "simpleblock-pin:i0"
      }
    ],
    "nextCursor": "",
    "size": 20
  }
}
```

Item field notes:

- `direction` — `blocking` or `blockedBy` relative to the subject. With
  `type=all` a mutually-blocked pair produces two entries (one per
  direction); with `type=blocking` / `type=blockedBy` only the matching
  direction is listed.
- `blockedAt` — Unix seconds of the current active `blockState = 1` pin.
- `blockPinId` — pin ID of that pin (enables pin-level audit and unblock
  flows).
- `view=profile` additionally includes `bio` / `bioId` (same semantics as
  the shipped lists).
- Unblock (`blockState = -1`) removes the pair from this read model;
  the counters in 4.2 drop accordingly.
- Read-model scope: confirmed pins materialize the confirmed read model;
  unconfirmed pins take effect immediately through the mempool overlay
  (Section 4.4b; OQ-2 resolved 2026-10-30).

### 4.4b Mempool instant-effect overlay (binding; owner decision 2026-10-30)

**Decision.** Unconfirmed `/follow` and `simpleblock` pins take effect in
every read view **immediately** (mempool-speed), not after block
confirmation. This resolves OQ-2 by reversing its confirmed-only default,
per the owner's 2026-10-30 call, citing the show.now precedent (the legacy
frontend already overlays unconfirmed follows — old metaso
`basicprotocols/metaso/sync_mempool.go`, mempool overlay collection with
`Mempool:1` markers).

**Model.** The confirmed read models (Sections 4.1–4.4) stay confirmed-only
and remain the single source of truth; live ingest, backfill replay and
restart recovery are unchanged. On top of them each aggregator keeps a small
**pending overlay** in separate Pebble keys
(`edge:mp:pair:` / `edge:mp:pin:` in `social`, `blk:mp:pair:` /
`blk:mp:pin:` in `block`):

1. **Write.** `HandleMempoolPin` folds a mempool `/follow` create/revoke or
   simpleblock pin into the overlay: follow/block materializes the pair,
   follow revoke / `blockState = -1` is an unblock tombstone hiding it.
2. **LWW per pair.** Exactly one overlay entry per (follower, target) /
   (from, to) pair, last-write-wins by local arrival sequence — mempool pins
   carry no height, arrival order is the only ordering available. Repeated
   delivery of the same pin is idempotent; a pin already folded into the
   confirmed model never re-enters the overlay.
3. **Effective read.** Every read surface — relationship (mutual), friends /
   following / followers lists, counts, batch relationships, `blocks`
   listing, `IsBlockedBetween`, `BlockingBy`/`BlockedBy`, and therefore the
   §4.5 unified filter and the §4.6 chat read-side union — answers from the
   **effective state**: overlay entry if present, else the confirmed edge.
   A pending follow makes the pair visible instantly (friend flips the
   moment the second direction lands in mempool); a pending revoke/unblock
   hides it instantly.
4. **Confirmation transition.** When the pin confirms, the normal confirmed
   fold runs and consumes the pin's overlay ref; the pair entry drains only
   if this pin still owns it (a newer unconfirmed mutation survives). The
   transition is seamless: same pin id, values unchanged, `pending` gone.
5. **Expiry janitor.** A sweeper (config `zmq.mempoolOverlaySweepInterval`,
   default 5m; env `METASO_P2P_ZMQ_MEMPOOL_OVERLAY_SWEEP_INTERVAL`) walks
   the overlay pin refs: entries whose pin already folded (missed
   consumption, e.g. across a restart) are consumed as self-heal; entries
   older than `zmq.mempoolOverlayExpiry` (default 2h; env
   `METASO_P2P_ZMQ_MEMPOOL_OVERLAY_EXPIRY`) whose pin never confirmed —
   dropped or replaced mempool transactions — are deleted, revealing the
   confirmed state beneath. No rollback bookkeeping exists or is needed.

**API surface.** Items whose edge comes from the overlay carry
`"pending": true` (additive, `omitempty` — confirmed-only responses are
byte-identical to before): `following`/`followers` list items,
`friends` items, both `relationship` sides, and `blocks` items. The batch
endpoint (4.3) folds effective state but does not add a pending flag.

**Pagination note.** `following`/`followers` surface pending-only edges
(no confirmed index entry yet) at the head of the **first page only**
(cursor omitted), newest first, capped at `size`; subsequent pages walk the
confirmed index alone, keeping the shared cursor contract intact.
`friends` and `blocks` merge the overlay into their in-memory sorts, so
pending entries paginate normally there.

**Notify bridge.** The §4.6 session-cache bridge stays confirmed-only
(`ApplyChainBlock`/`RemoveChainBlock` fire on confirmed folds). Chat
filtering is still mempool-fast: every filtered read consults the §4.6
read-side union, which resolves through the effective state.

**Residual risk (accepted).** Overlay state derives from the node mempool
view; a transaction replaced or dropped after our poll shows a stale effect
for at most the expiry window (default 2h), then self-corrects to the
confirmed model. Chain-order inversions between competing mempool
mutations resolve by arrival until confirmation, which is authoritative.

### 4.5 Unified filter layer (binding for S3)

The block read model is exposed to all consumers through one narrow
interface; every filtered surface depends on this interface, not on the
block storage directly.

```go
// internal/aggregator/social (or a small shared package) — suggested shape.
type BlockLister interface {
    // IsBlockedBetween reports whether either direction of the pair is
    // currently blocked (blockState = 1).
    IsBlockedBetween(a, b string) (bool, error)
    // BlockingBy lists peers the subject currently blocks.
    // BlockedBy lists peers currently blocking the subject.
    // Both are filtered-view primitives used by lists and counters.
}
```

Binding rules:

- `following`, `followers`, `friends` list endpoints and the `counts`
  endpoint apply the filter unconditionally: a pair with any active block in
  either direction is **removed** from those reads. There is no
  `includeBlocked` escape switch (safe-by-default; callers needing block
  state use `blocks` or the batch endpoint).
- The batch relationship endpoint (4.3) is the deliberate exception: it
  *reports* block state instead of filtering, because its purpose is to
  answer "what is the relation".
- The existing shipped list endpoints (`following`, `followers`) gain this
  filter as a behavior change of their read path; their response schema is
  unchanged. This is the only behavioral change to shipped endpoints in this
  specification (Section 4.7).
- Private-chat surfaces consume the same filter (Section 4.6).

### 4.6 Merge with the existing off-chain chat block read model

Current state (verified):

- `internal/aggregator/notify/module.go` maintains an off-chain chat block
  read model: pebble prefix `blocked:` (`module.go:43`), routes
  `get_user_blocked_chats` / `add_blocked_chat` / `remove_blocked_chat`
  (`module.go:73-75`). Its error envelope uses `code = 1` (a different,
  older error system than the social `40000` family).
- `docs/GOAL_DRIVEN.md:201` carries the unfinished acceptance item:
  after processing a `simpleprivateblock` pin,
  `GET /push-base/v1/push/get_user_blocked_chats` must show the record.

Merge design (binding for S3):

- **Authority**: the on-chain `simpleblock` aggregation (Section 4.4) is the
  authority for chain-level block state. The notify read model remains the
  session-layer cache for chat UI state.
- **Event bridge**: when the aggregator ingests a confirmed block pin
  (either path), it additionally writes the notify model's `blocked:` entry
  for the affected pair, satisfying the GOAL_DRIVEN.md:201 item. An
  ingested `blockState = -1` pin removes that entry.
- **Off-chain-only blocks** (added via `add_blocked_chat`, never published
  on-chain) keep working exactly as today; they are session-local UI state
  and never leak into the social block read model or the filter layer.
- **Read-side merge** for chat filtering: a chat surface is filtered when
  either the on-chain aggregated state or the off-chain session entry says
  blocked (union semantics). Each source keeps its own unblock path; a
  source re-blocks independently.
- Error-envelope convergence is explicitly **not** attempted in this round:
  the notify module keeps its `code = 1` system; the social endpoints use
  the `40000` family. Unifying envelopes is out of scope (Section 5.2).

### 4.7 Changes to shipped contracts (complete list)

1. `following` / `followers` list reads now exclude blocked pairs
   (Section 4.5). Response schemas unchanged.
2. Public identity inputs on new endpoints accept legacy hex as well as
   GlobalMetaID (Section 2.3). The shipped endpoints' input contract is not
   changed by this document; the shipped note against legacy inputs is
   superseded **for new endpoints only**.
3. (Mempool-effect round, 2026-10-30) List/count/relationship reads answer
   from effective state — unconfirmed `/follow` and `simpleblock` pins are
   visible immediately (Section 4.4b). Items sourced from the overlay carry
   an additive `"pending": true` field; confirmed-only responses are
   byte-identical to before.

Nothing else about shipped endpoints changes.

## 5. Scope

### 5.1 In scope (this specification round)

- Friend = mutual follow; friends list, relationship counters, batch
  relationship lookup (Sections 4.1–4.3).
- Canonical GlobalMetaID write convention and dual-identifier
  normalization (Sections 2–3).
- Simpleblock aggregation read model, dual-path ingest, unified filter
  layer, notify merge (Sections 1, 4.4–4.6).
- Mempool instant-effect overlay for `/follow` and `simpleblock`
  (Section 4.4b, added 2026-10-30).
- Relationship enumeration for consumers:
  `friend / follower / following / stranger / blocked` (five states, fold
  rule in 4.3).

### 5.2 Out of scope (explicitly)

- **Group blocking**: `simplegroupblock` and `simplegroupjoinblock` are not
  parsed, not aggregated, not filtered on. A later round may add them.
- Error-envelope unification between the notify module (`code = 1`) and the
  social API family (`40000` family).
- ~~Mempool-speed block effects~~ — moved **in scope** by Section 4.4b
  (owner decision 2026-10-30); the session-cache bridge itself stays
  confirmed-only (read-side union provides the mempool speed).
- Any write-side proxying: metaso-p2p remains a read/aggregation service;
  clients publish their own pins.
- Migrating or rewriting historical follow payloads (none needed —
  normalization is read-side only).

## 6. Open Questions for the Owner

These do not block implementation start; each has a default in the text that
applies unless the owner says otherwise.

- **OQ-1 — legacy-hex public inputs.** Section 2.3/4.7 deliberately allows
  legacy 64-hex inputs on the new endpoints (and normalizes them), while
  the shipped 2026-06-20 contract states public inputs are GlobalMetaID
  only. Default chosen here: allow both on new endpoints (matches the S2
  acceptance criterion "query by GlobalMetaID hits legacy-built edges" plus
  the practical reality that the whole on-chain inventory is hex). If the
  owner prefers strict GlobalMetaID-only public inputs, only the input
  validation clause changes — internal normalization is unaffected.
- **OQ-2 — block effect latency. [RESOLVED 2026-10-30, reversed by owner]**
  The original default was confirmed-only: a freshly written block or
  follow pin would take effect after chain confirmation + indexer pickup.
  The owner ruled the opposite — mempool transactions must take effect
  immediately, citing the show.now precedent (the legacy frontend already
  overlays unconfirmed follows). Section 4.4b is the binding
  implementation contract: pending overlay on top of the confirmed models,
  effective reads everywhere, seamless confirmation transition, expiry
  janitor; the notify session-cache bridge stays confirmed-only with the
  §4.6 read-side union providing mempool-speed chat filtering.
- **OQ-3 — `blockedBy` visibility.** The API exposes "who blocks me"
  (`type=blockedBy`, `blockedBy` booleans, `blockedByCount`). Some products
  hide this (blocking is meant to be invisible to the blocked side). The
  MetaBot use-case needs it (a bot must know a peer is unreachable), so the
  default here is expose. If the owner prefers hiding, drop `type=blockedBy`
  listings but keep the boolean in the batch endpoint (needed for correct
  message filtering).
