package skillservice

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// registerRoutes mounts the Bot Hub skill-service endpoints.
//
// Response envelope follows metaso-p2p native conventions: code=0 on
// success, code in {40000, 40400, 50000} on failure, HTTP 200 in all
// cases except true infrastructure failures. The /api/bot-hub/skill-
// service/* family does NOT inherit the code=1 success convention used
// by /api/info/* — that one is only there because meta-file-system has
// to be drop-in compatible with idchat's legacy man.ts client.
func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	bh := router.Group("/bot-hub/skill-service")

	// List endpoint (M5).
	bh.GET("/list", a.handleList)

	bh.GET("/detail/:serviceId", a.handleDetail)
}

// handleList parses the spec's query parameters into ListParams, runs the
// filter / sort / paginate pipeline (Aggregator.List), and emits the
// response envelope. Any internal error becomes code=50000 so the front
// end can fail closed; an invalid cursor is code=40000 — the cursor is
// caller-supplied input, not a server fault.
func (a *Aggregator) handleList(c *gin.Context) {
	p, err := parseListParams(c)
	if err != nil {
		api.RespErr(c, 40000, err.Error())
		return
	}

	result, err := a.List(p)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor") {
			api.RespErr(c, 40000, "invalid cursor")
			return
		}
		api.RespErr(c, 50000, "aggregation unavailable")
		return
	}
	api.RespSuccess(c, result)
}

// parseListParams pulls the documented query parameters out of the
// request. Size and includeInactive parsing tolerate empty values; sort /
// order normalisation lives inside Aggregator.List so the parsing layer
// stays small and predictable.
func parseListParams(c *gin.Context) (ListParams, error) {
	size := 0
	if raw := c.Query("size"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return ListParams{}, errInvalidSize
		}
		size = n
	}

	includeInactive := false
	if raw := c.Query("includeInactive"); raw != "" {
		switch strings.TrimSpace(raw) {
		case "1", "true", "TRUE", "yes":
			includeInactive = true
		case "0", "false", "FALSE", "no", "":
			includeInactive = false
		default:
			return ListParams{}, errInvalidIncludeInactive
		}
	}

	return ListParams{
		Size:                 size,
		Cursor:               c.Query("cursor"),
		Keyword:              c.Query("keyword"),
		Currency:             c.Query("currency"),
		ChainName:            c.Query("chainName"),
		OutputType:           c.Query("outputType"),
		ProviderGlobalMetaId: c.Query("providerGlobalMetaId"),
		SortBy:               c.Query("sortBy"),
		Order:                c.Query("order"),
		IncludeInactive:      includeInactive,
	}, nil
}

// Sentinel parsing errors so handleList can return them with stable
// messages. Keeping them as values (rather than string literals at the
// call site) lets tests assert on them without coupling to wording.
var (
	errInvalidSize            = listErr("invalid size")
	errInvalidIncludeInactive = listErr("invalid includeInactive")
)

type listErr string

func (e listErr) Error() string { return string(e) }

// handleDetail serves GET /api/bot-hub/skill-service/detail/:serviceId.
func (a *Aggregator) handleDetail(c *gin.Context) {
	serviceID := strings.TrimSpace(c.Param("serviceId"))
	if serviceID == "" {
		api.RespErr(c, 40000, "serviceId required")
		return
	}

	result, err := a.Detail(DetailParams{
		ServiceID: serviceID,
		ChainName: c.Query("chainName"),
		IDType:    c.Query("idType"),
	})
	if err != nil {
		if errors.Is(err, errInvalidIDType) {
			api.RespErr(c, 40000, detailErr(err))
			return
		}
		if errors.Is(err, errAmbiguousLookup) {
			api.RespErr(c, 40000, "ambiguous serviceId")
			return
		}
		api.RespErr(c, 50000, detailErr(err))
		return
	}
	if result == nil {
		api.RespErr(c, 40400, "service not found")
		return
	}
	api.RespSuccess(c, result)
}
