package mrc721nft

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

// Native collection search surface backing the IDBots endpoints
// /api/mrc721/collection/search and /api/mrc721/address/collections-issued
// once the mrc721 aggregate hands them over. The wire shape (SearchRow)
// and cursor format (base64 of a decimal uint64) are byte-compatible with
// the upstream-refreshed read model they replace — only the source changes.

// SearchRow is one collection summary row; JSON tags mirror the
// mrc721 collectionRow shape exactly (omitempty on every optional field).
type SearchRow struct {
	CollectionName   string `json:"collectionName"`
	Name             string `json:"name,omitempty"`
	Desc             string `json:"desc,omitempty"`
	TotalSupply      int64  `json:"totalSupply,omitempty"`
	Totalnum         int64  `json:"totalnum,omitempty"`
	RoyaltyRate      int64  `json:"royaltyRate,omitempty"`
	Pinid            string `json:"pinid,omitempty"`
	CreateAddress    string `json:"createAddress,omitempty"`
	CreateMetaId     string `json:"createMetaId,omitempty"`
	CreateTime       int64  `json:"createTime,omitempty"`
	Cover            string `json:"cover,omitempty"`
	GenesisChainName string `json:"genesisChainName,omitempty"`
	MintedCount      int64  `json:"mintedCount,omitempty"`

	// Zero-conf fields (R-F-5): present only on overlay-created rows (the
	// pointer nils out on confirmed rows so their bytes stay unchanged).
	Confirmed   *bool `json:"confirmed,omitempty"`
	FirstSeenAt int64 `json:"firstSeenAt,omitempty"`
}

func searchRowFrom(col *Collection) *SearchRow {
	if col == nil {
		return nil
	}
	return &SearchRow{
		CollectionName:   col.CollectionName,
		Name:             col.Name,
		Desc:             col.Desc,
		TotalSupply:      col.TotalSupply,
		Totalnum:         col.TotalNum,
		RoyaltyRate:      int64(col.RoyaltyRate),
		Pinid:            col.PinId,
		CreateAddress:    col.CreateAddress,
		CreateMetaId:     col.CreateMetaId,
		CreateTime:       col.CreateTime,
		Cover:            col.Cover,
		GenesisChainName: col.GenesisChainName,
		MintedCount:      col.TotalNum,
	}
}

// ErrInvalidSearch mirrors the mrc721 sentinel the HTTP layer maps onto its
// invalid-parameter wire code.
var ErrInvalidSearch = errors.New("invalid search parameter")

// searchRowMatchesIssuer mirrors the mrc721 issuer rule: createAddress
// matches exactly (base58 is case-sensitive) or createMetaId
// case-insensitively.
func searchRowMatchesIssuer(row *SearchRow, issuer string) bool {
	if row.CreateAddress == issuer {
		return true
	}
	return strings.EqualFold(row.CreateMetaId, issuer)
}

// searchRowMatchesKeyword mirrors the mrc721 keyword rule: case-insensitive
// substring over collectionName / name / desc, or an exact
// (case-insensitive) pinid.
func searchRowMatchesKeyword(row *SearchRow, keyword string) bool {
	if strings.EqualFold(row.Pinid, keyword) {
		return true
	}
	return strings.Contains(strings.ToLower(row.CollectionName), keyword) ||
		strings.Contains(strings.ToLower(row.Name), keyword) ||
		strings.Contains(strings.ToLower(row.Desc), keyword)
}

// decodeSearchCursor parses the shared base64(decimal uint64) cursor into a
// row offset; a non-empty invalid cursor is a client error.
func decodeSearchCursor(cursor string) (int64, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil {
		return 0, ErrInvalidSearch
	}
	offset, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
	if err != nil || offset < 0 {
		return 0, ErrInvalidSearch
	}
	return offset, nil
}

func encodeSearchCursor(offset int64) string {
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatInt(offset, 10)))
}

// SearchNativeCollections serves the collection/search contract over the
// native store: keyword case-insensitive substring over collectionName /
// name / desc with an exact (case-insensitive) pinid alternative, issuer
// createAddress-exact or createMetaId-case-insensitive, both filters
// optional and conjunctive, at least one required.
func (a *Aggregator) SearchNativeCollections(keyword, issuer, cursor string, size int, zeroConf bool) ([]*SearchRow, int, string, error) {
	offset, err := decodeSearchCursor(cursor)
	if err != nil {
		return nil, 0, "", err
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	issuer = strings.TrimSpace(issuer)
	if keyword == "" && issuer == "" {
		return nil, 0, "", ErrInvalidSearch
	}
	rows, total, next, err := a.nativeRows(offset, size, func(row *SearchRow) bool {
		if issuer != "" && !searchRowMatchesIssuer(row, issuer) {
			return false
		}
		if keyword != "" && !searchRowMatchesKeyword(row, keyword) {
			return false
		}
		return true
	}, zeroConf)
	return rows, total, next, err
}

// NativeCollectionsIssued serves the collections-issued contract over the
// native store: rows whose createAddress equals the requested address.
func (a *Aggregator) NativeCollectionsIssued(address, cursor string, size int, zeroConf bool) ([]*SearchRow, int, string, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, 0, "", ErrInvalidSearch
	}
	offset, err := decodeSearchCursor(cursor)
	if err != nil {
		return nil, 0, "", err
	}
	return a.nativeRows(offset, size, func(row *SearchRow) bool {
		return row.CreateAddress == address
	}, zeroConf)
}

// nativeRows materializes the matching name-ordered rows, applies offset
// pagination with the shared cursor format and clamps size (default 20,
// max 100 — same clamps as the mrc721 handlers).
func (a *Aggregator) nativeRows(offset int64, size int, predicate func(*SearchRow) bool, zeroConf bool) ([]*SearchRow, int, string, error) {
	if size <= 0 {
		size = 20
	}
	if size > 100 {
		size = 100
	}
	var matches []*SearchRow
	// iterate the collection main table in key (name) order
	err := a.store.ScanPrefix(Namespace, []byte(keyCollection), func(_, value []byte) error {
		var col Collection
		if err := json.Unmarshal(value, &col); err != nil {
			return nil // skip corrupt rows
		}
		if row := searchRowFrom(&col); predicate(row) {
			matches = append(matches, row)
		}
		return nil
	})
	if err != nil {
		return nil, 0, "", err
	}
	// Overlay-created albums join the search surface (R-F-2 acceptance) in
	// the same (collectionName, issuer) key order as the confirmed rows.
	if zeroConf {
		overlay := a.overlaySearchRows()
		for _, row := range overlay {
			if predicate(row) {
				matches = append(matches, row)
			}
		}
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].CollectionName != matches[j].CollectionName {
				return matches[i].CollectionName < matches[j].CollectionName
			}
			return matches[i].CreateAddress < matches[j].CreateAddress
		})
	}
	total := len(matches)
	if offset >= int64(total) {
		return []*SearchRow{}, total, "", nil
	}
	end := offset + int64(size)
	if end > int64(total) {
		end = int64(total)
	}
	page := matches[offset:end]
	next := ""
	if end < int64(total) && len(page) > 0 {
		next = encodeSearchCursor(end)
	}
	return page, total, next, nil
}

// overlaySearchRows serializes the overlay-created albums for the search
// surfaces (gate on): MintedCount carries the staged + overlay mint count,
// Confirmed=false marks the row provisional.
func (a *Aggregator) overlaySearchRows() []*SearchRow {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := a.overlaySnapshotLocked()
	rows := make([]*SearchRow, 0)
	for _, entry := range overlayAlbumsLocked(snap) {
		view := a.albumViewByPairLocked(snap, entry.ColCreate.CollectionName, entry.ColCreate.CreateAddress, viewOptions{})
		if view == nil || view.collection == nil {
			continue
		}
		row := searchRowFrom(view.collection)
		row.Totalnum = 0
		row.MintedCount = view.stagedMintCount + view.overlayMintCount
		confirmed := false
		row.Confirmed = &confirmed
		row.FirstSeenAt = view.firstSeenAt
		rows = append(rows, row)
	}
	return rows
}

// ItemCollectionRef resolves the collection identity of one indexed item
// for the sibling mrc721 package's offer-row joins (the mrc721.ItemCollectionSource
// implementation): empty strings when the item is unknown to this aggregate.
func (a *Aggregator) ItemCollectionRef(itemPinId string) (string, string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.db == nil {
		return "", ""
	}
	item, err := a.db.GetItem(itemPinId)
	if err != nil || item == nil {
		return "", ""
	}
	return item.CollectionPinId, item.CollectionName
}
