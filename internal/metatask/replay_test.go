package metatask

import (
	"encoding/json"
	"fmt"
	"testing"
)

// Unit vectors for the freeze engine (v1.1 §3/§3.1), beyond the skill's 11
// D8 fixtures: the edges the freeze adds or the vectors do not reach —
// evidence-conditional rejection below H_ACT, aggregation gating, boundary
// filtering, dedup, and input-order determinism.
//
// Timestamps are epoch-scale so the 48h/24h policy windows stay open at the
// default evaluation boundary tNow; the expiry tests pick boundaries around
// the deadlines explicitly (F(E,B): same E, different B).

const (
	aRoot = "idq1root"
	aBot1 = "idq1bot1"
	aBot2 = "idq1bot2"
	aBot3 = "idq1bot3"

	tBase = int64(1_700_000_000)
	tNow  = int64(1_700_010_000)
)

func tp(path, id string, height, tx, ts int64, author, payload string) *Pin {
	return &Pin{
		ID: id, Path: path, GlobalMetaID: author,
		GenesisHeight: height, TxIndex: tx, Timestamp: ts,
		Payload: []byte(payload), PayloadChannel: "content",
	}
}

func evMap(pins ...*Pin) map[string][]*Pin {
	m := map[string][]*Pin{}
	for _, p := range pins {
		m[p.Path] = append(m[p.Path], p)
	}
	return m
}

func taskPayload(ttl, quorum, window int) string {
	return fmt.Sprintf(`{"title":"tv","treeid":"tree1","specid":"","policy":{"claim_ttl_hours":%d,"verify_quorum":%d,"verify_window_hours":%d,"reward_sat":0}}`, ttl, quorum, window)
}

const treeOneNode = `{"root":"n1","nodes":[{"id":"n1","parent":null,"title":"root","kind":"aggregate","specid":null,"params":{},"deps":[]}]}`

const treeTwoNodes = `{"root":"n1","nodes":[` +
	`{"id":"n1","parent":null,"title":"root","kind":"aggregate","specid":null,"params":{},"deps":[]},` +
	`{"id":"n2","parent":"n1","title":"leaf","kind":"search","specid":null,"params":{},"deps":[]}]}`

func claimPayload(node string) string {
	return fmt.Sprintf(`{"taskid":"task1","node":%q}`, node)
}

func releasePayload(claimID string) string {
	return fmt.Sprintf(`{"taskid":"task1","node":"n1","claimid":%q}`, claimID)
}

func subPayload(claimID, node string) string {
	return fmt.Sprintf(`{"taskid":"task1","node":%q,"claimid":%q,"result":{"x":1},"hash":"h","contentType":"application/json"}`, node, claimID)
}

func votePayload(target, verdict, extra string) string {
	return fmt.Sprintf(`{"targetid":%q,"verdict":%q,"method":"r"%s}`, target, verdict, extra)
}

const withSC = `,"semantic_check":"ok"`
const withSCFR = `,"semantic_check":"ok","failreason":"why"`

func baseEvents(ttl, quorum, window int, tree string) []*Pin {
	return []*Pin{
		tp(PathTask, "task1", 100, 0, tBase, aRoot, taskPayload(ttl, quorum, window)),
		tp(PathTree, "tree1", 100, 1, tBase, aRoot, tree),
	}
}

func mustReplay(t *testing.T, events map[string][]*Pin, now int64) *ReplayResult {
	t.Helper()
	res, err := Replay(events, "task1", Boundary{Height: 0, BlockTs: now})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	return res
}

func nodeOf(t *testing.T, res *ReplayResult, id string) *NodeReplay {
	t.Helper()
	n, ok := res.Nodes[id]
	if !ok {
		t.Fatalf("node %s missing", id)
	}
	return n
}

func findVote(n *NodeReplay, pinID string) *VoteRecord {
	for i := range n.Votes {
		if n.Votes[i].VerifyPinID == pinID {
			return &n.Votes[i]
		}
	}
	return nil
}

func TestVerifiedPath(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct+2, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateVerified || n.DisplayState != StateVerified {
		t.Fatalf("state: %s/%s outcome=%s", n.MachineState, n.DisplayState, n.CycleOutcome)
	}
	if n.VerifiedAtBlockTs == nil || *n.VerifiedAtBlockTs != tBase+4_000 {
		t.Fatalf("verifiedAt: %v", n.VerifiedAtBlockTs)
	}
	if !res.RootVerified {
		t.Fatal("root should be verified")
	}
	if n.CycleSeq != 1 || n.CycleOutcome != OutcomeNone {
		t.Fatalf("cycle: %d/%s", n.CycleSeq, n.CycleOutcome)
	}
	for _, v := range n.Votes {
		if !v.Counts {
			t.Fatalf("vote %s should count (reason %s)", v.VerifyPinID, v.IgnoreReason)
		}
	}
	if n.LastWriteEventPinID == nil || *n.LastWriteEventPinID != "v2" {
		t.Fatalf("lastWrite: %v", n.LastWriteEventPinID)
	}
}

func TestSupersededClaim(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathClaim, "claim2", 102, 0, tBase+1_500, aBot2, claimPayload("n1")),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateClaimed || n.CurrentClaim == nil || n.CurrentClaim.Claimant != aBot1 {
		t.Fatalf("holder wrong: %+v", n.CurrentClaim)
	}
	var superseded *EventRecord
	for i := range n.Events {
		if n.Events[i].EventPinID == "claim2" {
			superseded = &n.Events[i]
		}
	}
	if superseded == nil || superseded.Validity != "ignored" || superseded.IgnoreReason != IgnoreSupersededClaim {
		t.Fatalf("claim2 not marked superseded: %+v", superseded)
	}
}

func TestReleaseNoRevive(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathClaim, "claim2", 102, 0, tBase+1_500, aBot2, claimPayload("n1")), // superseded
		tp(PathRelease, "release1", 103, 0, tBase+2_000, aBot1, releasePayload("claim1")),
		tp(PathClaim, "claim3", 104, 0, tBase+2_500, aBot3, claimPayload("n1")),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	// claim2 was ignored while the lock was held; releasing claim1 must not
	// revive it — claim3 takes the node as a fresh cycle.
	if n.MachineState != StateClaimed || n.CurrentClaim == nil || n.CurrentClaim.Claimant != aBot3 {
		t.Fatalf("claim3 should hold: %+v", n.CurrentClaim)
	}
	if n.CycleSeq != 2 || n.CycleOutcome != OutcomeNone {
		t.Fatalf("cycle: %d/%s", n.CycleSeq, n.CycleOutcome)
	}
}

func TestReleasePutsNodeOpen(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathRelease, "release1", 103, 0, tBase+2_000, aBot1, releasePayload("claim1")),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateOpen || n.DisplayState != "open" {
		t.Fatalf("state after release: %s/%s", n.MachineState, n.DisplayState)
	}
	if n.CycleOutcome != OutcomeReleased || n.CurrentClaim != nil {
		t.Fatalf("outcome: %s claim=%+v", n.CycleOutcome, n.CurrentClaim)
	}
}

func TestDualSubmissionSameCycle(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathSubmission, "sub2", 103, 0, tBase+2_500, aBot1, subPayload("claim1", "n1")),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.ActiveSubmission == nil || n.ActiveSubmission.SubmissionPinID != "sub1" {
		t.Fatalf("active submission: %+v", n.ActiveSubmission)
	}
	var dup *EventRecord
	for i := range n.Events {
		if n.Events[i].EventPinID == "sub2" {
			dup = &n.Events[i]
		}
	}
	if dup == nil || dup.IgnoreReason != IgnoreNotEarliestInCyc {
		t.Fatalf("sub2 not marked not_earliest: %+v", dup)
	}
}

func TestSubmitterNotHolder(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot2, subPayload("claim1", "n1")),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateClaimed || n.ActiveSubmission != nil {
		t.Fatalf("submission by non-holder attached: %+v", n.ActiveSubmission)
	}
	var rec *EventRecord
	for i := range n.Events {
		if n.Events[i].EventPinID == "sub1" {
			rec = &n.Events[i]
		}
	}
	if rec == nil || rec.IgnoreReason != IgnoreSubmitterNotHolder {
		t.Fatalf("sub1 not marked submitter_not_holder: %+v", rec)
	}
}

func TestFailRejectRequeue(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "vF", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "fail", withSCFR)),
		tp(PathClaim, "claim2", HAct+2, 0, tBase+4_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub2", HAct+3, 0, tBase+5_000, aBot1, subPayload("claim2", "n1")),
		tp(PathVerify, "v3", HAct+4, 0, tBase+6_000, aBot2, votePayload("sub2", "pass", withSC)),
		tp(PathVerify, "v4", HAct+5, 0, tBase+7_000, aBot3, votePayload("sub2", "pass", withSC)),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateVerified || n.VerifiedAtBlockTs == nil || *n.VerifiedAtBlockTs != tBase+7_000 {
		t.Fatalf("state %s verifiedAt %d", n.MachineState, n.VerifiedAtBlockTs)
	}
	if n.CycleSeq != 2 {
		t.Fatalf("cycleSeq: %d", n.CycleSeq)
	}
	vf := findVote(n, "vF")
	if vf == nil || !vf.Counts || vf.Verdict != "fail" {
		t.Fatalf("vF: %+v", vf)
	}
	// The rejected first cycle must be visible in the event stream.
	var rejectedSeen bool
	for _, e := range n.Events {
		if e.EventPinID == "vF" && e.Validity == "counted" {
			rejectedSeen = true
		}
	}
	if !rejectedSeen {
		t.Fatal("vF not recorded as counted in the node event stream")
	}
}

func TestFailGateDualTrack(t *testing.T) {
	// ≥ H_ACT: fail without failreason is gate-invalid (missing_failreason),
	// does not reject, does not block verification.
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "vF", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "fail", `,"semantic_check":"ok","evidence":"pin://x"`)),
		tp(PathVerify, "v1", HAct+2, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct+3, 0, tBase+5_000, aBot2, votePayload("sub1", "pass", withSC)),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateVerified {
		t.Fatalf("7a state: %s (outcome %s)", n.MachineState, n.CycleOutcome)
	}
	vf := findVote(n, "vF")
	if vf == nil || vf.Counts || vf.IgnoreReason != "missing_failreason" {
		t.Fatalf("7a vF: %+v", vf)
	}

	// < H_ACT with evidence: v1.1 semantics — an evidence-bearing fail rejects.
	events = evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "vF", HAct-1, 0, tBase+3_000, aBot2, votePayload("sub1", "fail", `,"semantic_check":"ok","evidence":"pin://x"`)),
	)...)
	res = mustReplay(t, events, tNow)
	n = nodeOf(t, res, "n1")
	if n.MachineState != StateOpen || n.CycleOutcome != OutcomeRejected {
		t.Fatalf("7b: %s/%s", n.MachineState, n.CycleOutcome)
	}

	// < H_ACT without evidence: neither rejects nor blocks verification.
	events = evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "vF", HAct-1, 0, tBase+3_000, aBot2, votePayload("sub1", "fail", `,"semantic_check":"ok"`)),
		tp(PathVerify, "v1", HAct, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct+1, 0, tBase+5_000, aBot2, votePayload("sub1", "pass", withSC)),
	)...)
	res = mustReplay(t, events, tNow)
	n = nodeOf(t, res, "n1")
	if n.MachineState != StateVerified || n.CycleOutcome != OutcomeNone {
		t.Fatalf("7c: %s/%s", n.MachineState, n.CycleOutcome)
	}
}

func TestSemanticCheckGate(t *testing.T) {
	// ≥ H_ACT: a pass without semantic_check does not count → 1 pass < 2.
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", "")),
		tp(PathVerify, "v2", HAct+2, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateSubmitted {
		t.Fatalf("8a state: %s", n.MachineState)
	}
	v1 := findVote(n, "v1")
	if v1 == nil || v1.Counts || v1.IgnoreReason != "missing_semantic_check" {
		t.Fatalf("8a v1: %+v", v1)
	}

	// < H_ACT: no requirement — both passes count → verified.
	events = evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct-1, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", "")),
		tp(PathVerify, "v2", HAct-1, 1, tBase+4_000, aBot3, votePayload("sub1", "pass", "")),
	)...)
	res = mustReplay(t, events, tNow)
	n = nodeOf(t, res, "n1")
	if n.MachineState != StateVerified {
		t.Fatalf("8b state: %s", n.MachineState)
	}
}

func TestTTLExpiryAtBoundary(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
	)...)
	// Same E, different B — the boundary is the only clock (D2).
	res := mustReplay(t, events, tBase+1_000+49*3600)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateOpen || n.DisplayState != "expired" || n.CycleOutcome != OutcomeExpiredClaim {
		t.Fatalf("expired: %s/%s/%s", n.MachineState, n.DisplayState, n.CycleOutcome)
	}
	res = mustReplay(t, events, tBase+1_000+47*3600)
	n = nodeOf(t, res, "n1")
	if n.MachineState != StateClaimed || n.CurrentClaim == nil {
		t.Fatalf("within ttl: %s", n.MachineState)
	}
}

func TestWindowExpiryAndLateVotes(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", withSC)),
		// window close = tBase+2_000 + 24*3600; this vote arrives after it.
		tp(PathVerify, "v2", HAct+2, 0, tBase+2_000+24*3600+100, aBot3, votePayload("sub1", "pass", withSC)),
	)...)
	res := mustReplay(t, events, tBase+2_000+24*3600+1_000)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateOpen || n.CycleOutcome != OutcomeExpiredReviewWindow {
		t.Fatalf("expired window: %s/%s", n.MachineState, n.CycleOutcome)
	}
	v2 := findVote(n, "v2")
	if v2 == nil || v2.Counts || v2.IgnoreReason != IgnoreAfterWindowClose {
		t.Fatalf("v2: %+v", v2)
	}
}

func TestVerdictInvalidSendsBack(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "vI", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "invalid", withSCFR)),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateOpen || n.CycleOutcome != OutcomeRejected {
		t.Fatalf("invalid should send back: %s/%s", n.MachineState, n.CycleOutcome)
	}
	vi := findVote(n, "vI")
	if vi == nil || vi.Counts {
		t.Fatalf("invalid vote must not occupy a slot: %+v", vi)
	}
}

func TestAggregationGatesParent(t *testing.T) {
	childOK := []*Pin{
		tp(PathClaim, "claim2", 101, 1, tBase+1_000, aBot2, claimPayload("n2")),
		tp(PathSubmission, "sub2", 102, 1, tBase+2_000, aBot2, subPayload("claim2", "n2")),
		tp(PathVerify, "w1", HAct+1, 1, tBase+3_000, aBot1, votePayload("sub2", "pass", withSC)),
		tp(PathVerify, "w2", HAct+2, 1, tBase+4_000, aBot3, votePayload("sub2", "pass", withSC)),
	}
	parent := []*Pin{
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct+3, 0, tBase+5_000, aBot2, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct+4, 0, tBase+6_000, aBot3, votePayload("sub1", "pass", withSC)),
	}
	// 12a: child verified + parent quorum met → parent verified, root done.
	events := evMap(append(baseEvents(48, 2, 24, treeTwoNodes), append(childOK, parent...)...)...)
	res := mustReplay(t, events, tNow)
	n1 := nodeOf(t, res, "n1")
	if n1.MachineState != StateVerified || !res.RootVerified {
		t.Fatalf("12a: n1=%s root=%v note=%s", n1.MachineState, res.RootVerified, n1.AggNote)
	}
	// 12b: child stuck claimed → parent agg-blocked (submitted + note).
	base := append(baseEvents(48, 2, 24, treeTwoNodes), tp(PathClaim, "claim2", 101, 1, tBase+1_000, aBot2, claimPayload("n2")))
	base = append(base, parent...)
	events = evMap(base...)
	res = mustReplay(t, events, tNow)
	n1 = nodeOf(t, res, "n1")
	if n1.MachineState != StateSubmitted || n1.DisplayState != StateSubmitted {
		t.Fatalf("12b: n1=%s/%s", n1.MachineState, n1.DisplayState)
	}
	if n1.AggNote == "" || res.RootVerified {
		t.Fatalf("12b note=%q root=%v", n1.AggNote, res.RootVerified)
	}
}

func TestMempoolAndDuplicateExcluded(t *testing.T) {
	mempoolClaim := tp(PathClaim, "mclaim", -1, 0, tBase+1_000, aBot1, claimPayload("n1"))
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
	)...)
	events[PathClaim] = append(events[PathClaim], mempoolClaim, mempoolClaim) // mempool + exact duplicate
	res := mustReplay(t, events, tNow)
	if res.ExcludedMempool != 1 {
		t.Fatalf("mempool excluded: %d", res.ExcludedMempool)
	}
	n := nodeOf(t, res, "n1")
	if n.CycleSeq != 1 {
		t.Fatalf("duplicate pin must not open a second cycle: %d", n.CycleSeq)
	}
	if n.MachineState != StateClaimed {
		t.Fatalf("state: %s", n.MachineState)
	}
}

func TestToBlockBoundaryFilter(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct+2, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
	)...)
	res, err := Replay(events, "task1", Boundary{Height: HAct + 1, BlockTs: tNow})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if got := res.EventCountByPath[PathVerify]; got != 1 {
		t.Fatalf("verify events beyond to_block must be filtered: %d", got)
	}
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateSubmitted {
		t.Fatalf("state at boundary: %s", n.MachineState)
	}
}

func TestPostVerifyVotesDoNotDrive(t *testing.T) {
	// Freeze §3: verified is terminal. Once quorum completes, the cycle's
	// submission stops being 当期生效 — later votes (including fails) are
	// recorded as target_not_active and never drive the state machine.
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct+2, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "vF", HAct+3, 0, tBase+5_000, aBot2, votePayload("sub1", "fail", withSCFR)),
		tp(PathVerify, "vP", HAct+4, 0, tBase+6_000, aBot3, votePayload("sub1", "pass", withSC)),
	)...)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateVerified || n.VerifiedAtBlockTs == nil || *n.VerifiedAtBlockTs != tBase+4_000 {
		t.Fatalf("post-verify votes must not drive: state=%s verifiedAt=%d", n.MachineState, n.VerifiedAtBlockTs)
	}
	for _, pid := range []string{"vF", "vP"} {
		v := findVote(n, pid)
		if v == nil || v.Counts || v.IgnoreReason != IgnoreTargetNotActive {
			t.Fatalf("%s must be target_not_active: %+v", pid, v)
		}
	}
}

func TestReplayDeterministicUnderInputOrder(t *testing.T) {
	pins := append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct+1, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct+2, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "vF", HAct+1, 1, tBase+3_200, aBot1, votePayload("sub1", "fail", withSCFR)),
	)
	res1 := mustReplay(t, evMap(pins...), tNow)
	// Reverse the input order of every path slice; the ≺ order must absorb it.
	rev := evMap(pins...)
	for path := range rev {
		for i, j := 0, len(rev[path])-1; i < j; i, j = i+1, j-1 {
			rev[path][i], rev[path][j] = rev[path][j], rev[path][i]
		}
	}
	res2 := mustReplay(t, rev, tNow)
	b1, err := json.Marshal(res1)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := json.Marshal(res2)
	if err != nil {
		t.Fatal(err)
	}
	if string(b1) != string(b2) {
		t.Fatal("replay output must be byte-identical regardless of input order (D3)")
	}
}
