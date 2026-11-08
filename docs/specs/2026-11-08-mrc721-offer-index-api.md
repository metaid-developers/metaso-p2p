# MRC-721 Passive-Offer Index (metaso-p2p, R-E)

Status: **implemented** on branch `feat/mrc721-offer-index` (2026-11-08),
flag-gated (`METASO_P2P_MRC721_OFFER_ENABLED`, default off until the
acceptance pass). Consumers: the IDBots host (`mrc721_offers_market` /
`mrc721_take_offer` market discovery) and any client of the same contract.

Related protocol: IDBots `docs/nft-offer-protocol.md` (frozen, owner-approved
2026-11-08), `docs/nft-offer-indexer-requirements.md` (requirement ids
R-E-1..R-E-3), `docs/nft-open-market-psbt-settlement.md` (design + security
analysis). The `/protocols/mrc721sell` slice (spec
`2026-11-04-mrc721-sell-collection-api.md` §1) is the implementation template
for everything here.

## 0. Architecture decision

The offer index follows the sell-intent architecture exactly: all MRC-721
business indexing lives in MetaSo-P2P, man-p2p is untouched (an offer pin is
an ordinary raw pin there). The R-E-3 delivery-output rule lands in THIS
repo: the item ownership served by `/api/mrc721/item/info` is computed
locally — the mrc721nft aggregate feeds its tracked item outpoints to the
MVC chain adapter (`internal/chain/mvc`, `DetectTransfers`) and folds the
detected spends back through `HandleBlockTransfers`; the upstream manapi
source only hydrates historical entities at bootstrap. No upstream change is
needed for offer settlements to resolve to the buyer.

## 1. Offer index (R-E-1)

### 1.1 Source paths

- Live: confirmed chain pins routed through the aggregator registry
  (`HandleBlockPin`). Pins whose normalised path is exactly
  `/protocols/mrc721offer` (version-suffixed `@<pinId>` paths normalise to
  the same) are ingested; every other path is ignored.
- Mempool: deliberately NOT ingested (same ruling as sell intents —
  chain-order-wins is only safe inside the block loop).
- Gate: both ingest and routes are gated by
  `METASO_P2P_MRC721_OFFER_ENABLED` / config `mrc721.offer.enabled`
  (default off). While the gate is off the offer paths are ignored and the
  routes 404; the protocol is brand new, so enabling starts the index from
  the live feed with no historical backfill.

### 1.2 Semantics

- Create-only: `create` operations are ingested; `modify`/`revoke` on the
  path are ignored (the protocol never uses them).
- Latest-wins per payload `itemPinId`: an incoming offer replaces the stored
  one only when it is strictly later in chain order, compared by (genesis
  height, chain time in seconds, offer pin id). Re-price / re-list = a new
  pin that naturally supersedes; older pins re-arriving never overwrite
  newer state; the exact same offer pin re-applying is a no-op (idempotent
  replay).
- Cancellation is a new pin with `"active": false` — an ordinary row, kept
  queryable (never deleted); the default list serves active offers only,
  and the info lookup returns the latest declaration whatever its active
  state. An offer whose item UTXO moves in any other way stays indexed:
  the index is an advertisement, chain state is the truth; clients
  re-verify the holder view and outpoint before taking.
- Required payload fields: `itemPinId` (`<64hex>i<n>`), `active` (boolean),
  `priceSats` (integer — a mirror for filtering; the authority is output0
  inside the signed tx), `offerTx` (base64 TxComposer JSON envelope).
  Unknown/extra fields are ignored, never rejected. Keys resolve
  exact-camelCase first, then case-insensitively with underscores folded;
  the body may be raw JSON, a quoted JSON string, or base64-wrapped JSON.
  Unparseable/invalid payloads are logged and skipped — they never fail the
  block pipeline.
- `offerTx` is stored and served VERBATIM: no parsing, no validation, no
  normalisation (the buyer's signature verification depends on byte
  identity with the on-chain payload). `sellerMetaId` is not a payload
  field — the seller identity is the offer pin creator (`pin.MetaId`,
  falling back to `pin.CreateMetaId`).
- Storage (Pebble namespace `mrc721`): `offer:<itemPinId>` holds the latest
  record; `otime:<invertedSeq>:<itemPinId>` is the recency index (newest
  write first) — the sell-intent key scheme with disjoint prefixes.

### 1.3 Endpoints

Both endpoints answer with the IDBots MRC-721 envelope (`{"code": 1,
"message": "ok", "data": ...}`, failures non-1 at HTTP 200) and are public
reads without authentication — identical to the sell-intent family.

`GET /api/mrc721/offers?itemPinId=&sellerMetaId=&collectionPinId=&active=&cursor=&size=`

- All filters optional, conjunctive. `sellerMetaId` (the pin creator) is
  exact; `itemPinId` short-circuits to the single record. `collectionPinId`
  joins through the item → collection relation of the native mrc721nft read
  model when available ("join where available"); with the join source
  absent the rows omit the collection fields and the filter matches
  nothing. `active` accepts `true|false|1|0` and DEFAULTS TO TRUE (the
  endpoint serves open offers; `active=false` lists cancellation rows).
- Ordering: newest winning offer first (chain order). Pagination is a
  cursor over the local write sequence (rows never shuffle between pages);
  `size` defaults to 20, max 100.
- `data`: `{"list": [rows], "total": <match count>, "cursor": "<next>"}`;
  `cursor` is omitted at the end of the list and `list` is always an array.

Row shape (camelCase plus the lowercase duplicate spellings the IDBots
parser tolerates; both spellings always carry the same value):

```json
{
  "itemPinId": "...", "itempinid": "...",
  "offerPinId": "...", "offerpinid": "...",
  "active": true,
  "priceSats": 30000, "pricesats": 30000,
  "offerTx": "<base64 TxComposer envelope, verbatim>",
  "offertx": "<same bytes>",
  "sellerMetaId": "...", "sellermetaid": "...",
  "collectionPinId": "...", "collectionpinid": "...",
  "collectionName": "...", "collectionname": "...",
  "createdAt": 1794000000, "createdat": 1794000000,
  "updatedAt": 1794000010, "updatedat": 1794000010,
  "chainName": "mvc", "chainname": "mvc",
  "height": 905, "txId": "<64hex>", "txid": "<64hex>"
}
```

`createdAt` comes from the payload (epoch seconds, millisecond values
normalised); `sellerMetaId`/`updatedAt`/`height`/`txId`/`chainName` are
facts of the winning offer pin; `collectionPinId`/`collectionName` are the
best-effort join (omitted when unknown).

`GET /api/mrc721/offer/info?itemPinId=<itemPinId>`

- The latest offer for ONE ITEM — the lookup key is `itemPinId` (the client
  keys offers per item, latest-wins), NOT the offer's own pinId.
- Full row including `offerTx`. Miss (no declaration for the item) =
  `{"code": 40400, ...}`; missing/empty parameter = 40000. Empty/unknown
  items never 500.

## 2. Delivery-output layout rule (R-E-3)

The one consensus-facing change — a convention, not a contract. The
passive-offer settlement tx mirrors the directed swap:

```
inputs[0]  = the item UTXO (seller-signed SIGHASH_SINGLE|FORKID|ACP)
outputs[0] = P2PKH(priceSats → seller)     ← inside the seller's signature
outputs[1] = P2PKH(itemValue → buyer)      ← the DELIVERY output
outputs[2+]= buyer change (optional)
```

Landing: `internal/chain/mvc/indexer.go` (`transferOwnerAddress` fast path
via `offerDeliveryOutput`) — the chain adapter both transfer consumers
derive from. Rule implemented:

- When the tracked item UTXO is input0 (or the tx is single-input) AND
  output0's value differs from the spent item's value (it is the price
  payment) AND output1 carries exactly the item value (the client's
  guarantee, used as the double-insurance), the new owner is read from
  output1 (`Output = <ledgerTxid>:1`, `Location = <ledgerTxid>:1:0`,
  `OutputValue = itemValue`).
- Every other shape keeps the legacy output-0 read: plain transfers
  (output0 value == item value), ambiguous layouts (output1 value
  mismatch), and spent-value lookup failures (RPC hiccup → no behaviour
  change).

Both the confirmed-block path (`DetectTransfers`) and the advisory mempool
pending path (`DetectMempoolSpends`) share `transferOwnerAddress`, so
pendings resolve to the buyer too. The mrc721nft aggregate itself needs no
change — it consumes the notices (`applyTransferHook`) and updates
`item.OwnerAddress` / holder views / transfer history as before.

## 3. Configuration

| Env / config key | Default | Purpose |
|---|---|---|
| `METASO_P2P_MRC721_OFFER_ENABLED` (`mrc721.offer.enabled`) | `false` | Gates the passive-offer index: live ingest of `/protocols/mrc721offer` pins plus `GET /api/mrc721/offers` and `GET /api/mrc721/offer/info`. Keep off until the acceptance pass; enable starts the index from the live feed (no backfill). |

No new knobs besides the gate: sizes/cursors reuse the house clamps, and
the mempool ruling is a fixed no-op like the sell index.

## 4. Verification (acceptance mapping)

1. Ingest unit tests (`internal/aggregator/mrc721/offer_test.go`):
   create-only (modify ignored), latest-wins incl. out-of-order re-apply,
   cancellation row kept, unknown-field tolerance, malformed-payload skip,
   flag-off gating.
2. Endpoint tests (`offer_api_test.go`): default active-only list, active
   filter, seller/item/collection filters, pagination envelope, info 40400
   miss + 40000 empty param, join present/absent.
3. `offerTx` round-trip byte-identity asserted at ingest, in the list rows,
   and in the info row (whitespace/+/- preserved).
4. R-E-3 tests (`internal/chain/mvc/transfer_test.go`): settlement layout
   resolves the buyer from output1 (with/without change outputs), ambiguous
   shape and value-lookup failure keep the legacy read, plain transfer
   untouched, mempool path identical.
5. Live E2E with the IDBots dev build (bot A publishes → offer visible on
   `/api/mrc721/offers` → bot B takes → tx confirms → item owner resolves
   to the buyer) is the deployment gate — see
   `docs/mrc721-index-deployment.md` §8 for the curl snippets.
