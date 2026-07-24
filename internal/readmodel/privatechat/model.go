// Package privatechat defines the compact, materialized private-chat read model.
// It intentionally contains no aggregator logic so both privatechat and
// groupchat can consume the same records without creating an import cycle.
package privatechat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	StateKeyString = "pchat-read-model-state:v1"
	IndexKeyPrefix = "pchat-idx:v1:"
	TimeKeyPrefix  = "pchat-time:v1:"
	MetaKeyPrefix  = "pchat-meta:v1:"
	LocatorPrefix  = "pchat-loc:v1:"
	HomeKeyPrefix  = "pchat-home:v1:"

	StatusBuilding = "building"
	StatusReady    = "ready"
)

// State is the durable feature gate. Readers only use materialized records
// after a complete backfill has been structurally verified and marked ready.
type State struct {
	Version           int       `json:"version"`
	Status            string    `json:"status"`
	SourceCount       int64     `json:"sourceCount"`
	IndexedCount      int64     `json:"indexedCount"`
	LocatorCount      int64     `json:"locatorCount"`
	HomeCount         int64     `json:"homeCount"`
	ConversationCount int64     `json:"conversationCount"`
	DuplicateCount    int64     `json:"duplicateCount"`
	InvalidCount      int64     `json:"invalidCount"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

type ConversationMeta struct {
	Count           int64 `json:"count"`
	NextIndex       int64 `json:"nextIndex"`
	LatestTimestamp int64 `json:"latestTimestamp"`
}

type Locator struct {
	StorageKey   string `json:"storageKey"`
	Conversation string `json:"conversation"`
	Index        int64  `json:"index"`
	Timestamp    int64  `json:"timestamp"`
}

// HomeRecord points at the latest source-of-truth message for one conversation.
// Peer identity fields keep the hot read path free of profile scans.
type HomeRecord struct {
	StorageKey       string          `json:"storageKey"`
	Conversation     string          `json:"conversation"`
	Index            int64           `json:"index"`
	Timestamp        int64           `json:"timestamp"`
	PeerMetaID       string          `json:"peerMetaId"`
	PeerGlobalMetaID string          `json:"peerGlobalMetaId,omitempty"`
	PeerAddress      string          `json:"peerAddress,omitempty"`
	Message          json.RawMessage `json:"message"`
}

func EncodeIdentity(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strings.ToLower(strings.TrimSpace(value))))
}

func ConversationID(first, second string) string {
	first = strings.ToLower(strings.TrimSpace(first))
	second = strings.ToLower(strings.TrimSpace(second))
	if first > second {
		first, second = second, first
	}
	return EncodeIdentity(first) + "." + EncodeIdentity(second)
}

func StateKey() []byte { return []byte(StateKeyString) }

func IndexPrefix(conversation string) []byte {
	return []byte(IndexKeyPrefix + conversation + ":")
}

func IndexKey(conversation string, index int64) []byte {
	return []byte(fmt.Sprintf("%s%s:%019d", IndexKeyPrefix, conversation, index))
}

func TimePrefix(conversation string) []byte {
	return []byte(TimeKeyPrefix + conversation + ":")
}

func TimeKey(conversation string, timestamp int64, index int64) []byte {
	return []byte(fmt.Sprintf("%s%s:%019d:%019d", TimeKeyPrefix, conversation, timestamp, index))
}

func MetaKey(conversation string) []byte {
	return []byte(MetaKeyPrefix + conversation)
}

func LocatorKey(pinID string) []byte {
	return []byte(LocatorPrefix + EncodeIdentity(pinID))
}

func HomePrefix(alias string) []byte {
	return []byte(HomeKeyPrefix + EncodeIdentity(alias) + ":")
}

func HomeKey(alias, conversation string) []byte {
	return []byte(HomeKeyPrefix + EncodeIdentity(alias) + ":" + conversation)
}

func PrefixUpperBound(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			return end[:i+1]
		}
	}
	return nil
}

func LoadState(store *storage.PebbleStore, namespace string) (State, error) {
	var state State
	raw, err := store.Get(namespace, StateKey())
	if err != nil {
		if err == pebble.ErrNotFound {
			return state, nil
		}
		return state, err
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	return state, nil
}

func IsReady(store *storage.PebbleStore, namespace string) bool {
	if store == nil {
		return false
	}
	state, err := LoadState(store, namespace)
	return err == nil && state.Version == 1 && state.Status == StatusReady && state.IndexedCount >= 0
}

func ListHomes(store *storage.PebbleStore, namespace, alias string) ([]HomeRecord, error) {
	if store == nil || strings.TrimSpace(alias) == "" {
		return nil, nil
	}
	var result []HomeRecord
	err := store.ScanPrefix(namespace, HomePrefix(alias), func(_, value []byte) error {
		var record HomeRecord
		if err := json.Unmarshal(value, &record); err != nil {
			return err
		}
		result = append(result, record)
		return nil
	})
	return result, err
}
