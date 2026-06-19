package bothomepage

import (
	"net/url"
	"testing"
	"time"
)

func TestParseOptionsDefaults(t *testing.T) {
	defaults := DefaultOptions()
	if defaults.Version != "" {
		t.Fatalf("DefaultOptions Version = %q, want empty", defaults.Version)
	}
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

func TestParseOptionsVersionV2(t *testing.T) {
	got, err := ParseOptions(url.Values{"version": {"v2"}, "chainName": {""}})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if got.Version != "v2" {
		t.Fatalf("Version = %q, want v2", got.Version)
	}
	if got.ChainName != "" {
		t.Fatalf("ChainName = %q, want empty", got.ChainName)
	}
	if !got.IncludeSections {
		t.Fatal("IncludeSections = false, want true for version=v2")
	}

	got, err = ParseOptions(url.Values{})
	if err != nil {
		t.Fatalf("ParseOptions defaults returned error: %v", err)
	}
	if got.Version != "" {
		t.Fatalf("default Version = %q, want empty", got.Version)
	}
	if got.IncludeSections {
		t.Fatal("default IncludeSections = true, want false")
	}

	got, err = ParseOptions(url.Values{"version": {"v2"}, "includeSections": {"false"}})
	if err != nil {
		t.Fatalf("ParseOptions explicit includeSections=false returned error: %v", err)
	}
	if got.Version != "v2" {
		t.Fatalf("explicit Version = %q, want v2", got.Version)
	}
	if got.IncludeSections {
		t.Fatal("explicit IncludeSections = true, want false")
	}
}

func TestParseOptionsVersionV3(t *testing.T) {
	got, err := ParseOptions(url.Values{"version": {"v3"}})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if got.Version != "v3" {
		t.Fatalf("Version = %q, want v3", got.Version)
	}

	got, err = ParseOptions(url.Values{"schemaVersion": {"botHomepage.v3"}})
	if err != nil {
		t.Fatalf("ParseOptions schemaVersion returned error: %v", err)
	}
	if got.Version != "v3" {
		t.Fatalf("schemaVersion Version = %q, want v3", got.Version)
	}
}

func TestParseOptionsV3Defaults(t *testing.T) {
	got, err := ParseOptions(url.Values{"version": {"v3"}, "chainName": {"btc"}})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if !got.IncludePresence {
		t.Fatal("IncludePresence = false, want true")
	}
	if !got.IncludeSections {
		t.Fatal("IncludeSections = false, want true")
	}
	if !got.IncludeServices {
		t.Fatal("IncludeServices = false, want true")
	}
	if !got.IncludeBuzzes {
		t.Fatal("IncludeBuzzes = false, want true")
	}
	if !got.IncludeMetaApps {
		t.Fatal("IncludeMetaApps = false, want true")
	}
	if got.IncludeSkills {
		t.Fatal("IncludeSkills = true, want false")
	}
	if got.IncludeProofs {
		t.Fatal("IncludeProofs = true, want false")
	}
	if got.ServiceSize != homepageSectionLimit {
		t.Fatalf("ServiceSize = %d, want homepage section limit %d", got.ServiceSize, homepageSectionLimit)
	}
	if got.ChainName != "" {
		t.Fatalf("ChainName = %q, want empty", got.ChainName)
	}
}

func TestParseOptionsV3Toggles(t *testing.T) {
	got, err := ParseOptions(url.Values{
		"version":                 {"v3"},
		"includePresence":         {"false"},
		"includeSections":         {"false"},
		"includeServices":         {"false"},
		"includeChats":            {"false"},
		"includeBuzzes":           {"false"},
		"includeMetaApps":         {"false"},
		"includeInactiveServices": {"true"},
	})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if got.IncludePresence {
		t.Fatal("IncludePresence = true, want false")
	}
	if got.IncludeSections {
		t.Fatal("IncludeSections = true, want false")
	}
	if got.IncludeServices {
		t.Fatal("IncludeServices = true, want false")
	}
	if got.IncludeBuzzes {
		t.Fatal("IncludeBuzzes = true, want false")
	}
	if got.IncludeMetaApps {
		t.Fatal("IncludeMetaApps = true, want false")
	}
	if !got.IncludeInactiveServices {
		t.Fatal("IncludeInactiveServices = false, want true")
	}
}

func TestParseOptionsDefaultSelectorRemainsV1(t *testing.T) {
	got, err := ParseOptions(url.Values{})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if got.Version != "" {
		t.Fatalf("Version = %q, want empty v1 selector", got.Version)
	}
}

func TestParseOptionsSchemaVersionV2KeepsV2Defaults(t *testing.T) {
	got, err := ParseOptions(url.Values{"schemaVersion": {"botHomepage.v2"}})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if got.Version != "v2" {
		t.Fatalf("Version = %q, want v2", got.Version)
	}
	if !got.IncludeSkills {
		t.Fatal("IncludeSkills = false, want true")
	}
	if !got.IncludeProofs {
		t.Fatal("IncludeProofs = false, want true")
	}
}

func TestParseOptionsV2SectionControlsAndFixedServiceSize(t *testing.T) {
	got, err := ParseOptions(url.Values{
		"version":         {"v2"},
		"serviceSize":     {"99"},
		"includeMetaApps": {"false"},
		"includeSkills":   {"false"},
		"includeBuzzes":   {"false"},
	})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if got.ServiceSize != homepageSectionLimit {
		t.Fatalf("ServiceSize = %d, want fixed v2 homepage limit %d", got.ServiceSize, homepageSectionLimit)
	}
	if got.IncludeMetaApps {
		t.Fatal("IncludeMetaApps = true, want false")
	}
	if got.IncludeSkills {
		t.Fatal("IncludeSkills = true, want false")
	}
	if got.IncludeBuzzes {
		t.Fatal("IncludeBuzzes = true, want false")
	}

	got, err = ParseOptions(url.Values{"version": {"v2"}, "serviceSize": {"not-public"}})
	if err != nil {
		t.Fatalf("v2 serviceSize should be ignored instead of rejected: %v", err)
	}
	if got.ServiceSize != homepageSectionLimit {
		t.Fatalf("ignored v2 ServiceSize = %d, want %d", got.ServiceSize, homepageSectionLimit)
	}
}

func TestParseOptionsV1ServiceSizeCompatibility(t *testing.T) {
	got, err := ParseOptions(url.Values{"serviceSize": {"7"}})
	if err != nil {
		t.Fatalf("ParseOptions returned error: %v", err)
	}
	if got.Version != "" {
		t.Fatalf("Version = %q, want v1/default", got.Version)
	}
	if got.ServiceSize != 7 {
		t.Fatalf("ServiceSize = %d, want v1 query size 7", got.ServiceSize)
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
		{"version": {"v3"}, "includeChats": {"maybe"}},
	}
	for _, values := range cases {
		if _, err := ParseOptions(values); err == nil {
			t.Fatalf("ParseOptions(%v) expected error", values)
		}
	}
}

func TestParseOptionsIgnoresIncludeChatsOutsideV3(t *testing.T) {
	if _, err := ParseOptions(url.Values{"version": {"v2"}, "includeChats": {"maybe"}}); err != nil {
		t.Fatalf("v2 includeChats should be ignored, got error: %v", err)
	}
	if _, err := ParseOptions(url.Values{"includeChats": {"maybe"}}); err != nil {
		t.Fatalf("default/v1 includeChats should be ignored, got error: %v", err)
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
