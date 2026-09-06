package metaweb

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
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
//
// Ranking hardening (2026-08-23): latin tokens only hit on word boundaries,
// and stopwords (English function words, plus CJK tokens made purely of
// Chinese function characters — stopwords.go) are excluded from scoring.
// Each token's weight is scaled by a smooth per-protocol-key IDF multiplier
// (2026-09-02, replacing the 2026-08-23 binary halving at 30% df):
//
//	multiplier = ln(1 + N/df) / ln(1 + N)
//
// where N is the protocol key's document count and df the token's document
// frequency in that namespace. df = 1 yields 1.0; df = N yields
// ln2/ln(1+N) — small but non-zero, so a direct query for a ubiquitous term
// (e.g. "metaid") still works, while corpus-common tokens stop pinning the
// page to one saturated plateau score.
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

// protocolPriors is the per-protocol-key prior multiplier applied to the
// final relevance score (2026-08-23, IDBots P2): knowledge-carrier protocols
// win near-ties over chatter. Unknown keys default to 1.0.
var protocolPriors = map[string]float64{
	"simplenote":     1.2, // curated knowledge carrier
	"metaprotocol":   1.2, // curated knowledge carrier
	"simplequestion": 1.2, // curated knowledge carrier (on-chain Q&A)
	"simpleanswer":   1.2, // curated knowledge carrier (on-chain Q&A)
	"simplebuzz":     0.9, // stream content
	"metaapp":        1.0, // neutral
	"metabot-skill":  1.0, // neutral
	"skill-service":  1.0, // neutral
}

func protocolPrior(protocolKey string) float64 {
	if prior, ok := protocolPriors[protocolKey]; ok {
		return prior
	}
	return 1.0
}

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

// isTokenBoundaryRune reports whether r delimits a latin token: any rune
// outside [a-z0-9] (the matched text is already lowercased).
func isTokenBoundaryRune(r rune) bool {
	return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
}

// tokenHitsText is the hit test shared by scoring, IDF counting, and
// newest-sort admission. lowerText must already be lowercased. Tokens
// containing CJK runes keep plain substring matching; latin/digit tokens hit
// only when bounded by non-[a-z0-9] runes or string boundaries, so "is" does
// not match "this" or "history".
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

// tagsHit reports whether the token hits any of the lowercased tags.
func tagsHit(tags []string, token string) bool {
	for _, tag := range tags {
		if tokenHitsText(tag, token) {
			return true
		}
	}
	return false
}

// documentTextUnion concatenates the lowercased searchable fields, separated
// by boundary newlines, for the per-request document-frequency scan.
func documentTextUnion(doc *metawebdoc.Document) string {
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
	return sb.String()
}

// tokenIDFWeights computes the per-request weight of each scoring token per
// protocol key: the base weight min(runeCount(token), 4) scaled by the smooth
// IDF multiplier ln(1+N/df)/ln(1+N) of that protocol key's namespace.
// Namespacing is by protocol key (not source aggregator), so a token
// ubiquitous in metabot-skill docs is down-weighted only when scoring
// metabot-skill docs. A token absent from a namespace (df = 0) keeps its
// full base weight, matching the weightsForProtocol fallback. In-memory
// only; the scan is one pass over the corpus.
func tokenIDFWeights(sources []DocumentSource, tokens []string) map[string][]float64 {
	weights := make(map[string][]float64)
	if len(tokens) == 0 {
		return weights
	}
	df := make(map[string][]int)   // protocol key -> per-token document frequency
	totals := make(map[string]int) // protocol key -> document count
	for _, source := range sources {
		if source == nil {
			continue
		}
		docs := source.SearchDocuments()
		for i := range docs {
			key := docs[i].ProtocolKey
			vec, ok := df[key]
			if !ok {
				vec = make([]int, len(tokens))
				df[key] = vec
			}
			totals[key]++
			union := documentTextUnion(&docs[i])
			for j, token := range tokens {
				if tokenHitsText(union, token) {
					vec[j]++
				}
			}
		}
	}
	for key, vec := range df {
		total := totals[key]
		w := make([]float64, len(tokens))
		for j, token := range tokens {
			weight := float64(tokenWeight(token))
			if total > 0 && vec[j] > 0 {
				weight *= math.Log1p(float64(total)/float64(vec[j])) / math.Log1p(float64(total))
			}
			w[j] = weight
		}
		weights[key] = w
	}
	return weights
}

// weightsForProtocol returns the IDF-adjusted weight vector of one protocol
// key, defaulting to full (unscaled) weights when the key has no stats — e.g.
// a doc that entered the snapshot between the df scan and the scoring pass.
func weightsForProtocol(weights map[string][]float64, protocolKey string, tokens []string) []float64 {
	if w, ok := weights[protocolKey]; ok {
		return w
	}
	full := make([]float64, len(tokens))
	for j, token := range tokens {
		full[j] = float64(tokenWeight(token))
	}
	return full
}

// scoreDocument applies the weighted partial match of the contract. Each
// (field, token) pair is counted once; tagsHit counts a token once when it
// hits any tag. weights[i] is the IDF-adjusted weight of tokens[i].
func scoreDocument(doc *metawebdoc.Document, tokens []string, weights []float64, query string) int {
	title := strings.ToLower(doc.Title)
	summary := strings.ToLower(doc.Summary)
	content := strings.ToLower(doc.ContentExcerpt)
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
	}

	// The exact-phrase boost requires at least one non-stopword scoring
	// token, so an all-stopword query (e.g. "what is") matches nothing.
	if len(tokens) > 0 {
		if trimmed := strings.ToLower(strings.TrimSpace(query)); trimmed != "" {
			if strings.Contains(title, trimmed) {
				score += exactTitleBoost
			}
			if strings.Contains(summary, trimmed) || containsAny(tags, trimmed) || strings.Contains(content, trimmed) {
				score += exactOtherBoost
			}
		}
	}
	// The protocol prior multiplies the final score; multiplying a zero
	// score stays zero, so the admission rule is unaffected.
	return int(score * protocolPrior(doc.ProtocolKey))
}

// documentMatchesAny is the sort=newest admission filter: scoring is
// bypassed, but a document must still match at least one scoring token
// (stopwords excluded by the caller) in at least one field.
func documentMatchesAny(doc *metawebdoc.Document, tokens []string) bool {
	title := strings.ToLower(doc.Title)
	summary := strings.ToLower(doc.Summary)
	content := strings.ToLower(doc.ContentExcerpt)
	for _, token := range tokens {
		if tokenHitsText(title, token) || tokenHitsText(summary, token) || tokenHitsText(content, token) {
			return true
		}
		for _, tag := range doc.Tags {
			if tokenHitsText(strings.ToLower(tag), token) {
				return true
			}
		}
	}
	return false
}

// containsAny is a plain substring test over several texts, used only by the
// exact-phrase boost (phrase matching is substring-based by contract).
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
