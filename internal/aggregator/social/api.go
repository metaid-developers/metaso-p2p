package social

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

type compactListItemResponse struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name,omitempty"`
	NameId       string `json:"nameId,omitempty"`
	AvatarId     string `json:"avatarId,omitempty"`
}

type profileListItemResponse struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name,omitempty"`
	NameId       string `json:"nameId,omitempty"`
	AvatarId     string `json:"avatarId,omitempty"`
	Bio          string `json:"bio,omitempty"`
	BioId        string `json:"bioId,omitempty"`
	FollowedAt   int64  `json:"followedAt"`
	FollowPinId  string `json:"followPinId"`
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
}

type relationshipTargetResponse struct {
	GlobalMetaId  string `json:"globalMetaId"`
	FollowsSource bool   `json:"followsSource"`
}

type relationshipResponse struct {
	Source relationshipSourceResponse `json:"source"`
	Target relationshipTargetResponse `json:"target"`
	Mutual bool                       `json:"mutual"`
}

func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	social := router.Group("/social")
	social.GET("/globalmetaid/:globalMetaId/following", a.handleFollowing)
	social.GET("/globalmetaid/:globalMetaId/followers", a.handleFollowers)
	social.GET("/relationship", a.handleRelationship)
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
		},
		Target: relationshipTargetResponse{
			GlobalMetaId:  result.Target.GlobalMetaId,
			FollowsSource: result.Target.FollowsSource,
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
	if _, err := decodeCursor(cursor); err != nil {
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
