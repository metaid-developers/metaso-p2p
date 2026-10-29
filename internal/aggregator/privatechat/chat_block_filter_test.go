package privatechat

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/block"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/notify"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// Wiring fixtures: chats are keyed by legacy metaids (as real clients do),
// chain blocks by canonical GlobalMetaIDs; the profile lookups bridge the
// two forms, mirroring the production userinfo adapters.

const (
	wiringGidAlice = "idq1sv98gg8x84mzgnlhe0guyj8ffs2yvvjenmhn0j"
	wiringGidBob   = "idp1qyq8s4zcvwyx9qvuqw8zrx3qcxqwqyujnkdxjm4tc"
	wiringGidCarol = "idq1carol0000000000000000000000000000000000"

	wiringMetaAlice = "alice-meta"
	wiringMetaBob   = "bob-meta"
	wiringMetaCarol = "carol-meta"
)

type wiringChatLookup struct{ byMetaId map[string]*IdentityProfile }

func (l wiringChatLookup) LookupByMetaId(id string) (*IdentityProfile, error) {
	return l.byMetaId[id], nil
}
func (l wiringChatLookup) LookupByGlobalMetaId(g string) (*IdentityProfile, error) {
	for _, p := range l.byMetaId {
		if p.GlobalMetaId == g {
			return p, nil
		}
	}
	return nil, nil
}
func (l wiringChatLookup) LookupByAddress(a string) (*IdentityProfile, error) {
	for _, p := range l.byMetaId {
		if p.Address == a {
			return p, nil
		}
	}
	return nil, nil
}

type wiringBlockLookup struct{ byMetaId map[string]*block.IdentityRef }

func (l wiringBlockLookup) LookupByMetaId(id string) (*block.IdentityRef, error) {
	return l.byMetaId[id], nil
}
func (l wiringBlockLookup) LookupByGlobalMetaId(g string) (*block.IdentityRef, error) {
	for _, p := range l.byMetaId {
		if p.GlobalMetaId == g {
			return p, nil
		}
	}
	return nil, nil
}
func (l wiringBlockLookup) LookupByAddress(a string) (*block.IdentityRef, error) {
	for _, p := range l.byMetaId {
		if p.Address == a {
			return p, nil
		}
	}
	return nil, nil
}

func wiringMsgPin(t *testing.T, id, fromMeta, toMeta, fromGlobal, content string, ts int64) *aggregator.PinInscription {
	t.Helper()
	return &aggregator.PinInscription{
		Id:            id,
		Path:          "/private/chat/simplemsg",
		Operation:     "create",
		CreateAddress: "1" + fromMeta,
		CreateMetaId:  fromMeta,
		GlobalMetaId:  fromGlobal,
		ChainName:     "mvc",
		Timestamp:     ts,
		ContentBody: mustMarshal(t, SimpleMsg{
			From:        fromMeta,
			To:          toMeta,
			Content:     content,
			ContentType: "text/plain",
			Encrypt:     "none",
		}),
	}
}

func wiringBlockPin(id, from, to string, state int64, ts int64) *aggregator.PinInscription {
	return &aggregator.PinInscription{
		Id:           id,
		Path:         "/protocols/simpleblock",
		Operation:    "create",
		GlobalMetaId: from,
		ChainName:    "mvc",
		Timestamp:    ts,
		ContentBody:  []byte(fmt.Sprintf(`{"to":%q,"blockState":%d}`, to, state)),
	}
}

// wiringFixture composes the production wiring over one store: privatechat
// reads consult notify.NewChatBlockFilter(notify, block) — the same objects
// main.go injects.
func wiringFixture(t *testing.T) (*Aggregator, *block.Aggregator, *gin.Engine) {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	provider := cache.New(store)

	notifyAgg := &notify.Aggregator{}
	if err := notifyAgg.Init(store, provider); err != nil {
		t.Fatalf("notify Init: %v", err)
	}
	blockAgg := &block.Aggregator{}
	if err := blockAgg.Init(store, provider); err != nil {
		t.Fatalf("block Init: %v", err)
	}
	blockAgg.SetProfileLookup(wiringBlockLookup{byMetaId: map[string]*block.IdentityRef{
		wiringMetaAlice: {MetaId: wiringMetaAlice, GlobalMetaId: wiringGidAlice, Address: "1Alice"},
		wiringMetaBob:   {MetaId: wiringMetaBob, GlobalMetaId: wiringGidBob, Address: "1Bob"},
		wiringMetaCarol: {MetaId: wiringMetaCarol, GlobalMetaId: wiringGidCarol, Address: "1Carol"},
	}})

	pcAgg := &Aggregator{}
	if err := pcAgg.Init(store, provider); err != nil {
		t.Fatalf("privatechat Init: %v", err)
	}
	pcAgg.SetProfileLookup(wiringChatLookup{byMetaId: map[string]*IdentityProfile{
		wiringMetaAlice: {MetaId: wiringMetaAlice, GlobalMetaId: wiringGidAlice, Address: "1Alice"},
		wiringMetaBob:   {MetaId: wiringMetaBob, GlobalMetaId: wiringGidBob, Address: "1Bob"},
		wiringMetaCarol: {MetaId: wiringMetaCarol, GlobalMetaId: wiringGidCarol, Address: "1Carol"},
	}})
	pcAgg.SetChatBlockFilter(notify.NewChatBlockFilter(notifyAgg, blockAgg))

	gin.SetMode(gin.TestMode)
	router := gin.New()
	pcAgg.RegisterRoutes(router.Group("/api"))
	return pcAgg, blockAgg, router
}

func homePartners(homes []*PrivateChatHome) []string {
	out := make([]string, 0, len(homes))
	for _, home := range homes {
		out = append(out, home.MetaId)
	}
	return out
}

func chatListByIndexHTTP(t *testing.T, router *gin.Engine, viewer, peer string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		"/api/private-chat/messages/by-index?metaId="+viewer+"&otherMetaId="+peer+"&startIndex=0&size=20", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("by-index HTTP status = %d; body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

// TestChatBlockFilterWiringFiltersChatSurfaces proves the composed wiring
// end to end: a chain block pin (either direction, spec §4.6 union) empties
// the message lists, drops the home and the path, keeps unrelated
// conversations intact, and a blockState = -1 pin restores everything.
// Both storage branches (legacy scan and read model incl. the raw-JSON hot
// path) are exercised.
func TestChatBlockFilterWiringFiltersChatSurfaces(t *testing.T) {
	pcAgg, blockAgg, router := wiringFixture(t)

	// Conversations: alice↔bob (2 messages), bob↔carol (1 message).
	for _, pin := range []*aggregator.PinInscription{
		wiringMsgPin(t, "wmsg-1:i0", wiringMetaAlice, wiringMetaBob, wiringGidAlice, "hi bob", 1000),
		wiringMsgPin(t, "wmsg-2:i0", wiringMetaBob, wiringMetaAlice, wiringGidBob, "hi alice", 1001),
		wiringMsgPin(t, "wmsg-3:i0", wiringMetaCarol, wiringMetaBob, wiringGidCarol, "hi bob", 1002),
	} {
		if _, err := pcAgg.HandleBlockPin(pin); err != nil {
			t.Fatalf("message ingest %s: %v", pin.Id, err)
		}
	}

	assertBaseline := func(phase string) {
		t.Helper()
		list, err := pcAgg.GetPrivateChatList(wiringMetaAlice, wiringMetaBob, "", 20, 0)
		if err != nil {
			t.Fatalf("%s GetPrivateChatList: %v", phase, err)
		}
		if list.Total != 2 || len(list.List) != 2 {
			t.Fatalf("%s message list = total %d len %d; want 2/2", phase, list.Total, len(list.List))
		}
		byIndex, err := pcAgg.GetPrivateChatListByIndex(wiringMetaAlice, wiringMetaBob, 0, 20)
		if err != nil {
			t.Fatalf("%s GetPrivateChatListByIndex: %v", phase, err)
		}
		if byIndex.Total != 2 {
			t.Fatalf("%s by-index total = %d; want 2", phase, byIndex.Total)
		}
		homes, err := pcAgg.GetPrivateChatHomes(wiringMetaAlice)
		if err != nil {
			t.Fatalf("%s homes(alice): %v", phase, err)
		}
		if len(homes) != 1 || homes[0].MetaId != wiringMetaBob {
			t.Fatalf("%s homes(alice) = %v; want exactly bob", phase, homePartners(homes))
		}
		paths, err := pcAgg.GetPrivateGroupPaths(wiringMetaAlice)
		if err != nil {
			t.Fatalf("%s paths(alice): %v", phase, err)
		}
		if len(paths) != 1 {
			t.Fatalf("%s paths(alice) len = %d; want 1", phase, len(paths))
		}
	}

	assertBlocked := func(phase string) {
		t.Helper()
		list, err := pcAgg.GetPrivateChatList(wiringMetaAlice, wiringMetaBob, "", 20, 0)
		if err != nil {
			t.Fatalf("%s GetPrivateChatList: %v", phase, err)
		}
		if list.Total != 0 || len(list.List) != 0 {
			t.Fatalf("%s message list = total %d len %d; want empty", phase, list.Total, len(list.List))
		}
		byIndex, err := pcAgg.GetPrivateChatListByIndex(wiringMetaAlice, wiringMetaBob, 0, 20)
		if err != nil {
			t.Fatalf("%s GetPrivateChatListByIndex: %v", phase, err)
		}
		if byIndex.Total != 0 || len(byIndex.List) != 0 {
			t.Fatalf("%s by-index = total %d len %d; want empty", phase, byIndex.Total, len(byIndex.List))
		}
		homes, err := pcAgg.GetPrivateChatHomes(wiringMetaAlice)
		if err != nil {
			t.Fatalf("%s homes(alice): %v", phase, err)
		}
		if len(homes) != 0 {
			t.Fatalf("%s homes(alice) = %v; want empty", phase, homePartners(homes))
		}
		paths, err := pcAgg.GetPrivateGroupPaths(wiringMetaAlice)
		if err != nil {
			t.Fatalf("%s paths(alice): %v", phase, err)
		}
		if len(paths) != 0 {
			t.Fatalf("%s paths(alice) len = %d; want 0", phase, len(paths))
		}
		// The blocked conversation is invisible, but bob's unrelated chat
		// with carol must survive (scoping).
		homesBob, err := pcAgg.GetPrivateChatHomes(wiringMetaBob)
		if err != nil {
			t.Fatalf("%s homes(bob): %v", phase, err)
		}
		partners := homePartners(homesBob)
		if len(partners) != 1 || partners[0] != wiringMetaCarol {
			t.Fatalf("%s homes(bob) = %v; want exactly carol", phase, partners)
		}
	}

	// --- legacy storage branch ---
	assertBaseline("legacy baseline")

	if _, err := blockAgg.HandleBlockPin(wiringBlockPin("wblk-1:i0", wiringGidAlice, wiringGidBob, 1, 2000)); err != nil {
		t.Fatalf("block ingest: %v", err)
	}
	assertBlocked("legacy blocked")

	if _, err := blockAgg.HandleBlockPin(wiringBlockPin("wblk-1-off:i0", wiringGidAlice, wiringGidBob, -1, 2001)); err != nil {
		t.Fatalf("unblock ingest: %v", err)
	}
	assertBaseline("legacy restored")

	// --- read-model branch (incl. raw-JSON hot path) ---
	if _, err := pcAgg.BackfillPrivateChatReadModel(context.Background()); err != nil {
		t.Fatalf("read model backfill: %v", err)
	}
	assertBaseline("read-model baseline")
	if body := chatListByIndexHTTP(t, router, wiringMetaAlice, wiringMetaBob); !strings.Contains(body, `"total":2`) {
		t.Fatalf("raw-JSON baseline body lacks total 2: %s", body)
	}

	// Reverse direction through the union semantics: bob blocks alice.
	if _, err := blockAgg.HandleBlockPin(wiringBlockPin("wblk-2:i0", wiringGidBob, wiringGidAlice, 1, 2002)); err != nil {
		t.Fatalf("reverse block ingest: %v", err)
	}
	assertBlocked("read-model blocked")
	if body := chatListByIndexHTTP(t, router, wiringMetaAlice, wiringMetaBob); !strings.Contains(body, `"total":0`) {
		t.Fatalf("raw-JSON blocked body lacks total 0: %s", body)
	}

	if _, err := blockAgg.HandleBlockPin(wiringBlockPin("wblk-2-off:i0", wiringGidBob, wiringGidAlice, -1, 2003)); err != nil {
		t.Fatalf("reverse unblock ingest: %v", err)
	}
	assertBaseline("read-model restored")
	if body := chatListByIndexHTTP(t, router, wiringMetaAlice, wiringMetaBob); !strings.Contains(body, `"total":2`) {
		t.Fatalf("raw-JSON restored body lacks total 2: %s", body)
	}
}
