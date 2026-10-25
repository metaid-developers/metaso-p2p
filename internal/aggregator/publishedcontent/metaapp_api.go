package publishedcontent

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// registerMetaAppRoutes mounts the public MetaApp aggregation endpoints
// described in docs/specs/2026-07-26-metaapp-query-api.md. Envelope follows
// the metaso-p2p native convention (code=0 success; 40000/40400/50000),
// same as the bot-hub skill-service family.
func registerMetaAppRoutes(a *Aggregator, router *gin.RouterGroup) {
	m := router.Group("/metaapp")
	m.GET("/list", a.handleMetaAppList)
	m.GET("/detail/:pinId", a.handleMetaAppDetail)
	m.GET("/forks/:pinId", a.handleMetaAppForks)
}

func (a *Aggregator) handleMetaAppList(c *gin.Context) {
	params, err := parseMetaAppListParams(c)
	if err != nil {
		api.RespErr(c, 40000, err.Error())
		return
	}
	result, err := a.ListMetaApps(params)
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

func (a *Aggregator) handleMetaAppDetail(c *gin.Context) {
	pinID := strings.TrimSpace(c.Param("pinId"))
	if pinID == "" {
		api.RespErr(c, 40000, "pinId required")
		return
	}
	result, err := a.MetaAppDetail(pinID, c.Query("chainName"))
	if err != nil {
		api.RespErr(c, 50000, "aggregation unavailable")
		return
	}
	if result == nil {
		api.RespErr(c, 40400, "metaapp not found")
		return
	}
	api.RespSuccess(c, result)
}

func (a *Aggregator) handleMetaAppForks(c *gin.Context) {
	pinID := strings.TrimSpace(c.Param("pinId"))
	if pinID == "" {
		api.RespErr(c, 40000, "pinId required")
		return
	}
	size, err := parseMetaAppSize(c.Query("size"))
	if err != nil {
		api.RespErr(c, 40000, err.Error())
		return
	}
	result, found, err := a.ListMetaAppForks(pinID, c.Query("chainName"), size, c.Query("cursor"))
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor") {
			api.RespErr(c, 40000, "invalid cursor")
			return
		}
		api.RespErr(c, 50000, "aggregation unavailable")
		return
	}
	if !found {
		api.RespErr(c, 40400, "metaapp not found")
		return
	}
	api.RespSuccess(c, result)
}

func parseMetaAppListParams(c *gin.Context) (MetaAppListParams, error) {
	size, err := parseMetaAppSize(c.Query("size"))
	if err != nil {
		return MetaAppListParams{}, err
	}
	since, err := parseMetaAppTimestamp(c.Query("since"))
	if err != nil {
		return MetaAppListParams{}, err
	}
	until, err := parseMetaAppTimestamp(c.Query("until"))
	if err != nil {
		return MetaAppListParams{}, err
	}
	includeDisabled, err := parseMetaAppIncludeDisabled(c.Query("includeDisabled"))
	if err != nil {
		return MetaAppListParams{}, err
	}
	preference, err := parseMetaAppPreferenceParams(c)
	if err != nil {
		return MetaAppListParams{}, err
	}
	return MetaAppListParams{
		Keyword:         c.Query("keyword"),
		Tag:             c.Query("tag"),
		ChainName:       c.Query("chainName"),
		Runtime:         c.Query("runtime"),
		Publisher:       c.Query("publisher"),
		Since:           since,
		Until:           until,
		IncludeDisabled: includeDisabled,
		Size:            size,
		Cursor:          c.Query("cursor"),

		Sort:      preference.sort,
		Order:     preference.order,
		Basis:     preference.basis,
		MinJudges: preference.minJudges,
		Learning:  preference.learning,
	}, nil
}

type metaAppPreferenceParams struct {
	sort      string
	order     string
	basis     string
	minJudges int
	learning  string
}

// parseMetaAppPreferenceParams validates the additive preference-listing
// parameters (metaso requirement R4). Existing calls without them keep their
// current behavior. basis=validated is reported honestly as unavailable with
// the stable message preference_validated_basis_unavailable (pre-R7).
func parseMetaAppPreferenceParams(c *gin.Context) (metaAppPreferenceParams, error) {
	params := metaAppPreferenceParams{order: "desc", basis: PreferenceBasisOpen}

	switch sortParam := strings.TrimSpace(c.Query("sort")); sortParam {
	case "":
	case "preference":
		params.sort = "preference"
	default:
		return params, errInvalidMetaAppSort
	}

	switch order := strings.TrimSpace(c.Query("order")); order {
	case "":
	case "desc":
		params.order = "desc"
	case "asc":
		params.order = "asc"
	default:
		return params, errInvalidMetaAppOrder
	}

	switch basis := strings.TrimSpace(c.Query("basis")); basis {
	case "":
	case PreferenceBasisOpen:
		params.basis = PreferenceBasisOpen
	case PreferenceBasisValidated:
		return params, errPreferenceValidatedBasisUnavailable
	default:
		return params, errInvalidMetaAppBasis
	}

	minJudgesRaw := strings.TrimSpace(c.Query("minJudges"))
	if minJudgesRaw != "" {
		n, err := strconv.Atoi(minJudgesRaw)
		if err != nil || n < 0 {
			return params, errInvalidMetaAppMinJudges
		}
		params.minJudges = n
	}

	switch learning := strings.TrimSpace(c.Query("learning")); learning {
	case "":
	case LearningStatusDeclared, LearningStatusIncomplete:
		params.learning = learning
	default:
		return params, errInvalidMetaAppLearning
	}

	if params.sort == "preference" && strings.TrimSpace(c.Query("chainName")) == "" {
		return params, errMetaAppPreferenceChainRequired
	}
	return params, nil
}

func parseMetaAppSize(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, errInvalidMetaAppSize
	}
	return n, nil
}

func parseMetaAppTimestamp(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, errInvalidMetaAppTimestamp
	}
	return n, nil
}

func parseMetaAppIncludeDisabled(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "", "0", "false", "FALSE", "no":
		return false, nil
	case "1", "true", "TRUE", "yes":
		return true, nil
	default:
		return false, errInvalidMetaAppIncludeDisabled
	}
}

var (
	errInvalidMetaAppSize             = metaAppParamErr("invalid size")
	errInvalidMetaAppTimestamp        = metaAppParamErr("invalid since/until")
	errInvalidMetaAppIncludeDisabled  = metaAppParamErr("invalid includeDisabled")
	errInvalidMetaAppSort             = metaAppParamErr("invalid sort")
	errInvalidMetaAppOrder            = metaAppParamErr("invalid order")
	errInvalidMetaAppBasis            = metaAppParamErr("invalid basis")
	errInvalidMetaAppMinJudges        = metaAppParamErr("invalid minJudges")
	errInvalidMetaAppLearning         = metaAppParamErr("invalid learning")
	errMetaAppPreferenceChainRequired = metaAppParamErr("sort=preference requires chainName")
	// errPreferenceValidatedBasisUnavailable carries the stable pre-R7
	// message contract consumed by IDBots (metaso requirement R4).
	errPreferenceValidatedBasisUnavailable = metaAppParamErr("preference_validated_basis_unavailable")
)

type metaAppParamErr string

func (e metaAppParamErr) Error() string { return string(e) }
