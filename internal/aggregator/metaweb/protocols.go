package metaweb

// GET /api/metaweb/protocols — the validated metaprotocol registry (R6):
// paginated descriptor list over the fresh-index metaprotocol records, with
// a strict path-shape validation and an honest per-page `rejected` audit
// list instead of the raw path-lists' silent inclusion of poisoned payloads
// and ~22KB truncation ceiling. See docs/specs/2026-09-13-metaweb-surf-reads-api.md §5.

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// protocolPathPattern is the documented registry validation: a metaprotocol
// descriptor's payload `path` must name a protocol directory under
// /protocols/. This rejects the observed poisoned payloads (upstream error
// text landing in the path field).
var protocolPathPattern = regexp.MustCompile(`^/protocols/[a-z0-9_]+(/[a-z0-9_]+)*$`)

type protocolItem struct {
	PinId        string      `json:"pinId"`
	CurrentPinId string      `json:"currentPinId"`
	ChainName    string      `json:"chainName"`
	CreatedAt    int64       `json:"createdAt"`
	Author       freshAuthor `json:"author"`
	Path         string      `json:"path"`
	Title        string      `json:"title"`
	ProtocolName string      `json:"protocolName,omitempty"`
	Intro        string      `json:"intro,omitempty"`
	Version      string      `json:"version,omitempty"`
}

type rejectedProtocol struct {
	PinId  string `json:"pinId"`
	Reason string `json:"reason"`
}

type protocolsData struct {
	Items      []protocolItem     `json:"items"`
	Rejected   []rejectedProtocol `json:"rejected"`
	HasMore    bool               `json:"hasMore"`
	NextCursor *string            `json:"nextCursor"`
}

func (a *Aggregator) handleProtocols(c *gin.Context) {
	if a.freshLookup == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	size := freshDefaultSize
	if raw := strings.TrimSpace(c.Query("size")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			api.RespErr(c, codeInvalidParam, "invalid size")
			return
		}
		if parsed > freshMaxSize {
			parsed = freshMaxSize
		}
		size = parsed
	}
	page, err := a.freshLookup.Fresh(publishedcontent.FreshParams{
		ProtocolPaths: []string{publishedcontent.PathMetaProtocol},
		Size:          size,
		Cursor:        strings.TrimSpace(c.Query("cursor")),
	})
	if err != nil {
		if err == publishedcontent.ErrInvalidFreshCursor {
			api.RespErr(c, codeInvalidParam, "invalid cursor")
			return
		}
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	items := make([]protocolItem, 0, len(page.Records))
	rejected := make([]rejectedProtocol, 0)
	for _, rec := range page.Records {
		// The fresh index never contains revoked records, so revoked
		// descriptors are excluded by construction.
		if reason := validateProtocolRecord(rec); reason != "" {
			rejected = append(rejected, rejectedProtocol{PinId: rec.SourcePinId, Reason: reason})
			continue
		}
		extracted := metawebdoc.ExtractPublished(rec.ProtocolPath, rec.PayloadJSON, rec.PayloadText)
		currentPinId := rec.CurrentPinId
		if currentPinId == "" {
			currentPinId = rec.SourcePinId
		}
		items = append(items, protocolItem{
			PinId:        rec.SourcePinId,
			CurrentPinId: currentPinId,
			ChainName:    rec.ChainName,
			CreatedAt:    metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
			Author: freshAuthor{
				Address:      rec.PublisherAddress,
				MetaId:       rec.PublisherMetaId,
				GlobalMetaId: rec.PublisherGlobalMetaId,
				Name:         a.creatorName(rec.PublisherGlobalMetaId, rec.PublisherMetaId),
			},
			Path:         stringFieldOf(rec.PayloadJSON, "path"),
			Title:        extracted.Title,
			ProtocolName: stringFieldOf(rec.PayloadJSON, "protocolName"),
			Intro:        stringFieldOf(rec.PayloadJSON, "intro"),
			Version:      stringFieldOf(rec.PayloadJSON, "version"),
		})
	}

	data := protocolsData{Items: items, Rejected: rejected, HasMore: page.HasMore}
	if page.HasMore && page.NextCursor != "" {
		cursor := page.NextCursor
		data.NextCursor = &cursor
	}
	api.RespSuccess(c, data)
}

// validateProtocolRecord applies the registry's validation rules; a non-empty
// return value is the rejection reason.
func validateProtocolRecord(rec *publishedcontent.Record) string {
	if rec == nil || !rec.PayloadExposed || rec.PayloadJSON == nil {
		return "missing or non-JSON payload"
	}
	path := stringFieldOf(rec.PayloadJSON, "path")
	if path == "" {
		return "missing path"
	}
	if !protocolPathPattern.MatchString(path) {
		return "invalid path: " + path
	}
	return ""
}
