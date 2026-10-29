package notify

import (
	"errors"
	"testing"
)

// failingChainSource always errors, to prove filter errors propagate
// instead of silently failing open.
type failingChainSource struct{}

func (failingChainSource) BlockedPeerIds(subject string) ([]string, error) {
	return nil, errors.New("chain source unavailable")
}

// TestChatBlockFilterUnionSemanticsAndAliases covers the adapter the
// privatechat wiring consumes: alias-list matching, union semantics across
// both sources, error propagation and the nil contracts.
func TestChatBlockFilterUnionSemanticsAndAliases(t *testing.T) {
	agg := newBridgeFixture(t)

	// Off-chain session entry keyed by the LEGACY form, exactly as a
	// client would have written it via add_blocked_chat.
	offChain := &UserBlockedChats{UserID: idUAlice, BlockedChats: []BlockedChat{
		{ChatID: "bob-hex-legacy", ChatType: "private", MetaID: idUAlice, Reason: "offchain"},
	}}
	if err := agg.saveBlockedChats(offChain); err != nil {
		t.Fatalf("seed off-chain entry: %v", err)
	}

	// No chain source: off-chain-only union still filters, and only when
	// one of the peer aliases matches the stored key.
	f := NewChatBlockFilter(agg, fakeChainBlockSource{})
	blocked, err := f.IsChatBlocked(idUAlice, []string{idUBob})
	if err != nil || blocked {
		t.Fatalf("canonical-only alias = %v, %v; want false (entry keyed by legacy form)", blocked, err)
	}
	blocked, err = f.IsChatBlocked(idUAlice, []string{idUBob, "bob-hex-legacy"})
	if err != nil || !blocked {
		t.Fatalf("alias list incl. legacy form = %v, %v; want true", blocked, err)
	}
	blocked, err = f.IsChatBlocked(idUBob, []string{idUAlice})
	if err != nil || blocked {
		t.Fatalf("peer's own view = %v, %v; want false (off-chain entries are per-owner session state)", blocked, err)
	}

	// Chain-only union: the chain source alone filters without any
	// off-chain entry (either direction relative to the viewer).
	chain := fakeChainBlockSource{peers: map[string][]string{
		idUAlice: {"idq1carol-chain-only"},
	}}
	fChain := NewChatBlockFilter(agg, chain)
	blocked, err = fChain.IsChatBlocked(idUAlice, []string{"idq1carol-chain-only"})
	if err != nil || !blocked {
		t.Fatalf("chain-only union = %v, %v; want true", blocked, err)
	}

	// Chain errors propagate: a read whose filter state cannot be
	// determined must fail rather than leak.
	fFail := NewChatBlockFilter(agg, failingChainSource{})
	if _, err := fFail.IsChatBlocked(idUAlice, []string{idUBob}); err == nil {
		t.Fatal("chain source error must propagate, got nil")
	}

	// Nil contracts: no notify model → no filter; a nil filter answers
	// unblocked for every pair.
	if NewChatBlockFilter(nil, chain) != nil {
		t.Fatal("NewChatBlockFilter(nil, …) must return a nil filter")
	}
	var nilFilter *ChatBlockFilter
	blocked, err = nilFilter.IsChatBlocked(idUAlice, []string{idUBob})
	if err != nil || blocked {
		t.Fatalf("nil filter = %v, %v; want false, nil (legacy behavior)", blocked, err)
	}
}
