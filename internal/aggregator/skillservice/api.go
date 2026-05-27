package skillservice

import (
	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/meta-socket/internal/api"
)

// registerRoutes mounts the Bot Hub skill-service endpoints. M1 only
// reserves the two path slots so the spec's URL contract is locked in
// from the start; the real handlers replace these stubs in M5 (list) and
// M6 (detail).
//
// Response envelope follows meta-socket native conventions: code=0 on
// success, code in {40000, 40400, 50000} on failure, HTTP 200 in all
// cases except true infrastructure failures. The /api/bot-hub/skill-
// service/* family does NOT inherit the code=1 success convention used
// by /api/info/* — that one is only there because meta-file-system has
// to be drop-in compatible with idchat's legacy man.ts client.
func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	bh := router.Group("/bot-hub/skill-service")

	// List endpoint. Real implementation lands in M5.
	bh.GET("/list", a.handleListStub)

	// Detail endpoint. Real implementation lands in M6.
	bh.GET("/detail/:serviceId", a.handleDetailStub)
}

// handleListStub returns an empty success envelope so the URL contract is
// reachable from M1 onward. It is replaced wholesale in M5.
func (a *Aggregator) handleListStub(c *gin.Context) {
	api.RespSuccess(c, gin.H{
		"list":          []any{},
		"nextCursor":    "",
		"total":         nil,
		"aggregatedAt":  0,
		"schemaVersion": "botHubSkillService.v1",
	})
}

// handleDetailStub returns 40400 not_found so clients hitting the endpoint
// before M6 ships get a clean failure rather than a misleading empty
// service body. It is replaced wholesale in M6.
func (a *Aggregator) handleDetailStub(c *gin.Context) {
	api.RespErr(c, 40400, "service not found")
}
