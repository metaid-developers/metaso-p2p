package socialcontent

import (
	"fmt"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

func TestFeedPaginationUsesBoundedNewestCursor(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	for i := 1; i <= 3; i++ {
		pin := testPin(fmt.Sprintf("page-post-%d:i0", i), PathSimpleBuzz, OperationCreate, "mvc", int64(100+i), []byte(fmt.Sprintf(`{"text":"post %d"}`, i)))
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
	}

	first, err := agg.List(FeedParams{Size: 2})
	if err != nil {
		t.Fatalf("List page 1: %v", err)
	}
	if len(first.Items) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("page 1 = %+v", first)
	}
	if first.Items[0].SourcePinId != "page-post-3:i0" || first.Items[1].SourcePinId != "page-post-2:i0" {
		t.Fatalf("page 1 order = %s, %s", first.Items[0].SourcePinId, first.Items[1].SourcePinId)
	}

	second, err := agg.List(FeedParams{Size: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("List page 2: %v", err)
	}
	if len(second.Items) != 1 || second.HasMore || second.NextCursor != "" {
		t.Fatalf("page 2 = %+v", second)
	}
	if second.Items[0].SourcePinId != "page-post-1:i0" {
		t.Fatalf("page 2 item = %+v", second.Items[0])
	}
}

func TestFeedHotReturnsBoundedTopN(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	now := time.Now().Unix()
	for i := 1; i <= 3; i++ {
		pin := testPin(fmt.Sprintf("hot-post-%d:i0", i), PathSimpleBuzz, OperationCreate, "mvc", now-int64(4-i), []byte(`{"text":"hot"}`))
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
	}
	page, err := agg.List(FeedParams{Size: 2, Sort: SortHot})
	if err != nil {
		t.Fatalf("List hot: %v", err)
	}
	if len(page.Items) != 2 || page.HasMore || page.NextCursor != "" {
		t.Fatalf("hot page = %+v", page)
	}
	if page.Items[0].SourcePinId != "hot-post-3:i0" {
		t.Fatalf("hot top item = %+v", page.Items[0])
	}
}

func TestFeedHotIgnoresPostsOutsideRecentWindow(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	now := time.Now().Unix()
	old := testPin("hot-old:i0", PathSimpleBuzz, OperationCreate, "mvc", now-7*24*3600, []byte(`{"text":"old hot"}`))
	if _, err := agg.HandleBlockPin(old); err != nil {
		t.Fatalf("old post: %v", err)
	}
	recent := testPin("hot-recent:i0", PathSimpleBuzz, OperationCreate, "mvc", now-3600, []byte(`{"text":"recent hot"}`))
	if _, err := agg.HandleBlockPin(recent); err != nil {
		t.Fatalf("recent post: %v", err)
	}
	page, err := agg.List(FeedParams{Size: 10, Sort: SortHot})
	if err != nil {
		t.Fatalf("List hot: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].SourcePinId != "hot-recent:i0" {
		t.Fatalf("hot window items = %+v", page.Items)
	}
}

func TestFeedHotOrdersByEngagement(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	now := time.Now().Unix()
	postA := testPin("engage-a:i0", PathSimpleBuzz, OperationCreate, "mvc", now-3600, []byte(`{"text":"A"}`))
	postB := testPin("engage-b:i0", PathSimpleBuzz, OperationCreate, "mvc", now-1800, []byte(`{"text":"B"}`))
	for _, pin := range []*aggregator.PinInscription{postA, postB} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("post: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		comment := testPin(fmt.Sprintf("engage-a-comment-%d:i0", i), PathPayComment, OperationCreate, "mvc", now-int64(3600-i), mustJSON(t, map[string]any{"commentTo": postA.Id, "content": "c"}))
		if _, err := agg.HandleBlockPin(comment); err != nil {
			t.Fatalf("comment a: %v", err)
		}
	}
	commentB := testPin("engage-b-comment:i0", PathPayComment, OperationCreate, "mvc", now-1700, mustJSON(t, map[string]any{"commentTo": postB.Id, "content": "c"}))
	if _, err := agg.HandleBlockPin(commentB); err != nil {
		t.Fatalf("comment b: %v", err)
	}

	page, err := agg.List(FeedParams{Size: 10, Sort: SortHot})
	if err != nil {
		t.Fatalf("List hot: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("hot items = %+v", page.Items)
	}
	if page.Items[0].SourcePinId != "engage-a:i0" || page.Items[0].CommentCount != 2 {
		t.Fatalf("hot top should be engage-a with 2 comments: %+v", page.Items[0])
	}
	if page.Items[1].SourcePinId != "engage-b:i0" {
		t.Fatalf("hot second = %+v", page.Items[1])
	}
}

func TestFeedKeywordsAndPublishersOR(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	now := time.Now().Unix()
	posts := []*aggregator.PinInscription{
		testPin("kw-ai:i0", PathSimpleBuzz, OperationCreate, "mvc", now-100, []byte(`{"text":"AI agent post"}`)),
		testPin("kw-mvc:i0", PathSimpleBuzz, OperationCreate, "mvc", now-200, []byte(`{"text":"MVC protocol post"}`)),
		testPin("kw-other:i0", PathSimpleBuzz, OperationCreate, "mvc", now-300, []byte(`{"text":"unrelated"}`)),
	}
	for _, pin := range posts {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("post: %v", err)
		}
	}

	page, err := agg.List(FeedParams{Size: 10, Keywords: []string{"AI", "MVC"}})
	if err != nil {
		t.Fatalf("List keywords: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("keywords OR items = %+v", page.Items)
	}
	seen := map[string]bool{}
	for _, item := range page.Items {
		seen[item.SourcePinId] = true
	}
	if !seen["kw-ai:i0"] || !seen["kw-mvc:i0"] || seen["kw-other:i0"] {
		t.Fatalf("keywords OR mismatch: %+v", seen)
	}

	byAuthors, err := agg.List(FeedParams{Size: 10, Publishers: []string{"idq-author"}})
	if err != nil {
		t.Fatalf("List publishers: %v", err)
	}
	if len(byAuthors.Items) != 3 {
		t.Fatalf("publishers items = %+v", byAuthors.Items)
	}
}

func TestFeedScopeFollowing(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	agg.SetFollowLister(fakeFollowLister{ids: []string{"idq-followed-a", "idq-followed-b"}})
	now := time.Now().Unix()
	makePost := func(id, author string) *aggregator.PinInscription {
		pin := testPin(id, PathSimpleBuzz, OperationCreate, "mvc", now, []byte(`{"text":"followed"}`))
		pin.GlobalMetaId = author
		pin.MetaId = author
		pin.Address = author
		pin.CreateMetaId = author
		pin.CreateAddress = author
		return pin
	}
	for _, pin := range []*aggregator.PinInscription{
		makePost("follow-a:i0", "idq-followed-a"),
		makePost("follow-b:i0", "idq-followed-b"),
		makePost("follow-c:i0", "idq-other"),
	} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("post: %v", err)
		}
	}

	page, err := agg.List(FeedParams{Size: 10, Scope: "following", User: "idq-me"})
	if err != nil {
		t.Fatalf("List following: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("following items = %+v", page.Items)
	}
	for _, item := range page.Items {
		if item.SourcePinId == "follow-c:i0" {
			t.Fatalf("following feed returned non-followed post: %+v", item)
		}
	}
}

func TestQuoteCountTracksQuotePinPosts(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	now := time.Now().Unix()
	target := testPin("quote-target:i0", PathSimpleBuzz, OperationCreate, "mvc", now-3600, []byte(`{"text":"original"}`))
	if _, err := agg.HandleBlockPin(target); err != nil {
		t.Fatalf("target post: %v", err)
	}
	quote := testPin("quote-1:i0", PathSimpleBuzz, OperationCreate, "mvc", now-1800, []byte(`{"text":"quote","quotePin":"quote-target:i0"}`))
	if _, err := agg.HandleBlockPin(quote); err != nil {
		t.Fatalf("quote post: %v", err)
	}
	missing := testPin("quote-missing:i0", PathSimpleBuzz, OperationCreate, "mvc", now-1700, []byte(`{"text":"quote","quotePin":"no-such-pin:i0"}`))
	if _, err := agg.HandleBlockPin(missing); err != nil {
		t.Fatalf("quote of missing target: %v", err)
	}

	detail, err := agg.FindPost("quote-target:i0", "mvc")
	if err != nil || detail == nil {
		t.Fatalf("FindPost: %+v err=%v", detail, err)
	}
	if detail.QuoteCount != 1 {
		t.Fatalf("quoteCount = %d, want 1", detail.QuoteCount)
	}
	page, err := agg.List(FeedParams{Size: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, item := range page.Items {
		if item.SourcePinId == "quote-target:i0" && item.QuoteCount != 1 {
			t.Fatalf("feed quoteCount = %d, want 1", item.QuoteCount)
		}
	}
}

type fakeFollowLister struct {
	ids []string
}

func (f fakeFollowLister) ListFollowing(string) ([]string, error) {
	return f.ids, nil
}
