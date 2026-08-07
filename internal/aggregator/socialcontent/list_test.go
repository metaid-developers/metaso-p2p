package socialcontent

import (
	"fmt"
	"testing"
	"time"
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
