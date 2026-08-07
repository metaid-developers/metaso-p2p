package socialcontent

import (
	"fmt"
	"testing"
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

func TestFeedHotKeepsOffsetCursorContract(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	for i := 1; i <= 3; i++ {
		pin := testPin(fmt.Sprintf("hot-post-%d:i0", i), PathSimpleBuzz, OperationCreate, "mvc", int64(100+i), []byte(`{"text":"hot"}`))
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("post %d: %v", i, err)
		}
	}
	page, err := agg.List(FeedParams{Size: 2, Sort: SortHot})
	if err != nil {
		t.Fatalf("List hot: %v", err)
	}
	if len(page.Items) != 2 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("hot page = %+v", page)
	}
	next, err := agg.List(FeedParams{Size: 2, Sort: SortHot, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("List hot page 2: %v", err)
	}
	if len(next.Items) != 1 || next.HasMore {
		t.Fatalf("hot page 2 = %+v", next)
	}
}
