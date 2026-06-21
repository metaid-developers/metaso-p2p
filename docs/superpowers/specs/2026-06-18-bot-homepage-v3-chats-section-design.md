# Bot Homepage v3 Chats Section Design

Date: 2026-06-18
Status: Approved design for implementation planning
Target repo: `metaso-p2p`

## Goal

Extend the `botHomepage.v3` response for:

```text
GET /api/bot-homepage/globalmetaid/:globalMetaId?version=v3
```

with a new homepage section named `chats`. The section represents recent
Bot-to-Bot interaction activity. The first implementation only reads outgoing
`/protocols/simplemsg` records sent by the requested Bot.

## Non-Goals

- Do not change v1 or v2 homepage responses.
- Do not expose full private chat message payloads in `botHomepage.v3`.
- Do not include incoming simplemsg records.
- Do not include `/follow` or other interaction sources in this round.
- Do not add public pagination controls or a separate chat-history endpoint.
- Do not expose private-chat storage keys, conversation indexes, encryption
  metadata, content, content type, txid, chain name, address, or publisher
  identity in the `chats` section.

## Section Order

When all v3 sections are enabled, `sections` must be returned in this fixed
order:

```text
services -> metaapps -> chats -> buzzes
```

The existing section inclusion flags keep their current behavior for
`services`, `metaapps`, and `buzzes`. The `chats` section is part of the default
v3 first-screen section set when `includeSections=true`.

## Section Shape

The `chats` section uses the same v3 section envelope as other sections:

```json
{
  "id": "chats",
  "protocolPath": "/protocols/simplemsg",
  "page": {
    "limit": 5,
    "count": 1,
    "hasMore": false
  },
  "items": []
}
```

The backend may read six matching records internally to compute `hasMore`, but
it must return at most five items.

## Item Shape

Each `chats` item keeps the common v3 item envelope:

```json
{
  "pinId": "8bc2...i0",
  "protocolPath": "/protocols/simplemsg",
  "timestamp": 1781258875,
  "data": {
    "interactWith": {
      "globalMetaId": "idq...",
      "name": "Peer Bot",
      "avatarId": "0123...cdefi0"
    }
  }
}
```

`data.interactWith` is the normalized interaction target for this row. It is a
flat object: `globalMetaId` is sourced from the simplemsg payload's `to` field
as stored by the private-chat aggregator, while `name` and `avatarId` come from
indexed profile data when available. `data` must not repeat `protocolPath` or
`timestamp` because those values already exist on the item envelope.

## Source Rules

For this round, a matching chat interaction is:

- a successfully indexed simplemsg whose effective protocol path is
  `/protocols/simplemsg`;
- sent by the requested Bot, not received by it;
- carrying a non-empty target in the simplemsg `to` field;
- associated with one of the requested Bot's known identity aliases, so records
  stored under legacy MetaID, globalMetaId, or address can still be found.

Ordering is descending by `timestamp`. Ties should be deterministic by pin id or
the existing private-chat dedupe key.

## Module Boundary

`bothomepage` should not scan private-chat Pebble keys directly. Add a narrow
homepage-facing reader interface to `bothomepage`, and have `privatechat`
implement it.

The interface should return already-normalized outgoing interaction summaries,
not private-chat API response objects. This keeps the homepage contract stable
when later sources such as `/follow` are added to `chats`.

## Failure Behavior

If the chat interaction source is unavailable, v3 should still return a
successful homepage response with an empty `chats` section and append:

```text
chats section source unavailable
```

If there are no outgoing simplemsg interactions, return an empty `chats`
section without a warning.

## Verification

Implementation should cover:

- v3 section order is exactly `services`, `metaapps`, `chats`, `buzzes`.
- `chats` only includes outgoing simplemsg interactions for the requested Bot.
- `chats` returns at most five items in descending timestamp order and sets
  `hasMore` when a sixth record exists.
- `chats.items[i].data` only contains `interactWith`; that object only contains
  `globalMetaId`, optional `name`, and optional `avatarId`.
- a missing or failing chat interaction reader does not fail the homepage.
- the full router wiring connects `privatechat` to `bothomepage`.
