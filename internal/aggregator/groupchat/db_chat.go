package groupchat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sort"

	"github.com/cockroachdb/pebble"
)

// ChatMessage represents a group chat message, matching IDCHAT_API_CONTRACT.md exactly.
type ChatMessage struct {
	TxId              string      `json:"txId"`
	PinId             string      `json:"pinId"`
	GroupId           string      `json:"groupId"`
	ChannelId         string      `json:"channelId,omitempty"`
	MetaId            string      `json:"metaId"`
	GlobalMetaId      string      `json:"globalMetaId"`
	Address           string      `json:"address"`
	UserInfo          interface{} `json:"userInfo,omitempty"`
	NickName          string      `json:"nickName,omitempty"`
	Protocol          string      `json:"protocol"`
	Content           string      `json:"content"`
	ContentType       string      `json:"contentType"`
	Encryption        string      `json:"encryption"`
	ChatType          string      `json:"chatType"`
	ReplyPin          string      `json:"replyPin,omitempty"`
	ReplyInfo         interface{} `json:"replyInfo,omitempty"`
	ReplyMetaId       string      `json:"replyMetaId,omitempty"`
	ReplyGlobalMetaId string      `json:"replyGlobalMetaId,omitempty"`
	Mention           []string    `json:"mention,omitempty"`
	Timestamp         int64       `json:"timestamp"`
	Chain             string      `json:"chain"`
	BlockHeight       int64       `json:"blockHeight"`
	Index             int64       `json:"index"`
}

// ChatListResult is the response format for chat list queries.
type ChatListResult struct {
	Total         int64          `json:"total"`
	NextCursor    string         `json:"nextCursor"`
	NextTimestamp int64          `json:"nextTimestamp"`
	List          []*ChatMessage `json:"list"`
}

// ChatListByIndexResult mirrors idchat's old indexed list envelope.
type ChatListByIndexResult struct {
	Total         int64          `json:"total"`
	LastIndex     int64          `json:"lastIndex"`
	NextTimestamp int64          `json:"nextTimestamp,omitempty"`
	List          []*ChatMessage `json:"list"`
}

// UserLatestChatInfo mirrors idchat's old unified session item shape.
type UserLatestChatInfo struct {
	Type               string      `json:"type"`
	GlobalMetaId       string      `json:"globalMetaId,omitempty"`
	GroupId            string      `json:"groupId,omitempty"`
	MetaId             string      `json:"metaId,omitempty"`
	Address            string      `json:"address,omitempty"`
	Timestamp          int64       `json:"timestamp"`
	ChatType           string      `json:"chatType,omitempty"`
	Content            string      `json:"content,omitempty"`
	CreateMetaId       string      `json:"createMetaId,omitempty"`
	CreateGlobalMetaId string      `json:"createGlobalMetaId,omitempty"`
	CreateAddress      string      `json:"createAddress,omitempty"`
	LastMessagePinId   string      `json:"lastMessagePinId,omitempty"`
	Version            string      `json:"version,omitempty"`
	BlockHeight        int64       `json:"blockHeight,omitempty"`
	Chain              string      `json:"chain,omitempty"`
	Index              int64       `json:"index,omitempty"`
	UserInfo           interface{} `json:"userInfo,omitempty"`
	RoomName           string      `json:"roomName,omitempty"`
	RoomJoinType       string      `json:"roomJoinType,omitempty"`
	RoomAvatarUrl      string      `json:"roomAvatarUrl,omitempty"`
	CreateUserInfo     interface{} `json:"createUserInfo,omitempty"`
	UserCount          int64       `json:"userCount,omitempty"`
	Path               string      `json:"path,omitempty"`
	LastMessage        interface{} `json:"lastMessage,omitempty"`
}

// GroupChannel is the old idchat channel-list item shape backed by indexed messages.
type GroupChannel struct {
	ChannelId                 string `json:"channelId"`
	GroupId                   string `json:"groupId"`
	ChannelName               string `json:"channelName"`
	ChannelIcon               string `json:"channelIcon,omitempty"`
	ChannelNote               string `json:"channelNote,omitempty"`
	ChannelType               string `json:"channelType,omitempty"`
	ChannelNewestPinId        string `json:"channelNewestPinId,omitempty"`
	ChannelNewestContent      string `json:"channelNewestContent,omitempty"`
	ChannelNewestTimestamp    int64  `json:"channelNewestTimestamp,omitempty"`
	ChannelNewestMetaId       string `json:"channelNewestMetaId,omitempty"`
	ChannelNewestGlobalMetaId string `json:"channelNewestGlobalMetaId,omitempty"`
	CreateUserMetaId          string `json:"createUserMetaId,omitempty"`
	CreateUserGlobalMetaId    string `json:"createUserGlobalMetaId,omitempty"`
	Timestamp                 int64  `json:"timestamp,omitempty"`
	Chain                     string `json:"chain,omitempty"`
	BlockHeight               int64  `json:"blockHeight,omitempty"`
	Index                     int64  `json:"index,omitempty"`
}

const (
	chatPrefix = "chat:"
)

func chatKey(groupId string, timestamp int64, txId string) []byte {
	// Format: chat:groupId:timestamp:txId
	// Pad timestamp to 19 digits for consistent key ordering (supports timestamps up to year 2286)
	return []byte(fmt.Sprintf("%s%s:%019d:%s", chatPrefix, groupId, timestamp, txId))
}

func chatKeyPrefix(groupId string) []byte {
	return []byte(chatPrefix + groupId + ":")
}

// SaveChatMessage persists a chat message to PebbleDB.
func (a *Aggregator) SaveChatMessage(msg *ChatMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, chatKey(msg.GroupId, msg.Timestamp, msg.TxId), raw)
}

// GetChatListV2 returns chat messages for a group with cursor-based pagination (descending by timestamp).
// The cursor is a base64-encoded index offset. Decode → skip entries → return nextCursor.
func (a *Aggregator) GetChatListV2(groupId string, cursorStr string, size int64) (*ChatListResult, error) {
	prefix := chatKeyPrefix(groupId)

	// Collect all messages in ascending order (oldest first by key)
	var allMessages []*ChatMessage
	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		var msg ChatMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		allMessages = append(allMessages, &msg)
		return nil
	})

	total := int64(len(allMessages))

	// Determine start position. Messages are in ascending order (oldest first).
	// We want descending (newest first), so we start from the end.
	// cursor encodes how many entries we've already returned.
	var startFromEnd int64 // offset from the end (0 = newest)
	if cursorStr != "" && cursorStr != "null" {
		decoded, cursorErr := base64.StdEncoding.DecodeString(cursorStr)
		if cursorErr == nil && len(decoded) >= 8 {
			startFromEnd = int64FromBytes(decoded[:8])
		}
	}

	// Take `size` entries starting from position (total - 1 - startFromEnd) going backwards.
	startIdx := total - 1 - startFromEnd
	if startIdx >= total {
		startIdx = total - 1
	}
	if startIdx < 0 {
		startIdx = -1 // will produce no results
	}

	var messages []*ChatMessage
	for i := startIdx; i >= 0 && int64(len(messages)) < size; i-- {
		messages = append(messages, allMessages[i])
	}
	if messages == nil {
		messages = []*ChatMessage{}
	}

	// Calculate next cursor
	nextCursor := ""
	newOffset := startFromEnd + int64(len(messages))
	if newOffset < total && int64(len(messages)) == size && len(messages) > 0 {
		nextCursor = base64.StdEncoding.EncodeToString(int64ToBytes(newOffset))
	}

	nextTimestamp := int64(0)
	if len(messages) > 0 {
		nextTimestamp = messages[len(messages)-1].Timestamp
	}

	return &ChatListResult{
		Total:         total,
		NextCursor:    nextCursor,
		NextTimestamp: nextTimestamp,
		List:          messages,
	}, nil
}

// int64FromBytes converts 8 bytes to int64.
func int64FromBytes(b []byte) int64 {
	var v int64
	for i := 0; i < len(b) && i < 8; i++ {
		v = (v << 8) | int64(b[i])
	}
	return v
}

// GetChatListByIndex returns chat messages by startIndex (descending by timestamp).
func (a *Aggregator) GetChatListByIndex(groupId string, startIndex int64, size int64) (*ChatListResult, error) {
	prefix := chatKeyPrefix(groupId)

	// Collect all messages first
	var allMessages []*ChatMessage
	var total int64

	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		total++
		var msg ChatMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		allMessages = append(allMessages, &msg)
		return nil
	})

	// Reverse for descending order (newest first)
	reversed := make([]*ChatMessage, len(allMessages))
	for i, msg := range allMessages {
		reversed[len(allMessages)-1-i] = msg
	}

	// Apply startIndex and size
	var messages []*ChatMessage
	for i := startIndex; i < total && int64(len(messages)) < size; i++ {
		messages = append(messages, reversed[i])
	}
	if messages == nil {
		messages = []*ChatMessage{}
	}

	nextTimestamp := int64(0)
	if len(messages) > 0 {
		nextTimestamp = messages[len(messages)-1].Timestamp
	}

	return &ChatListResult{
		Total:         total,
		NextTimestamp: nextTimestamp,
		List:          messages,
	}, nil
}

func (a *Aggregator) GetChatListByIndexCompat(groupId string, startIndex int64, size int64) (*ChatListByIndexResult, error) {
	result, err := a.GetChatListByIndex(groupId, startIndex, size)
	if err != nil {
		return nil, err
	}
	lastIndex := startIndex + int64(len(result.List))
	return &ChatListByIndexResult{
		Total:         result.Total,
		LastIndex:     lastIndex,
		NextTimestamp: result.NextTimestamp,
		List:          result.List,
	}, nil
}

func (a *Aggregator) GetChannelChatListV3(groupId, channelId, cursorStr string, size int64) (*ChatListResult, error) {
	allMessages := filterMessagesByChannel(a.collectGroupMessages(groupId), channelId)
	result := paginateMessagesDesc(allMessages, cursorStr, size)
	return result, nil
}

func (a *Aggregator) GetChannelChatListByIndex(groupId, channelId string, startIndex int64, size int64) (*ChatListByIndexResult, error) {
	allMessages := filterMessagesByChannel(a.collectGroupMessages(groupId), channelId)
	total := int64(len(allMessages))
	reversed := reverseMessages(allMessages)
	var messages []*ChatMessage
	for i := startIndex; i < total && int64(len(messages)) < size; i++ {
		messages = append(messages, reversed[i])
	}
	if messages == nil {
		messages = []*ChatMessage{}
	}

	nextTimestamp := int64(0)
	if len(messages) > 0 {
		nextTimestamp = messages[len(messages)-1].Timestamp
	}

	return &ChatListByIndexResult{
		Total:         total,
		LastIndex:     startIndex + int64(len(messages)),
		NextTimestamp: nextTimestamp,
		List:          messages,
	}, nil
}

func (a *Aggregator) GetGroupChannelList(groupId string) ([]*GroupChannel, error) {
	byChannel := make(map[string]*GroupChannel)
	for _, msg := range a.collectGroupMessages(groupId) {
		if msg.ChannelId == "" {
			continue
		}
		ch, ok := byChannel[msg.ChannelId]
		if !ok {
			ch = &GroupChannel{
				ChannelId:   msg.ChannelId,
				GroupId:     groupId,
				ChannelName: msg.ChannelId,
				ChannelType: "normal",
			}
			byChannel[msg.ChannelId] = ch
		}
		if msg.Timestamp >= ch.ChannelNewestTimestamp {
			ch.ChannelNewestPinId = msg.PinId
			ch.ChannelNewestContent = msg.Content
			ch.ChannelNewestTimestamp = msg.Timestamp
			ch.ChannelNewestMetaId = msg.MetaId
			ch.ChannelNewestGlobalMetaId = msg.GlobalMetaId
			ch.CreateUserMetaId = msg.MetaId
			ch.CreateUserGlobalMetaId = msg.GlobalMetaId
			ch.Timestamp = msg.Timestamp
			ch.Chain = msg.Chain
			ch.BlockHeight = msg.BlockHeight
			ch.Index = msg.Index
		}
	}

	channels := make([]*GroupChannel, 0, len(byChannel))
	for _, ch := range byChannel {
		channels = append(channels, ch)
	}
	sort.SliceStable(channels, func(i, j int) bool {
		return channels[i].ChannelNewestTimestamp > channels[j].ChannelNewestTimestamp
	})
	return channels, nil
}

func (a *Aggregator) GetGroupMetaIdJoinList(groupId, metaId string) ([]map[string]interface{}, error) {
	return []map[string]interface{}{}, nil
}

func (a *Aggregator) SearchGroupsAndUsers(query string, size int) ([]map[string]interface{}, error) {
	if size < 1 {
		size = 20
	}
	var results []map[string]interface{}

	a.store.ScanPrefix(namespace, groupKey(""), func(key, value []byte) error {
		if len(results) >= size {
			return nil
		}
		var group Group
		if err := json.Unmarshal(value, &group); err != nil {
			return nil
		}
		if contains(group.GroupId, query) || contains(group.GroupName, query) {
			results = append(results, map[string]interface{}{
				"type":          "group",
				"groupId":       group.GroupId,
				"groupName":     group.GroupName,
				"roomName":      group.GroupName,
				"avatar":        group.Avatar,
				"roomAvatarUrl": group.Avatar,
				"userCount":     group.MemberCount,
				"joinType":      group.JoinType,
				"roomJoinType":  group.JoinType,
			})
		}
		return nil
	})

	if len(results) < size {
		users, err := a.SearchUsers(query, size-len(results))
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			if len(results) >= size {
				break
			}
			user["type"] = "user"
			results = append(results, user)
		}
	}
	if results == nil {
		results = []map[string]interface{}{}
	}
	return results, nil
}

// GetUserLatestChatInfoList returns group and private sessions with their latest messages for a user.
func (a *Aggregator) GetUserLatestChatInfoList(metaId string) ([]*UserLatestChatInfo, error) {
	var result []*UserLatestChatInfo

	// Find all groups this user is a member of
	prefix := []byte(groupMemberPrefix)
	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		keyStr := string(key)
		parts := splitKey(keyStr[len(groupMemberPrefix):], ":")
		if len(parts) != 2 {
			return nil
		}
		if parts[1] != metaId {
			return nil
		}

		var m GroupMember
		if e := json.Unmarshal(value, &m); e != nil {
			return nil
		}
		if m.IsRemoved {
			return nil
		}

		groupId := parts[0]
		group, err := a.GetGroup(groupId)
		if err != nil || group == nil {
			return nil
		}

		// Get the latest message for this group
		var latest *ChatMessage
		chatResult, err := a.GetChatListByIndex(groupId, 0, 1)
		if err == nil && len(chatResult.List) > 0 {
			latest = chatResult.List[0]
		}

		info := &UserLatestChatInfo{
			Type:          "1",
			GroupId:       groupId,
			Timestamp:     group.CreatedAt,
			CreateMetaId:  group.CreatorMetaId,
			CreateAddress: group.Creator,
			RoomName:      group.GroupName,
			RoomJoinType:  group.JoinType,
			RoomAvatarUrl: group.Avatar,
			UserCount:     group.MemberCount,
			Chain:         group.Chain,
			BlockHeight:   group.BlockHeight,
			LastMessage:   latest,
		}
		if latest != nil {
			info.Timestamp = latest.Timestamp
			info.MetaId = latest.MetaId
			info.GlobalMetaId = latest.GlobalMetaId
			info.Address = latest.Address
			info.ChatType = latest.ChatType
			info.Content = latest.Content
			info.LastMessagePinId = latest.PinId
			info.Chain = latest.Chain
			info.BlockHeight = latest.BlockHeight
			info.Index = latest.Index
			info.Path = latest.Protocol
		}

		result = append(result, info)
		return nil
	})

	result = append(result, a.getPrivateLatestChatInfoList(metaId)...)
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Timestamp > result[j].Timestamp
	})
	if result == nil {
		result = []*UserLatestChatInfo{}
	}
	return result, nil
}

func (a *Aggregator) collectGroupMessages(groupId string) []*ChatMessage {
	prefix := chatKeyPrefix(groupId)
	var messages []*ChatMessage
	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		var msg ChatMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		messages = append(messages, &msg)
		return nil
	})
	if messages == nil {
		messages = []*ChatMessage{}
	}
	return messages
}

func filterMessagesByChannel(messages []*ChatMessage, channelId string) []*ChatMessage {
	var filtered []*ChatMessage
	for _, msg := range messages {
		if msg.ChannelId == channelId {
			filtered = append(filtered, msg)
		}
	}
	if filtered == nil {
		filtered = []*ChatMessage{}
	}
	return filtered
}

func paginateMessagesDesc(allMessages []*ChatMessage, cursorStr string, size int64) *ChatListResult {
	total := int64(len(allMessages))

	var startFromEnd int64
	if cursorStr != "" && cursorStr != "null" {
		decoded, cursorErr := base64.StdEncoding.DecodeString(cursorStr)
		if cursorErr == nil && len(decoded) >= 8 {
			startFromEnd = int64FromBytes(decoded[:8])
		}
	}

	startIdx := total - 1 - startFromEnd
	if startIdx >= total {
		startIdx = total - 1
	}
	if startIdx < 0 {
		startIdx = -1
	}

	var messages []*ChatMessage
	for i := startIdx; i >= 0 && int64(len(messages)) < size; i-- {
		messages = append(messages, allMessages[i])
	}
	if messages == nil {
		messages = []*ChatMessage{}
	}

	nextCursor := ""
	newOffset := startFromEnd + int64(len(messages))
	if newOffset < total && int64(len(messages)) == size && len(messages) > 0 {
		nextCursor = base64.StdEncoding.EncodeToString(int64ToBytes(newOffset))
	}

	nextTimestamp := int64(0)
	if len(messages) > 0 {
		nextTimestamp = messages[len(messages)-1].Timestamp
	}

	return &ChatListResult{
		Total:         total,
		NextCursor:    nextCursor,
		NextTimestamp: nextTimestamp,
		List:          messages,
	}
}

func reverseMessages(messages []*ChatMessage) []*ChatMessage {
	reversed := make([]*ChatMessage, len(messages))
	for i, msg := range messages {
		reversed[len(messages)-1-i] = msg
	}
	return reversed
}

type latestPrivateMessage struct {
	FromGlobalMetaId string      `json:"fromGlobalMetaId"`
	From             string      `json:"from"`
	FromAddress      string      `json:"fromAddress"`
	FromUserInfo     interface{} `json:"fromUserInfo,omitempty"`
	ToGlobalMetaId   string      `json:"toGlobalMetaId"`
	To               string      `json:"to"`
	ToAddress        string      `json:"toAddress"`
	ToUserInfo       interface{} `json:"toUserInfo,omitempty"`
	TxId             string      `json:"txId"`
	PinId            string      `json:"pinId"`
	Protocol         string      `json:"protocol"`
	Content          string      `json:"content"`
	ContentType      string      `json:"contentType"`
	Encryption       string      `json:"encryption"`
	Timestamp        int64       `json:"timestamp"`
	Chain            string      `json:"chain"`
	BlockHeight      int64       `json:"blockHeight"`
	Index            int64       `json:"index"`
}

func (a *Aggregator) getPrivateLatestChatInfoList(metaId string) []*UserLatestChatInfo {
	latestByPeer := make(map[string]*latestPrivateMessage)

	a.store.ScanPrefix("privatechat", []byte("pchat:"), func(key, value []byte) error {
		var msg latestPrivateMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		peer := ""
		if msg.From == metaId || msg.FromGlobalMetaId == metaId || msg.FromAddress == metaId {
			peer = msg.To
		} else if msg.To == metaId || msg.ToGlobalMetaId == metaId || msg.ToAddress == metaId {
			peer = msg.From
		}
		if peer == "" {
			return nil
		}
		if existing, ok := latestByPeer[peer]; !ok || msg.Timestamp > existing.Timestamp {
			copyMsg := msg
			latestByPeer[peer] = &copyMsg
		}
		return nil
	})

	var result []*UserLatestChatInfo
	for peer, msg := range latestByPeer {
		peerGlobalMetaId := msg.FromGlobalMetaId
		peerAddress := msg.FromAddress
		userInfo := msg.FromUserInfo
		if msg.From == metaId || msg.FromGlobalMetaId == metaId || msg.FromAddress == metaId {
			peerGlobalMetaId = msg.ToGlobalMetaId
			peerAddress = msg.ToAddress
			userInfo = msg.ToUserInfo
		}

		result = append(result, &UserLatestChatInfo{
			Type:             "2",
			GlobalMetaId:     peerGlobalMetaId,
			MetaId:           peer,
			Address:          peerAddress,
			Timestamp:        msg.Timestamp,
			ChatType:         "msg",
			Content:          msg.Content,
			LastMessagePinId: msg.PinId,
			BlockHeight:      msg.BlockHeight,
			Chain:            msg.Chain,
			Index:            msg.Index,
			UserInfo:         normalizePrivateUserInfo(userInfo),
			LastMessage:      msg,
			Path:             msg.Protocol,
		})
	}
	if result == nil {
		result = []*UserLatestChatInfo{}
	}
	return result
}

func normalizePrivateUserInfo(info interface{}) interface{} {
	m, ok := info.(map[string]interface{})
	if !ok {
		return info
	}
	if _, ok := m["chatPublicKey"]; !ok {
		if v, ok := m["chatpubkey"]; ok {
			m["chatPublicKey"] = v
		}
	}
	if _, ok := m["chatPublicKeyId"]; !ok {
		if v, ok := m["chatpubkeyId"]; ok {
			m["chatPublicKeyId"] = v
		}
	}
	return m
}

// SearchUsers searches for users by name or metaId across the userinfo namespace.
func (a *Aggregator) SearchUsers(query string, size int) ([]map[string]interface{}, error) {
	var results []map[string]interface{}
	queryLower := string(query)

	// Scan the userinfo namespace for profile info
	userNs := "userinfo" // the userinfo aggregator uses this namespace
	profilePrefix := []byte("profile:")

	err := a.store.ScanPrefix(userNs, profilePrefix, func(key, value []byte) error {
		if len(results) >= size {
			return nil
		}

		var profile map[string]interface{}
		if e := json.Unmarshal(value, &profile); e != nil {
			return nil
		}

		metaId, _ := profile["metaid"].(string)
		name, _ := profile["name"].(string)

		if metaId == "" && name == "" {
			return nil
		}

		if contains(metaId, queryLower) || contains(name, queryLower) {
			results = append(results, profile)
		}
		return nil
	})
	if err != nil {
		log.Printf("[groupchat] SearchUsers scan error: %v", err)
	}

	return results, nil
}

// contains checks if s contains substr (case-insensitive).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			c1 := s[i+j]
			c2 := substr[j]
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 32
			}
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// splitKey splits a string by delimiter and returns parts.
func splitKey(s, delimiter string) []string {
	var parts []string
	idx := 0
	for i := 0; i < len(s); i++ {
		if i+len(delimiter) <= len(s) && s[i:i+len(delimiter)] == delimiter {
			parts = append(parts, s[idx:i])
			idx = i + len(delimiter)
			i += len(delimiter) - 1
		}
	}
	parts = append(parts, s[idx:])
	return parts
}

// GetReplyInfo retrieves a chat message by pinId for reply resolution.
func (a *Aggregator) GetReplyInfo(pinId string) (*ChatMessage, error) {
	// Scan all chat messages (this is O(n) but acceptable for a reply lookup)
	prefix := []byte(chatPrefix)
	var found *ChatMessage

	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		if found != nil {
			return nil
		}
		var msg ChatMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		if msg.PinId == pinId {
			found = &msg
		}
		return nil
	})

	return found, nil
}

// GetChatMessage retrieves a single chat message by its Pebble key.
func (a *Aggregator) GetChatMessage(groupId string, timestamp int64, txId string) (*ChatMessage, error) {
	raw, err := a.store.Get(namespace, chatKey(groupId, timestamp, txId))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}

	var msg ChatMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
