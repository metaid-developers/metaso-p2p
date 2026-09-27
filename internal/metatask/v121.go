package metatask

// MetaTask v1.2.1 replay engine (registration body
// pin://cbae49e09697182b076652ae9bded629388a0dafc9048b01c6a7c8766247fc55i0).
//
// This engine is the Go side of the three-engine consistency contract
// (conformance.fixtureSet): the Python skill, this indexer and the IDBots TS
// engine (src/main/services/metatask/engine.ts, the semantic reference used
// to port this file) must run the same vector set green and produce
// byte-identical eventSetHash / settlement manifests on the same event set.
//
// Semantics (all heights are the v1.2.1 gates):
//   - H_ACT  = 190000: #8/#9 vote gates (semantic_check, failreason).
//   - H_ACT2 = 191500: tree weights, policy.split, /amend, /challenge,
//     roster same-side filtering, supersedeid, submitter≠root-author,
//     spec.validation, aggregation precondition.
//
// Ordering is the unified total order (height, txIndex, timestampMs, pinId);
// claims, releases, submissions and last-valid votes interleave in one walk;
// an acting fail reopens the node immediately (ruling three); expiry (claim
// TTL / review window / challenge TTL) is derived only against the evaluation
// clock passed in NowMs, never a wall clock.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// v1.2.1 protocol anchors (registration body v1.2.1, supersedes 1.2.0).
const (
	// HAct2 is the v1.2 feature switch (activation.H_ACT2, round height
	// published 2026-09-27, chain height at publish 191204). Features listed
	// in activation.gatedAtHAct2 bind at height >= HAct2 only; below it the
	// v1.1 semantics hold and existing instances are grandfathered.
	HAct2 = int64(191500)

	// ProtocolVersionV121 / ProtocolPinIDV121 anchor the semantics source.
	ProtocolVersionV121 = "1.2.1"
	ProtocolPinIDV121   = "cbae49e09697182b076652ae9bded629388a0dafc9048b01c6a7c8766247fc55i0"

	// EngineAlgoVersionV121 is the shared three-engine algorithm version the
	// v1.2.1 fixture set pins (its own engineAlgoVersion field). The manifest
	// self-describes the ALGORITHM, not the implementation, so all three
	// engines emit this same value for byte-identical manifests.
	EngineAlgoVersionV121 = "idbots-metatask-engine/1.2.1"

	// Settlement constants (settlement.definitions — deliberately protocol
	// constants, never payload-configurable).
	SubmitterShareBPDefault = int64(8000)
	SubmitterShareBPMin     = int64(6000)
	SubmitterShareBPMax     = int64(9000)
	ReviewerAccuracyFloorBP = int64(2500)
	ChallengeTTLDefaultDays = int64(14)
	TreeWeightTotal         = int64(10000)
)

// V121EventPaths is the fixed eventSetHash path order (settlement.
// eventSetHash.recipe): [task, tree, spec, claim, release, submission,
// verify, amend, challenge].
var V121EventPaths = []string{"task", "tree", "spec", "claim", "release", "submission", "verify", "amend", "challenge"}

// ChainEvent is one decoded protocol event. Path is the short path name
// (task|tree|spec|claim|release|submission|verify|amend|challenge).
// TimestampMs is milliseconds; pin adapters convert chain seconds ×1000.
type ChainEvent struct {
	PinID       string         `json:"pinId"`
	Path        string         `json:"path"`
	Author      string         `json:"author"`
	Height      int64          `json:"height"`
	TxIndex     int64          `json:"txIndex"`
	TimestampMs int64          `json:"timestampMs"`
	Body        map[string]any `json:"body"`
}

// V121Options configures one replay. NowMs is the evaluation clock in ms
// (claim TTL / review window / challenge TTL derivation); 0 = no expiry
// derivation. HAct2 < 0 disables the v1.2 gate (the TS engine's Infinity).
type V121Options struct {
	RootPinID     string
	NowMs         int64
	HAct          int64
	HAct2         int64
	EvaluatedAtMs int64
	RosterPins    map[string]map[string]any // roster pinId -> decoded body
}

// ---------------------------------------------------------------------------
// canonical JSON + double hash (Appendix A, python parity)
// ---------------------------------------------------------------------------

// canonJ canonicalizes exactly like the Appendix A reference:
// json.dumps(o, ensure_ascii=False, sort_keys=True, separators=(',',':')).
// Accepted values: nil, bool, string, int64/int, json.Number (verbatim — the
// decoded literal), float64, []any, map[string]any.
func canonJ(v any) ([]byte, error) {
	var b strings.Builder
	if err := writeCanonJ(&b, v); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func writeCanonJ(b *strings.Builder, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		b.WriteString(pyJSONString(t))
	case int:
		b.WriteString(strconv.Itoa(t))
	case int64:
		b.WriteString(strconv.FormatInt(t, 10))
	case json.Number:
		// The decoded literal, verbatim (integer literals stay integers).
		s := t.String()
		if s == "" {
			return fmt.Errorf("canonJ: empty json.Number")
		}
		b.WriteString(s)
	case float64:
		b.WriteString(strconv.FormatFloat(t, 'g', -1, 64))
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeCanonJ(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(pyJSONString(k))
			b.WriteByte(':')
			if err := writeCanonJ(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("canonJ: unsupported value %T", v)
	}
	return nil
}

// pyJSONString escapes exactly like Python's json.dumps ensure_ascii=False:
// backslash, double quote, \b \f \n \r \t, \u00xx for the remaining control
// range; everything else (including non-ASCII) is emitted raw.
func pyJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// V121InnerHash: sha256 over the result object with the top-level "hash" key
// removed (shallow delete) — Appendix A inner_hash.
func V121InnerHash(result map[string]any) (string, error) {
	core := make(map[string]any, len(result))
	for k, v := range result {
		if k == "hash" {
			continue
		}
		core[k] = v
	}
	b, err := canonJ(core)
	if err != nil {
		return "", err
	}
	return sha256Hex(b), nil
}

// V121OuterHash: sha256 over the full result object (inner hash embedded) —
// Appendix A outer_hash.
func V121OuterHash(result map[string]any) (string, error) {
	b, err := canonJ(result)
	if err != nil {
		return "", err
	}
	return sha256Hex(b), nil
}

// ---------------------------------------------------------------------------
// body accessors (JSON-decoded, json.Number-preserving)
// ---------------------------------------------------------------------------

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func truthyStrAny(v any) bool {
	s, ok := v.(string)
	return ok && strings.TrimSpace(s) != ""
}

func numAny(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return 0, false
		}
		return f, true
	}
	return 0, false
}

// ---------------------------------------------------------------------------
// ordering
// ---------------------------------------------------------------------------

type ordKey struct {
	h, tx, ts int64
	pin       string
}

func ordKeyOf(e *ChainEvent) ordKey {
	h := e.Height
	if h < 0 {
		h = int64(math.MaxInt64) // unconfirmed sorts last (TS BIG)
	}
	return ordKey{h: h, tx: e.TxIndex, ts: e.TimestampMs, pin: e.PinID}
}

func ordLess(a, b ordKey) bool {
	if a.h != b.h {
		return a.h < b.h
	}
	if a.tx != b.tx {
		return a.tx < b.tx
	}
	if a.ts != b.ts {
		return a.ts < b.ts
	}
	return a.pin < b.pin
}

func sortChainEvents(list []*ChainEvent) {
	sort.SliceStable(list, func(i, j int) bool { return ordLess(ordKeyOf(list[i]), ordKeyOf(list[j])) })
}

// ---------------------------------------------------------------------------
// tree nodes
// ---------------------------------------------------------------------------

// V121TreeNode keeps the raw JSON shapes: parent nil = null (root); weight
// nil = absent (legacy). add_node amends carry parent as a plain string
// (possibly ""), mirroring the reference's asStr(raw.parent).
type V121TreeNode struct {
	ID     string
	Parent any // nil (null) or string
	Title  string
	Kind   string
	SpecID any // nil (null/absent) or string
	Params map[string]any
	Deps   []string
	Weight any // nil or number
}

func treeNodeWeight(n *V121TreeNode) (int64, bool) {
	f, ok := numAny(n.Weight)
	if !ok || f != math.Trunc(f) || f < 1 || f > 10000 {
		return 0, false
	}
	return int64(f), true
}

func v121SumWeights(nodes map[string]*V121TreeNode) int64 {
	total := int64(0)
	for _, n := range nodes {
		w, ok := treeNodeWeight(n)
		if !ok {
			return -1
		}
		total += w
	}
	return total
}

func v121IsAcyclic(nodes map[string]*V121TreeNode) bool {
	for start := range nodes {
		seen := map[string]bool{}
		cursor := start
		for cursor != "" {
			if seen[cursor] {
				return false
			}
			seen[cursor] = true
			node := nodes[cursor]
			if node == nil {
				cursor = "" // missing parent behaves as null (reference reading)
				break
			}
			if p, ok := node.Parent.(string); ok {
				cursor = p
			} else {
				cursor = ""
			}
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// engine state
// ---------------------------------------------------------------------------

// V121CycleSubmission is one submission inside a claim cycle.
type V121CycleSubmission struct {
	PinID       string
	Author      string
	AtMs        int64
	Height      int64
	SupersedeID string // "" = none
	Body        map[string]any
}

// V121Cycle is one (node, claim) submission cycle.
type V121Cycle struct {
	Node        string
	ClaimID     string
	Claimant    string
	Submissions []*V121CycleSubmission
	Effective   *V121CycleSubmission
	Superseded  map[string]bool
	Outcome     string // verified | fail_rejected | open | superseded
	QuorumAtMs  int64  // ts of the acting pass that first reached quorum (additive)
}

// V121Holder is a node's live claim.
type V121Holder struct {
	PinID    string
	Claimant string
	SinceMs  int64
	Height   int64
}

// IgnoredEvent is one recorded-but-ineffective fact (reason strings match the
// reference engine's closed set).
type IgnoredEvent struct {
	PinID  string `json:"pinId"`
	Reason string `json:"reason"`
}

// V121Participant is one bot's role stats for the task.
type V121Participant struct {
	MetaID          string
	EffectiveClaims int
	Submissions     int
	VerifiedContrib int
	ReviewVotes     int
	ReviewCorrect   int
	ReviewTerminal  int
}

// V121Manifest is the settlement manifest (settlement.manifest). Canonical
// bytes are kept for the three-engine byte-parity check.
type V121Manifest struct {
	TaskID            string
	BoundaryBlock     int64
	EventSetHash      string
	EngineAlgoVersion string
	Shares            []V121Share
	UnpaidHistory     []V121Unpaid
	WeightsTableHash  string
	ShareSumBP        int64
	Canonical         []byte
}

// V121Share is one settled share row.
type V121Share struct {
	MetaID      string
	ShareBP     int64
	SubmittedBP int64
	ReviewedBP  int64
}

// V121Unpaid is one unpaid-history row (superseded or fail-rejected rework).
type V121Unpaid struct {
	Node   string
	Author string
	PinID  string
	Reason string
}

// V121Progress counts the node buckets.
type V121Progress struct {
	Total    int
	Verified int
	Claimed  int
	Open     int
	Disputed int
}

// V121NodeState is the per-node v1.2.1 view plus the renderer bookkeeping
// (frozen §5 projection rows live in replay.go).
type V121NodeState struct {
	Node       string
	Status     string // verified | claimed | open
	Disputed   bool
	Holder     *V121Holder
	Cycle      *V121Cycle // the node's current cycle (live or terminal)
	PassVotes  int
	FailVotes  int
	CycleCount int
	Weight     *int64

	// Renderer extras (not part of any hashed output).
	LastOutcome  string // none|rejected|released|expired_claim|expired_review_window
	VerifiedAtMs int64  // 0 = never (quorum-crossing vote ts)
	Votes        []VoteRecord
	Events       []EventRecord
	LastTouchPin string
	LastTouchMs  int64
	AggNote      string
	InTree       bool
}

// V121VoteDisposition is one ballot's frozen-view verdict: counted, or the
// closed ignore-reason set. Additive bookkeeping — never hashed.
type V121VoteDisposition struct {
	Counts bool
	Reason string
}

// V121Result is the replay conclusion for one task root.
type V121Result struct {
	RootPinID        string
	Title            string
	Brief            string
	Publisher        string
	Tags             []string
	RawPolicy        map[string]any
	Quorum           int
	TTLHours         int
	WindowHours      int
	ChallengeTTLDays int64
	HasSplit         bool
	RosterID         *string
	TreePinID        string
	SpecID           string
	AmendHead        string
	EffectiveTree    map[string]*V121TreeNode
	TreeOrder        []string // effective-tree insertion order
	NodeOrder        []string // nodeIds sorted by (len, lexicographic)
	NodeStates       map[string]*V121NodeState
	TaskComplete     bool
	RootVerified     bool // same as TaskComplete (v1.2.1: root node verified)
	Progress         V121Progress
	Participants     []V121Participant
	Settlement       *V121Manifest
	EventSetHash     string
	BoundaryBlock    int64
	IgnoredEvents    []IgnoredEvent
	ExpiryApplied    bool

	// Engine internals the renderer needs (not serialized).
	Cycles                 []*V121Cycle // insertion (chain) order
	ActiveCycleByNode      map[string]*V121Cycle
	Holders                map[string]*V121Holder
	FirstClaimOrder        map[string]ordKey
	FinalVerified          map[string]bool
	FinalVerifiedPins      map[string]bool
	DisputedNodes          map[string]bool
	VerifyByTarget         map[string][]*ChainEvent // every in-scope verify pin, sorted
	LastVoteByTargetBot    map[[2]string]*ChainEvent
	SubmissionEventsByNode map[string][]*ChainEvent
	ClaimEventsByNode      map[string][]*ChainEvent
	ReleaseEventsByNode    map[string][]*ChainEvent
	ClaimCounted           map[string]bool
	ReleaseCounted         map[string]bool
	SubmissionCycle        map[string]*V121Cycle // effective-chain members only
	NodeOutcome            map[string]string     // none|rejected|released|expired_claim|expired_review_window
	VerifyDisposition      map[string]*V121VoteDisposition
	IgnoredByPin           map[string]string // pin -> first ignore reason
	RootTreeNodeID         string
}

// v121GateReason mirrors the reference voteInvalidReason: the #8/#9
// requirements bind only at height >= hAct.
func v121GateReason(body map[string]any, height, hAct int64) string {
	if height < hAct {
		return ""
	}
	if !truthyStrAny(body["semantic_check"]) {
		return "missing_semantic_check"
	}
	if strAny(body["verdict"]) == "fail" && !truthyStrAny(body["failreason"]) {
		return "missing_failreason"
	}
	return ""
}

func rosterGroupsFor(body map[string]any) [][]string {
	if body == nil {
		return nil
	}
	var raw []any
	if g, ok := body["groups"].([]any); ok {
		raw = g
	} else {
		return nil // the reference only accepts {groups:[...]} objects here
	}
	var out [][]string
	for _, grp := range raw {
		arr, ok := grp.([]any)
		if !ok {
			continue
		}
		var ids []string
		for _, m := range arr {
			if s, ok := m.(string); ok {
				ids = append(ids, s)
			}
		}
		out = append(out, ids)
	}
	return out
}

func sameSide(groups [][]string, a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	for _, grp := range groups {
		ha, hb := false, false
		for _, id := range grp {
			if id == a {
				ha = true
			}
			if id == b {
				hb = true
			}
		}
		if ha && hb {
			return true
		}
	}
	return false
}

// foldAmends ports the reference foldAmends: publisher authority, bases
// version chain (earliest-wins conflicts), point-in-time freeze on the first
// effective claim, and the fold invariants. Any failure ignores the whole
// amend (paths.amend.foldInvariants).
func foldAmends(amends []*ChainEvent, rootAuthor, treePinID string, initial map[string]*V121TreeNode, order []string, firstClaimOrder map[string]ordKey, verifiedNodeIds map[string]bool, activeCycleNodes map[string]bool, rootVerified bool, hAct2 int64) (map[string]*V121TreeNode, []string, []IgnoredEvent, string) {
	ignored := []IgnoredEvent{}
	nodes := make(map[string]*V121TreeNode, len(initial))
	nodeOrder := append([]string{}, order...)
	for id, n := range initial {
		nodes[id] = n
	}
	takenBases := map[string]bool{}
	head := treePinID

	frozenAt := func(nodeID string, amend ordKey) bool {
		claim, ok := firstClaimOrder[nodeID]
		if !ok {
			return false
		}
		return ordLess(claim, amend)
	}

	for _, amend := range amends {
		key := ordKeyOf(amend)
		bases := strAny(amend.Body["bases"])
		if amend.Height < hAct2 {
			ignored = append(ignored, IgnoredEvent{amend.PinID, "below_h_act2"})
			continue
		}
		if amend.Author != rootAuthor {
			ignored = append(ignored, IgnoredEvent{amend.PinID, "amend_not_publisher"})
			continue
		}
		if rootVerified {
			ignored = append(ignored, IgnoredEvent{amend.PinID, "amend_task_finalized"})
			continue
		}
		if bases != head {
			reason := "amend_stale"
			if takenBases[bases] {
				reason = "amend_conflict"
			}
			ignored = append(ignored, IgnoredEvent{amend.PinID, reason})
			continue
		}
		takenBases[bases] = true

		// scratch copy; commit only if every invariant holds.
		scratch := make(map[string]*V121TreeNode, len(nodes))
		scratchOrder := append([]string{}, nodeOrder...)
		for id, n := range nodes {
			cp := *n
			scratch[id] = &cp
		}
		ok := true
		ops, _ := amend.Body["ops"].([]any)
	opsLoop:
		for _, rawOp := range ops {
			op, okOp := rawOp.(map[string]any)
			if !okOp {
				ok = false
				break opsLoop
			}
			kind := strAny(op["op"])
			targetID := strAny(op["node"])
			switch kind {
			case "add_node":
				var raw map[string]any
				if v, has := op["newNode"]; has {
					raw, _ = v.(map[string]any)
				}
				if raw == nil {
					if v, has := op["node"]; has {
						raw, _ = v.(map[string]any)
					}
				}
				id := strAny(raw["id"])
				parent := strAny(raw["parent"])
				if raw == nil || id == "" || nodes[id] != nil || scratch[id] != nil {
					ok = false
					break opsLoop
				}
				parentNode := scratch[parent]
				if parentNode == nil {
					parentNode = nodes[parent]
				}
				if parentNode == nil || verifiedNodeIds[parent] || activeCycleNodes[parent] {
					ok = false
					break opsLoop
				}
				n := &V121TreeNode{
					ID: id, Parent: parent, Title: strAny(raw["title"]),
					Kind: strAny(raw["kind"]), Params: map[string]any{},
				}
				if n.Kind == "" {
					n.Kind = "proof" // reference default when kind absent
				}
				if v, has := raw["specid"]; has {
					if s, isStr := v.(string); isStr {
						n.SpecID = s
					}
				}
				if p, isMap := raw["params"].(map[string]any); isMap {
					n.Params = p
				}
				if d, isArr := raw["deps"].([]any); isArr {
					for _, dv := range d {
						if s, isStr := dv.(string); isStr {
							n.Deps = append(n.Deps, s)
						}
					}
				}
				if w, has := raw["weight"]; has {
					n.Weight = w
				}
				scratch[id] = n
				scratchOrder = append(scratchOrder, id)
			case "remove_node":
				target := scratch[targetID]
				if target == nil {
					ok = false
					break opsLoop
				}
				if _, isRoot := target.Parent.(string); !isRoot {
					// parent null (or non-string) = root; root is not removable
					ok = false
					break opsLoop
				}
				stack := []string{targetID}
				for len(stack) > 0 && ok {
					cur := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					if frozenAt(cur, key) {
						ok = false
						break
					}
					for id, n := range scratch {
						if p, isStr := n.Parent.(string); isStr && p == cur {
							stack = append(stack, id)
						}
					}
				}
				if !ok {
					break opsLoop
				}
				// Reference reading: only the named node is deleted; its
				// children keep a dangling parent (documented divergence from
				// "整棵子树" removal — kept for three-engine behavior parity).
				delete(scratch, targetID)
				for i, id := range scratchOrder {
					if id == targetID {
						scratchOrder = append(scratchOrder[:i], scratchOrder[i+1:]...)
						break
					}
				}
			case "reweight":
				target := scratch[targetID]
				w, wOk := numAny(op["weight"])
				if target == nil || frozenAt(targetID, key) || !wOk || w != math.Trunc(w) || w < 1 || w > 10000 {
					ok = false
					break opsLoop
				}
				target.Weight = op["weight"]
			case "retitle":
				target := scratch[targetID]
				if target == nil || frozenAt(targetID, key) || !truthyStrAny(op["title"]) {
					ok = false
					break opsLoop
				}
				target.Title = strAny(op["title"])
			case "respec":
				target := scratch[targetID]
				if target == nil || frozenAt(targetID, key) || !truthyStrAny(op["specid"]) {
					ok = false
					break opsLoop
				}
				target.SpecID = strAny(op["specid"])
			default:
				ok = false
				break opsLoop
			}
		}
		if !ok || v121SumWeights(scratch) != TreeWeightTotal || !v121IsAcyclic(scratch) {
			ignored = append(ignored, IgnoredEvent{amend.PinID, "amend_invariant_violation"})
			continue
		}
		nodes = scratch
		nodeOrder = scratchOrder
		head = amend.PinID
	}
	return nodes, nodeOrder, ignored, head
}

// ReplayV121 replays one MetaTask from its decoded event set (any task scope;
// events are filtered to this root internally, mirroring the reference).
func ReplayV121(events []*ChainEvent, opts V121Options) (*V121Result, error) {
	hAct := opts.HAct
	if hAct == 0 {
		hAct = HAct
	}
	hAct2 := opts.HAct2
	if hAct2 == 0 {
		hAct2 = HAct2
	} else if hAct2 < 0 {
		hAct2 = int64(math.MaxInt64)
	}
	hasNow := opts.NowMs != 0
	now := opts.NowMs

	byPath := map[string][]*ChainEvent{}
	for _, e := range events {
		byPath[e.Path] = append(byPath[e.Path], e)
	}
	for _, list := range byPath {
		sortChainEvents(list)
	}

	// -- task root -----------------------------------------------------------
	taskPins := byPath["task"]
	var rootPin *ChainEvent
	if opts.RootPinID != "" {
		for _, p := range taskPins {
			if p.PinID == opts.RootPinID {
				rootPin = p
				break
			}
		}
	} else if len(taskPins) > 0 {
		rootPin = taskPins[len(taskPins)-1]
	}
	if rootPin == nil {
		return nil, fmt.Errorf("metatask v1.2.1 replay: no task root pin in event set")
	}
	rootAuthor := rootPin.Author
	taskBody := rootPin.Body
	policy, _ := taskBody["policy"].(map[string]any)
	if policy == nil {
		policy = map[string]any{}
	}
	quorum := 2
	if f, ok := numAny(policy["verify_quorum"]); ok {
		quorum = int(f)
	}
	if quorum < 1 {
		quorum = 1
	}
	ttlHours := 0
	if f, ok := numAny(policy["claim_ttl_hours"]); ok {
		ttlHours = int(f)
	}
	windowHours := 0
	if f, ok := numAny(policy["verify_window_hours"]); ok {
		windowHours = int(f)
	}
	challengeTTL := ChallengeTTLDefaultDays
	if f, ok := numAny(policy["challenge_ttl_days"]); ok {
		challengeTTL = int64(f)
	}
	split, hasSplit := policy["split"].(map[string]any)

	// -- task scoping ---------------------------------------------------------
	treePinID := strAny(taskBody["treeid"])
	specID := strAny(taskBody["specid"])
	var treePin *ChainEvent
	for _, p := range byPath["tree"] {
		if p.PinID == treePinID {
			treePin = p
			break
		}
	}
	submissions := []*ChainEvent{}
	for _, p := range byPath["submission"] {
		if strAny(p.Body["taskid"]) == rootPin.PinID {
			submissions = append(submissions, p)
		}
	}
	sortChainEvents(submissions)
	subAuthorByPin := map[string]string{}
	subBodyByPin := map[string]map[string]any{}
	knownTargets := map[string]bool{}
	for _, s := range submissions {
		subAuthorByPin[s.PinID] = s.Author
		subBodyByPin[s.PinID] = s.Body
		knownTargets[s.PinID] = true
	}
	submissionEventsByNode := map[string][]*ChainEvent{}
	for _, s := range submissions {
		node := strAny(s.Body["node"])
		if node != "" {
			submissionEventsByNode[node] = append(submissionEventsByNode[node], s)
		}
	}

	// -- vote pre-pass: gates, roster, last valid per (target, bot) -----------
	rosterGroups := [][]string(nil)
	rosterIDStr := strAny(split["rosterid"])
	if rosterIDStr != "" && opts.RosterPins != nil {
		rosterGroups = rosterGroupsFor(opts.RosterPins[rosterIDStr])
	}
	ignoredEvents := []IgnoredEvent{}
	verifyByTarget := map[string][]*ChainEvent{}
	lastVotes := map[[2]string]*ChainEvent{} // (target, bot) -> pin
	for _, vote := range byPath["verify"] {
		target := strAny(vote.Body["targetid"])
		if target == "" || !knownTargets[target] {
			continue
		}
		verifyByTarget[target] = append(verifyByTarget[target], vote)
		if reason := v121GateReason(vote.Body, vote.Height, hAct); reason != "" {
			ignoredEvents = append(ignoredEvents, IgnoredEvent{vote.PinID, reason})
			continue
		}
		if hAct2 != math.MaxInt64 && vote.Height >= hAct2 && len(rosterGroups) > 0 {
			submitter := subAuthorByPin[target]
			if sameSide(rosterGroups, vote.Author, submitter) || sameSide(rosterGroups, vote.Author, rootAuthor) {
				ignoredEvents = append(ignoredEvents, IgnoredEvent{vote.PinID, "same_side_roster"})
				continue
			}
		}
		lastVotes[[2]string{target, vote.Author}] = vote
	}
	votesByTarget := map[string][]*ChainEvent{}
	lastVoteByPin := map[string]*ChainEvent{}
	for _, v := range lastVotes { // map iteration order is irrelevant: grouped per target then sorted below
		target := strAny(v.Body["targetid"])
		votesByTarget[target] = append(votesByTarget[target], v)
		lastVoteByPin[v.PinID] = v
	}
	for _, list := range votesByTarget {
		sortChainEvents(list)
	}

	// -- unified ordered walk --------------------------------------------------
	claims := []*ChainEvent{}
	releases := []*ChainEvent{}
	for _, p := range byPath["claim"] {
		if strAny(p.Body["taskid"]) == rootPin.PinID {
			claims = append(claims, p)
		}
	}
	for _, p := range byPath["release"] {
		if strAny(p.Body["taskid"]) == rootPin.PinID {
			releases = append(releases, p)
		}
	}
	actingVotes := []*ChainEvent{}
	for _, p := range byPath["verify"] {
		if lastVoteByPin[p.PinID] != nil {
			actingVotes = append(actingVotes, p)
		}
	}
	timeline := make([]*ChainEvent, 0, len(claims)+len(releases)+len(submissions)+len(actingVotes))
	timeline = append(timeline, claims...)
	timeline = append(timeline, releases...)
	timeline = append(timeline, submissions...)
	timeline = append(timeline, actingVotes...)
	sortChainEvents(timeline)

	holders := map[string]*V121Holder{}
	firstClaimOrder := map[string]ordKey{}
	effectiveClaimCounts := map[string]int{}
	cycles := []*V121Cycle{} // insertion order = chain order of first attach
	cycleByNodeClaim := map[string]*V121Cycle{}
	verifiedSubmissionPins := map[string]bool{}
	passVotersByNode := map[string]map[string]string{}
	nodeOfLiveTarget := map[string]string{}
	claimEventsByNode := map[string][]*ChainEvent{}
	releaseEventsByNode := map[string][]*ChainEvent{}
	// Renderer bookkeeping (never part of a hashed output).
	claimCounted := map[string]bool{}   // claim pin took the lock at its position
	releaseCounted := map[string]bool{} // release matched the live holder
	submissionCycle := map[string]*V121Cycle{}
	nodeOutcome := map[string]string{}
	reachedVote := map[string]bool{} // surviving ballot reached a live target in the walk

	getCycle := func(node, claimID string) *V121Cycle {
		return cycleByNodeClaim[node+" "+claimID]
	}

	for _, pin := range timeline {
		switch pin.Path {
		case "claim":
			node := strAny(pin.Body["node"])
			if node == "" || holders[node] != nil {
				if node != "" {
					claimEventsByNode[node] = append(claimEventsByNode[node], pin)
				}
				continue
			}
			holders[node] = &V121Holder{PinID: pin.PinID, Claimant: pin.Author, SinceMs: pin.TimestampMs, Height: pin.Height}
			if _, ok := firstClaimOrder[node]; !ok {
				firstClaimOrder[node] = ordKeyOf(pin)
			}
			effectiveClaimCounts[pin.Author]++
			claimCounted[pin.PinID] = true
			nodeOutcome[node] = "none"
			claimEventsByNode[node] = append(claimEventsByNode[node], pin)
		case "release":
			node := strAny(pin.Body["node"])
			if node == "" {
				continue
			}
			releaseEventsByNode[node] = append(releaseEventsByNode[node], pin)
			if h := holders[node]; h != nil && h.PinID == strAny(pin.Body["claimid"]) {
				delete(holders, node)
				delete(passVotersByNode, node)
				releaseCounted[pin.PinID] = true
				nodeOutcome[node] = "released"
			}
		case "submission":
			node := strAny(pin.Body["node"])
			claimID := strAny(pin.Body["claimid"])
			if node == "" || claimID == "" {
				continue
			}
			holder := holders[node]
			if holder == nil || holder.PinID != claimID {
				continue // not the current cycle
			}
			cycle := getCycle(node, claimID)
			if cycle == nil {
				cycle = &V121Cycle{Node: node, ClaimID: claimID, Claimant: holder.Claimant, Superseded: map[string]bool{}, Outcome: "open"}
				cycleByNodeClaim[node+" "+claimID] = cycle
				cycles = append(cycles, cycle)
			}
			record := &V121CycleSubmission{
				PinID: pin.PinID, Author: pin.Author, AtMs: pin.TimestampMs, Height: pin.Height,
				SupersedeID: strAny(pin.Body["supersedeid"]), Body: pin.Body,
			}
			if cycle.Effective == nil {
				// v1.1 earliest-holds anchors the cycle.
				cycle.Submissions = append(cycle.Submissions, record)
				cycle.Effective = record
				nodeOfLiveTarget[record.PinID] = node
				submissionCycle[record.PinID] = cycle
				continue
			}
			if record.SupersedeID == "" {
				ignoredEvents = append(ignoredEvents, IgnoredEvent{record.PinID, "duplicate_without_supersede"})
				continue
			}
			// Supersede six predicates (paths.submission.supersedeRule).
			var target *V121CycleSubmission
			for _, s := range cycle.Submissions {
				if s.PinID == record.SupersedeID {
					target = s
					break
				}
			}
			chainOk := target != nil &&
				target.PinID != record.PinID &&
				target.PinID == cycle.Effective.PinID &&
				target.Author == record.Author &&
				record.Height >= hAct2 &&
				target.Height >= hAct2 &&
				!cycle.Superseded[record.SupersedeID]
			if chainOk {
				cycle.Superseded[record.SupersedeID] = true
				cycle.Submissions = append(cycle.Submissions, record)
				nodeOfLiveTarget[record.PinID] = node
				cycle.Effective = record
				submissionCycle[record.PinID] = cycle
			} else {
				ignoredEvents = append(ignoredEvents, IgnoredEvent{record.PinID, "supersede_predicate_failed"})
			}
		case "verify":
			vote := lastVoteByPin[pin.PinID]
			if vote == nil {
				continue
			}
			target := strAny(vote.Body["targetid"])
			node := nodeOfLiveTarget[target]
			if node == "" {
				continue // not a current-cycle effective submission
			}
			holder := holders[node]
			var cycle *V121Cycle
			if holder != nil {
				cycle = getCycle(node, holder.PinID)
			}
			if cycle == nil || cycle.Effective == nil || cycle.Effective.PinID != target {
				continue
			}
			// The ballot reached a live cycle-effective target at its walk
			// position (the frozen view calls this "counted-capable"; votes
			// that never reach — e.g. after a fail rejection — are facts only).
			reachedVote[vote.PinID] = true
			if strAny(vote.Body["verdict"]) == "fail" {
				// Ruling three: reopen now; identity is NOT filtered (reference
				// parity); a fail after provisional quorum undoes the verified
				// state (judgment is over all votes, not a race).
				cycle.Outcome = "fail_rejected"
				delete(verifiedSubmissionPins, target)
				delete(holders, node)
				delete(passVotersByNode, node)
				nodeOutcome[node] = "rejected"
				continue
			}
			if strAny(vote.Body["verdict"]) == "pass" {
				submitter := subAuthorByPin[target]
				if vote.Author == submitter || vote.Author == rootAuthor {
					continue // identity: never counts
				}
				voters := passVotersByNode[node]
				if voters == nil {
					voters = map[string]string{}
					passVotersByNode[node] = voters
				}
				if _, seen := voters[vote.Author]; !seen {
					voters[vote.Author] = vote.PinID
				}
				if len(voters) >= quorum {
					if cycle.QuorumAtMs == 0 {
						cycle.QuorumAtMs = vote.TimestampMs
					}
					cycle.Outcome = "verified"
					verifiedSubmissionPins[target] = true
				}
			}
		}
	}

	// A node's current cycle = its latest cycle in insertion order.
	activeCycleByNode := map[string]*V121Cycle{}
	for _, c := range cycles {
		activeCycleByNode[c.Node] = c
	}

	// VerifyDisposition: the per-pin ballot verdict for the frozen §5 view —
	// gate reasons, roster filtering, last-per-bot collapse, identity
	// conflicts, and non-active targets, all in one place. Purely additive
	// bookkeeping; it never feeds any hashed output.
	verifyDisposition := map[string]*V121VoteDisposition{}
	for target, pins := range verifyByTarget {
		submitter := subAuthorByPin[target]
		for _, v := range pins {
			d := &V121VoteDisposition{}
			verifyDisposition[v.PinID] = d
			if reason := v121GateReason(v.Body, v.Height, hAct); reason != "" {
				d.Reason = reason
				continue
			}
			if hAct2 != math.MaxInt64 && v.Height >= hAct2 && len(rosterGroups) > 0 &&
				(sameSide(rosterGroups, v.Author, submitter) || sameSide(rosterGroups, v.Author, rootAuthor)) {
				d.Reason = "same_side_roster"
				continue
			}
			if lastVotes[[2]string{target, v.Author}] != v {
				d.Reason = "duplicate_vote_superseded"
				continue
			}
			if !reachedVote[v.PinID] {
				d.Reason = "target_not_active"
				continue
			}
			// Surviving ballot that reached a live target: identity conflicts
			// apply to pass votes only (reference parity — a conflicted fail
			// still acts in the walk).
			switch strAny(v.Body["verdict"]) {
			case "pass":
				if v.Author == submitter {
					d.Reason = "voter_is_submitter"
					continue
				}
				if v.Author == rootAuthor {
					d.Reason = "voter_is_task_author"
					continue
				}
				d.Counts = true
			case "fail":
				d.Counts = true
			case "invalid":
				d.Reason = "verdict_invalid"
			default:
				d.Reason = "unknown_verdict"
			}
		}
	}

	// -- expiry pass (only when a clock is provided) ---------------------------
	if hasNow {
		for node, holder := range holders {
			cycle := cycleByNodeClaim[node+" "+holder.PinID]
			var effective *V121CycleSubmission
			if cycle != nil {
				effective = cycle.Effective
			}
			if effective == nil {
				if ttlHours > 0 && now-holder.SinceMs > int64(ttlHours)*3_600_000 {
					delete(holders, node)
					delete(passVotersByNode, node)
					nodeOutcome[node] = "expired_claim"
				}
			} else if windowHours > 0 {
				good := len(passVotersByNode[node])
				if good < quorum && now-effective.AtMs > int64(windowHours)*3_600_000 {
					delete(holders, node)
					delete(passVotersByNode, node)
					nodeOutcome[node] = "expired_review_window"
				}
			}
		}
	}

	// -- tree + amends ----------------------------------------------------------
	initialNodes := map[string]*V121TreeNode{}
	initialOrder := []string{}
	if treePin != nil {
		if rawNodes, ok := treePin.Body["nodes"].([]any); ok {
			for _, raw := range rawNodes {
				obj, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				id := strAny(obj["id"])
				if id == "" {
					continue
				}
				n := &V121TreeNode{ID: id, Params: map[string]any{}}
				if v, has := obj["parent"]; has && v != nil {
					n.Parent = strAny(v)
				}
				n.Title = strAny(obj["title"])
				n.Kind = strAny(obj["kind"])
				if v, has := obj["specid"]; has && v != nil {
					n.SpecID = strAny(v)
				}
				if p, isMap := obj["params"].(map[string]any); isMap {
					n.Params = p
				}
				if d, isArr := obj["deps"].([]any); isArr {
					for _, dv := range d {
						if s, isStr := dv.(string); isStr {
							n.Deps = append(n.Deps, s)
						}
					}
				}
				if w, has := obj["weight"]; has {
					n.Weight = w
				}
				if initialNodes[id] == nil {
					initialNodes[id] = n
					initialOrder = append(initialOrder, id)
				}
			}
		}
	}
	nodeIDSet := map[string]bool{}
	for id := range initialNodes {
		nodeIDSet[id] = true
	}
	for _, c := range cycles {
		nodeIDSet[c.Node] = true
	}
	for node := range holders {
		nodeIDSet[node] = true
	}
	verifiedNodeIds := map[string]bool{}
	for nodeID, cycle := range activeCycleByNode {
		if cycle.Effective != nil && verifiedSubmissionPins[cycle.Effective.PinID] {
			verifiedNodeIds[nodeID] = true
		}
	}

	rootVerifiedVoteLevel := false
	if treePin != nil && strAny(treePin.Body["root"]) != "" {
		rootVerifiedVoteLevel = verifiedNodeIds[strAny(treePin.Body["root"])]
	}
	amends := []*ChainEvent{}
	for _, p := range byPath["amend"] {
		if strAny(p.Body["taskid"]) == rootPin.PinID {
			amends = append(amends, p)
		}
	}
	sortChainEvents(amends)
	activeCycleNodes := map[string]bool{}
	for node := range holders {
		activeCycleNodes[node] = true
	}
	effectiveTree, treeOrder, amendIgnored, amendHead := foldAmends(
		amends, rootAuthor, treePinID, initialNodes, initialOrder,
		firstClaimOrder, verifiedNodeIds, activeCycleNodes, rootVerifiedVoteLevel, hAct2)
	ignoredEvents = append(ignoredEvents, amendIgnored...)
	for id := range effectiveTree {
		nodeIDSet[id] = true
	}

	// -- aggregation precondition (paths.aggregationPrecondition) --------------
	childrenOf := map[string][]string{}
	for _, id := range treeOrder {
		n := effectiveTree[id]
		if n == nil {
			continue
		}
		if p, isStr := n.Parent.(string); isStr {
			childrenOf[p] = append(childrenOf[p], n.ID)
		}
	}
	heightOfEffective := func(node string) int64 {
		cycle := activeCycleByNode[node]
		if cycle == nil || cycle.Effective == nil {
			return -1
		}
		return cycle.Effective.Height
	}
	finalVerifiedCache := map[string]bool{}
	var finalVerified func(node string) bool
	finalVerified = func(node string) bool {
		if v, ok := finalVerifiedCache[node]; ok {
			return v
		}
		finalVerifiedCache[node] = false // cycle guard
		result := false
		cycle := activeCycleByNode[node]
		if cycle != nil && cycle.Effective != nil && cycle.Outcome == "verified" {
			result = true
			children := childrenOf[node]
			if len(children) > 0 && heightOfEffective(node) >= hAct2 {
				childPins := map[string]bool{}
				allChildrenVerified := true
				for _, child := range children {
					if !finalVerified(child) {
						allChildrenVerified = false
						continue
					}
					cc := activeCycleByNode[child]
					if cc != nil && cc.Effective != nil {
						childPins[cc.Effective.PinID] = true
					}
				}
				var canonical []any
				body := subBodyByPin[cycle.Effective.PinID]
				if body != nil {
					if r, ok := body["result"].(map[string]any); ok {
						if cid, ok := r["childids"].([]any); ok {
							canonical = cid
						}
					}
					if canonical == nil {
						if cid, ok := body["childids"].([]any); ok {
							canonical = cid
						}
					}
				}
				listed := map[string]bool{}
				for _, idv := range canonical {
					if s, isStr := idv.(string); isStr {
						listed[s] = true
					}
				}
				result = allChildrenVerified &&
					len(listed) == len(childPins) &&
					func() bool {
						for pin := range childPins {
							if !listed[pin] {
								return false
							}
						}
						return true
					}()
			}
		}
		finalVerifiedCache[node] = result
		return result
	}
	finalVerifiedNodeIds := map[string]bool{}
	nodeOrder := make([]string, 0, len(nodeIDSet))
	for id := range nodeIDSet {
		nodeOrder = append(nodeOrder, id)
	}
	sort.SliceStable(nodeOrder, func(i, j int) bool {
		if len(nodeOrder[i]) != len(nodeOrder[j]) {
			return len(nodeOrder[i]) < len(nodeOrder[j])
		}
		return nodeOrder[i] < nodeOrder[j]
	})
	for _, node := range nodeOrder {
		if finalVerified(node) {
			finalVerifiedNodeIds[node] = true
		}
	}
	finalVerifiedPins := map[string]bool{}
	for node := range finalVerifiedNodeIds {
		if c := activeCycleByNode[node]; c != nil && c.Effective != nil {
			finalVerifiedPins[c.Effective.PinID] = true
		}
	}

	// -- challenges (H_ACT2): open = unwithdrawn, unexpired, not overturned ----
	type openChallenge struct {
		pinID, node, author, target string
	}
	openChallenges := map[string]*openChallenge{} // "target author" -> challenge
	if hAct2 != math.MaxInt64 {
		challenges := []*ChainEvent{}
		for _, p := range byPath["challenge"] {
			if knownTargets[strAny(p.Body["targetid"])] {
				challenges = append(challenges, p)
			}
		}
		for _, ch := range challenges {
			body := ch.Body
			target := strAny(body["targetid"])
			submitter := subAuthorByPin[target]
			if ch.Height < hAct2 {
				ignoredEvents = append(ignoredEvents, IgnoredEvent{ch.PinID, "below_h_act2"})
				continue
			}
			if ch.Author == submitter || ch.Author == rootAuthor || !truthyStrAny(body["evidence"]) {
				ignoredEvents = append(ignoredEvents, IgnoredEvent{ch.PinID, "challenge_gate_failed"})
				continue
			}
			if w, ok := body["withdraw"].(bool); ok && w {
				for key, open := range openChallenges {
					if open.author == ch.Author && open.target == target {
						delete(openChallenges, key)
					}
				}
				continue
			}
			key := target + " " + ch.Author
			if _, dup := openChallenges[key]; dup {
				ignoredEvents = append(ignoredEvents, IgnoredEvent{ch.PinID, "duplicate_open_challenge"})
				continue
			}
			var cycle *V121Cycle
			for _, c := range activeCycleByNode {
				if c.Effective != nil && c.Effective.PinID == target {
					cycle = c
					break
				}
			}
			if cycle == nil {
				ignoredEvents = append(ignoredEvents, IgnoredEvent{ch.PinID, "target_not_active_verified"})
				continue
			}
			if hasNow && ch.TimestampMs > 0 && now-ch.TimestampMs > challengeTTL*86_400_000 {
				ignoredEvents = append(ignoredEvents, IgnoredEvent{ch.PinID, "challenge_expired"})
				continue
			}
			openChallenges[key] = &openChallenge{pinID: ch.PinID, node: cycle.Node, author: ch.Author, target: target}
		}
	}
	// A challenge stands only while its target still anchors a FINAL-verified
	// node (closedLoop: overturned via the fail path = resolved, never revives).
	disputedNodeIds := map[string]bool{}
	for key, open := range openChallenges {
		cycle := activeCycleByNode[open.node]
		still := cycle != nil && cycle.Effective != nil && finalVerifiedPins[cycle.Effective.PinID]
		if still && cycle.Effective.PinID == open.target {
			disputedNodeIds[open.node] = true
		} else {
			delete(openChallenges, key)
		}
	}

	// -- node projections + participant stats ----------------------------------
	participants := map[string]*V121Participant{}
	bump := func(metaID string) *V121Participant {
		p := participants[metaID]
		if p == nil {
			p = &V121Participant{MetaID: metaID}
			participants[metaID] = p
		}
		return p
	}
	bump(rootAuthor)
	for author, count := range effectiveClaimCounts {
		bump(author).EffectiveClaims = count
	}

	progress := V121Progress{}
	nodeStates := map[string]*V121NodeState{}
	for _, node := range nodeOrder {
		treeRec := effectiveTree[node]
		cycle := activeCycleByNode[node]
		holder := holders[node]
		isVerified := finalVerifiedNodeIds[node]
		passVotes, failVotes := 0, 0
		var voteRows []VoteRecord
		if cycle != nil && cycle.Effective != nil {
			for _, v := range votesByTarget[cycle.Effective.PinID] {
				identityOk := v.Author != cycle.Effective.Author && v.Author != rootAuthor
				verdict := strAny(v.Body["verdict"])
				if verdict == "pass" && identityOk {
					passVotes++
				}
				if verdict == "fail" {
					failVotes++
				}
				voteRows = append(voteRows, VoteRecord{
					VerifyPinID: v.PinID, Voter: v.Author, Verdict: verdict,
					HasEvidence:      truthyStrAny(v.Body["evidence"]),
					HasFailreason:    truthyStrAny(v.Body["failreason"]),
					HasSemanticCheck: truthyStrAny(v.Body["semantic_check"]),
					BlockHeight:      v.Height, BlockTs: v.TimestampMs, txIdx: v.TxIndex,
				})
			}
		}
		status := "open"
		if isVerified {
			status = "verified"
		} else if holder != nil {
			status = "claimed"
		}
		disputed := disputedNodeIds[node]
		st := &V121NodeState{
			Node: node, Status: status, Disputed: disputed,
			PassVotes: passVotes, FailVotes: failVotes,
		}
		if treeRec != nil {
			st.InTree = true
			if w, ok := treeNodeWeight(treeRec); ok {
				wv := w
				st.Weight = &wv
			}
		}
		if holder != nil {
			st.Holder = holder
		}
		if cycle != nil {
			st.Cycle = cycle
		}
		cycleCount := 0
		for _, c := range cycles {
			if c.Node == node {
				cycleCount++
			}
		}
		st.CycleCount = cycleCount
		st.Votes = voteRows
		st.LastOutcome = nodeOutcome[node]
		if isVerified && cycle != nil {
			st.VerifiedAtMs = cycle.QuorumAtMs
		}
		nodeStates[node] = st
		progress.Total++
		if disputed {
			progress.Disputed++
		}
		if isVerified {
			progress.Verified++
		} else if holder != nil {
			progress.Claimed++
		} else {
			progress.Open++
		}
		if cycle != nil && cycle.Effective != nil {
			bump(cycle.Effective.Author).Submissions++
			if isVerified {
				bump(cycle.Effective.Author).VerifiedContrib++
			}
		}
	}

	// Review stats: last-valid votes on terminally-resolved cycles.
	for _, cycle := range cycles {
		if cycle.Outcome != "verified" && cycle.Outcome != "fail_rejected" {
			continue
		}
		if cycle.Effective == nil {
			continue
		}
		for _, v := range votesByTarget[cycle.Effective.PinID] {
			stats := bump(v.Author)
			stats.ReviewVotes++
			stats.ReviewTerminal++
			verdict := strAny(v.Body["verdict"])
			correct := (verdict == "pass" && cycle.Outcome == "verified") ||
				(verdict == "fail" && cycle.Outcome == "fail_rejected")
			if correct {
				stats.ReviewCorrect++
			}
		}
	}

	// Task completion = the ROOT node verified (v1.2.1 aggregation text).
	rootTreeNodeID := ""
	for _, id := range treeOrder {
		n := effectiveTree[id]
		if n == nil {
			continue
		}
		if _, isStr := n.Parent.(string); !isStr {
			rootTreeNodeID = n.ID
			break
		}
	}
	if rootTreeNodeID == "" && treePin != nil {
		rootTreeNodeID = strAny(treePin.Body["root"])
	}
	taskComplete := false
	if rootTreeNodeID != "" {
		taskComplete = finalVerifiedNodeIds[rootTreeNodeID]
	} else {
		taskComplete = progress.Total > 0 && progress.Verified == progress.Total
	}

	// -- eventSetHash (settlement.eventSetHash.recipe) ---------------------------
	specRefs := map[string]bool{}
	if specID != "" {
		specRefs[specID] = true
	}
	hashEntries := []any{}
	boundaryBlock := int64(-1)
	for _, path := range V121EventPaths {
		list := byPath[path]
		scoped := []*ChainEvent{}
		for _, pin := range list {
			if pin.Height < 0 {
				continue // confirmed only; mempool excluded
			}
			member := false
			switch path {
			case "task":
				member = pin.PinID == rootPin.PinID
			case "tree":
				member = pin.PinID == treePinID
			case "spec":
				member = specRefs[pin.PinID]
			case "verify", "challenge":
				member = knownTargets[strAny(pin.Body["targetid"])]
			default:
				member = strAny(pin.Body["taskid"]) == rootPin.PinID
			}
			if member {
				scoped = append(scoped, pin)
			}
		}
		sort.SliceStable(scoped, func(i, j int) bool {
			a, b := scoped[i], scoped[j]
			if a.Height != b.Height {
				return a.Height < b.Height
			}
			if a.TxIndex != b.TxIndex {
				return a.TxIndex < b.TxIndex
			}
			return a.PinID < b.PinID
		})
		for _, pin := range scoped {
			hashEntries = append(hashEntries, map[string]any{
				"path":    "/protocols/metatask/" + path,
				"pinId":   pin.PinID,
				"height":  pin.Height,
				"txIndex": pin.TxIndex,
			})
			if pin.Height > boundaryBlock {
				boundaryBlock = pin.Height
			}
		}
	}
	eventSetHashBytes, err := canonJ(hashEntries)
	if err != nil {
		return nil, err
	}
	eventSetHash := sha256Hex(eventSetHashBytes)

	// -- settlement manifest (v1.2) ---------------------------------------------
	var settlement *V121Manifest
	if taskComplete && len(openChallenges) == 0 {
		nodeCount := progress.Total
		weights := map[string]int64{}
		weightsValid := nodeCount > 0 && len(effectiveTree) == nodeCount
		if weightsValid {
			totalWeight := int64(0)
			for _, n := range effectiveTree {
				w, ok := treeNodeWeight(n)
				if !ok {
					weightsValid = false
					break
				}
				weights[n.ID] = w
				totalWeight += w
			}
			if totalWeight != TreeWeightTotal {
				weightsValid = false
			}
		}
		if !weightsValid {
			// Legacy fallback: uniform floor(10000/N), residue discarded
			// (settlement.legacyFallback — never routed to the root).
			uniform := int64(0)
			if nodeCount > 0 {
				uniform = TreeWeightTotal / int64(nodeCount)
			}
			for _, node := range nodeOrder {
				weights[node] = uniform
			}
		}
		sigma := SubmitterShareBPDefault
		if f, ok := numAny(split["submitterShareBP"]); ok {
			sigma = int64(f)
		}
		if sigma > SubmitterShareBPMax {
			sigma = SubmitterShareBPMax
		}
		if sigma < SubmitterShareBPMin {
			sigma = SubmitterShareBPMin
		}

		accuracy := map[string]int64{}
		for _, stats := range participants {
			smoothed := (10000 * (int64(stats.ReviewCorrect) + 1)) / (int64(stats.ReviewTerminal) + 2)
			a := smoothed
			if a < ReviewerAccuracyFloorBP {
				a = ReviewerAccuracyFloorBP
			}
			if a > 10000 {
				a = 10000
			}
			accuracy[stats.MetaID] = a
		}

		type shareParts struct{ submittedBP, reviewedBP int64 }
		sharePartsBy := map[string]*shareParts{}
		ensure := func(metaID string) *shareParts {
			p := sharePartsBy[metaID]
			if p == nil {
				p = &shareParts{}
				sharePartsBy[metaID] = p
			}
			return p
		}
		for _, node := range nodeOrder {
			cycle := activeCycleByNode[node]
			if cycle == nil || cycle.Effective == nil || !finalVerifiedPins[cycle.Effective.PinID] {
				continue
			}
			w := weights[node]
			if w <= 0 {
				continue
			}
			submitter := ensure(cycle.Effective.Author)
			subBP := (w * sigma) / TreeWeightTotal
			submitter.submittedBP += subBP
			pool := w - subBP // defined by subtraction: no double rounding
			var cycleVotes []*ChainEvent
			for _, v := range votesByTarget[cycle.Effective.PinID] {
				if strAny(v.Body["verdict"]) == "pass" && v.Author != cycle.Effective.Author && v.Author != rootAuthor {
					cycleVotes = append(cycleVotes, v)
				}
			}
			if len(cycleVotes) == 0 {
				submitter.submittedBP += pool // defensive: empty R(n) pool goes to the submitter
				continue
			}
			accSum := int64(0)
			for _, v := range cycleVotes {
				accSum += accuracy[v.Author]
			}
			for _, v := range cycleVotes {
				a := accuracy[v.Author]
				ensure(v.Author).reviewedBP += (pool * a) / accSum // residue discarded
			}
		}
		shares := []V121Share{}
		shareSum := int64(0)
		for metaID, parts := range sharePartsBy {
			shares = append(shares, V121Share{MetaID: metaID, ShareBP: parts.submittedBP + parts.reviewedBP, SubmittedBP: parts.submittedBP, ReviewedBP: parts.reviewedBP})
		}
		sort.SliceStable(shares, func(i, j int) bool {
			if shares[i].ShareBP != shares[j].ShareBP {
				return shares[i].ShareBP > shares[j].ShareBP
			}
			return shares[i].MetaID < shares[j].MetaID
		})
		for _, s := range shares {
			shareSum += s.ShareBP
		}

		unpaid := []V121Unpaid{}
		for _, cycle := range cycles {
			for _, s := range cycle.Submissions {
				if cycle.Superseded[s.PinID] {
					unpaid = append(unpaid, V121Unpaid{Node: cycle.Node, Author: s.Author, PinID: s.PinID, Reason: "superseded"})
					continue
				}
				if cycle.Effective != nil && cycle.Effective.PinID == s.PinID && cycle.Outcome == "fail_rejected" {
					unpaid = append(unpaid, V121Unpaid{Node: cycle.Node, Author: s.Author, PinID: s.PinID, Reason: "rework_cycle"})
				}
			}
		}

		weightsTable := []any{}
		weightIDs := make([]string, 0, len(weights))
		for id := range weights {
			weightIDs = append(weightIDs, id)
		}
		sort.Strings(weightIDs)
		for _, id := range weightIDs {
			weightsTable = append(weightsTable, map[string]any{"id": id, "weight": weights[id]})
		}
		wtBytes, err := canonJ(weightsTable)
		if err != nil {
			return nil, err
		}

		manifestMap := map[string]any{
			"taskid":            rootPin.PinID,
			"boundaryBlock":     boundaryBlock,
			"eventSetHash":      eventSetHash,
			"engineAlgoVersion": EngineAlgoVersionV121,
		}
		shareRows := []any{}
		for _, s := range shares {
			shareRows = append(shareRows, map[string]any{
				"metaId":  s.MetaID,
				"shareBP": s.ShareBP,
				"from":    map[string]any{"submittedBP": s.SubmittedBP, "reviewedBP": s.ReviewedBP},
			})
		}
		unpaidRows := []any{}
		for _, u := range unpaid {
			unpaidRows = append(unpaidRows, map[string]any{"node": u.Node, "author": u.Author, "pinId": u.PinID, "reason": u.Reason})
		}
		manifestMap["shares"] = shareRows
		manifestMap["unpaidHistory"] = unpaidRows
		manifestMap["disputed"] = []any{}
		manifestMap["weightsTableHash"] = sha256Hex(wtBytes)
		manifestBytes, err := canonJ(manifestMap)
		if err != nil {
			return nil, err
		}
		// Self-check invariant (settlement.manifest): shareBP = submittedBP + reviewedBP.
		for _, s := range shares {
			if s.ShareBP != s.SubmittedBP+s.ReviewedBP {
				return nil, fmt.Errorf("metatask v1.2.1 settlement self-check failed for %s", s.MetaID)
			}
		}
		settlement = &V121Manifest{
			TaskID: rootPin.PinID, BoundaryBlock: boundaryBlock, EventSetHash: eventSetHash,
			EngineAlgoVersion: EngineAlgoVersionV121, Shares: shares, UnpaidHistory: unpaid,
			WeightsTableHash: sha256Hex(wtBytes), ShareSumBP: shareSum, Canonical: manifestBytes,
		}
	}

	participantList := make([]V121Participant, 0, len(participants))
	for _, p := range participants {
		participantList = append(participantList, *p)
	}
	sort.SliceStable(participantList, func(i, j int) bool {
		if participantList[i].VerifiedContrib != participantList[j].VerifiedContrib {
			return participantList[i].VerifiedContrib > participantList[j].VerifiedContrib
		}
		return participantList[i].MetaID < participantList[j].MetaID
	})

	tags := []string{}
	if arr, ok := taskBody["tags"].([]any); ok {
		for _, tv := range arr {
			if s, isStr := tv.(string); isStr {
				tags = append(tags, s)
			}
		}
	}
	var rosterIDPtr *string
	if rosterIDStr != "" {
		rosterIDPtr = &rosterIDStr
	}

	ignoredByPin := map[string]string{}
	for _, ig := range ignoredEvents {
		if _, ok := ignoredByPin[ig.PinID]; !ok {
			ignoredByPin[ig.PinID] = ig.Reason
		}
	}

	return &V121Result{
		RootPinID: rootPin.PinID,
		Title:     strAny(taskBody["title"]),
		Brief:     strAny(taskBody["brief"]),
		Publisher: rootAuthor,
		Tags:      tags,
		RawPolicy: policy,
		Quorum:    quorum,
		TTLHours:  ttlHours, WindowHours: windowHours,
		ChallengeTTLDays:       challengeTTL,
		HasSplit:               hasSplit,
		RosterID:               rosterIDPtr,
		TreePinID:              treePinID,
		SpecID:                 specID,
		AmendHead:              amendHead,
		EffectiveTree:          effectiveTree,
		TreeOrder:              treeOrder,
		NodeOrder:              nodeOrder,
		NodeStates:             nodeStates,
		TaskComplete:           taskComplete,
		RootVerified:           taskComplete,
		Progress:               progress,
		Participants:           participantList,
		Settlement:             settlement,
		EventSetHash:           eventSetHash,
		BoundaryBlock:          boundaryBlock,
		IgnoredEvents:          ignoredEvents,
		ExpiryApplied:          hasNow,
		Cycles:                 cycles,
		ActiveCycleByNode:      activeCycleByNode,
		Holders:                holders,
		FirstClaimOrder:        firstClaimOrder,
		FinalVerified:          finalVerifiedNodeIds,
		FinalVerifiedPins:      finalVerifiedPins,
		DisputedNodes:          disputedNodeIds,
		VerifyByTarget:         verifyByTarget,
		LastVoteByTargetBot:    lastVotes,
		SubmissionEventsByNode: submissionEventsByNode,
		ClaimEventsByNode:      claimEventsByNode,
		ReleaseEventsByNode:    releaseEventsByNode,
		ClaimCounted:           claimCounted,
		ReleaseCounted:         releaseCounted,
		SubmissionCycle:        submissionCycle,
		NodeOutcome:            nodeOutcome,
		VerifyDisposition:      verifyDisposition,
		IgnoredByPin:           ignoredByPin,
		RootTreeNodeID:         rootTreeNodeID,
	}, nil
}
