package mrc721

import (
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// SellIntentFilter is the query surface of GET /api/mrc721/sell/intents.
// Empty fields match everything; Active==nil means "no active filter".
type SellIntentFilter struct {
	CollectionPinId string
	CollectionName  string
	ItemPinId       string
	SellerAddress   string
	Active          *bool
}

// SellIntentPage is one page of the latest-wins sell-intent list. NextCursor
// is "" at the end of the list.
type SellIntentPage struct {
	List       []*SellIntentRecord
	Total      int
	NextCursor string
}

// encodeSeqCursor renders a pagination cursor (opaque base64 of the write
// sequence).
func encodeSeqCursor(seq uint64) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(seq, 10)))
}

// decodeSeqCursor parses a pagination cursor; a non-empty invalid cursor is
// a client error.
func decodeSeqCursor(cursor string) (uint64, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, ErrInvalidParameter
	}
	seq, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil {
		return 0, ErrInvalidParameter
	}
	return seq, nil
}

// clampListSize applies the house list clamps (default 20, max 100).
func clampListSize(size int) int {
	if size <= 0 {
		return defaultListSize
	}
	if size > maxListSize {
		return maxListSize
	}
	return size
}

// ListSellIntents pages the sell-intent index newest-write-first, applying
// the given filters. The cursor anchors strictly after the previously
// returned row's write sequence, so inserts never shuffle pages.
func (a *Aggregator) ListSellIntents(filter SellIntentFilter, cursor string, size int) (*SellIntentPage, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("mrc721 aggregator is not initialised")
	}
	cursorSeq, err := decodeSeqCursor(cursor)
	if err != nil {
		return nil, err
	}
	size = clampListSize(size)

	// Exact item lookup short-circuits the index scan (merged view: an
	// overlay declaration wins over the confirmed record, R-F-4).
	if filter.ItemPinId != "" {
		rec, _, err := a.effectiveSellIntent(filter.ItemPinId)
		if err != nil {
			return nil, err
		}
		page := &SellIntentPage{List: []*SellIntentRecord{}}
		if rec != nil && sellIntentMatches(rec, filter) {
			page.List = append(page.List, rec)
			page.Total = 1
		}
		return page, nil
	}

	matches := make([]*SellIntentRecord, 0)
	err = a.store.ScanPrefix(Namespace, intentTimePrefix(), func(key, _ []byte) error {
		seq, itemPinId, ok := parseIntentTimeKey(key)
		if !ok {
			return nil
		}
		rec, err := a.loadSellIntent(itemPinId)
		if err != nil || rec == nil {
			// Corrupt record or dangling index entry: drop the entry so the
			// scan self-heals.
			if err == nil && rec == nil {
				a.deleteSellIntentIndexEntry(seq, itemPinId)
			}
			return nil
		}
		if rec.Seq != seq {
			// Stale index position left by an interrupted overwrite; the
			// item's newer entry covers it.
			a.deleteSellIntentIndexEntry(seq, itemPinId)
			return nil
		}
		matches = append(matches, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Zero-conf merge (R-F-4): overlay declarations take effect on top of
	// the confirmed latest-wins records — overlay rows lead the list (they
	// are the freshest), covered confirmed rows drop out. Filters apply to
	// the MERGED rows: a mempool cancellation hides the intent from an
	// active-filtered list.
	matches = a.mergeSellOverlay(matches)
	filtered := make([]*SellIntentRecord, 0, len(matches))
	for _, rec := range matches {
		if sellIntentMatches(rec, filter) {
			filtered = append(filtered, rec)
		}
	}
	matches = filtered

	// matches is newest-first; the cursor skips everything at or after the
	// anchor row (seq >= anchor means same row or newer). Overlay rows carry
	// no write sequence (0), so a cursor page never re-serves them.
	start := 0
	if cursorSeq > 0 {
		for start < len(matches) && matches[start].Seq >= cursorSeq {
			start++
		}
	}
	end := start + size
	if end > len(matches) {
		end = len(matches)
	}
	page := &SellIntentPage{
		List:  matches[start:end],
		Total: len(matches),
	}
	if end < len(matches) && end > start {
		page.NextCursor = encodeSeqCursor(page.List[len(page.List)-1].Seq)
	}
	if page.List == nil {
		page.List = []*SellIntentRecord{}
	}
	return page, nil
}

// SellIntentInfo resolves the latest declaration of one item (merged view:
// an overlay declaration wins over the confirmed record, R-F-4); nil means
// the item has no declaration.
func (a *Aggregator) SellIntentInfo(itemPinId string) (*SellIntentRecord, error) {
	itemPinId = strings.TrimSpace(itemPinId)
	if itemPinId == "" {
		return nil, ErrInvalidParameter
	}
	rec, _, err := a.effectiveSellIntent(itemPinId)
	return rec, err
}

// mergeSellOverlay folds the sell declaration overlay into a newest-first
// confirmed match list: overlay rows lead, covered confirmed rows drop.
func (a *Aggregator) mergeSellOverlay(matches []*SellIntentRecord) []*SellIntentRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.zeroConfTrade || len(a.overlayDecls) == 0 {
		return matches
	}
	overlayByItem := make(map[string]*overlayDeclEntry)
	for _, entry := range a.overlayDecls {
		if entry.sell == nil {
			continue
		}
		if prev, ok := overlayByItem[entry.sell.ItemPinId]; !ok || overlayBefore(prev, entry) {
			overlayByItem[entry.sell.ItemPinId] = entry
		}
	}
	if len(overlayByItem) == 0 {
		return matches
	}
	covered := make(map[string]struct{}, len(overlayByItem))
	overlayRecs := make([]*overlayDeclEntry, 0, len(overlayByItem))
	for _, entry := range overlayByItem {
		overlayRecs = append(overlayRecs, entry)
		covered[entry.sell.ItemPinId] = struct{}{}
	}
	// newest-first by provisional order
	sort.Slice(overlayRecs, func(i, j int) bool { return overlayBefore(overlayRecs[j], overlayRecs[i]) })
	out := make([]*SellIntentRecord, 0, len(matches)+len(overlayRecs))
	for _, entry := range overlayRecs {
		out = append(out, entry.sell)
	}
	for _, rec := range matches {
		if _, cov := covered[rec.ItemPinId]; cov {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// sellIntentMatches applies the remaining list filters to a record.
func sellIntentMatches(rec *SellIntentRecord, filter SellIntentFilter) bool {
	if rec == nil {
		return false
	}
	if filter.SellerAddress != "" && rec.SellerAddress != filter.SellerAddress {
		return false
	}
	if filter.CollectionPinId != "" && !strings.EqualFold(rec.CollectionPinId, filter.CollectionPinId) {
		return false
	}
	if filter.CollectionName != "" && !strings.EqualFold(rec.CollectionName, filter.CollectionName) {
		return false
	}
	if filter.Active != nil && rec.Active != *filter.Active {
		return false
	}
	return true
}
