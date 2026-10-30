package block

import (
	"errors"
	"strings"
)

// Direction of a block edge relative to a subject.
const (
	DirectionBlocking  = "blocking"  // subject blocks the peer
	DirectionBlockedBy = "blockedBy" // the peer blocks the subject
)

// Block type filter values for the blocks listing endpoint (spec §4.4).
// Stored lowercase: normalizeType lower-cases the raw query input before
// comparing (direction values in responses keep their spec spelling).
const (
	TypeAll       = "all"
	TypeBlocking  = "blocking"
	TypeBlockedBy = "blockedby"
)

const (
	ViewCompact = "compact"
	ViewProfile = "profile"
)

var (
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrNotFound         = errors.New("subject not found")
	ErrUnavailable      = errors.New("aggregation unavailable")
)

const (
	defaultListSize = 20
	maxListSize     = 100
	maxBatchTargets = 100
)

// BlockEdge is the folded, last-write-wins state of one (from, to) pair.
// The pair key stores only the currently active state; blockState = -1
// removes the pair from the active read model (spec §1.1/§4.4).
type BlockEdge struct {
	FromGlobalMetaId string `json:"fromGlobalMetaId"`
	ToGlobalMetaId   string `json:"toGlobalMetaId"`
	BlockPinId       string `json:"blockPinId"`
	BlockedAt        int64  `json:"blockedAt"`
	BlockState       int64  `json:"blockState"`
	Active           bool   `json:"active"`
	// Pending marks an edge materialized from the mempool overlay
	// (unconfirmed pin, spec §4.4b). Confirmed edges serialize it away
	// (omitempty), so the stored confirmed-model JSON is unchanged.
	Pending bool `json:"pending,omitempty"`
}

// BlockListItem is one peer entry of the blocks listing (spec §4.4).
type BlockListItem struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name,omitempty"`
	NameId       string `json:"nameId,omitempty"`
	AvatarId     string `json:"avatarId,omitempty"`
	Direction    string `json:"direction"`
	BlockedAt    int64  `json:"blockedAt"`
	BlockPinId   string `json:"blockPinId"`
	// Pending is true when the edge comes from the mempool overlay (the
	// show.now "Mempool:1" marker precedent, spec §4.4b).
	Pending bool `json:"pending,omitempty"`
	// Profile view additions (spec §4.4: bio/bioId).
	Bio   string `json:"bio,omitempty"`
	BioId string `json:"bioId,omitempty"`
}

type BlockListParams struct {
	GlobalMetaId string
	Type         string
	Cursor       string
	Size         int
	View         string
}

type BlockListResult struct {
	List       []BlockListItem `json:"list"`
	NextCursor string          `json:"nextCursor"`
	Size       int             `json:"size"`
}

// BlockPeer is a subject-facing view of one active block pair, used by the
// counters and the union read-side merge (spec §4.5/§4.6). Pending marks an
// overlay (unconfirmed) edge (spec §4.4b).
type BlockPeer struct {
	GlobalMetaId string
	BlockPinId   string
	BlockedAt    int64
	Pending      bool
}

// BlockLister is the unified filter interface every filtered surface depends
// on (spec §4.5). Consumers must depend on this interface, never on the
// block storage directly. The social aggregator (S2) wires this into the
// following/followers/friends/counts read paths in the merge round; the
// batch relationship endpoint deliberately reports instead of filtering.
type BlockLister interface {
	// IsBlockedBetween reports whether either direction of the pair is
	// currently blocked (blockState = 1 on the newest pin for the pair).
	//
	// Identity inputs accept a GlobalMetaID or a legacy 64-hex metaid and
	// are normalized internally (spec §2.3). An input that cannot be
	// normalized yields (false, nil): unknown identities have no known
	// block between them (fail-open), so one unregistered identity can
	// never break a whole filtered list read.
	IsBlockedBetween(a, b string) (bool, error)
}

// ChatBlockBridge is the notify-side event bridge target (spec §4.6): the
// on-chain block aggregation is the authority for chain-level block state,
// and every ingested confirmed block pin is additionally mirrored into the
// notify session-layer read model (`blocked:` entries).
//
// Alias slices carry every known identifier form of an identity
// (canonical GlobalMetaID first, then legacy hex metaid / address), so the
// notify side can key its session cache without depending on this package.
type ChatBlockBridge interface {
	// ApplyChainBlock records a chain-confirmed block from → to.
	ApplyChainBlock(fromAliases, toAliases []string, blockPinId string, blockedAt int64) error
	// RemoveChainBlock lifts the chain-confirmed block from → to. Only
	// entries written by the bridge (on-chain provenance) are removed;
	// off-chain session entries for the same peer stay untouched.
	RemoveChainBlock(fromAliases, toAliases []string) error
}

// normalizeType validates the blocks listing type filter.
func normalizeType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", TypeAll:
		return TypeAll, nil
	case TypeBlocking:
		return TypeBlocking, nil
	case TypeBlockedBy:
		return TypeBlockedBy, nil
	default:
		return "", ErrInvalidParameter
	}
}

func normalizeView(view string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(view)) {
	case "", ViewCompact:
		return ViewCompact, nil
	case ViewProfile:
		return ViewProfile, nil
	default:
		return "", ErrInvalidParameter
	}
}
