// Package publishedcontent indexes user-published MetaID content protocols
// for Bot Homepage sections.
package publishedcontent

const (
	PathSimpleBuzz     = "/protocols/simplebuzz"
	PathMetaApp        = "/protocols/metaapp"
	PathMetaBotSkill   = "/protocols/metabot-skill"
	PathSimpleNote     = "/protocols/simplenote"
	PathMetaProtocol   = "/protocols/metaprotocol"
	PathSimpleQuestion = "/protocols/simplequestion"
	PathSimpleAnswer   = "/protocols/simpleanswer"
)

const (
	OperationCreate = "create"
	OperationModify = "modify"
	OperationRevoke = "revoke"
)

const Namespace = "publishedcontent"

type Record struct {
	SourcePinId  string `json:"sourcePinId"`
	CurrentPinId string `json:"currentPinId"`
	ChainName    string `json:"chainName"`
	ProtocolPath string `json:"protocolPath"`

	PublisherGlobalMetaId string `json:"publisherGlobalMetaId"`
	PublisherMetaId       string `json:"publisherMetaId"`
	PublisherAddress      string `json:"publisherAddress"`

	// Creator* preserve the identity of the original create pin across
	// modify folds (the folded publisher tracks the latest modifier). The
	// preference aggregator uses them so an app creator cannot bypass the
	// author-conflict rule by judging their own work after someone else
	// modified the package.
	//
	// Legacy records (stored before this field existed) are migrated two
	// ways: create-only records get creator := folded publisher at startup
	// (exact, the publisher IS the author); modified legacy records are
	// repopulated only when the original create pin replays through
	// processCreate (metaapp backfill re-run) — until then the
	// author-conflict check degrades to the folded-publisher approximation.
	CreatorGlobalMetaId string `json:"creatorGlobalMetaId,omitempty"`
	CreatorMetaId       string `json:"creatorMetaId,omitempty"`
	CreatorAddress      string `json:"creatorAddress,omitempty"`

	Operation string `json:"operation"`
	Hidden    bool   `json:"hidden"`
	IsMempool bool   `json:"isMempool,omitempty"`

	ContentType    string         `json:"contentType"`
	PayloadText    string         `json:"payloadText,omitempty"`
	PayloadJSON    map[string]any `json:"payloadJSON,omitempty"`
	PayloadExposed bool           `json:"payloadExposed"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`

	SourceNumber  int64  `json:"sourceNumber,omitempty"`
	CurrentNumber int64  `json:"currentNumber,omitempty"`
	SourcePath    string `json:"sourcePath,omitempty"`
	CurrentPath   string `json:"currentPath,omitempty"`
	SourceHost    string `json:"sourceHost,omitempty"`
	CurrentHost   string `json:"currentHost,omitempty"`
}

type ListParams struct {
	ProtocolPath          string
	ChainName             string
	PublisherGlobalMetaId string
	PublisherMetaId       string
	PublisherAddress      string
	Size                  int
	IncludeHidden         bool
}

type ListResult struct {
	Items   []SectionItem `json:"items"`
	HasMore bool          `json:"hasMore"`
}

type SectionItem struct {
	SourcePinId  string `json:"sourcePinId"`
	CurrentPinId string `json:"currentPinId"`
	ChainName    string `json:"chainName"`
	ProtocolPath string `json:"protocolPath"`

	PublisherGlobalMetaId string `json:"publisherGlobalMetaId"`
	PublisherMetaId       string `json:"publisherMetaId"`
	PublisherAddress      string `json:"publisherAddress"`

	Operation string `json:"operation"`
	Hidden    bool   `json:"hidden"`
	IsMempool bool   `json:"isMempool,omitempty"`

	ContentType    string         `json:"contentType"`
	PayloadText    string         `json:"payloadText,omitempty"`
	PayloadJSON    map[string]any `json:"payloadJSON,omitempty"`
	PayloadExposed bool           `json:"payloadExposed"`

	CreatedAt int64 `json:"createdAt"`
	UpdatedAt int64 `json:"updatedAt"`

	SourceNumber  int64  `json:"sourceNumber,omitempty"`
	CurrentNumber int64  `json:"currentNumber,omitempty"`
	SourcePath    string `json:"sourcePath,omitempty"`
	CurrentPath   string `json:"currentPath,omitempty"`
	SourceHost    string `json:"sourceHost,omitempty"`
	CurrentHost   string `json:"currentHost,omitempty"`
}
