// Package agentpedia implements MetaSo's independent Agentpedia replay engine
// (adoption-algo-v1 semantics) and the L2 aggregation service described in the
// second deliverable §4.1 (pin://23e828698ca0cfc90edc2004074f85b4fd93227a34e1e1a6247fe328a1e0bc83i0).
//
// This is a deliberately separate implementation from the IDBots-side engine:
// both must produce the same view for the same event stream (G2 parity), which
// is only provable if the code paths differ. Semantics resolve through the six
// valid spec layers per chair errata E-1/E-2 (v0.1.4 voided; T1 ladder per E-2;
// D4 cluster-merged counting).
package agentpedia

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// Event mirrors one Agentpedia protocol pin in chain order.
type Event struct {
	Pin     string         `json:"pin"`
	Path    string         `json:"path"`
	Height  int64          `json:"height"`
	TxIndex int            `json:"txIndex"`
	Sender  string         `json:"sender"`
	Payload map[string]any `json:"payload"`
}

// Options carries scenario/runtime tuning for Replay.
type Options struct {
	BlocksPerHour          int
	BlocksPerDay           int
	ArbiterOverrides       map[string][]string // proposalPin -> injected arbiter set
	FrozenArbiterOverrides map[string][]string // entryKey -> injected freeze-write set
	EditorsRep             map[string]float64  // scenario reputation baselines
	ClusterAliases         map[string]string   // alias MetaID -> root MetaID (D4 counting)
}

// Version is one resolvable revision (original or equivalent).
type Version struct {
	ContentHash  string `json:"contentHash,omitempty"`
	Author       string `json:"author,omitempty"`
	ParentRev    string `json:"parentRev,omitempty"`
	BasedOn      string `json:"basedOn,omitempty"`
	Summary      string `json:"summary,omitempty"`
	EntryKey     string `json:"entryKey,omitempty"`
	EquivalentOf string `json:"equivalentOf,omitempty"`
}

// Contest records a stale basedOn declaration (spec v0.1 §3.2.3).
type Contest struct {
	Pin      string `json:"pin"`
	Expected string `json:"expected"`
	Declared string `json:"declared"`
}

// RedirectInfo marks a redirected entry (view renders a redirect plate).
type RedirectInfo struct {
	To string `json:"to"`
}

// Entry is the per-entryKey read view.
type Entry struct {
	Head        string             `json:"head"`
	Status      string             `json:"status"`
	History     []string           `json:"history"`
	Disputed    []string           `json:"disputed"`
	Contests    []Contest          `json:"contests"`
	Redirect    *RedirectInfo      `json:"redirect,omitempty"`
	FrozenAt    string             `json:"frozenAt,omitempty"`
	BaselineRev string             `json:"baselineRev,omitempty"`
	Versions    map[string]Version `json:"versions"`
}

// EditorState is the registry/reputation view of one MetaID.
type EditorState struct {
	Status       string  `json:"status"` // active | pending | none
	Tier         *string `json:"tier"`   // T0 | T0+ | T1 | T2; null when unregistered/suspended (reference-view parity, B1)
	Reputation   float64 `json:"reputation"`
	ValidRevs    int     `json:"validRevs"`
	RegisteredAt *int64  `json:"registeredAt"`
	Revoked      bool    `json:"revoked"`
	Banned       bool    `json:"banned"`
}

// ProposalState is the arbitration case view.
type ProposalState struct {
	State        string   `json:"state"` // open | effective | voided
	Outcome      string   `json:"outcome"`
	ApproveCount int      `json:"approveCount"`
	Arbiters     []string `json:"arbiters"`
	EntryKey     string   `json:"entryKey,omitempty"`
	BaselineRev  string   `json:"baselineRev,omitempty"`
}

// Grave is one graveyard record (never in the entry view, always exportable).
type Grave struct {
	Pin    string `json:"pin"`
	Reason string `json:"reason"`
}

// View is the deterministic replay output (spec v0.1 §13.5).
type View struct {
	Entries   map[string]*Entry         `json:"entries"`
	Graveyard []Grave                   `json:"graveyard"`
	Pending   []string                  `json:"pending"`
	Editors   map[string]*EditorState   `json:"editors"`
	Proposals map[string]*ProposalState `json:"proposals"`
	Params    map[string]float64        `json:"params"`
	Founders  []string                  `json:"founders"`
}

// Protocol paths (spec v0.1 §2).
const (
	PathRev           = "/protocols/agentpedia/rev"
	PathChallenge     = "/protocols/agentpedia/challenge"
	PathRuling        = "/protocols/agentpedia/ruling"
	PathReview        = "/protocols/agentpedia/review"
	PathEditor        = "/protocols/agentpedia/editor"
	PathConstitution  = "/protocols/agentpedia/constitution"
	PathParamProposal = "/protocols/agentpedia/param-proposal"
)

type editorRec struct {
	founder         bool
	registeredAt    *int64
	registerH       int64
	stakeOk         bool
	endorsements    map[string]bool
	reputation      float64
	validRevs       int
	suspensionUntil int64
	revoked         bool
	banned          bool
}

type entryRec struct {
	key            string
	head           string
	status         string // normal | frozen | protected
	history        []string
	disputed       map[string]bool
	contests       []Contest
	versions       map[string]Version
	redirect       string
	frozenAt       string
	baselineRev    string
	frozenArbiters []string
	revertWeights  []revertWeightRec
}

type revertWeightRec struct {
	h      int64
	editor string
	weight float64
	vf     bool
}

type challengeRec struct {
	targetRev  string
	challenger string
	entryKey   string
}

type regChallengeRec struct {
	applicant  string
	challenger string
	h          int64
	pocOk      bool
	stakeOk    bool
	registerH  int64
}

type proposalRec struct {
	pin          string
	challengePin string
	outcome      string
	params       map[string]any
	proposer     string
	height       int64
	expiryH      int64
	arbiters     []string
	approves     map[string]bool
	approveCount int
	state        string
	entryKey     string
	baselineRev  string
	voters       map[string]bool
}

type paramProposalRec struct {
	state    string
	approves map[string]bool
}

type replayState struct {
	opts            Options
	bph             int
	bpd             int
	params          map[string]float64
	genesisHeight   int64
	bootstrapEndH   int64
	founders        []string
	editors         map[string]*editorRec
	entries         map[string]*entryRec
	graveyard       []Grave
	pending         []string
	challenges      map[string]*challengeRec
	proposals       map[string]*proposalRec
	paramProposals  map[string]*paramProposalRec
	reviewsByTarget map[string][]reviewRec
	allReviews      []reviewRec
	regChallenges   map[string]*regChallengeRec
	dayGlobal       map[string]int
	daySlug         map[string]int
}

type reviewRec struct {
	pin      string
	reviewer string
}

func (s *replayState) grave(pin, reason string) {
	s.graveyard = append(s.graveyard, Grave{Pin: pin, Reason: reason})
}

func (s *replayState) pfloat(key string, def float64) float64 {
	if v, ok := s.params[key]; ok {
		return v
	}
	return def
}

func (s *replayState) editorOf(id string) *editorRec {
	if _, ok := s.editors[id]; !ok {
		s.editors[id] = &editorRec{endorsements: map[string]bool{}}
	}
	return s.editors[id]
}

func (s *replayState) entryOf(key string) *entryRec {
	if _, ok := s.entries[key]; !ok {
		s.entries[key] = &entryRec{key: key, status: "normal", disputed: map[string]bool{}, versions: map[string]Version{}}
	}
	return s.entries[key]
}

func (s *replayState) countRoot(id string) string {
	if root, ok := s.opts.ClusterAliases[id]; ok {
		return root
	}
	return id
}

func (s *replayState) tierOf(id string, h int64) string {
	ed, ok := s.editors[id]
	if !ok || ed.revoked || ed.banned || ed.registeredAt == nil {
		return ""
	}
	if ed.suspensionUntil != 0 && h < ed.suspensionUntil {
		return ""
	}
	ageH := float64(h-*ed.registeredAt) / float64(s.bph)
	ageDays := float64(h-*ed.registeredAt) / float64(s.bpd)
	bootstrapping := h < s.bootstrapEndH
	// E-2 layered authorization.
	if ed.founder && bootstrapping {
		return "T2"
	}
	if ageDays >= s.pfloat("t2MinDays", 14) && float64(ed.validRevs) >= s.pfloat("t2MinValidRevs", 100) {
		return "T2"
	}
	if ageH >= s.pfloat("t0DurationHours", 72) && float64(ed.validRevs) >= s.pfloat("t1MinValidRevs", 10) {
		return "T1"
	}
	if ageH >= s.pfloat("t0DurationHours", 72) {
		return "T0+"
	}
	return "T0"
}

func (s *replayState) isActive(id string, h int64) bool { return s.tierOf(id, h) != "" }

func (s *replayState) arbiterCandidates(h int64) []string {
	ids := make([]string, 0, len(s.editors))
	for id := range s.editors {
		if s.tierOf(id, h) == "T2" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// arbiterDraw implements arbiter-draw-v1 (spec v0.1 §13.4): sha256(seed+id) first
// 8 bytes big-endian as the sort key, top-N join the set.
func ArbiterDraw(seedPinId string, candidates []string, n int) []string {
	type scored struct {
		id  string
		key uint64
	}
	all := make([]scored, 0, len(candidates))
	for _, id := range candidates {
		sum := sha256.Sum256([]byte(seedPinId + id))
		all = append(all, scored{id, binary.BigEndian.Uint64(sum[:8])})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].key != all[j].key {
			return all[i].key < all[j].key
		}
		return all[i].id < all[j].id
	})
	out := make([]string, 0, n)
	for i := 0; i < n && i < len(all); i++ {
		out = append(out, all[i].id)
	}
	return out
}

func (s *replayState) dayOf(h int64) int64 { return (h - s.genesisHeight) / int64(s.bpd) }

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func num(m map[string]any, key string) (float64, bool) {
	v, ok := m[key].(float64)
	return v, ok
}

func sub(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

func (s *replayState) closeExpired(h int64) {
	for _, p := range s.proposals {
		if p.state == "open" && h >= p.expiryH {
			p.state = "voided" // v0.1.1 X7: expiry void + neutral auto-revert
			if p.entryKey != "" {
				en := s.entries[p.entryKey]
				if en != nil && en.status == "frozen" {
					if p.baselineRev != "" {
						en.head = p.baselineRev
					}
					en.status = "normal"
				}
			}
		}
	}
}

func (s *replayState) applyOutcome(p *proposalRec) {
	p.state = "effective"
	if p.entryKey != "" {
		en := s.entries[p.entryKey]
		switch p.outcome {
		case "dismiss":
			if ch, ok := s.challenges[p.challengePin]; ok {
				delete(en.disputed, ch.targetRev)
			}
		case "revert-to":
			rt, _ := p.params["revertTo"].(string)
			target := en.versions[rt]
			eq := p.pin + "::rev"
			en.versions[eq] = Version{ContentHash: target.ContentHash, Author: p.proposer, ParentRev: en.head, EntryKey: p.entryKey, EquivalentOf: p.pin}
			en.head = eq
			en.history = append(en.history, eq)
		case "protect":
			en.status = "protected"
		case "unprotect":
			en.status = "normal"
		case "unfreeze":
			if en.status == "frozen" {
				en.status = "normal"
			}
		case "transfer-slug":
			from, _ := p.params["fromEntry"].(string)
			to, _ := p.params["toEntry"].(string)
			if src, ok := s.entries[from]; ok {
				s.entries[to] = src
			}
		}
	}
	deltas := map[string]float64{"confirm-goodfaith": 1, "warn-editor": -1, "slash-stake-half": -2, "slash-stake-full": -5, "ban-editor": -5}
	if d, ok := deltas[p.outcome]; ok {
		if editor, ok := p.params["editor"].(string); ok && editor != "" {
			s.editorOf(editor).reputation += d
		}
	}
	if p.outcome == "ban-editor" {
		if editor, ok := p.params["editor"].(string); ok && editor != "" {
			s.editorOf(editor).banned = true
		}
	}
}

func (s *replayState) snapshotArbiters(p *proposalRec, h int64) []string {
	if set, ok := s.opts.ArbiterOverrides[p.pin]; ok {
		return append([]string(nil), set...)
	}
	return ArbiterDraw(p.challengePin, s.arbiterCandidates(h), int(s.pfloat("arbiterDrawN", 7)))
}

func (s *replayState) checkRevGates(ev Event, en *entryRec, h int64) string {
	if !s.isActive(ev.Sender, h) {
		return "unregistered"
	}
	if s.tierOf(ev.Sender, h) == "T0" {
		return "t0-no-rev" // E-2: T0 window is read+review only
	}
	if en.status == "frozen" {
		found := false
		for _, arb := range en.frozenArbiters {
			if arb == ev.Sender {
				found = true
				break
			}
		}
		if !found {
			return "frozen-unauthorized"
		}
	}
	if en.status == "protected" {
		ed := s.editors[ev.Sender]
		if !(s.tierOf(ev.Sender, h) == "T2" && ed != nil && ed.reputation >= s.pfloat("thetaProtect", 0)) {
			return "protected-unauthorized" // v0.1.3 §1 conjunction
		}
	}
	day := s.dayOf(h)
	root := s.countRoot(ev.Sender) // D4 cluster-merged counting
	gk := fmt.Sprintf("%s|%d", root, day)
	sk := fmt.Sprintf("%s|%s", gk, en.key)
	if s.dayGlobal[gk]+1 > int(s.pfloat("rateGlobalDaily", 20)) {
		return "rate-global"
	}
	if s.daySlug[sk]+1 > int(s.pfloat("ratePerSlugDaily", 10)) {
		return "rate-slug"
	}
	return ""
}

func (s *replayState) countRev(ev Event, en *entryRec, h int64) {
	day := s.dayOf(h)
	root := s.countRoot(ev.Sender)
	gk := fmt.Sprintf("%s|%d", root, day)
	sk := fmt.Sprintf("%s|%s", gk, en.key)
	s.dayGlobal[gk]++
	s.daySlug[sk]++
	s.editorOf(ev.Sender).validRevs++
}

func (s *replayState) revertWeightFor(en *entryRec, ev Event) float64 {
	claim := sub(ev.Payload, "claim")
	if changeType, _ := claim["changeType"].(string); changeType == "vandalism-fix" {
		root := s.countRoot(ev.Sender)
		for _, w := range en.revertWeights {
			if w.editor == root && w.vf {
				return 1 // F-1: same-editor repeat restores 1.0
			}
		}
		return 0.5
	}
	return 1
}

func asStringSlice(v any) []string {
	switch raw := v.(type) {
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			s, _ := item.(string)
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return raw
	}
	return nil
}

func (s *replayState) applyEditor(ev Event, h int64) {
	payload := ev.Payload
	action, _ := payload["action"].(string)
	applicant := str(payload, "editor")
	s.editorOf(applicant) // failed applicants stay visible (audit)
	switch action {
	case "challenge":
		if !s.isActive(ev.Sender, h) {
			s.grave(ev.Pin, "unregistered")
			return
		}
		s.regChallenges[ev.Pin] = &regChallengeRec{applicant: applicant, challenger: ev.Sender, h: h}
	case "poc-response":
		rc, ok := s.regChallenges[str(payload, "challengePin")]
		if !ok || rc.applicant != ev.Sender {
			s.grave(ev.Pin, "poc-challenge-mismatch")
			return
		}
		if h-rc.h > int64(s.bph) {
			s.grave(ev.Pin, "poc-window-exceeded") // 60 minutes
			return
		}
		found := false
		for _, r := range s.allReviews {
			if r.pin == str(payload, "responsePin") && r.reviewer == ev.Sender {
				found = true
				break
			}
		}
		if !found {
			s.grave(ev.Pin, "poc-response-missing")
			return
		}
		rc.pocOk = true
	case "register":
		rc, ok := s.regChallenges[str(payload, "challengePin")]
		if !ok || rc.applicant != ev.Sender || !rc.pocOk {
			s.grave(ev.Pin, "register-invalid")
			return
		}
		stake := sub(payload, "stake")
		amount, _ := stake["amountSat"].(float64)
		if stake == nil || str(stake, "txid") == "" || amount < s.pfloat("stakeAmountSat", 1) {
			s.grave(ev.Pin, "stake-insufficient")
			return
		}
		rc.stakeOk = true
		rc.registerH = h
		s.editorOf(ev.Sender).registerH = h
	case "endorse":
		if !s.isActive(ev.Sender, h) || s.tierOf(ev.Sender, h) != "T2" {
			s.grave(ev.Pin, "endorser-not-t2") // v0.1.3 §2: endorsers are T2 only
			return
		}
		var target *regChallengeRec
		for _, rc := range s.regChallenges {
			if rc.applicant == applicant && rc.stakeOk {
				target = rc
				break
			}
		}
		if target == nil {
			s.grave(ev.Pin, "endorse-no-register")
			return
		}
		if ev.Sender == applicant {
			s.grave(ev.Pin, "self-endorse")
			return
		}
		ed := s.editorOf(applicant)
		if ed.endorsements[ev.Sender] {
			s.grave(ev.Pin, "duplicate-endorse")
			return
		}
		ed.endorsements[ev.Sender] = true
		threshold := 2
		if h < s.bootstrapEndH {
			threshold = 4 // v0.1.2 D7.2 bootstrap doubling
		}
		if len(ed.endorsements) >= threshold && ed.registeredAt == nil {
			registerH := target.registerH
			ed.registeredAt = &registerH
		}
	case "suspend":
		p, ok := s.proposals[str(payload, "rulingPin")]
		if !ok || p.state != "effective" || s.tierOf(ev.Sender, h) != "T2" {
			s.grave(ev.Pin, "suspend-unauthorized")
			return
		}
		s.editorOf(applicant).suspensionUntil = h + int64(s.pfloat("arbiterSuspensionDays", 30))*int64(s.bpd)
	case "revoke":
		p, ok := s.proposals[str(payload, "rulingPin")]
		if !ok || p.state != "effective" || s.tierOf(ev.Sender, h) != "T2" {
			s.grave(ev.Pin, "revoke-unauthorized")
			return
		}
		s.editorOf(applicant).revoked = true
	default:
		s.grave(ev.Pin, "editor-action-unknown")
	}
}

func (s *replayState) applyReview(ev Event) {
	if !s.isActive(ev.Sender, ev.Height) {
		// No membership gate at record time: the registration PoC is itself a
		// review pinned by the not-yet-registered applicant (v0.1 §7.2).
	}
	targetRev := str(ev.Payload, "targetRev")
	targetKey := ""
	for key, en := range s.entries {
		if _, ok := en.versions[targetRev]; ok {
			targetKey = key
			break
		}
	}
	if targetKey == "" {
		s.grave(ev.Pin, "review-target-unresolvable")
		return
	}
	if s.entries[targetKey].versions[targetRev].Author == ev.Sender {
		s.grave(ev.Pin, "self-review")
		return
	}
	s.reviewsByTarget[targetRev] = append(s.reviewsByTarget[targetRev], reviewRec{pin: ev.Pin, reviewer: ev.Sender})
	s.allReviews = append(s.allReviews, reviewRec{pin: ev.Pin, reviewer: ev.Sender})
}

func (s *replayState) applyChallenge(ev Event) {
	targetRev := str(ev.Payload, "targetRev")
	targetKey := ""
	for key, en := range s.entries {
		if _, ok := en.versions[targetRev]; ok {
			targetKey = key
			break
		}
	}
	if targetKey == "" {
		s.grave(ev.Pin, "challenge-target-unresolvable")
		return
	}
	if !s.isActive(ev.Sender, ev.Height) {
		s.grave(ev.Pin, "unregistered")
		return
	}
	if s.entries[targetKey].versions[targetRev].Author == ev.Sender {
		s.grave(ev.Pin, "self-challenge")
		return
	}
	s.entries[targetKey].disputed[targetRev] = true
	s.challenges[ev.Pin] = &challengeRec{targetRev: targetRev, challenger: ev.Sender, entryKey: targetKey}
}

func (s *replayState) applyRuling(ev Event, h int64) {
	payload := ev.Payload
	action, _ := payload["action"].(string)
	if action == "proposal" {
		if !s.isActive(ev.Sender, h) {
			s.grave(ev.Pin, "unregistered")
			return
		}
		challengePin := str(payload, "challengePin")
		ch, ok := s.challenges[challengePin]
		if !ok {
			s.grave(ev.Pin, "ruling-challenge-missing")
			return
		}
		if str(payload, "seed") != challengePin {
			s.grave(ev.Pin, "ruling-seed-mismatch")
			return
		}
		en := s.entries[ch.entryKey]
		baselineRev := ""
		if en.status == "frozen" {
			declared := str(payload, "baselineRev")
			if declared == "" || declared != en.baselineRev {
				s.grave(ev.Pin, "baseline-rev-mismatch") // v0.1.1 §2 rule 1 (C10)
				return
			}
			baselineRev = declared
		} else if str(payload, "baselineRev") != "" {
			s.grave(ev.Pin, "baseline-rev-not-null")
			return
		}
		outcome, _ := payload["outcome"].(string)
		params := sub(payload, "params")
		okParams := true
		switch outcome {
		case "revert-to":
			okParams = params != nil && str(params, "revertTo") != ""
			if okParams {
				_, okParams = en.versions[str(params, "revertTo")]
			}
		case "transfer-slug":
			okParams = params != nil && str(params, "fromEntry") != "" && str(params, "toEntry") != ""
		case "warn-editor", "slash-stake-half", "slash-stake-full", "ban-editor", "confirm-goodfaith":
			okParams = params != nil && str(params, "editor") != ""
		case "protect", "unprotect":
			_, okParams = params["protected"].(bool)
		case "dismiss", "unfreeze":
			okParams = true
		default:
			okParams = false
		}
		if !okParams {
			s.grave(ev.Pin, "ruling-params-missing")
			return
		}
		p := &proposalRec{
			pin: ev.Pin, challengePin: challengePin, outcome: outcome, params: params,
			proposer: ev.Sender, height: h,
			expiryH:  h + int64(s.pfloat("voteWindowHours", 48))*int64(s.bph),
			approves: map[string]bool{}, state: "open", entryKey: ch.entryKey, baselineRev: baselineRev,
			voters: map[string]bool{},
		}
		p.arbiters = s.snapshotArbiters(p, h)
		s.proposals[ev.Pin] = p
		return
	}
	if action == "vote" {
		proposalPin := str(payload, "proposalPin")
		p, ok := s.proposals[proposalPin]
		if !ok || p.state != "open" || h > p.expiryH {
			s.grave(ev.Pin, "vote-on-closed-proposal")
			return
		}
		if p.voters[ev.Sender] {
			s.grave(ev.Pin, "duplicate-vote")
			return
		}
		p.voters[ev.Sender] = true
		approve, _ := payload["approve"].(bool)
		// approval-only (v0.1.1 X6): snapshot arbiters' approves count; the rest is
		// recorded but never counts (V15-R/V24).
		if approve {
			for _, arb := range p.arbiters {
				if arb == ev.Sender {
					p.approves[ev.Sender] = true
					p.approveCount = len(p.approves)
					if p.approveCount >= int(s.pfloat("rulingQuorum", 5)) {
						s.applyOutcome(p)
					}
					break
				}
			}
		}
		return
	}
	s.grave(ev.Pin, "ruling-action-unknown")
}

func (s *replayState) applyParamProposal(ev Event, h int64) {
	payload := ev.Payload
	kind, _ := payload["kind"].(string)
	if kind == "proposal" {
		if !s.isActive(ev.Sender, h) {
			s.grave(ev.Pin, "unregistered")
			return
		}
		s.paramProposals[ev.Pin] = &paramProposalRec{state: "open", approves: map[string]bool{}}
		return
	}
	if kind == "vote" {
		pp, ok := s.paramProposals[str(payload, "proposalPin")]
		if !ok || pp.state != "open" {
			s.grave(ev.Pin, "param-vote-on-closed")
			return
		}
		if approve, _ := payload["approve"].(bool); approve && s.tierOf(ev.Sender, h) == "T2" {
			pp.approves[ev.Sender] = true
		}
		need := int(int64(s.pfloat("arbiterPoolK", 21))*2/3) + 1 // ceil(2/3 * K) = 14 of 21
		if len(pp.approves) >= need {
			pp.state = "effective"
		}
		return
	}
	s.grave(ev.Pin, "param-proposal-kind-unknown")
}

func (s *replayState) applyRev(ev Event, h int64) {
	payload := ev.Payload
	lang := str(payload, "lang")
	slug := str(payload, "slug")
	entryKey := lang + ":" + slug
	en := s.entryOf(entryKey)
	evType, _ := payload["type"].(string)

	if reason := s.checkRevGates(ev, en, h); reason != "" {
		s.grave(ev.Pin, reason)
		return
	}

	switch evType {
	case "create":
		if en.head != "" {
			s.grave(ev.Pin, "duplicate-create") // V18 orphan
			return
		}
		en.head = ev.Pin
		en.history = append(en.history, ev.Pin)
		contentHash, _ := payload["contentHash"].(string)
		summary, _ := payload["summary"].(string)
		en.versions[ev.Pin] = Version{ContentHash: contentHash, Author: ev.Sender, Summary: summary, EntryKey: entryKey}
		s.countRev(ev, en, h)
	case "edit":
		if en.head == "" {
			s.grave(ev.Pin, "edit-without-entry")
			return
		}
		prevHead := en.head
		basedOn := str(payload, "basedOn")
		if basedOn != "" && basedOn != prevHead {
			en.contests = append(en.contests, Contest{Pin: ev.Pin, Expected: prevHead, Declared: basedOn}) // v0.1 §3.2.3
		}
		parentRev := str(payload, "parentRev")
		contentHash := str(payload, "contentHash")
		summary, _ := payload["summary"].(string)
		en.head = ev.Pin
		en.history = append(en.history, ev.Pin)
		en.versions[ev.Pin] = Version{ContentHash: contentHash, Author: ev.Sender, ParentRev: parentRev, BasedOn: basedOn, Summary: summary, EntryKey: entryKey}
		s.countRev(ev, en, h)
	case "revert":
		if en.head == "" {
			s.grave(ev.Pin, "revert-without-entry")
			return
		}
		revertTo := str(payload, "revertTo")
		target, inEntry := en.versions[revertTo]
		if !inEntry {
			cross := false
			for _, other := range s.entries {
				if _, ok := other.versions[revertTo]; ok {
					cross = true
					break
				}
			}
			if cross {
				s.grave(ev.Pin, "revert-entry-mismatch") // V05
			} else {
				s.grave(ev.Pin, "revert-target-unresolvable")
			}
			return
		}
		if target.EntryKey != entryKey {
			s.grave(ev.Pin, "revert-entry-mismatch")
			return
		}
		contentHash := str(payload, "contentHash")
		if contentHash != target.ContentHash {
			s.grave(ev.Pin, "revert-hash-mismatch") // V06
			return
		}
		preHead := en.head
		eq := ev.Pin + "::eq"
		en.versions[eq] = Version{ContentHash: target.ContentHash, Author: ev.Sender, ParentRev: preHead, EntryKey: entryKey, EquivalentOf: ev.Pin}
		en.head = eq
		en.history = append(en.history, eq)
		s.countRev(ev, en, h)
		// edit-war accounting: v0.1.2 §3 weights + v0.1.5 §1 sole criterion
		winH := int64(s.pfloat("revertWarWindowHours", 6)) * int64(s.bph)
		weight := s.revertWeightFor(en, ev)
		vf := false
		if claim := sub(payload, "claim"); claim != nil {
			vf = claim["changeType"] == "vandalism-fix"
		}
		kept := en.revertWeights[:0]
		for _, w := range en.revertWeights {
			if w.h > h-winH {
				kept = append(kept, w)
			}
		}
		en.revertWeights = append(kept, revertWeightRec{h: h, editor: s.countRoot(ev.Sender), weight: weight, vf: vf})
		cumulative := 0.0
		for _, w := range en.revertWeights {
			cumulative += w.weight
		}
		if en.status == "normal" && cumulative >= s.pfloat("revertWarThreshold", 3) {
			en.status = "frozen"
			en.frozenAt = ev.Pin
			en.baselineRev = preHead // last normal head before the triggering event
			if set, ok := s.opts.FrozenArbiterOverrides[entryKey]; ok {
				en.frozenArbiters = append([]string(nil), set...)
			} else {
				en.frozenArbiters = ArbiterDraw(ev.Pin, s.arbiterCandidates(h), int(s.pfloat("arbiterDrawN", 7)))
			}
		}
	case "redirect":
		if en.head == "" {
			s.grave(ev.Pin, "redirect-without-entry")
			return
		}
		redirectTo := str(payload, "redirectTo")
		target, ok := s.entries[lang+":"+redirectTo]
		if !ok || target.head == "" {
			s.grave(ev.Pin, "redirect-target-missing") // V19
			return
		}
		en.head = ev.Pin
		en.history = append(en.history, ev.Pin)
		en.redirect = redirectTo
		s.countRev(ev, en, h)
	default:
		s.grave(ev.Pin, "rev-type-unknown")
	}
}

func (s *replayState) apply(ev Event) {
	s.closeExpired(ev.Height)
	h := ev.Height
	switch ev.Path {
	case PathConstitution:
		revision, _ := ev.Payload["revision"].(float64)
		if revision == 0 {
			if s.params != nil {
				s.grave(ev.Pin, "duplicate-genesis")
				return
			}
			params := map[string]float64{}
			if raw, ok := ev.Payload["params"].(map[string]any); ok {
				for k, v := range raw {
					if f, ok := v.(float64); ok {
						params[k] = f
					}
				}
			}
			s.params = params
			s.genesisHeight = h
			s.founders = asStringSlice(ev.Payload["founders"])
			s.bootstrapEndH = h + int64(s.pfloat("bootstrapWindowDays", 30))*int64(s.bpd)
			for _, f := range s.founders {
				ed := s.editorOf(f)
				ed.founder = true
				ed.registeredAt = &h
			}
			for id, rep := range s.opts.EditorsRep {
				s.editorOf(id).reputation = rep
			}
			return
		}
		pp, ok := s.paramProposals[str(ev.Payload, "proposalPin")]
		if !ok || pp.state != "effective" {
			s.grave(ev.Pin, "constitution-without-effective-proposal")
			return
		}
		if raw, ok := ev.Payload["params"].(map[string]any); ok {
			for k, v := range raw {
				if f, ok := v.(float64); ok {
					s.params[k] = f
				}
			}
		}
		return
	}
	if s.params == nil {
		s.grave(ev.Pin, "no-genesis")
		return
	}
	switch ev.Path {
	case PathEditor:
		s.applyEditor(ev, h)
	case PathReview:
		s.applyReview(ev)
	case PathChallenge:
		s.applyChallenge(ev)
	case PathRuling:
		s.applyRuling(ev, h)
	case PathParamProposal:
		s.applyParamProposal(ev, h)
	case PathRev:
		s.applyRev(ev, h)
	default:
		s.grave(ev.Pin, "unknown-path")
	}
}

// Replay deterministically applies the ordered event stream and returns the view.
// Mempool pins (Height < 0) are recorded as pending and excluded (fact card F6).
func Replay(events []Event, opts Options) *View {
	s := &replayState{
		opts:            opts,
		bph:             opts.BlocksPerHour,
		bpd:             opts.BlocksPerDay,
		editors:         map[string]*editorRec{},
		entries:         map[string]*entryRec{},
		challenges:      map[string]*challengeRec{},
		proposals:       map[string]*proposalRec{},
		paramProposals:  map[string]*paramProposalRec{},
		reviewsByTarget: map[string][]reviewRec{},
		regChallenges:   map[string]*regChallengeRec{},
		dayGlobal:       map[string]int{},
		daySlug:         map[string]int{},
	}
	if s.bph <= 0 {
		s.bph = 6
	}
	if s.bpd <= 0 {
		s.bpd = 144
	}
	ordered := make([]Event, 0, len(events))
	for _, e := range events {
		if e.Height < 0 {
			s.pending = append(s.pending, e.Pin)
			continue
		}
		ordered = append(ordered, e)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Height != ordered[j].Height {
			return ordered[i].Height < ordered[j].Height
		}
		return ordered[i].TxIndex < ordered[j].TxIndex
	})
	for _, ev := range ordered {
		s.apply(ev)
	}
	lastHeight := int64(0)
	if len(ordered) > 0 {
		lastHeight = ordered[len(ordered)-1].Height
	}
	s.closeExpired(lastHeight + 1)

	pending := s.pending
	if pending == nil {
		pending = []string{}
	}
	graveyard := s.graveyard
	if graveyard == nil {
		graveyard = []Grave{}
	}
	view := &View{
		Entries:   map[string]*Entry{},
		Graveyard: graveyard,
		Pending:   pending,
		Editors:   map[string]*EditorState{},
		Proposals: map[string]*ProposalState{},
		Params:    s.params,
		Founders:  s.founders,
	}
	for key, en := range s.entries {
		if len(en.history) == 0 && en.head == "" {
			continue
		}
		disputed := make([]string, 0, len(en.disputed))
		for rev := range en.disputed {
			disputed = append(disputed, rev)
		}
		sort.Strings(disputed)
		var redirect *RedirectInfo
		if en.redirect != "" {
			redirect = &RedirectInfo{To: en.redirect}
		}
		contests := en.contests
		if contests == nil {
			contests = []Contest{} // empty-array parity with the reference view shape
		}
		view.Entries[key] = &Entry{
			Head: en.head, Status: en.status, History: en.history, Disputed: disputed,
			Contests: contests, Redirect: redirect, FrozenAt: en.frozenAt,
			BaselineRev: en.baselineRev, Versions: en.versions,
		}
	}
	for id, ed := range s.editors {
		status := "none"
		switch {
		case ed.registeredAt != nil:
			status = "active"
		case ed.registerH != 0:
			status = "pending"
		}
		var tier *string
		if len(ordered) > 0 {
			if t := s.tierOf(id, lastHeight); t != "" {
				tier = &t // null (not "") for unregistered/suspended — reference-view parity (B1)
			}
		}
		view.Editors[id] = &EditorState{
			Status: status, Tier: tier, Reputation: ed.reputation, ValidRevs: ed.validRevs,
			RegisteredAt: ed.registeredAt, Revoked: ed.revoked, Banned: ed.banned,
		}
	}
	for pin, p := range s.proposals {
		view.Proposals[pin] = &ProposalState{
			State: p.state, Outcome: p.outcome, ApproveCount: p.approveCount,
			Arbiters: p.arbiters, EntryKey: p.entryKey, BaselineRev: p.baselineRev,
		}
	}
	return view
}
