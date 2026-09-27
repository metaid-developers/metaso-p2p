package metatask

import (
	"encoding/json"
	"fmt"
	"sort"
)

// v1.2.1 replay renderer. The semantics live in v121.go (ReplayV121 — the
// three-engine consistency engine, ported from the IDBots TS reference); this
// file keeps the frozen §5 projection shape (字段即契约 — field names are the
// API contract) and renders it from the v1.2.1 conclusion:
//
//   - conclusion = F(E, B): a pure function over the nine-path event set E
//     (task/tree/spec/claim/release/submission/verify/amend/challenge) and
//     the evaluation boundary B (block height + that block's timestamp);
//   - the unified ordered walk, #8/#9 gates (H_ACT), the v1.2 feature gates
//     (H_ACT2), supersedeid, /amend, /challenge, the aggregation
//     precondition, settlement and eventSetHash are all v1.2.1 semantics —
//     the pre-upgrade freeze calibration (the 448-field comparison against
//     the on-chain skill engine read path) is realigned here per v1.2.1;
//   - mempool pins (genesisHeight < 0) never enter E; payload §〇 rejection
//     stays a collection-boundary concern (admitEvents);
//   - expiry (claim TTL / review window / challenge TTL) reads the boundary
//     timestamp only, never a wall clock.
//
// Validated against the v1.2.1 conformance vector set (16/16,
// testdata/metatask-conformance-vectors.v1.2.1.json + the TS-engine golden
// parity harness) and the vendored skill D8 guard fixtures (13/13 — their
// shapes do not reach the v1.1→v1.2.1 divergence points).

const (
	// HAct is the frozen #8/#9 vote-gate switch (ruling pin chain in
	// SemanticGateRulingPins; v1.2.1 activation.H_ACT, unchanged).
	HAct = 190000

	// ReplayAlgoVersion identifies the algorithm this engine implements. Any
	// semantic change bumps it and forces a full replay (D10). v2.0.0 = the
	// v1.2.1 registration-body semantics (supersedes the freeze draft).
	ReplayAlgoVersion = "metatask-replay-indexer/2.0.0+v1.2.1"
	// ProtocolVersion / ProtocolPinID anchor the protocol the semantics come
	// from (v1.2.1 registration body).
	ProtocolVersion = ProtocolVersionV121
	ProtocolPinID   = ProtocolPinIDV121
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
// the #8/#9 gate reasons; the v1.2.1 reasons (duplicate_without_supersede,
// supersede_predicate_failed, same_side_roster) mirror the reference engine's
// closed set; the rest carry the counting preconditions.
const (
	IgnoreSupersededClaim    = "superseded_by_earlier_claim"
	IgnoreNotEarliestInCyc   = "not_earliest_in_cycle" // pre-v1.2.1 name, kept for history
	IgnoreClaimIDMismatch    = "claimid_mismatch"
	IgnoreSubmitterNotHolder = "submitter_not_holder" // pre-v1.2.1 name (v1.2.1 attaches by claimid)
	IgnoreStateNoOp          = "state_no_op"
	IgnoreTargetNotActive    = "target_not_active"
	IgnoreUnresolvableTarget = "unresolvable_target"
	IgnoreDuplicateVote      = "duplicate_vote_superseded"
	IgnoreVoterIsSubmitter   = "voter_is_submitter"
	IgnoreVoterIsTaskAuthor  = "voter_is_task_author"
	IgnoreEmptyMethod        = "empty_method"
	IgnoreAfterWindowClose   = "after_window_close" // pre-v1.2.1 name
	IgnoreVerdictInvalid     = "verdict_invalid"
	IgnoreUnknownVerdict     = "unknown_verdict"

	IgnoreDuplicateWithoutSupersede = "duplicate_without_supersede"
	IgnoreSupersedePredicateFailed  = "supersede_predicate_failed"
	IgnoreSameSideRoster            = "same_side_roster"
)

// Boundary is B of F(E,B) (freeze §2): block height and that block's timestamp.
// Every expiry decision reads B.BlockTs (D2) — never a wall clock. A Height of
// 0 disables the to-block event filter (used by the vector harness).
type Boundary struct {
	Height  int64 `json:"height"`
	BlockTs int64 `json:"blockTs"`
}

// gateInvalid kept for the freeze-era §3.1 narrative; v1.2.1 gating lives in
// v121GateReason (v121.go).
func gateInvalid(b map[string]any, height int64) string {
	return v121GateReason(b, height, HAct)
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

// CurrentClaim is the freeze §5 node claim view. TTLDeadlineTs is null when
// the policy has no TTL (A2-3: absence is null, never zero).
type CurrentClaim struct {
	ClaimPinID    string `json:"claimPinId"`
	Claimant      string `json:"claimant"`
	BlockHeight   int64  `json:"blockHeight"`
	BlockTs       int64  `json:"blockTs"`
	TTLDeadlineTs *int64 `json:"ttlDeadlineTs"`
}

// ActiveSubmission is the freeze §5 node submission view. ContentType and
// Attachment are null when the payload omits them (A2-3: an absent value is
// JSON null — an empty string is reserved for a real empty-string value).
type ActiveSubmission struct {
	SubmissionPinID string         `json:"submissionPinId"`
	Submitter       string         `json:"submitter"`
	BlockHeight     int64          `json:"blockHeight"`
	BlockTs         int64          `json:"blockTs"`
	Hash            string         `json:"hash"`
	ContentType     *string        `json:"contentType"`
	Attachment      *string        `json:"attachment"`
	ChildIDs        []string       `json:"childids"`
	Result          map[string]any `json:"result"`
	// WindowDeadlineTs drives the review-window expiry but is not a frozen §5 key.
	WindowDeadlineTs int64 `json:"-"`
}

// VoteRecord is one verify ballot, counted or not (freeze: votes[] 含不计票票).
// BlockTs drives lastActivity internally; the frozen §5 vote keys end at
// blockHeight, so it stays out of the body.
type VoteRecord struct {
	VerifyPinID      string  `json:"verifyPinId"`
	Voter            string  `json:"voter"`
	Verdict          string  `json:"verdict"`
	HasEvidence      bool    `json:"hasEvidence"`
	HasFailreason    bool    `json:"hasFailreason"`
	HasSemanticCheck bool    `json:"hasSemanticCheck"`
	Counts           bool    `json:"counts"`
	IgnoreReason     *string `json:"ignoreReason"`
	BlockHeight      int64   `json:"blockHeight"`
	BlockTs          int64   `json:"blockTs"`
	txIdx            int64   // A4-3 ordering key, not serialized
	ver              int64   // A4-3 ordering key, not serialized
}

// EventRecord is the freeze §5 node-detail event row (每条可回源).
type EventRecord struct {
	EventPinID   string  `json:"eventPinId"`
	Path         string  `json:"path"`
	Version      int64   `json:"version"`
	BlockHeight  int64   `json:"blockHeight"`
	BlockTs      int64   `json:"blockTs"`
	Author       string  `json:"author"`
	Kind         string  `json:"kind"`
	Summary      string  `json:"summary"`
	Validity     string  `json:"validity"` // counted|ignored
	IgnoreReason *string `json:"ignoreReason"`
	txIdx        int64   // A4-3 ordering key, not serialized
}

// NodeReplay is one node's replay conclusion. Its JSON keys are the frozen
// §5 nodes[i] set plus the v1.2.1 additions (weight/disputed — additive per
// D7 只加不改). Empty lastWrite/lastActivity/verifiedAt serialize as null.
type NodeReplay struct {
	ID                  string            `json:"id"`
	Parent              *string           `json:"parent"`
	Title               string            `json:"title"`
	Kind                string            `json:"kind"`
	Params              map[string]any    `json:"params"`
	SpecID              *string           `json:"specid"`
	Deps                []string          `json:"deps"`
	Weight              *int64            `json:"weight"`
	MachineState        string            `json:"machineState"`
	DisplayState        string            `json:"displayState"`
	CycleOutcome        string            `json:"cycleOutcome"`
	CycleSeq            int               `json:"cycleSeq"`
	Disputed            bool              `json:"disputed"`
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

// ReplayResult is the conclusion of F(E,B) for one task root. The frozen §7
// keys are unchanged; the v1.2.1 block (taskComplete / eventSetHash /
// boundaryBlock / settlement / ignoredEvents / disputedNodes / amendHead /
// hAct2) is additive (D7).
type ReplayResult struct {
	RootPinID    string         `json:"rootPinId"`
	TaskTitle    string         `json:"taskTitle"`
	Publisher    string         `json:"publisher"`
	Policy       map[string]any `json:"policy,omitempty"`
	Quorum       int            `json:"quorum"`
	TTLHours     int            `json:"claimTtlHours"`
	WindowHours  int            `json:"verifyWindowHours"`
	TreeID       string         `json:"treeid,omitempty"`
	SpecID       string         `json:"specid,omitempty"`
	TreeResolved bool           `json:"treeResolved"`
	SpecResolved bool           `json:"specResolved"`
	RootVerified bool           `json:"rootVerified"`
	// §7 echo: every replay response carries the evaluated boundary at the
	// top level (A1-5).
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

	// v1.2.1 additions (registration body settlement / aggregation).
	TaskComplete  bool           `json:"taskComplete"`
	EventSetHash  string         `json:"eventSetHash"`
	BoundaryBlock int64          `json:"boundaryBlock"`
	Settlement    map[string]any `json:"settlement"` // null until the task settles
	IgnoredEvents []IgnoredEvent `json:"ignoredEvents"`
	DisputedNodes []string       `json:"disputedNodes"`
	AmendHead     string         `json:"amendHead,omitempty"`
	HAct2Height   int64          `json:"hAct2"`
}

// admitEvents applies the collection boundary: dedup by pinId, mempool
// excluded and counted, payload-accepted only, optional to-block filter
// (Height 0 disables it).
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

// reasonPtr returns a pointer to a fresh string copy for the nullable
// ignoreReason fields (A2-3: counted rows serialize the key with null).
func reasonPtr(s string) *string { return &s }

// DeriveBoundary picks the evaluation boundary for an enumerated event set:
// an explicit to-block when given, else the newest confirmed event height
// with that height's newest block timestamp.
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

// chainEventsFrom converts admitted pins into the v1.2.1 engine's neutral
// event form (path short names; timestamps normalized to milliseconds — pin
// timestamps are chain seconds).
func chainEventsFrom(admitted map[string][]*replayEvent) []*ChainEvent {
	out := []*ChainEvent{}
	for _, path := range EventPaths {
		for _, ev := range admitted[path] {
			out = append(out, &ChainEvent{
				PinID: ev.pin.ID, Path: PoolName(path), Author: ev.pin.GlobalMetaID,
				Height: ev.pin.GenesisHeight, TxIndex: ev.pin.TxIndex,
				TimestampMs: ev.ts * 1000, Body: ev.body,
			})
		}
	}
	return out
}

// Replay computes F(E,B) for one task root under v1.2.1 semantics and renders
// the frozen §5 projection. events maps each protocol path to its enumerated
// pins (raw, unfiltered); the collection rules (mempool, payload §〇,
// to-boundary filter) are applied here.
func Replay(events map[string][]*Pin, root string, boundary Boundary) (*ReplayResult, error) {
	res := &ReplayResult{
		RootPinID:          root,
		EvaluatedAtBlock:   boundary.Height,
		EvaluatedAtBlockTs: boundary.BlockTs,
		Nodes:              map[string]*NodeReplay{},
		EventCountByPath:   map[string]int{},
		Policy:             map[string]any{},
		IgnoredEvents:      []IgnoredEvent{},
		DisputedNodes:      []string{},
		HAct2Height:        HAct2,
	}
	admitted, mempool, rejected := admitEvents(events, boundary)
	res.ExcludedMempool = mempool
	res.RejectedPayload = rejected
	for _, path := range EventPaths {
		res.EventCountByPath[path] = len(admitted[path])
		res.EventCount += len(admitted[path])
	}

	v, err := ReplayV121(chainEventsFrom(admitted), V121Options{RootPinID: root, NowMs: boundary.BlockTs * 1000})
	if err != nil {
		return nil, err
	}

	res.Publisher = v.Publisher
	res.TaskTitle = v.Title
	res.TreeID = v.TreePinID
	res.SpecID = v.SpecID
	res.Policy = v.RawPolicy
	res.Quorum = v.Quorum
	res.TTLHours = v.TTLHours
	res.WindowHours = v.WindowHours
	res.TreeResolved = v.TreePinID != "" && v.EffectiveTree != nil
	for _, ev := range admitted[PathSpec] {
		if ev.pin.ID == res.SpecID {
			res.SpecResolved = true
			break
		}
	}

	res.TaskComplete = v.TaskComplete
	res.RootVerified = v.TaskComplete
	res.EventSetHash = v.EventSetHash
	res.BoundaryBlock = v.BoundaryBlock
	res.AmendHead = v.AmendHead
	res.IgnoredEvents = v.IgnoredEvents
	for _, node := range v.NodeOrder {
		if v.DisputedNodes[node] {
			res.DisputedNodes = append(res.DisputedNodes, node)
		}
	}
	if v.Settlement != nil {
		var m map[string]any
		if err := json.Unmarshal(v.Settlement.Canonical, &m); err != nil {
			return nil, fmt.Errorf("decode settlement manifest: %w", err)
		}
		res.Settlement = m
	}

	// Verify pins whose targetid matches no submission event at all stay
	// unresolved (A1-6 zero-drop); votes on another task's submissions are
	// that root's business and never pollute this root's set.
	globalSubTask := map[string]string{}
	for _, ev := range admitted[PathSubmission] {
		globalSubTask[ev.pin.ID] = payloadString(ev.body, "taskid")
	}

	childrenOf := map[string][]string{}
	for _, id := range v.TreeOrder {
		n := v.EffectiveTree[id]
		if n == nil {
			continue
		}
		if p, isStr := n.Parent.(string); isStr {
			childrenOf[p] = append(childrenOf[p], id)
		}
	}

	for _, id := range v.NodeOrder {
		st := v.NodeStates[id]
		treeRec := v.EffectiveTree[id]

		machine := StateOpen
		switch {
		case st.Status == "verified":
			machine = StateVerified
		case st.Holder != nil && st.Cycle != nil && st.Cycle.Effective != nil:
			machine = StateSubmitted
		case st.Holder != nil:
			machine = StateClaimed
		}
		outcome := st.LastOutcome
		if outcome == "" {
			outcome = OutcomeNone
		}
		claimCycles := 0
		for _, ev := range v.ClaimEventsByNode[id] {
			if v.ClaimCounted[ev.PinID] {
				claimCycles++
			}
		}

		nr := &NodeReplay{
			ID:           id,
			MachineState: machine,
			DisplayState: displayStateOf(machine, outcome),
			CycleOutcome: outcome,
			CycleSeq:     claimCycles,
			Disputed:     st.Disputed,
			Votes:        []VoteRecord{},
			Events:       []EventRecord{},
			InTree:       st.InTree,
		}
		if treeRec != nil {
			if p, isStr := treeRec.Parent.(string); isStr {
				nr.Parent = &p
			}
			nr.Title = treeRec.Title
			nr.Kind = treeRec.Kind
			if s, isStr := treeRec.SpecID.(string); isStr {
				nr.SpecID = &s
			}
			nr.Params = treeRec.Params
			if nr.Params == nil {
				nr.Params = map[string]any{}
			}
			nr.Deps = treeRec.Deps
			if nr.Deps == nil {
				nr.Deps = []string{}
			}
			if st.Weight != nil {
				w := *st.Weight
				nr.Weight = &w
			}
		} else {
			nr.Params = map[string]any{}
			nr.Deps = []string{}
			nr.AggNote = joinNote(nr.AggNote, "node not declared in tree event")
		}
		if st.Cycle != nil && st.Cycle.Outcome == "verified" && !v.FinalVerified[id] {
			allKids := true
			for _, c := range childrenOf[id] {
				if !v.FinalVerified[c] {
					allKids = false
					break
				}
			}
			if allKids && len(childrenOf[id]) > 0 {
				nr.AggNote = joinNote(nr.AggNote, "aggregation childids do not correspond to the children's verified submissions")
			} else {
				nr.AggNote = joinNote(nr.AggNote, "own quorum met but not all child nodes verified")
			}
		}
		if st.VerifiedAtMs > 0 && v.FinalVerified[id] {
			vts := st.VerifiedAtMs / 1000
			nr.VerifiedAtBlockTs = &vts
		}

		if st.Holder != nil {
			nr.CurrentClaim = &CurrentClaim{
				ClaimPinID:  st.Holder.PinID,
				Claimant:    st.Holder.Claimant,
				BlockHeight: st.Holder.Height,
				BlockTs:     st.Holder.SinceMs / 1000,
			}
			if res.TTLHours > 0 {
				wd := (st.Holder.SinceMs + int64(res.TTLHours)*3_600_000) / 1000
				nr.CurrentClaim.TTLDeadlineTs = &wd
			}
		}
		if st.Cycle != nil && st.Cycle.Effective != nil {
			sub := st.Cycle.Effective
			as := &ActiveSubmission{
				SubmissionPinID: sub.PinID,
				Submitter:       sub.Author,
				BlockHeight:     sub.Height,
				BlockTs:         sub.AtMs / 1000,
				Hash:            payloadString(sub.Body, "hash"),
				ChildIDs:        payloadStrings(sub.Body, "childids"),
			}
			if as.ChildIDs == nil {
				as.ChildIDs = []string{}
			}
			if cv := payloadString(sub.Body, "contentType"); cv != "" {
				as.ContentType = &cv
			}
			if av := payloadString(sub.Body, "attachment"); av != "" {
				as.Attachment = &av
			}
			if r, ok := sub.Body["result"].(map[string]any); ok {
				as.Result = r
			}
			if res.WindowHours > 0 {
				as.WindowDeadlineTs = (sub.AtMs + int64(res.WindowHours)*3_600_000) / 1000
			}
			nr.ActiveSubmission = as
		}

		// -- per-node event stream + votes (A4-1: events[] mirrors votes[]) ----
		type row struct {
			rec  EventRecord
			vote *VoteRecord
			key  ordKey
		}
		rows := []row{}
		addRow := func(ev *ChainEvent, path, kind, validity, reason, summary string, vote *VoteRecord) {
			er := EventRecord{
				EventPinID: ev.PinID, Path: path, Version: 0,
				BlockHeight: ev.Height, BlockTs: ev.TimestampMs / 1000,
				Author: ev.Author, Kind: kind, Summary: summary, Validity: validity,
				txIdx: ev.TxIndex,
			}
			if reason != "" {
				er.IgnoreReason = reasonPtr(reason)
				if vote != nil {
					vote.Counts = false
					vote.IgnoreReason = reasonPtr(reason)
				}
			}
			rows = append(rows, row{rec: er, vote: vote, key: ordKeyOf(ev)})
		}

		for _, ev := range v.ClaimEventsByNode[id] {
			validity, reason := "counted", ""
			if !v.ClaimCounted[ev.PinID] {
				validity, reason = "ignored", IgnoreSupersededClaim
			}
			addRow(ev, PathClaim, "claim", validity, reason, "claim node="+id, nil)
		}
		for _, ev := range v.ReleaseEventsByNode[id] {
			validity, reason := "counted", ""
			if !v.ReleaseCounted[ev.PinID] {
				validity, reason = "ignored", IgnoreStateNoOp
			}
			addRow(ev, PathRelease, "release", validity, reason, "release claimid="+payloadString(ev.Body, "claimid"), nil)
		}
		for _, ev := range v.SubmissionEventsByNode[id] {
			validity, reason := "counted", ""
			if v.SubmissionCycle[ev.PinID] == nil {
				validity = "ignored"
				reason = v.IgnoredByPin[ev.PinID]
				if reason != IgnoreDuplicateWithoutSupersede && reason != IgnoreSupersedePredicateFailed {
					reason = IgnoreClaimIDMismatch
				}
			}
			addRow(ev, PathSubmission, "submission", validity, reason, "submission node="+id, nil)
		}
		effectivePin := ""
		if st.Cycle != nil && st.Cycle.Effective != nil {
			effectivePin = st.Cycle.Effective.PinID
		}
		for _, sub := range v.SubmissionEventsByNode[id] {
			for _, ev := range v.VerifyByTarget[sub.PinID] {
				d := v.VerifyDisposition[ev.PinID]
				vr := &VoteRecord{
					VerifyPinID:      ev.PinID,
					Voter:            ev.Author,
					Verdict:          payloadString(ev.Body, "verdict"),
					HasEvidence:      payloadPresent(ev.Body, "evidence"),
					HasFailreason:    payloadPresent(ev.Body, "failreason"),
					HasSemanticCheck: payloadPresent(ev.Body, "semantic_check"),
					Counts:           d != nil && d.Counts,
					BlockHeight:      ev.Height,
					BlockTs:          ev.TimestampMs / 1000,
					txIdx:            ev.TxIndex,
				}
				validity, reason := "counted", ""
				if d == nil || !d.Counts {
					validity = "ignored"
					reason = IgnoreUnknownVerdict
					if d != nil && d.Reason != "" {
						reason = d.Reason
					}
					if d != nil && d.Reason == "" && sub.PinID != effectivePin {
						reason = IgnoreTargetNotActive
					}
				}
				addRow(ev, PathVerify, "verify", validity, reason,
					"verify verdict="+vr.Verdict+" target="+sub.PinID, vr)
			}
		}
		sort.SliceStable(rows, func(i, j int) bool { return ordLess(rows[i].key, rows[j].key) })
		var lastKey ordKey
		haveLast := false
		for _, r := range rows {
			nr.Events = append(nr.Events, r.rec)
			if r.vote != nil {
				nr.Votes = append(nr.Votes, *r.vote)
			}
			if !haveLast || ordLess(lastKey, r.key) {
				lastKey, haveLast = r.key, true
				lw := r.rec.EventPinID
				nr.LastWriteEventPinID = &lw
				lts := r.rec.BlockTs
				nr.LastActivityTs = &lts
			}
		}
		res.Nodes[id] = nr
	}
	res.NodeOrder = append([]string{}, v.NodeOrder...)

	for _, ev := range admitted[PathVerify] {
		target := payloadString(ev.body, "targetid")
		if _, known := globalSubTask[target]; known {
			continue // that submission's own node carries it (this task or another)
		}
		res.Unresolved = append(res.Unresolved, EventRecord{
			EventPinID: ev.pin.ID, Path: ev.pin.Path, Version: ev.pin.Version,
			BlockHeight: ev.pin.GenesisHeight, BlockTs: ev.ts, Author: ev.pin.GlobalMetaID,
			Kind: "verify", Summary: "verify target=" + target,
			Validity: "ignored", IgnoreReason: reasonPtr(IgnoreUnresolvableTarget),
			txIdx: ev.pin.TxIndex,
		})
	}
	sort.SliceStable(res.Unresolved, func(i, j int) bool {
		return replayLess(&replayEvent{pin: &Pin{GenesisHeight: res.Unresolved[i].BlockHeight, TxIndex: res.Unresolved[i].txIdx, Version: res.Unresolved[i].Version, ID: res.Unresolved[i].EventPinID}}, &replayEvent{pin: &Pin{GenesisHeight: res.Unresolved[j].BlockHeight, TxIndex: res.Unresolved[j].txIdx, Version: res.Unresolved[j].Version, ID: res.Unresolved[j].EventPinID}})
	})
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
