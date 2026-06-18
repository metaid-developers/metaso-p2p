# Bot Homepage v3 Chats Section Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `chats` section to `botHomepage.v3` that lists the requested Bot's latest outgoing simplemsg interactions.

**Architecture:** Keep private-chat storage ownership inside `internal/aggregator/privatechat`, expose a small homepage reader for outgoing interactions, and consume that reader from `internal/aggregator/bothomepage`. The v3 homepage builder will preserve the common item envelope while allowing `chats.items[i].data` to contain only `interactWith`.

**Tech Stack:** Go 1.26, Gin, Pebble, existing aggregator registry, existing `go test` package tests.

---

## File Structure

- Create `internal/aggregator/privatechat/homepage_interactions.go`
  - Defines the privatechat homepage interaction list params/result/item types.
  - Scans stored private-chat messages and returns outgoing simplemsg summaries.
- Create `internal/aggregator/privatechat/homepage_interactions_test.go`
  - Covers outgoing-only filtering, alias matching, descending order, five-item limit, and `hasMore`.
- Modify `internal/aggregator/bothomepage/build.go`
  - Adds the narrow `ChatInteractionLister` dependency interface.
- Modify `internal/aggregator/bothomepage/module.go`
  - Stores the chat interaction lister and exposes `SetChatInteractionLister`.
- Modify `internal/aggregator/bothomepage/types_v3.go`
  - Extends v3 item data so normal sections still emit `payload`, while chat items emit `interactWith`.
- Modify `internal/aggregator/bothomepage/build_v3.go`
  - Reorders sections to `services`, `metaapps`, `chats`, `buzzes`.
  - Adds `loadChatsSectionV3`.
- Modify `internal/aggregator/bothomepage/build_test.go`
  - Adds unit coverage for the new section and updates exact section order assertions.
- Modify `cmd/metaso-p2p/main.go`
  - Wires `privatechat` as the homepage chat interaction source.
- Modify `internal/api/router_test.go`
  - Mirrors production wiring and adds router-level coverage for outgoing simplemsg in v3.
- Modify `docs/specs/2026-06-17-bot-homepage-v3-api.md`
  - Updates the public v3 contract.
- Modify `docs/superpowers/specs/2026-06-17-bot-homepage-v3-implementation-spec.md`
  - Updates the internal implementation spec so it matches the accepted chats design.

---

### Task 1: Add PrivateChat Homepage Interaction Reader

**Files:**
- Create: `internal/aggregator/privatechat/homepage_interactions.go`
- Create: `internal/aggregator/privatechat/homepage_interactions_test.go`

- [ ] **Step 1: Write failing tests for outgoing homepage interactions**

Create `internal/aggregator/privatechat/homepage_interactions_test.go`:

```go
package privatechat

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

func TestListOutgoingHomepageInteractionsFiltersSortsAndLimits(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	for _, pin := range []*aggregator.PinInscription{
		simpleMsgPinForHomepage(t, "out-1:i0", "idqBot", "metaBot", "idqPeer1", 1001),
		simpleMsgPinForHomepage(t, "out-2:i0", "idqBot", "metaBot", "idqPeer2", 1002),
		simpleMsgPinForHomepage(t, "out-3:i0", "idqBot", "metaBot", "idqPeer3", 1003),
		simpleMsgPinForHomepage(t, "out-4:i0", "idqBot", "metaBot", "idqPeer4", 1004),
		simpleMsgPinForHomepage(t, "out-5:i0", "idqBot", "metaBot", "idqPeer5", 1005),
		simpleMsgPinForHomepage(t, "out-6:i0", "idqBot", "metaBot", "idqPeer6", 1006),
		simpleMsgPinForHomepage(t, "incoming:i0", "idqPeer7", "metaPeer7", "idqBot", 1007),
	} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "idqBot",
		MetaId:       "metaBot",
		Address:      "1BotAddress",
		Size:         5,
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions returned error: %v", err)
	}
	if got == nil {
		t.Fatal("ListOutgoingHomepageInteractions returned nil result")
	}
	if !got.HasMore {
		t.Fatalf("HasMore = false, want true")
	}
	if len(got.Items) != 5 {
		t.Fatalf("Items length = %d, want 5: %+v", len(got.Items), got.Items)
	}

	wantPinIDs := []string{"out-6:i0", "out-5:i0", "out-4:i0", "out-3:i0", "out-2:i0"}
	wantTargets := []string{"idqPeer6", "idqPeer5", "idqPeer4", "idqPeer3", "idqPeer2"}
	for i := range wantPinIDs {
		if got.Items[i].PinId != wantPinIDs[i] {
			t.Fatalf("Items[%d].PinId = %q, want %q; items=%+v", i, got.Items[i].PinId, wantPinIDs[i], got.Items)
		}
		if got.Items[i].ProtocolPath != HomepageSimpleMsgProtocolPath {
			t.Fatalf("Items[%d].ProtocolPath = %q, want %q", i, got.Items[i].ProtocolPath, HomepageSimpleMsgProtocolPath)
		}
		if got.Items[i].InteractWith != wantTargets[i] {
			t.Fatalf("Items[%d].InteractWith = %q, want %q", i, got.Items[i].InteractWith, wantTargets[i])
		}
		if got.Items[i].Timestamp == 0 {
			t.Fatalf("Items[%d].Timestamp = 0, want source timestamp", i)
		}
	}
}

func TestListOutgoingHomepageInteractionsUsesIdentityAliases(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakePrivateChatProfileLookup{
		byGlobalMetaId: map[string]*IdentityProfile{
			"idqBot": &IdentityProfile{MetaId: "metaBot", GlobalMetaId: "idqBot", Address: "1BotAddress"},
		},
		byMetaId: map[string]*IdentityProfile{
			"metaBot": &IdentityProfile{MetaId: "metaBot", GlobalMetaId: "idqBot", Address: "1BotAddress"},
		},
		byAddress: map[string]*IdentityProfile{
			"1BotAddress": &IdentityProfile{MetaId: "metaBot", GlobalMetaId: "idqBot", Address: "1BotAddress"},
		},
	})

	if err := agg.SavePrivateMessage(&PrivateMessage{
		FromGlobalMetaId: "idqBot",
		From:             "metaBot",
		FromAddress:      "1BotAddress",
		ToGlobalMetaId:   "idqPeer",
		To:               "idqPeer",
		PinId:            "alias-out:i0",
		Protocol:         "/private/chat/simplemsg",
		Timestamp:        2001,
	}); err != nil {
		t.Fatalf("SavePrivateMessage: %v", err)
	}

	got, err := agg.ListOutgoingHomepageInteractions(HomepageInteractionListParams{
		GlobalMetaId: "idqBot",
		Size:         5,
	})
	if err != nil {
		t.Fatalf("ListOutgoingHomepageInteractions returned error: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("Items length = %d, want 1: %+v", len(got.Items), got.Items)
	}
	if got.Items[0].PinId != "alias-out:i0" || got.Items[0].InteractWith != "idqPeer" {
		t.Fatalf("Items[0] = %+v, want alias-out interaction with idqPeer", got.Items[0])
	}
	if got.HasMore {
		t.Fatalf("HasMore = true, want false")
	}
}

func simpleMsgPinForHomepage(t *testing.T, pinID, fromGlobalMetaID, fromMetaID, toGlobalMetaID string, timestamp int64) *aggregator.PinInscription {
	t.Helper()
	return &aggregator.PinInscription{
		Id:            pinID,
		Path:          "/protocols/simplemsg",
		Operation:     "create",
		GlobalMetaId:  fromGlobalMetaID,
		CreateMetaId:  fromMetaID,
		MetaId:        fromMetaID,
		CreateAddress: "1BotAddress",
		Address:       "1BotAddress",
		ChainName:     "mvc",
		Timestamp:     timestamp,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        fromMetaID,
			To:          toGlobalMetaID,
			Content:     "encrypted body",
			ContentType: "text/plain",
			Encrypt:     "ecies",
		}),
	}
}
```

- [ ] **Step 2: Run the focused test and confirm it fails for missing API**

Run:

```bash
go test ./internal/aggregator/privatechat -run 'TestListOutgoingHomepageInteractions' -count=1
```

Expected: FAIL at compile time with undefined `HomepageInteractionListParams`, `HomepageSimpleMsgProtocolPath`, or `ListOutgoingHomepageInteractions`.

- [ ] **Step 3: Implement the privatechat reader**

Create `internal/aggregator/privatechat/homepage_interactions.go`:

```go
package privatechat

import (
	"encoding/json"
	"sort"
	"strings"
)

const HomepageSimpleMsgProtocolPath = "/protocols/simplemsg"

type HomepageInteractionListParams struct {
	GlobalMetaId string
	MetaId       string
	Address      string
	Size         int
}

type HomepageInteractionListResult struct {
	Items   []HomepageInteraction
	HasMore bool
}

type HomepageInteraction struct {
	PinId        string
	ProtocolPath string
	Timestamp    int64
	InteractWith string
}

func (a *Aggregator) ListOutgoingHomepageInteractions(params HomepageInteractionListParams) (*HomepageInteractionListResult, error) {
	size := params.Size
	if size <= 0 {
		size = 5
	}
	result := &HomepageInteractionListResult{Items: []HomepageInteraction{}}
	if a == nil || a.store == nil {
		return result, nil
	}

	aliases := a.homepageInteractionAliases(params)
	if len(aliases) == 0 {
		return result, nil
	}
	aliasSet := make(map[string]bool, len(aliases))
	for _, alias := range aliases {
		aliasSet[strings.ToLower(alias)] = true
	}

	seen := make(map[string]bool)
	rows := make([]HomepageInteraction, 0)
	err := a.store.ScanPrefix(namespace, []byte(pchatKeyConst), func(_, value []byte) error {
		var msg PrivateMessage
		if err := json.Unmarshal(value, &msg); err != nil {
			return nil
		}
		if !homepageIsSimpleMsg(msg.Protocol) {
			return nil
		}
		if !aliasSet[strings.ToLower(strings.TrimSpace(msg.From))] &&
			!aliasSet[strings.ToLower(strings.TrimSpace(msg.FromGlobalMetaId))] &&
			!aliasSet[strings.ToLower(strings.TrimSpace(msg.FromAddress))] {
			return nil
		}
		pinID := strings.TrimSpace(msg.PinId)
		interactWith := strings.TrimSpace(msg.To)
		if pinID == "" || interactWith == "" {
			return nil
		}
		if seen[pinID] {
			return nil
		}
		seen[pinID] = true
		rows = append(rows, HomepageInteraction{
			PinId:        pinID,
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    msg.Timestamp,
			InteractWith: interactWith,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Timestamp != rows[j].Timestamp {
			return rows[i].Timestamp > rows[j].Timestamp
		}
		return rows[i].PinId > rows[j].PinId
	})

	result.HasMore = len(rows) > size
	if result.HasMore {
		rows = rows[:size]
	}
	result.Items = rows
	return result, nil
}

func (a *Aggregator) homepageInteractionAliases(params HomepageInteractionListParams) []string {
	seen := make(map[string]bool)
	aliases := make([]string, 0, 6)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if seen[key] {
			return
		}
		seen[key] = true
		aliases = append(aliases, value)
	}

	for _, seed := range []string{params.GlobalMetaId, params.MetaId, params.Address} {
		add(seed)
		if a == nil {
			continue
		}
		for _, alias := range a.identityAliases(seed) {
			add(alias)
		}
	}

	return aliases
}

func homepageIsSimpleMsg(protocol string) bool {
	normalised := strings.ToLower(strings.Trim(strings.TrimSpace(protocol), "/"))
	return strings.HasSuffix(normalised, "simplemsg")
}
```

- [ ] **Step 4: Run the focused privatechat tests**

Run:

```bash
go test ./internal/aggregator/privatechat -run 'TestListOutgoingHomepageInteractions' -count=1
```

Expected: PASS.

- [ ] **Step 5: Run the full privatechat package tests**

Run:

```bash
go test ./internal/aggregator/privatechat -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit and post development journal**

Run:

```bash
git add internal/aggregator/privatechat/homepage_interactions.go internal/aggregator/privatechat/homepage_interactions_test.go
git commit -m "feat: add homepage chat interaction reader"
```

Use `metabot-post-buzz` with Eric to post a development journal for the commit. Include: commit hash, files changed, outgoing-only filtering, alias support, limit/hasMore behavior, and the privatechat test commands that passed.

---

### Task 2: Add `chats` to Bot Homepage v3 Builder

**Files:**
- Modify: `internal/aggregator/bothomepage/build.go`
- Modify: `internal/aggregator/bothomepage/module.go`
- Modify: `internal/aggregator/bothomepage/types_v3.go`
- Modify: `internal/aggregator/bothomepage/build_v3.go`
- Modify: `internal/aggregator/bothomepage/build_test.go`

- [ ] **Step 1: Write the failing bothomepage unit test**

In `internal/aggregator/bothomepage/build_test.go`, add `privatechat` to the imports:

```go
"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
```

Add this fake lister near the existing recording listers:

```go
type recordingChatInteractionLister struct {
	gotParams privatechat.HomepageInteractionListParams
	result    *privatechat.HomepageInteractionListResult
	err       error
}

func (r *recordingChatInteractionLister) ListOutgoingHomepageInteractions(params privatechat.HomepageInteractionListParams) (*privatechat.HomepageInteractionListResult, error) {
	r.gotParams = params
	return r.result, r.err
}
```

In `TestBuildV3SectionsAreServicesBuzzesMetaapps`, rename the function to:

```go
func TestBuildV3SectionsAreServicesMetaappsChatsBuzzes(t *testing.T) {
```

Inside the successful subtest, set the chat lister after the published content lister:

```go
chatLister := &recordingChatInteractionLister{result: &privatechat.HomepageInteractionListResult{
	Items: []privatechat.HomepageInteraction{{
		PinId:        "chat-current:i0",
		ProtocolPath: privatechat.HomepageSimpleMsgProtocolPath,
		Timestamp:    1710000005,
		InteractWith: "idqPeerBot",
	}},
	HasMore: true,
}}
agg.SetChatInteractionLister(chatLister)
```

Change the section order assertion:

```go
assertExactSectionIDsV3(t, got.Sections, []string{"services", "metaapps", "chats", "buzzes"})
```

Move the existing metaapps assertions to `got.Sections[1]`. Add chat assertions at `got.Sections[2]`:

```go
chats := got.Sections[2]
if chats.ProtocolPath != privatechat.HomepageSimpleMsgProtocolPath {
	t.Fatalf("chats.ProtocolPath = %q, want %q", chats.ProtocolPath, privatechat.HomepageSimpleMsgProtocolPath)
}
if chats.Page.Limit != homepageSectionLimit || chats.Page.Count != 1 || !chats.Page.HasMore {
	t.Fatalf("chats.Page = %+v", chats.Page)
}
if len(chats.Items) != 1 {
	t.Fatalf("chats.Items length = %d, want 1", len(chats.Items))
}
if chats.Items[0].PinId != "chat-current:i0" {
	t.Fatalf("chats.Items[0].PinId = %q, want chat-current:i0", chats.Items[0].PinId)
}
if chats.Items[0].Timestamp != 1710000005 {
	t.Fatalf("chats.Items[0].Timestamp = %d, want 1710000005", chats.Items[0].Timestamp)
}
if chats.Items[0].Data.InteractWith != "idqPeerBot" {
	t.Fatalf("chats.Items[0].Data.InteractWith = %q, want idqPeerBot", chats.Items[0].Data.InteractWith)
}
if chats.Items[0].Data.Payload != nil {
	t.Fatalf("chats.Items[0].Data.Payload = %#v, want nil", chats.Items[0].Data.Payload)
}
if chatLister.gotParams.GlobalMetaId != "idqCanonicalBot" || chatLister.gotParams.MetaId != "metaBot" || chatLister.gotParams.Address != "1BotAddress" {
	t.Fatalf("chat lister params = %+v, want canonical identity params", chatLister.gotParams)
}
if chatLister.gotParams.Size != homepageSectionReadSize {
	t.Fatalf("chat lister Size = %d, want %d", chatLister.gotParams.Size, homepageSectionReadSize)
}
```

Move the existing buzz assertions to `got.Sections[3]`.

- [ ] **Step 2: Write failing warning coverage**

In the section-source failure subtest in `TestBuildV3SectionsAreServicesMetaappsChatsBuzzes`, set:

```go
agg.SetChatInteractionLister(&recordingChatInteractionLister{err: errors.New("chats unavailable")})
```

Change the expected section IDs:

```go
assertExactSectionIDsV3(t, got.Sections, []string{"services", "metaapps", "chats", "buzzes"})
```

Change expected warnings to:

```go
assertWarnings(t, got.Warnings, []string{
	"services section source unavailable",
	"metaapps section source unavailable",
	"chats section source unavailable",
	"buzzes section source unavailable",
})
```

- [ ] **Step 3: Run the focused bothomepage test and confirm it fails**

Run:

```bash
go test ./internal/aggregator/bothomepage -run 'TestBuildV3SectionsAreServicesMetaappsChatsBuzzes' -count=1
```

Expected: FAIL at compile time with undefined `SetChatInteractionLister` or missing `InteractWith` field.

- [ ] **Step 4: Add the `ChatInteractionLister` interface**

In `internal/aggregator/bothomepage/build.go`, add the privatechat import:

```go
"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
```

Add the interface near the existing lister interfaces:

```go
type ChatInteractionLister interface {
	ListOutgoingHomepageInteractions(privatechat.HomepageInteractionListParams) (*privatechat.HomepageInteractionListResult, error)
}
```

- [ ] **Step 5: Store and expose the chat lister**

In `internal/aggregator/bothomepage/module.go`, add a field to `Aggregator`:

```go
chatInteractionLister ChatInteractionLister
```

Add this setter near the other setter methods:

```go
func (a *Aggregator) SetChatInteractionLister(lister ChatInteractionLister) {
	a.chatInteractionLister = lister
}
```

- [ ] **Step 6: Extend v3 item data without changing existing payload callers**

In `internal/aggregator/bothomepage/types_v3.go`, change `SectionItemDataV3` to:

```go
type SectionItemDataV3 struct {
	Payload      any    `json:"payload,omitempty"`
	InteractWith string `json:"interactWith,omitempty"`
}
```

Keep `SectionItemV3.Data` typed as `SectionItemDataV3`.

- [ ] **Step 7: Implement chats section loading and section order**

In `internal/aggregator/bothomepage/build_v3.go`, change `loadSectionsV3` to append sections in this order:

```go
if opts.IncludeServices {
	section, warning := a.loadServicesSectionV3(canonical, opts)
	sections = append(sections, section)
	if warning != "" {
		warnings = append(warnings, warning)
	}
}
if opts.IncludeMetaApps {
	section, warning := a.loadPublishedContentSectionV3(canonical, opts, "metaapps", publishedcontent.PathMetaApp, "metaapps section source unavailable")
	sections = append(sections, section)
	if warning != "" {
		warnings = append(warnings, warning)
	}
}
section, warning := a.loadChatsSectionV3(canonical)
sections = append(sections, section)
if warning != "" {
	warnings = append(warnings, warning)
}
if opts.IncludeBuzzes {
	section, warning := a.loadPublishedContentSectionV3(canonical, opts, "buzzes", publishedcontent.PathSimpleBuzz, "buzzes section source unavailable")
	sections = append(sections, section)
	if warning != "" {
		warnings = append(warnings, warning)
	}
}
```

Add this function in the same file:

```go
func (a *Aggregator) loadChatsSectionV3(canonical CanonicalIdentity) (SectionV3, string) {
	if a == nil || a.chatInteractionLister == nil {
		return emptySectionV3("chats", privatechat.HomepageSimpleMsgProtocolPath), ""
	}

	result, err := a.chatInteractionLister.ListOutgoingHomepageInteractions(privatechat.HomepageInteractionListParams{
		GlobalMetaId: canonical.GlobalMetaId,
		MetaId:       canonical.MetaId,
		Address:      canonical.Address,
		Size:         homepageSectionReadSize,
	})
	if err != nil {
		return emptySectionV3("chats", privatechat.HomepageSimpleMsgProtocolPath), "chats section source unavailable"
	}
	if result == nil || len(result.Items) == 0 {
		return emptySectionV3("chats", privatechat.HomepageSimpleMsgProtocolPath), ""
	}

	items := make([]SectionItemV3, 0, len(result.Items))
	for _, item := range result.Items {
		if item.PinId == "" || item.InteractWith == "" {
			continue
		}
		items = append(items, SectionItemV3{
			PinId:        item.PinId,
			ProtocolPath: privatechat.HomepageSimpleMsgProtocolPath,
			Timestamp:    item.Timestamp,
			Data: SectionItemDataV3{
				InteractWith: item.InteractWith,
			},
		})
	}
	if len(items) == 0 {
		return emptySectionV3("chats", privatechat.HomepageSimpleMsgProtocolPath), ""
	}

	return sectionWithItemsV3("chats", privatechat.HomepageSimpleMsgProtocolPath, items, result.HasMore), ""
}
```

Add the privatechat import to `build_v3.go`:

```go
"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
```

- [ ] **Step 8: Update exact v3 section order assertions**

In `internal/aggregator/bothomepage/build_test.go`, replace every v3 exact order assertion:

```go
[]string{"services", "buzzes", "metaapps"}
```

with:

```go
[]string{"services", "metaapps", "chats", "buzzes"}
```

For tests that index into `got.Sections`, update indices:

- `services` stays `got.Sections[0]`
- `metaapps` becomes `got.Sections[1]`
- `chats` becomes `got.Sections[2]`
- `buzzes` becomes `got.Sections[3]`

Do not add a chat lister to tests that do not need chat items; they should see an empty `chats` section without warnings.

- [ ] **Step 9: Add chat item JSON assertion**

In `internal/aggregator/bothomepage/build_test.go`, add:

```go
func assertChatSectionItemV3JSON(t *testing.T, item SectionItemV3) {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal chat section item: %v", err)
	}
	var encoded map[string]any
	if err := json.Unmarshal(raw, &encoded); err != nil {
		t.Fatalf("json.Unmarshal chat section item %s: %v", raw, err)
	}
	assertMapHasOnlyKeys(t, encoded, []string{"data", "pinId", "protocolPath", "timestamp"})
	data, ok := encoded["data"].(map[string]any)
	if !ok {
		t.Fatalf("encoded chat data = %#v, want object; raw=%s", encoded["data"], raw)
	}
	assertMapHasOnlyKeys(t, data, []string{"interactWith"})
	if data["interactWith"] != "idqPeerBot" {
		t.Fatalf("chat data.interactWith = %#v, want idqPeerBot", data["interactWith"])
	}
}
```

Call it in the successful section test:

```go
assertChatSectionItemV3JSON(t, chats.Items[0])
```

- [ ] **Step 10: Run focused bothomepage tests**

Run:

```bash
go test ./internal/aggregator/bothomepage -run 'TestBuildV3SectionsAreServicesMetaappsChatsBuzzes|TestBuildV3SectionItemsAreMinimal|TestBuildV3UsesMempoolProfileAndSectionPins|TestBuildV3ServicesSectionFallsBackToProviderVisibleServices' -count=1
```

Expected: PASS.

- [ ] **Step 11: Run full bothomepage tests**

Run:

```bash
go test ./internal/aggregator/bothomepage -count=1
```

Expected: PASS.

- [ ] **Step 12: Commit and post development journal**

Run:

```bash
git add internal/aggregator/bothomepage/build.go internal/aggregator/bothomepage/module.go internal/aggregator/bothomepage/types_v3.go internal/aggregator/bothomepage/build_v3.go internal/aggregator/bothomepage/build_test.go
git commit -m "feat: add bot homepage v3 chats section"
```

Use `metabot-post-buzz` with Eric to post a development journal for the commit. Include: commit hash, section order, item JSON shape, warning behavior, and bothomepage test commands that passed.

---

### Task 3: Wire PrivateChat Into Production and Router Tests

**Files:**
- Modify: `cmd/metaso-p2p/main.go`
- Modify: `internal/api/router_test.go`

- [ ] **Step 1: Write failing router test for v3 chats**

In `internal/api/router_test.go`, add:

```go
func TestRouterBotHomepageV3ExposesOutgoingChats(t *testing.T) {
	fixture := setupFullRouterFixture(t)
	seedBotProfile(t, fixture, "idq-bot")

	if _, err := fixture.privateAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            "chat-idq-bot:i0",
		Path:          "/protocols/simplemsg",
		Operation:     "create",
		ContentBody:   mustMarshalJSON(t, map[string]interface{}{"from": "meta-idq-bot", "to": "idq-peer", "content": "encrypted", "contentType": "text/plain", "encryption": "ecies"}),
		ContentType:   "application/json",
		ChainName:     "mvc",
		GlobalMetaId:  "idq-bot",
		MetaId:        "meta-idq-bot",
		CreateMetaId:  "meta-idq-bot",
		Address:       "addr-idq-bot",
		CreateAddress: "addr-idq-bot",
		Timestamp:     1710000201,
		Number:        201,
	}); err != nil {
		t.Fatalf("seed private chat: %v", err)
	}

	w, body := get(t, fixture.router, "/api/bot-homepage/globalmetaid/idq-bot?version=v3")
	if w.Code != http.StatusOK || body["code"] != float64(0) {
		t.Fatalf("v3 status=%d body=%s", w.Code, w.Body.String())
	}
	v3Data, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("v3 data = %T %#v, want object", body["data"], body["data"])
	}
	sections, ok := v3Data["sections"].([]interface{})
	if !ok {
		t.Fatalf("v3 sections = %T %#v, want array", v3Data["sections"], v3Data["sections"])
	}
	chats := routerSectionByID(t, sections, "chats")
	if chats["protocolPath"] != "/protocols/simplemsg" {
		t.Fatalf("chats.protocolPath = %#v, want /protocols/simplemsg", chats["protocolPath"])
	}
	items, ok := chats["items"].([]interface{})
	if !ok || len(items) != 1 {
		t.Fatalf("chats.items = %T %#v, want one item", chats["items"], chats["items"])
	}
	item, ok := items[0].(map[string]interface{})
	if !ok {
		t.Fatalf("chat item = %T %#v, want object", items[0], items[0])
	}
	if item["pinId"] != "chat-idq-bot:i0" || item["protocolPath"] != "/protocols/simplemsg" || item["timestamp"] != float64(1710000201) {
		t.Fatalf("chat item envelope = %#v", item)
	}
	data, ok := item["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("chat data = %T %#v, want object", item["data"], item["data"])
	}
	if data["interactWith"] != "idq-peer" {
		t.Fatalf("chat data.interactWith = %#v, want idq-peer", data["interactWith"])
	}
	if _, ok := data["payload"]; ok {
		t.Fatalf("chat data unexpectedly includes payload: %#v", data)
	}
}
```

- [ ] **Step 2: Run router test and confirm it fails before wiring**

Run:

```bash
go test ./internal/api -run 'TestRouterBotHomepageV3ExposesOutgoingChats' -count=1
```

Expected: FAIL because the `chats` section is empty or the lister is not wired.

- [ ] **Step 3: Wire the lister in router test fixture**

In `setupFullRouterFixture` in `internal/api/router_test.go`, after:

```go
botHomepageAgg.SetPublishedContentLister(publishedAgg)
```

add:

```go
botHomepageAgg.SetChatInteractionLister(privateAgg)
```

- [ ] **Step 4: Wire the lister in production main**

In `cmd/metaso-p2p/main.go`, after:

```go
botHomepageAgg.SetHomepageServiceLister(skillserviceAgg)
```

add:

```go
botHomepageAgg.SetChatInteractionLister(privatechatAgg)
```

- [ ] **Step 5: Run focused router test**

Run:

```bash
go test ./internal/api -run 'TestRouterBotHomepageV3ExposesOutgoingChats' -count=1
```

Expected: PASS.

- [ ] **Step 6: Run router package tests**

Run:

```bash
go test ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit and post development journal**

Run:

```bash
git add cmd/metaso-p2p/main.go internal/api/router_test.go
git commit -m "feat: wire homepage chats source"
```

Use `metabot-post-buzz` with Eric to post a development journal for the commit. Include: commit hash, production wiring point, router fixture wiring, router acceptance test, and test commands that passed.

---

### Task 4: Update v3 Contract Documentation

**Files:**
- Modify: `docs/specs/2026-06-17-bot-homepage-v3-api.md`
- Modify: `docs/superpowers/specs/2026-06-17-bot-homepage-v3-implementation-spec.md`

- [ ] **Step 1: Update the public v3 API section list**

In `docs/specs/2026-06-17-bot-homepage-v3-api.md`, replace the current section table with:

```markdown
| Section ID | Protocol path |
| --- | --- |
| `services` | `/protocols/skill-service` |
| `metaapps` | `/protocols/metaapp` |
| `chats` | `/protocols/simplemsg` |
| `buzzes` | `/protocols/simplebuzz` |
```

Replace the sentence above it with:

```markdown
v3 returns exactly these homepage sections, in this order:
```

- [ ] **Step 2: Add the public chats item example**

In the same file, after the MetaApp item example, add:

````markdown
Example chat item:

```json
{
  "pinId": "def...i0",
  "protocolPath": "/protocols/simplemsg",
  "timestamp": 1781258875,
  "data": {
    "interactWith": "idq..."
  }
}
```

For v3, `chats` is an abstract interaction section. The first implementation
only reads outgoing `/protocols/simplemsg` records and maps the simplemsg `to`
field to `data.interactWith`. It does not expose message content, encryption
metadata, txid, address, chain name, or the original simplemsg payload.
````

- [ ] **Step 3: Update the internal implementation spec**

In `docs/superpowers/specs/2026-06-17-bot-homepage-v3-implementation-spec.md`, update the fixed section table to:

```markdown
| Section ID | Source |
| --- | --- |
| `services` | `/protocols/skill-service` |
| `metaapps` | `/protocols/metaapp` |
| `chats` | outgoing `/protocols/simplemsg` |
| `buzzes` | `/protocols/simplebuzz` |
```

Update any acceptance criteria that says sections are exactly `services`, `buzzes`, and `metaapps` to:

```markdown
Sections are exactly `services`, `metaapps`, `chats`, and `buzzes` when all are enabled.
```

Add this acceptance criterion:

```markdown
`chats.items[i].data` only exposes `interactWith`; it does not expose `payload`, content, encryption metadata, txid, chain name, address, or private-chat storage keys.
```

- [ ] **Step 4: Run documentation checks**

Run:

```bash
rg -n 'services.*buzzes.*metaapps|services", "buzzes", "metaapps' docs/specs/2026-06-17-bot-homepage-v3-api.md docs/superpowers/specs/2026-06-17-bot-homepage-v3-implementation-spec.md
git diff --check
```

Expected: the `rg` command returns no stale section-order matches; `git diff --check` returns no whitespace errors.

- [ ] **Step 5: Commit and post development journal**

Run:

```bash
git add docs/specs/2026-06-17-bot-homepage-v3-api.md docs/superpowers/specs/2026-06-17-bot-homepage-v3-implementation-spec.md
git commit -m "docs: update bot homepage chats contract"
```

Use `metabot-post-buzz` with Eric to post a development journal for the commit. Include: commit hash, public contract changes, internal spec changes, and documentation checks that passed.

---

### Task 5: Final Verification

**Files:**
- Verify repository state after Tasks 1-4.

- [ ] **Step 1: Run focused package tests**

Run:

```bash
go test ./internal/aggregator/privatechat ./internal/aggregator/bothomepage ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 2: Run broader regression tests for touched integration paths**

Run:

```bash
go test ./internal/aggregator/... ./internal/api ./cmd/metaso-p2p -count=1
```

Expected: PASS.

- [ ] **Step 3: Run final diff hygiene**

Run:

```bash
git status --short
git diff --check
```

Expected: `git status --short` shows no unstaged implementation changes after the task commits; `git diff --check` returns no output.

- [ ] **Step 4: Summarize completion**

Report:

- commit hashes for each task;
- final test commands and results;
- the `chats` response shape;
- any remaining risk, especially that privatechat currently scans the `pchat:` prefix rather than using a sender-specific index.
