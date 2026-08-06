# Social Content Aggregation and Query Design

Date: 2026-08-06
Status: Proposed design for Stage 1 and Stage 2 implementation

## 1. Purpose

MetaSo currently indexes generic MetaID PINs, user identity data, follow
relationships, and user-published simplebuzz content for Bot Homepage sections.
It does not yet expose a global social read model that joins posts with likes,
comments, or donations.

This specification defines the first two implementation stages for an on-chain
social content aggregation layer. The result is intended to support broad,
structured retrieval for an Agent host. Natural-language interpretation and
final candidate selection remain outside MetaSo.

Example user intents and their structured equivalents are:

| User intent | Structured query |
| --- | --- |
| Posts from Alice in the last two days | `publisher=Alice&since=<unix>&sort=newest` |
| The latest posts today | `since=<today>&sort=newest` |
| The hottest posts | `sort=hot` |
| Posts about MVC in the last week | `keyword=MVC&since=<unix>&sort=newest` |

The service should return a reasonably broad candidate set. It is not the
recommendation engine and must not embed subjective LLM judgments in the
indexer.

## 2. Scope and stages

### Stage 1: normalized social data and basic retrieval

Stage 1 delivers the durable foundation:

- normalize `/protocols/simplebuzz` posts;
- normalize `/protocols/paylike` like and unlike events;
- normalize `/protocols/paycomment` comment events;
- preserve an extensible adapter boundary for additional social protocols;
- process confirmed pins through the existing indexer registry;
- handle mempool observations without exposing unconfirmed data publicly;
- support deterministic create/modify/revoke folding for posts;
- support historical MANAPI replay through the same protocol handlers;
- provide global newest, author-filtered, and time-windowed post retrieval;
- provide post detail and comment retrieval;
- expose enough raw protocol metadata for downstream inspection.

`simpledonate` is reserved as the first optional interaction adapter. It may be
enabled in Stage 1 only after its payload and cancellation contract is verified
against representative chain samples. The core model must not require it.

### Stage 2: search and engagement read models

Stage 2 adds the basic discovery features, but still does not implement a
personalized recommendation system:

- keyword filtering over normalized post text;
- like, comment, and donation counters where the protocol is enabled;
- current actor-like state for like/unlike folding;
- hot sorting using a documented deterministic score;
- efficient time, author, target, and keyword indexes;
- replay, idempotency, and consistency verification at production scale.

Agent-specific recall ranking, recommendation, embeddings, and semantic intent
classification are explicitly deferred until these stages have real data and
measured query behavior.

## 3. Architectural boundaries

### 3.1 Existing ingestion boundary

The existing chain adapters and indexer engine remain the source of parsed
`PinInscription` events. The new aggregator must not shell out to a chain CLI or
create a second block scanner.

The aggregator receives the same confirmed and mempool events as the existing
aggregators and writes to its own Pebble namespace.

### 3.2 Separate read models

Keep the responsibilities separate:

- `publishedcontent` remains the publisher-centric projection used by Bot
  Homepage. It folds content versions and exposes raw published payloads.
- `socialcontent` (name to be finalized during implementation) becomes the
  post- and interaction-centric projection used by social APIs.
- Pure protocol parsing helpers may be shared in a small package, but the two
  read models must not share mutable records or query semantics.

This separation prevents interaction events from being forced into the
publisher/version model and allows Bot Homepage behavior to remain stable.

### 3.3 Semantic boundary

MetaSo accepts structured filters and returns candidates. The Agent host owns:

- natural-language intent extraction;
- user-specific phrasing and disambiguation;
- final ranking or selection;
- opening or rendering a selected post.

There is no LLM call, vector database, or subjective safety/recommendation
decision in the Stage 1–2 indexer.

## 4. Protocol normalization

The protocol adapter interface should map a raw PIN to one of the following
normalized event types:

```text
PostCreate
PostModify
PostRevoke
LikeStateChange
CommentCreate
DonationCreate (optional)
```

Every normalized event must retain:

- chain name;
- event PIN ID and timestamp;
- operation and original/target PIN references;
- publisher identity fields as observed on-chain;
- raw content type and payload bytes or a bounded raw representation;
- mempool/confirmed state.

Malformed or unsupported payloads must be counted and logged with a stable
reason. They must not abort block processing. Unknown protocol paths should be
ignored by this aggregator while remaining available to other aggregators.

### 4.1 simplebuzz

The post record is rooted at the create PIN. Modify and revoke pins resolve to
that root using the protocol path target and `originalId` compatibility fields.
The visible record contains the latest non-revoked payload, while retaining both
the source PIN and current version PIN IDs.

The normalized post must expose the original body in a typed form when possible
and a safe raw fallback when the content type is not recognized. Text extraction
must be deterministic and bounded so that indexing cannot be exhausted by a
large payload.

### 4.2 paylike

The initial adapter follows the verified protocol shape:

```json
{
  "isLike": true,
  "likeTo": "target-pin-id"
}
```

`isLike=true` activates the actor-target state; `isLike=false` deactivates it.
The event itself remains append-only in the interaction record so replay and
auditing do not depend on the current state alone.

The actor identity is resolved from the publishing PIN using the existing
userinfo lookup chain, with the observed metaid/address retained as fallback.

### 4.3 paycomment

The initial adapter follows the verified protocol shape:

```json
{
  "commentTo": "target-pin-id",
  "content": "comment body",
  "contentType": "text/plain"
}
```

Comments are stored as independent records keyed by their own PIN ID and
indexed by target PIN ID. A comment that arrives before its target post is
stored as pending-target data and becomes visible to target queries once the
target is indexed.

### 4.4 Future protocols

Additional protocol adapters must declare:

- the path(s) they own;
- create/update/revoke or state-transition semantics;
- target reference fields;
- actor identity fields;
- cancellation/folding rules;
- whether the event contributes to engagement counters.

Adding a protocol must not change the wire contract of existing APIs.

## 5. Pebble read model

Use a dedicated namespace, provisionally `socialcontent`.

### 5.1 Canonical records

`PostRecord`:

```text
sourcePinId
currentPinId
chainName
protocolPath
authorGlobalMetaId / authorMetaId / authorAddress
payloadText / payloadJSON / contentType
createdAt / updatedAt
hidden / isMempool
likeCount / commentCount / donationCount
hotScore
```

`LikeEvent`:

```text
pinId
chainName
targetPinId
actorGlobalMetaId / actorMetaId / actorAddress
isLike
timestamp
isMempool
```

`CommentRecord`:

```text
pinId
chainName
targetPinId
authorGlobalMetaId / authorMetaId / authorAddress
content / contentType
timestamp
isMempool
```

Donation records use the same target/actor pattern when the optional adapter is
enabled.

### 5.2 Required indexes

The implementation should maintain the following logical indexes. Exact key
encoding must remain internal and be hidden behind opaque cursors:

- post by canonical identity: `(chain, sourcePinId)`;
- posts by reverse event time;
- posts by author and reverse event time;
- interactions by target and reverse event time;
- current like state by `(targetPinId, actorIdentity)`;
- comments by comment PIN ID;
- keyword-to-post candidates for Stage 2.

All index writes must be idempotent. Replaying the same PIN must not duplicate a
post, comment, or engagement count.

### 5.3 Version and revoke behavior

Posts use source-PIN identity and latest-version folding, consistent with the
existing published-content model. A revoked post is hidden from public lists
and does not contribute to counters or hot results. Its historical record may
remain for reconciliation and audit.

Interaction events are not version-folded as posts. Their current effect is
derived from the event stream and actor-target state key. A revoke or explicit
false like state must remove the actor's active like contribution exactly once.

## 6. Confirmed, mempool, and replay consistency

Public Stage 1–2 list and detail APIs return confirmed data only by default.
Mempool events are indexed for fast reconciliation but are either hidden or
explicitly marked in internal diagnostics.

When a mempool event is later confirmed, the confirmed event replaces the
tentative event without changing logical counts twice. If a mempool event is
evicted, its tentative state is removed.

Historical backfill must call the same normalization and apply functions as
real-time indexing. It must be oldest-first within a page sequence, tolerate
duplicate MANAPI results, and expose progress/failure metrics. Backfill is
disabled by default and enabled explicitly for an environment.

## 7. Query API contract

All endpoints use the existing metaso-p2p response envelope and return HTTP 200;
clients branch on `code`.

### 7.1 Post feed and search

```text
GET /api/social/feed
```

Stage 1 parameters:

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `protocol` | string | `simplebuzz` | Protocol path or normalized protocol name |
| `publisher` | string | empty | Global Meta ID, Meta ID, or address |
| `chainName` | string | empty | `mvc`, `btc`, `doge`, or `opcat` |
| `since` | unix seconds | empty | Inclusive lower timestamp |
| `until` | unix seconds | empty | Inclusive upper timestamp |
| `sort` | string | `newest` | Stage 1 supports `newest` |
| `size` | integer | `20` | Range `1..100` |
| `cursor` | string | empty | Opaque query-bound cursor |

Stage 2 adds:

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `keyword` | string | empty | Deterministic text match over indexed post text |
| `sort=hot` | string | - | Engagement/recency score ordering |

Response item fields should include the canonical and current PIN IDs, chain,
protocol, author identity, normalized text/payload, timestamps, and Stage 2
engagement counters. `nextCursor` is empty when there are no more results.

The endpoint is intentionally broad. It does not expose a recommendation score
or claim that the returned items are the best semantic matches.

### 7.2 Post detail

```text
GET /api/social/post/:pinId
```

The detail response returns the normalized post, latest version metadata,
engagement counters, and raw payload where available. Any version PIN ID may be
accepted and resolved to the canonical post.

### 7.3 Comments

```text
GET /api/social/post/:pinId/comments
```

Comments are newest-first with an opaque cursor. The response includes comment
PIN ID, author identity, content, content type, timestamp, and confirmation
state when diagnostics are enabled.

Like state is exposed as an aggregate count in public feed/detail responses.
Actor-specific state is a separate future extension and must not be inferred
from the aggregate count.

## 8. Hot score

Stage 2 hot ordering must be deterministic, explainable, and testable. The
initial implementation should use a versioned formula rather than a black-box
ranking model:

```text
engagement = likeCount + 2 * commentCount + 3 * donationCount
ageHours   = max((now - createdAt) / 3600, 1)
hotScore   = engagement / pow(ageHours + 2, 1.5)
```

Tie-breakers are `updatedAt` descending, then `chainName`, then canonical PIN
ID. The constants are implementation configuration, not a promise that this
is the final recommendation algorithm. The API should return `hotScore` only
for diagnostics or explicitly requested fields.

## 9. Error and failure handling

- Invalid size, timestamp, sort, or cursor returns `code=40000`.
- A missing post in detail/comments returns `code=40400`.
- A malformed protocol payload is skipped from the affected projection,
  recorded with a reason, and does not fail the block or batch.
- Storage or index failures return `code=50000` and emit structured logs.
- Unknown protocol versions remain inspectable through raw event diagnostics.

## 10. Testing and acceptance criteria

Before production rollout, the implementation must pass:

1. Parser fixtures for create/modify/revoke simplebuzz, like/unlike, comments,
   malformed payloads, unknown content types, and cross-chain metadata.
2. Idempotency tests that replay every event twice and verify identical records,
   indexes, and counters.
3. Ordering and cursor tests for newest, author, time-window, keyword, and hot
   queries, including ties and empty pages.
4. Version-folding tests proving that modify and revoke do not create duplicate
   visible posts.
5. Interaction tests proving that a like/unlike sequence changes the target
   count once and that comments remain attached to the target PIN.
6. Mempool-to-confirmed and mempool-eviction reconciliation tests.
7. Backfill tests using a paginated MANAPI fixture with duplicates and out-of-
   order responses.
8. API contract tests for the response envelope, error codes, opaque cursors,
   and confirmed-only public results.
9. A bounded performance test over a representative local dataset, covering
   newest, author/time, keyword, and hot queries.

The implementation is ready for evaluation when all of the above pass and a
replayed historical window produces the same read model as live ingestion.

## 11. Deferred work

The following are intentionally outside Stage 1–2:

- personalized or follow-based recommendation;
- semantic/vector search;
- Agent-specific recall ranking and candidate scoring;
- automatic natural-language parsing inside MetaSo;
- social graph recommendations;
- protocol write APIs;
- unbounded full-text indexing of arbitrary binary payloads.

After real data and query metrics are available, the next design review should
define the Agent recall contract, including candidate count, freshness,
explainability, fallback behavior, and whether ranking belongs in MetaSo or in
the host Agent.
