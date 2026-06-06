package federation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

func TestServiceDisabledIsNoopAndDoesNotRequireDependencies(t *testing.T) {
	cfg := config.Default().Federation
	cfg.Enabled = false
	cfg.NodePrivateKey = "not-a-valid-private-key"
	cfg.PublicBaseURL = ""
	cfg.MANAPIBaseURL = ""
	cfg.MetaletBaseURL = ""

	service, err := NewService(cfg, nil)
	if err != nil {
		t.Fatalf("NewService disabled returned error: %v", err)
	}

	if service.Enabled() {
		t.Fatal("disabled service should report disabled")
	}
	if service.NodeID() != "" {
		t.Fatalf("disabled service node ID: want empty got %q", service.NodeID())
	}
	if service.SnapshotProvider() != nil {
		t.Fatal("disabled service should not expose a snapshot provider")
	}
	if service.GlobalReader() != nil {
		t.Fatal("disabled service should not expose a global reader")
	}
	if service.publisher != nil || service.discovery != nil || service.puller != nil {
		t.Fatal("disabled service should not construct lifecycle components")
	}

	service.Start(context.Background())
	service.Stop()
	service.Stop()
}

func TestServiceEnabledWiresSignedSnapshotAndGlobalReader(t *testing.T) {
	cfg := validServiceFederationConfig()
	local := &serviceLocalReader{
		entries: []presence.OnlineEntry{
			{MetaId: "meta-a", Type: "app", ConnectedAt: 1700000000000, LastSeenAt: 1700000001000},
		},
	}

	service, err := NewService(
		cfg,
		local,
		WithServicePublisherClient(newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(50_000)})),
	)
	if err != nil {
		t.Fatalf("NewService enabled returned error: %v", err)
	}

	wantNodeID := "mvc:" + mvcTestAddress
	if service.NodeID() != wantNodeID {
		t.Fatalf("node ID: want %q got %q", wantNodeID, service.NodeID())
	}
	if !service.Enabled() {
		t.Fatal("enabled service should report enabled")
	}
	if service.SnapshotProvider() == nil {
		t.Fatal("enabled service should expose a snapshot provider")
	}
	if service.GlobalReader() == nil {
		t.Fatal("enabled service should expose a global reader")
	}
	if !service.GlobalReader().Enabled() {
		t.Fatal("global reader should be enabled")
	}
	if service.GlobalReader().DefaultScope() != cfg.DefaultScope {
		t.Fatalf("default scope: want %q got %q", cfg.DefaultScope, service.GlobalReader().DefaultScope())
	}

	snapshot, err := service.SnapshotProvider().Snapshot()
	if err != nil {
		t.Fatalf("snapshot returned error: %v", err)
	}
	if snapshot.NodeID != wantNodeID {
		t.Fatalf("snapshot node ID: want %q got %q", wantNodeID, snapshot.NodeID)
	}
	if snapshot.TTLSeconds != int64(cfg.PresenceTTL/time.Second) {
		t.Fatalf("snapshot TTL seconds: want %d got %d", int64(cfg.PresenceTTL/time.Second), snapshot.TTLSeconds)
	}
	if snapshot.Signature == "" {
		t.Fatal("snapshot should be signed")
	}
	if len(snapshot.Items) != 1 || snapshot.Items[0].MetaId != "meta-a" {
		t.Fatalf("snapshot items should come from local reader, got %#v", snapshot.Items)
	}
	if err := VerifySnapshot(snapshot, wantNodeID, mvcTestPublicKeyHex); err != nil {
		t.Fatalf("snapshot signature should verify: %v", err)
	}
}

func TestServiceRoundsSubSecondPresenceTTLUpForSnapshots(t *testing.T) {
	cfg := validServiceFederationConfig()
	cfg.PresenceTTL = 500 * time.Millisecond

	service, err := NewService(
		cfg,
		&serviceLocalReader{},
		WithServicePublisherClient(newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(50_000)})),
	)
	if err != nil {
		t.Fatalf("NewService enabled returned error: %v", err)
	}

	snapshot, err := service.SnapshotProvider().Snapshot()
	if err != nil {
		t.Fatalf("snapshot returned error: %v", err)
	}
	if snapshot.TTLSeconds != 1 {
		t.Fatalf("snapshot TTL seconds should round up to 1 for sub-second TTL, got %d", snapshot.TTLSeconds)
	}
}

func TestServiceInjectedClockDrivesSnapshotGeneratedAt(t *testing.T) {
	now := time.UnixMilli(1234)
	cfg := validServiceFederationConfig()

	service, err := NewService(
		cfg,
		&serviceLocalReader{},
		WithServicePublisherClient(newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(50_000)})),
		WithServiceClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("NewService enabled returned error: %v", err)
	}

	snapshot, err := service.SnapshotProvider().Snapshot()
	if err != nil {
		t.Fatalf("snapshot returned error: %v", err)
	}
	if snapshot.GeneratedAt != now.UnixMilli() {
		t.Fatalf("snapshot generatedAt should use injected clock: want %d got %d", now.UnixMilli(), snapshot.GeneratedAt)
	}
}

func TestServiceInjectedClockDrivesGlobalReaderFreshness(t *testing.T) {
	now := time.UnixMilli(1234)
	cfg := validServiceFederationConfig()

	pullerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now, 90, 1))
	}))
	defer pullerServer.Close()

	service, err := NewService(
		cfg,
		&serviceLocalReader{},
		WithServicePublisherClient(newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(50_000)})),
		WithServicePullerHTTPClient(pullerServer.Client()),
		WithServiceClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("NewService enabled returned error: %v", err)
	}
	service.store.UpsertPeer(pullerRegistryNode("node-a", pullerServer.URL, now.Add(time.Hour)))

	if err := service.puller.PullOnce(context.Background()); err != nil {
		t.Fatalf("PullOnce returned error: %v", err)
	}

	items := service.GlobalReader().OnlineList(nil, 1, 10)
	if len(items) != 1 || items[0].MetaId != "meta-node-a" {
		t.Fatalf("global reader should use injected clock and include pulled remote snapshot, got %#v", items)
	}
}

func TestServiceStartIsIdempotentAndStopCancelsLifecycleLoops(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	cfg := validServiceFederationConfig()

	discoveryRequests := make(chan struct{}, 20)
	manapiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case discoveryRequests <- struct{}{}:
		default:
		}
		writeDiscoveryMANAPIResponse(t, w, nil)
	}))
	defer manapiServer.Close()
	cfg.MANAPIBaseURL = manapiServer.URL + "/pin/path/list?path={protocol-path}&size={size}"

	pullerRequests := make(chan struct{}, 20)
	pullerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case pullerRequests <- struct{}{}:
		default:
		}
		writePullerSnapshot(t, w, pullerSignedSnapshot(t, "node-a", now, 90, 1))
	}))
	defer pullerServer.Close()

	publisherClient := newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(80_000)})
	publisherTickers := newServiceManualTickerFactory()
	discoveryTickers := newServiceManualTickerFactory()
	pullerTickers := newServiceManualTickerFactory()

	service, err := NewService(
		cfg,
		&serviceLocalReader{},
		WithServicePublisherClient(publisherClient),
		WithServiceDiscoveryHTTPClient(manapiServer.Client()),
		WithServicePullerHTTPClient(pullerServer.Client()),
		WithServiceClock(func() time.Time { return now }),
		WithServicePublisherTickerFactory(publisherTickers.NewPublisherTicker),
		WithServiceDiscoveryTickerFactory(discoveryTickers.NewDiscoveryTicker),
		WithServicePullerTickerFactory(pullerTickers.NewPullerTicker),
	)
	if err != nil {
		t.Fatalf("NewService enabled returned error: %v", err)
	}
	service.store.UpsertPeer(pullerRegistryNode("node-a", pullerServer.URL, now.Add(time.Hour)))

	ctx := context.Background()
	service.Start(ctx)
	service.Start(ctx)

	waitServicePublisherUTXORequests(t, publisherClient, 1)
	waitDiscoveryRequests(t, discoveryRequests, 1)
	waitServiceRequests(t, pullerRequests, 1, "puller")

	publisherTicker := publisherTickers.waitTicker(t)
	discoveryTicker := discoveryTickers.waitTicker(t)
	pullerTicker := pullerTickers.waitTicker(t)

	assertServicePublisherUTXORequestsStable(t, publisherClient, 1, 50*time.Millisecond)
	assertNoServiceRequest(t, discoveryRequests, 50*time.Millisecond, "discovery")
	assertNoServiceRequest(t, pullerRequests, 50*time.Millisecond, "puller")

	service.Stop()
	service.Stop()
	publisherTicker.waitStopped(t)
	discoveryTicker.waitStopped(t)
	pullerTicker.waitStopped(t)

	publisherTicker.tick(now.Add(time.Second))
	discoveryTicker.tick(now.Add(time.Second))
	pullerTicker.tick(now.Add(time.Second))

	assertServicePublisherUTXORequestsStable(t, publisherClient, 1, 50*time.Millisecond)
	assertNoServiceRequest(t, discoveryRequests, 50*time.Millisecond, "discovery")
	assertNoServiceRequest(t, pullerRequests, 50*time.Millisecond, "puller")
}

func TestServiceEnabledConfigStillRequiresValidatedFields(t *testing.T) {
	cfg := config.Default()
	cfg.Federation = validServiceFederationConfig()
	cfg.Federation.NodePrivateKey = ""

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate should reject enabled federation without nodePrivateKey")
	}
	if !strings.Contains(err.Error(), "federation.nodePrivateKey") {
		t.Fatalf("Validate error should mention nodePrivateKey, got %v", err)
	}
}

func validServiceFederationConfig() config.FederationConfig {
	cfg := config.Default().Federation
	cfg.Enabled = true
	cfg.Network = "mvc-mainnet"
	cfg.NodePrivateKey = mvcTestPrivateKeyHex
	cfg.PublicBaseURL = "https://node.example"
	cfg.MANAPIBaseURL = "https://manapi.example/pin/path/list?path={protocol-path}&size={size}"
	cfg.MetaletBaseURL = "https://metalet.example"
	cfg.RegistryPath = RegistryPath
	cfg.PresencePath = PresencePath
	cfg.RegistryRenewInterval = time.Hour
	cfg.RegistryValidFor = 2 * time.Hour
	cfg.DiscoveryInterval = time.Minute
	cfg.PresencePullInterval = time.Minute
	cfg.PresenceTTL = 90 * time.Second
	cfg.RequestTimeout = time.Second
	cfg.DefaultScope = "global"
	cfg.AllowInsecureHTTP = true
	cfg.MaxPeers = 10
	cfg.MaxSnapshotBytes = 1 << 20
	return cfg
}

type serviceLocalReader struct {
	entries []presence.OnlineEntry
}

func (r *serviceLocalReader) OnlineEntries() []presence.OnlineEntry {
	return append([]presence.OnlineEntry(nil), r.entries...)
}

func waitServiceRequests(t *testing.T, requests <-chan struct{}, want int, name string) {
	t.Helper()
	for i := 0; i < want; i++ {
		select {
		case <-requests:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s request %d", name, i+1)
		}
	}
}

func assertNoServiceRequest(t *testing.T, requests <-chan struct{}, wait time.Duration, name string) {
	t.Helper()
	select {
	case <-requests:
		t.Fatalf("expected no additional %s request", name)
	case <-time.After(wait):
	}
}

func waitServicePublisherUTXORequests(t *testing.T, client *publisherFakeClient, want int) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		if got := len(client.utxoRequests()); got >= want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for %d publisher UTXO request(s), got %d", want, len(client.utxoRequests()))
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func assertServicePublisherUTXORequestsStable(t *testing.T, client *publisherFakeClient, want int, wait time.Duration) {
	t.Helper()
	deadline := time.After(wait)
	for {
		if got := len(client.utxoRequests()); got != want {
			t.Fatalf("publisher UTXO requests should remain at %d, got %d", want, got)
		}
		select {
		case <-deadline:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

type serviceManualTickerFactory struct {
	mu      sync.Mutex
	tickers []*serviceManualTicker
	notify  chan struct{}
}

func newServiceManualTickerFactory() *serviceManualTickerFactory {
	return &serviceManualTickerFactory{notify: make(chan struct{}, 5)}
}

func (f *serviceManualTickerFactory) NewPublisherTicker(interval time.Duration) PublisherTicker {
	return f.newTicker(interval)
}

func (f *serviceManualTickerFactory) NewDiscoveryTicker(interval time.Duration) DiscoveryTicker {
	return f.newTicker(interval)
}

func (f *serviceManualTickerFactory) NewPullerTicker(interval time.Duration) PullerTicker {
	return f.newTicker(interval)
}

func (f *serviceManualTickerFactory) newTicker(interval time.Duration) *serviceManualTicker {
	ticker := &serviceManualTicker{
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

func (f *serviceManualTickerFactory) waitTicker(t *testing.T) *serviceManualTicker {
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
			t.Fatal("timed out waiting for puller ticker")
		}
	}
}

type serviceManualTicker struct {
	interval time.Duration
	ch       chan time.Time

	mu      sync.Mutex
	stopped bool
}

func (t *serviceManualTicker) Chan() <-chan time.Time {
	return t.ch
}

func (t *serviceManualTicker) Stop() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stopped = true
}

func (t *serviceManualTicker) tick(now time.Time) {
	t.ch <- now
}

func (t *serviceManualTicker) waitStopped(tb testing.TB) {
	tb.Helper()
	deadline := time.After(time.Second)
	for {
		t.mu.Lock()
		stopped := t.stopped
		t.mu.Unlock()
		if stopped {
			return
		}
		select {
		case <-deadline:
			tb.Fatal("timed out waiting for puller ticker to stop")
		case <-time.After(10 * time.Millisecond):
		}
	}
}
