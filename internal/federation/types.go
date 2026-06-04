package federation

const (
	ProtocolNode     = "metasocket-node"
	ProtocolPresence = "metasocket-presence"
	RegistryPath     = "/protocols/metasocket-node"
	PresencePath     = "/.well-known/metasocket/presence"
	Version          = "1.0.0"
)

// RegistryPayload is the JSON content published at RegistryPath for this node.
type RegistryPayload struct {
	Protocol      string   `json:"protocol"`
	Version       string   `json:"version"`
	NodeID        string   `json:"nodeId"`
	Network       string   `json:"network"`
	PublicBaseURL string   `json:"publicBaseUrl"`
	SocketURL     string   `json:"socketUrl"`
	PresenceURL   string   `json:"presenceUrl"`
	PublicKey     string   `json:"publicKey"`
	Capabilities  []string `json:"capabilities"`
	PublishedAt   int64    `json:"publishedAt"`
	ValidUntil    int64    `json:"validUntil"`
}
