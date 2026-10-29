package social

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

var errInvalidFriendCursor = errors.New("invalid friend cursor")

// FriendItem is one entry of a friends list (spec 2026-10-29 §4.1).
// FollowedAt/FollowPinId describe the subject→peer active edge (same
// semantics as the shipped following list); FriendSince is the Unix-second
// timestamp of the later of the two direction edges — the moment the mutual
// state came into existence.
type FriendItem struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name,omitempty"`
	NameId       string `json:"nameId,omitempty"`
	AvatarId     string `json:"avatarId,omitempty"`
	Bio          string `json:"bio,omitempty"`
	BioId        string `json:"bioId,omitempty"`
	FollowedAt   int64  `json:"followedAt"`
	FollowPinId  string `json:"followPinId"`
	FriendSince  int64  `json:"friendSince"`
}

type FriendListResult struct {
	List       []FriendItem `json:"list"`
	NextCursor string       `json:"nextCursor"`
	Size       int          `json:"size"`
}

// friendCursor is the opaque pagination anchor for the friends list: the exact
// (friendSince, peer) tuple of the last returned entry. A cursor whose entry
// no longer exists is stale and rejected (shared §4 pagination contract).
type friendCursor struct {
	FriendSince int64  `json:"fs"`
	Peer        string `json:"p"`
}

func encodeFriendCursor(cursor friendCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func decodeFriendCursor(raw string) (friendCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return friendCursor{}, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return friendCursor{}, err
	}
	var cursor friendCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return friendCursor{}, err
	}
	if cursor.Peer == "" {
		return friendCursor{}, errInvalidFriendCursor
	}
	return cursor, nil
}

// ListFriends returns the subject's friend list: peers whose follow edges are
// active in both directions (friend = mutual follow, spec §4.1). The subject
// input accepts both identifier forms (§2.3); blocked peers are excluded by
// the unified filter layer (§4.5).
func (a *Aggregator) ListFriends(params ListParams) (*FriendListResult, error) {
	view, err := normaliseListView(params.View)
	if err != nil {
		return nil, err
	}
	params.View = view

	subjectInput := strings.TrimSpace(params.GlobalMetaId)
	if subjectInput == "" {
		return nil, ErrInvalidParameter
	}
	subjectId, ok, err := a.normalizeIdentity(subjectInput)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}

	excluded, err := a.blockedPeerSet(subjectId)
	if err != nil {
		return nil, err
	}

	type friendEntry struct {
		peer   string
		record mutualRecord
	}
	entries := make([]friendEntry, 0)
	prefix := mutualIndexPrefix(subjectId)
	err = a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		peer := strings.TrimPrefix(string(key), string(prefix))
		if peer == "" || strings.Contains(peer, ":") {
			return nil
		}
		if _, blocked := excluded[peer]; blocked {
			return nil
		}
		var record mutualRecord
		if err := json.Unmarshal(value, &record); err != nil {
			return nil
		}
		entries = append(entries, friendEntry{peer: peer, record: record})
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Newest friendSince first; ties resolved deterministically by peer id
	// (shared §4 pagination contract).
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].record.FriendSince != entries[j].record.FriendSince {
			return entries[i].record.FriendSince > entries[j].record.FriendSince
		}
		return entries[i].peer > entries[j].peer
	})

	size := clampListSize(params.Size)
	start := 0
	if strings.TrimSpace(params.Cursor) != "" {
		cursor, cursorErr := decodeFriendCursor(params.Cursor)
		if cursorErr != nil {
			return nil, ErrInvalidParameter
		}
		start = -1
		for i, entry := range entries {
			if entry.record.FriendSince == cursor.FriendSince && entry.peer == cursor.Peer {
				start = i + 1
				break
			}
		}
		if start < 0 {
			return nil, ErrInvalidParameter
		}
	}

	end := start + size
	if end > len(entries) {
		end = len(entries)
	}
	list := make([]FriendItem, 0, end-start)
	for _, entry := range entries[start:end] {
		profile, profileErr := a.lookupListProfile(entry.peer)
		if profileErr != nil {
			return nil, profileErr
		}
		item := FriendItem{
			GlobalMetaId: profile.GlobalMetaId,
			Name:         profile.Name,
			NameId:       profile.NameId,
			AvatarId:     profile.AvatarId,
			FollowedAt:   entry.record.SubjectAt,
			FollowPinId:  entry.record.SubjectPinId,
			FriendSince:  entry.record.FriendSince,
		}
		if view == ViewProfile {
			item.Bio = profile.Bio
			item.BioId = profile.BioId
		}
		list = append(list, item)
	}

	nextCursor := ""
	if end < len(entries) && end > start {
		nextCursor, err = encodeFriendCursor(friendCursor{
			FriendSince: entries[end-1].record.FriendSince,
			Peer:        entries[end-1].peer,
		})
		if err != nil {
			return nil, err
		}
	}
	return &FriendListResult{List: list, NextCursor: nextCursor, Size: size}, nil
}
