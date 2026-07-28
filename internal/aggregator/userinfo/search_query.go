package userinfo

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	defaultMetaIDListSize = 20
	maxMetaIDListSize     = 100
	// metaIDExactNameBoost lifts the user whose name exactly equals the whole
	// keyword above any tiered-score sum, so "view <name>'s bot page" intents
	// rank that person first. Far above any realistic token score (3/token).
	metaIDExactNameBoost = 1000
)

// MetaIDListParams mirrors the /api/metaid/list query parameters described in
// docs/specs/2026-07-28-metaid-search-api.md.
type MetaIDListParams struct {
	Keyword       string
	Skill         string
	ChainName     string
	HasChatPubkey bool
	HasHomepage   bool
	Since         int64
	Until         int64
	Size          int
	Cursor        string
}

// MetaIDItem is the /api/metaid/list item shape. persona/llm/homepage raw
// JSON and the chat pubkey stay out of the list (size and readability); the
// detail endpoint returns them.
type MetaIDItem struct {
	GlobalMetaID  string   `json:"globalMetaId"`
	MetaID        string   `json:"metaId"`
	Address       string   `json:"address"`
	ChainName     string   `json:"chainName,omitempty"`
	Name          string   `json:"name,omitempty"`
	AvatarId      string   `json:"avatarId,omitempty"`
	Bio           string   `json:"bio,omitempty"`
	ChatSkills    []string `json:"chatSkills,omitempty"`
	HasChatPubkey bool     `json:"hasChatPubkey"`
	HasHomepage   bool     `json:"hasHomepage"`
	CreatedAt     int64    `json:"createdAt,omitempty"`
	UpdatedAt     int64    `json:"updatedAt,omitempty"`
}

type MetaIDListResult struct {
	Items      []MetaIDItem `json:"items"`
	NextCursor string       `json:"nextCursor,omitempty"`
	HasMore    bool         `json:"hasMore"`
}

// ListMetaIDs runs the /api/metaid/list pipeline over the in-memory search
// documents: filter, score (when keyword is present), sort, and paginate with
// an opaque offset cursor — the same philosophy as the MetaApp list (collect
// matches, sort in memory), which holds for the profile counts that already
// fit in profilesByIdentity.
func (a *Aggregator) ListMetaIDs(params MetaIDListParams) (*MetaIDListResult, error) {
	params = normaliseMetaIDListParams(params)
	offset, err := decodeMetaIDCursor(params.Cursor)
	if err != nil {
		return nil, err
	}

	tokens := metaIDKeywordTokens(params.Keyword)
	exactName := metaIDExactNameKey(params.Keyword)

	matches := make([]scoredMetaIDDoc, 0)

	a.searchDocsMu.RLock()
	for _, doc := range a.searchDocs {
		if params.ChainName != "" && !strings.EqualFold(doc.chainName, params.ChainName) {
			continue
		}
		if params.HasChatPubkey && !doc.hasChatPubkey {
			continue
		}
		if params.HasHomepage && !doc.hasHomepage {
			continue
		}
		if params.Since > 0 && doc.updatedAt < params.Since {
			continue
		}
		if params.Until > 0 && doc.updatedAt > params.Until {
			continue
		}
		if params.Skill != "" && !metaIDSkillMatch(doc.chatSkills, params.Skill) {
			continue
		}
		score := 0
		if len(tokens) > 0 {
			matched := true
			for _, token := range tokens {
				// AND semantics per token, best tier wins:
				// name 3, chatSkills 2, profile text 1.
				switch {
				case strings.Contains(doc.nameText, token):
					score += 3
				case strings.Contains(doc.skillText, token):
					score += 2
				case strings.Contains(doc.profileText, token):
					score += 1
				default:
					matched = false
				}
				if !matched {
					break
				}
			}
			if !matched {
				continue
			}
			if exactName != "" && doc.nameExact == exactName {
				score += metaIDExactNameBoost
			}
		}
		matches = append(matches, scoredMetaIDDoc{doc: doc, score: score})
	}
	a.searchDocsMu.RUnlock()

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		if matches[i].doc.updatedAt != matches[j].doc.updatedAt {
			return matches[i].doc.updatedAt > matches[j].doc.updatedAt
		}
		if matches[i].doc.chainName != matches[j].doc.chainName {
			return matches[i].doc.chainName < matches[j].doc.chainName
		}
		return matches[i].doc.globalMetaID < matches[j].doc.globalMetaID
	})

	return sliceMetaIDMatches(matches, offset, params.Size), nil
}

func normaliseMetaIDListParams(params MetaIDListParams) MetaIDListParams {
	params.Keyword = strings.TrimSpace(params.Keyword)
	params.Skill = strings.TrimSpace(params.Skill)
	params.ChainName = strings.ToLower(strings.TrimSpace(params.ChainName))
	if params.Size <= 0 {
		params.Size = defaultMetaIDListSize
	}
	if params.Size > maxMetaIDListSize {
		params.Size = maxMetaIDListSize
	}
	return params
}

func metaIDKeywordTokens(keyword string) []string {
	fields := strings.Fields(strings.ToLower(keyword))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

func metaIDSkillMatch(skills []string, filter string) bool {
	filter = strings.ToLower(strings.TrimSpace(filter))
	if filter == "" {
		return true
	}
	for _, skill := range skills {
		if strings.Contains(strings.ToLower(skill), filter) {
			return true
		}
	}
	return false
}

func metaIDItemFromDoc(doc *metaIDSearchDoc) MetaIDItem {
	return MetaIDItem{
		GlobalMetaID:  doc.globalMetaID,
		MetaID:        doc.metaID,
		Address:       doc.address,
		ChainName:     doc.chainName,
		Name:          doc.name,
		AvatarId:      doc.avatarID,
		Bio:           doc.bio,
		ChatSkills:    doc.chatSkills,
		HasChatPubkey: doc.hasChatPubkey,
		HasHomepage:   doc.hasHomepage,
		CreatedAt:     doc.createdAt,
		UpdatedAt:     doc.updatedAt,
	}
}

func sliceMetaIDMatches(all []scoredMetaIDDoc, offset, size int) *MetaIDListResult {
	if offset > len(all) {
		offset = len(all)
	}
	page := all[offset:]
	hasMore := len(page) > size
	if hasMore {
		page = page[:size]
	}
	items := make([]MetaIDItem, 0, len(page))
	for _, m := range page {
		items = append(items, metaIDItemFromDoc(m.doc))
	}
	result := &MetaIDListResult{Items: items, HasMore: hasMore}
	if hasMore {
		result.NextCursor = encodeMetaIDCursor(offset + size)
	}
	return result
}

type scoredMetaIDDoc struct {
	doc   *metaIDSearchDoc
	score int
}

// Opaque offset cursor, same wire format as the MetaApp list.
type metaIDCursorPayload struct {
	Offset int `json:"o"`
}

func encodeMetaIDCursor(offset int) string {
	raw, _ := json.Marshal(metaIDCursorPayload{Offset: offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeMetaIDCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	var p metaIDCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	if p.Offset < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return p.Offset, nil
}
