# Federated Presence Two-Node Local Smoke

## Purpose and Scope

This runbook verifies the local federated presence path without publishing real
registry pins on chain. It starts two local `metaso-p2p` nodes with different
HTTP ports and different deterministic test private keys, points discovery at a
local mock MANAPI response, connects one Socket.IO client to each node, then
checks local and global presence views.

The smoke covers:

- Local Socket.IO presence on each node.
- MANAPI discovery of the other local node.
- Remote presence snapshot pull from `/.well-known/metaso-p2p/presence`.
- Global online list and stats aggregation.

It does not prove real MVC registry publishing. The local mock Metalet endpoint
returns no UTXOs so publish attempts can fail without touching production
wallets or chain services.

No `scripts/smoke-federated-presence.sh` is included for this smoke. A stable
script would need to orchestrate a long-running mock MANAPI/Metalet server, two
Go nodes, and two persistent Socket.IO clients, so v1 intentionally documents
manual terminal steps instead.

## Required Terminals

Use six terminals from the repository root:

1. Terminal 1: local mock MANAPI and mock Metalet.
2. Terminal 2: node A on `127.0.0.1:18091`.
3. Terminal 3: node B on `127.0.0.1:18092`.
4. Terminal 4: Socket.IO client for `agent-a`.
5. Terminal 5: Socket.IO client for `agent-b`.
6. Terminal 6: curl checks.

Prerequisites:

- Go toolchain for `go run ./cmd/metaso-p2p`. The node commands below set
  `CGO_ENABLED=0` so this smoke still runs on macOS environments with a broken
  cgo SDK setup.
- Python 3 for the local mock server. On macOS, use `/usr/bin/python3` if PATH
  `python3` points to a broken framework build.
- `curl`.
- `jq` for readable JSON output.
- Node.js with `npm`. The repo does not bundle a Node Socket.IO client
  dependency, so the client commands below install `socket.io-client@4.7.5` in
  a temporary directory and expose it with `NODE_PATH`.

## Test Keys

These are local-only deterministic secp256k1 keys. Do not use them for
production wallets.

Node A uses the key already present in `internal/federation/mvc_tx_test.go`:

```text
private key: 0000000000000000000000000000000000000000000000000000000000000001
address:     1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH
nodeId:      mvc:1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH
public key:  0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798
```

Node B uses a different deterministic local-only key:

```text
private key: 0000000000000000000000000000000000000000000000000000000000000002
address:     1cMh228HTCiwS8ZsaakH8A8wze1JR5ZsP
nodeId:      mvc:1cMh228HTCiwS8ZsaakH8A8wze1JR5ZsP
public key:  02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5
```

## Port Preflight

Before starting Terminal 1, check whether the smoke ports are already occupied:

```bash
lsof -nP -iTCP:18090 -sTCP:LISTEN || true
lsof -nP -iTCP:18091 -sTCP:LISTEN || true
lsof -nP -iTCP:18092 -sTCP:LISTEN || true
```

If any port is occupied, stop that process if it belongs to a previous smoke
run. Otherwise choose alternate ports and update the mock MANAPI payload plus
both node env blocks consistently.

## Terminal 1: Local Mock MANAPI and Metalet

Start one local Python server that serves both:

- MANAPI path-list discovery at `/pin/path/list`.
- Mock Metalet wallet endpoints under `/wallet-api`.

The discovery adapter currently reads MANAPI entries from
`data.list[].id`, `operation`, `chainName`, `timestamp`, `contentBody`, and
`contentSummary`. The mock also includes `path` and `contentType` so the
response resembles real MetaID pin data. `contentBody` is used first; when it is
empty the adapter falls back to parsing `contentSummary`.

```bash
/usr/bin/python3 - <<'PY'
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse
import json
import time

NODE_A = {
    "protocol": "metaso-p2p-node",
    "version": "1.0.0",
    "nodeId": "mvc:1BgGZ9tcN4rm9KBzDn7KprQz87SZ26SAMH",
    "network": "mvc-mainnet",
    "publicBaseUrl": "http://127.0.0.1:18091",
    "socketUrl": "http://127.0.0.1:18091/socket/socket.io",
    "presenceUrl": "http://127.0.0.1:18091/.well-known/metaso-p2p/presence",
    "publicKey": "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
    "capabilities": ["presence-v1"],
}

NODE_B = {
    "protocol": "metaso-p2p-node",
    "version": "1.0.0",
    "nodeId": "mvc:1cMh228HTCiwS8ZsaakH8A8wze1JR5ZsP",
    "network": "mvc-mainnet",
    "publicBaseUrl": "http://127.0.0.1:18092",
    "socketUrl": "http://127.0.0.1:18092/socket/socket.io",
    "presenceUrl": "http://127.0.0.1:18092/.well-known/metaso-p2p/presence",
    "publicKey": "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5",
    "capabilities": ["presence-v1"],
}

class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):
        print("%s - %s" % (self.address_string(), fmt % args))

    def send_json(self, body, status=200):
        raw = json.dumps(body, separators=(",", ":")).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(raw)))
        self.end_headers()
        self.wfile.write(raw)

    def do_GET(self):
        parsed = urlparse(self.path)
        if parsed.path == "/pin/path/list":
            query = parse_qs(parsed.query)
            requested_path = query.get("path", [""])[0]
            if requested_path != "/protocols/metaso-p2p-node":
                return self.send_json({"code": 1, "message": "ok", "data": {"list": None, "nextCursor": "", "total": 0}})

            now_ms = int(time.time() * 1000)
            valid_until = now_ms + 24 * 60 * 60 * 1000
            payloads = [dict(NODE_A), dict(NODE_B)]
            pins = []
            for idx, payload in enumerate(payloads):
                payload["publishedAt"] = now_ms + idx
                payload["validUntil"] = valid_until + idx
                pins.append({
                    "id": "mock-node-%s-pin" % ("a" if idx == 0 else "b"),
                    "operation": "create",
                    "path": "/protocols/metaso-p2p-node",
                    "contentType": "application/json",
                    "chainName": "mvc",
                    "timestamp": int(time.time()) + idx,
                    "contentBody": "" if idx == 0 else json.dumps(payload, separators=(",", ":")),
                    "contentSummary": json.dumps(payload, separators=(",", ":")),
                })
            return self.send_json({"code": 1, "message": "ok", "data": {"list": pins, "nextCursor": "", "total": len(pins)}})

        if parsed.path == "/wallet-api/v4/mvc/address/utxo-list":
            return self.send_json({"code": 0, "message": "success", "processingTime": 1, "data": {"list": []}})

        return self.send_json({"code": 1, "message": "not found", "data": None}, status=404)

    def do_POST(self):
        parsed = urlparse(self.path)
        if parsed.path == "/wallet-api/v4/mvc/tx/broadcast":
            return self.send_json({"code": 1, "message": "mock wallet has no funded UTXO", "processingTime": 1, "data": None})
        return self.send_json({"code": 1, "message": "not found", "data": None}, status=404)

ThreadingHTTPServer(("127.0.0.1", 18090), Handler).serve_forever()
PY
```

Check the mock response:

```bash
curl -s 'http://127.0.0.1:18090/pin/path/list?path=/protocols/metaso-p2p-node&size=5' | jq .
```

## Terminal 2: Node A

```bash
CGO_ENABLED=0 \
METASO_P2P_HTTP_ADDR=127.0.0.1:18091 \
METASO_P2P_PEBBLE_ENABLED=false \
METASO_P2P_FEDERATION_ENABLED=true \
METASO_P2P_FEDERATION_NETWORK=mvc-mainnet \
METASO_P2P_FEDERATION_NODE_PRIVATE_KEY=0000000000000000000000000000000000000000000000000000000000000001 \
METASO_P2P_FEDERATION_PUBLIC_BASE_URL=http://127.0.0.1:18091 \
METASO_P2P_FEDERATION_MANAPI_BASE_URL='http://127.0.0.1:18090/pin/path/list?path={protocol-path}&size={size}' \
METASO_P2P_FEDERATION_METALET_BASE_URL=http://127.0.0.1:18090 \
METASO_P2P_FEDERATION_ALLOW_INSECURE_HTTP=true \
METASO_P2P_FEDERATION_DISCOVERY_INTERVAL=5s \
METASO_P2P_FEDERATION_PRESENCE_PULL_INTERVAL=5s \
METASO_P2P_FEDERATION_PRESENCE_TTL=60s \
METASO_P2P_FEDERATION_REQUEST_TIMEOUT=2s \
go run ./cmd/metaso-p2p
```

## Terminal 3: Node B

```bash
CGO_ENABLED=0 \
METASO_P2P_HTTP_ADDR=127.0.0.1:18092 \
METASO_P2P_PEBBLE_ENABLED=false \
METASO_P2P_FEDERATION_ENABLED=true \
METASO_P2P_FEDERATION_NETWORK=mvc-mainnet \
METASO_P2P_FEDERATION_NODE_PRIVATE_KEY=0000000000000000000000000000000000000000000000000000000000000002 \
METASO_P2P_FEDERATION_PUBLIC_BASE_URL=http://127.0.0.1:18092 \
METASO_P2P_FEDERATION_MANAPI_BASE_URL='http://127.0.0.1:18090/pin/path/list?path={protocol-path}&size={size}' \
METASO_P2P_FEDERATION_METALET_BASE_URL=http://127.0.0.1:18090 \
METASO_P2P_FEDERATION_ALLOW_INSECURE_HTTP=true \
METASO_P2P_FEDERATION_DISCOVERY_INTERVAL=5s \
METASO_P2P_FEDERATION_PRESENCE_PULL_INTERVAL=5s \
METASO_P2P_FEDERATION_PRESENCE_TTL=60s \
METASO_P2P_FEDERATION_REQUEST_TIMEOUT=2s \
go run ./cmd/metaso-p2p
```

## Terminals 4 and 5: Socket.IO Clients

The Socket.IO path is `/socket/socket.io`. Keep both clients running while the
curl checks execute. Each client uses a temporary `socket.io-client@4.7.5`
install and cleans it up when the subshell exits after Ctrl-C.

Terminal 4, connect `agent-a` to node A:

```bash
(
  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT
  npm install --silent --prefix "$tmpdir" socket.io-client@4.7.5 >/dev/null
  NODE_PATH="$tmpdir/node_modules" node -e '
  const { io } = require("socket.io-client");
  const socket = io("http://127.0.0.1:18091", {
    path: "/socket/socket.io",
    query: { metaid: "agent-a", type: "app" }
  });
  socket.on("connect", () => {
    console.log("agent-a connected", socket.id);
    socket.emit("ping");
  });
  socket.on("heartbeat_ack", () => console.log("agent-a heartbeat_ack"));
  socket.on("disconnect", (reason) => console.log("agent-a disconnect", reason));
  setInterval(() => socket.emit("ping"), 15000);
  process.stdin.resume();
  '
)
```

Terminal 5, connect `agent-b` to node B:

```bash
(
  tmpdir=$(mktemp -d)
  trap 'rm -rf "$tmpdir"' EXIT
  npm install --silent --prefix "$tmpdir" socket.io-client@4.7.5 >/dev/null
  NODE_PATH="$tmpdir/node_modules" node -e '
  const { io } = require("socket.io-client");
  const socket = io("http://127.0.0.1:18092", {
    path: "/socket/socket.io",
    query: { metaid: "agent-b", type: "app" }
  });
  socket.on("connect", () => {
    console.log("agent-b connected", socket.id);
    socket.emit("ping");
  });
  socket.on("heartbeat_ack", () => console.log("agent-b heartbeat_ack"));
  socket.on("disconnect", (reason) => console.log("agent-b disconnect", reason));
  setInterval(() => socket.emit("ping"), 15000);
  process.stdin.resume();
  '
)
```

## Terminal 6: Curl Checks

Wait at least one discovery interval plus one presence pull interval after both
nodes and both Socket.IO clients are running. With the env values above, wait
about 10 to 15 seconds.

Local list on A should show only `agent-a`:

```bash
curl -s 'http://127.0.0.1:18091/socket/online/list?scope=local&page=1&size=20' | jq .
```

Global list on A should show `agent-a` and `agent-b`:

```bash
curl -s 'http://127.0.0.1:18091/socket/online/list?scope=global&page=1&size=20' | jq .
```

Local stats on A:

```bash
curl -s 'http://127.0.0.1:18091/socket/online/stats?scope=local' | jq .
```

Global stats on A:

```bash
curl -s 'http://127.0.0.1:18091/socket/online/stats?scope=global' | jq .
```

Local list on B should show only `agent-b`:

```bash
curl -s 'http://127.0.0.1:18092/socket/online/list?scope=local&page=1&size=20' | jq .
```

Global list on B should show `agent-a` and `agent-b`:

```bash
curl -s 'http://127.0.0.1:18092/socket/online/list?scope=global&page=1&size=20' | jq .
```

Local stats on B:

```bash
curl -s 'http://127.0.0.1:18092/socket/online/stats?scope=local' | jq .
```

Global stats on B:

```bash
curl -s 'http://127.0.0.1:18092/socket/online/stats?scope=global' | jq .
```

Well-known presence snapshot on A:

```bash
curl -s 'http://127.0.0.1:18091/.well-known/metaso-p2p/presence' | jq .
```

Well-known presence snapshot on B:

```bash
curl -s 'http://127.0.0.1:18092/.well-known/metaso-p2p/presence' | jq .
```

Default live MANAPI empty-path check:

```bash
curl -s 'https://manapi.metaid.io/pin/path/list?path=/protocols/metaso-p2p-node&size=5' | jq .
```

When no real node registry pins are published yet, the live MANAPI check may
return an unpublished/empty result like:

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

An empty list is also acceptable:

```json
{
  "code": 1,
  "message": "ok",
  "data": {
    "list": [],
    "nextCursor": "",
    "total": 0
  }
}
```

## Expected Results

- Node A local list contains `agent-a` only.
- Node A global list contains both `agent-a` and `agent-b` after discovery and
  remote pull complete.
- Node A global stats includes `totalConnections`, `uniqueMetaIds`, and
  `nodes`. With one client per node, expect values equivalent to
  `totalConnections=2`, `uniqueMetaIds=2`, and `nodes=2`.
- The presence snapshots expose `protocol=metaso-p2p-presence`, each node's
  `nodeId`, a non-empty `signature`, and the node's local `items`.
- The mock MANAPI entries are accepted because their registry payload has
  `protocol=metaso-p2p-node`, version `1.0.0`, matching `mvc-mainnet`,
  parseable compressed public keys, `presence-v1`, and unexpired `validUntil`.
- The live MANAPI check can remain empty until real registry pins are published.

## Troubleshooting

- Metalet UTXO or broadcast failures do not block local Socket.IO presence,
  local online list, or the well-known presence snapshot. They only prevent real
  chain registry publish/renew.
- Local HTTP `publicBaseUrl` and `presenceUrl` values require
  `METASO_P2P_FEDERATION_ALLOW_INSECURE_HTTP=true`.
- Discovery and pull are interval-based. After starting clients, wait at least
  one `METASO_P2P_FEDERATION_DISCOVERY_INTERVAL` plus one
  `METASO_P2P_FEDERATION_PRESENCE_PULL_INTERVAL`.
- Use the Socket.IO path `/socket/socket.io`. If the client connects to the
  root path or `/socket.io`, the primary smoke path is not being exercised.
- If global list shows only the local metaid, check the mock MANAPI response,
  `chainName=mvc`, `operation=create`, `network=mvc-mainnet`, `validUntil`, and
  the peer's `publicKey`.
- If snapshots are pulled but rejected, confirm the mock registry `nodeId`
  matches the node's derived address and `publicKey` matches that node's
  private key.
