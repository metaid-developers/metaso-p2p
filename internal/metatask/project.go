package metatask

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Freeze §5/§6/§7 projections: taskPanorama, taskIndex, node events, plus the
// run-level replayMeta and the D7 canonical JSON writer. Field names here ARE
// the contract (freeze: 字段即契约, 只加不改).

// ReplayMeta is the run-level replay metadata (freeze §6 stats; semanticGate
// and the ruling-pin chain from v1.1 §0/§3.1).
type ReplayMeta struct {
	ReplayAlgoVersion  string         `json:"replayAlgoVersion"`
	ProtocolVersion    string         `json:"protocolVersion"`
	ProtocolPinID      string         `json:"protocolPinId"`
	EvaluatedAtBlock   int64          `json:"evaluatedAtBlock"`
	EvaluatedAtBlockTs int64          `json:"evaluatedAtBlockTs"`
	Cursor             ReplayCursor   `json:"cursor"`
	EventCount         int            `json:"eventCount"`
	EventCountByPath   map[string]int `json:"eventCountByPath"`
	Coverage           ReplayCoverage `json:"coverage"`
	SemanticGate       SemanticGate   `json:"semanticGate"`
	Freshness          Freshness      `json:"freshness"`
}

// ReplayCursor is the ≺ key of the last consumed event (freeze D5).
type ReplayCursor struct {
	BlockHeight int64 `json:"blockHeight"`
	TxIndex     int64 `json:"txIndex"`
	// OutputIndex is not exposed by the list API; -1 marks it unavailable and
	// the pinId takes the final ordering slot (replay.go order note).
	OutputIndex int64 `json:"outputIndex"`
	Version     int64 `json:"version"`
	LastBlockTs int64 `json:"lastBlockTs"`
}

// ReplayCoverage carries the never-silent completeness report (freeze §7;
// unresolvedCount is the A2-8 fourth subfield, added with the freeze
// clarification increment).
type ReplayCoverage struct {
	PerPath         map[string]string `json:"perPath"` // complete|partial per path
	OrderFallback   bool              `json:"orderFallback"`
	VersionEnum     string            `json:"versionEnum"`
	UnresolvedCount int               `json:"unresolvedCount"`
}

// SemanticGate echoes the frozen dual-track switch (D10).
type SemanticGate struct {
	HAct       int64    `json:"hAct"`
	RulingPins []string `json:"rulingPins"`
}

// Freshness is the interface SLO the acceptance C6 reads: chain-head minus
// evaluatedAtBlock must stay within MaxLagBlocks (v1.2 froze N=5; a constant,
// never per-request configurable; second-scale calibration is deployment-side
// per R7).
type Freshness struct {
	MaxLagBlocks int64 `json:"maxLagBlocks"`
}

// RunScope is one run's admitted event set (freeze collection boundary
// applied: dedup, confirmed only, payload-accepted only, to-block filter).
type RunScope struct {
	Admitted map[string][]*replayEvent
	Mempool  int
	Rejected int
	// UnresolvedCount is set by the driver after replaying all roots: the
	// run-level count of verify votes whose targetid matches no submission
	// event at all (A2-8; per-root details live in each ReplayResult).
	UnresolvedCount int
}

// ScopeEvents applies the collection boundary to an enumerated event set.
func ScopeEvents(events map[string][]*Pin, boundary Boundary) *RunScope {
	admitted, mempool, rejected := admitEvents(events, boundary)
	return &RunScope{Admitted: admitted, Mempool: mempool, Rejected: rejected}
}

// SumUnresolved aggregates the per-root unresolved vote counts into the
// run-level figure for coverage.unresolvedCount (A2-8). Per-root scoping in
// Replay keeps the sets disjoint, so a plain sum never double-counts.
func SumUnresolved(results map[string]*ReplayResult) int {
	n := 0
	for _, res := range results {
		n += len(res.Unresolved)
	}
	return n
}

// TaskRoots lists the task roots present in the scope, sorted.
func (s *RunScope) TaskRoots() []string {
	out := make([]string, 0, len(s.Admitted[PathTask]))
	for _, ev := range s.Admitted[PathTask] {
		out = append(out, ev.pin.ID)
	}
	sort.Strings(out)
	return out
}

// BuildReplayMeta assembles the run-level replayMeta from the scope and the
// enumeration report.
func BuildReplayMeta(scope *RunScope, enum Enumeration, boundary Boundary) ReplayMeta {
	m := ReplayMeta{
		ReplayAlgoVersion:  ReplayAlgoVersion,
		ProtocolVersion:    ProtocolVersion,
		ProtocolPinID:      ProtocolPinID,
		EvaluatedAtBlock:   boundary.Height,
		EvaluatedAtBlockTs: boundary.BlockTs,
		EventCountByPath:   map[string]int{},
		Coverage: ReplayCoverage{
			PerPath:         map[string]string{},
			VersionEnum:     "by_path",
			UnresolvedCount: scope.UnresolvedCount,
		},
		SemanticGate: SemanticGate{HAct: HAct, RulingPins: append([]string{}, SemanticGateRulingPins...)},
		Freshness:    Freshness{MaxLagBlocks: 5},
	}
	var cur *replayEvent
	for _, path := range EventPaths {
		m.EventCountByPath[path] = len(scope.Admitted[path])
		m.EventCount += len(scope.Admitted[path])
		for _, ev := range scope.Admitted[path] {
			if cur == nil || replayLess(cur, ev) {
				cur = ev
			}
		}
	}
	if cur != nil {
		m.Cursor = ReplayCursor{
			BlockHeight: cur.pin.GenesisHeight,
			TxIndex:     cur.pin.TxIndex,
			OutputIndex: -1,
			Version:     cur.pin.Version,
			LastBlockTs: cur.ts,
		}
	}
	for _, path := range EventPaths {
		complete := false
		if rep, ok := enum.Paths[path]; ok {
			complete = rep.Complete
		}
		m.Coverage.PerPath[path] = map[bool]string{true: "complete", false: "partial"}[complete]
	}
	return m
}

// ParticipantStat aggregates one bot's role counts for a task.
type ParticipantStat struct {
	MetaID          string `json:"metaId"`
	Claimed         int    `json:"claimed"`
	Submitted       int    `json:"submitted"`
	VerifiedContrib int    `json:"verifiedContrib"`
	Reviews         int    `json:"reviews"`
}

func participantsOf(root string, res *ReplayResult, scope *RunScope) []ParticipantStat {
	agg := map[string]*ParticipantStat{}
	get := func(id string) *ParticipantStat {
		if p, ok := agg[id]; ok {
			return p
		}
		p := &ParticipantStat{MetaID: id}
		agg[id] = p
		return p
	}
	for _, ev := range scope.Admitted[PathClaim] {
		if payloadString(ev.body, "taskid") == root {
			get(ev.pin.GlobalMetaID).Claimed++
		}
	}
	for _, ev := range scope.Admitted[PathSubmission] {
		if payloadString(ev.body, "taskid") == root {
			get(ev.pin.GlobalMetaID).Submitted++
		}
	}
	for _, id := range res.NodeOrder {
		n := res.Nodes[id]
		// §6 verifiedContrib counts submissions whose node is finally
		// verified (the aggregation-gated machine state), not merely
		// quorum-satisfied — an agg-blocked parent's submission is not a
		// verified one.
		if n.MachineState == StateVerified && n.ActiveSubmission != nil {
			get(n.ActiveSubmission.Submitter).VerifiedContrib++
		}
		for i := range n.Votes {
			if n.Votes[i].Counts {
				get(n.Votes[i].Voter).Reviews++
			}
		}
	}
	out := make([]ParticipantStat, 0, len(agg))
	for _, p := range agg {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MetaID < out[j].MetaID })
	return out
}

// TaskRow is one global task list row (freeze §6; v1.2 freezes
// participants as a metaId list).
type TaskRow struct {
	RootPinID         string       `json:"rootPinId"`
	Title             string       `json:"title"`
	NodeProgress      NodeProgress `json:"nodeProgress"`
	Publisher         string       `json:"publisher"`
	ParticipantsCount int          `json:"participantsCount"`
	Participants      []string     `json:"participants"`
	TaskState         string       `json:"taskState"`
	LastActivity      LastActivity `json:"lastActivity"`
}

// NodeProgress counts tree-declared nodes only.
type NodeProgress struct {
	Verified int `json:"verified"`
	Total    int `json:"total"`
}

// LastActivity is the 最近动静 provenance triple (freeze §6). The unexported
// order key (A4-3) decides same-block ties by the full ≺ order.
type LastActivity struct {
	BlockTs    int64  `json:"blockTs"`
	EventPinID string `json:"eventPinId"`
	Path       string `json:"path"`
	h          int64
	tx         int64
	ver        int64
}

type TaskStats struct {
	TaskTotal            int            `json:"taskTotal"`
	TaskActive           int            `json:"taskActive"`
	TaskDone             int            `json:"taskDone"`
	ParticipantsDistinct int            `json:"participantsDistinct"`
	NodesByState         map[string]int `json:"nodesByState"`
}

// Accuracy keeps reviewAccuracy in two integers (freeze §6, no floats).
type Accuracy struct {
	Num int `json:"num"`
	Den int `json:"den"`
}

type LeaderboardRow struct {
	MetaID         string   `json:"metaId"`
	Contribution   int      `json:"contribution"`
	ReviewScore    int      `json:"reviewScore"`
	ReviewAccuracy Accuracy `json:"reviewAccuracy"`
}

// TaskIndex is projection two: rows + stats + leaderboard (freeze §6).
type TaskIndex struct {
	Rows        []TaskRow        `json:"rows"`
	Stats       TaskStats        `json:"stats"`
	Leaderboard []LeaderboardRow `json:"leaderboard"`
	ReplayMeta  ReplayMeta       `json:"replayMeta"`
}

// BuildTaskIndex assembles the cross-task projection from per-root replay
// results and the run scope. Rows sort by lastActivity desc, ties by
// rootPinId asc (freeze §6 ordering contract).
func BuildTaskIndex(results map[string]*ReplayResult, scope *RunScope, meta ReplayMeta) TaskIndex {
	idx := TaskIndex{
		ReplayMeta: meta,
		// A2-3: a present set field carries explicit zero keys — claimed and
		// rejected appear even when no node sits in those display states.
		Stats: TaskStats{NodesByState: map[string]int{
			"open": 0, "claimed": 0, "submitted": 0, "verified": 0, "rejected": 0, "expired": 0,
		}},
	}
	participantsAll := map[string]bool{}
	for root, res := range results {
		parts := participantsOf(root, res, scope)
		for _, p := range parts {
			participantsAll[p.MetaID] = true
		}
		row := TaskRow{
			RootPinID:         root,
			Title:             res.TaskTitle,
			NodeProgress:      NodeProgress{Verified: countTreeNodes(res, StateVerified), Total: countTreeNodes(res, "")},
			Publisher:         res.Publisher,
			ParticipantsCount: len(parts),
			Participants:      participantIDs(parts),
			TaskState:         taskStateOf(res.RootVerified),
			LastActivity:      lastActivityOf(res),
		}
		idx.Rows = append(idx.Rows, row)
		for _, id := range res.NodeOrder {
			idx.Stats.NodesByState[res.Nodes[id].DisplayState]++
		}
		if res.RootVerified {
			idx.Stats.TaskDone++
		} else {
			idx.Stats.TaskActive++
		}
	}
	idx.Stats.TaskTotal = len(results)
	idx.Stats.ParticipantsDistinct = len(participantsAll)
	sort.Slice(idx.Rows, func(i, j int) bool {
		a, b := idx.Rows[i].LastActivity.BlockTs, idx.Rows[j].LastActivity.BlockTs
		if a != b {
			return a > b
		}
		return idx.Rows[i].RootPinID < idx.Rows[j].RootPinID
	})
	idx.Leaderboard = buildLeaderboard(results)
	return idx
}

// participantIDs flattens the participant aggregates to the frozen metaId
// list (v1.2 rows.participants).
func participantIDs(parts []ParticipantStat) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.MetaID)
	}
	return out
}

func countTreeNodes(res *ReplayResult, state string) int {
	n := 0
	for _, id := range res.NodeOrder {
		node := res.Nodes[id]
		if !node.InTree {
			continue
		}
		if state == "" || node.MachineState == state {
			n++
		}
	}
	return n
}

func taskStateOf(rootVerified bool) string {
	if rootVerified {
		return "done"
	}
	return "active"
}

// lastActivityOf picks the ≺-max in-scope event of a task (A4-3: the full
// order decides same-block ties — never arrival or collection order) across
// node event streams, votes and unresolved records.
func lastActivityOf(res *ReplayResult) LastActivity {
	best := LastActivity{}
	better := func(h, tx, ver int64, ts int64, pinID, path string) {
		if best.EventPinID == "" || activityOrderLess(best.h, best.tx, best.ver, best.EventPinID, h, tx, ver, pinID) {
			best = LastActivity{BlockTs: ts, EventPinID: pinID, Path: path, h: h, tx: tx, ver: ver}
		}
	}
	for _, id := range res.NodeOrder {
		n := res.Nodes[id]
		for _, e := range n.Events {
			better(e.BlockHeight, e.txIdx, e.Version, e.BlockTs, e.EventPinID, e.Path)
		}
		for _, v := range n.Votes {
			better(v.BlockHeight, v.txIdx, v.ver, v.BlockTs, v.VerifyPinID, PathVerify)
		}
	}
	for _, e := range res.Unresolved {
		better(e.BlockHeight, e.txIdx, e.Version, e.BlockTs, e.EventPinID, e.Path)
	}
	return best
}

// activityOrderLess is the frozen §2 ≺ tuple comparison (height, txIndex,
// version, pinId) over the unexported ordering keys — named apart from the
// pin-level orderLess in types.go.
func activityOrderLess(h1, tx1, v1 int64, p1 string, h2, tx2, v2 int64, p2 string) bool {
	if h1 != h2 {
		return h1 < h2
	}
	if tx1 != tx2 {
		return tx1 < tx2
	}
	if v1 != v2 {
		return v1 < v2
	}
	return p1 < p2
}

func buildLeaderboard(results map[string]*ReplayResult) []LeaderboardRow {
	agg := map[string]*LeaderboardRow{}
	get := func(id string) *LeaderboardRow {
		if r, ok := agg[id]; ok {
			return r
		}
		r := &LeaderboardRow{MetaID: id}
		agg[id] = r
		return r
	}
	for _, res := range results {
		for _, id := range res.NodeOrder {
			n := res.Nodes[id]
			verified := n.MachineState == StateVerified
			// §6 contribution counts finally-verified submissions: the
			// machine state is the aggregation-gated verdict, so a merely
			// quorum-satisfied (agg-blocked) parent does not contribute.
			if verified && n.ActiveSubmission != nil {
				get(n.ActiveSubmission.Submitter).Contribution++
			}
			for i := range n.Votes {
				v := &n.Votes[i]
				if !v.Counts {
					continue
				}
				row := get(v.Voter)
				row.ReviewScore++
				row.ReviewAccuracy.Den++
				switch {
				case v.Verdict == "pass" && verified:
					row.ReviewAccuracy.Num++
				case v.Verdict == "fail" && !verified:
					row.ReviewAccuracy.Num++
				}
			}
		}
	}
	out := make([]LeaderboardRow, 0, len(agg))
	for _, r := range agg {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Contribution != out[j].Contribution {
			return out[i].Contribution > out[j].Contribution
		}
		if out[i].ReviewScore != out[j].ReviewScore {
			return out[i].ReviewScore > out[j].ReviewScore
		}
		return out[i].MetaID < out[j].MetaID
	})
	return out
}

// SpecView is the freeze §5 spec object (验证器直链用).
type SpecView struct {
	Name              string `json:"name"`
	Lang              string `json:"lang"`
	Entry             string `json:"entry"`
	ScriptOrScriptPin string `json:"scriptOrScriptPin"`
	Input             string `json:"input"`
	Output            string `json:"output"`
}

// ProgressCounts uses the frozen five buckets (freeze §5; claimed/submitted
// nodes sit outside these buckets, total covers all nodes).
type ProgressCounts struct {
	Total    int `json:"total"`
	Verified int `json:"verified"`
	Open     int `json:"open"`
	Rejected int `json:"rejected"`
	Expired  int `json:"expired"`
}

// Panorama is projection one: single-request task full view (freeze §5).
type Panorama struct {
	RootPinID    string            `json:"rootPinId"`
	Title        string            `json:"title"`
	Brief        string            `json:"brief,omitempty"`
	Publisher    string            `json:"publisher"`
	Tags         []string          `json:"tags,omitempty"`
	TreeID       string            `json:"treeid,omitempty"`
	SpecID       string            `json:"specid,omitempty"`
	Spec         *SpecView         `json:"spec"`
	Policy       map[string]any    `json:"policy"`
	Nodes        []NodeReplay      `json:"nodes"`
	Progress     ProgressCounts    `json:"progress"`
	TaskState    string            `json:"taskState"`
	Participants []ParticipantStat `json:"participants"`
	ReplayMeta   ReplayMeta        `json:"replayMeta"`
	DiscoveredBy []string          `json:"discoveredBy"`
}

// BuildPanorama assembles the task full view for one replay result.
func BuildPanorama(res *ReplayResult, scope *RunScope, meta ReplayMeta) Panorama {
	p := Panorama{
		RootPinID:    res.RootPinID,
		Title:        res.TaskTitle,
		Publisher:    res.Publisher,
		TreeID:       res.TreeID,
		SpecID:       res.SpecID,
		Policy:       res.Policy,
		TaskState:    taskStateOf(res.RootVerified),
		Nodes:        make([]NodeReplay, 0, len(res.NodeOrder)),
		ReplayMeta:   meta,
		DiscoveredBy: []string{"index"},
	}
	for _, id := range res.NodeOrder {
		p.Nodes = append(p.Nodes, *res.Nodes[id])
	}
	p.Progress = ProgressCounts{Total: len(res.Nodes)}
	for _, id := range res.NodeOrder {
		switch res.Nodes[id].DisplayState {
		case "verified":
			p.Progress.Verified++
		case "open":
			p.Progress.Open++
		case "rejected":
			p.Progress.Rejected++
		case "expired":
			p.Progress.Expired++
		}
	}
	p.Participants = participantsOf(res.RootPinID, res, scope)
	for _, ev := range scope.Admitted[PathTask] {
		if ev.pin.ID != res.RootPinID {
			continue
		}
		p.Brief = payloadString(ev.body, "brief")
		p.Tags = payloadStrings(ev.body, "tags")
		break
	}
	for _, ev := range scope.Admitted[PathSpec] {
		if ev.pin.ID != res.SpecID {
			continue
		}
		p.Spec = &SpecView{
			Name:              payloadString(ev.body, "name"),
			Lang:              payloadString(ev.body, "lang"),
			Entry:             payloadString(ev.body, "entry"),
			ScriptOrScriptPin: payloadString(ev.body, "script"),
			Input:             payloadString(ev.body, "input"),
			Output:            payloadString(ev.body, "output"),
		}
		break
	}
	return p
}

// NodeEvents is the node-detail payload (freeze §7: {node, events[]}).
type NodeEvents struct {
	Node   *NodeReplay   `json:"node"`
	Events []EventRecord `json:"events"`
}

// BuildNodeEvents returns one node's detail with its full event stream.
func BuildNodeEvents(res *ReplayResult, nodeID string) (*NodeEvents, error) {
	n, ok := res.Nodes[nodeID]
	if !ok {
		return nil, fmt.Errorf("node %s not found in task %s", nodeID, res.RootPinID)
	}
	return &NodeEvents{Node: n, Events: n.Events}, nil
}

// CanonicalJSON is the D7 canonical serialization: compact separators, map
// keys sorted (encoding/json), HTML escaping off (python ensure_ascii=False
// parity), trailing newline trimmed. Two implementations "same conclusion"
// ⇔ byte-equal canonical output.
func CanonicalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
