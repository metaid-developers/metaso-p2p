package metatask

import (
	"fmt"
	"sort"
)

// voteInvalid ports metatask-replay v1.2.0's §3.7 ballot gate (revision items
// #8/#9). It returns the deterministic invalid reason, or "" for a valid ballot.
//
//	#9  a ballot without semantic_check does not count at all;
//	#8  a fail ballot without failreason is invalid (it neither occupies a fail
//	    slot nor triggers the send-back); evidence stopped being a requirement
//	    in v1.2.
//
// Ballots are filtered BEFORE the last-per-bot collapse, so a malformed ballot
// can never silently withdraw the same bot's earlier valid ballot.
func voteInvalid(b map[string]any) string {
	if !payloadPresent(b, "semantic_check") {
		return "missing_semantic_check"
	}
	if payloadString(b, "verdict") == "fail" && !payloadPresent(b, "failreason") {
		return "missing_failreason"
	}
	return ""
}

type holderRecord struct {
	Pin      string
	Claimant string
	T0       int64
	Inflight bool
}

type submissionRecord struct {
	Pin    string
	Author string
	Claim  string
	T      int64
	Body   map[string]any
}

type preparedEvent struct {
	pin     *Pin
	payload map[string]any
	state   string
}

// settlement is the internal prepared state shared by the main view and the
// guard view, mirroring the engine's two code paths on one event stream.
type settlement struct {
	root       string
	rootAuthor string
	rootPin    *Pin
	task       map[string]any
	policy     map[string]any
	quorum     int
	treeID     string
	treeNodes  []string

	claims    []*Pin
	releases  []*Pin
	subsPins  []*Pin
	verifies  []*Pin
	rejected  []*Pin
	rejectedN map[string]string

	holders map[string]*holderRecord // node -> holder
	subs    map[string]*submissionRecord
	last    map[[2]string]map[string]any // (target, bot) -> ballot payload
}

func newSettlement(events map[string][]*Pin, root string) (*settlement, error) {
	s := &settlement{
		root:      root,
		holders:   map[string]*holderRecord{},
		subs:      map[string]*submissionRecord{},
		last:      map[[2]string]map[string]any{},
		rejectedN: map[string]string{},
		claims:    sortedPins(events[PathClaim]),
		releases:  sortedPins(events[PathRelease]),
		subsPins:  sortedPins(events[PathSubmission]),
		verifies:  sortedPins(events[PathVerify]),
		rejected:  rejectedPins(events),
	}

	rootPin := findPin(events[PathTask], root)
	if rootPin == nil {
		return nil, fmt.Errorf("task root %s not found in the enumerated task pool", root)
	}
	s.rootPin = rootPin
	s.rootAuthor = rootPin.GlobalMetaID
	v := resolvePayload(rootPin)
	s.task = v.Payload
	if !v.Accepted() {
		return nil, fmt.Errorf("task root payload rejected: %s", v.Reason)
	}
	s.policy = map[string]any{}
	if p, ok := s.task["policy"].(map[string]any); ok {
		s.policy = p
	}
	s.quorum = policyInt(s.policy, "verify_quorum", 2)
	s.treeID = payloadString(s.task, "treeid")
	if s.treeID != "" {
		if tp := findPin(events[PathTree], s.treeID); tp != nil {
			if tv := resolvePayload(tp); tv.Accepted() {
				s.treeNodes = treeNodeIDs(tv.Payload)
			}
		}
	}
	s.walkClaims()
	s.walkSubmissions()
	s.collectBallots()
	return s, nil
}

func policyInt(policy map[string]any, key string, def int) int {
	if f, ok := payloadNumber(policy, key); ok {
		return int(f)
	}
	return def
}

func sortedPins(pins []*Pin) []*Pin {
	out := make([]*Pin, 0, len(pins))
	for _, p := range pins {
		if v := resolvePayload(p); v.Accepted() {
			out = append(out, p)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return orderLess(out[i], out[j]) })
	return out
}

func rejectedPins(events map[string][]*Pin) []*Pin {
	var out []*Pin
	for _, path := range EventPaths {
		for _, p := range events[path] {
			if v := resolvePayload(p); !v.Accepted() {
				out = append(out, p)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return orderLess(out[i], out[j]) })
	return out
}

func findPin(pins []*Pin, id string) *Pin {
	for _, p := range pins {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func treeNodeIDs(payload map[string]any) []string {
	nodes, ok := payload["nodes"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(nodes))
	seen := map[string]bool{}
	for _, n := range nodes {
		obj, ok := n.(map[string]any)
		if !ok {
			continue
		}
		id := payloadString(obj, "id")
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// walkClaims ports the §4.1 claim/release timeline: claims and releases are
// interleaved in one global (height, tx) order, the first claim on a free node
// holds the lock, a release removes exactly the claim it names, and a claim
// that was ignored while the lock was held never revives (§3.5).
func (s *settlement) walkClaims() {
	timeline := make([]*Pin, 0, len(s.claims)+len(s.releases))
	for _, p := range s.claims {
		if payloadString(resolvePayload(p).Payload, "taskid") == s.root {
			timeline = append(timeline, p)
		}
	}
	for _, p := range s.releases {
		if payloadString(resolvePayload(p).Payload, "taskid") == s.root {
			timeline = append(timeline, p)
		}
	}
	sort.SliceStable(timeline, func(i, j int) bool { return orderLess(timeline[i], timeline[j]) })
	for _, p := range timeline {
		body := resolvePayload(p).Payload
		node := payloadString(body, "node")
		if node == "" {
			continue
		}
		if p.Path == PathClaim {
			if _, held := s.holders[node]; !held {
				s.holders[node] = &holderRecord{Pin: p.ID, Claimant: p.GlobalMetaID, T0: pinTimestamp(p)}
			}
			continue
		}
		if h := s.holders[node]; h != nil && h.Pin == payloadString(body, "claimid") {
			delete(s.holders, node)
		}
	}
}

// walkSubmissions ports §4.6: the earliest submission under the current holder
// claim is effective; a submission riding any other claim is ignored.
func (s *settlement) walkSubmissions() {
	for _, p := range s.subsPins {
		body := resolvePayload(p).Payload
		if payloadString(body, "taskid") != s.root {
			continue
		}
		node := payloadString(body, "node")
		claimID := payloadString(body, "claimid")
		h := s.holders[node]
		if h == nil || h.Pin != claimID {
			continue
		}
		if _, ok := s.subs[node]; ok {
			continue
		}
		s.subs[node] = &submissionRecord{Pin: p.ID, Author: p.GlobalMetaID, Claim: claimID, T: pinTimestamp(p), Body: body}
	}
}

// collectBallots ports the v1.2.0 ballot collapse: invalid ballots are dropped
// first, then the last valid ballot per (target, bot) survives. The collapse is
// pool-wide, which is what the engine's `verify_votes` counter reports.
func (s *settlement) collectBallots() {
	for _, p := range s.verifies {
		body := resolvePayload(p).Payload
		target := payloadString(body, "targetid")
		bot := p.GlobalMetaID
		if target == "" || bot == "" || voteInvalid(body) != "" {
			continue
		}
		s.last[[2]string{target, bot}] = body
	}
}

// verifiedSub ports verified_sub(): a node is verified when its effective
// submission carries at least quorum valid passes from bots other than the
// submitter and the task author, and no valid fail.
func (s *settlement) verifiedSub(node string) (*submissionRecord, int, int) {
	sub := s.subs[node]
	if sub == nil {
		return nil, 0, 0
	}
	good, bad := 0, 0
	for key, body := range s.last {
		if key[0] != sub.Pin {
			continue
		}
		bot := key[1]
		switch payloadString(body, "verdict") {
		case "pass":
			if bot != sub.Author && bot != s.rootAuthor {
				good++
			}
		case "fail":
			bad++
		}
	}
	return sub, good, bad
}

// nodeIDSet is the engine's node universe: declared tree nodes plus every node
// that carries a claim or a submission.
func (s *settlement) nodeIDSet() []string {
	set := map[string]bool{}
	for _, n := range s.treeNodes {
		set[n] = true
	}
	for n := range s.holders {
		set[n] = true
	}
	for n := range s.subs {
		set[n] = true
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	// The engine orders nodes by (len, lexicographic); keep the same order so
	// comparison tables line up row for row.
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return out[i] < out[j]
	})
	return out
}

// guardState ports guard_state() (§4.7 claimPrecheck): the same settlement plus
// §4.2 claimTTL and §4.3 reviewWindow expiry, and the conservative in-flight
// rule for unconfirmed claims.
func (s *settlement) guardState(node string, now int64, ttlHours, windowHours int) (string, string) {
	holder := (*holderRecord)(nil)
	inflight := false
	claimsForNode := make([]*Pin, 0)
	for _, p := range s.claims {
		if payloadString(resolvePayload(p).Payload, "taskid") != s.root || payloadString(resolvePayload(p).Payload, "node") != node {
			continue
		}
		claimsForNode = append(claimsForNode, p)
	}
	rels := make([]*Pin, 0)
	for _, p := range s.releases {
		if payloadString(resolvePayload(p).Payload, "taskid") != s.root || payloadString(resolvePayload(p).Payload, "node") != node {
			continue
		}
		rels = append(rels, p)
	}
	timeline := append(append([]*Pin{}, claimsForNode...), rels...)
	sort.SliceStable(timeline, func(i, j int) bool { return orderLess(timeline[i], timeline[j]) })
	for _, p := range timeline {
		body := resolvePayload(p).Payload
		if p.Path == PathClaim {
			if holder == nil {
				holder = &holderRecord{Pin: p.ID, Claimant: p.GlobalMetaID, T0: pinTimestamp(p), Inflight: p.GenesisHeight < 0}
				if holder.Inflight {
					inflight = true
				}
			}
			continue
		}
		if holder != nil && holder.Pin == payloadString(body, "claimid") {
			holder = nil
		}
	}

	var sub *submissionRecord
	if holder != nil {
		for _, p := range s.subsPins {
			body := resolvePayload(p).Payload
			if payloadString(body, "taskid") != s.root || payloadString(body, "node") != node {
				continue
			}
			if payloadString(body, "claimid") == holder.Pin {
				sub = &submissionRecord{Pin: p.ID, Author: p.GlobalMetaID, Claim: holder.Pin, T: pinTimestamp(p), Body: body}
				break
			}
		}
	}

	good, bad := 0, 0
	if sub != nil {
		lastValid := map[string]map[string]any{}
		for _, p := range s.verifies {
			body := resolvePayload(p).Payload
			if payloadString(body, "targetid") != sub.Pin {
				continue
			}
			if voteInvalid(body) != "" {
				continue
			}
			if bot := p.GlobalMetaID; bot != "" {
				lastValid[bot] = body
			}
		}
		for bot, body := range lastValid {
			switch payloadString(body, "verdict") {
			case "pass":
				if bot != sub.Author && bot != s.rootAuthor {
					good++
				}
			case "fail":
				bad++
			}
		}
	}

	state := "open"
	switch {
	case inflight && holder != nil:
		state = "claimed"
	case sub != nil && good >= s.quorum && bad == 0:
		state = "verified"
	case sub != nil && bad > 0:
		state = "open"
	case holder != nil && sub == nil && ttlHours > 0 && now-int64(holder.T0) > int64(ttlHours)*3600:
		state = "open"
	case sub != nil && windowHours > 0 && now-sub.T > int64(windowHours)*3600 && good < s.quorum:
		state = "open"
	case holder != nil:
		state = "claimed"
	}
	reason := ""
	if state != "open" {
		reason = "claim-rejected:" + node + ":" + state
	}
	return state, reason
}

// policyTTL reads §4.2 claim TTL hours from the task policy.
func (s *settlement) policyTTL() int { return policyInt(s.policy, "claim_ttl_hours", 0) }

// policyWindow reads §4.3 review window hours from the task policy.
func (s *settlement) policyWindow() int { return policyInt(s.policy, "verify_window_hours", 0) }

// nodeVotes returns the ballots (valid and invalid) targeted at one submission.
func (s *settlement) nodeVotes(target string) (valid []Vote, invalid []Vote, passRaw, failRaw int) {
	for _, p := range s.verifies {
		body := resolvePayload(p).Payload
		if payloadString(body, "targetid") != target {
			continue
		}
		verdict := payloadString(body, "verdict")
		v := Vote{
			Pin:           p.ID,
			Bot:           p.GlobalMetaID,
			Verdict:       verdict,
			HasSub:        payloadPresent(body, "semantic_check"),
			HasFailReason: payloadPresent(body, "failreason"),
			SubType:       payloadTypeName(body, "semantic_check"),
			Height:        p.GenesisHeight,
			TxIndex:       p.TxIndex,
			Timestamp:     pinTimestamp(p),
			PayloadState:  resolvePayload(p).State,
		}
		switch verdict {
		case "pass":
			passRaw++
		case "fail":
			failRaw++
		}
		if reason := voteInvalid(body); reason != "" {
			v.Valid = false
			v.InvalidReason = reason
			invalid = append(invalid, v)
			continue
		}
		v.Valid = true
		valid = append(valid, v)
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Pin < valid[j].Pin })
	sort.SliceStable(invalid, func(i, j int) bool { return invalid[i].Pin < invalid[j].Pin })
	return valid, invalid, passRaw, failRaw
}

// lastValidByBot applies the v1.2.0 collapse to one target's valid ballots.
func lastValidByBot(valid []Vote) map[string]Vote {
	out := map[string]Vote{}
	for _, v := range valid {
		out[v.Bot] = v
	}
	return out
}
