package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

const (
	defaultPullerRequestTimeout   = 3 * time.Second
	defaultPullerMaxSnapshotBytes = 1 << 20
	defaultPullerInterval         = 20 * time.Second
	defaultPullerBackoffBase      = time.Second
	defaultPullerBackoffMax       = time.Minute
)

// PullerTicker is the pull-loop ticker abstraction used for deterministic tests.
type PullerTicker interface {
	Chan() <-chan time.Time
	Stop()
}

// PullerTickerFactory creates a ticker for the configured pull interval.
type PullerTickerFactory func(interval time.Duration) PullerTicker

// PullerOptions configures remote presence snapshot pulls.
type PullerOptions struct {
	Store            *Store
	SelfNodeID       string
	HTTPClient       *http.Client
	RequestTimeout   time.Duration
	MaxSnapshotBytes int
	Interval         time.Duration
	Clock            func() time.Time
	TickerFactory    PullerTickerFactory
	BackoffBase      time.Duration
	BackoffMax       time.Duration
}

// Puller fetches and verifies remote presence snapshots from active peers.
type Puller struct {
	store            *Store
	selfNodeID       string
	httpClient       *http.Client
	requestTimeout   time.Duration
	maxSnapshotBytes int
	interval         time.Duration
	clock            func() time.Time
	tickerFactory    PullerTickerFactory
	backoffBase      time.Duration
	backoffMax       time.Duration

	mu       sync.Mutex
	failures map[string]pullerPeerFailure
}

type pullerPeerFailure struct {
	count              int
	nextEligiblePullAt time.Time
}

// NewPuller creates a remote presence snapshot puller.
func NewPuller(opts PullerOptions) (*Puller, error) {
	if opts.Store == nil {
		return nil, errors.New("federation puller requires a store")
	}

	selfNodeID := strings.TrimSpace(opts.SelfNodeID)
	if selfNodeID == "" {
		selfNodeID = opts.Store.localNodeID
	}
	if selfNodeID == "" {
		selfNodeID = defaultLocalNodeID
	}

	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	requestTimeout := opts.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultPullerRequestTimeout
	}
	maxSnapshotBytes := opts.MaxSnapshotBytes
	if maxSnapshotBytes <= 0 {
		maxSnapshotBytes = defaultPullerMaxSnapshotBytes
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultPullerInterval
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	tickerFactory := opts.TickerFactory
	if tickerFactory == nil {
		tickerFactory = newRealPullerTicker
	}
	backoffBase := opts.BackoffBase
	if backoffBase <= 0 {
		backoffBase = defaultPullerBackoffBase
	}
	backoffMax := opts.BackoffMax
	if backoffMax <= 0 {
		backoffMax = defaultPullerBackoffMax
	}
	if backoffMax < backoffBase {
		backoffMax = backoffBase
	}

	return &Puller{
		store:            opts.Store,
		selfNodeID:       selfNodeID,
		httpClient:       httpClient,
		requestTimeout:   requestTimeout,
		maxSnapshotBytes: maxSnapshotBytes,
		interval:         interval,
		clock:            clock,
		tickerFactory:    tickerFactory,
		backoffBase:      backoffBase,
		backoffMax:       backoffMax,
		failures:         make(map[string]pullerPeerFailure),
	}, nil
}

// PullOnce fetches snapshots for all currently eligible active peers.
func (p *Puller) PullOnce(ctx context.Context) error {
	if p == nil {
		return errors.New("federation puller is nil")
	}

	ctx = pullerContext(ctx)
	peers := p.store.ActivePeers(p.clock())

	errs := make([]error, 0)
	for _, peer := range peers {
		if peer.NodeID == "" || peer.NodeID == p.selfNodeID {
			continue
		}
		if peerNow := p.clock(); !p.peerEligible(peer.NodeID, peerNow) {
			continue
		}
		if err := p.pullPeer(ctx, peer); err != nil {
			p.recordFailure(peer.NodeID, p.clock())
			errs = append(errs, fmt.Errorf("pull presence snapshot for %s: %w", peer.NodeID, err))
			continue
		}
		p.clearFailure(peer.NodeID)
	}
	return errors.Join(errs...)
}

// Start runs an immediate pull, then pulls on the configured interval.
func (p *Puller) Start(ctx context.Context) {
	if p == nil {
		return
	}
	ctx = pullerContext(ctx)
	go p.run(ctx)
}

func (p *Puller) run(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	_ = p.PullOnce(ctx)

	ticker := p.tickerFactory(p.interval)
	if ticker == nil {
		return
	}
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			_ = p.PullOnce(ctx)
		}
	}
}

func (p *Puller) pullPeer(ctx context.Context, peer RegistryNode) error {
	presenceURL := strings.TrimSpace(peer.PresenceURL)
	if presenceURL == "" {
		return errors.New("peer presenceUrl is required")
	}

	requestCtx := ctx
	if p.requestTimeout > 0 {
		var cancel context.CancelFunc
		requestCtx, cancel = context.WithTimeout(ctx, p.requestTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, presenceURL, nil)
	if err != nil {
		return fmt.Errorf("create presence snapshot request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch presence snapshot: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("presence snapshot returned HTTP %d", resp.StatusCode)
	}

	snapshot, err := p.decodeSnapshot(resp.Body)
	if err != nil {
		return err
	}
	now := p.clock()
	if err := p.validateSnapshot(snapshot, peer, now); err != nil {
		return err
	}
	p.store.UpsertSnapshot(snapshot)
	return nil
}

func (p *Puller) decodeSnapshot(body io.Reader) (presence.Snapshot, error) {
	limited := io.LimitReader(body, int64(p.maxSnapshotBytes)+1)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return presence.Snapshot{}, fmt.Errorf("read presence snapshot body: %w", err)
	}
	if len(raw) > p.maxSnapshotBytes {
		return presence.Snapshot{}, fmt.Errorf("presence snapshot body exceeds %d bytes", p.maxSnapshotBytes)
	}

	var snapshot presence.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return presence.Snapshot{}, fmt.Errorf("decode presence snapshot: %w", err)
	}
	return snapshot, nil
}

func (p *Puller) validateSnapshot(snapshot presence.Snapshot, peer RegistryNode, now time.Time) error {
	if snapshot.Protocol != ProtocolPresence {
		return fmt.Errorf("unsupported presence snapshot protocol %q", snapshot.Protocol)
	}
	if strings.TrimSpace(snapshot.Version) != Version {
		return fmt.Errorf("unsupported presence snapshot version %q", snapshot.Version)
	}
	if snapshot.GeneratedAt <= 0 {
		return errors.New("presence snapshot generatedAt is required")
	}
	if snapshot.TTLSeconds <= 0 {
		return errors.New("presence snapshot ttlSeconds must be positive")
	}
	if snapshotExpired(snapshot, now) {
		return fmt.Errorf("stale presence snapshot generatedAt=%d ttlSeconds=%d", snapshot.GeneratedAt, snapshot.TTLSeconds)
	}
	if err := VerifySnapshot(&snapshot, peer.NodeID, peer.PublicKey); err != nil {
		return err
	}
	if last, ok := p.store.Snapshot(peer.NodeID); ok && !snapshotExpired(last, now) && snapshot.Sequence <= last.Sequence {
		return fmt.Errorf("presence snapshot sequence %d is not greater than last accepted %d", snapshot.Sequence, last.Sequence)
	}
	return nil
}

func (p *Puller) peerEligible(nodeID string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	failure, ok := p.failures[nodeID]
	if !ok || failure.nextEligiblePullAt.IsZero() {
		return true
	}
	return !now.Before(failure.nextEligiblePullAt)
}

func (p *Puller) recordFailure(nodeID string, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()

	failure := p.failures[nodeID]
	failure.count++
	failure.nextEligiblePullAt = now.Add(p.backoffDelay(failure.count))
	p.failures[nodeID] = failure
}

func (p *Puller) clearFailure(nodeID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	delete(p.failures, nodeID)
}

func (p *Puller) backoffDelay(failureCount int) time.Duration {
	delay := p.backoffBase
	for i := 1; i < failureCount && delay < p.backoffMax; i++ {
		if delay > p.backoffMax/2 {
			delay = p.backoffMax
			break
		}
		delay *= 2
	}
	if delay > p.backoffMax {
		return p.backoffMax
	}
	return delay
}

func pullerContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type realPullerTicker struct {
	ticker *time.Ticker
}

func newRealPullerTicker(interval time.Duration) PullerTicker {
	return &realPullerTicker{ticker: time.NewTicker(interval)}
}

func (t *realPullerTicker) Chan() <-chan time.Time {
	return t.ticker.C
}

func (t *realPullerTicker) Stop() {
	t.ticker.Stop()
}
