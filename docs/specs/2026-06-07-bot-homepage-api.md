# Bot Homepage Aggregation API

Date: 2026-06-07

## Endpoint

`GET /api/bot-homepage/globalmetaid/:globalMetaId`

Returns one render-ready Bot homepage document for OAC Bot Browser and other MetaID clients.

## Envelope

Success:

```json
{"code":0,"message":"","data":{}}
```

Business errors:

| Code | Meaning |
| --- | --- |
| `40000` | invalid parameter |
| `40400` | bot homepage not found |
| `50000` | aggregation unavailable |

Business errors use the same JSON envelope with HTTP 200. Clients should inspect `code` rather than treating HTTP 200 as application success.

`/api/info/*` keeps its meta-file-system-compatible `code=1` success convention. This endpoint uses native metaso-p2p `code=0`.

## Query

| Parameter | Default | Description |
| --- | --- | --- |
| `includeServices` | `true` | Include provider skill services. |
| `serviceSize` | `20` | Service count cap, maximum `100`. |
| `includeInactiveServices` | `false` | Include revoked, disabled, or status-abnormal services. |
| `includeProofs` | `true` | Include proof summaries when indexed. |
| `includePresence` | `true` | Include online state when a presence reader can answer. |
| `chainName` | empty | Optional service chain filter. |

## Data Contract

`data.schemaVersion` is `botHomepage.v1`.

Required top-level fields:

- `schemaVersion`
- `resolvedAt`
- `globalMetaId`
- `canonical`
- `profile`
- `homepage`
- `presence`
- `services`
- `actions`
- `proofs`
- `source`
- `warnings`

## Service Rules

Services reuse `/api/bot-hub/skill-service/list` semantics with:

- `providerGlobalMetaId=<canonical.globalMetaId>`
- `sortBy=updated`
- `order=desc`
- `size=<serviceSize>`
- `includeInactive=<includeInactiveServices>`
- `chainName=<chainName>`

The endpoint does not return subjective fields such as `available`, `canOrder`, `disabledReason`, or `availableReason`.

## Proof Rules

The endpoint emits known `pinId` and `protocolPath` values. It does not fabricate `txid` or `contentHash`. Missing proof metadata returns `proofs.verificationState="partial"` or `"unverified"` and adds warnings.

## Presence Rules

Presence is a hint. If local or federated presence cannot answer confidently, the endpoint returns:

```json
{"state":"unknown","updatedAt":null,"source":""}
```

Presence failure does not fail the homepage response.
