package qa

// v2 endpoints of the Q&A contract (docs/specs/2026-09-07-metaweb-qa-comments-author-api.md):
// R5 comment threads on Q&A pins and R6 cross-question answer listing.

import (
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

const (
	defaultCommentSize = 20
	maxCommentSize     = 50
)

// ---------------------------------------------------------------------------
// GET /api/qa/pins/:pinId/comments (R5)
// ---------------------------------------------------------------------------

type commentListParams struct {
	oldest bool
	size   int
	offset int
}

// handlePinComments serves the flat comment thread of a Q&A pin. pinId may be
// any version of a question, or an answer pin; comments on simplebuzz pins
// are never served here.
func (a *Aggregator) handlePinComments(c *gin.Context) {
	pinId := strings.TrimSpace(c.Param("pinId"))
	if !pinIDPattern.MatchString(pinId) {
		api.RespErr(c, codeInvalidParam, "malformed pinId")
		return
	}
	params, errMsg := parseCommentListParams(c)
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	if a.store == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	target, ok := a.resolveCommentTarget(c, pinId)
	if !ok {
		return
	}

	comments := make([]*CommentRecord, 0)
	prefix := commentIndexPrefix(target.sourcePinId)
	if err := a.store.ScanPrefix(Namespace, prefix, func(key, value []byte) error {
		commentSrc := strings.TrimPrefix(string(key), string(prefix))
		if at := strings.LastIndex(commentSrc, ":"); at >= 0 {
			commentSrc = commentSrc[at+1:]
		}
		if commentSrc == "" {
			return nil
		}
		// The ct value carries the comment pin's own chain (a comment may
		// live on a different chain than its target).
		comment, err := a.loadComment(strings.TrimSpace(string(value)), commentSrc)
		if err != nil {
			return err
		}
		if comment != nil && !comment.Hidden {
			comments = append(comments, comment)
		}
		return nil
	}); err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	// Ascending key order is newest first (inverted timestamps, pinId tie);
	// oldest is the exact reverse for chronological thread reading.
	if params.oldest {
		for i, j := 0, len(comments)-1; i < j; i, j = i+1, j-1 {
			comments[i], comments[j] = comments[j], comments[i]
		}
	}

	offset := params.offset
	if offset > len(comments) {
		offset = len(comments)
	}
	page := comments[offset:]
	hasMore := len(page) > params.size
	if hasMore {
		page = page[:params.size]
	}
	items := make([]CommentItem, 0, len(page))
	for _, comment := range page {
		items = append(items, a.commentItemFromRecord(comment))
	}
	a.respondList(c, commentItemsToAny(items), hasMore, offset, params.size)
}

func commentItemsToAny(items []CommentItem) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

// resolveCommentTarget validates the :pinId path parameter as a visible Q&A
// pin (question or answer) and returns the locator of its stable record,
// writing the error response itself. An answer whose parent question is
// revoked — or de-indexed by the R7 title rule — is not visible anywhere,
// comment threads included.
func (a *Aggregator) resolveCommentTarget(c *gin.Context, pinId string) (recordLocator, bool) {
	locator, ok := a.lookupLocator(pinId)
	if !ok || !locator.isQATarget() {
		api.RespErr(c, codeNotFound, "Q&A pin not found")
		return recordLocator{}, false
	}
	switch locator.kind {
	case "q":
		rec, err := a.loadQuestion(locator.chainName, locator.sourcePinId)
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return recordLocator{}, false
		}
		if !questionVisible(rec) {
			api.RespErr(c, codeNotFound, "Q&A pin not found")
			return recordLocator{}, false
		}
	case "a":
		rec, err := a.loadAnswer(locator.chainName, locator.sourcePinId)
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return recordLocator{}, false
		}
		if rec == nil || rec.Hidden || rec.QuestionPinId == "" {
			api.RespErr(c, codeNotFound, "Q&A pin not found")
			return recordLocator{}, false
		}
		question, err := a.loadQuestion(rec.QuestionChain, rec.QuestionPinId)
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return recordLocator{}, false
		}
		if !questionVisible(question) {
			api.RespErr(c, codeNotFound, "Q&A pin not found")
			return recordLocator{}, false
		}
	}
	return locator, true
}

func parseCommentListParams(c *gin.Context) (commentListParams, string) {
	params := commentListParams{size: defaultCommentSize}
	switch sortParam := strings.ToLower(strings.TrimSpace(c.Query("sort"))); sortParam {
	case "", "newest":
	case "oldest":
		params.oldest = true
	default:
		return params, "invalid sort"
	}
	size, errMsg := parseSize(c.Query("size"), defaultCommentSize, maxCommentSize)
	if errMsg != "" {
		return params, errMsg
	}
	params.size = size
	offset, err := decodeOffsetCursor(strings.TrimSpace(c.Query("cursor")))
	if err != nil {
		return params, "invalid cursor"
	}
	params.offset = offset
	return params, ""
}

func (a *Aggregator) commentItemFromRecord(rec *CommentRecord) CommentItem {
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return CommentItem{
		Protocol:     "paycomment",
		PinId:        rec.SourcePinId,
		CurrentPinId: currentPinId,
		TargetPinId:  rec.TargetPinId,
		ChainName:    rec.ChainName,
		Content:      rec.Content,
		ContentType:  rec.ContentType,
		Publisher:    a.publisherInfoOf(rec.Publisher, false),
		CreatedAt:    rec.CreatedAt,
		IsMempool:    rec.IsMempool,
	}
}

// ---------------------------------------------------------------------------
// GET /api/qa/answers (R6)
// ---------------------------------------------------------------------------

type answersFeedParams struct {
	publisher string
	top       bool
	size      int
	offset    int
}

// handleAnswers lists answers across questions: the newest-answers feed
// (publisher omitted) or an author page's answers / best-answers tabs.
func (a *Aggregator) handleAnswers(c *gin.Context) {
	params, errMsg := parseAnswersFeedParams(c)
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	if a.store == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	answers := make([]*AnswerRecord, 0)
	if err := a.store.ScanPrefix(Namespace, answerTimePrefix(), func(key, _ []byte) error {
		chainName, sourcePinId, ok := parseAnswerTimeKey(key)
		if !ok {
			return nil
		}
		answer, err := a.loadAnswer(chainName, sourcePinId)
		if err != nil {
			return err
		}
		if answer == nil || answer.Hidden {
			return nil
		}
		if params.publisher != "" &&
			!strings.EqualFold(answer.Publisher.GlobalMetaId, params.publisher) &&
			!strings.EqualFold(answer.Publisher.MetaId, params.publisher) {
			return nil
		}
		answers = append(answers, answer)
		return nil
	}); err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	// The scan yields newest first (createdAt desc, pinId asc within a
	// timestamp); top re-ranks by score desc, tie newer first.
	if params.top {
		sort.SliceStable(answers, func(i, j int) bool {
			if answers[i].Score() != answers[j].Score() {
				return answers[i].Score() > answers[j].Score()
			}
			if answers[i].CreatedAt != answers[j].CreatedAt {
				return answers[i].CreatedAt > answers[j].CreatedAt
			}
			return answers[i].SourcePinId < answers[j].SourcePinId
		})
	}

	offset := params.offset
	if offset > len(answers) {
		offset = len(answers)
	}
	page := answers[offset:]
	hasMore := len(page) > params.size
	if hasMore {
		page = page[:params.size]
	}
	items := make([]AnswerItem, 0, len(page))
	for _, answer := range page {
		items = append(items, a.answerFeedItemFromRecord(answer))
	}
	a.respondList(c, answerItemsToAny(items), hasMore, offset, params.size)
}

func parseAnswersFeedParams(c *gin.Context) (answersFeedParams, string) {
	params := answersFeedParams{size: defaultFeedSize}
	params.publisher = strings.TrimSpace(c.Query("publisher"))
	switch sortParam := strings.ToLower(strings.TrimSpace(c.Query("sort"))); sortParam {
	case "", "newest":
	case "top":
		params.top = true
	default:
		return params, "invalid sort"
	}
	size, errMsg := parseSize(c.Query("size"), defaultFeedSize, maxFeedSize)
	if errMsg != "" {
		return params, errMsg
	}
	params.size = size
	offset, err := decodeOffsetCursor(strings.TrimSpace(c.Query("cursor")))
	if err != nil {
		return params, "invalid cursor"
	}
	params.offset = offset
	return params, ""
}

// answerFeedItemFromRecord projects an answer row for /api/qa/answers: the
// v1 answer item plus the light parent-question embed.
func (a *Aggregator) answerFeedItemFromRecord(rec *AnswerRecord) AnswerItem {
	item := a.answerItemFromRecord(rec)
	if question, err := a.loadQuestion(rec.QuestionChain, rec.QuestionPinId); err == nil && question != nil {
		item.Question = &questionEmbed{
			PinId:     question.SourcePinId,
			Title:     question.Title,
			ChainName: question.ChainName,
			CreatedAt: question.CreatedAt,
		}
	}
	return item
}
