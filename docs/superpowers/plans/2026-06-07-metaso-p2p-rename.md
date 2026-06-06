# metaso-p2p Rename Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rename the active project identity from `meta-socket` to `metaso-p2p` and produce downstream migration requirements for Bothub and ShowNow.

**Architecture:** Apply the rename in one feature branch/worktree, then merge back to `main` with `--no-ff`. Code, runtime config, Docker, federation protocol names, and active docs must move together so the product does not expose a mixed identity. Historical evidence can keep old names only when it is explicitly documenting past behavior.

**Tech Stack:** Go 1.26, Gin, Socket.IO, PebbleDB, Docker, Markdown docs, git worktrees.

---

## File Structure

- `go.mod`: module path changes to `github.com/metaid-developers/metaso-p2p`.
- `cmd/metaso-p2p/`: renamed command entrypoint and tests.
- `internal/config/`: new `METASO_P2P_` environment-variable prefix and tests.
- `internal/federation/`: new `metaso-p2p` protocol constants and tests.
- `internal/socket/`: new default presence snapshot path and updated imports.
- `internal/**`, `pkg/**`: import path updates.
- `Dockerfile`: build and install the `metaso-p2p` binary.
- `README.md`, `CLAUDE.md`, `config.example.toml`, active `docs/**`: active documentation uses the new name.
- `docs/downstream/`: downstream migration requirements for Bothub and ShowNow.

## Task 1: Write Failing Rename Tests

**Files:**
- Modify: `internal/config/config_test.go`
- Modify: `internal/federation/signature_test.go`
- Modify: `internal/federation/types.go` only after tests fail

- [ ] **Step 1: Change tests to expect new config and protocol names**

In `internal/config/config_test.go`, update default federation expectations:

```go
if cfg.Federation.RegistryPath != "/protocols/metaso-p2p-node" {
    t.Fatalf("expected default registry path to be metaso-p2p, got %q", cfg.Federation.RegistryPath)
}
if cfg.Federation.PresencePath != "/.well-known/metaso-p2p/presence" {
    t.Fatalf("expected default presence path to be metaso-p2p, got %q", cfg.Federation.PresencePath)
}
```

Update environment overrides in the same file from `META_SOCKET_...` to
`METASO_P2P_...`.

In `internal/federation/signature_test.go` and
`internal/federation/snapshot_test.go`, update expected JSON protocol fields
from `metasocket-presence` to `metaso-p2p-presence`.

- [ ] **Step 2: Verify RED**

Run:

```bash
CGO_ENABLED=0 go test ./internal/config ./internal/federation
```

Expected: FAIL because production defaults and environment reads still use old
names.

## Task 2: Rename Code and Runtime Identity

**Files:**
- Modify: `go.mod`
- Move: `cmd/meta-socket/` to `cmd/metaso-p2p/`
- Modify: `internal/config/config.go`
- Modify: `internal/federation/types.go`
- Modify: `internal/socket/presence.go`
- Modify: `internal/**`, `pkg/**`
- Modify: `Dockerfile`

- [ ] **Step 1: Apply mechanical module and import rename**

Run:

```bash
mv cmd/meta-socket cmd/metaso-p2p
perl -pi -e 's#github.com/metaid-developers/meta-socket#github.com/metaid-developers/metaso-p2p#g' go.mod $(find cmd internal pkg -type f -name '*.go')
```

- [ ] **Step 2: Apply runtime naming changes**

Change `internal/federation/types.go` constants to:

```go
const (
    ProtocolNode     = "metaso-p2p-node"
    ProtocolPresence = "metaso-p2p-presence"
    RegistryPath     = "/protocols/metaso-p2p-node"
    PresencePath     = "/.well-known/metaso-p2p/presence"
    Version          = "1.0.0"
)
```

Change `internal/socket/presence.go` default path to:

```go
const defaultPresenceSnapshotPath = "/.well-known/metaso-p2p/presence"
```

Change every `META_SOCKET_` environment variable read in production Go code and
tests to the `METASO_P2P_` prefix.

- [ ] **Step 3: Update Docker build**

Change Dockerfile build/install/entrypoint names to:

```dockerfile
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o metaso-p2p ./cmd/metaso-p2p/
COPY --from=builder /app/metaso-p2p /usr/local/bin/metaso-p2p
ENV METASO_P2P_PEBBLE_DATA_DIR=/data/pebble
ENTRYPOINT ["metaso-p2p"]
```

- [ ] **Step 4: Verify GREEN**

Run:

```bash
gofmt -w $(find cmd internal pkg -type f -name '*.go')
CGO_ENABLED=0 go test ./internal/config ./internal/federation ./internal/socket
```

Expected: PASS.

## Task 3: Rename Active Documentation and Examples

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `config.example.toml`
- Move/update active docs that contain the old project name.

- [ ] **Step 1: Rename active docs and examples**

Update active build, deployment, and config examples to use:

```bash
go build ./cmd/metaso-p2p/
METASO_P2P_HTTP_ADDR=:8080
./metaso-p2p
```

Update federation examples to use:

```toml
registryPath = "/protocols/metaso-p2p-node"
presencePath = "/.well-known/metaso-p2p/presence"
```

- [ ] **Step 2: Scan active code and docs**

Run:

```bash
rg -n "meta-socket|MetaSocket|metasocket|META_SOCKET|github.com/metaid-developers/meta-socket" go.mod cmd internal pkg Dockerfile README.md CLAUDE.md config.example.toml docs
```

Expected: only intentional migration/historical context remains in docs.

## Task 4: Add Downstream Requirement Documents

**Files:**
- Create: `docs/downstream/bothub-metaso-p2p-rename-requirements.md`
- Create: `docs/downstream/show-now-tmp-metaso-p2p-federation-rename-requirements.md`

- [ ] **Step 1: Document Bothub migration requirements**

The Bothub document must specify:

- Rename user-facing/backend label from `meta-socket` to `metaso-p2p`.
- Rename local env examples from `VITE_META_SOCKET_BASE_URL` to
  `VITE_METASO_P2P_BASE_URL`.
- Rename smoke-test env examples from `META_SOCKET_BASE_URL` to
  `METASO_P2P_BASE_URL`.
- Point the runtime base URL to the new host, likely `https://so.metaid.io`
  once DNS is ready.

- [ ] **Step 2: Document ShowNow migration requirements**

The ShowNow document must specify:

- Change registry path to `/protocols/metaso-p2p-node`.
- Change node protocol to `metaso-p2p-node`.
- Change presence protocol to `metaso-p2p-presence`.
- Change presence path to `/.well-known/metaso-p2p/presence`.

## Task 5: Final Verification, Commit, Merge, Push

**Files:**
- All modified files.

- [ ] **Step 1: Run final verification**

Run:

```bash
CGO_ENABLED=0 go test ./...
CGO_ENABLED=0 go build -o /tmp/metaso-p2p ./cmd/metaso-p2p
git diff --check
```

Expected: all commands pass.

- [ ] **Step 2: Commit feature branch changes**

Commit logical units with messages:

```bash
git commit -m "docs: plan metaso-p2p rename"
git commit -m "refactor: rename project to metaso-p2p"
git commit -m "docs: add downstream metaso-p2p migration requirements"
```

Post a detailed development-journal buzz after each commit.

- [ ] **Step 3: Merge and push**

From the main worktree:

```bash
git merge --no-ff codex/metaso-p2p-rename
CGO_ENABLED=0 go test ./...
git push origin main
git fetch --all --prune
git rev-list --left-right --count origin/main...main
```

Expected final divergence: `0 0`.

## Self-Review

- Spec coverage: covered repo/module/cmd rename, runtime env prefix,
  federation hard-cut, active docs, downstream docs, verification, merge, and
  push.
- Placeholder scan: no TBD/TODO placeholders.
- Type consistency: new strings are consistently `metaso-p2p`,
  `METASO_P2P`, `metaso-p2p-node`, and `metaso-p2p-presence`.
