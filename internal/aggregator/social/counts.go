package social

import (
	"strings"
)

// CountsResult is the §4.2 response: every relationship counter of the
// subject in one call. The follow/friend counters use the same filtered view
// as the list endpoints (blocked peers excluded), so counts always agree with
// what the lists return.
type CountsResult struct {
	Followers      int `json:"followers"`
	Following      int `json:"following"`
	Friends        int `json:"friends"`
	BlockedCount   int `json:"blockedCount"`
	BlockedByCount int `json:"blockedByCount"`
}

// Counts computes all relationship counters for the subject. The subject
// input accepts both identifier forms (§2.3).
func (a *Aggregator) Counts(subjectInput string) (*CountsResult, error) {
	subjectInput = strings.TrimSpace(subjectInput)
	if subjectInput == "" {
		return nil, ErrInvalidParameter
	}
	subjectId, ok, err := a.normalizeIdentity(subjectInput)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNotFound
	}

	excluded, err := a.blockedPeerSet(subjectId)
	if err != nil {
		return nil, err
	}

	following, err := a.countFollowing(subjectId, excluded)
	if err != nil {
		return nil, err
	}
	followers, err := a.countFollowers(subjectId, excluded)
	if err != nil {
		return nil, err
	}
	friends, err := a.countFriends(subjectId, excluded)
	if err != nil {
		return nil, err
	}
	blockingSet, blockedBySet, err := a.blockPeerSets(subjectId)
	if err != nil {
		return nil, err
	}

	return &CountsResult{
		Followers:      followers,
		Following:      following,
		Friends:        friends,
		BlockedCount:   len(blockingSet),
		BlockedByCount: len(blockedBySet),
	}, nil
}

func (a *Aggregator) countFollowing(subjectId string, excluded map[string]struct{}) (int, error) {
	count := 0
	seen := make(map[string]struct{})
	err := a.store.ScanPrefix(namespace, followingIndexPrefix(subjectId), func(key, _ []byte) error {
		peer, ok := peerFromEdgeIndexKey(key)
		if !ok {
			return nil
		}
		if _, blocked := excluded[peer]; blocked {
			return nil
		}
		// Effective edge: a pending revoke hides the pair pre-confirmation.
		edge, err := a.loadEffectiveEdge(subjectId, peer)
		if err != nil {
			return err
		}
		if edge != nil {
			count++
			seen[peer] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	// §4.4b: pending-only follows (no confirmed index entry) count too.
	for peer := range a.pendingFollowPeers(subjectId, true) {
		if _, dup := seen[peer]; dup {
			continue
		}
		if _, blocked := excluded[peer]; blocked {
			continue
		}
		if confirmed, _ := a.loadActiveEdge(subjectId, peer); confirmed == nil {
			count++
		}
	}
	return count, nil
}

func (a *Aggregator) countFollowers(subjectId string, excluded map[string]struct{}) (int, error) {
	count := 0
	seen := make(map[string]struct{})
	err := a.store.ScanPrefix(namespace, followerIndexPrefix(subjectId), func(key, _ []byte) error {
		peer, ok := peerFromEdgeIndexKey(key)
		if !ok {
			return nil
		}
		if _, blocked := excluded[peer]; blocked {
			return nil
		}
		// Effective edge: a pending revoke hides the pair pre-confirmation.
		edge, err := a.loadEffectiveEdge(peer, subjectId)
		if err != nil {
			return err
		}
		if edge != nil {
			count++
			seen[peer] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	// §4.4b: pending-only followers (no confirmed index entry) count too.
	for peer := range a.pendingFollowPeers(subjectId, false) {
		if _, dup := seen[peer]; dup {
			continue
		}
		if _, blocked := excluded[peer]; blocked {
			continue
		}
		if confirmed, _ := a.loadActiveEdge(peer, subjectId); confirmed == nil {
			count++
		}
	}
	return count, nil
}

func (a *Aggregator) countFriends(subjectId string, excluded map[string]struct{}) (int, error) {
	count := 0
	prefix := mutualIndexPrefix(subjectId)
	err := a.store.ScanPrefix(namespace, prefix, func(key, _ []byte) error {
		peer := strings.TrimPrefix(string(key), string(prefix))
		if peer == "" || strings.Contains(peer, ":") {
			return nil
		}
		if _, blocked := excluded[peer]; blocked {
			return nil
		}
		// Effective mutual: a pending revoke on either direction breaks the
		// friendship pre-confirmation (spec §4.4b).
		out, outErr := a.loadEffectiveEdge(subjectId, peer)
		if outErr != nil {
			return outErr
		}
		if out == nil {
			return nil
		}
		in, inErr := a.loadEffectiveEdge(peer, subjectId)
		if inErr != nil {
			return inErr
		}
		if in == nil {
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	// §4.4b: friendships that only exist through the overlay (e.g. the
	// second follow still unconfirmed) count too.
	candidates := a.pendingFollowPeers(subjectId, true)
	for peer := range a.pendingFollowPeers(subjectId, false) {
		candidates[peer] = pendingFollow{}
	}
	for peer := range candidates {
		if _, blocked := excluded[peer]; blocked {
			continue
		}
		out, _ := a.loadEffectiveEdge(subjectId, peer)
		if out == nil {
			continue
		}
		in, _ := a.loadEffectiveEdge(peer, subjectId)
		if in == nil {
			continue
		}
		if !out.Pending && !in.Pending {
			continue // pure confirmed pair: covered by the mutual index scan
		}
		confirmedOut, _ := a.loadActiveEdge(subjectId, peer)
		confirmedIn, _ := a.loadActiveEdge(peer, subjectId)
		if confirmedOut != nil && confirmedIn != nil {
			continue // already mutual in the confirmed model (scan counted it)
		}
		count++
	}
	return count, nil
}

// blockPeerSets returns the (blocking, blockedBy) peer sets of the subject
// with empty/duplicate entries removed.
func (a *Aggregator) blockPeerSets(subjectId string) (map[string]struct{}, map[string]struct{}, error) {
	lister := a.currentBlockLister()
	blocking, err := lister.BlockingBy(subjectId)
	if err != nil {
		return nil, nil, unavailableWrap(err)
	}
	blockedBy, err := lister.BlockedBy(subjectId)
	if err != nil {
		return nil, nil, unavailableWrap(err)
	}
	blockingSet := make(map[string]struct{}, len(blocking))
	for _, peer := range blocking {
		if peer = strings.TrimSpace(peer); peer != "" {
			blockingSet[peer] = struct{}{}
		}
	}
	blockedBySet := make(map[string]struct{}, len(blockedBy))
	for _, peer := range blockedBy {
		if peer = strings.TrimSpace(peer); peer != "" {
			blockedBySet[peer] = struct{}{}
		}
	}
	return blockingSet, blockedBySet, nil
}

// peerFromEdgeIndexKey extracts the peer GlobalMetaID from a following /
// followers index key; both key families end with ":<peerGlobalMetaId>".
func peerFromEdgeIndexKey(key []byte) (string, bool) {
	keyText := string(key)
	idx := strings.LastIndex(keyText, ":")
	if idx < 0 || idx+1 >= len(keyText) {
		return "", false
	}
	return keyText[idx+1:], true
}
