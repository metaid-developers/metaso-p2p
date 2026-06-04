package skillservice

import "testing"

func TestAssetResolver_Resolve(t *testing.T) {
	const base = "https://example.com/content"

	cases := []struct {
		name  string
		base  string
		input string
		want  string
	}{
		// Empty / whitespace → empty out, never a half URL.
		{"empty", base, "", ""},
		{"whitespace", base, "   ", ""},

		// Already absolute http(s) → passthrough (with trim).
		{"http passthrough", base, "http://other.example.com/x.png", "http://other.example.com/x.png"},
		{"https passthrough", base, "https://cdn.example.com/img/abc.png", "https://cdn.example.com/img/abc.png"},
		{"https with whitespace trimmed", base, "  https://cdn.example.com/x.png  ", "https://cdn.example.com/x.png"},
		{"uppercase scheme is still passthrough", base, "HTTPS://CDN.EXAMPLE.COM/A.PNG", "HTTPS://CDN.EXAMPLE.COM/A.PNG"},
		{"legacy manapi content url rehomed", base, "https://manapi.metaid.io/content/old-avatar:i0", "https://example.com/content/old-avatar:i0"},
		{"file indexer content url normalised", base, "https://file.metaid.io/metafile-indexer/content/current-avatar:i0", "https://example.com/content/current-avatar:i0"},

		// metafile:// and metafile: forms.
		{"metafile:// stripped + joined", base, "metafile://abc123i0", "https://example.com/content/abc123i0"},
		{"metafile: shorthand", base, "metafile:abc123i0", "https://example.com/content/abc123i0"},
		{"metafile with leading slash on id", base, "metafile:///abc123i0", "https://example.com/content/abc123i0"},

		// /content/ prefix (userinfo Avatar style).
		{"content prefix stripped + rejoined", base, "/content/abc123i0", "https://example.com/content/abc123i0"},

		// Bare pin id assumed.
		{"bare pin id joined", base, "abc123i0", "https://example.com/content/abc123i0"},

		// Base URL with trailing slash is normalised.
		{"trailing slash on base", "https://example.com/content/", "abc123i0", "https://example.com/content/abc123i0"},

		// Empty base URL falls back to the input (useful for tests).
		{"no base url returns id only", "", "abc123i0", "abc123i0"},
		{"no base url passthrough http", "", "http://x/y", "http://x/y"},

		// metafile: with empty body and empty base. We still trim
		// leading slashes off the id; an empty id collapses to "".
		{"metafile: empty id", base, "metafile:", ""},
		{"metafile:// empty id", base, "metafile://", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewAssetResolver(tc.base)
			got := r.Resolve(tc.input)
			if got != tc.want {
				t.Errorf("Resolve(%q) base=%q got %q want %q", tc.input, tc.base, got, tc.want)
			}
		})
	}
}

func TestAssetResolver_NilSafe(t *testing.T) {
	var r *AssetResolver
	if got := r.Resolve("abc"); got != "abc" {
		t.Errorf("nil resolver should pass through, got %q", got)
	}
	if got := r.BaseURL(); got != "" {
		t.Errorf("nil resolver BaseURL should be empty, got %q", got)
	}
}

func TestAggregator_ResolveAsset(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	// Before SetAssetBaseURL: passthrough.
	if got := agg.ResolveAsset("abc123i0"); got != "abc123i0" {
		t.Errorf("unset resolver should passthrough, got %q", got)
	}

	agg.SetAssetBaseURL("https://example.com/content")
	if got := agg.ResolveAsset("metafile://abc123i0"); got != "https://example.com/content/abc123i0" {
		t.Errorf("after SetAssetBaseURL: got %q", got)
	}
	if got := agg.ResolveAsset("https://other.example.com/x.png"); got != "https://other.example.com/x.png" {
		t.Errorf("absolute URL passthrough broken, got %q", got)
	}
}

func TestAggregator_SetAssetBaseURL_TrailingSlashTolerated(t *testing.T) {
	agg, store := setupAggregator(t)
	defer store.Close()

	agg.SetAssetBaseURL("https://x.com/content/")
	if got := agg.ResolveAsset("abc"); got != "https://x.com/content/abc" {
		t.Errorf("trailing slash not normalised: %q", got)
	}
}
