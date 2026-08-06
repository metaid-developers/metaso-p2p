// Package socialcontent provides a post- and interaction-centric read model
// for on-chain social protocols.
package socialcontent

import "errors"

const (
	Namespace       = "socialcontent"
	PathSimpleBuzz  = "/protocols/simplebuzz"
	PathPayLike     = "/protocols/paylike"
	PathPayComment  = "/protocols/paycomment"
	OperationCreate = "create"
	OperationModify = "modify"
	OperationRevoke = "revoke"
)

const (
	SortNewest = "newest"
	SortHot    = "hot"
)

const (
	defaultFeedSize    = 20
	maxFeedSize        = 100
	defaultCommentSize = 20
	maxCommentSize     = 100
)

var (
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrInvalidCursor    = errors.New("invalid cursor")
	ErrNotFound         = errors.New("social post not found")
	ErrMalformedPayload = errors.New("malformed social protocol payload")
)

// PostRecord is the canonical social post projection. SourcePinId remains
// stable across simplebuzz modify/revoke pins; CurrentPinId points to the
// latest observed version.
type PostRecord struct {
	SourcePinId  string `json:"sourcePinId"`
	CurrentPinId string `json:"currentPinId"`
	ChainName    string `json:"chainName"`
	ProtocolPath string `json:"protocolPath"`

	AuthorGlobalMetaId string `json:"authorGlobalMetaId,omitempty"`
	AuthorMetaId       string `json:"authorMetaId,omitempty"`
	AuthorAddress      string `json:"authorAddress,omitempty"`

	ContentType string         `json:"contentType,omitempty"`
	PayloadText string         `json:"payloadText,omitempty"`
	PayloadJSON map[string]any `json:"payloadJSON,omitempty"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`
	Hidden    bool  `json:"hidden"`
	IsMempool bool  `json:"isMempool,omitempty"`
}

// LikeEvent is an append-only normalized like/unlike event.
type LikeEvent struct {
	PinId       string `json:"pinId"`
	ChainName   string `json:"chainName"`
	TargetPinId string `json:"targetPinId"`

	ActorGlobalMetaId string `json:"actorGlobalMetaId,omitempty"`
	ActorMetaId       string `json:"actorMetaId,omitempty"`
	ActorAddress      string `json:"actorAddress,omitempty"`

	IsLike    bool  `json:"isLike"`
	Timestamp int64 `json:"timestamp"`
	IsMempool bool  `json:"isMempool,omitempty"`
}

// CommentRecord is a normalized comment joined to its target PIN.
type CommentRecord struct {
	PinId       string `json:"pinId"`
	ChainName   string `json:"chainName"`
	TargetPinId string `json:"targetPinId"`

	AuthorGlobalMetaId string `json:"authorGlobalMetaId,omitempty"`
	AuthorMetaId       string `json:"authorMetaId,omitempty"`
	AuthorAddress      string `json:"authorAddress,omitempty"`

	Content     string `json:"content"`
	ContentType string `json:"contentType,omitempty"`
	Timestamp   int64  `json:"timestamp"`
	IsMempool   bool   `json:"isMempool,omitempty"`
}

type FeedParams struct {
	Protocol  string
	Publisher string
	ChainName string
	Since     int64
	Until     int64
	Keyword   string
	Sort      string
	Size      int
	Cursor    string
}

type FeedResult struct {
	Items      []PostItem `json:"items"`
	NextCursor string     `json:"nextCursor"`
	HasMore    bool       `json:"hasMore"`
}

type PostItem struct {
	PinId        string     `json:"pinId"`
	SourcePinId  string     `json:"sourcePinId"`
	CurrentPinId string     `json:"currentPinId"`
	ChainName    string     `json:"chainName"`
	ProtocolPath string     `json:"protocolPath"`
	Author       AuthorItem `json:"author"`
	ContentType  string     `json:"contentType,omitempty"`
	Payload      any        `json:"payload,omitempty"`
	CreatedAt    int64      `json:"createdAt"`
	UpdatedAt    int64      `json:"updatedAt"`
}

type AuthorItem struct {
	GlobalMetaId string `json:"globalMetaId,omitempty"`
	MetaId       string `json:"metaId,omitempty"`
	Address      string `json:"address,omitempty"`
}

type CommentParams struct {
	PinId     string
	ChainName string
	Size      int
	Cursor    string
}

type CommentResult struct {
	Items      []CommentRecord `json:"items"`
	NextCursor string          `json:"nextCursor"`
	HasMore    bool            `json:"hasMore"`
}
