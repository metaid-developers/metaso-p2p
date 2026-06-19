package social

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestFollowingCompactResponseShape(t *testing.T) {
	router, agg := newSocialHandlerFixture(t)

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "idq-target", 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	status, body := callSocial(t, router, "/api/social/globalmetaid/idq-source/following")
	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if code := int(body["code"].(float64)); code != 0 {
		t.Fatalf("code = %d, want 0; body=%v", code, body)
	}

	data := mustObject(t, body, "data")
	if size := int(data["size"].(float64)); size != 20 {
		t.Fatalf("size = %d, want 20; data=%v", size, data)
	}
	if cursor := data["nextCursor"]; cursor != "" {
		t.Fatalf("nextCursor = %v, want empty; data=%v", cursor, data)
	}

	list := mustList(t, data, "list")
	if len(list) != 1 {
		t.Fatalf("list length = %d, want 1; list=%v", len(list), list)
	}

	item := mustObjectFromList(t, list, 0)
	if item["globalMetaId"] != "idq-target" {
		t.Fatalf("globalMetaId = %v, want idq-target; item=%v", item["globalMetaId"], item)
	}
	if item["name"] != "Target" {
		t.Fatalf("name = %v, want Target; item=%v", item["name"], item)
	}
	if item["nameId"] != "name-target:i0" {
		t.Fatalf("nameId = %v, want name-target:i0; item=%v", item["nameId"], item)
	}
	if got := item["avatarId"]; got != "avatar-target:i0" {
		t.Fatalf("avatarId = %v, want avatar-target:i0; item=%v", got, item)
	}
	for _, field := range []string{"bio", "bioId", "followedAt", "followPinId", "metaId", "address"} {
		if _, ok := item[field]; ok {
			t.Fatalf("compact response leaked %s: %+v", field, item)
		}
	}
}

func TestFollowingProfileResponseShape(t *testing.T) {
	router, agg := newSocialHandlerFixture(t)

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "idq-target", 1001)); err != nil {
		t.Fatalf("HandleBlockPin(create): %v", err)
	}

	status, body := callSocial(t, router, "/api/social/globalmetaid/idq-source/following?view=profile&size=20")
	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if code := int(body["code"].(float64)); code != 0 {
		t.Fatalf("code = %d, want 0; body=%v", code, body)
	}

	data := mustObject(t, body, "data")
	list := mustList(t, data, "list")
	if len(list) != 1 {
		t.Fatalf("list length = %d, want 1; list=%v", len(list), list)
	}

	item := mustObjectFromList(t, list, 0)
	if item["globalMetaId"] != "idq-target" {
		t.Fatalf("globalMetaId = %v, want idq-target; item=%v", item["globalMetaId"], item)
	}
	if item["bio"] != "target bio" {
		t.Fatalf("bio = %v, want target bio; item=%v", item["bio"], item)
	}
	if item["bioId"] != "bio-target:i0" {
		t.Fatalf("bioId = %v, want bio-target:i0; item=%v", item["bioId"], item)
	}
	if item["followPinId"] != "follow-1:i0" {
		t.Fatalf("followPinId = %v, want follow-1:i0; item=%v", item["followPinId"], item)
	}
	if got := int64(item["followedAt"].(float64)); got != 1001 {
		t.Fatalf("followedAt = %d, want 1001; item=%v", got, item)
	}
	for _, field := range []string{"metaId", "address"} {
		if _, ok := item[field]; ok {
			t.Fatalf("profile response leaked %s: %+v", field, item)
		}
	}
}

func TestRelationshipBidirectionalResponseShape(t *testing.T) {
	router, agg := newSocialHandlerFixture(t)

	if _, err := agg.HandleBlockPin(followPin("follow-1:i0", "idq-source", "meta-source", "1Source", "idq-target", 1001)); err != nil {
		t.Fatalf("HandleBlockPin(source->target): %v", err)
	}
	if _, err := agg.HandleBlockPin(followPin("follow-2:i0", "idq-target", "meta-target", "1Target", "idq-source", 1002)); err != nil {
		t.Fatalf("HandleBlockPin(target->source): %v", err)
	}

	status, body := callSocial(t, router, "/api/social/relationship?sourceGlobalMetaId=idq-source&targetGlobalMetaId=idq-target")
	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if code := int(body["code"].(float64)); code != 0 {
		t.Fatalf("code = %d, want 0; body=%v", code, body)
	}

	data := mustObject(t, body, "data")
	source := mustObject(t, data, "source")
	target := mustObject(t, data, "target")

	if source["globalMetaId"] != "idq-source" {
		t.Fatalf("source.globalMetaId = %v, want idq-source; source=%v", source["globalMetaId"], source)
	}
	if source["followsTarget"] != true {
		t.Fatalf("source.followsTarget = %v, want true; source=%v", source["followsTarget"], source)
	}
	if target["globalMetaId"] != "idq-target" {
		t.Fatalf("target.globalMetaId = %v, want idq-target; target=%v", target["globalMetaId"], target)
	}
	if target["followsSource"] != true {
		t.Fatalf("target.followsSource = %v, want true; target=%v", target["followsSource"], target)
	}
	if data["mutual"] != true {
		t.Fatalf("mutual = %v, want true; data=%v", data["mutual"], data)
	}
}

func TestRelationshipNoRelationStillReturnsFalseBooleans(t *testing.T) {
	router, _ := newSocialHandlerFixture(t)

	status, body := callSocial(t, router, "/api/social/relationship?sourceGlobalMetaId=idq-a&targetGlobalMetaId=idq-b")
	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200; body=%v", status, body)
	}
	if code := int(body["code"].(float64)); code != 0 {
		t.Fatalf("code = %d, want 0; body=%v", code, body)
	}

	data := mustObject(t, body, "data")
	source := mustObject(t, data, "source")
	target := mustObject(t, data, "target")
	if source["globalMetaId"] != "idq-a" {
		t.Fatalf("source.globalMetaId = %v, want idq-a; source=%v", source["globalMetaId"], source)
	}
	if source["followsTarget"] != false {
		t.Fatalf("source.followsTarget = %v, want false; source=%v", source["followsTarget"], source)
	}
	if target["globalMetaId"] != "idq-b" {
		t.Fatalf("target.globalMetaId = %v, want idq-b; target=%v", target["globalMetaId"], target)
	}
	if target["followsSource"] != false {
		t.Fatalf("target.followsSource = %v, want false; target=%v", target["followsSource"], target)
	}
	if data["mutual"] != false {
		t.Fatalf("mutual = %v, want false; data=%v", data["mutual"], data)
	}
}

func TestSocialHandlersReturnNotFoundForUnknownGlobalMetaId(t *testing.T) {
	router, _ := newSocialHandlerFixture(t)

	for _, path := range []string{
		"/api/social/globalmetaid/idq-missing/following",
		"/api/social/globalmetaid/idq-missing/followers",
		"/api/social/relationship?sourceGlobalMetaId=idq-missing&targetGlobalMetaId=idq-target",
	} {
		status, body := callSocial(t, router, path)
		if status != http.StatusOK {
			t.Fatalf("%s HTTP status = %d, want 200; body=%v", path, status, body)
		}
		if code := int(body["code"].(float64)); code != 40400 {
			t.Fatalf("%s code = %d, want 40400; body=%v", path, code, body)
		}
		if body["message"] != "subject not found" {
			t.Fatalf("%s message = %v, want subject not found; body=%v", path, body["message"], body)
		}
	}
}

func TestSocialHandlersRejectInvalidParams(t *testing.T) {
	router, _ := newSocialHandlerFixture(t)

	for _, path := range []string{
		"/api/social/globalmetaid/idq-source/following?size=0",
		"/api/social/globalmetaid/idq-source/following?size=101",
		"/api/social/globalmetaid/idq-source/following?view=full",
		"/api/social/globalmetaid/idq-source/followers?cursor=not-base64",
		"/api/social/relationship?targetGlobalMetaId=idq-target",
		"/api/social/relationship?sourceGlobalMetaId=idq-source",
	} {
		status, body := callSocial(t, router, path)
		if status != http.StatusOK {
			t.Fatalf("%s HTTP status = %d, want 200; body=%v", path, status, body)
		}
		if code := int(body["code"].(float64)); code != 40000 {
			t.Fatalf("%s code = %d, want 40000; body=%v", path, code, body)
		}
		if body["message"] != "invalid parameter" {
			t.Fatalf("%s message = %v, want invalid parameter; body=%v", path, body["message"], body)
		}
	}
}

func newSocialHandlerFixture(t *testing.T) (*gin.Engine, *Aggregator) {
	t.Helper()

	gin.SetMode(gin.TestMode)

	agg, store := newTestSocialAggregator(t)
	t.Cleanup(func() { _ = store.Close() })

	agg.SetProfileLookup(&fakeTargetLookup{
		byGlobalMetaId: map[string]*TargetRef{
			"idq-source": {
				MetaId:       "meta-source",
				GlobalMetaId: "idq-source",
				Address:      "1Source",
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
				Bio:          "source bio",
				BioId:        "bio-source:i0",
			},
			"idq-target": {
				MetaId:       "meta-target",
				GlobalMetaId: "idq-target",
				Address:      "1Target",
				Name:         "Target",
				NameId:       "name-target:i0",
				AvatarId:     "avatar-target:i0",
				Bio:          "target bio",
				BioId:        "bio-target:i0",
			},
			"idq-a": {
				MetaId:       "meta-a",
				GlobalMetaId: "idq-a",
				Address:      "1A",
				Name:         "A",
				NameId:       "name-a:i0",
				AvatarId:     "avatar-a:i0",
			},
			"idq-b": {
				MetaId:       "meta-b",
				GlobalMetaId: "idq-b",
				Address:      "1B",
				Name:         "B",
				NameId:       "name-b:i0",
				AvatarId:     "avatar-b:i0",
			},
		},
		byMetaId: map[string]*TargetRef{
			"meta-source": {
				MetaId:       "meta-source",
				GlobalMetaId: "idq-source",
				Address:      "1Source",
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
				Bio:          "source bio",
				BioId:        "bio-source:i0",
			},
			"meta-target": {
				MetaId:       "meta-target",
				GlobalMetaId: "idq-target",
				Address:      "1Target",
				Name:         "Target",
				NameId:       "name-target:i0",
				AvatarId:     "avatar-target:i0",
				Bio:          "target bio",
				BioId:        "bio-target:i0",
			},
		},
		byAddress: map[string]*TargetRef{
			"1Source": {
				MetaId:       "meta-source",
				GlobalMetaId: "idq-source",
				Address:      "1Source",
				Name:         "Source",
				NameId:       "name-source:i0",
				AvatarId:     "avatar-source:i0",
				Bio:          "source bio",
				BioId:        "bio-source:i0",
			},
			"1Target": {
				MetaId:       "meta-target",
				GlobalMetaId: "idq-target",
				Address:      "1Target",
				Name:         "Target",
				NameId:       "name-target:i0",
				AvatarId:     "avatar-target:i0",
				Bio:          "target bio",
				BioId:        "bio-target:i0",
			},
		},
	})

	router := gin.New()
	agg.RegisterRoutes(router.Group("/api"))
	return router, agg
}

func callSocial(t *testing.T, router *gin.Engine, path string) (int, map[string]any) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v; body=%q", err, rec.Body.String())
	}
	return rec.Code, body
}

func mustObject(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()

	value, ok := body[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %T, want object; body=%v", key, body[key], body)
	}
	return value
}

func mustList(t *testing.T, body map[string]any, key string) []any {
	t.Helper()

	value, ok := body[key].([]any)
	if !ok {
		t.Fatalf("%s = %T, want list; body=%v", key, body[key], body)
	}
	return value
}

func mustObjectFromList(t *testing.T, list []any, index int) map[string]any {
	t.Helper()

	item, ok := list[index].(map[string]any)
	if !ok {
		t.Fatalf("list[%d] = %T, want object; list=%v", index, list[index], list)
	}
	return item
}
