# MetaWeb Search Quality — Deduplication & Ranking Hardening

## Background

Quality follow-up to [`2026-08-23-metaweb-search-api.md`](2026-08-23-metaweb-search-api.md) (the base search contract; tokenization, scoring fields, envelope, and cursor conventions are inherited unchanged). Requirements source: IDBots `docs/metaweb-search-quality-requirements.md` (2026-09-02), whose 2026-09-02 production measurements showed two quality problems for unattended bot consumers:

- **Duplicate rows**: publishers re-post identical or near-identical content as brand-new `create` pins (each copy is its own modify-chain head, `pinId == currentPinId`), so one logical document occupies several result rows and burns client budget per row.
- **Score saturation**: corpus-common tokens (`skill`, `metaweb`) make many hits share one plateau score (observed: 184 = single ≥4-rune token hitting all fields plus exact-phrase boosts, ×1.2 protocol prior), so the page cannot be ordered.

This spec covers Q1 (result deduplication), Q2 (ranking hardening), and Q3 (inverted-index decision checkpoint) of that requirements doc. Everything is additive: envelope, parameters, error codes, and cursor wire format are unchanged.

## Q1 — Result Deduplication

One logical document occupies one result row. Dedupe applies to `GET /api/metaweb/search` under **both** `sort=relevance` and `sort=newest`, and identically inside `protocols`-filtered and `publisher`-filtered queries (grouping keys are per-publisher, so filtering never changes group membership of the surviving set). Cross-publisher identical bodies (quote-buzz / citation conventions) are **not** deduped — same publisher only.

### Q1a — Modify-chain collapse (baseline, already index-time)

Search snapshots are keyed by `chain:sourcePinId`, so exactly one search document exists per modify chain and only its latest version is searchable. Several versions of one document can therefore never appear as separate rows; a handler-level regression test pins this down. No response change.

### Q1b — Content-level near-duplicate suppression

Dedupe runs over the full filtered+sorted match list, **before** page slicing. Grouping is same-publisher only, publisher identity = `publisherGlobalMetaId` case-insensitive (falling back to `metaid` when the global id is empty).

**Normalization** (`normalizeDedupeText`): lowercase; drop all whitespace, punctuation, and symbol runes; keep letters/digits/CJK runes. Applied to `title` and to the 1024-rune `contentExcerpt` (the same excerpt used for weight-1 scoring; full bodies are not held in the search snapshot).

**Version markers**: a title marker is any match of `(?i)\bv\d+(?:\.\d+)*` or one of the literal Chinese markers `勘误版`, `修订版`, `最终版`, `终版`, `重发版`. `stripVersionMarkers(title)` removes them (and leftover empty brackets) before normalization for the grouping key; the extracted marker string (e.g. `v1.2+勘误版`, empty when absent) is the member's `version`.

**Grouping** (union-find over two equivalence keys, so transitive duplicates merge):

- body key = publisher + normalized content excerpt (non-empty only)
- base-title key = publisher + normalized marker-stripped title (non-empty only)

Two documents sharing either key are in one group.

**Group classification and representative**:

- **Hard collapse** (default): the representative is the first member in sorted order — for `sort=relevance` that is highest score, tie-broken by `createdAt` desc then `pinId` asc; for `sort=newest` the newest. All other members are suppressed.
- **Versions group**: members carry ≥2 distinct version-marker values (no-marker counts as one value). These are deliberate distinct publications and must not lose information: the representative is the **newest** member (regardless of score), and the group carries a `versions` list (below). Rationale: hard-collapsing `v1`/`v2` would silently drop a publication the publisher explicitly versioned.

**Response contract** (additive, on the representative's `extra`, merged with the protocol-specific `extra` of the base spec):

- `extra.duplicateCount: <int>` — number of suppressed rows in the group; present only when > 0.
- `extra.versions: [...]` — versions groups only: every member, newest first, each `{pinId, currentPinId, title, createdAt, version}` (`version` is the extracted marker string, `""` when absent). The representative is `versions[0]`.

**Cursor semantics**: because suppression happens before offset slicing, the opaque offset cursor walks the deduped list: no group is skipped or returned twice, pages may contain groups of formerly-several rows, and `hasMore` still terminates exactly at the deduped list end. Page sizes do not shrink mid-walk — suppression is not per-page.

## Q2 — Ranking Hardening

Scorer-internal; no response-schema changes.

1. **Smooth IDF replaces binary IDF-lite.** Per request, per protocol key (namespace unchanged from the base spec), a scoring token's weight becomes `tokenWeight(token) × idfMultiplier`, where `idfMultiplier = ln(1 + N/df) / ln(1 + N)`, `N` = document count of the protocol key, `df` = the token's document frequency in that namespace. Properties: `df = 1` ⇒ multiplier 1.0; `df = N` ⇒ `ln 2 / ln(1+N)` — small but non-zero, so a direct query for a corpus-ubiquitous term still works. Corpus-common tokens (`skill`, `metaweb`) are down-weighted smoothly instead of by a single 0.5 step at 30 % df, which breaks the observed score plateau: ordering comes from rare tokens and field placement, not from one saturated token. The exact-phrase boost stays flat (+100 title / +10 other, once per document) and still requires at least one non-stopword scoring token.
2. **Word-boundary matching for Latin tokens** — unchanged from the base spec (already live since 2026-08-23); CJK tokens keep plain substring matching.
3. **Chinese stopwords join English stopwords.** A CJK scoring token (single char or bigram produced by `cjkRuns`) whose runes are **all** Chinese function characters (table in `internal/aggregator/metaweb/stopwords.go`: 的了是在和就都而不及与或一个没我们你他她这那也有要会能可对从被把向么嘛呢吧啊吗) contributes no score and is dropped from the scoring token set alongside English stopwords. Tokens mixing function and content characters (`一样`, `视频`) are unaffected; whole-segment tokens containing any non-function char are unaffected. An all-stopword query still yields an empty result.
4. **Tie-break determinism** — unchanged from the base spec: score desc, `createdAt` desc, `pinId` asc (both sorts, `score=0` under `newest`).

## Q3 — Inverted-Index Decision Checkpoint (2026-09-02)

Answers to the base spec's open question 1, with measured numbers.

**Corpus size per indexed protocol** — counted by a `record:`-prefix scan over the node's Pebble stores (method in Implementation Notes). The local full-history backfill data copy (2026-08-23) holds the phase-1 knowledge protocols:

| Protocol key | Pins (2026-08-23 backfill copy) |
| --- | --- |
| simplenote | 86 |
| metaprotocol | 29 |
| skill-service | 0 |
| **Total in copy** | **115** |

The stream protocols (simplebuzz / metaapp / metabot-skill) live in the node's primary stores and are not part of that backfill copy; production totals are larger but still orders of magnitude below the trigger thresholds — the 2026-09-02 evidence pages return ≤ 10 rows per query, and no protocol shows multi-page result sets in the IDBots measurements. Growth rate is not yet tracked; the backfill tool's completion report (pins fetched per path) is the current counting source.

**Measured search latency** — `BenchmarkMetaWebSearch` (`internal/aggregator/metaweb/search_bench_test.go`, full handler path: per-request IDF corpus scan + scoring + dedupe + page assembly; synthetic docs with ~100-rune CJK excerpts, 10% match rate, 10% of matches duplicated; Apple M-series, `go test -bench`):

| Corpus (searchable docs) | Latency (ns/op) | ≈ per query |
| --- | --- | --- |
| 1,000 | 6,182,900 | ~6 ms |
| 10,000 | 61,231,917 | ~61 ms |
| 100,000 | 599,028,958 | ~600 ms |

Scaling is linear in corpus size (three corpus sweeps: df scan, scoring, match-set dedupe/sort). Production p95 remains observable via the per-response `processingTime` field and the 300 ms slow-query warn log; no latency histogram exists yet.

**Decision**: substring scoring over the in-memory snapshot remains acceptable at the projected 12-month corpus (thousands of docs ⇒ single-digit-ms p95). One adjustment to the base spec's trigger list, driven by the measurement above: at 100 k docs the worst-case query (10% match rate, heavy excerpts) already takes ~600 ms > the 500 ms contract, so the document-count trigger tightens from 300 k to 100 k. **Trigger to build the dedicated Pebble inverted index**: any of (a) search p95 > 400 ms sustained over 24 h, (b) searchable documents > **100 k**, (c) snapshot heap contribution > 512 MB. A cheaper intermediate step is available before the full index: cache per-protocol-key df stats instead of rescanning the corpus per request (removes one of the three sweeps).

## Performance

The contracted target is unchanged: search p95 < 500 ms at the then-current corpus. Dedupe adds one O(matches) pass with string-keyed maps; the smooth IDF keeps the single per-request corpus scan of the base spec (same pass count as IDF-lite). Measured numbers live in the Q3 table above.

## Acceptance Mapping (requirements §7 evidence)

| Evidence set | Expected behavior | Covered by |
| --- | --- | --- |
| A — byte-identical buzz ×2, same publisher | one row, `duplicateCount: 1` | dedupe unit + handler tests |
| B — identical title ×3, same publisher/second | one row, `duplicateCount: 2` | dedupe unit + handler tests |
| C — near-identical title pairs | one row per pair | dedupe unit tests |
| D — `v1`/`v2`, `v1.1`/`v1.2（勘误版）` pairs | one versions-group row each, representative = newest, `versions` lists both | dedupe unit tests |
| `q=skill` / `q=metaweb` plateau 184 | page ordered by rare-token/field placement, not saturation | smooth-IDF scorer test |

## Implementation Notes

- Dedupe is a pure in-memory pass in `internal/aggregator/metaweb/dedupe.go`, invoked in `handleSearch` between sorting and page slicing. No storage changes; snapshots stay immutable (the representative's `extra` map is **copied** before `duplicateCount`/`versions` are added — the base handler aliases the snapshot's map).
- Representative `score` under `sort=relevance`: hard-collapse rows keep the representative's score (it is the top-scored member). Versions-group rows report the newest member's score, which may be lower than a suppressed member's — the group still sits at its representative's position, documented here so clients are not surprised.
- Corpus-count method for Q3: `storage.PebbleStore.ScanPrefix` over `record:` keys grouped by protocol path segment, cross-checked against `SearchDocuments()` snapshot lengths; on production, the metaweb backfill tool's completion report gives the same per-protocol pins-fetched counts.

## Explicitly Out of Scope

- Cross-publisher dedupe (quote/citation reposts are legitimate).
- Simhash/minhash fuzzy-body matching beyond the normalization above (the 2026-09-02 evidence sets are all exact-after-normalization; revisit if fuzzier reposts appear).
- Simulated publisher-convention enforcement (updates-as-modify is a content-workstream concern, relayed by IDBots).
