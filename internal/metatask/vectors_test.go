package metatask

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

const vectorFile = "testdata/metatask-pools-vectors.v1.json"

type payloadVector struct {
	ID                 string `json:"id"`
	Payload            string `json:"payload"`
	DeclaredContentLen int64  `json:"declared_content_length"`
	Channel            string `json:"channel"`
	Expect             string `json:"expect"`
	ExpectState        string `json:"expect_state"`
	Note               string `json:"note"`
}

type enumerationVectorPage struct {
	ListSize    *int   `json:"list_size"`
	ListNull    bool   `json:"list_null"`
	ListMissing bool   `json:"list_missing"`
	NextCursor  string `json:"next_cursor"`
}

type enumerationVector struct {
	ID                string                  `json:"id"`
	Pages             []enumerationVectorPage `json:"pages"`
	ExpectComplete    bool                    `json:"expect_complete"`
	ExpectTermination string                  `json:"expect_termination"`
	ExpectUnique      *int                    `json:"expect_unique"`
	Note              string                  `json:"note"`
}

type settlementVectorEvent struct {
	Path          string         `json:"path"`
	ID            string         `json:"id"`
	GlobalMetaID  string         `json:"globalMetaId"`
	GenesisHeight int64          `json:"genesisHeight"`
	TxIndex       int64          `json:"txIndex"`
	Timestamp     int64          `json:"timestamp"`
	SeenTime      int64          `json:"seenTime"`
	Payload       map[string]any `json:"payload"`
}

type settlementVectorExpectNode struct {
	Status          string `json:"status"`
	EffectiveStatus string `json:"effective_status"`
	Holder          string `json:"holder"`
	HolderPin       string `json:"holder_pin"`
	Submission      string `json:"submission"`
	EffectivePass   *int   `json:"effective_pass"`
	ValidPassTotal  *int   `json:"valid_pass_total"`
	ValidFailTotal  *int   `json:"valid_fail_total"`
	InvalidVotes    *int   `json:"invalid_votes"`
}

type settlementVectorExpect struct {
	Quorum int                                   `json:"quorum"`
	Nodes  map[string]settlementVectorExpectNode `json:"nodes"`
}

type settlementVector struct {
	ID     string                  `json:"id"`
	Note   string                  `json:"note"`
	Events []settlementVectorEvent `json:"events"`
	Expect settlementVectorExpect  `json:"expect"`
}

type vectorsFile struct {
	Meta struct {
		Name               string `json:"name"`
		Engine             string `json:"engine"`
		EngineScriptSHA256 string `json:"engine_script_sha256"`
	} `json:"meta"`
	PayloadVectors     []payloadVector     `json:"payload_vectors"`
	EnumerationVectors []enumerationVector `json:"enumeration_vectors"`
	SettlementVectors  []settlementVector  `json:"settlement_vectors"`
}

func loadVectors(t *testing.T) vectorsFile {
	t.Helper()
	raw, err := os.ReadFile(vectorFile)
	if err != nil {
		t.Fatalf("read vectors: %v", err)
	}
	var vf vectorsFile
	if err := json.Unmarshal(raw, &vf); err != nil {
		t.Fatalf("parse vectors: %v", err)
	}
	if vf.Meta.EngineScriptSHA256 != fixtureScriptSHA {
		t.Fatalf("vector file pins engine sha %s, vendored fixture is %s", vf.Meta.EngineScriptSHA256, fixtureScriptSHA)
	}
	return vf
}

// TestPayloadVectors locks the §〇 acceptance rules, including the N1 vector:
// a truncated copy of an otherwise accepted payload must be refused.
func TestPayloadVectors(t *testing.T) {
	vf := loadVectors(t)
	if len(vf.PayloadVectors) == 0 {
		t.Fatal("no payload vectors")
	}
	for _, v := range vf.PayloadVectors {
		v := v
		t.Run(v.ID, func(t *testing.T) {
			verdict := AcceptPayloadBytes([]byte(v.Payload), v.DeclaredContentLen, v.Channel)
			got := "reject"
			if verdict.Accepted() {
				got = "accept"
			}
			if got != v.Expect {
				t.Fatalf("state=%s want %s (%s)", got, v.Expect, verdict.Reason)
			}
			if v.ExpectState != "" && verdict.State != v.ExpectState {
				t.Fatalf("state=%s want %s", verdict.State, v.ExpectState)
			}
			// Every rejection must carry a reason: a silent refusal is as bad
			// as a silent acceptance.
			if v.Expect == "reject" && verdict.Reason == "" {
				t.Fatal("rejection carried no reason string")
			}
		})
	}
}

// scriptedLister serves the pages a vector describes.
type scriptedLister struct {
	pages []Page
	calls int
}

func (s *scriptedLister) ListPath(_ context.Context, _ string, cursor string, _ int) (Page, error) {
	if s.calls >= len(s.pages) {
		return Page{}, fmt.Errorf("vector script exhausted at cursor %q", cursor)
	}
	p := s.pages[s.calls]
	s.calls++
	if cursor != p.CursorIn {
		return Page{}, fmt.Errorf("unexpected cursor %q, script recorded %q", cursor, p.CursorIn)
	}
	return p, nil
}

func pageFromVector(v enumerationVectorPage, cursorIn string, tag string) Page {
	p := Page{
		CursorIn:      cursorIn,
		NextCursor:    v.NextCursor,
		ListPresent:   !v.ListMissing,
		ListNull:      v.ListNull,
		CursorPresent: v.NextCursor != "",
		Raw:           []byte(tag + cursorIn),
		FetchedAt:     time.Unix(0, 0).UTC(),
	}
	if v.ListSize != nil {
		for i := 0; i < *v.ListSize; i++ {
			p.Pins = append(p.Pins, &Pin{ID: fmt.Sprintf("%s@%s-%d", tag, cursorIn, i), Path: PathVerify})
		}
	}
	return p
}

// TestEnumerationVectors locks the walk termination rules. The N5/N6/N7
// vectors are the ones that matter for acceptance: none of them may be read as
// an exhausted pool.
func TestEnumerationVectors(t *testing.T) {
	vf := loadVectors(t)
	for _, v := range vf.EnumerationVectors {
		v := v
		t.Run(v.ID, func(t *testing.T) {
			lister := &scriptedLister{}
			cursorIn := ""
			for _, pv := range v.Pages {
				lister.pages = append(lister.pages, pageFromVector(pv, cursorIn, v.ID))
				cursorIn = pv.NextCursor
			}
			pins, rep, err := Enumerate(context.Background(), lister, PathVerify, 50)
			if err != nil {
				t.Fatalf("enumerate: %v", err)
			}
			if rep.Complete != v.ExpectComplete {
				t.Fatalf("complete=%v want %v (termination=%s)", rep.Complete, v.ExpectComplete, rep.Termination)
			}
			if rep.Termination != v.ExpectTermination {
				t.Fatalf("termination=%s want %s", rep.Termination, v.ExpectTermination)
			}
			if v.ExpectUnique != nil && len(pins) != *v.ExpectUnique {
				t.Fatalf("unique pins=%d want %d", len(pins), *v.ExpectUnique)
			}
			if rep.Complete != rep.CursorExhausted {
				t.Fatalf("complete and cursor_exhausted disagree: %+v", rep)
			}
		})
	}
}

func eventsFromVector(v settlementVector) (map[string][]*Pin, string) {
	events := map[string][]*Pin{}
	root := ""
	for _, e := range v.Events {
		raw, _ := json.Marshal(e.Payload)
		p := &Pin{
			ID: e.ID, Path: e.Path, GlobalMetaID: e.GlobalMetaID,
			GenesisHeight: e.GenesisHeight, TxIndex: e.TxIndex,
			Timestamp: e.Timestamp, SeenTime: e.SeenTime,
			ContentLength: int64(len(raw)), Payload: raw, PayloadChannel: "vector",
			PayloadState: PayloadOK,
		}
		events[e.Path] = append(events[e.Path], p)
		if e.Path == PathTask && root == "" {
			root = e.ID
		}
	}
	return events, root
}

// TestSettlementVectors locks the v1.2.0 settlement and ballot-gate behaviour
// on this implementation.
func TestSettlementVectors(t *testing.T) {
	vf := loadVectors(t)
	for _, v := range vf.SettlementVectors {
		v := v
		t.Run(v.ID, func(t *testing.T) {
			events, root := eventsFromVector(v)
			agg, err := BuildAggregate(events, Options{Root: root, Now: 20000, GeneratedAt: time.Unix(0, 0)})
			if err != nil {
				t.Fatalf("aggregate: %v", err)
			}
			if agg.Quorum != v.Expect.Quorum {
				t.Fatalf("quorum=%d want %d", agg.Quorum, v.Expect.Quorum)
			}
			for node, want := range v.Expect.Nodes {
				var got *NodeSettlement
				for i := range agg.Nodes {
					if agg.Nodes[i].Node == node {
						got = &agg.Nodes[i]
					}
				}
				if got == nil {
					t.Fatalf("node %s missing from aggregation", node)
				}
				if want.Status != "" && got.Status != want.Status {
					t.Errorf("status=%s want %s", got.Status, want.Status)
				}
				if want.EffectiveStatus != "" && got.EffectiveStatus != want.EffectiveStatus {
					t.Errorf("effective_status=%s want %s", got.EffectiveStatus, want.EffectiveStatus)
				}
				if want.Holder != "" && got.Holder != want.Holder {
					t.Errorf("holder=%s want %s", got.Holder, want.Holder)
				}
				if want.HolderPin != "" && got.HolderPin != want.HolderPin {
					t.Errorf("holder_pin=%s want %s", got.HolderPin, want.HolderPin)
				}
				if want.Submission != "" && got.Submission != want.Submission {
					t.Errorf("submission=%s want %s", got.Submission, want.Submission)
				}
				if want.EffectivePass != nil && got.EffectivePass != *want.EffectivePass {
					t.Errorf("effective_pass=%d want %d", got.EffectivePass, *want.EffectivePass)
				}
				if want.ValidPassTotal != nil && got.ValidPassTotal != *want.ValidPassTotal {
					t.Errorf("valid_pass_total=%d want %d", got.ValidPassTotal, *want.ValidPassTotal)
				}
				if want.ValidFailTotal != nil && got.ValidFailTotal != *want.ValidFailTotal {
					t.Errorf("valid_fail_total=%d want %d", got.ValidFailTotal, *want.ValidFailTotal)
				}
				if want.InvalidVotes != nil && got.InvalidVotes != *want.InvalidVotes {
					t.Errorf("invalid_votes=%d want %d", got.InvalidVotes, *want.InvalidVotes)
				}
			}
		})
	}
}

// TestVectorFileIsCalibrated refuses a vacuous vector set.
func TestVectorFileIsCalibrated(t *testing.T) {
	vf := loadVectors(t)
	if len(vf.EnumerationVectors) == 0 || len(vf.SettlementVectors) == 0 || len(vf.PayloadVectors) == 0 {
		t.Fatal("vector file must cover payload, enumeration and settlement")
	}
	complete, incomplete := 0, 0
	for _, v := range vf.EnumerationVectors {
		if v.ExpectComplete {
			complete++
		} else {
			incomplete++
		}
	}
	if complete == 0 || incomplete == 0 {
		t.Fatalf("enumeration vectors must contain both complete and incomplete cases: %d/%d", complete, incomplete)
	}
	rejects := 0
	for _, v := range vf.PayloadVectors {
		if v.Expect == "reject" {
			rejects++
		}
	}
	if rejects == 0 {
		t.Fatal("payload vectors must contain rejections")
	}
}

func fileSHA256(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
