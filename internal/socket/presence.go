package socket

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const defaultPresenceSnapshotPath = "/.well-known/metasocket/presence"

// HandleOnlineStats returns the total number of active connections.
func (s *Server) HandleOnlineStats(c *gin.Context) {
	scope := s.resolvePresenceScope(c)
	reader := s.presenceGlobalReader()
	if scope == "global" && reader != nil && reader.Enabled() {
		stats := reader.Stats(s.manager.OnlineEntries())
		c.JSON(http.StatusOK, gin.H{
			"code": 0,
			"data": gin.H{
				"totalConnections": stats.TotalConnections,
				"uniqueMetaIds":    stats.UniqueMetaIds,
				"nodes":            stats.Nodes,
			},
			"message":        "",
			"processingTime": time.Now().UnixMilli(),
		})
		return
	}

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

	var items []OnlineEntry
	scope := s.resolvePresenceScope(c)
	reader := s.presenceGlobalReader()
	if scope == "global" && reader != nil && reader.Enabled() {
		items = onlineEntriesFromPresence(reader.OnlineList(s.manager.OnlineEntries(), page, size))
	} else {
		items = s.manager.OnlineList(page, size)
	}
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

// HandleIdchatOnlineUsers returns the old idchat /group-chat/socket/online-users shape.
func (s *Server) HandleIdchatOnlineUsers(c *gin.Context) {
	size, err := strconv.Atoi(c.DefaultQuery("size", "20"))
	if err != nil || size < 1 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	cursor := c.DefaultQuery("cursor", "")

	items := s.onlineItemsForIDChat(size)
	rows := make([]gin.H, 0, len(items))
	now := time.Now().UnixMilli()
	for _, item := range items {
		lastSeenAt := item.LastSeenAt
		if lastSeenAt == 0 {
			lastSeenAt = item.ConnectedAt
		}
		agoSeconds := int64(0)
		if lastSeenAt > 0 && now > lastSeenAt {
			agoSeconds = (now - lastSeenAt) / 1000
		}
		globalMetaId := item.MetaId
		if item.UserInfo != nil && item.UserInfo.GlobalMetaId != "" {
			globalMetaId = item.UserInfo.GlobalMetaId
		}
		rows = append(rows, gin.H{
			"globalMetaId":       globalMetaId,
			"lastSeenAt":         lastSeenAt,
			"lastSeenAgoSeconds": agoSeconds,
			"deviceCount":        1,
			"userInfo":           item.UserInfo,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"data": gin.H{
			"total":               len(rows),
			"cursor":              cursor,
			"size":                size,
			"onlineWindowSeconds": 35,
			"list":                rows,
		},
		"message":        "",
		"processingTime": time.Now().UnixMilli(),
	})
}

// HandleIdchatUserOnline returns whether the queried identity has an active connection.
func (s *Server) HandleIdchatUserOnline(c *gin.Context) {
	metaId := strings.TrimSpace(c.Query("metaId"))
	if metaId == "" {
		metaId = strings.TrimSpace(c.Query("globalMetaId"))
	}
	online := false
	if metaId != "" {
		for _, entry := range s.manager.OnlineEntries() {
			if entry.MetaId == metaId {
				online = true
				break
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"code":           0,
		"data":           gin.H{"online": online},
		"message":        "",
		"processingTime": time.Now().UnixMilli(),
	})
}

func (s *Server) onlineItemsForIDChat(size int) []OnlineEntry {
	scope := "local"
	reader := s.presenceGlobalReader()
	if reader != nil && reader.Enabled() && strings.ToLower(strings.TrimSpace(reader.DefaultScope())) == "global" {
		scope = "global"
	}
	var items []OnlineEntry
	if scope == "global" && reader != nil && reader.Enabled() {
		items = onlineEntriesFromPresence(reader.OnlineList(s.manager.OnlineEntries(), 1, size))
	} else {
		items = s.manager.OnlineList(1, size)
	}
	if items == nil {
		items = []OnlineEntry{}
	}
	return s.hydrateOnlineEntries(items)
}

// HandlePresenceSnapshot returns the local federated presence snapshot.
func (s *Server) HandlePresenceSnapshot(c *gin.Context) {
	provider := s.presenceSnapshotProvider()
	if provider == nil {
		c.Status(http.StatusNotFound)
		return
	}

	snapshot, err := provider.Snapshot()
	if err != nil || snapshot == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "presence snapshot unavailable"})
		return
	}

	c.JSON(http.StatusOK, snapshot)
}

// RegisterPresenceRoutes mounts presence HTTP endpoints on the given Gin router.
func (s *Server) RegisterPresenceRoutes(router *gin.Engine, snapshotPath ...string) {
	socketGroup := router.Group("/socket")
	{
		socketGroup.GET("/online/stats", s.HandleOnlineStats)
		socketGroup.GET("/online/list", s.HandleOnlineList)
	}

	path := defaultPresenceSnapshotPath
	if len(snapshotPath) > 0 && snapshotPath[0] != "" {
		path = snapshotPath[0]
	}
	router.GET(path, s.HandlePresenceSnapshot)
}

func (s *Server) resolvePresenceScope(c *gin.Context) string {
	requested := strings.ToLower(strings.TrimSpace(c.Query("scope")))
	switch requested {
	case "local", "global":
		return requested
	case "":
		reader := s.presenceGlobalReader()
		if reader != nil && reader.Enabled() && strings.ToLower(strings.TrimSpace(reader.DefaultScope())) == "global" {
			return "global"
		}
	}
	return "local"
}
