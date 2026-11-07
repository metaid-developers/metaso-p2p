# IDBots Change Requirements — MRC-721 Issuer-Only Protocol Revision

From: MetaSo-P2P team (MRC-721 read-surface owner) — release `9a9f0c4`, live on
`https://so.metaid.io` since 2026-11-07.
Audience: IDBots developers maintaining `mrc721Service.ts` / `mrc721AgentTools.ts`.
Status: **required work for IDBots** — R1/R2 are functional (current behavior is
wrong under the revised protocol), R3/R4 are strongly recommended.

Normative references (all in the metaso-p2p repo):

- Protocol semantics: `docs/mrc721-protocol-revision-issuer-only.md`
- Wire contract: `docs/specs/2026-11-05-mrc721-nft-api.md` (§1 collectionid
  口径, §2 pin semantics)
- Downstream impact: `docs/mrc721-nft-downstream-guide.md` §4

## 0. Background — what changed on 2026-11-07

The MRC-721 protocol semantics were revised by the protocol authors:

1. **Album identity = collectionid** (the `collection_desc` pinid); the
   human-level pair is (collectionName, issuer address). **Collection names
   are no longer unique and confer no ownership** — two albums may share a
   name, exactly like two ERC-721 contracts sharing a display name.
2. **Minting is issuer-only**: a create pin under `/nft/mrc721/{name}` is
   indexed only into the same-name album whose `collection_desc` author
   minted it. Pins by anyone else are never collected (no supply-slot
   consumption, no junk insertion).
3. **item_desc is issuer-gated** likewise; `creatorMinted` is `true` on
   every indexed item (field kept for wire compatibility); item numbers are
   dense 1..N per album.

Wire compatibility is preserved (paths, params, envelope codes, DTO
spellings all unchanged), so pure read flows keep working. What breaks is
**client-side logic that encoded the old "first registration wins
globally" / "open minting" semantics** — enumerated below.

One API behavior change to know: a `name` parameter on
`collection/info` / `collection/items/pageList` / `address/item` resolves to
the **earliest-created album under that name** (deterministic fallback).
man-p2p's "name wins over pinId" parameter precedence is kept, and the
no-data shapes are unchanged (unknown `pinid` → code 100; unknown `name` on
`items/pageList` → code 1 with an empty list). Precision requires the
collectionid — prefer `pinId` wherever the caller already has one.

## 1. R1 (must) — `mrc721_collection_create` precheck refuses legitimate issuances

**Where**: `src/main/libs/mrc721AgentTools.ts` ~673-690 (the issue tool's
precheck).

**Current behavior**: before broadcasting a `collection_desc`, the tool calls
`fetchMrc721CollectionInfo({ name })` (→ `/api/mrc721/collection/info?name=`)
and hard-refuses when ANY album exists under the name
("First registration wins globally — pick another collection_name, or mint
into it with mrc721_mint").

**Why it is wrong now**: another issuer's same-name album is a separate,
independent album. The precheck blocks the session MetaBot from legitimately
issuing its own album under a taken name — a functional regression, and the
refusal text teaches the agent the old protocol.

**Required behavior**:

1. Replace the global name lookup with an **issuer-scoped precheck**: does
   the session MetaBot's own address already have an album under this name?
   Call `GET /api/mrc721/collection/search?issuer=<sessionMvcAddress>&keyword=<collectionName>&size=100`
   and refuse only when a row matches with `row.collectionName === <name>`
   (exact match; `keyword` is a case-insensitive substring, so filter
   client-side) and `row.createAddress === <sessionMvcAddress>` (base58 is
   case-sensitive).
   Refusal text (suggested): `collection_name "<name>" is already issued by
   this MetaBot (collectionid <pinid>) — a (collection_name, issuer) pair
   registers once; mint into it with mrc721_mint or modify it with
   mrc721_collection_modify.`
2. If the search shows an album under the name owned by a **different**
   issuer, proceed — but append a note to the receipt: the name is shared
   with an independent album (collectionid …, issuer …); albums are
   distinguished by collectionid, and only this MetaBot's mints are collected
   into its own album.
3. Update the tool description and the zod description of
   `collection_name`: replace "globally unique — first registration wins"
   with the revised semantics (names are not unique; an album is identified
   by its collectionid and owned by its issuer; the same issuer may register
   a given name once).
4. The precheck-unavailable fallback stays (proceed with a warning), but
   reword "the pin is authoritative and first-registration-wins"
   accordingly.
5. Post-issue receipt text "if it never appears, the name was likely taken"
   must go: under the revision a registered album always appears; point at
   `collection/search?issuer=` for self-checks instead.

**Server-side idempotency note**: even if the precheck is skipped, a second
`collection_desc` by the same (name, issuer) is ignored by the indexer (no
duplicate album), but the broadcast fee is still spent — the client-side
refusal is a fee guard, not a correctness requirement.

## 2. R2 (must) — `mrc721_mint` must not let a non-issuer pay for a pin that never indexes

**Where**: `src/main/libs/mrc721AgentTools.ts` ~845-865 (the mint tool's
precheck; there is currently **no issuer check**), plus the receipt text in
`formatMrc721MintResult` (~405-440).

**Why it matters now**: under issuer-only minting, a mint into an album the
session MetaBot does not own broadcasts successfully, spends fees, and is
**never collected** — the current receipt ("this mint stays pending until
the collection registers") is misleading, because it will not be collected
unless the *minter's own* album under that name registers.

**Required behavior**:

1. Resolve the session MetaBot's own album under the requested name first
   (same `collection/search?issuer=<sessionMvcAddress>&keyword=<name>` call
   as R1; exact `collectionName` + `createAddress` match).
2. **Hard refuse** when the name is owned by a different issuer (a search
   with only `keyword=<name>` returns an album whose `createAddress` differs
   and no own-album row exists). Suggested text:
   `collection "<name>" is issued by another issuer (<createMetaId or
   createAddress>) — under issuer-only minting, mints by other addresses are
   never collected. Transfer an existing item instead (mrc721_transfer /
   marketplace), or issue your own album under a different name.`
   This refusal saves the user's fees; keep the tool's BTC-network refusal
   unchanged.
3. When the own album exists: run the existing `totalSupply`/`totalnum`
   exhaustion precheck **against that album's row** (`mintedCount` /
   `totalnum` / `totalSupply` from the search row — the current code checks
   the "earliest same-name album", which under non-unique names may be
   someone else's album with different numbers).
4. When no album exists under the name at all: keep the current
   proceed-with-warning flow (mint-before-register is legitimate
   out-of-order behavior for the future issuer), updating the warning to
   issuer-only wording: the item is collected once **this MetaBot's**
   collection under this name registers; if it never registers, the mint
   never indexes.
5. Update the tool description "any create pin under the collection mints" →
   "only the collection issuer's create pins are collected (issuer-only
   minting); the collection must be issued by this session MetaBot (or not
   yet registered)".
6. `formatMrc721MintResult`'s not-collected branch: replace "stays pending
   until the collection registers" with "stays pending until your collection
   under this name registers (issuer-only minting)".

## 3. R3 (should) — resolve albums by collectionid; make name-based lookups ambiguity-safe

**Where**: `src/main/services/mrc721Service.ts` —
`verifyMrc721Holding` (~740-760), `mrc721_collection_modify`'s client-side
precheck/content merge (~966), `loadMrc721CollectionForCard` consumers.

**Why**: `collection/info?name=` now returns the earliest album under the
name. When several same-name albums exist, a name-only resolution can target
someone else's album (wrong holding verification, wrong merged content for a
modify precheck). The server-side modify effect is already correct (the
indexer resolves the modifier's own album); this is client-side display and
precheck correctness.

**Required behavior**:

1. `verifyMrc721Holding`: when the caller passes only `collectionName`,
   detect ambiguity via `collection/search?keyword=<name>&size=100` — if
   more than one row has that exact `collectionName` (distinct `pinid`),
   fail with an error listing the candidate collectionids and asking for
   `collectionPinId`, instead of silently verifying against the earliest
   album. Single match or explicit `collectionPinId` → unchanged behavior.
2. `mrc721_collection_modify` precheck: resolve the session's own album via
   the R1 issuer-scoped search (never `collection/info?name=`), and verify
   the session address equals the row's `createAddress` before merging
   content client-side (mirrors the server's issuer-only rule).
3. Audit remaining `fetchMrc721CollectionInfo({ name })` call sites (~810,
   ~966): wherever a collectionid is already in hand, pass `pinId` instead.

## 4. R4 (should) — agent-facing text carrying the old protocol

These strings teach the agent the pre-revision semantics; update them in the
same change:

| Where | Current | Required direction |
|---|---|---|
| `mrc721AgentTools.ts` ~336, ~343 (verify output) | `OFFICIAL mint (creator-minted)` vs `third-party mint (open minting)` | `creatorMinted` is always true on MetaSo (issuer-only). Keep the OFFICIAL branch; reword the false branch to "not indexed under issuer-only rules (pre-revision / upstream source)" |
| `mrc721Service.ts` ~303 comment | "official vs third-party mint" basis | comment-only: same rewording |
| Issue tool description + zod (`collection_name`) | "globally unique", "first registration wins" | revised semantics (R1.3) |
| Mint tool description | "any create pin under the collection mints" | issuer-only wording (R2.5) |
| Issue precheck refusal | "First registration wins globally…" | issuer-scoped refusal (R1.1) |
| Mint receipt pending text | "stays pending until the collection registers" | "until your collection … registers" (R2.6) |

## 5. No change needed (do not touch)

- Read surface: paths, params, envelope, DTO spellings — byte-compatible;
  the base already defaults to `https://so.metaid.io`.
- `collection/items/pageList?pinid=` (collectionid-keyed), `item/info`,
  `item/transfers`, `item/verify` — unchanged wire shapes.
- The whole write path for **transfers** (`mrc721_transfer`), the sell
  intent flow, the atomic swap flow and the buy flow — they address items by
  pinId / UTXO outpoint, which the revision does not touch. Ownership
  transfer by non-issuers is fully supported under the revision.
- `metabotNftService` (MetaContract `wallet_nft_*` line) — unrelated to
  MRC-721.
- Rollback caveat: pointing the read base back at
  `https://manapi.metaid.io` returns to pre-revision semantics
  (`creatorMinted` can be false; same-name refusal would be "correct" there
  again). The new logic assumes the MetaSo base; do not support dual
  semantics behind one code path.

## 6. Acceptance checklist

```bash
BASE=https://so.metaid.io/api/mrc721
# Issuer-scoped precheck (R1/R2 building block): conjunctive filters,
# rows carry collectionName / pinid / createAddress / createMetaId /
# totalSupply / totalnum / mintedCount; at least one filter required.
curl -s "$BASE/collection/search?keyword=pixel&size=3"
curl -s "$BASE/collection/search?issuer=<sessionAddress>&keyword=<name>&size=5"
# Name fallback = earliest album (ambiguity exists by design):
curl -s "$BASE/collection/info?name=pixel-sun-v2"     # → earliest album under the name
# Every indexed item is issuer-minted; dense 1..N numbering:
curl -s "$BASE/collection/items/pageList?pinid=<collectionid>&size=5"
# Error shapes unchanged:
curl -s "$BASE/item/info"                             # {"code":404,...,"data":null}
curl -s "$BASE/collection/items/pageList?pinid=ghost" # {"code":100,...,"data":[]}
```

Test expectations for `mrc721AgentTools.test.mjs` / `mrc721Service.test.mjs`:

1. create precheck: own same-name album → refuse; other issuer's same-name
   album → proceed with the shared-name note; name free → proceed.
2. mint: name owned by another issuer → refuse (fee-guard message); own
   album → supply precheck reads the own album's row; free name → proceed
   with the issuer-only pending warning.
3. `verifyMrc721Holding`: ambiguous name without pinId → error listing
   collectionids; with pinId → exact verification.
4. String audit: no surviving "first registration wins globally",
   "open minting", "any create pin under the collection mints" texts.
5. Transfer/sell/swap suites green and untouched.
