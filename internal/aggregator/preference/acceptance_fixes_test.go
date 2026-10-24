package preference

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// reinitPreference re-creates the aggregator over the same store (startup
// path) with the resolver wired, exercising Init's resolution re-evaluation.
func reinitPreference(t *testing.T, store *storage.PebbleStore, pub *publishedcontent.Aggregator) *Aggregator {
	t.Helper()
	fresh := &Aggregator{}
	fresh.SetMetaAppResolver(NewMetaAppResolverAdapter(pub))
	if err := fresh.Init(store, cache.New(store)); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	return fresh
}

// TestExclusionSelfHealsWhenAppIndexedLater covers the acceptance-review
// case: a feedback pin indexed before its target app records an
// app-not-found exclusion; once the app is indexed, the startup/backfill
// re-evaluation must clear the exclusion and the score must appear.
func TestExclusionSelfHealsWhenAppIndexedLater(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	appB := motionVideoApp{rootPinId: "app-late-b:i0", versionPin: "app-late-b:i0", videoRef: "video-lb:i0", videoSha: shaB, chainName: "mvc", publisherId: "gid-author-lb"}

	// Feedback arrives while only app A is indexed.
	appA := motionVideoApp{rootPinId: "app-late-a:i0", versionPin: "app-late-a:i0", videoRef: "video-la:i0", videoSha: shaA, chainName: "mvc", publisherId: "gid-author-la"}
	publishMotionVideoApp(t, pub, appA)
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-late:i0", Signer: "gid-judge-1", Body: batchJSON(`"motion-video"`, itemJSON(appA.ref(), appB.ref(), "a")), Timestamp: 1000, Height: 10})

	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 0 {
		t.Fatalf("unindexed app B must exclude the pair: %+v", summary)
	}
	excludedView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeExcluded: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(excludedView.Items) != 1 || excludedView.Items[0].ExclusionReason != ExclusionAppNotFound {
		t.Fatalf("exclusion reason: %+v", excludedView.Items)
	}

	// The app lands on chain later; the next startup re-evaluation heals.
	publishMotionVideoApp(t, pub, appB)
	pref = reinitPreference(t, store, pub)

	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 1 || summary.Score == nil {
		t.Fatalf("healed exclusion must score: %+v", summary)
	}
	defaultView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(defaultView.Items) != 1 || defaultView.Items[0].Excluded {
		t.Fatalf("healed comparison must be eligible: %+v", defaultView.Items)
	}
}

// TestCreatorAuthorConflict covers the acceptance-review case D: after a
// third-party modify, the folded publisher tracks the modifier — the creator
// must still be conflict-excluded (via retained creator identity) and the
// modifier too.
func TestCreatorAuthorConflict(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	// A third party modifies app A: folded publisher becomes the modifier,
	// creator identity must survive the fold.
	mustPubProcess(t, pub, &aggregator.PinInscription{
		Id: "app-root-a-v2:i0", Path: "/protocols/metaapp@app-root-a:i0", Operation: "modify", ChainName: "mvc",
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"MV app-root-a:i0 v2","tags":["motion-video"],"runtime":"browser","prompt":"p","metadata":{"series":"motion-video","videoFile":"metafile://video-a:i0","videoSha256":"` + shaA + `","durationSec":12,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`),
		GlobalMetaId: "gid-modifier", MetaId: "gid-modifier", Address: "gid-modifier",
		Timestamp: 1050,
	})
	currentRef := `{"chainName":"mvc","app":"metaapp://app-root-a:i0","version":"metaapp://app-root-a-v2:i0","video":"metafile://video-a:i0","videoSha256":"` + shaA + `"}`

	// The CREATOR judges their own work: conflict via creator identity even
	// though the folded publisher is now the modifier.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-creator:i0", Signer: "gid-author-a", Body: batchJSON(`"motion-video"`, itemJSON(currentRef, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 1100, Height: 11})
	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 0 {
		t.Fatalf("creator judging own work must stay excluded after a third-party modify: %+v", summary)
	}

	// The MODIFIER judging also conflicts (folded publisher identity).
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-modifier:i0", Signer: "gid-modifier", Body: batchJSON(`"motion-video"`, itemJSON(currentRef, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 1150, Height: 12})
	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 0 {
		t.Fatalf("modifier judging the app must stay excluded: %+v", summary)
	}

	// An unrelated judge is eligible despite the modify fold.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-neutral:i0", Signer: "gid-judge-9", Body: batchJSON(`"motion-video"`, itemJSON(currentRef, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 1200, Height: 13})
	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 1 {
		t.Fatalf("neutral judge must be eligible: %+v", summary)
	}
}

// TestCurrentManifestMissingVideoDeclarationsIsMismatch covers
// acceptance-review case C: a current-version ref whose folded manifest
// lacks videoFile/videoSha256 declarations is a mismatch, never an implicit
// pass.
func TestCurrentManifestMissingVideoDeclarationsIsMismatch(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	// App without manifest video declarations.
	mustPubProcess(t, pub, &aggregator.PinInscription{
		Id: "app-undeclared:i0", Path: "/protocols/metaapp", Operation: "create", ChainName: "mvc",
		ContentType:  "application/json",
		ContentBody:  []byte(`{"title":"Undeclared MV","tags":["motion-video"],"metadata":{"series":"motion-video"}}`),
		GlobalMetaId: "gid-author-undeclared", MetaId: "gid-author-undeclared", Address: "gid-author-undeclared",
		Timestamp: 900,
	})
	undeclaredRef := `{"chainName":"mvc","app":"metaapp://app-undeclared:i0","version":"metaapp://app-undeclared:i0","video":"metafile://any-video:i0","videoSha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}`
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-undeclared:i0", Signer: "gid-judge-1", Body: batchJSON(`"motion-video"`, itemJSON(undeclaredRef, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 1000, Height: 10})

	excludedView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeExcluded: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	found := false
	for _, item := range excludedView.Items {
		if item.A.App == "metaapp://app-undeclared:i0" {
			found = true
			if item.ExclusionReason != ExclusionArtifactMismatch {
				t.Fatalf("missing declarations must be artifact-mismatch, got %q", item.ExclusionReason)
			}
		}
	}
	if !found {
		t.Fatalf("undeclared comparison missing from diagnostics: %+v", excludedView.Items)
	}
}

// TestManifestUnverifiedFlaggedForHistoricalVersions covers
// acceptance-review case B: historical (non-current) version refs are
// surfaced with manifestUnverified=true; current-version refs are not.
func TestManifestUnverifiedFlaggedForHistoricalVersions(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	appA, _ := twoAppsAB(t, pub)
	modifyMotionVideoAppVersion(t, pub, appA, "app-root-a-v2:i0", "video-a:i0", shaA)

	historicalRef := `{"chainName":"mvc","app":"metaapp://app-root-a:i0","version":"metaapp://app-root-a:i0","video":"metafile://video-a:i0","videoSha256":"` + shaA + `"}`
	currentRef := `{"chainName":"mvc","app":"metaapp://app-root-a:i0","version":"metaapp://app-root-a-v2:i0","video":"metafile://video-a:i0","videoSha256":"` + shaA + `"}`
	body := batchJSON(`"motion-video"`,
		itemJSON(historicalRef, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a"),
		itemJSON(currentRef, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a"),
	)
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-hist:i0", Signer: "gid-judge-1", Body: body, Timestamp: 1000, Height: 10})

	view, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(view.Items) != 2 {
		t.Fatalf("both comparisons eligible: %+v", view.Items)
	}
	unverified := 0
	for _, item := range view.Items {
		if item.ManifestUnverified {
			unverified++
			if item.Excluded {
				t.Fatalf("unverified is a label, not an exclusion: %+v", item)
			}
		}
	}
	if unverified != 1 {
		t.Fatalf("exactly one historical-version item must be flagged: %+v", view.Items)
	}
}

// TestLazyScoreRebuildOncePerBurst guards the O(N²) regression: a burst of
// confirmed pins triggers exactly one snapshot rebuild on the next read, and
// a backfill run pays one rebuild in total.
func TestLazyScoreRebuildOncePerBurst(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	// Init already warmed once; writes only mark dirty.
	before := pref.openScoreRebuildCount()
	for i := 0; i < 5; i++ {
		mustFeedbackBlock(t, pref, feedbackPinOpts{
			PinId:     "fb-burst-" + string(rune('0'+i)) + ":i0",
			Signer:    "gid-judge-" + string(rune('0'+i)),
			Body:      abBatch("a", ""),
			Timestamp: int64(1000 + i), Height: int64(10 + i),
		})
	}
	if got := pref.openScoreRebuildCount(); got != before {
		t.Fatalf("writes must not rebuild inline: %d rebuilds after %d writes", got-before, 5)
	}
	// First read rebuilds exactly once; subsequent reads are free.
	summary := pref.OpenSummaryFor("mvc", shaA)
	if summary.Comparisons != 5 {
		t.Fatalf("burst scores: %+v", summary)
	}
	afterFirstRead := pref.openScoreRebuildCount()
	if afterFirstRead != before+1 {
		t.Fatalf("expected exactly one rebuild, got %d", afterFirstRead-before)
	}
	pref.OpenSummaryFor("mvc", shaB)
	if got := pref.openScoreRebuildCount(); got != afterFirstRead {
		t.Fatalf("clean reads must not rebuild: %d -> %d", afterFirstRead, got)
	}
}
