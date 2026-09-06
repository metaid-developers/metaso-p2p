package qa

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Scoring of /api/qa/search, per docs/specs/2026-09-07-metaweb-qa-api.md:
//
//	score = 5*titleHit + 3*tagsHit + 2*summaryHit + 1*contentHit + 1*answersHit
//
// Tokenization, stopwords, the latin word-boundary hit test, and the smooth
// IDF multiplier mirror the /api/metaweb/search rules (see
// docs/specs/2026-08-23-metaweb-search-api.md and its 2026-09-02 quality
// addendum); the helpers are package-local copies per the established
// botsearch/metaweb convention. answersHit is the matched-question
// aggregation: answer keyword hits boost their question at the same low
// weight as the question body, never surface the answer itself.
const (
	defaultSearchSize = 10
	maxSearchSize     = 50

	weightTitle    = 5
	weightTags     = 3
	weightSummary  = 2
	weightContent  = 1
	weightAnswers  = 1
	maxTokenWeight = 4

	exactTitleBoost = 100
	exactOtherBoost = 10
)

// searchStopwords is the English function-word list excluded from scoring.
var searchStopwords = map[string]bool{
	"a": true, "an": true, "and": true, "or": true, "not": true,
	"the": true, "this": true, "that": true, "these": true, "those": true,
	"is": true, "are": true, "was": true, "were": true, "be": true, "been": true,
	"do": true, "does": true, "have": true, "has": true, "had": true,
	"can": true, "could": true, "will": true, "would": true, "should": true,
	"what": true, "how": true, "who": true, "which": true,
	"of": true, "to": true, "in": true, "on": true, "for": true, "with": true,
	"about": true, "at": true, "by": true, "from": true, "as": true, "if": true, "so": true,
	"it": true, "its": true,
	"i": true, "me": true, "my": true, "we": true, "our": true,
	"you": true, "your": true, "he": true, "his": true, "she": true, "her": true,
	"they": true, "them": true, "their": true,
}

// chineseFunctionChars: a CJK token composed entirely of these function
// characters is a stopword.
var chineseFunctionChars = map[rune]bool{
	'的': true, '了': true, '是': true, '在': true, '和': true, '就': true,
	'都': true, '而': true, '不': true, '及': true, '与': true, '或': true,
	'一': true, '个': true, '没': true, '我': true, '们': true, '你': true,
	'他': true, '她': true, '这': true, '那': true, '也': true, '有': true,
	'要': true, '会': true, '能': true, '可': true, '对': true, '从': true,
	'被': true, '把': true, '向': true, '么': true, '嘛': true, '呢': true,
	'吧': true, '啊': true, '吗': true,
}

func isStopwordToken(token string) bool {
	if !containsCJK(token) {
		return searchStopwords[token]
	}
	for _, r := range token {
		if !chineseFunctionChars[r] {
			return false
		}
	}
	return token != ""
}

func scoringTokens(tokens []string) []string {
	var kept []string
	for _, token := range tokens {
		if isStopwordToken(token) {
			continue
		}
		kept = append(kept, token)
	}
	return kept
}

// tokenizeQuery splits the query into deduplicated lowercased match tokens:
// whitespace segments; CJK segments additionally emit per-run bigrams (a
// single-char run yields that char); latin/digit segments are whole words.
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

// isTokenBoundaryRune reports whether r delimits a latin token: any rune
// outside [a-z0-9] (the matched text is already lowercased).
func isTokenBoundaryRune(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
}

// tokenHitsText: CJK tokens keep substring matching; latin/digit tokens hit
// only on word boundaries ("is" does not hit "this").
func tokenHitsText(lowerText, token string) bool {
	if token == "" {
		return false
	}
	if containsCJK(token) {
		return strings.Contains(lowerText, token)
	}
	for offset := 0; offset+len(token) <= len(lowerText); {
		idx := strings.Index(lowerText[offset:], token)
		if idx < 0 {
			return false
		}
		idx += offset
		leftOK := idx == 0
		if !leftOK {
			r, _ := utf8.DecodeLastRuneInString(lowerText[:idx])
			leftOK = isTokenBoundaryRune(r)
		}
		rightIdx := idx + len(token)
		rightOK := rightIdx == len(lowerText)
		if !rightOK {
			r, _ := utf8.DecodeRuneInString(lowerText[rightIdx:])
			rightOK = isTokenBoundaryRune(r)
		}
		if leftOK && rightOK {
			return true
		}
		offset = idx + 1
	}
	return false
}

func tagsHit(tags []string, token string) bool {
	for _, tag := range tags {
		if tokenHitsText(tag, token) {
			return true
		}
	}
	return false
}

// docTextUnion concatenates the lowercased scoring fields for the IDF
// document-frequency scan.
func docTextUnion(doc *questionDoc) string {
	var sb strings.Builder
	sb.WriteString(strings.ToLower(doc.Title))
	for _, tag := range doc.Tags {
		sb.WriteByte('\n')
		sb.WriteString(strings.ToLower(tag))
	}
	sb.WriteByte('\n')
	sb.WriteString(strings.ToLower(doc.Summary))
	sb.WriteByte('\n')
	sb.WriteString(strings.ToLower(doc.ContentExcerpt))
	sb.WriteByte('\n')
	sb.WriteString(strings.ToLower(doc.AnswersExcerpt))
	return sb.String()
}

// tokenIDFWeights computes the per-request IDF-adjusted weight of each
// scoring token over the single qa question namespace:
// weight = min(runeCount, 4) * ln(1+N/df) / ln(1+N). A token absent from the
// corpus (df = 0) keeps its full base weight.
func tokenIDFWeights(docs []questionDoc, tokens []string) []float64 {
	weights := make([]float64, len(tokens))
	if len(tokens) == 0 || len(docs) == 0 {
		for i, token := range tokens {
			weights[i] = float64(tokenWeight(token))
		}
		return weights
	}
	df := make([]int, len(tokens))
	for i := range docs {
		union := docTextUnion(&docs[i])
		for j, token := range tokens {
			if tokenHitsText(union, token) {
				df[j]++
			}
		}
	}
	total := float64(len(docs))
	for j, token := range tokens {
		weight := float64(tokenWeight(token))
		if df[j] > 0 {
			weight *= math.Log1p(total/float64(df[j])) / math.Log1p(total)
		}
		weights[j] = weight
	}
	return weights
}

// scoreQuestionDoc applies the weighted partial match; each (field, token)
// pair counts once. The exact-phrase boost requires at least one
// non-stopword token.
func scoreQuestionDoc(doc *questionDoc, tokens []string, weights []float64, query string) int {
	title := strings.ToLower(doc.Title)
	summary := strings.ToLower(doc.Summary)
	content := strings.ToLower(doc.ContentExcerpt)
	answers := strings.ToLower(doc.AnswersExcerpt)
	tags := make([]string, 0, len(doc.Tags))
	for _, tag := range doc.Tags {
		tags = append(tags, strings.ToLower(tag))
	}

	score := 0.0
	for i, token := range tokens {
		weight := weights[i]
		if tokenHitsText(title, token) {
			score += weightTitle * weight
		}
		if tagsHit(tags, token) {
			score += weightTags * weight
		}
		if tokenHitsText(summary, token) {
			score += weightSummary * weight
		}
		if tokenHitsText(content, token) {
			score += weightContent * weight
		}
		if tokenHitsText(answers, token) {
			score += weightAnswers * weight
		}
	}
	if len(tokens) > 0 {
		if trimmed := strings.ToLower(strings.TrimSpace(query)); trimmed != "" {
			if strings.Contains(title, trimmed) {
				score += exactTitleBoost
			}
			if strings.Contains(summary, trimmed) || containsAny(tags, trimmed) ||
				strings.Contains(content, trimmed) || strings.Contains(answers, trimmed) {
				score += exactOtherBoost
			}
		}
	}
	return int(score)
}

// docMatchesAny is the sort=newest admission filter: at least one scoring
// token must hit at least one field.
func docMatchesAny(doc *questionDoc, tokens []string) bool {
	title := strings.ToLower(doc.Title)
	summary := strings.ToLower(doc.Summary)
	content := strings.ToLower(doc.ContentExcerpt)
	answers := strings.ToLower(doc.AnswersExcerpt)
	for _, token := range tokens {
		if tokenHitsText(title, token) || tokenHitsText(summary, token) ||
			tokenHitsText(content, token) || tokenHitsText(answers, token) {
			return true
		}
		if tagsHit(doc.Tags, token) {
			return true
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

// Opaque offset cursor, same wire format as the metaweb / skill-service
// lists: base64url(JSON {"o": offset}).
type offsetCursorPayload struct {
	Offset int `json:"o"`
}

func encodeOffsetCursor(offset int) string {
	raw, _ := json.Marshal(offsetCursorPayload{Offset: offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeOffsetCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	var p offsetCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	if p.Offset < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return p.Offset, nil
}
