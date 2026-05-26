# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Meta-Socket is a modular, high-performance middleware backend for the MetaID protocol. It serves as a drop-in replacement for **idchat** (a decentralized chat application), requiring zero code changes to idchat itself — only configuration URLs change. It provides:

- Real-time Socket.IO push for group chat, private chat, and role-change notifications
- Group/private chat aggregation (message indexing, history queries)
- User identity indexing (name, avatar, bio, chat key)
- Chat blocking/unblocking management
- Multi-chain blockchain indexing (BTC, MVC, DOGE, OPCAT)

**Tech stack:** Go 1.26, Gin HTTP framework, Socket.IO v2 (Go), Pebble embedded storage, btcd RPC, ZMQ mempool, two-level LRU cache.

## Build & Test Commands

```bash
go build ./cmd/meta-socket/    # Build the binary
go test ./...                   # Run all tests
go test ./internal/storage/     # Run a specific package
```

All configuration uses environment variables with the `META_SOCKET_` prefix. See `internal/config/config.go`.

## Architecture

The project follows a pipeline pattern with pluggable aggregators:

```
Chain RPC + ZMQ  →  Indexer Engine  →  Aggregator Registry
                                           ├── UserInfo Aggregator    (HTTP + cache)
                                           ├── GroupChat Aggregator   (HTTP + push)
                                           ├── PrivateChat Aggregator (HTTP + push)
                                           └── Notify Aggregator      (HTTP)
                                                │
                                         Socket.IO Server  →  idchat clients
```

Key principles:
- **Zero external database** — PebbleDB only (embedded). No MongoDB, MySQL, or Redis.
- **Pluggable aggregators** — each domain module implements the `Aggregator` interface and registers with the Registry.
- **Two-level cache** — L1 in-memory expirable LRU + L2 Pebble persistent storage.
- **Wire-compatible with idchat** — API format and Socket.IO protocol match the legacy backend exactly.

### Source Layout

| Directory | Purpose |
|---|---|
| `cmd/meta-socket/main.go` | Entry point: bootstraps store, cache, aggregators, HTTP server |
| `internal/config/` | Env-based config (`META_SOCKET_*`), validation, defaults |
| `internal/storage/` | Namespaced PebbleDB wrapper (Set/Get/Delete/ScanPrefix) |
| `internal/cache/` | Two-level cache: L1 (LRU in-memory) + L2 (Pebble) |
| `internal/api/` | Unified JSON response format `{code, data, message, processingTime}` |
| `internal/chain/` | Chain and Indexer interfaces + BTC/MVC/DOGE/OPCAT adapters |
| `internal/indexer/` | Block scanning engine + ZMQ mempool loop |
| `internal/aggregator/` | Aggregator interface, Registry, and domain modules (userinfo, groupchat, privatechat, notify) |

### Development Phases

The project follows a 7-phase goal-driven development plan (see `docs/GOAL_DRIVEN.md`). All HTTP endpoints return a uniform JSON envelope: `{code: 0, data: ..., message: "", processingTime: <ms>}` for success, or `{code: 1, message: "..."}` for errors.

## Commit and Merge Rules

- If you notice unfamiliar or unrelated file changes, continue working and stay focused on your own scoped edits unless the user asks you to inspect them.
- For each completed round that modifies existing code/docs or adds new code/docs, automatically stage and commit only the files you changed and understand.
- For deletion changes, wait until the user explicitly says "commit" before staging and committing those deletions.
- Prefer small, frequent commits. Commit each independent, verifiable unit of work as soon as it is complete.
- For every modification or newly added feature, create one commit.
- For every commit, use the `metabot-post-buzz` skill with the Eric identity to post a detailed development-journal entry on-chain describing the change.
- Use commit messages in the format `<type>: <short description>`, where `<type>` is one of `feat`, `fix`, `refactor`, `docs`, or `chore`.
- Before committing, make sure the relevant local tests or verification steps pass for your changes.
- When merging completed work into `main`, use `git merge --no-ff` to preserve the feature merge point.

## Behavioral Guidelines

**Tradeoff:** These guidelines bias toward caution over speed. For trivial tasks, use judgment.

### 1. Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them — don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

### 2. Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

### 3. Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it — don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

### 4. Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

---

**These guidelines are working if:** fewer unnecessary changes in diffs, fewer rewrites due to overcomplication, and clarifying questions come before implementation rather than after mistakes.
