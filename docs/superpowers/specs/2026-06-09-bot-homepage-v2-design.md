# Bot Homepage v2 API Design

Date: 2026-06-09
Status: Product/API design for implementation planning
Target repo: `metaso-p2p`

## Context

`metaso-p2p` already exposes:

```text
GET /api/bot-homepage/globalmetaid/:globalMetaId
```

The current response is `botHomepage.v1`. It is enough for an early Bot Page prototype, but it is not enough for the next Agent Browser Core baseline.

Observed gaps in the live response:

- `profile.bio` may contain legacy JSON instead of a public introduction.
- `homepage.summary` may repeat the same legacy JSON string.
- `role`, `soul`, `goal`, `chatSkills`, and `LLM` are not returned as structured fields.
- The homepage only includes skill services.
- The homepage does not include this Bot's published MetaAPPs, published Bot skills, or recent Buzz posts.
- Proofs cover only the current v1 profile/service fields.
- The response does not yet map cleanly into Agent Browser Core's section-based resource envelope.

The product goal is to make MetaSO return the correct Bot public read model before Agent Browser Core consumes it.

## Decision

Add a grey-release v2 response for the existing Bot Homepage endpoint.

The default response remains `botHomepage.v1` until Agent Browser Core is ready.

Clients request v2 explicitly:

```text
GET /api/bot-homepage/globalmetaid/:globalMetaId?version=v2
```

The API may also accept this alias:

```text
GET /api/bot-homepage/globalmetaid/:globalMetaId?schemaVersion=botHomepage.v2
```

Both request forms return:

```json
{
  "code": 0,
  "message": "",
  "data": {
    "schemaVersion": "botHomepage.v2"
  }
}
```

## Non-Goals

- Do not change the default response from v1 to v2 in this milestone.
- Do not remove or break v1 fields.
- Do not add paginated "more" endpoints in this milestone.
- Do not include private chat history, orders, service-call traces, or private debug traces in the homepage response.
- Do not expose subjective action fields such as `canOrder`, `availableReason`, or `disabledReason`.
- Do not make Bot Homepage depend on Agent Browser Core code.

## Endpoint

```text
GET /api/bot-homepage/globalmetaid/:globalMetaId?version=v2
```

Business error envelope stays unchanged:

| Code | Meaning |
| --- | --- |
| `40000` | invalid parameter |
| `40400` | bot homepage not found |
| `50000` | aggregation unavailable |

Errors use HTTP 200 with the JSON business envelope, same as v1.

## Query Parameters

| Parameter | Default | v2 behavior |
| --- | --- | --- |
| `version` | `v1` | `v2` enables `botHomepage.v2`. |
| `schemaVersion` | empty | `botHomepage.v2` is accepted as an alias for `version=v2`. |
| `includeProofs` | `true` | Include proof summaries when indexed. |
| `includePresence` | `true` | Include online state when a presence reader can answer. |
| `includeSections` | `true` | Include homepage content sections. |
| `includeServices` | `true` | Include the services section. |
| `includeMetaApps` | `true` | Include the MetaAPPs section. |
| `includeSkills` | `true` | Include the Bot skills section. |
| `includeBuzzes` | `true` | Include the recent Buzzes section. |
| `includeInactiveServices` | `false` | Include revoked, disabled, or status-abnormal services when services are requested. |
| `chainName` | empty | Empty means aggregate across all supported chains. Non-empty filters every section to that chain where the section source supports chain filtering. |

The v2 homepage returns at most five items per homepage section. This is a fixed first-screen product rule. v2 should not expose `sectionSize`, `serviceSize`, `cursor`, or pagination fields as public query controls.

To compute `hasMore`, the backend may internally read six items per section, return the latest five, and set `hasMore=true` when a sixth item exists.

When `chainName` is empty, section sources must not pick one default chain. They should aggregate matching current records across all indexed chains and include each item's actual `chainName`.

## Top-Level Data Contract

`data.schemaVersion` must be:

```json
"botHomepage.v2"
```

Required top-level fields:

```json
{
  "schemaVersion": "botHomepage.v2",
  "resolvedAt": 1780986690050,
  "globalMetaId": "idq...",
  "canonical": {},
  "profile": {},
  "persona": {},
  "homepage": {},
  "presence": {},
  "sections": [],
  "actions": [],
  "proofs": {},
  "source": {},
  "warnings": []
}
```

## Identity And Profile

`canonical` keeps the canonical identity used to aggregate the page:

```json
{
  "globalMetaId": "idq...",
  "metaid": "1...",
  "address": "1...",
  "chainName": "mvc"
}
```

`profile` is only public identity and display data:

```json
{
  "name": "AI_Sunny",
  "avatar": "https://...",
  "avatarPinId": "...",
  "background": "",
  "backgroundPinId": "",
  "bio": "Public introduction text.",
  "bioPinId": "...",
  "chatPubkey": "...",
  "chatPubkeyPinId": "...",
  "nftAvatar": "",
  "displayGlobalMetaId": "idq14hmv...zwg9xz"
}
```

Rules:

- `profile.bio` is public introduction text only.
- `profile.bio` must not contain raw legacy JSON.
- If `/info/bio` is plain text, use it as `profile.bio`.
- If `/info/bio` is legacy JSON, parse it for compatibility and move behavior fields into `persona`.
- If only legacy JSON exists and there is no public bio field, return `profile.bio=""` and add a warning.
- `homepage.summary` may fall back to `persona.role` or `persona.goal` for display, but `profile.bio` must remain public bio only.

## Persona And Runtime Hints

`persona` describes the Bot's public behavior configuration. It is displayable and useful for other Bots, but it is separate from public profile identity.

```json
{
  "role": "I am your primary digital twin.",
  "rolePinId": "...",
  "soul": "Friendly and professional.",
  "soulPinId": "...",
  "goal": "Help users accomplish their tasks effectively.",
  "goalPinId": "...",
  "chatSkills": {
    "allowChatSkills": ["metabot-post-buzz", "weather", "metaid-master-wiki"],
    "pinId": "..."
  },
  "llm": {
    "primaryProvider": "deepseek",
    "fallbackProvider": null,
    "displayName": "DeepSeek",
    "pinId": "..."
  },
  "legacyBioParsed": true
}
```

Path mapping:

| Field | Preferred path |
| --- | --- |
| `persona.role` | `/info/role` |
| `persona.soul` | `/info/soul` |
| `persona.goal` | `/info/goal` |
| `persona.chatSkills` | `/info/chatSkills` |
| `persona.llm` | `/info/LLM` |

Compatibility mapping from legacy `/info/bio` JSON:

| Legacy JSON field | v2 field |
| --- | --- |
| `role` | `persona.role` |
| `soul` | `persona.soul` |
| `goal` | `persona.goal` |
| `llm` | `persona.llm.primaryProvider` |
| `allowChatSkills` | `persona.chatSkills.allowChatSkills` |
| `skills` | `persona.chatSkills.legacySkills` for debug compatibility |
| `tools` | `persona.chatSkills.legacyTools` for debug compatibility |

Preferred `/info/*` path values override legacy `/info/bio` JSON values when both exist.

## Homepage

`homepage` describes the selected homepage renderer source:

```json
{
  "mode": "default",
  "title": "AI_Sunny",
  "summary": "Public introduction or useful display fallback.",
  "custom": null
}
```

Rules:

- `mode="default"` when there is no custom homepage.
- `mode="custom"` when `/info/homepage` contains a non-empty JSON object.
- `homepage.custom` must return that `/info/homepage` JSON object as-is. The
  backend must not interpret, normalize, or inject fields into this object.
- `homepage.summary` should use the first non-empty value among:
  - `profile.bio`
  - `persona.role`
  - `persona.goal`
  - empty string
- `homepage.summary` must not contain raw legacy JSON.

Custom homepage shape:

```json
{
  "mode": "custom",
  "title": "AI_Sunny",
  "summary": "Public introduction or useful display fallback.",
  "custom": {
    "uri": "metaapp://...",
    "contentType": "application/vnd.metaapp",
    "renderer": "metaapp",
    "anyFutureField": "preserved"
  }
}
```

## Sections

`sections` is the canonical v2 content model. Agent Browser Core should render Bot Homepage content from `sections`.

The section data sources must be indexed read models, not request-time scans over raw chain data. The expected high-read path is:

1. Index confirmed and mempool PINs as they are observed.
2. Fold protocol version operations into a current logical record.
3. Read at most six matching records per section by canonical identity and optional chain filter.
4. Render five records and `hasMore`.

Mempool records are included in the same section results as confirmed records. The homepage API must not label them as "unconfirmed content" or otherwise change the display text because of confirmation state.

Each section has this shape:

```json
{
  "id": "services",
  "title": "Skill Services",
  "kind": "services",
  "items": [],
  "limit": 5,
  "returned": 5,
  "hasMore": true,
  "more": {
    "label": "More",
    "enabled": false
  }
}
```

Rules:

- Each section returns the latest five items at most.
- `limit` is always `5`.
- `returned` is the number of returned items.
- `hasMore=true` means more than five matching items exist.
- `more.enabled=false` in this milestone because no more endpoint is defined yet.
- The frontend may render a More button based on `hasMore`, but it should not call a v2 "more" API because no such API exists in this milestone.
- If a section source is temporarily unavailable, return an empty section and add a warning instead of failing the whole homepage response.

### Common Section Item

Each item should include common fields:

```json
{
  "id": "...",
  "pinId": "...",
  "title": "...",
  "summary": "...",
  "icon": "",
  "uri": "",
  "protocolPath": "/protocols/...",
  "chainName": "mvc",
  "createdAt": 0,
  "updatedAt": 0,
  "proof": {}
}
```

Rules:

- `id` should be stable. Prefer current pin id or source id depending on protocol semantics.
- `pinId` is the current item pin id when known.
- `title` should be displayable.
- `summary` should be short display text.
- `icon` is a display asset URL when known.
- `uri` is present only when a stable Browser URI exists.
- `proof.protocolPath` must match the protocol path used to derive the item.
- Protocol-specific parsed fields may be included under `data`.

### Payload Rule

The first v2 milestone should expose the protocol payload directly when it is not binary:

- Prefer the parsed PIN `contentBody` when it is non-empty and non-binary.
- Fall back to `contentSummary` when `contentBody` is empty and `contentSummary` is non-empty.
- If the selected payload is valid JSON, return it as structured JSON under `data.payload`.
- If the selected payload is plain text, return it as a string under `data.payload`.
- If the selected payload is binary or cannot be safely displayed as text/JSON, omit `data.payload` and keep only common metadata.
- Do not fetch remote `metafile://`, attachment, zip, or app-code bodies as part of the homepage response.

## Services Section

Section id:

```text
services
```

Protocol source:

```text
/protocols/skill-service
```

Sort:

```text
updatedAt desc
```

Filter:

```text
providerGlobalMetaId=<canonical.globalMetaId>
includeInactive=<includeInactiveServices>
```

Item example:

```json
{
  "id": "67ad...",
  "pinId": "67ad...",
  "title": "查询天气服务",
  "summary": "免费查询，告诉你全世界任意地方的天气",
  "icon": "https://...",
  "protocolPath": "/protocols/skill-service",
  "chainName": "mvc",
  "createdAt": 1773514659,
  "updatedAt": 1778597600,
  "proof": {
    "pinId": "67ad...",
    "protocolPath": "/protocols/skill-service",
    "publisherGlobalMetaId": "idq..."
  },
  "data": {
    "serviceName": "weather-service",
    "providerSkill": "weather",
    "outputType": "text",
    "price": "0",
    "currency": "SPACE",
    "settlementKind": "native",
    "paymentChain": "mvc",
    "paymentAddress": "1..."
  }
}
```

Services should remain directly requestable through Browser trusted actions. The homepage response returns facts; it does not decide whether the current actor can order.

## MetaAPPs Section

Section id:

```text
metaapps
```

Protocol source:

```text
/protocols/metaapp
```

Sort:

```text
updatedAt desc, fallback createdAt desc
```

Filter:

```text
publisherGlobalMetaId=<canonical.globalMetaId>
```

Item example:

```json
{
  "id": "metaapp-pin",
  "pinId": "metaapp-pin",
  "title": "My MetaAPP",
  "summary": "A short app description.",
  "icon": "https://...",
  "uri": "metaapp://metaapp-pin",
  "protocolPath": "/protocols/metaapp",
  "chainName": "mvc",
  "createdAt": 0,
  "updatedAt": 0,
  "proof": {
    "pinId": "metaapp-pin",
    "protocolPath": "/protocols/metaapp",
    "publisherGlobalMetaId": "idq..."
  },
  "data": {
    "appName": "My MetaAPP",
    "version": "1.0.0",
    "contentType": "application/zip"
  }
}
```

If the current MetaSO index does not yet have a MetaAPP aggregation read model, implementation should add one or a narrow read model sufficient for this section.

MetaAPP create, modify, and revoke PINs should be folded before homepage reads. The item `id` should remain the source create PIN id; `pinId` should be the current effective PIN id.

## Bot Skills Section

Section id:

```text
skills
```

Protocol source:

```text
/protocols/metabot-skill
```

Sort:

```text
updatedAt desc, fallback createdAt desc
```

Filter:

```text
publisherGlobalMetaId=<canonical.globalMetaId>
```

Item example:

```json
{
  "id": "skill-pin",
  "pinId": "skill-pin",
  "title": "weather",
  "summary": "Query weather for a location.",
  "icon": "",
  "uri": "",
  "protocolPath": "/protocols/metabot-skill",
  "chainName": "mvc",
  "createdAt": 0,
  "updatedAt": 0,
  "proof": {
    "pinId": "skill-pin",
    "protocolPath": "/protocols/metabot-skill",
    "publisherGlobalMetaId": "idq..."
  },
  "data": {
    "skillName": "weather",
    "description": "Query weather for a location."
  }
}
```

This section shows skills the Bot has published on chain. It is different from `persona.chatSkills.allowChatSkills`, which describes which local skills the Bot may use during private-chat auto replies.

Bot skill create, modify, and revoke PINs should be folded before homepage reads. The item `id` should remain the source create PIN id; `pinId` should be the current effective PIN id.

## Recent Buzzes Section

Section id:

```text
buzzes
```

Protocol source:

```text
/protocols/simplebuzz
```

Sort:

```text
createdAt desc
```

Filter:

```text
publisherGlobalMetaId=<canonical.globalMetaId>
```

Item example:

```json
{
  "id": "buzz-pin",
  "pinId": "buzz-pin",
  "title": "Recent Buzz",
  "summary": "The first useful display text from the buzz body.",
  "icon": "",
  "uri": "",
  "protocolPath": "/protocols/simplebuzz",
  "chainName": "mvc",
  "createdAt": 0,
  "updatedAt": 0,
  "proof": {
    "pinId": "buzz-pin",
    "protocolPath": "/protocols/simplebuzz",
    "publisherGlobalMetaId": "idq..."
  },
  "data": {
    "content": "Full or normalized buzz text.",
    "attachments": []
  }
}
```

Rules:

- `summary` should be a short text suitable for homepage cards.
- Attachments may be included when the simplebuzz payload exposes them clearly.
- Do not fetch remote attachment bodies as part of this homepage response.
- Buzz modify operations update the displayed payload, `pinId`, and `updatedAt`, but the buzzes section remains sorted by the original create record's `createdAt desc`.
- Buzz revoke operations hide the revoked logical buzz from the default section result.

## Actions

Actions remain Browser trusted action descriptors:

```json
[
  {
    "id": "message",
    "label": "Message",
    "kind": "private-chat",
    "enabled": true,
    "requiresUsingIdentity": true
  },
  {
    "id": "services",
    "label": "Services",
    "kind": "service-list",
    "enabled": true,
    "requiresUsingIdentity": true
  },
  {
    "id": "copy-uri",
    "label": "Copy URI",
    "kind": "copy",
    "enabled": true,
    "requiresUsingIdentity": false,
    "uri": "metaid://idq..."
  }
]
```

v2 may add:

```json
{
  "id": "share",
  "label": "Share",
  "kind": "share-resource",
  "enabled": true,
  "requiresUsingIdentity": false,
  "uri": "metaid://idq..."
}
```

Owner-only actions such as edit profile, configure chat, and view messages are not returned by MetaSO. Those are host-owned owner-affinity actions supplied by OAC, IDBots, or standalone wallet hosts.

## Proofs

v2 proofs should separate profile, persona, homepage, and section proofs:

```json
{
  "verificationState": "partial",
  "identity": null,
  "profile": [],
  "persona": [],
  "homepage": null,
  "sections": {
    "services": [],
    "metaapps": [],
    "skills": [],
    "buzzes": []
  }
}
```

Profile proof paths:

| Field | Path |
| --- | --- |
| `name` | `/info/name` |
| `avatar` | `/info/avatar` |
| `background` | `/info/background` |
| `bio` | `/info/bio` |
| `chatPubkey` | `/info/chatpubkey` |

Persona proof paths:

| Field | Path |
| --- | --- |
| `role` | `/info/role` |
| `soul` | `/info/soul` |
| `goal` | `/info/goal` |
| `chatSkills` | `/info/chatSkills` |
| `llm` | `/info/LLM` |

Rules:

- Do not fabricate `txid`, `contentHash`, or explorer URLs.
- If only `pinId` and `protocolPath` are known, return those and set `verificationState="partial"`.
- Add warnings for missing proof metadata.
- Section item proofs should also be embedded in each section item for convenient rendering.

## Source

`source` should identify the resolver and the section sources used:

```json
{
  "resolver": "metaso-p2p",
  "node": "https://manapi.metaid.io",
  "profileEndpoint": "/api/info/globalmetaid/:globalMetaId",
  "homepageEndpoint": "/api/bot-homepage/globalmetaid/:globalMetaId",
  "contentBaseUrl": "https://manapi.metaid.io/content",
  "fetchedAt": 1780986690050,
  "stale": false,
  "sections": {
    "services": {
      "protocolPath": "/protocols/skill-service",
      "source": "skillservice"
    },
    "metaapps": {
      "protocolPath": "/protocols/metaapp",
      "source": "metaapp"
    },
    "skills": {
      "protocolPath": "/protocols/metabot-skill",
      "source": "metabot-skill"
    },
    "buzzes": {
      "protocolPath": "/protocols/simplebuzz",
      "source": "simplebuzz"
    }
  }
}
```

`source` is diagnostic metadata. Agent Browser Core may use it for inspector/debug views, but default Bot Page templates should not display it as primary content.

## Section Availability And Failure Rules

- Missing profile lookup still returns `40400`.
- Failure to build canonical identity still returns `50000`.
- Failure in one optional section should not fail the homepage.
- Optional section failure returns an empty section and a warning such as:

```text
metaapps section unavailable
```

- Service section failure should follow the same optional-section rule in v2 unless profile lookup itself fails.
- Presence failure remains non-fatal.
- Mempool polling failure is non-fatal for the homepage request. It should be logged by the indexing layer and reflected in source diagnostics when available, but already-indexed confirmed data should still be returned.

## Sorting And Identity Matching

All sections must be scoped to the Bot's canonical identity.

Preferred matching order:

1. `pin.GlobalMetaId == canonical.globalMetaId`
2. `pin.MetaId == canonical.metaid`
3. `pin.Address == canonical.address`

When multiple identifiers are present and conflict, prefer `globalMetaId` and add a warning.

Sort rules:

| Section | Sort |
| --- | --- |
| services | `updatedAt desc`, fallback `createdAt desc` |
| metaapps | `updatedAt desc`, fallback `createdAt desc` |
| skills | `updatedAt desc`, fallback `createdAt desc` |
| buzzes | `createdAt desc` |

For buzzes, `createdAt` is the source create PIN timestamp. A later modify changes display payload and `updatedAt`, but it does not move the item to the top of the buzzes section.

## Aggregation And Backfill

MetaSO should add or extend read models so the homepage endpoint does not do heavy aggregation work per request.

Recommended read-model boundaries:

- Extend `userinfo` for `/info/role`, `/info/soul`, `/info/goal`, `/info/chatSkills`, `/info/LLM`, and `/info/homepage`.
- Keep `skillservice` as the service source, but add an efficient provider identity + updated-time read path for homepage reads.
- Add a published-content read model for `/protocols/simplebuzz`, `/protocols/metaapp`, and `/protocols/metabot-skill`.

Published-content folding rules:

- `create` creates the logical record, with `sourcePinId=currentPinId=pin.id`.
- `modify` resolves the target from `path @<pinId>` first, then `originalId`, and updates the target logical record.
- `revoke` resolves the target the same way and hides the logical record from default section results.
- Target resolution should follow MAN-p2p semantics for resolving a modify/revoke chain back to the source record.
- The read model should keep enough proof metadata to return source and current pin ids.

Historical backfill is required, but only for recent product-relevant content:

- Backfill at most the most recent two months of `/protocols/simplebuzz`, `/protocols/metaapp`, `/protocols/metabot-skill`, and the new `/info/*` persona/homepage paths.
- Use MANAPI path-list responses as the source of chain truth for backfill.
- Backfill should process both `contentBody` and `contentSummary` using the same payload rule as live indexing.

Mempool support is part of this milestone:

- The indexer must poll or otherwise receive mempool transactions from supported chains.
- Parsed mempool PINs must be routed through `Registry.RouteMempoolPin`.
- Aggregators that support homepage sources must handle mempool PINs through the same folding path as confirmed PINs.
- Confirmation should be idempotent: when a mempool PIN later appears in a block, the read model should update/confirm the same logical record rather than create duplicate homepage items.

## Backward Compatibility

- Default endpoint behavior remains v1.
- `version=v2` returns v2.
- v1 clients should not receive v2 by accident.
- v2 clients should ignore top-level v1-only fields if the implementation keeps transitional aliases.
- v2 response should not rely on OAC-specific owner actions.

## Acceptance Criteria

- `GET /api/bot-homepage/globalmetaid/:globalMetaId` still returns `botHomepage.v1`.
- `GET /api/bot-homepage/globalmetaid/:globalMetaId?version=v2` returns `botHomepage.v2`.
- v2 `profile.bio` never returns raw legacy JSON.
- v2 parses legacy `/info/bio` JSON into `persona` when preferred `/info/*` paths are missing.
- v2 preferred `/info/role`, `/info/soul`, `/info/goal`, `/info/chatSkills`, and `/info/LLM` override legacy `/info/bio` JSON.
- v2 has `sections` with `services`, `metaapps`, `skills`, and `buzzes` when `includeSections=true`.
- `chainName=""` aggregates matching section items across all indexed chains.
- Each section returns at most five items.
- Each section reports `limit=5`, `returned`, `hasMore`, and `more.enabled=false`.
- Section items include direct non-binary payload output under `data.payload` when available.
- MetaAPP, Bot skill, and Buzz create/modify/revoke operations are folded before section reads.
- Buzz modify updates displayed content but does not move the item ahead of newer source-created buzzes.
- Mempool PINs are included in section read models without "unconfirmed content" display text.
- The implementation includes recent two-month MANAPI backfill for the v2 content and persona paths.
- No pagination or more endpoint is added in this milestone.
- Optional section failures do not fail the whole homepage response.
- Proofs include profile, persona, homepage, and section proof summaries when indexed.
- Existing v1 tests continue to pass.
