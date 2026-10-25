package preference

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupComparisonsRouter(t *testing.T) (*Aggregator, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	pref, pub, store := setupPreferenceTest(t)
	t.Cleanup(func() { store.Close() })
	appA, appB := twoAppsAB(t, pub)
	appC := motionVideoApp{rootPinId: "app-root-c:i0", versionPin: "app-root-c:i0", videoRef: "video-c:i0", videoSha: shaC, chainName: "mvc", publisherId: "gid-author-c"}
	publishMotionVideoApp(t, pub, appC)

	body := batchJSON(`"motion-video"`,
		itemJSON(appA.ref(), appB.ref(), "a"),
		itemJSON(appB.ref(), appC.ref(), "a"), // B beats C
	)
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-e1:i0", Signer: "gid-judge-1", Body: body, Timestamp: 1000, Height: 10})
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-e2:i0", Signer: "gid-judge-2", Body: abBatch("tie", ""), Timestamp: 1100, Height: 11})
	mustFeedbackMempool(t, pref, feedbackPinOpts{PinId: "fb-e3:i0", Signer: "gid-judge-3", Body: abBatch("a", ""), Timestamp: 1200})
	mustFeedbackBlock(t, pref, feedbackPinOpts{PinId: "fb-e4:i0", Signer: "gid-judge-4", Body: abBatch("b", `"test:motion-video-smoke"`), Timestamp: 1300, Height: 12})

	router := gin.New()
	apiGroup := router.Group("/api")
	pref.RegisterRoutes(apiGroup)
	return pref, router
}

func getJSON(t *testing.T, router *gin.Engine, path string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	router.ServeHTTP(w, req)
	return w.Code, decodeTestBody(t, w.Body.Bytes())
}

func decodeTestBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode response body %s: %v", raw, err)
	}
	return body
}

func TestComparisonsEndpointDefaultView(t *testing.T) {
	_, router := setupComparisonsRouter(t)

	code, body := getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open")
	if code != 200 || body["code"] != float64(0) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	data := body["data"].(map[string]any)
	items := data["items"].([]any)
	// Default: eligible, confirmed, non-test → fb-e1 (2 items) + fb-e2 (1).
	if len(items) != 3 {
		t.Fatalf("default view items: %v", body["data"])
	}
	first := items[0].(map[string]any)
	if first["feedbackPinId"] != "fb-e2:i0" {
		t.Fatalf("newest confirmed first: %v", first)
	}
	if first["basis"] != "open" || first["isMempool"] != false {
		t.Fatalf("wire fields: %v", first)
	}
	if _, ok := first["excluded"]; ok {
		t.Fatalf("excluded fields must be absent in the default view: %v", first)
	}
	if first["signerGlobalMetaId"] != "gid-judge-2" {
		t.Fatalf("signer: %v", first)
	}
	client := first["client"].(map[string]any)
	if client["artifact"] != "metaapp://arena-root:i0" || client["mode"] != "arena" {
		t.Fatalf("client: %v", client)
	}
}

func TestComparisonsEndpointSubjectOutcome(t *testing.T) {
	_, router := setupComparisonsRouter(t)

	// subject = video B: preferred when B won, rejected when it lost; ties
	// only in all.
	code, body := getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&subject="+shaB+"&outcome=preferred")
	if code != 200 || body["code"] != float64(0) {
		t.Fatalf("status=%d body=%v", code, body)
	}
	items := body["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("preferred items: %v", items)
	}
	item := items[0].(map[string]any)
	if item["outcome"] != "preferred" || item["subject"] != shaB {
		t.Fatalf("subject-relative outcome: %v", item)
	}

	_, body = getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&subject="+shaB+"&outcome=rejected")
	items = body["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("rejected items: %v", items)
	}
	if items[0].(map[string]any)["outcome"] != "rejected" {
		t.Fatalf("rejected outcome: %v", items[0])
	}

	_, body = getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&subject="+shaB+"&outcome=all")
	items = body["data"].(map[string]any)["items"].([]any)
	// A/B loss + B/C win + A/B tie = 3.
	if len(items) != 3 {
		t.Fatalf("all items for subject B: %v", items)
	}
	for _, raw := range items {
		outcome := raw.(map[string]any)["outcome"]
		if outcome != "preferred" && outcome != "rejected" && outcome != "tie" {
			t.Fatalf("outcome must be subject-relative: %v", raw)
		}
	}
}

func TestComparisonsEndpointMempoolAndTestViews(t *testing.T) {
	_, router := setupComparisonsRouter(t)

	_, body := getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&includeMempool=1")
	items := body["data"].(map[string]any)["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("includeMempool adds the pending item: %v", items)
	}
	hasPending := false
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["isMempool"] == true {
			hasPending = true
			if item["confirmedAt"] != nil {
				t.Fatalf("pending item must have null confirmedAt: %v", item)
			}
		}
	}
	if !hasPending {
		t.Fatal("includeMempool=1 must surface the pending comparison")
	}

	_, body = getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&includeTest=1")
	items = body["data"].(map[string]any)["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("includeTest adds the smoke item: %v", items)
	}

	_, body = getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&includeExcluded=1")
	items = body["data"].(map[string]any)["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("no exclusions in this fixture: %v", items)
	}
}

func TestComparisonsEndpointParamValidation(t *testing.T) {
	_, router := setupComparisonsRouter(t)

	cases := []struct {
		name string
		path string
		code float64
		msg  string
	}{
		{"missing chain", "/api/preference/comparisons?category=motion-video&basis=open", 40000, "chainName"},
		{"wrong category", "/api/preference/comparisons?chainName=mvc&category=reactions&basis=open", 40000, "motion-video"},
		{"validated basis", "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=validated", 40000, "preference_validated_basis_unavailable"},
		{"outcome without subject", "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&outcome=preferred", 40000, "subject"},
		{"bad outcome", "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&outcome=worst", 40000, "outcome"},
		{"bad subject", "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&subject=xyz&outcome=all", 40000, "subject"},
		{"bad flag", "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&includeMempool=2", 40000, "flag"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := getJSON(t, router, tc.path)
			if code != 200 || body["code"] != tc.code {
				t.Fatalf("status=%d body=%v", code, body)
			}
			message := body["message"].(string)
			if !strings.Contains(message, tc.msg) {
				t.Fatalf("message %q must mention %q", message, tc.msg)
			}
		})
	}
}

func TestComparisonsEndpointPaginationAndCursorInvalidation(t *testing.T) {
	_, router := setupComparisonsRouter(t)
	base := "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&size=2"

	_, page1 := getJSON(t, router, base)
	data := page1["data"].(map[string]any)
	if data["hasMore"] != true {
		t.Fatalf("page1: %v", data)
	}
	cursor, _ := data["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("page1 cursor missing")
	}

	_, page2 := getJSON(t, router, base+"&cursor="+cursor)
	items := page2["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("page2 items: %v", items)
	}

	// Changing a filter invalidates the cursor.
	code, body := getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&size=2&includeTest=1&cursor="+cursor)
	if code != 200 || body["code"] != float64(40000) || body["message"] != "invalid cursor" {
		t.Fatalf("cursor must invalidate on filter change: %v", body)
	}

	// Garbage cursor is rejected.
	code, body = getJSON(t, router, base+"&cursor=garbage")
	if code != 200 || body["code"] != float64(40000) {
		t.Fatalf("garbage cursor: %v", body)
	}
}

func TestComparisonsEndpointWireShape(t *testing.T) {
	_, router := setupComparisonsRouter(t)
	_, body := getJSON(t, router, "/api/preference/comparisons?chainName=mvc&category=motion-video&basis=open&subject="+shaA+"&outcome=all")
	items := body["data"].(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("subject A items: %v", items)
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		a := item["a"].(map[string]any)
		b := item["b"].(map[string]any)
		for _, field := range []string{"chainName", "app", "version", "video", "videoSha256"} {
			if _, ok := a[field]; !ok {
				t.Fatalf("ArtifactRef missing %s: %v", field, a)
			}
			if _, ok := b[field]; !ok {
				t.Fatalf("ArtifactRef missing %s: %v", field, b)
			}
		}
		if a["chainName"] != "mvc" || b["chainName"] != "mvc" {
			t.Fatalf("chainName must scope both refs: %v", item)
		}
	}
}
