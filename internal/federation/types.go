package federation

const (
	ProtocolNode     = "metaso-p2p-node"
	ProtocolPresence = "metaso-p2p-presence"
	RegistryPath     = "/protocols/metaso-p2p-node"
	PresencePath     = "/.well-known/metaso-p2p/presence"
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
