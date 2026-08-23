package metaweb

import (
	"log"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
	"github.com/metaid-developers/metaso-p2p/internal/api"
)

// publisherInfo is the publisher block of one search item. name/avatar are
// best-effort userinfo enrichment of the returned page only.
type publisherInfo struct {
	GlobalMetaId string `json:"globalMetaId"`
	MetaId       string `json:"metaid"`
	Name         string `json:"name"`
	Avatar       string `json:"avatar"`
}

type itemLinks struct {
	Pin string `json:"pin"`
}

// searchItem is one /api/metaweb/search result row.
type searchItem struct {
	Protocol     string         `json:"protocol"`
	PinId        string         `json:"pinId"`
	CurrentPinId string         `json:"currentPinId"`
	ChainName    string         `json:"chainName"`
	Title        string         `json:"title"`
	Summary      string         `json:"summary"`
	Tags         []string       `json:"tags"`
	Publisher    publisherInfo  `json:"publisher"`
	CreatedAt    int64          `json:"createdAt"`
	Score        int            `json:"score"`
	Links        itemLinks      `json:"links"`
	Extra        map[string]any `json:"extra"`
}

type searchData struct {
	Items      []searchItem `json:"items"`
	NextCursor *string      `json:"nextCursor"`
	HasMore    bool         `json:"hasMore"`
}

// searchParams is the validated query parameter set of GET /metaweb/search.
type searchParams struct {
	query     string
	protocols map[string]struct{} // nil = all indexed
	publisher string
	since     int64
	sinceSet  bool
	until     int64
	untilSet  bool
	newest    bool
	size      int
	offset    int
}

func (a *Aggregator) handleSearch(c *gin.Context) {
	startMs := a.now()

	params, errMsg := parseSearchParams(c)
	if errMsg != "" {
		api.RespErr(c, codeInvalidParam, errMsg)
		return
	}
	if len(a.sources) == 0 {
		api.RespErr(c, codeUnavailable, "search unavailable")
		return
	}

	// Stopword tokens are excluded from scoring; an all-stopword query
	// yields no scoring tokens and therefore an empty result set.
	tokens := scoringTokens(tokenizeQuery(params.query))

	// Pass 1: lightweight IDF, document frequency counted per protocol key
	// over the merged snapshot (all docs, unfiltered). Pass 2 below applies
	// filters and computes the scores with each doc's own namespace weights.
	weights := tokenIDFWeights(a.sources, tokens)

	type scoredDoc struct {
		doc   metawebdoc.Document
		score int
	}
	matches := make([]scoredDoc, 0)

	// Pass 2: score over one merged snapshot per request; filters apply
	// before scoring. The snapshots are shared immutable slices read in
	// place.
	for _, source := range a.sources {
		if source == nil {
			continue
		}
		docs := source.SearchDocuments()
		for i := range docs {
			doc := docs[i]
			if params.protocols != nil {
				if _, ok := params.protocols[doc.ProtocolKey]; !ok {
					continue
				}
			}
			if params.publisher != "" &&
				!strings.EqualFold(doc.PublisherGlobalMetaId, params.publisher) &&
				!strings.EqualFold(doc.PublisherMetaId, params.publisher) {
				continue
			}
			if params.sinceSet && doc.CreatedAt < params.since {
				continue
			}
			if params.untilSet && doc.CreatedAt > params.until {
				continue
			}
			score := 0
			if params.newest {
				if !documentMatchesAny(&doc, tokens) {
					continue
				}
			} else {
				score = scoreDocument(&doc, tokens, weightsForProtocol(weights, doc.ProtocolKey, tokens), params.query)
				if score <= 0 {
					continue
				}
			}
			matches = append(matches, scoredDoc{doc: doc, score: score})
		}
	}

	if params.newest {
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].doc.CreatedAt != matches[j].doc.CreatedAt {
				return matches[i].doc.CreatedAt > matches[j].doc.CreatedAt
			}
			return matches[i].doc.SourcePinId < matches[j].doc.SourcePinId
		})
	} else {
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].score != matches[j].score {
				return matches[i].score > matches[j].score
			}
			if matches[i].doc.CreatedAt != matches[j].doc.CreatedAt {
				return matches[i].doc.CreatedAt > matches[j].doc.CreatedAt
			}
			return matches[i].doc.SourcePinId < matches[j].doc.SourcePinId
		})
	}

	offset := params.offset
	if offset > len(matches) {
		offset = len(matches)
	}
	page := matches[offset:]
	hasMore := len(page) > params.size
	if hasMore {
		page = page[:params.size]
	}

	// Enrich only the returned page with publisher name/avatar.
	items := make([]searchItem, 0, len(page))
	for _, match := range page {
		doc := match.doc
		currentPinId := doc.CurrentPinId
		if currentPinId == "" {
			currentPinId = doc.SourcePinId
		}
		tags := doc.Tags
		if tags == nil {
			tags = []string{}
		}
		extra := doc.Extra
		if extra == nil {
			extra = map[string]any{}
		}
		publisher := publisherInfo{
			GlobalMetaId: doc.PublisherGlobalMetaId,
			MetaId:       doc.PublisherMetaId,
		}
		if a.profileNamer != nil {
			publisher.Name, publisher.Avatar = a.profileNamer.ProfileNameAvatar(doc.PublisherGlobalMetaId, doc.PublisherMetaId)
		}
		items = append(items, searchItem{
			Protocol:     doc.ProtocolKey,
			PinId:        doc.SourcePinId,
			CurrentPinId: currentPinId,
			ChainName:    doc.ChainName,
			Title:        doc.Title,
			Summary:      doc.Summary,
			Tags:         tags,
			Publisher:    publisher,
			CreatedAt:    doc.CreatedAt,
			Score:        match.score,
			Links:        itemLinks{Pin: "/api/metaweb/pin/" + currentPinId},
			Extra:        extra,
		})
	}

	var nextCursor *string
	if hasMore {
		encoded := encodeSearchCursor(offset + params.size)
		nextCursor = &encoded
	}

	if elapsed := a.now() - startMs; elapsed > slowSearchThresholdMs {
		log.Printf("[metaweb] slow search q=%q protocols=%q publisher=%q sort=%s results=%d elapsed=%dms",
			params.query, c.Query("protocols"), params.publisher, c.DefaultQuery("sort", "relevance"), len(matches), elapsed)
	}

	api.RespSuccess(c, searchData{
		Items:      items,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	})
}

// parseSearchParams validates the query string; a non-empty error message
// maps to a 40000 response.
func parseSearchParams(c *gin.Context) (searchParams, string) {
	params := searchParams{
		query: strings.TrimSpace(c.Query("q")),
		size:  defaultSearchSize,
	}
	if params.query == "" {
		return params, "q is required"
	}

	if raw := strings.TrimSpace(c.Query("protocols")); raw != "" {
		params.protocols = make(map[string]struct{})
		for _, key := range strings.Split(raw, ",") {
			key = strings.ToLower(strings.TrimSpace(key))
			if key == "" {
				continue
			}
			if !metawebdoc.ValidProtocolKey(key) {
				return params, "unknown protocol: " + key
			}
			params.protocols[key] = struct{}{}
		}
	}

	params.publisher = strings.TrimSpace(c.Query("publisher"))

	since, errMsg := parseOptionalUnix(c.Query("since"))
	if errMsg != "" {
		return params, errMsg
	}
	params.since, params.sinceSet = since.value, since.set
	until, errMsg := parseOptionalUnix(c.Query("until"))
	if errMsg != "" {
		return params, errMsg
	}
	params.until, params.untilSet = until.value, until.set
	if params.sinceSet && params.untilSet && params.since > params.until {
		return params, "since must not exceed until"
	}

	switch sortParam := strings.ToLower(strings.TrimSpace(c.Query("sort"))); sortParam {
	case "", "relevance":
	case "newest":
		params.newest = true
	default:
		return params, "invalid sort"
	}

	if raw := strings.TrimSpace(c.Query("size")); raw != "" {
		size, err := strconv.Atoi(raw)
		if err != nil || size < 1 {
			return params, "invalid size"
		}
		if size > maxSearchSize {
			size = maxSearchSize
		}
		params.size = size
	}

	offset, err := decodeSearchCursor(strings.TrimSpace(c.Query("cursor")))
	if err != nil {
		return params, "invalid cursor"
	}
	params.offset = offset
	return params, ""
}

type optionalUnix struct {
	value int64
	set   bool
}

// parseOptionalUnix parses an optional unix-seconds parameter; non-numeric
// input is a contract error.
func parseOptionalUnix(raw string) (optionalUnix, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return optionalUnix{}, ""
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return optionalUnix{}, "invalid since/until"
	}
	return optionalUnix{value: value, set: true}, ""
}
