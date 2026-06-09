# Bot Homepage V2 API

`GET /api/bot-homepage/globalmetaid/:globalMetaId?version=v2`

`GET /api/bot-homepage/globalmetaid/:globalMetaId?schemaVersion=botHomepage.v2`

The endpoint defaults to the existing `botHomepage.v1` response when neither
`version=v2` nor `schemaVersion=botHomepage.v2` is provided. This preserves the
current `/api/bot-homepage/globalmetaid/:globalMetaId` compatibility contract.

`botHomepage.v2` adds persona and section read models on the same route. The
`chainName` query parameter filters section reads to one chain when set. An
empty `chainName` aggregates all indexed chains.

Sections are returned in fixed homepage groups:

- `services`
- `metaapps`
- `skills`
- `buzzes`

Each section reads up to six records, returns at most five items, and exposes
overflow with `hasMore`. The `more.enabled` flag is currently always `false`;
clients should not treat it as an active pagination affordance yet.

Mempool records flow through the same read models as confirmed records and can
appear in the matching sections or services. Content section items expose
`isMempool` when the item is still unconfirmed.

Non-binary payloads are exposed on response items as `payloadText` or
`payloadJson`, depending on the indexed content type and parse result. Binary
payload bytes are not exposed in the homepage response.
