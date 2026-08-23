package metaweb

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

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
		if got := scoreDocument(&doc, tokens, "alpha other"); got != tc.want {
			t.Errorf("%s: score = %d, want %d", tc.name, got, tc.want)
		}
	}

	// A token hitting several fields counts once per field.
	doc := metawebdoc.Document{Title: "alpha", Tags: []string{"alpha"}, Summary: "alpha", ContentExcerpt: "alpha"}
	if got, want := scoreDocument(&doc, tokens, "q"), (5+3+2+1)*4; got != want {
		t.Errorf("all-fields: score = %d, want %d", got, want)
	}
}

func TestScoreDocument_TokenWeightCapsAtFourRunes(t *testing.T) {
	doc := metawebdoc.Document{Title: "ab"}
	if got, want := scoreDocument(&doc, []string{"ab"}, "q"), 5*2; got != want {
		t.Errorf("2-rune token: score = %d, want %d", got, want)
	}
	doc = metawebdoc.Document{Title: "abcdefgh"}
	if got, want := scoreDocument(&doc, []string{"abcdefgh"}, "q"), 5*4; got != want {
		t.Errorf("8-rune token: score = %d, want %d (weight capped at 4)", got, want)
	}
}

func TestScoreDocument_ExactPhraseBoost(t *testing.T) {
	// Whole trimmed q substring of the title → flat +100.
	doc := metawebdoc.Document{Title: "The IDBots Beginner Tutorial"}
	score := scoreDocument(&doc, tokenizeQuery("IDBots Beginner Tutorial"), "IDBots Beginner Tutorial")
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
	tokens := tokenizeQuery("beginner tutorial")
	score = scoreDocument(&doc, tokens, "beginner tutorial")
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
	if got := scoreDocument(&doc, tokenizeQuery("nothing matches"), "nothing matches"); got != 0 {
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
