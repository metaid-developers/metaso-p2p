# metaso-p2p Deployment Guide

## Prerequisites

- **Go 1.26+** (for source builds) or **Docker** (for containerized deployment)
- **No external database** — metaso-p2p uses PebbleDB (embedded), no MongoDB/MySQL/Redis needed
- Port **8080** available (configurable via `METASO_P2P_HTTP_ADDR`)

## Quick Start (Source)

```bash
# Build the binary
go build -o metaso-p2p ./cmd/metaso-p2p/

# Run with defaults (PebbleDB in ./data/pebble, HTTP on :8080)
./metaso-p2p
```

The server starts with sensible defaults:
- HTTP on `:8080`
- Health check at `/healthz`
- Socket.IO on `/socket/socket.io` (primary) and `/socket.io` (legacy)
- PebbleDB data in `./data/pebble`
- All aggregators (userinfo, groupchat, notify) enabled

## Docker Deployment

### Build

```bash
docker build -t metaso-p2p .
```

The Dockerfile uses a multi-stage build:
1. **Stage 1** (`golang:1.26-alpine`): compiles the Go binary with `CGO_ENABLED=0` and stripped symbols.
2. **Stage 2** (`alpine:3.21`): copies only the binary, adds CA certs and tzdata. Final image is under 50 MB (about 40 MB on arm64 — the static Go binary alone is ~30 MB; further compression with UPX or a `scratch` base is a potential future optimization).

### Run

```bash
# Basic run with default data directory
docker run -d \
  --name metaso-p2p \
  -p 8080:8080 \
  -v ./data:/data \
  metaso-p2p
```

### Run with custom configuration

```bash
docker run -d \
  --name metaso-p2p \
  -p 8080:8080 \
  -v ./data:/data \
  -e METASO_P2P_CACHE_MAX_ENTRIES=20000 \
  -e METASO_P2P_CACHE_DEFAULT_TTL_SECONDS=600 \
  -e METASO_P2P_SOCKET_MAX_CONNECTIONS=20000 \
  metaso-p2p
```

### Docker Compose (example)

```yaml
version: "3.8"
services:
  metaso-p2p:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - metaso-p2p-data:/data
    environment:
      METASO_P2P_PEBBLE_DATA_DIR: /data/pebble
      METASO_P2P_CACHE_MAX_ENTRIES: 20000
    restart: unless-stopped

volumes:
  metaso-p2p-data:
```

## Health Check

```bash
# Check if the server is alive
curl http://localhost:8080/healthz
```

Expected response:
```json
{"code": 0, "data": {"status": "ok", "service": "metaso-p2p", "version": "dev"}, "message": "", "processingTime": <ms>}
```

## Key Environment Variables

All configuration is via environment variables with the `METASO_P2P_` prefix.
See `config.example.toml` for the complete reference. The most important ones:

| Variable | Default | Description |
|---|---|---|
| `METASO_P2P_HTTP_ADDR` | `:8080` | Listen address |
| `METASO_P2P_HEALTH_PATH` | `/healthz` | Health check path |
| `METASO_P2P_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout |
| `METASO_P2P_PEBBLE_ENABLED` | `true` | Enable persistent storage |
| `METASO_P2P_PEBBLE_DATA_DIR` | `./data/pebble` | PebbleDB data directory |
| `METASO_P2P_SOCKET_ENABLED` | `true` | Enable Socket.IO server |
| `METASO_P2P_SOCKET_PATH` | `/socket/socket.io` | Primary Socket.IO path |
| `METASO_P2P_SOCKET_LEGACY_PATH` | `/socket.io` | Legacy Socket.IO path |
| `METASO_P2P_SOCKET_MAX_CONNECTIONS` | `10000` | Max concurrent connections |
| `METASO_P2P_SOCKET_ALLOW_EIO3` | `true` | Allow EIO v3 clients (required for idchat) |
| `METASO_P2P_PROFILE_ENABLED` | `true` | Enable user profile resolution |
| `METASO_P2P_PROFILE_MODE` | `local-first` | Profile resolution mode |
| `METASO_P2P_PROFILE_REMOTE_BASE_URL` | `` | Remote profile service URL |
| `METASO_P2P_CACHE_MAX_ENTRIES` | `10000` | L1 LRU cache size |
| `METASO_P2P_CACHE_DEFAULT_TTL_SECONDS` | `300` | Cache TTL in seconds |
| `METASO_P2P_GROUPCHAT_MIGRATION_ENABLED` | `true` | Enable migration endpoints |
| `METASO_P2P_SOCKET_EXTRA_PUSH_AUTH_KEY` | `` | Push auth key (leave empty for dev) |

## Data Persistence

metaso-p2p stores all data in **PebbleDB**, an embedded key-value store (no external database server).

- **Default location**: `./data/pebble` (relative to working directory)
- **Docker**: bind-mount a host directory to `/data` and set `METASO_P2P_PEBBLE_DATA_DIR=/data/pebble`
- All indexed messages, user info, blocked-chat lists, and cursor state are persisted here
- To reset: stop the server, delete the data directory, restart
- For a new node that should start from a prebuilt index instead of replaying
  full history, restore a bootstrap snapshot into the Pebble directory before
  the first start. See [`docs/BOOTSTRAP.md`](./BOOTSTRAP.md).

### Backup & Recovery

There are two normal operator workflows:

- **Plain backup copy**: quick offline backup/rollback for the same node or the
  same operator-controlled directory layout.
- **Bootstrap snapshot**: standard artifact for seeding another node or moving
  indexed state between hosts with manifest and checksum checks. See
  [`docs/BOOTSTRAP.md`](./BOOTSTRAP.md).

#### Plain backup copy

PebbleDB is a single directory of files. To back up an offline node:

```bash
# Stop the server first, then:
cp -r ./data/pebble ./data/pebble-backup-$(date +%Y%m%d)
```

To restore, stop the server, replace the data directory with the backup, then
restart.

Use this when you already have the exact directory you want to put back and do
not need a packaged artifact.

#### Bootstrap snapshot

Use bootstrap snapshots when the goal is to seed a new node from an already
indexed source node, or to hand off a standard restore artifact between hosts.

Pack on the stopped source node:

```bash
scripts/bootstrap-pack.sh \
  --data-dir ./data/pebble \
  --output-dir ./artifacts \
  --network mainnet \
  --source-node prod-node-a
```

Restore on the target node before starting the service:

```bash
scripts/bootstrap-restore.sh \
  --archive ./artifacts/metaso-p2p-bootstrap-mainnet-<timestamp>.tar.gz \
  --target-dir ./data/pebble
```

If the target directory already contains data that should be replaced, use
`--force`. That moves the old directory to a sibling backup path before the new
snapshot is installed.

Prefer bootstrap restore over plain backup copy when the source and target are
different nodes and you want a repeatable artifact boundary with
`manifest.json` and checksum verification.

## Connecting idchat

See [`docs/IDCHAT_CONFIG_CHANGE.md`](./IDCHAT_CONFIG_CHANGE.md) for the exact config changes needed in idchat.

In short: change the idchat `config.json` to point the Socket.IO URL and API base URL at your metaso-p2p host.

## Connecting Bothub

Bothub should use the root metaso-p2p origin as its base URL. Do not include
the historical `/chat-api` prefix in `VITE_METASO_P2P_BASE_URL`; Bothub builds
native `/api/...` paths itself.

```dotenv
VITE_METASO_P2P_BASE_URL=https://<metaso-p2p-host>
VITE_USE_AGGREGATOR_MOCK=false
VITE_USE_WS_MOCK=false
```

The host assigned to Bothub must expose these routes on the same origin:

- `GET /healthz`
- `GET /api/bot-hub/skill-service/list`
- `GET /api/bot-hub/skill-service/detail/:serviceId`
- `GET /api/private-chat/homes/:metaId`
- `GET /api/private-chat/messages`
- `GET /api/private-chat/messages/by-index`
- `GET /api/private-chat/paths`
- Socket.IO at `/socket/socket.io`

If TLS or a public hostname is provided by nginx/Caddy/another reverse proxy,
proxy the whole root path to metaso-p2p; do not mount metaso-p2p under
`/chat-api`.

```nginx
location / {
  proxy_pass http://127.0.0.1:8080;
  proxy_http_version 1.1;
  proxy_set_header Host $host;
  proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
  proxy_set_header X-Forwarded-Proto $scheme;
}

location /socket/socket.io/ {
  proxy_pass http://127.0.0.1:8080;
  proxy_http_version 1.1;
  proxy_set_header Upgrade $http_upgrade;
  proxy_set_header Connection "upgrade";
  proxy_set_header Host $host;
}
```

Release smoke from the Bothub repo:

```bash
METASO_P2P_BASE_URL=https://<metaso-p2p-host> pnpm smoke:metaso-p2p
```

## Verifying the Deployment

```bash
# 1. Health check
curl http://localhost:8080/healthz

# 2. BotHub skill-service list/detail
curl "http://localhost:8080/api/bot-hub/skill-service/list?size=3&chainName=mvc&sortBy=updated&order=desc"
curl "http://localhost:8080/api/bot-hub/skill-service/detail/<SERVICE_PIN_ID>?chainName=mvc"

# 3. Canonical private chat aliases for Bothub Delivery
curl "http://localhost:8080/api/private-chat/homes/<METAID>"
curl "http://localhost:8080/api/private-chat/messages?metaId=<METAID>&otherMetaId=<OTHER_METAID>&cursor=&size=5"
curl "http://localhost:8080/api/private-chat/messages/by-index?metaId=<METAID>&otherMetaId=<OTHER_METAID>&startIndex=0&size=5"

# 4. User info (replace ADDRESS with a valid MetaID address)
curl "http://localhost:8080/api/info/address/<ADDRESS>"

# 5. Group list (replace METAID with a valid metaid)
curl "http://localhost:8080/api/group-chat/group-list?metaId=<METAID>&cursor=&size=10"

# 6. Check Socket.IO is reachable (should get a 200 with engine.io handshake)
curl "http://localhost:8080/socket/socket.io/?EIO=4&transport=polling"
```

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---|---|---|
| `bind: address already in use` | Port 8080 is taken | Set `METASO_P2P_HTTP_ADDR=:8081` |
| `pebble: directory does not exist` | Data dir parent missing | Create the parent directory or set `METASO_P2P_PEBBLE_DATA_DIR` |
| idchat can't connect Socket.IO | Wrong URL or port | Verify `METASO_P2P_SOCKET_PATH` and `METASO_P2P_SOCKET_ALLOW_EIO3=true` |
| No push notifications | Socket.IO disabled | Set `METASO_P2P_SOCKET_ENABLED=true` |
| Profile resolution failing | Remote URL not set | Set `METASO_P2P_PROFILE_REMOTE_BASE_URL` or use `local-only` mode |
