package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bitcoinsv/bsvutil"
)

const (
	defaultDiscoverySize           = 100
	defaultDiscoveryInterval       = 5 * time.Minute
	defaultDiscoveryRequestTimeout = 10 * time.Second
)

// RegistryPin is the stable internal boundary for MANAPI registry pins.
type RegistryPin struct {
	ID             string
	Operation      string
	ChainName      string
	Timestamp      int64
	ContentBody    []byte
	ContentSummary string
}

// DiscoveryTicker is the discovery-loop ticker abstraction used for deterministic tests.
type DiscoveryTicker interface {
	Chan() <-chan time.Time
	Stop()
}

// DiscoveryTickerFactory creates a ticker for the configured discovery interval.
type DiscoveryTickerFactory func(interval time.Duration) DiscoveryTicker

// DiscoveryOptions configures MANAPI registry discovery.
type DiscoveryOptions struct {
	MANAPIBaseURL     string
	RegistryPath      string
	Size              int
	SelfNodeID        string
	Network           string
	AllowInsecureHTTP bool
	MaxPeers          int
	RequestTimeout    time.Duration
	Store             *Store
	HTTPClient        *http.Client
	Clock             func() time.Time
	Interval          time.Duration
	TickerFactory     DiscoveryTickerFactory
}

// Discovery discovers federation peers from MANAPI registry pins.
type Discovery struct {
	manapiBaseURL     string
	registryPath      string
	size              int
	selfNodeID        string
	network           string
	allowInsecureHTTP bool
	maxPeers          int
	requestTimeout    time.Duration
	store             *Store
	httpClient        *http.Client
	clock             func() time.Time
	interval          time.Duration
	tickerFactory     DiscoveryTickerFactory
}

// NewDiscovery creates a MANAPI peer discovery client.
func NewDiscovery(opts DiscoveryOptions) (*Discovery, error) {
	manapiBaseURL := strings.TrimSpace(opts.MANAPIBaseURL)
	if manapiBaseURL == "" {
		return nil, errors.New("federation discovery requires MANAPI base URL")
	}
	if !strings.Contains(manapiBaseURL, "{protocol-path}") || !strings.Contains(manapiBaseURL, "{size}") {
		return nil, errors.New("MANAPI base URL requires {protocol-path} and {size} placeholders")
	}
	network, err := normalizePublisherNetwork(opts.Network)
	if err != nil {
		return nil, fmt.Errorf("normalize discovery network: %w", err)
	}

	registryPath := normalizedPublisherPath(opts.RegistryPath, RegistryPath)
	size := opts.Size
	if size <= 0 {
		size = defaultDiscoverySize
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultDiscoveryInterval
	}
	requestTimeout := opts.RequestTimeout
	if requestTimeout <= 0 {
		requestTimeout = defaultDiscoveryRequestTimeout
	}
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	tickerFactory := opts.TickerFactory
	if tickerFactory == nil {
		tickerFactory = newRealDiscoveryTicker
	}

	return &Discovery{
		manapiBaseURL:     manapiBaseURL,
		registryPath:      registryPath,
		size:              size,
		selfNodeID:        strings.TrimSpace(opts.SelfNodeID),
		network:           network,
		allowInsecureHTTP: opts.AllowInsecureHTTP,
		maxPeers:          opts.MaxPeers,
		requestTimeout:    requestTimeout,
		store:             opts.Store,
		httpClient:        httpClient,
		clock:             clock,
		interval:          interval,
		tickerFactory:     tickerFactory,
	}, nil
}

// DiscoverOnce fetches MANAPI registry pins, accepts valid remote peers, and applies them to the store.
func (d *Discovery) DiscoverOnce(ctx context.Context) ([]RegistryNode, error) {
	if d == nil {
		return nil, errors.New("federation discovery is nil")
	}
	pins, err := d.fetchRegistryPins(ctx)
	if err != nil {
		return nil, err
	}

	candidates := make(map[string]registryDiscoveryCandidate)
	now := d.clock()
	for _, pin := range pins {
		candidate, ok := d.candidateFromPin(pin, now)
		if !ok {
			continue
		}
		existing, exists := candidates[candidate.node.NodeID]
		if !exists || discoveryCandidateNewer(candidate, existing) {
			candidates[candidate.node.NodeID] = candidate
		}
	}

	nodeIDs := make([]string, 0, len(candidates))
	for nodeID := range candidates {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)

	accepted := make([]RegistryNode, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		candidate := candidates[nodeID]
		if candidate.remove {
			if d.store != nil {
				d.store.RemovePeer(nodeID)
			}
			continue
		}
		accepted = append(accepted, cloneRegistryNode(candidate.node))
	}

	if d.maxPeers > 0 && len(accepted) > d.maxPeers {
		accepted = accepted[:d.maxPeers]
	}
	if d.store != nil {
		for _, peer := range accepted {
			d.store.UpsertPeer(peer)
		}
	}
	return accepted, nil
}

// Start runs discovery immediately, then on the configured interval until ctx is canceled.
func (d *Discovery) Start(ctx context.Context) {
	if d == nil {
		return
	}
	ctx = discoveryContext(ctx)
	go d.run(ctx)
}

func (d *Discovery) run(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	_, _ = d.DiscoverOnce(ctx)

	ticker := d.tickerFactory(d.interval)
	if ticker == nil {
		return
	}
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Chan():
			_, _ = d.DiscoverOnce(ctx)
		}
	}
}

func (d *Discovery) fetchRegistryPins(ctx context.Context) ([]RegistryPin, error) {
	requestURL, err := expandMANAPIURL(d.manapiBaseURL, d.registryPath, d.size)
	if err != nil {
		return nil, err
	}
	ctx = discoveryContext(ctx)
	if d.requestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.requestTimeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create MANAPI discovery request: %w", err)
	}
	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch MANAPI registry pins: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("MANAPI registry discovery returned HTTP %d", resp.StatusCode)
	}

	var envelope manapiPathListResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode MANAPI registry discovery response: %w", err)
	}
	if envelope.Code != 1 {
		return nil, fmt.Errorf("MANAPI registry discovery failed: code=%d message=%s", envelope.Code, envelope.Message)
	}

	pins := make([]RegistryPin, 0, len(envelope.Data.List))
	for _, pin := range envelope.Data.List {
		pins = append(pins, RegistryPin{
			ID:             strings.TrimSpace(pin.ID),
			Operation:      strings.TrimSpace(pin.Operation),
			ChainName:      strings.TrimSpace(pin.ChainName),
			Timestamp:      pin.Timestamp,
			ContentBody:    cloneBytes(pin.ContentBody.Bytes()),
			ContentSummary: strings.TrimSpace(pin.ContentSummary),
		})
	}
	return pins, nil
}

func (d *Discovery) candidateFromPin(pin RegistryPin, now time.Time) (registryDiscoveryCandidate, bool) {
	operation := strings.ToLower(strings.TrimSpace(pin.Operation))
	if !strings.EqualFold(strings.TrimSpace(pin.ChainName), "mvc") {
		return registryDiscoveryCandidate{}, false
	}
	if operation != "create" && operation != "modify" && operation != "revoke" {
		return registryDiscoveryCandidate{}, false
	}

	node, ok := registryNodeFromPin(pin)
	if !ok || strings.TrimSpace(node.NodeID) == "" {
		return registryDiscoveryCandidate{}, false
	}
	node.NodeID = strings.TrimSpace(node.NodeID)
	if d.selfNodeID != "" && node.NodeID == d.selfNodeID {
		return registryDiscoveryCandidate{}, false
	}
	if d.network != "" && !strings.EqualFold(strings.TrimSpace(node.Network), d.network) {
		return registryDiscoveryCandidate{}, false
	}

	candidate := registryDiscoveryCandidate{
		pin:  pin,
		node: node,
	}
	if !d.acceptRegistryNode(node) {
		return registryDiscoveryCandidate{}, false
	}
	if operation == "revoke" {
		candidate.remove = true
		return candidate, true
	}
	return candidate, true
}

func (d *Discovery) acceptRegistryNode(node RegistryNode) bool {
	if node.Protocol != ProtocolNode {
		return false
	}
	if strings.TrimSpace(node.Version) == "" {
		return false
	}
	if strings.TrimSpace(node.Network) == "" {
		return false
	}
	if strings.TrimSpace(node.PresenceURL) == "" {
		return false
	}
	if strings.TrimSpace(node.PublicKey) == "" {
		return false
	}
	expectedAddress, err := mvcAddressFromPublicKey(node.PublicKey, d.network)
	if err != nil {
		return false
	}
	if strings.TrimSpace(node.NodeID) != "mvc:"+expectedAddress {
		return false
	}
	if !registryCapabilitiesInclude(node.Capabilities, publisherPresenceCapability) {
		return false
	}
	return discoveryPresenceURLAllowed(node.PresenceURL, d.allowInsecureHTTP)
}

func mvcAddressFromPublicKey(publicKeyHex string, network string) (string, error) {
	publicKey, err := parsePublicKeyHex(publicKeyHex)
	if err != nil {
		return "", err
	}
	params, err := mvcRegistryNetParams(network)
	if err != nil {
		return "", err
	}
	pubKeyHash := bsvutil.Hash160(publicKey.SerializeCompressed())
	address, err := bsvutil.NewLegacyAddressPubKeyHash(pubKeyHash, params)
	if err != nil {
		return "", fmt.Errorf("derive MVC address: %w", err)
	}
	return address.String(), nil
}

func registryNodeFromPin(pin RegistryPin) (RegistryNode, bool) {
	content := discoveryPinContent(pin)
	if len(content) == 0 {
		return RegistryNode{}, false
	}

	var payload RegistryPayload
	if err := json.Unmarshal(content, &payload); err != nil {
		return RegistryNode{}, false
	}
	return RegistryNode{
		Protocol:      strings.TrimSpace(payload.Protocol),
		Version:       strings.TrimSpace(payload.Version),
		NodeID:        strings.TrimSpace(payload.NodeID),
		Network:       strings.TrimSpace(payload.Network),
		PublicBaseURL: strings.TrimSpace(payload.PublicBaseURL),
		SocketURL:     strings.TrimSpace(payload.SocketURL),
		PresenceURL:   strings.TrimSpace(payload.PresenceURL),
		PublicKey:     strings.TrimSpace(payload.PublicKey),
		Capabilities:  append([]string(nil), payload.Capabilities...),
		PublishedAt:   payload.PublishedAt,
		ValidUntil:    payload.ValidUntil,
	}, true
}

func discoveryPinContent(pin RegistryPin) []byte {
	content := strings.TrimSpace(string(pin.ContentBody))
	if content != "" {
		return []byte(content)
	}
	content = strings.TrimSpace(pin.ContentSummary)
	if content != "" {
		return []byte(content)
	}
	return nil
}

func discoveryCandidateNewer(candidate registryDiscoveryCandidate, existing registryDiscoveryCandidate) bool {
	if candidate.pin.Timestamp != existing.pin.Timestamp {
		return candidate.pin.Timestamp > existing.pin.Timestamp
	}
	if candidate.node.PublishedAt != existing.node.PublishedAt {
		return candidate.node.PublishedAt > existing.node.PublishedAt
	}
	return candidate.pin.ID > existing.pin.ID
}

func registryCapabilitiesInclude(capabilities []string, capability string) bool {
	for _, item := range capabilities {
		if strings.EqualFold(strings.TrimSpace(item), capability) {
			return true
		}
	}
	return false
}

func discoveryPresenceURLAllowed(raw string, allowInsecureHTTP bool) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		return true
	case "http":
		return allowInsecureHTTP || discoveryIsLoopbackHost(parsed.Hostname())
	default:
		return false
	}
}

func discoveryIsLoopbackHost(host string) bool {
	host = strings.Trim(strings.ToLower(strings.TrimSpace(host)), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func expandMANAPIURL(template string, registryPath string, size int) (string, error) {
	raw := strings.TrimSpace(template)
	if raw == "" {
		return "", errors.New("MANAPI URL template is required")
	}
	expanded := strings.ReplaceAll(raw, "{protocol-path}", url.QueryEscape(registryPath))
	expanded = strings.ReplaceAll(expanded, "{size}", strconv.Itoa(size))

	parsed, err := url.Parse(expanded)
	if err != nil {
		return "", fmt.Errorf("parse MANAPI discovery URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("MANAPI discovery URL requires scheme and host")
	}
	return parsed.String(), nil
}

func discoveryContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

type registryDiscoveryCandidate struct {
	pin    RegistryPin
	node   RegistryNode
	remove bool
}

type manapiPathListResponse struct {
	Code    int                `json:"code"`
	Message string             `json:"message"`
	Data    manapiPathListData `json:"data"`
}

type manapiPathListData struct {
	List       []manapiPin `json:"list"`
	NextCursor string      `json:"nextCursor"`
	Total      int         `json:"total"`
}

type manapiPin struct {
	ID             string             `json:"id"`
	Operation      string             `json:"operation"`
	ChainName      string             `json:"chainName"`
	Timestamp      int64              `json:"timestamp"`
	ContentBody    manapiContentBytes `json:"contentBody"`
	ContentSummary string             `json:"contentSummary"`
}

type manapiContentBytes []byte

func (b *manapiContentBytes) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*b = nil
		return nil
	}

	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		text = strings.TrimSpace(text)
		if text == "" {
			*b = nil
			return nil
		}
		*b = []byte(text)
		return nil
	}

	*b = append((*b)[:0], []byte(trimmed)...)
	return nil
}

func (b manapiContentBytes) Bytes() []byte {
	return append([]byte(nil), b...)
}

func cloneBytes(value []byte) []byte {
	return append([]byte(nil), value...)
}

type realDiscoveryTicker struct {
	ticker *time.Ticker
}

func newRealDiscoveryTicker(interval time.Duration) DiscoveryTicker {
	return &realDiscoveryTicker{ticker: time.NewTicker(interval)}
}

func (t *realDiscoveryTicker) Chan() <-chan time.Time {
	return t.ticker.C
}

func (t *realDiscoveryTicker) Stop() {
	t.ticker.Stop()
}
