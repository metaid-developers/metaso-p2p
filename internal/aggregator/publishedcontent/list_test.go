package publishedcontent

import "testing"

func TestListByIdentityCrossChainReadsSixReturnsFive(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	for i := 1; i <= 6; i++ {
		chainName := "mvc"
		if i%2 == 0 {
			chainName = "opcat"
		}
		mustProcess(t, agg, makeContentPin(contentPinOpts{
			PinId:        "buzz-" + string(rune('0'+i)) + ":i0",
			Operation:    OperationCreate,
			ChainName:    chainName,
			Timestamp:    int64(i * 1000),
			ContentBody:  []byte("buzz item"),
			GlobalMetaId: "gid-cross",
			MetaId:       "meta-cross",
			Address:      "addr-cross",
		}))
	}

	result, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-cross",
		Size:                  5,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 5 {
		t.Fatalf("len: got %d want 5", len(result.Items))
	}
	if !result.HasMore {
		t.Fatal("HasMore should be true after reading size+1")
	}
	want := []string{"buzz-6:i0", "buzz-5:i0", "buzz-4:i0", "buzz-3:i0", "buzz-2:i0"}
	for i, item := range result.Items {
		if item.SourcePinId != want[i] {
			t.Fatalf("item[%d].SourcePinId: got %q want %q", i, item.SourcePinId, want[i])
		}
	}
}

func TestBuzzModifyDoesNotChangeCreatedSort(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()

	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "buzz-a:i0",
		Operation:    OperationCreate,
		Timestamp:    1000,
		ContentBody:  []byte("old A"),
		GlobalMetaId: "gid-sort",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "buzz-b:i0",
		Operation:    OperationCreate,
		Timestamp:    2000,
		ContentBody:  []byte("B"),
		GlobalMetaId: "gid-sort",
	}))
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:        "buzz-a-mod:i0",
		Path:         PathSimpleBuzz + "@buzz-a:i0",
		Operation:    OperationModify,
		Timestamp:    3000,
		ContentBody:  []byte("new A"),
		GlobalMetaId: "gid-sort",
	}))

	result, err := agg.List(ListParams{
		ProtocolPath:          PathSimpleBuzz,
		PublisherGlobalMetaId: "gid-sort",
		Size:                  2,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len: got %d want 2", len(result.Items))
	}
	if result.Items[0].SourcePinId != "buzz-b:i0" {
		t.Fatalf("first item should remain newer create buzz-b, got %q", result.Items[0].SourcePinId)
	}
	if result.Items[1].SourcePinId != "buzz-a:i0" {
		t.Fatalf("second item should be original buzz-a create sort slot, got %q", result.Items[1].SourcePinId)
	}
	if result.Items[1].CurrentPinId != "buzz-a-mod:i0" {
		t.Fatalf("modified buzz current pin: got %q", result.Items[1].CurrentPinId)
	}
	if result.Items[1].PayloadText != "new A" {
		t.Fatalf("modified buzz payload: got %q", result.Items[1].PayloadText)
	}
}
