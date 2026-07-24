# SimpleMsg delivery contract

This document defines the observable contract for `/private/chat/simplemsg`
consumers. It applies to the IDChat-compatible history and Socket.IO routes.

## Delivery and confirmation

- The indexer routes a simplemsg as soon as the transaction appears in the
  configured chain node's mempool. The default mempool polling interval is 10
  seconds.
- An incrementally fetched transaction is marked as seen only after PIN parsing
  succeeds. A transient fetch or parse failure is retried on the next poll
  instead of being suppressed by the mempool deduplication TTL.
- The pending message is immediately visible through
  `private-chat-list-by-index` and is pushed to connected recipients with
  `confirmed: false` and `blockHeight: 0`.
- When the pin is mined, the stored row is upgraded in place to
  `confirmed: true` with its positive `blockHeight`. The original delivery
  timestamp and conversation position are preserved.
- Confirmation and duplicate mempool observations do not generate a second
  socket delivery for the same `pinId`. A message first observed in a block is
  still stored and pushed once.

Mempool delivery depends on the transaction reaching the mempool of a chain
node configured for this metaso-p2p instance. It is not a promise about every
network node seeing the transaction simultaneously.

## Identity fields

`fromGlobalMetaId` and `toGlobalMetaId` contain canonical global MetaIDs.
Chain addresses remain available only in `fromAddress` and `toAddress`.
metaso-p2p resolves profiles first and can deterministically derive a global
MetaID from a valid MVC address. If an identity is still unresolved, such as a
bare 64-hex MetaID with no address/profile mapping, the message is omitted from
recipient delivery and history rather than emitting a malformed global MetaID.

When the sender profile is available, `fromUserInfo` includes
`globalMetaId`, `metaid`, `address`, and `chatPublicKey`. Consumers may use the
key directly for decryption.

## Conversation cursor

`private-chat-list-by-index` returns a deterministic chronological projection
of the conversation. `index` is unique and continuous across both directions:
`0, 1, 2, ...`. Clients request the next page with the last returned index plus
one. `nextTimestamp` remains the last returned index for compatibility, while
`nextCursor` contains the next start index when another page exists. `total`
is the complete conversation message count, not the current page length.

## Recipient socket fan-out

One recipient identity may own multiple concurrent connections. Each
`WS_SERVER_NOTIFY_PRIVATE_CHAT` envelope is emitted on the raw Socket.IO
`message` event to every tracked socket for every matching recipient identity
alias. Delivery is multi-device broadcast, not competing-consumer or
single-socket delivery. The configured per-type device limits still apply.

## Presence pagination

`socket/online-users` treats numeric `cursor` as a zero-based identity offset.
Rows are sorted by global MetaID and group concurrent connections into one row
with `deviceCount`. `total` is the full identity count and `nextCursor` is the
next offset when more rows exist. `onlineWindowSeconds` is backed by
`socket.heartbeatTimeout`, which defaults to 90 seconds and can be overridden
with `METASO_P2P_SOCKET_HEARTBEAT_TIMEOUT`.
