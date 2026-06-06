package federation

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRegistryPayloadBuildsPublicURLsAndValidUntil(t *testing.T) {
	now := time.UnixMilli(1780000000000)
	client := newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(50_000)})
	publisher := newTestPublisher(t, client, PublisherOptions{
		PublicBaseURL: " https://a.com/ ",
		Clock:         func() time.Time { return now },
		ValidFor:      24 * time.Hour,
	})

	if err := publisher.PublishOnce(context.Background(), "create"); err != nil {
		t.Fatalf("PublishOnce returned error: %v", err)
	}

	latest := publisher.Latest()
	if latest.Error != "" {
		t.Fatalf("latest error should be empty after successful publish, got %q", latest.Error)
	}
	if latest.TxID != "txid-1" {
		t.Fatalf("latest txid: want txid-1 got %q", latest.TxID)
	}

	payload := latest.Payload
	if payload.Protocol != ProtocolNode {
		t.Fatalf("protocol: want %q got %q", ProtocolNode, payload.Protocol)
	}
	if payload.Version != Version {
		t.Fatalf("version: want %q got %q", Version, payload.Version)
	}
	if payload.NodeID != "mvc:"+mvcTestAddress {
		t.Fatalf("nodeId: want mvc:%s got %q", mvcTestAddress, payload.NodeID)
	}
	if payload.Network != "mvc-mainnet" {
		t.Fatalf("network: want mvc-mainnet got %q", payload.Network)
	}
	if payload.PublicBaseURL != "https://a.com" {
		t.Fatalf("publicBaseUrl: want https://a.com got %q", payload.PublicBaseURL)
	}
	if payload.SocketURL != "https://a.com/socket/socket.io" {
		t.Fatalf("socketUrl: want derived socket URL got %q", payload.SocketURL)
	}
	if payload.PresenceURL != "https://a.com/.well-known/metaso-p2p/presence" {
		t.Fatalf("presenceUrl: want derived presence URL got %q", payload.PresenceURL)
	}
	if payload.PublicKey != mvcTestPublicKeyHex {
		t.Fatalf("publicKey: want %q got %q", mvcTestPublicKeyHex, payload.PublicKey)
	}
	assertStringSliceEqual(t, payload.Capabilities, []string{"presence-v1"})
	if payload.PublishedAt != now.UnixMilli() {
		t.Fatalf("publishedAt: want %d got %d", now.UnixMilli(), payload.PublishedAt)
	}
	if payload.ValidUntil != now.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("validUntil: want %d got %d", now.Add(24*time.Hour).UnixMilli(), payload.ValidUntil)
	}

	if len(client.utxoRequests()) != 1 {
		t.Fatalf("utxo calls: want 1 got %d", len(client.utxoRequests()))
	}
	utxoReq := client.utxoRequests()[0]
	if utxoReq.net != "livenet" {
		t.Fatalf("utxo net: want livenet got %q", utxoReq.net)
	}
	if utxoReq.address != mvcTestAddress {
		t.Fatalf("utxo address: want %q got %q", mvcTestAddress, utxoReq.address)
	}
	if utxoReq.flag != "" {
		t.Fatalf("utxo flag should be omitted by default, got %q", utxoReq.flag)
	}

	if len(client.broadcastRequests()) != 1 {
		t.Fatalf("broadcast calls: want 1 got %d", len(client.broadcastRequests()))
	}
	broadcast := client.broadcastRequests()[0]
	if broadcast.Chain != "mvc" {
		t.Fatalf("broadcast chain: want mvc got %q", broadcast.Chain)
	}
	if broadcast.Net != "livenet" {
		t.Fatalf("broadcast net: want livenet got %q", broadcast.Net)
	}
	if broadcast.PublicKey != mvcTestPublicKeyHex {
		t.Fatalf("broadcast publicKey: want %q got %q", mvcTestPublicKeyHex, broadcast.PublicKey)
	}
	if strings.TrimSpace(broadcast.RawTx) == "" {
		t.Fatal("broadcast rawTx should be set")
	}
}

func TestPublisherStartPublishesImmediatelyWhenEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(50_000)})
	publisher := newTestPublisher(t, client, PublisherOptions{
		RenewInterval: time.Hour,
	})

	publisher.Start(ctx)

	client.waitBroadcasts(t, 1)
	latest := publisher.Latest()
	if latest.Operation != "create" {
		t.Fatalf("initial operation: want create got %q", latest.Operation)
	}
}

func TestPublisherStartRenewsOnConfiguredInterval(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tickerFactory := newManualPublisherTickerFactory()
	client := newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(80_000)})
	publisher := newTestPublisher(t, client, PublisherOptions{
		RenewInterval: 6 * time.Hour,
		TickerFactory: tickerFactory.NewTicker,
	})

	publisher.Start(ctx)
	client.waitBroadcasts(t, 1)

	ticker := tickerFactory.waitTicker(t)
	if ticker.interval != 6*time.Hour {
		t.Fatalf("renew ticker interval: want 6h got %s", ticker.interval)
	}
	ticker.tick(time.UnixMilli(1780000001000))

	client.waitBroadcasts(t, 2)
	latest := publisher.Latest()
	if latest.Operation != "modify" {
		t.Fatalf("renew operation: want modify got %q", latest.Operation)
	}
	if latest.TxID != "txid-2" {
		t.Fatalf("renew txid: want txid-2 got %q", latest.TxID)
	}
}

func TestPublisherDisabledDoesNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(50_000)})
	publisher, err := NewPublisher(PublisherOptions{
		Enabled: false,
		Client:  client,
	})
	if err != nil {
		t.Fatalf("NewPublisher disabled returned error: %v", err)
	}

	publisher.Start(ctx)

	if err := publisher.PublishOnce(ctx, "create"); !errors.Is(err, ErrPublisherDisabled) {
		t.Fatalf("disabled PublishOnce error: want ErrPublisherDisabled got %v", err)
	}
	if got := len(client.utxoRequests()); got != 0 {
		t.Fatalf("disabled publisher should not request UTXOs, got %d call(s)", got)
	}
	if got := len(client.broadcastRequests()); got != 0 {
		t.Fatalf("disabled publisher should not broadcast, got %d call(s)", got)
	}
}

func TestPublisherHandlesInsufficientUTXOWithoutCrashing(t *testing.T) {
	client := newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(2)})
	publisher := newTestPublisher(t, client, PublisherOptions{
		FeeRate:    10,
		DustAmount: 1,
	})

	err := publisher.PublishOnce(context.Background(), "create")
	if err == nil {
		t.Fatal("expected insufficient UTXO error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "insufficient") {
		t.Fatalf("error should mention insufficient funds, got %v", err)
	}
	latest := publisher.Latest()
	if !strings.Contains(strings.ToLower(latest.Error), "insufficient") {
		t.Fatalf("latest error should store insufficient funds, got %q", latest.Error)
	}
	if len(client.broadcastRequests()) != 0 {
		t.Fatalf("insufficient UTXO should not broadcast, got %d call(s)", len(client.broadcastRequests()))
	}

	client.setUTXOs([]MVCUTXO{publisherTestUTXO(80_000)})
	if err := publisher.PublishOnce(context.Background(), "modify"); err != nil {
		t.Fatalf("publisher should retry successfully after UTXOs are available: %v", err)
	}
	if latest := publisher.Latest(); latest.Error != "" || latest.TxID != "txid-1" {
		t.Fatalf("latest after retry should have txid and no error, got %#v", latest)
	}
}

func TestPublisherSerializesPublishCallsWithInProcessLock(t *testing.T) {
	client := newPublisherFakeClient([]MVCUTXO{publisherTestUTXO(100_000)})
	client.blockUTXO = make(chan struct{})
	publisher := newTestPublisher(t, client, PublisherOptions{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- publisher.PublishOnce(ctx, "create")
	}()
	client.waitUTXOCalls(t, 1)

	secondDone := make(chan error, 1)
	go func() {
		secondDone <- publisher.PublishOnce(ctx, "modify")
	}()

	client.assertNoAdditionalUTXOCall(t, 20*time.Millisecond)
	close(client.blockUTXO)

	if err := <-firstDone; err != nil {
		t.Fatalf("first PublishOnce returned error: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second PublishOnce returned error: %v", err)
	}
	if client.maxConcurrentUTXOCalls() != 1 {
		t.Fatalf("UTXO calls should be serialized, max concurrent=%d", client.maxConcurrentUTXOCalls())
	}
	if len(client.broadcastRequests()) != 2 {
		t.Fatalf("serialized publishes should both complete, broadcasts=%d", len(client.broadcastRequests()))
	}
}

func newTestPublisher(t *testing.T, client *publisherFakeClient, overrides PublisherOptions) *Publisher {
	t.Helper()

	opts := PublisherOptions{
		Enabled:        true,
		Network:        "mvc-mainnet",
		NodePrivateKey: mvcTestPrivateKeyHex,
		PublicBaseURL:  "https://a.com",
		RegistryPath:   RegistryPath,
		PresencePath:   PresencePath,
		ValidFor:       24 * time.Hour,
		RenewInterval:  time.Hour,
		FeeRate:        1,
		DustAmount:     1,
		Client:         client,
		Clock:          func() time.Time { return time.UnixMilli(1780000000000) },
	}
	if overrides.Network != "" {
		opts.Network = overrides.Network
	}
	if overrides.NodePrivateKey != "" {
		opts.NodePrivateKey = overrides.NodePrivateKey
	}
	if overrides.PublicBaseURL != "" {
		opts.PublicBaseURL = overrides.PublicBaseURL
	}
	if overrides.RegistryPath != "" {
		opts.RegistryPath = overrides.RegistryPath
	}
	if overrides.PresencePath != "" {
		opts.PresencePath = overrides.PresencePath
	}
	if overrides.ValidFor != 0 {
		opts.ValidFor = overrides.ValidFor
	}
	if overrides.RenewInterval != 0 {
		opts.RenewInterval = overrides.RenewInterval
	}
	if overrides.FeeRate != 0 {
		opts.FeeRate = overrides.FeeRate
	}
	if overrides.DustAmount != 0 {
		opts.DustAmount = overrides.DustAmount
	}
	if overrides.Client != nil {
		opts.Client = overrides.Client
	}
	if overrides.Clock != nil {
		opts.Clock = overrides.Clock
	}
	if overrides.TickerFactory != nil {
		opts.TickerFactory = overrides.TickerFactory
	}

	publisher, err := NewPublisher(opts)
	if err != nil {
		t.Fatalf("NewPublisher returned error: %v", err)
	}
	return publisher
}

func publisherTestUTXO(value int64) MVCUTXO {
	return MVCUTXO{
		TxID:     "1111111111111111111111111111111111111111111111111111111111111111",
		OutIndex: 0,
		Value:    value,
		Address:  mvcTestAddress,
		Height:   100,
		Flag:     "confirmed",
	}
}

type publisherUTXORequest struct {
	net     string
	address string
	flag    string
}

type publisherFakeClient struct {
	mu                  sync.Mutex
	utxos               []MVCUTXO
	utxoRequestsMade    []publisherUTXORequest
	broadcasts          []MVCBroadcastRequest
	broadcastNotify     chan struct{}
	utxoNotify          chan struct{}
	blockUTXO           chan struct{}
	concurrentUTXOCalls int
	maxConcurrentUTXOs  int
}

func newPublisherFakeClient(utxos []MVCUTXO) *publisherFakeClient {
	return &publisherFakeClient{
		utxos:           append([]MVCUTXO(nil), utxos...),
		broadcastNotify: make(chan struct{}, 20),
		utxoNotify:      make(chan struct{}, 20),
	}
}

func (c *publisherFakeClient) MVCAddressUTXOs(ctx context.Context, net string, address string, flag string) ([]MVCUTXO, error) {
	c.mu.Lock()
	c.concurrentUTXOCalls++
	if c.concurrentUTXOCalls > c.maxConcurrentUTXOs {
		c.maxConcurrentUTXOs = c.concurrentUTXOCalls
	}
	c.utxoRequestsMade = append(c.utxoRequestsMade, publisherUTXORequest{
		net:     net,
		address: address,
		flag:    flag,
	})
	utxos := append([]MVCUTXO(nil), c.utxos...)
	c.mu.Unlock()

	select {
	case c.utxoNotify <- struct{}{}:
	default:
	}

	if c.blockUTXO != nil {
		select {
		case <-ctx.Done():
			c.finishUTXOCall()
			return nil, ctx.Err()
		case <-c.blockUTXO:
		}
	}

	c.finishUTXOCall()
	return utxos, nil
}

func (c *publisherFakeClient) BroadcastMVC(ctx context.Context, req MVCBroadcastRequest) (string, error) {
	c.mu.Lock()
	c.broadcasts = append(c.broadcasts, req)
	txid := "txid-" + strconv.Itoa(len(c.broadcasts))
	c.mu.Unlock()

	select {
	case c.broadcastNotify <- struct{}{}:
	default:
	}
	return txid, nil
}

func (c *publisherFakeClient) finishUTXOCall() {
	c.mu.Lock()
	c.concurrentUTXOCalls--
	c.mu.Unlock()
}

func (c *publisherFakeClient) setUTXOs(utxos []MVCUTXO) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.utxos = append([]MVCUTXO(nil), utxos...)
}

func (c *publisherFakeClient) utxoRequests() []publisherUTXORequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]publisherUTXORequest(nil), c.utxoRequestsMade...)
}

func (c *publisherFakeClient) broadcastRequests() []MVCBroadcastRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]MVCBroadcastRequest(nil), c.broadcasts...)
}

func (c *publisherFakeClient) maxConcurrentUTXOCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxConcurrentUTXOs
}

func (c *publisherFakeClient) waitBroadcasts(t *testing.T, want int) {
	t.Helper()
	for len(c.broadcastRequests()) < want {
		select {
		case <-c.broadcastNotify:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %d broadcasts, got %d", want, len(c.broadcastRequests()))
		}
	}
}

func (c *publisherFakeClient) waitUTXOCalls(t *testing.T, want int) {
	t.Helper()
	for len(c.utxoRequests()) < want {
		select {
		case <-c.utxoNotify:
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %d UTXO calls, got %d", want, len(c.utxoRequests()))
		}
	}
}

func (c *publisherFakeClient) assertNoAdditionalUTXOCall(t *testing.T, wait time.Duration) {
	t.Helper()
	current := len(c.utxoRequests())
	select {
	case <-c.utxoNotify:
		t.Fatalf("expected publisher lock to block second UTXO call, got %d calls", len(c.utxoRequests()))
	case <-time.After(wait):
		if got := len(c.utxoRequests()); got != current {
			t.Fatalf("expected %d UTXO calls during lock wait, got %d", current, got)
		}
	}
}

type manualPublisherTickerFactory struct {
	mu      sync.Mutex
	tickers []*manualPublisherTicker
	notify  chan struct{}
}

func newManualPublisherTickerFactory() *manualPublisherTickerFactory {
	return &manualPublisherTickerFactory{notify: make(chan struct{}, 5)}
}

func (f *manualPublisherTickerFactory) NewTicker(interval time.Duration) PublisherTicker {
	ticker := &manualPublisherTicker{
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

func (f *manualPublisherTickerFactory) waitTicker(t *testing.T) *manualPublisherTicker {
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
			t.Fatal("timed out waiting for publisher ticker")
		}
	}
}

type manualPublisherTicker struct {
	interval time.Duration
	ch       chan time.Time
	stopped  bool
}

func (t *manualPublisherTicker) Chan() <-chan time.Time {
	return t.ch
}

func (t *manualPublisherTicker) Stop() {
	t.stopped = true
}

func (t *manualPublisherTicker) tick(now time.Time) {
	t.ch <- now
}
