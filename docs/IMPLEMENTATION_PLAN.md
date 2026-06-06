# metaso-p2p Implementation Plan

Complements `docs/GOAL_DRIVEN.md` (authoritative on phases and acceptance criteria). This document provides module-level architecture specifications.

## 1. Context

metaso-p2p is a **modular, PebbleDB-backed middleware** for the MetaID protocol, replacing show-now-tmp as the backend for idchat.

## 2. Architecture Principles

1. **Zero external database dependencies** — PebbleDB only. No MongoDB, MySQL, Redis.
2. **Pluggable aggregators** — Each domain module implements `Aggregator` interface.
3. **Single data pipeline** — Chain RPC → Parser → Event → Aggregator → Store. No blockfile.
4. **Two-level cache** — L1 in-memory LRU + L2 Pebble. No Redis.
5. **Multi-chain from day 1** — BTC, MVC, DOGE, OPCAT. Adding a chain = Chain+Indexer pair.
6. **Wire-compatible with idchat** — API format and Socket.IO protocol match show-now-tmp.

## 3. Data Flow

```
Chain RPC + ZMQ → Indexer Engine → Aggregator Registry
                                       ├── UserInfo     → Pebble + HTTP
                                       ├── GroupChat    → Pebble + HTTP + Socket
                                       ├── PrivateChat  → Pebble + HTTP + Socket
                                       └── Notify       → Pebble + HTTP
                                            │
                                       Socket.IO Server → idchat
```

## 4. Module Specifications

### 4.1 Storage (`internal/storage/pebble.go`) ✅

`PebbleStore` — namespaced PebbleDB instances. CRUD, prefix scan, prefix delete. Each aggregator opens independent instances.

### 4.2 Cache (`internal/cache/cache.go`) ✅

`CacheProvider` + typed `Cache[T]`. L1 (expirable LRU) + L2 (Pebble). Namespace isolation per aggregator.

### 4.3 Chain Adapters (`internal/chain/`)

| File | Status |
|------|--------|
| `adapter.go` — interfaces | ✅ Done |
| `bitcoin/` | ⚠️ Skeleton — needs metaid-script-decoder |
| `mvc/` | ❌ Phase 6 |
| `dogecoin/` | ❌ Phase 6 (AuxPoW) |
| `opcat/` | ❌ Phase 6 (blob parser) |

### 4.4 Indexer Engine (`internal/indexer/engine.go`)

⚠️ Skeleton. Block scanner polls every 10s, dispatches to Registry, persists heights. ZMQ placeholder.

### 4.5 Aggregators (`internal/aggregator/`)

| Module | Status | Phase |
|--------|--------|-------|
| `aggregator.go` — interface + Registry | ✅ Done | 1 |
| `userinfo/` | ✅ Done | 1 (Phase 3 for chain integration) |
| `notify/` | ✅ Done | 1 |
| `groupchat/` | ⚠️ Placeholder | 4 |
| `privatechat/` | ⚠️ Placeholder | 5 |

### 4.6 Socket.IO (`internal/socket/`)

❌ Phase 2. Wire protocol: `{M, C, D}` envelope. Multi-device (PC ≤3, App ≤3). Heartbeat (client ping → server heartbeat_ack). Room broadcast.

**Reference**: `show-now-tmp/common/socket_util/socket_manager.go`

### 4.7 GlobalMetaId (`pkg/idaddress/`)

❌ Phase 3. Port from `show-now-tmp/idaddress/`: `idaddress.go`, `bech32.go`, `converter.go`.

### 4.8 HTTP API (`internal/api/`)

`response.go` ✅. Router and middleware in Phase 2.

### 4.9 Config (`internal/config/config.go`)

✅ Done. Env-based (`METASO_P2P_*`).

## 5. Implementation Phases

See `docs/GOAL_DRIVEN.md` for detailed acceptance criteria per phase.

| Phase | Deliverable | New Files |
|-------|------------|-----------|
| 1 ✅ | Skeleton compiles | 14 |
| 2 | Socket.IO server | 7 |
| 3 | BTC index + UserInfo | 8 |
| 4 | GroupChat aggregator | 9 |
| 5 | PrivateChat aggregator | 5 |
| 6 | MVC/DOGE/OPCAT adapters | 9 |
| 7 | Deploy + idchat verify | 4 |

**Strictly sequential execution** (2→3→4→5→6→7).

## 6. Testing Strategy

| Level | What | Tool |
|-------|------|------|
| Unit | Aggregator Pin processing, Pebble CRUD, cache Get/Set | `go test` |
| Integration | Engine + chain + aggregator against testnet | `go test` with RPC |
| Contract | API response format vs idchat spec | JSON snapshot tests |
| E2E | Socket connect → message → push | Socket.IO test client |

## 7. Key Design Decisions

| Decision | Rationale |
|----------|----------|
| Pure Pebble, no Redis | Zero external deps. L1 LRU provides Redis-comparable hot-path perf. |
| In-process event bus | No cross-process communication needed. Simpler deployment. |
| One Pebble instance per collection | Clean separation, independent backup. Pattern from meta-file-system. |
| Aggregator.NotifyChannel() | Push flows through channels, not callbacks. Decouples business logic from transport. |
| Per-chain Chain+Indexer | Each chain has unique block formats. Separate implementations avoid lowest-common-denominator. |
| Env-based config | Simpler for containers. No config file to mount. |
