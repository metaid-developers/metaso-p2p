package userinfo

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// metaIDTestInitPin registers a user (path "/") with a creation timestamp.
func metaIDTestInitPin(metaID, address, chain, pinID string, ts int64) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Path:          "/",
		Operation:     "init",
		MetaId:        metaID,
		Address:       address,
		ChainName:     chain,
		Id:            pinID,
		Timestamp:     ts,
		GenesisHeight: 1,
	}
}

func metaIDTestInfoPin(path, metaID, address, chain, body, pinID string, ts int64) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Path:          path,
		Operation:     "create",
		MetaId:        metaID,
		Address:       address,
		ChainName:     chain,
		ContentBody:   []byte(body),
		Id:            pinID,
		Timestamp:     ts,
		GenesisHeight: 1,
	}
}

func mustHandlePin(t *testing.T, agg *Aggregator, pin *aggregator.PinInscription) {
	t.Helper()
	if _, err := agg.HandleBlockPin(pin); err != nil {
		t.Fatalf("HandleBlockPin(%s) failed: %v", pin.Path, err)
	}
}

// seedAliceBobCharlie seeds three searchable users plus one init-only user:
//   - alice (mvc): name Alice, bio 喜欢画画性格开朗, chatpubkey, homepage, llm plain string
//   - bob (btc): name Bob, persona 开朗的链上助手
//   - carol (mvc): name Charlie翻译官, chatskills ["translate","draw"]
//   - dave (mvc): init only, excluded from corpus
func seedMetaIDSearchUsers(t *testing.T, agg *Aggregator) {
	t.Helper()

	mustHandlePin(t, agg, metaIDTestInitPin("alice-id", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", "init-alice:i0", 1768284700))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "alice-id", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", "Alice", "alice-name:i0", 1768284701))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/bio", "alice-id", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", "喜欢画画，性格开朗", "alice-bio:i0", 1768284702))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/chatpubkey", "alice-id", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", "02alicepubkey", "alice-pk:i0", 1768284703))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/homepage", "alice-id", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", `{"uri":"metaapp://alice-home"}`, "alice-home:i0", 1768284704))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/llm", "alice-id", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", "openai", "alice-llm:i0", 1768284705))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/persona", "alice-id", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", `{"role":"邻家女孩"}`, "alice-persona:i0", 1768284706))

	mustHandlePin(t, agg, metaIDTestInitPin("bob-id", "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", "btc", "init-bob:i0", 1768284710))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "bob-id", "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", "btc", "Bob", "bob-name:i0", 1768284711))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/persona", "bob-id", "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", "btc", `{"role":"开朗的链上助手"}`, "bob-persona:i0", 1768284712))

	mustHandlePin(t, agg, metaIDTestInitPin("carol-id", "1BitcoinEaterAddressDontSendf59kuE", "mvc", "init-carol:i0", 1768284720))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "carol-id", "1BitcoinEaterAddressDontSendf59kuE", "mvc", "Charlie翻译官", "carol-name:i0", 1768284721))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/chatskills", "carol-id", "1BitcoinEaterAddressDontSendf59kuE", "mvc", `["translate","draw"]`, "carol-skills:i0", 1768284722))

	mustHandlePin(t, agg, metaIDTestInitPin("dave-id", "1CounterpartyXXXXXXXXXXXXXXXUWLpVr", "mvc", "init-dave:i0", 1768284730))
}

func metaIDListNames(t *testing.T, agg *Aggregator, params MetaIDListParams) []string {
	t.Helper()
	result, err := agg.ListMetaIDs(params)
	if err != nil {
		t.Fatalf("ListMetaIDs(%+v) failed: %v", params, err)
	}
	names := make([]string, 0, len(result.Items))
	for _, item := range result.Items {
		names = append(names, item.Name)
	}
	return names
}

func TestMetaIDSearchDocBuild(t *testing.T) {
	if doc := buildMetaIDSearchDoc(nil); doc != nil {
		t.Fatalf("expected nil doc for nil profile")
	}
	if doc := buildMetaIDSearchDoc(&UserProfile{MetaID: "x"}); doc != nil {
		t.Fatalf("expected nil doc for profile without searchable content")
	}

	doc := buildMetaIDSearchDoc(&UserProfile{
		MetaID:        "alice-id",
		GlobalMetaID:  "idalice",
		Address:       "1Alice",
		ChainName:     "mvc",
		Name:          "Alice Wang",
		Bio:           "Bio text",
		ChatSkills:    `{"allow":["Translate","Draw"]}`,
		LLM:           `{"provider":"OpenAI","model":"gpt-4","name":"Helper"}`,
		Persona:       `{"role":"开朗"}`,
		ChatPublicKey: "02pk",
		Homepage:      `{"uri":"metaapp://x"}`,
	})
	if doc == nil {
		t.Fatalf("expected doc")
	}
	if doc.nameExact != "alicewang" {
		t.Fatalf("nameExact = %q, want alicewang", doc.nameExact)
	}
	for _, want := range []string{"translate", "draw"} {
		if !strings.Contains(doc.skillText, want) {
			t.Fatalf("skillText %q missing %q", doc.skillText, want)
		}
	}
	for _, want := range []string{"bio text", "openai", "gpt-4", "helper", "开朗"} {
		if !strings.Contains(doc.profileText, want) {
			t.Fatalf("profileText missing %q", want)
		}
	}
	if !doc.hasChatPubkey || !doc.hasHomepage {
		t.Fatalf("flags wrong: %+v", doc)
	}

	// Plain-string chatskills is a single skill; invalid JSON yields no skills.
	if skills := parseMetaIDChatSkills("translate"); len(skills) != 1 || skills[0] != "translate" {
		t.Fatalf("plain chatskills = %v", skills)
	}
	if skills := parseMetaIDChatSkills("{bad json"); skills != nil {
		t.Fatalf("invalid chatskills = %v, want nil", skills)
	}
	if skills := parseMetaIDChatSkills(`{"chatSkills":["a","b"]}`); len(skills) != 2 {
		t.Fatalf("object chatskills = %v", skills)
	}

	// Plain-string llm is the provider.
	if llm := parseMetaIDLLM("openai"); llm.Provider != "openai" || llm.Model != "" {
		t.Fatalf("plain llm = %+v", llm)
	}
}

func TestMetaIDListKeywordSearch(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	seedMetaIDSearchUsers(t, agg)

	// Exact name boost: "alice" ranks Alice first even though her bio
	// contributes no extra tier hits.
	names := metaIDListNames(t, agg, MetaIDListParams{Keyword: "alice"})
	if len(names) != 1 || names[0] != "Alice" {
		t.Fatalf("keyword=alice -> %v", names)
	}

	// Persona/bio corpus: 开朗 hits Alice (bio) and Bob (persona).
	names = metaIDListNames(t, agg, MetaIDListParams{Keyword: "开朗"})
	if len(names) != 2 {
		t.Fatalf("keyword=开朗 -> %v, want 2 users", names)
	}
	for _, name := range names {
		if name != "Alice" && name != "Bob" {
			t.Fatalf("keyword=开朗 unexpected hit %q", name)
		}
	}

	// AND semantics: every token must hit.
	if names := metaIDListNames(t, agg, MetaIDListParams{Keyword: "开朗 不存在词"}); len(names) != 0 {
		t.Fatalf("keyword AND semantics -> %v, want empty", names)
	}

	// Skill corpus hit.
	names = metaIDListNames(t, agg, MetaIDListParams{Keyword: "translate"})
	if len(names) != 1 || names[0] != "Charlie翻译官" {
		t.Fatalf("keyword=translate -> %v", names)
	}

	// The init-only user never appears, keyword or not.
	names = metaIDListNames(t, agg, MetaIDListParams{})
	for _, name := range names {
		if name == "" {
			t.Fatalf("unfiltered feed contains empty-corpus user: %v", names)
		}
	}
	if len(names) != 3 {
		t.Fatalf("unfiltered feed -> %v, want 3 searchable users", names)
	}
}

func TestMetaIDListExactNameBeatsTierScore(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()

	// Both users carry the token "alice": one as exact name, one only in bio.
	mustHandlePin(t, agg, metaIDTestInitPin("u1", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", "init-u1:i0", 100))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "u1", "1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa", "mvc", "Alice", "u1-name:i0", 101))
	mustHandlePin(t, agg, metaIDTestInitPin("u2", "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", "mvc", "init-u2:i0", 100))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "u2", "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", "mvc", "Zed", "u2-name:i0", 101))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/bio", "u2", "1BoatSLRHtKNngkdXEeobR76b53LETtpyT", "mvc", "friend of alice", "u2-bio:i0", 200))

	names := metaIDListNames(t, agg, MetaIDListParams{Keyword: "alice"})
	if len(names) != 2 || names[0] != "Alice" {
		t.Fatalf("exact-name boost order -> %v", names)
	}
}

func TestMetaIDListFilters(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	seedMetaIDSearchUsers(t, agg)

	if names := metaIDListNames(t, agg, MetaIDListParams{HasChatPubkey: true}); len(names) != 1 || names[0] != "Alice" {
		t.Fatalf("hasChatPubkey=1 -> %v", names)
	}
	if names := metaIDListNames(t, agg, MetaIDListParams{HasHomepage: true}); len(names) != 1 || names[0] != "Alice" {
		t.Fatalf("hasHomepage=1 -> %v", names)
	}
	if names := metaIDListNames(t, agg, MetaIDListParams{ChainName: "btc"}); len(names) != 1 || names[0] != "Bob" {
		t.Fatalf("chainName=btc -> %v", names)
	}
	if names := metaIDListNames(t, agg, MetaIDListParams{Skill: "draw"}); len(names) != 1 || names[0] != "Charlie翻译官" {
		t.Fatalf("skill=draw -> %v", names)
	}

	// Alice's last revision is 1768284706, Bob's 1768284712, Carol's 1768284722.
	if names := metaIDListNames(t, agg, MetaIDListParams{Since: 1768284720}); len(names) != 1 || names[0] != "Charlie翻译官" {
		t.Fatalf("since -> %v", names)
	}
	if names := metaIDListNames(t, agg, MetaIDListParams{Until: 1768284710}); len(names) != 1 || names[0] != "Alice" {
		t.Fatalf("until -> %v", names)
	}
}

func TestMetaIDListFeedOrderAndTimestamps(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	seedMetaIDSearchUsers(t, agg)

	result, err := agg.ListMetaIDs(MetaIDListParams{})
	if err != nil {
		t.Fatalf("ListMetaIDs failed: %v", err)
	}
	// updatedAt desc: Carol (1768284722) -> Bob (1768284712) -> Alice (1768284706).
	want := []string{"Charlie翻译官", "Bob", "Alice"}
	if len(result.Items) != len(want) {
		t.Fatalf("feed size = %d, want %d", len(result.Items), len(want))
	}
	for i, name := range want {
		if result.Items[i].Name != name {
			t.Fatalf("feed order[%d] = %q, want %q (full: %+v)", i, result.Items[i].Name, name, result.Items)
		}
	}
	if result.Items[2].UpdatedAt != 1768284706 {
		t.Fatalf("alice updatedAt = %d", result.Items[2].UpdatedAt)
	}
	// createdAt comes from the init pin timestamp via the creation record.
	if result.Items[2].CreatedAt != 1768284700 {
		t.Fatalf("alice createdAt = %d, want 1768284700", result.Items[2].CreatedAt)
	}
}

func TestMetaIDListPagination(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	seedMetaIDSearchUsers(t, agg)

	page1, err := agg.ListMetaIDs(MetaIDListParams{Size: 2})
	if err != nil {
		t.Fatalf("page1 failed: %v", err)
	}
	if len(page1.Items) != 2 || !page1.HasMore || page1.NextCursor == "" {
		t.Fatalf("page1 = %+v", page1)
	}
	page2, err := agg.ListMetaIDs(MetaIDListParams{Size: 2, Cursor: page1.NextCursor})
	if err != nil {
		t.Fatalf("page2 failed: %v", err)
	}
	if len(page2.Items) != 1 || page2.HasMore {
		t.Fatalf("page2 = %+v", page2)
	}
	if _, err := agg.ListMetaIDs(MetaIDListParams{Cursor: "@@not-base64@@"}); err == nil || !strings.Contains(err.Error(), "invalid cursor") {
		t.Fatalf("invalid cursor err = %v", err)
	}
}

func TestMetaIDListHTTP(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()
	seedMetaIDSearchUsers(t, agg)

	w := performRequest(t, router, http.MethodGet, "/api/metaid/list?keyword=alice")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var resp struct {
		Code    int `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Items []struct {
				GlobalMetaID  string   `json:"globalMetaId"`
				MetaID        string   `json:"metaId"`
				Address       string   `json:"address"`
				Name          string   `json:"name"`
				ChatSkills    []string `json:"chatSkills"`
				HasChatPubkey bool     `json:"hasChatPubkey"`
				HasHomepage   bool     `json:"hasHomepage"`
				UpdatedAt     int64    `json:"updatedAt"`
			} `json:"items"`
			HasMore bool `json:"hasMore"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v (body %s)", err, w.Body.String())
	}
	if resp.Code != 0 {
		t.Fatalf("code = %d, body %s", resp.Code, w.Body.String())
	}
	if len(resp.Data.Items) != 1 {
		t.Fatalf("items = %+v", resp.Data.Items)
	}
	item := resp.Data.Items[0]
	if item.Name != "Alice" || item.MetaID != "alice-id" || item.GlobalMetaID == "" || item.Address == "" {
		t.Fatalf("item identity = %+v", item)
	}
	if !item.HasChatPubkey || !item.HasHomepage {
		t.Fatalf("item flags = %+v", item)
	}

	for _, query := range []string{"size=abc", "size=-1", "since=xyz", "until=-5", "hasChatPubkey=maybe", "cursor=@@bad@@"} {
		w := performRequest(t, router, http.MethodGet, "/api/metaid/list?"+query)
		var errResp struct {
			Code int `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("%s: unmarshal: %v", query, err)
		}
		if errResp.Code != 40000 {
			t.Fatalf("%s: code = %d, want 40000 (body %s)", query, errResp.Code, w.Body.String())
		}
	}
}

func TestMetaIDDetailHTTP(t *testing.T) {
	agg, store, router := setupTestAggregator(t)
	defer store.Close()
	seedMetaIDSearchUsers(t, agg)

	type detailResponse struct {
		Code int `json:"code"`
		Data struct {
			GlobalMetaID  string            `json:"globalMetaId"`
			MetaID        string            `json:"metaId"`
			Name          string            `json:"name"`
			HasChatPubkey bool              `json:"hasChatPubkey"`
			ChatPublicKey string            `json:"chatPubkey"`
			CreatedAt     int64             `json:"createdAt"`
			UpdatedAt     int64             `json:"updatedAt"`
			Persona       map[string]any    `json:"persona"`
			Homepage      map[string]any    `json:"homepage"`
			LLM           *MetaIDLLMInfo    `json:"llm"`
			FieldPins     map[string]string `json:"fieldPins"`
		} `json:"data"`
	}

	getDetail := func(identity string) detailResponse {
		t.Helper()
		w := performRequest(t, router, http.MethodGet, "/api/metaid/detail/"+identity)
		var resp detailResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("detail(%s) unmarshal: %v (body %s)", identity, err, w.Body.String())
		}
		return resp
	}

	resp := getDetail("alice-id")
	if resp.Code != 0 {
		t.Fatalf("detail code = %d", resp.Code)
	}
	if resp.Data.Name != "Alice" || resp.Data.MetaID != "alice-id" {
		t.Fatalf("detail = %+v", resp.Data)
	}
	if resp.Data.GlobalMetaID == "" {
		t.Fatalf("detail missing globalMetaId: %+v", resp.Data)
	}
	if !resp.Data.HasChatPubkey || resp.Data.ChatPublicKey != "02alicepubkey" {
		t.Fatalf("detail chatpubkey = %+v", resp.Data)
	}
	if resp.Data.Persona["role"] != "邻家女孩" {
		t.Fatalf("detail persona = %+v", resp.Data.Persona)
	}
	if resp.Data.Homepage["uri"] != "metaapp://alice-home" {
		t.Fatalf("detail homepage = %+v", resp.Data.Homepage)
	}
	if resp.Data.LLM == nil || resp.Data.LLM.Provider != "openai" {
		t.Fatalf("detail llm = %+v", resp.Data.LLM)
	}
	if resp.Data.FieldPins["name"] != "alice-name:i0" || resp.Data.FieldPins["bio"] != "alice-bio:i0" {
		t.Fatalf("detail fieldPins = %+v", resp.Data.FieldPins)
	}
	if resp.Data.CreatedAt != 1768284700 || resp.Data.UpdatedAt != 1768284706 {
		t.Fatalf("detail timestamps = %d/%d", resp.Data.CreatedAt, resp.Data.UpdatedAt)
	}

	// The other identity forms resolve to the same profile.
	if byGmid := getDetail(resp.Data.GlobalMetaID); byGmid.Data.MetaID != "alice-id" {
		t.Fatalf("detail by globalMetaId = %+v", byGmid.Data)
	}
	if byAddr := getDetail("1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa"); byAddr.Data.MetaID != "alice-id" {
		t.Fatalf("detail by address = %+v", byAddr.Data)
	}

	// Unknown identity -> 40400.
	w := performRequest(t, router, http.MethodGet, "/api/metaid/detail/no-such-user")
	var errResp struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("404 unmarshal: %v", err)
	}
	if errResp.Code != 40400 {
		t.Fatalf("unknown identity code = %d, want 40400", errResp.Code)
	}
}
