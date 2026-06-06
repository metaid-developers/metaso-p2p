package groupchat

import (
	"encoding/json"
	"log"

	"github.com/cockroachdb/pebble"
)

// Community represents a MetaID community.
type Community struct {
	CommunityId   string `json:"communityId"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Cover         string `json:"cover,omitempty"`
	Icon          string `json:"icon,omitempty"`
	Creator       string `json:"creator"`
	CreatorMetaId string `json:"creatorMetaId"`
	MemberCount   int64  `json:"memberCount"`
	CreatedAt     int64  `json:"createdAt"`
	Chain         string `json:"chain,omitempty"`
	BlockHeight   int64  `json:"blockHeight"`
}

const (
	communityPrefix = "community:"
)

func communityKey(communityId string) []byte {
	return []byte(communityPrefix + communityId)
}

// SaveCommunity persists a community to PebbleDB.
func (a *Aggregator) SaveCommunity(community *Community) error {
	raw, err := json.Marshal(community)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, communityKey(community.CommunityId), raw)
}

// GetCommunity retrieves a community by ID from PebbleDB.
func (a *Aggregator) GetCommunity(communityId string) (*Community, error) {
	raw, err := a.store.Get(namespace, communityKey(communityId))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}

	var community Community
	if err := json.Unmarshal(raw, &community); err != nil {
		log.Printf("[groupchat] failed to unmarshal community %s: %v", communityId, err)
		return nil, err
	}
	return &community, nil
}

// ListCommunities returns a paginated list of communities.
func (a *Aggregator) ListCommunities(page, pageSize int64) ([]*Community, int64, error) {
	var communities []*Community
	var total int64

	prefix := []byte(communityPrefix)
	err := a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		total++
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	skip := (page - 1) * pageSize
	var count int64

	err = a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		if count < skip {
			count++
			return nil
		}
		if int64(len(communities)) >= pageSize {
			return nil
		}

		var c Community
		if e := json.Unmarshal(value, &c); e != nil {
			return nil // skip corrupt entries
		}
		communities = append(communities, &c)
		count++
		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return communities, total, nil
}
