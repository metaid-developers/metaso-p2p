package block

import (
	"errors"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/api"
)

type compactBlockItemResponse struct {
	GlobalMetaId string `json:"globalMetaId"`
	Name         string `json:"name"`
	NameId       string `json:"nameId"`
	AvatarId     string `json:"avatarId"`
	Direction    string `json:"direction"`
	BlockedAt    int64  `json:"blockedAt"`
	BlockPinId   string `json:"blockPinId"`
}

type profileBlockItemResponse struct {
	compactBlockItemResponse
	Bio   string `json:"bio"`
	BioId string `json:"bioId"`
}

type blocksListResponse struct {
	List       []compactBlockItemResponse `json:"list"`
	NextCursor string                     `json:"nextCursor"`
	Size       int                        `json:"size"`
}

type profileBlocksListResponse struct {
	List       []profileBlockItemResponse `json:"list"`
	NextCursor string                     `json:"nextCursor"`
	Size       int                        `json:"size"`
}

func registerRoutes(a *Aggregator, router *gin.RouterGroup) {
	social := router.Group("/social")
	social.GET("/globalmetaid/:globalMetaId/blocks", a.handleBlocks)
}

// handleBlocks serves GET /api/social/globalmetaid/:globalMetaId/blocks
// (spec §4.4): current block relationships folded last-write-wins per pair,
// filterable by type, shared pagination contract.
func (a *Aggregator) handleBlocks(c *gin.Context) {
	params, err := parseBlockListParams(c)
	if err != nil {
		respondBlockError(c, err)
		return
	}

	result, err := a.ListBlocks(params)
	if err != nil {
		respondBlockError(c, err)
		return
	}

	if params.View == ViewProfile {
		list := make([]profileBlockItemResponse, 0, len(result.List))
		for _, item := range result.List {
			list = append(list, profileBlockItemResponse{
				compactBlockItemResponse: compactBlockItemResponse{
					GlobalMetaId: item.GlobalMetaId,
					Name:         item.Name,
					NameId:       item.NameId,
					AvatarId:     item.AvatarId,
					Direction:    item.Direction,
					BlockedAt:    item.BlockedAt,
					BlockPinId:   item.BlockPinId,
				},
				Bio:   item.Bio,
				BioId: item.BioId,
			})
		}
		api.RespSuccess(c, profileBlocksListResponse{List: list, NextCursor: result.NextCursor, Size: result.Size})
		return
	}

	list := make([]compactBlockItemResponse, 0, len(result.List))
	for _, item := range result.List {
		list = append(list, compactBlockItemResponse{
			GlobalMetaId: item.GlobalMetaId,
			Name:         item.Name,
			NameId:       item.NameId,
			AvatarId:     item.AvatarId,
			Direction:    item.Direction,
			BlockedAt:    item.BlockedAt,
			BlockPinId:   item.BlockPinId,
		})
	}
	api.RespSuccess(c, blocksListResponse{List: list, NextCursor: result.NextCursor, Size: result.Size})
}

func parseBlockListParams(c *gin.Context) (BlockListParams, error) {
	size, err := parseListSize(c.Query("size"))
	if err != nil {
		return BlockListParams{}, err
	}
	blockType, err := normalizeType(c.Query("type"))
	if err != nil {
		return BlockListParams{}, err
	}
	view, err := normalizeView(c.Query("view"))
	if err != nil {
		return BlockListParams{}, err
	}
	cursor := strings.TrimSpace(c.Query("cursor"))
	if _, err := decodeCursor(cursor); err != nil {
		return BlockListParams{}, ErrInvalidParameter
	}
	return BlockListParams{
		GlobalMetaId: strings.TrimSpace(c.Param("globalMetaId")),
		Type:         blockType,
		Cursor:       cursor,
		Size:         size,
		View:         view,
	}, nil
}

func parseListSize(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultListSize, nil
	}
	size, err := strconv.Atoi(raw)
	if err != nil || size <= 0 || size > maxListSize {
		return 0, ErrInvalidParameter
	}
	return size, nil
}

// ListBlocks folds both directions for the subject into one listing
// (type=all produces one entry per direction, spec §4.4).
func (a *Aggregator) ListBlocks(params BlockListParams) (*BlockListResult, error) {
	view, err := normalizeView(params.View)
	if err != nil {
		return nil, err
	}
	blockType, err := normalizeType(params.Type)
	if err != nil {
		return nil, err
	}
	_ = view // view shapes the response layer (handleBlocks), not the fold

	subject, err := a.lookupRequestSubject(params.GlobalMetaId)
	if err != nil {
		return nil, err
	}

	size := params.Size
	if size <= 0 {
		size = defaultListSize
	}
	if size > maxListSize {
		size = maxListSize
	}
	if _, err := decodeCursor(params.Cursor); err != nil {
		return nil, ErrInvalidParameter
	}

	var entries []directionalEntry
	if blockType != TypeBlockedBy {
		outbound, err := a.activeEdgesFrom(subject.GlobalMetaId)
		if err != nil {
			return nil, err
		}
		for _, peer := range outbound {
			entries = append(entries, directionalEntry{peer: peer, direction: DirectionBlocking})
		}
	}
	if blockType != TypeBlocking {
		inbound, err := a.activeEdgesTo(subject.GlobalMetaId)
		if err != nil {
			return nil, err
		}
		for _, peer := range inbound {
			entries = append(entries, directionalEntry{peer: peer, direction: DirectionBlockedBy})
		}
	}
	entries = sortDirectionalEntries(entries)

	cursorText := params.Cursor
	start := 0
	if cursorText != "" {
		found := false
		for i, entry := range entries {
			if encodeCursor(entry.cursorKey()) == cursorText {
				start = i + 1
				found = true
				break
			}
		}
		if !found {
			return nil, ErrInvalidParameter
		}
	}

	items := make([]BlockListItem, 0, size)
	nextCursor := ""
	for i := start; i < len(entries); i++ {
		entry := entries[i]
		profile := a.lookupListProfile(entry.peer.GlobalMetaId)
		items = append(items, BlockListItem{
			GlobalMetaId: profile.GlobalMetaId,
			Name:         profile.Name,
			NameId:       profile.NameId,
			AvatarId:     profile.AvatarId,
			Direction:    entry.direction,
			BlockedAt:    entry.peer.BlockedAt,
			BlockPinId:   entry.peer.BlockPinId,
			Bio:          profile.Bio,
			BioId:        profile.BioId,
		})
		if len(items) == size+1 {
			nextCursor = encodeCursor(entries[i-1].cursorKey())
			items = items[:size]
			break
		}
	}
	return &BlockListResult{List: items, NextCursor: nextCursor, Size: size}, nil
}

type directionalEntry struct {
	peer      BlockPeer
	direction string
}

func (e directionalEntry) cursorKey() []byte {
	return []byte(e.direction + ":" + e.peer.GlobalMetaId)
}

// sortDirectionalEntries orders newest first across both directions, ties
// resolved deterministically by direction then peer id.
func sortDirectionalEntries(entries []directionalEntry) []directionalEntry {
	// insertion sort keeps this dependency-free and the dataset per page small
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && lessEntry(entries[j], entries[j-1]); j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
	return entries
}

func lessEntry(a, b directionalEntry) bool {
	if a.peer.BlockedAt != b.peer.BlockedAt {
		return a.peer.BlockedAt > b.peer.BlockedAt
	}
	if a.direction != b.direction {
		return a.direction < b.direction
	}
	return a.peer.GlobalMetaId < b.peer.GlobalMetaId
}

// lookupListProfile best-effort decorates a peer with profile fields; an
// unknown peer keeps its canonical id with empty display fields (same
// policy as the follow lists).
func (a *Aggregator) lookupListProfile(globalMetaId string) *IdentityRef {
	if a == nil || a.profileLookup == nil {
		return &IdentityRef{GlobalMetaId: globalMetaId}
	}
	profile, err := a.lookupRequestSubject(globalMetaId)
	if err == nil && profile != nil {
		return profile
	}
	return &IdentityRef{GlobalMetaId: globalMetaId}
}

// lookupRequestSubject normalizes the subject input (GlobalMetaID or legacy
// hex, spec §2.3). An input that does not normalize to an indexed
// GlobalMetaID is ErrNotFound → 40400 (same behavior as the shipped social
// list endpoints).
func (a *Aggregator) lookupRequestSubject(globalMetaId string) (*IdentityRef, error) {
	globalMetaId = strings.TrimSpace(globalMetaId)
	if globalMetaId == "" {
		return nil, ErrInvalidParameter
	}
	if a == nil || a.profileLookup == nil {
		// Degraded mode (no profile store wired): canonical ids are usable
		// bare; anything else cannot normalize.
		if isGlobalMetaIdRef(globalMetaId) {
			return &IdentityRef{GlobalMetaId: globalMetaId}, nil
		}
		return nil, ErrUnavailable
	}
	if isGlobalMetaIdRef(globalMetaId) {
		profile, err := a.profileLookup.LookupByGlobalMetaId(globalMetaId)
		if err != nil {
			return nil, errors.Join(ErrUnavailable, err)
		}
		if profile != nil && strings.TrimSpace(profile.GlobalMetaId) != "" {
			return profile, nil
		}
		return nil, ErrNotFound
	}
	target, err := a.lookupTargetRef(globalMetaId)
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if target == nil || strings.TrimSpace(target.GlobalMetaId) == "" {
		return nil, ErrNotFound
	}
	return target, nil
}

// IsBlockedBetween implements BlockLister (spec §4.5): true when either
// direction of the pair is currently active. Inputs accept both identifier
// forms and normalize per spec §2.3; unresolvable inputs yield
// (false, nil) — fail-open, see the interface contract.
func (a *Aggregator) IsBlockedBetween(aIdent, bIdent string) (bool, error) {
	from, ok := a.normalizeIdentityForQuery(aIdent)
	if !ok {
		return false, nil
	}
	to, ok := a.normalizeIdentityForQuery(bIdent)
	if !ok {
		return false, nil
	}
	if from == to {
		return false, nil
	}
	forward, err := a.loadActiveEdge(from, to)
	if err != nil {
		return false, err
	}
	if forward != nil {
		return true, nil
	}
	backward, err := a.loadActiveEdge(to, from)
	if err != nil {
		return false, err
	}
	return backward != nil, nil
}

// BlockingPeers lists peers the subject currently blocks, newest first.
func (a *Aggregator) BlockingPeers(subject string) ([]BlockPeer, error) {
	canonical, err := a.lookupRequestSubject(subject)
	if err != nil {
		return nil, err
	}
	return a.activeEdgesFrom(canonical.GlobalMetaId)
}

// BlockedByPeers lists peers currently blocking the subject, newest first.
func (a *Aggregator) BlockedByPeers(subject string) ([]BlockPeer, error) {
	canonical, err := a.lookupRequestSubject(subject)
	if err != nil {
		return nil, err
	}
	return a.activeEdgesTo(canonical.GlobalMetaId)
}

// BlockCounts returns the current active counters for a subject — the
// primitive behind the blockedCount/blockedByCount fields of the counts
// endpoint (spec §4.2, wired by the social aggregator in the merge round).
func (a *Aggregator) BlockCounts(subject string) (blocking, blockedBy int, err error) {
	blockingPeers, err := a.BlockingPeers(subject)
	if err != nil {
		return 0, 0, err
	}
	blockedByPeers, err := a.BlockedByPeers(subject)
	if err != nil {
		return 0, 0, err
	}
	return len(blockingPeers), len(blockedByPeers), nil
}

// normalizeIdentityForQuery resolves one identity for filter lookups:
// canonical ids pass through, hex resolves via the profile lookup,
// everything else reports not-normalized (fail-open at the caller).
func (a *Aggregator) normalizeIdentityForQuery(ident string) (string, bool) {
	ident = strings.TrimSpace(ident)
	if ident == "" {
		return "", false
	}
	if isGlobalMetaIdRef(ident) {
		return ident, true
	}
	if a == nil || a.profileLookup == nil {
		return "", false
	}
	target, err := a.lookupTargetRef(ident)
	if err != nil || target == nil {
		return "", false
	}
	globalMetaId := strings.TrimSpace(target.GlobalMetaId)
	if globalMetaId == "" {
		return "", false
	}
	return globalMetaId, true
}

// BlockedPeerIds returns the canonical globalMetaIds of all peers with an
// active on-chain block in either direction relative to the subject. It is
// the primitive the notify union read-side merge consumes (spec §4.6).
func (a *Aggregator) BlockedPeerIds(subject string) ([]string, error) {
	blocking, err := a.BlockingPeers(subject)
	if err != nil {
		return nil, err
	}
	blockedBy, err := a.BlockedByPeers(subject)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(blocking)+len(blockedBy))
	out := make([]string, 0, len(blocking)+len(blockedBy))
	for _, peer := range blocking {
		if _, dup := seen[peer.GlobalMetaId]; dup {
			continue
		}
		seen[peer.GlobalMetaId] = struct{}{}
		out = append(out, peer.GlobalMetaId)
	}
	for _, peer := range blockedBy {
		if _, dup := seen[peer.GlobalMetaId]; dup {
			continue
		}
		seen[peer.GlobalMetaId] = struct{}{}
		out = append(out, peer.GlobalMetaId)
	}
	return out, nil
}

func respondBlockError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidParameter):
		api.RespErr(c, 40000, "invalid parameter")
	case errors.Is(err, ErrNotFound):
		api.RespErr(c, 40400, "subject not found")
	default:
		api.RespErr(c, 50000, "aggregation unavailable")
	}
}
