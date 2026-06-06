# Bothub Delivery does not observe private-chat WebSocket pushes

## Summary

Bothub Delivery has frontend Socket.IO wiring for private-chat delivery updates,
but in live use the user does not see new Bot replies appear through WebSocket
push in the currently open Delivery conversation.

The current evidence suggests this is not simply a missing Delivery render
listener. Bothub connects to the public metaso-p2p Socket.IO endpoint, receives
heartbeats, and has code paths that merge `WS_SERVER_NOTIFY_PRIVATE_CHAT`
payloads into Delivery state. Meta-socket needs to confirm whether private-chat
push events are produced and routed correctly for BotHub order replies.

Please verify and fix the metaso-p2p side of private-chat WebSocket delivery,
especially target identity matching, push payload shape, and real-time indexer
event production.

## Environment

- Date checked: 2026-06-04 CST
- Public metaso-p2p base URL: `https://socket.metaid.io`
- Public metaso-p2p version from `/healthz`: `0b75c55`
- Bothub env:
  - `VITE_METASO_P2P_BASE_URL=https://socket.metaid.io`
  - `VITE_USE_AGGREGATOR_MOCK=false`
  - `VITE_USE_WS_MOCK=false`
- Observed connected buyer identity in prior Delivery QA:
  `idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0`

## Current Live Checks

Public health is healthy:

```bash
curl -sS https://socket.metaid.io/healthz
```

Result summary:

```json
{"code":0,"data":{"service":"metaso-p2p","status":"ok","version":"0b75c55"}}
```

Bothub smoke against the public endpoint passes HTTP service checks and Socket.IO
heartbeat:

```bash
METASO_P2P_BASE_URL=https://socket.metaid.io pnpm smoke:metaso-p2p
```

Result summary:

```json
{
  "ok": true,
  "baseUrl": "https://socket.metaid.io",
  "socket": {
    "onlineStats": {
      "totalConnections": 2
    },
    "heartbeatAck": true
  },
  "privateChat": {
    "skipped": true,
    "reason": "set METASO_P2P_PRIVATE_CHAT_METAID and METASO_P2P_PRIVATE_CHAT_OTHER_METAID to enable live private-chat checks"
  }
}
```

The public socket presence endpoint also shows Bothub-style app connections:

```bash
curl -sS 'https://socket.metaid.io/socket/online/list?page=1&size=20'
```

Result summary:

```json
{
  "code": 0,
  "data": {
    "items": [
      {
        "metaid": "idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0",
        "type": "app"
      },
      {
        "metaid": "12ghVWG1yAgNjzXj4mr3qK9DgyornMUikZ",
        "type": "app"
      }
    ]
  }
}
```

This proves that browser/app clients can connect and heartbeat. It does not
prove that private-chat order replies are being pushed.

## Bothub Frontend Behavior

Bothub does have private-chat WebSocket handling:

- `WalletHydrator` calls `connectSocket(lifecycleIdentity)` after wallet
  hydration.
- `useSocket.connect` opens Socket.IO connections for both `globalMetaId` and
  `mvcAddress`.
- `connectSocket` uses `/socket/socket.io`, query `{ metaid, type: "app" }`,
  emits `ping`, receives `heartbeat_ack`, and listens for the `message` event.
- `useSocket.handleEnvelope` accepts `WS_SERVER_NOTIFY_PRIVATE_CHAT`, normalizes
  the payload, checks that the message is addressed to the connected identity,
  then merges it into Delivery memory and IndexedDB.

Relevant Bothub tests currently cover:

- parsing Socket.IO `message` envelopes;
- normalizing private-chat rows with `from`/`to` payload fields;
- merging a live private-chat payload into Delivery state.

So the likely failure point is after connection but before or during live
private-chat event delivery.

## Meta-Socket Code Observations

Meta-socket has a private-chat notify path:

- `privatechat.HandleBlockPin` and `HandleMempoolPin` can return a
  `WS_SERVER_NOTIFY_PRIVATE_CHAT` event.
- `socket.Server.StartPushConsumer` reads aggregator notify channels.
- `routeNotifyEvent` sends private-chat pushes through `SendToUser`.

However, there are three concrete gaps/risks to verify.

### 1. Target identity may not match connected clients

Current private-chat notify construction sets:

```go
notifyEvent := &aggregator.NotifyEvent{
    Type:    "WS_SERVER_NOTIFY_PRIVATE_CHAT",
    MetaId:  toMetaId,
    Payload: notifyPayload,
}
```

The socket server then routes using exact equality:

```go
if vals, ok := query["metaid"]; ok && len(vals) > 0 && vals[0] == metaId {
    sock.Emit("message", msg)
}
```

If a simplemsg `to` value is a local metaid, address, globalMetaId, or another
alias that does not exactly match the connected `metaid` query, the message will
not be delivered even though the user is online.

Bothub currently connects with the wallet `globalMetaId` and MVC address. Please
confirm whether those are sufficient for all BotHub simplemsg order replies, or
route private-chat pushes to every known recipient alias.

### 2. Push payload is less complete than the private-chat API contract

The deployed private-chat push payload uses fields like:

```json
{
  "type": "WS_SERVER_NOTIFY_PRIVATE_CHAT",
  "from": "<sender>",
  "to": "<recipient>",
  "content": "...",
  "contentType": "...",
  "encryption": "...",
  "timestamp": 123,
  "pinId": "...",
  "txId": "..."
}
```

The documented/private-chat HTTP item shape includes `fromGlobalMetaId`,
`toGlobalMetaId`, `fromUserInfo`, `toUserInfo`, `protocol`, `chain`,
`blockHeight`, and `index`.

Bothub can normalize the current `from`/`to` shape, but incomplete payloads make
identity matching, profile hydration, decryption, and duplicate handling more
fragile. Please align the push payload with the canonical `PrivateChatItem`
shape returned by the private-chat HTTP APIs, or document the exact push-only
shape and guarantee stable aliases.

### 3. Real-time event production may not be complete

The current indexer engine starts a ZMQ loop, but the implementation is still a
placeholder:

```go
// In the full implementation, this starts per-chain ZMQ subscribers
// that receive raw transactions, parse them via the indexer,
// and route mempool pins to aggregators.
log.Printf("[indexer] ZMQ loop started (placeholder)")
```

If production relies only on block polling, private-chat "push" may happen only
after block catch-up and only if the target client remains connected to the same
metaso-p2p process at that time. This does not satisfy the user expectation
that Bot replies appear live in an open Delivery conversation.

Please confirm whether production has any real mempool/ZMQ path enabled outside
this code, and if not, implement or document the expected latency semantics.

## Expected Behavior

When a provider/Bot sends a private simplemsg reply to a buyer:

1. Meta-socket indexes or observes the new simplemsg.
2. The private-chat HTTP history endpoint can return the new row for the buyer
   and provider pair.
3. If the buyer has an active Socket.IO connection under any canonical wallet
   identity or known recipient alias, metaso-p2p emits one
   `message` event with `M = "WS_SERVER_NOTIFY_PRIVATE_CHAT"`.
4. The event payload is enough for Bothub to normalize it as a private-chat
   item, determine that it is addressed to the buyer, and merge it into the
   current Delivery conversation.

## Suggested Backend Validation

Use a controlled pair of identities and one real or synthetic simplemsg pin:

1. Connect a Socket.IO client with the buyer `globalMetaId`.
2. Connect another Socket.IO client with the buyer MVC address.
3. Send or inject a private simplemsg to the buyer.
4. Verify `/api/private-chat/messages?metaId=<buyer>&otherMetaId=<provider>`
   returns the new row.
5. Verify at least one connected buyer socket receives:

```json
{
  "M": "WS_SERVER_NOTIFY_PRIVATE_CHAT",
  "C": 0,
  "D": {
    "fromGlobalMetaId": "<provider-or-alias>",
    "toGlobalMetaId": "<buyer-or-alias>",
    "content": "...",
    "timestamp": 123,
    "pinId": "..."
  }
}
```

Repeat with the recipient encoded as each identity form metaso-p2p supports:

- buyer `globalMetaId`
- buyer MVC address
- any local `metaid` / address alias used by simplemsg `to`

## Acceptance Criteria

- A live BotHub order reply can be observed both in private-chat HTTP history
  and as a Socket.IO `WS_SERVER_NOTIFY_PRIVATE_CHAT` event without page refresh.
- Private-chat push routing reaches a buyer connected with the wallet
  `globalMetaId`; if simplemsg can target other aliases, routing also reaches
  the buyer through those aliases or documents the required connection key.
- The push payload is compatible with the canonical private-chat item shape, or
  Bothub receives a documented stable push shape with enough identity/profile
  fields to normalize and render the message.
- `METASO_P2P_BASE_URL=https://socket.metaid.io pnpm smoke:metaso-p2p` can be
  extended with a live private-chat push check and pass against production or a
  staging endpoint.

## Impact

- Bothub Delivery users do not see provider/Bot progress updates arrive live in
  the open conversation.
- Users may need refresh/history sync to discover replies, which makes Delivery
  feel incomplete even when HTTP history eventually contains the messages.
- This should be fixed in metaso-p2p rather than adding a custom Bothub backend
  or polling workaround unless push semantics are explicitly out of scope.
