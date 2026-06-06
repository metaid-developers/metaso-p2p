package groupchat

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// registerRoutes mounts all group-chat HTTP endpoints on the given router group.
func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	gc := router.Group("/group-chat")

	// Community
	gc.GET("/community/list", a.handleCommunityList)
	gc.GET("/community/:communityId", a.handleCommunityDetail)

	// Community stubs
	gc.GET("/community/:communityId/auth/info", a.handleStub)
	gc.GET("/community/auths/:metaId", a.handleStub)
	gc.GET("/community/metaname/:address", a.handleStub)
	gc.GET("/community/ens/:address", a.handleStub)
	gc.GET("/community/:communityId/person/info", a.handleStub)
	gc.GET("/community/:communityId/persons", a.handleStub)
	gc.GET("/community/:communityId/announcements", a.handleStub)

	// Group
	gc.GET("/group-info", a.handleGroupInfo)
	gc.GET("/group-person", a.handleGroupPerson)
	gc.GET("/group-user-role", a.handleGroupUserRole)
	gc.GET("/group-member-list", a.handleGroupMemberList)
	gc.GET("/search-group-members", a.handleSearchGroupMembers)
	gc.GET("/group-list", a.handleGroupList)
	gc.GET("/group-join-control-list", a.handleGroupJoinControlList)
	gc.GET("/group-channel-list", a.handleGroupChannelList)
	gc.GET("/group-metaid-join-list", a.handleGroupMetaIdJoinList)

	// Chat Messages
	gc.GET("/group-chat-list", a.handleGroupChatList)
	gc.GET("/group-chat-list-v2", a.handleGroupChatListV2)
	gc.GET("/group-chat-list-v3", a.handleGroupChatListV3)
	gc.GET("/group-chat-list-by-index", a.handleGroupChatListByIndex)
	gc.GET("/user/latest-chat-info-list", a.handleUserLatestChatInfoList)
	gc.GET("/channel-chat-list-v3", a.handleChannelChatListV3)
	gc.GET("/channel-chat-list-by-index", a.handleChannelChatListByIndex)

	// Search
	gc.GET("/search-users", a.handleSearchUsers)
	gc.GET("/search-groups-and-users", a.handleSearchGroupsAndUsers)

	// Private chat routes are owned by the privatechat aggregator (see
	// internal/aggregator/privatechat/api.go). Both aggregators share the
	// /group-chat/ prefix because idchat does not distinguish between group
	// and private chats at the URL level.
}

// --- Community Handlers ---

func (a *Aggregator) handleCommunityList(c *gin.Context) {
	page, _ := strconv.ParseInt(c.DefaultQuery("page", "1"), 10, 64)
	pageSize, _ := strconv.ParseInt(c.DefaultQuery("pageSize", "20"), 10, 64)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}

	communities, _, err := a.ListCommunities(page, pageSize)
	if err != nil {
		api.RespErr(c, 1, "failed to list communities")
		return
	}

	if communities == nil {
		communities = []*Community{}
	}

	api.RespSuccess(c, gin.H{
		"results": gin.H{
			"items": communities,
		},
	})
}

func (a *Aggregator) handleCommunityDetail(c *gin.Context) {
	communityId := c.Param("communityId")
	if communityId == "" {
		api.RespErr(c, 1, "communityId is required")
		return
	}

	community, err := a.GetCommunity(communityId)
	if err != nil {
		api.RespErr(c, 1, "failed to get community")
		return
	}
	if community == nil {
		api.RespErr(c, 1, "community not found")
		return
	}

	api.RespSuccess(c, community)
}

// --- Group Handlers ---

func (a *Aggregator) handleGroupInfo(c *gin.Context) {
	groupId := c.Query("groupId")
	if groupId == "" {
		api.RespErr(c, 1, "groupId is required")
		return
	}

	group, err := a.GetGroup(groupId)
	if err != nil {
		api.RespErr(c, 1, "failed to get group")
		return
	}
	if group == nil {
		api.RespErr(c, 1, "group not found")
		return
	}

	api.RespSuccess(c, a.groupCompatDTO(group))
}

func (a *Aggregator) handleGroupPerson(c *gin.Context) {
	metaId := c.Query("metaId")
	groupId := c.Query("groupId")
	if metaId == "" || groupId == "" {
		api.RespErr(c, 1, "metaId and groupId are required")
		return
	}

	isInGroup, err := a.IsUserInGroup(groupId, metaId)
	if err != nil {
		api.RespErr(c, 1, "failed to check group membership")
		return
	}

	api.RespSuccess(c, gin.H{
		"isInGroup": isInGroup,
	})
}

func (a *Aggregator) handleGroupUserRole(c *gin.Context) {
	groupId := c.Query("groupId")
	metaId := c.Query("metaId")
	if groupId == "" || metaId == "" {
		api.RespErr(c, 1, "groupId and metaId are required")
		return
	}

	role, err := a.GetGroupUserRole(groupId, metaId)
	if err != nil {
		api.RespErr(c, 1, "failed to get user role")
		return
	}
	if role == nil {
		// Default: no special roles
		api.RespSuccess(c, gin.H{
			"isCreator":   false,
			"isAdmin":     false,
			"isBlocked":   false,
			"isWhitelist": false,
			"isRemoved":   false,
		})
		return
	}

	api.RespSuccess(c, gin.H{
		"isCreator":   role.IsCreator,
		"isAdmin":     role.IsAdmin,
		"isBlocked":   role.IsBlocked,
		"isWhitelist": role.IsWhitelist,
		"isRemoved":   role.IsRemoved,
	})
}

func (a *Aggregator) handleGroupMemberList(c *gin.Context) {
	groupId := c.Query("groupId")
	if groupId == "" {
		api.RespErr(c, 1, "groupId is required")
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	members, _, err := a.GetGroupMemberList(groupId, cursor, size)
	if err != nil {
		api.RespErr(c, 1, "failed to get member list")
		return
	}

	if members == nil {
		members = []*GroupMember{}
	}

	// Get group info for creator field
	var creator string
	var adminsList []*GroupMember
	var blockList []*GroupMember
	var whiteList []*GroupMember

	group, _ := a.GetGroup(groupId)
	if group != nil {
		creator = group.CreatorMetaId
	}

	for _, m := range members {
		if m.IsAdmin {
			adminsList = append(adminsList, m)
		}
		if m.IsBlocked {
			blockList = append(blockList, m)
		}
		if m.IsWhitelist {
			whiteList = append(whiteList, m)
		}
	}

	if adminsList == nil {
		adminsList = []*GroupMember{}
	}
	if blockList == nil {
		blockList = []*GroupMember{}
	}
	if whiteList == nil {
		whiteList = []*GroupMember{}
	}

	api.RespSuccess(c, gin.H{
		"admins":    adminsList,
		"blockList": blockList,
		"creator":   creator,
		"list":      members,
		"total":     len(members),
		"whiteList": whiteList,
	})
}

func (a *Aggregator) handleSearchGroupMembers(c *gin.Context) {
	groupId := c.Query("groupId")
	query := c.Query("query")
	if groupId == "" || query == "" {
		api.RespErr(c, 1, "groupId and query are required")
		return
	}

	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	members, err := a.SearchGroupMembers(groupId, query, size)
	if err != nil {
		api.RespErr(c, 1, "failed to search members")
		return
	}

	if members == nil {
		members = []*GroupMember{}
	}

	api.RespSuccess(c, gin.H{
		"total": len(members),
		"list":  members,
	})
}

func (a *Aggregator) handleGroupList(c *gin.Context) {
	metaId := c.Query("metaId")
	cursor := c.DefaultQuery("cursor", "")
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	groups, nextCursorVal, total, err := a.GetGroupList(metaId, cursor, size)
	if err != nil {
		api.RespErr(c, 1, "failed to get group list")
		return
	}

	api.RespSuccess(c, gin.H{
		"list":       a.groupCompatDTOs(groups),
		"nextCursor": nextCursorVal,
		"total":      total,
	})
}

func (a *Aggregator) handleGroupJoinControlList(c *gin.Context) {
	groupId := c.Query("groupId")
	if groupId == "" {
		api.RespErr(c, 1, "groupId is required")
		return
	}

	control, err := a.GetGroupJoinControlList(groupId)
	if err != nil {
		api.RespErr(c, 1, "failed to get join control list")
		return
	}

	api.RespSuccess(c, control)
}

// --- Chat Handlers ---

func (a *Aggregator) handleGroupChatList(c *gin.Context) {
	a.handleGroupChatListV2(c)
}

func (a *Aggregator) handleGroupChatListV2(c *gin.Context) {
	groupId := c.Query("groupId")
	if groupId == "" {
		api.RespErr(c, 1, "groupId is required")
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	timestamp, _ := strconv.ParseInt(c.DefaultQuery("timestamp", "0"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	result, err := a.GetChatListV2BeforeTimestamp(groupId, cursor, size, timestamp)
	if err != nil {
		api.RespErr(c, 1, "failed to get chat list")
		return
	}

	api.RespSuccess(c, result)
}

func (a *Aggregator) handleGroupChatListV3(c *gin.Context) {
	a.handleGroupChatListV2(c)
}

func (a *Aggregator) handleGroupChatListByIndex(c *gin.Context) {
	groupId := c.Query("groupId")
	if groupId == "" {
		api.RespErr(c, 1, "groupId is required")
		return
	}

	startIndex, _ := strconv.ParseInt(c.DefaultQuery("startIndex", "0"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	result, err := a.GetChatListByIndexCompat(groupId, startIndex, size)
	if err != nil {
		api.RespErr(c, 1, "failed to get chat list")
		return
	}

	api.RespSuccess(c, result)
}

func (a *Aggregator) handleChannelChatListV3(c *gin.Context) {
	groupId := c.Query("groupId")
	channelId := c.Query("channelId")
	if channelId == "" {
		api.RespErr(c, 1, "channelId is required")
		return
	}

	cursor := c.DefaultQuery("cursor", "")
	timestamp, _ := strconv.ParseInt(c.DefaultQuery("timestamp", "0"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	result, err := a.GetChannelChatListV3(groupId, channelId, cursor, size, timestamp)
	if err != nil {
		api.RespErr(c, 1, "failed to get channel chat list")
		return
	}

	api.RespSuccess(c, result)
}

func (a *Aggregator) handleChannelChatListByIndex(c *gin.Context) {
	groupId := c.Query("groupId")
	channelId := c.Query("channelId")
	if channelId == "" {
		api.RespErr(c, 1, "channelId is required")
		return
	}

	startIndex, _ := strconv.ParseInt(c.DefaultQuery("startIndex", "0"), 10, 64)
	size, _ := strconv.ParseInt(c.DefaultQuery("size", "20"), 10, 64)
	if size < 1 || size > 100 {
		size = 20
	}

	result, err := a.GetChannelChatListByIndex(groupId, channelId, startIndex, size)
	if err != nil {
		api.RespErr(c, 1, "failed to get channel chat list by index")
		return
	}

	api.RespSuccess(c, result)
}

func (a *Aggregator) handleUserLatestChatInfoList(c *gin.Context) {
	metaId := c.Query("metaId")
	if metaId == "" {
		api.RespErr(c, 1, "metaId is required")
		return
	}

	infos, err := a.GetUserLatestChatInfoList(metaId)
	if err != nil {
		api.RespErr(c, 1, "failed to get latest chat info")
		return
	}

	if infos == nil {
		infos = []*UserLatestChatInfo{}
	}

	api.RespSuccess(c, gin.H{
		"total": len(infos),
		"list":  infos,
	})
}

// --- Search Handler ---

func (a *Aggregator) handleSearchUsers(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		api.RespErr(c, 1, "query is required")
		return
	}

	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	if size < 1 || size > 100 {
		size = 50
	}

	users, err := a.SearchUsers(query, size)
	if err != nil {
		api.RespErr(c, 1, "failed to search users")
		return
	}

	if users == nil {
		users = []map[string]interface{}{}
	}

	api.RespSuccess(c, gin.H{
		"total": len(users),
		"list":  users,
	})
}

func (a *Aggregator) handleSearchGroupsAndUsers(c *gin.Context) {
	query := c.Query("query")
	if query == "" {
		api.RespErr(c, 1, "query is required")
		return
	}

	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	if size < 1 || size > 100 {
		size = 20
	}

	results, err := a.SearchGroupsAndUsers(query, size)
	if err != nil {
		api.RespErr(c, 1, "failed to search groups and users")
		return
	}
	if results == nil {
		results = []map[string]interface{}{}
	}

	api.RespSuccess(c, gin.H{
		"total": len(results),
		"list":  results,
	})
}

func (a *Aggregator) handleGroupChannelList(c *gin.Context) {
	groupId := c.Query("groupId")
	if groupId == "" {
		api.RespErr(c, 1, "groupId is required")
		return
	}

	channels, err := a.GetGroupChannelList(groupId)
	if err != nil {
		api.RespErr(c, 1, "failed to get group channel list")
		return
	}
	if channels == nil {
		channels = []*GroupChannel{}
	}

	api.RespSuccess(c, gin.H{
		"total": len(channels),
		"list":  channels,
	})
}

func (a *Aggregator) handleGroupMetaIdJoinList(c *gin.Context) {
	metaId := c.Query("metaId")
	if metaId == "" {
		metaId = c.Query("globalMetaId")
	}
	items, err := a.GetGroupMetaIdJoinList(c.Query("groupId"), metaId)
	if err != nil {
		api.RespErr(c, 1, "failed to get group metaid join list")
		return
	}
	if items == nil {
		items = []map[string]interface{}{}
	}

	api.RespSuccess(c, gin.H{
		"metaId": metaId,
		"items":  items,
	})
}

// --- Stub Handler ---

func (a *Aggregator) handleStub(c *gin.Context) {
	api.RespSuccess(c, gin.H{})
}
