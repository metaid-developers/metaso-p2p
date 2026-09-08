package qa

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// Business error codes of the /api/qa/* contract (same family as
// /api/metaweb/*). HTTP stays 200 for envelope compatibility.
const (
	codeInvalidParam = 40000
	codeNotFound     = 40400
	codeUnavailable  = 50000
)

const (
	defaultFeedSize   = 10
	maxFeedSize       = 50
	defaultAnswerSize = 50
	maxAnswerSize     = 50

	// hotWindow limits hot ranking to questions created within the window
	// (see docs/specs/2026-09-07-metaweb-qa-api.md, Hot Ranking).
	hotWindowSeconds = 7 * 24 * 3600
)

// pinIDPattern enforces the `<64 hex>i<n>` MetaID pin id shape.
var pinIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{64}i[0-9]+$`)

// ---------------------------------------------------------------------------
// GET /api/qa/search (R2)
// ---------------------------------------------------------------------------

type searchParams struct {
	query       string
	tags        []string
	publisher   string
	answered    bool
	answeredSet bool
	newest      bool
	size        int
	offset      int
}

func (a *Aggregator) handleSearch(c *gin.Context) {
	params, errMsg := parseSearchParams(c)
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	if a.store == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	docs := a.searchDocSnapshot()
	// Stopword tokens are excluded from scoring; an all-stopword query
	// yields no scoring tokens and therefore an empty result set.
	tokens := scoringTokens(tokenizeQuery(params.query))
	weights := tokenIDFWeights(docs, tokens)

	type scoredDoc struct {
		doc   questionDoc
		score int
	}
	matches := make([]scoredDoc, 0)
	for i := range docs {
		doc := docs[i]
		if !tagsMatchAll(doc.Tags, params.tags) {
			continue
		}
		if params.publisher != "" &&
			!strings.EqualFold(doc.PublisherGlobalMetaId, params.publisher) &&
			!strings.EqualFold(doc.PublisherMetaId, params.publisher) {
			continue
		}
		if params.answeredSet && (doc.AnswerCount > 0) != params.answered {
			continue
		}
		score := 0
		if params.newest {
			if !docMatchesAny(&doc, tokens) {
				continue
			}
		} else {
			score = scoreQuestionDoc(&doc, tokens, weights, params.query)
			if score <= 0 {
				continue
			}
		}
		matches = append(matches, scoredDoc{doc: doc, score: score})
	}

	if params.newest {
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].doc.CreatedAt != matches[j].doc.CreatedAt {
				return matches[i].doc.CreatedAt > matches[j].doc.CreatedAt
			}
			return matches[i].doc.SourcePinId < matches[j].doc.SourcePinId
		})
	} else {
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].score != matches[j].score {
				return matches[i].score > matches[j].score
			}
			if matches[i].doc.CreatedAt != matches[j].doc.CreatedAt {
				return matches[i].doc.CreatedAt > matches[j].doc.CreatedAt
			}
			return matches[i].doc.SourcePinId < matches[j].doc.SourcePinId
		})
	}

	offset := params.offset
	if offset > len(matches) {
		offset = len(matches)
	}
	page := matches[offset:]
	hasMore := len(page) > params.size
	if hasMore {
		page = page[:params.size]
	}

	items := make([]QuestionItem, 0, len(page))
	for _, match := range page {
		item := a.questionItemFromDoc(match.doc)
		score := match.score
		item.Score = &score
		items = append(items, item)
	}
	a.respondList(c, questionItemsToAny(items), hasMore, offset, params.size)
}

func parseSearchParams(c *gin.Context) (searchParams, string) {
	params := searchParams{
		query: strings.TrimSpace(c.Query("q")),
		size:  defaultSearchSize,
	}
	if params.query == "" {
		return params, "q is required"
	}
	params.tags = parseCSVTags(c.Query("tags"))
	params.publisher = strings.TrimSpace(c.Query("publisher"))
	if raw := strings.TrimSpace(c.Query("answered")); raw != "" {
		switch strings.ToLower(raw) {
		case "true":
			params.answered, params.answeredSet = true, true
		case "false":
			params.answered, params.answeredSet = false, true
		default:
			return params, "invalid answered"
		}
	}
	switch sortParam := strings.ToLower(strings.TrimSpace(c.Query("sort"))); sortParam {
	case "", "relevance":
	case "newest":
		params.newest = true
	default:
		return params, "invalid sort"
	}
	size, errMsg := parseSize(c.Query("size"), defaultSearchSize, maxSearchSize)
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

// ---------------------------------------------------------------------------
// GET /api/qa/questions (R3)
// ---------------------------------------------------------------------------

type feedParams struct {
	tags       []string
	publisher  string
	minAnswers int
	minSet     bool
	maxAnswers int
	maxSet     bool
	hot        bool
	size       int
	offset     int
}

func (a *Aggregator) handleQuestions(c *gin.Context) {
	params, errMsg := parseFeedParams(c)
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	if a.store == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	type feedRow struct {
		rec      *QuestionRecord
		hotScore int
	}
	rows := make([]feedRow, 0)

	if params.hot {
		windowStart := a.now() - hotWindowSeconds
		err := a.store.ScanPrefix(Namespace, questionTimePrefix(), func(key, _ []byte) error {
			chainName, sourcePinId, ok := parseQuestionTimeKey(key)
			if !ok {
				return nil
			}
			rec, err := a.loadQuestion(chainName, sourcePinId)
			if err != nil {
				return err
			}
			if !questionVisible(rec) || rec.CreatedAt < windowStart {
				return nil
			}
			if !feedRecordMatches(rec, params) {
				return nil
			}
			rows = append(rows, feedRow{rec: rec, hotScore: rec.HotPoints})
			return nil
		})
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].hotScore != rows[j].hotScore {
				return rows[i].hotScore > rows[j].hotScore
			}
			if rows[i].rec.CreatedAt != rows[j].rec.CreatedAt {
				return rows[i].rec.CreatedAt > rows[j].rec.CreatedAt
			}
			return rows[i].rec.SourcePinId < rows[j].rec.SourcePinId
		})
	} else {
		// Newest: the reverse-time index already yields createdAt desc, so
		// rows append in final order and no in-memory sort is needed.
		if err := a.store.ScanPrefix(Namespace, questionTimePrefix(), func(key, _ []byte) error {
			chainName, sourcePinId, ok := parseQuestionTimeKey(key)
			if !ok {
				return nil
			}
			rec, err := a.loadQuestion(chainName, sourcePinId)
			if err != nil {
				return err
			}
			if !questionVisible(rec) {
				return nil
			}
			if !feedRecordMatches(rec, params) {
				return nil
			}
			rows = append(rows, feedRow{rec: rec})
			return nil
		}); err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return
		}
	}

	offset := params.offset
	if offset > len(rows) {
		offset = len(rows)
	}
	page := rows[offset:]
	hasMore := len(page) > params.size
	if hasMore {
		page = page[:params.size]
	}

	items := make([]QuestionItem, 0, len(page))
	for _, row := range page {
		item := a.questionItemFromRecord(row.rec)
		if params.hot {
			hotScore := row.hotScore
			item.HotScore = &hotScore
		}
		items = append(items, item)
	}
	a.respondList(c, questionItemsToAny(items), hasMore, offset, params.size)
}

func feedRecordMatches(rec *QuestionRecord, params feedParams) bool {
	if !tagsMatchAll(rec.Tags, params.tags) {
		return false
	}
	if params.publisher != "" &&
		!strings.EqualFold(rec.Publisher.GlobalMetaId, params.publisher) &&
		!strings.EqualFold(rec.Publisher.MetaId, params.publisher) {
		return false
	}
	if params.minSet && rec.AnswerCount < params.minAnswers {
		return false
	}
	if params.maxSet && rec.AnswerCount > params.maxAnswers {
		return false
	}
	return true
}

func parseFeedParams(c *gin.Context) (feedParams, string) {
	params := feedParams{size: defaultFeedSize}
	params.tags = parseCSVTags(c.Query("tags"))
	params.publisher = strings.TrimSpace(c.Query("publisher"))
	if raw := strings.TrimSpace(c.Query("minAnswers")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return params, "invalid minAnswers"
		}
		params.minAnswers, params.minSet = value, true
	}
	if raw := strings.TrimSpace(c.Query("maxAnswers")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 {
			return params, "invalid maxAnswers"
		}
		params.maxAnswers, params.maxSet = value, true
	}
	if params.minSet && params.maxSet && params.minAnswers > params.maxAnswers {
		return params, "minAnswers must not exceed maxAnswers"
	}
	switch sortParam := strings.ToLower(strings.TrimSpace(c.Query("sort"))); sortParam {
	case "", "newest":
	case "hot":
		params.hot = true
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

// ---------------------------------------------------------------------------
// GET /api/qa/questions/:pinId and /answers (R4)
// ---------------------------------------------------------------------------

func (a *Aggregator) handleQuestionDetail(c *gin.Context) {
	question, ok := a.resolveQuestionParam(c)
	if !ok {
		return
	}
	size, errMsg := parseSize(c.Query("size"), defaultAnswerSize, maxAnswerSize)
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	offset, err := decodeOffsetCursor(strings.TrimSpace(c.Query("cursor")))
	if err != nil {
		api.RespErr(c, codeInvalidParam, "invalid cursor")
		return
	}

	answers, hasMore, err := a.rankedAnswers(question, "", offset, size)
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	var nextCursor *string
	if hasMore {
		encoded := encodeOffsetCursor(offset + size)
		nextCursor = &encoded
	}
	api.RespSuccess(c, detailData{
		Question:   a.questionItemFromRecord(question),
		Answers:    answers,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	})
}

func (a *Aggregator) handleQuestionAnswers(c *gin.Context) {
	question, ok := a.resolveQuestionParam(c)
	if !ok {
		return
	}
	size, errMsg := parseSize(c.Query("size"), defaultAnswerSize, maxAnswerSize)
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	offset, err := decodeOffsetCursor(strings.TrimSpace(c.Query("cursor")))
	if err != nil {
		api.RespErr(c, codeInvalidParam, "invalid cursor")
		return
	}
	publisher := strings.TrimSpace(c.Query("publisher"))

	answers, hasMore, err := a.rankedAnswers(question, publisher, offset, size)
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	a.respondList(c, answerItemsToAny(answers), hasMore, offset, size)
}

// resolveQuestionParam validates and resolves the :pinId path parameter to a
// visible question record, writing the error response itself.
func (a *Aggregator) resolveQuestionParam(c *gin.Context) (*QuestionRecord, bool) {
	pinId := strings.TrimSpace(c.Param("pinId"))
	if !pinIDPattern.MatchString(pinId) {
		api.RespErr(c, codeInvalidParam, "malformed pinId")
		return nil, false
	}
	if a.store == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return nil, false
	}
	locator, ok := a.lookupLocator(pinId)
	if !ok || locator.kind != "q" {
		api.RespErr(c, codeNotFound, "question not found")
		return nil, false
	}
	rec, err := a.loadQuestion(locator.chainName, locator.sourcePinId)
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return nil, false
	}
	if !questionVisible(rec) {
		api.RespErr(c, codeNotFound, "question not found")
		return nil, false
	}
	return rec, true
}

// rankedAnswers loads the question's non-hidden answers in R4 order
// (score desc, createdAt desc, pinId asc), optionally filtered by answer
// publisher, and slices the requested offset page.
func (a *Aggregator) rankedAnswers(question *QuestionRecord, publisher string, offset, size int) ([]AnswerItem, bool, error) {
	answers := make([]*AnswerRecord, 0, question.AnswerCount)
	prefix := answerIndexPrefix(question.ChainName, question.SourcePinId)
	err := a.store.ScanPrefix(Namespace, prefix, func(key, _ []byte) error {
		parts := strings.SplitN(strings.TrimPrefix(string(key), string(prefix)), ":", 2)
		if len(parts) != 2 {
			return nil
		}
		answer, err := a.loadAnswer(question.ChainName, parts[1])
		if err != nil {
			return err
		}
		if answer == nil || answer.Hidden {
			return nil
		}
		if publisher != "" &&
			!strings.EqualFold(answer.Publisher.GlobalMetaId, publisher) &&
			!strings.EqualFold(answer.Publisher.MetaId, publisher) {
			return nil
		}
		answers = append(answers, answer)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(answers, func(i, j int) bool {
		if answers[i].Score() != answers[j].Score() {
			return answers[i].Score() > answers[j].Score()
		}
		if answers[i].CreatedAt != answers[j].CreatedAt {
			return answers[i].CreatedAt > answers[j].CreatedAt
		}
		return answers[i].SourcePinId < answers[j].SourcePinId
	})

	if offset > len(answers) {
		offset = len(answers)
	}
	page := answers[offset:]
	hasMore := len(page) > size
	if hasMore {
		page = page[:size]
	}
	items := make([]AnswerItem, 0, len(page))
	for _, answer := range page {
		items = append(items, a.answerItemFromRecord(answer))
	}
	return items, hasMore, nil
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

func (a *Aggregator) respondList(c *gin.Context, items []any, hasMore bool, offset, size int) {
	var nextCursor *string
	if hasMore {
		encoded := encodeOffsetCursor(offset + size)
		nextCursor = &encoded
	}
	api.RespSuccess(c, listData{Items: items, NextCursor: nextCursor, HasMore: hasMore})
}

func questionItemsToAny(items []QuestionItem) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func answerItemsToAny(items []AnswerItem) []any {
	out := make([]any, 0, len(items))
	for _, item := range items {
		out = append(out, item)
	}
	return out
}

func (a *Aggregator) publisherInfoOf(identity Identity, withAvatar bool) publisherInfo {
	info := publisherInfo{
		GlobalMetaId: identity.GlobalMetaId,
		MetaId:       identity.MetaId,
	}
	if a.profileNamer != nil {
		name, avatar := a.profileNamer.ProfileNameAvatar(identity.GlobalMetaId, identity.MetaId)
		info.Name = name
		if withAvatar {
			info.Avatar = avatar
		}
	}
	return info
}

func (a *Aggregator) topAnswerItemOf(top *TopAnswerInfo) *topAnswerItem {
	if top == nil {
		return nil
	}
	return &topAnswerItem{
		PinId:        top.PinId,
		Summary:      top.Summary,
		Publisher:    a.publisherInfoOf(top.Publisher, false),
		CreatedAt:    top.CreatedAt,
		LikeCount:    top.LikeCount,
		DislikeCount: top.DislikeCount,
		Score:        top.LikeCount - top.DislikeCount,
	}
}

func (a *Aggregator) questionItemFromRecord(rec *QuestionRecord) QuestionItem {
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return QuestionItem{
		Protocol:     "simplequestion",
		PinId:        rec.SourcePinId,
		CurrentPinId: currentPinId,
		ChainName:    rec.ChainName,
		Title:        rec.Title,
		Summary:      rec.Summary,
		Tags:         orEmptyTags(rec.Tags),
		ContentType:  rec.ContentType,
		Publisher:    a.publisherInfoOf(rec.Publisher, true),
		CreatedAt:    rec.CreatedAt,
		IsMempool:    rec.IsMempool,
		LikeCount:    rec.LikeCount,
		DislikeCount: rec.DislikeCount,
		CommentCount: rec.CommentCount,
		AnswerCount:  rec.AnswerCount,
		TopAnswer:    a.topAnswerItemOf(rec.TopAnswer),
	}
}

func (a *Aggregator) questionItemFromDoc(doc questionDoc) QuestionItem {
	return QuestionItem{
		Protocol:     "simplequestion",
		PinId:        doc.SourcePinId,
		CurrentPinId: doc.CurrentPinId,
		ChainName:    doc.ChainName,
		Title:        doc.Title,
		Summary:      doc.Summary,
		Tags:         orEmptyTags(doc.Tags),
		Publisher: a.publisherInfoOf(Identity{
			GlobalMetaId: doc.PublisherGlobalMetaId,
			MetaId:       doc.PublisherMetaId,
		}, true),
		CreatedAt:   doc.CreatedAt,
		AnswerCount: doc.AnswerCount,
		TopAnswer:   a.topAnswerItemOf(doc.TopAnswer),
	}
}

func (a *Aggregator) answerItemFromRecord(rec *AnswerRecord) AnswerItem {
	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return AnswerItem{
		Protocol:      "simpleanswer",
		PinId:         rec.SourcePinId,
		CurrentPinId:  currentPinId,
		QuestionPinId: rec.QuestionPinId,
		ChainName:     rec.ChainName,
		Summary:       rec.Summary,
		Tags:          orEmptyTags(rec.Tags),
		Publisher:     a.publisherInfoOf(rec.Publisher, false),
		CreatedAt:     rec.CreatedAt,
		IsMempool:     rec.IsMempool,
		LikeCount:     rec.LikeCount,
		DislikeCount:  rec.DislikeCount,
		CommentCount:  rec.CommentCount,
		Score:         rec.Score(),
	}
}

func orEmptyTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// parseCSVTags splits a CSV tags parameter into trimmed, lowercased values.
func parseCSVTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

// tagsMatchAll reports whether the record tags carry every filter tag
// (case-insensitive exact match).
func tagsMatchAll(tags []string, filter []string) bool {
	for _, wanted := range filter {
		found := false
		for _, tag := range tags {
			if strings.EqualFold(tag, wanted) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// parseSize validates an optional size parameter: non-numeric or < 1 is a
// contract error; values above max clamp to max.
func parseSize(raw string, defaultSize, maxSize int) (int, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultSize, ""
	}
	size, err := strconv.Atoi(raw)
	if err != nil || size < 1 {
		return defaultSize, "invalid size"
	}
	if size > maxSize {
		size = maxSize
	}
	return size, ""
}
