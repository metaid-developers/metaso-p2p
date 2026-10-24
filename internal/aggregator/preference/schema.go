package preference

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Schema validation of the comparison-batch payload (shared contract §4.2).
// Validation is atomic: the first violation marks the whole pin malformed;
// indexers never salvage part of a batch.

var (
	categoryTagPattern = regexp.MustCompile(`^[a-z0-9-]+(:[a-z0-9-]+)?$`)
	dimIDPattern       = regexp.MustCompile(`^[a-z][a-z0-9_]{0,23}$`)
	lowerHex64Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	clientModePattern  = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

// parseComparisonBatch validates the payload atomically and returns the
// normalised categories, client and items. Any violation returns an error
// describing the first failure; the caller stores the pin as malformed.
func parseComparisonBatch(body []byte) (categories []string, client FeedbackClient, items []FeedbackComparison, err error) {
	var payload struct {
		Category []string `json:"category"`
		Kind     string   `json:"kind"`
		Client   struct {
			Artifact string `json:"artifact"`
			Mode     string `json:"mode"`
		} `json:"client"`
		Items []struct {
			A      ArtifactRef       `json:"a"`
			B      ArtifactRef       `json:"b"`
			Pick   string            `json:"pick"`
			Reason string            `json:"reason"`
			Dims   map[string]string `json:"dims"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, FeedbackClient{}, nil, fmt.Errorf("payload is not a JSON object: %w", err)
	}

	if len(payload.Category) < 1 || len(payload.Category) > maxCategoriesPerBatch {
		return nil, FeedbackClient{}, nil, fmt.Errorf("category must contain 1-%d tags", maxCategoriesPerBatch)
	}
	categories = make([]string, 0, len(payload.Category))
	for _, tag := range payload.Category {
		tag = strings.TrimSpace(tag)
		if !categoryTagPattern.MatchString(tag) {
			return nil, FeedbackClient{}, nil, fmt.Errorf("category tag %q fails the tag pattern", tag)
		}
		categories = append(categories, tag)
	}

	if payload.Kind != "comparison-batch" {
		return nil, FeedbackClient{}, nil, fmt.Errorf("kind must be comparison-batch, got %q", payload.Kind)
	}

	clientArtifact := strings.TrimSpace(payload.Client.Artifact)
	if !isMetaAppURI(clientArtifact) {
		return nil, FeedbackClient{}, nil, fmt.Errorf("client.artifact must be a metaapp:// root reference")
	}
	clientMode := strings.TrimSpace(payload.Client.Mode)
	if clientMode != "" && !clientModePattern.MatchString(clientMode) {
		return nil, FeedbackClient{}, nil, fmt.Errorf("client.mode %q is not a lowercase interface label", clientMode)
	}
	client = FeedbackClient{Artifact: clientArtifact, Mode: clientMode}

	if len(payload.Items) < minItemsPerBatch || len(payload.Items) > maxItemsPerBatch {
		return nil, FeedbackClient{}, nil, fmt.Errorf("items must contain 1-%d comparisons, got %d", maxItemsPerBatch, len(payload.Items))
	}

	items = make([]FeedbackComparison, 0, len(payload.Items))
	for i, raw := range payload.Items {
		item, err := validateComparisonItem(raw.A, raw.B, raw.Pick, raw.Reason, raw.Dims)
		if err != nil {
			return nil, FeedbackClient{}, nil, fmt.Errorf("item %d: %w", i, err)
		}
		items = append(items, item)
	}
	return categories, client, items, nil
}

func validateComparisonItem(a, b ArtifactRef, pick, reason string, dims map[string]string) (FeedbackComparison, error) {
	if err := validateArtifactRef(a, "a"); err != nil {
		return FeedbackComparison{}, err
	}
	if err := validateArtifactRef(b, "b"); err != nil {
		return FeedbackComparison{}, err
	}
	if a.ChainName != b.ChainName {
		return FeedbackComparison{}, fmt.Errorf("a and b must share one chainName")
	}
	if a.VideoSha256 == b.VideoSha256 {
		return FeedbackComparison{}, fmt.Errorf("a pair with identical videoSha256 values is not a visual comparison")
	}
	switch pick {
	case "a", "b", "tie":
	default:
		return FeedbackComparison{}, fmt.Errorf("pick must be a, b or tie, got %q", pick)
	}
	reason = strings.TrimSpace(reason)
	if len([]rune(reason)) > maxReasonRunes {
		return FeedbackComparison{}, fmt.Errorf("reason exceeds %d characters", maxReasonRunes)
	}
	if len(dims) > maxDimsPerItem {
		return FeedbackComparison{}, fmt.Errorf("dims exceeds %d entries", maxDimsPerItem)
	}
	for id, value := range dims {
		if !dimIDPattern.MatchString(id) {
			return FeedbackComparison{}, fmt.Errorf("dims id %q fails the dimension pattern", id)
		}
		switch value {
		case "a", "b", "tie":
		default:
			return FeedbackComparison{}, fmt.Errorf("dims[%s] must be a, b or tie, got %q", id, value)
		}
	}
	return FeedbackComparison{A: a, B: b, Pick: pick, Reason: reason, Dims: dims}, nil
}

func validateArtifactRef(ref ArtifactRef, side string) error {
	if strings.TrimSpace(ref.ChainName) == "" {
		return fmt.Errorf("%s.chainName is required", side)
	}
	if !isMetaAppURI(ref.App) {
		return fmt.Errorf("%s.app must be a metaapp:// root reference", side)
	}
	if !isMetaAppURI(ref.Version) {
		return fmt.Errorf("%s.version must be a metaapp:// version reference", side)
	}
	if !isMetaFileURI(ref.Video) {
		return fmt.Errorf("%s.video must be a metafile:// reference", side)
	}
	if !lowerHex64Pattern.MatchString(ref.VideoSha256) {
		return fmt.Errorf("%s.videoSha256 must be 64 lowercase hex characters", side)
	}
	return nil
}

func isMetaAppURI(ref string) bool {
	return strings.HasPrefix(ref, "metaapp://") && len(strings.TrimPrefix(ref, "metaapp://")) > 0
}

func isMetaFileURI(ref string) bool {
	return strings.HasPrefix(ref, "metafile://") && len(strings.TrimPrefix(ref, "metafile://")) > 0
}

// hasMotionVideoCategory reports whether the batch belongs to the v1 Motion
// Video projection; other categories are unsupported, not malformed.
func hasMotionVideoCategory(categories []string) bool {
	for _, category := range categories {
		if category == CategoryMotionVideo {
			return true
		}
	}
	return false
}

// isTestBatch reports whether any category tag marks non-production smoke
// evidence (test:...). Test batches never enter production scores,
// preference ordering or default learning evidence.
func isTestBatch(categories []string) bool {
	for _, category := range categories {
		if strings.HasPrefix(category, "test:") {
			return true
		}
	}
	return false
}
