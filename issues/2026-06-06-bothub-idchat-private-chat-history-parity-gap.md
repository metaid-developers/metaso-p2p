# Bothub private-chat history differs from IDChat for AI_Sunny conversation

## Summary

Public `socket.metaid.io` can now return the latest AI_Sunny private-chat row
that Bothub was missing, including pin
`f96a3e0b725a95d52bd25e9eff26dd382d76ce71603afe69e72725fc1989f44ci0`.

However, the metaso-p2p private-chat contract is still not IDChat-compatible
for the same two conversation participants. The same message has different
participant identities, different index values, different `txId` semantics, and
the homes endpoint exposes address aliases instead of the IDChat globalMetaId
peer. This causes BotHub/Delivery clients to split one human-bot conversation
across address aliases unless the frontend performs extra profile lookups and
canonicalization.

Please fix or explicitly document the metaso-p2p private-chat API contract so
caller-side apps can hydrate the same private-chat chain that IDChat displays.

## Environment

- Date checked: 2026-06-06 CST
- Public metaso-p2p base URL: `https://socket.metaid.io`
- Public IDChat chat API: `https://api.idchat.io/chat-api`
- Buyer / self globalMetaId:
  `idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0`
- IDChat AI_Sunny peer globalMetaId:
  `idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz`
- AI_Sunny address alias exposed by metaso-p2p homes/profile:
  `1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za`
- Buyer historical address alias observed in metaso-p2p history:
  `12ghVWG1yAgNjzXj4mr3qK9DgyornMUikZ`
- Target latest message pin:
  `f96a3e0b725a95d52bd25e9eff26dd382d76ce71603afe69e72725fc1989f44ci0`

## Reproduction

Compare the same pair through metaso-p2p and IDChat:

```bash
A='idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0'
B='idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz'
P='1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za'

curl "https://socket.metaid.io/api/private-chat/messages?metaId=$A&otherMetaId=$B&cursor=&timestamp=0&size=100"
curl "https://socket.metaid.io/api/private-chat/messages/by-index?metaId=$A&otherMetaId=$B&startIndex=100&size=100"
curl "https://socket.metaid.io/api/private-chat/homes/$A"

curl "https://api.idchat.io/chat-api/group-chat/private-chat-list?metaId=$A&otherMetaId=$B&cursor=0&timestamp=0&size=100"
curl "https://api.idchat.io/chat-api/group-chat/private-chat-list-by-index?metaId=$A&otherMetaId=$B&startIndex=100&size=100"
```

## Evidence

### 1. Latest message is present but participant fields differ

Meta-socket latest page:

```json
{
  "pinId": "f96a3e0b725a95d52bd25e9eff26dd382d76ce71603afe69e72725fc1989f44ci0",
  "txId": "f96a3e0b725a95d52bd25e9eff26dd382d76ce71603afe69e72725fc1989f44ci0",
  "index": 4,
  "timestamp": 1780684799,
  "fromGlobalMetaId": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
  "from": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
  "fromAddress": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
  "toGlobalMetaId": "idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0"
}
```

IDChat latest page for the same pin:

```json
{
  "pinId": "f96a3e0b725a95d52bd25e9eff26dd382d76ce71603afe69e72725fc1989f44ci0",
  "txId": "f96a3e0b725a95d52bd25e9eff26dd382d76ce71603afe69e72725fc1989f44c",
  "index": 150,
  "timestamp": 1780684297,
  "fromGlobalMetaId": "idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz",
  "from": "2eb21238314aca030b67ed7b7c4c613f2e8cb7e42ee9140589a4df9da3854aa2",
  "toGlobalMetaId": "idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0"
}
```

### 2. The index route is not IDChat-compatible

Meta-socket:

```json
{
  "route": "/api/private-chat/messages/by-index",
  "startIndex": 0,
  "total": 5,
  "latestIndexInPage": 4
}
```

Meta-socket with `startIndex=100`:

```json
{
  "total": 0,
  "list": []
}
```

IDChat with `startIndex=100`:

```json
{
  "total": 51,
  "latestIndexInPage": 150,
  "containsTargetPin": true
}
```

IDChat's continuous index for this conversation reaches `150`; metaso-p2p only
has five indexed rows for the same pair. A caller using index-based incremental
sync will miss most of the conversation through metaso-p2p.

### 3. Homes exposes address aliases, not the IDChat conversation peer

Meta-socket homes for the buyer contains:

```json
{
  "metaId": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
  "globalMetaId": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
  "lastMessage": {
    "pinId": "d33032077c1d595a20aef9f9c2d2798648a2d99349fa1370e5953f93b7414c9bi0",
    "fromGlobalMetaId": "1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za",
    "toGlobalMetaId": "idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0",
    "timestamp": 1780684799,
    "index": 2
  }
}
```

There is no homes row keyed by
`idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz`, even though IDChat treats that
globalMetaId as the AI_Sunny private-chat peer.

### 4. One metaso-p2p history page contains both address directions

The latest metaso-p2p page for `A` and `B` contains two dominant directions:

```json
[
  ["1GrqX7K9jdnUor8hAoAfDx99uFH2tT75Za -> idq1zfazvxaq69uw6txe3ewce30ewyhy9a7mzykgv0", 60],
  ["12ghVWG1yAgNjzXj4mr3qK9DgyornMUikZ -> idq14hmv23j5fnlx4ccnmvlyldjd38xjsechzwg9xz", 40]
]
```

The second direction represents buyer messages, but the buyer side is exposed
as address alias `12gh...` instead of buyer globalMetaId `idq1zfaz...`.
Frontend callers must perform profile lookups on both sides before they can
know that these 40 rows belong to the same `idq1zfaz <-> idq14hmv` conversation.

## Expected contract

For the same private-chat pair, metaso-p2p should either:

1. Return the same `fromGlobalMetaId`, `toGlobalMetaId`, `index`, and `txId`
   semantics as IDChat; or
2. Document a different contract and include enough canonical identity fields
   in every message and homes row so caller apps do not need to infer aliases
   through separate profile calls.

At minimum:

- `fromGlobalMetaId` and `toGlobalMetaId` should be canonical globalMetaIds
  when they are known.
- `fromAddress` and `toAddress` can carry address aliases separately.
- `/api/private-chat/homes/:metaId` should expose the canonical peer
  globalMetaId, or include both canonical peer and address alias fields.
- `/api/private-chat/messages/by-index` should preserve the same conversation
  index sequence that IDChat exposes, or it should not be advertised as an
  equivalent compatibility route.
- `txId` should not include the `i0` suffix if IDChat compatibility expects raw
  transaction ids there; keep the full pin id in `pinId`.

## Impact

Bothub has added defensive frontend canonicalization so current address-shaped
rows are less likely to split the Delivery UI. That is a workaround, not a
complete fix:

- every caller must now resolve profiles for both participants before grouping;
- index-based sync remains unsafe through metaso-p2p;
- homes still points callers at address aliases instead of the IDChat peer;
- websocket payloads may have the same alias-routing risk.

Please fix the upstream private-chat identity and index parity so BotHub and
other caller-side apps can display the same human-bot chat history as IDChat.
