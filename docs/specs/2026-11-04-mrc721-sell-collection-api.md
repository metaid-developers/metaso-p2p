# MRC-721 Sell-Intent Index and Collection Views (metaso-p2p)

Status: **implemented** on branch `feat/mrc721-indexes` (2026-11-04).
Consumers: the IDBots host (`mrc721_market_intents`, sell-intent service) and
any MetaWeb client that needs MRC-721 market discovery reads.

Related protocol: `IDBots docs/nft-sell-protocol-draft.md` §1–§5 (FINAL,
implemented host-side). Indexer requirement ids: R-C (sell-intent index),
R-A (collection search), R-B (issuer view).

## 0. Architecture decision (owner, 2026-11-05)

ALL NFT-related business indexing lives in MetaSo-P2P. man-p2p stays a pure
raw-pin indexer and is NOT modified by this feature: `/protocols/mrc721sell`
pins are ordinary pins in man-p2p's raw index, and MetaSo subscribes to them
through the same manapi-compatible pin source every other metaso feature
uses (`publishedcontent.BackfillClient` for replays, the chain-driven
aggregator feed for live pins). The MRC-721 collection views likewise
consume the existing manapi-compatible collection page list instead of
re-indexing `/nft/mrc721` collection pins.

## 1. Sell-intent index (R-C)

### 1.1 Source paths

- Live: confirmed chain pins routed through the aggregator registry
  (`HandleBlockPin`). Pins whose normalised path is exactly
  `/protocols/mrc721sell` (version-suffixed `@<pinId>` paths normalise to
  the same) are ingested; every other path is ignored.
- Mempool: deliberately NOT ingested (`HandleMempoolPin` is a no-op). The
  protocol is an append-only declaration log whose semantics are "latest
  pin by chain order wins per itemPinId"; folding unconfirmed pins into the
  latest-wins record would let a mempool re-declare overwrite a confirmed
  cancellation with no recovery path. Latency is one confirmation.
- Backfill: `BackfillSell` pages the MANAPI path listing
  (`/pin/path/list?path=/protocols/mrc721sell`, newest-first) and replays
  pins oldest-first, so overwrites settle exactly like live aggregation.

Triggers: `METASO_P2P_MRC721_SELL_BACKFILL_ENABLED=true` runs one replay at
daemon startup (lookback/timeout/page size configurable); the one-shot
`cmd/metaso-p2p-mrc721-backfill` builds/repairs the index offline
(idempotent, safe to re-run).

### 1.2 Semantics

- Create-only: `create` operations are ingested; `modify`/`revoke` on the
  path are ignored (the protocol never uses them).
- Latest-wins per payload `itemPinId`: an incoming declaration replaces the
  stored one only when it is strictly later in chain order, compared by
  (genesis height, chain time in seconds, sell pin id) — the pin id only
  breaks same-tx ties deterministically. Older pins re-arriving from a
  backfill/live overlap never overwrite newer state, and the exact same
  sell pin re-applying is a no-op (idempotent replay).
- Cancellation is a new declaration with `"active": false`; a later
  re-declaration with `active: true` wins again.
- Required payload fields: `itemPinId` (`<64hex>i<n>`), `sellerAddress`
  (base58 MVC), `active` (boolean). Everything else is advisory. Keys
  resolve exact-camelCase first, then case-insensitively with underscores
  folded (`itemPinId` / `itempinid` / `collection_name` all resolve), and
  the body may be raw JSON, a quoted JSON string, or base64-wrapped JSON.
  Unparseable/invalid payloads are logged and skipped — they never fail the
  block pipeline.
- Storage (Pebble namespace `mrc721`): `intent:<itemPinId>` holds the
  latest record; `itime:<invertedSeq>:<itemPinId>` is the recency index
  (newest write first). Overwrites delete the item's previous index
  position before writing the new one; scans skip-and-delete stale or
  dangling index entries, so the index self-heals after a crash between
  writes.

### 1.3 Endpoints

Both endpoints answer with the IDBots MRC-721 envelope:

```json
{"code": 1, "message": "ok", "data": ...}
```

Failures carry a non-1 code with an explanatory message (HTTP 200); the
IDBots client treats `code !== 1` as a structured miss/error.

`GET /api/mrc721/sell/intents?collectionPinId=&collectionName=&itemPinId=&sellerAddress=&active=&cursor=&size=`

- All filters optional, conjunctive. `collectionName` matches
  case-insensitively; `sellerAddress` is exact (base58 is case-sensitive);
  `active` accepts `true|false|1|0`; `itemPinId` short-circuits to the
  single record.
- Ordering: newest winning declaration first. Pagination is a cursor over
  the local write sequence, so rows never shuffle between pages. `size`
  defaults to 20, max 100.
- `data`: `{"list": [rows], "total": <match count>, "cursor": "<next>"}`;
  `cursor` is omitted at the end of the list and `list` is always an array.

Row shape (camelCase plus the lowercase duplicate spellings the IDBots
parser tolerates; both spellings always carry the same value):

```json
{
  "itemPinId": "...", "itempinid": "...",
  "sellPinId": "...", "sellpinid": "...",
  "active": true,
  "collectionName": "...", "collectionname": "...",
  "collectionPinId": "...", "collectionpinid": "...",
  "itemOutpoint": {"txid": "...", "vout": 0, "satoshis": 1},
  "sellerAddress": "...", "selleraddress": "...",
  "sellerMetaId": "...", "sellermetaid": "...",
  "priceHintSats": 30000, "pricehintsats": 30000,
  "acceptedCurrencies": ["space", "fiat:CNY"],
  "acceptedcurrencies": ["space", "fiat:CNY"],
  "settlement": ["atomic-swap", "escrow"],
  "note": "...",
  "createdAt": 1793868000, "createdat": 1793868000,
  "expiresAt": 1794472800, "expiresat": 1794472800,
  "updatedAt": 1793868010, "updatedat": 1793868010,
  "chainName": "mvc", "chainname": "mvc",
  "height": 905, "txId": "<64hex>", "txid": "<64hex>"
}
```

`createdAt`/`expiresAt` come from the payload (epoch seconds, millisecond
values normalised); `updatedAt`/`height`/`txId`/`chainName` are chain facts
of the winning sell pin. `itemOutpoint` is omitted when the payload had
none.

`GET /api/mrc721/sell/intent/info?itemPinId=`

- Single-record lookup with the same row shape. Unknown item:
  `{"code": 40400, "message": "sell intent not found"}` (the client reads
  this as `null`). Missing parameter: code 40000.

## 2. Collection search (R-A) and issuer view (R-B)

### 2.1 Read model and refresh policy

MetaSo does not re-index `/nft/mrc721` collection pins. A read model
(`coll:<pinid>` + `ctime:` recency index, same namespace) is refreshed from
the manapi-compatible collection page list
`GET {base}/api/mrc721/collection/pageList?cursor=&size=` (envelope-tolerant
decode: `{code,data:{list,cursor}}` and flat `{code,list,cursor}` both
parse; codes 0 and 1 both mean success; malformed rows are skipped).

Refresh policy (documented choice): **full-sweep upsert by pinid**.
- The collection registry is append-mostly (a collection_desc pin's FIRST
  declaration wins; items mint later), so rows are only ever added or
  refreshed — never deleted.
- Unchanged rows keep their original write sequence (no position churn);
  changed rows (e.g. mintedCount moved) refresh.
- Triggers: the daemon loop (`METASO_P2P_MRC721_COLLECTION_ENABLED=true`,
  one initial sweep then every `METASO_P2P_MRC721_COLLECTION_SYNC_INTERVAL`,
  default 10m) and the collection phase of `cmd/metaso-p2p-mrc721-backfill`.
- Staleness: queries serve the last successful sweep; a failed sweep logs a
  warning and keeps the previous data.

### 2.2 Endpoints

`GET /api/mrc721/collection/search?keyword=&issuer=&cursor=&size=`

- `keyword`: case-insensitive substring over `collectionName`, `name`,
  `desc`, OR an exact (case-insensitive) `pinid` match.
- `issuer`: matches `createAddress` exactly (base58 is case-sensitive) OR
  `createMetaId` case-insensitively.
- At least one of `keyword`/`issuer` is required (else code 40000).
- Ordering: most recently refreshed row first; same cursor mechanics as the
  sell list.
- `data`: `{"list": [collectionRows], "total": <n>, "cursor": "<next>"}`.

`GET /api/mrc721/address/collections-issued?address=&cursor=&size=`

- Rows whose `createAddress` equals the requested address (exact). Missing
  address: code 40000.

Collection row shape (mirrors the MRC-721 collection summary the IDBots
client reads; absent upstream fields stay omitted):

```json
{
  "collectionName": "pixel-cats",
  "name": "Pixel Cats",
  "desc": "...",
  "totalSupply": 100,
  "totalnum": 100,
  "royaltyRate": 5,
  "pinid": "<collection_desc pinid>",
  "createAddress": "...",
  "createMetaId": "...",
  "createTime": 1793868000,
  "cover": "metafile://...",
  "genesisChainName": "mvc",
  "mintedCount": 42
}
```

## 3. Configuration

| Env var | Default | Meaning |
|---------|---------|---------|
| `METASO_P2P_MRC721_SELL_BACKFILL_ENABLED` | `false` | Replay the sell declaration log at daemon startup |
| `METASO_P2P_MRC721_SELL_BACKFILL_LOOKBACK` | `1440h` | Only replay pins newer than this |
| `METASO_P2P_MRC721_SELL_BACKFILL_TIMEOUT` | `2m` | Startup replay time budget |
| `METASO_P2P_MRC721_SELL_BACKFILL_PAGE_SIZE` | `100` | MANAPI page size |
| `METASO_P2P_MRC721_SELL_BACKFILL_MANAPI_BASE_URL` | `https://manapi.metaid.io` | Pin source base |
| `METASO_P2P_MRC721_COLLECTION_ENABLED` | `false` | Collection read-model refresh loop |
| `METASO_P2P_MRC721_COLLECTION_SYNC_INTERVAL` | `10m` | Sweep interval |
| `METASO_P2P_MRC721_COLLECTION_TIMEOUT` | `1m` | Per-sweep time budget |
| `METASO_P2P_MRC721_COLLECTION_PAGE_SIZE` | `100` | pageList page size |
| `METASO_P2P_MRC721_COLLECTION_MANAPI_BASE_URL` | `https://manapi.metaid.io` | Collection source base |

## 4. Manual backfill

```bash
go run ./cmd/metaso-p2p-mrc721-backfill \
  --data-dir /path/to/pebble \
  --manapi-base-url https://manapi.metaid.io \
  --collection-base-url https://manapi.metaid.io \
  --page-size 100            # --lookback 0 = full history (the default;
                             # a positive lookback narrows the sell replay)
# flags: --skip-sell / --skip-collection to run one phase only
```

The daemon must not run concurrently against the same data dir (Pebble
single-writer); the command is idempotent and prints a per-phase report
(sell: fetched/applied/skipped/errors; collection:
pages/fetched/upserted/skipped).
