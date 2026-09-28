package metatask

import (
	"sort"
	"testing"
)

// Wave-1 parity regression tests for the IDBots pre-launch audit change
// request (2026-09-29). They pin the three engine-parity fixes the TS
// reference engine already landed, so the manifests stay byte-identical
// across the three engines:
//
//	B1 ghost-claim weight poisoning — a claim/release/submission pin whose
//	   node id is not in the task's EFFECTIVE (post-amend) tree is ignored
//	   with reason unknown_node: no node state, no openNodes entry, no
//	   progress.total inflation, no settlement weight-table damage.
//	B2 reviewer-accuracy identity filter — reviewCorrect/reviewTerminal (the
//	   Laplace accuracy a(r) feeding the reviewer pool) exclude votes where
//	   voter == submission author or voter == task root author, exactly as
//	   the settlement reviewer set R(n) already does.
//	B3 same-side roster — the roster pool (/protocols/metatask-roster) is
//	   swept and its bodies passed into replay; votes at height >= H_ACT2
//	   from a voter sharing a roster group with the submitter or the root
//	   author are ignored (reason same_side_roster); pre-H_ACT2 votes are
//	   untouched.

func ce(pinID, path, author string, h, tx int64, body map[string]any) *ChainEvent {
	return &ChainEvent{PinID: pinID, Path: path, Author: author, Height: h, TxIndex: tx, TimestampMs: tBase*1000 + h, Body: body}
}

func shareOf(t *testing.T, res *V121Result, metaID string) V121Share {
	t.Helper()
	if res.Settlement == nil {
		t.Fatalf("expected a settlement manifest, got none")
	}
	for _, s := range res.Settlement.Shares {
		if s.MetaID == metaID {
			return s
		}
	}
	t.Fatalf("no share row for %s (shares: %+v)", metaID, res.Settlement.Shares)
	return V121Share{}
}

func participantOf(t *testing.T, res *V121Result, metaID string) *V121Participant {
	t.Helper()
	for i := range res.Participants {
		if res.Participants[i].MetaID == metaID {
			return &res.Participants[i]
		}
	}
	return nil
}

func hasIgnored(res *V121Result, pinID, reason string) bool {
	for _, ig := range res.IgnoredEvents {
		if ig.PinID == pinID && ig.Reason == reason {
			return true
		}
	}
	return false
}

// B1: one stray claim on an invented node id must not de-validate the tree
// weights (the unfixed engine silently fell back to the legacy uniform split:
// a weighted 3000/7000 task's 8000/1000+1000 allocation became 5332/666/666
// with no trace in the manifest).
func TestV121GhostNodeClaimCannotPoisonWeights(t *testing.T) {
	const (
		rootA = "idq1root"
		subS  = "idq1sub"
		revR1 = "idq1rev1"
		ghost = "idq1ghost"
	)
	events := []*ChainEvent{
		ce("tree1", "tree", rootA, 191000, 0, map[string]any{"root": "r", "nodes": []any{
			map[string]any{"id": "r", "parent": nil, "title": "root", "kind": "aggregate", "weight": 3000},
			map[string]any{"id": "c", "parent": "r", "title": "leaf", "kind": "proof", "weight": 7000},
		}}),
		ce("task1", "task", rootA, 191001, 0, map[string]any{"title": "t", "treeid": "tree1", "policy": map[string]any{"verify_quorum": 1}}),
		ce("cl-c", "claim", subS, 191002, 0, map[string]any{"taskid": "task1", "node": "c"}),
		ce("sub-c", "submission", subS, 191003, 0, map[string]any{"taskid": "task1", "node": "c", "claimid": "cl-c"}),
		ce("v-c", "verify", revR1, 191004, 0, map[string]any{"targetid": "sub-c", "verdict": "pass", "semantic_check": "ok"}),
		ce("cl-r", "claim", subS, 191005, 0, map[string]any{"taskid": "task1", "node": "r"}),
		ce("sub-r", "submission", subS, 191006, 0, map[string]any{"taskid": "task1", "node": "r", "claimid": "cl-r"}),
		ce("v-r", "verify", revR1, 191007, 0, map[string]any{"targetid": "sub-r", "verdict": "pass", "semantic_check": "ok"}),
		// The poisoning pin: a claim on a node the effective tree never declares.
		ce("cl-g", "claim", ghost, 191008, 0, map[string]any{"taskid": "task1", "node": "ghost"}),
	}
	res, err := ReplayV121(events, V121Options{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	// The ghost pin is recorded but creates no state.
	if !hasIgnored(res, "cl-g", "unknown_node") {
		t.Fatalf("ghost claim must be ignored as unknown_node: %+v", res.IgnoredEvents)
	}
	if _, exists := res.NodeStates["ghost"]; exists {
		t.Fatalf("ghost node must not create node state")
	}
	ids := make([]string, 0, len(res.NodeStates))
	for id := range res.NodeStates {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) != 2 || ids[0] != "c" || ids[1] != "r" {
		t.Fatalf("nodes must be exactly the effective tree: %v", ids)
	}
	if res.Progress.Total != 2 {
		t.Fatalf("progress.total must not count the ghost node: %d", res.Progress.Total)
	}
	if p := participantOf(t, res, ghost); p != nil {
		t.Fatalf("ghost claimant must not enter the participant stats: %+v", p)
	}

	// The settlement keeps the tree weights (7000/3000), not the uniform
	// fallback (3333/3333/3333): S earns 5600+2400 submitted, R1 the pools.
	if !res.TaskComplete {
		t.Fatalf("task must complete (root verified)")
	}
	s := shareOf(t, res, subS)
	if s.SubmittedBP != 8000 || s.ReviewedBP != 0 || s.ShareBP != 8000 {
		t.Fatalf("submitter share must follow tree weights (5600+2400): %+v", s)
	}
	r := shareOf(t, res, revR1)
	if r.ReviewedBP != 2000 || r.ShareBP != 2000 {
		t.Fatalf("reviewer share must be the two full pools (1400+600): %+v", r)
	}
	wtBytes, err := canonJ([]any{
		map[string]any{"id": "c", "weight": int64(7000)},
		map[string]any{"id": "r", "weight": int64(3000)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Settlement.WeightsTableHash != sha256Hex(wtBytes) {
		t.Fatalf("weights table must hold the tree weights, not the uniform fallback")
	}
	if res.Settlement.BoundaryBlock != 191008 {
		t.Fatalf("the ghost pin stays a fact of the event set (boundary 191008): %d", res.Settlement.BoundaryBlock)
	}
}

// B1 (renderer level): the ghost node must not surface in the frozen §5
// projection either (openNodes / node streams).
func TestV121GhostNodeAbsentFromProjection(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathClaim, "claimG", 102, 0, tBase+1_500, aBot2, claimPayload("ghost")),
	)...)
	res := mustReplay(t, events, tNow)
	if _, exists := res.Nodes["ghost"]; exists {
		t.Fatal("ghost node must not appear in the projection")
	}
	if len(res.NodeOrder) != 1 || res.NodeOrder[0] != "n1" {
		t.Fatalf("nodeOrder: %v", res.NodeOrder)
	}
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateClaimed || n.CurrentClaim == nil || n.CurrentClaim.Claimant != aBot1 {
		t.Fatalf("real node unaffected: %+v", n.CurrentClaim)
	}
	found := false
	for _, ig := range res.IgnoredEvents {
		if ig.PinID == "claimG" && ig.Reason == "unknown_node" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ignoredEvents must record the ghost claim: %+v", res.IgnoredEvents)
	}
}

// B2: a vote where voter == submission author or voter == task root author
// must not feed the Laplace reviewer accuracy — exactly as the settlement
// reviewer set R(n) already excludes them. Unfiltered, two pin fees raised a
// reviewer's accuracy from 5000 to 7500 (+50% reviewer-pool share).
func TestV121ReviewerAccuracyIdentityFilter(t *testing.T) {
	const (
		rootA = "idq1root"
		subS  = "idq1sub"
		botX  = "idq1botX"
		botY  = "idq1botY"
		botZ  = "idq1botZ"
	)
	events := []*ChainEvent{
		ce("tree1", "tree", rootA, 191000, 0, map[string]any{"root": "r", "nodes": []any{
			map[string]any{"id": "r", "parent": nil, "kind": "aggregate", "weight": 2000},
			map[string]any{"id": "a", "parent": "r", "kind": "proof", "weight": 5000},
			map[string]any{"id": "b", "parent": "r", "kind": "proof", "weight": 3000},
		}}),
		ce("task1", "task", rootA, 191001, 0, map[string]any{"title": "t", "treeid": "tree1", "policy": map[string]any{"verify_quorum": 1}}),
		// node a: submitted by S, reviewed by X and Z.
		ce("cl-a", "claim", subS, 191102, 0, map[string]any{"taskid": "task1", "node": "a"}),
		ce("sub-a", "submission", subS, 191103, 0, map[string]any{"taskid": "task1", "node": "a", "claimid": "cl-a"}),
		ce("v-a1", "verify", botX, 191104, 0, map[string]any{"targetid": "sub-a", "verdict": "pass", "semantic_check": "ok"}),
		ce("v-a2", "verify", botZ, 191105, 0, map[string]any{"targetid": "sub-a", "verdict": "pass", "semantic_check": "ok"}),
		// node b: submitted by X; Y's pass verifies it; X's self-vote must not
		// count toward quorum (already) NOR feed X's accuracy (this fix).
		ce("cl-b", "claim", botX, 191106, 0, map[string]any{"taskid": "task1", "node": "b"}),
		ce("sub-b", "submission", botX, 191107, 0, map[string]any{"taskid": "task1", "node": "b", "claimid": "cl-b"}),
		ce("v-b1", "verify", botY, 191108, 0, map[string]any{"targetid": "sub-b", "verdict": "pass", "semantic_check": "ok"}),
		ce("v-b2", "verify", botX, 191109, 0, map[string]any{"targetid": "sub-b", "verdict": "pass", "semantic_check": "ok"}),
		// root r: submitted by S, reviewed by X.
		ce("cl-r", "claim", subS, 191110, 0, map[string]any{"taskid": "task1", "node": "r"}),
		ce("sub-r", "submission", subS, 191111, 0, map[string]any{"taskid": "task1", "node": "r", "claimid": "cl-r"}),
		ce("v-r1", "verify", botX, 191112, 0, map[string]any{"targetid": "sub-r", "verdict": "pass", "semantic_check": "ok"}),
	}
	res, err := ReplayV121(events, V121Options{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !res.TaskComplete {
		t.Fatalf("task must complete")
	}

	// X's terminal record is the two legit reviews (a, r) — the self-vote on
	// its own submission b is excluded: a(X) = (2+1)/(2+2) = 7500, not 10000.
	x := participantOf(t, res, botX)
	if x == nil {
		t.Fatalf("X missing from participants")
	}
	if x.ReviewTerminal != 2 || x.ReviewCorrect != 2 {
		t.Fatalf("X terminal/correct must exclude the self-vote: %d/%d", x.ReviewTerminal, x.ReviewCorrect)
	}

	// Pool of node a (1000 BP) splits X:Z by accuracy 7500:6666 → 529/470;
	// unfiltered (10000:6666) it would be 600/399.
	sa := shareOf(t, res, subS)
	if sa.ShareBP != 5600 || sa.SubmittedBP != 5600 {
		t.Fatalf("S: %+v", sa)
	}
	sx := shareOf(t, res, botX)
	if sx.SubmittedBP != 2400 || sx.ReviewedBP != 929 || sx.ShareBP != 3329 {
		t.Fatalf("X: %+v (unfiltered accuracy would pay 3400)", sx)
	}
	sz := shareOf(t, res, botZ)
	if sz.ShareBP != 470 || sz.ReviewedBP != 470 {
		t.Fatalf("Z: %+v", sz)
	}
	sy := shareOf(t, res, botY)
	if sy.ShareBP != 600 || sy.ReviewedBP != 600 {
		t.Fatalf("Y: %+v", sy)
	}
	if res.Settlement.ShareSumBP != 9999 {
		t.Fatalf("share sum: %d", res.Settlement.ShareSumBP)
	}
}

// B3 (engine level): votes at height >= H_ACT2 from a voter sharing a roster
// group with the submitter (or the root author) are ignored as
// same_side_roster; without the roster body there is no filtering; pre-H_ACT2
// votes stay untouched.
func TestV121SameSideRosterGate(t *testing.T) {
	const (
		rootA = "idq1root"
		subS  = "idq1sub"
		revV1 = "idq1rev1"
		revV2 = "idq1rev2"
	)
	rosterBody := map[string]any{"groups": []any{[]any{subS, revV2}}}
	task := map[string]any{"title": "t", "treeid": "tree1", "policy": map[string]any{
		"verify_quorum": 2, "split": map[string]any{"rosterid": "roster1"}}}
	base := []*ChainEvent{
		ce("tree1", "tree", rootA, HAct2, 0, map[string]any{"root": "n1", "nodes": []any{
			map[string]any{"id": "n1", "parent": nil, "kind": "proof", "weight": 10000},
		}}),
		ce("task1", "task", rootA, HAct2, 1, task),
		ce("cl1", "claim", subS, HAct2+1, 0, map[string]any{"taskid": "task1", "node": "n1"}),
		ce("sub1", "submission", subS, HAct2+2, 0, map[string]any{"taskid": "task1", "node": "n1", "claimid": "cl1"}),
		ce("v1", "verify", revV1, HAct2+3, 0, map[string]any{"targetid": "sub1", "verdict": "pass", "semantic_check": "ok"}),
	}
	sameSideVote := ce("v2", "verify", revV2, HAct2+4, 0, map[string]any{"targetid": "sub1", "verdict": "pass", "semantic_check": "ok"})

	// With the roster body: v2 is same-side with the submitter → ignored; the
	// quorum (2) is unmet and the node stays submitted.
	res, err := ReplayV121(append(append([]*ChainEvent{}, base...), sameSideVote), V121Options{RosterPins: map[string]map[string]any{"roster1": rosterBody}})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !hasIgnored(res, "v2", "same_side_roster") {
		t.Fatalf("v2 must be ignored same_side_roster: %+v", res.IgnoredEvents)
	}
	if d := res.VerifyDisposition["v2"]; d == nil || d.Counts || d.Reason != "same_side_roster" {
		t.Fatalf("disposition: %+v", d)
	}
	if st := res.NodeStates["n1"]; st == nil || st.Status != "claimed" {
		t.Fatalf("quorum unmet after the same-side vote is filtered: %+v", st)
	}

	// Without the roster body the same event set verifies (no filtering).
	res2, err := ReplayV121(append(append([]*ChainEvent{}, base...), sameSideVote), V121Options{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if st := res2.NodeStates["n1"]; st == nil || st.Status != "verified" {
		t.Fatalf("without roster the two passes verify the node: %+v", st)
	}

	// Pre-H_ACT2 same-side votes stay untouched (pilot projections unchanged):
	// the whole activity anchors below the gate, so the roster filter is
	// inert and V2's ballot verifies the node together with V1's.
	preGate := []*ChainEvent{
		ce("tree1", "tree", rootA, 191400, 0, map[string]any{"root": "n1", "nodes": []any{
			map[string]any{"id": "n1", "parent": nil, "kind": "proof", "weight": 10000},
		}}),
		ce("task1", "task", rootA, 191400, 1, task),
		ce("cl1", "claim", subS, 191401, 0, map[string]any{"taskid": "task1", "node": "n1"}),
		ce("sub1", "submission", subS, 191402, 0, map[string]any{"taskid": "task1", "node": "n1", "claimid": "cl1"}),
		ce("v0", "verify", revV2, 191403, 0, map[string]any{"targetid": "sub1", "verdict": "pass", "semantic_check": "ok"}),
		ce("v1", "verify", revV1, 191404, 0, map[string]any{"targetid": "sub1", "verdict": "pass", "semantic_check": "ok"}),
	}
	res3, err := ReplayV121(preGate, V121Options{RosterPins: map[string]map[string]any{"roster1": rosterBody}})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if st := res3.NodeStates["n1"]; st == nil || st.Status != "verified" {
		t.Fatalf("pre-H_ACT2 same-side vote must still count: %+v", st)
	}
	if d := res3.VerifyDisposition["v0"]; d == nil || !d.Counts {
		t.Fatalf("pre-gate ballot counts: %+v", d)
	}
}

// B3 (renderer level): the roster body reaches the replay through the
// collected tenth pool (events[PathRoster]) — no signature change, mempool
// and out-of-boundary roster pins excluded.
func TestRosterPoolReachesReplay(t *testing.T) {
	taskWithRoster := `{"title":"tv","treeid":"tree1","specid":"","policy":{"claim_ttl_hours":48,"verify_quorum":2,"verify_window_hours":24,"split":{"rosterid":"roster1"}}}`
	rosterPayload := `{"groups":[["idq1bot1","idq1bot3"]]}`
	events := evMap(
		tp(PathTask, "task1", 100, 0, tBase, aRoot, taskWithRoster),
		tp(PathTree, "tree1", 100, 1, tBase, aRoot, treeOneNode),
		tp(PathRoster, "roster1", 101, 0, tBase+500, aRoot, rosterPayload),
		tp(PathClaim, "claim1", HAct2+1, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", HAct2+2, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct2+3, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct2+4, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
	)
	res := mustReplay(t, events, tNow)
	n := nodeOf(t, res, "n1")
	if n.MachineState != StateSubmitted {
		t.Fatalf("v2 is same-side with the submitter (roster) and must be filtered: %s", n.MachineState)
	}
	v2 := findVote(n, "v2")
	if v2 == nil || v2.Counts || v2.IgnoreReason == nil || *v2.IgnoreReason != IgnoreSameSideRoster {
		t.Fatalf("v2 disposition: %+v", v2)
	}
	if !res.RosterResolved {
		t.Fatalf("roster1 must resolve from the swept pool")
	}
	// The roster pin is reference data, not an event: it must not enter the
	// event accounting nor move the evaluation boundary.
	if res.EventCountByPath[PathRoster] != 0 {
		t.Fatalf("roster pins are not events of E: %v", res.EventCountByPath)
	}

	// A mempool roster pin is not confirmation — it must not arm the filter.
	events2 := evMap(
		tp(PathTask, "task1", 100, 0, tBase, aRoot, taskWithRoster),
		tp(PathTree, "tree1", 100, 1, tBase, aRoot, treeOneNode),
		tp(PathRoster, "roster1", -1, 0, tBase+500, aRoot, rosterPayload),
		tp(PathClaim, "claim1", HAct2+1, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", HAct2+2, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
		tp(PathVerify, "v1", HAct2+3, 0, tBase+3_000, aBot2, votePayload("sub1", "pass", withSC)),
		tp(PathVerify, "v2", HAct2+4, 0, tBase+4_000, aBot3, votePayload("sub1", "pass", withSC)),
	)
	res2 := mustReplay(t, events2, tNow)
	n2 := nodeOf(t, res2, "n1")
	if n2.MachineState != StateVerified || res2.RosterResolved {
		t.Fatalf("mempool roster must not filter votes: %s resolved=%v", n2.MachineState, res2.RosterResolved)
	}
}
