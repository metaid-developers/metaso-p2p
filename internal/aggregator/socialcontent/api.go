package socialcontent

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	social := router.Group("/social")
	social.GET("/feed", a.handleFeed)
	social.GET("/post/:pinId", a.handlePost)
	social.GET("/post/:pinId/comments", a.handleComments)
}

func (a *Aggregator) handleFeed(c *gin.Context) {
	params, err := parseFeedParams(c)
	if err != nil {
		api.RespErr(c, 40000, err.Error())
		return
	}
	result, err := a.List(params)
	if err != nil {
		if err == ErrInvalidCursor {
			api.RespErr(c, 40000, "invalid cursor")
			return
		}
		api.RespErr(c, 50000, "aggregation unavailable")
		return
	}
	api.RespSuccess(c, result)
}

func (a *Aggregator) handlePost(c *gin.Context) {
	pinID := strings.TrimSpace(c.Param("pinId"))
	if pinID == "" {
		api.RespErr(c, 40000, "pinId required")
		return
	}
	post, err := a.FindPost(pinID, c.Query("chainName"))
	if err != nil {
		api.RespErr(c, 50000, "aggregation unavailable")
		return
	}
	if post == nil || post.Hidden || post.IsMempool {
		api.RespErr(c, 40400, "social post not found")
		return
	}
	api.RespSuccess(c, postItemFromRecord(post))
}

func (a *Aggregator) handleComments(c *gin.Context) {
	size, err := parseSize(c.Query("size"))
	if err != nil {
		api.RespErr(c, 40000, "invalid size")
		return
	}
	result, err := a.ListComments(CommentParams{
		PinId:     c.Param("pinId"),
		ChainName: c.Query("chainName"),
		Size:      size,
		Cursor:    c.Query("cursor"),
	})
	if err != nil {
		switch err {
		case ErrInvalidParameter, ErrInvalidCursor:
			api.RespErr(c, 40000, err.Error())
		default:
			api.RespErr(c, 50000, "aggregation unavailable")
		}
		return
	}
	api.RespSuccess(c, result)
}

func parseFeedParams(c *gin.Context) (FeedParams, error) {
	size, err := parseSize(c.Query("size"))
	if err != nil {
		return FeedParams{}, err
	}
	since, err := parseTimestamp(c.Query("since"))
	if err != nil {
		return FeedParams{}, err
	}
	until, err := parseTimestamp(c.Query("until"))
	if err != nil {
		return FeedParams{}, err
	}
	if since > 0 && until > 0 && since > until {
		return FeedParams{}, ErrInvalidParameter
	}
	protocol := protocolPathFromPinPath(c.Query("protocol"))
	if protocol != "" && protocol != PathSimpleBuzz {
		return FeedParams{}, ErrInvalidParameter
	}
	sortName := strings.ToLower(strings.TrimSpace(c.Query("sort")))
	if sortName != "" && sortName != SortNewest && sortName != SortHot {
		return FeedParams{}, ErrInvalidParameter
	}
	scope := strings.ToLower(strings.TrimSpace(c.Query("scope")))
	if scope != "" && scope != "following" {
		return FeedParams{}, ErrInvalidParameter
	}
	user := strings.TrimSpace(c.Query("user"))
	if scope == "following" && user == "" {
		return FeedParams{}, ErrInvalidParameter
	}
	keyword := strings.TrimSpace(c.Query("keyword"))
	keywordsText := strings.TrimSpace(c.Query("keywords"))
	if keyword != "" && keywordsText != "" {
		return FeedParams{}, ErrInvalidParameter
	}
	publisher := strings.TrimSpace(c.Query("publisher"))
	publishersText := strings.TrimSpace(c.Query("publishers"))
	if publisher != "" && publishersText != "" {
		return FeedParams{}, ErrInvalidParameter
	}
	return FeedParams{
		Protocol:  protocol,
		Publisher: publisher,
		Keywords:  splitCSV(keywordsText),
		ChainName: c.Query("chainName"),
		Since:     since,
		Until:     until,
		Keyword:   keyword,
		Sort:      sortName,
		Size:      size,
		Cursor:    c.Query("cursor"),
		Scope:     scope,
		User:      user,
	}, nil
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func parseSize(raw string) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxFeedSize {
		return 0, ErrInvalidParameter
	}
	return n, nil
}

func parseTimestamp(raw string) (int64, error) {
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, ErrInvalidParameter
	}
	return n, nil
}
