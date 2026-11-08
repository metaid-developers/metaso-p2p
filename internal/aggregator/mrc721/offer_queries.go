// Query surface of the passive-offer index (R-E-2,
// docs/nft-offer-indexer-requirements.md): the /api/mrc721/offers list and
// the per-item /api/mrc721/offer/info lookup, shaped like the sell-intent
// queries (inverted-seq recency index, seq-anchored cursor, house clamps).

package mrc721

import (
	"errors"
	"sort"
	"strings"
)

// OfferFilter is the query surface of GET /api/mrc721/offers. Empty fields
// match everything; Active==nil means "no active filter".
type OfferFilter struct {
	ItemPinId       string
	SellerMetaId    string
	CollectionPinId string
	Active          *bool
}

// OfferPage is one page of the latest-wins offer list. NextCursor is "" at
// the end of the list.
type OfferPage struct {
	List       []*OfferRecord
	Total      int
	NextCursor string
}

// ItemCollectionSource abstracts the native mrc721nft item read model for
// the offer rows' best-effort item → collection join (R-E-2: collectionPinId
// filtering/joining "where available"). Empty strings mean the item is
// unknown to the source.
type ItemCollectionSource interface {
	ItemCollectionRef(itemPinId string) (collectionPinId string, collectionName string)
}

// SetItemCollectionSource hands the offer rows the native item read model
// (called from the daemon startup after both aggregators registered; nil
// keeps the rows collection-free).
func (a *Aggregator) SetItemCollectionSource(src ItemCollectionSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.itemCollectionSrc = src
}

// itemCollectionSource snapshots the optional join source for a request.
func (a *Aggregator) itemCollectionSource() ItemCollectionSource {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.itemCollectionSrc
}

// offerCollectionRef resolves one item's collection identity through the
// native item read model (best-effort: empty strings when unavailable).
func (a *Aggregator) offerCollectionRef(itemPinId string) (string, string) {
	if src := a.itemCollectionSource(); src != nil {
		return src.ItemCollectionRef(itemPinId)
	}
	return "", ""
}

// ListOffers pages the offer index newest-write-first, applying the given
// filters. The cursor anchors strictly after the previously returned row's
// write sequence, so inserts never shuffle pages. The collectionPinId
// filter joins through the native item read model when available; with the
// source absent the filter matches nothing (an honest empty page).
func (a *Aggregator) ListOffers(filter OfferFilter, cursor string, size int) (*OfferPage, error) {
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
		rec, _, err := a.effectiveOffer(filter.ItemPinId)
		if err != nil {
			return nil, err
		}
		page := &OfferPage{List: []*OfferRecord{}}
		if rec != nil {
			collectionPinId, _ := a.offerCollectionRef(rec.ItemPinId)
			if a.offerMatches(rec, filter, collectionPinId) {
				page.List = append(page.List, rec)
				page.Total = 1
			}
		}
		return page, nil
	}

	matches := make([]*OfferRecord, 0)
	err = a.store.ScanPrefix(Namespace, offerTimePrefix(), func(key, _ []byte) error {
		seq, itemPinId, ok := parseOfferTimeKey(key)
		if !ok {
			return nil
		}
		rec, err := a.loadOffer(itemPinId)
		if err != nil || rec == nil {
			// Corrupt record or dangling index entry: drop the entry so the
			// scan self-heals.
			if err == nil && rec == nil {
				a.deleteOfferIndexEntry(seq, itemPinId)
			}
			return nil
		}
		if rec.Seq != seq {
			// Stale index position left by an interrupted overwrite; the
			// item's newer entry covers it.
			a.deleteOfferIndexEntry(seq, itemPinId)
			return nil
		}
		matches = append(matches, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Zero-conf merge (R-F-4): overlay declarations lead the list, covered
	// confirmed rows drop out. Filters apply to the MERGED rows: a mempool
	// cancellation hides the offer from an active-filtered list.
	matches = a.mergeOfferOverlay(matches)
	filtered := make([]*OfferRecord, 0, len(matches))
	for _, rec := range matches {
		if filterNeedsCollection(filter) {
			collectionPinId, _ := a.offerCollectionRef(rec.ItemPinId)
			if !a.offerMatches(rec, filter, collectionPinId) {
				continue
			}
		} else if !a.offerMatches(rec, filter, "") {
			continue
		}
		filtered = append(filtered, rec)
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
	page := &OfferPage{
		List:  matches[start:end],
		Total: len(matches),
	}
	if end < len(matches) && end > start {
		page.NextCursor = encodeSeqCursor(page.List[len(page.List)-1].Seq)
	}
	if page.List == nil {
		page.List = []*OfferRecord{}
	}
	return page, nil
}

// filterNeedsCollection reports whether the filter consults the item →
// collection join.
func filterNeedsCollection(filter OfferFilter) bool {
	return filter.CollectionPinId != ""
}

// OfferInfo resolves the latest offer of one item (merged view: an overlay
// declaration wins over the confirmed record, R-F-4; any active state —
// cancellations stay queryable); nil means the item has no offer.
func (a *Aggregator) OfferInfo(itemPinId string) (*OfferRecord, error) {
	itemPinId = strings.TrimSpace(itemPinId)
	if itemPinId == "" {
		return nil, ErrInvalidParameter
	}
	rec, _, err := a.effectiveOffer(itemPinId)
	return rec, err
}

// mergeOfferOverlay folds the offer declaration overlay into a newest-first
// confirmed match list.
func (a *Aggregator) mergeOfferOverlay(matches []*OfferRecord) []*OfferRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.zeroConfTrade || len(a.overlayDecls) == 0 {
		return matches
	}
	overlayByItem := make(map[string]*overlayDeclEntry)
	for _, entry := range a.overlayDecls {
		if entry.offer == nil {
			continue
		}
		if prev, ok := overlayByItem[entry.offer.ItemPinId]; !ok || overlayBefore(prev, entry) {
			overlayByItem[entry.offer.ItemPinId] = entry
		}
	}
	if len(overlayByItem) == 0 {
		return matches
	}
	covered := make(map[string]struct{}, len(overlayByItem))
	overlayRecs := make([]*overlayDeclEntry, 0, len(overlayByItem))
	for _, entry := range overlayByItem {
		overlayRecs = append(overlayRecs, entry)
		covered[entry.offer.ItemPinId] = struct{}{}
	}
	sort.Slice(overlayRecs, func(i, j int) bool { return overlayBefore(overlayRecs[j], overlayRecs[i]) })
	out := make([]*OfferRecord, 0, len(matches)+len(overlayRecs))
	for _, entry := range overlayRecs {
		out = append(out, entry.offer)
	}
	for _, rec := range matches {
		if _, cov := covered[rec.ItemPinId]; cov {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// offerMatches applies the remaining list filters to a record;
// collectionPinId is the record's joined collection identity ("" when the
// join source is unavailable).
func (a *Aggregator) offerMatches(rec *OfferRecord, filter OfferFilter, collectionPinId string) bool {
	if rec == nil {
		return false
	}
	if filter.SellerMetaId != "" && rec.SellerMetaId != filter.SellerMetaId {
		return false
	}
	if filter.CollectionPinId != "" && !strings.EqualFold(collectionPinId, filter.CollectionPinId) {
		return false
	}
	if filter.Active != nil && rec.Active != *filter.Active {
		return false
	}
	return true
}
