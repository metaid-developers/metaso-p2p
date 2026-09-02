package metaweb

import (
	"math"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// fullWeights returns the IDF-neutral weight of each token for tests that
// exercise scoring without a corpus scan.
func fullWeights(tokens []string) []float64 {
	weights := make([]float64, len(tokens))
	for i, token := range tokens {
		weights[i] = float64(tokenWeight(token))
	}
	return weights
}

// smoothIDFWeight mirrors the scorer's per-namespace formula for test
// expectations: tokenWeight × ln(1+N/df)/ln(1+N); df=0 keeps the base weight.
func smoothIDFWeight(token string, total, df int) float64 {
	if total <= 0 || df <= 0 {
		return float64(tokenWeight(token))
	}
	return float64(tokenWeight(token)) * math.Log1p(float64(total)/float64(df)) / math.Log1p(float64(total))
}

func almostEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestTokenizeQuery_SingleCJKCharRun(t *testing.T) {
	// A single-CJK-char run yields that char; the segment also yields itself.
	tokens := tokenizeQuery("a链b")
	want := []string{"a链b", "链"}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Fatalf("tokens = %v, want %v", tokens, want)
		}
	}
}

func TestTokenizeQuery_CJKBigrams(t *testing.T) {
	// "教程笔记" (4 CJK runes) yields the segment plus 3 bigrams.
	tokens := tokenizeQuery("教程笔记")
	want := []string{"教程笔记", "教程", "程笔", "笔记"}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Fatalf("tokens = %v, want %v", tokens, want)
		}
	}
}

func TestTokenizeQuery_MixedCJkAndLatinSegments(t *testing.T) {
	tokens := tokenizeQuery("MetaID 教程")
	want := []string{"metaid", "教程"}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Fatalf("tokens = %v, want %v", tokens, want)
		}
	}
}

func TestTokenizeQuery_Dedup(t *testing.T) {
	// "教程教程" contributes the bigrams 教程 (dup), 程教, 教程 (dup); the
	// repeated segment and repeated bigrams are emitted once each.
	tokens := tokenizeQuery("教程 教程教程 教程")
	want := []string{"教程", "教程教程", "程教"}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Fatalf("tokens = %v, want %v", tokens, want)
		}
	}
}

func TestScoreDocument_FieldWeights(t *testing.T) {
	// Token "alpha" (5 runes, capped weight 4) hitting each field in
	// isolation must score 5/3/2/1 * 4.
	base := metawebdoc.Document{}
	tokens := []string{"alpha"}

	cases := []struct {
		name string
		doc  metawebdoc.Document
		want int
	}{
		{"title", metawebdoc.Document{Title: "alpha release"}, 5 * 4},
		{"tags", metawebdoc.Document{Tags: []string{"alpha"}}, 3 * 4},
		{"summary", metawebdoc.Document{Summary: "an alpha summary"}, 2 * 4},
		{"content", metawebdoc.Document{ContentExcerpt: "alpha body"}, 1 * 4},
	}
	for _, tc := range cases {
		doc := base
		doc = tc.doc
		if got := scoreDocument(&doc, tokens, fullWeights(tokens), "alpha other"); got != tc.want {
			t.Errorf("%s: score = %d, want %d", tc.name, got, tc.want)
		}
	}

	// A token hitting several fields counts once per field.
	doc := metawebdoc.Document{Title: "alpha", Tags: []string{"alpha"}, Summary: "alpha", ContentExcerpt: "alpha"}
	if got, want := scoreDocument(&doc, tokens, fullWeights(tokens), "q"), (5+3+2+1)*4; got != want {
		t.Errorf("all-fields: score = %d, want %d", got, want)
	}
}

func TestScoreDocument_TokenWeightCapsAtFourRunes(t *testing.T) {
	doc := metawebdoc.Document{Title: "ab"}
	if got, want := scoreDocument(&doc, []string{"ab"}, []float64{2}, "q"), 5*2; got != want {
		t.Errorf("2-rune token: score = %d, want %d", got, want)
	}
	doc = metawebdoc.Document{Title: "abcdefgh"}
	if got, want := scoreDocument(&doc, []string{"abcdefgh"}, []float64{4}, "q"), 5*4; got != want {
		t.Errorf("8-rune token: score = %d, want %d (weight capped at 4)", got, want)
	}
}

func TestScoreDocument_ExactPhraseBoost(t *testing.T) {
	// Whole trimmed q substring of the title → flat +100.
	doc := metawebdoc.Document{Title: "The IDBots Beginner Tutorial"}
	tokens := tokenizeQuery("IDBots Beginner Tutorial")
	score := scoreDocument(&doc, tokens, fullWeights(tokens), "IDBots Beginner Tutorial")
	if score < exactTitleBoost {
		t.Fatalf("title phrase boost missing: score = %d", score)
	}

	// Phrase only in summary/content/tags → flat +10, at most once per doc.
	doc = metawebdoc.Document{
		Title:          "something else",
		Summary:        "see the beginner tutorial notes",
		Tags:           []string{"beginner tutorial"},
		ContentExcerpt: "a beginner tutorial body",
	}
	tokens = tokenizeQuery("beginner tutorial")
	score = scoreDocument(&doc, tokens, fullWeights(tokens), "beginner tutorial")
	// Token weights: "beginner" (4), "tutorial" (4); the whole CJK-less
	// segment "beginner tutorial" is not emitted (whitespace-split), so only
	// the two latin tokens hit summary+tags+content: 3 fields * 2 tokens.
	want := (2+3+1)*4 + (2+3+1)*4 + exactOtherBoost
	if score != want {
		t.Fatalf("summary/tag/content phrase boost: score = %d, want %d", score, want)
	}
}

func TestScoreDocument_ScoreZeroExcludedByCaller(t *testing.T) {
	doc := metawebdoc.Document{Title: "unrelated"}
	tokens := tokenizeQuery("nothing matches")
	if got := scoreDocument(&doc, tokens, fullWeights(tokens), "nothing matches"); got != 0 {
		t.Fatalf("score = %d, want 0", got)
	}
}

func TestSearchCursor_RoundTrip(t *testing.T) {
	encoded := encodeSearchCursor(20)
	offset, err := decodeSearchCursor(encoded)
	if err != nil || offset != 20 {
		t.Fatalf("round trip = %d, %v", offset, err)
	}
	if offset, err := decodeSearchCursor(""); err != nil || offset != 0 {
		t.Fatalf("empty cursor = %d, %v", offset, err)
	}
	if _, err := decodeSearchCursor("!!!not-base64!!!"); err == nil {
		t.Fatalf("invalid base64 cursor accepted")
	}
	// Negative offset payload must be rejected.
	bad := encodeSearchCursor(-5)
	if _, err := decodeSearchCursor(bad); err == nil {
		t.Fatalf("negative offset cursor accepted")
	}
}

func TestDocumentMatchesAny(t *testing.T) {
	doc := &metawebdoc.Document{Title: "nothing here", Tags: []string{"misc"}}
	if documentMatchesAny(doc, tokenizeQuery("absent")) {
		t.Fatalf("unexpected match")
	}
	doc.Tags = []string{"Tutorial"}
	if !documentMatchesAny(doc, tokenizeQuery("tutorial")) {
		t.Fatalf("tag match missed")
	}
}

func TestTokenHitsText_LatinWordBoundaries(t *testing.T) {
	cases := []struct {
		text  string
		token string
		want  bool
	}{
		{"this history", "is", false},                // "is" as a substring of this/history must not hit
		{"what is this", "is", true},                 // whole word hits
		{"history", "history", true},                 // whole-word self hit
		{"the metaid-protocol spec", "metaid", true}, // punctuation bounds a token
		{"metaid2", "metaid", false},                 // a digit is not a boundary
		{"seedance", "seedance", true},               // string boundaries on both sides
		{"an island", "is", false},                   // prefix of a longer word
	}
	for _, tc := range cases {
		if got := tokenHitsText(tc.text, tc.token); got != tc.want {
			t.Errorf("tokenHitsText(%q, %q) = %v, want %v", tc.text, tc.token, got, tc.want)
		}
	}
}

func TestTokenHitsText_CJKSubstringUnchanged(t *testing.T) {
	// CJK tokens and bigrams keep plain substring matching.
	if !tokenHitsText("数字身份协议", "身份") {
		t.Fatalf("CJK bigram substring match missed")
	}
	if tokenHitsText("数字协议", "身份") {
		t.Fatalf("unexpected CJK match")
	}
}

func TestScoringTokens_StopwordsExcluded(t *testing.T) {
	// Mixed query: stopwords drop out, content words stay.
	tokens := scoringTokens(tokenizeQuery("what is seedance"))
	if len(tokens) != 1 || tokens[0] != "seedance" {
		t.Fatalf("tokens = %v, want [seedance]", tokens)
	}
	// All-stopword query yields no scoring tokens.
	if tokens := scoringTokens(tokenizeQuery("what is")); len(tokens) != 0 {
		t.Fatalf("tokens = %v, want empty", tokens)
	}
	// CJK tokens mixing function and content characters still score.
	tokens = scoringTokens(tokenizeQuery("是什么"))
	want := []string{"是什么", "是什", "什么"}
	if len(tokens) != len(want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
	for i := range want {
		if tokens[i] != want[i] {
			t.Fatalf("tokens = %v, want %v", tokens, want)
		}
	}
}

func TestScoreDocument_StopwordTokenContributesZero(t *testing.T) {
	// A stopword token passed with weight 0 contributes nothing even when
	// the exact phrase is present; without scoring tokens the boost is off.
	doc := metawebdoc.Document{Title: "what is this"}
	if got := scoreDocument(&doc, nil, nil, "what is"); got != 0 {
		t.Fatalf("all-stopword query: score = %d, want 0", got)
	}
	// Stopword hits do not add weight in a mixed query either: only the
	// content word "seedance" (weight 4, title field) scores.
	doc = metawebdoc.Document{Title: "what is seedance", Summary: "what is"}
	tokens := scoringTokens(tokenizeQuery("what is seedance"))
	if got, want := scoreDocument(&doc, tokens, fullWeights(tokens), "q"), 5*4; got != want {
		t.Fatalf("mixed query: score = %d, want %d", got, want)
	}
}

// Chinese stopwords (2026-09-02): a CJK token composed entirely of function
// characters contributes no score; mixed tokens keep scoring.
func TestScoringTokens_ChineseFunctionTokens(t *testing.T) {
	// "我们" is all function chars and drops; "学习" stays.
	tokens := scoringTokens(tokenizeQuery("我们 学习"))
	if len(tokens) != 1 || tokens[0] != "学习" {
		t.Fatalf("tokens = %v, want [学习]", tokens)
	}
	// An all-function CJK query yields no scoring tokens (matches nothing).
	if tokens := scoringTokens(tokenizeQuery("我们")); len(tokens) != 0 {
		t.Fatalf("tokens = %v, want empty", tokens)
	}

	stopwordTokens := []string{"我们", "一个", "是", "的了", "吗", "也有"}
	for _, token := range stopwordTokens {
		if !isStopwordToken(token) {
			t.Errorf("isStopwordToken(%q) = false, want true", token)
		}
	}
	contentTokens := []string{"视频", "一样", "是什", "什么", "学习", "不只"}
	for _, token := range contentTokens {
		if isStopwordToken(token) {
			t.Errorf("isStopwordToken(%q) = true, want false", token)
		}
	}
}

func TestTokenIDFWeights_SmoothDownweighting(t *testing.T) {
	// 10 simplenote docs: "common" in 4, "rare" in 3. The smooth multiplier
	// ln(1+N/df)/ln(1+N) grades both below full weight, the commoner lower.
	docs := make([]metawebdoc.Document, 0, 10)
	for i := 0; i < 4; i++ {
		docs = append(docs, metawebdoc.Document{ProtocolKey: "simplenote", Title: "common doc"})
	}
	for i := 0; i < 3; i++ {
		docs = append(docs, metawebdoc.Document{ProtocolKey: "simplenote", Title: "rare doc"})
	}
	for i := 0; i < 3; i++ {
		docs = append(docs, metawebdoc.Document{ProtocolKey: "simplenote", Title: "filler doc"})
	}
	sources := []DocumentSource{&fakeDocSource{docs: docs}}

	weights := tokenIDFWeights(sources, []string{"common", "rare"})["simplenote"]
	if want := smoothIDFWeight("common", 10, 4); !almostEqual(weights[0], want) {
		t.Fatalf("common weight = %v, want %v", weights[0], want)
	}
	if want := smoothIDFWeight("rare", 10, 3); !almostEqual(weights[1], want) {
		t.Fatalf("rare weight = %v, want %v", weights[1], want)
	}
	if !(weights[0] < weights[1] && weights[1] < 4.0) {
		t.Fatalf("weights not graded: common=%v rare=%v full=4", weights[0], weights[1])
	}

	// Boundaries: df=1 keeps the full base weight; df=N yields
	// tokenWeight × ln2/ln(1+N) — small but non-zero, so direct queries for
	// ubiquitous terms still work.
	single := []DocumentSource{&fakeDocSource{docs: []metawebdoc.Document{
		{ProtocolKey: "simplenote", Title: "unique term"},
	}}}
	if got := tokenIDFWeights(single, []string{"unique"})["simplenote"][0]; got != 4.0 {
		t.Fatalf("df=1 weight = %v, want 4.0 (full)", got)
	}
	if got, want := weights[0], 0.0; got <= want {
		t.Fatalf("df<N weight must stay positive, got %v", got)
	}
	allHit := make([]metawebdoc.Document, 0, 10)
	for i := 0; i < 10; i++ {
		allHit = append(allHit, metawebdoc.Document{ProtocolKey: "simplenote", Title: "everywhere doc"})
	}
	got := tokenIDFWeights([]DocumentSource{&fakeDocSource{docs: allHit}}, []string{"everywhere"})["simplenote"][0]
	if want := 4 * math.Log(2) / math.Log1p(10); !almostEqual(got, want) {
		t.Fatalf("df=N weight = %v, want %v (ln2/ln(1+N) scaled)", got, want)
	}

	// The scaled weight scales the resulting score: title hit of "common".
	doc := metawebdoc.Document{Title: "common term"}
	if got, want := scoreDocument(&doc, []string{"common"}, weights[:1], "q"), int(5*weights[0]); got != want {
		t.Fatalf("scaled score = %d, want %d", got, want)
	}
}

// Acceptance analog for the 2026-09-02 q=skill/q=metaweb saturation report:
// in a corpus where every doc carries the common token, a doc matching the
// rare token outranks the common-only docs by a wide, non-plateau margin.
func TestScoreDocument_RareTokenOutranksCorpusCommon(t *testing.T) {
	docs := make([]metawebdoc.Document, 0, 10)
	for i := 0; i < 9; i++ {
		docs = append(docs, metawebdoc.Document{ProtocolKey: "simplenote", Title: "metaweb weekly"})
	}
	docs = append(docs, metawebdoc.Document{ProtocolKey: "simplenote", Title: "metaweb 入门教程"})
	sources := []DocumentSource{&fakeDocSource{docs: docs}}
	tokens := []string{"metaweb", "教程"}
	weights := weightsForProtocol(tokenIDFWeights(sources, tokens), "simplenote", tokens)

	common := metawebdoc.Document{ProtocolKey: "simplenote", Title: "metaweb weekly"}
	rare := metawebdoc.Document{ProtocolKey: "simplenote", Title: "metaweb 入门教程"}
	// No doc contains the whole phrase, so no exact-phrase boost applies.
	scoreCommon := scoreDocument(&common, tokens, weights, "metaweb 教程")
	scoreRare := scoreDocument(&rare, tokens, weights, "metaweb 教程")
	// Common-only: 5 × (4 × ln2/ln11) × 1.2 prior → 6.
	// Rare: (that + 5×2 title hit of 教程) × 1.2 → 18.
	if scoreCommon != 6 || scoreRare != 18 {
		t.Fatalf("scores = %d, %d; want 6, 18", scoreCommon, scoreRare)
	}
	if scoreRare <= scoreCommon {
		t.Fatalf("rare-token doc must outrank corpus-common-only docs")
	}
}

func TestTokenIDFWeights_PerProtocolKey(t *testing.T) {
	// "skill" is ubiquitous within metabot-skill (4 of 4) but rare within
	// simplenote (1 of 4): down-weighted only for metabot-skill.
	docs := []metawebdoc.Document{
		{ProtocolKey: "metabot-skill", Title: "skill one"},
		{ProtocolKey: "metabot-skill", Title: "skill two"},
		{ProtocolKey: "metabot-skill", Title: "skill three"},
		{ProtocolKey: "metabot-skill", Title: "skill four"},
		{ProtocolKey: "simplenote", Title: "skill notes"},
		{ProtocolKey: "simplenote", Title: "cooking notes"},
		{ProtocolKey: "simplenote", Title: "travel notes"},
		{ProtocolKey: "simplenote", Title: "reading notes"},
	}
	weights := tokenIDFWeights([]DocumentSource{&fakeDocSource{docs: docs}}, []string{"skill"})
	if got, want := weights["metabot-skill"][0], smoothIDFWeight("skill", 4, 4); !almostEqual(got, want) {
		t.Fatalf("metabot-skill weight = %v, want %v (df=N)", got, want)
	}
	if got, want := weights["simplenote"][0], 4.0; got != want {
		t.Fatalf("simplenote weight = %v, want %v (df=1, full)", got, want)
	}
}

func TestTokenIDFWeights_CrossKeyIsolation(t *testing.T) {
	// A token common in simplebuzz must not affect metabot-skill scoring,
	// even though both ride the same source aggregator.
	docs := []metawebdoc.Document{
		{ProtocolKey: "simplebuzz", Title: "metaid buzz one"},
		{ProtocolKey: "simplebuzz", Title: "metaid buzz two"},
		{ProtocolKey: "metabot-skill", Title: "ping tool"},
		{ProtocolKey: "metabot-skill", Title: "image tool"},
		{ProtocolKey: "metabot-skill", Title: "video tool"},
		{ProtocolKey: "metabot-skill", Title: "voice tool"},
	}
	weights := tokenIDFWeights([]DocumentSource{&fakeDocSource{docs: docs}}, []string{"metaid"})
	// simplebuzz: df 2/2 → ln2/ln3 scaled.
	if got, want := weights["simplebuzz"][0], smoothIDFWeight("metaid", 2, 2); !almostEqual(got, want) {
		t.Fatalf("simplebuzz weight = %v, want %v", got, want)
	}
	// metabot-skill: df 0/4 → full weight despite the simplebuzz frequency.
	if got, want := weights["metabot-skill"][0], 4.0; got != want {
		t.Fatalf("metabot-skill weight = %v, want %v (full)", got, want)
	}
}

func TestTokenIDFWeights_MergedAcrossSources(t *testing.T) {
	// Document frequency is per protocol key, accumulated across sources:
	// the same protocol key served by two sources shares one namespace.
	half := []metawebdoc.Document{
		{ProtocolKey: "simplenote", Title: "metaid doc one"},
		{ProtocolKey: "simplenote", Title: "metaid doc two"},
		{ProtocolKey: "simplenote", Title: "other doc"},
	}
	sources := []DocumentSource{&fakeDocSource{docs: half}, &fakeDocSource{docs: half}}
	weights := tokenIDFWeights(sources, []string{"metaid"})
	// df = 4 of 6 → ln(1+6/4)/ln(7) scaled.
	if got, want := weights["simplenote"][0], smoothIDFWeight("metaid", 6, 4); !almostEqual(got, want) {
		t.Fatalf("metaid weight = %v, want %v", got, want)
	}
}

func TestWeightsForProtocol_UnknownKeyDefaultsFull(t *testing.T) {
	// A protocol key without stats (e.g. entered between the df scan and
	// the scoring pass) falls back to full weights.
	weights := weightsForProtocol(map[string][]float64{}, "metaapp", []string{"ab", "abcde"})
	if len(weights) != 2 || weights[0] != 2.0 || weights[1] != 4.0 {
		t.Fatalf("weights = %v, want [2 4]", weights)
	}
}

func TestDocumentMatchesAny_WordBoundary(t *testing.T) {
	// The newest-sort admission filter uses the same boundary hit test.
	doc := &metawebdoc.Document{Title: "this history lesson"}
	if documentMatchesAny(doc, []string{"is"}) {
		t.Fatalf("stopword-shaped substring admitted")
	}
	if !documentMatchesAny(doc, []string{"history"}) {
		t.Fatalf("whole-word match missed")
	}
}

func TestProtocolPrior(t *testing.T) {
	cases := map[string]float64{
		"simplenote":    1.2,
		"metaprotocol":  1.2,
		"simplebuzz":    0.9,
		"metaapp":       1.0,
		"metabot-skill": 1.0,
		"skill-service": 1.0,
		"unknown-proto": 1.0, // unknown keys default to 1.0
		"":              1.0,
	}
	for key, want := range cases {
		if got := protocolPrior(key); got != want {
			t.Errorf("protocolPrior(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestScoreDocument_ProtocolPriorAppliedToFinalScore(t *testing.T) {
	// Same raw title hit (weight 2, field 5 → raw 10) scales by the prior:
	// simplenote ×1.2 → 12, simplebuzz ×0.9 → 9, metaprotocol ×1.2 → 12.
	tokens := []string{"knotwork"}
	weights := []float64{2}
	cases := []struct {
		protocol string
		want     int
	}{
		{"simplenote", 12},
		{"metaprotocol", 12},
		{"simplebuzz", 9},
		{"metabot-skill", 10},
	}
	for _, tc := range cases {
		doc := metawebdoc.Document{ProtocolKey: tc.protocol, Title: "knotwork notes"}
		if got := scoreDocument(&doc, tokens, weights, "q"); got != tc.want {
			t.Errorf("%s: score = %d, want %d", tc.protocol, got, tc.want)
		}
	}

	// The prior never resurrects a zero-score document.
	doc := metawebdoc.Document{ProtocolKey: "simplenote", Title: "unrelated"}
	if got := scoreDocument(&doc, tokens, weights, "q"); got != 0 {
		t.Fatalf("zero score with prior = %d, want 0", got)
	}
}
