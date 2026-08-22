package groupchat

import (
	"encoding/json"
	"sort"
)

// GroupHistoryItem describes one group an identity created or joined. It is
// the groupchat-side building block for the bot search API's
// recentGroupTasks fold-in; see docs/specs/2026-08-22-bot-search-api.md.
type GroupHistoryItem struct {
	GroupId     string `json:"groupId"`
	Title       string `json:"title"`          // groupName from the create pin
	Goal        string `json:"goal,omitempty"` // groupNote from the create pin
	JoinedAs    string `json:"joinedAs"`       // "chair" when the identity created the group, else "member"
	JoinPinId   string `json:"joinPinId"`      // join pin for members, create pin for the chair
	JoinedAt    int64  `json:"joinedAt"`       // unix milliseconds, same unit as pin timestamps
	StillMember bool   `json:"stillMember"`
}

// GroupHistoryForIdentity returns every group any of the given identity
// aliases (metaId / globalMetaId / address, deduplicated case-insensitively)
// created or joined, newest first. It is built by scanning the existing
// per-identity `groupjoin:<identity>:<groupId>:...` index written by the
// create/join/leave pin handlers, so it needs no new index and no backfill:
// groups whose pins predate this method are still covered, and an identity
// with no indexed pins simply yields an empty history.
func (a *Aggregator) GroupHistoryForIdentity(identities ...string) ([]GroupHistoryItem, error) {
	items := []GroupHistoryItem{}
	if a == nil || a.store == nil {
		return items, nil
	}

	var aliases []string
	seenAliases := make(map[string]bool)
	for _, identity := range identities {
		addIdentityAlias(&aliases, seenAliases, identity)
	}
	if len(aliases) == 0 {
		return items, nil
	}

	// Collect join items per group across all aliases. Multiple aliases of the
	// same identity index the same item, so per-group state collapses them.
	type groupState struct {
		create *GroupMetaIdJoinItem
		join   *GroupMetaIdJoinItem // earliest join item
		latest *GroupMetaIdJoinItem // newest item of any type (membership fallback)
	}
	byGroup := make(map[string]*groupState)

	for _, identity := range aliases {
		prefix := []byte(groupJoinPrefix + identity + ":")
		if err := a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
			groupId := parseGroupJoinKeyGroupId(string(key[len(prefix):]))
			if groupId == "" {
				return nil
			}
			var item GroupMetaIdJoinItem
			if err := json.Unmarshal(value, &item); err != nil {
				return nil
			}
			state := byGroup[groupId]
			if state == nil {
				state = &groupState{}
				byGroup[groupId] = state
			}
			switch item.JoinType {
			case "create":
				if state.create == nil || item.JoinTimestamp < state.create.JoinTimestamp {
					copy := item
					state.create = &copy
				}
			case "join":
				if state.join == nil || item.JoinTimestamp < state.join.JoinTimestamp {
					copy := item
					state.join = &copy
				}
			}
			if state.latest == nil || item.JoinTimestamp > state.latest.JoinTimestamp {
				copy := item
				state.latest = &copy
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}

	for groupId, state := range byGroup {
		chosen := state.join
		joinedAs := "member"
		if state.create != nil {
			chosen = state.create
			joinedAs = "chair"
		}
		if chosen == nil {
			// Only leave items indexed (e.g. the join pin predates indexing):
			// still report the group, anchored at the newest known item.
			chosen = state.latest
		}
		if chosen == nil {
			continue
		}

		stillMember := false
		if member := a.findGroupMemberByIdentity(groupId, aliases); member != nil {
			stillMember = !member.IsRemoved
		} else if state.latest != nil {
			// No member record (e.g. only a leave pin was indexed): fall back
			// to the newest join-item state.
			stillMember = state.latest.GroupState == 1
		}

		title, goal := "", ""
		if group, err := a.GetGroup(groupId); err == nil && group != nil {
			title = group.GroupName
			goal = group.GroupNote
		}

		items = append(items, GroupHistoryItem{
			GroupId:     groupId,
			Title:       title,
			Goal:        goal,
			JoinedAs:    joinedAs,
			JoinPinId:   chosen.JoinPinId,
			JoinedAt:    chosen.JoinTimestamp,
			StillMember: stillMember,
		})
	}

	sort.SliceStable(items, func(i, j int) bool {
		if items[i].JoinedAt != items[j].JoinedAt {
			return items[i].JoinedAt > items[j].JoinedAt
		}
		return items[i].GroupId < items[j].GroupId
	})
	return items, nil
}

// parseGroupJoinKeyGroupId extracts the groupId from the part of a
// groupjoin: key that follows the identity segment. The remainder has the
// shape `<groupId>:<19-digit zero-padded timestamp>:<pinId>` (see
// groupJoinKey) and both the groupId and the pinId may themselves contain
// ':' (pin ids are `txid:iN`), so the fixed-width timestamp is the only
// reliable anchor.
func parseGroupJoinKeyGroupId(rest string) string {
	for i := 0; i+21 < len(rest); i++ {
		if rest[i] != ':' || rest[i+20] != ':' {
			continue
		}
		digits := rest[i+1 : i+20]
		ok := true
		for j := 0; j < len(digits); j++ {
			if digits[j] < '0' || digits[j] > '9' {
				ok = false
				break
			}
		}
		if ok && i > 0 {
			return rest[:i]
		}
	}
	return ""
}

// findGroupMemberByIdentity scans a group's member records for one matching
// any of the identity aliases. Member keys are metaIds from pin time, which
// do not necessarily match the alias the caller holds, so a scan with the
// same identityEqual matching used elsewhere is the robust lookup.
func (a *Aggregator) findGroupMemberByIdentity(groupId string, aliases []string) *GroupMember {
	if groupId == "" || len(aliases) == 0 {
		return nil
	}
	var found *GroupMember
	_ = a.store.ScanPrefix(namespace, []byte(groupMemberPrefix+groupId+":"), func(_, value []byte) error {
		if found != nil {
			return nil
		}
		var member GroupMember
		if err := json.Unmarshal(value, &member); err != nil {
			return nil
		}
		for _, identity := range aliases {
			if groupMemberMatchesIdentity(identity, &member) {
				copy := member
				found = &copy
				break
			}
		}
		return nil
	})
	return found
}
