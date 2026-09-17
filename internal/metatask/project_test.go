package metatask

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCanonicalJSONSortedKeysNoHTMLEscape(t *testing.T) {
	v := map[string]any{
		"b": 1,
		"a": "<a>&\"quote\"中文",
		"c": map[string]any{"y": json.Number("2"), "x": "v"},
	}
	b, err := CanonicalJSON(v)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if got != `{"a":"<a>&\"quote\"中文","b":1,"c":{"x":"v","y":2}}` {
		t.Fatalf("canonical output mismatch: %s", got)
	}
	if strings.Contains(got, `\u003c`) {
		t.Fatal("HTML escaping must stay off (ensure_ascii=False parity)")
	}
}

func TestBuildReplayMetaCursorAndCoverage(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathClaim, "claim1", HAct+9, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", HAct+5, 3, tBase+2_000, aBot1, subPayload("claim1", "n1")),
	)...)
	scope := ScopeEvents(events, Boundary{Height: HAct + 9, BlockTs: tBase + 9_000})
	enum := Enumeration{Paths: map[string]PageEnumeration{}}
	for _, path := range EventPaths {
		enum.Paths[path] = PageEnumeration{Complete: true}
	}
	enum.Paths[PathVerify] = PageEnumeration{Complete: false}
	meta := BuildReplayMeta(scope, enum, Boundary{Height: HAct + 9, BlockTs: tBase + 9_000})
	if meta.Cursor.BlockHeight != HAct+9 || meta.Cursor.TxIndex != 0 {
		t.Fatalf("cursor should sit on the ≺-max event (claim1): %+v", meta.Cursor)
	}
	if meta.Cursor.OutputIndex != -1 {
		t.Fatalf("outputIndex sentinel: %d", meta.Cursor.OutputIndex)
	}
	if meta.CanonID != CanonID || CanonID != "canonJ-1.1.0" {
		t.Fatalf("canonId: %q", meta.CanonID)
	}
	if meta.Coverage.PerPath[PathVerify] != "partial" || meta.Coverage.PerPath[PathClaim] != "complete" {
		t.Fatalf("perPath coverage: %+v", meta.Coverage.PerPath)
	}
	if meta.SemanticGate.HAct != HAct || len(meta.SemanticGate.RulingPins) != 3 {
		t.Fatalf("semanticGate: %+v", meta.SemanticGate)
	}
	if meta.Freshness.MaxLagBlocks != 5 {
		t.Fatalf("freshness: %+v", meta.Freshness)
	}
}

func TestBuildTaskIndexRowOrderAndParticipants(t *testing.T) {
	scope := &RunScope{Admitted: map[string][]*replayEvent{}}
	// Two roots: rootOld verified long ago, rootNew active with a fresh vote.
	old := &ReplayResult{
		RootPinID: "rootOld", TaskTitle: "old", Publisher: aRoot, RootVerified: true,
		Nodes: map[string]*NodeReplay{
			"n1": {NodeID: "n1", InTree: true, MachineState: StateVerified, DisplayState: StateVerified,
				ActiveSubmission: &ActiveSubmission{SubmissionPinID: "s", Submitter: aBot1}, VerifiedAtBlockTs: 100},
		},
		NodeOrder: []string{"n1"},
	}
	newer := &ReplayResult{
		RootPinID: "rootNew", TaskTitle: "new", Publisher: aRoot,
		Nodes: map[string]*NodeReplay{
			"n1": {NodeID: "n1", InTree: true, MachineState: StateSubmitted, DisplayState: StateSubmitted,
				ActiveSubmission: &ActiveSubmission{SubmissionPinID: "s2", Submitter: aBot2},
				Votes:            []VoteRecord{{VerifyPinID: "vv", Voter: aBot3, Verdict: "pass", Counts: true, BlockTs: 900}}},
		},
		NodeOrder: []string{"n1"},
	}
	meta := ReplayMeta{EventCountByPath: map[string]int{}, Coverage: ReplayCoverage{PerPath: map[string]string{}}}
	idx := BuildTaskIndex(map[string]*ReplayResult{"rootOld": old, "rootNew": newer}, scope, meta)
	if len(idx.Rows) != 2 {
		t.Fatalf("rows: %d", len(idx.Rows))
	}
	if idx.Rows[0].RootPinID != "rootNew" {
		t.Fatalf("rows must sort by lastActivity desc: %s first", idx.Rows[0].RootPinID)
	}
	if idx.Stats.TaskDone != 1 || idx.Stats.TaskActive != 1 || idx.Stats.TaskTotal != 2 {
		t.Fatalf("stats: %+v", idx.Stats)
	}
	if idx.Stats.NodesByState["verified"] != 1 || idx.Stats.NodesByState["submitted"] != 1 {
		t.Fatalf("nodesByState: %+v", idx.Stats.NodesByState)
	}
	var row *TaskRow
	for i := range idx.Rows {
		if idx.Rows[i].RootPinID == "rootNew" {
			row = &idx.Rows[i]
		}
	}
	if row == nil || row.ParticipantsCount != 1 || row.Participants[0].MetaID != aBot3 || row.Participants[0].Reviews != 1 {
		t.Fatalf("participants: %+v", row.Participants)
	}
	if row.LastActivity.EventPinID != "vv" || row.LastActivity.BlockTs != 900 {
		t.Fatalf("lastActivity: %+v", row.LastActivity)
	}
	// Leaderboard: reviewer has one counted vote; verified submitter contributes.
	foundReviewer := false
	for _, r := range idx.Leaderboard {
		if r.MetaID == aBot3 {
			foundReviewer = true
			if r.ReviewScore != 1 || r.ReviewAccuracy.Den != 1 || r.ReviewAccuracy.Num != 0 {
				t.Fatalf("reviewer row: %+v", r)
			}
		}
		if r.MetaID == aBot1 && r.Contribution != 1 {
			t.Fatalf("contributor row: %+v", r)
		}
	}
	if !foundReviewer {
		t.Fatal("reviewer missing from leaderboard")
	}
}

func TestBuildPanoramaProgressAndSpec(t *testing.T) {
	events := evMap(append(baseEvents(48, 2, 24, treeOneNode),
		tp(PathSpec, "spec1", 100, 2, tBase, aRoot, `{"name":"s","lang":"python","entry":"v.py","script":"pin://scriptpin","input":"i","output":"o"}`),
		tp(PathClaim, "claim1", 101, 0, tBase+1_000, aBot1, claimPayload("n1")),
		tp(PathSubmission, "sub1", 102, 0, tBase+2_000, aBot1, subPayload("claim1", "n1")),
	)...)
	// Give the task payload a specid so the spec resolves.
	events[PathTask][0].Payload = []byte(`{"title":"tv","brief":"b","treeid":"tree1","specid":"spec1","policy":{"claim_ttl_hours":48,"verify_quorum":2,"verify_window_hours":24},"tags":["t"]}`)
	scope := ScopeEvents(events, Boundary{Height: 0, BlockTs: tNow})
	res, err := Replay(events, "task1", Boundary{Height: 0, BlockTs: tNow})
	if err != nil {
		t.Fatal(err)
	}
	meta := BuildReplayMeta(scope, Enumeration{Paths: map[string]PageEnumeration{}}, Boundary{Height: 0, BlockTs: tNow})
	p := BuildPanorama(res, scope, meta)
	if p.Title != "tv" || p.Brief != "b" || len(p.Tags) != 1 || p.Tags[0] != "t" {
		t.Fatalf("panorama header: %+v", p)
	}
	if p.Spec == nil || p.Spec.ScriptOrScriptPin != "pin://scriptpin" || p.Spec.Lang != "python" {
		t.Fatalf("spec: %+v", p.Spec)
	}
	if p.Progress.Total != 1 || p.Progress.Open != 0 {
		t.Fatalf("progress: %+v", p.Progress)
	}
	if p.DiscoveredBy == nil || p.DiscoveredBy[0] != "index" {
		t.Fatalf("discoveredBy: %+v", p.DiscoveredBy)
	}
	ne, err := BuildNodeEvents(res, "n1")
	if err != nil {
		t.Fatal(err)
	}
	if ne.Node.NodeID != "n1" || len(ne.Events) < 2 {
		t.Fatalf("node events: %+v", ne)
	}
}
