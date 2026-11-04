# MRC-721 Index Deployment & Configuration Guide

What to deploy so IDBots (and any client of the same contract) can query
MRC-721 sell-intents, collection search/issuer views, and the full
manapi-compatible NFT surface (collections, items, transfers) from
MetaSo-P2P.
Specs: `docs/specs/2026-11-04-mrc721-sell-collection-api.md` (sell index +
collection views) and `docs/specs/2026-11-05-mrc721-nft-api.md` (native NFT
aggregate).

## 1. Build & run

Standard metaso-p2p build/run (see `docs/DEPLOY.md`). The MRC-721 indexes
ship in the main binary; nothing extra to build except the optional one-shot
backfill tools (`cmd/metaso-p2p-mrc721-backfill` for sell history,
`cmd/metaso-p2p-mrc721-nft-backfill` for the NFT aggregate bootstrap).

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

Nothing to do: the IDBots client ships with this base BUILT IN
(`https://so.metaid.io`, mirroring how it treats manapi.metaid.io) —
`mrc721_market_intents` and `mrc721_search_collections` hit MetaSo
automatically. The `IDBOTS_METASO_API_BASE` env exists only as a
staging/local-testing override.

## 6. What NOT to deploy

man-p2p needs no changes and no new release for any of this:
`/protocols/mrc721sell` pins are ordinary pins in its raw index by design.

## 7. Native NFT aggregate (manapi-compatible /api/mrc721 surface)

Enables the full man-p2p-compatible MRC-721 collection/item/transfer API
(`docs/specs/2026-11-05-mrc721-nft-api.md`): `/api/mrc721/collection/*`,
`/api/mrc721/address/*`, `/api/mrc721/item/*`, `/api/mrc721/admin/replay-pins`,
plus the generic `/api/tx/status`.

Deploy sequence (order matters):

1. Deploy the binary with the gate OFF — nothing changes.
2. Stop the daemon and bootstrap the aggregate from the upstream read
   views (idempotent; the hydrated store is diff-identical to what
   man-p2p currently serves):

   ```bash
   go run ./cmd/metaso-p2p-mrc721-nft-backfill --data-dir <pebble data dir>
   ```

   **Prod lesson (2026-11-05)**: run the bootstrap AS THE SERVICE USER (or
   `chown -R <service-user> <data-dir>/<namespace>` afterwards). A
   root-run backfill leaves a root-owned `<data-dir>/mrc721nft/LOCK`, and
   the daemon (non-root) then fails `Init` with `permission denied` — the
   aggregator silently drops out of the registry and every
   `/api/mrc721/*` route 404s until the ownership is fixed and the daemon
   restarts.

3. Start the daemon with the gate on:

   | Env | Default | Purpose |
   |---|---|---|
   | `METASO_P2P_MRC721_NFT_ENABLED` | `false` | Registers the native aggregate: mounts the manapi-compatible surface, starts live `/nft/mrc721` ingestion, block transfer detection, the 30s mempool pending channel, and hands the collection search / issuer endpoints to the native read model (same wire shape). |
   | `METASO_P2P_MRC721_NFT_MANAPI_BASE_URL` | `https://manapi.metaid.io` | Upstream the bootstrap and the admin replay-pins repair hydrate from. |

4. Verify the surface (curls in the spec §7), then downstream consumers
   switch their base URL. Live transfers/pending visibility are the cutover
   gate: watch `item/info`'s `pending` key and the mempool log lines on a
   real transfer before announcing the switch.

Notes:

- The gate must stay off until the bootstrap has run: the read models are
  empty and the collection search would serve an empty registry.
- The admin replay-pins endpoint repairs individual entities from the
  upstream single-object views (same idempotency/never-rewind rules); it
  carries man-p2p's no-auth posture.
- Mempool pending visibility costs one full mempool RPC sweep per 30s
  (man-p2p's cadence).
