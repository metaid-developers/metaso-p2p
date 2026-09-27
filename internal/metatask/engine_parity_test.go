package metatask

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// The on-chain engine (metatask-replay v1.2.0) ships nine discriminating
// fixtures. They are vendored verbatim under testdata/engine-fixtures and run
// here against this package's independent guard implementation, so the §4.7
// entry guard is compared on the engine's own calibration set rather than on
// vectors authored by this side.
const (
	fixtureDir       = "testdata/engine-fixtures"
	fixtureScriptSHA = "828a9b95b1f2c9648ca250122cd78063d622202ac53b776508c737a639cf3f25"
)

func loadFixtureDir(t *testing.T, dir string) map[string][]*Pin {
	t.Helper()
	events := map[string][]*Pin{}
	for _, path := range EventPaths {
		pool := PoolName(path)
		raw, err := os.ReadFile(filepath.Join(dir, pool+".json"))
		if err != nil {
			if os.IsNotExist(err) {
				continue // pre-v1.2 fixture sets carry no amend/challenge pools
			}
			t.Fatalf("read fixture %s: %v", pool, err)
		}
		var rows []map[string]any
		if err := json.Unmarshal(raw, &rows); err != nil {
			t.Fatalf("parse fixture %s: %v", pool, err)
		}
		for _, row := range rows {
			p := pinFromMap(row)
			if p.Path == "" {
				p.Path = path
			}
			if p.Path != path {
				t.Fatalf("fixture %s carries path %s", pool, p.Path)
			}
			events[path] = append(events[path], p)
		}
	}
	return events
}

func fixtureRoot(t *testing.T, events map[string][]*Pin) string {
	t.Helper()
	if len(events[PathTask]) != 1 {
		t.Fatalf("fixture must carry exactly one task pin, got %d", len(events[PathTask]))
	}
	return events[PathTask][0].ID
}

// TestEngineFixtureGuardParity runs all nine engine fixtures through the guard
// view and asserts the verdict/reason pair matches expect.json exactly.
func TestEngineFixtureGuardParity(t *testing.T) {
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("read fixture dir: %v", err)
	}
	ran := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(fixtureDir, e.Name())
		raw, err := os.ReadFile(filepath.Join(dir, "expect.json"))
		if err != nil {
			t.Fatalf("read %s/expect.json: %v", e.Name(), err)
		}
		var expect struct {
			Now           int64   `json:"now"`
			ExpectVerdict string  `json:"expect_verdict"`
			ExpectReason  *string `json:"expect_reason"`
		}
		if err := json.Unmarshal(raw, &expect); err != nil {
			t.Fatalf("parse %s/expect.json: %v", e.Name(), err)
		}
		events := loadFixtureDir(t, dir)
		root := fixtureRoot(t, events)
		s, err := newSettlement(events, root)
		if err != nil {
			t.Fatalf("%s: newSettlement: %v", e.Name(), err)
		}
		state, reason := s.guardState("t1", expect.Now, s.policyTTL(), s.policyWindow())
		verdict := "reject"
		if state == "open" {
			verdict = "allow"
		}
		wantReason := ""
		if expect.ExpectReason != nil {
			wantReason = *expect.ExpectReason
		}
		if verdict != expect.ExpectVerdict {
			t.Errorf("%s: verdict=%s want %s (state=%s)", e.Name(), verdict, expect.ExpectVerdict, state)
		}
		if reason != wantReason {
			t.Errorf("%s: reason=%q want %q", e.Name(), reason, wantReason)
		}
		ran++
	}
	if ran != 9 {
		t.Fatalf("expected 9 engine fixtures, ran %d", ran)
	}
}

// TestEngineFixtureScriptSHA pins the vendored engine script so a silent
// re-vendor cannot change the calibration baseline unnoticed.
func TestEngineFixtureScriptSHA(t *testing.T) {
	got, err := fileSHA256(filepath.Join(fixtureDir, "metatask_replay.v1.2.0.py"))
	if err != nil {
		t.Fatalf("hash engine script: %v", err)
	}
	if got != fixtureScriptSHA {
		t.Fatalf("vendored engine script sha256=%s, want %s", got, fixtureScriptSHA)
	}
}

// TestEngineFixturesAreDistinguishing guards against a vacuous calibration: the
// fixture set must not be uniform, i.e. it must contain both allow and reject
// expectations and at least three distinct states.
func TestEngineFixturesAreDistinguishing(t *testing.T) {
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("read fixture dir: %v", err)
	}
	verdicts := map[string]int{}
	states := map[string]int{}
	names := []string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		names = append(names, e.Name())
		events := loadFixtureDir(t, filepath.Join(fixtureDir, e.Name()))
		root := fixtureRoot(t, events)
		s, err := newSettlement(events, root)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		state, _ := s.guardState("t1", 1789413000, s.policyTTL(), s.policyWindow())
		states[state]++
		if state == "open" {
			verdicts["allow"]++
		} else {
			verdicts["reject"]++
		}
	}
	sort.Strings(names)
	if verdicts["allow"] == 0 || verdicts["reject"] == 0 {
		t.Fatalf("fixture set is not distinguishing: %v", verdicts)
	}
	if len(states) < 3 {
		t.Fatalf("fixture set covers %d states, want >= 3: %v", len(states), states)
	}
	t.Logf("fixtures=%v verdicts=%v states=%v", names, verdicts, states)
}
