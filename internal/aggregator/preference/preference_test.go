package preference

import (
	"fmt"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// setupPreferenceTest wires the preference aggregator against a real
// publishedcontent aggregator sharing one Pebble store, mirroring the
// production wiring in cmd/metaso-p2p/main.go.
func setupPreferenceTest(t *testing.T) (*Aggregator, *publishedcontent.Aggregator, *storage.PebbleStore) {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	cacheProvider := cache.New(store)
	pub := &publishedcontent.Aggregator{}
	if err := pub.Init(store, cacheProvider); err != nil {
		t.Fatalf("publishedcontent Init: %v", err)
	}
	pref := &Aggregator{}
	pref.SetMetaAppResolver(NewMetaAppResolverAdapter(pub))
	if err := pref.Init(store, cacheProvider); err != nil {
		t.Fatalf("preference Init: %v", err)
	}
	return pref, pub, store
}

type motionVideoApp struct {
	rootPinId   string
	versionPin  string // current version pin (equals root for create-only)
	videoRef    string
	videoSha    string
	chainName   string
	publisherId string
}

func (app motionVideoApp) ref() string {
	return fmt.Sprintf(`{"chainName":%q,"app":"metaapp://%s","version":"metaapp://%s","video":"metafile://%s","videoSha256":"%s"}`,
		app.chainName, app.rootPinId, app.versionPin, app.videoRef, app.videoSha)
}

// publishMotionVideoApp indexes a Motion Video MetaApp through the
// publishedcontent aggregator (create, optional modify for a new version).
func publishMotionVideoApp(t *testing.T, pub *publishedcontent.Aggregator, app motionVideoApp) {
	t.Helper()
	payload := fmt.Sprintf(`{"title":"MV %s","tags":["motion-video"],"runtime":"browser","prompt":"reproduce me","metadata":{"series":"motion-video","videoFile":"metafile://%s","videoSha256":"%s","durationSec":10,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`,
		app.rootPinId, app.videoRef, app.videoSha)
	mustPubProcess(t, pub, &aggregator.PinInscription{
		Id:           app.rootPinId,
		Path:         "/protocols/metaapp",
		Operation:    "create",
		ChainName:    app.chainName,
		ContentType:  "application/json",
		ContentBody:  []byte(payload),
		GlobalMetaId: app.publisherId,
		MetaId:       app.publisherId,
		Address:      app.publisherId,
		Timestamp:    1000,
	})
}

// modifyMotionVideoAppVersion folds a new package version (optionally with a
// new video) into the app chain.
func modifyMotionVideoAppVersion(t *testing.T, pub *publishedcontent.Aggregator, app motionVideoApp, versionPinId, videoRef, videoSha string) {
	t.Helper()
	payload := fmt.Sprintf(`{"title":"MV %s v2","tags":["motion-video"],"runtime":"browser","prompt":"reproduce me","metadata":{"series":"motion-video","videoFile":"metafile://%s","videoSha256":"%s","durationSec":12,"fps":24,"resolution":"1920x1080","engine":"e","style":"s","sourceIncluded":true}}`,
		app.rootPinId, videoRef, videoSha)
	mustPubProcess(t, pub, &aggregator.PinInscription{
		Id:           versionPinId,
		Path:         "/protocols/metaapp@" + app.rootPinId,
		Operation:    "modify",
		ChainName:    app.chainName,
		ContentType:  "application/json",
		ContentBody:  []byte(payload),
		GlobalMetaId: app.publisherId,
		MetaId:       app.publisherId,
		Address:      app.publisherId,
		Timestamp:    2000,
	})
}

func mustPubProcess(t *testing.T, pub *publishedcontent.Aggregator, pin *aggregator.PinInscription) {
	t.Helper()
	if _, err := pub.HandleBlockPin(pin); err != nil {
		t.Fatalf("publishedcontent HandleBlockPin(%s): %v", pin.Id, err)
	}
}

type feedbackPinOpts struct {
	PinId     string
	Signer    string
	ChainName string
	Path      string
	Operation string
	Body      string
	Timestamp int64
	Height    int64
}

func makeFeedbackPin(opts feedbackPinOpts) *aggregator.PinInscription {
	chainName := opts.ChainName
	if chainName == "" {
		chainName = "mvc"
	}
	path := opts.Path
	if path == "" {
		path = PathPreferenceFeedback
	}
	operation := opts.Operation
	if operation == "" {
		operation = "create"
	}
	return &aggregator.PinInscription{
		Id:            opts.PinId,
		Path:          path,
		Operation:     operation,
		ChainName:     chainName,
		ContentType:   "application/json",
		ContentBody:   []byte(opts.Body),
		GlobalMetaId:  opts.Signer,
		MetaId:        opts.Signer,
		Address:       opts.Signer,
		Timestamp:     opts.Timestamp,
		GenesisHeight: opts.Height,
	}
}

func mustFeedbackBlock(t *testing.T, pref *Aggregator, opts feedbackPinOpts) {
	t.Helper()
	if _, err := pref.HandleBlockPin(makeFeedbackPin(opts)); err != nil {
		t.Fatalf("HandleBlockPin(%s): %v", opts.PinId, err)
	}
}

func mustFeedbackMempool(t *testing.T, pref *Aggregator, opts feedbackPinOpts) {
	t.Helper()
	if _, err := pref.HandleMempoolPin(makeFeedbackPin(opts)); err != nil {
		t.Fatalf("HandleMempoolPin(%s): %v", opts.PinId, err)
	}
}

func twoAppsAB(t *testing.T, pub *publishedcontent.Aggregator) (motionVideoApp, motionVideoApp) {
	t.Helper()
	appA := motionVideoApp{rootPinId: "app-root-a:i0", versionPin: "app-root-a:i0", videoRef: "video-a:i0", videoSha: shaA, chainName: "mvc", publisherId: "gid-author-a"}
	appB := motionVideoApp{rootPinId: "app-root-b:i0", versionPin: "app-root-b:i0", videoRef: "video-b:i0", videoSha: shaB, chainName: "mvc", publisherId: "gid-author-b"}
	publishMotionVideoApp(t, pub, appA)
	publishMotionVideoApp(t, pub, appB)
	return appA, appB
}

func abBatch(pick, extraCategory string) string {
	categories := `"motion-video"`
	if extraCategory != "" {
		categories = `"motion-video",` + extraCategory
	}
	return batchJSON(categories, itemJSON(refA("app-root-a:i0", "app-root-a:i0", "video-a:i0", shaA), refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), pick))
}

func TestConfirmedFeedbackScoresAndStates(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	// One signer picks A: A n=1 wins=1 → (1+1)/(1+2)=0.667, B (0+1)/3=0.333.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-1:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1000, Height: 10})

	a := pref.OpenSummaryFor("mvc", shaA)
	b := pref.OpenSummaryFor("mvc", shaB)
	if a.Score == nil || *a.Score < 0.666 || *a.Score > 0.667 {
		t.Fatalf("A score: %v", a.Score)
	}
	if b.Score == nil || *b.Score < 0.333 || *b.Score > 0.334 {
		t.Fatalf("B score: %v", b.Score)
	}
	if a.State != StateEmerging || b.State != StateEmerging {
		t.Fatalf("states: %q %q (1 judge each → emerging)", a.State, b.State)
	}
	if a.Comparisons != 1 || a.Judges != 1 || a.LastComparedAt == nil || *a.LastComparedAt != 1000 {
		t.Fatalf("A summary: %+v", a)
	}

	// Second and third judges reach the established threshold for both.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-2:i0", Signer: "gid-judge-2", Body: abBatch("b", ""), Timestamp: 1100, Height: 11})
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-3:i0", Signer: "gid-judge-3", Body: abBatch("a", ""), Timestamp: 1200, Height: 12})
	a = pref.OpenSummaryFor("mvc", shaA)
	b = pref.OpenSummaryFor("mvc", shaB)
	if a.State != StateEstablished || b.State != StateEstablished {
		t.Fatalf("states after 3 judges: %q %q", a.State, b.State)
	}
	// A: 2 wins of 3 → (2+1)/5 = 0.6; ties counted 0.5 must not appear.
	if a.Comparisons != 3 || a.Judges != 3 {
		t.Fatalf("A summary: %+v", a)
	}

	// Tie contributes 0.5 to both sides.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-4:i0", Signer: "gid-judge-4", Body: abBatch("tie", ""), Timestamp: 1300, Height: 13})
	a = pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 4 {
		t.Fatalf("A comparisons after tie: %+v", a)
	}
	// A: 2 wins + 1 tie of 4 → (2+0.5+1)/(4+2) = 0.5833
	if a.Score == nil || *a.Score < 0.583 || *a.Score > 0.584 {
		t.Fatalf("A score with tie: %v", a.Score)
	}
}

func TestUnjudgedVideoSummaryIsExactZeroEvidence(t *testing.T) {
	pref, _, store := setupPreferenceTest(t)
	defer store.Close()

	summary := pref.OpenSummaryFor("mvc", shaC)
	if summary.Score != nil || summary.State != StateUnjudged || summary.Comparisons != 0 || summary.Judges != 0 || summary.LastComparedAt != nil {
		t.Fatalf("unjudged summary must be the exact zero-evidence block: %+v", summary)
	}
	if summary.ScoreVersion != ScoreVersion {
		t.Fatalf("scoreVersion: %q", summary.ScoreVersion)
	}
}

func TestLatestPerSignerPairWinsByConfirmedOrder(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	_, appB := twoAppsAB(t, pub)

	// Same signer, same unordered pair, opposite picks: only the later
	// confirmed comparison counts (n=1, not 2).
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-old:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1000, Height: 10})
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-new:i0", Signer: "gid-judge-1", Body: abBatch("b", ""), Timestamp: 2000, Height: 20})

	a := pref.OpenSummaryFor("mvc", shaA)
	// Latest pick is b: A has 0 wins, 0 ties, n=1 → (0+1)/(1+2)=1/3.
	if a.Comparisons != 1 || a.Score == nil || *a.Score < 0.333 || *a.Score > 0.334 {
		t.Fatalf("A after latest-wins: %+v", a)
	}
	b := pref.OpenSummaryFor("mvc", shaB)
	if b.Comparisons != 1 {
		t.Fatalf("B comparisons: %+v", b)
	}
	// Both pins stay stored for audit.
	records, err := pref.listFeedbackRecords()
	if err != nil {
		t.Fatalf("listFeedbackRecords: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("audit retention: got %d records want 2", len(records))
	}
	_ = appB
}

func TestMempoolFeedbackPendingOnlyUntilConfirmation(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	_, _ = twoAppsAB(t, pub)

	// Mempool batch: stored as pending, score-neutral.
	mustFeedbackMempool(t, pref, feedbackPinOpts{PinId: "fb-mem:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1000})
	a := pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 0 || a.Score != nil || a.State != StateUnjudged {
		t.Fatalf("mempool must not change scores/latest state: %+v", a)
	}

	// Visible only with includeMempool, flagged isMempool.
	pending, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeMempool: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons includeMempool: %v", err)
	}
	if len(pending.Items) != 1 || !pending.Items[0].IsMempool || pending.Items[0].ConfirmedAt != nil {
		t.Fatalf("pending evidence: %+v", pending.Items)
	}

	// Confirmation replaces the pending record and applies chain order.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-mem:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1000, Height: 10})
	a = pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 1 || a.Score == nil {
		t.Fatalf("confirmation must apply the comparison: %+v", a)
	}
	confirmed, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(confirmed.Items) != 1 || confirmed.Items[0].IsMempool || confirmed.Items[0].ConfirmedAt == nil {
		t.Fatalf("confirmed evidence: %+v", confirmed.Items)
	}

	// A mempool echo after confirmation never demotes the confirmed record.
	mustFeedbackMempool(t, pref, feedbackPinOpts{PinId: "fb-mem:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1500})
	confirmed, err = pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(confirmed.Items) != 1 || confirmed.Items[0].IsMempool {
		t.Fatalf("post-confirmation mempool echo must be ignored: %+v", confirmed.Items)
	}
}

func TestAuthorConflictExcludedWithoutDiscardingOtherPairs(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	appA, appB := twoAppsAB(t, pub)
	// A third app so the conflicting signer has one clean pair and one
	// conflicted pair in the same batch.
	appC := motionVideoApp{rootPinId: "app-root-c:i0", versionPin: "app-root-c:i0", videoRef: "video-c:i0", videoSha: shaC, chainName: "mvc", publisherId: "gid-author-c"}
	publishMotionVideoApp(t, pub, appC)

	body := batchJSON(`"motion-video"`,
		itemJSON(appA.ref(), appB.ref(), "a"), // author-a judging own work → conflict
		itemJSON(appB.ref(), appC.ref(), "b"), // unrelated clean pair
	)
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-conflict:i0", Signer: "gid-author-a", Body: body, Timestamp: 1000, Height: 10})

	a := pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 0 || a.Score != nil {
		t.Fatalf("conflicted pair must not score: %+v", a)
	}
	b := pref.OpenSummaryFor("mvc", shaB)
	if b.Comparisons != 1 || b.Score == nil {
		t.Fatalf("unrelated valid pair must stay eligible: %+v", b)
	}

	// Default evidence hides the exclusion; includeExcluded marks it.
	defaultView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(defaultView.Items) != 1 || defaultView.Items[0].Excluded {
		t.Fatalf("default view: %+v", defaultView.Items)
	}
	excludedView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeExcluded: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons includeExcluded: %v", err)
	}
	if len(excludedView.Items) != 2 {
		t.Fatalf("excluded view must show both pairs: %+v", excludedView.Items)
	}
	for _, item := range excludedView.Items {
		if item.Excluded && item.ExclusionReason != ExclusionAuthorConflict {
			t.Fatalf("exclusion reason: %+v", item)
		}
	}
}

func TestArtifactRefResolutionFailures(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	// Version pin outside the app chain (belongs to another app).
	outside := `{"chainName":"mvc","app":"metaapp://app-root-a:i0","version":"metaapp://app-root-b:i0","video":"metafile://video-a:i0","videoSha256":"` + shaA + `"}`
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-outside:i0", Signer: "gid-judge-1", Body: batchJSON(`"motion-video"`, itemJSON(outside, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 1000, Height: 10})

	// Video/hash disagreeing with the current manifest claim.
	mismatch := `{"chainName":"mvc","app":"metaapp://app-root-a:i0","version":"metaapp://app-root-a:i0","video":"metafile://other-video:i0","videoSha256":"` + shaA + `"}`
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-mismatch:i0", Signer: "gid-judge-1", Body: batchJSON(`"motion-video"`, itemJSON(mismatch, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 1100, Height: 11})

	// Unknown app root.
	unknown := `{"chainName":"mvc","app":"metaapp://ghost-root:i0","version":"metaapp://ghost-root:i0","video":"metafile://video-g:i0","videoSha256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"}`
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-ghost:i0", Signer: "gid-judge-1", Body: batchJSON(`"motion-video"`, itemJSON(unknown, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 1200, Height: 12})

	for _, sha := range []string{shaA, shaB} {
		if summary := pref.OpenSummaryFor("mvc", sha); summary.Comparisons != 0 {
			t.Fatalf("unresolvable refs must not score %s: %+v", sha, summary)
		}
	}
	excludedView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeExcluded: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(excludedView.Items) != 3 {
		t.Fatalf("excluded diagnostics must retain all three: %+v", excludedView.Items)
	}
	reasons := map[string]bool{}
	for _, item := range excludedView.Items {
		reasons[item.ExclusionReason] = true
	}
	if !reasons[ExclusionVersionOutsideApp] || !reasons[ExclusionArtifactMismatch] || !reasons[ExclusionAppNotFound] {
		t.Fatalf("exclusion reasons: %v", reasons)
	}
}

func TestUnsupportedCategoryRecordedNotProjected(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	body := batchJSON(`"other-art-form"`, itemJSON(refA("app-root-a:i0", "app-root-a:i0", "video-a:i0", shaA), refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a"))
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-other:i0", Signer: "gid-judge-1", Body: body, Timestamp: 1000, Height: 10})

	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 0 {
		t.Fatalf("unsupported category must not project: %+v", summary)
	}
	view, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeTest: true, includeMempool: true, includeExcluded: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(view.Items) != 0 {
		t.Fatalf("unsupported batches are outside the projection entirely: %+v", view.Items)
	}
}

func TestTestTagBatchesDiagnoticOnly(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-smoke:i0", Signer: "gid-judge-1", Body: abBatch("a", `"test:motion-video-smoke"`), Timestamp: 1000, Height: 10})

	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 0 || summary.Score != nil {
		t.Fatalf("test: batch must not change scores: %+v", summary)
	}
	defaultView, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", size: 20})
	if err != nil {
		t.Fatalf("ListComparisons: %v", err)
	}
	if len(defaultView.Items) != 0 {
		t.Fatalf("test: batch must stay out of default evidence: %+v", defaultView.Items)
	}
	diagnostics, err := pref.ListComparisons(comparisonsQuery{chainName: "mvc", category: CategoryMotionVideo, basis: BasisOpen, outcome: "all", includeTest: true, size: 20})
	if err != nil {
		t.Fatalf("ListComparisons includeTest: %v", err)
	}
	if len(diagnostics.Items) != 1 || diagnostics.Items[0].FeedbackPinId != "fb-smoke:i0" {
		t.Fatalf("includeTest=1 must expose the smoke batch: %+v", diagnostics.Items)
	}
}

func TestModifyAndRevokePinsIgnored(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-mod:i0", Signer: "gid-judge-1", Operation: "modify", Body: abBatch("a", ""), Timestamp: 1000, Height: 10})
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-rev:i0", Signer: "gid-judge-1", Operation: "revoke", Body: abBatch("a", ""), Timestamp: 1100, Height: 11})
	// Off-path pins are ignored too.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-off:i0", Signer: "gid-judge-1", Path: "/protocols/simplebuzz", Body: abBatch("a", ""), Timestamp: 1200, Height: 12})

	records, err := pref.listFeedbackRecords()
	if err != nil {
		t.Fatalf("listFeedbackRecords: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("modify/revoke/off-path pins must be ignored: %+v", records)
	}
}

func TestMalformedBatchStoredForAuditNeverScored(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	badBody := `{"category":["motion-video"],"kind":"comparison-batch","client":{"artifact":"metaapp://arena-root:i0"},"items":[{"a":{"chainName":"mvc"},"b":{},"pick":"a"}]}`
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-bad:i0", Signer: "gid-judge-1", Body: badBody, Timestamp: 1000, Height: 10})

	if summary := pref.OpenSummaryFor("mvc", shaA); summary.Comparisons != 0 {
		t.Fatalf("malformed batch must not score: %+v", summary)
	}
	rec, err := pref.loadFeedbackRecord("mvc", "fb-bad:i0")
	if err != nil || rec == nil {
		t.Fatalf("malformed pin must stay stored for audit: %v %v", rec, err)
	}
	if rec.Status != StatusMalformed || rec.InvalidReason == "" {
		t.Fatalf("malformed status: %+v", rec)
	}
}

func TestDuplicateDeliveryIdempotent(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	twoAppsAB(t, pub)

	opts := feedbackPinOpts{PinId: "fb-dup:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1000, Height: 10}
	mustFeedbackBlock(t, pref, opts)
	mustFeedbackBlock(t, pref, opts)
	mustFeedbackBlock(t, pref, opts)

	if a := pref.OpenSummaryFor("mvc", shaA); a.Comparisons != 1 || a.Judges != 1 {
		t.Fatalf("duplicate deliveries must be idempotent: %+v", a)
	}
	records, err := pref.listFeedbackRecords()
	if err != nil {
		t.Fatalf("listFeedbackRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records: got %d want 1", len(records))
	}
}

func TestCrossChainIsolationSameVideoHash(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	_, _ = twoAppsAB(t, pub)
	// Same videoSha256 published on another chain.
	appOtherChain := motionVideoApp{rootPinId: "app-root-x:i0", versionPin: "app-root-x:i0", videoRef: "video-x:i0", videoSha: shaA, chainName: "btc", publisherId: "gid-author-x"}
	publishMotionVideoApp(t, pub, appOtherChain)
	appY := motionVideoApp{rootPinId: "app-root-y:i0", versionPin: "app-root-y:i0", videoRef: "video-y:i0", videoSha: shaB, chainName: "btc", publisherId: "gid-author-y"}
	publishMotionVideoApp(t, pub, appY)

	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-mvc:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1000, Height: 10})
	btcBatch := batchJSON(`"motion-video"`, itemJSON(appOtherChain.ref(), appY.ref(), "b"))
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-btc:i0", Signer: "gid-judge-2", ChainName: "btc", Body: btcBatch, Timestamp: 1100, Height: 11})

	mvc := pref.OpenSummaryFor("mvc", shaA)
	btc := pref.OpenSummaryFor("btc", shaA)
	if mvc.Comparisons != 1 || btc.Comparisons != 1 {
		t.Fatalf("chain-scoped aggregates: mvc=%+v btc=%+v", mvc, btc)
	}
	if mvc.Judges != 1 || btc.Judges != 1 {
		t.Fatalf("judges must not merge across chains: mvc=%+v btc=%+v", mvc, btc)
	}
	// The btc judge's pick was b: on btc the shaA video lost (score 1/3).
	if btc.Score == nil || *btc.Score > 0.334 {
		t.Fatalf("btc shaA score after loss: %+v", btc)
	}
	if mvc.Score == nil || *mvc.Score < 0.666 {
		t.Fatalf("mvc shaA score after win: %+v", mvc)
	}
}

func TestVersionBoundariesDocEditVersusNewVideo(t *testing.T) {
	pref, pub, store := setupPreferenceTest(t)
	defer store.Close()
	appA, appB := twoAppsAB(t, pub)

	// Judge compares the current versions.
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-v1:i0", Signer: "gid-judge-1", Body: abBatch("a", ""), Timestamp: 1000, Height: 10})
	if a := pref.OpenSummaryFor("mvc", shaA); a.Comparisons != 1 {
		t.Fatalf("baseline evidence missing: %+v", a)
	}

	// Documentation-only update keeps the same video hash: visual evidence
	// carries over and the historical-version evidence stays valid.
	modifyMotionVideoAppVersion(t, pub, appA, "app-root-a-v2:i0", "video-a:i0", shaA)
	_ = appB
	historical := `{"chainName":"mvc","app":"metaapp://app-root-a:i0","version":"metaapp://app-root-a:i0","video":"metafile://video-a:i0","videoSha256":"` + shaA + `"}`
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-doc:i0", Signer: "gid-judge-2", Body: batchJSON(`"motion-video"`, itemJSON(historical, refA("app-root-b:i0", "app-root-b:i0", "video-b:i0", shaB), "a")), Timestamp: 2000, Height: 20})
	a := pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 2 || a.Judges != 2 {
		t.Fatalf("same-hash doc edit must retain visual evidence: %+v", a)
	}

	// New video hash under the same root: fresh aggregate, no inheritance.
	newSha := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	modifyMotionVideoAppVersion(t, pub, appA, "app-root-a-v3:i0", "video-a3:i0", newSha)
	newAgg := pref.OpenSummaryFor("mvc", newSha)
	if newAgg.Comparisons != 0 || newAgg.Score != nil || newAgg.State != StateUnjudged {
		t.Fatalf("new video hash must start an independent aggregate: %+v", newAgg)
	}
	// Old-video evidence remains queryable.
	a = pref.OpenSummaryFor("mvc", shaA)
	if a.Comparisons != 2 {
		t.Fatalf("old-video evidence must stay queryable: %+v", a)
	}
}

func TestReplayDeterminism(t *testing.T) {
	build := func(t *testing.T) map[string]OpenSummary {
		t.Helper()
		pref, pub, store := setupPreferenceTest(t)
		defer store.Close()
		appA, appB := twoAppsAB(t, pub)
		appC := motionVideoApp{rootPinId: "app-root-c:i0", versionPin: "app-root-c:i0", videoRef: "video-c:i0", videoSha: shaC, chainName: "mvc", publisherId: "gid-author-c"}
		publishMotionVideoApp(t, pub, appC)

		threeWay := batchJSON(`"motion-video"`,
			itemJSON(appA.ref(), appB.ref(), "a"),
			itemJSON(appB.ref(), appC.ref(), "tie"),
			itemJSON(appA.ref(), appC.ref(), "b"),
		)
		pins := []feedbackPinOpts{
			{PinId: "fb-1:i0", Signer: "gid-judge-1", Body: threeWay, Timestamp: 1000, Height: 10},
			{PinId: "fb-2:i0", Signer: "gid-judge-2", Body: abBatch("b", ""), Timestamp: 1100, Height: 11},
			{PinId: "fb-3:i0", Signer: "gid-judge-3", Body: batchJSON(`"motion-video"`, itemJSON(appA.ref(), appB.ref(), "tie")), Timestamp: 1200, Height: 12},
			{PinId: "fb-4:i0", Signer: "gid-judge-1", Body: abBatch("tie", ""), Timestamp: 1300, Height: 13}, // supersedes fb-1's A/B pick
		}
		// Replay in a different grouping/order than "natural" arrival: the
		// confirmed chain order (height) must decide latest-per-signer/pair.
		for _, idx := range []int{2, 0, 3, 1} {
			mustFeedbackBlock(t, pref, pins[idx])
		}
		out := map[string]OpenSummary{}
		for _, sha := range []string{shaA, shaB, shaC} {
			out[sha] = pref.OpenSummaryFor("mvc", sha)
		}
		return out
	}

	first := build(t)
	second := build(t)
	for sha, want := range first {
		got := second[sha]
		if got.Comparisons != want.Comparisons || got.Judges != want.Judges || got.State != want.State {
			t.Fatalf("replay drift for %s: %+v vs %+v", sha, got, want)
		}
		if (got.Score == nil) != (want.Score == nil) || (got.Score != nil && *got.Score != *want.Score) {
			t.Fatalf("replay score drift for %s: %+v vs %+v", sha, got, want)
		}
	}
	// fb-4 superseded judge-1's A/B pick with a tie. A is involved in 4
	// latest comparisons: A/B×3 (tie,b,tie) and A/C×1 (loss).
	if a := first[shaA]; a.Comparisons != 4 {
		t.Fatalf("A comparisons: %+v", a)
	}
	// A: 0 wins + 2 ties of 4 → (0+0.5×2+1)/(4+2) = 2/6 ≈ 0.333
	if a := first[shaA]; a.Score == nil || *a.Score < 0.332 || *a.Score > 0.334 {
		t.Fatalf("A score after supersede: %+v", a)
	}
	if a := first[shaA]; a.Judges != 3 || a.State != StateEstablished {
		t.Fatalf("A judges/state: %+v", a)
	}
}

func TestOpenNamespacePrefixIsolation(t *testing.T) {
	pref, _, store := setupPreferenceTest(t)
	defer store.Close()

	// Every stored key must live under the open: prefix so the future
	// validated namespace stays physically separate.
	pref.store.Set(Namespace, []byte("validated:tally:probe"), []byte("{}"))
	found := map[string]bool{}
	err := pref.store.ScanPrefix(Namespace, []byte(keyOpenFeedback), func(key, _ []byte) error {
		found[string(key)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("ScanPrefix: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("no feedback stored yet, got %v", found)
	}
	err = pref.store.ScanPrefix(Namespace, []byte("validated:"), func(key, _ []byte) error {
		if !found["seen-validated"] {
			found["seen-validated"] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ScanPrefix validated: %v", err)
	}
	if !found["seen-validated"] {
		t.Fatal("validated namespace probe must be reachable and separate")
	}
}
