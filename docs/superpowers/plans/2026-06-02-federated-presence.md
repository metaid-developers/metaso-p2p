# Federated Presence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build MVC-backed Metaso P2P federation so any enabled node can expose a global online AI Agent list aggregated from all discovered Metaso P2P nodes.

**Architecture:** MVC chain stores node registry pins at `/protocols/metaso-p2p-node`; MANAPI reads those pins for peer discovery; each peer exposes a signed `/.well-known/metaso-p2p/presence` snapshot; each node pulls snapshots, verifies signatures, and merges them with local `ConnectionManager` state. Federation is disabled by default and preserves current local-only behavior unless enabled.

**Tech Stack:** Go 1.26, Gin, Socket.IO, btcsuite/btcd, MVC MetaID OP_RETURN pins, MANAPI HTTP client, Metalet wallet API UTXO/broadcast endpoints.

---

## Source Documents

- Design spec: `docs/superpowers/specs/2026-06-02-federated-presence-design.md`
- Current local presence API: `internal/socket/presence.go`
- Current local connection store: `internal/socket/manager.go`
- Current config style: `internal/config/config.go`, `config.example.toml`
- MVC parser reference: `internal/chain/mvc/indexer.go`
- Local MVC publish reference: `/Users/tusm/Documents/MetaID_Projects/chat-assistant-service/service/common_service/metaid_service.go`
- Local MVC tx reference: `/Users/tusm/Documents/MetaID_Projects/chat-assistant-service/common/common_mvc_tx.go`
- Metalet MVC UTXO Swagger: `https://www.metalet.space/wallet-api/swagger/index.html#/wallet-v4-mvc/get_v4_mvc_address_utxo_list`

## Worker Model

Use subagent-driven development. The main session should not implement the whole feature inline; it should:

1. Dispatch one focused subagent per task below.
2. Review the subagent diff.
3. Run the task's verification commands.
4. Stage and commit only the files changed for that task.
5. Post a detailed development journal with `metabot-post-buzz` after each commit.
6. Continue to the next task only after the current task is accepted.

Follow `AGENTS.md` commit rules. Ignore unrelated untracked files unless the user asks to inspect them.

## File Structure

Create:

- `internal/presence/types.go`
  - Import-neutral DTOs and interfaces shared by `socket`, `api`, and `federation`.
  - This package prevents Go import cycles: `socket` may import `presence`, and
    `federation` may import `presence`, but `socket` must never import `federation`.
- `internal/federation/types.go`
  - Protocol constants, registry DTOs, config-facing defaults.
- `internal/federation/signature.go`
  - Canonical JSON encoding, secp256k1 sign/verify helpers.
- `internal/federation/snapshot.go`
  - Build local signed snapshots from `presence.LocalReader`.
- `internal/federation/store.go`
  - In-memory peer set and remote snapshot store with TTL, sequence, merge logic.
- `internal/federation/metalet_client.go`
  - HTTP client for Metalet MVC UTXO and broadcast endpoints.
- `internal/federation/mvc_tx.go`
  - Minimal MVC MetaID transaction builder for registry create/modify/revoke.
- `internal/federation/publisher.go`
  - Registry payload creation and renew loop.
- `internal/federation/discovery.go`
  - MANAPI client and registry pin normalization.
- `internal/federation/puller.go`
  - Remote presence snapshot pull loop with timeout, backoff, and signature validation.
- `internal/federation/service.go`
  - Lifecycle coordinator for publisher, discovery, puller, and store.
- `internal/federation/*_test.go`
  - Unit tests for every new boundary.

Modify:

- `internal/config/config.go`
  - Add `FederationConfig`, defaults, env parsing, validation, summary.
- `config.example.toml`
  - Add documented `[federation]` section.
- `internal/socket/manager.go`
  - Add a local snapshot method that returns unpaginated `presence.OnlineEntry` data with `lastSeenAt`.
- `internal/socket/presence.go`
  - Add `scope` handling and delegate global reads through a `presence.GlobalReader` interface.
- `internal/socket/server.go`
  - Store optional `presence.GlobalReader`; do not import `internal/federation`.
- `internal/api/router.go`
  - Mount `/.well-known/metaso-p2p/presence`.
- `cmd/metaso-p2p/main.go`
  - Wire federation service startup/shutdown if this command is the runtime entrypoint.
- `internal/socket/server_test.go`
  - Cover backward compatibility and global scope behavior.
- `internal/api/router_test.go`
  - Cover well-known route mounting.

Do not modify:

- Existing aggregators unless a test proves they must change.
- Existing chain indexers unless MANAPI fallback to local indexer is explicitly added later.

## Package Boundary Rule

Use this dependency direction:

```text
internal/socket      -> internal/presence
internal/api         -> internal/presence
internal/federation  -> internal/presence
cmd/metaso-p2p      -> internal/federation, internal/socket, internal/api
```

Forbidden:

```text
internal/socket -> internal/federation
```

The actual federation service can implement `presence.GlobalReader` and
`presence.SnapshotProvider`, but `socket` and `api` should only know those
interfaces. Add a verification check whenever touching `internal/socket`:

```bash
rg 'internal/federation' internal/socket
```

Expected: no matches.

## API Contracts

Keep the existing response envelope:

```json
{
  "code": 0,
  "data": {
    "items": []
  },
  "message": "",
  "processingTime": 1780000000000
}
```

For global list items, preserve existing fields and add optional fields:

```json
{
  "metaid": "agent-metaid-1",
  "type": "app",
  "connectedAt": 1779999900000,
  "lastSeenAt": 1780000000000,
  "sourceNodeIds": ["mvc:1Node"],
  "sources": 1
}
```

Use `scope=local|global` on:

- `GET /socket/online/list?page=1&size=20&scope=global`
- `GET /socket/online/stats?scope=global`

When `federation.enabled=false`, both endpoints must behave exactly as today even if `scope=global` is passed.

## Task 1: Federation Config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `config.example.toml`

- [ ] **Step 1: Write failing config tests**

Add tests for:

```go
func TestDefaultFederationConfigDisabled(t *testing.T)
func TestLoadFederationEnv(t *testing.T)
func TestValidateFederationRequiresPublicBaseURLWhenEnabled(t *testing.T)
func TestValidateFederationRequiresNodePrivateKeyWhenEnabled(t *testing.T)
func TestValidateFederationRequiresDiscoveryAndWalletURLsWhenEnabled(t *testing.T)
func TestValidateFederationDefaultScope(t *testing.T)
```

Expected defaults:

- `Enabled=false`
- `Network="mvc-mainnet"`
- `MANAPIBaseURL="https://manapi.metaid.io/pin/path/list?path={protocol-path}&size={size}"`
- `RegistryPath="/protocols/metaso-p2p-node"`
- `PresencePath="/.well-known/metaso-p2p/presence"`
- `RegistryRenewInterval=6*time.Hour`
- `RegistryValidFor=24*time.Hour`
- `DiscoveryInterval=5*time.Minute`
- `PresencePullInterval=20*time.Second`
- `PresenceTTL=90*time.Second`
- `RequestTimeout=3*time.Second`
- `DefaultScope="global"`

- [ ] **Step 2: Run tests and confirm they fail**

Run:

```bash
go test ./internal/config -run 'Federation|ValidateFederation' -count=1
```

Expected: FAIL because `FederationConfig` does not exist.

- [ ] **Step 3: Implement config**

Add `Federation FederationConfig` to `Config` and implement:

```go
type FederationConfig struct {
    Enabled bool `json:"enabled"`
    Network string `json:"network"`
    NodePrivateKey string `json:"nodePrivateKey"`
    PublicBaseURL string `json:"publicBaseUrl"`
    MANAPIBaseURL string `json:"manapiBaseUrl"`
    MetaletBaseURL string `json:"metaletBaseUrl"`
    RegistryPath string `json:"registryPath"`
    PresencePath string `json:"presencePath"`
    RegistryRenewInterval time.Duration `json:"registryRenewInterval"`
    RegistryValidFor time.Duration `json:"registryValidFor"`
    DiscoveryInterval time.Duration `json:"discoveryInterval"`
    PresencePullInterval time.Duration `json:"presencePullInterval"`
    PresenceTTL time.Duration `json:"presenceTTL"`
    RequestTimeout time.Duration `json:"requestTimeout"`
    DefaultScope string `json:"defaultScope"`
    AllowInsecureHTTP bool `json:"allowInsecureHttp"`
    MaxPeers int `json:"maxPeers"`
    MaxSnapshotBytes int `json:"maxSnapshotBytes"`
}
```

Environment variables must use `METASO_P2P_FEDERATION_*`.

Enabled-mode validation must require:

- `nodePrivateKey`
- `publicBaseURL`
- `manapiBaseURL`, which must be a MANAPI path-list URL template containing
  `{protocol-path}` and `{size}` for the first version
- `metaletBaseURL`
- `registryPath` and `presencePath` starting with `/`
- positive duration values
- `defaultScope` equal to `local` or `global`

- [ ] **Step 4: Update example config**

Add a `[federation]` section with comments and all env var names.

- [ ] **Step 5: Verify**

Run:

```bash
go test ./internal/config -count=1
git diff --check -- internal/config/config.go internal/config/config_test.go config.example.toml
```

Expected: PASS.

- [ ] **Step 6: Commit and post buzz**

```bash
git add internal/config/config.go internal/config/config_test.go config.example.toml
git commit -m "feat: add federation config"
```

Then use `metabot-post-buzz` with a development journal for this commit.

## Task 2: Local Presence Snapshot Model

**Files:**
- Modify: `internal/socket/manager.go`
- Modify: `internal/socket/server_test.go`
- Create: `internal/presence/types.go`
- Create: `internal/presence/types_test.go`
- Create: `internal/federation/types.go`
- Create: `internal/federation/snapshot.go`
- Create: `internal/federation/snapshot_test.go`

- [ ] **Step 1: Write failing tests**

Cover:

- `ConnectionManager` can return all local entries without pagination.
- Local snapshot includes `lastSeenAt` from `TrackedConnection.LastPing`.
- Snapshot excludes socket IDs.
- Snapshot is stable enough for deterministic assertions.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/socket ./internal/federation -run 'Snapshot|OnlineEntries' -count=1
```

Expected: FAIL because federation package and unpaginated local read do not exist.

- [ ] **Step 3: Add local read method**

Add a method similar to:

```go
func (m *ConnectionManager) OnlineEntries() []presence.OnlineEntry
```

Move the shared online entry DTO to `internal/presence` and either return that
type directly or keep a backwards-compatible alias in `internal/socket`:

```go
package presence

type OnlineEntry struct {
    MetaId string `json:"metaid"`
    Type string `json:"type"`
    ConnectedAt int64 `json:"connectedAt"`
    LastSeenAt int64 `json:"lastSeenAt,omitempty"`
    SourceNodeIds []string `json:"sourceNodeIds,omitempty"`
    Sources int `json:"sources,omitempty"`
}
```

If a socket alias is kept, it must be:

```go
type OnlineEntry = presence.OnlineEntry
```

Do not create two independent `OnlineEntry` structs.

The local read method should be:

```go
func (m *ConnectionManager) OnlineEntries() []presence.OnlineEntry
```

Fields:

```go
LastSeenAt int64 `json:"lastSeenAt,omitempty"`
SourceNodeIds []string `json:"sourceNodeIds,omitempty"`
Sources int `json:"sources,omitempty"`
```

Keep existing `metaid`, `type`, `connectedAt` field names unchanged.

- [ ] **Step 4: Add federation DTOs and snapshot builder**

Define:

```go
package presence

type LocalReader interface {
    OnlineEntries() []OnlineEntry
}

type SnapshotProvider interface {
    Snapshot() (*Snapshot, error)
}

type GlobalReader interface {
    Enabled() bool
    DefaultScope() string
    OnlineList(local []OnlineEntry, page int, size int) []OnlineEntry
    Stats(local []OnlineEntry) GlobalStats
}

type Snapshot struct {
    Protocol string `json:"protocol"`
    Version string `json:"version"`
    NodeID string `json:"nodeId"`
    GeneratedAt int64 `json:"generatedAt"`
    TTLSeconds int64 `json:"ttlSeconds"`
    Sequence uint64 `json:"sequence"`
    Items []OnlineEntry `json:"items"`
    Signature string `json:"signature"`
}

type GlobalStats struct {
    TotalConnections int `json:"totalConnections"`
    UniqueMetaIds int `json:"uniqueMetaIds"`
    Nodes int `json:"nodes"`
}
```

Define federation protocol constants separately:

```go
const ProtocolNode = "metaso-p2p-node"
const ProtocolPresence = "metaso-p2p-presence"
const RegistryPath = "/protocols/metaso-p2p-node"
const PresencePath = "/.well-known/metaso-p2p/presence"
```

Create snapshot builder that accepts:

```go
presence.LocalReader
```

- [ ] **Step 5: Verify**

```bash
go test ./internal/socket ./internal/federation -run 'Snapshot|OnlineEntries' -count=1
go test ./internal/presence ./internal/socket ./internal/federation -run 'Snapshot|OnlineEntries|PresenceTypes' -count=1
git diff --check -- internal/presence/types.go internal/presence/types_test.go internal/socket/manager.go internal/socket/server_test.go internal/federation/types.go internal/federation/snapshot.go internal/federation/snapshot_test.go
```

Run:

```bash
rg 'internal/federation' internal/socket
```

Expected: no matches.

Expected: PASS.

- [ ] **Step 6: Commit and post buzz**

```bash
git add internal/presence/types.go internal/presence/types_test.go internal/socket/manager.go internal/socket/server_test.go internal/federation/types.go internal/federation/snapshot.go internal/federation/snapshot_test.go
git commit -m "feat: add local presence snapshots"
```

Then use `metabot-post-buzz`.

## Task 3: Signatures and Snapshot Verification

**Files:**
- Create: `internal/federation/signature.go`
- Create: `internal/federation/signature_test.go`
- Modify: `internal/federation/snapshot.go`
- Modify: `internal/federation/snapshot_test.go`

- [ ] **Step 1: Write failing signature tests**

Cover:

- Canonical JSON excludes `signature`.
- Signing the same payload twice produces verifiable output.
- Verification fails if `items` changes.
- Verification fails if `nodeId` does not match expected registry node.
- Invalid private/public keys return errors, not panics.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/federation -run 'Signature|Canonical|VerifySnapshot' -count=1
```

Expected: FAIL because signature helpers do not exist.

- [ ] **Step 3: Implement canonical JSON**

Use a deterministic encoder for structs. Avoid signing arbitrary Go maps. For extensible maps, sort keys recursively before marshaling.

- [ ] **Step 4: Implement secp256k1 helpers**

Use existing btcd dependencies:

```go
github.com/btcsuite/btcd/btcec/v2
github.com/btcsuite/btcd/btcec/v2/ecdsa
```

Hash canonical bytes with SHA-256 before signing.

- [ ] **Step 5: Wire signing into snapshot builder**

Snapshot builder must produce `signature` only after all non-signature fields are final.

- [ ] **Step 6: Verify**

```bash
go test ./internal/federation -run 'Signature|Canonical|VerifySnapshot|Snapshot' -count=1
git diff --check -- internal/federation
```

Expected: PASS.

- [ ] **Step 7: Commit and post buzz**

```bash
git add internal/federation/signature.go internal/federation/signature_test.go internal/federation/snapshot.go internal/federation/snapshot_test.go
git commit -m "feat: sign presence snapshots"
```

Then use `metabot-post-buzz`.

## Task 4: Presence HTTP Endpoint

**Files:**
- Modify: `internal/api/router.go`
- Modify: `internal/api/router_test.go`
- Modify: `internal/socket/server.go`
- Modify: `internal/socket/presence.go`
- Create or modify: `internal/federation/service.go`

- [ ] **Step 1: Write failing route tests**

Cover:

- `GET /.well-known/metaso-p2p/presence` returns 404 or disabled response when federation is disabled.
- When federation is enabled with a fake snapshot provider, the endpoint returns protocol `metaso-p2p-presence`.
- Endpoint response has no socket IDs.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/api ./internal/socket -run 'WellKnown|PresenceEndpoint' -count=1
```

Expected: FAIL because the route is not mounted.

- [ ] **Step 3: Add interface boundary**

Avoid coupling `socket.Server` or `api` to the full federation service. Use the
neutral `presence.SnapshotProvider` interface:

```go
type SnapshotProvider interface {
    Snapshot() (*presence.Snapshot, error)
}
```

The router can mount the well-known route if provider is non-nil.

- [ ] **Step 4: Implement route**

Return HTTP 200 with JSON snapshot. On signing/build errors, return HTTP 503.

- [ ] **Step 5: Verify**

```bash
go test ./internal/api ./internal/socket ./internal/federation -run 'WellKnown|PresenceEndpoint|Snapshot' -count=1
go test ./internal/presence ./internal/api ./internal/socket ./internal/federation -run 'WellKnown|PresenceEndpoint|Snapshot' -count=1
git diff --check -- internal/api/router.go internal/api/router_test.go internal/socket/server.go internal/socket/presence.go internal/federation/service.go internal/presence/types.go
rg 'internal/federation' internal/socket
```

Expected: tests PASS and `rg` returns no matches.

- [ ] **Step 6: Commit and post buzz**

```bash
git add internal/api/router.go internal/api/router_test.go internal/socket/server.go internal/socket/presence.go internal/federation/service.go
git commit -m "feat: expose presence snapshot endpoint"
```

Then use `metabot-post-buzz`.

## Task 5: Federation Store and Aggregation

**Files:**
- Create: `internal/federation/store.go`
- Create: `internal/federation/store_test.go`
- Modify: `internal/socket/presence.go`
- Modify: `internal/socket/server.go`
- Modify: `internal/socket/server_test.go`

- [ ] **Step 1: Write failing aggregation tests**

Cover:

- Empty remote store returns local entries.
- Same `metaid + type` across nodes merges into one item.
- `connectedAt` chooses earliest value.
- `lastSeenAt` chooses latest value.
- Expired snapshots are excluded.
- Pagination happens after global merge and stable sort.
- `scope=local` preserves current local-only result.
- `scope=global` with federation disabled behaves local-only.
- duplicate `metaid + type` entries from multiple nodes merge into one list item,
  but `totalConnections` still counts all source observations before that merge.
- `GET /socket/online/stats?scope=global` returns exact fields:
  `data.totalConnections`, `data.uniqueMetaIds`, and `data.nodes`.
- `GET /socket/online/stats?scope=local` preserves the current response shape with
  `data.totalConnections`.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/federation ./internal/socket -run 'Aggregate|GlobalOnline|Scope' -count=1
```

Expected: FAIL because store and scope support do not exist.

- [ ] **Step 3: Implement store**

Implement:

```go
type Store struct { ... }
func (s *Store) UpsertPeer(peer RegistryNode)
func (s *Store) RemovePeer(nodeID string)
func (s *Store) UpsertSnapshot(snapshot PresenceSnapshot)
func (s *Store) GlobalOnline(local []presence.OnlineEntry, now time.Time) []presence.OnlineEntry
func (s *Store) Stats(local []presence.OnlineEntry, now time.Time) presence.GlobalStats
```

`presence.GlobalStats` must be explicit:

```go
type GlobalStats struct {
    TotalConnections int `json:"totalConnections"`
    UniqueMetaIds int `json:"uniqueMetaIds"`
    Nodes int `json:"nodes"`
}
```

- [ ] **Step 4: Wire scope handling**

`HandleOnlineList` should parse `scope`. If federation is enabled and scope resolves to global, use global aggregate. Otherwise call the existing local path.

`HandleOnlineStats` should parse `scope` with the same defaulting rules. For global scope, respond with:

```json
{
  "code": 0,
  "data": {
    "totalConnections": 12,
    "uniqueMetaIds": 9,
    "nodes": 3
  },
  "message": "",
  "processingTime": 1780000000000
}
```

Global stats semantics:

- `totalConnections`: number of local plus remote source observations before
  `metaid + type` list-item merge. If `agent-a/app` appears on two nodes, it
  contributes `2`.
- `uniqueMetaIds`: distinct `metaid` count after filtering expired snapshots.
- `nodes`: current node plus accepted, non-expired remote nodes contributing to
  the aggregate window.

For local scope or disabled federation, keep the current shape:

```json
{
  "code": 0,
  "data": {
    "totalConnections": 3
  },
  "message": "",
  "processingTime": 1780000000000
}
```

- [ ] **Step 5: Verify**

```bash
go test ./internal/federation ./internal/socket -run 'Aggregate|GlobalOnline|Scope|OnlineList|OnlineStats' -count=1
go test ./internal/presence ./internal/federation ./internal/socket -run 'Aggregate|GlobalOnline|Scope|OnlineList|OnlineStats' -count=1
git diff --check -- internal/presence/types.go internal/federation/store.go internal/federation/store_test.go internal/socket/presence.go internal/socket/server.go internal/socket/server_test.go
rg 'internal/federation' internal/socket
```

Expected: tests PASS and `rg` returns no matches.

- [ ] **Step 6: Commit and post buzz**

```bash
git add internal/presence/types.go internal/federation/store.go internal/federation/store_test.go internal/socket/presence.go internal/socket/server.go internal/socket/server_test.go
git commit -m "feat: aggregate federated online users"
```

Then use `metabot-post-buzz`.

## Task 6: Metalet MVC Wallet Client

**Files:**
- Create: `internal/federation/metalet_client.go`
- Create: `internal/federation/metalet_client_test.go`

- [ ] **Step 1: Write failing HTTP client tests**

Use `httptest.Server` and cover:

- UTXO request path `/wallet-api/v4/mvc/address/utxo-list`.
- Query params `net`, `address`, optional `flag`.
- Response maps `txid`, `outIndex`, `value`, `address`, `height`, `flag`.
- Broadcast request path `/wallet-api/v4/mvc/tx/broadcast`.
- Broadcast body includes `chain`, `net`, `publicKey`, `rawTx`.
- Non-2xx response returns a typed error.
- Request timeout is honored.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/federation -run 'Metalet' -count=1
```

Expected: FAIL because Metalet client does not exist.

- [ ] **Step 3: Implement client**

Implement only the MVC endpoints:

```go
GET {base}/wallet-api/v4/mvc/address/utxo-list?net=livenet&address=...
POST {base}/wallet-api/v4/mvc/tx/broadcast
```

Normalize base URL so both `https://www.metalet.space` and
`https://www.metalet.space/wallet-api` do not double-prefix accidentally.

- [ ] **Step 4: Verify**

```bash
go test ./internal/federation -run 'Metalet' -count=1
git diff --check -- internal/federation/metalet_client.go internal/federation/metalet_client_test.go
```

Expected: PASS.

- [ ] **Step 5: Commit and post buzz**

```bash
git add internal/federation/metalet_client.go internal/federation/metalet_client_test.go
git commit -m "feat: add metalet mvc client"
```

Then use `metabot-post-buzz`.

## Task 7: MVC MetaID Transaction Builder

**Files:**
- Create: `internal/federation/mvc_tx.go`
- Create: `internal/federation/mvc_tx_test.go`

- [ ] **Step 1: Write failing transaction tests**

Cover:

- Private key derives expected public key and address.
- Registry payload builds an OP_RETURN MetaID pin at `/protocols/metaso-p2p-node`.
- Operation can be `create`, `modify`, or `revoke`.
- Content type is `application/json`.
- Transaction has owner output and change output.
- Final fee is at least requested fee rate after signing.
- Insufficient UTXO returns an error.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/federation -run 'MVCTx|RegistryTx' -count=1
```

Expected: FAIL because transaction builder does not exist.

- [ ] **Step 3: Implement minimal builder**

Use `btcsuite/btcd` packages already in the repo. The transaction should match the MVC parser shape in `internal/chain/mvc/indexer.go`:

```text
OP_RETURN
"metaid"
operation
path
"0"
version
"application/json"
content chunks
```

Chunk content so each pushed data item stays within script limits. Set MVC tx version as required by the reference implementation.

- [ ] **Step 4: Implement fee and UTXO lock boundary**

This task only implements builder-level validation. Runtime UTXO locking belongs in publisher.

- [ ] **Step 5: Verify**

```bash
go test ./internal/federation -run 'MVCTx|RegistryTx' -count=1
git diff --check -- internal/federation/mvc_tx.go internal/federation/mvc_tx_test.go
```

Expected: PASS.

- [ ] **Step 6: Commit and post buzz**

```bash
git add internal/federation/mvc_tx.go internal/federation/mvc_tx_test.go
git commit -m "feat: build mvc registry pins"
```

Then use `metabot-post-buzz`.

## Task 8: Registry Publisher

**Files:**
- Create: `internal/federation/publisher.go`
- Create: `internal/federation/publisher_test.go`
- Modify: `internal/federation/types.go`
- Modify: `internal/federation/service.go`

- [ ] **Step 1: Write failing publisher tests**

Use fake UTXO and broadcast clients. Cover:

- Builds registry payload with public URLs and validUntil.
- Publishes immediately on start when enabled.
- Renews on configured interval.
- Does not run when federation disabled.
- Handles insufficient UTXO without crashing service.
- Serializes publish calls with an in-process lock.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/federation -run 'Publisher|RegistryPayload' -count=1
```

Expected: FAIL because publisher does not exist.

- [ ] **Step 3: Implement publisher**

Publisher should expose:

```go
type Publisher struct { ... }
func (p *Publisher) PublishOnce(ctx context.Context, operation string) error
func (p *Publisher) Start(ctx context.Context)
```

Store the latest txid/pin metadata in memory first. Persistent metadata can be added later if needed.

- [ ] **Step 4: Verify**

```bash
go test ./internal/federation -run 'Publisher|RegistryPayload' -count=1
git diff --check -- internal/federation/publisher.go internal/federation/publisher_test.go internal/federation/types.go internal/federation/service.go
```

Expected: PASS.

- [ ] **Step 5: Commit and post buzz**

```bash
git add internal/federation/publisher.go internal/federation/publisher_test.go internal/federation/types.go internal/federation/service.go
git commit -m "feat: publish metaso-p2p node registry"
```

Then use `metabot-post-buzz`.

## Task 9: MANAPI Discovery

**Files:**
- Create: `internal/federation/discovery.go`
- Create: `internal/federation/discovery_test.go`
- Modify: `internal/federation/store.go`
- Modify: `internal/federation/types.go`

- [ ] **Step 1: Write failing discovery tests**

Use `httptest.Server` and cover:

- Expands the default MANAPI URL template
  `https://manapi.metaid.io/pin/path/list?path={protocol-path}&size={size}`
  into a request equivalent to
  `/pin/path/list?path=/protocols/metaso-p2p-node&size=100`.
- URL-encodes the protocol path correctly if the implementation uses `url.Values`.
- Accepts MANAPI response envelope `code=1,message=ok,data.list,nextCursor,total`.
- Treats `data.list=null` as an empty peer list.
- Accepts MVC create/modify pins with valid payload.
- Parses registry payload from `contentBody`, falling back to `contentSummary` when
  `contentBody` is empty.
- Drops revoke pins.
- Drops expired `validUntil`.
- Deduplicates by `nodeId`, newest valid pin wins.
- Skips self node.
- Rejects HTTP presence URLs unless `AllowInsecureHTTP=true` or host is localhost.
- Enforces `MaxPeers`.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/federation -run 'Discovery|RegistryPin' -count=1
```

Expected: FAIL because discovery does not exist.

- [ ] **Step 3: Implement MANAPI client boundary**

Create a small DTO for remote MANAPI pin responses. Do not pass raw remote JSON into the store.

The first-version default discovery URL is a path-list template:

```text
https://manapi.metaid.io/pin/path/list?path={protocol-path}&size={size}
```

For `/protocols/metaso-p2p-node` with size `100`, the request should be equivalent to:

```text
https://manapi.metaid.io/pin/path/list?path=/protocols/metaso-p2p-node&size=100
```

The response envelope shape is:

```go
type MANAPIPathListResponse struct {
    Code int `json:"code"`       // success is 1
    Message string `json:"message"`
    Data MANAPIPathListData `json:"data"`
}

type MANAPIPathListData struct {
    List []MANAPIPin `json:"list"` // null must decode/normalize to empty
    NextCursor string `json:"nextCursor"`
    Total int `json:"total"`
}
```

If the exact MANAPI endpoint shape differs from this plan, isolate it in one adapter method and keep the store input stable:

```go
type RegistryPin struct {
    ID string
    Operation string
    ChainName string
    Timestamp int64
    ContentBody []byte
    ContentSummary string
}
```

When converting `MANAPIPin` to `RegistryPin`, parse JSON payload from `contentBody`
first and fallback to `contentSummary`. This matches the current MANAPI path-list
shape where some pins expose JSON summary while `contentBody` is empty.

- [ ] **Step 4: Implement poll loop**

Discovery should run once on start, then every `DiscoveryInterval`.

- [ ] **Step 5: Verify**

```bash
go test ./internal/federation -run 'Discovery|RegistryPin|Store' -count=1
git diff --check -- internal/federation/discovery.go internal/federation/discovery_test.go internal/federation/store.go internal/federation/types.go
```

Expected: PASS.

- [ ] **Step 6: Commit and post buzz**

```bash
git add internal/federation/discovery.go internal/federation/discovery_test.go internal/federation/store.go internal/federation/types.go
git commit -m "feat: discover metaso-p2p peers"
```

Then use `metabot-post-buzz`.

## Task 10: Remote Presence Puller

**Files:**
- Create: `internal/federation/puller.go`
- Create: `internal/federation/puller_test.go`
- Modify: `internal/federation/store.go`
- Modify: `internal/federation/service.go`

- [ ] **Step 1: Write failing puller tests**

Use fake peers and `httptest.Server`. Cover:

- Pulls `presenceUrl` for active peers.
- Uses configured request timeout.
- Enforces `MaxSnapshotBytes`.
- Verifies snapshot signature with registry public key.
- Rejects nodeId mismatch.
- Rejects stale `generatedAt + ttlSeconds`.
- Rejects lower sequence than last accepted.
- Applies backoff after repeated failures.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./internal/federation -run 'Puller|RemoteSnapshot' -count=1
```

Expected: FAIL because puller does not exist.

- [ ] **Step 3: Implement puller**

Puller loop:

1. Read active peers from store.
2. Skip self.
3. Fetch snapshot with timeout and body limit.
4. Verify signature and freshness.
5. Upsert accepted snapshot.
6. Track failure count and next eligible pull time.

- [ ] **Step 4: Verify**

```bash
go test ./internal/federation -run 'Puller|RemoteSnapshot|Store' -count=1
git diff --check -- internal/federation/puller.go internal/federation/puller_test.go internal/federation/store.go internal/federation/service.go
```

Expected: PASS.

- [ ] **Step 5: Commit and post buzz**

```bash
git add internal/federation/puller.go internal/federation/puller_test.go internal/federation/store.go internal/federation/service.go
git commit -m "feat: pull remote presence snapshots"
```

Then use `metabot-post-buzz`.

## Task 11: Service Wiring and Lifecycle

**Files:**
- Modify: `cmd/metaso-p2p/main.go`
- Modify: `internal/api/router.go`
- Modify: `internal/api/router_test.go`
- Modify: `internal/socket/server.go`
- Modify: `internal/federation/service.go`
- Create: `internal/federation/service_test.go`

- [ ] **Step 1: Write failing lifecycle tests**

Cover:

- Disabled federation does not construct publisher/discovery/puller.
- Enabled federation wires snapshot provider and global presence reader.
- Shutdown cancels background loops.
- Missing required enabled config prevents service startup through config validation.

- [ ] **Step 2: Run tests and confirm failure**

```bash
go test ./cmd/metaso-p2p ./internal/api ./internal/socket ./internal/federation -run 'Federation|Lifecycle|Shutdown' -count=1
```

Expected: FAIL because runtime wiring does not exist.

- [ ] **Step 3: Wire service**

In the command entrypoint:

1. Load config.
2. Create socket server.
3. If federation enabled, create federation service with socket manager as local reader.
4. Pass federation interfaces to router/socket presence handlers.
5. Start federation loops after HTTP dependencies exist.
6. Stop federation loops during shutdown.

- [ ] **Step 4: Verify**

```bash
go test ./cmd/metaso-p2p ./internal/api ./internal/socket ./internal/federation -run 'Federation|Lifecycle|Shutdown|Presence' -count=1
go test ./... -count=1
git diff --check -- cmd/metaso-p2p/main.go internal/api/router.go internal/api/router_test.go internal/socket/server.go internal/federation/service.go internal/federation/service_test.go
```

Expected: PASS.

- [ ] **Step 5: Commit and post buzz**

```bash
git add cmd/metaso-p2p/main.go internal/api/router.go internal/api/router_test.go internal/socket/server.go internal/federation/service.go internal/federation/service_test.go
git commit -m "feat: wire federation service"
```

Then use `metabot-post-buzz`.

## Task 12: Two-Node Local Smoke Test

**Files:**
- Create: `docs/superpowers/smoke/2026-06-02-federated-presence-smoke.md`
- Optionally create: `scripts/smoke-federated-presence.sh`

- [ ] **Step 1: Write smoke runbook**

Document how to run two local nodes with:

- Different HTTP ports.
- Different node private keys.
- `AllowInsecureHTTP=true`.
- Fake or test MANAPI source if chain publish is not practical locally.
- Two Socket.IO clients connecting different `metaid` values.

The mock MANAPI response used by the runbook must include the concrete pin shape the
discovery adapter expects. If the adapter normalizes another real MANAPI shape, show
that real shape instead; otherwise use this mock response:

```json
{
  "code": 1,
  "message": "ok",
  "data": {
    "list": [
      {
        "id": "mock-node-a-pin",
        "operation": "create",
        "path": "/protocols/metaso-p2p-node",
        "contentType": "application/json",
        "chainName": "mvc",
        "timestamp": 1780000000,
        "contentBody": "",
        "contentSummary": "{\"protocol\":\"metaso-p2p-node\",\"version\":\"1.0.0\",\"nodeId\":\"mvc:node-a\",\"network\":\"mvc-testnet\",\"publicBaseUrl\":\"http://127.0.0.1:18091\",\"socketUrl\":\"http://127.0.0.1:18091/socket/socket.io\",\"presenceUrl\":\"http://127.0.0.1:18091/.well-known/metaso-p2p/presence\",\"publicKey\":\"02abcdef...\",\"capabilities\":[\"presence-v1\"],\"publishedAt\":1780000000000,\"validUntil\":1780086400000}"
      },
      {
        "id": "mock-node-b-pin",
        "operation": "create",
        "path": "/protocols/metaso-p2p-node",
        "contentType": "application/json",
        "chainName": "mvc",
        "timestamp": 1780000001,
        "contentBody": "",
        "contentSummary": "{\"protocol\":\"metaso-p2p-node\",\"version\":\"1.0.0\",\"nodeId\":\"mvc:node-b\",\"network\":\"mvc-testnet\",\"publicBaseUrl\":\"http://127.0.0.1:18092\",\"socketUrl\":\"http://127.0.0.1:18092/socket/socket.io\",\"presenceUrl\":\"http://127.0.0.1:18092/.well-known/metaso-p2p/presence\",\"publicKey\":\"02fedcba...\",\"capabilities\":[\"presence-v1\"],\"publishedAt\":1780000001000,\"validUntil\":1780086401000}"
      }
    ],
    "nextCursor": "",
    "total": 2
  }
}
```

When the smoke test uses the real MANAPI path-list endpoint, the equivalent empty
response for an unpublished protocol is:

```json
{
  "code": 1,
  "message": "ok",
  "data": {
    "list": null,
    "nextCursor": "",
    "total": 0
  }
}
```

- [ ] **Step 2: Add script only if it is stable**

If a script is added, it must not require production private keys. It should use local test keys and local mock MANAPI.

- [ ] **Step 3: Run local smoke**

Example shape:

```bash
METASO_P2P_HTTP_ADDR=:18091 \
METASO_P2P_FEDERATION_ENABLED=true \
METASO_P2P_FEDERATION_NODE_PRIVATE_KEY=<node-a-test-private-key> \
METASO_P2P_FEDERATION_PUBLIC_BASE_URL=http://127.0.0.1:18091 \
METASO_P2P_FEDERATION_ALLOW_INSECURE_HTTP=true \
go run ./cmd/metaso-p2p
```

Run node B with a different test private key and port:

```bash
METASO_P2P_HTTP_ADDR=:18092 \
METASO_P2P_FEDERATION_ENABLED=true \
METASO_P2P_FEDERATION_NODE_PRIVATE_KEY=<node-b-test-private-key> \
METASO_P2P_FEDERATION_PUBLIC_BASE_URL=http://127.0.0.1:18092 \
METASO_P2P_FEDERATION_ALLOW_INSECURE_HTTP=true \
go run ./cmd/metaso-p2p
```

Then query:

```bash
curl -s 'http://127.0.0.1:18091/socket/online/list?scope=global' | jq .
curl -s 'http://127.0.0.1:18092/socket/online/list?scope=global' | jq .
curl -s 'http://127.0.0.1:18091/socket/online/stats?scope=global' | jq .
curl -s 'http://127.0.0.1:18092/socket/online/stats?scope=global' | jq .
```

Expected: both nodes show both test `metaid` values after remote pull interval,
and global stats include `totalConnections`, `uniqueMetaIds`, and `nodes`.

- [ ] **Step 4: Verify**

```bash
go test ./... -count=1
git diff --check -- docs/superpowers/smoke/2026-06-02-federated-presence-smoke.md scripts/smoke-federated-presence.sh
```

Expected: PASS and smoke output captured in the runbook.

- [ ] **Step 5: Commit and post buzz**

```bash
git add docs/superpowers/smoke/2026-06-02-federated-presence-smoke.md
git add scripts/smoke-federated-presence.sh # only if created
git commit -m "docs: add federated presence smoke test"
```

Then use `metabot-post-buzz`.

## Final Acceptance

Run from `/Users/tusm/Documents/MetaID_Projects/metaso-p2p`:

```bash
go test ./... -count=1
git status --short --branch
```

Manual acceptance:

1. Start node A and node B.
2. Connect one Socket.IO client to node A with `metaid=agent-a&type=app`.
3. Connect one Socket.IO client to node B with `metaid=agent-b&type=app`.
4. Call `GET /socket/online/list?scope=local` on node A and confirm only `agent-a`.
5. Call `GET /socket/online/list?scope=global` on node A and confirm `agent-a` and `agent-b`.
6. Call `GET /socket/online/stats?scope=global` on node A and confirm
   `data.totalConnections`, `data.uniqueMetaIds`, and `data.nodes` are present and correct.
7. Stop node B and confirm `agent-b` disappears from node A global list after `presenceTTL`.
8. Publish or simulate a revoke pin for node B and confirm discovery removes it.

Release criteria:

- All unit tests pass.
- Two-node smoke passes.
- Federation disabled behavior matches current production behavior.
- Development journal buzz posted for every commit.
- No unrelated files staged or committed.
