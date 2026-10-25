package preference

import (
	"encoding/json"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// stripStoredCreator simulates a legacy record written before the Creator*
// fields existed by deleting them from the stored publishedcontent record.
func stripStoredCreator(t *testing.T, store *storage.PebbleStore, rootPinId string) {
	t.Helper()
	key := []byte("record:mvc:/protocols/metaapp:" + rootPinId)
	raw, err := store.Get("publishedcontent", key)
	if err != nil || raw == nil {
		t.Fatalf("load legacy record: %v %v", raw, err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal record: %v", err)
	}
	delete(rec, "creatorGlobalMetaId")
	delete(rec, "creatorMetaId")
	delete(rec, "creatorAddress")
	updated, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal record: %v", err)
	}
	if err := store.Set("publishedcontent", key, updated); err != nil {
		t.Fatalf("rewrite record: %v", err)
	}
}

// TestLegacyCreatorBackfilledOnCreateReplayConflict is the acceptance-review
// regression: a legacy record without Creator*, folded by a third-party
// modify, must regain its creator when the create pin replays — and the
// author-conflict check must then exclude the creator's own-judgment (which
// the pre-migration approximation missed).
func TestLegacyCreatorBackfilledOnCreateReplayConflict(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	appA, _ := twoAppsAB(t, pub)

	// Make app A "legacy" and fold a third-party modify on top.
	stripStoredCreator(t, store, appA.rootPinId)
	mustPubProcess(t, pub, &aggregator.PinInscription{
		Id: "app-root-a-v2:i0", Path: "/protocols/metaapp@app-root-a:i0", Operation: "modify", ChainName: "mvc",
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"MV v2","tags":["motion-video"],"runtime":"browser","prompt":"p","metadata":{"series":"motion-video","videoFile":"metafile://video-a:i0","videoSha256":"` + shaA + `","durationSec":12,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`),
		GlobalMetaId: "gid-modifier", MetaId: "gid-modifier", Address: "gid-modifier",
		Timestamp: 1050,
	})
	currentRef := `{"chainName":"mvc","app":"metaapp://app-root-a:i0","version":"metaapp://app-root-a-v2:i0","video":"metafile://video-a:i0","videoSha256":"` + shaA + `"}`

	// Pre-migration gap: the creator judging their own work slips through,
	// because the folded publisher is the modifier and creator is unknown.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-legacy:i0", Signer: "gid-author-a", Body: batchJSON(`"motion-video"`, itemJSON(currentRef, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 1100, Height: 11})
	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 1 {
		t.Fatalf("pre-migration: creator self-judgment should slip through the approximation: %+v", summary)
	}

	// The create pin replays (backfill re-run): creator is backfilled onto
	// the legacy record.
	publishMotionVideoApp(t, pub, appA) // same create pin replayed
	snapshot, ok := pub.ResolveMetaAppSnapshot("mvc", appA.rootPinId)
	if !ok || snapshot.CreatorGlobalMetaId != "gid-author-a" {
		t.Fatalf("create-pin replay must backfill the creator: %+v", snapshot)
	}

	// Startup re-evaluation flips the comparison to an author conflict.
	pref = reinitPreference(t, store, pub)
	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 0 {
		t.Fatalf("post-migration: creator self-judgment must be excluded: %+v", summary)
	}
	excludedView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeExcluded: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(excludedView.Items) != 1 || excludedView.Items[0].ExclusionReason != ExclusionAuthorConflict {
		t.Fatalf("post-migration exclusion: %+v", excludedView.Items)
	}
}

// TestInitMigratesCreateOnlyLegacyCreator verifies the startup migration:
// legacy create-only records get creator := folded publisher (exact, since
// no modify ever folded the record).
func TestInitMigratesCreateOnlyLegacyCreator(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()
	cacheProvider := cache.New(store)
	pub := &publishedcontent.Aggregator{}
	if err := pub.Init(store, cacheProvider); err != nil {
		t.Fatalf("publishedcontent Init: %v", err)
	}
	appA := motionVideoApp{rootPinId: "app-legacy-co:i0", versionPin: "app-legacy-co:i0", videoRef: "video-co:i0", videoSha: shaA, chainName: "mvc", publisherId: "gid-legacy-publisher"}
	publishMotionVideoApp(t, pub, appA)
	stripStoredCreator(t, store, appA.rootPinId)

	// A fresh startup migrates the create-only legacy record.
	fresh := &publishedcontent.Aggregator{}
	if err := fresh.Init(store, cacheProvider); err != nil {
		t.Fatalf("fresh Init: %v", err)
	}
	snapshot, ok := fresh.ResolveMetaAppSnapshot("mvc", appA.rootPinId)
	if !ok {
		t.Fatal("snapshot must resolve")
	}
	if snapshot.CreatorGlobalMetaId != "gid-legacy-publisher" || snapshot.CreatorMetaId != "gid-legacy-publisher" {
		t.Fatalf("startup migration must set creator := publisher for create-only records: %+v", snapshot)
	}
}
