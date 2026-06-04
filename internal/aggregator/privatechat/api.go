package privatechat

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/meta-socket/internal/api"
)

// registerRoutes mounts all private-chat HTTP endpoints on the given router group.
func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	gc := router.Group("/group-chat")

	// Private chat message list with cursor pagination
	gc.GET("/private-chat-list", a.handlePrivateChatList)

	// Private chat message list with index-based pagination
	gc.GET("/private-chat-list-by-index", a.handlePrivateChatListByIndex)

	// Private chat paths for a user
	gc.GET("/private-group-paths", a.handlePrivateGroupPaths)

	// Chat homes: list of conversation partners with last message preview
	gc.GET("/chat/homes/:metaid", a.handleChatHomes)

	pc := router.Group("/private-chat")

	// Canonical private-chat namespace for native meta-socket clients. These
	// routes intentionally share handlers with the historical group-chat paths
	// so response envelopes and query semantics stay identical.
	pc.GET("/messages", a.handlePrivateChatList)
	pc.GET("/messages/by-index", a.handlePrivateChatListByIndex)
	pc.GET("/paths", a.handlePrivateGroupPaths)
	pc.GET("/homes/:metaid", a.handleChatHomes)
}

// handlePrivateChatList returns bidirectionally filtered messages between two users
// with cursor-based pagination.
func (a *Aggregator) handlePrivateChatList(c *gin.Context) {
	metaId := c.Query("metaId")
	otherMetaId := c.Query("otherMetaId")
	if metaId == "" || otherMetaId == "" {
		api.RespErr(c, 1, "metaId and otherMetaId are required")
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	timestamp, _ := strconv.ParseInt(c.DefaultQuery("timestamp", "0"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	result, err := a.GetPrivateChatList(metaId, otherMetaId, cursor, size, timestamp)
	if err != nil {
		api.RespErr(c, 1, "failed to get private chat list")
		return
	}

	api.RespSuccess(c, result)
}

// handlePrivateChatListByIndex returns messages by startIndex.
func (a *Aggregator) handlePrivateChatListByIndex(c *gin.Context) {
	metaId := c.Query("metaId")
	otherMetaId := c.Query("otherMetaId")
	if metaId == "" || otherMetaId == "" {
		api.RespErr(c, 1, "metaId and otherMetaId are required")
		return
	}

	startIndex, _ := strconv.ParseInt(c.DefaultQuery("startIndex", "0"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	result, err := a.GetPrivateChatListByIndex(metaId, otherMetaId, startIndex, size)
	if err != nil {
		api.RespErr(c, 1, "failed to get private chat list by index")
		return
	}

	api.RespSuccess(c, result)
}

// handlePrivateGroupPaths returns the list of paths where the user has private chat.
func (a *Aggregator) handlePrivateGroupPaths(c *gin.Context) {
	metaId := c.Query("metaId")
	if metaId == "" {
		api.RespErr(c, 1, "metaId is required")
		return
	}

	paths, err := a.GetPrivateGroupPaths(metaId)
	if err != nil {
		api.RespErr(c, 1, "failed to get private group paths")
		return
	}

	api.RespSuccess(c, gin.H{
		"total": len(paths),
		"list":  paths,
	})
}

// handleChatHomes returns the conversation list with last message preview.
func (a *Aggregator) handleChatHomes(c *gin.Context) {
	metaid := c.Param("metaid")
	if metaid == "" {
		api.RespErr(c, 1, "metaid is required")
		return
	}

	homes, err := a.GetPrivateChatHomes(metaid)
	if err != nil {
		api.RespErr(c, 1, "failed to get chat homes")
		return
	}

	api.RespSuccess(c, gin.H{
		"list": homes,
	})
}
