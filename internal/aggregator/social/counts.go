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
	// §4.4c: counts run over the same canonical merged view the list
	// endpoints serve, so they agree by construction.
	views, err := a.mergedPeerEdges(subjectId, true, excluded)
	if err != nil {
		return 0, err
	}
	return len(views), nil
}

func (a *Aggregator) countFollowers(subjectId string, excluded map[string]struct{}) (int, error) {
	views, err := a.mergedPeerEdges(subjectId, false, excluded)
	if err != nil {
		return 0, err
	}
	return len(views), nil
}

func (a *Aggregator) countFriends(subjectId string, excluded map[string]struct{}) (int, error) {
	entries, err := a.mergedFriendEntries(subjectId, excluded)
	if err != nil {
		return 0, err
	}
	return len(entries), nil
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
