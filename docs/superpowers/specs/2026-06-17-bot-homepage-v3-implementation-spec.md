# Bot Homepage v3 Implementation Spec

Date: 2026-06-17
Status: Internal development spec for implementation planning
Target repo: `metaso-p2p`

## Goal

Add an explicit `botHomepage.v3` response to:

```text
GET /api/bot-homepage/globalmetaid/:globalMetaId
```

v3 is a clean first-screen Bot homepage document. It removes the v2 debug and
compatibility-heavy fields while preserving enough provenance for clients to
render and identify chain-backed data.

The public API contract is:

```text
docs/specs/2026-06-17-bot-homepage-v3-api.md
```

## Non-Goals

- Do not change default v1 behavior.
- Do not break `botHomepage.v2`.
- Do not add pagination or "more" endpoints.
- Do not return Web2 resolver/source information in v3 `data`.
- Do not return top-level `proofs`, `source`, `actions`, or `services`.
- Do not include a v3 `skills` section.
- Do not expose chain-specific fields such as `chainName`, address, or
  publisher identity in v3.
- Do not implement a separate full proof or detail endpoint in this task.

## Request Selection

Extend the Bot Homepage query parser so v3 is selected by either:

```text
version=v3
schemaVersion=botHomepage.v3
```

The success envelope remains the existing metaso-p2p envelope:

```json
{"code":0,"message":"","processingTime":1781620466182,"data":{}}
```

The v3 contract applies to `data`.

## Data Model

Add v3-specific response types rather than reusing the v2 `Data` shape. The
target `data` shape is:

```json
{
  "schemaVersion": "botHomepage.v3",
  "identity": {},
  "profile": {},
  "presence": {},
  "sections": [],
  "warnings": []
}
```

Forbidden v3 top-level keys:

- `globalMetaId`
- `canonical`
- `persona`
- `homepage`
- `services`
- `actions`
- `proofs`
- `source`
- `resolvedAt`

`persona` and `homepage` live under `profile`.

## Bot Info Sources

Use the Bot Info protocol in:

```text
/Users/tusm/Documents/MetaID_Projects/open-agent-connect/docs/metaid_protocols/06-bot-info.md
```

The v3 profile sources are:

| v3 field | Source path | Output behavior |
| --- | --- | --- |
| `profile.name` | `/info/name` | UTF-8 string. |
| `profile.bio` | `/info/bio` | UTF-8 string. |
| `profile.chatPubkey` | `/info/chatpubkey` | UTF-8 public chat key string. |
| `profile.avatar` | `/info/avatar` | `{pinId, contentType}` or `null`; do not return content URLs. |
| `profile.llm` | `/info/llm` | `{pinId, payload}` or `null`; payload is raw JSON object. |
| `profile.persona` | `/info/persona` | `{pinId, payload}` or `null`; payload is raw JSON object. |
| `profile.homepage` | `/info/homepage` | `{pinId, payload}` or `null`; payload is raw JSON object. |

Implementation notes:

- The canonical Bot Info paths are lower-case.
- Ingestion should normalize path casing for historical records.
- Existing `/info/LLM` records should still hydrate `profile.llm`, but v3 must
  not expose the legacy path casing.
- Existing v2 fields `/info/role`, `/info/soul`, `/info/goal`, and
  `/info/chatSkills` remain v2 compatibility concerns. v3 must not use them to
  synthesize `profile.persona`.
- `/info/chatSkills` is excluded from v3.
- `/info/background` is excluded from v3.
- `/info/chatpubkey` is included in v3 even though the current Bot Info
  protocol document needs a matching section. Bot Page needs it to start
  private chat without a second profile request.

## User Info Read Model Work

Extend the userinfo read model enough for v3:

- Store the latest valid `/info/persona` payload and its pin id.
- Store the latest valid `/info/llm` payload and its pin id using lower-case
  canonical semantics while accepting legacy `/info/LLM`.
- Keep existing `/info/chatpubkey` support and expose its pin id.
- Preserve or add `/info/avatar` content type so v3 can return
  `profile.avatar.contentType` without returning a URL.
- Preserve mempool behavior for `/info/*`: pending valid pins should be visible
  through the same latest-record path used by confirmed pins.

Clear semantics:

- Empty payload at a Bot Info path clears that field.
- Cleared JSON blocks return `null`.
- Invalid JSON for `llm`, `persona`, or `homepage` returns `null` and adds a
  warning.

`profile.pins` only contains scalar field source pins:

```json
{
  "name": "<info-name-pin-id>",
  "bio": "<info-bio-pin-id>",
  "chatPubkey": "<info-chatpubkey-pin-id>"
}
```

Do not duplicate the avatar, llm, persona, or homepage pin ids in
`profile.pins`; those blocks carry their own `pinId`.

## Identity Rules

v3 `identity` is:

```json
{
  "globalMetaId": "idq...",
  "legacyMetaId": "2e...",
  "display": "idq14hmv...zwg9xz"
}
```

Rules:

- `globalMetaId` is the canonical public identity.
- `legacyMetaId` is the existing profile `MetaId`.
- `display` uses the same abbreviation helper as v2.
- Do not return `chainName`, address, or publisher identity fields.

## Presence

Reuse v2 presence resolution and shape:

```json
{
  "state": "unknown",
  "updatedAt": null,
  "source": ""
}
```

Presence remains a metaso-p2p node hint. Presence lookup failures must not fail
the v3 response.

## Sections

v3 sections are fixed:

| Section ID | Source |
| --- | --- |
| `services` | `/protocols/skill-service` |
| `metaapps` | `/protocols/metaapp` |
| `chats` | `/protocols/simplemsg` |
| `buzzes` | `/protocols/simplebuzz` |

Do not include `skills`.

Each section shape:

```json
{
  "id": "services",
  "protocolPath": "/protocols/skill-service",
  "page": {
    "limit": 5,
    "count": 0,
    "hasMore": false
  },
  "items": []
}
```

Read up to six records internally, return at most five, and set `hasMore=true`
when a sixth matching record exists.

## Section Items

Every section item uses:

```json
{
  "pinId": "string",
  "protocolPath": "string",
  "timestamp": 0,
  "data": {}
}
```

Rules:

- `pinId` is the current effective data PIN.
- `timestamp` is the indexer timestamp used for homepage ordering/display.
- For modify-capable protocols, use the current effective record timestamp.
- Non-chat section items expose `data.payload`, the parsed protocol payload.
  JSON becomes an object; non-binary text becomes a string.
- Chat section items expose only `data.interactWith`.
- Do not include binary payload bytes.
- Do not return `sourcePinId`, `currentPinId`, `createdAt`, `updatedAt`,
  `chainName`, publisher identity, `proof`, `service`, `payloadJson`,
  `payloadText`, or `payloadExposed`.

### Services Source

The v3 services section should use the provider-global service list that backs
the current v2 top-level `services` output, or fix the homepage-provider read
path before using it. The v3 services section must not be empty when the same
Bot has visible services in the current provider service list.

The service item payload should be the service declaration payload fields, not
the v2 service wrapper. Do not include provider profile hydration, rating
objects, chain identity, or action verdict fields in v3.

### MetaApps Source

Use the existing `publishedcontent` read model for `/protocols/metaapp`.

Use its current effective item pin, effective sort timestamp, and parsed
payload. Do not expose hidden/revoked items by default.

### Chats Source

Use `/protocols/simplemsg` records to populate the `chats` section. The current
source is outgoing simplemsg records created by privatechat, but the v3 API
must describe them as chat interactions rather than privatechat internals.

Extraction rule:

- Include only outgoing simplemsg records for the current Bot.
- Parse the simplemsg payload as JSON.
- Set `data.interactWith` from the payload JSON `to` field.
- Skip records without a usable `to` value.

The chat item must still use the normal v3 item envelope:

```json
{
  "pinId": "string",
  "protocolPath": "/protocols/simplemsg",
  "timestamp": 0,
  "data": {
    "interactWith": "idq..."
  }
}
```

Do not expose message content, encryption metadata, the original simplemsg
payload, txid fields, address fields, chain fields, or source fields inside
chat item `data`.

### Buzzes Source

Use the existing `publishedcontent` read model for `/protocols/simplebuzz`.

Use its current effective item pin, effective sort timestamp, and parsed
payload. Do not expose hidden/revoked items by default.

## Query Parameters

v3 should keep only consumer-useful query knobs:

| Parameter | Default | Behavior |
| --- | --- | --- |
| `version` | empty | `v3` selects v3. |
| `schemaVersion` | empty | `botHomepage.v3` selects v3. |
| `includePresence` | `true` | Reuse v2 presence inclusion behavior. |
| `includeSections` | `true` | Allows callers to request profile-only v3. |
| `includeServices` | `true` | Omits services section when false. |
| `includeMetaApps` | `true` | Omits metaapps section when false. |
| `includeChats` | `true` | Omits chats section when false. |
| `includeBuzzes` | `true` | Omits buzzes section when false. |
| `includeInactiveServices` | `false` | Matches existing service visibility behavior. |

Do not document or depend on `chainName` for v3. v3 should weaken chain-specific
surface area.

## Warnings

`warnings` contains non-fatal response issues:

- invalid JSON in `/info/llm`
- invalid JSON in `/info/persona`
- invalid JSON in `/info/homepage`
- optional section source unavailable

Do not include deployment URLs, local filesystem paths, or endpoint names in
warnings unless a client genuinely needs that detail.

## Required Tests

Add focused tests before implementation is considered complete:

1. Query parsing selects v3 for `version=v3` and
   `schemaVersion=botHomepage.v3`.
2. Default route still returns v1 and explicit v2 still returns v2.
3. v3 top-level keys match the contract and forbidden v2 keys are absent.
4. `identity` returns `globalMetaId`, `legacyMetaId`, and `display` only.
5. `profile.llm`, `profile.persona`, and `profile.homepage` return raw JSON
   payload blocks with `pinId`.
6. Invalid or cleared JSON Bot Info blocks return `null` and add warnings.
7. v3 includes `profile.chatPubkey` and `profile.pins.chatPubkey`.
8. v3 excludes `chatSkills` and `background`.
9. Presence shape and failure behavior match v2.
10. Sections are returned in the documented order when all are enabled:
    `services`
    `metaapps`
    `chats`
    `buzzes`
11. Each section item only includes `pinId`, `protocolPath`, `timestamp`, and
    `data`.
12. Chat item `data` includes only `interactWith` and excludes message content,
    encryption metadata, original simplemsg payload, txid, address, chain, and
    source fields.
13. Services section uses the same visible services that v2 top-level services
    can see for the provider.
14. Mempool `/info/persona`, `/info/llm`, `/info/homepage`, service, MetaApp,
    chat, and buzz records are visible through v3 when the underlying read
    model marks them current.

Recommended verification commands:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage ./internal/aggregator/userinfo ./internal/aggregator/skillservice ./internal/aggregator/publishedcontent ./internal/api -count=1
git diff --check
```

Use broader `CGO_ENABLED=0 go test ./...` before merge or deploy.

## Acceptance Checklist

- `GET /api/bot-homepage/globalmetaid/:id?version=v3` returns
  `schemaVersion="botHomepage.v3"`.
- v1 and v2 compatibility are unchanged.
- v3 data contains no top-level `proofs`, `source`, `actions`, or `services`.
- v3 data contains no `chainName`, address, `sourcePinId`, `currentPinId`,
  `createdAt`, or `updatedAt`.
- `profile.persona.payload`, `profile.llm.payload`, and
  `profile.homepage.payload` are raw chain JSON objects when present.
- Section items are minimal and self-describing.
- Section order matches the public contract.
- The `chats` section exposes only interaction counterpart identity.
- The public contract doc and implementation tests agree on field names.
