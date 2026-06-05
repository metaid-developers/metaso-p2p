package groupchat

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	groupJoinPrefix    = "groupjoin:"
	groupChannelPrefix = "channel:"
)

type GroupMetaIdJoinItem struct {
	JoinPinId      string `json:"joinPinId"`
	JoinType       string `json:"joinType"`
	JoinTimestamp  int64  `json:"joinTimestamp"`
	GroupState     int64  `json:"groupState"`
	Address        string `json:"address"`
	Referrer       string `json:"referrer"`
	K              string `json:"k"`
	BlockHeight    int64  `json:"blockHeight"`
	Chain          string `json:"chain"`
	ByGlobalMetaId string `json:"byGlobalMetaId"`
	ByMetaId       string `json:"byMetaId"`
	ByAddress      string `json:"byAddress"`
}

func groupJoinKey(identity, groupId string, timestamp int64, pinId string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:%019d:%s", groupJoinPrefix, identity, groupId, timestamp, pinId))
}

func groupJoinKeyPrefix(identity, groupId string) []byte {
	return []byte(fmt.Sprintf("%s%s:%s:", groupJoinPrefix, identity, groupId))
}

func groupChannelKey(groupId, channelId string) []byte {
	return []byte(groupChannelPrefix + groupId + ":" + channelId)
}

func groupChannelKeyPrefix(groupId string) []byte {
	return []byte(groupChannelPrefix + groupId + ":")
}

func (a *Aggregator) SaveGroupMetaIdJoinItem(groupId string, item *GroupMetaIdJoinItem) error {
	if groupId == "" || item == nil {
		return nil
	}
	raw, err := json.Marshal(item)
	if err != nil {
		return err
	}

	for _, identity := range groupMetaIdJoinItemAliases(item) {
		if err := a.store.Set(namespace, groupJoinKey(identity, groupId, item.JoinTimestamp, item.JoinPinId), raw); err != nil {
			return err
		}
	}
	return nil
}

func (a *Aggregator) collectGroupMetaIdJoinItems(groupId, metaId string) ([]*GroupMetaIdJoinItem, error) {
	aliases := a.groupIdentityAliases(groupId, metaId)
	seen := make(map[string]bool)
	var items []*GroupMetaIdJoinItem

	for _, identity := range aliases {
		prefix := groupJoinKeyPrefix(identity, groupId)
		if err := a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
			var item GroupMetaIdJoinItem
			if err := json.Unmarshal(value, &item); err != nil {
				return nil
			}
			seenKey := fmt.Sprintf("%s:%s:%d", item.JoinPinId, item.JoinType, item.JoinTimestamp)
			if seen[seenKey] {
				return nil
			}
			seen[seenKey] = true
			items = append(items, &item)
			return nil
		}); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].JoinTimestamp == items[j].JoinTimestamp {
			return items[i].JoinPinId < items[j].JoinPinId
		}
		return items[i].JoinTimestamp < items[j].JoinTimestamp
	})
	if items == nil {
		items = []*GroupMetaIdJoinItem{}
	}
	return items, nil
}

func groupMetaIdJoinItemAliases(item *GroupMetaIdJoinItem) []string {
	var aliases []string
	seen := make(map[string]bool)
	addIdentityAlias(&aliases, seen, item.ByMetaId)
	addIdentityAlias(&aliases, seen, item.ByGlobalMetaId)
	addIdentityAlias(&aliases, seen, item.ByAddress)
	addIdentityAlias(&aliases, seen, item.Address)
	return aliases
}

func (a *Aggregator) groupIdentityAliases(groupId, identity string) []string {
	var aliases []string
	seen := make(map[string]bool)
	addIdentityAlias(&aliases, seen, identity)

	if groupId == "" {
		return aliases
	}

	prefix := []byte(groupMemberPrefix + groupId + ":")
	_ = a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		var member GroupMember
		if err := json.Unmarshal(value, &member); err != nil {
			return nil
		}
		if !groupMemberMatchesIdentity(identity, &member) {
			return nil
		}
		addIdentityAlias(&aliases, seen, member.MetaId)
		addIdentityAlias(&aliases, seen, member.GlobalMetaId)
		addIdentityAlias(&aliases, seen, member.Address)
		return nil
	})
	return aliases
}

func groupMemberMatchesIdentity(identity string, member *GroupMember) bool {
	if member == nil {
		return false
	}
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return false
	}
	return identityEqual(identity, member.MetaId) ||
		identityEqual(identity, member.GlobalMetaId) ||
		identityEqual(identity, member.Address)
}

func identityEqual(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return a == b || strings.EqualFold(a, b)
}

func addIdentityAlias(aliases *[]string, seen map[string]bool, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	key := strings.ToLower(value)
	if seen[key] {
		return
	}
	seen[key] = true
	*aliases = append(*aliases, value)
}

func (a *Aggregator) SaveGroupChannel(channel *GroupChannel) error {
	if channel == nil || channel.GroupId == "" || channel.ChannelId == "" {
		return nil
	}
	raw, err := json.Marshal(channel)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, groupChannelKey(channel.GroupId, channel.ChannelId), raw)
}

func (a *Aggregator) getStoredGroupChannels(groupId string) ([]*GroupChannel, error) {
	var channels []*GroupChannel
	err := a.store.ScanPrefix(namespace, groupChannelKeyPrefix(groupId), func(key, value []byte) error {
		var channel GroupChannel
		if err := json.Unmarshal(value, &channel); err != nil {
			return nil
		}
		if channel.ChannelId != "" {
			channels = append(channels, &channel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if channels == nil {
		channels = []*GroupChannel{}
	}
	return channels, nil
}

func channelSortTimestamp(channel *GroupChannel) int64 {
	if channel == nil {
		return 0
	}
	if channel.ChannelNewestTimestamp > 0 {
		return channel.ChannelNewestTimestamp
	}
	return channel.Timestamp
}

func firstProtocolString(values ...interface{}) string {
	for _, value := range values {
		if result := protocolStringValue(value); result != "" {
			return result
		}
	}
	return ""
}

func protocolStringValue(value interface{}) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	case float32:
		if v == float32(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%v", v)
	case int:
		return fmt.Sprintf("%d", v)
	case int64:
		return fmt.Sprintf("%d", v)
	case uint64:
		return fmt.Sprintf("%d", v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func protocolInt64Value(value interface{}, defaultValue int64) int64 {
	switch v := value.(type) {
	case nil:
		return defaultValue
	case int:
		return int64(v)
	case int64:
		return v
	case uint64:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return n
		}
	case string:
		var n int64
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
	}
	return defaultValue
}
