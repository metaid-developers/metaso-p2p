package publishedcontent

import "testing"

// TestPreferenceListWithoutLookupFailsLoudly covers the acceptance-review
// nil-degradation case: sort=preference must fail explicitly when the score
// source is not wired, never silently return all-unjudged items.
func TestPreferenceListWithoutLookupFailsLoudly(t *testing.T) {
	agg := setupPreferenceListAggregator(t)
	agg.SetPreferenceLookup(nil)

	_, err := agg.ListMetaApps(MetaAppListParams{
		ChainName: "mvc", Tag: "motion-video", Sort: "preference", Basis: PreferenceBasisOpen,
	})
	if err != errPreferenceLookupUnavailable {
		t.Fatalf("expected explicit preference-lookup error, got %v", err)
	}

	// Plain listings stay unaffected by the missing lookup.
	result, err := agg.ListMetaApps(MetaAppListParams{ChainName: "mvc", Tag: "motion-video"})
	if err != nil {
		t.Fatalf("default listing must not need the preference lookup: %v", err)
	}
	if len(result.Items) != 3 {
		t.Fatalf("default listing items: %d", len(result.Items))
	}
}
