package userinfo

import (
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

func setupBotSearchTestAggregator(t *testing.T) *Aggregator {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	agg := &Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		t.Fatalf("init aggregator: %v", err)
	}
	return agg
}

func findBotSearchProfile(profiles []BotSearchProfile, metaID string) *BotSearchProfile {
	for i := range profiles {
		if profiles[i].MetaID == metaID {
			return &profiles[i]
		}
	}
	return nil
}

// IDBots Bot edit writes a single /info/persona JSON {"role","soul","goal"}
// and no separate /info/role|goal pins; BotSearchProfiles must surface the
// persona role/goal so they reach the candidate payload and scoring.
func TestBotSearchProfilesPersonaRoleGoalFallback(t *testing.T) {
	agg := setupBotSearchTestAggregator(t)

	mustHandlePin(t, agg, metaIDTestInitPin("counsel-id", "1CounselAddress", "mvc", "init-counsel:i0", 1768284800))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "counsel-id", "1CounselAddress", "mvc", "Counsel-bot", "counsel-name:i0", 1768284801))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/bio", "counsel-id", "1CounselAddress", "mvc", "商业合同与合规审查", "counsel-bio:i0", 1768284802))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/persona", "counsel-id", "1CounselAddress", "mvc", `{"role":"法律顾问","soul":"严谨","goal":"让每份合同无漏洞"}`, "counsel-persona:i0", 1768284803))

	profiles := agg.BotSearchProfiles()
	profile := findBotSearchProfile(profiles, "counsel-id")
	if profile == nil {
		t.Fatalf("counsel-id missing from snapshot: %+v", profiles)
	}
	if profile.Role != "法律顾问" {
		t.Fatalf("role = %q, want persona fallback 法律顾问", profile.Role)
	}
	if profile.Goal != "让每份合同无漏洞" {
		t.Fatalf("goal = %q, want persona fallback", profile.Goal)
	}
}

// A dedicated /info/role|goal pin wins over the persona JSON when both exist.
func TestBotSearchProfilesExplicitRoleGoalWinOverPersona(t *testing.T) {
	agg := setupBotSearchTestAggregator(t)

	mustHandlePin(t, agg, metaIDTestInitPin("duo-id", "1DuoAddress", "mvc", "init-duo:i0", 1768284810))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "duo-id", "1DuoAddress", "mvc", "Duo", "duo-name:i0", 1768284811))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/role", "duo-id", "1DuoAddress", "mvc", "独立角色", "duo-role:i0", 1768284812))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/persona", "duo-id", "1DuoAddress", "mvc", `{"role":"persona角色","goal":"persona目标"}`, "duo-persona:i0", 1768284813))

	profile := findBotSearchProfile(agg.BotSearchProfiles(), "duo-id")
	if profile == nil {
		t.Fatal("duo-id missing from snapshot")
	}
	if profile.Role != "独立角色" {
		t.Fatalf("role = %q, want explicit /info/role 独立角色", profile.Role)
	}
	if profile.Goal != "persona目标" {
		t.Fatalf("goal = %q, want persona fallback persona目标", profile.Goal)
	}
}

func TestPersonaRoleGoalInvalidJSON(t *testing.T) {
	for _, raw := range []string{"", "  ", "not-json", `{"role":1}`, `["法律顾问"]`} {
		if role, goal := personaRoleGoal(raw); role != "" || goal != "" {
			t.Fatalf("personaRoleGoal(%q) = (%q, %q), want empty", raw, role, goal)
		}
	}
}

// Legacy pre-v3 clients wrote the whole profile as one /info/bio JSON object
// ({"role","soul","goal","background","allowChatSkills","llm"}). With no v3
// persona/chatSkills pins present, the snapshot must unpack it: bio ←
// background, role/goal ← the object, chatSkills ← allowChatSkills.
func TestBotSearchProfilesLegacyBioBlobFallback(t *testing.T) {
	agg := setupBotSearchTestAggregator(t)

	mustHandlePin(t, agg, metaIDTestInitPin("legacy-id", "1LegacyAddress", "mvc", "init-legacy:i0", 1768284820))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "legacy-id", "1LegacyAddress", "mvc", "纳瓦尔_AI", "legacy-name:i0", 1768284821))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/bio", "legacy-id", "1LegacyAddress", "mvc",
		`{"role":"哲学家-投资人","soul":"格言密度高","goal":"帮用户做长期选择","background":"对外简介：谈杠杆与复利","allowChatSkills":["naval-perspective","metabot-post-buzz"],"llm":"openai"}`,
		"legacy-bio:i0", 1768284822))

	profile := findBotSearchProfile(agg.BotSearchProfiles(), "legacy-id")
	if profile == nil {
		t.Fatal("legacy-id missing from snapshot")
	}
	if profile.Bio != "对外简介：谈杠杆与复利" {
		t.Fatalf("bio = %q, want background field, not the raw JSON", profile.Bio)
	}
	if profile.Role != "哲学家-投资人" || profile.Goal != "帮用户做长期选择" {
		t.Fatalf("role/goal = %q/%q, want legacy blob values", profile.Role, profile.Goal)
	}
	if len(profile.ChatSkills) != 2 || profile.ChatSkills[0] != "naval-perspective" || profile.ChatSkills[1] != "metabot-post-buzz" {
		t.Fatalf("chatSkills = %v, want allowChatSkills", profile.ChatSkills)
	}
}

// The snake_case allow_chat_skills variant is honoured, and the plain bio
// field is used when background is absent.
func TestBotSearchProfilesLegacyBioBlobSnakeSkills(t *testing.T) {
	agg := setupBotSearchTestAggregator(t)

	mustHandlePin(t, agg, metaIDTestInitPin("snake-id", "1SnakeAddress", "mvc", "init-snake:i0", 1768284830))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "snake-id", "1SnakeAddress", "mvc", "Snake-bot", "snake-name:i0", 1768284831))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/bio", "snake-id", "1SnakeAddress", "mvc",
		`{"role":"翻译官","bio":"纯文本 bio 字段","allow_chat_skills":["translate"]}`,
		"snake-bio:i0", 1768284832))

	profile := findBotSearchProfile(agg.BotSearchProfiles(), "snake-id")
	if profile == nil {
		t.Fatal("snake-id missing from snapshot")
	}
	if profile.Bio != "纯文本 bio 字段" {
		t.Fatalf("bio = %q, want the bio field", profile.Bio)
	}
	if profile.Role != "翻译官" {
		t.Fatalf("role = %q, want 翻译官", profile.Role)
	}
	if len(profile.ChatSkills) != 1 || profile.ChatSkills[0] != "translate" {
		t.Fatalf("chatSkills = %v, want [translate]", profile.ChatSkills)
	}
}

// A v3 /info/chatSkills pin disables the legacy fallback entirely: the bio
// JSON stays as-is and the pin's skills win.
func TestBotSearchProfilesLegacyBioBlobDisabledByChatSkillsPin(t *testing.T) {
	agg := setupBotSearchTestAggregator(t)

	mustHandlePin(t, agg, metaIDTestInitPin("mixed-id", "1MixedAddress", "mvc", "init-mixed:i0", 1768284840))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "mixed-id", "1MixedAddress", "mvc", "Mixed-bot", "mixed-name:i0", 1768284841))
	rawBio := `{"role":"旧角色","background":"旧简介","allowChatSkills":["old-skill"]}`
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/bio", "mixed-id", "1MixedAddress", "mvc", rawBio, "mixed-bio:i0", 1768284842))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/chatskills", "mixed-id", "1MixedAddress", "mvc", `{"allowPrivateChatSkills":["new-skill"],"allowGroupChatSkills":[]}`, "mixed-skills:i0", 1768284843))

	profile := findBotSearchProfile(agg.BotSearchProfiles(), "mixed-id")
	if profile == nil {
		t.Fatal("mixed-id missing from snapshot")
	}
	if profile.Bio != rawBio {
		t.Fatalf("bio = %q, want raw JSON untouched when chatSkills pin exists", profile.Bio)
	}
	if len(profile.ChatSkills) != 1 || profile.ChatSkills[0] != "new-skill" {
		t.Fatalf("chatSkills = %v, want [new-skill] from the pin", profile.ChatSkills)
	}
}

// A dedicated /info/role pin wins over the legacy blob's role; the blob
// still fills the goal the pin never wrote.
func TestBotSearchProfilesLegacyBioBlobExplicitRoleWins(t *testing.T) {
	agg := setupBotSearchTestAggregator(t)

	mustHandlePin(t, agg, metaIDTestInitPin("rolepin-id", "1RolePinAddress", "mvc", "init-rolepin:i0", 1768284850))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "rolepin-id", "1RolePinAddress", "mvc", "RolePin-bot", "rolepin-name:i0", 1768284851))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/bio", "rolepin-id", "1RolePinAddress", "mvc",
		`{"role":"blob角色","goal":"blob目标","background":"blob简介"}`,
		"rolepin-bio:i0", 1768284852))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/role", "rolepin-id", "1RolePinAddress", "mvc", "pin角色", "rolepin-role:i0", 1768284853))

	profile := findBotSearchProfile(agg.BotSearchProfiles(), "rolepin-id")
	if profile == nil {
		t.Fatal("rolepin-id missing from snapshot")
	}
	if profile.Role != "pin角色" {
		t.Fatalf("role = %q, want explicit /info/role pin角色", profile.Role)
	}
	if profile.Goal != "blob目标" {
		t.Fatalf("goal = %q, want blob fallback blob目标", profile.Goal)
	}
	if profile.Bio != "blob简介" {
		t.Fatalf("bio = %q, want blob background", profile.Bio)
	}
}

// A bio that parses as JSON but carries no known profile keys is not a
// legacy blob and must pass through untouched.
func TestBotSearchProfilesArbitraryJSONBioUntouched(t *testing.T) {
	agg := setupBotSearchTestAggregator(t)

	mustHandlePin(t, agg, metaIDTestInitPin("jsonbio-id", "1JSONBioAddress", "mvc", "init-jsonbio:i0", 1768284860))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/name", "jsonbio-id", "1JSONBioAddress", "mvc", "JSONBio-bot", "jsonbio-name:i0", 1768284861))
	mustHandlePin(t, agg, metaIDTestInfoPin("/info/bio", "jsonbio-id", "1JSONBioAddress", "mvc", `{"mood":"happy"}`, "jsonbio-bio:i0", 1768284862))

	profile := findBotSearchProfile(agg.BotSearchProfiles(), "jsonbio-id")
	if profile == nil {
		t.Fatal("jsonbio-id missing from snapshot")
	}
	if profile.Bio != `{"mood":"happy"}` {
		t.Fatalf("bio = %q, want raw value untouched", profile.Bio)
	}
	if profile.Role != "" || len(profile.ChatSkills) != 0 {
		t.Fatalf("role/chatSkills = %q/%v, want empty", profile.Role, profile.ChatSkills)
	}
}
