package block

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func newBlockHandlerFixture(t *testing.T) (*gin.Engine, *Aggregator) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	agg, _ := newTestAggregatorWithLookup(t)

	router := gin.New()
	apiGroup := router.Group("/api")
	agg.RegisterRoutes(apiGroup)
	return router, agg
}

func callBlocks(t *testing.T, router *gin.Engine, path string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response for %s: %v (body=%s)", path, err, rec.Body.String())
	}
	return rec.Code, body
}

func TestBlocksEndpointTypeAllListsBothDirections(t *testing.T) {
	router, agg := newBlockHandlerFixture(t)

	// Alice blocks Bob; Carol blocks Alice.
	if _, err := agg.HandleBlockPin(blockPin("blk-1:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("alice blocks bob: %v", err)
	}
	if _, err := agg.HandleBlockPin(blockPin("blk-2:i0", "/private/block/simpleprivateblock", "meta-dave", hexDave, idAlice, 1, 1001)); err != nil {
		t.Fatalf("carol(hex) blocks alice: %v", err)
	}

	status, body := callBlocks(t, router, "/api/social/globalmetaid/"+idAlice+"/blocks")
	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d; body=%v", status, body)
	}
	if code := int(body["code"].(float64)); code != 0 {
		t.Fatalf("code = %d; body=%v", code, body)
	}
	data := body["data"].(map[string]interface{})
	if size := int(data["size"].(float64)); size != 20 {
		t.Fatalf("size = %d; want 20", size)
	}
	if data["nextCursor"] != "" {
		t.Fatalf("nextCursor = %v; want empty", data["nextCursor"])
	}
	list := data["list"].([]interface{})
	if len(list) != 2 {
		t.Fatalf("list length = %d; want 2 (one per direction)", len(list))
	}

	// Newest first: Carol→Alice (ts 1001) before Alice→Bob (ts 1000).
	first := list[0].(map[string]interface{})
	if first["globalMetaId"] != idCarol || first["direction"] != "blockedBy" {
		t.Fatalf("first = %v; want idCarol/blockedBy", first)
	}
	second := list[1].(map[string]interface{})
	if second["globalMetaId"] != idBob || second["direction"] != "blocking" {
		t.Fatalf("second = %v; want idBob/blocking", second)
	}
	if got := mustFloat(t, first, "blockedAt"); got != 1001 {
		t.Fatalf("blockedAt = %v; want 1001", got)
	}
	if got := first["blockPinId"]; got != "blk-2:i0" {
		t.Fatalf("blockPinId = %v; want blk-2:i0", got)
	}
}

func TestBlocksEndpointTypeFilters(t *testing.T) {
	router, agg := newBlockHandlerFixture(t)

	if _, err := agg.HandleBlockPin(blockPin("blk-1:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("alice blocks bob: %v", err)
	}
	if _, err := agg.HandleBlockPin(blockPin("blk-2:i0", "/protocols/simpleblock", idCarol, "", idAlice, 1, 1001)); err != nil {
		t.Fatalf("carol blocks alice: %v", err)
	}

	_, body := callBlocks(t, router, "/api/social/globalmetaid/"+idAlice+"/blocks?type=blocking")
	list := body["data"].(map[string]interface{})["list"].([]interface{})
	if len(list) != 1 || list[0].(map[string]interface{})["globalMetaId"] != idBob {
		t.Fatalf("blocking list = %v; want only idBob", list)
	}

	_, body = callBlocks(t, router, "/api/social/globalmetaid/"+idAlice+"/blocks?type=blockedBy")
	list = body["data"].(map[string]interface{})["list"].([]interface{})
	if len(list) != 1 || list[0].(map[string]interface{})["globalMetaId"] != idCarol {
		t.Fatalf("blockedBy list = %v; want only idCarol", list)
	}

	_, body = callBlocks(t, router, "/api/social/globalmetaid/"+idAlice+"/blocks?type=nonsense")
	if code := int(body["code"].(float64)); code != 40000 {
		t.Fatalf("bad type code = %d; want 40000", code)
	}
}

func TestBlocksEndpointProfileViewAndLegacyHexSubject(t *testing.T) {
	router, agg := newBlockHandlerFixture(t)

	if _, err := agg.HandleBlockPin(blockPin("blk-1:i0", "/protocols/simpleblock", idAlice, "", idBob, 1, 1000)); err != nil {
		t.Fatalf("alice blocks bob: %v", err)
	}

	// Subject by legacy hex metaid normalizes to the canonical id.
	status, body := callBlocks(t, router, "/api/social/globalmetaid/meta-alice/blocks?view=profile")
	if status != http.StatusOK || int(body["code"].(float64)) != 0 {
		t.Fatalf("hex subject lookup failed: status=%d body=%v", status, body)
	}
	list := body["data"].(map[string]interface{})["list"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("list = %v; want 1", list)
	}
	item := list[0].(map[string]interface{})
	if item["globalMetaId"] != idBob {
		t.Fatalf("globalMetaId = %v; want idBob", item["globalMetaId"])
	}
	if item["bio"] != "bob bio" || item["bioId"] != "bob-bio:i0" {
		t.Fatalf("profile fields = %v; want bio/bioId", item)
	}
	if item["name"] != "Bob" || item["nameId"] != "name-bob:i0" {
		t.Fatalf("display fields = %v", item)
	}
}

func TestBlocksEndpointPagination(t *testing.T) {
	router, agg := newBlockHandlerFixture(t)

	// Alice blocks 3 peers at increasing timestamps.
	for i, peer := range []string{idBob, idCarol, "idq1dave000000000000000000000000000000000"} {
		pin := blockPin("blk-"+itoaHelper(i+1)+":i0", "/protocols/simpleblock", idAlice, "", peer, 1, int64(1000+i))
		if _, err := agg.HandleBlockPin(pin); err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
	}

	status, body := callBlocks(t, router, "/api/social/globalmetaid/"+idAlice+"/blocks?size=2")
	if status != http.StatusOK {
		t.Fatalf("page1 status = %d", status)
	}
	data := body["data"].(map[string]interface{})
	list := data["list"].([]interface{})
	if len(list) != 2 {
		t.Fatalf("page1 list = %d; want 2", len(list))
	}
	cursor, ok := data["nextCursor"].(string)
	if !ok || cursor == "" {
		t.Fatalf("page1 nextCursor = %v; want non-empty", data["nextCursor"])
	}

	_, body = callBlocks(t, router, "/api/social/globalmetaid/"+idAlice+"/blocks?size=2&cursor="+cursor)
	list = body["data"].(map[string]interface{})["list"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("page2 list = %d; want 1", len(list))
	}
	if got := body["data"].(map[string]interface{})["nextCursor"]; got != "" {
		t.Fatalf("page2 nextCursor = %v; want empty", got)
	}

	// A cursor from the wrong subject is rejected (40000).
	_, body = callBlocks(t, router, "/api/social/globalmetaid/"+idBob+"/blocks?cursor="+cursor)
	if code := int(body["code"].(float64)); code != 40000 {
		t.Fatalf("wrong-subject cursor code = %d; want 40000", code)
	}
}

func TestBlocksEndpointErrorContract(t *testing.T) {
	router, _ := newBlockHandlerFixture(t)

	// Unknown subject → 40400 subject not found.
	_, body := callBlocks(t, router, "/api/social/globalmetaid/"+idCarol+"/blocks")
	if code := int(body["code"].(float64)); code != 40400 {
		t.Fatalf("unknown subject code = %d; want 40400 (idCarol has no profile)", code)
	}
	if body["message"] != "subject not found" {
		t.Fatalf("message = %v", body["message"])
	}

	for _, path := range []string{
		"/api/social/globalmetaid/" + idAlice + "/blocks?size=0",
		"/api/social/globalmetaid/" + idAlice + "/blocks?size=101",
		"/api/social/globalmetaid/" + idAlice + "/blocks?view=full",
		"/api/social/globalmetaid/" + idAlice + "/blocks?cursor=not-base64!",
	} {
		_, body := callBlocks(t, router, path)
		if code := int(body["code"].(float64)); code != 40000 {
			t.Fatalf("%s code = %d; want 40000", path, code)
		}
		if body["message"] != "invalid parameter" {
			t.Fatalf("%s message = %v", path, body["message"])
		}
	}
}

func mustFloat(t *testing.T, obj map[string]interface{}, key string) int64 {
	t.Helper()
	raw, ok := obj[key].(float64)
	if !ok {
		t.Fatalf("key %s = %T, want number", key, obj[key])
	}
	return int64(raw)
}

func itoaHelper(v int) string {
	digits := []byte{}
	for v > 0 {
		digits = append([]byte{byte('0' + v%10)}, digits...)
		v /= 10
	}
	return string(digits)
}
