package publishedcontent

import (
	"regexp"
	"strconv"
	"strings"
)

// MetaAppMotionVideo is the additive Motion Video playback projection on
// /api/metaapp list and detail items (shared contract §2 / metaso R1). Values
// come straight from the current folded manifest's `metadata` block; missing
// values stay null so "unknown" is never collapsed into false or zero.
// `declared` in Learning.Status is a manifest-completeness result only — it is
// never a package/playback verification, and `verified` is reserved for a
// later contract and never emitted here.
type MetaAppMotionVideo struct {
	VideoFile    string                     `json:"videoFile"`
	VideoURL     string                     `json:"videoUrl,omitempty"`
	VideoSha256  string                     `json:"videoSha256,omitempty"`
	DurationSec  *float64                   `json:"durationSec"`
	FPS          *float64                   `json:"fps"`
	Frames       *int64                     `json:"frames"`
	Resolution   string                     `json:"resolution,omitempty"`
	HasAudio     *bool                      `json:"hasAudio"`
	LoudnessLufs *float64                   `json:"loudnessLufs"`
	Engine       string                     `json:"engine,omitempty"`
	Style        string                     `json:"style,omitempty"`
	Learning     MetaAppLearningDeclaration `json:"learning"`
}

// MetaAppLearningDeclaration reports the publisher-declared learning metadata.
// Status is `declared` exactly when the manifest carries every playback
// declaration named in the shared contract §2.2 (series, videoFile,
// videoSha256, durationSec, fps, resolution, engine, style),
// metadata.sourceIncluded === true and the root manifest prompt is non-empty;
// otherwise `incomplete`.
type MetaAppLearningDeclaration struct {
	SourceIncluded bool   `json:"sourceIncluded"`
	HasPrompt      bool   `json:"hasPrompt"`
	Status         string `json:"status"`
}

const (
	LearningStatusDeclared   = "declared"
	LearningStatusIncomplete = "incomplete"

	motionVideoSeries = "motion-video"
	motionVideoTag    = "motion-video"
)

var lowerHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// metaAppMotionVideoFromPayload projects the motionVideo block from the folded
// manifest payload. It returns nil for non-Motion-Video apps (no series
// declaration and no motion-video tag).
func metaAppMotionVideoFromPayload(payload map[string]any, tags []string) *MetaAppMotionVideo {
	meta := payloadMap(payload, "metadata")
	if meta == nil {
		meta = map[string]any{}
	}
	if !isMotionVideoManifest(meta, tags) {
		return nil
	}

	learning := metaAppLearningDeclaration(payload, meta)
	return &MetaAppMotionVideo{
		VideoFile:    payloadString(meta, "videoFile"),
		VideoURL:     payloadString(meta, "videoUrl"),
		VideoSha256:  payloadString(meta, "videoSha256"),
		DurationSec:  payloadFloat64Ptr(meta, "durationSec"),
		FPS:          payloadFloat64Ptr(meta, "fps"),
		Frames:       payloadInt64Ptr(meta, "frames"),
		Resolution:   payloadString(meta, "resolution"),
		HasAudio:     payloadBoolPtr(meta, "hasAudio"),
		LoudnessLufs: payloadFloat64Ptr(meta, "loudnessLufs"),
		Engine:       payloadString(meta, "engine"),
		Style:        payloadString(meta, "style"),
		Learning:     learning,
	}
}

// isMotionVideoManifest matches apps that self-identify as Motion Video via
// metadata.series or the motion-video tag.
func isMotionVideoManifest(meta map[string]any, tags []string) bool {
	if payloadString(meta, "series") == motionVideoSeries {
		return true
	}
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), motionVideoTag) {
			return true
		}
	}
	return false
}

// metaAppLearningDeclaration applies the exact manifest-completeness rule:
// every §2.2 playback declaration present and well-formed (series equals
// motion-video, videoFile is a metafile:// reference, videoSha256 is 64
// lowercase hex, positive durationSec/fps, non-empty resolution/engine/style),
// metadata.sourceIncluded === true, and a non-empty trimmed root prompt.
func metaAppLearningDeclaration(payload, meta map[string]any) MetaAppLearningDeclaration {
	sourceIncluded := payloadBool(meta, "sourceIncluded")
	hasPrompt := strings.TrimSpace(payloadString(payload, "prompt")) != ""

	declared := payloadString(meta, "series") == motionVideoSeries &&
		isMetaFileReference(payloadString(meta, "videoFile")) &&
		lowerHex64.MatchString(payloadString(meta, "videoSha256")) &&
		payloadPositiveFloat(meta, "durationSec") &&
		payloadPositiveFloat(meta, "fps") &&
		payloadString(meta, "resolution") != "" &&
		payloadString(meta, "engine") != "" &&
		payloadString(meta, "style") != "" &&
		sourceIncluded && hasPrompt

	status := LearningStatusIncomplete
	if declared {
		status = LearningStatusDeclared
	}
	return MetaAppLearningDeclaration{
		SourceIncluded: sourceIncluded,
		HasPrompt:      hasPrompt,
		Status:         status,
	}
}

// isMetaFileReference accepts metafile://<pinId> with an optional filename
// extension; the extension carries no validity meaning (contract §2.2).
func isMetaFileReference(ref string) bool {
	rest := strings.TrimPrefix(strings.TrimSpace(ref), "metafile://")
	return rest != "" && !strings.ContainsAny(rest, " \t")
}

// IsMotionVideoItem reports whether the projected item is a Motion Video app.
func (m *MetaAppMotionVideo) IsMotionVideoItem() bool { return m != nil }

func payloadMap(payload map[string]any, key string) map[string]any {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return nil
	}
	if m, ok := raw.(map[string]any); ok {
		return m
	}
	return nil
}

func payloadFloat64Ptr(payload map[string]any, key string) *float64 {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case float64:
		value := v
		return &value
	case string:
		return parseFloat64Ptr(strings.TrimSpace(v))
	}
	return nil
}

func payloadInt64Ptr(payload map[string]any, key string) *int64 {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case float64:
		value := int64(v)
		return &value
	case string:
		if f := parseFloat64Ptr(strings.TrimSpace(v)); f != nil {
			value := int64(*f)
			return &value
		}
	}
	return nil
}

// payloadBoolPtr preserves the missing/unknown distinction: absent fields stay
// nil instead of collapsing to false.
func payloadBoolPtr(payload map[string]any, key string) *bool {
	raw, ok := payload[key]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case bool:
		value := v
		return &value
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil
		}
		value := strings.EqualFold(trimmed, "true")
		return &value
	}
	return nil
}

func payloadPositiveFloat(payload map[string]any, key string) bool {
	value := payloadFloat64Ptr(payload, key)
	return value != nil && *value > 0
}

func parseFloat64Ptr(raw string) *float64 {
	if raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &v
}
