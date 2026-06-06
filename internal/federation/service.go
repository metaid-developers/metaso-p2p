package federation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

// Service wires federation components into the metaso-p2p lifecycle.
type Service struct {
	enabled bool
	nodeID  string

	store            *Store
	snapshotProvider presence.SnapshotProvider
	publisher        *Publisher
	discovery        *Discovery
	puller           *Puller

	mu     sync.Mutex
	cancel context.CancelFunc
}

type serviceOptions struct {
	publisherClient        PublisherClient
	discoveryHTTPClient    *http.Client
	pullerHTTPClient       *http.Client
	clock                  func() time.Time
	publisherTickerFactory PublisherTickerFactory
	discoveryTickerFactory DiscoveryTickerFactory
	pullerTickerFactory    PullerTickerFactory
}

// ServiceOption customizes federation service construction.
type ServiceOption func(*serviceOptions)

// WithServicePublisherClient injects the wallet client used by registry publishing.
func WithServicePublisherClient(client PublisherClient) ServiceOption {
	return func(opts *serviceOptions) {
		opts.publisherClient = client
	}
}

// WithServiceDiscoveryHTTPClient injects the HTTP client used by MANAPI discovery.
func WithServiceDiscoveryHTTPClient(client *http.Client) ServiceOption {
	return func(opts *serviceOptions) {
		if client != nil {
			opts.discoveryHTTPClient = client
		}
	}
}

// WithServicePullerHTTPClient injects the HTTP client used by remote presence pulls.
func WithServicePullerHTTPClient(client *http.Client) ServiceOption {
	return func(opts *serviceOptions) {
		if client != nil {
			opts.pullerHTTPClient = client
		}
	}
}

// WithServiceClock injects a shared clock for deterministic federation components.
func WithServiceClock(clock func() time.Time) ServiceOption {
	return func(opts *serviceOptions) {
		if clock != nil {
			opts.clock = clock
		}
	}
}

// WithServicePublisherTickerFactory injects the publisher renew-loop ticker.
func WithServicePublisherTickerFactory(factory PublisherTickerFactory) ServiceOption {
	return func(opts *serviceOptions) {
		if factory != nil {
			opts.publisherTickerFactory = factory
		}
	}
}

// WithServiceDiscoveryTickerFactory injects the MANAPI discovery-loop ticker.
func WithServiceDiscoveryTickerFactory(factory DiscoveryTickerFactory) ServiceOption {
	return func(opts *serviceOptions) {
		if factory != nil {
			opts.discoveryTickerFactory = factory
		}
	}
}

// WithServicePullerTickerFactory injects the remote presence pull-loop ticker.
func WithServicePullerTickerFactory(factory PullerTickerFactory) ServiceOption {
	return func(opts *serviceOptions) {
		if factory != nil {
			opts.pullerTickerFactory = factory
		}
	}
}

// NewService creates the federation lifecycle coordinator.
func NewService(cfg config.FederationConfig, local presence.LocalReader, opts ...ServiceOption) (*Service, error) {
	options := serviceOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}

	service := &Service{enabled: cfg.Enabled}
	if !cfg.Enabled {
		return service, nil
	}
	if local == nil {
		return nil, errors.New("federation service requires a local presence reader when enabled")
	}

	network, err := normalizePublisherNetwork(cfg.Network)
	if err != nil {
		return nil, fmt.Errorf("normalize federation network: %w", err)
	}
	address, _, err := MVCIdentityFromPrivateKey(cfg.NodePrivateKey, network)
	if err != nil {
		return nil, fmt.Errorf("derive federation node identity: %w", err)
	}
	nodeID := "mvc:" + address

	store := NewStore(
		nodeID,
		WithStoreEnabled(true),
		WithStoreDefaultScope(cfg.DefaultScope),
		WithStoreClock(options.clock),
	)
	ttlSeconds := servicePresenceTTLSeconds(cfg.PresenceTTL)
	snapshotProvider := NewSnapshotBuilder(
		local,
		nodeID,
		ttlSeconds,
		WithSnapshotSigningKey(cfg.NodePrivateKey),
		WithClock(options.clock),
	)

	publisherClient := options.publisherClient
	if publisherClient == nil {
		metaletClient, err := NewMetaletClient(cfg.MetaletBaseURL, WithMetaletTimeout(cfg.RequestTimeout))
		if err != nil {
			return nil, fmt.Errorf("create federation metalet client: %w", err)
		}
		publisherClient = metaletClient
	}

	publisher, err := NewPublisher(PublisherOptions{
		Enabled:        true,
		Network:        network,
		NodePrivateKey: cfg.NodePrivateKey,
		PublicBaseURL:  cfg.PublicBaseURL,
		RegistryPath:   cfg.RegistryPath,
		PresencePath:   cfg.PresencePath,
		ValidFor:       cfg.RegistryValidFor,
		RenewInterval:  cfg.RegistryRenewInterval,
		Client:         publisherClient,
		Clock:          options.clock,
		TickerFactory:  options.publisherTickerFactory,
	})
	if err != nil {
		return nil, fmt.Errorf("create federation publisher: %w", err)
	}

	discovery, err := NewDiscovery(DiscoveryOptions{
		MANAPIBaseURL:     cfg.MANAPIBaseURL,
		RegistryPath:      cfg.RegistryPath,
		SelfNodeID:        nodeID,
		Network:           network,
		AllowInsecureHTTP: cfg.AllowInsecureHTTP,
		MaxPeers:          cfg.MaxPeers,
		RequestTimeout:    cfg.RequestTimeout,
		Store:             store,
		HTTPClient:        options.discoveryHTTPClient,
		Clock:             options.clock,
		Interval:          cfg.DiscoveryInterval,
		TickerFactory:     options.discoveryTickerFactory,
	})
	if err != nil {
		return nil, fmt.Errorf("create federation discovery: %w", err)
	}

	puller, err := NewPuller(PullerOptions{
		Store:            store,
		SelfNodeID:       nodeID,
		HTTPClient:       options.pullerHTTPClient,
		RequestTimeout:   cfg.RequestTimeout,
		MaxSnapshotBytes: cfg.MaxSnapshotBytes,
		Interval:         cfg.PresencePullInterval,
		Clock:            options.clock,
		TickerFactory:    options.pullerTickerFactory,
	})
	if err != nil {
		return nil, fmt.Errorf("create federation puller: %w", err)
	}

	service.nodeID = nodeID
	service.store = store
	service.snapshotProvider = snapshotProvider
	service.publisher = publisher
	service.discovery = discovery
	service.puller = puller
	return service, nil
}

// Enabled reports whether federation is enabled.
func (s *Service) Enabled() bool {
	return s != nil && s.enabled
}

// NodeID returns this node's stable federation node ID.
func (s *Service) NodeID() string {
	if s == nil {
		return ""
	}
	return s.nodeID
}

// SnapshotProvider returns the signed local presence snapshot provider.
func (s *Service) SnapshotProvider() presence.SnapshotProvider {
	if s == nil || !s.enabled {
		return nil
	}
	return s.snapshotProvider
}

// GlobalReader returns the global presence reader/store.
func (s *Service) GlobalReader() presence.GlobalReader {
	if s == nil || !s.enabled {
		return nil
	}
	return s.store
}

// Start launches federation background loops. Calling Start while already started is a no-op.
func (s *Service) Start(ctx context.Context) {
	if s == nil || !s.enabled {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.mu.Lock()
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	publisher := s.publisher
	discovery := s.discovery
	puller := s.puller
	s.mu.Unlock()

	if publisher != nil {
		publisher.Start(runCtx)
	}
	if discovery != nil {
		discovery.Start(runCtx)
	}
	if puller != nil {
		puller.Start(runCtx)
	}
}

// Stop cancels federation background loops. It is safe to call more than once.
func (s *Service) Stop() {
	if s == nil {
		return
	}

	s.mu.Lock()
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()

	if cancel != nil {
		cancel()
	}
}

func servicePresenceTTLSeconds(ttl time.Duration) int64 {
	if ttl <= 0 {
		return 0
	}
	seconds := ttl / time.Second
	if ttl%time.Second != 0 {
		seconds++
	}
	if seconds <= 0 {
		return 1
	}
	return int64(seconds)
}
