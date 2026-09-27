package metatask

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// v1.2.1 engine-conformance harness (registration body conformance.fixtureSet).
//
// The vector set is the single cross-engine artifact: the Python skill, this
// Go indexer and the IDBots TS engine all run the SAME file. The vendored
// copy must be byte-identical to the announced pinned copy and its
// canonical-JSON sha256 must equal the announced value — a mismatch means the
// artifact is not the one the registration publisher pinned, and the run is
// vacuous.

const (
	conformanceVectorFile  = "testdata/metatask-conformance-vectors.v1.2.1.json"
	conformanceFileSHA256  = "f8396fe28ccea2c20f075b845d662237127ce299fecad279f4d2e097b9df4c0c" // raw-file sha256 of the vendored copy
	conformanceCanonSHA256 = "106aa1f3bee8ebd48339ceb65f97a12e54247e1e831296a394a974c9cb22f2c4" // announced canonical-JSON sha256
)

type confVector struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Description string          `json:"description"`
	Input       map[string]any  `json:"input"`
	ExpectInner string          `json:"expectInner"`
	ExpectOuter string          `json:"expectOuter"`
	Events      json.RawMessage `json:"events"`
	Options     struct {
		Now int64 `json:"now"`
	} `json:"options"`
	Expect struct {
		Nodes                map[string]string `json:"nodes"`
		TaskComplete         *bool             `json:"taskComplete"`
		Settlement           *json.RawMessage  `json:"settlement"`
		SettlementShareSumBP *int64            `json:"settlementShareSumBP"`
		IgnoredContains      *struct {
			PinID  string `json:"pinId"`
			Reason string `json:"reason"`
		} `json:"ignoredContains"`
	} `json:"expect"`
}

type confSet struct {
	Version           int    `json:"version"`
	ProtocolVersion   string `json:"protocolVersion"`
	EngineAlgoVersion string `json:"engineAlgoVersion"`
	HashConstants     struct {
		HAct  int64 `json:"hAct"`
		HAct2 int64 `json:"hAct2"`
	} `json:"hashConstants"`
	Vectors []confVector `json:"vectors"`
}

func loadConformanceSet(t *testing.T) *confSet {
	t.Helper()
	path := conformanceVectorFile
	if override := os.Getenv("METATASK_CONFORMANCE_VECTORS"); override != "" {
		path = override
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read conformance vectors: %v", err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != conformanceFileSHA256 && os.Getenv("METATASK_CONFORMANCE_VECTORS") == "" {
		t.Fatalf("vendored vector file sha256=%s, want %s (the announced pinned copy)", hex.EncodeToString(sum[:]), conformanceFileSHA256)
	}
	var set confSet
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&set); err != nil {
		t.Fatalf("parse conformance vectors: %v", err)
	}
	var parsed any
	dec2 := json.NewDecoder(bytes.NewReader(raw))
	dec2.UseNumber()
	if err := dec2.Decode(&parsed); err != nil {
		t.Fatalf("re-parse conformance vectors: %v", err)
	}
	canonical, err := canonJ(parsed)
	if err != nil {
		t.Fatalf("canonJ vector set: %v", err)
	}
	if got := sha256Hex(canonical); got != conformanceCanonSHA256 {
		t.Fatalf("canonical-JSON sha256=%s, want %s (announced value)", got, conformanceCanonSHA256)
	}
	if set.ProtocolVersion != "1.2.1" {
		t.Fatalf("vector set protocolVersion=%s, want 1.2.1", set.ProtocolVersion)
	}
	if set.HashConstants.HAct != HAct || set.HashConstants.HAct2 != HAct2 {
		t.Fatalf("vector hashConstants %v do not match the engine constants (%d/%d)", set.HashConstants, HAct, HAct2)
	}
	return &set
}

// chainEventsFromVector decodes the vector's event list into ChainEvents.
// Bodies keep their decoded JSON values (numbers as json.Number so canonJ and
// the integer checks read the exact literals).
func chainEventsFromVector(t *testing.T, raw json.RawMessage) []*ChainEvent {
	t.Helper()
	var events []struct {
		PinID       string         `json:"pinId"`
		Path        string         `json:"path"`
		Author      string         `json:"author"`
		Height      int64          `json:"height"`
		TxIndex     int64          `json:"txIndex"`
		TimestampMs int64          `json:"timestampMs"`
		Body        map[string]any `json:"body"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&events); err != nil {
		t.Fatalf("decode vector events: %v", err)
	}
	out := make([]*ChainEvent, 0, len(events))
	for _, e := range events {
		out = append(out, &ChainEvent{
			PinID: e.PinID, Path: e.Path, Author: e.Author,
			Height: e.Height, TxIndex: e.TxIndex, TimestampMs: e.TimestampMs,
			Body: e.Body,
		})
	}
	return out
}

// TestV121ConformanceVectors runs the full 16-vector set (2 hash + 14 replay)
// against the v1.2.1 engine. Acceptance = 16/16 green.
func TestV121ConformanceVectors(t *testing.T) {
	set := loadConformanceSet(t)
	if len(set.Vectors) != 16 {
		t.Fatalf("expected 16 vectors, got %d", len(set.Vectors))
	}
	for _, v := range set.Vectors {
		v := v
		t.Run(v.ID, func(t *testing.T) {
			switch v.Kind {
			case "hash":
				inner, err := V121InnerHash(v.Input)
				if err != nil {
					t.Fatalf("inner hash: %v", err)
				}
				outerIn := map[string]any{}
				for k, val := range v.Input {
					outerIn[k] = val
				}
				outerIn["hash"] = inner
				outer, err := V121OuterHash(outerIn)
				if err != nil {
					t.Fatalf("outer hash: %v", err)
				}
				if inner != v.ExpectInner || outer != v.ExpectOuter {
					t.Fatalf("inner=%s outer=%s want %s/%s", inner, outer, v.ExpectInner, v.ExpectOuter)
				}
			case "replay":
				events := chainEventsFromVector(t, v.Events)
				opts := V121Options{NowMs: v.Options.Now}
				res, err := ReplayV121(events, opts)
				if err != nil {
					t.Fatalf("replay: %v", err)
				}
				for node, want := range v.Expect.Nodes {
					st, ok := res.NodeStates[node]
					if !ok {
						var keys []string
						for k := range res.NodeStates {
							keys = append(keys, k)
						}
						sort.Strings(keys)
						t.Fatalf("node %s missing (nodes: %v)", node, keys)
					}
					if st.Status != want {
						t.Errorf("node %s status=%s want %s", node, st.Status, want)
					}
				}
				if v.Expect.TaskComplete != nil && res.TaskComplete != *v.Expect.TaskComplete {
					t.Errorf("taskComplete=%v want %v", res.TaskComplete, *v.Expect.TaskComplete)
				}
				if v.Expect.Settlement != nil && string(*v.Expect.Settlement) == "null" && res.Settlement != nil {
					t.Errorf("expected no settlement, got %+v", res.Settlement)
				}
				if v.Expect.SettlementShareSumBP != nil {
					if res.Settlement == nil {
						t.Fatalf("expected a settlement, got none")
					}
					if res.Settlement.ShareSumBP != *v.Expect.SettlementShareSumBP {
						t.Errorf("settlement sum=%d want %d", res.Settlement.ShareSumBP, *v.Expect.SettlementShareSumBP)
					}
				}
				if v.Expect.IgnoredContains != nil {
					hit := false
					for _, ig := range res.IgnoredEvents {
						if ig.PinID == v.Expect.IgnoredContains.PinID && ig.Reason == v.Expect.IgnoredContains.Reason {
							hit = true
							break
						}
					}
					if !hit {
						t.Errorf("missing ignored entry {%s %s}; got %+v", v.Expect.IgnoredContains.PinID, v.Expect.IgnoredContains.Reason, res.IgnoredEvents)
					}
				}
			default:
				t.Fatalf("unknown vector kind %q", v.Kind)
			}
		})
	}
}
