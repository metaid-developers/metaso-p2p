package metaweb

// searchStopwords is the English function-word list excluded from scoring
// (v1 ranking hardening, 2026-08-23). Stopword tokens contribute nothing:
// they are removed from the scoring token set before boundary matching, IDF
// weighting, and newest-sort admission. The list is intentionally small and
// easy to append to.
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

// isStopwordToken reports whether a tokenized query token is an English
// stopword. Tokens containing CJK runes are never stopwords.
func isStopwordToken(token string) bool {
	return !containsCJK(token) && searchStopwords[token]
}

// scoringTokens drops stopword tokens from the tokenized query. The remaining
// tokens drive scoring, IDF weighting, and newest-sort admission; an empty
// result means the query consisted of stopwords only and matches nothing.
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
