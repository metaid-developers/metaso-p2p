package botsearch

import (
	"reflect"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/groupchat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
)

func TestTokenizeCJKBigrams(t *testing.T) {
	// The contract's recall example: query 占卜 塔罗 命运 must produce tokens
	// that substring-match the bio 占卜塔罗牌.
	tokens := tokenize("占卜 塔罗 命运", nil)
	want := []string{"占卜", "塔罗", "命运"}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}

	// A 3-char segment yields itself plus both bigrams.
	tokens = tokenize("塔罗牌", nil)
	want = []string{"塔罗牌", "塔罗", "罗牌"}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}

	// A single CJK char yields itself.
	tokens = tokenize("占", nil)
	if !reflect.DeepEqual(tokens, []string{"占"}) {
		t.Fatalf("tokens = %v, want [占]", tokens)
	}

	// A mixed segment yields the whole segment plus its CJK bigrams.
	tokens = tokenize("塔罗bot", nil)
	want = []string{"塔罗bot", "塔罗"}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
}

func TestTokenizeLatinAndDedupe(t *testing.T) {
	tokens := tokenize("Legal contract LEGAL", []string{"Contract Law"})
	want := []string{"legal", "contract", "law"}
	if !reflect.DeepEqual(tokens, want) {
		t.Fatalf("tokens = %v, want %v", tokens, want)
	}
}

func TestScoreCandidateWeightedPartialMatch(t *testing.T) {
	// Contract example: query 占卜 塔罗 命运 must recall a bio of 占卜塔罗牌
	// (partial, not hard AND — 命运 does not hit).
	profile := userinfo.BotSearchProfile{Name: "小占", Bio: "占卜塔罗牌"}
	result := scoreCandidate(profile, nil, tokenize("占卜 塔罗 命运", nil), "", "占卜 塔罗 命运")
	// 占卜 weight 2 + 塔罗 weight 2, both bio hits at coefficient 1.
	if result.score != 4 {
		t.Fatalf("score = %v, want 4 (reasons=%+v)", result.score, result.reasons)
	}
	assertHasReason(t, result.reasons, matchReason{Field: "bio", Token: "占卜", Weight: 2})
	assertHasReason(t, result.reasons, matchReason{Field: "bio", Token: "塔罗", Weight: 2})
}

func TestScoreCandidateFieldWeights(t *testing.T) {
	profile := userinfo.BotSearchProfile{
		Name:       "Counsel-bot",
		Bio:        "商业合同与合规审查",
		Role:       "法律顾问",
		ChatSkills: []string{"legal-review", "contract"},
	}
	history := []groupchat.GroupHistoryItem{{GroupId: "g1:i0", Title: "合同审查协作"}}
	tokens := tokenize("counsel 合同 legal", nil)
	result := scoreCandidate(profile, history, tokens, "", "counsel 合同 legal")

	// counsel: name hit, weight min(7,4)=4 → 4*4 = 16
	// 合同: bio hit weight 2 → 2, groupTaskTitle hit weight 2 → 2
	// legal: chatSkills hit weight min(5,4)=4 → 2*4 = 8
	if result.score != 16+2+2+8 {
		t.Fatalf("score = %v, want 28 (reasons=%+v)", result.score, result.reasons)
	}
	assertHasReason(t, result.reasons, matchReason{Field: "name", Token: "counsel", Weight: 16})
	assertHasReason(t, result.reasons, matchReason{Field: "chatSkills", Token: "legal", Weight: 8})
	assertHasReason(t, result.reasons, matchReason{Field: "bio", Token: "合同", Weight: 2})
	assertHasReason(t, result.reasons, matchReason{Field: "groupTaskTitle", Token: "合同", Weight: 2})

	// Reasons are ordered by weight descending.
	for i := 1; i < len(result.reasons); i++ {
		if result.reasons[i-1].Weight < result.reasons[i].Weight {
			t.Fatalf("reasons not weight-desc: %+v", result.reasons)
		}
	}
}

func TestScoreCandidateRoleHintSynonym(t *testing.T) {
	profile := userinfo.BotSearchProfile{Name: "Counsel-bot", Role: "法律顾问", Bio: "商业合同与合规审查"}
	result := scoreCandidate(profile, nil, nil, "domain", "")
	if result.score != 0.5 {
		t.Fatalf("score = %v, want 0.5 flat roleHint hit (reasons=%+v)", result.score, result.reasons)
	}
	assertHasReason(t, result.reasons, matchReason{Field: "roleHint", Token: "顾问", Weight: 0.5})

	// No synonym hit → no score.
	other := scoreCandidate(profile, nil, nil, "design", "")
	if other.score != 0 {
		t.Fatalf("score = %v, want 0 for non-matching roleHint", other.score)
	}
}

func TestScoreCandidateExactNameBoost(t *testing.T) {
	exact := userinfo.BotSearchProfile{Name: "Echo"}
	fuzzy := userinfo.BotSearchProfile{Name: "Echo Farm", Bio: "echo echo echo"}
	tokens := tokenize("Echo", nil)

	exactResult := scoreCandidate(exact, nil, tokens, "", "Echo")
	fuzzyResult := scoreCandidate(fuzzy, nil, tokens, "", "Echo")

	if fuzzyResult.score <= 0 {
		t.Fatalf("fuzzy score = %v, want > 0", fuzzyResult.score)
	}
	if exactResult.score <= fuzzyResult.score {
		t.Fatalf("exact name score %v must outrank fuzzy-only %v", exactResult.score, fuzzyResult.score)
	}
	if exactResult.score < exactNameBoost {
		t.Fatalf("exact name score %v missing the +%v boost", exactResult.score, exactNameBoost)
	}
}

func TestSearchCursorRoundTrip(t *testing.T) {
	cursor := encodeSearchCursor(40)
	offset, err := decodeSearchCursor(cursor)
	if err != nil || offset != 40 {
		t.Fatalf("round trip = %d, %v; want 40, nil", offset, err)
	}
	if offset, err := decodeSearchCursor(""); err != nil || offset != 0 {
		t.Fatalf("empty cursor = %d, %v; want 0, nil", offset, err)
	}
	for _, bad := range []string{"!!!not-base64!!!", "aGVsbG8" /* valid b64, not JSON */} {
		if _, err := decodeSearchCursor(bad); err == nil {
			t.Fatalf("cursor %q should be rejected", bad)
		}
	}
}

func assertHasReason(t *testing.T, reasons []matchReason, want matchReason) {
	t.Helper()
	for _, reason := range reasons {
		if reason == want {
			return
		}
	}
	t.Fatalf("missing reason %+v in %+v", want, reasons)
}
