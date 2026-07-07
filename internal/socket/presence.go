package socket

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

const defaultPresenceSnapshotPath = "/.well-known/metaso-p2p/presence"

const globalPresenceLookupFallbackSize = 10000

type globalPresenceStatus struct {
	GlobalMetaId  string   `json:"globalMetaId"`
	State         string   `json:"state"`
	UpdatedAt     *int64   `json:"updatedAt"`
	Source        string   `json:"source"`
	Scope         string   `json:"scope"`
	SourceNodeIds []string `json:"sourceNodeIds,omitempty"`
	Connections   int      `json:"connections,omitempty"`
}

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

// HandleGlobalPresenceByGlobalMetaId returns the merged global presence status
// for one requested globalMetaId. It requires an enabled global reader and does
// not fall back to local-only socket state, because the contract is explicitly
// global.
func (s *Server) HandleGlobalPresenceByGlobalMetaId(c *gin.Context) {
	globalMetaId := strings.TrimSpace(c.Param("globalMetaId"))
	if globalMetaId == "" {
		c.JSON(http.StatusOK, gin.H{
			"code":    40000,
			"message": "globalMetaId required",
		})
		return
	}

	reader := s.presenceGlobalReader()
	if reader == nil || !reader.Enabled() {
		c.JSON(http.StatusOK, gin.H{
			"code":    50000,
			"message": "global presence unavailable",
		})
		return
	}

	local := s.manager.OnlineEntries()
	items := s.globalPresenceEntries(reader, local)
	status := s.globalPresenceStatus(globalMetaId, items)
	c.JSON(http.StatusOK, gin.H{
		"code":           0,
		"data":           status,
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
	router.GET("/api/presence/globalmetaid/:globalMetaId", s.HandleGlobalPresenceByGlobalMetaId)

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

type globalPresenceEntriesReader interface {
	OnlineEntries(local []presence.OnlineEntry) []presence.OnlineEntry
}

func (s *Server) globalPresenceEntries(reader presence.GlobalReader, local []presence.OnlineEntry) []presence.OnlineEntry {
	if allReader, ok := reader.(globalPresenceEntriesReader); ok {
		return allReader.OnlineEntries(local)
	}
	return reader.OnlineList(local, 1, globalPresenceLookupFallbackSize)
}

func (s *Server) globalPresenceStatus(requestedGlobalMetaId string, items []presence.OnlineEntry) globalPresenceStatus {
	requestedGlobalMetaId = strings.TrimSpace(requestedGlobalMetaId)
	status := globalPresenceStatus{
		GlobalMetaId: requestedGlobalMetaId,
		State:        "unknown",
		Scope:        "global",
	}

	candidates := s.presenceIdentityCandidates(requestedGlobalMetaId)
	matches := findMatchingPresenceEntries(items, candidates)
	if len(matches) == 0 {
		return status
	}

	status.State = "online"
	status.Source = "global-presence"

	var updatedAt int64
	connectionCount := 0
	sourceNodeSet := make(map[string]struct{})
	for _, item := range matches {
		itemUpdatedAt := item.LastSeenAt
		if itemUpdatedAt == 0 {
			itemUpdatedAt = item.ConnectedAt
		}
		if itemUpdatedAt > updatedAt {
			updatedAt = itemUpdatedAt
		}

		if item.Sources > 0 {
			connectionCount += item.Sources
		} else {
			connectionCount++
		}
		for _, nodeID := range item.SourceNodeIds {
			nodeID = strings.TrimSpace(nodeID)
			if nodeID == "" {
				continue
			}
			sourceNodeSet[nodeID] = struct{}{}
		}
	}
	if updatedAt > 0 {
		status.UpdatedAt = &updatedAt
	}
	if connectionCount > 0 {
		status.Connections = connectionCount
	}
	if len(sourceNodeSet) > 0 {
		status.SourceNodeIds = make([]string, 0, len(sourceNodeSet))
		for nodeID := range sourceNodeSet {
			status.SourceNodeIds = append(status.SourceNodeIds, nodeID)
		}
		sort.Strings(status.SourceNodeIds)
	}

	return status
}

func (s *Server) presenceIdentityCandidates(globalMetaId string) []string {
	candidates := []string{globalMetaId}
	profile := s.lookupPresenceProfile(globalMetaId)
	if profile == nil {
		return uniquePresenceCandidates(candidates)
	}
	return uniquePresenceCandidates([]string{
		globalMetaId,
		profile.GlobalMetaId,
		profile.MetaId,
		profile.Address,
	})
}

func uniquePresenceCandidates(values []string) []string {
	candidates := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		candidates = append(candidates, value)
	}
	return candidates
}

func findMatchingPresenceEntries(items []presence.OnlineEntry, candidates []string) []presence.OnlineEntry {
	if len(items) == 0 || len(candidates) == 0 {
		return nil
	}

	lookup := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		lookup[strings.ToLower(strings.TrimSpace(candidate))] = struct{}{}
	}

	matches := make([]presence.OnlineEntry, 0, len(items))
	for _, item := range items {
		if _, ok := lookup[strings.ToLower(strings.TrimSpace(item.MetaId))]; ok {
			matches = append(matches, item)
		}
	}
	return matches
}
