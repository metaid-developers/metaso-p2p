package groupchat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/cockroachdb/pebble"
)

// ChatMessage represents a group chat message, matching IDCHAT_API_CONTRACT.md exactly.
type ChatMessage struct {
	TxId             string      `json:"txId"`
	PinId            string      `json:"pinId"`
	GroupId          string      `json:"groupId"`
	ChannelId        string      `json:"channelId,omitempty"`
	MetaId           string      `json:"metaId"`
	GlobalMetaId     string      `json:"globalMetaId"`
	Address          string      `json:"address"`
	UserInfo         interface{} `json:"userInfo,omitempty"`
	NickName         string      `json:"nickName,omitempty"`
	Protocol         string      `json:"protocol"`
	Content          string      `json:"content"`
	ContentType      string      `json:"contentType"`
	Encryption       string      `json:"encryption"`
	ChatType         string      `json:"chatType"`
	ReplyPin         string      `json:"replyPin,omitempty"`
	ReplyInfo        interface{} `json:"replyInfo,omitempty"`
	ReplyMetaId      string      `json:"replyMetaId,omitempty"`
	ReplyGlobalMetaId string     `json:"replyGlobalMetaId,omitempty"`
	Mention          []string    `json:"mention,omitempty"`
	Timestamp        int64       `json:"timestamp"`
	Chain            string      `json:"chain"`
	BlockHeight      int64       `json:"blockHeight"`
	Index            int64       `json:"index"`
}

// ChatListResult is the response format for chat list queries.
type ChatListResult struct {
	Total         int64          `json:"total"`
	NextCursor    string         `json:"nextCursor"`
	NextTimestamp int64          `json:"nextTimestamp"`
	List          []*ChatMessage `json:"list"`
}

// UserLatestChatInfo represents a group with its latest chat info for a user.
type UserLatestChatInfo struct {
	GroupId    string `json:"groupId"`
	GroupName  string `json:"groupName"`
	Avatar     string `json:"avatar,omitempty"`
	MemberCount int64 `json:"memberCount"`
	LastMessage *ChatMessage `json:"lastMessage,omitempty"`
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

// GetUserLatestChatInfoList returns groups with their latest messages for a user.
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

		info := &UserLatestChatInfo{
			GroupId:     groupId,
			GroupName:   group.GroupName,
			Avatar:      group.Avatar,
			MemberCount: group.MemberCount,
		}

		// Get the latest message for this group
		chatResult, err := a.GetChatListByIndex(groupId, 0, 1)
		if err == nil && len(chatResult.List) > 0 {
			info.LastMessage = chatResult.List[0]
		}

		result = append(result, info)
		return nil
	})

	return result, nil
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
