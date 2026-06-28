package bothomepage

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	defaultServiceSize = 20
	maxServiceSize     = 100
)

// Options captures the read-model expansion knobs accepted by the bot
// homepage endpoint.
type Options struct {
	Version                 string
	IncludeServices         bool
	IncludeSections         bool
	IncludeChats            bool
	IncludeMetaApps         bool
	IncludeSkills           bool
	IncludeBuzzes           bool
	ServiceSize             int
	IncludeInactiveServices bool
	IncludeProofs           bool
	IncludePresence         bool
	ChainName               string
}

// DefaultOptions returns the default query expansion knobs for the Bot
// homepage endpoint.
func DefaultOptions() Options {
	return Options{
		IncludeServices: true,
		IncludeChats:    true,
		IncludeMetaApps: true,
		IncludeSkills:   true,
		IncludeBuzzes:   true,
		ServiceSize:     defaultServiceSize,
		IncludeProofs:   true,
		IncludePresence: true,
	}
}

// ParseOptions normalizes and validates bot homepage query parameters.
func ParseOptions(values url.Values) (Options, error) {
	opts := DefaultOptions()
	opts.Version = parseVersion(values)
	if opts.Version == "v3" {
		return parseV3Options(values)
	}
	opts.IncludeSections = opts.Version == "v2"
	opts.ChainName = strings.ToLower(strings.TrimSpace(values.Get("chainName")))

	var err error
	if opts.IncludeServices, err = parseBool(values, "includeServices", opts.IncludeServices); err != nil {
		return Options{}, err
	}
	if opts.IncludeSections, err = parseBool(values, "includeSections", opts.IncludeSections); err != nil {
		return Options{}, err
	}
	if opts.Version == "v2" {
		if opts.IncludeMetaApps, err = parseBool(values, "includeMetaApps", opts.IncludeMetaApps); err != nil {
			return Options{}, err
		}
		if opts.IncludeSkills, err = parseBool(values, "includeSkills", opts.IncludeSkills); err != nil {
			return Options{}, err
		}
		if opts.IncludeBuzzes, err = parseBool(values, "includeBuzzes", opts.IncludeBuzzes); err != nil {
			return Options{}, err
		}
	}
	if opts.IncludeInactiveServices, err = parseBool(values, "includeInactiveServices", opts.IncludeInactiveServices); err != nil {
		return Options{}, err
	}
	if opts.IncludeProofs, err = parseBool(values, "includeProofs", opts.IncludeProofs); err != nil {
		return Options{}, err
	}
	if opts.IncludePresence, err = parseBool(values, "includePresence", opts.IncludePresence); err != nil {
		return Options{}, err
	}
	if opts.Version == "v2" {
		opts.ServiceSize = homepageSectionLimit
	} else {
		if opts.ServiceSize, err = parseServiceSize(values); err != nil {
			return Options{}, err
		}
	}

	return opts, nil
}

func parseV3Options(values url.Values) (Options, error) {
	opts := Options{
		Version:         "v3",
		IncludeServices: true,
		IncludeSections: true,
		IncludeChats:    true,
		IncludeMetaApps: true,
		IncludeBuzzes:   true,
		ServiceSize:     homepageSectionLimit,
		IncludePresence: true,
	}

	var err error
	if opts.IncludeServices, err = parseBool(values, "includeServices", opts.IncludeServices); err != nil {
		return Options{}, err
	}
	if opts.IncludeChats, err = parseBool(values, "includeChats", opts.IncludeChats); err != nil {
		return Options{}, err
	}
	if opts.IncludeSections, err = parseBool(values, "includeSections", opts.IncludeSections); err != nil {
		return Options{}, err
	}
	if opts.IncludeMetaApps, err = parseBool(values, "includeMetaApps", opts.IncludeMetaApps); err != nil {
		return Options{}, err
	}
	if opts.IncludeBuzzes, err = parseBool(values, "includeBuzzes", opts.IncludeBuzzes); err != nil {
		return Options{}, err
	}
	if opts.IncludeInactiveServices, err = parseBool(values, "includeInactiveServices", opts.IncludeInactiveServices); err != nil {
		return Options{}, err
	}
	if opts.IncludePresence, err = parseBool(values, "includePresence", opts.IncludePresence); err != nil {
		return Options{}, err
	}

	return opts, nil
}

func parseVersion(values url.Values) string {
	version := strings.ToLower(strings.TrimSpace(values.Get("version")))
	schemaVersion := strings.TrimSpace(values.Get("schemaVersion"))
	if version == "v3" || version == "3" || schemaVersion == schemaVersionV3 {
		return "v3"
	}
	if version == "v2" || schemaVersion == "botHomepage.v2" {
		return "v2"
	}
	return ""
}

func parseBool(values url.Values, key string, fallback bool) (bool, error) {
	rawValues, ok := values[key]
	if !ok {
		return fallback, nil
	}

	raw := ""
	if len(rawValues) > 0 {
		raw = rawValues[0]
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true, nil
	case "0", "false", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid %s value %q", key, raw)
	}
}

func parseServiceSize(values url.Values) (int, error) {
	rawValues, ok := values["serviceSize"]
	if !ok {
		return defaultServiceSize, nil
	}

	raw := ""
	if len(rawValues) > 0 {
		raw = rawValues[0]
	}
	size, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("invalid serviceSize value %q", raw)
	}
	if size < 0 {
		return 0, fmt.Errorf("serviceSize must be non-negative")
	}
	if size == 0 {
		return defaultServiceSize, nil
	}
	if size > maxServiceSize {
		return maxServiceSize, nil
	}
	return size, nil
}
