package userinfo

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// BenchmarkMetaIDListKeyword measures the O(N) scan cost of /api/metaid/list
// against synthetic in-memory corpora of varying sizes, so we can reason
// about the user scale the current architecture supports. Docs are built
// through the real buildMetaIDSearchDoc path with realistic field sizes
// (~750 bytes of CJK profile text per user); ~10% of docs match the AND
// keyword so scoring + sort of the match set is exercised too.
func BenchmarkMetaIDListKeyword(b *testing.B) {
	for _, n := range []int{10_000, 100_000, 1_000_000} {
		agg := benchmarkSearchAggregator(b, n)
		runtime.GC()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		b.Run(fmt.Sprintf("users=%d", n), func(b *testing.B) {
			b.ReportMetric(float64(mem.HeapAlloc)/float64(n)/1024, "heap-KB/doc")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result, err := agg.ListMetaIDs(MetaIDListParams{Keyword: "开朗 助手", Size: 10})
				if err != nil {
					b.Fatal(err)
				}
				if len(result.Items) == 0 {
					b.Fatal("expected matches")
				}
			}
		})
	}
}

func benchmarkSearchAggregator(b *testing.B, n int) *Aggregator {
	b.Helper()
	agg := &Aggregator{}
	agg.searchDocs = make(map[string]*metaIDSearchDoc, n)
	bio := strings.Repeat("记录链上生活，分享日常。", 8)          // ~160 bytes CJK
	persona := `{"role":"链上助手","style":"温和耐心","intro":"` + strings.Repeat("帮助用户解决问题。", 12) + `"}` // ~250 bytes CJK
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("meta%08d", i)
		profile := &UserProfile{
			MetaID:       key,
			GlobalMetaID: fmt.Sprintf("id%039d", i),
			Address:      fmt.Sprintf("1Addr%032d", i),
			ChainName:    "mvc",
			Name:         fmt.Sprintf("测试用户%06d", i),
			AvatarId:     fmt.Sprintf("%064x", i) + "i0",
			Bio:          bio,
			Persona:      persona,
			ChatSkills:   `["聊天","翻译","画画"]`,
			LLM:          `{"provider":"openai","model":"gpt-4"}`,
		}
		if i%10 == 0 {
			profile.Bio = bio + "性格开朗"
			profile.ChatSkills = `["聊天","助手","翻译"]`
		}
		if doc := buildMetaIDSearchDoc(profile); doc != nil {
			agg.searchDocs[key] = doc
		}
	}
	return agg
}
