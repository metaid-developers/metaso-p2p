package metawebdoc

import (
	"regexp"
	"strings"
)

var (
	// markdownHeading strips ATX heading markers at line starts.
	markdownHeading = regexp.MustCompile(`(?m)^\s{0,3}#{1,6}\s+`)
	// markdownImage reduces an image to its alt text.
	markdownImage = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	// markdownLink reduces a link to its text.
	markdownLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	// markdownMarkers removes emphasis/code markers.
	markdownMarkers = strings.NewReplacer("**", "", "__", "", "~~", "", "*", "", "`", "")
)

// StripMarkdown removes heading/emphasis markers and reduces links/images to
// their text, per the search spec's "markdown-stripped" definition.
func StripMarkdown(s string) string {
	if s == "" {
		return ""
	}
	s = markdownImage.ReplaceAllString(s, "$1")
	s = markdownLink.ReplaceAllString(s, "$1")
	s = markdownHeading.ReplaceAllString(s, "")
	s = markdownMarkers.Replace(s)
	return s
}

// CapRunes truncates s to at most max runes.
func CapRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
