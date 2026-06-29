# Bot Homepage Backfill CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a reusable one-off Bot Homepage backfill command that can backfill all supported homepage read-model data since a chosen time, and can narrow the run by output section or protocol path.

**Architecture:** Keep the online service startup behavior unchanged. Add a separate CLI under `cmd/metaso-p2p-bot-homepage-backfill` that opens the same Pebble store while the service is stopped, initializes the relevant aggregators, maps sections/paths to existing backfill implementations, and runs them with a shared MANAPI client configuration. Update `publishedcontent.Backfill` to replay historical pages oldest-first like `userinfo` and `skillservice`, so modify/revoke chains fold correctly during larger historical runs.

**Tech Stack:** Go, Pebble, existing `userinfo`, `skillservice`, and `publishedcontent` aggregators, MANAPI `/pin/path/list`.

---

### Task 1: Backfill Target Parsing

**Files:**
- Create: `cmd/metaso-p2p-bot-homepage-backfill/main_test.go`
- Create: `cmd/metaso-p2p-bot-homepage-backfill/main.go`

- [ ] **Step 1: Write failing tests for defaults and filters**

Add tests that prove `parseOptions`:
- uses explicit `--since` over `--lookback`
- defaults to all supported homepage sections
- maps `--sections metaapps` to `/protocols/metaapp`
- maps `--paths /protocols/metaapp` to only the publishedcontent metaapp path

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
CGO_ENABLED=0 go test ./cmd/metaso-p2p-bot-homepage-backfill -count=1
```

Expected: fails because the command package does not exist.

- [ ] **Step 3: Implement minimal parser and target model**

Create the CLI with flags:

```text
--data-dir
--manapi-base-url
--since
--lookback
--timeout
--page-size
--sections
--paths
```

Supported sections:

```text
profile  -> userinfo default info paths
services -> skillservice default paths
metaapps -> /protocols/metaapp
buzzes   -> /protocols/simplebuzz
skills   -> /protocols/metabot-skill
all      -> profile,services,metaapps,buzzes,skills
```

Reject unsupported sections and unsupported paths with a clear error.

- [ ] **Step 4: Run parser tests green**

Run:

```bash
CGO_ENABLED=0 go test ./cmd/metaso-p2p-bot-homepage-backfill -count=1
```

Expected: pass.

### Task 2: Published Content Oldest-First Replay

**Files:**
- Modify: `internal/aggregator/publishedcontent/backfill_test.go`
- Modify: `internal/aggregator/publishedcontent/backfill.go`

- [ ] **Step 1: Write failing modify-chain replay test**

Add a test where MANAPI returns a `/protocols/metaapp` modify before its create in newest-first order. The final record must expose the modified payload and keep the original create timestamp.

- [ ] **Step 2: Run test to verify it fails**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/publishedcontent -run TestBackfillReplaysPublishedContentPinsOldestFirstWithinLookback -count=1
```

Expected: fails because current backfill processes MANAPI rows in returned order.

- [ ] **Step 3: Implement buffered oldest-first replay**

Match `userinfo` and `skillservice`: collect pins inside the cutoff for each path, then replay from the oldest collected item to newest via `processPin`.

- [ ] **Step 4: Run package tests green**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/publishedcontent -count=1
```

Expected: pass.

### Task 3: Execute Backfill Command

**Files:**
- Modify: `cmd/metaso-p2p-bot-homepage-backfill/main.go`
- Test: `cmd/metaso-p2p-bot-homepage-backfill/main_test.go`

- [ ] **Step 1: Implement aggregator initialization and execution**

Open the Pebble store, create one cache provider, initialize only the target aggregators, and run each selected aggregator with the selected paths. Log start and complete lines including data dir, MANAPI URL, since, sections, paths, page size, and timeout.

- [ ] **Step 2: Run focused tests**

Run:

```bash
CGO_ENABLED=0 go test ./cmd/metaso-p2p-bot-homepage-backfill ./internal/aggregator/publishedcontent -count=1
```

Expected: pass.

- [ ] **Step 3: Run broader verification**

Run:

```bash
CGO_ENABLED=0 go test ./... -count=1
git diff --check
```

Expected: pass.

### Task 4: Commit, Deploy, and Production MetaAPP Backfill

**Files:**
- Commit modified/new files only.
- Build:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w -X main.version=$(git rev-parse --short HEAD)" -o /tmp/metaso-p2p-<sha>-linux-amd64 ./cmd/metaso-p2p
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /tmp/metaso-p2p-bot-homepage-backfill-<sha>-linux-amd64 ./cmd/metaso-p2p-bot-homepage-backfill
```

- Deploy main branch binary to `47.242.16.22`.
- Stop `metaso-p2p.service`.
- Run:

```bash
/tmp/metaso-p2p-bot-homepage-backfill-<sha>-linux-amd64 \
  --data-dir /mnt/metaso-p2p/pebble \
  --manapi-base-url https://manapi.metaid.io \
  --since 2025-11-01 \
  --timeout 60m \
  --page-size 100 \
  --sections metaapps
```

- Start `metaso-p2p.service`.
- Verify `https://so.metaid.io/healthz`, `https://socket.metaid.io/healthz`, and `bot-homepage.v3.sections.metaapps`.
