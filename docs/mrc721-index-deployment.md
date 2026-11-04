# MRC-721 Index Deployment & Configuration Guide

What to deploy so IDBots (and any client of the same contract) can query
MRC-721 sell-intents and collection search/issuer views from MetaSo-P2P.
Spec: `docs/specs/2026-11-04-mrc721-sell-collection-api.md`.

## 1. Build & run

Standard metaso-p2p build/run (see `docs/DEPLOY.md`). The MRC-721 indexes
ship in the main binary; nothing extra to build except the optional one-shot
backfill tool (`cmd/metaso-p2p-mrc721-backfill`).

## 2. Required configuration

| Env / config key | Default | Purpose |
|---|---|---|
| `METASO_P2P_HTTP_ADDR` | `:8080` | HTTP listen address — the four `/api/mrc721/*` endpoints are served here. |
| MRC-721 collection sync block (`mrc721Collection` in config.toml / `METASO_P2P_MRC721_COLLECTION_*` envs) | disabled | Enables the collection read model (search + collections-issued). Set `ENABLED=true` and point `MANAPI_BASE_URL` at the production manapi-compatible source (default `https://manapi.metaid.io`). Refresh loop defaults to 10 min. |
| MRC-721 sell backfill block (`mrc721SellBackfill` / `METASO_P2P_MRC721_SELL_BACKFILL_*` envs) | disabled | Optional daemon-startup backfill of `/protocols/mrc721sell` history from the manapi pin-path source. Live ingestion (block loop) always runs. |

Notes:

- Live sell-intent ingestion needs no flag: every confirmed block's pins at
  `/protocols/mrc721sell` are indexed automatically (create-only,
  latest-pin-wins per itemPinId). Mempool pins are deliberately not indexed
  (chain-order-wins semantics; see the spec §1.1).
- The collection read model is a full-sweep upsert-by-pinid mirror of the
  upstream pageList — rows are never deleted; staleness equals the last
  successful sweep.

## 3. Historical backfill (recommended on first deploy)

Either enable the daemon-startup backfill block, or run the one-shot tool
once with the daemon stopped:

```bash
go run ./cmd/metaso-p2p-mrc721-backfill --data-dir <pebble data dir>
# defaults to a full-history replay; idempotent
```

## 4. Verify the endpoints

```bash
BASE=http://127.0.0.1:8080
curl -s "$BASE/api/mrc721/sell/intents?size=5"                 # expect {"code":1,...,data:{list:[...]}}
curl -s "$BASE/api/mrc721/sell/intent/info?itemPinId=<pinid>"  # single row or code!=1 miss
curl -s "$BASE/api/mrc721/collection/search?keyword=<kw>"      # collection rows + total
curl -s "$BASE/api/mrc721/address/collections-issued?address=<addr>"
```

## 5. Wire the client (IDBots)

Set on the IDBots host:

```
IDBOTS_METASO_API_BASE=http://<metaso-host>:8080
```

With that set, IDBots' `mrc721_market_intents` and
`mrc721_search_collections` route to MetaSo first (honest fallback to the
manapi interim otherwise). No other IDBots configuration changes.

## 6. What NOT to deploy

man-p2p needs no changes and no new release for any of this:
`/protocols/mrc721sell` pins are ordinary pins in its raw index by design.
