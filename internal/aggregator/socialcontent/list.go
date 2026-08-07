package socialcontent

import (
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func (a *Aggregator) List(params FeedParams) (*FeedResult, error) {
	params = normaliseFeedParams(params)
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
	if params.Publisher != "" {
		prefix = postAuthorPrefix(params.Publisher)
	}
	need := params.Size + 1
	var lastKey []byte
	scan := func(key, value []byte) error {
		record, err := a.recordFromIndexKey(key, value, params.Publisher != "")
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

// listHot ranks the full candidate set by the hot score, so it scans all
// matching posts and keeps the offset cursor contract.
func (a *Aggregator) listHot(params FeedParams) (*FeedResult, error) {
	offset, err := decodeCursor(params.Cursor)
	if err != nil {
		return nil, err
	}
	records := make([]*PostRecord, 0)
	seen := make(map[string]struct{})
	prefix := postTimePrefix()
	if params.Publisher != "" {
		prefix = postAuthorPrefix(params.Publisher)
	}
	if err := a.store.ScanPrefix(Namespace, prefix, func(key, value []byte) error {
		record, err := a.recordFromIndexKey(key, value, params.Publisher != "")
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
		records = append(records, record)
		return nil
	}); err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	sort.SliceStable(records, func(i, j int) bool {
		left, right := hotScore(records[i], now), hotScore(records[j], now)
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

	if offset > len(records) {
		return nil, ErrInvalidCursor
	}
	end := offset + params.Size
	if end > len(records) {
		end = len(records)
	}
	result := &FeedResult{Items: make([]PostItem, 0, end-offset)}
	for _, record := range records[offset:end] {
		result.Items = append(result.Items, postItemFromRecord(record))
	}
	result.HasMore = end < len(records)
	if result.HasMore {
		result.NextCursor = encodeCursor(end)
	}
	return result, nil
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
	if params.Publisher != "" && !publisherMatch(record, params.Publisher) {
		return false
	}
	if params.Keyword != "" && !keywordMatch(record, params.Keyword) {
		return false
	}
	return true
}

func normaliseFeedParams(params FeedParams) FeedParams {
	params.Protocol = protocolPathFromPinPath(params.Protocol)
	if params.Protocol == "" {
		params.Protocol = PathSimpleBuzz
	}
	params.Publisher = strings.TrimSpace(params.Publisher)
	params.ChainName = strings.ToLower(strings.TrimSpace(params.ChainName))
	params.Keyword = strings.TrimSpace(params.Keyword)
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
	return params
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

	var found *PostRecord
	err := a.store.ScanPrefix(Namespace, postTimePrefix(), func(key, value []byte) error {
		chain, source, ok := parsePostTimeKey(key)
		if !ok {
			return nil
		}
		if len(value) > 0 {
			source = string(value)
		}
		record, err := a.loadPost(chain, source)
		if err != nil {
			return err
		}
		if record != nil && (record.SourcePinId == pinID || record.CurrentPinId == pinID) {
			found = record
			return errStop
		}
		return nil
	})
	if err != nil && err != errStop {
		return nil, err
	}
	return found, nil
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
		HotScore:     hotScore(record, time.Now().Unix()),
	}
	if record.PayloadJSON != nil {
		item.Payload = record.PayloadJSON
	} else {
		item.Payload = record.PayloadText
	}
	return item
}

func hotScore(record *PostRecord, now int64) float64 {
	if record == nil {
		return 0
	}
	ageHours := float64(now-record.CreatedAt) / 3600
	if ageHours < 1 {
		ageHours = 1
	}
	engagement := float64(record.LikeCount + 2*record.CommentCount + 3*record.DonateCount)
	return engagement / math.Pow(ageHours+2, 1.5)
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
