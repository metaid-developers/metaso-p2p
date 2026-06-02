package presence

// OnlineEntry represents one online presence record.
type OnlineEntry struct {
	MetaId        string   `json:"metaid"`
	Type          string   `json:"type"`
	ConnectedAt   int64    `json:"connectedAt"`
	LastSeenAt    int64    `json:"lastSeenAt,omitempty"`
	SourceNodeIds []string `json:"sourceNodeIds,omitempty"`
	Sources       int      `json:"sources,omitempty"`
}

// LocalReader reads active local presence entries.
type LocalReader interface {
	OnlineEntries() []OnlineEntry
}

// SnapshotProvider builds a local presence snapshot.
type SnapshotProvider interface {
	Snapshot() (*Snapshot, error)
}

// GlobalReader reads merged local and federated presence.
type GlobalReader interface {
	Enabled() bool
	DefaultScope() string
	OnlineList(local []OnlineEntry, page int, size int) []OnlineEntry
	Stats(local []OnlineEntry) GlobalStats
}

// Snapshot is the signed local presence document shared between nodes.
type Snapshot struct {
	Protocol    string        `json:"protocol"`
	Version     string        `json:"version"`
	NodeID      string        `json:"nodeId"`
	GeneratedAt int64         `json:"generatedAt"`
	TTLSeconds  int64         `json:"ttlSeconds"`
	Sequence    uint64        `json:"sequence"`
	Items       []OnlineEntry `json:"items"`
	Signature   string        `json:"signature"`
}

// GlobalStats summarizes merged local and federated presence.
type GlobalStats struct {
	TotalConnections int `json:"totalConnections"`
	UniqueMetaIds    int `json:"uniqueMetaIds"`
	Nodes            int `json:"nodes"`
}
