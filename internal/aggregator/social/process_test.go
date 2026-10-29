package social

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// testIdentity derives a real-format identity triple from a label: a valid
// IDAddress (idq1…), a legacy 64-hex metaid and a Base58Check P2PKH address
// that all encode the same payload, so normalizeIdentity maps address → gid
// and profile lookups map metaid → gid. The spec's author note forbids
// placeholder identifier strings in tests; these are deterministic instead.
func testIdentity(label string) (globalMetaId, metaId, address string) {
	payload := sha256.Sum256([]byte("metaso-p2p-social-test:" + label))
	metaIdDigest := sha256.Sum256([]byte("metaso-p2p-social-test-metaid:" + label))
	globalMetaId, err := idaddress.EncodeIDAddress(idaddress.VersionP2PKH, payload[:20])
	if err != nil {
		panic(err)
	}
	return globalMetaId, hex.EncodeToString(metaIdDigest[:]), idaddress.Base58CheckEncode(0x00, payload[:20])
}

var (
	testGidSource, testMetaSource, testAddrSource = testIdentity("source")
	testGidTarget, testMetaTarget, testAddrTarget = testIdentity("target")
	testGidA, testMetaA, testAddrA                = testIdentity("a")
	testGidB, testMetaB, testAddrB                = testIdentity("b")
	testGidC, testMetaC, testAddrC                = testIdentity("c")
	testGidF1, testMetaF1, testAddrF1             = testIdentity("f1")
	testGidF2, testMetaF2, testAddrF2             = testIdentity("f2")
	testGidF3, testMetaF3, testAddrF3             = testIdentity("f3")
	testGidMissing, _, _                          = testIdentity("missing")
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

func TestAggregatorNameAndNormalizeWithoutProfileLookup(t *testing.T) {
	agg := &Aggregator{}
	if got := agg.Name(); got != "social" {
		t.Fatalf("Name() = %q, want social", got)
	}

	// Step 1 is pure format validation and works without any profile lookup.
	canonical, ok, err := agg.normalizeIdentity(testGidTarget)
	if err != nil || !ok || canonical != testGidTarget {
		t.Fatalf("normalizeIdentity(valid gid) = (%q, %v, %v), want (%q, true, nil)", canonical, ok, err, testGidTarget)
	}
	// A legacy hex id needs the profile archive; without it the reference is
	// unresolved, not an error.
	canonical, ok, err = agg.normalizeIdentity(testMetaTarget)
	if err != nil || ok || canonical != "" {
		t.Fatalf("normalizeIdentity(hex without lookup) = (%q, %v, %v), want unresolved without error", canonical, ok, err)
	}
}

func TestNormalizeIdentityPipeline(t *testing.T) {
	t.Run("idaddress accepted without profile", func(t *testing.T) {
		agg := &Aggregator{}
		canonical, ok, err := agg.normalizeIdentity(testGidTarget)
		if err != nil {
			t.Fatalf("normalizeIdentity returned error: %v", err)
		}
		if !ok || canonical != testGidTarget {
			t.Fatalf("normalizeIdentity = (%q, %v), want (%q, true)", canonical, ok, testGidTarget)
		}
	})

	t.Run("idaddress case-insensitive", func(t *testing.T) {
		agg := &Aggregator{}
		canonical, ok, err := agg.normalizeIdentity(strings.ToUpper(testGidTarget))
		if err != nil {
			t.Fatalf("normalizeIdentity returned error: %v", err)
		}
		if !ok || canonical != testGidTarget {
			t.Fatalf("normalizeIdentity = (%q, %v), want (%q, true)", canonical, ok, testGidTarget)
		}
	})

	t.Run("malformed id-prefixed string rejected", func(t *testing.T) {
		agg := &Aggregator{}
		if _, ok, err := agg.normalizeIdentity("idq-not-a-real-checksum"); err != nil || ok {
			t.Fatalf("normalizeIdentity(malformed id) ok=%v err=%v, want ok=false err=nil", ok, err)
		}
	})

	t.Run("legacy hex resolved through profile lookup", func(t *testing.T) {
		agg := &Aggregator{}
		agg.SetProfileLookup(&fakeTargetLookup{
			byMetaId: map[string]*TargetRef{
				testMetaTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
			},
		})
		canonical, ok, err := agg.normalizeIdentity(testMetaTarget)
		if err != nil {
			t.Fatalf("normalizeIdentity returned error: %v", err)
		}
		if !ok || canonical != testGidTarget {
			t.Fatalf("normalizeIdentity = (%q, %v), want (%q, true)", canonical, ok, testGidTarget)
		}
	})

	t.Run("legacy hex miss is unresolved not error", func(t *testing.T) {
		agg := &Aggregator{}
		agg.SetProfileLookup(&fakeTargetLookup{})
		if canonical, ok, err := agg.normalizeIdentity(testMetaTarget); err != nil || ok || canonical != "" {
			t.Fatalf("normalizeIdentity(hex miss) = (%q, %v, %v), want unresolved without error", canonical, ok, err)
		}
	})

	t.Run("profile dependency failure surfaces as unavailable", func(t *testing.T) {
		agg := &Aggregator{}
		agg.SetProfileLookup(lookupErrorStub{err: errors.New("remote down")})
		_, _, err := agg.normalizeIdentity(testMetaTarget)
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("normalizeIdentity(hex with broken lookup) err = %v, want ErrUnavailable", err)
		}
	})

	t.Run("chain address converted by idaddress algorithm", func(t *testing.T) {
		agg := &Aggregator{}
		canonical, ok, err := agg.normalizeIdentity(testAddrSource)
		if err != nil {
			t.Fatalf("normalizeIdentity returned error: %v", err)
		}
		if !ok || canonical != testGidSource {
			t.Fatalf("normalizeIdentity(address) = (%q, %v), want (%q, true)", canonical, ok, testGidSource)
		}
	})

	t.Run("garbage rejected", func(t *testing.T) {
		agg := &Aggregator{}
		for _, input := range []string{"", "   ", "hello", "0x1234", strings.Repeat("a", 63), strings.Repeat("g", 64)} {
			if canonical, ok, err := agg.normalizeIdentity(input); err != nil || ok || canonical != "" {
				t.Fatalf("normalizeIdentity(%q) = (%q, %v, %v), want unresolved without error", input, canonical, ok, err)
			}
		}
	})
}

type lookupErrorStub struct{ err error }

func (s lookupErrorStub) LookupByMetaId(metaId string) (*TargetRef, error) { return nil, s.err }

func (s lookupErrorStub) LookupByGlobalMetaId(globalMetaId string) (*TargetRef, error) {
	return nil, s.err
}

func (s lookupErrorStub) LookupByAddress(address string) (*TargetRef, error) { return nil, s.err }

func TestHandleBlockPinCreateAndRelationshipState(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: {
				MetaId:       testMetaSource,
				GlobalMetaId: testGidSource,
				Address:      testAddrSource,
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
				Bio:          "source bio",
				BioId:        "bio-source:i0",
			},
			testGidTarget: {
				MetaId:       testMetaTarget,
				GlobalMetaId: testGidTarget,
				Address:      testAddrTarget,
				Name:         "Target",
				NameId:       "name-target:i0",
				AvatarId:     "avatar-target:i0",
				Bio:          "target bio",
				BioId:        "bio-target:i0",
			},
		},
		byMetaId: map[string]*TargetRef{
			testMetaTarget: {
				MetaId:       testMetaTarget,
				GlobalMetaId: testGidTarget,
				Address:      testAddrTarget,
				Name:         "Target",
				NameId:       "name-target:i0",
				AvatarId:     "avatar-target:i0",
				Bio:          "target bio",
				BioId:        "bio-target:i0",
			},
		},
	})

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", testGidSource, testMetaSource, testAddrSource, testMetaTarget, 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	rel, err := agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: testGidSource,
		TargetGlobalMetaId: testGidTarget,
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

	if _, err := agg.HandleBlockPin(followPin("follow-2:i0", testGidTarget, testMetaTarget, testAddrTarget, testGidSource, 1002)); err != nil {
		t.Fatalf("HandleBlockPin(reverse create): %v", err)
	}

	rel, err = agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: testGidSource,
		TargetGlobalMetaId: testGidTarget,
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

func TestHandleBlockPinCreateAcceptsIDAddressTargetWithoutProfile(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: {
				MetaId:       testMetaSource,
				GlobalMetaId: testGidSource,
				Address:      testAddrSource,
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
			},
		},
	})

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewCompact})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 1 {
		t.Fatalf("ListFollowing len = %d, want 1; list=%s", len(following.List), marshalItems(t, following.List))
	}
	if following.List[0].GlobalMetaId != testGidTarget {
		t.Fatalf("ListFollowing[0].GlobalMetaId = %q, want idq-target; list=%s", following.List[0].GlobalMetaId, marshalItems(t, following.List))
	}
}

func TestHandleBlockPinCreateLeavesTrailForUnresolvedTarget(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	lookup := &fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: {
				MetaId:       testMetaSource,
				GlobalMetaId: testGidSource,
				Address:      testAddrSource,
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
			},
		},
	}
	agg.SetProfileLookup(lookup)

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", testGidSource, testMetaSource, testAddrSource, testMetaTarget, 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewCompact})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 0 {
		t.Fatalf("ListFollowing len = %d, want 0; list=%s", len(following.List), marshalItems(t, following.List))
	}

	// §2.5: the unresolved pin is still recorded at the pin level…
	trail, err := store.Get(namespace, pinToPairKey("follow-1:i0"))
	if err != nil {
		t.Fatalf("load pin trail: %v", err)
	}
	var record unresolvedPinRecord
	if err := json.Unmarshal(trail, &record); err != nil {
		t.Fatalf("decode pin trail: %v; raw=%s", err, trail)
	}
	if !record.Unresolved || record.Follower != testGidSource || record.RawTargetRef != testMetaTarget {
		t.Fatalf("pin trail = %+v, want unresolved record for source→%s", record, testMetaTarget)
	}

	// …and a replay after profile coverage improves materializes the edge.
	lookup.byMetaId = map[string]*TargetRef{
		testMetaTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
	}
	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", testGidSource, testMetaSource, testAddrSource, testMetaTarget, 1001)); err != nil {
		t.Fatalf("HandleBlockPin(replay): %v", err)
	}
	following, err = agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewCompact})
	if err != nil {
		t.Fatalf("ListFollowing(replay): %v", err)
	}
	if len(following.List) != 1 || following.List[0].GlobalMetaId != testGidTarget {
		t.Fatalf("ListFollowing(replay) = %s, want [%s]", marshalItems(t, following.List), testGidTarget)
	}
}

func TestHandleBlockPinRevokeRemovesActiveRelation(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testGidTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
		},
	})

	_, _ = agg.HandleBlockPin(followPin("follow-1:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1001))
	if _, err := agg.HandleBlockPin(revokePin("revoke-1:i0", "follow-1:i0", 1002)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}

	rel, err := agg.Relationship(RelationshipParams{
		SourceGlobalMetaId: testGidSource,
		TargetGlobalMetaId: testGidTarget,
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

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 0 || following.Size != 10 {
		t.Fatalf("ListFollowing = %+v, want empty list and size=10", following)
	}

	followers, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, Size: 10, View: ViewCompact})
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
			testGidSource: {MetaId: testMetaSource, GlobalMetaId: testGidSource, Address: testAddrSource, Name: "Source", NameId: "name-source:i0", AvatarId: "avatar-source:i0", Bio: "source bio", BioId: "bio-source:i0"},
			testGidA:      {MetaId: testMetaA, GlobalMetaId: testGidA, Address: testAddrA, Name: "Alice", NameId: "name-a:i0", AvatarId: "avatar-a:i0", Bio: "bio a", BioId: "bio-a:i0"},
			testGidB:      {MetaId: testMetaB, GlobalMetaId: testGidB, Address: testAddrB, Name: "Bob", NameId: "name-b:i0", AvatarId: "avatar-b:i0", Bio: "bio b", BioId: "bio-b:i0"},
			testGidC:      {MetaId: testMetaC, GlobalMetaId: testGidC, Address: testAddrC, Name: "Cara", NameId: "name-c:i0", AvatarId: "avatar-c:i0", Bio: "bio c", BioId: "bio-c:i0"},
			testGidTarget: {MetaId: testMetaTarget, GlobalMetaId: testGidTarget, Address: testAddrTarget, Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0", Bio: "target bio", BioId: "bio-target:i0"},
			testGidF1:     {MetaId: testMetaF1, GlobalMetaId: testGidF1, Address: testAddrF1, Name: "Follower One", NameId: "name-f1:i0", AvatarId: "avatar-f1:i0", Bio: "bio f1", BioId: "bio-f1:i0"},
			testGidF2:     {MetaId: testMetaF2, GlobalMetaId: testGidF2, Address: testAddrF2, Name: "Follower Two", NameId: "name-f2:i0", AvatarId: "avatar-f2:i0", Bio: "bio f2", BioId: "bio-f2:i0"},
			testGidF3:     {MetaId: testMetaF3, GlobalMetaId: testGidF3, Address: testAddrF3, Name: "Follower Three", NameId: "name-f3:i0", AvatarId: "avatar-f3:i0", Bio: "bio f3", BioId: "bio-f3:i0"},
		},
	})

	for _, pin := range []*aggregator.PinInscription{
		followPin("follow-a:i0", testGidSource, testMetaSource, testAddrSource, testGidA, 1001),
		followPin("follow-b:i0", testGidSource, testMetaSource, testAddrSource, testGidB, 1002),
		followPin("follow-c:i0", testGidSource, testMetaSource, testAddrSource, testGidC, 1003),
		followPin("follow-f1:i0", testGidF1, testMetaF1, testAddrF1, testGidTarget, 1001),
		followPin("follow-f2:i0", testGidF2, testMetaF2, testAddrF2, testGidTarget, 1002),
		followPin("follow-f3:i0", testGidF3, testMetaF3, testAddrF3, testGidTarget, 1003),
	} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	followingPage1, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 2, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing(page1): %v", err)
	}
	if followingPage1.Size != 2 {
		t.Fatalf("ListFollowing(page1).Size = %d, want 2", followingPage1.Size)
	}
	assertListOrder(t, followingPage1.List, []string{testGidC, testGidB})
	assertProfileItem(t, followingPage1.List[0], "Cara", "name-c:i0", "avatar-c:i0", "bio c", "bio-c:i0", 1003, "follow-c:i0")
	assertProfileItem(t, followingPage1.List[1], "Bob", "name-b:i0", "avatar-b:i0", "bio b", "bio-b:i0", 1002, "follow-b:i0")
	if followingPage1.NextCursor == "" {
		t.Fatal("ListFollowing(page1) nextCursor empty, want non-empty")
	}

	followingPage2, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 2, Cursor: followingPage1.NextCursor, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing(page2): %v", err)
	}
	if followingPage2.Size != 2 {
		t.Fatalf("ListFollowing(page2).Size = %d, want 2", followingPage2.Size)
	}
	assertListOrder(t, followingPage2.List, []string{testGidA})
	assertProfileItem(t, followingPage2.List[0], "Alice", "name-a:i0", "avatar-a:i0", "bio a", "bio-a:i0", 1001, "follow-a:i0")
	if followingPage2.NextCursor != "" {
		t.Fatalf("ListFollowing(page2) nextCursor = %q, want empty", followingPage2.NextCursor)
	}

	followersPage1, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, Size: 2})
	if err != nil {
		t.Fatalf("ListFollowers(page1): %v", err)
	}
	if followersPage1.Size != 2 {
		t.Fatalf("ListFollowers(page1).Size = %d, want 2", followersPage1.Size)
	}
	assertListOrder(t, followersPage1.List, []string{testGidF3, testGidF2})
	assertCompactItem(t, followersPage1.List[0], "Follower Three", "name-f3:i0", "avatar-f3:i0")
	assertCompactItem(t, followersPage1.List[1], "Follower Two", "name-f2:i0", "avatar-f2:i0")
	if followersPage1.NextCursor == "" {
		t.Fatal("ListFollowers(page1) nextCursor empty, want non-empty")
	}

	followersPage2, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, Size: 2, Cursor: followersPage1.NextCursor, View: ViewCompact})
	if err != nil {
		t.Fatalf("ListFollowers(page2): %v", err)
	}
	if followersPage2.Size != 2 {
		t.Fatalf("ListFollowers(page2).Size = %d, want 2", followersPage2.Size)
	}
	assertListOrder(t, followersPage2.List, []string{testGidF1})
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

	if _, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, View: "full"}); !errors.Is(err, ErrInvalidParameter) {
		t.Fatalf("ListFollowing invalid view err = %v, want ErrInvalidParameter", err)
	}

	agg.SetProfileLookup(&fakeTargetLookup{})
	if _, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidMissing}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListFollowing missing subject err = %v, want ErrNotFound", err)
	}

	gidMetaOnly, metaOnly, addrMetaOnly := testIdentity("meta-only")
	gidAddressOnly, metaAddrOnly, addrAddressOnly := testIdentity("address-only")
	agg.SetProfileLookup(&fakeTargetLookup{
		byMetaId: map[string]*TargetRef{
			metaOnly: newTargetRef(metaOnly, gidMetaOnly, addrMetaOnly),
		},
		byAddress: map[string]*TargetRef{
			addrAddressOnly: newTargetRef(metaAddrOnly, gidAddressOnly, addrAddressOnly),
		},
	})
	if _, err := agg.ListFollowers(ListParams{GlobalMetaId: metaOnly}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListFollowers metaId subject err = %v, want ErrNotFound", err)
	}
	if _, err := agg.Relationship(RelationshipParams{SourceGlobalMetaId: addrAddressOnly, TargetGlobalMetaId: testGidTarget}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Relationship address subject err = %v, want ErrNotFound", err)
	}
}

func TestListFollowersRejectsStaleCursor(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidTarget: {MetaId: testMetaTarget, GlobalMetaId: testGidTarget, Address: testAddrTarget, Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0"},
			testGidF1:     {MetaId: testMetaF1, GlobalMetaId: testGidF1, Address: testAddrF1, Name: "Follower One", NameId: "name-f1:i0", AvatarId: "avatar-f1:i0"},
			testGidF2:     {MetaId: testMetaF2, GlobalMetaId: testGidF2, Address: testAddrF2, Name: "Follower Two", NameId: "name-f2:i0", AvatarId: "avatar-f2:i0"},
		},
	})

	for _, pin := range []*aggregator.PinInscription{
		followPin("follow-f1:i0", testGidF1, testMetaF1, testAddrF1, testGidTarget, 1001),
		followPin("follow-f2:i0", testGidF2, testMetaF2, testAddrF2, testGidTarget, 1002),
	} {
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.Id, err)
		}
	}

	page1, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, Size: 1})
	if err != nil {
		t.Fatalf("ListFollowers(page1): %v", err)
	}
	if page1.NextCursor == "" {
		t.Fatal("ListFollowers(page1) nextCursor empty, want non-empty")
	}

	if _, err := agg.HandleBlockPin(revokePin("revoke-f2:i0", "follow-f2:i0", 1003)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}

	if _, err := agg.ListFollowers(ListParams{GlobalMetaId: testGidTarget, Size: 1, Cursor: page1.NextCursor}); !errors.Is(err, ErrInvalidParameter) {
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
