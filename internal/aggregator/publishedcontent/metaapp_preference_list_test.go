package publishedcontent

import (
	"testing"
)

// stubPreferenceLookup is a test PreferenceLookup serving fixed summaries.
type stubPreferenceLookup struct {
	summaries map[string]MetaAppPreferenceOpen
}

func (s stubPreferenceLookup) OpenPreferenceSummary(chainName, videoSha256 string) MetaAppPreferenceOpen {
	if summary, ok := s.summaries[chainName+":"+videoSha256]; ok {
		return summary
	}
	return UnjudgedMetaAppPreferenceOpen()
}

func scorePtr(v float64) *float64 { return &v }
func timePtr(v int64) *int64      { return &v }

func setupPreferenceListAggregator(t *testing.T) *Aggregator {
	t.Helper()
	agg, store := setupTestAggregator(t)
	t.Cleanup(func() { store.Close() })
	processMotionVideoFixtures(t, agg)
	// A third motion video app that stays unjudged (null score).
	mustProcess(t, agg, makeContentPin(contentPinOpts{
		PinId:       "cccc0003unjudged:i0",
		Path:        PathMetaApp,
		Timestamp:   3000,
		ContentType: "application/json",
		ContentBody: []byte(`{"title":"Unjudged MV","tags":["motion-video"],"prompt":"p","metadata":{"series":"motion-video","videoFile":"metafile://u1i0","videoSha256":"1111111111111111111111111111111111111111111111111111111111111111","durationSec":5,"fps":24,"resolution":"r","engine":"e","style":"s","sourceIncluded":true}}`),
	}))
	// Fixture A: 0.75 established; fixture B: 0.40 emerging.
	agg.SetPreferenceLookup(stubPreferenceLookup{summaries: map[string]MetaAppPreferenceOpen{
		"mvc:" + fixtureAVideoSha: {
			Score: scorePtr(0.75), ScoreVersion: OpenPreferenceScoreVersion,
			State: PreferenceStateEstablished, Comparisons: 4, Judges: 3, LastComparedAt: timePtr(5000),
		},
		"mvc:" + fixtureBVideoSha: {
			Score: scorePtr(0.40), ScoreVersion: OpenPreferenceScoreVersion,
			State: PreferenceStateEmerging, Comparisons: 2, Judges: 1, LastComparedAt: timePtr(4000),
		},
	}})
	return agg
}

func TestMetaAppListPreferenceOrderingDesc(t *testing.T) {
	agg := setupPreferenceListAggregator(t)

	result, err := agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video",
		Sort: "preference", Order: "desc", Basis: PreferenceBasisOpen,
	})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("len: got %d want 3", len(result.Items))
	}
	wantOrder := []string{
		"22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0",
		"5686db0dd51ac19ec60c4a02ff66bc51adcf0149b8484141b38e730c734d9bb4i0",
		"cccc0003unjudged:i0",
	}
	for i, want := range wantOrder {
		if result.Items[i].PinID != want {
			t.Fatalf("desc order[%d]: got %q want %q", i, result.Items[i].PinID, want)
		}
		if result.Items[i].Preference == nil {
			t.Fatalf("item[%d] must carry the preference block", i)
		}
	}
	// Unjudged item: null score in BOTH summaries, zero counts.
	unjudged := result.Items[2].Preference
	if unjudged.Open.Score != nil || unjudged.Open.State != PreferenceStateUnjudged || unjudged.Open.Comparisons != 0 {
		t.Fatalf("unjudged open summary: %+v", unjudged.Open)
	}
	// Pre-R7 validated block is exactly {score:null,state:"unavailable"}.
	if unjudged.Validated.Score != nil || unjudged.Validated.State != "unavailable" {
		t.Fatalf("validated block: %+v", unjudged.Validated)
	}
	top := result.Items[0].Preference
	if top.Open.Score == nil || *top.Open.Score != 0.75 || top.Open.Judges != 3 || top.Open.Comparisons != 4 {
		t.Fatalf("top open summary: %+v", top.Open)
	}
	if top.Open.ScoreVersion != "motion-video-open-pref-v1" {
		t.Fatalf("scoreVersion: %q", top.Open.ScoreVersion)
	}
}

func TestMetaAppListPreferenceOrderingAscNullsStillLast(t *testing.T) {
	agg := setupPreferenceListAggregator(t)

	result, err := agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video",
		Sort: "preference", Order: "asc", Basis: PreferenceBasisOpen,
	})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	// Ascending is meaningful only for judged items: 0.40 then 0.75, null last.
	wantOrder := []string{
		"5686db0dd51ac19ec60c4a02ff66bc51adcf0149b8484141b38e730c734d9bb4i0",
		"22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0",
		"cccc0003unjudged:i0",
	}
	for i, want := range wantOrder {
		if result.Items[i].PinID != want {
			t.Fatalf("asc order[%d]: got %q want %q", i, result.Items[i].PinID, want)
		}
	}
}

func TestMetaAppListPreferenceMinJudgesAndLearningFilters(t *testing.T) {
	agg := setupPreferenceListAggregator(t)

	result, err := agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", MinJudges: 2,
	})
	if err != nil {
		t.Fatalf("ListMetaApps minJudges: %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].PinID != "22ce6ca2ba950c3659c739e235b519e94341fe0a19f13c876807dad6e0955659i0" {
		t.Fatalf("minJudges=2 keeps only the 3-judge fixture: %+v", result.Items)
	}

	// Learning filter: all three fixtures are declared, so declared keeps 3
	// and incomplete keeps 0.
	result, err = agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", Learning: LearningStatusDeclared,
	})
	if err != nil {
		t.Fatalf("ListMetaApps learning=declared: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("learning=declared: got %d want 3", len(result.Items))
	}
	result, err = agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", Learning: LearningStatusIncomplete,
	})
	if err != nil {
		t.Fatalf("ListMetaApps learning=incomplete: %v", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("learning=incomplete: got %d want 0", len(result.Items))
	}
}

func TestMetaAppListPreferenceCursorStabilityAndInvalidation(t *testing.T) {
	agg := setupPreferenceListAggregator(t)

	page1, err := agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", Size: 2,
	})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(page1.Items) != 2 || !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("page1: %+v", page1)
	}

	// Same query + cursor: stable continuation.
	page2, err := agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", Size: 2, Cursor: page1.NextCursor,
	})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(page2.Items) != 1 || page2.Items[0].PinID != "cccc0003unjudged:i0" {
		t.Fatalf("page2: %+v", page2.Items)
	}

	// Changing a filter (order) invalidates the cursor.
	_, err = agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", Order: "asc", Size: 2, Cursor: page1.NextCursor,
	})
	if err == nil || err.Error() != "invalid cursor" {
		t.Fatalf("cursor must invalidate on order change, got %v", err)
	}
	// Changing minJudges invalidates the cursor.
	_, err = agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", MinJudges: 1, Size: 2, Cursor: page1.NextCursor,
	})
	if err == nil || err.Error() != "invalid cursor" {
		t.Fatalf("cursor must invalidate on minJudges change, got %v", err)
	}
	// A legacy plain offset cursor is not a valid preference cursor.
	_, err = agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", Size: 2, Cursor: encodeMetaAppCursor(1),
	})
	if err == nil || err.Error() != "invalid cursor" {
		t.Fatalf("legacy cursor must be rejected in preference mode, got %v", err)
	}
}

func TestMetaAppListWithoutPreferenceParamsUnchanged(t *testing.T) {
	agg := setupPreferenceListAggregator(t)

	// No sort=preference: existing feed order, no preference block.
	result, err := agg.ListMetaApps(MetaAppListParams{ChainName: "mvc", Tag: "motion-video"})
	if err != nil {
		t.Fatalf("ListMetaApps: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("len: got %d want 3", len(result.Items))
	}
	for _, item := range result.Items {
		if item.Preference != nil {
			t.Fatal("default listing must not carry the preference block")
		}
		if item.MotionVideo == nil {
			t.Fatal("motionVideo block is additive and always present for motion-video items")
		}
	}
}
