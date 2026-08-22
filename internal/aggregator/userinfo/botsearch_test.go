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
