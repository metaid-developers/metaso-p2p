# MRC-721 NFT Index — Downstream Migration Guide (man-p2p → MetaSo-P2P)

Audience: clients that read the man-p2p `/api/mrc721/*` MRC-721 surface —
today that means **IDBots** (`mrc721Service.ts`, the `mrc721_*` agent tools).
Deployed on production 2026-11-05, release `41eeebc`, live at
`https://so.metaid.io` with real registry data bootstrapped from the
manapi-compatible upstream.

**TL;DR**: MetaSo-P2P now serves the full man-p2p MRC-721 read surface
**byte-for-byte** — same paths, params, pagination, envelope codes and DTO
field spellings. The migration is a **base-URL switch**. There are exactly
**two deliberate divergences** (§3: metaid fields now carry global metaids;
§4: the 2026-11-07 issuer-only protocol revision — album identity by
collectionid, issuer-only minting, non-unique collection names), the first
matching the IDBots requirement doc (§2/§R5: identity display prefers
globalMetaId), the second the agreed protocol upgrade in
[`docs/mrc721-protocol-revision-issuer-only.md`](mrc721-protocol-revision-issuer-only.md).

## 1. What changed upstream

- MetaSo-P2P natively indexes `/nft/mrc721` pins (previously man-p2p's job)
  with man-p2p's aggregation semantics ported 1:1 and then **revised
  2026-11-07** (§4): albums identified by collectionid with issuer-only
  minting attribution, issuer-only `collection_desc modify` version chains,
  issuer-gated first-per-field `item_desc` backfill, supply-gated mints,
  out-of-order self-healing.
- The confirmed transfer pipeline runs every block (ledger-txid pointers,
  input-0 → output-0 ownership rule, meltdown burns) and a 30s full-mempool
  scan exposes **unconfirmed spends** through the pending channel — closing
  the "sent but unconfirmed vs not sent" visibility gap IDBots filed as a
  defect (man-p2p commit 8582bc2 lineage).
- man-p2p stays a raw-pin indexer and keeps serving its own copy; the two
  aggregates are hydrated from the same source and remain equivalent
  (except the metaid口径 in §3).

Normative contract: [`docs/specs/2026-11-05-mrc721-nft-api.md`](specs/2026-11-05-mrc721-nft-api.md).
Deployment/ops: [`docs/mrc721-index-deployment.md`](mrc721-index-deployment.md).

## 2. The switch (IDBots)

All MRC-721 NFT reads flow through one point in
`IDBots/src/main/services/mrc721Service.ts`:

```ts
const MANAPI_BASE = 'https://manapi.metaid.io';   // line ~29
function buildMrc721Url(localPath, query) { … return `${MANAPI_BASE}${localPath}…`; }
```

Change the base to MetaSo (the metaso base resolver added with the
sell-intent integration already exists in the same file):

```ts
const MRC721_READ_BASE = resolveMetasoApiBase();   // 'https://so.metaid.io', IDBOTS_METASO_API_BASE overrides
```

Notes:

- `fetchFromLocalOrFallback` (local sqlite → `IDBOTS_MAN_P2P_LOCAL_BASE`
  override → remote) is untouched: the local cache and the staging override
  keep working; only the remote fallback target changes.
- The `mrc721_*` **write path** (mint/issue/transfer pin construction and
  broadcasting) is unaffected — pins are pins; only the read base moves.
- The sell-intent endpoints (`/api/mrc721/sell/*`,
  `collection/search`, `address/collections-issued`) already point at
  `https://so.metaid.io` and are unchanged.

## 3. First divergence: metaid fields carry global metaids

**man-p2p** emits `metaid` / `createMetaId` / `ownerMetaId` (and their
lowercase aliases) as the **legacy metaid**: `sha256(address)` hex
(64 lowercase hex chars, no prefix).

**MetaSo** emits those same fields as the **global metaid**: the IDAddress
encoding of the address (`id…`, produced by the ecosystem's
`ConvertFromBitcoin`/IDAddress algorithm — the same values man-p2p puts in
its `GlobalMetaId` fields and the same convention the social/Q&A surfaces
normalize identities with).

- Field **names are unchanged**; only the value source changed.
- This matches IDBots requirements §2/R5 ("身份展示：优先展示
  MetaID(globalMetaId)，链地址为辅") — display code that prefers
  globalMetaId can now read the metaid fields directly.
- Migration rules for client code:
  - Never compare a legacy metaid against a global metaid (different
    formats: 64-hex vs `id…`). Key caches/records by **address or pinId**,
    not by metaid.
  - Persisted legacy metaids in local caches are display-only history; if
    an identity value is needed, re-derive from the address (the address
    fields are unchanged and authoritative).
  - Unconvertible addresses yield `""` (an improvement: empty owners no
    longer produce a junk sha256-of-empty value).
- man-p2p has **not** made this change on its side; if you ever point the
  base back at man-p2p, metaid fields revert to legacy hex (§7).

## 4. Second divergence (2026-11-07): issuer-only minting, non-unique names

The MRC-721 protocol semantics were revised by the protocol authors
([`docs/mrc721-protocol-revision-issuer-only.md`](mrc721-protocol-revision-issuer-only.md))
while on-chain NFT volume is still minimal — MetaSo implements the revised
semantics; man-p2p still runs the original open-mint / unique-name behavior.

What changed:

- **Album identity = collectionid** (the `collection_desc` pinid), with
  (collectionName, issuer address) as the human-level pair. Collection
  names are **not unique** and confer no ownership: two albums may share a
  name exactly like two ERC-721 contracts sharing a display name. Address
  albums by `pinid` / `collectionPinId`, never by name.
- **Minting is issuer-only**: an item under `/nft/mrc721/{name}` is indexed
  only into the same-name album whose `collection_desc` author minted it.
  Third parties can no longer consume `totalSupply` slots or insert items
  into someone else's album. Consequently `creatorMinted` is `true` on
  every indexed item (field kept for wire compatibility), and album item
  numbers are dense 1..N of the issuer's mints.
- **item_desc is issuer-gated** too: only the referenced item's album
  issuer can backfill its name/desc/cover/metadata.

API impact (wire shapes unchanged):

- A `name` parameter (`collection/info`, `collection/items/pageList`,
  `address/item` filter) resolves to the **earliest-created album** under
  that name — deterministic, but ambiguous by design; prefer the `pinId`
  parameter wherever possible.
- List endpoints (`collection/pageList`, `collection/search`) can return
  several rows with the same `collectionname` — distinct albums; key UI
  state by `pinid`.
- Item sets differ from man-p2p's for any album where third-party pins
  exist under the album path (man-p2p indexes them open-mint style, MetaSo
  does not). This is the intended divergence.

## 5. Contract guarantees (what did NOT change)

| Aspect | Guarantee |
|---|---|
| Paths | Identical: `collection/pageList`, `collection/info`, `collection/items/pageList`, `address/collection`, `address/item`, `item/info`, `item/transfers`, `item/verify` |
| Envelope | Identical: HTTP always 200; success `{code:1,message:"ok",data}`; parameter/service errors `{code:404,…,"data":null}`; no-data `{code:100,"no data found.","data":[]}` |
| Pagination | Identical: legacy misspelled `cousor` takes precedence over `cursor`; int64 offset; `size` default 20 / clamp 200 |
| DTOs | Identical dual spellings (`collectionname`/`collectionName`, `pinid`, `totalnum`, `txOutput`/`output`, `txValue`/`outputValue`, `metaData`/`metadata`, `contentString`/`contentSummary`, …); `address`/`metaId` = current holder |
| Outpoints | `output`/`txOutput` and history `txId` use **ledger txids** (MVC version≥10 reordered hash) — spendable on-chain references, transfer construction safe (no `[-25] Missing inputs`) |
| Pending channel | `item/info` adds a `pending` key **only** when an unconfirmed spend exists (absent ≠ null); `item/transfers` always carries a `pending` array that is never merged into `list`/`total` |
| Ordering | `collection/pageList` keyed by (name, issuer); items in mint order (dense 1..N per album); holder views deduped per album with `ownnum` |
| Identity keying | Albums are keyed by **collectionid** (`pinid`/`collectionPinId`); a `name` param resolves to the earliest album under the name (man-p2p's "name wins" precedence kept) — prefer `pinId` (§4) |
| Bonus endpoints | `GET /api/tx/status?chainName=&txid=` (generic transaction status, man-p2p parity) and `GET /api/mrc721/admin/replay-pins?pinIds=` (ops repair, re-applies an entity's upstream state — semantics differ slightly from man-p2p's pin replay: unknown pin ids answer `pin not found`) |

## 6. Verification checklist (run before + after the switch)

```bash
BASE=https://so.metaid.io/api/mrc721
curl -s "$BASE/collection/pageList?size=5"                       # {code:1,data:{list,total}}
curl -s "$BASE/collection/info?name=<name>"
curl -s "$BASE/collection/items/pageList?pinid=<collectionid>"
curl -s "$BASE/address/collection?address=<addr>"                # rows carry ownnum
curl -s "$BASE/address/item?address=<addr>&pinId=<collectionid>"
curl -s "$BASE/item/info?pinId=<itemPinId>"                      # output/outputValue = current UTXO
curl -s "$BASE/item/transfers?pinId=<itemPinId>"                 # {list,total,pending}
curl -s "$BASE/item/verify?pinId=<itemPinId>"
curl -s "$BASE/item/info"                                        # {"code":404,…,"data":null}
curl -s "$BASE/item/info?pinId=bogus"                            # {"code":100,…,"data":[]}
curl -s "https://so.metaid.io/api/tx/status?chainName=mvc&txid=<real txid>"
```

Cutover gate (recommended before announcing the switch): observe **one real
NFT transfer** on the new base — the spent item's `item/info` should show a
`pending` object while the spend is in the mempool, and the pending should
disappear with a new `item/transfers` row once the block confirms.
(MVC was in a quiet period at release time, so this channel is live but not
yet exercised by a real transfer.)

## 7. Zero-confirmation reads (R-F, live since 2026-11-09)

Every MRC-721 read surface now serves mempool facts immediately (issuance
AND trading): a fresh mint, the album registration, an item_desc batch, a
transfer, an atomic-swap settlement, a sell/offer declaration — all become
visible within one mempool poll (~10s) instead of one block. Spec:
[`docs/specs/2026-11-09-mrc721-zero-conf-api.md`](specs/2026-11-09-mrc721-zero-conf-api.md).

What IDBots should read (all fields additive; absent = fully confirmed):

- `confirmed:false` → show the pending badge on cards (row produced or
  changed by a mempool pin/spend; `firstSeenAt` carries first arrival).
- `numberFinal:false` → the provisional `number` may still reorder
  (parallel-funded mints); parent→child mint chains are `true` immediately.
- `ownershipConfirmed:false` → the item's owner/output reflect an
  unconfirmed spend; `confirmedOwnerAddress` is the last confirmed owner.
  Treat the item as in-flight, not spendable-by-the-buyer-yet on OTHER
  indexers' terms — this service's `output` IS the overlay spend target, so
  chained unconfirmed transfers/resales work against it at any depth.
- `mintedCount` (collection) → confirmed + staged + overlay mints; this is
  the mint supply precheck surface (`confirmedMintedCount` is the
  confirmed-only count).
- `descConfirmed:false` → a display field was filled by an unconfirmed
  item_desc.
- Sell intents / offers carry the same `confirmed` flag; a mempool
  cancellation hides the intent from `active=true` lists immediately and
  evicting that cancellation restores it — safe to offer re-declares.
- `overSupply:true` rows appear only with `includeOverSupply=1` (they sit
  past `totalSupply`; the chain's final order is expected to agree).

Strict confirmed-only audits/accounting: append `confirmedOnly=1` to ANY
endpoint — the response is byte-identical to the pre-R-F service.

Timestamp units (2026-11-09 note, downstream field report): every MRC-721
time field (`item/transfers` `time`, `pending.firstSeenAt`, overlay rows'
`firstSeenAt`, sell/offer `updatedAt`/`createdAt`, `createTime`) is **unix
SECONDS** — same as man-p2p served. Rendering with `new Date(value)` treats
the value as milliseconds and shows 1970; use `new Date(value * 1000)` (or
tolerance: values `< 10^12` are seconds).

## 8. Rollback

Point the base back at `https://manapi.metaid.io` — man-p2p keeps serving
its own copy of the aggregate. Caveats: metaid fields revert to legacy
sha256 hex there, and man-p2p still runs the pre-revision open-mint /
unique-name semantics — for albums with third-party pins under the album
path the two bases legitimately disagree on the item sets (§4). Do not mix
identities or item sets across the two bases.

## 9. References

- Spec: [`docs/specs/2026-11-05-mrc721-nft-api.md`](specs/2026-11-05-mrc721-nft-api.md)
- Deployment/ops: [`docs/mrc721-index-deployment.md`](mrc721-index-deployment.md)
- Sell-intent spec (already live on MetaSo): [`docs/specs/2026-11-04-mrc721-sell-collection-api.md`](specs/2026-11-04-mrc721-sell-collection-api.md)
- Protocol revision (2026-11-07): [`docs/mrc721-protocol-revision-issuer-only.md`](mrc721-protocol-revision-issuer-only.md)
- Zero-conf spec (2026-11-09): [`docs/specs/2026-11-09-mrc721-zero-conf-api.md`](specs/2026-11-09-mrc721-zero-conf-api.md)
- IDBots requirement input: man-p2p `docs/IDBOTS_MRC721_REQUIREMENTS.md`
