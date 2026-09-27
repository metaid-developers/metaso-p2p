// Package metatask implements MetaSo's read path for the /protocols/metatask
// pools. Given a task root it enumerates the claim/submission/verify pools
// (cursor paging to an explicit empty page), settles them with the
// metatask-replay v1.2.0 semantics, and reports the participating bots, vote
// counts, fees and timestamps as metadata.
//
// Determinism contract:
//   - The settlement is a pure function of the pin stream: the same pins yield
//     the same view. No wall-clock value participates except the explicit
//     `now` passed to the §4.7 guard view (EffectiveStatus).
//   - Enumeration is completed-or-flagged. A short page is never silently
//     accepted: see Termination in PathEnumeration and the §〇 payload rules in
//     payload.go.
//
// The settlement rules mirror metatask-replay v1.2.0
// (pin://de90cb26a480f3132d90b7ff937cc0cfa030bb77edc6fa6826d9b2201c76ff58i0),
// independently re-implemented here — including the #8/#9 vote filter — so the
// two code paths can be diffed item by item.
package metatask

import "time"

// Protocol paths of the nine metaTask event families (v1.2.1 eventSetHash
// path order: task, tree, spec, claim, release, submission, verify, amend,
// challenge).
const (
	PathTask       = "/protocols/metatask/task"
	PathTree       = "/protocols/metatask/tree"
	PathSpec       = "/protocols/metatask/spec"
	PathClaim      = "/protocols/metatask/claim"
	PathRelease    = "/protocols/metatask/release"
	PathSubmission = "/protocols/metatask/submission"
	PathVerify     = "/protocols/metatask/verify"
	PathAmend      = "/protocols/metatask/amend"
	PathChallenge  = "/protocols/metatask/challenge"
)

// EventPaths is the full set of paths the replay consumes, in the v1.2.1
// eventSetHash order (settlement.eventSetHash.recipe).
var EventPaths = []string{PathTask, PathTree, PathSpec, PathClaim, PathRelease, PathSubmission, PathVerify, PathAmend, PathChallenge}

// PoolPaths are the three pools the aggregation reports on.
var PoolPaths = []string{PathClaim, PathSubmission, PathVerify}

// Pin is one pool record with its byte-exact payload.
//
// Payload is the raw payload bytes as published; it is never reconstructed from
// a display layer. PayloadChannel records where those bytes came from
// ("content", "contentBody" or "contentSummary") so a ruling can state its
// evidence path; PayloadState records the §〇 acceptance verdict.
type Pin struct {
	ID                 string `json:"id"`
	Path               string `json:"path"`
	Operation          string `json:"operation"`
	GlobalMetaID       string `json:"globalMetaId"`
	MetaID             string `json:"metaId"`
	Address            string `json:"address"`
	GenesisHeight      int64  `json:"genesisHeight"`
	TxIndex            int64  `json:"txIndex"`
	Timestamp          int64  `json:"timestamp"`
	SeenTime           int64  `json:"seenTime"`
	GenesisTransaction string `json:"genesisTransaction"`
	GenesisFee         int64  `json:"genesisFee"`
	OutputValue        int64  `json:"outputValue"`
	Number             int64  `json:"number"`
	ContentLength      int64  `json:"contentLength"`
	Version            int64  `json:"version"`
	ContentSummary     string `json:"contentSummary,omitempty"`

	Payload        []byte `json:"-"`
	PayloadChannel string `json:"-"`
	PayloadState   string `json:"-"`
	PayloadError   string `json:"-"`
}

// PoolName maps a protocol path to the short pool name used in reports.
func PoolName(path string) string {
	switch path {
	case PathClaim:
		return "claim"
	case PathSubmission:
		return "submission"
	case PathVerify:
		return "verify"
	case PathTask:
		return "task"
	case PathTree:
		return "tree"
	case PathSpec:
		return "spec"
	case PathRelease:
		return "release"
	case PathAmend:
		return "amend"
	case PathChallenge:
		return "challenge"
	}
	return path
}

// Vote is one verify ballot with its §3.7 validity verdict.
type Vote struct {
	Pin           string `json:"pin"`
	Bot           string `json:"bot"`
	Verdict       string `json:"verdict"`
	Valid         bool   `json:"valid"`
	InvalidReason string `json:"invalid_reason,omitempty"`
	HasSub        bool   `json:"has_semantic_check"`
	HasFailReason bool   `json:"has_failreason"`
	// SubType is the observed JSON type of semantic_check ("string", "array",
	// ...). The engine's presence rule accepts any non-empty value, so an
	// array is a valid ballot; the type is reported so the looseness is visible.
	SubType      string `json:"semantic_check_type"`
	Height       int64  `json:"genesis_height"`
	TxIndex      int64  `json:"tx_index"`
	Timestamp    int64  `json:"timestamp"`
	PayloadState string `json:"payload_state"`
	PayloadError string `json:"payload_error,omitempty"`
}

// NodeSettlement is one task node's settled view.
type NodeSettlement struct {
	Node string `json:"node"`

	// Status is the metatask-replay v1.2.0 main-path verdict
	// (verified | claimed | open) — no TTL/reviewWindow expiry.
	Status string `json:"status"`
	// EffectiveStatus is the §4.7 guard verdict at the configured clock
	// (adds §4.2 claimTTL and §4.3 reviewWindow expiry). Equal to Status
	// whenever no window has expired.
	EffectiveStatus string `json:"effective_status"`
	EffectiveReason string `json:"effective_reason,omitempty"`

	Holder    string `json:"holder,omitempty"`
	HolderPin string `json:"holder_pin,omitempty"`

	Submission    string `json:"submission,omitempty"`
	SubmissionBy  string `json:"submission_author,omitempty"`
	ClaimIDOfSub  string `json:"submission_claimid,omitempty"`
	SubmissionRec int    `json:"submission_records"`

	// EffectivePass is the engine-parity field: votes counted toward the
	// verified verdict, reported as 0 while the node is not verified.
	EffectivePass int `json:"effective_pass"`
	// ValidPassTotal / ValidFailTotal count every valid ballot on the
	// effective submission, verified or not.
	ValidPassTotal int      `json:"valid_pass_total"`
	ValidFailTotal int      `json:"valid_fail_total"`
	PassRaw        int      `json:"pass_raw"`
	FailRaw        int      `json:"fail_raw"`
	InvalidVotes   int      `json:"invalid_votes"`
	VoterBots      int      `json:"voter_bots"`
	Voters         []string `json:"voters,omitempty"`
	Votes          []Vote   `json:"votes,omitempty"`

	ClaimRecords  int      `json:"claim_records"`
	IgnoredClaims []string `json:"ignored_claims,omitempty"`
	ReleasedPins  []string `json:"released_pins,omitempty"`
	IgnoredSubs   []string `json:"ignored_submissions,omitempty"`
}

// RejectedRecord is one pool record refused by the §〇 acceptance rules.
type RejectedRecord struct {
	Pin     string `json:"pin"`
	State   string `json:"state"`
	Reason  string `json:"reason"`
	Channel string `json:"channel,omitempty"`
}

// PoolReport is the per-pool aggregation (claim / submission / verify).
type PoolReport struct {
	Pool string `json:"pool"`

	ProtocolPath string `json:"protocol_path"`
	// Records is every enumerated record of this pool (whole pool, all task
	// roots — the indexer path is not task-scoped).
	Records int `json:"records"`
	// RecordsInScope is the subset belonging to the requested task root.
	RecordsInScope int `json:"records_in_scope"`
	// Effective is the subset that the settlement keeps in effect.
	Effective int `json:"effective"`
	// Ignored is enumerated, in scope, but not in effect (superseded claim,
	// submission riding a stale claim, ...).
	Ignored int `json:"ignored"`
	// Rejected is records whose payload failed the §〇 acceptance rules;
	// RejectedRecords carries the pin and the reason for each one.
	Rejected        int              `json:"rejected"`
	RejectedRecords []RejectedRecord `json:"rejected_records,omitempty"`

	Participants    int      `json:"participants"`
	ParticipantBots []string `json:"participant_bots,omitempty"`

	// Vote tallies (verify pool only).
	ValidPass  int `json:"valid_pass,omitempty"`
	ValidFail  int `json:"valid_fail,omitempty"`
	Invalid    int `json:"invalid_votes,omitempty"`
	BotsVoting int `json:"bots_voting,omitempty"`

	FirstTimestamp int64 `json:"first_timestamp,omitempty"`
	LastTimestamp  int64 `json:"last_timestamp,omitempty"`
	FirstSeen      int64 `json:"first_seen_time,omitempty"`
	LastSeen       int64 `json:"last_seen_time,omitempty"`
	GenesisFeeSum  int64 `json:"genesis_fee_sum"`
	OutputValueSum int64 `json:"output_value_sum"`
	Confirmed      int   `json:"confirmed"` // genesisHeight >= 0
	Mempool        int   `json:"mempool"`   // genesisHeight < 0
}

// PageEnumeration describes one path's enumeration run.
type PageEnumeration struct {
	ProtocolPath string `json:"protocol_path"`
	Pages        int    `json:"pages"`
	Records      int    `json:"records"`
	Unique       int    `json:"unique_records"`
	Duplicates   int    `json:"duplicates_dropped"`

	// Termination is the observed stop condition. Only
	// "empty_page_cursor_exhausted" is a complete enumeration; every other
	// value is an UNKNOWN/INCOMPLETE and must be surfaced, never accepted.
	Termination string `json:"termination"`
	// CursorExhausted is true only when the walk ended on an explicit empty
	// page (list null/empty) with an empty nextCursor.
	CursorExhausted bool `json:"cursor_exhausted"`
	Complete        bool `json:"complete"`

	PageHashes []string `json:"page_hashes"`
	PageSizes  []int    `json:"page_sizes"`
	Totals     []int64  `json:"reported_totals"`
	FetchedAt  string   `json:"fetched_at"`
}

// Enumeration is the full-pool enumeration report.
type Enumeration struct {
	Size     int                        `json:"page_size"`
	Paths    map[string]PageEnumeration `json:"paths"`
	Complete bool                       `json:"complete"`
	// Unresolved lists paths whose walk did not reach an explicit empty page.
	Unresolved []string `json:"unresolved,omitempty"`
	// PinSetSHA256 is sha256 over the sorted unique pin ids of all walked
	// paths — the reproducible identity of the frozen material.
	PinSetSHA256 string `json:"pin_set_sha256"`
}

// Meta carries the fee and timestamp metadata for the aggregation.
type Meta struct {
	GeneratedAt string `json:"generated_at"`
	Now         int64  `json:"now"`
	// FeeFields are the per-pin fee fields this read path actually exposes.
	// The indexer carries no transaction-fee field: genesisFee is the pin's
	// own genesis output fee and outputValue its output value, both reported
	// as sums so a reader can see they are not tx fees.
	FeeFields      []string `json:"fee_fields"`
	GenesisFeeSum  int64    `json:"genesis_fee_sum"`
	OutputValueSum int64    `json:"output_value_sum"`
	FeeNote        string   `json:"fee_note"`

	FirstTimestamp int64 `json:"first_timestamp,omitempty"`
	LastTimestamp  int64 `json:"last_timestamp,omitempty"`

	PinsEnumerated int `json:"pins_enumerated"`
	PinsRejected   int `json:"pins_rejected"`
	PinsUnreadable int `json:"pins_unreadable"`
}

// Aggregate is the response shape of the metaTask pool read path.
type Aggregate struct {
	TaskRoot    string           `json:"task_root"`
	TaskTitle   string           `json:"task_title,omitempty"`
	Quorum      int              `json:"quorum"`
	Policy      map[string]any   `json:"policy,omitempty"`
	TaskAuthor  string           `json:"task_author"`
	TreeID      string           `json:"tree_id,omitempty"`
	TreeNodes   int              `json:"tree_nodes"`
	AllVerified bool             `json:"all_verified"`
	Nodes       []NodeSettlement `json:"nodes"`
	Reputation  map[string]int   `json:"reputation"`
	// VerifyVotes is the engine-parity counter: valid last-per-(target,bot)
	// ballots across the WHOLE verify pool (not task-scoped), matching the
	// on-chain engine's `verify_votes`.
	VerifyVotes     int                   `json:"verify_votes"`
	VerifyVotesTask int                   `json:"verify_votes_task_scoped"`
	Pools           map[string]PoolReport `json:"pools"`
	Enumeration     Enumeration           `json:"enumeration"`
	Meta            Meta                  `json:"meta"`
}

// OrderKey is the replay ordering key: (genesisHeight, txIndex, seenTime,
// pinId). Unconfirmed pins (genesisHeight < 0) sort last, as in the engine.
func OrderKey(p *Pin) (int64, int64, int64, string) {
	height := p.GenesisHeight
	if height < 0 {
		height = int64(^uint64(0) >> 1)
	}
	tx := p.TxIndex
	if tx < 0 {
		tx = 0
	}
	ts := p.SeenTime
	if ts == 0 {
		ts = p.Timestamp
	}
	return height, tx, ts, p.ID
}

func orderLess(a, b *Pin) bool {
	ah, at, as, ai := OrderKey(a)
	bh, bt, bs, bi := OrderKey(b)
	if ah != bh {
		return ah < bh
	}
	if at != bt {
		return at < bt
	}
	if as != bs {
		return as < bs
	}
	return ai < bi
}

func pinTimestamp(p *Pin) int64 {
	if p.Timestamp != 0 {
		return p.Timestamp
	}
	return p.SeenTime
}

func nowRFC3339(now time.Time) string { return now.UTC().Format(time.RFC3339) }
