// Package preference indexes lightweight open comparison-feedback pins under
// /protocols/preference/feedback and serves the chain-scoped open-preference
// read models (scores, evidence listing) for Motion Video. Open evidence is
// kept in its own "open:" storage namespace; validated (sealed-round)
// aggregation is a separate later workstream (R7) and is never blended into
// this projection.
package preference

import "github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"

const (
	// Namespace is the Pebble storage namespace of this aggregator.
	Namespace = "preference"
	// PathPreferenceFeedback is the lightweight feedback protocol path.
	PathPreferenceFeedback = "/protocols/preference/feedback"
	// ScoreVersion identifies the open scoring policy (metaso requirement
	// R3); shared with the publishedcontent list projection.
	ScoreVersion = publishedcontent.OpenPreferenceScoreVersion
	// CategoryMotionVideo is the only category the v1 projection accepts.
	CategoryMotionVideo = "motion-video"

	// Feedback states of a stored batch record.
	StatusValid       = "valid"
	StatusUnsupported = "unsupported" // schema-valid but no motion-video category
	StatusMalformed   = "malformed"   // failed the atomic schema check

	// Basis identifies the evidence projection; v1 emits only open.
	BasisOpen = "open"

	// Open score states.
	StateUnjudged    = "unjudged"
	StateEmerging    = "emerging"
	StateEstablished = "established"

	// Comparison batch size bounds (shared contract §4.2).
	minItemsPerBatch = 1
	maxItemsPerBatch = 28

	maxCategoriesPerBatch = 8
	maxReasonRunes        = 1000
	maxDimsPerItem        = 6

	establishedJudgeThreshold = 3
)

// ArtifactRef is the exact artifact reference of the shared contract §3.
// chainName scopes resolution and aggregation; app is the stable create root,
// version the exact package the judge saw, video the metafile reference and
// videoSha256 the chain-scoped video identity preference attaches to.
type ArtifactRef struct {
	ChainName   string `json:"chainName"`
	App         string `json:"app"`
	Version     string `json:"version"`
	Video       string `json:"video"`
	VideoSha256 string `json:"videoSha256"`
}

// FeedbackClient records the submitting client (shared contract §4.2).
type FeedbackClient struct {
	Artifact string `json:"artifact"`
	Mode     string `json:"mode,omitempty"`
}

// FeedbackComparison is one item of a comparison batch, together with its
// resolution state against the indexed MetaApp manifests. Schema-invalid
// batches never reach this structure: one invalid item invalidates the whole
// pin, which is then stored as malformed for audit only.
type FeedbackComparison struct {
	A      ArtifactRef       `json:"a"`
	B      ArtifactRef       `json:"b"`
	Pick   string            `json:"pick"`
	Reason string            `json:"reason,omitempty"`
	Dims   map[string]string `json:"dims,omitempty"`

	// Excluded items are schema-valid but not scoring-eligible (author
	// conflict, unresolvable artifact, manifest mismatch). They stay
	// queryable in the includeExcluded diagnostics view.
	Excluded        bool   `json:"excluded,omitempty"`
	ExclusionReason string `json:"exclusionReason,omitempty"`

	// ManifestUnverified marks comparisons whose ArtifactRef version is not
	// the current folded package: MetaSo retains only the folded manifest,
	// so historical-version video/hash fields are recorded as stated and
	// were never checked against a manifest claim. Honest evidence labelling
	// required by the acceptance review (deviation B).
	ManifestUnverified bool `json:"manifestUnverified,omitempty"`
}

// Exclusion reasons recorded on excluded comparisons.
const (
	ExclusionAuthorConflict    = "author-conflict"
	ExclusionAppNotFound       = "app-not-found"
	ExclusionVersionOutsideApp = "version-outside-app"
	ExclusionArtifactMismatch  = "artifact-mismatch"
	ExclusionChainMismatch     = "chain-mismatch"
)

// FeedbackRecord is the stored projection of one feedback pin (all pins are
// retained for audit; only confirmed valid supported non-test records feed
// the open scoring state).
type FeedbackRecord struct {
	FeedbackPinId      string `json:"feedbackPinId"`
	ChainName          string `json:"chainName"`
	SignerGlobalMetaId string `json:"signerGlobalMetaId"`
	SignerMetaId       string `json:"signerMetaId,omitempty"`
	SignerAddress      string `json:"signerAddress,omitempty"`

	IsMempool bool `json:"isMempool,omitempty"`

	// Confirmed chain order (height, then timestamp, then pin id).
	ConfirmedHeight int64 `json:"confirmedHeight,omitempty"`
	ConfirmedAt     int64 `json:"confirmedAt"`
	// ObservedAt is the pin timestamp as first relayed (mempool view).
	ObservedAt int64 `json:"observedAt"`

	Client     FeedbackClient `json:"client"`
	Categories []string       `json:"categories"`
	IsTest     bool           `json:"isTest,omitempty"`

	Status        string               `json:"status"`
	InvalidReason string               `json:"invalidReason,omitempty"`
	Items         []FeedbackComparison `json:"items,omitempty"`
}

// ComparisonEvidence is the wire item of GET /api/preference/comparisons.
type ComparisonEvidence struct {
	FeedbackPinId string `json:"feedbackPinId"`
	ChainName     string `json:"chainName"`
	Basis         string `json:"basis"`
	IsMempool     bool   `json:"isMempool"`
	ConfirmedAt   *int64 `json:"confirmedAt"`
	ObservedAt    int64  `json:"observedAt"`

	A      ArtifactRef       `json:"a"`
	B      ArtifactRef       `json:"b"`
	Pick   string            `json:"pick"`
	Reason string            `json:"reason,omitempty"`
	Dims   map[string]string `json:"dims,omitempty"`

	SignerGlobalMetaId string          `json:"signerGlobalMetaId"`
	Client             *FeedbackClient `json:"client,omitempty"`

	// Subject-relative outcome, present only when a subject filter is used.
	Subject string `json:"subject,omitempty"`
	Outcome string `json:"outcome,omitempty"`

	// Present only in the includeExcluded diagnostics view.
	Excluded        bool   `json:"excluded,omitempty"`
	ExclusionReason string `json:"exclusionReason,omitempty"`

	// True when the comparison involves a historical (non-current) package
	// version whose manifest is not retained, so its video/hash fields are
	// recorded as stated rather than manifest-checked.
	ManifestUnverified bool `json:"manifestUnverified,omitempty"`
}

// OpenSummary is the open-evidence score summary of one (chainName,
// videoSha256). Score is null for unjudged videos — never a zero-quality
// value.
type OpenSummary struct {
	Score          *float64 `json:"score"`
	ScoreVersion   string   `json:"scoreVersion"`
	State          string   `json:"state"`
	Comparisons    int      `json:"comparisons"`
	Judges         int      `json:"judges"`
	LastComparedAt *int64   `json:"lastComparedAt"`
}

// UnjudgedOpenSummary is the exact zero-evidence summary (measured counts,
// not defaults for missing data).
func UnjudgedOpenSummary() OpenSummary {
	return OpenSummary{
		Score:        nil,
		ScoreVersion: ScoreVersion,
		State:        StateUnjudged,
		Comparisons:  0,
		Judges:       0,
	}
}
