package preference

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// GET /api/preference/comparisons — the open comparison evidence API
// (metaso requirement R5). Defaults return only scoring-eligible, confirmed,
// non-test open comparisons; includeMempool / includeTest / includeExcluded
// open the respective diagnostic views.

const (
	defaultComparisonsSize = 20
	maxComparisonsSize     = 100
)

type comparisonsQuery struct {
	chainName       string
	category        string
	basis           string
	subject         string
	outcome         string // all | preferred | rejected
	includeMempool  bool
	includeTest     bool
	includeExcluded bool
	size            int
	cursor          string
}

type comparisonsResult struct {
	Items      []ComparisonEvidence `json:"items"`
	NextCursor string               `json:"nextCursor,omitempty"`
	HasMore    bool                 `json:"hasMore"`
}

func (a *Aggregator) handleComparisons(c *gin.Context) {
	query, err := parseComparisonsQuery(c)
	if err != nil {
		api.RespErr(c, 40000, err.Error())
		return
	}
	result, err := a.ListComparisons(query)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor") {
			api.RespErr(c, 40000, "invalid cursor")
			return
		}
		api.RespErr(c, 50000, "aggregation unavailable")
		return
	}
	api.RespSuccess(c, result)
}

var lowerHex64Query = regexp.MustCompile(`^[0-9a-f]{64}$`)

func parseComparisonsQuery(c *gin.Context) (comparisonsQuery, error) {
	query := comparisonsQuery{
		chainName: strings.ToLower(strings.TrimSpace(c.Query("chainName"))),
		category:  strings.TrimSpace(c.Query("category")),
		basis:     strings.TrimSpace(c.Query("basis")),
		subject:   strings.ToLower(strings.TrimSpace(c.Query("subject"))),
		outcome:   strings.TrimSpace(c.Query("outcome")),
		size:      defaultComparisonsSize,
		cursor:    strings.TrimSpace(c.Query("cursor")),
	}

	if query.chainName == "" {
		return query, paramErr("chainName is required")
	}
	if query.category != CategoryMotionVideo {
		return query, paramErr("category must be motion-video in this milestone")
	}
	switch query.basis {
	case "":
		query.basis = BasisOpen
	case BasisOpen:
	default:
		// The validated basis is reported honestly as unavailable until its
		// separately accepted aggregation workstream (R7) lands.
		return query, paramErr("preference_validated_basis_unavailable")
	}
	switch query.outcome {
	case "":
		query.outcome = "all"
	case "all", "preferred", "rejected":
	default:
		return query, paramErr("invalid outcome")
	}
	if query.subject != "" && !lowerHex64Query.MatchString(query.subject) {
		return query, paramErr("subject must be a 64 lowercase hex videoSha256")
	}
	if query.outcome != "all" && query.subject == "" {
		return query, paramErr("outcome other than all requires subject")
	}

	var err error
	if query.includeMempool, err = parseComparisonsFlag(c.Query("includeMempool")); err != nil {
		return query, err
	}
	if query.includeTest, err = parseComparisonsFlag(c.Query("includeTest")); err != nil {
		return query, err
	}
	if query.includeExcluded, err = parseComparisonsFlag(c.Query("includeExcluded")); err != nil {
		return query, err
	}

	if raw := strings.TrimSpace(c.Query("size")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return query, paramErr("invalid size")
		}
		query.size = n
	}
	if query.size <= 0 {
		query.size = defaultComparisonsSize
	}
	if query.size > maxComparisonsSize {
		query.size = maxComparisonsSize
	}
	return query, nil
}

func parseComparisonsFlag(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "", "0", "false", "FALSE":
		return false, nil
	case "1", "true", "TRUE":
		return true, nil
	default:
		return false, paramErr("invalid flag value")
	}
}

// ListComparisons filters and pages the stored feedback evidence.
func (a *Aggregator) ListComparisons(query comparisonsQuery) (*comparisonsResult, error) {
	if a.store == nil {
		return nil, ErrStoreNotInitialised
	}
	fingerprint := comparisonsCursorFingerprint(query)
	offset, err := decodeComparisonsCursor(query.cursor, fingerprint)
	if err != nil {
		return nil, err
	}

	records, err := a.listFeedbackRecords()
	if err != nil {
		return nil, err
	}

	items := make([]ComparisonEvidence, 0)
	for _, rec := range records {
		if rec.ChainName != query.chainName {
			continue
		}
		if rec.Status != StatusValid {
			continue
		}
		if rec.IsTest && !query.includeTest {
			continue
		}
		if rec.IsMempool && !query.includeMempool {
			continue
		}
		for _, item := range rec.Items {
			if item.Excluded && !query.includeExcluded {
				continue
			}
			evidence := comparisonEvidenceFromRecord(rec, item)
			if query.subject != "" {
				outcome, involves := subjectOutcome(evidence, query.subject)
				if !involves {
					continue
				}
				if query.outcome != "all" && query.outcome != outcome {
					continue
				}
				evidence.Subject = query.subject
				evidence.Outcome = outcome
			}
			items = append(items, evidence)
		}
	}

	if offset > len(items) {
		offset = len(items)
	}
	page := items[offset:]
	hasMore := len(page) > query.size
	if hasMore {
		page = page[:query.size]
	}
	result := &comparisonsResult{Items: page, HasMore: hasMore}
	if hasMore {
		result.NextCursor = encodeComparisonsCursor(offset+query.size, fingerprint)
	}
	return result, nil
}

func comparisonEvidenceFromRecord(rec FeedbackRecord, item FeedbackComparison) ComparisonEvidence {
	evidence := ComparisonEvidence{
		FeedbackPinId:      rec.FeedbackPinId,
		ChainName:          rec.ChainName,
		Basis:              BasisOpen,
		IsMempool:          rec.IsMempool,
		ObservedAt:         rec.ObservedAt,
		A:                  item.A,
		B:                  item.B,
		Pick:               item.Pick,
		Reason:             item.Reason,
		Dims:               item.Dims,
		SignerGlobalMetaId: rec.SignerGlobalMetaId,
	}
	if !rec.IsMempool {
		confirmedAt := rec.ConfirmedAt
		evidence.ConfirmedAt = &confirmedAt
	}
	client := rec.Client
	evidence.Client = &client
	if item.Excluded {
		evidence.Excluded = true
		evidence.ExclusionReason = item.ExclusionReason
	}
	return evidence
}

// subjectOutcome computes the subject-relative outcome of one comparison:
// preferred when the subject's side won, rejected when it lost; ties belong
// to the all view only.
func subjectOutcome(evidence ComparisonEvidence, subject string) (outcome string, involves bool) {
	if evidence.A.VideoSha256 != subject && evidence.B.VideoSha256 != subject {
		return "", false
	}
	switch evidence.Pick {
	case "tie":
		return "tie", true
	case "a":
		if evidence.A.VideoSha256 == subject {
			return "preferred", true
		}
		return "rejected", true
	case "b":
		if evidence.B.VideoSha256 == subject {
			return "preferred", true
		}
		return "rejected", true
	}
	return "", false
}

func comparisonsCursorFingerprint(query comparisonsQuery) string {
	parts := []string{
		"v1",
		query.chainName,
		query.category,
		query.basis,
		query.subject,
		query.outcome,
		strconv.FormatBool(query.includeMempool),
		strconv.FormatBool(query.includeTest),
		strconv.FormatBool(query.includeExcluded),
		ScoreVersion,
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x1f")))
	return hex.EncodeToString(digest[:])
}

type comparisonsCursorPayload struct {
	Offset      int    `json:"o"`
	Fingerprint string `json:"f,omitempty"`
}

func encodeComparisonsCursor(offset int, fingerprint string) string {
	raw, _ := json.Marshal(comparisonsCursorPayload{Offset: offset, Fingerprint: fingerprint})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeComparisonsCursor(cursor, fingerprint string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	var p comparisonsCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	if p.Offset < 0 || p.Fingerprint != fingerprint {
		return 0, fmt.Errorf("invalid cursor")
	}
	return p.Offset, nil
}

type paramError string

func paramErr(message string) error { return paramError(message) }

func (e paramError) Error() string { return string(e) }
