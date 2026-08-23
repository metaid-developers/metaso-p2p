package metaweb

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// Scoring constants of the /api/metaweb/search contract:
//
//	score = 5*titleHit + 3*tagsHit + 2*summaryHit + 1*contentHit
//
// where each hit sums token weights, token weight = min(runeCount(token), 4),
// and each (field, token) pair is counted once. The exact-phrase boost adds a
// flat +100 when the whole trimmed query is a substring of the title, and a
// flat +10 (at most once per document) when it is a substring of the summary,
// any tag, or the content excerpt.
const (
	defaultSearchSize = 10
	maxSearchSize     = 50

	weightTitle   = 5
	weightTags    = 3
	weightSummary = 2
	weightContent = 1

	maxTokenWeight = 4

	exactTitleBoost = 100
	exactOtherBoost = 10

	// slowSearchThresholdMs is the elapsed budget above which the handler
	// logs a warn line with the query, active filters, result count, and
	// elapsed milliseconds.
	slowSearchThresholdMs = 300
)

// tokenizeQuery turns the query into the deduplicated, lowercased match-token
// set — identical rules to botsearch:
//   - whitespace-split into segments;
//   - a segment containing CJK runes (Han, Hiragana, Katakana, Hangul) yields
//     the whole segment plus every CJK bigram within each maximal run of
//     consecutive CJK runes (a single-CJK-char run yields that char);
//   - a latin/digit segment yields one whole-word token.
//
// Order of first occurrence is preserved.
func tokenizeQuery(query string) []string {
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

	for _, segment := range strings.Fields(query) {
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

// scoreDocument applies the weighted partial match of the contract. Each
// (field, token) pair is counted once; tagsHit counts a token once when it
// hits any tag.
func scoreDocument(doc *metawebdoc.Document, tokens []string, query string) int {
	title := strings.ToLower(doc.Title)
	summary := strings.ToLower(doc.Summary)
	content := strings.ToLower(doc.ContentExcerpt)
	tags := make([]string, 0, len(doc.Tags))
	for _, tag := range doc.Tags {
		tags = append(tags, strings.ToLower(tag))
	}

	score := 0
	for _, token := range tokens {
		weight := tokenWeight(token)
		if strings.Contains(title, token) {
			score += weightTitle * weight
		}
		if containsAny(tags, token) {
			score += weightTags * weight
		}
		if strings.Contains(summary, token) {
			score += weightSummary * weight
		}
		if strings.Contains(content, token) {
			score += weightContent * weight
		}
	}

	if trimmed := strings.ToLower(strings.TrimSpace(query)); trimmed != "" {
		if strings.Contains(title, trimmed) {
			score += exactTitleBoost
		}
		if strings.Contains(summary, trimmed) || containsAny(tags, trimmed) || strings.Contains(content, trimmed) {
			score += exactOtherBoost
		}
	}
	return score
}

// documentMatchesAny is the sort=newest admission filter: scoring is
// bypassed, but a document must still match at least one token in at least
// one field.
func documentMatchesAny(doc *metawebdoc.Document, tokens []string) bool {
	title := strings.ToLower(doc.Title)
	summary := strings.ToLower(doc.Summary)
	content := strings.ToLower(doc.ContentExcerpt)
	for _, token := range tokens {
		if strings.Contains(title, token) || strings.Contains(summary, token) || strings.Contains(content, token) {
			return true
		}
		for _, tag := range doc.Tags {
			if strings.Contains(strings.ToLower(tag), token) {
				return true
			}
		}
	}
	return false
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
// service lists and botsearch: base64url(JSON {"o": offset}).
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
