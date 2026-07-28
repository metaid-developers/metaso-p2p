package userinfo

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// registerMetaIDSearchRoutes mounts the public MetaID search endpoints
// described in docs/specs/2026-07-28-metaid-search-api.md. Envelope follows
// the metaso-p2p native convention (code=0 success; 40000/40400/50000),
// identical to the MetaApp aggregation API so downstream learns one shape.
func registerMetaIDSearchRoutes(a *Aggregator, router *gin.RouterGroup) {
	m := router.Group("/metaid")
	m.GET("/list", a.handleMetaIDList)
	m.GET("/detail/:identity", a.handleMetaIDDetail)
}

func (a *Aggregator) handleMetaIDList(c *gin.Context) {
	params, err := parseMetaIDListParams(c)
	if err != nil {
		api.RespErr(c, 40000, err.Error())
		return
	}
	result, err := a.ListMetaIDs(params)
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

func (a *Aggregator) handleMetaIDDetail(c *gin.Context) {
	identity := strings.TrimSpace(c.Param("identity"))
	if identity == "" {
		api.RespErr(c, 40000, "identity required")
		return
	}
	result, err := a.MetaIDDetail(identity)
	if err != nil {
		api.RespErr(c, 50000, "aggregation unavailable")
		return
	}
	if result == nil {
		api.RespErr(c, 40400, "user not found")
		return
	}
	api.RespSuccess(c, result)
}

func parseMetaIDListParams(c *gin.Context) (MetaIDListParams, error) {
	size, err := parseMetaIDSize(c.Query("size"))
	if err != nil {
		return MetaIDListParams{}, err
	}
	since, err := parseMetaIDTimestamp(c.Query("since"))
	if err != nil {
		return MetaIDListParams{}, err
	}
	until, err := parseMetaIDTimestamp(c.Query("until"))
	if err != nil {
		return MetaIDListParams{}, err
	}
	hasChatPubkey, err := parseMetaIDBoolFlag("hasChatPubkey", c.Query("hasChatPubkey"))
	if err != nil {
		return MetaIDListParams{}, err
	}
	hasHomepage, err := parseMetaIDBoolFlag("hasHomepage", c.Query("hasHomepage"))
	if err != nil {
		return MetaIDListParams{}, err
	}
	return MetaIDListParams{
		Keyword:       c.Query("keyword"),
		Skill:         c.Query("skill"),
		ChainName:     c.Query("chainName"),
		HasChatPubkey: hasChatPubkey,
		HasHomepage:   hasHomepage,
		Since:         since,
		Until:         until,
		Size:          size,
		Cursor:        c.Query("cursor"),
	}, nil
}

func parseMetaIDSize(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, metaIDParamErr("invalid size")
	}
	return n, nil
}

func parseMetaIDTimestamp(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, metaIDParamErr("invalid since/until")
	}
	return n, nil
}

func parseMetaIDBoolFlag(name, raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "", "0", "false", "FALSE", "no":
		return false, nil
	case "1", "true", "TRUE", "yes":
		return true, nil
	default:
		return false, metaIDParamErr("invalid " + name)
	}
}

type metaIDParamErr string

func (e metaIDParamErr) Error() string { return string(e) }
