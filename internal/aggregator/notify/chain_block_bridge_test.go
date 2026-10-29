package notify

import (
	"strings"
	"testing"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/block"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	idUAlice = "idq1sv98gg8x84mzgnlhe0guyj8ffs2yvvjenmhn0j"
	idUBob   = "idp1qyq8s4zcvwyx9qvuqw8zrx3qcxqwqyujnkdxjm4tc"
	hexAlice = "meta-alice-hex"
)

func newBridgeFixture(t *testing.T) *Aggregator {
	t.Helper()
	store := storage.NewPebbleStore(t.TempDir())
	t.Cleanup(func() { _ = store.Close() })
	cacheProvider := cache.New(store)
	agg := &Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return agg
}

func TestApplyChainBlockWritesBothAliasKeys(t *testing.T) {
	agg := newBridgeFixture(t)

	// Canonical id first, legacy hex alias second (block aggregator passes
	// every known alias form).
	if err := agg.ApplyChainBlock([]string{idUAlice, hexAlice}, []string{idUBob}, "blk-1:i0", 1000); err != nil {
		t.Fatalf("ApplyChainBlock: %v", err)
	}

	for _, alias := range []string{idUAlice, hexAlice} {
		blocked, err := agg.getBlockedChats(alias)
		if err != nil {
			t.Fatalf("getBlockedChats(%s): %v", alias, err)
		}
		if blocked == nil || len(blocked.BlockedChats) != 1 {
			t.Fatalf("alias %s blocked = %+v; want exactly the bridged entry", alias, blocked)
		}
		entry := blocked.BlockedChats[0]
		if entry.ChatID != idUBob || entry.ChatType != "private" {
			t.Fatalf("entry = %+v; want private chat with idUBob", entry)
		}
		if !strings.HasPrefix(entry.Reason, "on-chain:simpleblock:blk-1:i0") {
			t.Fatalf("reason = %q; want chain provenance", entry.Reason)
		}
	}
}

func TestRemoveChainBlockKeepsOffChainEntries(t *testing.T) {
	agg := newBridgeFixture(t)

	// Off-chain session entry added by the HTTP API path (no chain pin).
	offChain := &UserBlockedChats{UserID: idUAlice, BlockedChats: []BlockedChat{
		{ChatID: idUBob, ChatType: "private", MetaID: idUAlice, Reason: "user prefers quiet"},
	}}
	if err := agg.saveBlockedChats(offChain); err != nil {
		t.Fatalf("seed off-chain entry: %v", err)
	}

	if err := agg.ApplyChainBlock([]string{idUAlice}, []string{idUBob}, "blk-1:i0", 1000); err != nil {
		t.Fatalf("ApplyChainBlock: %v", err)
	}
	blocked, _ := agg.getBlockedChats(idUAlice)
	if len(blocked.BlockedChats) != 2 {
		t.Fatalf("blocked = %+v; want off-chain + chain entries", blocked)
	}

	// Chain unblock removes only the bridged entry (each source keeps its
	// own unblock path, spec §4.6).
	if err := agg.RemoveChainBlock([]string{idUAlice}, []string{idUBob}); err != nil {
		t.Fatalf("RemoveChainBlock: %v", err)
	}
	blocked, _ = agg.getBlockedChats(idUAlice)
	if len(blocked.BlockedChats) != 1 {
		t.Fatalf("blocked after unblock = %+v; want only the off-chain entry", blocked)
	}
	if blocked.BlockedChats[0].Reason != "user prefers quiet" {
		t.Fatalf("surviving entry = %+v; want the off-chain one", blocked.BlockedChats[0])
	}

	// Re-applying the chain block works again (a source re-blocks
	// independently).
	if err := agg.ApplyChainBlock([]string{idUAlice}, []string{idUBob}, "blk-2:i0", 2000); err != nil {
		t.Fatalf("re-apply: %v", err)
	}
	blocked, _ = agg.getBlockedChats(idUAlice)
	if len(blocked.BlockedChats) != 2 {
		t.Fatalf("blocked after re-block = %+v; want 2", blocked)
	}
}

func TestApplyChainBlockReplacesPreviousBridgeEntry(t *testing.T) {
	agg := newBridgeFixture(t)

	if err := agg.ApplyChainBlock([]string{idUAlice}, []string{idUBob}, "blk-1:i0", 1000); err != nil {
		t.Fatalf("apply 1: %v", err)
	}
	// Re-affirm block with a newer pin: the bridge entry reflects the
	// currently active chain pin instead of stacking duplicates.
	if err := agg.ApplyChainBlock([]string{idUAlice}, []string{idUBob}, "blk-3:i0", 3000); err != nil {
		t.Fatalf("apply 3: %v", err)
	}
	blocked, _ := agg.getBlockedChats(idUAlice)
	if len(blocked.BlockedChats) != 1 {
		t.Fatalf("blocked = %+v; want single replaced entry", blocked)
	}
	if !strings.HasSuffix(blocked.BlockedChats[0].Reason, "blk-3:i0") {
		t.Fatalf("reason = %q; want newest pin", blocked.BlockedChats[0].Reason)
	}
}

// fakeChainBlockSource implements the notify-side ChainBlockSource view of
// block.Aggregator.BlockedPeerIds.
type fakeChainBlockSource struct {
	peers map[string][]string
}

func (f fakeChainBlockSource) BlockedPeerIds(subject string) ([]string, error) {
	return f.peers[subject], nil
}

func TestBlockedChatsMergedUnion(t *testing.T) {
	agg := newBridgeFixture(t)

	// Off-chain: Alice blocked Carol via the session API only.
	offChain := &UserBlockedChats{UserID: idUAlice, BlockedChats: []BlockedChat{
		{ChatID: "idq1carol-session-only", ChatType: "private", MetaID: idUAlice, Reason: "offchain"},
	}}
	if err := agg.saveBlockedChats(offChain); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// On-chain: Alice blocks Bob and Dave blocks Alice (either direction).
	chain := fakeChainBlockSource{peers: map[string][]string{
		idUAlice: {idUBob, "idq1dave-chain-only"},
	}}

	merged, err := agg.BlockedChatsMerged(idUAlice, chain)
	if err != nil {
		t.Fatalf("BlockedChatsMerged: %v", err)
	}
	ids := map[string]bool{}
	for _, bc := range merged.BlockedChats {
		ids[bc.ChatID] = true
	}
	if !ids["idq1carol-session-only"] || !ids[idUBob] || !ids["idq1dave-chain-only"] {
		t.Fatalf("merged = %v; want union of both sources", ids)
	}

	// Single-chat filter question: union semantics.
	for peer, want := range map[string]bool{
		"idq1carol-session-only": true, // off-chain only
		idUBob:                   true, // on-chain outbound
		"idq1dave-chain-only":    true, // on-chain inbound
		"idq1stranger":           false,
	} {
		got, err := agg.IsChatBlockedMerged(idUAlice, peer, chain)
		if err != nil {
			t.Fatalf("IsChatBlockedMerged(%s): %v", peer, err)
		}
		if got != want {
			t.Fatalf("IsChatBlockedMerged(%s) = %v; want %v", peer, got, want)
		}
	}
}

func TestBridgeHandlesEmptyAliasesAndNilStore(t *testing.T) {
	agg := newBridgeFixture(t)
	if err := agg.ApplyChainBlock(nil, []string{idUBob}, "blk-x:i0", 0); err != nil {
		t.Fatalf("empty from aliases must be a safe no-op: %v", err)
	}
	if err := agg.RemoveChainBlock([]string{idUAlice}, nil); err != nil {
		t.Fatalf("empty to aliases must be a safe no-op: %v", err)
	}

	var bare *Aggregator
	if err := bare.ApplyChainBlock([]string{idUAlice}, []string{idUBob}, "blk-x:i0", 0); err != nil {
		t.Fatalf("nil receiver must be a safe no-op: %v", err)
	}
}

// The bridge constant must stay in lockstep with the block package's
// provenance marker (single source of truth).
func TestChainReasonPrefixMatchesBlockPackage(t *testing.T) {
	if chainBlockReasonPrefix != block.ChainBlockReasonPrefix {
		t.Fatalf("notify prefix %q != block prefix %q", chainBlockReasonPrefix, block.ChainBlockReasonPrefix)
	}
}
