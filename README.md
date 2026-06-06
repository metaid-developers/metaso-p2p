# metaso-p2p

A modular, high-performance middleware for the MetaID protocol. Provides real-time Socket.IO push, group chat / private chat aggregation, user identity indexing, chat management, and Bot Hub skill-service aggregation — all backed by a pure PebbleDB storage layer with zero external database dependencies.

## Architecture

```
Chain RPC + ZMQ  →  Indexer Engine  →  Aggregator Registry
                                         ├── UserInfo Aggregator     (HTTP + cache)
                                         ├── GroupChat Aggregator    (HTTP + push)
                                         ├── PrivateChat Aggregator  (HTTP + push)
                                         ├── Notify Aggregator       (HTTP)
                                         ├── SkillService Aggregator (HTTP, Bot Hub)
                                         └── BotHomepage Aggregator  (HTTP, OAC Bot Browser)

                                         Socket.IO Server  →  idchat clients
                                         HTTP / JSON       →  IDBots Bot Hub + OAC Bot Browser
```

- **Chain adapters** (`internal/chain/`) — RPC + parsing for BTC, MVC, DOGE, OPCAT
- **Indexer engine** (`internal/indexer/`) — block scanning, ZMQ mempool, multi-chain coordination
- **Aggregators** (`internal/aggregator/`) — pluggable modules that consume parsed pins
- **Two-level cache** (`internal/cache/`) — L1 in-memory LRU + L2 Pebble persistent
- **Pebble storage** (`internal/storage/`) — namespaced key-value store, zero external deps

## Quick Start

```bash
# Install dependencies
go mod tidy

# Build
go build ./cmd/metaso-p2p/

# Run (all config via environment variables)
export METASO_P2P_HTTP_ADDR=":8080"
export METASO_P2P_SOCKET_ENABLED="true"
export METASO_P2P_PEBBLE_ENABLED="true"
export METASO_P2P_PEBBLE_DATA_DIR="./data/pebble"
export METASO_P2P_ZMQ_ENABLED="true"
export METASO_P2P_ZMQ_BTC_ENABLED="true"
export METASO_P2P_ZMQ_BTC_ENDPOINT="tcp://127.0.0.1:28336"
export METASO_P2P_ZMQ_BTC_RPC_HOST="127.0.0.1:8332"
export METASO_P2P_ZMQ_BTC_RPC_USER="user"
export METASO_P2P_ZMQ_BTC_RPC_PASS="pass"
./metaso-p2p
```

## Configuration

All settings are loaded from environment variables (prefix `METASO_P2P_`). See `internal/config/config.go` for the full list.

Key sections:
- `METASO_P2P_SOCKET_*` — Socket.IO server settings
- `METASO_P2P_ZMQ_*` — ZMQ mempool listeners per chain
- `METASO_P2P_PEBBLE_*` — PebbleDB storage path
- `METASO_P2P_CACHE_*` — Cache size and TTL

## API Endpoints

### User Info
- `GET /api/info/address/:address` — user profile by address
- `GET /api/info/metaid/:metaid` — user profile by metaid
- `GET /api/info/globalmetaid/:globalMetaId` — user profile by globalMetaId

### Chat Blocking
- `GET /push-base/v1/push/get_user_blocked_chats?metaId=` — get blocked chats
- `POST /push-base/v1/push/add_blocked_chat` — block a chat
- `POST /push-base/v1/push/remove_blocked_chat` — unblock a chat

### Socket.IO
- Connect: `wss://host/socket/socket.io?metaid=<globalMetaId>&type=pc|app`
- Events: `WS_SERVER_NOTIFY_GROUP_CHAT`, `WS_SERVER_NOTIFY_PRIVATE_CHAT`, `WS_SERVER_NOTIFY_GROUP_ROLE`

### Bot Hub Skill Service
- `GET /api/bot-hub/skill-service/list` — paginated skill-service listing for the Bot Hub
- `GET /api/bot-hub/skill-service/detail/:serviceId` — service detail including provider profile and payment declaration

See [`docs/specs/2026-05-28-bot-hub-skill-service-aggregation-api.md`](docs/specs/2026-05-28-bot-hub-skill-service-aggregation-api.md) for the full v1 contract.

### Bot Homepage
- `GET /api/bot-homepage/globalmetaid/:globalMetaId` — render-ready Bot homepage aggregation for OAC Bot Browser

See [`docs/specs/2026-06-07-bot-homepage-api.md`](docs/specs/2026-06-07-bot-homepage-api.md) for the full v1 contract.

## Development

```bash
# Run tests
go test ./...

# Run specific package tests
go test ./internal/storage/
go test ./internal/cache/
```

## License

MIT
