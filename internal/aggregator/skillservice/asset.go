package skillservice

import (
	"net/url"
	"strings"
)

// AssetResolver turns a chain-declared asset reference into something the
// frontend can load directly. The Bot Hub spec requires that every
// `serviceIcon` / `providerAvatar` returned by the list / detail endpoints
// is either an absolute http(s) URL or `${baseURL}/<id>` — never a bare
// pin id, never a `metafile://` URI, never an empty string for known
// values.
//
// The resolver is intentionally tiny so each rule is greppable:
//   - empty input         → "" (caller decides whether to omit / keep field)
//   - already http(s) URL → returned verbatim after a trim
//   - "metafile://<id>"   → "<baseURL>/<id>"
//   - "/content/<id>"     → "<baseURL>/<id>" (matches userinfo Avatar)
//   - anything else       → "<baseURL>/<input>" (assume bare pin id)
//
// baseURL is configurable via METASO_P2P_ASSET_BASE_URL; the value is
// passed through config.BotHubConfig.AssetBaseURL. A trailing slash on
// the base URL is tolerated.
type AssetResolver struct {
	baseURL string
}

// NewAssetResolver builds a resolver. The base URL is normalised once on
// construction so per-call work stays minimal.
func NewAssetResolver(baseURL string) *AssetResolver {
	return &AssetResolver{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
	}
}

// BaseURL returns the configured asset base URL (without trailing slash).
// Useful for diagnostic logging and config-summary endpoints.
func (r *AssetResolver) BaseURL() string {
	if r == nil {
		return ""
	}
	return r.baseURL
}

// Resolve converts a chain-declared asset reference into a load-ready URL.
//
// Pass through cases (returned verbatim):
//   - "" stays "" so the caller can decide whether to omit the field
//   - an existing http:// or https:// URL is kept (callers control whether
//     to validate further). We do strip surrounding whitespace.
//
// Normalised cases (joined with baseURL):
//   - "metafile://<id>" → strip the scheme, treat the remainder as the id
//   - "/content/<id>"   → strip the prefix, treat the remainder as the id
//     (this is what the userinfo aggregator stores for Avatar fields)
//   - anything else     → assume bare pin id and join directly
//
// When baseURL is empty the function returns the input unchanged — this is
// the test-time default and is safer than silently returning a partial
// URL. Production deployments must configure METASO_P2P_ASSET_BASE_URL
// (config.Default sets it to the documented recommendation).
func (r *AssetResolver) Resolve(asset string) string {
	if r == nil {
		return asset
	}
	asset = strings.TrimSpace(asset)
	if asset == "" {
		return ""
	}
	lower := strings.ToLower(asset)
	switch {
	case isLegacyManAPIContentURL(asset), isFileIndexerContentURL(asset):
		return r.joinID(assetIDFromContentURL(asset))
	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		return asset
	case strings.HasPrefix(lower, "metafile://"):
		// "metafile://" is 11 chars; the spec sometimes also writes
		// "metafile:" (no slashes) so we accept the shorter form too.
		id := asset[len("metafile://"):]
		return r.joinID(id)
	case strings.HasPrefix(lower, "metafile:"):
		return r.joinID(asset[len("metafile:"):])
	case strings.HasPrefix(asset, "/content/"):
		return r.joinID(asset[len("/content/"):])
	default:
		return r.joinID(asset)
	}
}

func isLegacyManAPIContentURL(asset string) bool {
	parsed, err := url.Parse(strings.TrimSpace(asset))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, "manapi.metaid.io") && strings.HasPrefix(parsed.Path, "/content/")
}

func isFileIndexerContentURL(asset string) bool {
	parsed, err := url.Parse(strings.TrimSpace(asset))
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, "file.metaid.io") && strings.HasPrefix(parsed.Path, "/metafile-indexer/content/")
}

func assetIDFromContentURL(asset string) string {
	parsed, err := url.Parse(strings.TrimSpace(asset))
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return strings.TrimSpace(parts[len(parts)-1])
}

// joinID appends an id to the configured base URL. Returns the id
// unchanged when no base URL is set, so the caller does not get a
// half-constructed URL.
func (r *AssetResolver) joinID(id string) string {
	id = strings.TrimLeft(strings.TrimSpace(id), "/")
	if id == "" {
		return ""
	}
	if r.baseURL == "" {
		return id
	}
	return r.baseURL + "/" + id
}
