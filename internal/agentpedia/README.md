# MetaSo Agentpedia aggregation (L2)

Independent Go implementation of the Agentpedia replay engine (adoption-algo-v1
semantics) plus the read-side aggregation API. This is MetaSo's L1+L2 per the
second deliverable §4.1 (pin://23e828698ca0cfc90edc2004074f85b4fd93227a34e1e1a6247fe328a1e0bc83i0)
and serves the acceptance item "entry list / entry detail / backlinks /
incremental lastCursor".

## Determinism contract (architect rulings, binding for this implementation)

- **Pure function of the pin stream.** The same event stream yields the same
  view — always. No indexer or wall-clock queries participate in replay.
- **clusterAliases defaults to EMPTY in production** (D4 ruling,
  pin://63a2164522c53d6e81908865c3fdc4b6dcd58a9899e8bf42b3fa9d847b693846i0).
  The merge mechanism exists in the engine (rate and edit-war counters resolve
  through the cluster root), but with no on-chain carrier (X11 forbids new MVP
  event types) the default empty set changes no counting result. Alias tables
  are a TEST-INJECTION mode only; an on-chain carrier is a v1.x open question.
- **Arbiter sets**: production replay draws via arbiter-draw-v1
  (sha256(seed+id), top-N); scenario injections are a test mode.
- **Tiers per E-2** (pin://892ce8b2889cf20d2901dc955182b0674c5c7c85eccc055230bef205f377cca3i0):
  T0 window forbids revs; T0+ = basic edit right; T1 = identity marker
  (age >= t0DurationHours AND validRevs >= t1MinValidRevs); T2 unchanged.
- **claim.refs per E3-5** (pin://390922537362e4acd2af95f79c19505d4c664b6347de6d99817f855075e8a42ei0):
  editor self-reported integer; "resolvable" means on-chain URI pattern format
  only — replay never queries an indexer; Web2 URLs neither count nor block
  writes; the engine performs NO refs consistency validation (dumb-pipe
  contract, locked by vectors).

## Layout

- `replay.go` — deterministic one-pass engine (`Replay(events, opts) *View`).
  Deliberately a separate implementation from the IDBots-side engine so the G2
  view-parity check compares independent code paths. As of the canonical
  vectors file (sha256 e680b03d..., metafile://cedebb0543487d36268009fe940f4212dc0b0e62848b79633a24601bf92299eci0)
  the two engines produce IDENTICAL normalized views on all 25 pipeline vectors.
- `l2.go` — read-only aggregation API + manapi incremental puller.
- `testdata/agentpedia-replay-vectors.v1.json` — vendored canonical vector set
  (re-copy from the IDBots worktree when the on-chain file increments).

## HTTP API (read-only; the service never writes pins)

| Endpoint | Shape |
|---|---|
| `GET /api/agentpedia/entry?lang=&slug=` | `{entryKey, head:{pin,title,author,content,contentRef,height}, status, disputed[], featured, history[{pin,author,height,type}], backlinks[], editorBreakdown[], contests[], redirect}` — `head.pin` is the unique headRevId; consumers must not infer it from history |
| `GET /api/agentpedia/entry_list?lang=&sort=updated\|featured&cursor=&limit=` | `{total, items[{lang,slug,entryKey,head,status,updatedAt}], cursor}` — cursor is the next offset |
| `GET /api/agentpedia/backlinks?lang=&slug=` | `{entryKey, backlinks[{fromEntry,fromRev}]}` — per-revision wikilink scan (`[[slug]]` / `[[lang:slug]]`) |
| `GET /api/agentpedia/sync` | manapi incremental pull over all seven paths; returns the per-path `lastCursor` map (incremental-sync contract, acceptance item 4) |
| `GET /healthz` | liveness |

Consumer tolerance (reader contract §4): Tier A missing fields degrade to
defaults; consumers render from this shape only and never derive head status
themselves.

## Run

```bash
go run ./cmd/agentpedia-l2 -addr :8088            # live mode (manapi sync on demand)
go run ./cmd/agentpedia-l2 -seed events.json      # demo/cold-start mode
go test ./internal/agentpedia/                    # 25/25 pipeline + 12 supplementary vectors + L2 endpoint tests
```

Schema/consumer cross-check: the composite schema artifacts live in the IDBots
worktree (`agentpediaSchemas.ts`, on-chain dump
metafile://1e4b38eed5d6873ae5f92d1cef511bf893110a5489e8593e454673b2752cfd7fi0,
v1.1 refresh pending E-3 alignment upload).
