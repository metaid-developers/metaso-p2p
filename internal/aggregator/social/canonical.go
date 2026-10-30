package social

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// Canonical cross-form merge for follow edges (spec §4.4c).
//
// Production defect (found 2026-10-30 on AI_Sunny's followers: 23 rows /
// 18 unique identities): the live MVC indexer supplies the wallet chain
// address in pin.GlobalMetaId, and the follower resolution used to trust
// that field verbatim — so live-delivered pins keyed their confirmed edges
// by ADDRESS while manapi backfill replay keyed the same pins by
// GlobalMetaID. One real follow pair then occupied two index keys and
// appeared twice in every list, with counts to match.
//
// The fix has two layers:
//
//   - Ingest (root cause): resolveFollowerGlobalMetaId now flows every
//     pin-supplied identity through the §2.3 normalization pipeline, so
//     new writes always land on canonical GlobalMetaID keys. Confirmed
//     saves and revokes additionally sweep legacy twin rows of the same
//     pair (same identity under an address/hex key form), which lets the
//     idempotent backfill replay organically rewrite the pre-fix data.
//   - Read (this file): every read surface answers through a merged view
//     keyed by the canonical GlobalMetaID, so any surviving mixed-form
//     rows still render as one row per identity.
//
// Selection rule (§4.4c), applied per canonical (follower, target) pair:
// all stored key forms of the pair collapse into one group — legacy hex,
// chain-address and GlobalMetaID keys alike; inside the group the newest
// mutation wins by followedAt, ties going to the mempool overlay (an
// unconfirmed pin is the fresher chain intent): a winning pending revoke
// hides the pair, a winning pending follow materializes it with
// pending=true; otherwise the newest confirmed active edge shows (ties
// broken by the canonical key form, then pinId).

// canonicalPeerId resolves a stored peer key — canonical GlobalMetaID,
// legacy 64-hex metaid, or chain address — to the canonical GlobalMetaID.
// Addresses convert locally through the IDAddress algorithm; hex ids
// resolve through the profile archive. A form that resolves to nothing
// stays distinct (its own identity) rather than silently collapsing into
// another peer.
func (a *Aggregator) canonicalPeerId(peer string) string {
	peer = strings.TrimSpace(peer)
	if peer == "" {
		return peer
	}
	if idaddress.ValidateIDAddress(strings.ToLower(peer)) {
		return strings.ToLower(peer)
	}
	if canonical, ok, err := a.normalizeIdentity(peer); err == nil && ok && canonical != "" {
		return canonical
	}
	return peer
}

// identityKeyForms returns every stored key form an identity may appear
// under: the given id first, then the profile aliases (chain address and
// legacy hex metaid). Deterministic order, deduplicated. Profile lookup
// failures degrade to the single given form — reads stay available, they
// just miss that identity's legacy twin rows.
func (a *Aggregator) identityKeyForms(id string) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	forms := []string{id}
	addAlias := func(alias string) {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			return
		}
		for _, existing := range forms {
			if existing == alias {
				return
			}
		}
		forms = append(forms, alias)
	}
	if a != nil && a.profileLookup != nil {
		if ref, err := a.profileLookup.LookupByGlobalMetaId(id); err == nil && ref != nil {
			addAlias(ref.Address)
			addAlias(ref.MetaId)
		}
	}
	return forms
}

// confirmedEdgeAcrossForms returns the newest confirmed active edge of the
// pair across every stored key form of follower and target, or nil.
func (a *Aggregator) confirmedEdgeAcrossForms(followerId, targetId string) (*FollowEdge, error) {
	followerForms := a.identityKeyForms(followerId)
	targetForms := a.identityKeyForms(targetId)
	var newest *FollowEdge
	newestIsCanonicalForm := false
	for _, f := range followerForms {
		for _, t := range targetForms {
			edge, err := a.loadActiveEdge(f, t)
			if err != nil {
				return nil, err
			}
			if edge == nil {
				continue
			}
			canonicalForm := f == followerId && t == targetId
			if newest == nil ||
				edge.FollowedAt > newest.FollowedAt ||
				(edge.FollowedAt == newest.FollowedAt && canonicalForm && !newestIsCanonicalForm) ||
				(edge.FollowedAt == newest.FollowedAt && canonicalForm == newestIsCanonicalForm && edge.FollowPinId > newest.FollowPinId) {
				newest = edge
				newestIsCanonicalForm = canonicalForm
			}
		}
	}
	return newest, nil
}

// effectiveEdgeAcrossForms answers the §4.4c effective state of one pair:
// all stored key forms collapse into one group, then the newest mutation
// wins by followedAt — a pending revoke hides the pair, a pending follow
// materializes it with pending=true — with ties going to the overlay (an
// unconfirmed pin is the fresher chain intent). A stale pending mutation
// (older than the pair's newest confirmed edge) yields to the confirmed
// edge.
func (a *Aggregator) effectiveEdgeAcrossForms(followerId, targetId string) (*FollowEdge, error) {
	followerForms := a.identityKeyForms(followerId)
	targetForms := a.identityKeyForms(targetId)
	var newestOverlay *pendingFollow
	for _, f := range followerForms {
		for _, t := range targetForms {
			if entry := a.loadPendingFollow(f, t); entry != nil && (newestOverlay == nil || entry.Seq > newestOverlay.Seq) {
				newestOverlay = entry
			}
		}
	}
	confirmed, err := a.confirmedEdgeAcrossForms(followerId, targetId)
	if err != nil {
		return nil, err
	}
	if newestOverlay != nil && (confirmed == nil || newestOverlay.FollowedAt >= confirmed.FollowedAt) {
		if newestOverlay.Kind == pendingKindRevoke {
			return nil, nil
		}
		return &FollowEdge{
			FollowerGlobalMetaId: followerId,
			TargetGlobalMetaId:   targetId,
			FollowPinId:          newestOverlay.FollowPinId,
			FollowedAt:           newestOverlay.FollowedAt,
			Active:               true,
			Pending:              true,
		}, nil
	}
	return confirmed, nil
}

// sweepPairTwins deletes stored edges of the canonical pair that sit under
// non-canonical key forms — the pre-fix confirmed rows keyed by chain
// address or legacy hex (§4.4c ingest heal). extraFollowerForms are the raw
// identity strings carried by the pin being folded; together with the
// canonical follower and its profile aliases they cover every form the
// historical writers used. The canonical (follower, target) key itself is
// never swept here.
func (a *Aggregator) sweepPairTwins(extraFollowerForms []string, canonicalFollower, canonicalTarget string) {
	if a == nil || a.store == nil || canonicalFollower == "" || canonicalTarget == "" {
		return
	}
	followerForms := a.identityKeyForms(canonicalFollower)
	for _, raw := range extraFollowerForms {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		duplicated := false
		for _, existing := range followerForms {
			if existing == raw {
				duplicated = true
				break
			}
		}
		if !duplicated {
			followerForms = append(followerForms, raw)
		}
	}
	targetForms := a.identityKeyForms(canonicalTarget)
	for _, f := range followerForms {
		for _, t := range targetForms {
			if f == canonicalFollower && t == canonicalTarget {
				continue
			}
			edge, err := a.loadActiveEdge(f, t)
			if err != nil || edge == nil {
				continue
			}
			_ = a.deleteActiveEdge(edge)
		}
	}
}

// peerEdgeView is one row of the merged, direction-aware follow view: the
// canonical peer id plus the winning effective edge.
type peerEdgeView struct {
	peer string
	edge *FollowEdge
}

// mergedPeerEdges builds the §4.4c deduplicated effective view of one
// direction for the subject: candidate peers come from the confirmed
// index scan (any stored key form) and the mempool overlay, every
// candidate resolves through the canonical merge, blocked peers drop.
// Rows sort with the pending bucket first (§4.4b first-page contract),
// then newest followedAt, ties by canonical peer id.
func (a *Aggregator) mergedPeerEdges(subjectId string, outbound bool, excluded map[string]struct{}) ([]peerEdgeView, error) {
	subjectId = strings.TrimSpace(subjectId)
	if subjectId == "" {
		return nil, ErrInvalidParameter
	}
	prefix := followerIndexPrefix(subjectId)
	if outbound {
		prefix = followingIndexPrefix(subjectId)
	}

	candidates := make([]string, 0)
	seen := make(map[string]struct{})
	addCandidate := func(peerRaw string) bool {
		canonical := a.canonicalPeerId(peerRaw)
		if canonical == "" {
			return false
		}
		if _, blocked := excluded[canonical]; blocked {
			return false
		}
		if _, dup := seen[canonical]; dup {
			return false
		}
		seen[canonical] = struct{}{}
		candidates = append(candidates, canonical)
		return true
	}

	if err := a.store.ScanPrefix(namespace, prefix, func(key, _ []byte) error {
		peer, ok := peerFromEdgeIndexKey(key)
		if ok {
			addCandidate(peer)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := a.forEachPendingPair(func(follower, target string, _ *pendingFollow) error {
		if outbound {
			if a.canonicalPeerId(follower) != subjectId {
				return nil
			}
			addCandidate(target)
			return nil
		}
		if a.canonicalPeerId(target) != subjectId {
			return nil
		}
		addCandidate(follower)
		return nil
	}); err != nil {
		return nil, err
	}

	views := make([]peerEdgeView, 0, len(candidates))
	for _, canonical := range candidates {
		var (
			edge *FollowEdge
			err  error
		)
		if outbound {
			edge, err = a.effectiveEdgeAcrossForms(subjectId, canonical)
		} else {
			edge, err = a.effectiveEdgeAcrossForms(canonical, subjectId)
		}
		if err != nil {
			return nil, err
		}
		if edge == nil {
			continue
		}
		views = append(views, peerEdgeView{peer: canonical, edge: edge})
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].edge.Pending != views[j].edge.Pending {
			return views[i].edge.Pending
		}
		if views[i].edge.FollowedAt != views[j].edge.FollowedAt {
			return views[i].edge.FollowedAt > views[j].edge.FollowedAt
		}
		return views[i].peer < views[j].peer
	})
	return views, nil
}

var errInvalidEdgeListCursor = errors.New("invalid edge list cursor")

// edgeListCursor is the opaque pagination anchor for the followers /
// following lists: the exact (bucket, followedAt, peer) tuple of the last
// returned row plus the list kind, so a cursor from one list is rejected
// on the other and a stale cursor (anchor row gone) is rejected. Same
// external contract as before: opaque string, stale → 40000.
type edgeListCursor struct {
	Kind       string `json:"k"`
	Pending    bool   `json:"p,omitempty"`
	FollowedAt int64  `json:"t"`
	Peer       string `json:"peer"`
}

func encodeEdgeListCursor(cursor edgeListCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func decodeEdgeListCursor(kind, raw string) (edgeListCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return edgeListCursor{}, nil
	}
	cursor, err := decodeEdgeListCursorShape(raw)
	if err != nil {
		return edgeListCursor{}, err
	}
	if cursor.Kind != kind {
		return edgeListCursor{}, errInvalidEdgeListCursor
	}
	return cursor, nil
}

// decodeEdgeListCursorShape validates the cursor envelope without binding
// it to a list kind (the parameter layer uses it; the list functions
// enforce the kind).
func decodeEdgeListCursorShape(raw string) (edgeListCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return edgeListCursor{}, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return edgeListCursor{}, errInvalidEdgeListCursor
	}
	var cursor edgeListCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return edgeListCursor{}, errInvalidEdgeListCursor
	}
	if strings.TrimSpace(cursor.Peer) == "" || cursor.FollowedAt == 0 {
		return edgeListCursor{}, errInvalidEdgeListCursor
	}
	return cursor, nil
}
