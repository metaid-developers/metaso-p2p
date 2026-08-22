package botsearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/groupchat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const fixedNowMs = 1_780_000_000_000

type fakeLocalPresence struct {
	entries []presence.OnlineEntry
}

func (f *fakeLocalPresence) OnlineEntries() []presence.OnlineEntry { return f.entries }

type fakeGlobalPresence struct {
	enabled bool
	items   []presence.OnlineEntry
}

func (f *fakeGlobalPresence) Enabled() bool        { return f.enabled }
func (f *fakeGlobalPresence) DefaultScope() string { return "global" }
func (f *fakeGlobalPresence) OnlineList(local []presence.OnlineEntry, page, size int) []presence.OnlineEntry {
	merged := append([]presence.OnlineEntry{}, local...)
	return append(merged, f.items...)
}
func (f *fakeGlobalPresence) Stats(local []presence.OnlineEntry) presence.GlobalStats {
	return presence.GlobalStats{}
}

type searchTestBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Candidates []candidate `json:"candidates"`
		NextCursor *string     `json:"nextCursor"`
		QueriedAt  int64       `json:"queriedAt"`
	} `json:"data"`
}

// searchFixture wires the real userinfo / groupchat / skillservice
// aggregators on one Pebble store, exactly like main.go, plus a botsearch
// aggregator under test with a deterministic clock.
type searchFixture struct {
	router   *gin.Engine
	userAgg  *userinfo.Aggregator
	groupAgg *groupchat.Aggregator
	skillAgg *skillservice.Aggregator
	search   *Aggregator
}

func newSearchFixture(t *testing.T, local presence.LocalReader, global presence.GlobalReader) *searchFixture {
	t.Helper()

	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { store.Close() })
	cacheProvider := cache.New(store)

	userAgg := &userinfo.Aggregator{}
	if err := userAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("init userinfo: %v", err)
	}
	groupAgg := &groupchat.Aggregator{}
	if err := groupAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("init groupchat: %v", err)
	}
	skillAgg := &skillservice.Aggregator{}
	if err := skillAgg.Init(store, cacheProvider); err != nil {
		t.Fatalf("init skillservice: %v", err)
	}

	search := &Aggregator{now: func() int64 { return fixedNowMs }}
	if err := search.Init(store, cacheProvider); err != nil {
		t.Fatalf("init botsearch: %v", err)
	}
	search.SetProfileSource(userAgg)
	search.SetGroupHistorySource(groupAgg)
	search.SetSkillLister(skillAgg)
	search.SetPresenceReaders(local, global)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	search.RegisterRoutes(router.Group("/api"))

	return &searchFixture{router: router, userAgg: userAgg, groupAgg: groupAgg, skillAgg: skillAgg, search: search}
}

func (f *searchFixture) call(t *testing.T, body string) (int, searchTestBody) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/bots/search", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	var decoded searchTestBody
	if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode body: %v raw=%s", err, w.Body.String())
	}
	return w.Code, decoded
}

type profileSeed struct {
	metaId   string
	global   string
	address  string
	name     string
	bio      string
	role     string
	goal     string
	skills   string // raw /info/chatskills body
	pubkey   string
	homepage string
}

func (f *searchFixture) seedProfile(t *testing.T, seed profileSeed) {
	t.Helper()
	ts := int64(1_778_000_000_000)
	pin := func(idSuffix, path, body string) {
		t.Helper()
		if _, err := f.userAgg.HandleBlockPin(&aggregator.PinInscription{
			Id:           fmt.Sprintf("%s-%s:i0", idSuffix, seed.metaId),
			Path:         path,
			Operation:    "create",
			ContentBody:  []byte(body),
			MetaId:       seed.metaId,
			CreateMetaId: seed.metaId,
			Address:      seed.address,
			GlobalMetaId: seed.global,
			ChainName:    "mvc",
			Timestamp:    ts,
		}); err != nil {
			t.Fatalf("seed %s for %s: %v", path, seed.metaId, err)
		}
		ts += 1000
	}
	pin("init", "/", "")
	if seed.name != "" {
		pin("name", "/info/name", seed.name)
	}
	if seed.bio != "" {
		pin("bio", "/info/bio", seed.bio)
	}
	if seed.role != "" {
		pin("role", "/info/role", seed.role)
	}
	if seed.goal != "" {
		pin("goal", "/info/goal", seed.goal)
	}
	if seed.skills != "" {
		pin("skills", "/info/chatskills", seed.skills)
	}
	if seed.pubkey != "" {
		pin("pubkey", "/info/chatpubkey", seed.pubkey)
	}
	if seed.homepage != "" {
		pin("homepage", "/info/homepage", seed.homepage)
	}
}

func (f *searchFixture) seedGroupCreate(t *testing.T, pinId, groupId, name, note, metaId, global, address string, ts int64) {
	t.Helper()
	if _, err := f.groupAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            pinId,
		Path:          "/protocols/simplegroupcreate",
		Operation:     "create",
		CreateAddress: address,
		CreateMetaId:  metaId,
		MetaId:        metaId,
		GlobalMetaId:  global,
		ChainName:     "mvc",
		Timestamp:     ts,
		ContentBody:   mustJSON(t, map[string]interface{}{"groupId": groupId, "groupName": name, "groupNote": note}),
	}); err != nil {
		t.Fatalf("seed group create %s: %v", groupId, err)
	}
}

func (f *searchFixture) seedGroupJoin(t *testing.T, pinId, groupId string, state float64, metaId, global, address string, ts int64) {
	t.Helper()
	if _, err := f.groupAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            pinId,
		Path:          "/protocols/simplegroupjoin",
		Operation:     "create",
		CreateAddress: address,
		CreateMetaId:  metaId,
		MetaId:        metaId,
		GlobalMetaId:  global,
		ChainName:     "mvc",
		Timestamp:     ts,
		ContentBody:   mustJSON(t, map[string]interface{}{"groupId": groupId, "state": state}),
	}); err != nil {
		t.Fatalf("seed group join %s state=%v: %v", groupId, state, err)
	}
}

func mustJSON(t *testing.T, value interface{}) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// seedMainFixture builds the shared staffing scenario:
//   - bot A "Counsel-bot": legal profile, online, homepage + chatpubkey,
//     published skill service, created g1 and joined g2;
//   - bot B "设计狮": design profile, online, created g2, joined then left g1;
//   - bot C "离线写手": writer profile, offline;
//   - bot D "无钥bot": contract bio but no chatpubkey, offline;
//   - bot E "占卜师": tarot bio (contract recall example), online, no groups.
func seedMainFixture(t *testing.T) *searchFixture {
	t.Helper()

	local := &fakeLocalPresence{entries: []presence.OnlineEntry{
		{MetaId: "idq1-bot-a", Type: "app", ConnectedAt: fixedNowMs - 60_000, LastSeenAt: fixedNowMs - 42_000},
		{MetaId: "meta-bot-b", Type: "pc", ConnectedAt: fixedNowMs - 100_000},
		{MetaId: "idq1-bot-e", Type: "app", ConnectedAt: fixedNowMs - 20_000, LastSeenAt: fixedNowMs - 10_000},
	}}
	f := newSearchFixture(t, local, nil)

	f.seedProfile(t, profileSeed{
		metaId: "meta-bot-a", global: "idq1-bot-a", address: "addr-bot-a",
		name: "Counsel-bot", bio: "商业合同与合规审查", role: "法律顾问", goal: "帮客户审查合同",
		skills: `["legal-review","contract"]`, pubkey: "pk-a", homepage: "metaapp://home-a:i0",
	})
	f.seedProfile(t, profileSeed{
		metaId: "meta-bot-b", global: "idq1-bot-b", address: "addr-bot-b",
		name: "设计狮", bio: "视觉设计与出图", skills: `["design","poster"]`, pubkey: "pk-b",
	})
	f.seedProfile(t, profileSeed{
		metaId: "meta-bot-c", global: "idq1-bot-c", address: "addr-bot-c",
		name: "离线写手", bio: "文案写作", skills: `["writing"]`, pubkey: "pk-c",
	})
	f.seedProfile(t, profileSeed{
		metaId: "meta-bot-d", global: "idq1-bot-d", address: "addr-bot-d",
		name: "无钥bot", bio: "合同纠纷咨询",
	})
	f.seedProfile(t, profileSeed{
		metaId: "meta-bot-e", global: "idq1-bot-e", address: "addr-bot-e",
		name: "占卜师", bio: "占卜塔罗牌", pubkey: "pk-e",
	})

	const (
		tCreateG1 = 1_779_000_000_000
		tCreateG2 = 1_779_100_000_000
		tJoinG1   = 1_779_050_000_000
		tLeaveG1  = 1_779_060_000_000
		tJoinG2   = 1_779_200_000_000
	)
	f.seedGroupCreate(t, "create-g1:i0", "g1:i0", "合同审查协作", "审查合同条款", "meta-bot-a", "idq1-bot-a", "addr-bot-a", tCreateG1)
	f.seedGroupCreate(t, "create-g2:i0", "g2:i0", "海报设计任务", "", "meta-bot-b", "idq1-bot-b", "addr-bot-b", tCreateG2)
	f.seedGroupJoin(t, "join-g1-b:i0", "g1:i0", 1, "meta-bot-b", "idq1-bot-b", "addr-bot-b", tJoinG1)
	f.seedGroupJoin(t, "leave-g1-b:i0", "g1:i0", -1, "meta-bot-b", "idq1-bot-b", "addr-bot-b", tLeaveG1)
	f.seedGroupJoin(t, "join-g2-a:i0", "g2:i0", 1, "meta-bot-a", "idq1-bot-a", "addr-bot-a", tJoinG2)

	// One published skill service for A.
	if _, err := f.skillAgg.HandleBlockPin(&aggregator.PinInscription{
		Id:            "service-a:i0",
		Path:          skillservice.PathSkillService,
		Operation:     skillservice.OperationCreate,
		ContentBody:   mustJSON(t, map[string]interface{}{"serviceName": "legal-review-service", "displayName": "Legal Review", "providerSkill": "legal-review", "outputType": "text", "price": "1", "currency": "SPACE", "settlementKind": "address", "paymentAddress": "addr-bot-a"}),
		ContentType:   "application/json",
		ChainName:     "mvc",
		GlobalMetaId:  "idq1-bot-a",
		MetaId:        "meta-bot-a",
		CreateMetaId:  "meta-bot-a",
		Address:       "addr-bot-a",
		CreateAddress: "addr-bot-a",
		Timestamp:     1_779_300_000_000,
	}); err != nil {
		t.Fatalf("seed skill service: %v", err)
	}

	return f
}

func candidateByGlobalMetaId(t *testing.T, candidates []candidate, globalMetaId string) candidate {
	t.Helper()
	for _, c := range candidates {
		if c.GlobalMetaId == globalMetaId {
			return c
		}
	}
	t.Fatalf("candidate %s not found in %+v", globalMetaId, candidates)
	return candidate{}
}

func TestSearchDefaultsFilterAndEnrich(t *testing.T) {
	f := seedMainFixture(t)

	// onlineOnly and hasChatPubkey both default to true: C (offline), D (no
	// pubkey) must not appear even though D's bio matches 合同.
	status, body := f.call(t, `{"query":"合同"}`)
	if status != http.StatusOK || body.Code != 0 {
		t.Fatalf("status=%d code=%d message=%q", status, body.Code, body.Message)
	}
	if len(body.Data.Candidates) != 2 {
		t.Fatalf("candidates = %+v, want [A B]", body.Data.Candidates)
	}

	a := body.Data.Candidates[0]
	if a.GlobalMetaId != "idq1-bot-a" {
		t.Fatalf("first candidate = %s, want idq1-bot-a (higher score)", a.GlobalMetaId)
	}
	if a.Name != "Counsel-bot" || a.Role != "法律顾问" || a.Goal != "帮客户审查合同" || a.Bio != "商业合同与合规审查" {
		t.Fatalf("candidate A profile fields = %+v", a)
	}
	if !a.HasChatPubkey || !a.HasHomepage || a.Homepage != "metaapp://home-a:i0" {
		t.Fatalf("candidate A flags = %+v", a)
	}
	if !a.IsOnline || a.LastSeenAgoSeconds == nil || *a.LastSeenAgoSeconds != 42 {
		t.Fatalf("candidate A presence = online:%v ago:%v", a.IsOnline, a.LastSeenAgoSeconds)
	}
	if len(a.PublishedSkills) != 1 || a.PublishedSkills[0] != "legal-review-service" {
		t.Fatalf("candidate A publishedSkills = %v", a.PublishedSkills)
	}
	if a.ChainName != "mvc" {
		t.Fatalf("candidate A chainName = %q", a.ChainName)
	}

	// Group-task fold-in: newest first, chair vs member, cap fields.
	if a.GroupTaskCount != 2 || len(a.RecentGroupTasks) != 2 {
		t.Fatalf("candidate A history = count %d recent %+v", a.GroupTaskCount, a.RecentGroupTasks)
	}
	newest := a.RecentGroupTasks[0]
	if newest.GroupId != "g2:i0" || newest.JoinedAs != "member" || newest.JoinPinId != "join-g2-a:i0" {
		t.Fatalf("A newest group task = %+v", newest)
	}
	oldest := a.RecentGroupTasks[1]
	if oldest.GroupId != "g1:i0" || oldest.JoinedAs != "chair" || oldest.JoinPinId != "create-g1:i0" {
		t.Fatalf("A oldest group task = %+v", oldest)
	}
	if oldest.Title != "合同审查协作" || oldest.Goal != "审查合同条款" {
		t.Fatalf("g1 title/goal = %q/%q", oldest.Title, oldest.Goal)
	}
	if !oldest.StillMember || oldest.MessageCount != 0 || oldest.Kind != "group" {
		t.Fatalf("g1 flags = %+v", oldest)
	}
	if oldest.JoinedAt != 1_779_000_000 {
		t.Fatalf("g1 joinedAt = %d, want seconds", oldest.JoinedAt)
	}

	// matchReasons: bio and groupTaskTitle hits for 合同, weight 2 each.
	assertHasReason(t, a.MatchReasons, matchReason{Field: "bio", Token: "合同", Weight: 2})
	assertHasReason(t, a.MatchReasons, matchReason{Field: "groupTaskTitle", Token: "合同", Weight: 2})

	// B matched only through group-task title; stillMember=false after leave.
	b := candidateByGlobalMetaId(t, body.Data.Candidates, "idq1-bot-b")
	if b.GroupTaskCount != 2 {
		t.Fatalf("B groupTaskCount = %d, want 2", b.GroupTaskCount)
	}
	for _, task := range b.RecentGroupTasks {
		if task.GroupId == "g1:i0" && task.StillMember {
			t.Fatalf("B g1 stillMember = true after leave: %+v", task)
		}
	}
	if len(b.PublishedSkills) != 0 {
		t.Fatalf("B publishedSkills = %v, want []", b.PublishedSkills)
	}
}

func TestSearchExcludeGlobalMetaIdsCaseInsensitive(t *testing.T) {
	f := seedMainFixture(t)

	_, body := f.call(t, `{"query":"合同","excludeGlobalMetaIds":["IDQ1-BOT-A"]}`)
	if body.Code != 0 {
		t.Fatalf("code = %d message=%q", body.Code, body.Message)
	}
	if len(body.Data.Candidates) != 1 || body.Data.Candidates[0].GlobalMetaId != "idq1-bot-b" {
		t.Fatalf("candidates = %+v, want only idq1-bot-b", body.Data.Candidates)
	}
}

func TestSearchOptOutFlags(t *testing.T) {
	f := seedMainFixture(t)

	// onlineOnly=false + hasChatPubkey=false: D (offline, no pubkey) appears,
	// C (offline, no match on 合同) does not.
	_, body := f.call(t, `{"query":"合同","onlineOnly":false,"hasChatPubkey":false}`)
	if body.Code != 0 {
		t.Fatalf("code = %d message=%q", body.Code, body.Message)
	}
	if body.Data.Candidates[0].GlobalMetaId != "idq1-bot-a" {
		t.Fatalf("first candidate = %s, want idq1-bot-a", body.Data.Candidates[0].GlobalMetaId)
	}
	d := candidateByGlobalMetaId(t, body.Data.Candidates, "idq1-bot-d")
	if d.IsOnline || d.LastSeenAgoSeconds != nil {
		t.Fatalf("D presence = online:%v ago:%v, want offline/null", d.IsOnline, d.LastSeenAgoSeconds)
	}
	if d.HasChatPubkey {
		t.Fatalf("D hasChatPubkey = true, want false")
	}
}

func TestSearchPagingWithCursor(t *testing.T) {
	f := seedMainFixture(t)

	_, page1 := f.call(t, `{"query":"合同","limit":1}`)
	if page1.Code != 0 {
		t.Fatalf("page1 code = %d", page1.Code)
	}
	if len(page1.Data.Candidates) != 1 || page1.Data.Candidates[0].GlobalMetaId != "idq1-bot-a" {
		t.Fatalf("page1 candidates = %+v", page1.Data.Candidates)
	}
	if page1.Data.NextCursor == nil {
		t.Fatalf("page1 nextCursor = nil, want opaque cursor")
	}

	payload, _ := json.Marshal(map[string]interface{}{"query": "合同", "limit": 1, "cursor": *page1.Data.NextCursor})
	_, page2 := f.call(t, string(payload))
	if page2.Code != 0 {
		t.Fatalf("page2 code = %d message=%q", page2.Code, page2.Message)
	}
	if len(page2.Data.Candidates) != 1 || page2.Data.Candidates[0].GlobalMetaId != "idq1-bot-b" {
		t.Fatalf("page2 candidates = %+v", page2.Data.Candidates)
	}
	if page2.Data.NextCursor != nil {
		t.Fatalf("page2 nextCursor = %q, want null", *page2.Data.NextCursor)
	}
}

func TestSearchValidationErrors(t *testing.T) {
	f := seedMainFixture(t)

	cases := []struct {
		name string
		body string
	}{
		{"empty query skills roleHint", `{}`},
		{"whitespace query", `{"query":"   "}`},
		{"invalid roleHint", `{"query":"合同","roleHint":"wizardry"}`},
		{"limit above max", `{"query":"合同","limit":51}`},
		{"negative limit", `{"query":"合同","limit":-1}`},
		{"invalid cursor", `{"query":"合同","cursor":"%%%not-base64"}`},
		{"malformed body", `{"query":`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := f.call(t, tc.body)
			if status != http.StatusOK {
				t.Fatalf("HTTP status = %d, want 200", status)
			}
			if body.Code != codeInvalidQuery {
				t.Fatalf("code = %d, want %d (message=%q)", body.Code, codeInvalidQuery, body.Message)
			}
		})
	}
}

func TestSearchPresenceUnavailable(t *testing.T) {
	// No presence readers wired: local nil, global nil.
	f := newSearchFixture(t, nil, nil)
	f.seedProfile(t, profileSeed{
		metaId: "meta-bot-a", global: "idq1-bot-a", address: "addr-bot-a",
		name: "Counsel-bot", bio: "商业合同与合规审查", pubkey: "pk-a",
	})

	// onlineOnly defaults to true → presence_unavailable, empty data, HTTP 200.
	status, body := f.call(t, `{"query":"合同"}`)
	if status != http.StatusOK {
		t.Fatalf("HTTP status = %d, want 200", status)
	}
	if body.Code != codePresenceUnavailable || body.Message != "presence_unavailable" {
		t.Fatalf("code=%d message=%q, want 1002 presence_unavailable", body.Code, body.Message)
	}
	if len(body.Data.Candidates) != 0 {
		t.Fatalf("candidates = %+v, want empty on presence_unavailable", body.Data.Candidates)
	}

	// With onlineOnly=false the same node answers with isOnline=false rows
	// instead of failing.
	_, body = f.call(t, `{"query":"合同","onlineOnly":false}`)
	if body.Code != 0 {
		t.Fatalf("code = %d message=%q", body.Code, body.Message)
	}
	if len(body.Data.Candidates) != 1 {
		t.Fatalf("candidates = %+v, want 1", body.Data.Candidates)
	}
	if body.Data.Candidates[0].IsOnline || body.Data.Candidates[0].LastSeenAgoSeconds != nil {
		t.Fatalf("presence fields = %+v, want offline/null", body.Data.Candidates[0])
	}
}

func TestSearchRoleHintOnlyQuery(t *testing.T) {
	f := seedMainFixture(t)

	_, body := f.call(t, `{"roleHint":"design"}`)
	if body.Code != 0 {
		t.Fatalf("code = %d message=%q", body.Code, body.Message)
	}
	if len(body.Data.Candidates) != 1 || body.Data.Candidates[0].GlobalMetaId != "idq1-bot-b" {
		t.Fatalf("candidates = %+v, want only idq1-bot-b", body.Data.Candidates)
	}
	assertHasReason(t, body.Data.Candidates[0].MatchReasons, matchReason{Field: "roleHint", Token: "design", Weight: 0.5})
}

func TestSearchCJKPartialRecallFromContract(t *testing.T) {
	f := seedMainFixture(t)

	// The contract's recall example: 占卜 塔罗 命运 must recall the bot whose
	// bio only contains 占卜塔罗牌.
	_, body := f.call(t, `{"query":"占卜 塔罗 命运"}`)
	if body.Code != 0 {
		t.Fatalf("code = %d message=%q", body.Code, body.Message)
	}
	if len(body.Data.Candidates) != 1 || body.Data.Candidates[0].GlobalMetaId != "idq1-bot-e" {
		t.Fatalf("candidates = %+v, want only idq1-bot-e", body.Data.Candidates)
	}
	e := body.Data.Candidates[0]
	if e.GroupTaskCount != 0 || len(e.RecentGroupTasks) != 0 {
		t.Fatalf("E history = count %d recent %+v, want empty", e.GroupTaskCount, e.RecentGroupTasks)
	}
}

func TestSearchExactNameBoostOrdering(t *testing.T) {
	f := seedMainFixture(t)

	// "设计狮" is B's exact name; it also hits B's bio 视觉设计与出图 via the
	// 设计 bigram — but the exact-name boost must pin B to the top even when
	// other candidates match fuzzily. A matches nothing here, so instead
	// assert B outranks an exact-vs-fuzzy pair: query "Counsel-bot" hits A
	// exactly and B not at all.
	_, body := f.call(t, `{"query":"Counsel-bot"}`)
	if body.Code != 0 || len(body.Data.Candidates) == 0 {
		t.Fatalf("code=%d candidates=%+v", body.Code, body.Data.Candidates)
	}
	first := body.Data.Candidates[0]
	if first.GlobalMetaId != "idq1-bot-a" {
		t.Fatalf("first = %s, want exact-name idq1-bot-a", first.GlobalMetaId)
	}
	if first.Score < exactNameBoost {
		t.Fatalf("exact-name score = %v, want >= %v", first.Score, exactNameBoost)
	}
}

func TestSearchRecentGroupTasksCapAndOrder(t *testing.T) {
	f := newSearchFixture(t, &fakeLocalPresence{entries: []presence.OnlineEntry{
		{MetaId: "idq1-taskbot", Type: "app", ConnectedAt: fixedNowMs - 5000, LastSeenAt: fixedNowMs - 5000},
	}}, nil)

	f.seedProfile(t, profileSeed{
		metaId: "meta-taskbot", global: "idq1-taskbot", address: "addr-taskbot",
		name: "TaskBot", bio: "群组任务执行者", pubkey: "pk-t",
	})
	for i := 1; i <= 6; i++ {
		f.seedGroupCreate(t, fmt.Sprintf("create-g%d:i0", i), fmt.Sprintf("g%d:i0", i),
			fmt.Sprintf("任务 %d", i), "", "meta-taskbot", "idq1-taskbot", "addr-taskbot",
			1_779_000_000_000+int64(i)*1000)
	}

	_, body := f.call(t, `{"query":"TaskBot"}`)
	if body.Code != 0 || len(body.Data.Candidates) != 1 {
		t.Fatalf("code=%d candidates=%+v", body.Code, body.Data.Candidates)
	}
	bot := body.Data.Candidates[0]
	if bot.GroupTaskCount != 6 {
		t.Fatalf("groupTaskCount = %d, want full count 6", bot.GroupTaskCount)
	}
	if len(bot.RecentGroupTasks) != 5 {
		t.Fatalf("recentGroupTasks len = %d, want cap 5", len(bot.RecentGroupTasks))
	}
	if bot.RecentGroupTasks[0].GroupId != "g6:i0" || bot.RecentGroupTasks[4].GroupId != "g2:i0" {
		t.Fatalf("recent order = [%s ... %s], want newest first g6..g2",
			bot.RecentGroupTasks[0].GroupId, bot.RecentGroupTasks[4].GroupId)
	}
	if bot.RecentGroupTasks[0].JoinedAs != "chair" || bot.RecentGroupTasks[0].JoinPinId != "create-g6:i0" {
		t.Fatalf("g6 row = %+v", bot.RecentGroupTasks[0])
	}
	if bot.RecentGroupTasks[0].JoinedAt != 1_779_000_006 {
		t.Fatalf("g6 joinedAt = %d, want seconds 1779000006", bot.RecentGroupTasks[0].JoinedAt)
	}
}

func TestSearchFederatedPresenceCountsAsAvailable(t *testing.T) {
	// Local reader nil but federation global reader enabled → presence is
	// available and federated entries mark candidates online.
	global := &fakeGlobalPresence{enabled: true, items: []presence.OnlineEntry{
		{MetaId: "idq1-bot-a", Type: "app", ConnectedAt: fixedNowMs - 7000, LastSeenAt: fixedNowMs - 7000},
	}}
	f := newSearchFixture(t, nil, global)
	f.seedProfile(t, profileSeed{
		metaId: "meta-bot-a", global: "idq1-bot-a", address: "addr-bot-a",
		name: "Counsel-bot", bio: "商业合同与合规审查", pubkey: "pk-a",
	})

	_, body := f.call(t, `{"query":"合同"}`)
	if body.Code != 0 {
		t.Fatalf("code = %d message=%q, want 0 (federated presence available)", body.Code, body.Message)
	}
	if len(body.Data.Candidates) != 1 || !body.Data.Candidates[0].IsOnline {
		t.Fatalf("candidates = %+v, want idq1-bot-a online", body.Data.Candidates)
	}
	if ago := body.Data.Candidates[0].LastSeenAgoSeconds; ago == nil || *ago != 7 {
		t.Fatalf("lastSeenAgoSeconds = %v, want 7", ago)
	}
}
