# Social Follow APIs Design

Date: 2026-06-20
Status: Approved design for implementation planning
Target repo: `metaso-p2p`

## Goal

Add a new public social read-model surface for follow relationships using the
repo's current API conventions rather than the legacy MetaSo contract.

The new API set is:

```text
GET /api/social/globalmetaid/:globalMetaId/following
GET /api/social/globalmetaid/:globalMetaId/followers
GET /api/social/relationship?sourceGlobalMetaId=...&targetGlobalMetaId=...
```

These endpoints expose:

- the accounts a subject currently follows;
- the accounts currently following a subject;
- the current bidirectional follow state between two subjects.

## Non-Goals

- Do not preserve the legacy `metaid` path/query contract.
- Do not reintroduce legacy response fields such as `followCount`,
  `background`, `pdv`, `fdv`, or `address`.
- Do not return Web2 asset URLs such as `/content/<pinId>` or full HTTP URLs.
- Do not include mempool-only or pending-state flags in the public contract.
- Do not add reverse-relationship flags to each list item.
- Do not add total-count semantics to the paginated list endpoints.
- Do not implement block, mute, subscribe, or other social relations in this
  round.

## API Surface

### Following List

```text
GET /api/social/globalmetaid/:globalMetaId/following
```

Returns the current confirmed outgoing follow relations for the subject
identified by `:globalMetaId`.

### Followers List

```text
GET /api/social/globalmetaid/:globalMetaId/followers
```

Returns the current confirmed incoming follow relations for the subject
identified by `:globalMetaId`.

### Relationship

```text
GET /api/social/relationship?sourceGlobalMetaId=...&targetGlobalMetaId=...
```

Returns the current bidirectional follow relationship between `source` and
`target`.

## Identity Rules

- Public API identity input is `globalMetaId` only.
- Public paths must use the same `globalmetaid` segment style already used by
  `/api/info/globalmetaid/:globalMetaId` and
  `/api/bot-homepage/globalmetaid/:globalMetaId`.
- Internal storage may still resolve aliases through userinfo data, legacy
  MetaID, or address mappings, but those identities are not exposed in the new
  response contract.

## Query Parameters

### List Endpoints

```text
cursor=<opaque cursor>
size=<1..100>
view=compact|profile
```

Rules:

- `cursor` omitted or empty means the first page.
- `size` defaults to `20`.
- `size` must be within `1..100`.
- `view` defaults to `compact`.
- `view` only accepts `compact` or `profile`.

### Relationship Endpoint

```text
sourceGlobalMetaId=<required>
targetGlobalMetaId=<required>
```

Rules:

- both query parameters are required;
- empty or whitespace-only values are invalid.

## Response Envelope

All three endpoints use the native metaso-p2p success envelope:

```json
{
  "code": 0,
  "data": {},
  "message": "",
  "processingTime": 12
}
```

`processingTime` is the request processing duration in milliseconds. It is not
a Unix timestamp and must not reuse the current timestamp-shaped helper
behavior.

## List Response Shape

### Compact View

```json
{
  "code": 0,
  "data": {
    "list": [
      {
        "globalMetaId": "idq456...",
        "name": "Alice",
        "nameId": "pin_name_xxx",
        "avatarId": "pin_avatar_xxx"
      }
    ],
    "nextCursor": "opaque_cursor",
    "size": 20
  },
  "message": "",
  "processingTime": 12
}
```

### Profile View

```json
{
  "code": 0,
  "data": {
    "list": [
      {
        "globalMetaId": "idq456...",
        "name": "Alice",
        "nameId": "pin_name_xxx",
        "avatarId": "pin_avatar_xxx",
        "bio": "short bio",
        "bioId": "pin_bio_xxx",
        "followedAt": 1760800000,
        "followPinId": "pin_follow_xxx"
      }
    ],
    "nextCursor": "opaque_cursor",
    "size": 20
  },
  "message": "",
  "processingTime": 12
}
```

## List Field Semantics

Each list item always exposes:

- `globalMetaId`
- `name`
- `nameId`
- `avatarId`

`view=profile` additionally exposes:

- `bio`
- `bioId`
- `followedAt`
- `followPinId`

The contract intentionally excludes:

- `metaId`
- `address`
- `followCount`
- `background`
- `pdv`
- `fdv`
- reverse follow state
- asset URLs

Resource references such as avatar and name provenance are represented by pin
ids only. Clients that need binary content or resolved asset URLs must call a
different surface.

## Relationship Response Shape

```json
{
  "code": 0,
  "data": {
    "source": {
      "globalMetaId": "idq_source",
      "followsTarget": true,
      "followPinId": "pin_follow_source_to_target",
      "followedAt": 1760800000
    },
    "target": {
      "globalMetaId": "idq_target",
      "followsSource": false,
      "followPinId": "",
      "followedAt": 0
    },
    "mutual": false
  },
  "message": "",
  "processingTime": 8
}
```

Semantics:

- `source.followsTarget` means the directed relation `source -> target`.
- `target.followsSource` means the directed relation `target -> source`.
- `mutual` is `true` only when both directed relations exist.

When no directed relation exists on one side:

- the boolean field is `false`;
- `followPinId` is `""`;
- `followedAt` is `0`.

The endpoint must still return a successful response even when neither side
follows the other, as long as the request parameters are valid and the subject
identities can be resolved.

## Error Semantics

Use the repo's existing explicit error-code style for caller faults and backend
failures:

- `40000`: invalid request parameters
- `40400`: subject identity not found
- `50000`: aggregation unavailable

Examples that must return `40000`:

- missing `sourceGlobalMetaId`
- missing `targetGlobalMetaId`
- empty `:globalMetaId`
- invalid `size`
- invalid `view`
- invalid `cursor`

Examples that should return `40400`:

- the subject of a list endpoint cannot be resolved to a known profile;
- the source or target identity of the relationship endpoint cannot be
  resolved.

Examples that should return `50000`:

- Pebble read-model failure;
- internal relation index corruption;
- profile lookup dependency unavailable in a way that prevents response
  construction.

## Pagination Semantics

The list endpoints are cursor-paginated and do not return `total`.

Rules:

- `list` length is always `<= size`.
- `nextCursor == ""` means there is no next page.
- `size` echoes the effective page size used for this response.
- ordering must be deterministic and stable across pages.

The implementation may internally fetch one extra row to compute
`nextCursor`, but that extra row must not be returned in `list`.

## Ordering

Both `following` and `followers` lists are ordered descending by relation
creation time:

- primary sort: `followedAt` descending;
- tie-break: deterministic by follow pin id or equivalent stable record key.

This ordering keeps the API aligned with the current repo pattern of
newest-first cursor lists.

## Module Boundary

Do not graft these routes into `userinfo`.

Instead:

- add a dedicated `social` aggregator;
- let it own follow pin processing, relation storage, and `/api/social/*`
  routes;
- let it read lightweight profile data from `userinfo` when building compact or
  profile views.

This keeps profile aggregation and social graph aggregation separate and avoids
turning `/api/info/*` into a mixed domain surface.

## Storage and Read-Model Semantics

The implementation should model a follow relation as a current directed edge
between two canonical subjects:

- follower subject
- followed subject
- follow pin id
- follow timestamp
- active/inactive state

The public API must expose only active current relations.

Revokes or unfollows must remove the relation from public list results and
relationship booleans. Historical inactive relations are implementation detail
for index maintenance, not a public API concern in this round.

## History and Backfill

The public contract assumes read-model data can come from both:

- forward indexing of new follow pins;
- optional historical backfill of old follow pins.

If implementation starts without backfill, the API still keeps the same
contract, but returned data will only reflect relations indexed after rollout.
That rollout limitation should be treated as operational scope, not as part of
the public response shape.

## Verification

Implementation should cover:

- route registration for all three endpoints under `/api/social/*`;
- `globalmetaid` path form exactly matches the approved contract;
- invalid `size`, `view`, and `cursor` return `40000`;
- unresolved subjects return `40400`;
- `processingTime` reports elapsed milliseconds, not a timestamp;
- compact view only returns the approved compact fields;
- profile view adds only the approved extra fields;
- list responses never expose legacy `metaId`, `address`, `background`,
  `followCount`, `pdv`, `fdv`, or asset URLs;
- relationship responses correctly distinguish `source -> target`,
  `target -> source`, and `mutual`;
- no-relation responses still return `code=0` with false booleans and empty
  relation metadata;
- pagination is deterministic and newest-first.
