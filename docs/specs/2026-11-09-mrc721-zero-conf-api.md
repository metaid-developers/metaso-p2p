# MRC-721 Zero-Confirmation Indexing (R-F) — API & Behavior Spec

Date: 2026-11-09. Implements the IDBots requirement
`nft-zero-conf-indexer-requirements.md` (R-F-1 … R-F-11) plus the owner
decisions confirmed on 2026-11-09 (§5 open questions): provisional numbers
served with `numberFinal:false`, overlay cap 50,000 entries per aggregator,
`zeroConfTrade` switch = env + restart (no hot reload).

Owner directive: **every MRC-721 read surface goes zero-confirmation,
trading included.** MVC has no RBF; a fee-sufficient mempool transaction
confirms with near certainty, so waiting for a block adds latency only.
Trading sits behind its own gate so the owner can move it back to
confirmed-only without a redeploy.

## 0. Design summary

- **Overlay, never mutation.** Confirmed state stays exactly as before in
  the Pebble aggregates (`mrc721nft`, `mrc721`). Mempool effects live in an
  in-memory overlay that is recomputed from the current mempool — a dropped
  transaction's effects simply stop applying, revealing the confirmed state
  beneath. The overlay is never persisted, never part of backfill or
  replay; after a restart it rebuilds from the node mempool within one poll
  plus one full scan.
- **Confirmed wins.** When a pin or spend confirms, the block path ingests
  it first and the overlay entries of that txid drop in the same critical
  section (no duplicate rows, no gap). A confirmed album always wins over
  an overlay registration for the same (name, issuer) pair.
- **Eviction.** Each full-mempool round hands its fully observed txid set
  (ledger convention, `chain.MempoolTxIDLister`) to the
  `aggregator.MempoolOverlayReconciler` implementations; entries whose txid
  is gone are evicted. A failed fetch never reconciles.
- **Deterministic provisional order.** Overlay mints order topologically
  (a parent mint tx before any mint tx spending its outputs — resolved via
  `PinInscription.Inputs` × `PinInscription.WireHash`, correct across MVC
  version≥10 ledger-vs-wire hash divergence), then `firstSeenAt`, then txid.
  Funding chains through non-pin mempool transactions are invisible to
  ancestry, which keeps such mints `numberFinal:false` (conservative).
- **Strict opt-out.** Every MRC-721 endpoint accepts `confirmedOnly=1` and
  then serves exactly the pre-zero-conf bytes (no new fields anywhere).

## 1. Gates (R-F-6)

`config.toml` `[mrc721]` (defaults — owner directive: both on):

```toml
[mrc721]
zeroConfIssuance = true   # R-F-2: mempool collection_desc / mint / item_desc
zeroConfTrade    = true   # R-F-3/R-F-4: ownership moves + sell/offer declarations
```

Env overrides: `METASO_P2P_MRC721_ZERO_CONF_ISSUANCE`,
`METASO_P2P_MRC721_ZERO_CONF_TRADE`. Turning `zeroConfTrade` off restores
the pre-zero-conf trading behavior exactly (no overlay recording, no
incremental spend channel, confirmed-only views) with a restart.

## 2. Wire contract (R-F-5, additive)

New fields appear **only when the relevant gate is on** (absent = the row is
fully confirmed-backed; `confirmedOnly=1` omits them all):

| DTO | Field | Meaning |
|---|---|---|
| collection, item, sell intent, offer, transfer row | `confirmed` (bool) | `false` when any overlay pin or spend produced or changed the row; `true` otherwise (gate-on responses) |
| same | `firstSeenAt` (unix s) | Overlay rows only |
| collection, item | `genesisHeight` | `-1` for overlay registrations and mints (MAN convention) |
| item | `numberFinal` (bool) | `false` when the provisional number could still change (a parallel-funded overlay mint ranked ahead is not a tx ancestor); `true` when every overlay mint ranked ahead is a confirmed pin or a tx ancestor |
| item | `ownershipConfirmed`, `confirmedOwnerAddress` | R-F-3 ownership overlay |
| item | `descConfirmed` | `false` when any served display field was filled by an overlay/staged `item_desc` |
| item | `overSupply` (bool) | Overlay mint past the remaining supply. Only served with `includeOverSupply=1`; hidden by default (out of lists and counts) |
| collection | `mintedCount` | Confirmed + staged + overlay (non-oversupply) — the mint supply precheck surface |
| collection | `confirmedMintedCount` | The confirmed-only count (`totalnum` keeps meaning the confirmed count for byte parity) |
| collection | `contentConfirmed` | `false` while any overlay `collection_desc` modify applies |
| sell intent, offer rows | `confirmed`, `firstSeenAt` | Overlay declaration rows |
| search rows (`collection/search`, `address/collections-issued`) | `confirmed`, `firstSeenAt` | Overlay-created album rows (`mintedCount` = staged + overlay) |

Unknown/extra fields are tolerated by the client contract (unchanged rule).

## 3. Read surfaces (merged view = confirmed base + overlay)

| Endpoint | Overlay behavior |
|---|---|
| `collection/info` (`pinId`/`name`) | Serves overlay-created albums (`confirmed:false`, collectionid = mempool pinId, `genesisHeight:-1`); confirmed albums win per (name, issuer); overlay modifies fold onto the content with `contentConfirmed:false` |
| `collection/pageList` | Confirmed rows + overlay-created albums appended |
| `collection/items/pageList` | Confirmed items (mint order) → staged confirmed mints (chain order — exactly what `drainPending` will apply, R-F-2.3) → overlay mints (topological), provisional numbers `confirmedTotal + rank`; `total` = the overlay-inclusive minted count |
| `item/info` | Overlay/staged mints served (`confirmed:false`); ownership folds chained unconfirmed spends into `address`/`ownerAddress` + `output`/`outputValue` with `ownershipConfirmed:false` + `confirmedOwnerAddress`; the legacy `pending` object stays |
| `item/transfers` | Overlay transfers trail the confirmed history (`confirmed:false`, `height:-1`), `total` includes them; the `pending` array unchanged |
| `address/item`, `address/collection` | Items whose overlay owner moved away hide; overlay/staged items owned by the address join (any chain depth: mint → list → buy → resell, all unconfirmed) |
| `item/verify` | Overlay facts with `confirmed:false`; `collected:true` means provisionally collected |
| `collection/search`, `address/collections-issued` (native source) | Overlay-created albums join in (name, issuer) key order |
| `sell/intents`, `sell/intent/info`, `offers`, `offer/info` | Overlay declarations lead the list; the last overlay pin per item wins; a mempool cancellation hides the intent from `active=true` lists immediately; evicting it restores the confirmed record |

`TrackedOutputs()` (the engine's spend detection input) includes overlay
outputs when the trade gate is on: freshly minted items and freshly
delivered items are spendable targets for chained unconfirmed transfers.
The CONFIRMED block path filters spends against the base set only, so a
mempool-seen spend still settles authoritatively when it confirms.

## 4. Latency (R-F-7)

- Issuance/declaration pins: visible within one `mempoolPollInterval`
  (default 10s) of the tx entering the node mempool; p95 ≤ 20s.
- Spends: tracked-output spend detection runs on EVERY incremental mempool
  poll (with a chaining loop, depth ≤ 8, so mint → buy → resell in one
  batch resolves in one round); the full mempool scan remains for eviction
  reconcile only, configurable `METASO_P2P_ZMQ_MEMPOOL_FULL_SCAN_INTERVAL`
  (clamped 30–60s, default 30s).

## 5. Correctness invariants (R-F-8, test-backed)

1. Confirmation handoff: no duplicate rows, no gap (same-lock drop);
   numbers/fields identical before/after except
   `confirmed`/`genesisHeight`/`numberFinal`/`firstSeenAt`.
2. Eviction: a fully observed round without a txid removes all of its
   effects; failed fetches never reconcile.
3. Restart: overlay empty at boot, rebuilt within one poll + one full scan;
   nothing persisted into Pebble, nothing replayed by backfill.
4. Parallel-funded mints: `numberFinal:false`; the block's order wins and
   the overlay disappears.
5. Supply race: confirmed + staged + overlay (non-oversupply) never exceed
   `totalSupply` in served counts.
6. Reorg: out of scope — the overlay recomputes from the new mempool.
7. Overlay cap: 50,000 entries per aggregator, oldest-first eviction with a
   log line.

## 6. Confirmed-path defect repairs (shipped first, P0)

- **R-F-10** — staged (mint-before-register) mints drain in chain order:
  `PinInscription.TxIndex` (block tx position, MVC adapter) + pin output
  index feed `PendingMint`; `drainPending` sorts by (height, txIndex,
  output index); the totalSupply cut follows the same order. Repairs
  existing albums via `cmd/metaso-p2p-mrc721-renumber` (dry-run default,
  `--apply` executes; run with the daemon STOPPED as the service user;
  prints the affected collectionids for publication).
- **R-F-11** — `item_desc` tolerates any JSON `metadata` (`json.RawMessage`;
  strings verbatim, other values as compact JSON text, per-ref parse, a
  malformed ref is logged and skipped). Previously dropped pins replay via
  `cmd/metaso-p2p-mrc721-replay --pinIds <a,b,c>` (re-parses confirmed pins
  from the node through the live block path; first-write-wins makes replay
  safe).

## 7. Known scope lines

- Ancestry (numberFinal) resolves through pin-carried inputs only; chains
  through non-pin mempool txs read `numberFinal:false` until confirmation.
- item/transfers overlay rows use `height:-1` and trail the confirmed list
  (ascending timeline); clients should read `confirmed:false` rows as
  unconfirmed regardless of position.
- No mempool persistence, no "was pending" history, no fee policing (P2
  feeRate exposure left out).
