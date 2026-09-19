package metatask

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestD8VectorsAgainstSkillFixtures runs the freeze engine against the
// metatask-replay skill v1.2.2 discriminant vectors (testdata/d8vectors,
// copied verbatim; the skill runner's own log is RUN_LOG.txt, 11/11 PASS).
//
// The vectors are guard-view expectations (allow/reject + reason). The
// mapping from the freeze engine's machine states follows the skill's
// guard_state collapse: open→allow; submitted→claimed; claimed→claimed;
// verified→verified.
func TestD8VectorsAgainstSkillFixtures(t *testing.T) {
	dir := "testdata/d8vectors"
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	ran := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		ran++
		t.Run(name, func(t *testing.T) {
			fx := filepath.Join(dir, name)
			events, root, err := loadFixture(fx)
			if err != nil {
				t.Fatalf("load fixture: %v", err)
			}
			raw, err := os.ReadFile(filepath.Join(fx, "expect.json"))
			if err != nil {
				t.Fatalf("read expect: %v", err)
			}
			var exp struct {
				Now                    int64   `json:"now"`
				ExpectVerdict          string  `json:"expect_verdict"`
				ExpectReason           *string `json:"expect_reason"`
				ExpectCycleSeq         *int    `json:"expect_cycle_seq"`
				ExpectActiveSubmission *string `json:"expect_active_submission"`
			}
			if err := json.Unmarshal(raw, &exp); err != nil {
				t.Fatalf("decode expect: %v", err)
			}
			res, err := Replay(events, root, Boundary{Height: 0, BlockTs: exp.Now})
			if err != nil {
				t.Fatalf("replay: %v", err)
			}
			node, ok := res.Nodes["t1"]
			if !ok {
				t.Fatalf("node t1 missing from replay result (nodes: %v)", res.NodeOrder)
			}
			gotVerdict, gotReason := guardView(node)
			if gotVerdict != exp.ExpectVerdict {
				t.Fatalf("verdict: got %q want %q (machine=%s outcome=%s display=%s)",
					gotVerdict, exp.ExpectVerdict, node.MachineState, node.CycleOutcome, node.DisplayState)
			}
			wantReason := ""
			if exp.ExpectReason != nil {
				wantReason = *exp.ExpectReason
			}
			if gotReason != wantReason {
				t.Fatalf("reason: got %q want %q", gotReason, wantReason)
			}
			// Node-level discriminants (A4-5): the guard view alone cannot
			// tell a correct cycle-2 verdict from the cycle-boundary-blind
			// cycle-1 verdict — both collapse to verified. When the fixture
			// pins the cycle anchor, assert it.
			if exp.ExpectCycleSeq != nil && node.CycleSeq != *exp.ExpectCycleSeq {
				t.Fatalf("cycleSeq: got %d want %d (cycle-boundary discriminant; activeSub=%s)",
					node.CycleSeq, *exp.ExpectCycleSeq, activeSubID(node))
			}
			if exp.ExpectActiveSubmission != nil && activeSubID(node) != *exp.ExpectActiveSubmission {
				t.Fatalf("activeSubmission: got %q want %q",
					activeSubID(node), *exp.ExpectActiveSubmission)
			}
		})
	}
	if ran != 13 {
		t.Fatalf("expected 13 vector directories, ran %d", ran)
	}
}

// guardView maps a node's machine state onto the skill guard verdict/reason.
func guardView(n *NodeReplay) (verdict, reason string) {
	if n.MachineState == StateOpen {
		return "allow", ""
	}
	state := n.MachineState
	if state == StateSubmitted {
		state = StateClaimed // guard_state collapses submitted to claimed
	}
	return "reject", fmt.Sprintf("claim-rejected:%s:%s", n.ID, state)
}

func activeSubID(n *NodeReplay) string {
	if n.ActiveSubmission == nil {
		return ""
	}
	return n.ActiveSubmission.SubmissionPinID
}

// loadFixture reads one skill fixture directory: <pool>.json files holding
// arrays of pin objects (payloads inline in contentSummary) plus expect.json.
func loadFixture(dir string) (map[string][]*Pin, string, error) {
	events := map[string][]*Pin{}
	root := ""
	for _, path := range EventPaths {
		raw, err := os.ReadFile(filepath.Join(dir, PoolName(path)+".json"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, "", err
		}
		var items []map[string]any
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, "", fmt.Errorf("decode %s: %w", path, err)
		}
		for _, item := range items {
			p := pinFromMap(item)
			if p.ID == "" {
				continue
			}
			events[path] = append(events[path], p)
			if path == PathTask && root == "" {
				root = p.ID
			}
		}
	}
	if root == "" {
		return nil, "", fmt.Errorf("no task pin found in %s", dir)
	}
	return events, root, nil
}
