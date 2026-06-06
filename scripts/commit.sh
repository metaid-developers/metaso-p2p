#!/bin/bash
# Run from repo root to commit the v2 architecture + docs.
set -e

git add -A
git commit -m "$(cat <<'EOF'
feat: metaso-p2p v2 architecture + goal-driven development docs

Clean rewrite. 14 Go source files (Phase 1 skeleton) + 3 docs.

Architecture:
- internal/storage/  — namespaced PebbleStore
- internal/cache/    — two-level cache (L1 LRU + L2 Pebble)
- internal/chain/    — Chain + Indexer interfaces + BTC adapter
- internal/indexer/  — block scanning engine
- internal/aggregator/ — pluggable Aggregator interface + Registry
- internal/api/      — idchat-compatible response format
- internal/config/   — env-based configuration

Docs:
- docs/GOAL_DRIVEN.md         — 7-phase goal-driven development plan
- docs/IMPLEMENTATION_PLAN.md — module architecture specs
- docs/IDCHAT_API_CONTRACT.md — complete idchat API contract

Old prototype code (adapter, pipeline, groupchat, socket, tests) removed.
Phase 1 complete. Ready for Phase 2 (Socket.IO server).
EOF
)"
