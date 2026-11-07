# MRC-721 Protocol Revision Proposal: Issuer-Only Minting and Non-Unique Collection Names

Status: **agreed** (2026-11-07, aligned among the protocol authors) — the
original docs.metaid.io page predates this revision and is being updated
separately; any residual ambiguity there should be read against this
document. The MetaSo indexer implements these semantics on its native read
model (branch `feat/mrc721-issuer-only`).

Date: 2026-11-07

## 1. Background

The current MRC-721 doc (docs.metaid.io, `ft-and-nft-protocols/mrc-721`)
defines the album paths and the `totalSupply` validity rule, but leaves two
things unspecified:

1. **Who may mint.** Nothing states whether users other than the collection
   issuer may create NFT pins under `/nft/mrc721/{collection_name}`.
2. **Whether `collection_name` is unique.** Nothing requires global
   uniqueness, yet the rule "if an album has multiple `collection_desc`
   pins, only the first counts, later ones are ignored" combined with
   "MRC-721 uses the path to distinguish albums" effectively makes the
   first `collection_desc` author the permanent, untransferable owner of
   the name — a scarcity with no registry behind it.

The unspecified mint permission has a concrete failure mode: with the
first-come-first-served indexing rule ("only the first `totalSupply` pins
under the album path are valid"), any user can pin arbitrary junk under an
open album path and consume the supply slots. An issuer who wants
"large or unlimited supply, all items minted by me" (e.g. dynamically
generated artwork signed and published by the issuer) cannot express that
at all.

## 2. Revised semantics

### 2.1 Album identity

- The album's index key is **`collectionid`** — the pinid of its
  `collection_desc` pin (already stated by the current doc; this revision
  makes it the primary identity).
- The human-level identity is the pair **(collection_name, issuer
  address)**. `collection_name` is **not globally unique** and confers no
  ownership: any user may create an album using any valid path name.
- Two albums sharing a name are independent albums, exactly like two ERC-721
  contracts sharing a display name.

### 2.2 collection_desc

- Per (collection_name, issuer address), only the **first** confirmed
  `collection_desc` create pin counts; later creates by the same address
  under the same name are ignored (idempotent re-registration).
- A `collection_desc` by a different address under the same name defines a
  **separate, independent album** — it is not ignored, and it does not
  affect the first album.
- `collection_desc` modify remains issuer-only and forms a version chain
  (unchanged from the current doc). Lowering `totalSupply` gates future
  mints only.

### 2.3 Minting (the new permission rule)

- **Issuer-only.** A create pin under `/nft/mrc721/{collection_name}`
  (any sub-path that is not `collection_desc`/`item_desc`) is indexed as a
  mint of the album identified by (collection_name, minter address) — i.e.
  the album under that name whose `collection_desc` author is the minter
  itself.
- Pins by users who have no registered album under that name are **not
  attributed to anyone's album**. They are staged (or rejected) per the
  indexer's out-of-order rules and only ever commit to an album registered
  by the same address.
- Consequence: `totalSupply` counts only the issuer's own mints; third
  parties cannot consume supply slots, and cannot insert items into someone
  else's album.
- `totalSupply` range and default are unchanged: [-1, 1e12], -1 = unlimited.

### 2.4 item_desc

- An `item_desc` pin backfills item metadata by pinid reference, unchanged,
  with one new gate: a reference is applied **only when the `item_desc`
  author equals the issuer of the album the referenced item belongs to**.
  This closes the description-poisoning hole (anyone backfilling names onto
  someone else's items).

### 2.5 Transfers

Unchanged. Non-issuer addresses freely hold and transfer items after mint;
the issuer-only rule governs minting (genesis attribution), not ownership.

## 3. Rationale

1. **The issuer is the value anchor.** MetaID is identity-centric: an
   album is valuable because of who issued it, not what it is named. The
   revised model matches the industry-proven structure (ERC-721 collection
   identity = contract address; display names are non-unique;
   impersonation is handled by anchoring to the address, at the
   presentation layer).
2. **No registry, no race.** Album creation becomes a conflict-free O(1)
   operation — no name squatting, no first-claim monitoring, no griefing
   via supply-slot consumption. This is the shape that survives millions of
   users arriving.
3. **Issuer-only completes non-uniqueness.** Without a mint permission
   rule, non-unique names would make mint attribution undefined; without
   non-uniqueness, issuer-only would let a first-claim squatter lock a
   name against its rightful issuer. The two rules only work together.

## 4. Compatibility and impact

- **Divergence**: indexers implementing the old open-mint / unique-name
  semantics (e.g. the current manapi behavior) will report different item
  sets for albums where third-party pins exist under the album path. This
  divergence is intentional and is the point of the revision.
- **Downstream keying**: consumers must address albums by `collectionid`
  (collection_desc pinid), not by name. Name-based lookups are ambiguous by
  design; indexers may resolve them to the earliest-created album under the
  name as a compatibility fallback.
- **`creatorMinted`**: under issuer-only minting every indexed item is
  minted by the issuer, so the flag is always true; the field is kept for
  wire compatibility.
- **Impersonation**: same-name albums by other issuers are possible by
  design; "official" attribution is a presentation-layer decision anchored
  on the issuer address / collectionid, not a protocol guarantee.

## 5. MetaSo indexer implementation notes

- Native read model (`internal/aggregator/mrc721nft`): collection main key
  moves from `collectionName` to (collectionName, issuer address) with the
  `collectionid` reverse lookup; in-collection order, holder index and
  out-of-order staging keys gain the issuer dimension.
- All collection resolution in the query API switches to `collectionid`;
  name-based parameters fall back to the earliest album under the name.
- The store is rebuilt from genesis via bootstrap replay — the protocol is
  young and on-chain volume is minimal, so no online migration is needed.
