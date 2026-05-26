# meta-socket Deployment Guide

## Prerequisites

- **Go 1.26+** (for source builds) or **Docker** (for containerized deployment)
- **No external database** — meta-socket uses PebbleDB (embedded), no MongoDB/MySQL/Redis needed
- Port **8080** available (configurable via `META_SOCKET_HTTP_ADDR`)

## Quick Start (Source)

```bash
# Build the binary
go build -o meta-socket ./cmd/meta-socket/

# Run with defaults (PebbleDB in ./data/pebble, HTTP on :8080)
./meta-socket
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
docker build -t meta-socket .
```

The Dockerfile uses a multi-stage build:
1. **Stage 1** (`golang:1.26-alpine`): compiles the Go binary with `CGO_ENABLED=0` and stripped symbols.
2. **Stage 2** (`alpine:3.21`): copies only the binary, adds CA certs and tzdata. Final image is under 50 MB (about 40 MB on arm64 — the static Go binary alone is ~30 MB; further compression with UPX or a `scratch` base is a potential future optimization).

### Run

```bash
# Basic run with default data directory
docker run -d \
  --name meta-socket \
  -p 8080:8080 \
  -v ./data:/data \
  meta-socket
```

### Run with custom configuration

```bash
docker run -d \
  --name meta-socket \
  -p 8080:8080 \
  -v ./data:/data \
  -e META_SOCKET_CACHE_MAX_ENTRIES=20000 \
  -e META_SOCKET_CACHE_DEFAULT_TTL_SECONDS=600 \
  -e META_SOCKET_SOCKET_MAX_CONNECTIONS=20000 \
  meta-socket
```

### Docker Compose (example)

```yaml
version: "3.8"
services:
  meta-socket:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - meta-socket-data:/data
    environment:
      META_SOCKET_PEBBLE_DATA_DIR: /data/pebble
      META_SOCKET_CACHE_MAX_ENTRIES: 20000
    restart: unless-stopped

volumes:
  meta-socket-data:
```

## Health Check

```bash
# Check if the server is alive
curl http://localhost:8080/healthz
```

Expected response:
```json
{"code": 0, "data": {"status": "ok", "service": "meta-socket", "version": "dev"}, "message": "", "processingTime": <ms>}
```

## Key Environment Variables

All configuration is via environment variables with the `META_SOCKET_` prefix.
See `config.example.toml` for the complete reference. The most important ones:

| Variable | Default | Description |
|---|---|---|
| `META_SOCKET_HTTP_ADDR` | `:8080` | Listen address |
| `META_SOCKET_HEALTH_PATH` | `/healthz` | Health check path |
| `META_SOCKET_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown timeout |
| `META_SOCKET_PEBBLE_ENABLED` | `true` | Enable persistent storage |
| `META_SOCKET_PEBBLE_DATA_DIR` | `./data/pebble` | PebbleDB data directory |
| `META_SOCKET_SOCKET_ENABLED` | `true` | Enable Socket.IO server |
| `META_SOCKET_SOCKET_PATH` | `/socket/socket.io` | Primary Socket.IO path |
| `META_SOCKET_SOCKET_LEGACY_PATH` | `/socket.io` | Legacy Socket.IO path |
| `META_SOCKET_SOCKET_MAX_CONNECTIONS` | `10000` | Max concurrent connections |
| `META_SOCKET_SOCKET_ALLOW_EIO3` | `true` | Allow EIO v3 clients (required for idchat) |
| `META_SOCKET_PROFILE_ENABLED` | `true` | Enable user profile resolution |
| `META_SOCKET_PROFILE_MODE` | `local-first` | Profile resolution mode |
| `META_SOCKET_PROFILE_REMOTE_BASE_URL` | `` | Remote profile service URL |
| `META_SOCKET_CACHE_MAX_ENTRIES` | `10000` | L1 LRU cache size |
| `META_SOCKET_CACHE_DEFAULT_TTL_SECONDS` | `300` | Cache TTL in seconds |
| `META_SOCKET_GROUPCHAT_MIGRATION_ENABLED` | `true` | Enable migration endpoints |
| `META_SOCKET_SOCKET_EXTRA_PUSH_AUTH_KEY` | `` | Push auth key (leave empty for dev) |

## Data Persistence

meta-socket stores all data in **PebbleDB**, an embedded key-value store (no external database server).

- **Default location**: `./data/pebble` (relative to working directory)
- **Docker**: bind-mount a host directory to `/data` and set `META_SOCKET_PEBBLE_DATA_DIR=/data/pebble`
- All indexed messages, user info, blocked-chat lists, and cursor state are persisted here
- To reset: stop the server, delete the data directory, restart

### Backup & Recovery

PebbleDB is a single directory of files. To back up:
```bash
# Stop the server first, then:
cp -r ./data/pebble ./data/pebble-backup-$(date +%Y%m%d)
```

To restore: stop the server, replace the data directory with the backup, restart.

## Connecting idchat

See [`docs/IDCHAT_CONFIG_CHANGE.md`](./IDCHAT_CONFIG_CHANGE.md) for the exact config changes needed in idchat.

In short: change the idchat `config.json` to point the Socket.IO URL and API base URL at your meta-socket host.

## Verifying the Deployment

```bash
# 1. Health check
curl http://localhost:8080/healthz

# 2. User info (replace ADDRESS with a valid MetaID address)
curl "http://localhost:8080/api/info/address/<ADDRESS>"

# 3. Group list (replace METAID with a valid metaid)
curl "http://localhost:8080/api/group-chat/group-list?metaId=<METAID>&cursor=&size=10"

# 4. Check Socket.IO is reachable (should get a 200 with engine.io handshake)
curl "http://localhost:8080/socket/socket.io/?EIO=4&transport=polling"
```

## Troubleshooting

| Symptom | Likely Cause | Fix |
|---|---|---|
| `bind: address already in use` | Port 8080 is taken | Set `META_SOCKET_HTTP_ADDR=:8081` |
| `pebble: directory does not exist` | Data dir parent missing | Create the parent directory or set `META_SOCKET_PEBBLE_DATA_DIR` |
| idchat can't connect Socket.IO | Wrong URL or port | Verify `META_SOCKET_SOCKET_PATH` and `META_SOCKET_SOCKET_ALLOW_EIO3=true` |
| No push notifications | Socket.IO disabled | Set `META_SOCKET_SOCKET_ENABLED=true` |
| Profile resolution failing | Remote URL not set | Set `META_SOCKET_PROFILE_REMOTE_BASE_URL` or use `local-only` mode |
