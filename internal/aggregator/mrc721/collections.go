package mrc721

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// CollectionRecord is one MRC-721 collection row of the local read model.
// MetaSo does not re-index /nft/mrc721 collection pins itself; rows are
// refreshed from the manapi-compatible collection page list (upsert by
// pinid) and served through /api/mrc721/collection/search and
// /api/mrc721/address/collections-issued. Seq orders the recency index.
type CollectionRecord struct {
	CollectionName   string `json:"collectionName"`
	Name             string `json:"name,omitempty"`
	Desc             string `json:"desc,omitempty"`
	TotalSupply      int64  `json:"totalSupply,omitempty"`
	Totalnum         int64  `json:"totalnum,omitempty"`
	RoyaltyRate      int64  `json:"royaltyRate,omitempty"`
	PinId            string `json:"pinid,omitempty"`
	CreateAddress    string `json:"createAddress,omitempty"`
	CreateMetaId     string `json:"createMetaId,omitempty"`
	CreateTime       int64  `json:"createTime,omitempty"`
	Cover            string `json:"cover,omitempty"`
	GenesisChainName string `json:"genesisChainName,omitempty"`
	MintedCount      int64  `json:"mintedCount,omitempty"`
	// Upstream extras kept for completeness of the cached row (not part of
	// the served contract).
	Website       string `json:"website,omitempty"`
	Metadata      string `json:"metadata,omitempty"`
	GenesisHeight int64  `json:"genesisHeight,omitempty"`
	GenesisTx     string `json:"genesisTx,omitempty"`

	Seq uint64 `json:"seq"`
}

// CollectionClient fetches the manapi-compatible MRC-721 collection page
// list: GET {baseURL}/api/mrc721/collection/pageList?cursor=&size= answered
// with {code:1,message,data:{list:[rows],cursor}}.
type CollectionClient struct {
	baseURL    string
	httpClient *http.Client
}

func NewCollectionClient(baseURL string, httpClient *http.Client) *CollectionClient {
	return &CollectionClient{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		httpClient: httpClient,
	}
}

// collectionPage is one upstream page.
type collectionPage struct {
	Records    []*CollectionRecord
	NextCursor string
}

// ListPage fetches one upstream page. cursor "" starts the sweep.
func (c *CollectionClient) ListPage(ctx context.Context, cursor string, size int) (*collectionPage, error) {
	if c == nil || strings.TrimSpace(c.baseURL) == "" {
		return nil, errors.New("mrc721 collection base URL is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := url.Parse(c.baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse mrc721 collection base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("mrc721 collection base URL requires scheme and host")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/mrc721/collection/pageList"
	q := parsed.Query()
	q.Set("cursor", cursor)
	if size > 0 {
		q.Set("size", strconv.Itoa(size))
	}
	parsed.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create mrc721 collection request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch mrc721 collection page list: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("mrc721 collection page list returned HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("read mrc721 collection response: %w", err)
	}
	return decodeCollectionPage(raw)
}

// decodeCollectionPage accepts the manapi envelope shapes seen in the wild
// ({code,data:{list,cursor}} and the flat {code,list,cursor}); code 0 and 1
// both mean success (same tolerance as the shared backfill client).
func decodeCollectionPage(raw []byte) (*collectionPage, error) {
	var envelope struct {
		Code       int               `json:"code"`
		Message    string            `json:"message"`
		List       []json.RawMessage `json:"list"`
		NextCursor string            `json:"nextCursor"`
		Cursor     string            `json:"cursor"`
		Data       struct {
			List       []json.RawMessage `json:"list"`
			NextCursor string            `json:"nextCursor"`
			Cursor     string            `json:"cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode mrc721 collection response: %w", err)
	}
	if envelope.Code != 0 && envelope.Code != 1 {
		return nil, fmt.Errorf("mrc721 collection page list failed: code=%d message=%s", envelope.Code, envelope.Message)
	}
	list := envelope.Data.List
	if list == nil {
		list = envelope.List
	}
	nextCursor := firstNonEmptyString(envelope.Data.NextCursor, envelope.Data.Cursor, envelope.NextCursor, envelope.Cursor)

	page := &collectionPage{NextCursor: nextCursor}
	for _, entry := range list {
		rec, err := decodeCollectionRow(entry)
		if err != nil {
			// A malformed row is skipped, not fatal: the sweep continues and
			// the row re-arrives on the next sync.
			continue
		}
		page.Records = append(page.Records, rec)
	}
	return page, nil
}

// decodeCollectionRow parses one upstream collection row with exact-key
// first, case-insensitive-fallback resolution (camelCase / lowercase
// duplicates tolerated).
func decodeCollectionRow(raw json.RawMessage) (*CollectionRecord, error) {
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&fields); err != nil {
		return nil, err
	}
	rec := &CollectionRecord{}
	rec.CollectionName, _ = payloadString(fields, "collectionName")
	rec.Name, _ = payloadString(fields, "name")
	rec.Desc, _ = payloadString(fields, "desc")
	rec.TotalSupply, _ = payloadInt(fields, "totalSupply")
	rec.Totalnum, _ = payloadInt(fields, "totalnum")
	rec.RoyaltyRate, _ = payloadInt(fields, "royaltyRate")
	rec.PinId, _ = payloadString(fields, "pinid")
	rec.CreateAddress, _ = payloadString(fields, "createAddress")
	rec.CreateMetaId, _ = payloadString(fields, "createMetaId")
	rec.CreateTime, _ = payloadInt(fields, "createTime")
	if rec.CreateTime > 100000000000 {
		rec.CreateTime /= 1000
	}
	rec.Cover, _ = payloadString(fields, "cover")
	rec.GenesisChainName, _ = payloadString(fields, "genesisChainName")
	rec.MintedCount, _ = payloadInt(fields, "mintedCount")
	rec.Website, _ = payloadString(fields, "website")
	rec.Metadata, _ = payloadString(fields, "metadata")
	rec.GenesisHeight, _ = payloadInt(fields, "genesisHeight")
	rec.GenesisTx, _ = payloadString(fields, "genesisTx")
	if strings.TrimSpace(rec.PinId) == "" && strings.TrimSpace(rec.CollectionName) == "" {
		return nil, errors.New("collection row has neither pinid nor collectionName")
	}
	rec.PinId = strings.TrimSpace(rec.PinId)
	return rec, nil
}

// CollectionSyncStats is the completion report of one full pageList sweep.
type CollectionSyncStats struct {
	Pages    int
	Fetched  int64
	Upserted int64
	Skipped  int64
}

// SyncCollections sweeps the upstream page list until exhausted and upserts
// every row into the read model. Idempotent. Refresh policy: the registry
// is append-mostly (collection_desc first-wins), so rows are only ever
// added or refreshed, never deleted.
func (a *Aggregator) SyncCollections(ctx context.Context, client *CollectionClient, pageSize int) (*CollectionSyncStats, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("mrc721 aggregator is not initialised")
	}
	if client == nil {
		return nil, errors.New("mrc721 collection client is required")
	}
	if pageSize <= 0 {
		pageSize = 100
	}
	if ctx == nil {
		ctx = context.Background()
	}

	stats := &CollectionSyncStats{}
	cursor := ""
	seenCursors := make(map[string]struct{})
	for {
		seenCursors[cursor] = struct{}{}
		page, err := client.ListPage(ctx, cursor, pageSize)
		if err != nil {
			return stats, err
		}
		stats.Pages++
		stats.Fetched += int64(len(page.Records))
		a.mu.Lock()
		for _, rec := range page.Records {
			upserted, err := a.upsertCollection(rec)
			if err != nil {
				a.mu.Unlock()
				return stats, err
			}
			if upserted {
				stats.Upserted++
			} else {
				stats.Skipped++
			}
		}
		a.mu.Unlock()
		if page.NextCursor == "" || len(page.Records) == 0 {
			break
		}
		if _, seen := seenCursors[page.NextCursor]; seen {
			return stats, errors.New("repeated mrc721 collection pageList cursor")
		}
		cursor = page.NextCursor
	}
	return stats, nil
}

// upsertCollection writes one row (callers hold a.mu). Rows without a pinid
// are skipped (nothing to key on).
func (a *Aggregator) upsertCollection(rec *CollectionRecord) (bool, error) {
	if rec == nil || strings.TrimSpace(rec.PinId) == "" {
		return false, nil
	}
	if existing, err := a.loadCollection(rec.PinId); err == nil && existing != nil && collectionRowsEqual(existing, rec) {
		return false, nil // unchanged row: keep the original seq/position
	}
	seq, err := a.nextSeq()
	if err != nil {
		return false, err
	}
	if existing, err := a.loadCollection(rec.PinId); err == nil && existing != nil && existing.Seq > 0 {
		if err := a.store.Delete(Namespace, collectionTimeKey(existing.Seq, rec.PinId)); err != nil {
			return false, err
		}
	}
	rec.Seq = seq
	raw, err := json.Marshal(rec)
	if err != nil {
		return false, err
	}
	if err := a.store.Set(Namespace, collectionKey(rec.PinId), raw); err != nil {
		return false, err
	}
	return true, a.store.Set(Namespace, collectionTimeKey(rec.Seq, rec.PinId), []byte("1"))
}

// collectionRowsEqual compares the content fields (not seq) of two rows.
func collectionRowsEqual(a, b *CollectionRecord) bool {
	x, y := *a, *b
	x.Seq, y.Seq = 0, 0
	return x == y
}

func (a *Aggregator) loadCollection(pinId string) (*CollectionRecord, error) {
	raw, err := a.store.Get(Namespace, collectionKey(pinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rec CollectionRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("corrupt collection record %s: %w", pinId, err)
	}
	return &rec, nil
}

// CollectionPage is one page of a collection query.
type CollectionPage struct {
	List       []*CollectionRecord
	Total      int
	NextCursor string
}

// SearchCollections serves GET /api/mrc721/collection/search: keyword is a
// case-insensitive substring over collectionName / name / desc with an
// exact (case-insensitive) pinid alternative; issuer matches createAddress
// exactly (base58 is case-sensitive) or createMetaId case-insensitively.
// Both filters are optional and combine conjunctively.
func (a *Aggregator) SearchCollections(keyword, issuer, cursor string, size int) (*CollectionPage, error) {
	cursorSeq, err := decodeSeqCursor(cursor)
	if err != nil {
		return nil, err
	}
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	issuer = strings.TrimSpace(issuer)
	if keyword == "" && issuer == "" {
		return nil, ErrInvalidParameter
	}

	matches, err := a.scanCollections(func(rec *CollectionRecord) bool {
		if issuer != "" && !collectionIssuedBy(rec, issuer) {
			return false
		}
		if keyword != "" && !collectionMatchesKeyword(rec, keyword) {
			return false
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return pageCollections(matches, cursorSeq, size), nil
}

// CollectionsIssuedByAddress serves GET
// /api/mrc721/address/collections-issued: rows whose createAddress equals
// the requested address.
func (a *Aggregator) CollectionsIssuedByAddress(address, cursor string, size int) (*CollectionPage, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return nil, ErrInvalidParameter
	}
	cursorSeq, err := decodeSeqCursor(cursor)
	if err != nil {
		return nil, err
	}
	matches, err := a.scanCollections(func(rec *CollectionRecord) bool {
		return rec.CreateAddress == address
	})
	if err != nil {
		return nil, err
	}
	return pageCollections(matches, cursorSeq, size), nil
}

// scanCollections walks the collection recency index newest-first, dropping
// stale index entries, and collects rows matching the predicate.
func (a *Aggregator) scanCollections(predicate func(*CollectionRecord) bool) ([]*CollectionRecord, error) {
	matches := make([]*CollectionRecord, 0)
	err := a.store.ScanPrefix(Namespace, collectionTimePrefix(), func(key, _ []byte) error {
		seq, pinId, ok := parseCollectionTimeKey(key)
		if !ok {
			return nil
		}
		rec, err := a.loadCollection(pinId)
		if err != nil || rec == nil {
			return nil
		}
		if rec.Seq != seq {
			return nil
		}
		if predicate(rec) {
			matches = append(matches, rec)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return matches, nil
}

// pageCollections applies cursor pagination to a newest-first match list.
func pageCollections(matches []*CollectionRecord, cursorSeq uint64, size int) *CollectionPage {
	start := 0
	if cursorSeq > 0 {
		for start < len(matches) && matches[start].Seq >= cursorSeq {
			start++
		}
	}
	size = clampListSize(size)
	end := start + size
	if end > len(matches) {
		end = len(matches)
	}
	page := &CollectionPage{List: matches[start:end], Total: len(matches)}
	if end < len(matches) && end > start {
		page.NextCursor = encodeSeqCursor(page.List[len(page.List)-1].Seq)
	}
	if page.List == nil {
		page.List = []*CollectionRecord{}
	}
	return page
}

// collectionIssuedBy reports whether the row was issued by the given
// address or metaid.
func collectionIssuedBy(rec *CollectionRecord, issuer string) bool {
	return rec.CreateAddress == issuer || (rec.CreateMetaId != "" && strings.EqualFold(rec.CreateMetaId, issuer))
}

// collectionMatchesKeyword implements the R-A keyword rule.
func collectionMatchesKeyword(rec *CollectionRecord, keyword string) bool {
	if keyword == "" {
		return true
	}
	if strings.EqualFold(rec.PinId, keyword) && rec.PinId != "" {
		return true
	}
	haystacks := []string{rec.CollectionName, rec.Name, rec.Desc}
	for _, hay := range haystacks {
		if hay != "" && strings.Contains(strings.ToLower(hay), keyword) {
			return true
		}
	}
	return false
}

// RunCollectionSyncLoop drives the periodic collection refresh: one initial
// sweep, then a sweep per tick until stop closes. Errors are logged and
// never fatal — the read model keeps serving the previous sweep.
func RunCollectionSyncLoop(ctx context.Context, agg *Aggregator, client *CollectionClient, interval, timeout time.Duration, pageSize int, stop <-chan struct{}, logf func(format string, args ...interface{})) {
	if agg == nil || client == nil {
		return
	}
	if logf == nil {
		logf = func(string, ...interface{}) {}
	}
	runOnce := func() {
		syncCtx, cancel := context.WithTimeout(ctx, timeout)
		stats, err := agg.SyncCollections(syncCtx, client, pageSize)
		cancel()
		if err != nil {
			logf("WARNING: mrc721 collection sync: %v", err)
		}
		if stats != nil {
			logf("mrc721 collection sync done: pages=%d fetched=%d upserted=%d skipped=%d", stats.Pages, stats.Fetched, stats.Upserted, stats.Skipped)
		}
	}
	runOnce()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
