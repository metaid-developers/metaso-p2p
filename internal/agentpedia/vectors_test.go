package agentpedia

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// ---- fixture types (subset of the vectors file the harness needs) ----

type vectorEvent struct {
	Pin      string         `json:"pin"`
	Path     string         `json:"path"`
	Sender   string         `json:"sender"`
	Height   int64          `json:"height"`
	Arbiters []string       `json:"arbiters"`
	Payload  map[string]any `json:"payload"`
}

type vectorExpect map[string]any

type vector struct {
	ID                      string              `json:"id"`
	Title                   string              `json:"title"`
	Events                  []vectorEvent       `json:"events"`
	Expect                  vectorExpect        `json:"expect"`
	Checkpoints             []vectorCheckpoint  `json:"checkpoints"`
	Founders                []string            `json:"founders"`
	ParamOverrides          map[string]float64  `json:"paramOverrides"`
	ArbiterOverridesByEvent map[string][]string `json:"-"`
	ClusterAliases          map[string]string   `json:"clusterAliases"`
	EditorsRep              map[string]float64  `json:"editorsRep"`
	FrozenArbiterOverrides  map[string][]string `json:"frozenArbiterOverrides"`
}

type vectorCheckpoint struct {
	Pins   []string     `json:"pins"`
	Expect vectorExpect `json:"expect"`
}

type vectorsFile struct {
	Meta struct {
		PipelineCount      int `json:"pipelineCount"`
		SupplementaryCount int `json:"supplementaryCount"`
	} `json:"meta"`
	Defaults struct {
		BlocksPerHour int            `json:"blocksPerHour"`
		BlocksPerDay  int            `json:"blocksPerDay"`
		Founders      []string       `json:"founders"`
		Arbiters      []string       `json:"arbiters"`
		Params        map[string]any `json:"params"`
	} `json:"defaults"`
	Vectors       []vector `json:"vectors"`
	Supplementary []vector `json:"supplementary"`
}

func loadVectors(t *testing.T) *vectorsFile {
	t.Helper()
	raw, err := os.ReadFile("testdata/agentpedia-replay-vectors.v1.json")
	if err != nil {
		t.Fatalf("read vectors fixture: %v", err)
	}
	var f vectorsFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("parse vectors fixture: %v", err)
	}
	return &f
}

// osReadVectorsDefaults exposes the fixture constitution params to other tests.
func osReadVectorsDefaults() (map[string]float64, error) {
	raw, err := os.ReadFile("testdata/agentpedia-replay-vectors.v1.json")
	if err != nil {
		return nil, err
	}
	var f struct {
		Defaults struct {
			Params map[string]float64 `json:"params"`
		} `json:"defaults"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, err
	}
	return f.Defaults.Params, nil
}

var pathByID = map[string]string{
	"rev": PathRev, "challenge": PathChallenge, "ruling": PathRuling,
	"review": PathReview, "editor": PathEditor, "constitution": PathConstitution,
	"param-proposal": PathParamProposal,
}

func paramsToAny(m map[string]float64) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (f *vectorsFile) buildEvents(vec vector) ([]Event, Options) {
	founders := vec.Founders
	if founders == nil {
		founders = f.Defaults.Founders
	}
	params := map[string]any{}
	for k, v := range f.Defaults.Params {
		params[k] = v
	}
	for k, v := range vec.ParamOverrides {
		params[k] = v
	}
	events := []Event{{
		Pin: "genesis", Path: PathConstitution, Sender: founders[0], Height: 1, TxIndex: 0,
		Payload: map[string]any{
			"v": 1.0, "revision": 0.0, "prevConstitution": nil, "proposalPin": nil,
			"founders": founders, "params": params,
			"algoVersions": map[string]any{"adoption": "adoption-algo-v1", "reputation": "reputation-algo-v1", "arbiterDraw": "arbiter-draw-v1"},
		},
	}}
	arbiterOverrides := map[string][]string{}
	for _, e := range vec.Events {
		path, ok := pathByID[e.Path]
		if !ok {
			path = e.Path
		}
		events = append(events, Event{Pin: e.Pin, Path: path, Sender: e.Sender, Height: e.Height, TxIndex: 0, Payload: e.Payload})
		arbiters := e.Arbiters
		if arbiters == nil {
			arbiters = f.Defaults.Arbiters
		}
		if e.Path == "ruling" && e.Payload["action"] == "proposal" {
			arbiterOverrides[e.Pin] = arbiters
		}
	}
	opts := Options{
		BlocksPerHour:          f.Defaults.BlocksPerHour,
		BlocksPerDay:           f.Defaults.BlocksPerDay,
		ArbiterOverrides:       arbiterOverrides,
		FrozenArbiterOverrides: vec.FrozenArbiterOverrides,
		EditorsRep:             vec.EditorsRep,
		ClusterAliases:         vec.ClusterAliases,
	}
	return events, opts
}

// resolve walks a dotted path over the JSON-normalized view
// (e.g. "entries.zh:a.head", "graveyard", "editors.X.status").
func resolve(view *View, dotted string) (any, error) {
	normalized, err := json.Marshal(view)
	if err != nil {
		return nil, err
	}
	var cur any
	if err := json.Unmarshal(normalized, &cur); err != nil {
		return nil, err
	}
	for _, seg := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("cannot descend into %T at %q", cur, seg)
		}
		cur, ok = m[seg]
		if !ok {
			return nil, fmt.Errorf("missing key %q", seg)
		}
	}
	return cur, nil
}

func deepEqualJSON(a, b any) bool {
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return reflect.DeepEqual(aj, bj)
}

func assertExpect(t *testing.T, view *View, expect vectorExpect) {
	t.Helper()
	keys := make([]string, 0, len(expect))
	for k := range expect {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, path := range keys {
		expected := expect[path]
		switch {
		case strings.HasSuffix(path, ".$contains"):
			parent, err := resolve(view, strings.TrimSuffix(path, ".$contains"))
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			list, ok := parent.([]any)
			if !ok {
				t.Fatalf("%s: not an array: %v", path, parent)
			}
			found := false
			for _, el := range list {
				if expectedMap, isMap := expected.(map[string]any); isMap {
					elMap, ok := el.(map[string]any)
					if !ok {
						continue
					}
					all := true
					for k, v := range expectedMap {
						if !deepEqualJSON(elMap[k], v) {
							all = false
							break
						}
					}
					if all {
						found = true
						break
					}
				} else if deepEqualJSON(el, expected) {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s: expected to contain %v, got %v", path, expected, parent)
			}
		default:
			if idx, parentPath, ok := splitIndexedContains(path); ok {
				parent, err := resolve(view, parentPath)
				if err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				list, ok := parent.([]any)
				if !ok {
					t.Fatalf("%s: not an array: %v", parentPath, parent)
				}
				if idx >= len(list) {
					t.Fatalf("%s: index %d out of range (len %d)", path, idx, len(list))
				}
				if !deepEqualJSON(list[idx], expected) {
					t.Fatalf("%s: expected %v, got %v", path, expected, list[idx])
				}
				continue
			}
			if strings.HasSuffix(path, ".length") {
				parent, err := resolve(view, strings.TrimSuffix(path, ".length"))
				if err != nil {
					t.Fatalf("%s: %v", path, err)
				}
				list, ok := parent.([]any)
				if !ok {
					t.Fatalf("%s: not an array: %v", path, parent)
				}
				want, _ := expected.(float64)
				if float64(len(list)) != want {
					t.Fatalf("%s: expected length %v, got %d", path, expected, len(list))
				}
				continue
			}
			actual, err := resolve(view, path)
			if err != nil {
				t.Fatalf("%s: %v", path, err)
			}
			if !deepEqualJSON(actual, expected) {
				t.Fatalf("%s: expected %v (%T), got %v (%T)", path, expected, expected, actual, actual)
			}
		}
	}
}

// splitIndexedContains matches "<parent>.$contains.<n>".
func splitIndexedContains(path string) (int, string, bool) {
	marker := ".$contains."
	i := strings.LastIndex(path, marker)
	if i < 0 {
		return 0, "", false
	}
	tail := path[i+len(marker):]
	n := 0
	for _, c := range tail {
		if c < '0' || c > '9' {
			return 0, "", false
		}
		n = n*10 + int(c-'0')
	}
	if tail == "" {
		return 0, "", false
	}
	return n, path[:i], true
}

func runVector(t *testing.T, f *vectorsFile, vec vector) {
	t.Helper()
	events, opts := f.buildEvents(vec)
	checkpoints := vec.Checkpoints
	if checkpoints == nil {
		checkpoints = []vectorCheckpoint{{Pins: nil, Expect: vec.Expect}}
	}
	for _, cp := range checkpoints {
		slice := events
		if cp.Pins != nil {
			keep := map[string]bool{}
			for _, p := range cp.Pins {
				keep[p] = true
			}
			slice = nil
			for _, e := range events {
				if keep[e.Pin] {
					slice = append(slice, e)
				}
			}
		}
		view := Replay(slice, opts)
		assertExpect(t, view, cp.Expect)
	}
}

func TestPipelineVectors25Of25(t *testing.T) {
	f := loadVectors(t)
	if len(f.Vectors) != 25 {
		t.Fatalf("expected 25 pipeline vectors, got %d", len(f.Vectors))
	}
	for _, vec := range f.Vectors {
		vec := vec
		t.Run(vec.ID, func(t *testing.T) {
			runVector(t, f, vec)
		})
	}
}

func TestSupplementaryVectors(t *testing.T) {
	f := loadVectors(t)
	for _, vec := range f.Supplementary {
		vec := vec
		t.Run(vec.ID, func(t *testing.T) {
			runVector(t, f, vec)
		})
	}
}

func TestHeadline25Of25(t *testing.T) {
	f := loadVectors(t)
	for _, vec := range f.Vectors {
		runVector(t, f, vec)
	}
}
