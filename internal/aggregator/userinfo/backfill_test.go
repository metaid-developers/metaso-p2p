package userinfo

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestDefaultBackfillPathsCoverSupportedProfilePaths(t *testing.T) {
	want := []string{
		"/info/name",
		"/info/avatar",
		"/info/nft-avatar",
		"/info/bio",
		"/info/role",
		"/info/soul",
		"/info/goal",
		"/info/chatSkills",
		"/info/LLM",
		"/info/llm",
		"/info/persona",
		"/info/homepage",
		"/info/background",
		"/info/chatpubkey",
	}
	if got := DefaultBackfillPaths(); !reflect.DeepEqual(got, want) {
		t.Fatalf("DefaultBackfillPaths() = %#v, want %#v", got, want)
	}
}

func TestBackfillInfoPathsStoresPersonaAndHomepage(t *testing.T) {
	now := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)
	metaID := "meta-user"
	globalMetaID := "gid-user"
	address := "addr-user"
	newPin := func(id, path, body string) map[string]any {
		return manapiInfoPinForTest(id, path, body, now.AddDate(0, -1, 0), metaID, globalMetaID, address)
	}
	olderRole := manapiInfoPinForTest("old-role:i0", "/info/role", "old role", now.AddDate(0, -3, 0), metaID, globalMetaID, address)

	requested := make(map[string]int)
	server := newUserInfoBackfillMANAPIServer(t, requested, map[string][]map[string]any{
		"/info/role":       {newPin("role:i0", "/info/role", "Backfilled role"), olderRole},
		"/info/soul":       {newPin("soul:i0", "/info/soul", "Backfilled soul")},
		"/info/goal":       {newPin("goal:i0", "/info/goal", "Backfilled goal")},
		"/info/chatSkills": {newPin("skills:i0", "/info/chatSkills", `["metabot-post-buzz"]`)},
		"/info/LLM": {
			func() map[string]any {
				pin := newPin("llm:i0", "/info/LLM", "")
				pin["contentSummary"] = `{"provider":"deepseek","model":"v3"}`
				return pin
			}(),
		},
		"/info/homepage": {newPin("homepage:i0", "/info/homepage", `{"uri":"metafile://homepage","renderer":"html"}`)},
	})
	defer server.Close()

	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	if err := agg.Backfill(BackfillOptions{
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    now.AddDate(0, -2, 0),
		PageSize: 2,
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	for _, path := range []string{"/info/role", "/info/soul", "/info/goal", "/info/chatSkills", "/info/LLM", "/info/homepage"} {
		if requested[path] != 1 {
			t.Fatalf("requested[%q] = %d, want 1; requested=%v", path, requested[path], requested)
		}
	}

	profile, err := agg.LookupByGlobalMetaId(globalMetaID)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile == nil {
		t.Fatal("LookupByGlobalMetaId returned nil profile")
	}
	if profile.Role != "Backfilled role" || profile.RoleId != "role:i0" {
		t.Fatalf("role not backfilled or old role was not skipped: %#v", profile)
	}
	if profile.Soul != "Backfilled soul" || profile.SoulId != "soul:i0" {
		t.Fatalf("soul not backfilled: %#v", profile)
	}
	if profile.Goal != "Backfilled goal" || profile.GoalId != "goal:i0" {
		t.Fatalf("goal not backfilled: %#v", profile)
	}
	if profile.ChatSkills != `["metabot-post-buzz"]` || profile.ChatSkillsId != "skills:i0" {
		t.Fatalf("chatSkills not backfilled: %#v", profile)
	}
	if profile.LLM != `{"provider":"deepseek","model":"v3"}` || profile.LLMId != "llm:i0" {
		t.Fatalf("LLM summary fallback not backfilled: %#v", profile)
	}
	if profile.Homepage != `{"uri":"metafile://homepage","renderer":"html"}` || profile.HomepageId != "homepage:i0" {
		t.Fatalf("homepage not backfilled: %#v", profile)
	}
	if profile.GlobalMetaID != globalMetaID || profile.Address != address || profile.ChainName != "mvc" {
		t.Fatalf("identity fields not backfilled: %#v", profile)
	}
}

func TestBackfillReplaysInfoPinsOldestToNewestAcrossPages(t *testing.T) {
	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	metaID := "meta-user"
	globalMetaID := "gid-user"
	address := "addr-user"

	olderClear := manapiInfoPinForTest("older-clear:i0", "/info/homepage", "", now.Add(-2*time.Minute), metaID, globalMetaID, address)
	newerHomepage := manapiInfoPinForTest("newer-homepage:i0", "/info/homepage", `{"uri":"metaapp://homepage","renderer":"metaapp","contentType":"application/vnd.metaapp"}`, now.Add(-time.Minute), metaID, globalMetaID, address)
	olderAvatar := manapiInfoPinForTest("older-avatar:i0", "/info/avatar", "older", now.Add(-3*time.Minute), metaID, globalMetaID, address)
	middleAvatar := manapiInfoPinForTest("middle-avatar:i0", "/info/avatar", "middle", now.Add(-2*time.Minute), metaID, globalMetaID, address)
	newerAvatar := manapiInfoPinForTest("newer-avatar:i0", "/info/avatar", "newer", now.Add(-time.Minute), metaID, globalMetaID, address)
	for _, avatar := range []map[string]any{olderAvatar, middleAvatar, newerAvatar} {
		avatar["contentType"] = "image/png"
	}

	server := newUserInfoBackfillMANAPIServer(t, map[string]int{}, map[string][]map[string]any{
		"/info/homepage": {newerHomepage, olderClear},
		// Deliberately neither newest-first nor oldest-first. Backfill must
		// order revisions before applying fixed-path create semantics.
		"/info/avatar": {middleAvatar, newerAvatar, olderAvatar},
	})
	defer server.Close()

	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	if err := agg.Backfill(BackfillOptions{
		Client:   NewBackfillClient(server.URL, server.Client()),
		Since:    now.Add(-time.Hour),
		PageSize: 1,
	}); err != nil {
		t.Fatalf("Backfill: %v", err)
	}

	profile, err := agg.LookupByGlobalMetaId(globalMetaID)
	if err != nil {
		t.Fatalf("LookupByGlobalMetaId: %v", err)
	}
	if profile == nil {
		t.Fatal("LookupByGlobalMetaId returned nil profile")
	}
	if profile.HomepageId != "newer-homepage:i0" {
		t.Fatalf("HomepageId = %q, want newer-homepage:i0; profile=%#v", profile.HomepageId, profile)
	}
	if profile.Homepage != `{"uri":"metaapp://homepage","renderer":"metaapp","contentType":"application/vnd.metaapp"}` {
		t.Fatalf("Homepage = %q, want newer homepage payload", profile.Homepage)
	}
	if profile.AvatarId != "newer-avatar:i0" || profile.Avatar != "/content/newer-avatar:i0" {
		t.Fatalf("avatar create revisions did not resolve to newest pin: %#v", profile)
	}
	if profile.AvatarContentType != "image/png" {
		t.Fatalf("AvatarContentType = %q, want image/png", profile.AvatarContentType)
	}
}

func manapiInfoPinForTest(id, path, body string, ts time.Time, metaID, globalMetaID, address string) map[string]any {
	return map[string]any{
		"id":             id,
		"path":           path,
		"originalPath":   path,
		"operation":      "create",
		"contentType":    "text/plain",
		"contentBody":    body,
		"contentSummary": "",
		"metaId":         metaID,
		"globalMetaId":   globalMetaID,
		"address":        address,
		"createMetaId":   metaID,
		"createAddress":  address,
		"chainName":      "mvc",
		"timestamp":      ts.UnixMilli(),
		"genesisHeight":  int64(123),
		"originalId":     "",
	}
}

func newUserInfoBackfillMANAPIServer(t *testing.T, requested map[string]int, pinsByPath map[string][]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/path/list" {
			t.Fatalf("request path: got %q want /pin/path/list", r.URL.Path)
		}
		path := r.URL.Query().Get("path")
		if path == "" {
			t.Fatal("missing path query")
		}
		if r.URL.Query().Get("size") == "" {
			t.Fatal("missing size query")
		}
		size, err := strconv.Atoi(r.URL.Query().Get("size"))
		if err != nil || size <= 0 {
			t.Fatalf("invalid size query %q", r.URL.Query().Get("size"))
		}
		requested[path]++
		pins := pinsByPath[path]
		start := 0
		if cursor := r.URL.Query().Get("cursor"); cursor != "" {
			start, err = strconv.Atoi(cursor)
			if err != nil || start < 0 {
				t.Fatalf("invalid cursor query %q", cursor)
			}
		}
		if start > len(pins) {
			start = len(pins)
		}
		end := start + size
		if end > len(pins) {
			end = len(pins)
		}
		nextCursor := ""
		if end < len(pins) {
			nextCursor = strconv.Itoa(end)
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"code": 1,
			"data": map[string]any{
				"list":       pins[start:end],
				"nextCursor": nextCursor,
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
}
