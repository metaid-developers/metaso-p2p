package bothomepage

import (
	"net/url"
	"testing"
	"time"
)

func TestParseOptionsDefaults(t *testing.T) {
	defaults := DefaultOptions()
	if !defaults.IncludeServices {
		t.Fatal("DefaultOptions IncludeServices should be true")
	}
	if defaults.ServiceSize != 20 {
		t.Fatalf("DefaultOptions ServiceSize = %d, want 20", defaults.ServiceSize)
	}
	if defaults.IncludeInactiveServices {
		t.Fatal("DefaultOptions IncludeInactiveServices should be false")
	}
	if !defaults.IncludeProofs {
		t.Fatal("DefaultOptions IncludeProofs should be true")
	}
	if !defaults.IncludePresence {
		t.Fatal("DefaultOptions IncludePresence should be true")
	}
	if defaults.ChainName != "" {
		t.Fatalf("DefaultOptions ChainName = %q, want empty", defaults.ChainName)
	}

	got, err := ParseOptions(url.Values{})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if !got.IncludeServices {
		t.Fatal("IncludeServices default should be true")
	}
	if got.ServiceSize != 20 {
		t.Fatalf("ServiceSize = %d, want 20", got.ServiceSize)
	}
	if got.IncludeInactiveServices {
		t.Fatal("IncludeInactiveServices default should be false")
	}
	if !got.IncludeProofs {
		t.Fatal("IncludeProofs default should be true")
	}
	if !got.IncludePresence {
		t.Fatal("IncludePresence default should be true")
	}
	if got.ChainName != "" {
		t.Fatalf("ChainName = %q, want empty", got.ChainName)
	}
}

func TestParseOptionsClampsServiceSize(t *testing.T) {
	got, err := ParseOptions(url.Values{"serviceSize": {"101"}})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if got.ServiceSize != 100 {
		t.Fatalf("ServiceSize = %d, want 100", got.ServiceSize)
	}
}

func TestParseOptionsRejectsInvalidValues(t *testing.T) {
	cases := []url.Values{
		{"includeServices": {"maybe"}},
		{"includeProofs": {"sometimes"}},
		{"includePresence": {"unknown"}},
		{"includeInactiveServices": {"wat"}},
		{"serviceSize": {"-1"}},
		{"serviceSize": {"abc"}},
	}
	for _, values := range cases {
		if _, err := ParseOptions(values); err == nil {
			t.Fatalf("ParseOptions(%v) expected error", values)
		}
	}
}

func TestAggregatorInterfaceSkeleton(t *testing.T) {
	if cacheMaxEntries != 1000 {
		t.Fatalf("cacheMaxEntries = %d, want 1000", cacheMaxEntries)
	}
	if cacheTTL != 30*time.Second {
		t.Fatalf("cacheTTL = %s, want 30s", cacheTTL)
	}

	agg := &Aggregator{}
	if agg.Name() != "bothomepage" {
		t.Fatalf("Name() = %q, want bothomepage", agg.Name())
	}
	if evt, err := agg.HandleBlockPin(nil); err != nil || evt != nil {
		t.Fatalf("HandleBlockPin(nil) = (%v, %v), want nil nil", evt, err)
	}
	if evt, err := agg.HandleMempoolPin(nil); err != nil || evt != nil {
		t.Fatalf("HandleMempoolPin(nil) = (%v, %v), want nil nil", evt, err)
	}

	customNow := func() int64 { return 42 }
	agg.now = customNow
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if got := agg.now(); got != 42 {
		t.Fatalf("Init overwrote existing now: got %d, want 42", got)
	}

	agg = &Aggregator{}
	if err := agg.Init(nil, nil); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if agg.now == nil {
		t.Fatal("Init should set nil now")
	}
	if got := agg.now(); got <= 0 {
		t.Fatalf("Init now() = %d, want unix milliseconds", got)
	}
}
