package socket

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// HandleOnlineStats returns the total number of active connections.
func (s *Server) HandleOnlineStats(c *gin.Context) {
	total := s.manager.TotalConnections()
	c.JSON(http.StatusOK, gin.H{
		"code":           0,
		"data":           gin.H{"totalConnections": total},
		"message":        "",
		"processingTime": time.Now().UnixMilli(),
	})
}

// HandleOnlineList returns a paginated list of online connections.
func (s *Server) HandleOnlineList(c *gin.Context) {
	pageStr := c.DefaultQuery("page", "1")
	sizeStr := c.DefaultQuery("size", "20")

	page, err := strconv.Atoi(pageStr)
	if err != nil || page < 1 {
		page = 1
	}

	size, err := strconv.Atoi(sizeStr)
	if err != nil || size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}

	items := s.manager.OnlineList(page, size)
	if items == nil {
		items = []OnlineEntry{}
	}
	items = s.hydrateOnlineEntries(items)

	c.JSON(http.StatusOK, gin.H{
		"code":           0,
		"data":           gin.H{"items": items},
		"message":        "",
		"processingTime": time.Now().UnixMilli(),
	})
}

// RegisterPresenceRoutes mounts presence HTTP endpoints on the given Gin router.
func (s *Server) RegisterPresenceRoutes(router *gin.Engine) {
	socketGroup := router.Group("/socket")
	{
		socketGroup.GET("/online/stats", s.HandleOnlineStats)
		socketGroup.GET("/online/list", s.HandleOnlineList)
	}
}
