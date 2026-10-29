package social

import (
	"encoding/json"
	"strings"
)

// RebuildMutualIndex reconstructs the edge:mutual reverse index from the
// canonical pair edges already stored under edge:pair:*. It is the migration
// path for stores populated before the mutual index existed: the index is
// wiped and recomputed for every pair with both directions active. Returns
// the number of mutual pairs written (each pair materializes two index
// entries, one per subject).
func (a *Aggregator) RebuildMutualIndex() (int, error) {
	if a == nil || a.store == nil {
		return 0, ErrUnavailable
	}

	type pair struct {
		follower string
		target   string
	}
	edges := make(map[pair]*FollowEdge)
	err := a.store.ScanPrefix(namespace, []byte(keyEdgeByPair), func(key, value []byte) error {
		parts := strings.SplitN(strings.TrimPrefix(string(key), keyEdgeByPair), ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil
		}
		var edge FollowEdge
		if err := json.Unmarshal(value, &edge); err != nil {
			return nil
		}
		if !edge.Active {
			return nil
		}
		edges[pair{follower: parts[0], target: parts[1]}] = &edge
		return nil
	})
	if err != nil {
		return 0, err
	}

	if err := a.store.DeleteByPrefix(namespace, []byte(keyMutualIndex)); err != nil {
		return 0, err
	}

	written := 0
	for p := range edges {
		if _, mutual := edges[pair{follower: p.target, target: p.follower}]; !mutual {
			continue
		}
		// Process each unordered pair exactly once (maintainMutualForPair
		// writes both per-subject entries); the guard also skips self-edges.
		if p.follower >= p.target {
			continue
		}
		if err := a.maintainMutualForPair(p.follower, p.target); err != nil {
			return written, err
		}
		written++
	}
	return written, nil
}
