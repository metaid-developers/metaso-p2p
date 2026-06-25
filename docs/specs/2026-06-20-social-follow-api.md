# Social Follow API Contract

Date: 2026-06-20
Status: Current contract for shipped social follow endpoints

## Endpoints

```text
GET /api/social/globalmetaid/:globalMetaId/following
GET /api/social/globalmetaid/:globalMetaId/followers
GET /api/social/relationship?sourceGlobalMetaId=...&targetGlobalMetaId=...
```

These endpoints expose the current confirmed follow graph read model only:

- `following`: whom the subject currently follows
- `followers`: who currently follows the subject
- `relationship`: the current bidirectional follow state between two subjects

The public input identity is `globalMetaId` only. The contract does not accept
legacy `metaId` or address as public API parameters.

## Transport Contract

All three endpoints use the native metaso-p2p envelope and always return HTTP
status `200`.

Success:

```json
{
  "code": 0,
  "message": "",
  "processingTime": 12,
  "data": {}
}
```

Error:

```json
{
  "code": 40000,
  "message": "invalid parameter"
}
```

Notes:

- `processingTime` is request processing duration in milliseconds, not a
  timestamp.
- Error responses currently return `code` and `message` only.
- Clients must branch on `code`, not on HTTP status.

## Read Model Scope

- Only confirmed `/follow` pins are materialized into this API.
- Mempool follow state is intentionally excluded.
- The backend folds create/revoke events into one current active edge per
  follower-target pair.
- These endpoints return current state, not historical follow timelines.
- These endpoints do not return total counts.

## Follow PIN Write Contract

This section is for clients that publish `/follow` pins and expect those pins to
be indexed by this read model.

### Create A Follow Edge

Publish a MetaID PIN with:

| PIN Field | Required Value |
| --- | --- |
| `path` | `/follow` |
| `operation` | `create` |
| `contentBody` | Target account reference. Prefer the target `globalMetaId`. |
| `contentSummary` | Optional fallback target account reference when `contentBody` is empty in MANAPI replay data. |
| publisher identity | The account that is following the target. |

The target account reference must be a plain string value, not a JSON object.
These forms are valid:

```text
idqTargetGlobalMetaId
"idqTargetGlobalMetaId"
targetMetaId
targetAddress
```

This form is not accepted by the current indexer:

```json
{"globalMetaId":"idqTargetGlobalMetaId"}
```

Target resolution rules:

- The preferred target reference is `globalMetaId`.
- `metaId` and address references are accepted only if they resolve through the
  indexed profile lookup.
- A target reference that already looks like a canonical `globalMetaId`
  (`idq...`) can still be indexed even if profile lookup is missing.
- An unresolved non-`globalMetaId` reference is ignored and does not create a
  follow edge.

The follower is derived from the publishing PIN identity. The indexer uses the
PIN `globalMetaId` when present, otherwise it attempts to resolve `metaId`,
`createMetaId`, `address`, or `createAddress` through the indexed profile
lookup.

Repeated create pins for the same follower-target pair replace the previous
active edge. The newest active create pin supplies `followPinId` and
`followedAt` in `view=profile` responses.

### Revoke A Follow Edge

To cancel a follow, publish a MetaID PIN with:

| PIN Field | Required Value |
| --- | --- |
| `path` | `/follow@<originalFollowPinId>` |
| `operation` | `revoke` |
| `originalId` | `<originalFollowPinId>` |
| `contentBody` | Ignored; may be empty. |

`originalId` is the preferred target pointer. The `/follow@<originalFollowPinId>`
path form is kept as a compatibility fallback when `originalId` is unavailable.

The revoke target must be the currently active follow pin ID for that
follower-target pair. Revoking an older follow pin that has already been
replaced by a newer create pin does not remove the newest active edge.

### Raw OP_RETURN Field Order

Clients that construct raw MetaID OP_RETURN data must preserve the chain parser
field order:

```text
OP_RETURN "metaid" <operation> <path> <encryption> <version> <contentType> <contentBody>
```

The social indexer currently does not branch on `contentType`; it reads the
target from `contentBody` as described above.

## Common List Query Parameters

The `following` and `followers` endpoints share the same query contract.

| Parameter | Type | Required | Default | Rules |
| --- | --- | --- | --- | --- |
| `cursor` | string | No | empty | Opaque pagination cursor. First page uses an empty or omitted cursor. |
| `size` | number | No | `20` | Must be in `1..100`. |
| `view` | string | No | `compact` | Only `compact` or `profile`. |

Cursor rules:

- `nextCursor=""` means no next page.
- Clients must treat `cursor` as opaque.
- The current implementation encodes an internal index key, so cursors are
  only valid for the matching endpoint and subject.
- If the underlying active edge disappears between page requests, an old cursor
  can become invalid and the server returns `40000`.

List ordering:

- Results are returned newest first by active follow timestamp.
- Ties are resolved deterministically by internal index order.
- Clients must not rely on any implied secondary sort semantics beyond newest
  first.

## GET /api/social/globalmetaid/:globalMetaId/following

Returns the current active outgoing follow edges for `:globalMetaId`.

### Compact View

```text
GET /api/social/globalmetaid/idq123/following
GET /api/social/globalmetaid/idq123/following?view=compact&size=20
```

Response:

```json
{
  "code": 0,
  "message": "",
  "processingTime": 9,
  "data": {
    "list": [
      {
        "globalMetaId": "idq456",
        "name": "Alice",
        "nameId": "name-alice:i0",
        "avatarId": "avatar-alice:i0"
      }
    ],
    "nextCursor": "b3BhcXVlLWN1cnNvcg==",
    "size": 20
  }
}
```

### Profile View

```text
GET /api/social/globalmetaid/idq123/following?view=profile&size=20
```

Response:

```json
{
  "code": 0,
  "message": "",
  "processingTime": 10,
  "data": {
    "list": [
      {
        "globalMetaId": "idq456",
        "name": "Alice",
        "nameId": "name-alice:i0",
        "avatarId": "avatar-alice:i0",
        "bio": "short bio",
        "bioId": "bio-alice:i0",
        "followedAt": 1760800000,
        "followPinId": "follow-alice:i0"
      }
    ],
    "nextCursor": "",
    "size": 20
  }
}
```

## GET /api/social/globalmetaid/:globalMetaId/followers

Returns the current active incoming follow edges for `:globalMetaId`.

```text
GET /api/social/globalmetaid/idq123/followers
GET /api/social/globalmetaid/idq123/followers?cursor=...&size=20&view=compact
GET /api/social/globalmetaid/idq123/followers?cursor=...&size=20&view=profile
```

The response shape is identical to `following`. The only difference is edge
direction:

- `following` lists follow targets of the subject
- `followers` lists follower accounts of the subject

## List Response Fields

### Root Fields

| Field | Type | Meaning |
| --- | --- | --- |
| `list` | array | Current page items. |
| `nextCursor` | string | Opaque cursor for the next page. Empty string means end of results. |
| `size` | number | Normalized page size for this response, not the item count actually returned. |

### Compact Item

| Field | Type | Meaning |
| --- | --- | --- |
| `globalMetaId` | string | Canonical peer globalMetaId. |
| `name` | string | Latest indexed public name, or empty string if unavailable. |
| `nameId` | string | Pin ID of the latest public name record, or empty string if unavailable. |
| `avatarId` | string | Pin ID of the latest public avatar record, or empty string if unavailable. |

### Profile Item

`view=profile` includes all compact fields plus:

| Field | Type | Meaning |
| --- | --- | --- |
| `bio` | string | Latest indexed public bio, or empty string if unavailable. |
| `bioId` | string | Pin ID of the latest public bio record, or empty string if unavailable. |
| `followedAt` | number | Unix timestamp in seconds from the current active follow pin. |
| `followPinId` | string | Pin ID of the current active follow edge. |

### List Behavior Notes

- The subject account is represented by the path parameter, so list items do
  not repeat any `subjectGlobalMetaId` field.
- List items never include reverse-relationship flags such as
  `followedByMe` or `followsMe`.
- List items never include legacy identity or profile fields such as
  `metaId`, `address`, `background`, `pdv`, `fdv`, or `followCount`.
- Asset-related fields return pin IDs only. They do not return resolved HTTP
  URLs or `/content/...` paths.
- If the peer follow edge exists but the current profile lookup is missing,
  the item is still returned and profile-derived string fields are returned as
  empty strings.
- `view=compact` intentionally omits `bio`, `bioId`, `followedAt`, and
  `followPinId`.

## GET /api/social/relationship

```text
GET /api/social/relationship?sourceGlobalMetaId=idqSource&targetGlobalMetaId=idqTarget
```

### Query Parameters

| Parameter | Type | Required | Rules |
| --- | --- | --- | --- |
| `sourceGlobalMetaId` | string | Yes | Must be a non-empty globalMetaId that resolves to an indexed subject. |
| `targetGlobalMetaId` | string | Yes | Must be a non-empty globalMetaId that resolves to an indexed subject. |

Response:

```json
{
  "code": 0,
  "message": "",
  "processingTime": 4,
  "data": {
    "source": {
      "globalMetaId": "idqSource",
      "followsTarget": true,
      "followPinId": "follow-source-target:i0",
      "followedAt": 1760800000
    },
    "target": {
      "globalMetaId": "idqTarget",
      "followsSource": false,
      "followPinId": "",
      "followedAt": 0
    },
    "mutual": false
  }
}
```

### Relationship Fields

| Field | Type | Meaning |
| --- | --- | --- |
| `source.globalMetaId` | string | Echo of the resolved source globalMetaId. |
| `source.followsTarget` | boolean | Whether source currently follows target. |
| `source.followPinId` | string | Active source-to-target follow pin ID, or empty string when absent. |
| `source.followedAt` | number | Active source-to-target follow timestamp in Unix seconds, or `0` when absent. |
| `target.globalMetaId` | string | Echo of the resolved target globalMetaId. |
| `target.followsSource` | boolean | Whether target currently follows source. |
| `target.followPinId` | string | Active target-to-source follow pin ID, or empty string when absent. |
| `target.followedAt` | number | Active target-to-source follow timestamp in Unix seconds, or `0` when absent. |
| `mutual` | boolean | `true` only when both follow directions are currently active. |

Notes:

- `relationship` returns both directions explicitly. Clients do not need to
  call the endpoint twice.
- Absence of a relation is represented by `false`, empty string, and `0`, not
  by `null`.

## Error Contract

Business errors are:

| Code | Message | Meaning |
| --- | --- | --- |
| `40000` | `invalid parameter` | Invalid or missing public parameters. |
| `40400` | `subject not found` | The requested subject does not resolve to an indexed globalMetaId. |
| `50000` | `aggregation unavailable` | The read model or its dependencies are temporarily unavailable. |

`40000` includes:

- empty `:globalMetaId`
- invalid `size`
- invalid `view`
- malformed `cursor`
- cursor that belongs to another subject or endpoint
- stale cursor whose referenced edge no longer exists
- missing `sourceGlobalMetaId`
- missing `targetGlobalMetaId`

`40400` includes:

- `following` or `followers` path subjects that do not resolve
- `relationship` source or target subjects that do not resolve
- passing a legacy `metaId` or address string where a public `globalMetaId`
  is required

`50000` includes storage or profile lookup failures in the underlying
aggregation stack.

## Excluded Fields and Semantics

This contract intentionally does not expose:

- legacy `metaId` as the public API input
- `subjectGlobalMetaId` on list items
- reverse relationship flags on list items
- mempool or pending flags such as `isPending`
- resolved Web2 asset URLs
- legacy fields such as `background`, `pdv`, `fdv`, or `followCount`

Clients that need richer profile details should compose them from other
endpoints such as `/api/info/globalmetaid/:globalMetaId`.
