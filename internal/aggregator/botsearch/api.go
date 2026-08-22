package botsearch

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/groupchat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// searchRequest is the POST /api/bots/search body. onlineOnly and
// hasChatPubkey are pointers because both default to true when absent.
type searchRequest struct {
	Query                string   `json:"query"`
	RoleHint             string   `json:"roleHint"`
	Skills               []string `json:"skills"`
	Language             string   `json:"language"`
	OnlineOnly           *bool    `json:"onlineOnly"`
	HasChatPubkey        *bool    `json:"hasChatPubkey"`
	ExcludeGlobalMetaIds []string `json:"excludeGlobalMetaIds"`
	Limit                int      `json:"limit"`
	Cursor               string   `json:"cursor"`
}

// groupTask is one recentGroupTasks row. kind is always "group" for now:
// simplegroupchat content is AES-encrypted, so the `[GROUP TASK]` kickoff tag
// cannot be detected and casual groups cannot yet be told apart from Group
// Tasks. messageCount stays 0 — chat sender counts are not indexed.
type groupTask struct {
	GroupId      string `json:"groupId"`
	Title        string `json:"title"`
	Goal         string `json:"goal"`
	JoinedAs     string `json:"joinedAs"`
	JoinedAt     int64  `json:"joinedAt"` // unix seconds
	JoinPinId    string `json:"joinPinId"`
	StillMember  bool   `json:"stillMember"`
	MessageCount int64  `json:"messageCount"`
	Kind         string `json:"kind"`
}

// candidate is one /api/bots/search result row.
type candidate struct {
	GlobalMetaId       string        `json:"globalMetaId"`
	MetaId             string        `json:"metaId"`
	Name               string        `json:"name"`
	AvatarId           string        `json:"avatarId,omitempty"`
	Bio                string        `json:"bio"`
	Role               string        `json:"role"`
	Goal               string        `json:"goal"`
	ChatSkills         []string      `json:"chatSkills"`
	PublishedSkills    []string      `json:"publishedSkills"`
	ChainName          string        `json:"chainName"`
	HasChatPubkey      bool          `json:"hasChatPubkey"`
	HasHomepage        bool          `json:"hasHomepage"`
	Homepage           string        `json:"homepage"`
	IsOnline           bool          `json:"isOnline"`
	LastSeenAgoSeconds *int64        `json:"lastSeenAgoSeconds"`
	GroupTaskCount     int           `json:"groupTaskCount"`
	RecentGroupTasks   []groupTask   `json:"recentGroupTasks"`
	Score              float64       `json:"score"`
	MatchReasons       []matchReason `json:"matchReasons"`
}

type searchResponse struct {
	Candidates []candidate `json:"candidates"`
	NextCursor *string     `json:"nextCursor"`
	QueriedAt  int64       `json:"queriedAt"` // unix milliseconds
}

func (a *Aggregator) handleSearch(c *gin.Context) {
	var req searchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespErr(c, codeInvalidQuery, "invalid request body")
		return
	}

	query := strings.TrimSpace(req.Query)
	roleHint := strings.ToLower(strings.TrimSpace(req.RoleHint))
	if roleHint != "" {
		if _, ok := roleHintSynonyms[roleHint]; !ok {
			api.RespErr(c, codeInvalidQuery, "invalid roleHint")
			return
		}
	}
	skills := make([]string, 0, len(req.Skills))
	for _, skill := range req.Skills {
		if trimmed := strings.TrimSpace(skill); trimmed != "" {
			skills = append(skills, trimmed)
		}
	}
	if query == "" && len(skills) == 0 && roleHint == "" {
		api.RespErr(c, codeInvalidQuery, "query, skills and roleHint are all empty")
		return
	}
	limit := req.Limit
	if limit == 0 {
		limit = defaultSearchLimit
	}
	if limit < 1 || limit > maxSearchLimit {
		api.RespErr(c, codeInvalidQuery, "limit out of range [1,50]")
		return
	}
	offset, err := decodeSearchCursor(strings.TrimSpace(req.Cursor))
	if err != nil {
		api.RespErr(c, codeInvalidQuery, "invalid cursor")
		return
	}

	onlineOnly := req.OnlineOnly == nil || *req.OnlineOnly
	requireChatPubkey := req.HasChatPubkey == nil || *req.HasChatPubkey

	// Presence gate: never answer an onlineOnly query from a node that cannot
	// see presence — that would silently return stale "maybe online" rows.
	if onlineOnly && !a.presenceAvailable() {
		c.JSON(http.StatusOK, gin.H{
			"code":    codePresenceUnavailable,
			"message": "presence_unavailable",
			"data": searchResponse{
				Candidates: []candidate{},
				NextCursor: nil,
				QueriedAt:  a.now(),
			},
		})
		return
	}

	if a.profiles == nil {
		api.RespErr(c, codeInternalFailure, "profile source unavailable")
		return
	}

	presenceItems := a.presenceSnapshot()

	excluded := make(map[string]struct{}, len(req.ExcludeGlobalMetaIds))
	for _, id := range req.ExcludeGlobalMetaIds {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			excluded[strings.ToLower(trimmed)] = struct{}{}
		}
	}

	tokens := tokenize(query, skills)

	type scoredCandidate struct {
		profile    userinfo.BotSearchProfile
		history    []groupchat.GroupHistoryItem
		score      float64
		reasons    []matchReason
		online     bool
		lastSeenMs int64
	}
	matches := make([]scoredCandidate, 0)

	for _, profile := range a.profiles.BotSearchProfiles() {
		if requireChatPubkey && !profile.HasChatPubkey {
			continue
		}
		if profile.GlobalMetaID != "" {
			if _, skip := excluded[strings.ToLower(profile.GlobalMetaID)]; skip {
				continue
			}
		}

		online := false
		var lastSeenMs int64
		if entry, ok := findPresence(presenceItems, identityCandidates(profile.GlobalMetaID, profile.MetaID, profile.Address)); ok {
			online = true
			lastSeenMs = entry.LastSeenAt
			if lastSeenMs == 0 {
				lastSeenMs = entry.ConnectedAt
			}
		}
		if onlineOnly && !online {
			continue
		}

		history := []groupchat.GroupHistoryItem{}
		if a.history != nil {
			history, err = a.history.GroupHistoryForIdentity(profile.MetaID, profile.GlobalMetaID, profile.Address)
			if err != nil {
				api.RespErr(c, codeInternalFailure, "group history lookup failed")
				return
			}
		}

		result := scoreCandidate(profile, history, tokens, roleHint, query)
		if result.score <= 0 {
			continue
		}
		matches = append(matches, scoredCandidate{
			profile:    profile,
			history:    history,
			score:      result.score,
			reasons:    result.reasons,
			online:     online,
			lastSeenMs: lastSeenMs,
		})
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		nameI, nameJ := strings.ToLower(matches[i].profile.Name), strings.ToLower(matches[j].profile.Name)
		if nameI != nameJ {
			return nameI < nameJ
		}
		return matches[i].profile.GlobalMetaID < matches[j].profile.GlobalMetaID
	})

	if offset > len(matches) {
		offset = len(matches)
	}
	page := matches[offset:]
	hasMore := len(page) > limit
	if hasMore {
		page = page[:limit]
	}

	// Enrich only the returned page: presence timestamps, published skills,
	// and the recentGroupTasks fold-in.
	nowMs := a.now()
	candidates := make([]candidate, 0, len(page))
	for _, match := range page {
		profile := match.profile

		var lastSeenAgo *int64
		if match.online && match.lastSeenMs > 0 {
			ago := (nowMs - match.lastSeenMs) / 1000
			if ago < 0 {
				ago = 0
			}
			lastSeenAgo = &ago
		}

		publishedSkills := []string{}
		if a.skills != nil && profile.GlobalMetaID != "" {
			if names, err := a.skills.PublishedServiceNames(profile.GlobalMetaID); err == nil && names != nil {
				publishedSkills = names
			}
		}

		recent := make([]groupTask, 0, min(recentGroupTasksCap, len(match.history)))
		for _, item := range match.history[:min(recentGroupTasksCap, len(match.history))] {
			recent = append(recent, groupTask{
				GroupId:     item.GroupId,
				Title:       item.Title,
				Goal:        item.Goal,
				JoinedAs:    item.JoinedAs,
				JoinedAt:    unixSeconds(item.JoinedAt),
				JoinPinId:   item.JoinPinId,
				StillMember: item.StillMember,
				Kind:        "group",
			})
		}

		chatSkills := profile.ChatSkills
		if chatSkills == nil {
			chatSkills = []string{}
		}

		candidates = append(candidates, candidate{
			GlobalMetaId:       profile.GlobalMetaID,
			MetaId:             profile.MetaID,
			Name:               profile.Name,
			AvatarId:           profile.AvatarId,
			Bio:                profile.Bio,
			Role:               profile.Role,
			Goal:               profile.Goal,
			ChatSkills:         chatSkills,
			PublishedSkills:    publishedSkills,
			ChainName:          profile.ChainName,
			HasChatPubkey:      profile.HasChatPubkey,
			HasHomepage:        profile.Homepage != "",
			Homepage:           profile.Homepage,
			IsOnline:           match.online,
			LastSeenAgoSeconds: lastSeenAgo,
			GroupTaskCount:     len(match.history),
			RecentGroupTasks:   recent,
			Score:              match.score,
			MatchReasons:       match.reasons,
		})
	}

	var nextCursor *string
	if hasMore {
		encoded := encodeSearchCursor(offset + limit)
		nextCursor = &encoded
	}

	api.RespSuccess(c, searchResponse{
		Candidates: candidates,
		NextCursor: nextCursor,
		QueriedAt:  nowMs,
	})
}

// unixSeconds normalises a pin timestamp (unix milliseconds at write time)
// to the seconds granularity the contract uses for joinedAt.
func unixSeconds(timestampMs int64) int64 {
	if timestampMs > 1e12 {
		return timestampMs / 1000
	}
	return timestampMs
}
