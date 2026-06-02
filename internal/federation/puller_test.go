package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metaid-developers/meta-socket/internal/presence"
)

func TestPullerPullsPresenceURLForActivePeers(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/presence":
			requests++
			writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now, 90, 1))
		default:
			t.Fatalf("unexpected pull path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-self", server.URL+"/self", now.Add(time.Hour)))
	store.UpsertPeer(pullerRegistryNode("node-expired", server.URL+"/expired", now.Add(-time.Second)))
	store.UpsertPeer(pullerRegistryNode("node-a", server.URL+"/presence", now.Add(time.Hour)))

	puller := newTestPuller(t, PullerOptions{
		Store:      store,
		HTTPClient: server.Client(),
		Clock:      func() time.Time { return now },
	})

	if err := puller.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce returned error: %v", err)
	}
	if requests != 1 {
		t.Fatalf("presence requests: want 1 got %d", requests)
	}
	snapshot, ok := store.Snapshot("node-a")
	if !ok {
		t.Fatal("accepted remote snapshot should be stored")
	}
	if snapshot.Sequence != 1 {
		t.Fatalf("stored sequence: want 1 got %d", snapshot.Sequence)
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].MetaId != "meta-node-a" {
		t.Fatalf("stored snapshot items mismatch: %#v", snapshot.Items)
	}
}

func TestPullerUsesConfiguredRequestTimeout(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now, 90, 1))
	}))
	defer server.Close()

	requestTimeout := 37 * time.Millisecond
	capture := &deadlineCaptureTransport{
		base:     server.Client().Transport,
		expected: requestTimeout,
	}
	client := server.Client()
	client.Transport = capture

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, now.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:          store,
		HTTPClient:     client,
		RequestTimeout: requestTimeout,
		Clock:          func() time.Time { return now },
	})

	if err := puller.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce returned error: %v", err)
	}
	if err := capture.err(); err != nil {
		t.Fatal(err)
	}
	if !capture.sawDeadline() {
		t.Fatal("puller request should set a context deadline")
	}
}

func TestPullerEnforcesMaxSnapshotBytes(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, strings.Repeat("x", 33))
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, now.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:            store,
		HTTPClient:       server.Client(),
		MaxSnapshotBytes: 32,
		Clock:            func() time.Time { return now },
	})

	err := puller.PullOnce(context.Background())
	if err == nil {
		t.Fatal("PullOnce should reject an oversized snapshot body")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized body error should mention exceeds, got %v", err)
	}
	if _, ok := store.Snapshot("node-a"); ok {
		t.Fatal("oversized snapshot should not be stored")
	}
}

func TestPullerVerifiesSnapshotSignatureWithRegistryPublicKey(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now, 90, 1))
	}))
	defer server.Close()

	otherPublicKey := pullerPublicKeyForPrivateKey(t, "0000000000000000000000000000000000000000000000000000000000000002")
	peer := pullerRegistryNode("node-a", server.URL, now.Add(time.Hour))
	peer.PublicKey = otherPublicKey
	store.UpsertPeer(peer)

	puller := newTestPuller(t, PullerOptions{
		Store:      store,
		HTTPClient: server.Client(),
		Clock:      func() time.Time { return now },
	})

	err := puller.PullOnce(context.Background())
	if err == nil {
		t.Fatal("PullOnce should reject snapshots signed by a different key")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "signature") {
		t.Fatalf("signature verification error should mention signature, got %v", err)
	}
	if _, ok := store.Snapshot("node-a"); ok {
		t.Fatal("signature verification failure should not store snapshot")
	}
}

func TestPullerRejectsNodeIDMismatch(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-b", now, 90, 1))
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, now.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:      store,
		HTTPClient: server.Client(),
		Clock:      func() time.Time { return now },
	})

	err := puller.PullOnce(context.Background())
	if err == nil {
		t.Fatal("PullOnce should reject a snapshot nodeId that differs from the registry node")
	}
	if !strings.Contains(err.Error(), "nodeId") {
		t.Fatalf("nodeId mismatch error should mention nodeId, got %v", err)
	}
	if _, ok := store.Snapshot("node-a"); ok {
		t.Fatal("nodeId mismatch should not store snapshot")
	}
}

func TestPullerRejectsStaleSnapshot(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now.Add(-91*time.Second), 90, 1))
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, now.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:      store,
		HTTPClient: server.Client(),
		Clock:      func() time.Time { return now },
	})

	err := puller.PullOnce(context.Background())
	if err == nil {
		t.Fatal("PullOnce should reject a stale remote snapshot")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "stale") {
		t.Fatalf("stale snapshot error should mention stale, got %v", err)
	}
	if _, ok := store.Snapshot("node-a"); ok {
		t.Fatal("stale snapshot should not be stored")
	}
}

func TestPullerRejectsSnapshotThatExpiresDuringFetch(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))
	requestStart := now

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now = requestStart.Add(2 * time.Second)
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", requestStart.Add(-89*time.Second), 90, 1))
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, requestStart.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:      store,
		HTTPClient: server.Client(),
		Clock:      func() time.Time { return now },
	})

	err := puller.PullOnce(context.Background())
	if err == nil {
		t.Fatal("PullOnce should reject a snapshot that expired before validation")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "stale") {
		t.Fatalf("expired-during-fetch error should mention stale, got %v", err)
	}
	if _, ok := store.Snapshot("node-a"); ok {
		t.Fatal("snapshot that expired during fetch should not be stored")
	}
}

func TestPullerRejectsLowerSequenceThanLastAccepted(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))
	store.UpsertSnapshot(pullerSignedSnapshot(t, "node-a", now.Add(-time.Second), 90, 5))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now, 90, 4))
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, now.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:      store,
		HTTPClient: server.Client(),
		Clock:      func() time.Time { return now },
	})

	err := puller.PullOnce(context.Background())
	if err == nil {
		t.Fatal("PullOnce should reject a sequence lower than the last accepted snapshot")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sequence") {
		t.Fatalf("sequence error should mention sequence, got %v", err)
	}
	snapshot, ok := store.Snapshot("node-a")
	if !ok {
		t.Fatal("existing snapshot should remain stored")
	}
	if snapshot.Sequence != 5 {
		t.Fatalf("stored sequence should remain 5 after rejected replay, got %d", snapshot.Sequence)
	}
}

func TestPullerRejectsEqualSequenceThanLastAccepted(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))
	store.UpsertSnapshot(pullerSignedSnapshot(t, "node-a", now.Add(-time.Second), 90, 5))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now, 90, 5))
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, now.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:      store,
		HTTPClient: server.Client(),
		Clock:      func() time.Time { return now },
	})

	err := puller.PullOnce(context.Background())
	if err == nil {
		t.Fatal("PullOnce should reject an equal sequence while the stored snapshot is fresh")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sequence") {
		t.Fatalf("equal sequence error should mention sequence, got %v", err)
	}
	snapshot, ok := store.Snapshot("node-a")
	if !ok {
		t.Fatal("existing snapshot should remain stored")
	}
	if snapshot.Sequence != 5 {
		t.Fatalf("stored sequence should remain 5 after rejected duplicate, got %d", snapshot.Sequence)
	}
}

func TestPullerAcceptsLowerSequenceWhenStoredSnapshotIsStale(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))
	store.UpsertSnapshot(pullerSignedSnapshot(t, "node-a", now.Add(-91*time.Second), 90, 5))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now, 90, 1))
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, now.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:      store,
		HTTPClient: server.Client(),
		Clock:      func() time.Time { return now },
	})

	if err := puller.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce should allow reset sequence when stored snapshot is stale: %v", err)
	}
	snapshot, ok := store.Snapshot("node-a")
	if !ok {
		t.Fatal("fresh replacement snapshot should be stored")
	}
	if snapshot.Sequence != 1 {
		t.Fatalf("stored sequence should be replaced with reset sequence 1, got %d", snapshot.Sequence)
	}
}

func TestPullerAppliesBackoffAfterRepeatedFailures(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		http.Error(w, "temporary failure", http.StatusInternalServerError)
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, now.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:       store,
		HTTPClient:  server.Client(),
		Clock:       func() time.Time { return now },
		BackoffBase: 5 * time.Second,
		BackoffMax:  20 * time.Second,
	})

	if err := puller.PullOnce(context.Background()); err == nil {
		t.Fatal("first failed pull should return an error")
	}
	if requests != 1 {
		t.Fatalf("requests after first failure: want 1 got %d", requests)
	}
	if err := puller.PullOnce(context.Background()); err != nil {
		t.Fatalf("peer under backoff should be skipped without error, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("backoff should skip immediate retry, requests=%d", requests)
	}

	now = now.Add(5*time.Second - time.Nanosecond)
	if err := puller.PullOnce(context.Background()); err != nil {
		t.Fatalf("peer just before retry eligibility should be skipped, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("backoff should skip before eligibility, requests=%d", requests)
	}

	now = now.Add(time.Nanosecond)
	if err := puller.PullOnce(context.Background()); err == nil {
		t.Fatal("second failed pull should return an error")
	}
	if requests != 2 {
		t.Fatalf("requests after second eligible failure: want 2 got %d", requests)
	}

	now = now.Add(5 * time.Second)
	if err := puller.PullOnce(context.Background()); err != nil {
		t.Fatalf("second failure should apply doubled backoff and skip at 5s, got %v", err)
	}
	if requests != 2 {
		t.Fatalf("doubled backoff should skip at 5s, requests=%d", requests)
	}

	now = now.Add(5 * time.Second)
	if err := puller.PullOnce(context.Background()); err == nil {
		t.Fatal("third failed pull should run after doubled backoff")
	}
	if requests != 3 {
		t.Fatalf("requests after doubled backoff expires: want 3 got %d", requests)
	}
}

func TestPullerBackoffStartsWhenSlowFailureIsObserved(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	requestStart := now
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))
	requests := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		now = requestStart.Add(10 * time.Second)
		http.Error(w, "slow failure", http.StatusInternalServerError)
	}))
	defer server.Close()

	store.UpsertPeer(pullerRegistryNode("node-a", server.URL, requestStart.Add(time.Hour)))
	puller := newTestPuller(t, PullerOptions{
		Store:       store,
		HTTPClient:  server.Client(),
		Clock:       func() time.Time { return now },
		BackoffBase: 5 * time.Second,
		BackoffMax:  20 * time.Second,
	})

	if err := puller.PullOnce(context.Background()); err == nil {
		t.Fatal("first slow failed pull should return an error")
	}
	if requests != 1 {
		t.Fatalf("requests after slow failure: want 1 got %d", requests)
	}
	if err := puller.PullOnce(context.Background()); err != nil {
		t.Fatalf("immediate retry after slow failure should be skipped, got %v", err)
	}
	if requests != 1 {
		t.Fatalf("backoff should be relative to failure-observed time and skip immediate retry, requests=%d", requests)
	}
}

func TestRemoteSnapshotStoreHelpersReturnClonesAndActivePeers(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	store := NewStore("node-self", WithStoreClock(func() time.Time { return now }))

	store.UpsertPeer(pullerRegistryNode("node-b", "https://b.example/presence", now.Add(time.Hour)))
	store.UpsertPeer(pullerRegistryNode("node-expired", "https://expired.example/presence", now.Add(-time.Second)))
	store.UpsertPeer(pullerRegistryNode("node-a", "https://a.example/presence", now.Add(time.Hour)))
	store.UpsertPeer(pullerRegistryNode("node-self", "https://self.example/presence", now.Add(time.Hour)))
	originalSnapshot := pullerSignedSnapshot(t, "node-a", now, 90, 7)
	originalSnapshot.Items[0].SourceNodeIds = []string{"node-a", "node-b"}
	store.UpsertSnapshot(originalSnapshot)
	originalSnapshot.Items[0].SourceNodeIds[0] = "mutated-original"

	peers := store.ActivePeers(now)
	if len(peers) != 3 {
		t.Fatalf("active peers: want 3 got %d: %#v", len(peers), peers)
	}
	if peers[0].NodeID != "node-a" || peers[1].NodeID != "node-b" || peers[2].NodeID != "node-self" {
		t.Fatalf("active peers should be sorted by nodeId and include non-expired self, got %#v", peers)
	}
	peers[0].Capabilities[0] = "mutated"
	again := store.ActivePeers(now)
	if again[0].Capabilities[0] != publisherPresenceCapability {
		t.Fatalf("ActivePeers should return cloned peers, got capabilities %#v", again[0].Capabilities)
	}

	snapshot, ok := store.Snapshot("node-a")
	if !ok {
		t.Fatal("Snapshot should return stored remote snapshot")
	}
	snapshot.Items[0].MetaId = "mutated"
	againSnapshot, ok := store.Snapshot("node-a")
	if !ok {
		t.Fatal("Snapshot should still return stored remote snapshot")
	}
	if againSnapshot.Items[0].MetaId != "meta-node-a" {
		t.Fatalf("Snapshot should return a clone, got items %#v", againSnapshot.Items)
	}
	if againSnapshot.Items[0].SourceNodeIds[0] != "node-a" {
		t.Fatalf("UpsertSnapshot should deep-clone nested SourceNodeIds, got %#v", againSnapshot.Items[0].SourceNodeIds)
	}
	snapshotWithSources, ok := store.Snapshot("node-a")
	if !ok {
		t.Fatal("Snapshot should still return stored remote snapshot")
	}
	snapshotWithSources.Items[0].SourceNodeIds[0] = "mutated-return"
	againSnapshot, ok = store.Snapshot("node-a")
	if !ok {
		t.Fatal("Snapshot should still return stored remote snapshot")
	}
	if againSnapshot.Items[0].SourceNodeIds[0] != "node-a" {
		t.Fatalf("Snapshot should deep-clone nested SourceNodeIds, got %#v", againSnapshot.Items[0].SourceNodeIds)
	}
}

func newTestPuller(t *testing.T, overrides PullerOptions) *Puller {
	t.Helper()

	opts := PullerOptions{
		Store:            NewStore("node-self"),
		SelfNodeID:       "node-self",
		RequestTimeout:   time.Second,
		MaxSnapshotBytes: 1 << 20,
		Interval:         time.Hour,
		Clock:            time.Now,
		BackoffBase:      time.Second,
		BackoffMax:       time.Minute,
	}
	if overrides.Store != nil {
		opts.Store = overrides.Store
	}
	if overrides.SelfNodeID != "" {
		opts.SelfNodeID = overrides.SelfNodeID
	}
	if overrides.HTTPClient != nil {
		opts.HTTPClient = overrides.HTTPClient
	}
	if overrides.RequestTimeout != 0 {
		opts.RequestTimeout = overrides.RequestTimeout
	}
	if overrides.MaxSnapshotBytes != 0 {
		opts.MaxSnapshotBytes = overrides.MaxSnapshotBytes
	}
	if overrides.Interval != 0 {
		opts.Interval = overrides.Interval
	}
	if overrides.Clock != nil {
		opts.Clock = overrides.Clock
	}
	if overrides.TickerFactory != nil {
		opts.TickerFactory = overrides.TickerFactory
	}
	if overrides.BackoffBase != 0 {
		opts.BackoffBase = overrides.BackoffBase
	}
	if overrides.BackoffMax != 0 {
		opts.BackoffMax = overrides.BackoffMax
	}

	puller, err := NewPuller(opts)
	if err != nil {
		t.Fatalf("NewPuller returned error: %v", err)
	}
	return puller
}

func pullerRegistryNode(nodeID string, presenceURL string, validUntil time.Time) RegistryNode {
	return RegistryNode{
		Protocol:      ProtocolNode,
		Version:       Version,
		NodeID:        nodeID,
		Network:       "mvc-mainnet",
		PublicBaseURL: "https://" + nodeID + ".example",
		SocketURL:     "https://" + nodeID + ".example/socket/socket.io",
		PresenceURL:   presenceURL,
		PublicKey:     mvcTestPublicKeyHex,
		Capabilities:  []string{publisherPresenceCapability},
		PublishedAt:   validUntil.Add(-time.Hour).UnixMilli(),
		ValidUntil:    validUntil.UnixMilli(),
	}
}

func pullerSignedSnapshot(t *testing.T, nodeID string, generatedAt time.Time, ttlSeconds int64, sequence uint64) presence.Snapshot {
	t.Helper()

	snapshot := presence.Snapshot{
		Protocol:    ProtocolPresence,
		Version:     Version,
		NodeID:      nodeID,
		GeneratedAt: generatedAt.UnixMilli(),
		TTLSeconds:  ttlSeconds,
		Sequence:    sequence,
		Items: []presence.OnlineEntry{
			{
				MetaId:      "meta-" + nodeID,
				Type:        "app",
				ConnectedAt: generatedAt.Add(-time.Second).UnixMilli(),
				LastSeenAt:  generatedAt.UnixMilli(),
			},
		},
	}
	signature, err := SignSnapshot(&snapshot, mvcTestPrivateKeyHex)
	if err != nil {
		t.Fatalf("sign puller snapshot: %v", err)
	}
	snapshot.Signature = signature
	return snapshot
}

func writePullerSnapshot(t *testing.T, w http.ResponseWriter, snapshot presence.Snapshot) {
	t.Helper()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		t.Fatalf("encode puller snapshot: %v", err)
	}
}

func pullerPublicKeyForPrivateKey(t *testing.T, privateKeyHex string) string {
	t.Helper()

	_, publicKey, err := MVCIdentityFromPrivateKey(privateKeyHex, "mvc-mainnet")
	if err != nil {
		t.Fatalf("derive test public key: %v", err)
	}
	return publicKey
}

type deadlineCaptureTransport struct {
	base     http.RoundTripper
	expected time.Duration

	mu       sync.Mutex
	saw      bool
	problems []string
}

func (t *deadlineCaptureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	deadline, ok := req.Context().Deadline()
	now := time.Now()

	t.mu.Lock()
	t.saw = ok
	if !ok {
		t.problems = append(t.problems, "request context has no deadline")
	} else if !deadline.After(now) {
		t.problems = append(t.problems, "request deadline is not in the future")
	} else if remaining := deadline.Sub(now); remaining > t.expected {
		t.problems = append(t.problems, fmt.Sprintf("deadline remaining %s exceeds configured timeout %s", remaining, t.expected))
	}
	t.mu.Unlock()

	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(req)
}

func (t *deadlineCaptureTransport) sawDeadline() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.saw
}

func (t *deadlineCaptureTransport) err() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(t.problems, "; "))
}
