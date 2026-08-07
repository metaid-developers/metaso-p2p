package socialcontent

import (
	"bytes"
	"container/heap"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// hotWindow limits hot ranking to recent posts, matching the product intent
// that nobody cares about a hot list from months ago.
const hotWindow = 48 * time.Hour

func (a *Aggregator) List(params FeedParams) (*FeedResult, error) {
	params = normaliseFeedParams(params)
	if params.Scope == "following" {
		if a.followLister == nil {
			return nil, errors.New("following feed unavailable")
		}
		followed, err := a.followLister.ListFollowing(params.User)
		if err != nil {
			return nil, err
		}
		if len(followed) == 0 {
			return &FeedResult{Items: []PostItem{}}, nil
		}
		params.Publishers = followed
	}
	if params.Sort == SortHot {
		return a.listHot(params)
	}
	return a.listNewest(params)
}

// listNewest streams the newest-first index and stops once one page plus the
// hasMore probe is collected, so feed reads stay bounded regardless of the
// total post count. The index order matches the newest sort contract.
func (a *Aggregator) listNewest(params FeedParams) (*FeedResult, error) {
	after, err := decodePostCursor(params.Cursor)
	if err != nil {
		return nil, err
	}

	records := make([]*PostRecord, 0)
	seen := make(map[string]struct{})
	prefix := postTimePrefix()
	authorIndexed := len(params.Publishers) == 1
	if authorIndexed {
		prefix = postAuthorPrefix(params.Publishers[0])
	}
	need := params.Size + 1
	var lastKey []byte
	scan := func(key, value []byte) error {
		record, err := a.recordFromIndexKey(key, value, authorIndexed)
		if err != nil || record == nil || record.Hidden || record.IsMempool {
			return err
		}
		if !feedRecordMatches(record, params) {
			return nil
		}
		keyID := record.ChainName + ":" + record.SourcePinId
		if _, ok := seen[keyID]; ok {
			return nil
		}
		seen[keyID] = struct{}{}
		if len(records) < params.Size {
			lastKey = append(lastKey[:0], key...)
		}
		records = append(records, record)
		if len(records) >= need {
			return errStop
		}
		return nil
	}
	if len(after) > 0 {
		err = a.store.ScanPrefixAfter(Namespace, prefix, after, scan)
	} else {
		err = a.store.ScanPrefix(Namespace, prefix, scan)
	}
	if err != nil && err != errStop {
		return nil, err
	}

	hasMore := len(records) > params.Size
	if hasMore {
		records = records[:params.Size]
	}
	result := &FeedResult{Items: make([]PostItem, 0, len(records))}
	for _, record := range records {
		result.Items = append(result.Items, postItemFromRecord(record))
	}
	result.HasMore = hasMore
	if hasMore {
		result.NextCursor = encodePostCursor(lastKey)
	}
	return result, nil
}

// listHot ranks only posts created within hotWindow by the hot score. The
// newest-first index scan stops at the first post older than the window, so
// the request stays fast and memory stays proportional to the page size. The
// result is a top-N snapshot without offset pagination.
func (a *Aggregator) listHot(params FeedParams) (*FeedResult, error) {
	windowStart := time.Now().Unix() - int64(hotWindow.Seconds())
	top := &hotTopHeap{limit: params.Size}
	seen := make(map[string]struct{})
	prefix := postTimePrefix()
	authorIndexed := len(params.Publishers) == 1
	if authorIndexed {
		prefix = postAuthorPrefix(params.Publishers[0])
	}
	if err := a.store.ScanPrefix(Namespace, prefix, func(key, value []byte) error {
		record, err := a.recordFromIndexKey(key, value, authorIndexed)
		if err != nil || record == nil || record.Hidden || record.IsMempool {
			return err
		}
		if record.CreatedAt < windowStart {
			return errStop
		}
		if !feedRecordMatches(record, params) {
			return nil
		}
		keyID := record.ChainName + ":" + record.SourcePinId
		if _, ok := seen[keyID]; ok {
			return nil
		}
		seen[keyID] = struct{}{}
		top.push(record)
		return nil
	}); err != nil && err != errStop {
		return nil, err
	}

	records := top.records()
	sort.SliceStable(records, func(i, j int) bool {
		left, right := postEngagement(records[i]), postEngagement(records[j])
		if left != right {
			return left > right
		}
		if records[i].CreatedAt != records[j].CreatedAt {
			return records[i].CreatedAt > records[j].CreatedAt
		}
		if records[i].ChainName != records[j].ChainName {
			return records[i].ChainName < records[j].ChainName
		}
		return records[i].SourcePinId < records[j].SourcePinId
	})

	result := &FeedResult{Items: make([]PostItem, 0, len(records))}
	for _, record := range records {
		item := postItemFromRecord(record)
		item.HotScore = float64(postEngagement(record))
		result.Items = append(result.Items, item)
	}
	return result, nil
}

type hotHeapItem struct {
	record *PostRecord
	score  int
}

type hotTopHeap struct {
	items []hotHeapItem
	limit int
}

func (h *hotTopHeap) push(record *PostRecord) {
	item := hotHeapItem{record: record, score: postEngagement(record)}
	if len(h.items) < h.limit {
		heap.Push(h, item)
		return
	}
	if h.Len() > 0 && hotWorse(item, h.items[0]) {
		return
	}
	h.items[0] = item
	heap.Fix(h, 0)
}

func (h *hotTopHeap) records() []*PostRecord {
	records := make([]*PostRecord, 0, len(h.items))
	for h.Len() > 0 {
		records = append(records, heap.Pop(h).(hotHeapItem).record)
	}
	return records
}

func (h hotTopHeap) Len() int { return len(h.items) }

func (h hotTopHeap) Less(i, j int) bool {
	return hotWorse(h.items[i], h.items[j])
}

func (h hotTopHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *hotTopHeap) Push(value any) { h.items = append(h.items, value.(hotHeapItem)) }

func (h *hotTopHeap) Pop() any {
	old := h.items
	last := old[len(old)-1]
	h.items = old[:len(old)-1]
	return last
}

// hotWorse reports whether left ranks below right in the hot ordering, so the
// heap root is always the current worst candidate.
func hotWorse(left, right hotHeapItem) bool {
	if left.score != right.score {
		return left.score < right.score
	}
	// The heap root must be the worst candidate: lowest engagement, then
	// oldest, then lexicographically largest chain/source.
	if left.record.CreatedAt != right.record.CreatedAt {
		return left.record.CreatedAt < right.record.CreatedAt
	}
	if left.record.ChainName != right.record.ChainName {
		return left.record.ChainName > right.record.ChainName
	}
	return left.record.SourcePinId > right.record.SourcePinId
}

func postEngagement(record *PostRecord) int {
	if record == nil {
		return 0
	}
	return record.LikeCount + record.CommentCount + record.DonateCount
}

func feedRecordMatches(record *PostRecord, params FeedParams) bool {
	if params.ChainName != "" && record.ChainName != params.ChainName {
		return false
	}
	if params.Since > 0 && record.CreatedAt < params.Since {
		return false
	}
	if params.Until > 0 && record.CreatedAt > params.Until {
		return false
	}
	if len(params.Publishers) > 0 {
		matched := false
		for _, publisher := range params.Publishers {
			if publisherMatch(record, publisher) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(params.Keywords) > 0 {
		matched := false
		for _, keyword := range params.Keywords {
			if keywordMatch(record, keyword) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func normaliseFeedParams(params FeedParams) FeedParams {
	params.Protocol = protocolPathFromPinPath(params.Protocol)
	if params.Protocol == "" {
		params.Protocol = PathSimpleBuzz
	}
	params.Publisher = strings.TrimSpace(params.Publisher)
	if params.Publisher != "" {
		params.Publishers = append(params.Publishers, params.Publisher)
	}
	params.Publishers = normaliseIdentities(params.Publishers)
	params.ChainName = strings.ToLower(strings.TrimSpace(params.ChainName))
	params.Keyword = strings.TrimSpace(params.Keyword)
	if params.Keyword != "" {
		params.Keywords = append(params.Keywords, params.Keyword)
	}
	params.Keywords = normaliseTerms(params.Keywords)
	params.Sort = strings.ToLower(strings.TrimSpace(params.Sort))
	if params.Sort == "" {
		params.Sort = SortNewest
	}
	if params.Size <= 0 {
		params.Size = defaultFeedSize
	}
	if params.Size > maxFeedSize {
		params.Size = maxFeedSize
	}
	params.Scope = strings.ToLower(strings.TrimSpace(params.Scope))
	params.User = strings.TrimSpace(params.User)
	return params
}

func normaliseIdentities(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func normaliseTerms(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{})
	for _, value := range values {
		for _, term := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(term)
			if trimmed == "" {
				continue
			}
			key := strings.ToLower(trimmed)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, trimmed)
		}
	}
	return out
}

func (a *Aggregator) recordFromIndexKey(key, value []byte, author bool) (*PostRecord, error) {
	var chain, source string
	var ok bool
	if author {
		chain, source, ok = parsePostAuthorKey(key)
	} else {
		chain, source, ok = parsePostTimeKey(key)
	}
	if !ok {
		return nil, nil
	}
	if len(value) > 0 {
		source = string(value)
	}
	return a.loadPost(chain, source)
}

func (a *Aggregator) loadPost(chain, source string) (*PostRecord, error) {
	var record PostRecord
	if err := loadJSON(a.store, postRecordKey(chain, source), &record); err != nil {
		return nil, err
	}
	if record.SourcePinId == "" {
		return nil, nil
	}
	return &record, nil
}

// FindPost resolves a source, current, or modify/revoke PIN ID to its
// canonical post record. The optional chainName avoids a cross-chain scan.
func (a *Aggregator) FindPost(pinID, chainName string) (*PostRecord, error) {
	pinID = strings.TrimSpace(pinID)
	chainName = strings.ToLower(strings.TrimSpace(chainName))
	if pinID == "" {
		return nil, ErrInvalidParameter
	}
	if chainName != "" {
		raw, err := a.store.Get(Namespace, postPinKey(chainName, pinID))
		if err != nil {
			if !errors.Is(err, pebble.ErrNotFound) {
				return nil, err
			}
			raw = nil
		}
		source := string(raw)
		if source == "" {
			source = pinID
		}
		return a.loadPost(chainName, source)
	}

	// Fast path: pins written after the chain index key resolve in one read.
	chainRaw, err := a.store.Get(Namespace, postPinChainKey(pinID))
	if err == nil && len(chainRaw) > 0 {
		chain := string(chainRaw)
		raw, err := a.store.Get(Namespace, postPinKey(chain, pinID))
		if err != nil {
			if !errors.Is(err, pebble.ErrNotFound) {
				return nil, err
			}
			raw = nil
		}
		source := string(raw)
		if source == "" {
			source = pinID
		}
		return a.loadPost(chain, source)
	}
	if err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return nil, err
	}

	// Legacy fallback: scan the compact pin-to-source index without loading
	// every post record, then resolve the canonical chain and record.
	suffix := []byte(":" + pinID)
	var foundChain, foundSource string
	err = a.store.ScanPrefix(Namespace, postPinPrefix(), func(key, _ []byte) error {
		if !bytes.HasSuffix(key, suffix) {
			return nil
		}
		rest := strings.TrimPrefix(string(key), keyPostPin)
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			return nil
		}
		foundChain, foundSource = parts[0], parts[1]
		return errStop
	})
	if err != nil && err != errStop {
		return nil, err
	}
	if foundChain == "" {
		return nil, nil
	}
	return a.loadPost(foundChain, foundSource)
}

func publisherMatch(record *PostRecord, publisher string) bool {
	target := strings.ToLower(strings.TrimSpace(publisher))
	return target != "" && (strings.EqualFold(record.AuthorGlobalMetaId, target) ||
		strings.EqualFold(record.AuthorMetaId, target) || strings.EqualFold(record.AuthorAddress, target))
}

func keywordMatch(record *PostRecord, keyword string) bool {
	needle := strings.ToLower(strings.TrimSpace(keyword))
	if needle == "" {
		return true
	}
	text := strings.ToLower(record.PayloadText)
	if strings.Contains(text, needle) {
		return true
	}
	for _, value := range record.PayloadJSON {
		if strings.Contains(strings.ToLower(toSearchText(value)), needle) {
			return true
		}
	}
	return false
}

func toSearchText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, toSearchText(item))
		}
		return strings.Join(parts, " ")
	default:
		return ""
	}
}

func postItemFromRecord(record *PostRecord) PostItem {
	item := PostItem{
		PinId:        record.SourcePinId,
		SourcePinId:  record.SourcePinId,
		CurrentPinId: record.CurrentPinId,
		ChainName:    record.ChainName,
		ProtocolPath: record.ProtocolPath,
		Author:       AuthorItem{GlobalMetaId: record.AuthorGlobalMetaId, MetaId: record.AuthorMetaId, Address: record.AuthorAddress},
		ContentType:  record.ContentType,
		CreatedAt:    record.CreatedAt,
		UpdatedAt:    record.UpdatedAt,
		LikeCount:    record.LikeCount,
		CommentCount: record.CommentCount,
		DonateCount:  record.DonateCount,
		QuoteCount:   record.QuoteCount,
	}
	if record.PayloadJSON != nil {
		item.Payload = record.PayloadJSON
	} else {
		item.Payload = record.PayloadText
	}
	return item
}

func (a *Aggregator) ListComments(params CommentParams) (*CommentResult, error) {
	params.ChainName = strings.ToLower(strings.TrimSpace(params.ChainName))
	params.PinId = strings.TrimSpace(params.PinId)
	if params.PinId == "" {
		return nil, ErrInvalidParameter
	}
	if post, err := a.FindPost(params.PinId, params.ChainName); err != nil {
		return nil, err
	} else if post != nil {
		params.PinId = post.SourcePinId
	}
	if params.Size <= 0 {
		params.Size = defaultCommentSize
	}
	if params.Size > maxCommentSize {
		params.Size = maxCommentSize
	}
	offset, err := decodeCursor(params.Cursor)
	if err != nil {
		return nil, err
	}

	comments := make([]CommentRecord, 0)
	collect := func(chain string) error {
		return a.store.ScanPrefix(Namespace, commentTargetPrefix(chain, params.PinId), func(_, value []byte) error {
			commentID := string(value)
			if commentID == "" {
				return nil
			}
			var comment CommentRecord
			if err := loadJSON(a.store, commentRecordKey(chain, commentID), &comment); err != nil {
				return err
			}
			if comment.PinId != "" && !comment.IsMempool {
				comments = append(comments, comment)
			}
			return nil
		})
	}
	if params.ChainName != "" {
		if err := collect(params.ChainName); err != nil {
			return nil, err
		}
	} else {
		if err := a.store.ScanPrefix(Namespace, []byte(keyCommentTarget), func(key, value []byte) error {
			parts := strings.SplitN(strings.TrimPrefix(string(key), keyCommentTarget), ":", 2)
			if len(parts) != 2 {
				return nil
			}
			chain := parts[0]
			commentID := string(value)
			if commentID == "" {
				return nil
			}
			var comment CommentRecord
			if err := loadJSON(a.store, commentRecordKey(chain, commentID), &comment); err != nil {
				return err
			}
			if comment.TargetPinId == params.PinId && comment.PinId != "" && !comment.IsMempool {
				comments = append(comments, comment)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(comments, func(i, j int) bool {
		if comments[i].Timestamp != comments[j].Timestamp {
			return comments[i].Timestamp > comments[j].Timestamp
		}
		return comments[i].PinId < comments[j].PinId
	})
	if offset > len(comments) {
		return nil, ErrInvalidCursor
	}
	end := offset + params.Size
	if end > len(comments) {
		end = len(comments)
	}
	result := &CommentResult{Items: comments[offset:end], HasMore: end < len(comments)}
	if result.HasMore {
		result.NextCursor = encodeCursor(end)
	}
	return result, nil
}

// Keep the storage import explicit in this file as a guard against accidentally
// moving the list implementation to a non-Pebble data source without review.
var _ *storage.PebbleStore
