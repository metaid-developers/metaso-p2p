package federation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"
)

const (
	mvcSecondPrivateKeyHex = "0000000000000000000000000000000000000000000000000000000000000002"
	mvcSecondPublicKeyHex  = "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5"
	mvcSecondAddress       = "1cMh228HTCiwS8ZsaakH8A8wze1JR5ZsP"
)

func TestDiscoveryExpandsDefaultTemplateAndAcceptsMANAPIEnvelope(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self")
	nodeAID := discoveryRegistryNodeID(t, "node-a", "mvc-mainnet")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/pin/path/list" {
			t.Fatalf("request path: want /pin/path/list got %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("path"); got != RegistryPath {
			t.Fatalf("request path query: want %q got %q", RegistryPath, got)
		}
		if got := r.URL.Query().Get("size"); got != "100" {
			t.Fatalf("request size query: want 100 got %q", got)
		}
		writeDiscoveryMANAPIResponse(t, w, []map[string]any{
			discoveryMANAPIPin(t, "pin-1", "create", "mvc", now.UnixMilli(), discoveryRegistryPayload(t, "node-a", now)),
		})
	}))
	defer server.Close()

	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		RegistryPath:  RegistryPath,
		Size:          100,
		SelfNodeID:    "node-self",
		Store:         store,
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	peers, err := discovery.DiscoverOnce(context.Background())
	if err != nil {
		t.Fatalf("DiscoverOnce returned error: %v", err)
	}
	if len(peers) != 1 {
		t.Fatalf("peers: want 1 got %d", len(peers))
	}
	if peers[0].NodeID != nodeAID {
		t.Fatalf("accepted nodeId: want %q got %q", nodeAID, peers[0].NodeID)
	}

	stored, ok := store.Peer(nodeAID)
	if !ok {
		t.Fatalf("store should contain accepted peer %s", nodeAID)
	}
	if stored.PresenceURL != "https://node-a.example/.well-known/metaso-p2p/presence" {
		t.Fatalf("stored presenceUrl: got %q", stored.PresenceURL)
	}
	stored.Capabilities[0] = "mutated"
	storedAgain, ok := store.Peer(nodeAID)
	if !ok {
		t.Fatalf("store should still contain accepted peer %s", nodeAID)
	}
	if storedAgain.Capabilities[0] != "presence-v1" {
		t.Fatalf("store Peer must return a clone, got capabilities %#v", storedAgain.Capabilities)
	}
}

func TestDiscoveryURLEncodesRegistryPathPlaceholder(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	registryPath := "/protocols/meta socket/node?tag=a&x=1"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("path"); got != registryPath {
			t.Fatalf("request path query: want %q got %q raw=%q", registryPath, got, r.URL.RawQuery)
		}
		if got := r.URL.Query().Get("tag"); got != "" {
			t.Fatalf("registry path must not leak tag into query params, got %q raw=%q", got, r.URL.RawQuery)
		}
		if got := r.URL.Query().Get("x"); got != "" {
			t.Fatalf("registry path must not leak x into query params, got %q raw=%q", got, r.URL.RawQuery)
		}
		writeDiscoveryMANAPIResponse(t, w, nil)
	}))
	defer server.Close()

	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		RegistryPath:  registryPath,
		Size:          5,
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	peers, err := discovery.DiscoverOnce(context.Background())
	if err != nil {
		t.Fatalf("DiscoverOnce returned error: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("peers: want empty got %d", len(peers))
	}
}

func TestDiscoveryTreatsNullListAsEmpty(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"code":1,"message":"ok","data":{"list":null,"nextCursor":"cursor-1","total":7}}`)
	}))
	defer server.Close()

	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	peers, err := discovery.DiscoverOnce(context.Background())
	if err != nil {
		t.Fatalf("DiscoverOnce returned error: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("peers: want empty got %d", len(peers))
	}
}

func TestDiscoveryAcceptsCreateModifyAndContentSummaryFallback(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	summaryPayload := discoveryRegistryPayload(t, "node-summary", now)
	wantNodeIDs := discoverySortedNodeIDs(t, "mvc-mainnet", "node-body", "node-summary")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDiscoveryMANAPIResponse(t, w, []map[string]any{
			discoveryMANAPIPin(t, "pin-body", "create", "mvc", now.UnixMilli(), discoveryRegistryPayload(t, "node-body", now)),
			{
				"id":             "pin-summary",
				"operation":      "modify",
				"chainName":      "MVC",
				"timestamp":      now.Add(time.Millisecond).UnixMilli(),
				"contentBody":    "",
				"contentSummary": discoveryPayloadJSON(t, summaryPayload),
			},
		})
	}))
	defer server.Close()

	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	peers, err := discovery.DiscoverOnce(context.Background())
	if err != nil {
		t.Fatalf("DiscoverOnce returned error: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("peers: want 2 got %d: %#v", len(peers), peers)
	}
	if peers[0].NodeID != wantNodeIDs[0] || peers[1].NodeID != wantNodeIDs[1] {
		t.Fatalf("peers should be sorted and include body plus summary payloads, want %#v got %#v", wantNodeIDs, peers)
	}
}

func TestDiscoveryFiltersPinsDeduplicatesNewestAndAppliesRemovals(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self")
	revoked := discoveryRegistryPayload(t, "node-revoked", now)
	expiredPeer := discoveryRegistryPayload(t, "node-expired", now)
	selfID := discoveryRegistryNodeID(t, "node-self", "mvc-mainnet")
	dupeID := discoveryRegistryNodeID(t, "node-dupe", "mvc-mainnet")
	store.UpsertPeer(revoked)
	store.UpsertPeer(expiredPeer)

	oldDupe := discoveryRegistryPayload(t, "node-dupe", now)
	oldDupe.PresenceURL = "https://old.example/.well-known/metaso-p2p/presence"
	newDupe := discoveryRegistryPayload(t, "node-dupe", now)
	newDupe.PresenceURL = "https://new.example/.well-known/metaso-p2p/presence"
	expired := discoveryRegistryPayload(t, "node-expired", now)
	expired.ValidUntil = now.Add(-time.Second).UnixMilli()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDiscoveryMANAPIResponse(t, w, []map[string]any{
			discoveryMANAPIPin(t, "pin-old", "create", "mvc", now.UnixMilli(), oldDupe),
			discoveryMANAPIPin(t, "pin-new", "modify", "mvc", now.Add(time.Second).UnixMilli(), newDupe),
			discoveryMANAPIPin(t, "pin-revoke-create", "create", "mvc", now.UnixMilli(), discoveryRegistryPayload(t, "node-revoked", now)),
			discoveryMANAPIPin(t, "pin-revoke", "revoke", "mvc", now.Add(2*time.Second).UnixMilli(), discoveryRegistryPayload(t, "node-revoked", now)),
			discoveryMANAPIPin(t, "pin-expired", "create", "mvc", now.UnixMilli(), expired),
			discoveryMANAPIPin(t, "pin-self", "create", "mvc", now.UnixMilli(), discoveryRegistryPayload(t, "node-self", now)),
			discoveryMANAPIPin(t, "pin-btc", "create", "btc", now.UnixMilli(), discoveryRegistryPayload(t, "node-btc", now)),
			discoveryMANAPIPin(t, "pin-delete", "delete", "mvc", now.UnixMilli(), discoveryRegistryPayload(t, "node-delete", now)),
		})
	}))
	defer server.Close()

	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		SelfNodeID:    selfID,
		Store:         store,
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	peers, err := discovery.DiscoverOnce(context.Background())
	if err != nil {
		t.Fatalf("DiscoverOnce returned error: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("peers: want 2 got %d: %#v", len(peers), peers)
	}
	if peers[0].NodeID != dupeID && peers[1].NodeID != dupeID {
		t.Fatalf("newest valid duplicate should still be discovered, got %#v", peers)
	}
	storedDupe, ok := store.Peer(dupeID)
	if !ok {
		t.Fatal("newest valid duplicate should still be stored")
	}
	if storedDupe.PresenceURL != newDupe.PresenceURL {
		t.Fatalf("newest valid duplicate should win in store, got %#v", storedDupe)
	}
	if _, ok := store.Peer(revoked.NodeID); ok {
		t.Fatal("revoked peer should be removed from store")
	}
	storedExpired, ok := store.Peer(expiredPeer.NodeID)
	if !ok {
		t.Fatal("expired registry lease should not remove peer from store")
	}
	if storedExpired.NodeID != expiredPeer.NodeID {
		t.Fatalf("expired registry lease peer should remain addressable, got %#v", storedExpired)
	}
	if _, ok := store.Peer(selfID); ok {
		t.Fatal("self node should not be inserted into peer store")
	}
}

func TestDiscoveryRejectsRegistryNodeIDNotBoundToPublicKey(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self")
	payload := discoveryRegistryPayload(t, "node-a", now)
	if payload.NodeID != "mvc:"+mvcTestAddress {
		t.Fatalf("test fixture nodeId: want mvc:%s got %q", mvcTestAddress, payload.NodeID)
	}
	payload.PublicKey = mvcSecondPublicKeyHex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDiscoveryMANAPIResponse(t, w, []map[string]any{
			discoveryMANAPIPin(t, "pin-mismatch", "create", "mvc", now.UnixMilli(), payload),
		})
	}))
	defer server.Close()

	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		Store:         store,
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	peers, err := discovery.DiscoverOnce(context.Background())
	if err != nil {
		t.Fatalf("DiscoverOnce returned error: %v", err)
	}
	if len(peers) != 0 {
		t.Fatalf("mismatched nodeId/publicKey should be rejected, got %#v", peers)
	}
	if _, ok := store.Peer(payload.NodeID); ok {
		t.Fatal("mismatched nodeId/publicKey should not be stored")
	}
}

func TestDiscoveryAcceptsRegistryNodeIDBoundToPublicKey(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self")
	payload := discoveryRegistryPayload(t, "node-b", now)
	if payload.NodeID != "mvc:"+mvcSecondAddress {
		t.Fatalf("test fixture nodeId: want mvc:%s got %q", mvcSecondAddress, payload.NodeID)
	}
	if payload.PublicKey != mvcSecondPublicKeyHex {
		t.Fatalf("test fixture publicKey: want %q got %q", mvcSecondPublicKeyHex, payload.PublicKey)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDiscoveryMANAPIResponse(t, w, []map[string]any{
			discoveryMANAPIPin(t, "pin-bound", "create", "mvc", now.UnixMilli(), payload),
		})
	}))
	defer server.Close()

	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		Store:         store,
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	peers, err := discovery.DiscoverOnce(context.Background())
	if err != nil {
		t.Fatalf("DiscoverOnce returned error: %v", err)
	}
	if len(peers) != 1 || peers[0].NodeID != payload.NodeID {
		t.Fatalf("bound nodeId/publicKey should be accepted, got %#v", peers)
	}
	if _, ok := store.Peer(payload.NodeID); !ok {
		t.Fatal("bound nodeId/publicKey should be stored")
	}
}

func TestDiscoveryDoesNotRemovePeerFromMismatchedRegistryIdentityRemovalPin(t *testing.T) {
	now := time.UnixMilli(1780000000000)

	tests := []struct {
		name      string
		operation string
		expire    bool
	}{
		{name: "revoke", operation: "revoke"},
		{name: "expired", operation: "modify", expire: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore("node-self")
			valid := discoveryRegistryPayload(t, "node-a", now)
			store.UpsertPeer(valid)
			malformed := valid
			malformed.PublicKey = mvcSecondPublicKeyHex
			if tt.expire {
				malformed.ValidUntil = now.Add(-time.Second).UnixMilli()
			}

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeDiscoveryMANAPIResponse(t, w, []map[string]any{
					discoveryMANAPIPin(t, "pin-mismatched-removal", tt.operation, "mvc", now.UnixMilli(), malformed),
				})
			}))
			defer server.Close()

			discovery := newTestDiscovery(t, DiscoveryOptions{
				MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
				Store:         store,
				HTTPClient:    server.Client(),
				Clock:         func() time.Time { return now },
			})

			if _, err := discovery.DiscoverOnce(context.Background()); err != nil {
				t.Fatalf("DiscoverOnce returned error: %v", err)
			}
			if _, ok := store.Peer(valid.NodeID); !ok {
				t.Fatal("mismatched nodeId/publicKey removal pin must not remove existing peer")
			}
		})
	}
}

func TestDiscoveryDoesNotRemovePeerFromMalformedRemovalPin(t *testing.T) {
	now := time.UnixMilli(1780000000000)

	tests := []struct {
		name          string
		operation     string
		malformedPeer func(RegistryNode) RegistryNode
		validPeer     func(RegistryNode) RegistryNode
		wantRemoved   bool
	}{
		{
			name:      "revoke invalid public key",
			operation: "revoke",
			malformedPeer: func(peer RegistryNode) RegistryNode {
				peer.PublicKey = "not-a-compressed-public-key"
				return peer
			},
			validPeer: func(peer RegistryNode) RegistryNode {
				return peer
			},
			wantRemoved: true,
		},
		{
			name:      "expired missing capability",
			operation: "modify",
			malformedPeer: func(peer RegistryNode) RegistryNode {
				peer.Capabilities = nil
				peer.ValidUntil = now.Add(-time.Second).UnixMilli()
				return peer
			},
			validPeer: func(peer RegistryNode) RegistryNode {
				peer.ValidUntil = now.Add(-time.Second).UnixMilli()
				return peer
			},
			wantRemoved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewStore("node-self")
			victim := discoveryRegistryPayload(t, "node-victim", now)
			store.UpsertPeer(victim)

			malformed := tt.malformedPeer(victim)
			malformedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeDiscoveryMANAPIResponse(t, w, []map[string]any{
					discoveryMANAPIPin(t, "pin-malformed", tt.operation, "mvc", now.UnixMilli(), malformed),
				})
			}))

			malformedDiscovery := newTestDiscovery(t, DiscoveryOptions{
				MANAPIBaseURL: malformedServer.URL + "/pin/path/list?path={protocol-path}&size={size}",
				Store:         store,
				HTTPClient:    malformedServer.Client(),
				Clock:         func() time.Time { return now },
			})
			_, err := malformedDiscovery.DiscoverOnce(context.Background())
			malformedServer.Close()
			if err != nil {
				t.Fatalf("malformed removal DiscoverOnce returned error: %v", err)
			}
			if _, ok := store.Peer(victim.NodeID); !ok {
				t.Fatal("malformed removal pin must not remove existing peer")
			}

			valid := tt.validPeer(victim)
			validServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeDiscoveryMANAPIResponse(t, w, []map[string]any{
					discoveryMANAPIPin(t, "pin-valid", tt.operation, "mvc", now.Add(time.Second).UnixMilli(), valid),
				})
			}))
			defer validServer.Close()

			validDiscovery := newTestDiscovery(t, DiscoveryOptions{
				MANAPIBaseURL: validServer.URL + "/pin/path/list?path={protocol-path}&size={size}",
				Store:         store,
				HTTPClient:    validServer.Client(),
				Clock:         func() time.Time { return now },
			})
			_, err = validDiscovery.DiscoverOnce(context.Background())
			if err != nil {
				t.Fatalf("valid removal DiscoverOnce returned error: %v", err)
			}
			_, ok := store.Peer(victim.NodeID)
			if tt.wantRemoved && ok {
				t.Fatal("valid removal pin should remove existing peer")
			}
			if !tt.wantRemoved && !ok {
				t.Fatal("expired registry lease should no longer remove existing peer")
			}
		})
	}
}

func TestDiscoveryRejectsHTTPPresenceURLUnlessAllowedOrLocalhost(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	tests := []struct {
		name         string
		presenceURL  string
		allowHTTP    bool
		wantAccepted bool
	}{
		{name: "reject remote http", presenceURL: "http://remote.example/.well-known/metaso-p2p/presence", wantAccepted: false},
		{name: "allow remote http when configured", presenceURL: "http://remote.example/.well-known/metaso-p2p/presence", allowHTTP: true, wantAccepted: true},
		{name: "allow localhost http", presenceURL: "http://localhost:8080/.well-known/metaso-p2p/presence", wantAccepted: true},
		{name: "allow 127 loopback http", presenceURL: "http://127.0.0.1:8080/.well-known/metaso-p2p/presence", wantAccepted: true},
		{name: "allow ipv6 loopback http", presenceURL: "http://[::1]:8080/.well-known/metaso-p2p/presence", wantAccepted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := discoveryRegistryPayload(t, "node-http", now)
			payload.PresenceURL = tt.presenceURL

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeDiscoveryMANAPIResponse(t, w, []map[string]any{
					discoveryMANAPIPin(t, "pin-http", "create", "mvc", now.UnixMilli(), payload),
				})
			}))
			defer server.Close()

			discovery := newTestDiscovery(t, DiscoveryOptions{
				MANAPIBaseURL:     server.URL + "/pin/path/list?path={protocol-path}&size={size}",
				AllowInsecureHTTP: tt.allowHTTP,
				HTTPClient:        server.Client(),
				Clock:             func() time.Time { return now },
			})

			peers, err := discovery.DiscoverOnce(context.Background())
			if err != nil {
				t.Fatalf("DiscoverOnce returned error: %v", err)
			}
			if got := len(peers) == 1; got != tt.wantAccepted {
				t.Fatalf("accepted=%t want %t peers=%#v", got, tt.wantAccepted, peers)
			}
		})
	}
}

func TestDiscoveryNormalizesNetworkAliases(t *testing.T) {
	now := time.UnixMilli(1780000000000)

	tests := []struct {
		name           string
		optionNetwork  string
		payloadNetwork string
	}{
		{name: "livenet accepts mvc mainnet", optionNetwork: "livenet", payloadNetwork: "mvc-mainnet"},
		{name: "testnet3 accepts mvc testnet", optionNetwork: "testnet3", payloadNetwork: "mvc-testnet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := discoveryRegistryPayloadForNetwork(t, "node-alias", now, tt.payloadNetwork)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeDiscoveryMANAPIResponse(t, w, []map[string]any{
					discoveryMANAPIPin(t, "pin-alias", "create", "mvc", now.UnixMilli(), payload),
				})
			}))
			defer server.Close()

			discovery := newTestDiscovery(t, DiscoveryOptions{
				MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
				Network:       tt.optionNetwork,
				HTTPClient:    server.Client(),
				Clock:         func() time.Time { return now },
			})

			peers, err := discovery.DiscoverOnce(context.Background())
			if err != nil {
				t.Fatalf("DiscoverOnce returned error: %v", err)
			}
			if len(peers) != 1 {
				t.Fatalf("peers: want 1 got %d: %#v", len(peers), peers)
			}
			if peers[0].Network != tt.payloadNetwork {
				t.Fatalf("accepted network: want %q got %q", tt.payloadNetwork, peers[0].Network)
			}
		})
	}
}

func TestNewDiscoveryRejectsUnsupportedNetwork(t *testing.T) {
	_, err := NewDiscovery(DiscoveryOptions{
		MANAPIBaseURL: "https://manapi.metaid.io/pin/path/list?path={protocol-path}&size={size}",
		RegistryPath:  RegistryPath,
		Network:       "dogecoin",
	})
	if err == nil {
		t.Fatal("NewDiscovery should reject unsupported network")
	}
}

func TestDiscoveryEnforcesMaxPeersDeterministically(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	wantNodeIDs := discoverySortedNodeIDs(t, "mvc-mainnet", "node-c", "node-a", "node-b")[:2]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeDiscoveryMANAPIResponse(t, w, []map[string]any{
			discoveryMANAPIPin(t, "pin-c", "create", "mvc", now.UnixMilli(), discoveryRegistryPayload(t, "node-c", now)),
			discoveryMANAPIPin(t, "pin-a", "create", "mvc", now.UnixMilli(), discoveryRegistryPayload(t, "node-a", now)),
			discoveryMANAPIPin(t, "pin-b", "create", "mvc", now.UnixMilli(), discoveryRegistryPayload(t, "node-b", now)),
		})
	}))
	defer server.Close()

	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		MaxPeers:      2,
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	peers, err := discovery.DiscoverOnce(context.Background())
	if err != nil {
		t.Fatalf("DiscoverOnce returned error: %v", err)
	}
	if len(peers) != 2 {
		t.Fatalf("peers: want 2 got %d", len(peers))
	}
	if peers[0].NodeID != wantNodeIDs[0] || peers[1].NodeID != wantNodeIDs[1] {
		t.Fatalf("max peers should cap deterministic nodeId order, want %#v got %#v", wantNodeIDs, peers)
	}
}

func TestDiscoveryStartRunsImmediatelyThenOnInterval(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	requests := make(chan struct{}, 5)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		writeDiscoveryMANAPIResponse(t, w, nil)
	}))
	defer server.Close()

	tickerFactory := newManualDiscoveryTickerFactory()
	discovery := newTestDiscovery(t, DiscoveryOptions{
		MANAPIBaseURL: server.URL + "/pin/path/list?path={protocol-path}&size={size}",
		Interval:      5 * time.Minute,
		TickerFactory: tickerFactory.NewTicker,
		HTTPClient:    server.Client(),
		Clock:         func() time.Time { return now },
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	discovery.Start(ctx)

	waitDiscoveryRequests(t, requests, 1)
	ticker := tickerFactory.waitTicker(t)
	if ticker.interval != 5*time.Minute {
		t.Fatalf("ticker interval: want 5m got %s", ticker.interval)
	}

	ticker.tick(now.Add(5 * time.Minute))
	waitDiscoveryRequests(t, requests, 1)
	cancel()
}

func newTestDiscovery(t *testing.T, overrides DiscoveryOptions) *Discovery {
	t.Helper()

	opts := DiscoveryOptions{
		MANAPIBaseURL: "https://manapi.metaid.io/pin/path/list?path={protocol-path}&size={size}",
		RegistryPath:  RegistryPath,
		Size:          100,
		SelfNodeID:    discoveryRegistryNodeID(t, "node-self", "mvc-mainnet"),
		Network:       "mvc-mainnet",
		Clock:         func() time.Time { return time.UnixMilli(1780000000000) },
		Interval:      time.Minute,
	}
	if overrides.MANAPIBaseURL != "" {
		opts.MANAPIBaseURL = overrides.MANAPIBaseURL
	}
	if overrides.RegistryPath != "" {
		opts.RegistryPath = overrides.RegistryPath
	}
	if overrides.Size != 0 {
		opts.Size = overrides.Size
	}
	if overrides.SelfNodeID != "" {
		opts.SelfNodeID = overrides.SelfNodeID
	}
	if overrides.Network != "" {
		opts.Network = overrides.Network
	}
	if overrides.AllowInsecureHTTP {
		opts.AllowInsecureHTTP = overrides.AllowInsecureHTTP
	}
	if overrides.MaxPeers != 0 {
		opts.MaxPeers = overrides.MaxPeers
	}
	if overrides.RequestTimeout != 0 {
		opts.RequestTimeout = overrides.RequestTimeout
	}
	if overrides.Store != nil {
		opts.Store = overrides.Store
	}
	if overrides.HTTPClient != nil {
		opts.HTTPClient = overrides.HTTPClient
	}
	if overrides.Clock != nil {
		opts.Clock = overrides.Clock
	}
	if overrides.Interval != 0 {
		opts.Interval = overrides.Interval
	}
	if overrides.TickerFactory != nil {
		opts.TickerFactory = overrides.TickerFactory
	}

	discovery, err := NewDiscovery(opts)
	if err != nil {
		t.Fatalf("NewDiscovery returned error: %v", err)
	}
	return discovery
}

func discoveryRegistryPayload(t *testing.T, alias string, now time.Time) RegistryNode {
	t.Helper()
	return discoveryRegistryPayloadForNetwork(t, alias, now, "mvc-mainnet")
}

func discoveryRegistryPayloadForNetwork(t *testing.T, alias string, now time.Time, network string) RegistryNode {
	t.Helper()
	address, publicKey := discoveryRegistryIdentity(t, alias, network)
	publicBaseURL := "https://" + alias + ".example"
	return RegistryNode{
		Protocol:      ProtocolNode,
		Version:       Version,
		NodeID:        "mvc:" + address,
		Network:       network,
		PublicBaseURL: publicBaseURL,
		SocketURL:     publicBaseURL + "/socket/socket.io",
		PresenceURL:   publicBaseURL + "/.well-known/metaso-p2p/presence",
		PublicKey:     publicKey,
		Capabilities:  []string{"presence-v1"},
		PublishedAt:   now.UnixMilli(),
		ValidUntil:    now.Add(time.Hour).UnixMilli(),
	}
}

func discoveryRegistryNodeID(t *testing.T, alias string, network string) string {
	t.Helper()
	address, _ := discoveryRegistryIdentity(t, alias, network)
	return "mvc:" + address
}

func discoverySortedNodeIDs(t *testing.T, network string, aliases ...string) []string {
	t.Helper()
	nodeIDs := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		nodeIDs = append(nodeIDs, discoveryRegistryNodeID(t, alias, network))
	}
	sort.Strings(nodeIDs)
	return nodeIDs
}

func discoveryRegistryIdentity(t *testing.T, alias string, network string) (address string, publicKey string) {
	t.Helper()
	address, publicKey, err := MVCIdentityFromPrivateKey(discoveryRegistryPrivateKeyHex(alias), network)
	if err != nil {
		t.Fatalf("derive discovery registry identity for %s on %s: %v", alias, network, err)
	}
	return address, publicKey
}

func discoveryRegistryPrivateKeyHex(alias string) string {
	switch alias {
	case "node-a":
		return mvcTestPrivateKeyHex
	case "node-b":
		return mvcSecondPrivateKeyHex
	default:
		digest := sha256.Sum256([]byte("metaso-p2p discovery test identity:" + alias))
		digest[0] = 1
		return hex.EncodeToString(digest[:])
	}
}

func discoveryMANAPIPin(t *testing.T, id string, operation string, chainName string, timestamp int64, payload RegistryNode) map[string]any {
	t.Helper()
	return map[string]any{
		"id":             id,
		"operation":      operation,
		"chainName":      chainName,
		"timestamp":      timestamp,
		"contentBody":    discoveryPayloadJSON(t, payload),
		"contentSummary": "",
	}
}

func discoveryPayloadJSON(t *testing.T, payload RegistryNode) string {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal discovery payload: %v", err)
	}
	return string(data)
}

func writeDiscoveryMANAPIResponse(t *testing.T, w http.ResponseWriter, pins []map[string]any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if pins == nil {
		fmt.Fprint(w, `{"code":1,"message":"ok","data":{"list":[],"nextCursor":"","total":0}}`)
		return
	}
	if err := json.NewEncoder(w).Encode(map[string]any{
		"code":    1,
		"message": "ok",
		"data": map[string]any{
			"list":       pins,
			"nextCursor": "",
			"total":      len(pins),
		},
	}); err != nil {
		t.Fatalf("write MANAPI response: %v", err)
	}
}

func waitDiscoveryRequests(t *testing.T, requests <-chan struct{}, want int) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-requests:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for request %d", i+1)
		}
	}
}

type manualDiscoveryTickerFactory struct {
	mu      sync.Mutex
	tickers []*manualDiscoveryTicker
	notify  chan struct{}
}

func newManualDiscoveryTickerFactory() *manualDiscoveryTickerFactory {
	return &manualDiscoveryTickerFactory{notify: make(chan struct{}, 5)}
}

func (f *manualDiscoveryTickerFactory) NewTicker(interval time.Duration) DiscoveryTicker {
	ticker := &manualDiscoveryTicker{
		interval: interval,
		ch:       make(chan time.Time, 5),
	}
	f.mu.Lock()
	f.tickers = append(f.tickers, ticker)
	f.mu.Unlock()
	select {
	case f.notify <- struct{}{}:
	default:
	}
	return ticker
}

func (f *manualDiscoveryTickerFactory) waitTicker(t *testing.T) *manualDiscoveryTicker {
	t.Helper()
	for {
		f.mu.Lock()
		if len(f.tickers) > 0 {
			ticker := f.tickers[0]
			f.mu.Unlock()
			return ticker
		}
		f.mu.Unlock()
		select {
		case <-f.notify:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for discovery ticker")
		}
	}
}

type manualDiscoveryTicker struct {
	interval time.Duration
	ch       chan time.Time
	stopped  bool
}

func (t *manualDiscoveryTicker) Chan() <-chan time.Time {
	return t.ch
}

func (t *manualDiscoveryTicker) Stop() {
	t.stopped = true
}

func (t *manualDiscoveryTicker) tick(now time.Time) {
	t.ch <- now
}
