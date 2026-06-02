package federation

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/metaid-developers/meta-socket/internal/presence"
)

// SnapshotBuilder creates unsigned local presence snapshots.
type SnapshotBuilder struct {
	local      presence.LocalReader
	nodeID     string
	ttlSeconds int64

	clock    func() time.Time
	sequence func() uint64

	mu      sync.Mutex
	nextSeq uint64
}

// SnapshotOption customizes snapshot generation.
type SnapshotOption func(*SnapshotBuilder)

// WithClock sets the clock used for generatedAt.
func WithClock(clock func() time.Time) SnapshotOption {
	return func(b *SnapshotBuilder) {
		if clock != nil {
			b.clock = clock
		}
	}
}

// WithSequence sets the sequence generator used for snapshots.
func WithSequence(sequence func() uint64) SnapshotOption {
	return func(b *SnapshotBuilder) {
		if sequence != nil {
			b.sequence = sequence
		}
	}
}

// NewSnapshotBuilder returns a snapshot provider for local presence entries.
func NewSnapshotBuilder(local presence.LocalReader, nodeID string, ttlSeconds int64, opts ...SnapshotOption) *SnapshotBuilder {
	builder := &SnapshotBuilder{
		local:      local,
		nodeID:     nodeID,
		ttlSeconds: ttlSeconds,
		clock:      time.Now,
	}
	builder.sequence = builder.nextSequence

	for _, opt := range opts {
		opt(builder)
	}
	return builder
}

// Snapshot builds an unsigned local presence snapshot.
func (b *SnapshotBuilder) Snapshot() (*presence.Snapshot, error) {
	if b.local == nil {
		return nil, errors.New("federation snapshot requires a local presence reader")
	}

	items := b.local.OnlineEntries()
	if items == nil {
		items = []presence.OnlineEntry{}
	} else {
		items = append([]presence.OnlineEntry(nil), items...)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].MetaId != items[j].MetaId {
			return items[i].MetaId < items[j].MetaId
		}
		if items[i].Type != items[j].Type {
			return items[i].Type < items[j].Type
		}
		if items[i].ConnectedAt != items[j].ConnectedAt {
			return items[i].ConnectedAt < items[j].ConnectedAt
		}
		return items[i].LastSeenAt < items[j].LastSeenAt
	})

	return &presence.Snapshot{
		Protocol:    ProtocolPresence,
		Version:     Version,
		NodeID:      b.nodeID,
		GeneratedAt: b.clock().UnixMilli(),
		TTLSeconds:  b.ttlSeconds,
		Sequence:    b.sequence(),
		Items:       items,
		Signature:   "",
	}, nil
}

func (b *SnapshotBuilder) nextSequence() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextSeq++
	return b.nextSeq
}
