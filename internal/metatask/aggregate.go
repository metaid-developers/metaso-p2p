package metatask

import (
	"sort"
	"time"
)

// Options configures one aggregation run.
type Options struct {
	// Root is the task root pinId being aggregated.
	Root string
	// Now is the clock used by the §4.7 guard view (EffectiveStatus) and
	// reported in Meta.Now. It never affects Status, which is the pure
	// final-state replay.
	Now int64
	// GeneratedAt is recorded in Meta for provenance.
	GeneratedAt time.Time
	// Enumeration is the walk report of the same pin stream.
	Enumeration Enumeration
}

// BuildAggregate builds the pool read path for one task root from an already
// enumerated pin stream. The settlement is deterministic: the same pins always
// yield the same view.
func BuildAggregate(events map[string][]*Pin, opts Options) (*Aggregate, error) {
	s, err := newSettlement(events, opts.Root)
	if err != nil {
		return nil, err
	}
	now := opts.Now
	if now == 0 {
		now = time.Now().Unix()
	}
	ttl := s.policyTTL()
	window := s.policyWindow()

	out := &Aggregate{
		TaskRoot:    opts.Root,
		TaskTitle:   payloadString(s.task, "title"),
		Quorum:      s.quorum,
		Policy:      s.policy,
		TaskAuthor:  s.rootAuthor,
		TreeID:      s.treeID,
		TreeNodes:   len(s.treeNodes),
		Reputation:  map[string]int{},
		VerifyVotes: len(s.last),
		Nodes:       make([]NodeSettlement, 0, len(s.treeNodes)),
		Pools:       map[string]PoolReport{},
		Enumeration: opts.Enumeration,
		Meta: Meta{
			GeneratedAt: nowRFC3339(opts.GeneratedAt),
			Now:         now,
			FeeFields:   []string{"genesisFee", "outputValue"},
			FeeNote: "genesisFee/outputValue are the pin's own genesis output fields; " +
				"the indexer pin object carries no transaction-fee field, so the actual " +
				"on-chain fee of each write is NOT derivable from this read path",
		},
	}

	inScopeSubs := map[string]bool{}
	for _, p := range s.subsPins {
		if payloadString(resolvePayload(p).Payload, "taskid") == s.root {
			inScopeSubs[p.ID] = true
		}
	}
	effectiveSubs := map[string]bool{}
	for _, sub := range s.subs {
		effectiveSubs[sub.Pin] = true
	}
	for key := range s.last {
		if effectiveSubs[key[0]] {
			out.VerifyVotesTask++
		}
	}

	for _, node := range s.nodeIDSet() {
		ns := NodeSettlement{Node: node}
		holder := s.holders[node]
		sub, good, bad := s.verifiedSub(node)
		verified := sub != nil && good >= s.quorum && bad == 0
		switch {
		case verified:
			ns.Status = "verified"
		case holder != nil:
			ns.Status = "claimed"
		default:
			ns.Status = "open"
		}
		if holder != nil {
			ns.Holder = holder.Claimant
			ns.HolderPin = holder.Pin
		}
		if sub != nil {
			ns.Submission = sub.Pin
			ns.SubmissionBy = sub.Author
			ns.ClaimIDOfSub = sub.Claim
		}
		if verified {
			ns.EffectivePass = good
			out.Reputation[sub.Author]++
		}
		ns.ValidPassTotal, ns.ValidFailTotal = good, bad
		ns.EffectiveStatus, ns.EffectiveReason = s.guardState(node, now, ttl, window)

		// Pool-scoped per-node detail.
		for _, p := range s.claims {
			body := resolvePayload(p).Payload
			if payloadString(body, "taskid") != s.root || payloadString(body, "node") != node {
				continue
			}
			ns.ClaimRecords++
			if holder == nil || holder.Pin != p.ID {
				ns.IgnoredClaims = append(ns.IgnoredClaims, p.ID)
			}
		}
		for _, p := range s.releases {
			body := resolvePayload(p).Payload
			if payloadString(body, "taskid") != s.root || payloadString(body, "node") != node {
				continue
			}
			ns.ReleasedPins = append(ns.ReleasedPins, p.ID)
		}
		subRecs := 0
		for _, p := range s.subsPins {
			body := resolvePayload(p).Payload
			if payloadString(body, "taskid") != s.root || payloadString(body, "node") != node {
				continue
			}
			subRecs++
			if ns.Submission != p.ID {
				ns.IgnoredSubs = append(ns.IgnoredSubs, p.ID)
			}
		}
		ns.SubmissionRec = subRecs
		if ns.Submission != "" {
			valid, invalid, passRaw, failRaw := s.nodeVotes(ns.Submission)
			lastValid := lastValidByBot(valid)
			ns.ValidPassTotal = 0
			ns.ValidFailTotal = 0
			bots := map[string]bool{}
			for _, v := range valid {
				bots[v.Bot] = true
				ns.Votes = append(ns.Votes, v)
			}
			for _, v := range invalid {
				bots[v.Bot] = true
				ns.Votes = append(ns.Votes, v)
			}
			for bot, v := range lastValid {
				switch v.Verdict {
				case "pass":
					if bot != ns.SubmissionBy && bot != s.rootAuthor {
						ns.ValidPassTotal++
					}
				case "fail":
					ns.ValidFailTotal++
				}
			}
			ns.PassRaw, ns.FailRaw = passRaw, failRaw
			ns.InvalidVotes = len(invalid)
			ns.VoterBots = len(bots)
			for b := range bots {
				ns.Voters = append(ns.Voters, b)
			}
			sort.Strings(ns.Voters)
		}
		for _, p := range s.rejected {
			if PoolName(p.Path) == "claim" || PoolName(p.Path) == "submission" {
				ns.RejectedPins = append(ns.RejectedPins, p.ID)
			}
		}
		out.Nodes = append(out.Nodes, ns)
	}

	out.AllVerified = len(out.Nodes) > 0
	for _, n := range out.Nodes {
		if n.Status != "verified" {
			out.AllVerified = false
			break
		}
	}

	out.Pools = s.poolReports(inScopeSubs, effectiveSubs, out.VerifyVotesTask)
	out.Meta.PinsRejected = len(s.rejected)
	return out, nil
}

// poolReports builds the three-pool aggregation.
func (s *settlement) poolReports(inScopeSubs, effectiveSubs map[string]bool, taskVotes int) map[string]PoolReport {
	reports := map[string]PoolReport{}

	claim := PoolReport{Pool: "claim", ProtocolPath: PathClaim}
	claimParticipants := map[string]bool{}
	for _, p := range s.claims {
		claim.Records++
		if payloadString(resolvePayload(p).Payload, "taskid") != s.root {
			continue
		}
		claim.RecordsInScope++
		claimParticipants[p.GlobalMetaID] = true
		claim = accumulatePinMeta(claim, p)
	}
	claim.Effective = len(s.holders)
	claim.Ignored = claim.RecordsInScope - claim.Effective
	if claim.Ignored < 0 {
		claim.Ignored = 0
	}
	claim.Participants = len(claimParticipants)
	claim.ParticipantBots = sortedKeys(claimParticipants)
	claim.Rejected = s.rejectedInPool(PathClaim)
	reports["claim"] = claim

	sub := PoolReport{Pool: "submission", ProtocolPath: PathSubmission}
	subParticipants := map[string]bool{}
	for _, p := range s.subsPins {
		sub.Records++
		if payloadString(resolvePayload(p).Payload, "taskid") != s.root {
			continue
		}
		sub.RecordsInScope++
		subParticipants[p.GlobalMetaID] = true
		sub = accumulatePinMeta(sub, p)
	}
	sub.Effective = len(s.subs)
	sub.Ignored = sub.RecordsInScope - sub.Effective
	if sub.Ignored < 0 {
		sub.Ignored = 0
	}
	sub.Participants = len(subParticipants)
	sub.ParticipantBots = sortedKeys(subParticipants)
	sub.Rejected = s.rejectedInPool(PathSubmission)
	reports["submission"] = sub

	verify := PoolReport{Pool: "verify", ProtocolPath: PathVerify}
	verifyParticipants := map[string]bool{}
	for _, p := range s.verifies {
		verify.Records++
		body := resolvePayload(p).Payload
		target := payloadString(body, "targetid")
		if !inScopeSubs[target] {
			continue
		}
		verify.RecordsInScope++
		verifyParticipants[p.GlobalMetaID] = true
		verify = accumulatePinMeta(verify, p)
		if reason := voteInvalid(body); reason != "" {
			verify.Invalid++
		} else {
			switch payloadString(body, "verdict") {
			case "pass":
				verify.ValidPass++
			case "fail":
				verify.ValidFail++
			}
		}
	}
	verify.Effective = taskVotes
	verify.Ignored = verify.RecordsInScope - verify.Effective
	if verify.Ignored < 0 {
		verify.Ignored = 0
	}
	verify.Participants = len(verifyParticipants)
	verify.ParticipantBots = sortedKeys(verifyParticipants)
	verify.BotsVoting = len(verifyParticipants)
	verify.Rejected = s.rejectedInPool(PathVerify)
	reports["verify"] = verify
	return reports
}

func (s *settlement) rejectedInPool(path string) int {
	n := 0
	for _, p := range s.rejected {
		if p.Path == path {
			n++
		}
	}
	return n
}

func accumulatePinMeta(r PoolReport, p *Pin) PoolReport {
	ts, seen := pinTimestamp(p), p.SeenTime
	if ts != 0 {
		if r.FirstTimestamp == 0 || ts < r.FirstTimestamp {
			r.FirstTimestamp = ts
		}
		if ts > r.LastTimestamp {
			r.LastTimestamp = ts
		}
	}
	if seen != 0 {
		if r.FirstSeen == 0 || seen < r.FirstSeen {
			r.FirstSeen = seen
		}
		if seen > r.LastSeen {
			r.LastSeen = seen
		}
	}
	r.GenesisFeeSum += p.GenesisFee
	r.OutputValueSum += p.OutputValue
	if p.GenesisHeight >= 0 {
		r.Confirmed++
	} else {
		r.Mempool++
	}
	return r
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
