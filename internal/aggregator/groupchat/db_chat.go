package groupchat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/cockroachdb/pebble"

	privatechatread "github.com/metaid-developers/metaso-p2p/internal/readmodel/privatechat"
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
	TxId                      string `json:"txId,omitempty"`
	PinId                     string `json:"pinId,omitempty"`
	ChannelName               string `json:"channelName"`
	ChannelIcon               string `json:"channelIcon,omitempty"`
	ChannelNote               string `json:"channelNote,omitempty"`
	ChannelType               int64  `json:"channelType"`
	ChannelNewestTxId         string `json:"channelNewestTxId,omitempty"`
	ChannelNewestPinId        string `json:"channelNewestPinId,omitempty"`
	ChannelNewestProtocol     string `json:"channelNewestProtocol,omitempty"`
	ChannelNewestContent      string `json:"channelNewestContent,omitempty"`
	ChannelNewestTimestamp    int64  `json:"channelNewestTimestamp,omitempty"`
	ChannelNewestMetaId       string `json:"channelNewestMetaId,omitempty"`
	ChannelNewestGlobalMetaId string `json:"channelNewestGlobalMetaId,omitempty"`
	CreateUserMetaId          string `json:"createUserMetaId,omitempty"`
	CreateUserGlobalMetaId    string `json:"createUserGlobalMetaId,omitempty"`
	CreateUserAddress         string `json:"createUserAddress,omitempty"`
	Timestamp                 int64  `json:"timestamp,omitempty"`
	Chain                     string `json:"chain,omitempty"`
	BlockHeight               int64  `json:"blockHeight,omitempty"`
	Index                     int64  `json:"index,omitempty"`
	Version                   string `json:"version,omitempty"`
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
	if msg == nil {
		return nil
	}
	if msg.Index < 0 {
		msg.Index = a.nextChatIndex(msg.GroupId, msg.ChannelId)
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, chatKey(msg.GroupId, msg.Timestamp, msg.TxId), raw)
}

// GetChatListV2 returns chat messages for a group with cursor-based pagination (descending by timestamp).
// The cursor is a base64-encoded index offset. Decode → skip entries → return nextCursor.
func (a *Aggregator) GetChatListV2(groupId string, cursorStr string, size int64) (*ChatListResult, error) {
	return a.GetChatListV2BeforeTimestamp(groupId, cursorStr, size, 0)
}

func (a *Aggregator) GetChatListV2BeforeTimestamp(groupId string, cursorStr string, size int64, beforeTimestamp int64) (*ChatListResult, error) {
	allMessages := filterMessagesBeforeTimestamp(a.collectGroupRootMessages(groupId), beforeTimestamp)
	return paginateMessagesDesc(allMessages, cursorStr, size), nil
}

// int64FromBytes converts 8 bytes to int64.
func int64FromBytes(b []byte) int64 {
	var v int64
	for i := 0; i < len(b) && i < 8; i++ {
		v = (v << 8) | int64(b[i])
	}
	return v
}

// GetChatListByIndex returns chat messages by their continuous message index.
func (a *Aggregator) GetChatListByIndex(groupId string, startIndex int64, size int64) (*ChatListResult, error) {
	messages, lastIndex := sliceMessagesByIndex(a.collectGroupRootMessages(groupId), startIndex, size)
	return &ChatListResult{
		Total:         int64(len(messages)),
		NextTimestamp: lastIndex,
		List:          messages,
	}, nil
}

func (a *Aggregator) GetChatListByIndexCompat(groupId string, startIndex int64, size int64) (*ChatListByIndexResult, error) {
	messages, lastIndex := sliceMessagesByIndex(a.collectGroupRootMessages(groupId), startIndex, size)
	return &ChatListByIndexResult{
		Total:         int64(len(messages)),
		LastIndex:     lastIndex,
		NextTimestamp: lastIndex,
		List:          messages,
	}, nil
}

func (a *Aggregator) GetChannelChatListV3(groupId, channelId, cursorStr string, size int64, beforeTimestamp int64) (*ChatListResult, error) {
	allMessages := filterMessagesBeforeTimestamp(a.collectChannelMessages(groupId, channelId), beforeTimestamp)
	result := paginateMessagesDesc(allMessages, cursorStr, size)
	return result, nil
}

func (a *Aggregator) GetChannelChatListByIndex(groupId, channelId string, startIndex int64, size int64) (*ChatListByIndexResult, error) {
	messages, lastIndex := sliceMessagesByIndex(a.collectChannelMessages(groupId, channelId), startIndex, size)
	return &ChatListByIndexResult{
		Total:         int64(len(messages)),
		LastIndex:     lastIndex,
		NextTimestamp: lastIndex,
		List:          messages,
	}, nil
}

func (a *Aggregator) GetGroupChannelList(groupId string) ([]*GroupChannel, error) {
	byChannel := make(map[string]*GroupChannel)
	storedChannels, err := a.getStoredGroupChannels(groupId)
	if err != nil {
		return nil, err
	}
	for _, stored := range storedChannels {
		ch := *stored
		byChannel[ch.ChannelId] = &ch
	}

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
				ChannelType: 0,
			}
			byChannel[msg.ChannelId] = ch
		}
		if msg.Timestamp >= ch.ChannelNewestTimestamp {
			ch.ChannelNewestTxId = msg.TxId
			ch.ChannelNewestPinId = msg.PinId
			ch.ChannelNewestProtocol = msg.Protocol
			ch.ChannelNewestContent = msg.Content
			ch.ChannelNewestTimestamp = msg.Timestamp
			ch.ChannelNewestMetaId = msg.MetaId
			ch.ChannelNewestGlobalMetaId = msg.GlobalMetaId
			if ch.CreateUserMetaId == "" {
				ch.CreateUserMetaId = msg.MetaId
			}
			if ch.CreateUserGlobalMetaId == "" {
				ch.CreateUserGlobalMetaId = msg.GlobalMetaId
			}
			if ch.CreateUserAddress == "" {
				ch.CreateUserAddress = msg.Address
			}
			if ch.Timestamp == 0 {
				ch.Timestamp = msg.Timestamp
			}
			if ch.Chain == "" {
				ch.Chain = msg.Chain
			}
			if ch.BlockHeight == 0 {
				ch.BlockHeight = msg.BlockHeight
			}
			ch.Index = msg.Index
		}
	}

	channels := make([]*GroupChannel, 0, len(byChannel))
	for _, ch := range byChannel {
		channels = append(channels, ch)
	}
	sort.SliceStable(channels, func(i, j int) bool {
		return channelSortTimestamp(channels[i]) > channelSortTimestamp(channels[j])
	})
	return channels, nil
}

func (a *Aggregator) GetGroupMetaIdJoinList(groupId, metaId string) ([]map[string]interface{}, error) {
	items, err := a.collectGroupMetaIdJoinItems(groupId, metaId)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]interface{}{
			"joinPinId":      item.JoinPinId,
			"joinType":       item.JoinType,
			"joinTimestamp":  item.JoinTimestamp,
			"groupState":     item.GroupState,
			"address":        item.Address,
			"referrer":       item.Referrer,
			"k":              item.K,
			"blockHeight":    item.BlockHeight,
			"chain":          item.Chain,
			"byGlobalMetaId": item.ByGlobalMetaId,
			"byMetaId":       item.ByMetaId,
			"byAddress":      item.ByAddress,
		})
	}
	return result, nil
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

		var m GroupMember
		if e := json.Unmarshal(value, &m); e != nil {
			return nil
		}
		if !groupMemberMatchesIdentity(metaId, &m) {
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

		latest := latestMessageByTimestamp(a.collectGroupRootMessages(groupId))

		info := &UserLatestChatInfo{
			Type:               "1",
			GroupId:            groupId,
			Timestamp:          group.CreatedAt,
			CreateMetaId:       group.CreatorMetaId,
			CreateGlobalMetaId: group.CreatorGlobalMetaId,
			CreateAddress:      group.Creator,
			RoomName:           group.GroupName,
			RoomJoinType:       group.JoinType,
			RoomAvatarUrl:      group.Avatar,
			UserCount:          group.MemberCount,
			Chain:              group.Chain,
			BlockHeight:        group.BlockHeight,
			LastMessage:        latest,
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

func (a *Aggregator) collectAllChatMessages() []*ChatMessage {
	var messages []*ChatMessage
	a.store.ScanPrefix(namespace, []byte(chatPrefix), func(key, value []byte) error {
		var msg ChatMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		messages = append(messages, &msg)
		return nil
	})
	sortChatMessagesAscending(messages)
	if messages == nil {
		messages = []*ChatMessage{}
	}
	return messages
}

func (a *Aggregator) collectGroupRootMessages(groupId string) []*ChatMessage {
	var messages []*ChatMessage
	for _, msg := range a.collectGroupMessages(groupId) {
		if msg.ChannelId == "" {
			messages = append(messages, msg)
		}
	}
	if messages == nil {
		messages = []*ChatMessage{}
	}
	return messages
}

func (a *Aggregator) collectChannelMessages(groupId, channelId string) []*ChatMessage {
	var source []*ChatMessage
	if groupId != "" {
		source = a.collectGroupMessages(groupId)
	} else {
		source = a.collectAllChatMessages()
	}
	return filterMessagesByChannel(source, channelId)
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

func filterMessagesBeforeTimestamp(messages []*ChatMessage, beforeTimestamp int64) []*ChatMessage {
	if beforeTimestamp <= 0 {
		return messages
	}
	filtered := make([]*ChatMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Timestamp < beforeTimestamp {
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

func sliceMessagesByIndex(allMessages []*ChatMessage, startIndex int64, size int64) ([]*ChatMessage, int64) {
	sort.SliceStable(allMessages, func(i, j int) bool {
		if allMessages[i].Index != allMessages[j].Index {
			return allMessages[i].Index < allMessages[j].Index
		}
		if allMessages[i].Timestamp != allMessages[j].Timestamp {
			return allMessages[i].Timestamp < allMessages[j].Timestamp
		}
		return allMessages[i].PinId < allMessages[j].PinId
	})

	var messages []*ChatMessage
	lastIndex := int64(0)
	for _, msg := range allMessages {
		if msg.Index < startIndex {
			continue
		}
		messages = append(messages, msg)
		if msg.Index > lastIndex {
			lastIndex = msg.Index
		}
		if int64(len(messages)) >= size {
			break
		}
	}
	if messages == nil {
		messages = []*ChatMessage{}
	}
	return messages, lastIndex
}

func (a *Aggregator) nextChatIndex(groupId, channelId string) int64 {
	var messages []*ChatMessage
	if channelId != "" {
		messages = a.collectChannelMessages(groupId, channelId)
	} else {
		messages = a.collectGroupRootMessages(groupId)
	}
	maxIndex := int64(-1)
	for _, msg := range messages {
		if msg.Index > maxIndex {
			maxIndex = msg.Index
		}
	}
	return maxIndex + 1
}

func latestMessageByTimestamp(messages []*ChatMessage) *ChatMessage {
	var latest *ChatMessage
	for _, msg := range messages {
		if latest == nil ||
			msg.Timestamp > latest.Timestamp ||
			(msg.Timestamp == latest.Timestamp && msg.PinId > latest.PinId) {
			latest = msg
		}
	}
	return latest
}

func sortChatMessagesAscending(messages []*ChatMessage) {
	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i].Timestamp != messages[j].Timestamp {
			return messages[i].Timestamp < messages[j].Timestamp
		}
		if messages[i].Index != messages[j].Index {
			return messages[i].Index < messages[j].Index
		}
		return messages[i].PinId < messages[j].PinId
	})
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
	if a.privateChatReadModelReady {
		result, err := a.getPrivateLatestChatInfoListByReadModel(metaId)
		if err != nil {
			log.Printf("private chat read model query failed for %q: %v", metaId, err)
			return []*UserLatestChatInfo{}
		}
		return result
	}
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
		userInfo = normalizePrivateUserInfo(userInfo)
		if !privateUserInfoHasChatPublicKey(userInfo) {
			if hydrated := a.lookupPrivateUserInfo(peer, peerGlobalMetaId, peerAddress); hydrated != nil {
				userInfo = hydrated
			}
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
			UserInfo:         userInfo,
			LastMessage:      msg,
			Path:             msg.Protocol,
		})
	}
	if result == nil {
		result = []*UserLatestChatInfo{}
	}
	return result
}

func (a *Aggregator) getPrivateLatestChatInfoListByReadModel(metaID string) ([]*UserLatestChatInfo, error) {
	records, err := privatechatread.ListHomes(a.store, "privatechat", metaID)
	if err != nil {
		return nil, err
	}
	result := make([]*UserLatestChatInfo, 0, len(records))
	for _, record := range records {
		var msg latestPrivateMessage
		if err := json.Unmarshal(record.Message, &msg); err != nil {
			return nil, err
		}
		msg.Index = record.Index
		userInfo := msg.FromUserInfo
		if msg.From == record.PeerMetaID || msg.FromGlobalMetaId == record.PeerGlobalMetaID || msg.FromAddress == record.PeerAddress {
			userInfo = msg.FromUserInfo
		} else {
			userInfo = msg.ToUserInfo
		}
		userInfo = normalizePrivateUserInfo(userInfo)
		if !privateUserInfoHasChatPublicKey(userInfo) {
			if hydrated := a.lookupPrivateUserInfo(record.PeerMetaID, record.PeerGlobalMetaID, record.PeerAddress); hydrated != nil {
				userInfo = hydrated
			}
		}
		result = append(result, &UserLatestChatInfo{
			Type:             "2",
			GlobalMetaId:     record.PeerGlobalMetaID,
			MetaId:           record.PeerMetaID,
			Address:          record.PeerAddress,
			Timestamp:        msg.Timestamp,
			ChatType:         "msg",
			Content:          msg.Content,
			LastMessagePinId: msg.PinId,
			BlockHeight:      msg.BlockHeight,
			Chain:            msg.Chain,
			Index:            record.Index,
			UserInfo:         userInfo,
			LastMessage:      &msg,
			Path:             msg.Protocol,
		})
	}
	return result, nil
}

func (a *Aggregator) lookupPrivateUserInfo(aliases ...string) interface{} {
	var wanted []string
	seenWanted := make(map[string]bool)
	for _, alias := range aliases {
		addIdentityAlias(&wanted, seenWanted, alias)
	}
	if len(wanted) == 0 {
		return nil
	}
	if a.profileLookup != nil {
		for _, alias := range wanted {
			profile, err := a.profileLookup.LookupLocalByIdentity(alias)
			if err == nil && profile != nil {
				return profile
			}
		}
		return nil
	}

	for _, alias := range wanted {
		raw, err := a.store.Get("userinfo", []byte("profile:"+alias))
		if err != nil || raw == nil {
			continue
		}
		var profile map[string]interface{}
		if e := json.Unmarshal(raw, &profile); e != nil {
			continue
		}
		return normalizePrivateUserInfo(profile)
	}
	return nil
}

func privateUserInfoHasChatPublicKey(info interface{}) bool {
	if profile, ok := info.(*ProfileSnapshot); ok {
		return profile != nil && strings.TrimSpace(profile.ChatPublicKey) != ""
	}
	m, ok := info.(map[string]interface{})
	if !ok {
		return false
	}
	if value, ok := m["chatPublicKey"].(string); ok && strings.TrimSpace(value) != "" {
		return true
	}
	if value, ok := m["chatpubkey"].(string); ok && strings.TrimSpace(value) != "" {
		return true
	}
	return false
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
