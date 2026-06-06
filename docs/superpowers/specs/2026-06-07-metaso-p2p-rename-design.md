# metaso-p2p Rename Design

## Goal

Rename this project from `meta-socket` / `MetaSocket` / `metasocket` to
`metaso-p2p` across the active codebase, runtime artifacts, and downstream
handoff documents.

The rename is a product-positioning reset, not a fork. The project should keep
its Git history and continue as the same code line under the new repository
name `metaso-p2p`.

## Decisions

- The GitHub repository should be renamed from `metaid-developers/meta-socket`
  to `metaid-developers/metaso-p2p`.
- The local project directory should become
  `/Users/tusm/Documents/MetaID_Projects/metaso-p2p` after the main branch is
  merged and pushed.
- The Go module path becomes `github.com/metaid-developers/metaso-p2p`.
- The command package and binary become `cmd/metaso-p2p` and `metaso-p2p`.
- Runtime environment variables use the `METASO_P2P_` prefix. The old
  `META_SOCKET_` prefix is intentionally not supported in active code.
- Federation protocol names hard-cut to:
  - `/protocols/metaso-p2p-node`
  - `metaso-p2p-node`
  - `metaso-p2p-presence`
  - `/.well-known/metaso-p2p/presence`
- Production DNS is handled outside this code change. Documentation should use
  `https://so.metaid.io` as the likely target when a concrete example is useful,
  while still allowing the final host to be supplied by deployment config.
- Historical issues may mention the old name only when they are describing old
  evidence. Active docs, examples, tests, code, config, and Docker artifacts
  should use `metaso-p2p`.

## Architecture

The rename is implemented as a direct in-place rename. No copied repository and
no parallel service are created.

Code-level identity has three active naming surfaces:

1. **Build identity:** Go module imports, command path, Docker binary name, and
   README build commands.
2. **Runtime configuration:** environment variable prefix and examples.
3. **Federation wire identity:** registry protocol, presence protocol, registry
   path, and well-known presence path.

The implementation should change all three surfaces together so the service
does not present a mixed identity.

## Downstream Impact

Bothub is the only direct downstream frontend. It needs a requirement document
that tells it to rename its public config and smoke-test naming from
`META_SOCKET` / `meta-socket` to `METASO_P2P` / `metaso-p2p`, and to point at
the new public root once DNS is ready.

ShowNow is the sibling federation implementation. It needs a requirement
document that tells it to change the federation registry and presence protocol
names to the new `metaso-p2p` names.

## Verification

Required local verification:

- `rg` confirms there are no old naming tokens in active code paths.
- `CGO_ENABLED=0 go test ./...` passes.
- `CGO_ENABLED=0 go build -o /tmp/metaso-p2p ./cmd/metaso-p2p` passes.
- `git diff --check` passes.

Required repository verification after merge and push:

- `git rev-list --left-right --count origin/main...main` returns `0 0`.
- The only remaining old naming references are explicitly historical or
  migration-context references in docs.
