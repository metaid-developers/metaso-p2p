package publishedcontent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupMetaAppAPIRouter(t *testing.T, agg *Aggregator) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	registerMetaAppRoutes(agg, router.Group("/api"))
	return router
}

func TestMetaAppListPreferenceParamErrors(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	router := setupMetaAppAPIRouter(t, agg)

	cases := []struct {
		name string
		url  string
		msg  string
	}{
		{
			name: "validated basis unavailable",
			url:  "/api/metaapp/list?chainName=mvc&sort=preference&basis=validated",
			msg:  "preference_validated_basis_unavailable",
		},
		{
			name: "validated basis unavailable without sort",
			url:  "/api/metaapp/list?chainName=mvc&basis=validated",
			msg:  "preference_validated_basis_unavailable",
		},
		{
			name: "preference requires chainName",
			url:  "/api/metaapp/list?sort=preference&tag=motion-video",
			msg:  "sort=preference requires chainName",
		},
		{
			name: "invalid sort",
			url:  "/api/metaapp/list?chainName=mvc&sort=elo",
			msg:  "invalid sort",
		},
		{
			name: "invalid order",
			url:  "/api/metaapp/list?chainName=mvc&sort=preference&order=down",
			msg:  "invalid order",
		},
		{
			name: "invalid basis",
			url:  "/api/metaapp/list?chainName=mvc&sort=preference&basis=blended",
			msg:  "invalid basis",
		},
		{
			name: "invalid minJudges",
			url:  "/api/metaapp/list?chainName=mvc&sort=preference&minJudges=-1",
			msg:  "invalid minJudges",
		},
		{
			name: "invalid learning",
			url:  "/api/metaapp/list?chainName=mvc&sort=preference&learning=verified",
			msg:  "invalid learning",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest(http.MethodGet, tc.url, nil)
			router.ServeHTTP(w, req)
			body := decodeMetaAppTestBody(t, w.Body.Bytes())
			if body["code"] != float64(40000) {
				t.Fatalf("code: %v body=%v", body["code"], body)
			}
			if body["message"] != tc.msg {
				t.Fatalf("message: got %q want %q", body["message"], tc.msg)
			}
		})
	}
}

func TestMetaAppListValidatedUnavailableErrorIsStable(t *testing.T) {
	agg, store := setupTestAggregator(t)
	defer store.Close()
	router := setupMetaAppAPIRouter(t, agg)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/metaapp/list?chainName=mvc&sort=preference&basis=validated", nil)
	router.ServeHTTP(w, req)
	body := decodeMetaAppTestBody(t, w.Body.Bytes())
	// The exact stable message is the IDBots-facing contract; assert it
	// verbatim (not Contains).
	if body["message"] != "preference_validated_basis_unavailable" {
		t.Fatalf("stable message: %v", body["message"])
	}
}

func decodeMetaAppTestBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode body %s: %v", raw, err)
	}
	return body
}
