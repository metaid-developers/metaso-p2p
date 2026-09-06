// Package metawebdoc defines the MetaWeb search-document projection and the
// shared per-protocol title/summary/tags/content extraction rules consumed by
// both the unified search index-time snapshots (publishedcontent,
// skillservice) and the generic pin-read endpoint
// (internal/aggregator/metaweb). It is a leaf package: it must not import any
// aggregator package, so publishedcontent and skillservice can consume it
// without import cycles.
//
// See docs/specs/2026-08-23-metaweb-search-api.md (search document fields and
// extraction table) and docs/specs/2026-08-23-metaweb-pin-read-api.md (shared
// meta/text derivation).
package metawebdoc

import "strings"

// Protocol paths covered by the unified search v1 contract. The values mirror
// the path constants in publishedcontent / skillservice; they are duplicated
// here because this leaf package cannot import the aggregators.
const (
	PathSimpleNote     = "/protocols/simplenote"
	PathSimpleBuzz     = "/protocols/simplebuzz"
	PathMetaApp        = "/protocols/metaapp"
	PathMetaBotSkill   = "/protocols/metabot-skill"
	PathSkillService   = "/protocols/skill-service"
	PathMetaProtocol   = "/protocols/metaprotocol"
	PathSimpleQuestion = "/protocols/simplequestion"
	PathSimpleAnswer   = "/protocols/simpleanswer"
)

// Protocol keys of the unified search contract.
const (
	KeySimpleNote     = "simplenote"
	KeySimpleBuzz     = "simplebuzz"
	KeyMetaApp        = "metaapp"
	KeyMetaBotSkill   = "metabot-skill"
	KeySkillService   = "skill-service"
	KeyMetaProtocol   = "metaprotocol"
	KeySimpleQuestion = "simplequestion"
	KeySimpleAnswer   = "simpleanswer"
)

var protocolKeysByPath = map[string]string{
	PathSimpleNote:     KeySimpleNote,
	PathSimpleBuzz:     KeySimpleBuzz,
	PathMetaApp:        KeyMetaApp,
	PathMetaBotSkill:   KeyMetaBotSkill,
	PathSkillService:   KeySkillService,
	PathMetaProtocol:   KeyMetaProtocol,
	PathSimpleQuestion: KeySimpleQuestion,
	PathSimpleAnswer:   KeySimpleAnswer,
}

// Document is one searchable projection of an indexed record. Derived fields
// (title/summary/tags/contentExcerpt) are computed at index time, so list and
// detail views can never drift. Snapshots share Document values by reference;
// callers must not mutate them.
type Document struct {
	ProtocolKey           string         `json:"protocolKey"`
	SourcePinId           string         `json:"sourcePinId"`
	CurrentPinId          string         `json:"currentPinId"`
	ChainName             string         `json:"chainName"`
	Title                 string         `json:"title"`
	Summary               string         `json:"summary"`
	Tags                  []string       `json:"tags"`
	ContentExcerpt        string         `json:"contentExcerpt"`
	PublisherGlobalMetaId string         `json:"publisherGlobalMetaId"`
	PublisherMetaId       string         `json:"publisherMetaId"`
	CreatedAt             int64          `json:"createdAt"` // unix seconds
	UpdatedAt             int64          `json:"updatedAt"` // unix seconds
	Extra                 map[string]any `json:"extra"`
}

// NormaliseProtocolPath lowercases a pin path and strips the trailing slash
// and any `@<pinId>` version-target suffix.
func NormaliseProtocolPath(path string) string {
	base := strings.ToLower(strings.TrimRight(strings.TrimSpace(path), "/"))
	if at := strings.Index(base, "@"); at > 0 {
		base = base[:at]
	}
	return base
}

// ProtocolKeyForPath maps a protocol path onto its unified-search key. Paths
// outside the known table (only reachable through the pin-read remote
// fallback) resolve to the last path segment.
func ProtocolKeyForPath(path string) string {
	base := NormaliseProtocolPath(path)
	if key, ok := protocolKeysByPath[base]; ok {
		return key
	}
	if idx := strings.LastIndex(base, "/"); idx >= 0 && idx+1 < len(base) {
		return base[idx+1:]
	}
	return base
}

// ValidProtocolKey reports whether key is one of the six v1 protocol keys.
func ValidProtocolKey(key string) bool {
	for _, known := range protocolKeysByPath {
		if key == known {
			return true
		}
	}
	return false
}

// NormalizeUnixSeconds converts a pin timestamp to unix seconds. Chain
// timestamps arrive in mixed second/millisecond granularity; values above
// 1e11 are treated as milliseconds (mirrors the backfill clients).
func NormalizeUnixSeconds(ts int64) int64 {
	if ts > 100000000000 {
		return ts / 1000
	}
	return ts
}
