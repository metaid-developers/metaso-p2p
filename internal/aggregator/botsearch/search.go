package botsearch

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/groupchat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
)

// Scoring constants of the /api/bots/search contract:
//
//	score = 4*nameHit + 2*skillHit + 1*bioHit + 0.5*roleHintHit + 1*groupTaskHit
//
// where each hit sums token weights, token weight = min(runeCount(token), 4),
// and each (field-category, token) pair is counted once.
const (
	defaultSearchLimit = 10
	maxSearchLimit     = 50

	weightName      = 4.0
	weightSkill     = 2.0
	weightBio       = 1.0 // bio/role/goal share one field-category
	weightGroupTask = 1.0 // recent group title/note share one field-category
	weightRoleHint  = 0.5 // flat, at most one hit per candidate

	maxTokenWeight = 4

	// exactNameBoost lifts a candidate whose name exactly equals the whole
	// query above any fuzzy-only score, so exact hits never sort below
	// partial matches.
	exactNameBoost = 1000.0

	// recentGroupTasksCap is the page size of the recentGroupTasks fold-in;
	// groupTaskCount always reports the full history length.
	recentGroupTasksCap = 5
)

// roleHintSynonyms maps each query-side roleHint onto free-text synonyms
// (en+zh) matched as substrings against role + bio + chatSkills. The roleHint
// is a soft prior from the caller, never a field bots stamp on themselves.
var roleHintSynonyms = map[string][]string{
	"content":     {"content", "内容", "文案", "写作", "writer"},
	"design":      {"design", "设计", "视觉", "ui"},
	"engineering": {"engineering", "开发", "工程", "代码", "code"},
	"promotion":   {"promotion", "推广", "营销", "运营"},
	"domain":      {"domain", "领域", "专家", "顾问"},
}

// matchReason is one contributing (field, token) pair, weight = field
// coefficient × token weight (roleHint is the flat 0.5 exception).
type matchReason struct {
	Field  string  `json:"field"`
	Token  string  `json:"token"`
	Weight float64 `json:"weight"`
}

// tokenize turns the query and skill strings into the deduplicated,
// lowercased match-token set:
//   - whitespace-split every input into segments;
//   - a segment containing CJK runes yields the whole segment plus every CJK
//     bigram within it (a 2-char segment thereby yields itself once; a run of
//     a single CJK char yields that char);
//   - a latin/digit segment yields one whole-word token.
//
// Order of first occurrence is preserved so matchReasons stay deterministic.
func tokenize(query string, skills []string) []string {
	var tokens []string
	seen := make(map[string]struct{})
	emit := func(token string) {
		if token == "" {
			return
		}
		if _, ok := seen[token]; ok {
			return
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}

	segments := strings.Fields(query)
	for _, skill := range skills {
		segments = append(segments, strings.Fields(skill)...)
	}
	for _, segment := range segments {
		segment = strings.ToLower(segment)
		if !containsCJK(segment) {
			emit(segment)
			continue
		}
		emit(segment)
		for _, run := range cjkRuns(segment) {
			runes := []rune(run)
			if len(runes) == 1 {
				emit(string(runes[0]))
				continue
			}
			for i := 0; i+1 < len(runes); i++ {
				emit(string(runes[i : i+2]))
			}
		}
	}
	return tokens
}

func isCJKRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func containsCJK(s string) bool {
	for _, r := range s {
		if isCJKRune(r) {
			return true
		}
	}
	return false
}

// cjkRuns splits s into maximal runs of consecutive CJK runes.
func cjkRuns(s string) []string {
	var runs []string
	var current []rune
	flush := func() {
		if len(current) > 0 {
			runs = append(runs, string(current))
			current = nil
		}
	}
	for _, r := range s {
		if isCJKRune(r) {
			current = append(current, r)
		} else {
			flush()
		}
	}
	flush()
	return runs
}

func tokenWeight(token string) int {
	runes := utf8.RuneCountInString(token)
	if runes > maxTokenWeight {
		return maxTokenWeight
	}
	return runes
}

// scoreResult is the per-candidate scoring output.
type scoreResult struct {
	score   float64
	reasons []matchReason
}

// scoreCandidate applies the weighted partial match of the contract. History
// titles/notes take part in scoring (the "has done similar work" soft bonus),
// so the caller passes the already-fetched group history in.
func scoreCandidate(profile userinfo.BotSearchProfile, history []groupchat.GroupHistoryItem, tokens []string, roleHint, query string) scoreResult {
	result := scoreResult{reasons: make([]matchReason, 0)}

	nameText := strings.ToLower(profile.Name)
	bioText := strings.ToLower(profile.Bio)
	roleText := strings.ToLower(profile.Role)
	goalText := strings.ToLower(profile.Goal)
	skillTexts := make([]string, 0, len(profile.ChatSkills))
	for _, skill := range profile.ChatSkills {
		skillTexts = append(skillTexts, strings.ToLower(skill))
	}
	groupTitleTexts := make([]string, 0, len(history))
	groupNoteTexts := make([]string, 0, len(history))
	for _, item := range history {
		groupTitleTexts = append(groupTitleTexts, strings.ToLower(item.Title))
		groupNoteTexts = append(groupNoteTexts, strings.ToLower(item.Goal))
	}

	for _, token := range tokens {
		weight := float64(tokenWeight(token))
		if strings.Contains(nameText, token) {
			result.score += weightName * weight
			result.reasons = append(result.reasons, matchReason{Field: "name", Token: token, Weight: weightName * weight})
		}
		if containsAny(skillTexts, token) {
			result.score += weightSkill * weight
			result.reasons = append(result.reasons, matchReason{Field: "chatSkills", Token: token, Weight: weightSkill * weight})
		}
		// bio/role/goal are one field-category: a token is counted once even
		// when it hits several of them; the reason names the first hit.
		switch {
		case strings.Contains(bioText, token):
			result.score += weightBio * weight
			result.reasons = append(result.reasons, matchReason{Field: "bio", Token: token, Weight: weightBio * weight})
		case strings.Contains(roleText, token):
			result.score += weightBio * weight
			result.reasons = append(result.reasons, matchReason{Field: "role", Token: token, Weight: weightBio * weight})
		case strings.Contains(goalText, token):
			result.score += weightBio * weight
			result.reasons = append(result.reasons, matchReason{Field: "goal", Token: token, Weight: weightBio * weight})
		}
		// Same one-category rule for recent group title/note.
		switch {
		case containsAny(groupTitleTexts, token):
			result.score += weightGroupTask * weight
			result.reasons = append(result.reasons, matchReason{Field: "groupTaskTitle", Token: token, Weight: weightGroupTask * weight})
		case containsAny(groupNoteTexts, token):
			result.score += weightGroupTask * weight
			result.reasons = append(result.reasons, matchReason{Field: "groupTaskNote", Token: token, Weight: weightGroupTask * weight})
		}
	}

	if synonyms, ok := roleHintSynonyms[roleHint]; ok {
		haystack := roleText + "\n" + bioText + "\n" + strings.Join(skillTexts, "\n")
		for _, synonym := range synonyms {
			if strings.Contains(haystack, strings.ToLower(synonym)) {
				result.score += weightRoleHint
				result.reasons = append(result.reasons, matchReason{Field: "roleHint", Token: synonym, Weight: weightRoleHint})
				break
			}
		}
	}

	// Exact full-query name match: case-insensitive equality, never sorts
	// below fuzzy-only hits.
	if trimmed := strings.TrimSpace(query); trimmed != "" && strings.EqualFold(strings.TrimSpace(profile.Name), trimmed) {
		result.score += exactNameBoost
		result.reasons = append(result.reasons, matchReason{Field: "name", Token: trimmed, Weight: exactNameBoost})
	}

	sort.SliceStable(result.reasons, func(i, j int) bool {
		return result.reasons[i].Weight > result.reasons[j].Weight
	})
	return result
}

func containsAny(texts []string, token string) bool {
	for _, text := range texts {
		if strings.Contains(text, token) {
			return true
		}
	}
	return false
}

// Opaque offset cursor, same wire format as the MetaApp / MetaID / skill
// service lists: base64url(JSON {"o": offset}).
type searchCursorPayload struct {
	Offset int `json:"o"`
}

func encodeSearchCursor(offset int) string {
	raw, _ := json.Marshal(searchCursorPayload{Offset: offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeSearchCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	var p searchCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	if p.Offset < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return p.Offset, nil
}
