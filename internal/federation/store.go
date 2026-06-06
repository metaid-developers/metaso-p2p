package federation

import (
	"sort"
	"sync"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/presence"
)

const defaultLocalNodeID = "local"

// RegistryNode is the normalized node registry entry accepted by the store.
type RegistryNode struct {
	Protocol      string   `json:"protocol,omitempty"`
	Version       string   `json:"version,omitempty"`
	NodeID        string   `json:"nodeId"`
	Network       string   `json:"network,omitempty"`
	PublicBaseURL string   `json:"publicBaseUrl,omitempty"`
	SocketURL     string   `json:"socketUrl,omitempty"`
	PresenceURL   string   `json:"presenceUrl,omitempty"`
	PublicKey     string   `json:"publicKey,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	PublishedAt   int64    `json:"publishedAt,omitempty"`
	ValidUntil    int64    `json:"validUntil,omitempty"`
}

// Store keeps accepted federation peers and their latest presence snapshots.
type Store struct {
	mu           sync.RWMutex
	localNodeID  string
	enabled      bool
	defaultScope string
	clock        func() time.Time

	peers     map[string]RegistryNode
	snapshots map[string]presence.Snapshot
}

// StoreOption customizes federation store behavior.
type StoreOption func(*Store)

// WithStoreClock sets the clock used by presence.GlobalReader methods.
func WithStoreClock(clock func() time.Time) StoreOption {
	return func(s *Store) {
		if clock != nil {
			s.clock = clock
		}
	}
}

// WithStoreEnabled sets whether this store should serve global reads.
func WithStoreEnabled(enabled bool) StoreOption {
	return func(s *Store) {
		s.enabled = enabled
	}
}

// WithStoreDefaultScope sets the default HTTP presence scope advertised by the reader.
func WithStoreDefaultScope(scope string) StoreOption {
	return func(s *Store) {
		if scope == "local" || scope == "global" {
			s.defaultScope = scope
		}
	}
}

// NewStore returns a federation presence store.
func NewStore(localNodeID string, opts ...StoreOption) *Store {
	if localNodeID == "" {
		localNodeID = defaultLocalNodeID
	}

	store := &Store{
		localNodeID:  localNodeID,
		enabled:      true,
		defaultScope: "global",
		clock:        time.Now,
		peers:        make(map[string]RegistryNode),
		snapshots:    make(map[string]presence.Snapshot),
	}
	for _, opt := range opts {
		opt(store)
	}
	if store.clock == nil {
		store.clock = time.Now
	}
	if store.defaultScope != "local" && store.defaultScope != "global" {
		store.defaultScope = "global"
	}
	return store
}

// Enabled reports whether the store should serve global reads.
func (s *Store) Enabled() bool {
	if s == nil {
		return false
	}
	return s.enabled
}

// DefaultScope returns the configured default HTTP presence scope.
func (s *Store) DefaultScope() string {
	if s == nil || s.defaultScope == "" {
		return "local"
	}
	if s.defaultScope != "global" {
		return "local"
	}
	return "global"
}

// UpsertPeer records or replaces an accepted federation peer.
func (s *Store) UpsertPeer(peer RegistryNode) {
	if s == nil || peer.NodeID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.peers[peer.NodeID] = cloneRegistryNode(peer)
}

// RemovePeer removes a peer and its last known snapshot.
func (s *Store) RemovePeer(nodeID string) {
	if s == nil || nodeID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.peers, nodeID)
	delete(s.snapshots, nodeID)
}

// Peer returns a cloned registry peer by node ID.
func (s *Store) Peer(nodeID string) (RegistryNode, bool) {
	if s == nil || nodeID == "" {
		return RegistryNode{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	peer, ok := s.peers[nodeID]
	if !ok {
		return RegistryNode{}, false
	}
	return cloneRegistryNode(peer), true
}

// ActivePeers returns cloned non-expired registry peers sorted by node ID.
func (s *Store) ActivePeers(now time.Time) []RegistryNode {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	peers := make([]RegistryNode, 0, len(s.peers))
	for _, peer := range s.peers {
		if peerExpired(peer, now) {
			continue
		}
		peers = append(peers, cloneRegistryNode(peer))
	}
	sort.Slice(peers, func(i, j int) bool {
		return peers[i].NodeID < peers[j].NodeID
	})
	return peers
}

// UpsertSnapshot records the latest accepted presence snapshot for a node.
func (s *Store) UpsertSnapshot(snapshot presence.Snapshot) {
	if s == nil || snapshot.NodeID == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.snapshots[snapshot.NodeID] = cloneSnapshot(snapshot)
}

// Snapshot returns a cloned accepted presence snapshot by node ID.
func (s *Store) Snapshot(nodeID string) (presence.Snapshot, bool) {
	if s == nil || nodeID == "" {
		return presence.Snapshot{}, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshot, ok := s.snapshots[nodeID]
	if !ok {
		return presence.Snapshot{}, false
	}
	return cloneSnapshot(snapshot), true
}

// GlobalOnline returns merged local and non-expired remote presence entries.
func (s *Store) GlobalOnline(local []presence.OnlineEntry, now time.Time) []presence.OnlineEntry {
	snapshots := s.activeSnapshots(now)
	result := aggregatePresence(local, snapshots, s.localNodeID)
	return result.items
}

// OnlineList returns a paginated global presence list.
func (s *Store) OnlineList(local []presence.OnlineEntry, page int, size int) []presence.OnlineEntry {
	items := s.GlobalOnline(local, s.now())
	return paginatePresence(items, page, size)
}

// Stats implements presence.GlobalReader using the store clock.
func (s *Store) Stats(local []presence.OnlineEntry) presence.GlobalStats {
	return s.StatsAt(local, s.now())
}

// StatsAt returns global stats for an explicit point in time.
func (s *Store) StatsAt(local []presence.OnlineEntry, now time.Time) presence.GlobalStats {
	snapshots := s.activeSnapshots(now)
	result := aggregatePresence(local, snapshots, s.localNodeID)
	return presence.GlobalStats{
		TotalConnections: result.totalConnections,
		UniqueMetaIds:    result.uniqueMetaIds,
		Nodes:            1 + len(snapshots),
	}
}

func (s *Store) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now()
	}
	return s.clock()
}

func (s *Store) activeSnapshots(now time.Time) []presence.Snapshot {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	snapshots := make([]presence.Snapshot, 0, len(s.snapshots))
	for nodeID, snapshot := range s.snapshots {
		if nodeID == s.localNodeID {
			continue
		}
		peer, ok := s.peers[nodeID]
		if !ok || peerExpired(peer, now) {
			continue
		}
		if snapshotExpired(snapshot, now) {
			continue
		}
		snapshots = append(snapshots, cloneSnapshot(snapshot))
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].NodeID < snapshots[j].NodeID
	})
	return snapshots
}

type aggregateResult struct {
	items            []presence.OnlineEntry
	totalConnections int
	uniqueMetaIds    int
}

type aggregateEntry struct {
	entry       presence.OnlineEntry
	sourceSeen  map[string]struct{}
	sourceCount int
}

func aggregatePresence(local []presence.OnlineEntry, snapshots []presence.Snapshot, localNodeID string) aggregateResult {
	if localNodeID == "" {
		localNodeID = defaultLocalNodeID
	}

	entriesByKey := make(map[string]*aggregateEntry)
	uniqueMetaIds := make(map[string]struct{})
	totalConnections := 0

	for _, item := range local {
		totalConnections++
		uniqueMetaIds[item.MetaId] = struct{}{}
		addAggregateObservation(entriesByKey, item, localNodeID)
	}

	for _, snapshot := range snapshots {
		for _, item := range snapshot.Items {
			totalConnections++
			uniqueMetaIds[item.MetaId] = struct{}{}
			addAggregateObservation(entriesByKey, item, snapshot.NodeID)
		}
	}

	items := make([]presence.OnlineEntry, 0, len(entriesByKey))
	for _, aggregated := range entriesByKey {
		item := aggregated.entry
		item.Sources = aggregated.sourceCount
		item.SourceNodeIds = sortedSourceNodeIDs(aggregated.sourceSeen)
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].LastSeenAt != items[j].LastSeenAt {
			return items[i].LastSeenAt > items[j].LastSeenAt
		}
		if items[i].MetaId != items[j].MetaId {
			return items[i].MetaId < items[j].MetaId
		}
		return items[i].Type < items[j].Type
	})

	return aggregateResult{
		items:            items,
		totalConnections: totalConnections,
		uniqueMetaIds:    len(uniqueMetaIds),
	}
}

func addAggregateObservation(entriesByKey map[string]*aggregateEntry, item presence.OnlineEntry, nodeID string) {
	if nodeID == "" {
		nodeID = defaultLocalNodeID
	}

	key := item.MetaId + "\x00" + item.Type
	aggregated, ok := entriesByKey[key]
	if !ok {
		entriesByKey[key] = &aggregateEntry{
			entry: presence.OnlineEntry{
				MetaId:      item.MetaId,
				Type:        item.Type,
				ConnectedAt: item.ConnectedAt,
				LastSeenAt:  item.LastSeenAt,
			},
			sourceSeen:  map[string]struct{}{nodeID: {}},
			sourceCount: 1,
		}
		return
	}

	if item.ConnectedAt != 0 && (aggregated.entry.ConnectedAt == 0 || item.ConnectedAt < aggregated.entry.ConnectedAt) {
		aggregated.entry.ConnectedAt = item.ConnectedAt
	}
	if item.LastSeenAt > aggregated.entry.LastSeenAt {
		aggregated.entry.LastSeenAt = item.LastSeenAt
	}
	aggregated.sourceSeen[nodeID] = struct{}{}
	aggregated.sourceCount++
}

func sortedSourceNodeIDs(sourceSeen map[string]struct{}) []string {
	sourceNodeIDs := make([]string, 0, len(sourceSeen))
	for nodeID := range sourceSeen {
		sourceNodeIDs = append(sourceNodeIDs, nodeID)
	}
	sort.Strings(sourceNodeIDs)
	return sourceNodeIDs
}

func paginatePresence(items []presence.OnlineEntry, page int, size int) []presence.OnlineEntry {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}

	start := (page - 1) * size
	if start >= len(items) {
		return []presence.OnlineEntry{}
	}
	end := start + size
	if end > len(items) {
		end = len(items)
	}
	return append([]presence.OnlineEntry(nil), items[start:end]...)
}

func snapshotExpired(snapshot presence.Snapshot, now time.Time) bool {
	expiresAt := snapshot.GeneratedAt + snapshot.TTLSeconds*1000
	return expiresAt < now.UnixMilli()
}

func peerExpired(peer RegistryNode, now time.Time) bool {
	return peer.ValidUntil > 0 && peer.ValidUntil < now.UnixMilli()
}

func cloneRegistryNode(peer RegistryNode) RegistryNode {
	peer.Capabilities = append([]string(nil), peer.Capabilities...)
	return peer
}

func cloneSnapshot(snapshot presence.Snapshot) presence.Snapshot {
	snapshot.Items = append([]presence.OnlineEntry(nil), snapshot.Items...)
	for i := range snapshot.Items {
		snapshot.Items[i].SourceNodeIds = append([]string(nil), snapshot.Items[i].SourceNodeIds...)
	}
	return snapshot
}
