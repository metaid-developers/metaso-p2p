package metatask

import (
	"fmt"
	"sort"
)

// Freeze-draft replay engine (v1.1, pin://4c733705605f944c774da2f314d55d8f83cae7b18c6cfd2b8181ccbcd934c46bi0):
// a pure function  conclusion = F(E, B)  over the seven-path event set E and
// the evaluation boundary B (block height + that block's timestamp).
//
// Differences from the pool read path (aggregate.go/settle.go, pinned to
// metatask-replay v1.2.0 semantics — both stay untouched):
//   - dual-track vote gate: the #8/#9 requirements apply only at block height
//     >= HAct (freeze §3.1); below it the v1.1.0 protocol semantics hold;
//   - total order excludes seenTime (freeze §2/D3): (height, txIndex, version,
//     pinId) — the list API exposes no outputIndex, so the unique pinId takes
//     the final slot; still a pure function of E;
//   - mempool pins (genesisHeight < 0) never enter E (freeze §1.3);
//   - sequential state machine: a valid fail sends the node back to open at
//     its own block (裁定三, freeze 计票结果 row has no state precondition);
//     claim TTL / review-window expiry are evaluated against B, never a wall
//     clock (D2).
//
// Validated against the skill v1.2.2 discriminant vectors 11/11
// (testdata/d8vectors, see d8_test.go and RUN_LOG.txt).

const (
	// HAct is the frozen dual-track switch (freeze §3.1; ruling pin chain in
	// SemanticGateRulingPins). Explicit block-height constant, no per-event
	// version forking.
	HAct = 190000

	// ReplayAlgoVersion identifies the frozen algorithm this engine implements.
	// Any change to the algorithm or to HAct bumps it and forces a full replay
	// (freeze D10). Contract = freeze draft v1.2 (pin://1d6ac1cc…, chair-locked
	// as the sole frozen version; the late-landing v1.3 was retracted to
	// reference-only, so no canonId echoes anywhere in the projections).
	ReplayAlgoVersion = "metatask-replay-indexer/1.0.0+freeze1.2"
	// ProtocolVersion / ProtocolPinID anchor the protocol the semantics come from.
	ProtocolVersion = "1.1.0"
	ProtocolPinID   = "ff7d0b59a44d760a84c3660e8656e37fe5cef3dde6a67b217ea87d6d216789bfi0"
)

// SemanticGateRulingPins is the frozen ruling-pin chain cited by freeze §0:
// H_ACT ruling, H_ACT correction, and the "existing events replay uniformly"
// ruling (which pins the pre-H_ACT semantics to v1.1).
var SemanticGateRulingPins = []string{
	"8420000052f41882f19cd5e052b52b511338f6d3e51443a4f22bc28ad7bb683ci0",
	"e66beedbe22d5801d72b3b1ac8b9a446cc2cdd0eb6b388852b27cc2ea7e8753di0",
	"1547beab3f4c0a81ff31f35547da67d7b1d6bd494944912fcf6565546b8f7b29i0",
}

// Machine states (freeze §3) and cycle outcomes.
const (
	StateOpen      = "open"
	StateClaimed   = "claimed"
	StateSubmitted = "submitted"
	StateVerified  = "verified"

	OutcomeNone                = "none"
	OutcomeReleased            = "released"
	OutcomeRejected            = "rejected"
	OutcomeExpiredClaim        = "expired_claim"
	OutcomeExpiredReviewWindow = "expired_review_window"
)

// Vote/record ignore reasons. missing_semantic_check / missing_failreason are
// the frozen §3.1 gate reasons; the rest carry the counting preconditions.
const (
	IgnoreSupersededClaim    = "superseded_by_earlier_claim"
	IgnoreNotEarliestInCyc   = "not_earliest_in_cycle"
	IgnoreClaimIDMismatch    = "claimid_mismatch"
	IgnoreSubmitterNotHolder = "submitter_not_holder"
	IgnoreNoMatchingClaim    = "no_matching_active_claim"
	IgnoreNodeNotOpen        = "node_not_open"
	IgnoreTargetNotActive    = "target_not_active"
	IgnoreUnresolvedTarget   = "unresolved_target"
	IgnoreVoterIsSubmitter   = "voter_is_submitter"
	IgnoreVoterIsRootAuthor  = "voter_is_root_author"
	IgnoreEmptyMethod        = "empty_method"
	IgnoreAfterWindowClose   = "after_window_close"
	IgnoreVerdictInvalid     = "verdict_invalid"
	IgnoreUnknownVerdict     = "unknown_verdict"
)

// Boundary is B of F(E,B) (freeze §2): block height and that block's timestamp.
// Every expiry decision reads B.BlockTs (D2) — never a wall clock. A Height of
// 0 disables the to-block event filter (used by the vector harness).
type Boundary struct {
	Height  int64 `json:"height"`
	BlockTs int64 `json:"blockTs"`
}

// gateInvalid mirrors metatask-replay v1.2.2 vote_invalid(b, height): the
// #8/#9 requirements bind only at block height >= HAct. payloadPresent is the
// python-parity "present and non-empty" test (payload.go), matching the
// engine's str(value or "").strip() rule, so parity holds field by field.
func gateInvalid(b map[string]any, height int64) string {
	if height < HAct {
		return "" // v1.1 semantics below the switch: no #8/#9 requirements
	}
	if !payloadPresent(b, "semantic_check") {
		return "missing_semantic_check"
	}
	if payloadString(b, "verdict") == "fail" && !payloadPresent(b, "failreason") {
		return "missing_failreason"
	}
	return ""
}

// treeNodeInfo is the resolved per-node tree metadata. parent/specid keep
// their null-ness from the tree event (null parent = root; null specid =
// inherit the task-root specid).
type treeNodeInfo struct {
	parent *string
	title  string
	kind   string
	specid *string
	deps   []string
	params map[string]any
}

// replayEvent is one admitted event: confirmed, payload-accepted, decoded.
type replayEvent struct {
	pin  *Pin
	body map[string]any
	ts   int64 // 块时间戳 (pin timestamp; seenTime never participates, D3)
}

func replayLess(a, b *replayEvent) bool {
	if a.pin.GenesisHeight != b.pin.GenesisHeight {
		return a.pin.GenesisHeight < b.pin.GenesisHeight
	}
	if a.pin.TxIndex != b.pin.TxIndex {
		return a.pin.TxIndex < b.pin.TxIndex
	}
	if a.pin.Version != b.pin.Version {
		return a.pin.Version < b.pin.Version
	}
	return a.pin.ID < b.pin.ID
}

// CurrentClaim is the freeze §5 node claim view.
type CurrentClaim struct {
	ClaimPinID    string `json:"claimPinId"`
	Claimant      string `json:"claimant"`
	BlockHeight   int64  `json:"blockHeight"`
	BlockTs       int64  `json:"blockTs"`
	TTLDeadlineTs int64  `json:"ttlDeadlineTs"` // 0 when policy has no TTL
}

// ActiveSubmission is the freeze §5 node submission view.
type ActiveSubmission struct {
	SubmissionPinID string         `json:"submissionPinId"`
	Submitter       string         `json:"submitter"`
	BlockHeight     int64          `json:"blockHeight"`
	BlockTs         int64          `json:"blockTs"`
	Hash            string         `json:"hash"`
	ContentType     string         `json:"contentType"`
	Attachment      string         `json:"attachment"`
	ChildIDs        []string       `json:"childids"`
	Result          map[string]any `json:"result"`
	// WindowDeadlineTs drives the §4.3 expiry but is not a frozen §5 key.
	WindowDeadlineTs int64 `json:"-"`
}

// VoteRecord is one verify ballot, counted or not (freeze: votes[] 含不计票票).
// BlockTs drives lastActivity internally; the frozen §5 vote keys end at
// blockHeight, so it stays out of the body.
type VoteRecord struct {
	VerifyPinID      string `json:"verifyPinId"`
	Voter            string `json:"voter"`
	Verdict          string `json:"verdict"`
	HasFailreason    bool   `json:"hasFailreason"`
	HasSemanticCheck bool   `json:"hasSemanticCheck"`
	Counts           bool   `json:"counts"`
	IgnoreReason     string `json:"ignoreReason,omitempty"`
	BlockHeight      int64  `json:"blockHeight"`
	BlockTs          int64  `json:"-"`
}

// EventRecord is the freeze §5 node-detail event row (每条可回源).
type EventRecord struct {
	EventPinID   string `json:"eventPinId"`
	Path         string `json:"path"`
	Version      int64  `json:"version"`
	BlockHeight  int64  `json:"blockHeight"`
	BlockTs      int64  `json:"blockTs"`
	Author       string `json:"author"`
	Kind         string `json:"kind"`
	Summary      string `json:"summary"`
	Validity     string `json:"validity"` // counted|ignored
	IgnoreReason string `json:"ignoreReason,omitempty"`
}

// NodeReplay is one node's replay conclusion. Its JSON keys are the frozen
// §5 nodes[i] set: id/parent/title/kind/params/deps/specid from the tree
// event 原样 (parent/specid keep null-ness), plus the replay view keys.
// Empty lastWrite/lastActivity/verifiedAt serialize as null (D7 needs one
// zero-value convention — the independent replayer's null, not 0/"").
type NodeReplay struct {
	ID                  string            `json:"id"`
	Parent              *string           `json:"parent"`
	Title               string            `json:"title"`
	Kind                string            `json:"kind"`
	Params              map[string]any    `json:"params"`
	SpecID              *string           `json:"specid"`
	Deps                []string          `json:"deps"`
	MachineState        string            `json:"machineState"`
	DisplayState        string            `json:"displayState"`
	CycleOutcome        string            `json:"cycleOutcome"`
	CycleSeq            int               `json:"cycleSeq"`
	CurrentClaim        *CurrentClaim     `json:"currentClaim"`
	ActiveSubmission    *ActiveSubmission `json:"activeSubmission"`
	VerifiedAtBlockTs   *int64            `json:"verifiedAtBlockTs"`
	Votes               []VoteRecord      `json:"votes,omitempty"`
	LastWriteEventPinID *string           `json:"lastWriteEventPinId"`
	LastActivityTs      *int64            `json:"lastActivityTs"`
	Events              []EventRecord     `json:"events,omitempty"`

	// Internal accounting — not part of the frozen §5 key set.
	InTree  bool   `json:"-"`
	AggNote string `json:"-"`
}

type nodeState struct {
	id         string
	holder     *replayEvent // current holding claim; nil = node open
	holderID   string       // holding claim's pinId
	claimTs    int64
	sub        *replayEvent // current cycle's effective submission; nil = none
	subTs      int64
	state      string
	outcome    string
	cycleSeq   int
	ballots    map[string]string // bot -> last valid verdict on the active submission
	passes     int
	verifiedAt int64
	lastWrite  string
	lastActTs  int64
	votes      []VoteRecord
	events     []EventRecord
	aggNote    string
	inTree     bool
}

func (s *nodeState) subID() string {
	if s.sub == nil {
		return ""
	}
	return s.sub.pin.ID
}

func (s *nodeState) subAuthor() string {
	if s.sub == nil {
		return ""
	}
	return s.sub.pin.GlobalMetaID
}

// ReplayResult is the conclusion of F(E,B) for one task root.
type ReplayResult struct {
	RootPinID    string         `json:"rootPinId"`
	TaskTitle    string         `json:"taskTitle"`
	Publisher    string         `json:"publisher"`
	Policy       map[string]any `json:"policy,omitempty"`
	Quorum       int            `json:"quorum"`
	TTLHours     int            `json:"claimTtlHours"`
	WindowHours  int            `json:"verifyWindowHours"`
	TreeID       string         `json:"treeId,omitempty"`
	SpecID       string         `json:"specId,omitempty"`
	TreeResolved bool           `json:"treeResolved"`
	SpecResolved bool           `json:"specResolved"`
	RootVerified bool           `json:"rootVerified"`
	Boundary     Boundary       `json:"boundary"`
	// §7 echo: every replay response carries the evaluated boundary at the
	// top level as well (the HTTP layer also echoes it in headers).
	EvaluatedAtBlock   int64                  `json:"evaluatedAtBlock"`
	EvaluatedAtBlockTs int64                  `json:"evaluatedAtBlockTs"`
	Nodes              map[string]*NodeReplay `json:"nodes"`
	NodeOrder          []string               `json:"nodeOrder"`
	Unresolved         []EventRecord          `json:"unresolved,omitempty"`
	// Scope accounting (transparency; additive fields).
	EventCount       int            `json:"eventCount"`
	EventCountByPath map[string]int `json:"eventCountByPath"`
	ExcludedMempool  int            `json:"excludedMempool"`
	RejectedPayload  int            `json:"rejectedPayload"`
	OrderFallback    bool           `json:"orderFallback"`
}

// admitEvents applies the freeze collection boundary: dedup by pinId (the
// event unique key is (pinId, version); the list API exposes no version, so
// one pinId = one event), mempool excluded and counted, payload-accepted
// only, optional to-block filter (Height 0 disables it).
func admitEvents(events map[string][]*Pin, boundary Boundary) (admitted map[string][]*replayEvent, mempool, rejected int) {
	admitted = map[string][]*replayEvent{}
	seen := map[string]bool{}
	for _, path := range EventPaths {
		for _, p := range events[path] {
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			if p.GenesisHeight < 0 {
				mempool++
				continue
			}
			if boundary.Height > 0 && p.GenesisHeight > boundary.Height {
				continue
			}
			v := resolvePayload(p)
			if !v.Accepted() {
				rejected++
				continue
			}
			admitted[path] = append(admitted[path], &replayEvent{pin: p, body: v.Payload, ts: pinTimestamp(p)})
		}
	}
	return admitted, mempool, rejected
}

// DeriveBoundary picks the evaluation boundary for an enumerated event set:
// an explicit to-block when given, else the newest confirmed event height
// with that height's newest block timestamp (the list API carries a per-pin
// block time; an exact chain-RPC block timestamp is a server-side refinement).
func DeriveBoundary(events map[string][]*Pin, toBlock int64) Boundary {
	height := int64(-1)
	ts := int64(0)
	for _, path := range EventPaths {
		for _, p := range events[path] {
			if p.GenesisHeight < 0 {
				continue
			}
			if toBlock > 0 && p.GenesisHeight > toBlock {
				continue
			}
			switch {
			case p.GenesisHeight > height:
				height, ts = p.GenesisHeight, p.Timestamp
			case p.GenesisHeight == height && p.Timestamp > ts:
				ts = p.Timestamp
			}
		}
	}
	if height < 0 {
		height = 0
	}
	return Boundary{Height: height, BlockTs: ts}
}

// Replay computes F(E,B) for one task root. events maps each protocol path to
// its enumerated pins (raw, unfiltered); the function applies the freeze
// collection rules itself: mempool excluded, payload-accepted only, taskid
// scope, to-boundary filter.
func Replay(events map[string][]*Pin, root string, boundary Boundary) (*ReplayResult, error) {
	res := &ReplayResult{
		RootPinID:          root,
		Boundary:           boundary,
		EvaluatedAtBlock:   boundary.Height,
		EvaluatedAtBlockTs: boundary.BlockTs,
		Nodes:              map[string]*NodeReplay{},
		EventCountByPath:   map[string]int{},
		Policy:             map[string]any{},
	}
	// ---- admit events: confirmed, in-boundary, payload-accepted ----
	admitted, mempool, rejected := admitEvents(events, boundary)
	res.ExcludedMempool = mempool
	res.RejectedPayload = rejected
	for _, path := range EventPaths {
		res.EventCountByPath[path] = len(admitted[path])
		res.EventCount += len(admitted[path])
	}

	// ---- task root ----
	var taskEv *replayEvent
	for _, ev := range admitted[PathTask] {
		if ev.pin.ID == root {
			taskEv = ev
			break
		}
	}
	if taskEv == nil {
		return nil, fmt.Errorf("task root %s not found among admitted /protocols/metatask/task events", root)
	}
	res.Publisher = taskEv.pin.GlobalMetaID
	res.TaskTitle = payloadString(taskEv.body, "title")
	res.TreeID = payloadString(taskEv.body, "treeid")
	res.SpecID = payloadString(taskEv.body, "specid")
	if p, ok := taskEv.body["policy"].(map[string]any); ok {
		res.Policy = p
	}
	res.Quorum = policyInt(res.Policy, "verify_quorum", 2)
	res.TTLHours = policyInt(res.Policy, "claim_ttl_hours", 0)
	res.WindowHours = policyInt(res.Policy, "verify_window_hours", 0)

	// spec resolution (panorama直链用).
	for _, ev := range admitted[PathSpec] {
		if ev.pin.ID == res.SpecID {
			res.SpecResolved = true
			break
		}
	}

	// tree: node universe + parent/title/kind/specid/deps.
	tree := map[string]treeNodeInfo{}
	if res.TreeID != "" {
		for _, ev := range admitted[PathTree] {
			if ev.pin.ID != res.TreeID {
				continue
			}
			if nodes, ok := ev.body["nodes"].([]any); ok {
				for _, n := range nodes {
					obj, ok := n.(map[string]any)
					if !ok {
						continue
					}
					id := payloadString(obj, "id")
					if id == "" {
						continue
					}
					tm := tree[id]
					if v, ok := obj["parent"]; ok && v != nil {
						if ps, ok2 := v.(string); ok2 {
							tm.parent = &ps
						}
					}
					tm.title = payloadString(obj, "title")
					tm.kind = payloadString(obj, "kind")
					if v, ok := obj["specid"]; ok && v != nil {
						if ss, ok2 := v.(string); ok2 {
							tm.specid = &ss
						}
					}
					tm.deps = payloadStrings(obj, "deps")
					if tm.deps == nil {
						tm.deps = []string{}
					}
					if pv, ok := obj["params"].(map[string]any); ok {
						tm.params = pv
					} else {
						tm.params = map[string]any{}
					}
					tree[id] = tm
				}
				res.TreeResolved = true
			}
			break
		}
	}
	rootAuthor := taskEv.pin.GlobalMetaID

	// ---- node states ----
	states := map[string]*nodeState{}
	getState := func(id string) *nodeState {
		if s, ok := states[id]; ok {
			return s
		}
		s := &nodeState{id: id, state: StateOpen, outcome: OutcomeNone, ballots: map[string]string{}}
		if _, ok := tree[id]; ok {
			s.inTree = true
		}
		states[id] = s
		return s
	}
	for id := range tree {
		getState(id)
	}

	// merged timeline: claim + release + submission (+ verify) in ≺ order.
	var timeline []*replayEvent
	inScope := func(ev *replayEvent) bool { return payloadString(ev.body, "taskid") == root }
	for _, ev := range admitted[PathClaim] {
		if inScope(ev) {
			timeline = append(timeline, ev)
		}
	}
	for _, ev := range admitted[PathRelease] {
		if inScope(ev) {
			timeline = append(timeline, ev)
		}
	}
	for _, ev := range admitted[PathSubmission] {
		if inScope(ev) {
			timeline = append(timeline, ev)
		}
	}
	timeline = append(timeline, admitted[PathVerify]...) // verify filtered by target at processing time
	sort.SliceStable(timeline, func(i, j int) bool { return replayLess(timeline[i], timeline[j]) })

	// submission ownership: active (drives) and ever-attached (records only).
	activeSubOwner := map[string]string{}   // submission pinId -> node (current cycle only)
	attachedSubOwner := map[string]string{} // submission pinId -> node (any cycle)

	ttlDeadline := func(claimTs int64) int64 {
		if res.TTLHours <= 0 {
			return 0
		}
		return claimTs + int64(res.TTLHours)*3600
	}
	windowDeadline := func(subTs int64) int64 {
		if res.WindowHours <= 0 {
			return 0
		}
		return subTs + int64(res.WindowHours)*3600
	}

	touch := func(s *nodeState, ev *replayEvent) {
		s.lastWrite = ev.pin.ID
		if ev.ts > s.lastActTs {
			s.lastActTs = ev.ts
		}
	}

	addEvent := func(s *nodeState, ev *replayEvent, validity, reason, summary string) {
		s.events = append(s.events, EventRecord{
			EventPinID: ev.pin.ID, Path: ev.pin.Path, Version: ev.pin.Version,
			BlockHeight: ev.pin.GenesisHeight, BlockTs: ev.ts, Author: ev.pin.GlobalMetaID,
			Kind: PoolName(ev.pin.Path), Summary: summary, Validity: validity, IgnoreReason: reason,
		})
	}

	reject := func(s *nodeState, ev *replayEvent) {
		// 裁定三: any effective fail sends the node back to open immediately;
		// the current cycle's submission stops being active (target_not_active
		// for any later vote on it).
		s.state = StateOpen
		s.outcome = OutcomeRejected
		delete(activeSubOwner, s.subID())
		s.sub, s.subTs = nil, 0
		s.holder, s.holderID, s.claimTs = nil, "", 0
		touch(s, ev)
	}

	for _, ev := range timeline {
		switch ev.pin.Path {
		case PathClaim:
			node := payloadString(ev.body, "node")
			s := getState(node)
			summary := "claim node=" + node
			switch {
			case s.state == StateVerified:
				// verified is terminal: claims bind only on open nodes.
				addEvent(s, ev, "ignored", IgnoreNodeNotOpen, summary)
				touch(s, ev)
			case s.holder != nil:
				addEvent(s, ev, "ignored", IgnoreSupersededClaim, summary)
				touch(s, ev)
			default:
				// new cycle
				s.holder, s.holderID, s.claimTs = ev, ev.pin.ID, ev.ts
				s.sub, s.subTs = nil, 0
				s.ballots = map[string]string{}
				s.passes = 0
				s.cycleSeq++
				s.outcome = OutcomeNone
				s.state = StateClaimed
				addEvent(s, ev, "counted", "", summary)
				touch(s, ev)
			}

		case PathRelease:
			node := payloadString(ev.body, "node")
			s := getState(node)
			summary := "release claimid=" + payloadString(ev.body, "claimid")
			if s.state == StateVerified || s.holder == nil || s.holderID != payloadString(ev.body, "claimid") {
				addEvent(s, ev, "ignored", IgnoreNoMatchingClaim, summary)
				touch(s, ev)
				break
			}
			s.holder, s.holderID, s.claimTs = nil, "", 0
			delete(activeSubOwner, s.subID())
			s.sub, s.subTs = nil, 0
			s.ballots = map[string]string{}
			s.passes = 0
			s.state = StateOpen
			s.outcome = OutcomeReleased
			addEvent(s, ev, "counted", "", summary)
			touch(s, ev)

		case PathSubmission:
			node := payloadString(ev.body, "node")
			s := getState(node)
			summary := "submission node=" + node
			switch {
			case s.holder == nil || payloadString(ev.body, "claimid") != s.holderID:
				addEvent(s, ev, "ignored", IgnoreClaimIDMismatch, summary)
				touch(s, ev)
			case ev.pin.GlobalMetaID != s.holder.pin.GlobalMetaID:
				// Freeze submission row: 作者=当期持有人 — the right claim
				// ridden by anyone else never attaches.
				addEvent(s, ev, "ignored", IgnoreSubmitterNotHolder, summary)
				touch(s, ev)
			case s.sub != nil:
				addEvent(s, ev, "ignored", IgnoreNotEarliestInCyc, summary)
				touch(s, ev)
			default:
				s.sub, s.subTs = ev, ev.ts
				s.state = StateSubmitted
				activeSubOwner[ev.pin.ID] = node
				attachedSubOwner[ev.pin.ID] = node
				addEvent(s, ev, "counted", "", summary)
				touch(s, ev)
			}

		case PathVerify:
			target := payloadString(ev.body, "targetid")
			vote := VoteRecord{
				VerifyPinID:      ev.pin.ID,
				Voter:            ev.pin.GlobalMetaID,
				Verdict:          payloadString(ev.body, "verdict"),
				HasFailreason:    payloadPresent(ev.body, "failreason"),
				HasSemanticCheck: payloadPresent(ev.body, "semantic_check"),
				BlockHeight:      ev.pin.GenesisHeight,
				BlockTs:          ev.ts,
			}
			node, active := activeSubOwner[target]
			if !active {
				// Not the current cycle's active submission: recorded, never drives.
				if owner, attached := attachedSubOwner[target]; attached {
					s := getState(owner)
					vote.Counts = false
					vote.IgnoreReason = IgnoreTargetNotActive
					s.votes = append(s.votes, vote)
					addEvent(s, ev, "ignored", IgnoreTargetNotActive, "verify target="+target)
					touch(s, ev)
				} else {
					res.Unresolved = append(res.Unresolved, EventRecord{
						EventPinID: ev.pin.ID, Path: ev.pin.Path, Version: ev.pin.Version,
						BlockHeight: ev.pin.GenesisHeight, BlockTs: ev.ts, Author: ev.pin.GlobalMetaID,
						Kind: "verify", Summary: "verify target=" + target,
						Validity: "ignored", IgnoreReason: IgnoreUnresolvedTarget,
					})
				}
				break
			}
			s := getState(node)
			s.votes = append(s.votes, vote)
			idx := len(s.votes) - 1
			mark := func(reason string) {
				s.votes[idx].Counts = false
				s.votes[idx].IgnoreReason = reason
				addEvent(s, ev, "ignored", reason, "verify verdict="+vote.Verdict+" target="+target)
				touch(s, ev)
			}
			gateReason := gateInvalid(ev.body, ev.pin.GenesisHeight)
			windowOver := false
			if wd := windowDeadline(s.subTs); wd != 0 && ev.ts > wd {
				windowOver = true
			}
			switch {
			case vote.Voter == s.subAuthor():
				mark(IgnoreVoterIsSubmitter)
			case vote.Voter == rootAuthor:
				mark(IgnoreVoterIsRootAuthor)
			case !payloadPresent(ev.body, "method"):
				mark(IgnoreEmptyMethod)
			case gateReason != "":
				mark(gateReason)
			case windowOver:
				// R2: votes arriving after the review-window close never count;
				// the fact is kept in votes[]/events.
				mark(IgnoreAfterWindowClose)
			case vote.Verdict == "invalid":
				// Explicit verdict=invalid: sends back, occupies no fail slot
				// (protocol verifyCount, all heights — freeze §3).
				s.votes[idx].Counts = false
				s.votes[idx].IgnoreReason = IgnoreVerdictInvalid
				addEvent(s, ev, "ignored", IgnoreVerdictInvalid, "verify verdict=invalid target="+target)
				reject(s, ev)
			case vote.Verdict == "fail":
				s.ballots[vote.Voter] = "fail"
				s.votes[idx].Counts = true
				addEvent(s, ev, "counted", "", "verify verdict=fail target="+target)
				if ev.pin.GenesisHeight < HAct && !payloadPresent(ev.body, "evidence") {
					// v1.1 semantics below the switch: a fail without evidence
					// neither rejects nor blocks verification (freeze §3.1).
					touch(s, ev)
					break
				}
				reject(s, ev)
			case vote.Verdict == "pass":
				if prev, ok := s.ballots[vote.Voter]; !ok || prev != "pass" {
					s.passes++
				}
				s.ballots[vote.Voter] = "pass"
				s.votes[idx].Counts = true
				addEvent(s, ev, "counted", "", "verify verdict=pass target="+target)
				if s.passes >= res.Quorum && s.state != StateVerified {
					s.state = StateVerified
					s.verifiedAt = ev.ts
					// The cycle is complete: its submission stops being the
					// 当期生效 target, so later ballots on it are recorded as
					// target_not_active and never drive (freeze §3 — verified
					// is terminal, 协议无 un-verify; a post-verify fail must
					// not send the node back).
					delete(activeSubOwner, s.subID())
				}
				touch(s, ev)
			default:
				mark(IgnoreUnknownVerdict)
			}
		}
	}

	// ---- boundary expiry (freeze §3 [B] rows; strictly-greater deadlines) ----
	for _, s := range states {
		if s.state == StateVerified {
			continue
		}
		switch {
		case s.state == StateClaimed && s.holder != nil:
			if wd := ttlDeadline(s.claimTs); wd != 0 && boundary.BlockTs > wd {
				s.state = StateOpen
				s.outcome = OutcomeExpiredClaim
				s.holder, s.holderID, s.claimTs = nil, "", 0
			}
		case s.state == StateSubmitted && s.sub != nil:
			if wd := windowDeadline(s.subTs); wd != 0 && boundary.BlockTs > wd && s.passes < res.Quorum {
				s.state = StateOpen
				s.outcome = OutcomeExpiredReviewWindow
				delete(activeSubOwner, s.subID())
				s.sub, s.subTs = nil, 0
				s.ballots = map[string]string{}
				s.passes = 0
			}
		}
	}

	// ---- aggregation: parent verified = all children verified && own passed ----
	aggVerified := map[string]bool{}
	var agg func(id string) bool
	agg = func(id string) bool {
		if v, ok := aggVerified[id]; ok {
			return v
		}
		s := states[id]
		if s == nil {
			aggVerified[id] = false
			return false
		}
		own := s.state == StateVerified
		all := true
		for _, cid := range treeChildren(tree, id) {
			if !agg(cid) {
				all = false
			}
		}
		out := own && all
		if own && !all {
			s.aggNote = joinNote(s.aggNote, "own quorum met but not all child nodes verified")
		}
		aggVerified[id] = out
		return out
	}

	// ---- render ----
	for id, s := range states {
		machine := s.state
		if s.state == StateVerified && !agg(id) {
			// Aggregation gates the parent's verified state (freeze §3).
			machine = StateSubmitted
		}
		nr := &NodeReplay{
			ID: id, Parent: tree[id].parent, Title: tree[id].title, Kind: tree[id].kind,
			Params: tree[id].params, SpecID: tree[id].specid, Deps: tree[id].deps,
			MachineState: machine,
			DisplayState: displayStateOf(machine, s.outcome),
			CycleOutcome: s.outcome, CycleSeq: s.cycleSeq,
			Votes:  s.votes,
			Events: s.events,
			InTree: s.inTree, AggNote: s.aggNote,
		}
		if s.verifiedAt > 0 {
			vts := s.verifiedAt
			nr.VerifiedAtBlockTs = &vts
		}
		if s.lastWrite != "" {
			lw := s.lastWrite
			nr.LastWriteEventPinID = &lw
		}
		if s.lastActTs > 0 {
			lts := s.lastActTs
			nr.LastActivityTs = &lts
		}
		if _, isTree := tree[id]; !isTree {
			nr.AggNote = joinNote(nr.AggNote, "node not declared in tree event")
		}
		if s.holder != nil {
			nr.CurrentClaim = &CurrentClaim{
				ClaimPinID: s.holderID, Claimant: s.holder.pin.GlobalMetaID,
				BlockHeight: s.holder.pin.GenesisHeight, BlockTs: s.claimTs,
				TTLDeadlineTs: ttlDeadline(s.claimTs),
			}
		}
		if s.sub != nil {
			as := &ActiveSubmission{
				SubmissionPinID: s.sub.pin.ID, Submitter: s.sub.pin.GlobalMetaID,
				BlockHeight: s.sub.pin.GenesisHeight, BlockTs: s.subTs,
				Hash:             payloadString(s.sub.body, "hash"),
				ContentType:      payloadString(s.sub.body, "contentType"),
				Attachment:       payloadString(s.sub.body, "attachment"),
				ChildIDs:         payloadStrings(s.sub.body, "childids"),
				WindowDeadlineTs: windowDeadline(s.subTs),
			}
			if r, ok := s.sub.body["result"].(map[string]any); ok {
				as.Result = r
			}
			nr.ActiveSubmission = as
		}
		res.Nodes[id] = nr
	}
	res.NodeOrder = sortedNodeIDs(states)
	if rootID := treeRootOf(tree); rootID != "" {
		if rootState := states[rootID]; rootState != nil {
			res.RootVerified = agg(rootID)
		}
	}
	return res, nil
}

// displayStateOf is the frozen six-state display mapping (freeze §3):
// open+none→open；open+released→open；open+rejected→rejected；
// open+expired_*→expired；claimed→claimed；submitted→submitted；verified→verified.
func displayStateOf(state, outcome string) string {
	switch {
	case state == StateOpen && outcome == OutcomeRejected:
		return "rejected"
	case state == StateOpen && (outcome == OutcomeExpiredClaim || outcome == OutcomeExpiredReviewWindow):
		return "expired"
	case state == StateOpen:
		return "open"
	case state == StateClaimed:
		return "claimed"
	case state == StateSubmitted:
		return "submitted"
	case state == StateVerified:
		return "verified"
	}
	return state
}

func treeChildren(tree map[string]treeNodeInfo, id string) []string {
	var out []string
	for nid, tm := range tree {
		if tm.parent != nil && *tm.parent == id {
			out = append(out, nid)
		}
	}
	sort.Strings(out)
	return out
}

func treeRootOf(tree map[string]treeNodeInfo) string {
	var roots []string
	for id, tm := range tree {
		if tm.parent == nil {
			roots = append(roots, id)
		}
	}
	if len(roots) == 0 {
		return ""
	}
	sort.Strings(roots) // deterministic pick when a malformed tree has several
	return roots[0]
}

func sortedNodeIDs(states map[string]*nodeState) []string {
	out := make([]string, 0, len(states))
	for id := range states {
		out = append(out, id)
	}
	// (len, lexicographic) — same node ordering the pool read path uses.
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// payloadStrings reads a string-array field (e.g. childids, deps).
func payloadStrings(payload map[string]any, key string) []string {
	if payload == nil {
		return nil
	}
	arr, ok := payload[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
