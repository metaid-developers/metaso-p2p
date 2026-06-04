package socket

import (
	"net/url"
	"strings"
)

// ProfileSnapshot is the display profile attached to app presence rows.
type ProfileSnapshot struct {
	GlobalMetaId  string `json:"globalMetaId,omitempty"`
	MetaId        string `json:"metaid,omitempty"`
	Address       string `json:"address,omitempty"`
	Name          string `json:"name,omitempty"`
	Avatar        string `json:"avatar,omitempty"`
	AvatarId      string `json:"avatarId,omitempty"`
	AvatarUrl     string `json:"avatarUrl,omitempty"`
	ChatPublicKey string `json:"chatPublicKey,omitempty"`
	Bio           any    `json:"bio,omitempty"`
}

// ProfileLookup is the narrow profile contract /socket/online/list needs.
type ProfileLookup interface {
	LookupByMetaId(metaid string) (*ProfileSnapshot, error)
	LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error)
	LookupByAddress(address string) (*ProfileSnapshot, error)
}

func (s *Server) hydrateOnlineEntries(items []OnlineEntry) []OnlineEntry {
	if s == nil || s.profileLookup == nil {
		return items
	}
	for i := range items {
		if items[i].Type != string(ConnTypeApp) {
			continue
		}
		profile := s.lookupPresenceProfile(items[i].MetaId)
		if profile == nil {
			continue
		}
		profile.AvatarUrl = s.resolveProfileAvatarURL(profile.Avatar, profile.AvatarId)
		items[i].UserInfo = profile
	}
	return items
}

func (s *Server) lookupPresenceProfile(id string) *ProfileSnapshot {
	id = strings.TrimSpace(id)
	if id == "" || s == nil || s.profileLookup == nil {
		return nil
	}

	lookups := []struct {
		key string
		fn  func(string) (*ProfileSnapshot, error)
	}{
		{key: "metaid:" + id, fn: s.profileLookup.LookupByMetaId},
		{key: "globalmetaid:" + id, fn: s.profileLookup.LookupByGlobalMetaId},
		{key: "address:" + id, fn: s.profileLookup.LookupByAddress},
	}
	if strings.HasPrefix(strings.ToLower(id), "idq") {
		lookups[0], lookups[1] = lookups[1], lookups[0]
	}

	seen := make(map[string]bool, len(lookups))
	for _, lookup := range lookups {
		if seen[lookup.key] {
			continue
		}
		seen[lookup.key] = true
		profile, err := lookup.fn(id)
		if err == nil && profile != nil {
			return profile
		}
	}
	return nil
}

func (s *Server) resolveProfileAvatarURL(avatar, avatarID string) string {
	avatar = strings.TrimSpace(avatar)
	avatarID = firstNonEmptyString(avatarID, avatarIDFromReference(avatar))
	if avatarID != "" && s != nil && s.profileAssetBaseURL != "" {
		return s.profileAssetBaseURL + "/" + strings.TrimLeft(avatarID, "/")
	}
	if isHTTPURL(avatar) {
		return avatar
	}
	return ""
}

func avatarIDFromReference(avatar string) string {
	avatar = strings.TrimSpace(avatar)
	if avatar == "" || avatar == "/content/" {
		return ""
	}
	lower := strings.ToLower(avatar)
	switch {
	case strings.HasPrefix(avatar, "/content/"):
		return strings.Trim(strings.TrimPrefix(avatar, "/content/"), "/")
	case strings.HasPrefix(lower, "metafile://"):
		return strings.Trim(strings.TrimPrefix(avatar, "metafile://"), "/")
	case strings.HasPrefix(lower, "metafile:"):
		return strings.Trim(strings.TrimPrefix(avatar, "metafile:"), "/")
	case isHTTPURL(avatar):
		if parsed, err := url.Parse(avatar); err == nil {
			parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
			if len(parts) > 0 {
				return strings.TrimSpace(parts[len(parts)-1])
			}
		}
	}
	return ""
}

func isHTTPURL(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
