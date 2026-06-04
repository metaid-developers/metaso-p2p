# Private Chat WebSocket Push Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure private-chat `WS_SERVER_NOTIFY_PRIVATE_CHAT` pushes reach connected BotHub Delivery clients across known identity aliases and carry a stable payload compatible with the private-chat HTTP item shape.

**Architecture:** Keep identity alias resolution owned by the private-chat aggregator, because that is where HTTP history already resolves aliases. Extend `NotifyEvent` to carry all target identities, let the socket layer fan out to unique matching connections, and build the push payload from the persisted `PrivateMessage` instead of a narrower hand-written event body.

**Tech Stack:** Go, Gin, Socket.IO v2 (`github.com/zishang520/socket.io/v2/socket`), Pebble-backed aggregators, `testing` package with `CGO_ENABLED=0`.

---

## File Structure

- Modify `internal/aggregator/aggregator.go`: add `TargetIds []string` to `NotifyEvent` so aggregators can route to multiple known aliases without changing existing single-target callers.
- Modify `internal/aggregator/privatechat/process.go`: populate `PrivateMessage` fields first, build a canonical push payload from it, and set `NotifyEvent.TargetIds` with recipient aliases.
- Modify `internal/aggregator/privatechat/process_test.go`: add red/green tests for alias target fan-out and canonical private-chat payload fields.
- Modify `internal/socket/server.go`: route private-chat and group-role events to all unique targets in `TargetIds`, falling back to `MetaId` / `GlobalMetaId` for existing aggregators.
- Modify `internal/socket/server_test.go`: add unit coverage for unique target extraction without needing a live Socket.IO client.
- Optionally update `docs/IDCHAT_API_CONTRACT.md`: document that private-chat push payloads now use the `PrivateMessage` / `PrivateChatItem` compatible shape and may be routed to all known recipient aliases.

## Task 1: Save This Plan

**Files:**
- Create: `docs/superpowers/plans/2026-06-04-private-chat-websocket-push.md`

- [ ] **Step 1: Add the plan file**

Use this document as the saved implementation plan.

- [ ] **Step 2: Verify the plan file is tracked as an add**

Run:

```bash
git status --short docs/superpowers/plans/2026-06-04-private-chat-websocket-push.md
```

Expected output:

```text
?? docs/superpowers/plans/2026-06-04-private-chat-websocket-push.md
```

- [ ] **Step 3: Commit the plan**

Run:

```bash
git add docs/superpowers/plans/2026-06-04-private-chat-websocket-push.md
git commit -m "docs: plan private chat websocket push fix"
```

Expected: commit succeeds and includes only the plan file.

## Task 2: Route Private-Chat Pushes To Recipient Aliases

**Files:**
- Modify: `internal/aggregator/aggregator.go`
- Modify: `internal/aggregator/privatechat/process.go`
- Modify: `internal/aggregator/privatechat/process_test.go`
- Modify: `internal/socket/server.go`
- Modify: `internal/socket/server_test.go`

- [ ] **Step 1: Write failing private-chat target alias test**

Add this test to `internal/aggregator/privatechat/process_test.go`:

```go
func TestPrivateChatNotifyEventTargetsRecipientAliases(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	agg.SetProfileLookup(&fakePrivateChatProfileLookup{
		byGlobalMetaId: map[string]*IdentityProfile{
			"idqBuyerGlobal": {
				MetaId:       "buyer_local_meta",
				GlobalMetaId: "idqBuyerGlobal",
				Address:      "1BuyerAddress",
			},
		},
	})

	pin := &aggregator.PinInscription{
		Id:            "alias_push:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1ProviderAddress",
		CreateMetaId:  "provider_meta",
		GlobalMetaId:  "idqProviderGlobal",
		ChainName:     "mvc",
		Timestamp:     1780562000,
		GenesisHeight: 176100,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "provider_meta",
			To:          "idqBuyerGlobal",
			Content:     "alias route",
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}
	if evt == nil {
		t.Fatal("expected NotifyEvent")
	}

	want := []string{"idqBuyerGlobal", "buyer_local_meta", "1BuyerAddress"}
	if !reflect.DeepEqual(evt.TargetIds, want) {
		t.Fatalf("TargetIds = %#v, want %#v", evt.TargetIds, want)
	}
	if evt.MetaId != "idqBuyerGlobal" {
		t.Fatalf("MetaId fallback = %q, want idqBuyerGlobal", evt.MetaId)
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/privatechat -run TestPrivateChatNotifyEventTargetsRecipientAliases -count=1
```

Expected: FAIL because `aggregator.NotifyEvent` has no `TargetIds` field.

- [ ] **Step 3: Add `TargetIds` to the notify contract**

In `internal/aggregator/aggregator.go`, change `NotifyEvent` to:

```go
type NotifyEvent struct {
	Type         string      // WS_SERVER_NOTIFY_GROUP_CHAT, etc.
	MetaId       string      // target user MetaId
	GlobalMetaId string      // target user GlobalMetaId
	TargetIds    []string    // all known target identities/aliases for user-directed pushes
	GroupId      string      // target group (for room broadcast)
	Payload      interface{} // notification body
}
```

- [ ] **Step 4: Populate alias targets from private-chat**

In `internal/aggregator/privatechat/process.go`, before creating `notifyEvent`, add:

```go
targetIds := a.identityAliases(toMetaId)
```

Then create the event as:

```go
notifyEvent := &aggregator.NotifyEvent{
	Type:      "WS_SERVER_NOTIFY_PRIVATE_CHAT",
	MetaId:    toMetaId,
	TargetIds: targetIds,
	Payload:   notifyPayload,
}
```

- [ ] **Step 5: Add socket target extraction unit test**

Add this test to `internal/socket/server_test.go`:

```go
func TestNotifyEventTargetIdsDeduplicateAndFallback(t *testing.T) {
	evt := &aggregator.NotifyEvent{
		MetaId:       "buyer_local_meta",
		GlobalMetaId: "idqBuyerGlobal",
		TargetIds:    []string{"idqBuyerGlobal", "buyer_local_meta", "1BuyerAddress", "idqBuyerGlobal", " "},
	}

	got := notifyEventTargetIds(evt)
	want := []string{"idqBuyerGlobal", "buyer_local_meta", "1BuyerAddress"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notifyEventTargetIds() = %#v, want %#v", got, want)
	}
}

func TestNotifyEventTargetIdsFallbackWithoutTargetIds(t *testing.T) {
	evt := &aggregator.NotifyEvent{
		MetaId:       "buyer_local_meta",
		GlobalMetaId: "idqBuyerGlobal",
	}

	got := notifyEventTargetIds(evt)
	want := []string{"buyer_local_meta", "idqBuyerGlobal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("notifyEventTargetIds() = %#v, want %#v", got, want)
	}
}
```

Ensure `internal/socket/server_test.go` imports `reflect` and `github.com/metaid-developers/meta-socket/internal/aggregator`.

- [ ] **Step 6: Run socket target tests and verify RED**

Run:

```bash
CGO_ENABLED=0 go test ./internal/socket -run 'TestNotifyEventTargetIds' -count=1
```

Expected: FAIL because `notifyEventTargetIds` is not defined.

- [ ] **Step 7: Implement socket target extraction and fan-out**

In `internal/socket/server.go`, add:

```go
func notifyEventTargetIds(evt *aggregator.NotifyEvent) []string {
	if evt == nil {
		return nil
	}

	targets := make([]string, 0, len(evt.TargetIds)+2)
	seen := make(map[string]bool)
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		key := strings.ToLower(id)
		if seen[key] {
			return
		}
		seen[key] = true
		targets = append(targets, id)
	}

	for _, id := range evt.TargetIds {
		add(id)
	}
	if len(targets) == 0 {
		add(evt.MetaId)
		add(evt.GlobalMetaId)
	}
	return targets
}

func (s *Server) SendToUsers(metaIds []string, msg *PushEnvelope) {
	for _, metaId := range metaIds {
		s.SendToUser(metaId, msg)
	}
}
```

Then change `routeNotifyEvent` private-chat and group-role user-directed branches to:

```go
for _, targetId := range notifyEventTargetIds(evt) {
	s.SendToUser(targetId, envelope)
}
```

Keep group-room broadcasting behavior unchanged.

- [ ] **Step 8: Run focused tests and verify GREEN**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/privatechat ./internal/socket -run 'TestPrivateChatNotifyEventTargetsRecipientAliases|TestNotifyEventTargetIds' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit alias routing**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/privatechat ./internal/socket -count=1
git add internal/aggregator/aggregator.go internal/aggregator/privatechat/process.go internal/aggregator/privatechat/process_test.go internal/socket/server.go internal/socket/server_test.go
git commit -m "fix: route private chat pushes to identity aliases"
```

Expected: tests pass and commit includes only alias routing changes.

## Task 3: Align Private-Chat Push Payload With Canonical Message Shape

**Files:**
- Modify: `internal/aggregator/privatechat/process.go`
- Modify: `internal/aggregator/privatechat/process_test.go`
- Modify: `docs/IDCHAT_API_CONTRACT.md`

- [ ] **Step 1: Write failing canonical payload test**

Add this test to `internal/aggregator/privatechat/process_test.go`:

```go
func TestPrivateChatNotifyPayloadUsesCanonicalMessageShape(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	pin := &aggregator.PinInscription{
		Id:            "canonical_payload:i0",
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1ProviderAddress",
		CreateMetaId:  "provider_meta",
		GlobalMetaId:  "idqProviderGlobal",
		ChainName:     "mvc",
		Timestamp:     1780562100,
		GenesisHeight: 176101,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        "provider_meta",
			To:          "idqBuyerGlobal",
			Content:     "canonical payload",
			ContentType: "text/markdown",
			Encrypt:     "ecdh",
		}),
	}

	evt, err := agg.HandleBlockPin(pin)
	if err != nil {
		t.Fatalf("HandleBlockPin failed: %v", err)
	}
	if evt == nil {
		t.Fatal("expected NotifyEvent")
	}

	payload, ok := evt.Payload.(*PrivateMessage)
	if !ok {
		t.Fatalf("Payload = %T, want *PrivateMessage", evt.Payload)
	}
	if payload.FromGlobalMetaId != "idqProviderGlobal" {
		t.Fatalf("FromGlobalMetaId = %q, want idqProviderGlobal", payload.FromGlobalMetaId)
	}
	if payload.ToGlobalMetaId != "idqBuyerGlobal" {
		t.Fatalf("ToGlobalMetaId = %q, want idqBuyerGlobal", payload.ToGlobalMetaId)
	}
	if payload.Protocol != "/private/chat/simplemsg" {
		t.Fatalf("Protocol = %q, want /private/chat/simplemsg", payload.Protocol)
	}
	if payload.Chain != "mvc" {
		t.Fatalf("Chain = %q, want mvc", payload.Chain)
	}
	if payload.BlockHeight != 176101 {
		t.Fatalf("BlockHeight = %d, want 176101", payload.BlockHeight)
	}
	if payload.Index != -1 {
		t.Fatalf("Index = %d, want -1", payload.Index)
	}
	if payload.ContentType != "text/markdown" || payload.Encryption != "ecdh" {
		t.Fatalf("ContentType/Encryption = %q/%q, want text/markdown/ecdh", payload.ContentType, payload.Encryption)
	}
}
```

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/privatechat -run TestPrivateChatNotifyPayloadUsesCanonicalMessageShape -count=1
```

Expected: FAIL because payload is currently `map[string]interface{}`.

- [ ] **Step 3: Use `PrivateMessage` as the push payload**

In `internal/aggregator/privatechat/process.go`, remove the hand-written `notifyPayload := map[string]interface{}{...}` block and set:

```go
notifyPayload := msg
```

Keep `notifyEvent.Type` as `WS_SERVER_NOTIFY_PRIVATE_CHAT`; the Socket.IO envelope still carries the event type in `M`.

- [ ] **Step 4: Update existing payload assertions**

In `TestSocketPushNotification`, replace the map assertions with:

```go
payload, ok := evt.Payload.(*PrivateMessage)
if !ok {
	t.Fatal("expected payload to be *PrivateMessage")
}
if payload.From != "alice_push" {
	t.Errorf("expected payload.From='alice_push', got %q", payload.From)
}
if payload.Content != "Push notification test" {
	t.Errorf("expected payload.Content='Push notification test', got %q", payload.Content)
}
if payload.ToGlobalMetaId != "bob_push" {
	t.Errorf("expected payload.ToGlobalMetaId='bob_push', got %q", payload.ToGlobalMetaId)
}
```

- [ ] **Step 5: Document private-chat push payload shape**

In `docs/IDCHAT_API_CONTRACT.md`, under `PrivateChatItem`, add:

```markdown
Private-chat Socket.IO pushes use the same `PrivateChatItem`-compatible object
as `D` in the `{M, C, D}` envelope. Clients should use `M` for event dispatch
and treat `D.from`, `D.to`, `D.fromGlobalMetaId`, `D.toGlobalMetaId`, `D.pinId`,
and `D.txId` as the stable identity and de-duplication fields.
```

- [ ] **Step 6: Run focused tests and verify GREEN**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/privatechat -run 'TestPrivateChatNotifyPayloadUsesCanonicalMessageShape|TestSocketPushNotification' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit payload alignment**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/privatechat -count=1
git add internal/aggregator/privatechat/process.go internal/aggregator/privatechat/process_test.go docs/IDCHAT_API_CONTRACT.md
git commit -m "fix: align private chat push payload shape"
```

Expected: tests pass and commit includes only payload/documentation changes.

## Task 4: Final Verification And Development Journal

**Files:**
- Read: changed files from Tasks 1-3

- [ ] **Step 1: Run targeted regression suite**

Run:

```bash
CGO_ENABLED=0 go test ./internal/aggregator/privatechat ./internal/socket ./internal/indexer ./cmd/meta-socket -count=1
```

Expected: PASS for all listed packages.

- [ ] **Step 2: Run full repository tests**

Run:

```bash
CGO_ENABLED=0 go test ./... -count=1
```

Expected: PASS for all packages.

- [ ] **Step 3: Check formatting and whitespace**

Run:

```bash
gofmt -w internal/aggregator/aggregator.go internal/aggregator/privatechat/process.go internal/aggregator/privatechat/process_test.go internal/socket/server.go internal/socket/server_test.go
git diff --check
```

Expected: no output from `git diff --check`.

- [ ] **Step 4: Inspect git state**

Run:

```bash
git status --short --branch
git log --oneline --max-count=6
```

Expected: branch may be ahead of `origin/main`; working tree contains no unstaged files from this task.

- [ ] **Step 5: Post development journal for each commit**

For every implementation commit created in this plan, create a request file like:

```json
{
  "content": "meta-socket development journal: <commit hash> <commit subject>. Summary: <what changed>. Verification: <commands and pass results>. Impact: BotHub Delivery private-chat websocket pushes now route across known recipient aliases and carry a canonical payload shape."
}
```

Run:

```bash
$HOME/.metabot/bin/metabot buzz post --request-file /tmp/meta-socket-buzz-<commit>.json
```

Expected: command returns a successful JSON envelope. If it includes `localUiUrl`, report that link.

## Self-Review

- Spec coverage: Task 2 covers identity alias routing; Task 3 covers payload shape; Task 4 covers verification. ZMQ/mempool real-time implementation is intentionally not in this fix because it is a larger milestone and the immediate bug can be fixed for confirmed/indexed messages.
- Placeholder scan: no TBD/TODO/fill-in placeholders remain; exact files, code snippets, and commands are specified.
- Type consistency: `NotifyEvent.TargetIds`, `notifyEventTargetIds`, and `PrivateMessage` payload usage are consistently named across tasks.
