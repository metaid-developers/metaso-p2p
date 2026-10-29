package social

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// fakeBlockLister stubs the §4.5 BlockLister for filter tests. The no-op
// default installed by Init is exercised by every other test in the package.
type fakeBlockLister struct {
	blocking  map[string][]string // subject -> peers the subject blocks
	blockedBy map[string][]string // subject -> peers blocking the subject
}

func (f fakeBlockLister) IsBlockedBetween(a, b string) (bool, error) {
	for _, peer := range append(f.blocking[a], f.blockedBy[a]...) {
		if peer == b {
			return true, nil
		}
	}
	for _, peer := range append(f.blocking[b], f.blockedBy[b]...) {
		if peer == a {
			return true, nil
		}
	}
	return false, nil
}

func (f fakeBlockLister) BlockingBy(subject string) ([]string, error) {
	return f.blocking[subject], nil
}

func (f fakeBlockLister) BlockedBy(subject string) ([]string, error) {
	return f.blockedBy[subject], nil
}

func callSocialPost(t *testing.T, router *gin.Engine, path string, payload any) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if s, ok := payload.(string); ok {
		raw = []byte(s)
	} else {
		var err error
		if raw, err = json.Marshal(payload); err != nil {
			t.Fatalf("marshal request: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v; body=%q", err, rec.Body.String())
	}
	return rec.Code, body
}

func TestIngestFoldsLegacyHexAndGlobalEdgesIntoOnePairKey(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testGidTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
		},
		byMetaId: map[string]*TargetRef{
			testMetaTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
			testMetaSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
		},
	})

	// A legacy-hex target ref and a GlobalMetaID target ref name the same
	// human; both ingest paths must land on the same canonical pair key.
	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", testGidSource, testMetaSource, testAddrSource, testMetaTarget, 1001)); err != nil {
		t.Fatalf("HandleBlockPin(hex target): %v", err)
	}
	if _, err := agg.HandleBlockPin(followPin("follow-2:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1002)); err != nil {
		t.Fatalf("HandleBlockPin(global target): %v", err)
	}

	following, err := agg.ListFollowing(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFollowing: %v", err)
	}
	if len(following.List) != 1 {
		t.Fatalf("ListFollowing len = %d, want 1 (edges folded); list=%s", len(following.List), marshalItems(t, following.List))
	}
	if following.List[0].GlobalMetaId != testGidTarget {
		t.Fatalf("ListFollowing[0] = %s, want %s", marshalItems(t, following.List), testGidTarget)
	}
	if following.List[0].FollowPinId != "follow-2:i0" || following.List[0].FollowedAt != 1002 {
		t.Fatalf("folded edge = %+v, want newest create (follow-2:i0 at 1002) to win", following.List[0])
	}

	// The reverse edge written with a hex ref must complete the friendship
	// keyed on canonical ids.
	if _, err := agg.HandleBlockPin(followPin("follow-3:i0", testGidTarget, testMetaTarget, testAddrTarget, testMetaSource, 1003)); err != nil {
		t.Fatalf("HandleBlockPin(reverse hex target): %v", err)
	}
	friends, err := agg.ListFriends(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFriends: %v", err)
	}
	if len(friends.List) != 1 || friends.List[0].GlobalMetaId != testGidTarget {
		t.Fatalf("ListFriends = %+v, want single friend %s", friends.List, testGidTarget)
	}
	if friends.List[0].FriendSince != 1003 {
		t.Fatalf("FriendSince = %d, want 1003 (later of 1001/1002 vs 1003)", friends.List[0].FriendSince)
	}

	raw, err := store.Get(namespace, pairKey(testGidSource, testGidTarget))
	if err != nil || raw == nil {
		t.Fatalf("canonical pair key missing after fold: raw=%v err=%v", raw, err)
	}
}

func TestMutualIndexLifecycle(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testGidTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
		},
	})

	// One directional edge: mutual entries must not exist yet.
	_, _ = agg.HandleBlockPin(followPin("follow-1:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1001))
	if raw, _ := store.Get(namespace, mutualIndexKey(testGidSource, testGidTarget)); raw != nil {
		t.Fatal("mutual entry exists after single directional edge")
	}

	// Second directional edge materializes both per-subject entries.
	if _, err := agg.HandleBlockPin(followPin("follow-2:i0", testGidTarget, testMetaTarget, testAddrTarget, testGidSource, 1002)); err != nil {
		t.Fatalf("HandleBlockPin(reverse): %v", err)
	}
	for _, key := range [][]byte{mutualIndexKey(testGidSource, testGidTarget), mutualIndexKey(testGidTarget, testGidSource)} {
		if raw, _ := store.Get(namespace, key); raw == nil {
			t.Fatalf("mutual entry missing after second directional edge: key=%s", key)
		}
	}

	// Revoking one direction removes the friendship on both sides.
	if _, err := agg.HandleBlockPin(revokePin("revoke-2:i0", "follow-2:i0", 1003)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}
	for _, key := range [][]byte{mutualIndexKey(testGidSource, testGidTarget), mutualIndexKey(testGidTarget, testGidSource)} {
		if raw, _ := store.Get(namespace, key); raw != nil {
			t.Fatalf("mutual entry survived one-sided revoke: key=%s", key)
		}
	}

	// Re-following later moves friendSince to the new later edge.
	if _, err := agg.HandleBlockPin(followPin("follow-4:i0", testGidTarget, testMetaTarget, testAddrTarget, testGidSource, 1005)); err != nil {
		t.Fatalf("HandleBlockPin(re-follow): %v", err)
	}
	friends, err := agg.ListFriends(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFriends: %v", err)
	}
	if len(friends.List) != 1 || friends.List[0].FriendSince != 1005 {
		t.Fatalf("ListFriends after re-follow = %+v, want friendSince 1005", friends.List)
	}
	if friends.List[0].FollowPinId != "follow-1:i0" || friends.List[0].FollowedAt != 1001 {
		t.Fatalf("subject→peer edge fields = %+v, want follow-1:i0 at 1001", friends.List[0])
	}
}

func TestRebuildMutualIndexRecomputesAndDropsStaleEntries(t *testing.T) {
	agg, store := newTestSocialAggregator(t)
	defer store.Close()

	gidGhost, _, _ := testIdentity("ghost")
	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testGidTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
			testGidA:      newTargetRef(testMetaA, testGidA, testAddrA),
			testGidB:      newTargetRef(testMetaB, testGidB, testAddrB),
		},
	})

	for _, pin := range []struct {
		id, follower, followerMeta, followerAddr, target string
		ts                                               int64
	}{
		{"follow-1:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1001},
		{"follow-2:i0", testGidTarget, testMetaTarget, testAddrTarget, testGidSource, 1002},
		{"follow-3:i0", testGidA, testMetaA, testAddrA, testGidB, 1003},
		{"follow-4:i0", testGidB, testMetaB, testAddrB, testGidA, 1004},
		{"follow-5:i0", testGidSource, testMetaSource, testAddrSource, testGidA, 1005}, // one-sided
	} {
		if _, err := agg.HandleBlockPin(followPin(pin.id, pin.follower, pin.followerMeta, pin.followerAddr, pin.target, pin.ts)); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.id, err)
		}
	}

	// Simulate a pre-index store: wipe the mutual index and confirm the read
	// path is empty, then seed a stale entry the rebuild must drop.
	if err := store.DeleteByPrefix(namespace, []byte(keyMutualIndex)); err != nil {
		t.Fatalf("wipe mutual index: %v", err)
	}
	if friends, err := agg.ListFriends(ListParams{GlobalMetaId: testGidSource, Size: 10}); err != nil || len(friends.List) != 0 {
		t.Fatalf("ListFriends after wipe = %v (err %v), want empty", friends.List, err)
	}
	if err := store.Set(namespace, mutualIndexKey(testGidSource, gidGhost), []byte(`{"friendSince":1}`)); err != nil {
		t.Fatalf("seed stale entry: %v", err)
	}

	written, err := agg.RebuildMutualIndex()
	if err != nil {
		t.Fatalf("RebuildMutualIndex: %v", err)
	}
	if written != 2 {
		t.Fatalf("RebuildMutualIndex written = %d, want 2 mutual pairs", written)
	}
	if raw, _ := store.Get(namespace, mutualIndexKey(testGidSource, gidGhost)); raw != nil {
		t.Fatal("stale mutual entry survived rebuild")
	}

	friends, err := agg.ListFriends(ListParams{GlobalMetaId: testGidSource, Size: 10, View: ViewProfile})
	if err != nil {
		t.Fatalf("ListFriends after rebuild: %v", err)
	}
	if len(friends.List) != 1 || friends.List[0].GlobalMetaId != testGidTarget || friends.List[0].FriendSince != 1002 {
		t.Fatalf("ListFriends after rebuild = %+v, want [%s friendSince 1002]", friends.List, testGidTarget)
	}
}

func newFriendsFixture(t *testing.T) (*gin.Engine, *Aggregator) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	agg, store := newTestSocialAggregator(t)
	t.Cleanup(func() { _ = store.Close() })
	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: {MetaId: testMetaSource, GlobalMetaId: testGidSource, Address: testAddrSource, Name: "Source", NameId: "name-source:i0", AvatarId: "avatar-source:i0", Bio: "source bio", BioId: "bio-source:i0"},
			testGidTarget: {MetaId: testMetaTarget, GlobalMetaId: testGidTarget, Address: testAddrTarget, Name: "Target", NameId: "name-target:i0", AvatarId: "avatar-target:i0", Bio: "target bio", BioId: "bio-target:i0"},
			testGidA:      {MetaId: testMetaA, GlobalMetaId: testGidA, Address: testAddrA, Name: "A", NameId: "name-a:i0", AvatarId: "avatar-a:i0", Bio: "bio a", BioId: "bio-a:i0"},
			testGidB:      {MetaId: testMetaB, GlobalMetaId: testGidB, Address: testAddrB, Name: "B", NameId: "name-b:i0", AvatarId: "avatar-b:i0", Bio: "bio b", BioId: "bio-b:i0"},
			testGidC:      {MetaId: testMetaC, GlobalMetaId: testGidC, Address: testAddrC, Name: "C", NameId: "name-c:i0", AvatarId: "avatar-c:i0", Bio: "bio c", BioId: "bio-c:i0"},
		},
		byMetaId: map[string]*TargetRef{
			testMetaSource: newTargetRef(testMetaSource, testGidSource, testAddrSource),
			testMetaTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
			testMetaC:      newTargetRef(testMetaC, testGidC, testAddrC),
		},
	})
	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))
	return router, agg
}

// seedFriendGraph creates: source↔target (friendSince 1002), source↔a
// (friendSince 1003), source↔b (friendSince 1004), plus one-sided c→source
// and source→d-follow edges.
func seedFriendGraph(t *testing.T, agg *Aggregator) {
	t.Helper()
	pins := []struct {
		id, follower, followerMeta, followerAddr, target string
		ts                                               int64
	}{
		{"follow-st1:i0", testGidSource, testMetaSource, testAddrSource, testGidTarget, 1001},
		{"follow-ts1:i0", testGidTarget, testMetaTarget, testAddrTarget, testGidSource, 1002},
		{"follow-sa1:i0", testGidSource, testMetaSource, testAddrSource, testGidA, 1002},
		{"follow-as1:i0", testGidA, testMetaA, testAddrA, testGidSource, 1003},
		{"follow-sb1:i0", testGidSource, testMetaSource, testAddrSource, testGidB, 1003},
		{"follow-bs1:i0", testGidB, testMetaB, testAddrB, testGidSource, 1004},
		{"follow-cs1:i0", testGidC, testMetaC, testAddrC, testGidSource, 1005},
	}
	for _, pin := range pins {
		if _, err := agg.HandleBlockPin(followPin(pin.id, pin.follower, pin.followerMeta, pin.followerAddr, pin.target, pin.ts)); err != nil {
			t.Fatalf("HandleBlockPin(%s): %v", pin.id, err)
		}
	}
}

func TestFriendsEndpointCompactAndProfileShapes(t *testing.T) {
	router, agg := newFriendsFixture(t)
	seedFriendGraph(t, agg)

	status, body := callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/friends")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("friends compact code = %v status=%d; body=%v", body["code"], status, body)
	}
	data := mustObject(t, body, "data")
	list := mustList(t, data, "list")
	if len(list) != 3 {
		t.Fatalf("compact list len = %d, want 3; data=%v", len(list), data)
	}
	// Newest friendSince first: b (1004), a (1003), target (1002).
	item := mustObjectFromList(t, list, 0)
	if item["globalMetaId"] != testGidB {
		t.Fatalf("list[0].globalMetaId = %v, want %s", item["globalMetaId"], testGidB)
	}
	if item["name"] != "B" || item["nameId"] != "name-b:i0" || item["avatarId"] != "avatar-b:i0" {
		t.Fatalf("list[0] compact identity = %v", item)
	}
	for _, field := range []string{"bio", "bioId", "followedAt", "followPinId", "friendSince"} {
		if _, ok := item[field]; ok {
			t.Fatalf("compact response leaked %s: %v", field, item)
		}
	}

	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/friends?view=profile")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("friends profile code = %v; body=%v", body["code"], body)
	}
	list = mustList(t, mustObject(t, body, "data"), "list")
	if len(list) != 3 {
		t.Fatalf("profile list len = %d, want 3", len(list))
	}
	item = mustObjectFromList(t, list, 2) // oldest friend = target
	if item["globalMetaId"] != testGidTarget {
		t.Fatalf("profile list[2].globalMetaId = %v, want %s", item["globalMetaId"], testGidTarget)
	}
	if item["bio"] != "target bio" || item["bioId"] != "bio-target:i0" {
		t.Fatalf("profile list[2] bio fields = %v", item)
	}
	if item["followPinId"] != "follow-st1:i0" || int64(item["followedAt"].(float64)) != 1001 {
		t.Fatalf("profile list[2] subject→peer edge = %v, want follow-st1:i0 at 1001", item)
	}
	if int64(item["friendSince"].(float64)) != 1002 {
		t.Fatalf("profile list[2].friendSince = %v, want 1002", item["friendSince"])
	}
}

func TestFriendsEndpointPaginationAndStaleCursor(t *testing.T) {
	router, agg := newFriendsFixture(t)
	seedFriendGraph(t, agg)

	status, body := callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/friends?size=2")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("page1 code = %v; body=%v", body["code"], body)
	}
	data := mustObject(t, body, "data")
	if size := int(data["size"].(float64)); size != 2 {
		t.Fatalf("page1 size = %d, want 2", size)
	}
	list := mustList(t, data, "list")
	if len(list) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(list))
	}
	if mustObjectFromList(t, list, 0)["globalMetaId"] != testGidB || mustObjectFromList(t, list, 1)["globalMetaId"] != testGidA {
		t.Fatalf("page1 order = %v, want [b, a]", list)
	}
	cursor, _ := data["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("page1 nextCursor empty, want non-empty")
	}

	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/friends?size=2&cursor="+cursor)
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("page2 code = %v; body=%v", body["code"], body)
	}
	data = mustObject(t, body, "data")
	list = mustList(t, data, "list")
	if len(list) != 1 || mustObjectFromList(t, list, 0)["globalMetaId"] != testGidTarget {
		t.Fatalf("page2 = %v, want [target]", list)
	}
	if data["nextCursor"] != "" {
		t.Fatalf("page2 nextCursor = %v, want empty", data["nextCursor"])
	}

	// A cursor whose anchor entry disappeared is stale → 40000. The anchor
	// is the last returned entry (a, friendSince 1003): revoking the a→source
	// edge removes that friendship entirely.
	if _, err := agg.HandleBlockPin(revokePin("revoke-as1:i0", "follow-as1:i0", 2000)); err != nil {
		t.Fatalf("HandleBlockPin(revoke): %v", err)
	}
	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/friends?size=2&cursor="+cursor)
	if status != http.StatusOK || int(body["code"].(float64)) != 40000 {
		t.Fatalf("stale cursor code = %v, want 40000; body=%v", body["code"], body)
	}
}

func TestFriendsEndpointSubjectNormalization(t *testing.T) {
	router, agg := newFriendsFixture(t)
	seedFriendGraph(t, agg)

	// Unknown-format subject: valid IDAddress with no indexed presence is
	// total (200 + empty list), per §4.3's totality principle.
	status, body := callSocial(t, router, "/api/social/globalmetaid/"+testGidMissing+"/friends")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("unknown subject code = %v, want 0; body=%v", body["code"], body)
	}
	if list := mustList(t, mustObject(t, body, "data"), "list"); len(list) != 0 {
		t.Fatalf("unknown subject list = %v, want empty", list)
	}

	// A legacy hex subject resolves through the profile archive.
	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testMetaSource+"/friends")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("hex subject code = %v, want 0; body=%v", body["code"], body)
	}
	if list := mustList(t, mustObject(t, body, "data"), "list"); len(list) != 3 {
		t.Fatalf("hex subject list len = %d, want 3", len(list))
	}

	// Garbage subject does not normalize → 40400.
	status, body = callSocial(t, router, "/api/social/globalmetaid/not-an-identity/friends")
	if status != http.StatusOK || int(body["code"].(float64)) != 40400 {
		t.Fatalf("garbage subject code = %v, want 40400", body["code"])
	}

	// Shared pagination contract still applies.
	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/friends?view=full")
	if status != http.StatusOK || int(body["code"].(float64)) != 40000 {
		t.Fatalf("invalid view code = %v, want 40000", body["code"])
	}
}

func TestCountsEndpointFiveCountersWithBlockFilter(t *testing.T) {
	router, agg := newFriendsFixture(t)
	seedFriendGraph(t, agg) // followers: target,a,b (3); following: target,a,b (3); friends: 3; c→source is 4th follower? no: c→source adds follower
	// Graph: source↔target, source↔a, source↔b (mutual ×3); c→source (follower).
	// following = {target, a, b} = 3; followers = {target, a, b, c} = 4.

	status, body := callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/counts")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("counts code = %v; body=%v", body["code"], body)
	}
	data := mustObject(t, body, "data")
	if int(data["followers"].(float64)) != 4 || int(data["following"].(float64)) != 3 || int(data["friends"].(float64)) != 3 {
		t.Fatalf("unfiltered counts = %v, want followers 4 / following 3 / friends 3", data)
	}
	if int(data["blockedCount"].(float64)) != 0 || int(data["blockedByCount"].(float64)) != 0 {
		t.Fatalf("no-op default produced non-zero block counters: %v", data)
	}

	// Filtered view: subject blocks b (drops following + friends by one),
	// c blocks the subject (drops the follower).
	agg.SetBlockLister(fakeBlockLister{
		blocking:  map[string][]string{testGidSource: {testGidB}},
		blockedBy: map[string][]string{testGidSource: {testGidC}},
	})
	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/counts")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("filtered counts code = %v; body=%v", body["code"], body)
	}
	data = mustObject(t, body, "data")
	// b drops following and friends by one; c drops followers by one
	// (b and c both sat in the follower set).
	if int(data["followers"].(float64)) != 2 || int(data["following"].(float64)) != 2 || int(data["friends"].(float64)) != 2 {
		t.Fatalf("filtered counts = %v, want followers 2 / following 2 / friends 2", data)
	}
	if int(data["blockedCount"].(float64)) != 1 || int(data["blockedByCount"].(float64)) != 1 {
		t.Fatalf("block counters = %v, want 1/1", data)
	}

	// Reset to the no-op default restores the unfiltered view.
	agg.SetBlockLister(nil)
	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/counts")
	if status != http.StatusOK {
		t.Fatalf("counts after reset status = %d", status)
	}
	data = mustObject(t, body, "data")
	if int(data["followers"].(float64)) != 4 || int(data["friends"].(float64)) != 3 {
		t.Fatalf("counts after reset = %v, want restored", data)
	}
}

func TestListEndpointsExcludeBlockedPairs(t *testing.T) {
	router, agg := newFriendsFixture(t)
	seedFriendGraph(t, agg)

	agg.SetBlockLister(fakeBlockLister{
		blocking: map[string][]string{testGidSource: {testGidA}},
	})

	status, body := callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/following")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("following code = %v; body=%v", body["code"], body)
	}
	list := mustList(t, mustObject(t, body, "data"), "list")
	if len(list) != 2 {
		t.Fatalf("following after block filter len = %d, want 2 (a excluded); list=%v", len(list), list)
	}
	for i := range list {
		if mustObjectFromList(t, list, i)["globalMetaId"] == testGidA {
			t.Fatalf("blocked peer leaked into following list: %v", list)
		}
	}

	// The friends read path goes through the same filter point.
	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/friends")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("friends code = %v; body=%v", body["code"], body)
	}
	list = mustList(t, mustObject(t, body, "data"), "list")
	if len(list) != 2 {
		t.Fatalf("friends after block filter len = %d, want 2 (a excluded)", len(list))
	}

	// Pagination still terminates correctly across the filtered-out entry.
	status, body = callSocial(t, router, "/api/social/globalmetaid/"+testGidSource+"/following?size=1")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("following page1 code = %v; body=%v", body["code"], body)
	}
	data := mustObject(t, body, "data")
	if cursor, _ := data["nextCursor"].(string); cursor == "" {
		t.Fatal("following page1 nextCursor empty with more entries remaining")
	}
}

func TestBatchRelationshipsFoldAndBooleans(t *testing.T) {
	router, agg := newFriendsFixture(t)
	seedFriendGraph(t, agg)

	gidD, metaD, addrD := testIdentity("d")
	_, metaGhost, _ := testIdentity("ghost")

	// d is mutual AND blocked by the subject: blocked wins the fold.
	if _, err := agg.HandleBlockPin(followPin("follow-sd1:i0", testGidSource, testMetaSource, testAddrSource, gidD, 1100)); err != nil {
		t.Fatalf("HandleBlockPin(source→d): %v", err)
	}
	if _, err := agg.HandleBlockPin(followPin("follow-ds1:i0", gidD, metaD, addrD, testGidSource, 1101)); err != nil {
		t.Fatalf("HandleBlockPin(d→source): %v", err)
	}
	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			testGidSource: {MetaId: testMetaSource, GlobalMetaId: testGidSource, Address: testAddrSource},
			testGidTarget: {MetaId: testMetaTarget, GlobalMetaId: testGidTarget, Address: testAddrTarget},
			testGidA:      {MetaId: testMetaA, GlobalMetaId: testGidA, Address: testAddrA},
			testGidB:      {MetaId: testMetaB, GlobalMetaId: testGidB, Address: testAddrB},
			testGidC:      {MetaId: testMetaC, GlobalMetaId: testGidC, Address: testAddrC},
			gidD:          {MetaId: metaD, GlobalMetaId: gidD, Address: addrD},
		},
		byMetaId: map[string]*TargetRef{
			testMetaC:      newTargetRef(testMetaC, testGidC, testAddrC),
			testMetaTarget: newTargetRef(testMetaTarget, testGidTarget, testAddrTarget),
		},
	})
	agg.SetBlockLister(fakeBlockLister{
		blocking:  map[string][]string{testGidSource: {gidD}},
		blockedBy: map[string][]string{testGidSource: {testGidB}},
	})

	status, body := callSocialPost(t, router, "/api/social/relationships/batch", map[string]any{
		"sourceGlobalMetaId": testGidSource,
		"targets":            []string{testGidTarget, testGidA, testGidB, testGidC, gidD, testMetaC, metaGhost},
	})
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("batch code = %v; body=%v", body["code"], body)
	}
	list := mustList(t, mustObject(t, body, "data"), "list")
	if len(list) != 7 {
		t.Fatalf("batch list len = %d, want 7", len(list))
	}

	expect := []struct {
		gid      string
		relation string
	}{
		{testGidTarget, RelationFriend},
		{testGidA, RelationFriend},
		{testGidB, RelationBlocked},   // follower but blockedBy beats follower
		{testGidC, RelationFollower},  // c→source only
		{gidD, RelationBlocked},       // mutual but blocking beats friend
		{testGidC, RelationFollower},  // hex form of c echoes canonical
		{metaGhost, RelationStranger}, // unresolved hex: total semantics, raw echo
	}
	for i, want := range expect {
		item := mustObjectFromList(t, list, i)
		if item["globalMetaId"] != want.gid {
			t.Fatalf("batch[%d].globalMetaId = %v, want %s (order must match request)", i, item["globalMetaId"], want.gid)
		}
		if item["relation"] != want.relation {
			t.Fatalf("batch[%d].relation = %v, want %s; item=%v", i, item["relation"], want.relation, item)
		}
	}

	// Structured booleans keep direction detail the fold throws away.
	item := mustObjectFromList(t, list, 2) // b: mutual but blockedBy → folded blocked
	if item["followsMe"] != true || item["followedByMe"] != true || item["isFriend"] != true {
		t.Fatalf("batch[b] booleans = %v, want mutual (booleans report, fold overrides)", item)
	}
	if item["blocking"] != false || item["blockedBy"] != true {
		t.Fatalf("batch[b] block booleans = %v, want blockedBy=true", item)
	}
	item = mustObjectFromList(t, list, 4) // d
	if item["followsMe"] != true || item["followedByMe"] != true || item["isFriend"] != true {
		t.Fatalf("batch[d] booleans = %v, want mutual", item)
	}
	if item["blocking"] != true || item["blockedBy"] != false {
		t.Fatalf("batch[d] block booleans = %v, want blocking=true", item)
	}
}

func TestBatchRelationshipsValidationErrors(t *testing.T) {
	router, _ := newFriendsFixture(t)

	cases := []struct {
		name     string
		payload  any
		wantCode int
	}{
		{"empty targets", map[string]any{"sourceGlobalMetaId": testGidSource, "targets": []string{}}, 40000},
		{"too many targets", map[string]any{"sourceGlobalMetaId": testGidSource, "targets": make([]string, 101)}, 40000},
		{"garbage target", map[string]any{"sourceGlobalMetaId": testGidSource, "targets": []string{"garbage"}}, 40000},
		{"address target rejected", map[string]any{"sourceGlobalMetaId": testGidSource, "targets": []string{testAddrTarget}}, 40000},
		{"unresolvable source", map[string]any{"sourceGlobalMetaId": "garbage", "targets": []string{testGidTarget}}, 40400},
		{"missing source", map[string]any{"targets": []string{testGidTarget}}, 40400},
		{"malformed body", `{"sourceGlobalMetaId":`, 40000},
	}
	for _, tc := range cases {
		status, body := callSocialPost(t, router, "/api/social/relationships/batch", tc.payload)
		if status != http.StatusOK {
			t.Fatalf("%s: HTTP status = %d, want 200", tc.name, status)
		}
		if code := int(body["code"].(float64)); code != tc.wantCode {
			t.Fatalf("%s: code = %d, want %d; body=%v", tc.name, code, tc.wantCode, body)
		}
	}
}

func TestBatchRelationshipsSourceAcceptsHexForm(t *testing.T) {
	router, agg := newFriendsFixture(t)
	seedFriendGraph(t, agg)

	status, body := callSocialPost(t, router, "/api/social/relationships/batch", map[string]any{
		"sourceGlobalMetaId": testMetaSource,
		"targets":            []string{testGidTarget},
	})
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("batch hex-source code = %v; body=%v", body["code"], body)
	}
	list := mustList(t, mustObject(t, body, "data"), "list")
	if len(list) != 1 {
		t.Fatalf("batch list len = %d, want 1", len(list))
	}
	item := mustObjectFromList(t, list, 0)
	if item["relation"] != RelationFriend || item["isFriend"] != true {
		t.Fatalf("batch hex-source item = %v, want friend", item)
	}
}
