// Package inbox defines the shared types of the "interactions targeting my
// pins" read path (GET /api/metaweb/interactions, R3 of
// docs/specs/2026-09-13-metaweb-surf-reads-api.md). It is a leaf package so
// the qa and socialcontent read models can produce hits and the metaweb
// aggregator can merge them without import cycles.
package inbox

import "strings"

// Hit types of the inbox contract.
const (
	TypePayLike      = "paylike"
	TypePayComment   = "paycomment"
	TypeSimpleAnswer = "simpleanswer"
)

// Hit is one interaction targeting a pin owned by the queried owner.
// CreatedAt is unix seconds. Dislike is only meaningful for paylike hits
// from the qa read model (last state = dislike).
type Hit struct {
	Type        string `json:"type"`
	PinId       string `json:"pinId"`
	ChainName   string `json:"chainName"`
	TargetPinId string `json:"targetPinId"`

	ActorGlobalMetaId string `json:"actorGlobalMetaId,omitempty"`
	ActorMetaId       string `json:"actorMetaId,omitempty"`
	ActorAddress      string `json:"actorAddress,omitempty"`

	CreatedAt int64  `json:"createdAt"`
	Excerpt   string `json:"excerpt,omitempty"`
	IsMempool bool   `json:"isMempool,omitempty"`
	Dislike   bool   `json:"dislike,omitempty"`
}

// SortKey is the contract ordering tuple (createdAt DESC, pinId DESC).
func (h Hit) After(ts int64, pinId string) bool {
	if h.CreatedAt != ts {
		return h.CreatedAt < ts
	}
	return h.PinId < pinId
}

// OwnerIdentities normalises the owner identity candidates (globalMetaId,
// metaId, address) into deduplicated lowercase index keys.
func OwnerIdentities(values ...string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, dup := seen[trimmed]; dup {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
