# Social Follow APIs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `GET /api/social/globalmetaid/:globalMetaId/following`, `GET /api/social/globalmetaid/:globalMetaId/followers`, and `GET /api/social/relationship` with the approved `globalMetaId`-only contract.

**Architecture:** Add a dedicated `internal/aggregator/social` read model. It owns follow-edge persistence, list/relationship queries, and `/api/social/*` HTTP handlers. It depends on `userinfo` only for canonical identity resolution and response projection, so `/api/info/*` stays separate from social graph reads.

**Tech Stack:** Go 1.26, Gin, Pebble, existing aggregator registry, existing `userinfo` lookup methods, `CGO_ENABLED=0 go test`.

---

## Review Status

This file is an implementation plan only. Do not start code changes from this
plan until the plan itself is reviewed and accepted.

Current baseline when this plan was written:

```text
repo: /Users/tusm/Documents/MetaID_Projects/metaso-p2p
branch: main
head: 2465022
status: untracked plan file only
spec: docs/superpowers/specs/2026-06-20-social-follow-apis-design.md
```

## Execution Protocol

After approval, execute in an isolated worktree from `main`:

```bash
git status --short --branch
git worktree add ../metaso-p2p-social-follow -b codex/social-follow-apis main
cd ../metaso-p2p-social-follow
git status --short --branch
```

Execution rules for the implementer:

1. Finish one task at a time.
2. Run the task-local verification before committing.
3. Commit only files changed and understood for that task.
4. After each commit, publish the required development journal with `metabot buzz post --from eric`.
5. Do not include historical backfill in the first shipping branch unless Task 5 is explicitly approved; Tasks 1-4 already satisfy the public API contract.

## File Structure

- Create `internal/aggregator/social/module.go`
  - Aggregator struct, lifecycle methods, route registration, lookup injection.
- Create `internal/aggregator/social/types.go`
  - Query params, DTOs, internal relation record, error sentinels, constants.
- Create `internal/aggregator/social/db.go`
  - Pebble key layout, active-edge upsert/delete, forward/reverse scans, cursor encoding.
- Create `internal/aggregator/social/process.go`
  - `/follow` pin parsing, canonical subject resolution, create/revoke fold, read queries.
- Create `internal/aggregator/social/api.go`
  - `following`, `followers`, `relationship` handlers and response mapping.
- Create `internal/aggregator/social/userinfo_adapter.go`
  - Narrow adapter from `userinfo.Aggregator` to social lookup interface.
- Create `internal/aggregator/social/process_test.go`
  - Unit tests for identity resolution, fold semantics, list ordering, relationship state.
- Create `internal/aggregator/social/api_test.go`
  - HTTP-level tests for field shape, validation, and error codes.
- Modify `cmd/metaso-p2p/main.go`
  - Register and wire the social aggregator in production.
- Modify `internal/api/router_test.go`
  - Mirror production wiring and add end-to-end router coverage.
- Optional later: create `internal/aggregator/social/backfill.go`
  - Historical replay from MANAPI. This is rollout work, not first-branch scope.

## Contract Notes

Implementation must preserve the approved design exactly:

- public input is `globalMetaId` only;
- list routes are:
  - `GET /api/social/globalmetaid/:globalMetaId/following`
  - `GET /api/social/globalmetaid/:globalMetaId/followers`
- relationship route is:
  - `GET /api/social/relationship?sourceGlobalMetaId=...&targetGlobalMetaId=...`
- `view` only supports `compact` and `profile`;
- compact items only expose `globalMetaId`, `name`, `nameId`, `avatarId`;
- profile items add only `bio`, `bioId`, `followedAt`, `followPinId`;
- no `metaId`, no `address`, no asset URLs, no reverse flags in list items;
- relationship response must expose both directions and `mutual`;
- unresolved subjects return `40400`;
- invalid params return `40000`;
- storage/read failures return `50000`;
- `processingTime` already means elapsed milliseconds via `internal/api/processingTimeMillis`.

The main technical risk is subject normalization. A historical or live `/follow`
pin may identify the target by legacy MetaID, `globalMetaId`, or address. The
social read model must canonicalize those forms only for indexed edge storage.
Public request subjects must still be resolved by `globalMetaId` alone.

## Task 1: Create The Social Aggregator Skeleton And Canonical Resolver

**Files:**
- Create: `internal/aggregator/social/module.go`
- Create: `internal/aggregator/social/types.go`
- Create: `internal/aggregator/social/userinfo_adapter.go`
- Create: `internal/aggregator/social/process_test.go`

- [ ] **Step 1: Write the failing lookup tests**

Create `internal/aggregator/social/process_test.go` with the first two tests
plus the shared test helpers:

```go
func TestAggregatorNameAndEmptyLookup(t *testing.T) {
	agg := newTestSocialAggregator(t)
	if agg.Name() != "social" {
		t.Fatalf("Name() = %q, want social", agg.Name())
	}

	got, err := agg.lookupTargetRef("idq-missing")
	if err != nil {
		t.Fatalf("lookupTargetRef error: %v", err)
	}
	if got != nil {
		t.Fatalf("lookupTargetRef = %+v, want nil", got)
	}
}

func TestLookupSubjectAcceptsGlobalMetaIdMetaIdAndAddress(t *testing.T) {
	agg := newTestSocialAggregator(t)
	agg.SetProfileLookup(&fakeProfileLookup{
		byGlobalMetaId: map[string]*ProfileSnapshot{
			"idq-target": {GlobalMetaId: "idq-target", MetaId: "meta-target", Address: "1Target"},
		},
		byMetaId: map[string]*ProfileSnapshot{
			"meta-target": {GlobalMetaId: "idq-target", MetaId: "meta-target", Address: "1Target"},
		},
		byAddress: map[string]*ProfileSnapshot{
			"1Target": {GlobalMetaId: "idq-target", MetaId: "meta-target", Address: "1Target"},
		},
	})

	for _, ref := range []string{"idq-target", "meta-target", "1Target"} {
		got, err := agg.lookupTargetRef(ref)
		if err != nil {
			t.Fatalf("lookupTargetRef(%q) error: %v", ref, err)
		}
		if got == nil || got.GlobalMetaId != "idq-target" {
			t.Fatalf("lookupTargetRef(%q) = %+v, want canonical idq-target", ref, got)
		}
	}
}

func (f *fakeProfileLookup) LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error) {
	return f.byGlobalMetaId[globalMetaId], nil
}

func (f *fakeProfileLookup) LookupByMetaId(metaId string) (*ProfileSnapshot, error) {
	return f.byMetaId[metaId], nil
}

func (f *fakeProfileLookup) LookupByAddress(address string) (*ProfileSnapshot, error) {
	return f.byAddress[address], nil
}

func newTestSocialAggregator(t *testing.T) *Aggregator {
	t.Helper()

	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	cacheProvider := cache.New(store)

	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg
}

func newSeededSocialAggregator(t *testing.T) *Aggregator {
	t.Helper()

	agg := newTestSocialAggregator(t)
	agg.SetProfileLookup(&fakeProfileLookup{
		byGlobalMetaId: map[string]*ProfileSnapshot{
			"idq-source": {GlobalMetaId: "idq-source", MetaId: "meta-source", Address: "1Source", Name: "Source", NameId: "name-source:i0", AvatarId: "avatar-source:i0"},
			"idq-target": {GlobalMetaId: "idq-target", MetaId: "meta-target", Address: "1Target", Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0", Bio: "target bio", BioId: "bio-target:i0"},
			"idq-a":      {GlobalMetaId: "idq-a", MetaId: "meta-a", Address: "1A", Name: "A", NameId: "name-a:i0", AvatarId: "avatar-a:i0"},
			"idq-b":      {GlobalMetaId: "idq-b", MetaId: "meta-b", Address: "1B", Name: "B", NameId: "name-b:i0", AvatarId: "avatar-b:i0"},
			"idq-c":      {GlobalMetaId: "idq-c", MetaId: "meta-c", Address: "1C", Name: "C", NameId: "name-c:i0", AvatarId: "avatar-c:i0"},
		},
		byMetaId: map[string]*ProfileSnapshot{
			"meta-target": {GlobalMetaId: "idq-target", MetaId: "meta-target", Address: "1Target", Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0", Bio: "target bio", BioId: "bio-target:i0"},
		},
		byAddress: map[string]*ProfileSnapshot{
			"1Target": {GlobalMetaId: "idq-target", MetaId: "meta-target", Address: "1Target", Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0", Bio: "target bio", BioId: "bio-target:i0"},
		},
	})
	return agg
}

func followPin(id, followerGlobalMetaId, followerMetaId, followerAddress, targetRef string, ts int64) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:            id,
		Path:          "/follow",
		Operation:     "create",
		GlobalMetaId:  followerGlobalMetaId,
		MetaId:        followerMetaId,
		CreateMetaId:  followerMetaId,
		Address:       followerAddress,
		CreateAddress: followerAddress,
		ChainName:     "mvc",
		Timestamp:     ts,
		ContentBody:   []byte(targetRef),
	}
}

type fakeProfileLookup struct {
	byGlobalMetaId map[string]*ProfileSnapshot
	byMetaId       map[string]*ProfileSnapshot
	byAddress      map[string]*ProfileSnapshot
}
```

- [ ] **Step 2: Run the focused test and confirm the package is still missing**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/social -run 'TestAggregatorNameAndEmptyLookup|TestLookupSubjectAcceptsGlobalMetaIdMetaIdAndAddress' -count=1
```

Expected: compile failure because the social package does not exist yet.

- [ ] **Step 3: Add the package skeleton**

Create `internal/aggregator/social/types.go` with the base lookup shapes:

```go
type SubjectSnapshot struct {
	GlobalMetaId string
	MetaId       string
	Address      string
	Name         string
	NameId       string
	AvatarId     string
	Bio          string
	BioId        string
}

type ProfileSnapshot = SubjectSnapshot

type ProfileLookup interface {
	LookupByMetaId(metaid string) (*ProfileSnapshot, error)
	LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error)
	LookupByAddress(address string) (*ProfileSnapshot, error)
}
```

Create `internal/aggregator/social/module.go` using the same lifecycle shape as
`privatechat` and `userinfo`:

```go
type Aggregator struct {
	store         *storage.PebbleStore
	cache         *cache.Cache[[]byte]
	notifyCh      chan *aggregator.NotifyEvent
	profileLookup ProfileLookup
}

func (a *Aggregator) Name() string { return "social" }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	a.cache = cacheProvider.Namespace("social", 2000, 5*time.Minute)
	a.notifyCh = make(chan *aggregator.NotifyEvent, 256)
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent { return a.notifyCh }
func (a *Aggregator) SetProfileLookup(lookup ProfileLookup)         { a.profileLookup = lookup }
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup)        { registerRoutes(a, router) }
func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}
```

Add the internal target-resolution helper in the same file. This helper is only
for folding `/follow` pins; it is not for public request identity input:

```go
func (a *Aggregator) lookupTargetRef(ref string) (*SubjectSnapshot, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, nil
	}
	if a == nil || a.profileLookup == nil {
		return nil, ErrUnavailable
	}
	for _, fn := range []func(string) (*ProfileSnapshot, error){
		a.profileLookup.LookupByGlobalMetaId,
		a.profileLookup.LookupByMetaId,
		a.profileLookup.LookupByAddress,
	} {
		profile, err := fn(ref)
		if err != nil {
			return nil, err
		}
		if profile != nil {
			return &SubjectSnapshot{
				GlobalMetaId: profile.GlobalMetaId,
				MetaId:       profile.MetaId,
				Address:      profile.Address,
				Name:         profile.Name,
				NameId:       profile.NameId,
				AvatarId:     profile.AvatarId,
				Bio:          profile.Bio,
				BioId:        profile.BioId,
			}, nil
		}
	}
	return nil, nil
}
```

Add a separate public request resolver that only accepts `globalMetaId`:

```go
func (a *Aggregator) lookupByGlobalMetaId(globalMetaId string) (*SubjectSnapshot, error) {
	globalMetaId = strings.TrimSpace(globalMetaId)
	if globalMetaId == "" {
		return nil, ErrInvalidParameter
	}
	if a == nil || a.profileLookup == nil {
		return nil, ErrUnavailable
	}
	profile, err := a.profileLookup.LookupByGlobalMetaId(globalMetaId)
	if err != nil {
		return nil, ErrUnavailable
	}
	if profile == nil {
		return nil, ErrNotFound
	}
	return &SubjectSnapshot{
		GlobalMetaId: profile.GlobalMetaId,
		MetaId:       profile.MetaId,
		Address:      profile.Address,
		Name:         profile.Name,
		NameId:       profile.NameId,
		AvatarId:     profile.AvatarId,
		Bio:          profile.Bio,
		BioId:        profile.BioId,
	}, nil
}
```

Create `internal/aggregator/social/userinfo_adapter.go` by copying the narrow
adapter pattern used in `internal/aggregator/privatechat/userinfo_adapter.go`.

- [ ] **Step 4: Run the focused test and confirm it passes**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/social -run 'TestAggregatorNameAndEmptyLookup|TestLookupSubjectAcceptsGlobalMetaIdMetaIdAndAddress' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the skeleton**

```bash
git add internal/aggregator/social/module.go internal/aggregator/social/types.go internal/aggregator/social/userinfo_adapter.go internal/aggregator/social/process_test.go
git commit -m "feat: add social aggregator skeleton"
```

## Task 2: Implement Active Follow Relation Folding And Read Queries

**Files:**
- Create: `internal/aggregator/social/db.go`
- Create: `internal/aggregator/social/process.go`
- Modify: `internal/aggregator/social/types.go`
- Modify: `internal/aggregator/social/process_test.go`

- [ ] **Step 1: Write the failing fold/query tests**

Append to `internal/aggregator/social/process_test.go`:

```go
func TestHandleBlockPinCreateAndRelationshipState(t *testing.T) {
	agg := newSeededSocialAggregator(t)

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "meta-target", 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	rel, err := agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: "idq-source",
		TargetGlobalMetaId: "idq-target",
	})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if !rel.Source.FollowsTarget || rel.Target.FollowsSource || rel.Mutual {
		t.Fatalf("Relationship = %+v, want one-way source->target", rel)
	}
	if rel.Source.FollowPinId != "follow-1:i0" || rel.Source.FollowedAt != 1001 {
		t.Fatalf("unexpected source edge metadata: %+v", rel.Source)
	}
}

func TestHandleBlockPinRevokeRemovesActiveRelation(t *testing.T) {
	agg := newSeededSocialAggregator(t)

	_, _ = agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "idq-target", 1001))
	_, _ = agg.HandleBlockPin(&aggregator.PinInscription{
		Id:         "revoke-1:i0",
		Path:       "/follow@follow-1:i0",
		Operation:  "revoke",
		OriginalId: "follow-1:i0",
		Timestamp:  1002,
	})

	rel, err := agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: "idq-source",
		TargetGlobalMetaId: "idq-target",
	})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if rel.Source.FollowsTarget || rel.Source.FollowPinId != "" || rel.Source.FollowedAt != 0 {
		t.Fatalf("expected cleared source edge, got %+v", rel.Source)
	}
}

func TestListFollowingAndFollowersNewestFirst(t *testing.T) {
	agg := newSeededSocialAggregator(t)

	_, _ = agg.HandleBlockPin(followPin("follow-a:i0", "idq-source", "meta-source", "1Source", "idq-a", 1001))
	_, _ = agg.HandleBlockPin(followPin("follow-b:i0", "idq-source", "meta-source", "1Source", "idq-b", 1002))
	_, _ = agg.HandleBlockPin(followPin("follow-c:i0", "idq-source", "meta-source", "1Source", "idq-c", 1003))

	page1, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 2, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing page1: %v", err)
	}
	if len(page1.List) != 2 || page1.List[0].GlobalMetaId != "idq-c" || page1.List[1].GlobalMetaId != "idq-b" {
		t.Fatalf("page1 = %+v, want c then b", page1.List)
	}

	page2, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 2, Cursor: page1.NextCursor, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing page2: %v", err)
	}
	if len(page2.List) != 1 || page2.List[0].GlobalMetaId != "idq-a" || page2.NextCursor != "" {
		t.Fatalf("page2 = %+v, want final a and empty cursor", page2)
	}
}
```

- [ ] **Step 2: Run the focused tests and confirm query methods do not exist yet**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/social -run 'TestHandleBlockPinCreateAndRelationshipState|TestHandleBlockPinRevokeRemovesActiveRelation|TestListFollowingAndFollowersNewestFirst' -count=1
```

Expected: compile failure because relation storage and query methods are still missing.

- [ ] **Step 3: Add the internal relation model**

Extend `internal/aggregator/social/types.go` with the query and response types:

```go
const (
	ViewCompact = "compact"
	ViewProfile = "profile"
)

type ListParams struct {
	GlobalMetaId string
	Cursor       string
	Size         int
	View         string
}

type RelationshipParams struct {
	SourceGlobalMetaId string
	TargetGlobalMetaId string
}

type ListResult struct {
	List       []ListItem `json:"list"`
	NextCursor string     `json:"nextCursor"`
	Size       int        `json:"size"`
}

type FollowEdge struct {
	FollowerGlobalMetaId string `json:"followerGlobalMetaId"`
	TargetGlobalMetaId   string `json:"targetGlobalMetaId"`
	FollowPinId          string `json:"followPinId"`
	FollowedAt           int64  `json:"followedAt"`
	Active               bool   `json:"active"`
}

type ListItem struct {
	GlobalMetaId string
	Name         string
	NameId       string
	AvatarId     string
	Bio          string
	BioId        string
	FollowedAt   int64
	FollowPinId  string
}
```

Add result shapes for the approved relationship contract:

```go
type RelationshipSide struct {
	GlobalMetaId  string `json:"globalMetaId"`
	FollowsTarget bool   `json:"followsTarget"`
	FollowsSource bool   `json:"followsSource"`
	FollowPinId   string `json:"followPinId"`
	FollowedAt    int64  `json:"followedAt"`
}

type RelationshipResult struct {
	Source RelationshipSide `json:"source"`
	Target RelationshipSide `json:"target"`
	Mutual bool             `json:"mutual"`
}

var (
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrNotFound         = errors.New("subject not found")
	ErrUnavailable      = errors.New("aggregation unavailable")
)
```

- [ ] **Step 4: Implement Pebble persistence and fold logic**

Create `internal/aggregator/social/db.go` with two active indexes:

```go
const (
	edgeByPairPrefix      = "edge:pair:"
	followingByUserPrefix = "edge:following:"
	followerByUserPrefix  = "edge:followers:"
)

func pairKey(followerGlobalMetaId, targetGlobalMetaId string) []byte
func followingIndexKey(followerGlobalMetaId string, followedAt int64, followPinId string) []byte
func followerIndexKey(targetGlobalMetaId string, followedAt int64, followPinId string) []byte

func (a *Aggregator) saveActiveEdge(edge *FollowEdge) error
func (a *Aggregator) deleteActiveEdge(edge *FollowEdge) error
func (a *Aggregator) loadActiveEdge(followerGlobalMetaId, targetGlobalMetaId string) (*FollowEdge, error)
```

The scan order must encode newest-first. Use an inverted timestamp or an
equivalent descending lexical key so paging does not require in-memory sort.

Create `internal/aggregator/social/process.go` with the fold entrypoint:

```go
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if err := a.processPin(pin); err != nil {
		return nil, err
	}
	return nil, nil
}

func (a *Aggregator) processPin(pin *aggregator.PinInscription) error
```

Fold semantics:

- ignore non-`/follow` pins and unrelated revoke paths;
- for `create`, resolve follower from the pin and target from `contentBody`;
- for `revoke`, locate the original edge by `OriginalId` or normalized pair data and remove the active indexes;
- mempool stays ignored in this first version.

Add query methods in the same file:

```go
func (a *Aggregator) Relationship(params RelationshipParams) (*RelationshipResult, error)
func (a *Aggregator) ListFollowing(params ListParams) (*ListResult, error)
func (a *Aggregator) ListFollowers(params ListParams) (*ListResult, error)
```

These methods should:

- resolve the request subject via `lookupByGlobalMetaId`;
- return `ErrNotFound` when the subject profile cannot be resolved;
- return `ErrUnavailable` when `profileLookup` is missing or a dependent lookup fails;
- read only active edges;
- project profile fields from `userinfo`;
- keep list order newest-first;
- compute `nextCursor` by reading one extra row.

- [ ] **Step 5: Run the focused tests**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/social -run 'TestHandleBlockPinCreateAndRelationshipState|TestHandleBlockPinRevokeRemovesActiveRelation|TestListFollowingAndFollowersNewestFirst' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit the fold/query layer**

```bash
git add internal/aggregator/social/db.go internal/aggregator/social/process.go internal/aggregator/social/types.go internal/aggregator/social/process_test.go
git commit -m "feat: add social follow relation model"
```

## Task 3: Implement The `/api/social/*` HTTP Contract

**Files:**
- Create: `internal/aggregator/social/api.go`
- Create: `internal/aggregator/social/api_test.go`
- Modify: `internal/aggregator/social/process.go`
- Modify: `internal/aggregator/social/types.go`

- [ ] **Step 1: Write the failing handler tests**

Create `internal/aggregator/social/api_test.go` with six focused cases:

```go
func TestFollowingCompactResponseShape(t *testing.T)
func TestFollowingProfileResponseShape(t *testing.T)
func TestRelationshipBidirectionalResponseShape(t *testing.T)
func TestRelationshipNoRelationStillReturnsFalseBooleans(t *testing.T)
func TestSocialHandlersReturnNotFoundForUnknownGlobalMetaId(t *testing.T)
func TestSocialHandlersRejectInvalidParams(t *testing.T)
```

Key assertions those tests must include:

```go
if _, ok := item["metaId"]; ok {
	t.Fatalf("compact response leaked metaId: %+v", item)
}
if _, ok := item["address"]; ok {
	t.Fatalf("compact response leaked address: %+v", item)
}
if got := item["avatarId"]; got != "avatar-target:i0" {
	t.Fatalf("avatarId = %v, want avatar pin id", got)
}
if code := int(body["code"].(float64)); code != 40000 {
	t.Fatalf("code = %d, want 40000", code)
}
```

Add invalid-request coverage for:

- `size=0`
- `size=101`
- `view=full`
- malformed cursor
- missing `sourceGlobalMetaId`
- missing `targetGlobalMetaId`

Add spec-critical assertions for:

- `GET /api/social/globalmetaid/idq-missing/following` returns `code=40400`;
- `GET /api/social/relationship?...` with two valid known subjects and no edge returns `code=0`;
- the no-relation body still includes `source.followsTarget == false` and `target.followsSource == false`.

- [ ] **Step 2: Run the focused test and confirm the handlers are still missing**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/social -run 'TestFollowingCompactResponseShape|TestFollowingProfileResponseShape|TestRelationshipBidirectionalResponseShape|TestSocialHandlersRejectInvalidParams' -count=1
```

Expected: compile failure because route handlers and request validation do not exist yet.

- [ ] **Step 3: Add request parsing and route registration**

Create `internal/aggregator/social/api.go`:

```go
func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	social := router.Group("/social")
	social.GET("/globalmetaid/:globalMetaId/following", a.handleFollowing)
	social.GET("/globalmetaid/:globalMetaId/followers", a.handleFollowers)
	social.GET("/relationship", a.handleRelationship)
}
```

Use the repo's existing response style from `internal/aggregator/skillservice/api.go`
and `internal/aggregator/bothomepage/api.go`:

```go
func respondSocialError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidParameter):
		api.RespErr(c, 40000, "invalid parameter")
	case errors.Is(err, ErrNotFound):
		api.RespErr(c, 40400, "subject not found")
	default:
		api.RespErr(c, 50000, "aggregation unavailable")
	}
}
```

Each handler should:

- parse and trim params;
- default `size` to `20`;
- restrict `size` to `1..100`;
- default `view` to `compact`;
- reject unsupported `view`;
- decode the opaque cursor;
- call the matching aggregator method;
- return `api.RespSuccess(c, result)`.

Do not serialize `ListItem` directly for the list route. Build explicit compact
and profile response DTOs so compact responses never expose profile-only
fields, while profile responses always include the approved extras.

- [ ] **Step 4: Run the handler test set**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/social -run 'TestFollowingCompactResponseShape|TestFollowingProfileResponseShape|TestRelationshipBidirectionalResponseShape|TestSocialHandlersRejectInvalidParams' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the HTTP contract**

```bash
git add internal/aggregator/social/api.go internal/aggregator/social/api_test.go internal/aggregator/social/process.go internal/aggregator/social/types.go
git commit -m "feat: add social follow api routes"
```

## Task 4: Wire The Aggregator Into Production And Router Fixtures

**Files:**
- Modify: `cmd/metaso-p2p/main.go`
- Modify: `internal/api/router_test.go`

- [ ] **Step 1: Write the failing router coverage**

Add one end-to-end router test in `internal/api/router_test.go`:

```go
func TestRouterSocialFollowEndpoints(t *testing.T) {
	fixture := setupFullRouterFixture(t)

	seedSocialUserProfile(t, fixture.store, "idq-source", "meta-source", "1Source")
	seedSocialUserProfile(t, fixture.store, "idq-target", "meta-target", "1Target")

	if _, err := fixture.socialAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            "follow-1:i0",
		Path:          "/follow",
		Operation:     "create",
		GlobalMetaId:  "idq-source",
		MetaId:        "meta-source",
		CreateMetaId:  "meta-source",
		Address:       "1Source",
		CreateAddress: "1Source",
		ChainName:     "mvc",
		Timestamp:     1001,
		ContentBody:   []byte("meta-target"),
	}); err != nil {
		t.Fatalf("HandleBlockPin: %v", err)
	}

	w, body := get(t, fixture.router, "/api/social/globalmetaid/idq-source/following?view=profile&size=20")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	data := assertResponseDataKeys(t, body, "list", "nextCursor", "size")
	list := data["list"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("list len = %d, want 1", len(list))
	}
}
```

Add a local helper near that test by following the storage style already used by
`seedBotProfile` in the same file:

```go
func seedSocialUserProfile(t *testing.T, store *storage.PebbleStore, globalMetaId, metaId, address string) {
	t.Helper()

	profile := userinfo.UserProfile{
		GlobalMetaID: globalMetaId,
		MetaID:       metaId,
		Address:      address,
		Name:         "Profile " + globalMetaId,
		NameId:       "name-" + globalMetaId + ":i0",
		AvatarId:     "avatar-" + globalMetaId + ":i0",
		Bio:          "bio-" + globalMetaId,
		BioId:        "bio-" + globalMetaId + ":i0",
		ChainName:    "mvc",
	}

	raw, err := json.Marshal(profile)
	if err != nil {
		t.Fatalf("marshal profile: %v", err)
	}
	if err := store.Set("userinfo", []byte("profile:"+metaId), raw); err != nil {
		t.Fatalf("seed userinfo profile: %v", err)
	}
	if err := store.Set("userinfo", []byte("globalmetaid:"+strings.ToLower(globalMetaId)), []byte(metaId)); err != nil {
		t.Fatalf("seed userinfo globalMetaId index: %v", err)
	}
}
```

- [ ] **Step 2: Run the router test and confirm production wiring is incomplete**

```bash
CGO_ENABLED=0 go test ./internal/api -run 'TestRouterSocialFollowEndpoints' -count=1
```

Expected: compile failure because `setupFullRouterFixture` does not yet expose a wired `socialAgg`.

- [ ] **Step 3: Register and wire the social aggregator**

In `cmd/metaso-p2p/main.go`, mirror the existing registration style:

```go
import "github.com/metaid-developers/metaso-p2p/internal/aggregator/social"
```

Inside the aggregator-registration block:

```go
var socialAgg *social.Aggregator
socialCandidate := &social.Aggregator{}
if err := aggRegistry.Register(socialCandidate); err != nil {
	log.Printf("WARNING: social aggregator init failed: %v", err)
} else {
	socialAgg = socialCandidate
}
```

After `userinfoAgg` exists:

```go
if socialAgg != nil {
	socialAgg.SetProfileLookup(social.NewUserInfoLookupAdapter(userinfoAgg))
}
```

In `internal/api/router_test.go`:

- import the new `social` package;
- add `socialAgg *social.Aggregator` to `fullRouterFixture`;
- register `socialAgg` in `setupFullRouterFixture`;
- wire `socialAgg.SetProfileLookup(social.NewUserInfoLookupAdapter(userAgg))`;
- return `socialAgg` from the fixture.

- [ ] **Step 4: Run the router and package verification**

```bash
CGO_ENABLED=0 go test ./internal/api -run 'TestRouterSocialFollowEndpoints' -count=1
CGO_ENABLED=0 go test ./internal/aggregator/social ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit the wiring**

```bash
git add cmd/metaso-p2p/main.go internal/api/router_test.go
git commit -m "feat: wire social follow aggregator"
```

## Task 5: Optional Rollout Task For Historical Backfill

This task is intentionally not part of the first implementation branch unless
the user explicitly approves it. The public API contract is complete after
Tasks 1-4. Backfill only affects how much historical data is available on day
one.

**Files:**
- Create: `internal/aggregator/social/backfill.go`
- Create: `internal/aggregator/social/backfill_test.go`
- Maybe modify later: `cmd/metaso-p2p/main.go`
- Maybe modify later: `internal/config/config.go`

- [ ] **Step 1: Confirm rollout scope before touching code**

Answer these before implementation:

- add a dedicated social config, or keep backfill as a manual operator action?
- replay only `/follow`, or also legacy revoke path variants?
- enable by default, or rollout behind an explicit env flag?

Do not reuse `BotHomepageV2Backfill` as a production control knob for social
backfill. If this task is approved later, it should get its own dedicated
config or remain a manual one-shot operation.

- [ ] **Step 2: If approved, mirror the existing backfill pattern**

Use `internal/aggregator/userinfo/backfill.go` and
`internal/aggregator/publishedcontent/backfill.go` as the model:

```go
type BackfillOptions struct {
	Context  context.Context
	Client   *BackfillClient
	Since    time.Time
	PageSize int
}

func (a *Aggregator) Backfill(opts BackfillOptions) error
func (c *BackfillClient) ListPath(ctx context.Context, path, cursor string, size int) (backfillPage, error)
```

Historical replay must preserve oldest-to-newest fold order so a late revoke
wins over an earlier follow.

## Final Verification

- [ ] **Step 1: Run the full targeted verification set**

For the first shipping branch:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/social ./internal/api -count=1
git diff --check
git status --short
```

If production wiring changed:

```bash
CGO_ENABLED=0 go test ./cmd/metaso-p2p -count=1
```

Expected:

- social package tests pass;
- router tests pass;
- `git diff --check` prints nothing.

- [ ] **Step 2: Publish the development journal for each commit**

Minimum journal content per commit:

```text
- commit hash
- files changed
- exact CGO_ENABLED=0 go test commands
- spec sections covered
- any remaining caveat
```

- [ ] **Step 3: Stop after user-visible contract completion**

Do not extend scope into writes, counters, recommendations, or social backfill
unless explicitly requested after Tasks 1-4 land.
