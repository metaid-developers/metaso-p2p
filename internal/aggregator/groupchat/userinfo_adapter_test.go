package groupchat

import (
	"encoding/json"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
)

type indexedProfileLookup struct {
	profile *ProfileSnapshot
	calls   []string
}

func (l *indexedProfileLookup) LookupLocalByIdentity(identity string) (*ProfileSnapshot, error) {
	l.calls = append(l.calls, identity)
	return l.profile, nil
}

func TestLookupPrivateUserInfoUsesIndexedProfileLookup(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	lookup := &indexedProfileLookup{profile: &ProfileSnapshot{
		MetaId:        "peer-metaid",
		GlobalMetaId:  "idq1peer",
		Address:       "1PeerAddress",
		ChatPublicKey: "peer-chat-key",
	}}
	agg.SetProfileLookup(lookup)

	got := agg.lookupPrivateUserInfo("idq1peer", "1PeerAddress")
	if got != lookup.profile {
		t.Fatalf("lookup result = %#v, want indexed profile", got)
	}
	if len(lookup.calls) != 1 || lookup.calls[0] != "idq1peer" {
		t.Fatalf("lookup calls = %#v", lookup.calls)
	}
}

func TestLookupPrivateUserInfoDoesNotFallbackToProfileScan(t *testing.T) {
	agg, store, _ := setupTestAggregator(t)
	defer store.Close()
	raw := []byte(`{"metaid":"stored-under-another-key","globalMetaId":"idq1scan-target","chatpubkey":"key"}`)
	if err := store.Set("userinfo", []byte("profile:unrelated-key"), raw); err != nil {
		t.Fatal(err)
	}
	if got := agg.lookupPrivateUserInfo("idq1scan-target"); got != nil {
		t.Fatalf("lookup unexpectedly scanned profiles: %#v", got)
	}
}

func TestGroupChatProfileSnapshotPreservesChatPublicKeyCompatibility(t *testing.T) {
	snapshot := groupChatProfileFromUserInfo(&userinfo.UserProfile{
		MetaID:          "peer-metaid",
		GlobalMetaID:    "idq1peer",
		Address:         "1PeerAddress",
		ChatPublicKey:   "peer-chat-key",
		ChatPublicKeyId: "peer-chat-key-pin",
	})
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"chatpubkey", "chatPublicKey", "chatpubkeyId", "chatPublicKeyId"} {
		if decoded[key] == "" || decoded[key] == nil {
			t.Fatalf("missing compatibility field %q in %s", key, raw)
		}
	}
}
