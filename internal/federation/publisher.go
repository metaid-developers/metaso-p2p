package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	defaultPublisherRenewInterval = 6 * time.Hour
	defaultPublisherValidFor      = 24 * time.Hour
	defaultPublisherFeeRate       = int64(1)
	defaultPublisherDustAmount    = int64(1)
	defaultPublisherUTXOFlag      = "confirmed"
	publisherPresenceCapability   = "presence-v1"
)

// ErrPublisherDisabled is returned by PublishOnce when federation publishing is disabled.
var ErrPublisherDisabled = errors.New("federation publisher is disabled")

// PublisherClient is the wallet API surface used by registry publishing.
type PublisherClient interface {
	MVCAddressUTXOs(ctx context.Context, net string, address string, flag string) ([]MVCUTXO, error)
	BroadcastMVC(ctx context.Context, req MVCBroadcastRequest) (string, error)
}

// PublisherTicker is the renew-loop ticker abstraction used for deterministic tests.
type PublisherTicker interface {
	Chan() <-chan time.Time
	Stop()
}

// PublisherTickerFactory creates a ticker for the configured renew interval.
type PublisherTickerFactory func(interval time.Duration) PublisherTicker

// PublisherOptions configures registry publishing.
type PublisherOptions struct {
	Enabled        bool
	Network        string
	NodePrivateKey string
	PublicBaseURL  string
	RegistryPath   string
	PresencePath   string
	ValidFor       time.Duration
	RenewInterval  time.Duration
	FeeRate        int64
	DustAmount     int64
	UTXOFlag       string
	Client         PublisherClient
	Clock          func() time.Time
	TickerFactory  PublisherTickerFactory
}

// PublisherLatest is the in-memory status of the last publish attempt.
type PublisherLatest struct {
	TxID      string
	Operation string
	Payload   RegistryPayload
	Error     string
}

// Publisher publishes this node's registry payload and renews it on an interval.
type Publisher struct {
	enabled        bool
	network        string
	metaletNet     string
	nodePrivateKey string
	publicBaseURL  string
	registryPath   string
	presencePath   string
	validFor       time.Duration
	renewInterval  time.Duration
	feeRate        int64
	dustAmount     int64
	utxoFlag       string
	client         PublisherClient
	clock          func() time.Time
	tickerFactory  PublisherTickerFactory
	address        string
	publicKey      string

	publishMu sync.Mutex
	latestMu  sync.RWMutex
	latest    PublisherLatest
}

// NewPublisher creates a registry publisher from service-level federation settings.
func NewPublisher(opts PublisherOptions) (*Publisher, error) {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	tickerFactory := opts.TickerFactory
	if tickerFactory == nil {
		tickerFactory = newRealPublisherTicker
	}
	network, err := normalizePublisherNetwork(opts.Network)
	if err != nil {
		return nil, err
	}

	publisher := &Publisher{
		enabled:        opts.Enabled,
		network:        network,
		metaletNet:     publisherMetaletNet(network),
		nodePrivateKey: strings.TrimSpace(opts.NodePrivateKey),
		registryPath:   normalizedPublisherPath(opts.RegistryPath, RegistryPath),
		presencePath:   normalizedPublisherPath(opts.PresencePath, PresencePath),
		validFor:       opts.ValidFor,
		renewInterval:  opts.RenewInterval,
		feeRate:        opts.FeeRate,
		dustAmount:     opts.DustAmount,
		utxoFlag:       opts.UTXOFlag,
		client:         opts.Client,
		clock:          clock,
		tickerFactory:  tickerFactory,
	}
	if publisher.validFor <= 0 {
		publisher.validFor = defaultPublisherValidFor
	}
	if publisher.renewInterval <= 0 {
		publisher.renewInterval = defaultPublisherRenewInterval
	}
	if publisher.feeRate <= 0 {
		publisher.feeRate = defaultPublisherFeeRate
	}
	if publisher.dustAmount <= 0 {
		publisher.dustAmount = defaultPublisherDustAmount
	}
	if strings.TrimSpace(publisher.utxoFlag) == "" {
		publisher.utxoFlag = defaultPublisherUTXOFlag
	}

	if !publisher.enabled {
		return publisher, nil
	}
	if publisher.client == nil {
		return nil, errors.New("federation publisher requires a client")
	}
	publicBaseURL, err := normalizePublisherBaseURL(opts.PublicBaseURL)
	if err != nil {
		return nil, err
	}
	publisher.publicBaseURL = publicBaseURL
	if publisher.nodePrivateKey == "" {
		return nil, errors.New("federation publisher requires node private key")
	}
	address, publicKey, err := MVCIdentityFromPrivateKey(publisher.nodePrivateKey, publisher.network)
	if err != nil {
		return nil, fmt.Errorf("derive publisher identity: %w", err)
	}
	publisher.address = address
	publisher.publicKey = publicKey
	return publisher, nil
}

// PublishOnce publishes one create/modify/revoke registry operation.
func (p *Publisher) PublishOnce(ctx context.Context, operation string) error {
	if p == nil || !p.enabled {
		return ErrPublisherDisabled
	}
	ctx = publisherContext(ctx)

	p.publishMu.Lock()
	defer p.publishMu.Unlock()

	now := p.clock()
	payload := p.registryPayload(now)
	content, err := json.Marshal(payload)
	if err != nil {
		return p.storePublishError(operation, payload, fmt.Errorf("encode registry payload: %w", err))
	}

	utxos, err := p.client.MVCAddressUTXOs(ctx, p.metaletNet, p.address, p.utxoFlag)
	if err != nil {
		return p.storePublishError(operation, payload, fmt.Errorf("fetch MVC UTXOs: %w", err))
	}
	registryUTXOs, err := publisherRegistryUTXOs(utxos)
	if err != nil {
		return p.storePublishError(operation, payload, err)
	}

	result, err := BuildMVCRegistryTx(MVCRegistryTxRequest{
		Net:           p.network,
		PrivateKeyHex: p.nodePrivateKey,
		UTXOs:         registryUTXOs,
		Operation:     operation,
		Path:          p.registryPath,
		Version:       Version,
		Content:       content,
		FeeRate:       p.feeRate,
		DustAmount:    p.dustAmount,
	})
	if err != nil {
		return p.storePublishError(operation, payload, fmt.Errorf("build MVC registry tx: %w", err))
	}

	txid, err := p.client.BroadcastMVC(ctx, MVCBroadcastRequest{
		Chain:     "mvc",
		Net:       p.metaletNet,
		PublicKey: p.publicKey,
		RawTx:     result.RawTx,
	})
	if err != nil {
		return p.storePublishError(operation, payload, fmt.Errorf("broadcast MVC registry tx: %w", err))
	}

	p.latestMu.Lock()
	p.latest = PublisherLatest{
		TxID:      strings.TrimSpace(txid),
		Operation: strings.TrimSpace(operation),
		Payload:   cloneRegistryPayload(payload),
	}
	p.latestMu.Unlock()
	return nil
}

// Start runs the publisher renew loop in the background.
func (p *Publisher) Start(ctx context.Context) {
	if p == nil || !p.enabled {
		return
	}
	ctx = publisherContext(ctx)
	go p.run(ctx)
}

// Latest returns a snapshot of the latest in-memory publish metadata.
func (p *Publisher) Latest() PublisherLatest {
	if p == nil {
		return PublisherLatest{}
	}
	p.latestMu.RLock()
	defer p.latestMu.RUnlock()
	latest := p.latest
	latest.Payload = cloneRegistryPayload(latest.Payload)
	return latest
}

func (p *Publisher) run(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	_ = p.PublishOnce(ctx, "create")

	ticker := p.tickerFactory(p.renewInterval)
	if ticker == nil {
		return
	}
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			_ = p.PublishOnce(ctx, "modify")
		}
	}
}

func (p *Publisher) registryPayload(now time.Time) RegistryPayload {
	return RegistryPayload{
		Protocol:      ProtocolNode,
		Version:       Version,
		NodeID:        "mvc:" + p.address,
		Network:       p.network,
		PublicBaseURL: p.publicBaseURL,
		SocketURL:     publisherJoinURL(p.publicBaseURL, "/socket/socket.io"),
		PresenceURL:   publisherJoinURL(p.publicBaseURL, p.presencePath),
		PublicKey:     p.publicKey,
		Capabilities:  []string{publisherPresenceCapability},
		PublishedAt:   now.UnixMilli(),
		ValidUntil:    now.Add(p.validFor).UnixMilli(),
	}
}

func (p *Publisher) storePublishError(operation string, payload RegistryPayload, err error) error {
	p.latestMu.Lock()
	p.latest = PublisherLatest{
		TxID:      p.latest.TxID,
		Operation: strings.TrimSpace(operation),
		Payload:   cloneRegistryPayload(payload),
		Error:     err.Error(),
	}
	p.latestMu.Unlock()
	return err
}

func publisherRegistryUTXOs(utxos []MVCUTXO) ([]MVCRegistryUTXO, error) {
	if len(utxos) == 0 {
		return nil, errors.New("insufficient UTXO value: no MVC UTXOs available")
	}
	registryUTXOs := make([]MVCRegistryUTXO, 0, len(utxos))
	for i, utxo := range utxos {
		if strings.TrimSpace(utxo.TxID) == "" {
			return nil, fmt.Errorf("utxo %d txid is required", i)
		}
		if utxo.OutIndex < 0 {
			return nil, fmt.Errorf("utxo %d outIndex must be non-negative", i)
		}
		if utxo.OutIndex > math.MaxUint32 {
			return nil, fmt.Errorf("utxo %d outIndex exceeds uint32", i)
		}
		registryUTXOs = append(registryUTXOs, MVCRegistryUTXO{
			TxID:     strings.TrimSpace(utxo.TxID),
			OutIndex: uint32(utxo.OutIndex),
			Value:    utxo.Value,
			Address:  strings.TrimSpace(utxo.Address),
		})
	}
	return registryUTXOs, nil
}

func normalizePublisherNetwork(network string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(network)) {
	case "", "mvc-mainnet", "mainnet", "mvc", "livenet", "0":
		return "mvc-mainnet", nil
	case "mvc-testnet", "testnet", "testnet3", "1":
		return "mvc-testnet", nil
	case "mvc-regtest", "regtest", "regression", "2":
		return "mvc-regtest", nil
	default:
		return "", fmt.Errorf("unsupported publisher network %q", network)
	}
}

func publisherMetaletNet(network string) string {
	// Metalet wallet APIs use livenet/testnet while registry payloads use MVC labels.
	if network == "mvc-mainnet" {
		return "livenet"
	}
	return "testnet"
}

func normalizePublisherBaseURL(baseURL string) (string, error) {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		return "", errors.New("federation publisher requires public base URL")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse public base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("public base URL requires scheme and host")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("public base URL must not include query or fragment")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func normalizedPublisherPath(pathValue string, defaultPath string) string {
	pathValue = strings.TrimSpace(pathValue)
	if pathValue == "" {
		return defaultPath
	}
	if !strings.HasPrefix(pathValue, "/") {
		pathValue = "/" + pathValue
	}
	return pathValue
}

func publisherJoinURL(baseURL string, pathValue string) string {
	return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(pathValue, "/")
}

func cloneRegistryPayload(payload RegistryPayload) RegistryPayload {
	payload.Capabilities = append([]string(nil), payload.Capabilities...)
	return payload
}

func publisherContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type realPublisherTicker struct {
	ticker *time.Ticker
}

func newRealPublisherTicker(interval time.Duration) PublisherTicker {
	return &realPublisherTicker{ticker: time.NewTicker(interval)}
}

func (t *realPublisherTicker) Chan() <-chan time.Time {
	return t.ticker.C
}

func (t *realPublisherTicker) Stop() {
	t.ticker.Stop()
}
