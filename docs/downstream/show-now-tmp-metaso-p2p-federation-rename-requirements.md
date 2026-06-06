# show-now-tmp metaso-p2p Federation Rename Requirements

## Purpose

`show-now-tmp` implements the sibling federation-online flow. The metaso-p2p
rename changes the federation naming surface, so show-now-tmp should use the
new protocol names and well-known presence path when discovering or publishing
online-presence nodes.

This is a hard rename. There is no need to preserve the old
`metasocket-node` protocol name because the current federation deployments are
owned by the same project family.

## Required Wire Names

Use these values everywhere show-now-tmp publishes, discovers, validates, or
tests federation nodes:

| Surface | New value |
| --- | --- |
| Registry path | `/protocols/metaso-p2p-node` |
| Node protocol | `metaso-p2p-node` |
| Presence protocol | `metaso-p2p-presence` |
| Presence path | `/.well-known/metaso-p2p/presence` |

## Required Code Changes

1. Replace any registry path lookup for:

```text
/protocols/metasocket-node
```

with:

```text
/protocols/metaso-p2p-node
```

2. Replace any registry payload protocol field:

```json
{"protocol":"metasocket-node"}
```

with:

```json
{"protocol":"metaso-p2p-node"}
```

3. Replace any presence payload protocol field:

```json
{"protocol":"metasocket-presence"}
```

with:

```json
{"protocol":"metaso-p2p-presence"}
```

4. Replace any well-known presence URL suffix:

```text
/.well-known/metasocket/presence
```

with:

```text
/.well-known/metaso-p2p/presence
```

## Acceptance Checks

After the rename, show-now-tmp should verify:

- local federation discovery queries `/protocols/metaso-p2p-node`;
- published node payloads use `protocol: "metaso-p2p-node"`;
- remote snapshot payloads use `protocol: "metaso-p2p-presence"`;
- HTTP presence snapshots are served under
  `/.well-known/metaso-p2p/presence`;
- `scope=global` online-state checks still include remote-node users;
- tests and smoke docs no longer require `metasocket-node`.

## Coordination

Deploy show-now-tmp federation changes after metaso-p2p publishes its new
registry path and presence endpoint. Since the old federation protocol is not
used by external users, no long compatibility window is required.
