package metaweb

import (
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

// Content-level result deduplication (2026-09-02, Q1b of
// docs/specs/2026-09-02-metaweb-search-quality.md). Publishers re-post
// identical or near-identical content as brand-new create pins, so one
// logical document can occupy several result rows. Modify-chain collapse
// (Q1a) is already guaranteed at index time: snapshots are keyed by
// chain:sourcePinId. This file suppresses duplicates ACROSS separate chains,
// same publisher only — cross-publisher identical bodies (quote/citation
// reposts) are out of scope by contract.

// scoredDoc is one filtered match: the shared immutable search document plus
// its relevance score (0 under sort=newest).
type scoredDoc struct {
	doc   metawebdoc.Document
	score int
}

// versionEntry is one member row of a versions group (extra.versions).
type versionEntry struct {
	PinId        string `json:"pinId"`
	CurrentPinId string `json:"currentPinId"`
	Title        string `json:"title"`
	CreatedAt    int64  `json:"createdAt"`
	Version      string `json:"version"`
}

// groupedDoc is one deduplicated result row: the representative match, the
// number of suppressed rows, and — for versions groups only — the member
// list, newest first, with the representative at index 0.
type groupedDoc struct {
	rep            scoredDoc
	duplicateCount int
	versions       []versionEntry
}

// latinVersionMarker matches explicit version markers such as v2 or v1.2;
// the leading boundary keeps "rev2" or "eva2" from matching.
var latinVersionMarker = regexp.MustCompile(`(?i)\bv\d+(?:\.\d+)*`)

// chineseVersionMarkers are literal Chinese version markers. 勘误版 (errata),
// 修订版 (revised), 最终版/终版 (final), 重发版 (repost) mark deliberate
// re-publications.
var chineseVersionMarkers = []string{"勘误版", "修订版", "最终版", "终版", "重发版"}

// extractVersionMarker returns the normalized version marker of a title: all
// latin v-matches (lowercased) plus any Chinese markers, joined with "+".
// Empty when the title carries no explicit version marker.
func extractVersionMarker(title string) string {
	var parts []string
	for _, m := range latinVersionMarker.FindAllString(title, -1) {
		parts = append(parts, strings.ToLower(m))
	}
	for _, marker := range chineseVersionMarkers {
		if strings.Contains(title, marker) {
			parts = append(parts, marker)
		}
	}
	return strings.Join(parts, "+")
}

// stripVersionMarkers removes every version marker from the title. Leftover
// empty brackets are harmless: normalizeDedupeText drops punctuation anyway.
func stripVersionMarkers(title string) string {
	stripped := latinVersionMarker.ReplaceAllString(title, " ")
	for _, marker := range chineseVersionMarkers {
		stripped = strings.ReplaceAll(stripped, marker, " ")
	}
	return stripped
}

// normalizeDedupeText lowercases and drops whitespace, punctuation, and
// symbol runes, keeping letters/digits (CJK included). Two titles or content
// excerpts that differ only in spacing or punctuation normalize equal.
func normalizeDedupeText(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// publisherDedupeKey scopes grouping to one publisher. Documents without any
// publisher identity fall back to their own pin id, so they never group.
func publisherDedupeKey(doc *metawebdoc.Document) string {
	if id := strings.ToLower(strings.TrimSpace(doc.PublisherGlobalMetaId)); id != "" {
		return id
	}
	if id := strings.ToLower(strings.TrimSpace(doc.PublisherMetaId)); id != "" {
		return id
	}
	return "\x00nopub\x00" + doc.SourcePinId
}

// dedupeMatches groups the sorted match list into result rows. Group order
// follows the earliest member's sorted position; within a hard-collapse
// group the representative is that earliest member (highest score, then
// newest — the sorted order), while a versions group (members carrying two
// or more distinct version markers, no-marker counting as one value) keeps
// the newest member as representative and lists every member in versions.
func dedupeMatches(matches []scoredDoc) []groupedDoc {
	parent := make([]int, len(matches))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(i, j int) {
		ri, rj := find(i), find(j)
		if ri != rj {
			parent[rj] = ri
		}
	}

	// First occurrence of each equivalence key unions later matches into it.
	seen := make(map[string]int, len(matches)*2)
	for i := range matches {
		doc := &matches[i].doc
		pub := publisherDedupeKey(doc)
		if body := normalizeDedupeText(doc.ContentExcerpt); body != "" {
			key := pub + "\x00body\x00" + body
			if first, ok := seen[key]; ok {
				union(first, i)
			} else {
				seen[key] = i
			}
		}
		if title := normalizeDedupeText(stripVersionMarkers(doc.Title)); title != "" {
			key := pub + "\x00title\x00" + title
			if first, ok := seen[key]; ok {
				union(first, i)
			} else {
				seen[key] = i
			}
		}
	}

	groupsByRoot := make(map[int][]int)
	var rootOrder []int
	for i := range matches {
		root := find(i)
		if _, ok := groupsByRoot[root]; !ok {
			rootOrder = append(rootOrder, root)
		}
		groupsByRoot[root] = append(groupsByRoot[root], i)
	}
	// Roots were registered at their earliest member, so rootOrder is already
	// the sorted-position order of the result rows.
	groups := make([]groupedDoc, 0, len(rootOrder))
	for _, root := range rootOrder {
		members := groupsByRoot[root]
		groups = append(groups, buildGroup(matches, members))
	}
	return groups
}

// buildGroup classifies one member set (indices into matches, in sorted
// order) and picks its representative.
func buildGroup(matches []scoredDoc, members []int) groupedDoc {
	repIdx := members[0]
	group := groupedDoc{rep: matches[repIdx], duplicateCount: len(members) - 1}
	if len(members) == 1 {
		return group
	}

	distinct := make(map[string]struct{}, len(members))
	for _, i := range members {
		distinct[extractVersionMarker(matches[i].doc.Title)] = struct{}{}
	}
	if len(distinct) < 2 {
		return group // hard collapse
	}

	// Versions group: representative = newest member (createdAt desc, ties
	// keep sorted order); versions lists every member newest first.
	byNewest := make([]int, len(members))
	copy(byNewest, members)
	sort.SliceStable(byNewest, func(a, b int) bool {
		return matches[byNewest[a]].doc.CreatedAt > matches[byNewest[b]].doc.CreatedAt
	})
	repIdx = byNewest[0]
	group.rep = matches[repIdx]
	group.versions = make([]versionEntry, 0, len(byNewest))
	for _, i := range byNewest {
		doc := matches[i].doc
		currentPinId := doc.CurrentPinId
		if currentPinId == "" {
			currentPinId = doc.SourcePinId
		}
		group.versions = append(group.versions, versionEntry{
			PinId:        doc.SourcePinId,
			CurrentPinId: currentPinId,
			Title:        doc.Title,
			CreatedAt:    doc.CreatedAt,
			Version:      extractVersionMarker(doc.Title),
		})
	}
	return group
}
