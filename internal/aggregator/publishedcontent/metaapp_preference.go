package publishedcontent

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// OpenPreferenceScoreVersion identifies the open-preference scoring policy
// (metaso requirement R3). Kept here so the list projection and the
// preference aggregator share one source of truth.
const OpenPreferenceScoreVersion = "motion-video-open-pref-v1"

// Preference score / basis states surfaced on list items.
const (
	PreferenceStateUnjudged    = "unjudged"
	PreferenceStateEmerging    = "emerging"
	PreferenceStateEstablished = "established"

	PreferenceBasisOpen      = "open"
	PreferenceBasisValidated = "validated"

	// LearningStatusDeclared / LearningStatusIncomplete are defined with the
	// motionVideo projection in metaapp_motionvideo.go.
)

// MetaAppPreferenceOpen is the open-evidence summary of one video identity.
// Score is null when the video is unjudged — never a zero-quality value.
type MetaAppPreferenceOpen struct {
	Score          *float64 `json:"score"`
	ScoreVersion   string   `json:"scoreVersion"`
	State          string   `json:"state"`
	Comparisons    int      `json:"comparisons"`
	Judges         int      `json:"judges"`
	LastComparedAt *int64   `json:"lastComparedAt"`
}

// MetaAppPreferenceValidated is the validated-evidence summary. Before the
// validated aggregation workstream (R7) it is always exactly
// {score: null, state: "unavailable"} — never a zero masquerading as data.
type MetaAppPreferenceValidated struct {
	Score *float64 `json:"score"`
	State string   `json:"state"`
}

// MetaAppPreferenceBlock is the evidence block attached to items of a
// preference-sorted listing (metaso requirement R4).
type MetaAppPreferenceBlock struct {
	Open      MetaAppPreferenceOpen      `json:"open"`
	Validated MetaAppPreferenceValidated `json:"validated"`
}

// UnjudgedMetaAppPreferenceOpen returns the exact zero-evidence open summary.
func UnjudgedMetaAppPreferenceOpen() MetaAppPreferenceOpen {
	return MetaAppPreferenceOpen{
		Score:        nil,
		ScoreVersion: OpenPreferenceScoreVersion,
		State:        PreferenceStateUnjudged,
		Comparisons:  0,
		Judges:       0,
	}
}

// UnavailableMetaAppPreferenceValidated returns the pre-R7 validated block.
func UnavailableMetaAppPreferenceValidated() MetaAppPreferenceValidated {
	return MetaAppPreferenceValidated{Score: nil, State: "unavailable"}
}

// PreferenceLookup supplies open-preference summaries to the MetaApp list
// projection; implemented by the preference aggregator and wired in main.
// A nil lookup degrades every item to the unjudged summary.
type PreferenceLookup interface {
	OpenPreferenceSummary(chainName, videoSha256 string) MetaAppPreferenceOpen
}

// SetPreferenceLookup wires the open-preference score source. Without it the
// preference listing fails explicitly (errPreferenceLookupUnavailable) — it
// never silently degrades items to unjudged summaries.
func (a *Aggregator) SetPreferenceLookup(lookup PreferenceLookup) {
	a.preferenceLookup = lookup
}

var errPreferenceLookupUnavailable = metaAppParamErr("preference summaries unavailable")

func (a *Aggregator) metaAppPreferenceBlock(chainName, videoSha256 string) (MetaAppPreferenceBlock, error) {
	if a.preferenceLookup == nil {
		return MetaAppPreferenceBlock{}, errPreferenceLookupUnavailable
	}
	open := a.preferenceLookup.OpenPreferenceSummary(strings.ToLower(strings.TrimSpace(chainName)), strings.TrimSpace(videoSha256))
	return MetaAppPreferenceBlock{Open: open, Validated: UnavailableMetaAppPreferenceValidated()}, nil
}

// MetaAppSnapshot is the resolved view of a MetaApp version chain consumed
// by the preference aggregator for ArtifactRef validation: stable root,
// current folded version, publisher identity and the current manifest
// payload. It reads the live record store, so it works for both the live
// indexer and backfill replays.
type MetaAppSnapshot struct {
	RootPinId             string
	CurrentPinId          string
	PublisherGlobalMetaId string
	PublisherMetaId       string
	PublisherAddress      string
	// Creator* carry the original create-pin identity (empty on records
	// stored before the field existed). Author-conflict checks must test
	// both identities: the folded publisher tracks the latest modifier.
	CreatorGlobalMetaId string
	CreatorMetaId       string
	CreatorAddress      string
	ManifestPayload     map[string]any
}

// ResolveMetaAppSnapshot resolves any pin id of a MetaApp version chain
// (root, mid-chain version, or current) to its chain snapshot. Returns
// ok=false when the pin does not resolve to an indexed metaapp record on the
// given chain.
func (a *Aggregator) ResolveMetaAppSnapshot(chainName, anyPinId string) (*MetaAppSnapshot, bool) {
	rec, err := a.loadRecordByAnyPinId(strings.ToLower(strings.TrimSpace(chainName)), PathMetaApp, strings.TrimSpace(anyPinId))
	if err != nil || rec == nil {
		return nil, false
	}
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return &MetaAppSnapshot{
		RootPinId:             rec.SourcePinId,
		CurrentPinId:          currentPinId,
		PublisherGlobalMetaId: rec.PublisherGlobalMetaId,
		PublisherMetaId:       rec.PublisherMetaId,
		PublisherAddress:      rec.PublisherAddress,
		CreatorGlobalMetaId:   rec.CreatorGlobalMetaId,
		CreatorMetaId:         rec.CreatorMetaId,
		CreatorAddress:        rec.CreatorAddress,
		ManifestPayload:       rec.PayloadJSON,
	}, true
}

// preferenceCursorFingerprint binds the preference cursor to the full query:
// chain, basis, filters and score version, so changing any of them
// invalidates an outstanding cursor (metaso requirement R4).
func preferenceCursorFingerprint(params MetaAppListParams) string {
	parts := []string{
		"v1",
		params.ChainName,
		params.Basis,
		params.Order,
		params.Keyword,
		params.Tag,
		params.Runtime,
		params.Publisher,
		strconv.FormatInt(params.Since, 10),
		strconv.FormatInt(params.Until, 10),
		strconv.FormatBool(params.IncludeDisabled),
		strconv.Itoa(params.MinJudges),
		params.Learning,
		OpenPreferenceScoreVersion,
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(digest[:])
}
