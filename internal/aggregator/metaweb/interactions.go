package metaweb

// GET /api/metaweb/interactions — the "interactions targeting my pins" inbox
// (R3). Sources (qa, socialcontent) expose owner-keyed interaction scans;
// this handler merges them into the contract order
// (createdAt DESC, pinId DESC) with the same no-gap/no-duplicate cursor
// paging as the fresh feed. See docs/specs/2026-09-13-metaweb-surf-reads-api.md §3.

import (
	"encoding/base64"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/inbox"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

const (
	interactionsDefaultSize = 50
	interactionsMaxSize     = 100
	// interactionsSourceCap bounds each source's per-window scan.
	interactionsSourceCap = 2000
)

// InboxSource is one read model contributing inbox hits (qa, socialcontent).
type InboxSource interface {
	InboxHits(owner string, sinceSec int64, types map[string]bool, afterTs int64, afterPinId string, limit int) ([]inbox.Hit, error)
}

type interactionItem struct {
	Type        string      `json:"type"`
	PinId       string      `json:"pinId"`
	ChainName   string      `json:"chainName"`
	TargetPinId string      `json:"targetPinId"`
	Actor       freshAuthor `json:"actor"`
	CreatedAt   int64       `json:"createdAt"`
	Excerpt     string      `json:"excerpt"`
	IsMempool   bool        `json:"isMempool,omitempty"`
	Dislike     bool        `json:"dislike,omitempty"`
}

type interactionsData struct {
	Items      []interactionItem `json:"items"`
	HasMore    bool              `json:"hasMore"`
	NextCursor *string           `json:"nextCursor"`
	ServerTime int64             `json:"serverTime"`
}

// SetInteractionSources injects the inbox read models (call once).
func (a *Aggregator) SetInteractionSources(sources ...InboxSource) {
	a.interactionSources = sources
}

func (a *Aggregator) handleInteractions(c *gin.Context) {
	owner := strings.TrimSpace(c.Query("owner"))
	if owner == "" {
		api.RespErr(c, codeInvalidParam, "owner is required")
		return
	}

	since, errMsg := parseOptionalUnix(c.Query("since"))
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}

	types, errMsg := parseInteractionTypes(c.Query("types"))
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}

	size := interactionsDefaultSize
	if raw := strings.TrimSpace(c.Query("size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			api.RespErr(c, codeInvalidParam, "invalid size")
			return
		}
		if parsed > interactionsMaxSize {
			parsed = interactionsMaxSize
		}
		size = parsed
	}

	afterTs, afterPinId, errMsg := decodeInteractionCursor(c.Query("cursor"))
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	if len(a.interactionSources) == 0 {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	hits := make([]inbox.Hit, 0, 64)
	for _, source := range a.interactionSources {
		if source == nil {
			continue
		}
		sourceHits, err := source.InboxHits(strings.ToLower(owner), since.value, types, afterTs, afterPinId, interactionsSourceCap)
		if err != nil {
			api.RespErr(c, codeUnavailable, "aggregation unavailable")
			return
		}
		hits = append(hits, sourceHits...)
	}

	// Contract order: createdAt DESC, pinId DESC (the documented tiebreak).
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].CreatedAt != hits[j].CreatedAt {
			return hits[i].CreatedAt > hits[j].CreatedAt
		}
		return hits[i].PinId > hits[j].PinId
	})

	hasMore := len(hits) > size
	if hasMore {
		hits = hits[:size]
	}

	items := make([]interactionItem, 0, len(hits))
	for _, hit := range hits {
		item := interactionItem{
			Type:        hit.Type,
			PinId:       hit.PinId,
			ChainName:   hit.ChainName,
			TargetPinId: hit.TargetPinId,
			Actor: freshAuthor{
				Address:      hit.ActorAddress,
				MetaId:       hit.ActorMetaId,
				GlobalMetaId: hit.ActorGlobalMetaId,
			},
			CreatedAt: hit.CreatedAt,
			Excerpt:   hit.Excerpt,
			IsMempool: hit.IsMempool,
			Dislike:   hit.Dislike,
		}
		item.Actor.Name = a.creatorName(hit.ActorGlobalMetaId, hit.ActorMetaId)
		items = append(items, item)
	}

	data := interactionsData{
		Items:      items,
		HasMore:    hasMore,
		ServerTime: a.now() / 1000,
	}
	if hasMore && len(items) > 0 {
		last := items[len(items)-1]
		encoded := encodeInteractionCursor(last.CreatedAt, last.PinId)
		data.NextCursor = &encoded
	}
	api.RespSuccess(c, data)
}

func parseInteractionTypes(raw string) (map[string]bool, string) {
	types := map[string]bool{
		inbox.TypePayLike:      true,
		inbox.TypePayComment:   true,
		inbox.TypeSimpleAnswer: true,
	}
	if raw = strings.TrimSpace(raw); raw == "" {
		return types, ""
	}
	types = map[string]bool{}
	for _, name := range strings.Split(raw, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		switch name {
		case inbox.TypePayLike, inbox.TypePayComment, inbox.TypeSimpleAnswer:
			types[name] = true
		default:
			return nil, "unsupported type: " + name
		}
	}
	if len(types) == 0 {
		return nil, "types is required when present"
	}
	return types, ""
}

func encodeInteractionCursor(ts int64, pinId string) string {
	return "c:" + base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(ts, 10)+":"+pinId))
}

func decodeInteractionCursor(cursor string) (int64, string, string) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, "", ""
	}
	if !strings.HasPrefix(cursor, "c:") {
		return 0, "", "invalid cursor"
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(cursor, "c:"))
	if err != nil {
		return 0, "", "invalid cursor"
	}
	sep := strings.IndexByte(string(raw), ':')
	if sep <= 0 {
		return 0, "", "invalid cursor"
	}
	ts, err := strconv.ParseInt(string(raw[:sep]), 10, 64)
	if err != nil || ts < 0 {
		return 0, "", "invalid cursor"
	}
	return ts, string(raw[sep+1:]), ""
}
