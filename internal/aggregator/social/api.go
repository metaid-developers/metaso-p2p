package social

import (
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

type compactListItemResponse struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name"`
	NameId       string `json:"nameId"`
	AvatarId     string `json:"avatarId"`
	Pending      bool   `json:"pending,omitempty"`
}

type profileListItemResponse struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name"`
	NameId       string `json:"nameId"`
	AvatarId     string `json:"avatarId"`
	Bio          string `json:"bio"`
	BioId        string `json:"bioId"`
	FollowedAt   int64  `json:"followedAt"`
	FollowPinId  string `json:"followPinId"`
	Pending      bool   `json:"pending,omitempty"`
}

type compactListResponse struct {
	List       []compactListItemResponse `json:"list"`
	NextCursor string                    `json:"nextCursor"`
	Size       int                       `json:"size"`
}

type profileListResponse struct {
	List       []profileListItemResponse `json:"list"`
	NextCursor string                    `json:"nextCursor"`
	Size       int                       `json:"size"`
}

type relationshipSourceResponse struct {
	GlobalMetaId  string `json:"globalMetaId"`
	FollowsTarget bool   `json:"followsTarget"`
	FollowPinId   string `json:"followPinId"`
	FollowedAt    int64  `json:"followedAt"`
	Pending       bool   `json:"pending,omitempty"`
}

type relationshipTargetResponse struct {
	GlobalMetaId  string `json:"globalMetaId"`
	FollowsSource bool   `json:"followsSource"`
	FollowPinId   string `json:"followPinId"`
	FollowedAt    int64  `json:"followedAt"`
	Pending       bool   `json:"pending,omitempty"`
}

type relationshipResponse struct {
	Source relationshipSourceResponse `json:"source"`
	Target relationshipTargetResponse `json:"target"`
	Mutual bool                       `json:"mutual"`
}

type friendCompactItemResponse struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name"`
	NameId       string `json:"nameId"`
	AvatarId     string `json:"avatarId"`
	Pending      bool   `json:"pending,omitempty"`
}

type friendProfileItemResponse struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name"`
	NameId       string `json:"nameId"`
	AvatarId     string `json:"avatarId"`
	Bio          string `json:"bio"`
	BioId        string `json:"bioId"`
	FollowedAt   int64  `json:"followedAt"`
	FollowPinId  string `json:"followPinId"`
	FriendSince  int64  `json:"friendSince"`
	Pending      bool   `json:"pending,omitempty"`
}

type friendCompactListResponse struct {
	List       []friendCompactItemResponse `json:"list"`
	NextCursor string                      `json:"nextCursor"`
	Size       int                         `json:"size"`
}

type friendProfileListResponse struct {
	List       []friendProfileItemResponse `json:"list"`
	NextCursor string                      `json:"nextCursor"`
	Size       int                         `json:"size"`
}

func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	social := router.Group("/social")
	social.GET("/globalmetaid/:globalMetaId/following", a.handleFollowing)
	social.GET("/globalmetaid/:globalMetaId/followers", a.handleFollowers)
	social.GET("/globalmetaid/:globalMetaId/friends", a.handleFriends)
	social.GET("/globalmetaid/:globalMetaId/counts", a.handleCounts)
	social.GET("/relationship", a.handleRelationship)
	social.POST("/relationships/batch", a.handleBatchRelationships)
}

func (a *Aggregator) handleFollowing(c *gin.Context) {
	params, err := parseListParams(c)
	if err != nil {
		respondSocialError(c, err)
		return
	}

	result, err := a.ListFollowing(params)
	if err != nil {
		respondSocialError(c, err)
		return
	}
	api.RespSuccess(c, buildListResponse(result, params.View))
}

func (a *Aggregator) handleFollowers(c *gin.Context) {
	params, err := parseListParams(c)
	if err != nil {
		respondSocialError(c, err)
		return
	}

	result, err := a.ListFollowers(params)
	if err != nil {
		respondSocialError(c, err)
		return
	}
	api.RespSuccess(c, buildListResponse(result, params.View))
}

func (a *Aggregator) handleFriends(c *gin.Context) {
	params, err := parseListParams(c)
	if err != nil {
		respondSocialError(c, err)
		return
	}

	result, err := a.ListFriends(params)
	if err != nil {
		respondSocialError(c, err)
		return
	}
	api.RespSuccess(c, buildFriendListResponse(result, params.View))
}

func (a *Aggregator) handleCounts(c *gin.Context) {
	subject := strings.TrimSpace(c.Param("globalMetaId"))
	if subject == "" {
		respondSocialError(c, ErrInvalidParameter)
		return
	}

	result, err := a.Counts(subject)
	if err != nil {
		respondSocialError(c, err)
		return
	}
	api.RespSuccess(c, result)
}

func (a *Aggregator) handleBatchRelationships(c *gin.Context) {
	var request struct {
		SourceGlobalMetaId string   `json:"sourceGlobalMetaId"`
		Targets            []string `json:"targets"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		respondSocialError(c, ErrInvalidParameter)
		return
	}

	result, err := a.BatchRelationships(BatchRelationshipParams{
		SourceGlobalMetaId: request.SourceGlobalMetaId,
		Targets:            request.Targets,
	})
	if err != nil {
		respondSocialError(c, err)
		return
	}
	api.RespSuccess(c, result)
}

func (a *Aggregator) handleRelationship(c *gin.Context) {
	params := RelationshipParams{
		SourceGlobalMetaId: strings.TrimSpace(c.Query("sourceGlobalMetaId")),
		TargetGlobalMetaId: strings.TrimSpace(c.Query("targetGlobalMetaId")),
	}
	if params.SourceGlobalMetaId == "" || params.TargetGlobalMetaId == "" {
		respondSocialError(c, ErrInvalidParameter)
		return
	}

	result, err := a.Relationship(params)
	if err != nil {
		respondSocialError(c, err)
		return
	}
	api.RespSuccess(c, relationshipResponse{
		Source: relationshipSourceResponse{
			GlobalMetaId:  result.Source.GlobalMetaId,
			FollowsTarget: result.Source.FollowsTarget,
			FollowPinId:   result.Source.FollowPinId,
			FollowedAt:    result.Source.FollowedAt,
			Pending:       result.Source.Pending,
		},
		Target: relationshipTargetResponse{
			GlobalMetaId:  result.Target.GlobalMetaId,
			FollowsSource: result.Target.FollowsSource,
			FollowPinId:   result.Target.FollowPinId,
			FollowedAt:    result.Target.FollowedAt,
			Pending:       result.Target.Pending,
		},
		Mutual: result.Mutual,
	})
}

func parseListParams(c *gin.Context) (ListParams, error) {
	size, err := parseListSize(c.Query("size"))
	if err != nil {
		return ListParams{}, err
	}

	view, err := normaliseListView(c.Query("view"))
	if err != nil {
		return ListParams{}, err
	}

	cursor := strings.TrimSpace(c.Query("cursor"))
	// Base64 envelope check only: each list endpoint decodes and validates
	// its own cursor shape (edge tuple vs friend tuple) afterwards.
	if _, err := base64.StdEncoding.DecodeString(cursor); err != nil {
		return ListParams{}, ErrInvalidParameter
	}

	return ListParams{
		GlobalMetaId: strings.TrimSpace(c.Param("globalMetaId")),
		Cursor:       cursor,
		Size:         size,
		View:         view,
	}, nil
}

func parseListSize(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultListSize, nil
	}

	size, err := strconv.Atoi(raw)
	if err != nil || size <= 0 || size > maxListSize {
		return 0, ErrInvalidParameter
	}
	return size, nil
}

func buildFriendListResponse(result *FriendListResult, view string) any {
	if result == nil {
		result = &FriendListResult{Size: defaultListSize}
	}

	if view == ViewProfile {
		list := make([]friendProfileItemResponse, 0, len(result.List))
		for _, item := range result.List {
			list = append(list, friendProfileItemResponse{
				GlobalMetaId: item.GlobalMetaId,
				Name:         item.Name,
				NameId:       item.NameId,
				AvatarId:     item.AvatarId,
				Bio:          item.Bio,
				BioId:        item.BioId,
				FollowedAt:   item.FollowedAt,
				FollowPinId:  item.FollowPinId,
				FriendSince:  item.FriendSince,
				Pending:      item.Pending,
			})
		}
		return friendProfileListResponse{
			List:       list,
			NextCursor: result.NextCursor,
			Size:       result.Size,
		}
	}

	list := make([]friendCompactItemResponse, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, friendCompactItemResponse{
			GlobalMetaId: item.GlobalMetaId,
			Name:         item.Name,
			NameId:       item.NameId,
			AvatarId:     item.AvatarId,
			Pending:      item.Pending,
		})
	}
	return friendCompactListResponse{
		List:       list,
		NextCursor: result.NextCursor,
		Size:       result.Size,
	}
}

func buildListResponse(result *ListResult, view string) any {
	if result == nil {
		result = &ListResult{Size: defaultListSize}
	}

	if view == ViewProfile {
		list := make([]profileListItemResponse, 0, len(result.List))
		for _, item := range result.List {
			list = append(list, profileListItemResponse{
				GlobalMetaId: item.GlobalMetaId,
				Name:         item.Name,
				NameId:       item.NameId,
				AvatarId:     item.AvatarId,
				Bio:          item.Bio,
				BioId:        item.BioId,
				FollowedAt:   item.FollowedAt,
				FollowPinId:  item.FollowPinId,
				Pending:      item.Pending,
			})
		}
		return profileListResponse{
			List:       list,
			NextCursor: result.NextCursor,
			Size:       result.Size,
		}
	}

	list := make([]compactListItemResponse, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, compactListItemResponse{
			GlobalMetaId: item.GlobalMetaId,
			Name:         item.Name,
			NameId:       item.NameId,
			AvatarId:     item.AvatarId,
			Pending:      item.Pending,
		})
	}
	return compactListResponse{
		List:       list,
		NextCursor: result.NextCursor,
		Size:       result.Size,
	}
}

func respondSocialError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidParameter):
		api.RespErr(c, 40000, "invalid parameter")
	case errors.Is(err, ErrNotFound):
		api.RespErr(c, 40400, "subject not found")
	default:
		api.RespErr(c, 50000, "aggregation unavailable")
	}
}
