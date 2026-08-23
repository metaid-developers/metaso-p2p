package metaweb

import (
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
	// CJK tokens are never stopwords.
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

func TestTokenIDFWeights_HighFrequencyHalved(t *testing.T) {
	// 10 simplenote docs: "common" in 4 (40% > 30%) → halved; "rare" in 3
	// (30%, the threshold is strict) → full weight.
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
	if got, want := weights[0], 2.0; got != want {
		t.Fatalf("common weight = %v, want %v (halved from 4)", got, want)
	}
	if got, want := weights[1], 4.0; got != want {
		t.Fatalf("rare weight = %v, want %v (full)", got, want)
	}

	// The halved weight halves the resulting score: title hit of "common".
	doc := metawebdoc.Document{Title: "common term"}
	if got, want := scoreDocument(&doc, []string{"common"}, weights[:1], "q"), 5*2; got != want {
		t.Fatalf("halved score = %d, want %d", got, want)
	}
	if got, want := scoreDocument(&doc, []string{"common"}, []float64{4}, "q"), 5*4; got != want {
		t.Fatalf("full score = %d, want %d", got, want)
	}
}

func TestTokenIDFWeights_PerProtocolKey(t *testing.T) {
	// "skill" is ubiquitous within metabot-skill (4 of 4 = 100% > 30%) but
	// rare within simplenote (1 of 4 = 25%): halved only for metabot-skill.
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
	if got, want := weights["metabot-skill"][0], 2.0; got != want {
		t.Fatalf("metabot-skill weight = %v, want %v (halved)", got, want)
	}
	if got, want := weights["simplenote"][0], 4.0; got != want {
		t.Fatalf("simplenote weight = %v, want %v (full)", got, want)
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
	// simplebuzz: df 2/2 = 100% > 30% → halved.
	if got, want := weights["simplebuzz"][0], 2.0; got != want {
		t.Fatalf("simplebuzz weight = %v, want %v (halved)", got, want)
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
	// df = 4 of 6 (67% > 30%) → halved.
	if got, want := weights["simplenote"][0], 2.0; got != want {
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
