package bothomepage

import (
	"net/url"
	"testing"
)

func TestParseOptionsDefaults(t *testing.T) {
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
}
