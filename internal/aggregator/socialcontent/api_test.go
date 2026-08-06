package socialcontent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRoutesExposeFeedDetailAndComments(t *testing.T) {
	agg, _ := setupTestAggregator(t)
	post := testPin("api-buzz:i0", PathSimpleBuzz, OperationCreate, "mvc", 500, []byte(`{"text":"api post"}`))
	if _, err := agg.HandleBlockPin(post); err != nil {
		t.Fatalf("post: %v", err)
	}
	comment := testPin("api-comment:i0", PathPayComment, OperationCreate, "mvc", 510, mustJSON(t, map[string]any{"commentTo": post.Id, "content": "api comment"}))
	if _, err := agg.HandleBlockPin(comment); err != nil {
		t.Fatalf("comment: %v", err)
	}

	gin.SetMode(gin.TestMode)
	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))

	for _, path := range []string{
		"/api/social/feed?size=10",
		"/api/social/post/api-buzz:i0",
		"/api/social/post/api-buzz:i0/comments?size=10",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, resp.Code)
		}
		var envelope struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("GET %s response: %v", path, err)
		}
		if envelope.Code != 0 {
			t.Fatalf("GET %s code = %d, body=%s", path, envelope.Code, resp.Body.String())
		}
	}
}
