package metaweb

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// BenchmarkMetaWebSearch measures the full /api/metaweb/search handler path
// (per-request IDF corpus scan + scoring + content-level dedupe + page
// assembly) against synthetic in-memory corpora, so the Q3 inverted-index
// checkpoint (docs/specs/2026-09-02-metaweb-search-quality.md) can reason
// about the corpus size the current architecture supports. Docs carry
// realistic field sizes (~100 runes of CJK excerpt text); ~10% match the
// query, and ~10% of matches are same-publisher duplicate reposts so the
// dedupe pass is exercised too.
func BenchmarkMetaWebSearch(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		agg := &Aggregator{}
		if err := agg.Init(nil, nil); err != nil {
			b.Fatalf("Init: %v", err)
		}
		agg.SetDocumentSources(&fakeDocSource{docs: benchmarkCorpus(n)})
		router := newTestRouter(agg)
		runtime.GC()
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		b.Run(fmt.Sprintf("docs=%d", n), func(b *testing.B) {
			b.ReportMetric(float64(mem.HeapAlloc)/float64(n)/1024, "heap-KB/doc")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				req := httptest.NewRequest(http.MethodGet, "/api/metaweb/search?q=skill%20教程", nil)
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, req)
				if recorder.Code != http.StatusOK {
					b.Fatalf("status = %d", recorder.Code)
				}
			}
		})
	}
}

// benchmarkCorpus builds n synthetic search documents across the six protocol
// keys: 90% filler carrying the corpus-common token "metaweb", 10% matching
// "skill 教程", every tenth match a same-publisher duplicate of the previous
// match (fresh pin id, identical title/body) to exercise dedupe.
func benchmarkCorpus(n int) []metawebdoc.Document {
	keys := []string{"simplenote", "simplebuzz", "metaapp", "metabot-skill", "skill-service", "metaprotocol"}
	filler := "metaweb 动态 " + strings.Repeat("记录链上生活片段。", 10)
	match := "skill 教程 " + strings.Repeat("手把手教你完成一次链上发布。", 6)
	docs := make([]metawebdoc.Document, 0, n)
	var lastMatchIdx int
	for i := 0; i < n; i++ {
		doc := metawebdoc.Document{
			ProtocolKey:           keys[i%len(keys)],
			SourcePinId:           fmt.Sprintf("%064x:i0", i),
			CurrentPinId:          fmt.Sprintf("%064x:i0", i),
			ChainName:             "mvc",
			Title:                 fmt.Sprintf("metaweb 动态 %d", i),
			Summary:               filler[:120],
			ContentExcerpt:        filler,
			PublisherGlobalMetaId: fmt.Sprintf("idq1%019d", i),
			CreatedAt:             1755000000 + int64(i),
		}
		switch {
		case i%10 != 0:
			// filler doc
		case i%100 == 10 && i > 0:
			// duplicate repost of the previous matching doc
			prev := docs[lastMatchIdx]
			doc.Title = prev.Title
			doc.Summary = prev.Summary
			doc.ContentExcerpt = prev.ContentExcerpt
			doc.PublisherGlobalMetaId = prev.PublisherGlobalMetaId
		default:
			doc.Title = fmt.Sprintf("skill 教程 指南 %d", i)
			doc.Summary = match[:120]
			doc.ContentExcerpt = match
			lastMatchIdx = i
		}
		docs = append(docs, doc)
	}
	return docs
}
