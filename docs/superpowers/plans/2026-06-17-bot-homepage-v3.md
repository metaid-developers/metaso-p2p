# Bot Homepage V3 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an explicit `botHomepage.v3` response for `GET /api/bot-homepage/globalmetaid/:globalMetaId` while preserving the existing default v1 response and explicit v2 behavior.

**Architecture:** Keep the public route and response envelope unchanged, and branch only inside the Bot Homepage aggregator when the caller requests `version=v3` or `schemaVersion=botHomepage.v3`. Add v3-only response types so the clean v3 document cannot accidentally marshal v1/v2 debug fields, then extend the existing `userinfo`, `skillservice`, and `publishedcontent` read models only where v3 needs data that is not currently available.

**Tech Stack:** Go, Gin, existing `internal/aggregator` registry, Pebble-backed read models, `CGO_ENABLED=0 go test`.

---

## Review Status

This is an implementation plan only. Do not implement from this file until the plan has been reviewed and accepted.

Baseline confirmed before authoring:

```text
branch: main
head: bfa7b3f
status: clean
upstream: main is ahead of origin/main by 1 commit
```

Primary specs:

- Public contract: `docs/specs/2026-06-17-bot-homepage-v3-api.md`
- Internal implementation spec: `docs/superpowers/specs/2026-06-17-bot-homepage-v3-implementation-spec.md`
- Bot Info protocol source: `/Users/tusm/Documents/MetaID_Projects/open-agent-connect/docs/metaid_protocols/06-bot-info.md`

## Execution Protocol

After review approval, create an isolated branch and worktree from current `main`:

```bash
git status --short
git worktree add ../metaso-p2p-bot-homepage-v3 -b codex/bot-homepage-v3 main
cd ../metaso-p2p-bot-homepage-v3
git status --short
```

Use subagent-driven development for execution:

1. The controller dispatches one fresh implementer subagent per task.
2. The implementer completes only that task, runs the task verification, and commits only files changed and understood for that task.
3. After each commit, use `metabot-post-buzz` to publish a detailed development-journal entry with the commit hash, files touched, tests run, and spec requirements covered.
4. The controller reviews the task diff before starting the next task.
5. If any task requires deleting files or deleting tracked behavior, stop before staging and wait for an explicit user instruction containing `commit`.

Commit messages must use the repository format:

```text
feat: <short description>
fix: <short description>
refactor: <short description>
docs: <short description>
chore: <short description>
```

## File Structure

- Modify `internal/aggregator/userinfo/module.go`: add v3 Bot Info read-model fields, normalize `/info/*` path casing, store avatar content type, preserve mempool handling, and implement clear semantics.
- Modify `internal/aggregator/userinfo/backfill.go`: include lower-case `/info/llm` and `/info/persona` in historical Bot Info reads while preserving legacy `/info/LLM`.
- Modify `internal/aggregator/userinfo/module_test.go`: cover lower-case and legacy LLM paths, persona storage, avatar content type, clear behavior, and mempool `/info/*` visibility.
- Modify `internal/aggregator/bothomepage/userinfo_adapter.go`: pass new userinfo fields into the Bot Homepage build layer.
- Create `internal/aggregator/bothomepage/types_v3.go`: define v3-only response structs.
- Create `internal/aggregator/bothomepage/build_v3.go`: assemble identity, profile, presence, sections, and warnings for v3.
- Modify `internal/aggregator/bothomepage/query.go`: parse `version=v3` and `schemaVersion=botHomepage.v3`, and apply v3-specific query knobs.
- Modify `internal/aggregator/bothomepage/api.go`: route v3 requests to `BuildV3` while leaving v1/v2 on existing `Build`.
- Modify `internal/aggregator/bothomepage/build_test.go`: cover v3 data shape, profile mapping, sections, warnings, and compatibility with v1/v2.
- Modify `internal/aggregator/bothomepage/query_test.go`: cover v3 query selection and v1/v2 compatibility.
- Modify `internal/api/router_test.go` only if existing package-level tests do not cover the public envelope and route behavior.
- Modify `internal/aggregator/skillservice/list.go` only if the current homepage service list cannot expose raw service declaration fields safely for v3.
- Modify `internal/aggregator/skillservice/*_test.go` only if a new v3-safe service payload helper is needed.
- Modify `internal/aggregator/publishedcontent/*_test.go` only if existing published content tests do not already prove mempool buzz/metaapp visibility for the homepage read path.

## Data Contract Summary

v3 `data` must have only these top-level keys:

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

```text
globalMetaId
canonical
persona
homepage
services
actions
proofs
source
resolvedAt
```

Forbidden section item keys:

```text
sourcePinId
currentPinId
createdAt
updatedAt
chainName
publisher
proof
service
payloadJson
payloadText
payloadExposed
```

## Task 1: Extend UserInfo Bot Info Read Model

**Files:**

- Modify: `internal/aggregator/userinfo/module.go`
- Modify: `internal/aggregator/userinfo/backfill.go`
- Modify: `internal/aggregator/userinfo/module_test.go`

- [ ] **Step 1: Write failing tests for v3 Bot Info fields**

Add focused tests in `internal/aggregator/userinfo/module_test.go`:

```go
func TestHandleBlockPin_StoresV3BotInfoPaths(t *testing.T) {
	agg := setupTestAggregator(t)
	metaid := "meta_v3_info"
	address := "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"
	global := idaddress.EncodeGlobalMetaId(address, "mvc")

	pins := []*aggregator.PinInscription{
		{Id: "init:i0", Path: "/", Operation: "init", MetaId: metaid, Address: address, ChainName: "mvc"},
		{Id: "llm-lower:i0", Path: "/info/llm", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"primaryProvider":"codex"}`)},
		{Id: "persona:i0", Path: "/info/persona", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"role":"assistant","soul":"careful","goal":"help"}`)},
		{Id: "avatar:i0", Path: "/info/avatar", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentType: "image/png;binary", ContentBody: []byte{0x89, 0x50, 0x4e, 0x47}},
		{Id: "chat:i0", Path: "/info/chatpubkey", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte("046a")},
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Path, err)
		}
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile.LLM != `{"primaryProvider":"codex"}` || profile.LLMId != "llm-lower:i0" {
		t.Fatalf("lower-case llm not stored: %#v", profile)
	}
	if profile.Persona != `{"role":"assistant","soul":"careful","goal":"help"}` || profile.PersonaId != "persona:i0" {
		t.Fatalf("persona not stored: %#v", profile)
	}
	if profile.AvatarId != "avatar:i0" || profile.AvatarContentType != "image/png;binary" {
		t.Fatalf("avatar content type not stored: %#v", profile)
	}
	if profile.ChatPublicKey != "046a" || profile.ChatPublicKeyId != "chat:i0" {
		t.Fatalf("chatpubkey not stored: %#v", profile)
	}
}

func TestHandleBlockPin_AcceptsLegacyLLMCasing(t *testing.T) {
	agg := setupTestAggregator(t)
	metaid := "meta_legacy_llm"
	address := "1BoatSLRHtKNngkdXEeobR76b53LETtpyT"
	global := idaddress.EncodeGlobalMetaId(address, "mvc")

	for _, pin := range []*aggregator.PinInscription{
		{Id: "init:i0", Path: "/", Operation: "init", MetaId: metaid, Address: address, ChainName: "mvc"},
		{Id: "llm-legacy:i0", Path: "/info/LLM", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"primaryProvider":"legacy"}`)},
	} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatal(err)
		}
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatal(err)
	}
	if profile.LLM != `{"primaryProvider":"legacy"}` || profile.LLMId != "llm-legacy:i0" {
		t.Fatalf("legacy /info/LLM not stored: %#v", profile)
	}
}

func TestHandleBlockPin_ClearsV3BotInfoFields(t *testing.T) {
	agg := setupTestAggregator(t)
	metaid := "meta_clear_v3"
	address := "1dice8EMZmqKvrGE4Qc9bUFf9PX3xaYDp"
	global := idaddress.EncodeGlobalMetaId(address, "mvc")

	pins := []*aggregator.PinInscription{
		{Id: "init:i0", Path: "/", Operation: "init", MetaId: metaid, Address: address, ChainName: "mvc"},
		{Id: "persona:i0", Path: "/info/persona", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"role":"assistant"}`)},
		{Id: "persona-clear:i0", Path: "/info/persona", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: nil},
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatal(err)
		}
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Persona != "" || profile.PersonaId != "" {
		t.Fatalf("cleared persona should be empty: %#v", profile)
	}
}

func TestHandleMempoolPin_StoresV3BotInfoPaths(t *testing.T) {
	agg := setupTestAggregator(t)
	metaid := "meta_mempool_v3"
	address := "1CounterpartyXXXXXXXXXXXXXXXUWLpVr"
	global := idaddress.EncodeGlobalMetaId(address, "mvc")

	if _, err := agg.HandleBlockPin(&aggregator.PinInscription{
		Id: "init:i0", Path: "/", Operation: "init", MetaId: metaid, Address: address, ChainName: "mvc",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := agg.HandleMempoolPin(&aggregator.PinInscription{
		Id: "persona:m0", Path: "/info/persona", Operation: "create", MetaId: metaid, Address: address, ChainName: "mvc", ContentBody: []byte(`{"role":"pending"}`),
	}); err != nil {
		t.Fatal(err)
	}

	profile, err := agg.LookupByGlobalMetaId(global)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Persona != `{"role":"pending"}` || profile.PersonaId != "persona:m0" {
		t.Fatalf("mempool persona not visible: %#v", profile)
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/userinfo -run 'TestHandleBlockPin_StoresV3BotInfoPaths|TestHandleBlockPin_AcceptsLegacyLLMCasing|TestHandleBlockPin_ClearsV3BotInfoFields|TestHandleMempoolPin_StoresV3BotInfoPaths' -count=1
```

Expected: FAIL because `Persona`, `PersonaId`, `AvatarContentType`, lower-case `/info/llm`, and clear semantics are not fully implemented.

- [ ] **Step 3: Implement the read-model fields**

In `internal/aggregator/userinfo/module.go`, extend `UserProfile`:

```go
AvatarContentType string `json:"avatarContentType,omitempty"`
Persona           string `json:"persona,omitempty"`
PersonaId         string `json:"personaId,omitempty"`
```

Add a path normalizer near existing helper functions:

```go
func normalizeInfoPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimRight(path, "/")
	if strings.HasPrefix(strings.ToLower(path), "/info/") {
		return strings.ToLower(path)
	}
	return path
}
```

Use the normalized path for the `/info/*` switch in `HandleBlockPin`:

```go
path := normalizeInfoPath(pin.Path)
body := string(pin.ContentBody)
cleared := len(pin.ContentBody) == 0
```

Add or update cases:

```go
case path == "/info/avatar":
	if cleared {
		profile.Avatar = ""
		profile.AvatarId = ""
		profile.AvatarContentType = ""
	} else {
		profile.Avatar = "/content/" + pin.Id
		profile.AvatarId = pin.Id
		profile.AvatarContentType = pin.ContentType
	}
case path == "/info/llm":
	if cleared {
		profile.LLM = ""
		profile.LLMId = ""
	} else {
		profile.LLM = body
		profile.LLMId = pin.Id
	}
case path == "/info/persona":
	if cleared {
		profile.Persona = ""
		profile.PersonaId = ""
	} else {
		profile.Persona = body
		profile.PersonaId = pin.Id
	}
case path == "/info/homepage":
	if cleared {
		profile.Homepage = ""
		profile.HomepageId = ""
	} else {
		profile.Homepage = body
		profile.HomepageId = pin.Id
	}
case path == "/info/chatpubkey":
	if cleared {
		profile.ChatPublicKey = ""
		profile.ChatPublicKeyId = ""
	} else {
		profile.ChatPublicKey = body
		profile.ChatPublicKeyId = pin.Id
	}
case path == "/info/chatskills":
	if cleared {
		profile.ChatSkills = ""
		profile.ChatSkillsId = ""
	} else {
		profile.ChatSkills = body
		profile.ChatSkillsId = pin.Id
	}
```

Keep existing v2 fields `/info/role`, `/info/soul`, `/info/goal`, and `/info/background` working under the lower-case switch.

- [ ] **Step 4: Update backfill path list**

In `internal/aggregator/userinfo/backfill.go`, keep the legacy path and add canonical v3 paths:

```go
"/info/LLM",
"/info/llm",
"/info/persona",
```

- [ ] **Step 5: Run focused userinfo tests**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/userinfo -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit and post buzz**

```bash
git status --short
git add internal/aggregator/userinfo/module.go internal/aggregator/userinfo/backfill.go internal/aggregator/userinfo/module_test.go
git commit -m "feat: extend userinfo bot info read model"
```

Post a `metabot-post-buzz` development journal entry with the commit hash, files changed, and `CGO_ENABLED=0 go test ./internal/aggregator/userinfo -count=1` result.

## Task 2: Add V3 Query Selection and Response Types

**Files:**

- Modify: `internal/aggregator/bothomepage/query.go`
- Modify: `internal/aggregator/bothomepage/query_test.go`
- Create: `internal/aggregator/bothomepage/types_v3.go`
- Modify: `internal/aggregator/bothomepage/api.go`

- [ ] **Step 1: Write failing query parser tests**

Add tests in `internal/aggregator/bothomepage/query_test.go`:

```go
func TestParseOptions_SelectsV3ByVersion(t *testing.T) {
	values := url.Values{}
	values.Set("version", "v3")
	opts, err := ParseOptions(values)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if opts.Version != "v3" {
		t.Fatalf("Version = %q, want v3", opts.Version)
	}
	if !opts.IncludePresence || !opts.IncludeSections || !opts.IncludeServices || !opts.IncludeBuzzes || !opts.IncludeMetaApps {
		t.Fatalf("v3 defaults should include presence and sections: %#v", opts)
	}
	if opts.IncludeSkills || opts.IncludeProofs {
		t.Fatalf("v3 should not include skills or proofs: %#v", opts)
	}
}

func TestParseOptions_SelectsV3BySchemaVersion(t *testing.T) {
	values := url.Values{}
	values.Set("schemaVersion", "botHomepage.v3")
	opts, err := ParseOptions(values)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if opts.Version != "v3" {
		t.Fatalf("Version = %q, want v3", opts.Version)
	}
}

func TestParseOptions_V3SectionToggles(t *testing.T) {
	values := url.Values{}
	values.Set("version", "v3")
	values.Set("includeSections", "false")
	values.Set("includePresence", "false")
	values.Set("includeServices", "false")
	values.Set("includeBuzzes", "false")
	values.Set("includeMetaApps", "false")
	values.Set("includeInactiveServices", "true")
	opts, err := ParseOptions(values)
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	if opts.IncludeSections || opts.IncludePresence || opts.IncludeServices || opts.IncludeBuzzes || opts.IncludeMetaApps {
		t.Fatalf("v3 toggles not applied: %#v", opts)
	}
	if !opts.IncludeInactiveServices {
		t.Fatalf("includeInactiveServices not applied: %#v", opts)
	}
}

func TestParseOptions_DefaultAndV2Compatibility(t *testing.T) {
	defaultOpts, err := ParseOptions(url.Values{})
	if err != nil {
		t.Fatalf("ParseOptions default: %v", err)
	}
	if defaultOpts.Version != "" {
		t.Fatalf("default Version = %q, want empty v1 selector", defaultOpts.Version)
	}

	v2Values := url.Values{}
	v2Values.Set("schemaVersion", "botHomepage.v2")
	v2Opts, err := ParseOptions(v2Values)
	if err != nil {
		t.Fatalf("ParseOptions v2: %v", err)
	}
	if v2Opts.Version != "v2" || !v2Opts.IncludeSkills || !v2Opts.IncludeProofs {
		t.Fatalf("v2 compatibility changed: %#v", v2Opts)
	}
}
```

- [ ] **Step 2: Run query tests to verify failure**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage -run 'TestParseOptions_SelectsV3|TestParseOptions_V3|TestParseOptions_DefaultAndV2Compatibility' -count=1
```

Expected: FAIL because v3 parsing does not exist yet.

- [ ] **Step 3: Implement v3 query parsing**

Update `parseVersion` in `internal/aggregator/bothomepage/query.go`:

```go
func parseVersion(values url.Values) string {
	switch strings.ToLower(strings.TrimSpace(values.Get("version"))) {
	case "v3":
		return "v3"
	case "v2":
		return "v2"
	}
	switch strings.TrimSpace(values.Get("schemaVersion")) {
	case schemaVersionV3:
		return "v3"
	case schemaVersionV2:
		return "v2"
	default:
		return ""
	}
}
```

Add `schemaVersionV3` as a constant in the Bot Homepage package:

```go
const schemaVersionV3 = "botHomepage.v3"
```

In `ParseOptions`, apply v3 defaults after `opts.Version` is known:

```go
if opts.Version == "v3" {
	opts.IncludePresence = true
	opts.IncludeSections = true
	opts.IncludeServices = true
	opts.IncludeMetaApps = true
	opts.IncludeBuzzes = true
	opts.IncludeSkills = false
	opts.IncludeProofs = false
	opts.ServiceSize = homepageSectionLimit
}
```

Parse v3-supported toggles only:

```go
if opts.Version == "v3" {
	if opts.IncludePresence, err = parseBool(values, "includePresence", opts.IncludePresence); err != nil {
		return opts, err
	}
	if opts.IncludeSections, err = parseBool(values, "includeSections", opts.IncludeSections); err != nil {
		return opts, err
	}
	if opts.IncludeServices, err = parseBool(values, "includeServices", opts.IncludeServices); err != nil {
		return opts, err
	}
	if opts.IncludeBuzzes, err = parseBool(values, "includeBuzzes", opts.IncludeBuzzes); err != nil {
		return opts, err
	}
	if opts.IncludeMetaApps, err = parseBool(values, "includeMetaApps", opts.IncludeMetaApps); err != nil {
		return opts, err
	}
	if opts.IncludeInactiveServices, err = parseBool(values, "includeInactiveServices", opts.IncludeInactiveServices); err != nil {
		return opts, err
	}
	opts.ChainName = ""
	return opts, nil
}
```

Leave existing v1/v2 parsing below this branch.

- [ ] **Step 4: Define v3 response structs**

Create `internal/aggregator/bothomepage/types_v3.go`:

```go
package bothomepage

type DataV3 struct {
	SchemaVersion string      `json:"schemaVersion"`
	Identity      IdentityV3  `json:"identity"`
	Profile       ProfileV3   `json:"profile"`
	Presence      Presence    `json:"presence"`
	Sections      []SectionV3 `json:"sections"`
	Warnings      []string    `json:"warnings"`
}

type IdentityV3 struct {
	GlobalMetaId string `json:"globalMetaId"`
	LegacyMetaId string `json:"legacyMetaId,omitempty"`
	Display      string `json:"display"`
}

type ProfileV3 struct {
	Name       string       `json:"name"`
	Avatar     *AvatarV3    `json:"avatar"`
	Bio        string       `json:"bio"`
	ChatPubkey string       `json:"chatPubkey,omitempty"`
	LLM        *JSONBlockV3 `json:"llm"`
	Persona    *JSONBlockV3 `json:"persona"`
	Homepage   *JSONBlockV3 `json:"homepage"`
	Pins       ProfilePinsV3 `json:"pins"`
}

type AvatarV3 struct {
	PinId       string `json:"pinId"`
	ContentType string `json:"contentType"`
}

type JSONBlockV3 struct {
	PinId   string         `json:"pinId"`
	Payload map[string]any `json:"payload"`
}

type ProfilePinsV3 struct {
	Name       string `json:"name,omitempty"`
	Bio        string `json:"bio,omitempty"`
	ChatPubkey string `json:"chatPubkey,omitempty"`
}

type SectionV3 struct {
	ID           string          `json:"id"`
	ProtocolPath string          `json:"protocolPath"`
	Page         SectionPageV3   `json:"page"`
	Items        []SectionItemV3 `json:"items"`
}

type SectionPageV3 struct {
	Limit   int  `json:"limit"`
	Count   int  `json:"count"`
	HasMore bool `json:"hasMore"`
}

type SectionItemV3 struct {
	PinId        string            `json:"pinId"`
	ProtocolPath string            `json:"protocolPath"`
	Timestamp    int64             `json:"timestamp"`
	Data         SectionItemDataV3 `json:"data"`
}

type SectionItemDataV3 struct {
	Payload any `json:"payload,omitempty"`
}
```

- [ ] **Step 5: Add API branch for v3**

In `internal/aggregator/bothomepage/api.go`, branch after parsing options:

```go
if opts.Version == "v3" {
	data, err := a.BuildV3(globalMetaId, opts)
	if err != nil {
		respondBuildError(c, err)
		return
	}
	api.RespSuccess(c, data)
	return
}
```

Keep existing v1/v2 handling unchanged.

- [ ] **Step 6: Run focused tests**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage -run 'TestParseOptions' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit and post buzz**

```bash
git status --short
git add internal/aggregator/bothomepage/query.go internal/aggregator/bothomepage/query_test.go internal/aggregator/bothomepage/types_v3.go internal/aggregator/bothomepage/api.go
git commit -m "feat: add bot homepage v3 query types"
```

Post a `metabot-post-buzz` development journal entry with the commit hash, parser behavior, files changed, and focused test result.

## Task 3: Build V3 Identity and Profile

**Files:**

- Modify: `internal/aggregator/bothomepage/userinfo_adapter.go`
- Create: `internal/aggregator/bothomepage/build_v3.go`
- Modify: `internal/aggregator/bothomepage/build_test.go`

- [ ] **Step 1: Write failing profile build tests**

Add tests in `internal/aggregator/bothomepage/build_test.go`:

```go
func TestBuildV3ProfileUsesRawBotInfoBlocks(t *testing.T) {
	builder := setupTestBuilder(t)
	builder.profileLookup = &fakeProfileLookup{
		byGlobal: map[string]*ProfileSnapshot{
			"idqbot": {
				GlobalMetaId:       "idqbot",
				MetaId:             "legacy-meta",
				Name:               "AI_Sunny",
				NameId:             "name:i0",
				Bio:                "Public bio",
				BioId:              "bio:i0",
				ChatPublicKey:      "046a",
				ChatPublicKeyId:    "chat:i0",
				AvatarId:           "avatar:i0",
				AvatarContentType:  "image/png;binary",
				LLM:                `{"primaryProvider":"codex","fallbackProvider":"claude-code"}`,
				LLMId:              "llm:i0",
				Persona:            `{"role":"assistant","soul":"careful","goal":"help"}`,
				PersonaId:          "persona:i0",
				Homepage:           `{"uri":"metaapp://abc","renderer":"metaapp","contentType":"application/vnd.metaapp"}`,
				HomepageId:         "homepage:i0",
			},
		},
	}

	data, err := builder.BuildV3("idqbot", DefaultOptions())
	if err != nil {
		t.Fatalf("BuildV3: %v", err)
	}
	if data.SchemaVersion != schemaVersionV3 {
		t.Fatalf("schemaVersion = %q", data.SchemaVersion)
	}
	if data.Identity.GlobalMetaId != "idqbot" || data.Identity.LegacyMetaId != "legacy-meta" {
		t.Fatalf("identity mismatch: %#v", data.Identity)
	}
	if data.Profile.Avatar == nil || data.Profile.Avatar.PinId != "avatar:i0" || data.Profile.Avatar.ContentType != "image/png" {
		t.Fatalf("avatar mismatch: %#v", data.Profile.Avatar)
	}
	if data.Profile.Pins.Name != "name:i0" || data.Profile.Pins.Bio != "bio:i0" || data.Profile.Pins.ChatPubkey != "chat:i0" {
		t.Fatalf("pins mismatch: %#v", data.Profile.Pins)
	}
	if data.Profile.LLM == nil || data.Profile.LLM.PinId != "llm:i0" || data.Profile.LLM.Payload["primaryProvider"] != "codex" {
		t.Fatalf("llm mismatch: %#v", data.Profile.LLM)
	}
	if data.Profile.Persona == nil || data.Profile.Persona.PinId != "persona:i0" || data.Profile.Persona.Payload["role"] != "assistant" {
		t.Fatalf("persona mismatch: %#v", data.Profile.Persona)
	}
	if data.Profile.Homepage == nil || data.Profile.Homepage.PinId != "homepage:i0" || data.Profile.Homepage.Payload["uri"] != "metaapp://abc" {
		t.Fatalf("homepage mismatch: %#v", data.Profile.Homepage)
	}
	if len(data.Warnings) != 0 {
		t.Fatalf("warnings = %#v", data.Warnings)
	}
}

func TestBuildV3InvalidJSONBlocksReturnNullWithWarnings(t *testing.T) {
	builder := setupTestBuilder(t)
	builder.profileLookup = &fakeProfileLookup{
		byGlobal: map[string]*ProfileSnapshot{
			"idqbot": {
				GlobalMetaId: "idqbot",
				MetaId:       "legacy-meta",
				LLM:          `{bad`,
				LLMId:        "llm:i0",
				Persona:      `{bad`,
				PersonaId:    "persona:i0",
				Homepage:     `{bad`,
				HomepageId:   "homepage:i0",
			},
		},
	}

	data, err := builder.BuildV3("idqbot", DefaultOptions())
	if err != nil {
		t.Fatalf("BuildV3: %v", err)
	}
	if data.Profile.LLM != nil || data.Profile.Persona != nil || data.Profile.Homepage != nil {
		t.Fatalf("invalid JSON blocks should be nil: %#v", data.Profile)
	}
	want := []string{
		"invalid JSON in /info/llm",
		"invalid JSON in /info/persona",
		"invalid JSON in /info/homepage",
	}
	if !reflect.DeepEqual(data.Warnings, want) {
		t.Fatalf("warnings = %#v, want %#v", data.Warnings, want)
	}
}

func TestBuildV3TopLevelShapeExcludesV2Fields(t *testing.T) {
	builder := setupTestBuilder(t)
	builder.profileLookup = &fakeProfileLookup{
		byGlobal: map[string]*ProfileSnapshot{
			"idqbot": {GlobalMetaId: "idqbot", MetaId: "legacy-meta"},
		},
	}

	data, err := builder.BuildV3("idqbot", DefaultOptions())
	if err != nil {
		t.Fatalf("BuildV3: %v", err)
	}
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"globalMetaId", "canonical", "persona", "homepage", "services", "actions", "proofs", "source", "resolvedAt"} {
		if _, ok := got[forbidden]; ok {
			t.Fatalf("forbidden key %q present in %#v", forbidden, got)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify failure**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage -run 'TestBuildV3ProfileUsesRawBotInfoBlocks|TestBuildV3InvalidJSONBlocksReturnNullWithWarnings|TestBuildV3TopLevelShapeExcludesV2Fields' -count=1
```

Expected: FAIL because `BuildV3` and adapter fields do not exist yet.

- [ ] **Step 3: Extend the userinfo adapter**

In `internal/aggregator/bothomepage/userinfo_adapter.go`, add fields to `ProfileSnapshot`:

```go
AvatarContentType string
Persona           string
PersonaId         string
```

Map them from `userinfo.UserProfile`:

```go
AvatarContentType: p.AvatarContentType,
Persona:           p.Persona,
PersonaId:         p.PersonaId,
```

- [ ] **Step 4: Implement v3 profile helpers**

In `internal/aggregator/bothomepage/build_v3.go`, add:

```go
func (a *Aggregator) BuildV3(requestGlobalMetaId string, opts Options) (*DataV3, error) {
	canonical, profile, err := a.resolveHomepageProfile(requestGlobalMetaId)
	if err != nil {
		return nil, err
	}

	warnings := make([]string, 0)
	data := &DataV3{
		SchemaVersion: schemaVersionV3,
		Identity: IdentityV3{
			GlobalMetaId: canonical.GlobalMetaId,
			LegacyMetaId: profile.MetaId,
			Display:      abbreviateGlobalMetaId(canonical.GlobalMetaId),
		},
		Profile:  buildProfileV3(profile, &warnings),
		Presence: Presence{State: PresenceUnknown, UpdatedAt: nil, Source: ""},
		Sections: []SectionV3{},
		Warnings: warnings,
	}
	if opts.IncludePresence {
		data.Presence = a.resolvePresence(canonical)
	}
	if opts.IncludeSections {
		sections, sectionWarnings := a.loadSectionsV3(canonical, opts)
		data.Sections = sections
		data.Warnings = append(data.Warnings, sectionWarnings...)
	}
	return data, nil
}
```

If the existing profile resolution logic is private inside `Build`, extract only the shared lookup portion into:

```go
func (a *Aggregator) resolveHomepageProfile(requestGlobalMetaId string) (CanonicalIdentity, ProfileSnapshot, error)
```

Keep the existing `Build` behavior byte-for-byte equivalent for v1/v2.

Add JSON block parsing:

```go
func buildJSONBlockV3(path, raw, pinId string, warnings *[]string) *JSONBlockV3 {
	if strings.TrimSpace(raw) == "" || strings.TrimSpace(pinId) == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		*warnings = append(*warnings, "invalid JSON in "+path)
		return nil
	}
	return &JSONBlockV3{PinId: pinId, Payload: payload}
}
```

Add avatar content type cleanup:

```go
func avatarContentTypeV3(contentType string) string {
	contentType = strings.TrimSpace(contentType)
	contentType = strings.TrimSuffix(contentType, ";binary")
	return contentType
}
```

Build the profile:

```go
func buildProfileV3(profile ProfileSnapshot, warnings *[]string) ProfileV3 {
	out := ProfileV3{
		Name:       profile.Name,
		Bio:        profile.Bio,
		ChatPubkey: profile.ChatPublicKey,
		LLM:        buildJSONBlockV3("/info/llm", profile.LLM, profile.LLMId, warnings),
		Persona:    buildJSONBlockV3("/info/persona", profile.Persona, profile.PersonaId, warnings),
		Homepage:   buildJSONBlockV3("/info/homepage", profile.Homepage, profile.HomepageId, warnings),
		Pins: ProfilePinsV3{
			Name:       profile.NameId,
			Bio:        profile.BioId,
			ChatPubkey: profile.ChatPublicKeyId,
		},
	}
	if profile.AvatarId != "" {
		out.Avatar = &AvatarV3{
			PinId:       profile.AvatarId,
			ContentType: avatarContentTypeV3(profile.AvatarContentType),
		}
	}
	return out
}
```

- [ ] **Step 5: Run focused build tests**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage -run 'TestBuildV3ProfileUsesRawBotInfoBlocks|TestBuildV3InvalidJSONBlocksReturnNullWithWarnings|TestBuildV3TopLevelShapeExcludesV2Fields' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit and post buzz**

```bash
git status --short
git add internal/aggregator/bothomepage/userinfo_adapter.go internal/aggregator/bothomepage/build_v3.go internal/aggregator/bothomepage/build_test.go
git commit -m "feat: build bot homepage v3 profile"
```

Post a `metabot-post-buzz` development journal entry with the commit hash, profile fields covered, warnings behavior, and focused test result.

## Task 4: Build V3 Sections

**Files:**

- Modify: `internal/aggregator/bothomepage/build_v3.go`
- Modify: `internal/aggregator/bothomepage/build_test.go`
- Modify: `internal/aggregator/skillservice/list.go` only if raw service declaration payloads are not available through the existing list item.
- Modify: `internal/aggregator/skillservice/list_test.go` only if `skillservice/list.go` changes.

- [ ] **Step 1: Write failing section tests**

Add tests in `internal/aggregator/bothomepage/build_test.go`:

```go
func TestBuildV3SectionsAreServicesBuzzesMetaapps(t *testing.T) {
	builder := setupTestBuilder(t)
	builder.profileLookup = &fakeProfileLookup{
		byGlobal: map[string]*ProfileSnapshot{
			"idqbot": {GlobalMetaId: "idqbot", MetaId: "legacy-meta"},
		},
	}
	builder.homepageServices = &fakeHomepageServiceLister{
		items: []skillservice.ServiceListItem{{
			CurrentPinId: "service-current:i0",
			ServiceName:  "topup",
			DisplayName:  "Top Up",
			Description:  "Mobile top up",
			UpdatedAt:    100,
		}},
	}
	builder.publishedContent = &fakePublishedContentLister{
		byPath: map[string]publishedcontent.ListResult{
			publishedcontent.PathSimpleBuzz: {
				Items: []publishedcontent.SectionItem{{
					CurrentPinId:    "buzz-current:i0",
					ProtocolPath:    publishedcontent.PathSimpleBuzz,
					CreatedAt:       200,
					UpdatedAt:       210,
					PayloadJSON:     map[string]any{"content": "hello"},
					PayloadExposed:  true,
				}},
			},
			publishedcontent.PathMetaApp: {
				Items: []publishedcontent.SectionItem{{
					CurrentPinId:    "metaapp-current:i0",
					ProtocolPath:    publishedcontent.PathMetaApp,
					CreatedAt:       300,
					UpdatedAt:       310,
					PayloadJSON:     map[string]any{"name": "Home"},
					PayloadExposed:  true,
				}},
			},
		},
	}

	data, err := builder.BuildV3("idqbot", DefaultOptions())
	if err != nil {
		t.Fatalf("BuildV3: %v", err)
	}
	gotIDs := make([]string, 0, len(data.Sections))
	for _, section := range data.Sections {
		gotIDs = append(gotIDs, section.ID)
	}
	wantIDs := []string{"services", "buzzes", "metaapps"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("sections = %#v, want %#v", gotIDs, wantIDs)
	}
	if data.Sections[0].ProtocolPath != "/protocols/skill-service" {
		t.Fatalf("services protocolPath = %q", data.Sections[0].ProtocolPath)
	}
	if data.Sections[1].Items[0].PinId != "buzz-current:i0" || data.Sections[2].Items[0].PinId != "metaapp-current:i0" {
		t.Fatalf("published content pin ids mismatch: %#v", data.Sections)
	}
}

func TestBuildV3SectionItemsAreMinimal(t *testing.T) {
	builder := setupTestBuilder(t)
	builder.profileLookup = &fakeProfileLookup{
		byGlobal: map[string]*ProfileSnapshot{
			"idqbot": {GlobalMetaId: "idqbot", MetaId: "legacy-meta"},
		},
	}
	builder.publishedContent = &fakePublishedContentLister{
		byPath: map[string]publishedcontent.ListResult{
			publishedcontent.PathSimpleBuzz: {
				Items: []publishedcontent.SectionItem{{
					CurrentPinId:   "buzz-current:i0",
					ProtocolPath:   publishedcontent.PathSimpleBuzz,
					CreatedAt:      200,
					UpdatedAt:      210,
					PayloadText:    "hello",
					PayloadExposed: true,
				}},
			},
		},
	}

	data, err := builder.BuildV3("idqbot", DefaultOptions())
	if err != nil {
		t.Fatalf("BuildV3: %v", err)
	}
	raw, err := json.Marshal(data.Sections[1].Items[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"data", "pinId", "protocolPath", "timestamp"}
	gotKeys := make([]string, 0, len(got))
	for key := range got {
		gotKeys = append(gotKeys, key)
	}
	sort.Strings(gotKeys)
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("item keys = %#v, want %#v", gotKeys, wantKeys)
	}
	for _, forbidden := range []string{"sourcePinId", "currentPinId", "createdAt", "updatedAt", "chainName", "publisher", "proof", "service", "payloadJson", "payloadText", "payloadExposed"} {
		if _, ok := got[forbidden]; ok {
			t.Fatalf("forbidden key %q present in %#v", forbidden, got)
		}
	}
}
```

- [ ] **Step 2: Run section tests to verify failure**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage -run 'TestBuildV3SectionsAreServicesBuzzesMetaapps|TestBuildV3SectionItemsAreMinimal' -count=1
```

Expected: FAIL because v3 section loading does not exist yet.

- [ ] **Step 3: Implement v3 section loading**

In `internal/aggregator/bothomepage/build_v3.go`, add:

```go
func (a *Aggregator) loadSectionsV3(canonical CanonicalIdentity, opts Options) ([]SectionV3, []string) {
	sections := make([]SectionV3, 0, 3)
	warnings := make([]string, 0)
	if opts.IncludeServices {
		section, err := a.loadServicesSectionV3(canonical, opts)
		if err != nil {
			warnings = append(warnings, "services section source unavailable")
		}
		sections = append(sections, section)
	}
	if opts.IncludeBuzzes {
		section, err := a.loadPublishedContentSectionV3(canonical, opts, "buzzes", publishedcontent.PathSimpleBuzz)
		if err != nil {
			warnings = append(warnings, "buzzes section source unavailable")
		}
		sections = append(sections, section)
	}
	if opts.IncludeMetaApps {
		section, err := a.loadPublishedContentSectionV3(canonical, opts, "metaapps", publishedcontent.PathMetaApp)
		if err != nil {
			warnings = append(warnings, "metaapps section source unavailable")
		}
		sections = append(sections, section)
	}
	return sections, warnings
}
```

For services, use the same provider-global service list as v2:

```go
func (a *Aggregator) loadServicesSectionV3(canonical CanonicalIdentity, opts Options) (SectionV3, error) {
	section := emptySectionV3("services", skillservice.ProtocolPath, homepageSectionLimit)
	if a.homepageServices == nil {
		return section, nil
	}
	result, err := a.homepageServices.ListHomepageByProvider(skillservice.HomepageListParams{
		ProviderGlobalMetaId: canonical.GlobalMetaId,
		Size:                 homepageSectionReadSize,
		IncludeInactive:      opts.IncludeInactiveServices,
	})
	if err != nil {
		return section, err
	}
	section.Page.HasMore = result.HasMore
	for _, item := range result.Items {
		if len(section.Items) >= homepageSectionLimit {
			break
		}
		section.Items = append(section.Items, serviceSectionItemV3(item))
	}
	section.Page.Count = len(section.Items)
	return section, nil
}
```

Map service payloads with an allow-list:

```go
func serviceSectionItemV3(item skillservice.ServiceListItem) SectionItemV3 {
	return SectionItemV3{
		PinId:        firstNonEmpty(item.CurrentPinId, item.SourceServicePinId),
		ProtocolPath: skillservice.ProtocolPath,
		Timestamp:    item.UpdatedAt,
		Data: SectionItemDataV3{
			Payload: map[string]any{
				"serviceName":    item.ServiceName,
				"displayName":    item.DisplayName,
				"description":    item.Description,
				"serviceIcon":    item.ServiceIcon,
				"providerSkill":  item.ProviderSkill,
				"outputType":     item.OutputType,
				"price":          item.Price,
				"currency":       item.Currency,
				"settlementKind": item.SettlementKind,
				"paymentAddress": item.PaymentAddress,
			},
		},
	}
}
```

If `item.ServiceIcon` is resolver-expanded rather than the raw declaration value, adjust `skillservice` in this task so v3 receives the raw declaration field or omit `serviceIcon` from v3 payload until raw data is available.

For buzzes and metaapps:

```go
func (a *Aggregator) loadPublishedContentSectionV3(canonical CanonicalIdentity, opts Options, id, protocolPath string) (SectionV3, error) {
	section := emptySectionV3(id, protocolPath, homepageSectionLimit)
	result, err := a.loadPublishedContentByCanonicalIdentity(canonical, Options{Version: "v3"}, protocolPath)
	if err != nil {
		return section, err
	}
	section.Page.HasMore = result.HasMore
	for _, item := range result.Items {
		if len(section.Items) >= homepageSectionLimit {
			break
		}
		section.Items = append(section.Items, publishedContentSectionItemV3(item))
	}
	section.Page.Count = len(section.Items)
	return section, nil
}

func publishedContentSectionItemV3(item publishedcontent.SectionItem) SectionItemV3 {
	return SectionItemV3{
		PinId:        firstNonEmpty(item.CurrentPinId, item.SourcePinId),
		ProtocolPath: item.ProtocolPath,
		Timestamp:    publishedContentItemSortTimestamp(item),
		Data: SectionItemDataV3{
			Payload: sectionItemData(item),
		},
	}
}
```

Do not include `publishedcontent.PathMetaBotSkill` in v3.

- [ ] **Step 4: Run focused section tests**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage -run 'TestBuildV3SectionsAreServicesBuzzesMetaapps|TestBuildV3SectionItemsAreMinimal' -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit and post buzz**

```bash
git status --short
git add internal/aggregator/bothomepage/build_v3.go internal/aggregator/bothomepage/build_test.go
git diff --cached --name-only
git commit -m "feat: add bot homepage v3 sections"
```

If `skillservice` files changed in this task, include only those files that were changed and tested:

```bash
git add internal/aggregator/skillservice/list.go internal/aggregator/skillservice/list_test.go
```

Post a `metabot-post-buzz` development journal entry with the commit hash, section sources, item mapping, and test result.

## Task 5: Add Route-Level and Mempool Coverage

**Files:**

- Modify: `internal/aggregator/bothomepage/build_test.go`
- Modify: `internal/api/router_test.go` if route envelope coverage is missing
- Modify: `internal/aggregator/skillservice/*_test.go` only if mempool service visibility needs additional proof
- Modify: `internal/aggregator/publishedcontent/*_test.go` only if mempool buzz/metaapp visibility needs additional proof

- [ ] **Step 1: Write compatibility tests**

Add or update tests so they prove:

```text
default request -> v1 shape
version=v2 -> botHomepage.v2 shape
schemaVersion=botHomepage.v2 -> botHomepage.v2 shape
version=v3 -> botHomepage.v3 shape
schemaVersion=botHomepage.v3 -> botHomepage.v3 shape
```

In the v3 route/envelope test, assert:

```go
if got.Code != 0 {
	t.Fatalf("code = %d, want 0", got.Code)
}
if got.Data.SchemaVersion != "botHomepage.v3" {
	t.Fatalf("schemaVersion = %q", got.Data.SchemaVersion)
}
```

- [ ] **Step 2: Write mempool visibility tests**

Add one integrated test path that seeds pending records through existing aggregator mempool handlers and then builds v3:

```text
userinfo.HandleMempoolPin(/info/persona)
userinfo.HandleMempoolPin(/info/llm)
userinfo.HandleMempoolPin(/info/homepage)
skillservice.HandleMempoolPin(/protocols/skill-service)
publishedcontent.HandleMempoolPin(/protocols/simplebuzz)
publishedcontent.HandleMempoolPin(/protocols/metaapp)
bothomepage.BuildV3(...)
```

Assert:

```text
profile.persona.pinId == pending persona pin id
profile.llm.pinId == pending llm pin id
profile.homepage.pinId == pending homepage pin id
sections.services has the pending service
sections.buzzes has the pending buzz
sections.metaapps has the pending metaapp
```

Use existing test helpers where possible. Do not add a new fake read model if an existing package-level mempool test can exercise the real folding path.

- [ ] **Step 3: Run route and mempool tests**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage ./internal/aggregator/userinfo ./internal/aggregator/skillservice ./internal/aggregator/publishedcontent ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 4: Commit and post buzz**

```bash
git status --short
git add internal/aggregator/bothomepage/build_test.go internal/api/router_test.go internal/aggregator/skillservice internal/aggregator/publishedcontent
git diff --cached --name-only
git commit -m "chore: cover bot homepage v3 routing"
```

Before staging package directories, inspect `git diff --name-status` and remove any unrelated files from the index.

Post a `metabot-post-buzz` development journal entry with the commit hash, route compatibility coverage, mempool coverage, and full focused test result.

## Task 6: Final Verification and Review Handoff

**Files:**

- No planned source changes.
- Modify docs only if implementation reveals a contract contradiction and the user explicitly approves the contract update.

- [ ] **Step 1: Run focused verification**

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage ./internal/aggregator/userinfo ./internal/aggregator/skillservice ./internal/aggregator/publishedcontent ./internal/api -count=1
git diff --check
```

Expected: both commands PASS.

- [ ] **Step 2: Run broad verification if the implementation touched shared paths**

```bash
CGO_ENABLED=0 go test ./...
```

Expected: PASS.

- [ ] **Step 3: Inspect final diff**

```bash
git status --short
git diff --stat main...HEAD
git diff --name-status main...HEAD
```

Expected: only v3 implementation files, focused tests, and any approved docs changes are present.

- [ ] **Step 4: Dispatch final review**

Ask the review session to verify:

```text
v1 default compatibility
v2 explicit compatibility
v3 top-level forbidden fields absent
v3 section item forbidden fields absent
raw JSON payload blocks are not normalized
mempool records are visible
/info/LLM and /info/llm both hydrate profile.llm
avatar contentType has no ;binary suffix
services section does not leak provider profile, rating, action, chain identity, proofs, or resolver URLs
```

Do not merge into `main` until review passes and the user requests merge. When merging completed work into `main`, use:

```bash
git switch main
git merge --no-ff codex/bot-homepage-v3
```

## Risk Register

- **Mempool visibility:** v3 is only correct if `userinfo`, `skillservice`, and `publishedcontent` pending pins enter the same latest-record paths as confirmed pins. Tests must seed mempool records through real handlers.
- **`/info/llm` casing:** current code may only recognize legacy `/info/LLM`. Implementation must accept both `/info/llm` and `/info/LLM`, while v3 never exposes the legacy path casing.
- **`/info/persona` raw JSON:** v3 must read only `/info/persona`. It must not synthesize persona from v2 compatibility fields such as `/info/role`, `/info/soul`, `/info/goal`, `/info/chatSkills`, or legacy bio parsing.
- **`chatPubkey`:** userinfo currently uses `ChatPublicKey` internally and may serialize as `chatpubkey`. v3 must output `chatPubkey` and `profile.pins.chatPubkey`.
- **Avatar content type:** current userinfo may only store `/content/<pinId>`. v3 must output no URL and must preserve enough content type to return MIME without `;binary`.
- **Service payload leakage:** current service list items may include provider hydration, rating, action verdicts, chain fields, or resolver-expanded `serviceIcon`. v3 must use a strict allow-list and avoid Web2 resolver details.
- **Timestamp semantics:** publishedcontent currently has protocol-specific ordering behavior. v3 item `timestamp` must be the same timestamp used for homepage ordering/display.
- **Type leakage:** v3 must not reuse the v1/v2 `Data` struct because it contains forbidden fields.
- **Query compatibility:** v3-specific parsing must not change default v1 or explicit v2 parsing, especially `includeProofs`, `includeSkills`, `serviceSize`, and `chainName` behavior for older clients.

## Acceptance Checklist

- [ ] `GET /api/bot-homepage/globalmetaid/:id?version=v3` returns `data.schemaVersion == "botHomepage.v3"`.
- [ ] `GET /api/bot-homepage/globalmetaid/:id?schemaVersion=botHomepage.v3` returns `data.schemaVersion == "botHomepage.v3"`.
- [ ] Default route behavior remains v1.
- [ ] Explicit v2 behavior remains v2.
- [ ] v3 top-level keys are limited to `schemaVersion`, `identity`, `profile`, `presence`, `sections`, and `warnings`.
- [ ] v3 does not return top-level `proofs`, `source`, `actions`, or `services`.
- [ ] v3 does not return `chainName`, address, `sourcePinId`, `currentPinId`, `createdAt`, or `updatedAt`.
- [ ] `profile.llm.payload`, `profile.persona.payload`, and `profile.homepage.payload` are raw chain JSON objects when present.
- [ ] Invalid JSON blocks return `null` and add warnings.
- [ ] Cleared JSON blocks return `null`.
- [ ] `profile.chatPubkey` and `profile.pins.chatPubkey` are present when `/info/chatpubkey` exists.
- [ ] `profile.avatar` returns only `pinId` and MIME `contentType`.
- [ ] Sections are exactly `services`, `buzzes`, and `metaapps` when all section toggles are enabled.
- [ ] Section items are limited to `pinId`, `protocolPath`, `timestamp`, and `data`.
- [ ] Services section is non-empty when v2 top-level services can see visible provider services for the same Bot.
- [ ] Mempool `/info/persona`, `/info/llm`, `/info/homepage`, service, buzz, and metaapp records are visible through v3.

## Required Verification Commands

Run during implementation:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/userinfo -count=1
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage -count=1
CGO_ENABLED=0 go test ./internal/aggregator/skillservice ./internal/aggregator/publishedcontent -count=1
CGO_ENABLED=0 go test ./internal/api -count=1
```

Run before final review:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/bothomepage ./internal/aggregator/userinfo ./internal/aggregator/skillservice ./internal/aggregator/publishedcontent ./internal/api -count=1
git diff --check
```

Run before merge or deploy when the implementation touches shared paths:

```bash
CGO_ENABLED=0 go test ./...
```
