package metaweb

import (
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/qa"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/socialcontent"
)

// engagement_adapters bridge the concrete qa / socialcontent aggregators
// onto the metaweb engagement lookup interfaces (composition wired in
// cmd/metaso-p2p/main.go, mirroring the userinfo profile-namer adapter).

type buzzEngagementAdapter struct {
	agg *socialcontent.Aggregator
}

// NewBuzzEngagementLookup adapts the socialcontent aggregator onto
// BuzzEngagementLookup.
func NewBuzzEngagementLookup(agg *socialcontent.Aggregator) BuzzEngagementLookup {
	return buzzEngagementAdapter{agg: agg}
}

func (a buzzEngagementAdapter) BuzzEngagement(sourcePinId string) (likeCount, commentCount int, ok bool) {
	if a.agg == nil {
		return 0, 0, false
	}
	return a.agg.Engagement(sourcePinId)
}

type qaEngagementAdapter struct {
	agg *qa.Aggregator
}

// NewQAEngagementLookup adapts the qa aggregator onto QAEngagementLookup.
func NewQAEngagementLookup(agg *qa.Aggregator) QAEngagementLookup {
	return qaEngagementAdapter{agg: agg}
}

func (a qaEngagementAdapter) QuestionEngagement(sourcePinId string) (likeCount, dislikeCount, commentCount, answerCount int, ok bool) {
	if a.agg == nil {
		return 0, 0, 0, 0, false
	}
	return a.agg.QuestionEngagement(sourcePinId)
}

func (a qaEngagementAdapter) AnswerEngagement(sourcePinId string) (likeCount, dislikeCount, commentCount int, ok bool) {
	if a.agg == nil {
		return 0, 0, 0, false
	}
	return a.agg.AnswerEngagement(sourcePinId)
}
