package metaweb

// GET /api/metaweb/fresh — the unified fresh-content feed across the
// published protocols (R1) with deterministic duplicate/per-author noise
// suppression (R5). See docs/specs/2026-09-13-metaweb-surf-reads-api.md §1.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	lru "github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

const (
	freshDefaultSize = 50
	freshMaxSize     = 100
	// freshCacheTTL bounds the server-side response cache. Every bot in the
	// fleet asks for the same nightly window within the same seconds; the
	// spec fixes 5s as correctness-safe (one index scan per window slice).
	freshCacheTTL = 5 * time.Second
	freshCacheMax = 512
)

// freshSupportedProtocols maps accepted `protocols` keys onto fresh-index
// protocol paths. skill-service lives in a separate read model and is not
// part of the fresh feed.
var freshSupportedProtocols = map[string]string{
	metawebdoc.KeySimpleBuzz:     publishedcontent.PathSimpleBuzz,
	metawebdoc.KeySimpleNote:     publishedcontent.PathSimpleNote,
	metawebdoc.KeySimpleQuestion: publishedcontent.PathSimpleQuestion,
	metawebdoc.KeySimpleAnswer:   publishedcontent.PathSimpleAnswer,
	metawebdoc.KeyMetaProtocol:   publishedcontent.PathMetaProtocol,
	metawebdoc.KeyMetaApp:        publishedcontent.PathMetaApp,
	metawebdoc.KeyMetaBotSkill:   publishedcontent.PathMetaBotSkill,
}

type freshAuthor struct {
	Address      string `json:"address,omitempty"`
	MetaId       string `json:"metaid,omitempty"`
	GlobalMetaId string `json:"globalMetaId,omitempty"`
	Name         string `json:"name,omitempty"`
}

// freshItem is one /api/metaweb/fresh row. Summary is a derived excerpt, not
// the body (F6); full content stays behind the pin-read endpoints.
type freshItem struct {
	PinId        string         `json:"pinId"`
	CurrentPinId string         `json:"currentPinId"`
	Protocol     string         `json:"protocol"`
	Path         string         `json:"path"`
	ChainName    string         `json:"chainName"`
	CreatedAt    int64          `json:"createdAt"`
	Author       freshAuthor    `json:"author"`
	Title        string         `json:"title"`
	Summary      string         `json:"summary"`
	LikeCount    int            `json:"likeCount"`
	CommentCount int            `json:"commentCount"`
	IsMempool    bool           `json:"isMempool,omitempty"`
	Duplicates   int            `json:"duplicates,omitempty"`
	Extra        map[string]any `json:"extra,omitempty"`
}

type suppressedBlock struct {
	Duplicates int `json:"duplicates"`
	Throttled  int `json:"throttled"`
}

type freshData struct {
	Items      []freshItem      `json:"items"`
	HasMore    bool             `json:"hasMore"`
	NextCursor *string          `json:"nextCursor"`
	ServerTime int64            `json:"serverTime"`
	Suppressed *suppressedBlock `json:"suppressed,omitempty"`
}

// freshQuery is the validated parameter set of GET /metaweb/fresh.
type freshQuery struct {
	protocolPaths []string
	since         int64
	size          int
	cursor        string
	dedupe        bool
	maxPerAuthor  int
}

// cacheKey is the exact server-side cache identity: equal parameter sets
// share one index scan (spec §1 cache criterion).
func (q freshQuery) cacheKey() string {
	paths := append([]string(nil), q.protocolPaths...)
	sort.Strings(paths)
	return strings.Join([]string{
		"v1", strings.Join(paths, ","), strconv.FormatInt(q.since, 10),
		strconv.Itoa(q.size), q.cursor,
		strconv.FormatBool(q.dedupe), strconv.Itoa(q.maxPerAuthor),
	}, "|")
}

// freshResponseCache is the in-process TTL cache for fresh responses. The
// shared CacheProvider is deliberately not used: its L2 tier persists every
// entry into Pebble, which is wrong for a 5s response cache.
type freshResponseCache struct {
	lru *lru.LRU[string, []byte]
}

func newFreshResponseCache() *freshResponseCache {
	return &freshResponseCache{lru: lru.NewLRU[string, []byte](freshCacheMax, nil, freshCacheTTL)}
}

func (c *freshResponseCache) get(key string) ([]byte, bool) {
	if c == nil || c.lru == nil {
		return nil, false
	}
	return c.lru.Get(key)
}

func (c *freshResponseCache) set(key string, raw []byte) {
	if c == nil || c.lru == nil {
		return
	}
	c.lru.Add(key, raw)
}

func (a *Aggregator) handleFresh(c *gin.Context) {
	startMs := a.now()

	q, errMsg := parseFreshQuery(c)
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	if a.freshLookup == nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	cacheKey := q.cacheKey()
	if raw, ok := a.freshCache.get(cacheKey); ok {
		api.RespSuccessRawData(c, raw)
		return
	}

	page, err := a.freshLookup.Fresh(publishedcontent.FreshParams{
		ProtocolPaths: q.protocolPaths,
		SinceSec:      q.since,
		Size:          q.size,
		Cursor:        q.cursor,
	})
	if err != nil {
		if errors.Is(err, publishedcontent.ErrInvalidFreshCursor) {
			api.RespErr(c, codeInvalidParam, "invalid cursor")
			return
		}
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}

	items, suppressed := a.freshItemsFromRecords(page.Records, q)
	data := freshData{
		Items:      items,
		HasMore:    page.HasMore,
		ServerTime: a.now() / 1000,
		Suppressed: suppressed,
	}
	if page.HasMore && page.NextCursor != "" {
		cursor := page.NextCursor
		data.NextCursor = &cursor
	}

	raw, err := json.Marshal(data)
	if err != nil {
		api.RespErr(c, codeUnavailable, "aggregation unavailable")
		return
	}
	a.freshCache.set(cacheKey, raw)

	if elapsed := a.now() - startMs; elapsed > slowSearchThresholdMs {
		log.Printf("[metaweb] slow fresh protocols=%q since=%d cursor=%q items=%d elapsed=%dms",
			c.Query("protocols"), q.since, q.cursor, len(items), elapsed)
	}

	api.RespSuccessRawData(c, raw)
}

func parseFreshQuery(c *gin.Context) (freshQuery, string) {
	q := freshQuery{size: freshDefaultSize}

	if raw := strings.TrimSpace(c.Query("protocols")); raw != "" {
		seen := make(map[string]struct{})
		for _, key := range strings.Split(raw, ",") {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" {
				continue
			}
			path, ok := freshSupportedProtocols[key]
			if !ok {
				return q, "unsupported protocol: " + key
			}
			if _, dup := seen[path]; dup {
				continue
			}
			seen[path] = struct{}{}
			q.protocolPaths = append(q.protocolPaths, path)
		}
	}

	since, errMsg := parseOptionalUnix(c.Query("since"))
	if errMsg != "" {
		return q, errMsg
	}
	q.since = since.value

	if raw := strings.TrimSpace(c.Query("size")); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 1 {
			return q, "invalid size"
		}
		if size > freshMaxSize {
			size = freshMaxSize
		}
		q.size = size
	}

	q.cursor = strings.TrimSpace(c.Query("cursor"))

	switch dedupe := strings.ToLower(strings.TrimSpace(c.Query("dedupe"))); dedupe {
	case "":
	case "identical":
		q.dedupe = true
	default:
		return q, "unsupported dedupe mode: " + dedupe
	}

	if raw := strings.TrimSpace(c.Query("maxPerAuthor")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return q, "invalid maxPerAuthor"
		}
		q.maxPerAuthor = n
	}

	return q, ""
}

// freshItemsFromRecords projects fresh-index records onto wire items and
// applies the R5 suppression passes. Suppression state is per page scan
// (spec §1.3): duplicates are collapsed onto the first copy found in this
// scan, and a duplicate of an item shown on an earlier page is simply the
// first copy of this scan — cross-page dedupe is not attempted.
func (a *Aggregator) freshItemsFromRecords(records []*publishedcontent.Record, q freshQuery) ([]freshItem, *suppressedBlock) {
	if len(records) == 0 {
		return []freshItem{}, nil
	}

	applySuppression := q.dedupe || q.maxPerAuthor > 0
	var (
		block       suppressedBlock
		firstIndex  = map[string]int{} // content hash → index of the first occurrence in items (-1 = dropped)
		authorCount = map[string]int{}
	)
	items := make([]freshItem, 0, len(records))
	for _, rec := range records {
		item := a.freshItemFromRecord(rec)
		hash := ""
		if q.dedupe {
			hash = freshContentHash(rec)
			if first, seen := firstIndex[hash]; seen {
				block.Duplicates++
				if first >= 0 {
					items[first].Duplicates++
				}
				continue
			}
		}
		if q.maxPerAuthor > 0 {
			authorKey := freshAuthorKey(rec)
			authorCount[authorKey]++
			if authorCount[authorKey] > q.maxPerAuthor {
				block.Throttled++
				if q.dedupe {
					firstIndex[hash] = -1
				}
				continue
			}
		}
		if q.dedupe {
			firstIndex[hash] = len(items)
		}
		items = append(items, item)
	}

	if !applySuppression {
		return items, nil
	}
	return items, &block
}

// freshItemFromRecord projects one publishedcontent record onto a fresh item,
// including the best-effort engagement joins (buzz counts from socialcontent,
// question/answer counts from qa) and protocol-specific extras.
func (a *Aggregator) freshItemFromRecord(rec *publishedcontent.Record) freshItem {
	key := metawebdoc.ProtocolKeyForPath(rec.ProtocolPath)
	extracted := metawebdoc.ExtractPublished(rec.ProtocolPath, rec.PayloadJSON, rec.PayloadText)

	var extra map[string]any
	if len(extracted.Extra) > 0 {
		extra = make(map[string]any, len(extracted.Extra)+2)
		for k, v := range extracted.Extra {
			extra[k] = v
		}
	}

	likeCount, commentCount := 0, 0
	switch key {
	case metawebdoc.KeySimpleBuzz:
		if a.buzzEngagement != nil {
			if likes, comments, ok := a.buzzEngagement.BuzzEngagement(rec.SourcePinId); ok {
				likeCount, commentCount = likes, comments
			}
		}
	case metawebdoc.KeySimpleQuestion:
		if a.qaEngagement != nil {
			if likes, _, comments, answers, ok := a.qaEngagement.QuestionEngagement(rec.SourcePinId); ok {
				likeCount, commentCount = likes, comments
				if extra == nil {
					extra = map[string]any{}
				}
				extra["answerCount"] = answers
			}
		}
	case metawebdoc.KeySimpleAnswer:
		if a.qaEngagement != nil {
			if likes, _, comments, ok := a.qaEngagement.AnswerEngagement(rec.SourcePinId); ok {
				likeCount, commentCount = likes, comments
			}
		}
	case metawebdoc.KeyMetaProtocol:
		if path := stringFieldOf(rec.PayloadJSON, "path"); path != "" {
			if extra == nil {
				extra = map[string]any{}
			}
			extra["path"] = path
		}
	}

	currentPinId := rec.CurrentPinId
	if currentPinId == "" {
		currentPinId = rec.SourcePinId
	}
	return freshItem{
		PinId:        rec.SourcePinId,
		CurrentPinId: currentPinId,
		Protocol:     key,
		Path:         rec.ProtocolPath,
		ChainName:    rec.ChainName,
		CreatedAt:    metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
		Author: freshAuthor{
			Address:      rec.PublisherAddress,
			MetaId:       rec.PublisherMetaId,
			GlobalMetaId: rec.PublisherGlobalMetaId,
			Name:         a.creatorName(rec.PublisherGlobalMetaId, rec.PublisherMetaId),
		},
		Title:        extracted.Title,
		Summary:      extracted.Summary,
		LikeCount:    likeCount,
		CommentCount: commentCount,
		IsMempool:    rec.IsMempool,
		Extra:        extra,
	}
}

// freshContentHash is the byte-identity key of R5 dedupe: SHA-256 over the
// payload content text, or the canonical JSON serialization for JSON
// payloads (encoding/json sorts map keys, so equal payloads hash equal).
func freshContentHash(rec *publishedcontent.Record) string {
	content := rec.PayloadText
	if content == "" && rec.PayloadJSON != nil {
		if raw, err := json.Marshal(rec.PayloadJSON); err == nil {
			content = string(raw)
		}
	}
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// freshAuthorKey is the per-author throttle identity: globalMetaId, else
// metaId, else address, case-insensitive.
func freshAuthorKey(rec *publishedcontent.Record) string {
	for _, value := range []string{rec.PublisherGlobalMetaId, rec.PublisherMetaId, rec.PublisherAddress} {
		if trimmed := strings.ToLower(strings.TrimSpace(value)); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func stringFieldOf(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if value, ok := payload[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}
