package preference

import (
	"errors"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// TestCorruptRecordErrorPropagatesAndDoesNotDemote injects a storage-level
// failure (corrupt record bytes → non-ErrNotFound load error) and proves a
// mempool echo neither demotes the confirmed record nor silently passes.
func TestCorruptRecordErrorPropagatesAndDoesNotDemote(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	opts := feedbackPinOpts{PinId: "fb-corrupt:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1000, Height: 10}
	mustFeedbackBlock(t, pref, opts)
	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 1 {
		t.Fatalf("baseline: %+v", summary)
	}

	key := openFeedbackKey("mvc", "fb-corrupt:i0")
	original, err := store.Get(Namespace, key)
	if err != nil || original == nil {
		t.Fatalf("load stored record: %v", err)
	}
	// Corrupt the stored record: unmarshalling into FeedbackRecord fails —
	// a genuine storage error, not key absence.
	if err := store.Set(Namespace, key, []byte(`"corrupt"`)); err != nil {
		t.Fatalf("inject corruption: %v", err)
	}

	if _, err := pref.HandleMempoolPin(makeFeedbackPin(opts)); err == nil {
		t.Fatal("a load error must propagate, never silently pass")
	} else if !strings.Contains(err.Error(), "corrupt record") {
		t.Fatalf("error should identify the corrupt record, got: %v", err)
	}

	// Restore the original bytes: the failed echo must have written nothing,
	// so the confirmed record and its score are intact.
	if err := store.Set(Namespace, key, original); err != nil {
		t.Fatalf("restore record: %v", err)
	}
	summary := pref.OpenSummaryFor("mvc", shaA)
	if summary.Comparisons != 1 || summary.Judges != 1 {
		t.Fatalf("confirmed evidence must be intact after the failed echo: %+v", summary)
	}
	view, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(view.Items) != 1 || view.Items[0].IsMempool {
		t.Fatalf("record must still be confirmed: %+v", view.Items)
	}
}

// TestBackfillSinglePinFailureContinuesAndRetries verifies the failure
// semantics: one failing pin is counted and skipped, the run finishes the
// remaining pins, exits non-zero with the failure list, and the next run
// (idempotent full rescan) converges.
func TestBackfillSinglePinFailureContinuesAndRetries(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	pins := []map[string]any{
		manapiFeedbackPin("fb-r1:i0", 1000, abBatch("a", "")),
		manapiFeedbackPin("fb-r2:i0", 2000, abBatch("a", "")),
		manapiFeedbackPin("fb-r3:i0", 3000, abBatch("b", "")),
	}
	server := newPreferenceMANAPIServer(t, pins)
	client := publishedcontent.NewBackfillClient(server.URL, nil)

	// Run 1: the middle pin fails; the others must still apply.
	pref.backfillApplyPin = func(pin *aggregator.PinInscription) error {
		if pin.Id == "fb-r2:i0" {
			return errors.New("injected storage failure")
		}
		return pref.processPin(pin, false)
	}
	stats := &BackfillStats{}
	err := pref.Backfill(BackfillOptions{Client: client, Stats: stats})
	if err == nil || !strings.Contains(err.Error(), "fb-r2:i0") {
		t.Fatalf("run 1 must exit non-zero naming the failed pin: %v", err)
	}
	chainStats := stats.chain(PathPreferenceFeedback, "mvc")
	if chainStats.Fetched != 3 || chainStats.Applied != 2 || chainStats.Errors != 1 {
		t.Fatalf("run 1 stats: %+v", chainStats)
	}
	// Only judges 1 and 3 scored; judge 2's pin was skipped.
	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 2 || summary.Judges != 2 {
		t.Fatalf("run 1 scores: %+v", summary)
	}

	// Run 2: clean retry converges (the failed pin replays).
	pref.backfillApplyPin = nil
	if err := pref.Backfill(BackfillOptions{Client: client}); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	summary := pref.OpenSummaryFor("mvc", shaA)
	if summary.Comparisons != 3 || summary.Judges != 3 {
		t.Fatalf("run 2 must converge to 3 judges: %+v", summary)
	}
}

// TestChainMismatchExcluded gives ExclusionChainMismatch a direct test: the
// feedback pin's chain differs from the refs' declared chain.
func TestChainMismatchExcluded(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	// Both refs declare btc while the pin arrives on mvc; the pair itself is
	// same-chain so only the runtime chain check can reject it.
	btcA := `{"chainName":"btc","app":"metaapp://app-root-a:i0","version":"metaapp://app-root-a:i0","video":"metafile://video-a:i0","videoSha256":"` + shaA + `"}`
	btcB := `{"chainName":"btc","app":"metaapp://app-root-b:i0","version":"metaapp://app-root-b:i0","video":"metafile://video-b:i0","videoSha256":"` + shaB + `"}`
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-chain:i0", Signer: "gid-judge-1", Body: batchJSON(`"motion-video"`, itemJSON(btcA, btcB, "a")), Timestamp: 1000, Height: 10})

	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 0 {
		t.Fatalf("chain-mismatched refs must not score: %+v", summary)
	}
	if summary := pref.OpenSummaryFor("btc", shaA); summary.Comparisons != 0 {
		t.Fatalf("chain-mismatched refs must not score on the declared chain either: %+v", summary)
	}
	excludedView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeExcluded: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(excludedView.Items) != 1 || excludedView.Items[0].ExclusionReason != ExclusionChainMismatch {
		t.Fatalf("chain-mismatch reason: %+v", excludedView.Items)
	}
}

// TestRegistryDispatchFeedsPreferenceAggregator proves the full dispatch
// chain the acceptance review asked for: block and mempool pins routed
// through aggregator.Registry reach the preference aggregator and produce
// scores/evidence — not just direct handler calls.
func TestRegistryDispatchFeedsPreferenceAggregator(t *testing.T) {
	store := storage.NewPebbleStore(t.TempDir())
	defer store.Close()
	cacheProvider := cache.New(store)
	registry := aggregator.NewRegistry(store, cacheProvider)

	pub := &publishedcontent.Aggregator{}
	if err := registry.Register(pub); err != nil {
		t.Fatalf("register publishedcontent: %v", err)
	}
	pref := &Aggregator{}
	pref.SetMetaAppResolver(NewMetaAppResolverAdapter(pub))
	if err := registry.Register(pref); err != nil {
		t.Fatalf("register preference: %v", err)
	}
	pub.SetPreferenceLookup(pref)

	shaA2 := strings.Repeat("aa", 32)
	shaB2 := strings.Repeat("bb", 32)
	metaAppPin := func(pinID, videoSha string, ts int64) *aggregator.PinInscription {
		payload := `{"title":"Reg MV ` + pinID + `","tags":["motion-video"],"runtime":"browser","prompt":"p","metadata":{"series":"motion-video","videoFile":"metafile://` + pinID + `-video:i0","videoSha256":"` + videoSha + `","durationSec":10,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`
		return &aggregator.PinInscription{
			Id: pinID, Path: "/protocols/metaapp", Operation: "create", ChainName: "mvc",
			ContentType: "application/json", ContentBody: []byte(payload),
			GlobalMetaId: "gid-reg-author", MetaId: "gid-reg-author", Address: "gid-reg-author",
			Timestamp: ts,
		}
	}
	registry.RouteBlockPin(metaAppPin("reg-mv-a:i0", shaA2, 1000))
	registry.RouteBlockPin(metaAppPin("reg-mv-b:i0", shaB2, 1100))

	batch := `{"category":["motion-video"],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0","mode":"arena"},"items":[` +
		`{"a":{"chainName":"mvc","app":"metaapp://reg-mv-a:i0","version":"metaapp://reg-mv-a:i0","video":"metafile://reg-mv-a:i0-video:i0","videoSha256":"` + shaA2 + `"},` +
		`"b":{"chainName":"mvc","app":"metaapp://reg-mv-b:i0","version":"metaapp://reg-mv-b:i0","video":"metafile://reg-mv-b:i0-video:i0","videoSha256":"` + shaB2 + `"},` +
		`"pick":"a"}]}`

	registry.RouteBlockPin(&aggregator.PinInscription{
		Id: "reg-fb-1:i0", Path: PathPreferenceFeedback, Operation: "create", ChainName: "mvc",
		ContentType: "application/json", ContentBody: []byte(batch),
		GlobalMetaId: "gid-reg-judge-1", MetaId: "gid-reg-judge-1", Address: "gid-reg-judge-1",
		Timestamp: 1200, GenesisHeight: 30,
	})
	// Mempool dispatch stays score-neutral.
	registry.RouteMempoolPin(&aggregator.PinInscription{
		Id: "reg-fb-mem:i0", Path: PathPreferenceFeedback, Operation: "create", ChainName: "mvc",
		ContentType: "application/json", ContentBody: []byte(batch),
		GlobalMetaId: "gid-reg-judge-2", MetaId: "gid-reg-judge-2", Address: "gid-reg-judge-2",
		Timestamp: 1300,
	})

	summary := pref.OpenSummaryFor("mvc", shaA2)
	if summary.Comparisons != 1 || summary.Score == nil {
		t.Fatalf("registry-dispatched block pin must score: %+v", summary)
	}
	// And the lookup wiring feeds the publishedcontent list projection.
	list, err := pub.ListMetaApps(publishedcontent.MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", Basis: "open", Order: "desc",
	})
	if err != nil {
		t.Fatalf("preference list: %v", err)
	}
	if len(list.Items) != 2 || list.Items[0].PinID != "reg-mv-a:i0" {
		t.Fatalf("preference list through registry wiring: %+v", list.Items)
	}
	if open := list.Items[0].Preference.Open; open.Comparisons != 1 {
		t.Fatalf("list must read dispatched evidence: %+v", open)
	}
}
