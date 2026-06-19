package social

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type fakeTargetLookup struct {
	byMetaId       map[string]*TargetRef
	byGlobalMetaId map[string]*TargetRef
	byAddress      map[string]*TargetRef
	calls          []string
}

func (f *fakeTargetLookup) LookupByMetaId(metaId string) (*TargetRef, error) {
	f.calls = append(f.calls, "metaid:"+metaId)
	return f.byMetaId[metaId], nil
}

func (f *fakeTargetLookup) LookupByGlobalMetaId(globalMetaId string) (*TargetRef, error) {
	f.calls = append(f.calls, "global:"+globalMetaId)
	return f.byGlobalMetaId[globalMetaId], nil
}

func (f *fakeTargetLookup) LookupByAddress(address string) (*TargetRef, error) {
	f.calls = append(f.calls, "address:"+address)
	return f.byAddress[address], nil
}

func newTargetRef(metaId, globalMetaId, address string) *TargetRef {
	return &TargetRef{
		MetaId:       metaId,
		GlobalMetaId: globalMetaId,
		Address:      address,
	}
}

func newTestSocialAggregator(t *testing.T) (*Aggregator, *storage.PebbleStore) {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	cacheProvider := cache.New(store)
	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg, store
}

func followPin(id, followerGlobalMetaId, followerMetaId, followerAddress, targetRef string, ts int64) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:            id,
		Path:          "/follow",
		Operation:     "create",
		GlobalMetaId:  followerGlobalMetaId,
		MetaId:        followerMetaId,
		CreateMetaId:  followerMetaId,
		Address:       followerAddress,
		CreateAddress: followerAddress,
		ChainName:     "mvc",
		Timestamp:     ts,
		ContentBody:   []byte(targetRef),
	}
}

func revokePin(id, originalID string, ts int64) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:         id,
		Path:       "/follow@" + originalID,
		Operation:  "revoke",
		OriginalId: originalID,
		ChainName:  "mvc",
		Timestamp:  ts,
	}
}

func TestAggregatorNameAndEmptyLookup(t *testing.T) {
	agg := &Aggregator{}
	if got := agg.Name(); got != "social" {
		t.Fatalf("Name() = %q, want social", got)
	}

	target, err := agg.lookupTargetRef("anything")
	if err != nil {
		t.Fatalf("lookupTargetRef returned error: %v", err)
	}
	if target != nil {
		t.Fatalf("lookupTargetRef returned %+v, want nil", target)
	}
}

func TestLookupSubjectAcceptsGlobalMetaIdMetaIdAndAddress(t *testing.T) {
	t.Run("global meta id", func(t *testing.T) {
		lookup := &fakeTargetLookup{
			byGlobalMetaId: map[string]*TargetRef{
				"gid-1": newTargetRef("meta-1", "gid-1", "addr-1"),
			},
		}
		agg := &Aggregator{}
		agg.SetProfileLookup(lookup)

		target, err := agg.lookupTargetRef("gid-1")
		if err != nil {
			t.Fatalf("lookupTargetRef returned error: %v", err)
		}
		if target == nil || target.GlobalMetaId != "gid-1" {
			t.Fatalf("lookupTargetRef = %+v, want globalMetaId gid-1", target)
		}
		assertCalls(t, lookup.calls, []string{"global:gid-1"})
	})

	t.Run("meta id", func(t *testing.T) {
		lookup := &fakeTargetLookup{
			byMetaId: map[string]*TargetRef{
				"meta-2": newTargetRef("meta-2", "gid-2", "addr-2"),
			},
		}
		agg := &Aggregator{}
		agg.SetProfileLookup(lookup)

		target, err := agg.lookupTargetRef("meta-2")
		if err != nil {
			t.Fatalf("lookupTargetRef returned error: %v", err)
		}
		if target == nil || target.MetaId != "meta-2" {
			t.Fatalf("lookupTargetRef = %+v, want metaId meta-2", target)
		}
		assertCalls(t, lookup.calls, []string{"global:meta-2", "metaid:meta-2"})
	})

	t.Run("address", func(t *testing.T) {
		lookup := &fakeTargetLookup{
			byAddress: map[string]*TargetRef{
				"addr-3": newTargetRef("meta-3", "gid-3", "addr-3"),
			},
		}
		agg := &Aggregator{}
		agg.SetProfileLookup(lookup)

		target, err := agg.lookupTargetRef("addr-3")
		if err != nil {
			t.Fatalf("lookupTargetRef returned error: %v", err)
		}
		if target == nil || target.Address != "addr-3" {
			t.Fatalf("lookupTargetRef = %+v, want address addr-3", target)
		}
		assertCalls(t, lookup.calls, []string{"global:addr-3", "metaid:addr-3", "address:addr-3"})
	})
}

func TestHandleBlockPinCreateAndRelationshipState(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": {
				MetaId:       "meta-source",
				GlobalMetaId: "idq-source",
				Address:      "1Source",
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
				Bio:          "source bio",
				BioId:        "bio-source:i0",
			},
			"idq-target": {
				MetaId:       "meta-target",
				GlobalMetaId: "idq-target",
				Address:      "1Target",
				Name:         "Target",
				NameId:       "name-target:i0",
				AvatarId:     "avatar-target:i0",
				Bio:          "target bio",
				BioId:        "bio-target:i0",
			},
		},
		byMetaId: map[string]*TargetRef{
			"meta-target": {
				MetaId:       "meta-target",
				GlobalMetaId: "idq-target",
				Address:      "1Target",
				Name:         "Target",
				NameId:       "name-target:i0",
				AvatarId:     "avatar-target:i0",
				Bio:          "target bio",
				BioId:        "bio-target:i0",
			},
		},
	})

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "meta-target", 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	rel, err := agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: "idq-source",
		TargetGlobalMetaId: "idq-target",
	})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if !rel.Source.FollowsTarget {
		t.Fatalf("source follows target = false, want true")
	}
	if rel.Source.FollowsSource {
		t.Fatalf("source follows source = true, want false")
	}
	if rel.Target.FollowsSource {
		t.Fatalf("target follows source = true, want false")
	}
	if rel.Target.FollowsTarget {
		t.Fatalf("target follows source = true, want false")
	}
	if rel.Source.FollowPinId != "follow-1:i0" || rel.Source.FollowedAt != 1001 {
		t.Fatalf("source side = %+v, want follow pin/time populated", rel.Source)
	}
	if rel.Target.FollowPinId != "" || rel.Target.FollowedAt != 0 || rel.Mutual {
		t.Fatalf("target side = %+v, mutual=%v, want no reverse edge metadata", rel.Target, rel.Mutual)
	}

	if _, err := agg.HandleBlockPin(followPin("follow-2:i0", "idq-target", "meta-target", "1Target", "idq-source", 1002)); err != nil {
		t.Fatalf("HandleBlockPin(reverse create): %v", err)
	}

	rel, err = agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: "idq-source",
		TargetGlobalMetaId: "idq-target",
	})
	if err != nil {
		t.Fatalf("Relationship(mutual): %v", err)
	}
	if !rel.Source.FollowsTarget {
		t.Fatalf("source follows target = false after reverse follow, want true")
	}
	if !rel.Target.FollowsSource {
		t.Fatalf("target follows source = false after reverse follow, want true")
	}
	if rel.Target.FollowsTarget {
		t.Fatalf("target follows target = true after reverse follow, want false")
	}
	if !rel.Mutual {
		t.Fatalf("mutual = false after reverse follow, want true")
	}
	if rel.Target.FollowPinId != "follow-2:i0" || rel.Target.FollowedAt != 1002 {
		t.Fatalf("target side = %+v, want reverse follow pin/time populated", rel.Target)
	}
}

func TestHandleBlockPinCreateFallsBackToCanonicalGlobalMetaIdTarget(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": {
				MetaId:       "meta-source",
				GlobalMetaId: "idq-source",
				Address:      "1Source",
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
			},
		},
	})

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "idq-target", 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 10, View: ViewCompact})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 1 {
		t.Fatalf("ListFollowing len = %d, want 1; list=%s", len(following.List), marshalItems(t, following.List))
	}
	if following.List[0].GlobalMetaId != "idq-target" {
		t.Fatalf("ListFollowing[0].GlobalMetaId = %q, want idq-target; list=%s", following.List[0].GlobalMetaId, marshalItems(t, following.List))
	}
}

func TestHandleBlockPinCreateDoesNotFallbackForUnresolvedMetaIdTarget(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": {
				MetaId:       "meta-source",
				GlobalMetaId: "idq-source",
				Address:      "1Source",
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
			},
		},
	})

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "meta-target", 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 10, View: ViewCompact})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 0 {
		t.Fatalf("ListFollowing len = %d, want 0; list=%s", len(following.List), marshalItems(t, following.List))
	}
}

func TestHandleBlockPinRevokeRemovesActiveRelation(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": newTargetRef("meta-source", "idq-source", "1Source"),
			"idq-target": newTargetRef("meta-target", "idq-target", "1Target"),
		},
	})

	_, _ = agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "idq-target", 1001))
	if _, err := agg.HandleBlockPin(revokePin("revoke-1:i0", "follow-1:i0", 1002)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}

	rel, err := agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: "idq-source",
		TargetGlobalMetaId: "idq-target",
	})
	if err != nil {
		t.Fatalf("Relationship: %v", err)
	}
	if rel.Source.FollowsTarget || rel.Source.FollowsSource || rel.Target.FollowsTarget || rel.Target.FollowsSource || rel.Mutual {
		t.Fatalf("relationship after revoke = %+v, want no active relation", rel)
	}
	if rel.Source.FollowPinId != "" || rel.Source.FollowedAt != 0 {
		t.Fatalf("source side after revoke = %+v, want cleared metadata", rel.Source)
	}
	if rel.Target.FollowPinId != "" || rel.Target.FollowedAt != 0 {
		t.Fatalf("target side after revoke = %+v, want cleared metadata", rel.Target)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 0 || following.Size != 10 {
		t.Fatalf("ListFollowing = %+v, want empty list and size=10", following)
	}

	followers, err := agg.ListFollowers(ListParams{GlobalMetaId: "idq-target", Size: 10, View: ViewCompact})
	if err != nil {
		t.Fatalf("ListFollowers: %v", err)
	}
	if len(followers.List) != 0 || followers.Size != 10 {
		t.Fatalf("ListFollowers = %+v, want empty list and size=10", followers)
	}
}

func TestListFollowingAndFollowersNewestFirst(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": {MetaId: "meta-source", GlobalMetaId: "idq-source", Address: "1Source", Name: "Source", NameId: "name-source:i0", AvatarId: "avatar-source:i0", Bio: "source bio", BioId: "bio-source:i0"},
			"idq-a":      {MetaId: "meta-a", GlobalMetaId: "idq-a", Address: "1A", Name: "Alice", NameId: "name-a:i0", AvatarId: "avatar-a:i0", Bio: "bio a", BioId: "bio-a:i0"},
			"idq-b":      {MetaId: "meta-b", GlobalMetaId: "idq-b", Address: "1B", Name: "Bob", NameId: "name-b:i0", AvatarId: "avatar-b:i0", Bio: "bio b", BioId: "bio-b:i0"},
			"idq-c":      {MetaId: "meta-c", GlobalMetaId: "idq-c", Address: "1C", Name: "Cara", NameId: "name-c:i0", AvatarId: "avatar-c:i0", Bio: "bio c", BioId: "bio-c:i0"},
			"idq-target": {MetaId: "meta-target", GlobalMetaId: "idq-target", Address: "1Target", Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0", Bio: "target bio", BioId: "bio-target:i0"},
			"idq-f1":     {MetaId: "meta-f1", GlobalMetaId: "idq-f1", Address: "1F1", Name: "Follower One", NameId: "name-f1:i0", AvatarId: "avatar-f1:i0", Bio: "bio f1", BioId: "bio-f1:i0"},
			"idq-f2":     {MetaId: "meta-f2", GlobalMetaId: "idq-f2", Address: "1F2", Name: "Follower Two", NameId: "name-f2:i0", AvatarId: "avatar-f2:i0", Bio: "bio f2", BioId: "bio-f2:i0"},
			"idq-f3":     {MetaId: "meta-f3", GlobalMetaId: "idq-f3", Address: "1F3", Name: "Follower Three", NameId: "name-f3:i0", AvatarId: "avatar-f3:i0", Bio: "bio f3", BioId: "bio-f3:i0"},
		},
	})

	for _, pin := range []*aggregator.PinInscription{
		followPin("follow-a:i0", "idq-source", "meta-source", "1Source", "idq-a", 1001),
		followPin("follow-b:i0", "idq-source", "meta-source", "1Source", "idq-b", 1002),
		followPin("follow-c:i0", "idq-source", "meta-source", "1Source", "idq-c", 1003),
		followPin("follow-f1:i0", "idq-f1", "meta-f1", "1F1", "idq-target", 1001),
		followPin("follow-f2:i0", "idq-f2", "meta-f2", "1F2", "idq-target", 1002),
		followPin("follow-f3:i0", "idq-f3", "meta-f3", "1F3", "idq-target", 1003),
	} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	followingPage1, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 2, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing(page1): %v", err)
	}
	if followingPage1.Size != 2 {
		t.Fatalf("ListFollowing(page1).Size = %d, want 2", followingPage1.Size)
	}
	assertListOrder(t, followingPage1.List, []string{"idq-c", "idq-b"})
	assertProfileItem(t, followingPage1.List[0], "Cara", "name-c:i0", "avatar-c:i0", "bio c", "bio-c:i0", 1003, "follow-c:i0")
	assertProfileItem(t, followingPage1.List[1], "Bob", "name-b:i0", "avatar-b:i0", "bio b", "bio-b:i0", 1002, "follow-b:i0")
	if followingPage1.NextCursor == "" {
		t.Fatal("ListFollowing(page1) nextCursor empty, want non-empty")
	}

	followingPage2, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", Size: 2, Cursor: followingPage1.NextCursor, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing(page2): %v", err)
	}
	if followingPage2.Size != 2 {
		t.Fatalf("ListFollowing(page2).Size = %d, want 2", followingPage2.Size)
	}
	assertListOrder(t, followingPage2.List, []string{"idq-a"})
	assertProfileItem(t, followingPage2.List[0], "Alice", "name-a:i0", "avatar-a:i0", "bio a", "bio-a:i0", 1001, "follow-a:i0")
	if followingPage2.NextCursor != "" {
		t.Fatalf("ListFollowing(page2) nextCursor = %q, want empty", followingPage2.NextCursor)
	}

	followersPage1, err := agg.ListFollowers(ListParams{GlobalMetaId: "idq-target", Size: 2})
	if err != nil {
		t.Fatalf("ListFollowers(page1): %v", err)
	}
	if followersPage1.Size != 2 {
		t.Fatalf("ListFollowers(page1).Size = %d, want 2", followersPage1.Size)
	}
	assertListOrder(t, followersPage1.List, []string{"idq-f3", "idq-f2"})
	assertCompactItem(t, followersPage1.List[0], "Follower Three", "name-f3:i0", "avatar-f3:i0")
	assertCompactItem(t, followersPage1.List[1], "Follower Two", "name-f2:i0", "avatar-f2:i0")
	if followersPage1.NextCursor == "" {
		t.Fatal("ListFollowers(page1) nextCursor empty, want non-empty")
	}

	followersPage2, err := agg.ListFollowers(ListParams{GlobalMetaId: "idq-target", Size: 2, Cursor: followersPage1.NextCursor, View: ViewCompact})
	if err != nil {
		t.Fatalf("ListFollowers(page2): %v", err)
	}
	if followersPage2.Size != 2 {
		t.Fatalf("ListFollowers(page2).Size = %d, want 2", followersPage2.Size)
	}
	assertListOrder(t, followersPage2.List, []string{"idq-f1"})
	assertCompactItem(t, followersPage2.List[0], "Follower One", "name-f1:i0", "avatar-f1:i0")
	if followersPage2.NextCursor != "" {
		t.Fatalf("ListFollowers(page2) nextCursor = %q, want empty", followersPage2.NextCursor)
	}
}

func TestLookupRequestSubjectDistinguishesErrors(t *testing.T) {
	agg := &Aggregator{}
	if _, err := agg.ListFollowing(ListParams{}); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("ListFollowing empty subject err = %v, want ErrInvalidParameter", err)
	}

	if _, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-source", View: "full"}); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("ListFollowing invalid view err = %v, want ErrInvalidParameter", err)
	}

	agg.SetProfileLookup(&fakeTargetLookup{})
	if _, err := agg.ListFollowing(ListParams{GlobalMetaId: "idq-missing"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListFollowing missing subject err = %v, want ErrNotFound", err)
	}

	agg.SetProfileLookup(&fakeTargetLookup{
		byMetaId: map[string]*TargetRef{
			"meta-only": newTargetRef("meta-only", "idq-meta-only", "1MetaOnly"),
		},
		byAddress: map[string]*TargetRef{
			"1AddressOnly": newTargetRef("meta-address-only", "idq-address-only", "1AddressOnly"),
		},
	})
	if _, err := agg.ListFollowers(ListParams{GlobalMetaId: "meta-only"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListFollowers metaId subject err = %v, want ErrNotFound", err)
	}
	if _, err := agg.Relationship(RelationshipParams{SourceGlobalMetaId: "1AddressOnly", TargetGlobalMetaId: "idq-target"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Relationship address subject err = %v, want ErrNotFound", err)
	}
}

func TestListFollowersRejectsStaleCursor(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-target": {MetaId: "meta-target", GlobalMetaId: "idq-target", Address: "1Target", Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0"},
			"idq-f1":     {MetaId: "meta-f1", GlobalMetaId: "idq-f1", Address: "1F1", Name: "Follower One", NameId: "name-f1:i0", AvatarId: "avatar-f1:i0"},
			"idq-f2":     {MetaId: "meta-f2", GlobalMetaId: "idq-f2", Address: "1F2", Name: "Follower Two", NameId: "name-f2:i0", AvatarId: "avatar-f2:i0"},
		},
	})

	for _, pin := range []*aggregator.PinInscription{
		followPin("follow-f1:i0", "idq-f1", "meta-f1", "1F1", "idq-target", 1001),
		followPin("follow-f2:i0", "idq-f2", "meta-f2", "1F2", "idq-target", 1002),
	} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	page1, err := agg.ListFollowers(ListParams{GlobalMetaId: "idq-target", Size: 1})
	if err != nil {
		t.Fatalf("ListFollowers(page1): %v", err)
	}
	if page1.NextCursor == "" {
		t.Fatal("ListFollowers(page1) nextCursor empty, want non-empty")
	}

	if _, err := agg.HandleBlockPin(revokePin("revoke-f2:i0", "follow-f2:i0", 1003)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}

	if _, err := agg.ListFollowers(ListParams{GlobalMetaId: "idq-target", Size: 1, Cursor: page1.NextCursor}); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("ListFollowers stale cursor err = %v, want ErrInvalidParameter", err)
	}
}

func assertListOrder(t *testing.T, items []ListItem, want []string) {
	t.Helper()
	if len(items) != len(want) {
		t.Fatalf("items len = %d, want %d; items=%s", len(items), len(want), marshalItems(t, items))
	}
	for i, globalMetaId := range want {
		if items[i].GlobalMetaId != globalMetaId {
			t.Fatalf("items[%d].GlobalMetaId = %q, want %q; items=%s", i, items[i].GlobalMetaId, globalMetaId, marshalItems(t, items))
		}
	}
}

func marshalItems(t *testing.T, items []ListItem) string {
	t.Helper()
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("marshal items: %v", err)
	}
	return string(raw)
}

func assertCompactItem(t *testing.T, item ListItem, wantName, wantNameId, wantAvatarId string) {
	t.Helper()
	if item.Name != wantName || item.NameId != wantNameId || item.AvatarId != wantAvatarId {
		t.Fatalf("compact item = %+v, want name=%q nameId=%q avatarId=%q", item, wantName, wantNameId, wantAvatarId)
	}
	if item.Bio != "" || item.BioId != "" || item.FollowedAt != 0 || item.FollowPinId != "" {
		t.Fatalf("compact item = %+v, want profile-only fields cleared", item)
	}
}

func assertProfileItem(t *testing.T, item ListItem, wantName, wantNameId, wantAvatarId, wantBio, wantBioId string, wantFollowedAt int64, wantFollowPinId string) {
	t.Helper()
	if item.Name != wantName || item.NameId != wantNameId || item.AvatarId != wantAvatarId {
		t.Fatalf("profile item identity = %+v, want name=%q nameId=%q avatarId=%q", item, wantName, wantNameId, wantAvatarId)
	}
	if item.Bio != wantBio || item.BioId != wantBioId || item.FollowedAt != wantFollowedAt || item.FollowPinId != wantFollowPinId {
		t.Fatalf("profile item = %+v, want bio=%q bioId=%q followedAt=%d followPinId=%q", item, wantBio, wantBioId, wantFollowedAt, wantFollowPinId)
	}
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls len = %d, want %d; got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls[%d] = %q, want %q; got=%v want=%v", i, got[i], want[i], got, want)
		}
	}
}
