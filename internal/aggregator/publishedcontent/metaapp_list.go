package publishedcontent

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ListMetaApps runs the /api/metaapp/list pipeline: iterate the metaapp
// reverse-time index, filter, score (when keyword is present), sort, and
// paginate with an opaque offset cursor. The catalog is small enough that
// collecting all matches before sorting is fine; revisit if metaapp counts
// grow past the skill-service catalog assumptions.
//
// With Sort=preference the same matches are instead ordered by the
// open-preference score of each item's video identity (metaso requirement
// R4); null scores sort after every scored item in both directions.
func (a *Aggregator) ListMetaApps(params MetaAppListParams) (*MetaAppListResult, error) {
	params = normaliseMetaAppListParams(params)
	if params.Sort == "preference" {
		return a.listMetaAppsByPreference(params)
	}
	offset, err := decodeMetaAppCursor(params.Cursor)
	if err != nil {
		return nil, err
	}

	tokens := metaAppKeywordTokens(params.Keyword)
	matches, err := a.collectMetaAppMatches(params, tokens)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		left := matches[i].rec.sortTimestamp()
		right := matches[j].rec.sortTimestamp()
		if left != right {
			return left > right
		}
		if matches[i].rec.ChainName != matches[j].rec.ChainName {
			return matches[i].rec.ChainName < matches[j].rec.ChainName
		}
		return matches[i].rec.SourcePinId < matches[j].rec.SourcePinId
	})

	result := sliceMetaAppMatches(matches, offset, params.Size, func(s scoredItem) MetaAppItem { return s.item })
	for i := range result.Items {
		a.enrichMetaAppPublisher(&result.Items[i])
	}
	return result, nil
}

type scoredItem struct {
	rec   *Record
	item  MetaAppItem
	score int
}

// collectMetaAppMatches scans the metaapp reverse-time index applying the
// shared filter set (chain, visibility, time window, disabled, tag, runtime,
// publisher, keyword). It is the shared candidate source of the default and
// preference listings.
func (a *Aggregator) collectMetaAppMatches(params MetaAppListParams, tokens []string) ([]scoredItem, error) {
	matches := make([]scoredItem, 0)
	err := a.store.ScanPrefix(Namespace, byTimeProtocolPrefix(PathMetaApp), func(key, _ []byte) error {
		chainName, sourcePinId, ok := parseByTimeIndexKey(key, byTimeProtocolPrefix(PathMetaApp))
		if !ok {
			return nil
		}
		rec, err := a.loadRecord(chainName, PathMetaApp, sourcePinId)
		if err != nil || rec == nil {
			return err
		}
		if params.ChainName != "" && rec.ChainName != params.ChainName {
			return nil
		}
		if rec.Hidden {
			return nil
		}
		if params.Since > 0 && rec.sortTimestamp() < params.Since {
			return nil
		}
		if params.Until > 0 && rec.sortTimestamp() > params.Until {
			return nil
		}
		item := metaAppItemFromRecord(rec)
		if !params.IncludeDisabled && item.Disabled {
			return nil
		}
		if !metaAppTagFilterMatch(item.Tags, params.Tag) {
			return nil
		}
		if params.Runtime != "" && !strings.Contains(strings.ToLower(item.Runtime), strings.ToLower(params.Runtime)) {
			return nil
		}
		if params.Publisher != "" && !metaAppPublisherMatch(rec, params.Publisher) {
			return nil
		}
		score := 0
		if len(tokens) > 0 {
			var matched bool
			score, matched = metaAppKeywordScore(item, tokens)
			if !matched {
				return nil
			}
		}
		matches = append(matches, scoredItem{rec: rec, item: item, score: score})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// listMetaAppsByPreference is the sort=preference listing (metaso
// requirement R4). Candidates come from the shared filter scan; ordering is
// the selected-basis score in the requested direction (null scores always
// last), then comparisons descending, update recency, then stable app root
// id ascending. The cursor is bound to the whole query fingerprint so
// changing basis, chain, filters or score version invalidates it.
func (a *Aggregator) listMetaAppsByPreference(params MetaAppListParams) (*MetaAppListResult, error) {
	fingerprint := preferenceCursorFingerprint(params)
	offset, err := decodeMetaAppPreferenceCursor(params.Cursor, fingerprint)
	if err != nil {
		return nil, err
	}

	tokens := metaAppKeywordTokens(params.Keyword)
	matches, err := a.collectMetaAppMatches(params, tokens)
	if err != nil {
		return nil, err
	}

	ascending := params.Order == "asc"
	if a.preferenceLookup == nil {
		// Fail before filtering so callers never mistake an unwired score
		// source for "no evidence yet".
		return nil, errPreferenceLookupUnavailable
	}
	type prefItem struct {
		match   scoredItem
		summary MetaAppPreferenceOpen
	}
	prefs := make([]prefItem, 0, len(matches))
	for _, match := range matches {
		videoSha256 := ""
		if match.item.MotionVideo != nil {
			videoSha256 = match.item.MotionVideo.VideoSha256
		}
		if params.Learning != "" {
			if match.item.MotionVideo == nil || match.item.MotionVideo.Learning.Status != params.Learning {
				continue
			}
		}
		block, err := a.metaAppPreferenceBlock(match.item.ChainName, videoSha256)
		if err != nil {
			return nil, err
		}
		summary := block.Open
		if params.MinJudges > 0 && summary.Judges < params.MinJudges {
			continue
		}
		prefs = append(prefs, prefItem{match: match, summary: summary})
	}

	sort.SliceStable(prefs, func(i, j int) bool {
		left, right := prefs[i].summary, prefs[j].summary
		// Null scores sort after every scored item in both directions.
		if (left.Score == nil) != (right.Score == nil) {
			return right.Score == nil
		}
		if left.Score != nil && *left.Score != *right.Score {
			if ascending {
				return *left.Score < *right.Score
			}
			return *left.Score > *right.Score
		}
		if left.Comparisons != right.Comparisons {
			return left.Comparisons > right.Comparisons
		}
		leftTs := prefs[i].match.rec.sortTimestamp()
		rightTs := prefs[j].match.rec.sortTimestamp()
		if leftTs != rightTs {
			return leftTs > rightTs
		}
		if prefs[i].match.rec.ChainName != prefs[j].match.rec.ChainName {
			return prefs[i].match.rec.ChainName < prefs[j].match.rec.ChainName
		}
		return prefs[i].match.rec.SourcePinId < prefs[j].match.rec.SourcePinId
	})

	if offset > len(prefs) {
		offset = len(prefs)
	}
	page := prefs[offset:]
	hasMore := len(page) > params.Size
	if hasMore {
		page = page[:params.Size]
	}
	items := make([]MetaAppItem, 0, len(page))
	for _, p := range page {
		item := p.match.item
		block := MetaAppPreferenceBlock{Open: p.summary, Validated: UnavailableMetaAppPreferenceValidated()}
		item.Preference = &block
		items = append(items, item)
	}
	result := &MetaAppListResult{Items: items, HasMore: hasMore}
	if hasMore {
		result.NextCursor = encodeMetaAppPreferenceCursor(offset+params.Size, fingerprint)
	}
	for i := range result.Items {
		a.enrichMetaAppPublisher(&result.Items[i])
	}
	return result, nil
}

// MetaAppDetail resolves any version pinId to its latest record. Without a
// chainName hint it scans the metaapp time index, then falls back to the
// pin_to_source mapping for mid-chain version pinIds.
func (a *Aggregator) MetaAppDetail(pinID, chainName string) (*MetaAppDetail, error) {
	rec, err := a.findMetaAppRecord(pinID, chainName)
	if err != nil || rec == nil || rec.Hidden {
		return nil, err
	}
	detail := metaAppDetailFromRecord(rec)
	a.enrichMetaAppPublisher(&detail.MetaAppItem)
	return detail, nil
}

func (a *Aggregator) findMetaAppRecord(pinID, chainName string) (*Record, error) {
	pinID = strings.TrimSpace(pinID)
	if pinID == "" {
		return nil, nil
	}
	if chainName != "" {
		return a.loadRecordByAnyPinId(strings.ToLower(chainName), PathMetaApp, pinID)
	}

	chains := map[string]bool{}
	var found *Record
	err := a.store.ScanPrefix(Namespace, byTimeProtocolPrefix(PathMetaApp), func(key, _ []byte) error {
		chain, sourcePinId, ok := parseByTimeIndexKey(key, byTimeProtocolPrefix(PathMetaApp))
		if !ok {
			return nil
		}
		chains[chain] = true
		if sourcePinId != pinID {
			rec, err := a.loadRecord(chain, PathMetaApp, sourcePinId)
			if err != nil {
				return err
			}
			if rec == nil || rec.CurrentPinId != pinID {
				return nil
			}
			found = rec
			return errStopScan
		}
		rec, err := a.loadRecord(chain, PathMetaApp, sourcePinId)
		if err != nil {
			return err
		}
		if rec != nil {
			found = rec
			return errStopScan
		}
		return nil
	})
	if err != nil && err != errStopScan {
		return nil, err
	}
	if found != nil {
		return found, nil
	}
	for chain := range chains {
		rec, err := a.loadRecordByAnyPinId(chain, PathMetaApp, pinID)
		if err != nil || rec != nil {
			return rec, err
		}
	}
	return nil, nil
}

// ListMetaAppForks returns direct children of the given app version chain.
// A child qualifies when its forkedFrom points at any version of the parent
// chain (resolved through pin_to_source, same-chain only).
func (a *Aggregator) ListMetaAppForks(pinID, chainName string, size int, cursor string) (*MetaAppListResult, bool, error) {
	parent, err := a.findMetaAppRecord(pinID, chainName)
	if err != nil || parent == nil || parent.Hidden {
		return nil, false, err
	}
	offset, err := decodeMetaAppCursor(cursor)
	if err != nil {
		return nil, true, err
	}
	if size <= 0 {
		size = defaultMetaAppListSize
	}
	if size > maxMetaAppListSize {
		size = maxMetaAppListSize
	}

	children := make([]*Record, 0)
	err = a.store.ScanPrefix(Namespace, byTimeProtocolPrefix(PathMetaApp), func(key, _ []byte) error {
		chain, sourcePinId, ok := parseByTimeIndexKey(key, byTimeProtocolPrefix(PathMetaApp))
		if !ok || chain != parent.ChainName || sourcePinId == parent.SourcePinId {
			return nil
		}
		rec, err := a.loadRecord(chain, PathMetaApp, sourcePinId)
		if err != nil || rec == nil {
			return err
		}
		if rec.Hidden {
			return nil
		}
		item := metaAppItemFromRecord(rec)
		if item.ForkedFrom == "" {
			return nil
		}
		if item.ForkedFrom == parent.SourcePinId || item.ForkedFrom == parent.CurrentPinId ||
			a.sourcePinIdFor(chain, item.ForkedFrom) == parent.SourcePinId {
			children = append(children, rec)
		}
		return nil
	})
	if err != nil {
		return nil, true, err
	}

	sort.SliceStable(children, func(i, j int) bool {
		if children[i].CreatedAt != children[j].CreatedAt {
			return children[i].CreatedAt > children[j].CreatedAt
		}
		return children[i].SourcePinId < children[j].SourcePinId
	})

	result := sliceMetaAppMatches(children, offset, size, func(rec *Record) MetaAppItem { return metaAppItemFromRecord(rec) })
	for i := range result.Items {
		a.enrichMetaAppPublisher(&result.Items[i])
	}
	return result, true, nil
}

func normaliseMetaAppListParams(params MetaAppListParams) MetaAppListParams {
	params.Keyword = strings.TrimSpace(params.Keyword)
	params.Tag = strings.TrimSpace(params.Tag)
	params.ChainName = strings.ToLower(strings.TrimSpace(params.ChainName))
	params.Runtime = strings.TrimSpace(params.Runtime)
	params.Publisher = strings.TrimSpace(params.Publisher)
	if params.Size <= 0 {
		params.Size = defaultMetaAppListSize
	}
	if params.Size > maxMetaAppListSize {
		params.Size = maxMetaAppListSize
	}
	return params
}

func metaAppKeywordTokens(keyword string) []string {
	fields := strings.Fields(strings.ToLower(keyword))
	if len(fields) == 0 {
		return nil
	}
	return fields
}

// metaAppKeywordScore implements AND semantics: every token must hit at
// least one corpus field. Per token the best tier wins — tag 3, title /
// appName 2, intro 1.
func metaAppKeywordScore(item MetaAppItem, tokens []string) (int, bool) {
	score := 0
	title := strings.ToLower(item.Title + " " + item.AppName)
	intro := strings.ToLower(item.Intro)
	for _, token := range tokens {
		tagHit := false
		for _, tag := range item.Tags {
			if strings.Contains(strings.ToLower(tag), token) {
				tagHit = true
				break
			}
		}
		switch {
		case tagHit:
			score += 3
		case strings.Contains(title, token):
			score += 2
		case strings.Contains(intro, token):
			score += 1
		default:
			return 0, false
		}
	}
	return score, true
}

func metaAppTagFilterMatch(tags []string, filter string) bool {
	if filter == "" {
		return true
	}
	for _, want := range strings.Split(filter, ",") {
		want = strings.ToLower(strings.TrimSpace(want))
		if want == "" {
			continue
		}
		for _, tag := range tags {
			if strings.ToLower(tag) == want {
				return true
			}
		}
	}
	return false
}

func metaAppPublisherMatch(rec *Record, publisher string) bool {
	for _, candidate := range []string{rec.PublisherGlobalMetaId, rec.PublisherMetaId, rec.PublisherAddress} {
		if candidate != "" && strings.EqualFold(candidate, publisher) {
			return true
		}
	}
	return false
}

func sliceMetaAppMatches[T any](all []T, offset, size int, toItem func(T) MetaAppItem) *MetaAppListResult {
	if offset > len(all) {
		offset = len(all)
	}
	page := all[offset:]
	hasMore := len(page) > size
	if hasMore {
		page = page[:size]
	}
	items := make([]MetaAppItem, 0, len(page))
	for _, m := range page {
		items = append(items, toItem(m))
	}
	result := &MetaAppListResult{Items: items, HasMore: hasMore}
	if hasMore {
		result.NextCursor = encodeMetaAppCursor(offset + size)
	}
	return result
}

// Opaque offset cursor, same wire format as the skill-service list.
type metaAppCursorPayload struct {
	Offset int `json:"o"`
}

func encodeMetaAppCursor(offset int) string {
	raw, _ := json.Marshal(metaAppCursorPayload{Offset: offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeMetaAppCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	var p metaAppCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	if p.Offset < 0 {
		return 0, fmt.Errorf("invalid cursor")
	}
	return p.Offset, nil
}

// Preference-listing cursor: an offset bound to the query fingerprint, so
// an outstanding cursor is rejected whenever basis, chain, filters or score
// version change (metaso requirement R4).
type metaAppPreferenceCursorPayload struct {
	Offset      int    `json:"o"`
	Fingerprint string `json:"f,omitempty"`
}

func encodeMetaAppPreferenceCursor(offset int, fingerprint string) string {
	raw, _ := json.Marshal(metaAppPreferenceCursorPayload{Offset: offset, Fingerprint: fingerprint})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeMetaAppPreferenceCursor(cursor, fingerprint string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	var p metaAppPreferenceCursorPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("invalid cursor")
	}
	if p.Offset < 0 || p.Fingerprint != fingerprint {
		return 0, fmt.Errorf("invalid cursor")
	}
	return p.Offset, nil
}
