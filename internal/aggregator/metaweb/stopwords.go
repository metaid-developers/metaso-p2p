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

// chineseFunctionChars are Chinese function characters (2026-09-02 ranking
// hardening): particles, auxiliary verbs, pronouns, and quantifiers that
// carry no topical signal. A CJK scoring token (single char or bigram from
// cjkRuns) whose runes are ALL function characters is a stopword; tokens
// mixing function and content characters ("一样", "视频") keep scoring.
var chineseFunctionChars = map[rune]bool{
	'的': true, '了': true, '是': true, '在': true, '和': true, '就': true,
	'都': true, '而': true, '不': true, '及': true, '与': true, '或': true,
	'一': true, '个': true, '没': true, '我': true, '们': true, '你': true,
	'他': true, '她': true, '这': true, '那': true, '也': true, '有': true,
	'要': true, '会': true, '能': true, '可': true, '对': true, '从': true,
	'被': true, '把': true, '向': true, '么': true, '嘛': true, '呢': true,
	'吧': true, '啊': true, '吗': true,
}

// isStopwordToken reports whether a tokenized query token is a stopword:
// either an English function word, or a CJK token composed entirely of
// Chinese function characters (e.g. "我们", "一个", "是").
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
