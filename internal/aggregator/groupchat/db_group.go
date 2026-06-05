package groupchat

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"strings"

	"github.com/cockroachdb/pebble"
)

// Group represents a group chat room.
type Group struct {
	GroupId       string `json:"groupId"`
	GroupName     string `json:"groupName"`
	Avatar        string `json:"avatar,omitempty"`
	Creator       string `json:"creator"`
	CreatorMetaId string `json:"creatorMetaId"`
	MemberCount   int64  `json:"memberCount"`
	CommunityId   string `json:"communityId,omitempty"`
	JoinType      string `json:"joinType,omitempty"`
	CreatedAt     int64  `json:"createdAt"`
	Chain         string `json:"chain,omitempty"`
	BlockHeight   int64  `json:"blockHeight"`
}

// GroupMember represents a member of a group.
type GroupMember struct {
	MetaId       string `json:"metaId"`
	GlobalMetaId string `json:"globalMetaId"`
	Address      string `json:"address,omitempty"`
	UserName     string `json:"userName,omitempty"`
	Avatar       string `json:"avatar,omitempty"`
	IsCreator    bool   `json:"isCreator"`
	IsAdmin      bool   `json:"isAdmin"`
	IsBlocked    bool   `json:"isBlocked"`
	IsWhitelist  bool   `json:"isWhitelist"`
	IsRemoved    bool   `json:"isRemoved"`
	Timestamp    int64  `json:"timestamp"`
}

// GroupMemberRole stores role data per member-group pair.
type GroupMemberRole struct {
	MetaId      string `json:"metaId"`
	GroupId     string `json:"groupId"`
	IsCreator   bool   `json:"isCreator"`
	IsAdmin     bool   `json:"isAdmin"`
	IsBlocked   bool   `json:"isBlocked"`
	IsWhitelist bool   `json:"isWhitelist"`
	IsRemoved   bool   `json:"isRemoved"`
	Timestamp   int64  `json:"timestamp"`
	BlockHeight int64  `json:"blockHeight"`
}

const (
	groupPrefix       = "group:"
	groupMemberPrefix = "member:"
	groupRolePrefix   = "grouprole:"
)

func groupKey(groupId string) []byte {
	return []byte(groupPrefix + groupId)
}

func groupMemberKey(groupId, metaId string) []byte {
	return []byte(groupMemberPrefix + groupId + ":" + metaId)
}

func groupRoleKey(groupId, metaId string) []byte {
	return []byte(groupRolePrefix + groupId + ":" + metaId)
}

// SaveGroup persists a group to PebbleDB.
func (a *Aggregator) SaveGroup(group *Group) error {
	raw, err := json.Marshal(group)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, groupKey(group.GroupId), raw)
}

// GetGroup retrieves a group by ID.
func (a *Aggregator) GetGroup(groupId string) (*Group, error) {
	raw, err := a.store.Get(namespace, groupKey(groupId))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}

	var group Group
	if err := json.Unmarshal(raw, &group); err != nil {
		log.Printf("[groupchat] failed to unmarshal group %s: %v", groupId, err)
		return nil, err
	}
	return &group, nil
}

// SaveGroupMember persists a member with explicit groupId.
func (a *Aggregator) SaveGroupMember(groupId, metaId string, member *GroupMember) error {
	raw, err := json.Marshal(member)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, groupMemberKey(groupId, metaId), raw)
}

// GetGroupMember retrieves a group member.
func (a *Aggregator) GetGroupMember(groupId, metaId string) (*GroupMember, error) {
	raw, err := a.store.Get(namespace, groupMemberKey(groupId, metaId))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}

	var member GroupMember
	if err := json.Unmarshal(raw, &member); err != nil {
		return nil, err
	}
	return &member, nil
}

// GetGroupMemberList returns all members of a group with cursor-based pagination.
// Results include roles (isCreator, isAdmin, isBlocked, isWhitelist, isRemoved).
func (a *Aggregator) GetGroupMemberList(groupId string, cursorStr string, size int64) ([]*GroupMember, string, error) {
	prefix := []byte(groupMemberPrefix + groupId + ":")
	var members []*GroupMember

	// Decode cursor if present
	var seekKey []byte
	if cursorStr != "" && cursorStr != "null" {
		decoded, err := base64.StdEncoding.DecodeString(cursorStr)
		if err != nil {
			seekKey = prefix
		} else {
			seekKey = decoded
		}
	} else {
		seekKey = prefix
	}

	var lastKey []byte
	count := int64(0)

	err := a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		// Skip until we reach the cursor
		if seekKey != nil && string(key) <= string(seekKey) {
			return nil
		}

		if count >= size {
			return nil
		}

		var member GroupMember
		if e := json.Unmarshal(value, &member); e != nil {
			return nil
		}

		// Resolve roles from role storage
		role, _ := a.getGroupMemberRole(groupId, member.MetaId)
		if role != nil {
			member.IsCreator = role.IsCreator
			member.IsAdmin = role.IsAdmin
			member.IsBlocked = role.IsBlocked
			member.IsWhitelist = role.IsWhitelist
			member.IsRemoved = role.IsRemoved
		}

		members = append(members, &member)
		lastKey = make([]byte, len(key))
		copy(lastKey, key)
		count++
		return nil
	})

	if err != nil {
		return nil, "", err
	}

	nextCursor := ""
	if count == size && lastKey != nil {
		nextCursor = base64.StdEncoding.EncodeToString(lastKey)
	}

	return members, nextCursor, nil
}

// GetGroupList returns the list of groups a user is a member of, with cursor-based pagination.
func (a *Aggregator) GetGroupList(metaId string, cursorStr string, size int64) ([]*Group, string, int64, error) {
	// First, find all membership entries for this user across all groups.
	prefix := []byte(groupMemberPrefix)
	var memberEntries []struct {
		groupId string
		member  GroupMember
	}

	err := a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		keyStr := string(key)
		// Key format: member:groupId:metaId
		parts := strings.SplitN(keyStr[len(groupMemberPrefix):], ":", 2)
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
		if !m.IsRemoved {
			memberEntries = append(memberEntries, struct {
				groupId string
				member  GroupMember
			}{parts[0], m})
		}
		return nil
	})
	if err != nil {
		return nil, "", 0, err
	}

	total := int64(len(memberEntries))

	// Apply cursor pagination
	var cursorIdx int64
	if cursorStr != "" && cursorStr != "null" {
		decoded, err := base64.StdEncoding.DecodeString(cursorStr)
		if err == nil {
			cursorIdx = bytesToInt64(decoded)
		}
	}

	var groups []*Group
	for i := cursorIdx; i < total && int64(len(groups)) < size; i++ {
		group, err := a.GetGroup(memberEntries[i].groupId)
		if err != nil || group == nil {
			continue
		}
		groups = append(groups, group)
	}

	nextCursor := ""
	if cursorIdx+size < total {
		nextCursor = base64.StdEncoding.EncodeToString(int64ToBytes(cursorIdx + size))
	}

	return groups, nextCursor, total, nil
}

// int64ToBytes converts int64 to bytes for cursor encoding.
func int64ToBytes(v int64) []byte {
	b := make([]byte, 8)
	for i := 0; i < 8; i++ {
		b[i] = byte(v >> (56 - i*8))
	}
	return b
}

// bytesToInt64 converts bytes back to int64.
func bytesToInt64(b []byte) int64 {
	var v int64
	for i := 0; i < len(b) && i < 8; i++ {
		v = (v << 8) | int64(b[i])
	}
	return v
}

// SetGroupRole sets the role for a member in a group.
func (a *Aggregator) SetGroupRole(groupId, metaId string, role *GroupMemberRole) error {
	raw, err := json.Marshal(role)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, groupRoleKey(groupId, metaId), raw)
}

// getGroupMemberRole retrieves the role for a member in a group.
func (a *Aggregator) getGroupMemberRole(groupId, metaId string) (*GroupMemberRole, error) {
	raw, err := a.store.Get(namespace, groupRoleKey(groupId, metaId))
	if err != nil || raw == nil {
		return nil, nil
	}

	var role GroupMemberRole
	if err := json.Unmarshal(raw, &role); err != nil {
		return nil, err
	}
	return &role, nil
}

// GetGroupUserRole returns the role info for a member in a group.
func (a *Aggregator) GetGroupUserRole(groupId, metaId string) (*GroupMemberRole, error) {
	return a.getGroupMemberRole(groupId, metaId)
}

// IsUserInGroup checks if a user is a member of a group.
func (a *Aggregator) IsUserInGroup(groupId, metaId string) (bool, error) {
	member, err := a.GetGroupMember(groupId, metaId)
	if err != nil || member == nil {
		return false, err
	}
	return !member.IsRemoved, nil
}

// SearchGroupMembers searches for group members by name or metaId prefix.
func (a *Aggregator) SearchGroupMembers(groupId, query string, size int64) ([]*GroupMember, error) {
	prefix := []byte(groupMemberPrefix + groupId + ":")
	var members []*GroupMember
	queryLower := strings.ToLower(query)

	err := a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		if int64(len(members)) >= size {
			return nil
		}

		var member GroupMember
		if e := json.Unmarshal(value, &member); e != nil {
			return nil
		}

		if strings.Contains(strings.ToLower(member.MetaId), queryLower) ||
			strings.Contains(strings.ToLower(member.UserName), queryLower) {
			// Resolve roles
			role, _ := a.getGroupMemberRole(groupId, member.MetaId)
			if role != nil {
				member.IsCreator = role.IsCreator
				member.IsAdmin = role.IsAdmin
				member.IsBlocked = role.IsBlocked
				member.IsWhitelist = role.IsWhitelist
				member.IsRemoved = role.IsRemoved
			}
			members = append(members, &member)
		}
		return nil
	})

	return members, err
}

func (a *Aggregator) GroupMemberTargetIds(groupId string) []string {
	seen := make(map[string]bool)
	var ids []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		key := strings.ToLower(id)
		if seen[key] {
			return
		}
		seen[key] = true
		ids = append(ids, id)
	}

	prefix := []byte(groupMemberPrefix + groupId + ":")
	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		var member GroupMember
		if e := json.Unmarshal(value, &member); e != nil {
			return nil
		}
		if member.IsRemoved {
			return nil
		}
		add(member.MetaId)
		add(member.GlobalMetaId)
		add(member.Address)
		return nil
	})
	return ids
}

// GetGroupJoinControlList returns the join control lists for a group.
func (a *Aggregator) GetGroupJoinControlList(groupId string) (*GroupJoinControlList, error) {
	var blockedMetaIds []string
	var whitelistMetaIds []string
	var blockedGlobalMetaIds []string
	var whitelistGlobalMetaIds []string

	prefix := []byte(groupRolePrefix + groupId + ":")
	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		var role GroupMemberRole
		if e := json.Unmarshal(value, &role); e != nil {
			return nil
		}
		if role.IsBlocked {
			blockedMetaIds = append(blockedMetaIds, role.MetaId)
			if member, _ := a.GetGroupMember(groupId, role.MetaId); member != nil && member.GlobalMetaId != "" {
				blockedGlobalMetaIds = append(blockedGlobalMetaIds, member.GlobalMetaId)
			}
		}
		if role.IsWhitelist {
			whitelistMetaIds = append(whitelistMetaIds, role.MetaId)
			if member, _ := a.GetGroupMember(groupId, role.MetaId); member != nil && member.GlobalMetaId != "" {
				whitelistGlobalMetaIds = append(whitelistGlobalMetaIds, member.GlobalMetaId)
			}
		}
		return nil
	})
	if blockedMetaIds == nil {
		blockedMetaIds = []string{}
	}
	if whitelistMetaIds == nil {
		whitelistMetaIds = []string{}
	}
	if blockedGlobalMetaIds == nil {
		blockedGlobalMetaIds = []string{}
	}
	if whitelistGlobalMetaIds == nil {
		whitelistGlobalMetaIds = []string{}
	}

	return &GroupJoinControlList{
		GroupId:                    groupId,
		JoinBlockMetaIds:           blockedMetaIds,
		JoinBlockGlobalMetaIds:     blockedGlobalMetaIds,
		JoinWhitelistMetaIds:       whitelistMetaIds,
		JoinWhitelistGlobalMetaIds: whitelistGlobalMetaIds,
	}, nil
}

// GroupJoinControlList represents join control configuration.
type GroupJoinControlList struct {
	GroupId                    string   `json:"groupId"`
	JoinBlockMetaIds           []string `json:"joinBlockMetaIds"`
	JoinBlockGlobalMetaIds     []string `json:"joinBlockGlobalMetaIds"`
	JoinWhitelistMetaIds       []string `json:"joinWhitelistMetaIds"`
	JoinWhitelistGlobalMetaIds []string `json:"joinWhitelistGlobalMetaIds"`
}
