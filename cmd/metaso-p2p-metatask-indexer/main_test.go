package main

import "testing"

// TestChainRPCConfigDisablesTLS pins the deployment-blocking defect found in
// the 2026-09-18 pre-fire check: the rpcclient only speaks plain http when
// DisableTLS is set, so a missing bit turns every chain-head probe into a
// failed https attempt and GetBestHeight silently falls back to 0
// (X-Metatask-Chain-Head: 0). The config must mirror the internal/config
// block-index defaults.
func TestChainRPCConfigDisablesTLS(t *testing.T) {
	cfg := chainRPCConfig("127.0.0.1:9884", "user", "pass")
	if !cfg.RPCDisableTLS {
		t.Fatal("RPCDisableTLS must be set: the SOP RPC target is plain http; without this bit every connection goes https and the chain head silently degrades to 0")
	}
	if !cfg.RPCHTTPPostMode {
		t.Fatal("RPCHTTPPostMode must be set (matches the block-index defaults)")
	}
	if cfg.RPCHost != "127.0.0.1:9884" || cfg.RPCUser != "user" || cfg.RPCPass != "pass" {
		t.Fatalf("rpc credentials must pass through verbatim: %+v", cfg)
	}
}
