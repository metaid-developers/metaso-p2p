// Package qa implements the on-chain Q&A read model over the SimpleQuestion /
// SimpleAnswer protocols with PayLike / PayComment engagement aggregation, and
// serves the read-only /api/qa/* endpoints (search, latest-questions feed,
// question detail with ranked answers).
//
// The generic publishedcontent pipeline additionally stores both content
// protocols (full bodies stay behind GET /api/metaweb/pin/:pinId and both are
// searchable via GET /api/metaweb/search); this package owns the Q&A
// projection: the question⇄answer join, engagement counts, answer ranking and
// the Q&A-specific surfaces, which return summaries only.
//
// See docs/specs/2026-09-07-metaweb-qa-api.md for the contract and
// docs/metaid_protocols/08-qanda.md (IDBots repo) for the protocol payloads.
package qa

import "errors"

const (
	Namespace          = "qa"
	PathSimpleQuestion = "/protocols/simplequestion"
	PathSimpleAnswer   = "/protocols/simpleanswer"
	PathPayLike        = "/protocols/paylike"
	PathPayComment     = "/protocols/paycomment"
)

const (
	OperationCreate = "create"
	OperationModify = "modify"
	OperationRevoke = "revoke"
)

var (
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrInvalidCursor    = errors.New("invalid cursor")
	ErrNotFound         = errors.New("question not found")
	ErrMalformedPayload = errors.New("malformed qa protocol payload")
)

// Identity is the publisher/actor identity triple of a pin. GlobalMetaId is
// canonicalised at parse time (idq1… form); MetaId and Address are fallbacks.
type Identity struct {
	GlobalMetaId string `json:"globalMetaId,omitempty"`
	MetaId       string `json:"metaId,omitempty"`
	Address      string `json:"address,omitempty"`
}

// actorKey is the stable key of an identity for last-state-wins like
// aggregation: canonical global id, else metaid, else address, lowercased.
func (i Identity) actorKey() string {
	for _, value := range []string{i.GlobalMetaId, i.MetaId, i.Address} {
		if value != "" {
			return value
		}
	}
	return ""
}

// QuestionRecord is the canonical question projection. SourcePinId stays
// stable across modify/revoke versions; CurrentPinId tracks the newest
// observed version. Counts are denormalised from the like/comment/answer
// indexes on every write.
type QuestionRecord struct {
	SourcePinId  string `json:"sourcePinId"`
	CurrentPinId string `json:"currentPinId"`
	ChainName    string `json:"chainName"`

	Title       string   `json:"title"`
	Content     string   `json:"content,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ContentType string   `json:"contentType,omitempty"`
	Attachments []string `json:"attachments,omitempty"`

	Publisher Identity `json:"publisher"`

	Operation string `json:"operation"`
	Hidden    bool   `json:"hidden"`
	IsMempool bool   `json:"isMempool,omitempty"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`

	LikeCount    int `json:"likeCount"`
	DislikeCount int `json:"dislikeCount"`
	CommentCount int `json:"commentCount"`

	AnswerCount int            `json:"answerCount"`
	AnswersText string         `json:"answersText,omitempty"` // search-only excerpt, capped
	HotPoints   int            `json:"hotPoints"`             // windowed hot score input, see list.go
	TopAnswer   *TopAnswerInfo `json:"topAnswer,omitempty"`
}

// TopAnswerInfo is the denormalised best answer of a question (highest
// likeCount−dislikeCount, tie newer first). Serialized on the record so list
// and search surfaces avoid a per-question answer scan.
type TopAnswerInfo struct {
	PinId        string   `json:"pinId"`
	Summary      string   `json:"summary"`
	Publisher    Identity `json:"publisher"`
	CreatedAt    int64    `json:"createdAt"`
	LikeCount    int      `json:"likeCount"`
	DislikeCount int      `json:"dislikeCount"`
}

// AnswerRecord is the canonical answer projection. QuestionPinId is the
// resolved question source pin id; an answer whose answerTo does not resolve
// stays pending (QuestionPinId empty, pending index entry) and is excluded
// from every list surface. QuestionChain is the chain of the resolved
// question (answers may live on a different chain than their question).
type AnswerRecord struct {
	SourcePinId  string `json:"sourcePinId"`
	CurrentPinId string `json:"currentPinId"`
	ChainName    string `json:"chainName"`

	QuestionChain string `json:"questionChain,omitempty"`
	QuestionPinId string `json:"questionPinId,omitempty"`
	AnswerTo      string `json:"answerTo,omitempty"`

	Content     string   `json:"content,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ContentType string   `json:"contentType,omitempty"`
	Attachments []string `json:"attachments,omitempty"`

	Publisher Identity `json:"publisher"`

	Operation string `json:"operation"`
	Hidden    bool   `json:"hidden"`
	IsMempool bool   `json:"isMempool,omitempty"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`

	LikeCount    int `json:"likeCount"`
	DislikeCount int `json:"dislikeCount"`
	CommentCount int `json:"commentCount"`
}

// Score is the ranking value of an answer: likeCount − dislikeCount.
func (r *AnswerRecord) Score() int {
	return r.LikeCount - r.DislikeCount
}

// likeState is the last PayLike state of one (target, actor) pair. IsLike 1 =
// like, −1 = dislike, 0 = cancelled (present but counted as neither).
type likeState struct {
	PinId     string `json:"pinId"`
	IsLike    int    `json:"isLike"`
	Timestamp int64  `json:"timestamp"`
	IsMempool bool   `json:"isMempool,omitempty"`
}

// publisherInfo is the publisher block of the wire items. Name/Avatar are
// best-effort userinfo enrichment of the returned page only.
type publisherInfo struct {
	GlobalMetaId string `json:"globalMetaId"`
	MetaId       string `json:"metaid"`
	Name         string `json:"name"`
	Avatar       string `json:"avatar,omitempty"`
}

// topAnswerItem is the topAnswer block of a question item.
type topAnswerItem struct {
	PinId        string        `json:"pinId"`
	Summary      string        `json:"summary"`
	Publisher    publisherInfo `json:"publisher"`
	CreatedAt    int64         `json:"createdAt"`
	LikeCount    int           `json:"likeCount"`
	DislikeCount int           `json:"dislikeCount"`
	Score        int           `json:"score"`
}

// QuestionItem is the question row of every /api/qa/* list surface.
type QuestionItem struct {
	Protocol     string         `json:"protocol"`
	PinId        string         `json:"pinId"`
	CurrentPinId string         `json:"currentPinId"`
	ChainName    string         `json:"chainName"`
	Title        string         `json:"title"`
	Summary      string         `json:"summary"`
	Tags         []string       `json:"tags"`
	ContentType  string         `json:"contentType,omitempty"`
	Publisher    publisherInfo  `json:"publisher"`
	CreatedAt    int64          `json:"createdAt"`
	IsMempool    bool           `json:"isMempool"`
	LikeCount    int            `json:"likeCount"`
	DislikeCount int            `json:"dislikeCount"`
	CommentCount int            `json:"commentCount"`
	AnswerCount  int            `json:"answerCount"`
	TopAnswer    *topAnswerItem `json:"topAnswer"`
	Score        *int           `json:"score,omitempty"`
	HotScore     *int           `json:"hotScore,omitempty"`
}

// AnswerItem is an answer row of the detail / answers surfaces.
type AnswerItem struct {
	Protocol      string        `json:"protocol"`
	PinId         string        `json:"pinId"`
	CurrentPinId  string        `json:"currentPinId"`
	QuestionPinId string        `json:"questionPinId"`
	ChainName     string        `json:"chainName"`
	Summary       string        `json:"summary"`
	Tags          []string      `json:"tags"`
	Publisher     publisherInfo `json:"publisher"`
	CreatedAt     int64         `json:"createdAt"`
	IsMempool     bool          `json:"isMempool"`
	LikeCount     int           `json:"likeCount"`
	DislikeCount  int           `json:"dislikeCount"`
	CommentCount  int           `json:"commentCount"`
	Score         int           `json:"score"`
}

type listData struct {
	Items      []any   `json:"items"`
	NextCursor *string `json:"nextCursor"`
	HasMore    bool    `json:"hasMore"`
}

type detailData struct {
	Question   QuestionItem `json:"question"`
	Answers    []AnswerItem `json:"answers"`
	NextCursor *string      `json:"nextCursor"`
	HasMore    bool         `json:"hasMore"`
}
