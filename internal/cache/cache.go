package cache

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2/expirable"

	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// CacheProvider manages two-level caches: L1 (in-memory LRU) and L2 (Pebble persistent).
// Each namespace gets independent L1 and L2 storage.
type CacheProvider struct {
	store      *storage.PebbleStore
	namespaces map[string]*namespaceCache
	mu         sync.RWMutex
}

type namespaceCache struct {
	l1       *lru.LRU[string, []byte] // in-memory LRU
	l2Prefix string                   // Pebble namespace prefix for L2
}

// New creates a CacheProvider backed by the given PebbleStore.
func New(store *storage.PebbleStore) *CacheProvider {
	return &CacheProvider{
		store:      store,
		namespaces: make(map[string]*namespaceCache),
	}
}

// Namespace returns a typed Cache for the given namespace.
// The first call for a namespace creates it; subsequent calls return the same instance.
func (cp *CacheProvider) Namespace(name string, maxEntries int, ttl time.Duration) *Cache[[]byte] {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	if ns, ok := cp.namespaces[name]; ok {
		return &Cache[[]byte]{ns: ns, name: name, provider: cp}
	}

	ns := &namespaceCache{
		l1:       lru.NewLRU[string, []byte](maxEntries, nil, ttl),
		l2Prefix: "cache_" + name,
	}
	cp.namespaces[name] = ns
	return &Cache[[]byte]{ns: ns, name: name, provider: cp}
}

// Cache is a typed two-level cache for a specific namespace.
type Cache[T any] struct {
	ns       *namespaceCache
	name     string
	provider *CacheProvider
}

// Get retrieves a value by key. Checks L1 first, then L2, then returns zero value if not found.
func (c *Cache[T]) Get(key string) (T, bool) {
	var zero T

	// L1: in-memory LRU
	if val, ok := c.ns.l1.Get(key); ok {
		var result T
		if err := unmarshalValue(val, &result); err == nil {
			return result, true
		}
	}

	// L2: Pebble persistent
	pebbleKey := []byte(key)
	raw, err := c.provider.store.Get(c.ns.l2Prefix, pebbleKey)
	if err != nil || raw == nil {
		return zero, false
	}

	var result T
	if err := unmarshalValue(raw, &result); err != nil {
		return zero, false
	}

	// Backfill L1
	c.ns.l1.Add(key, raw)

	return result, true
}

// Set stores a value in both L1 and L2 with the given TTL.
func (c *Cache[T]) Set(key string, value T, ttl time.Duration) error {
	raw, err := marshalValue(value)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	// L1: memory
	c.ns.l1.Add(key, raw)

	// L2: Pebble (ignore TTL at this level; L1 handles eviction)
	return c.provider.store.Set(c.ns.l2Prefix, []byte(key), raw)
}

// Delete removes a key from both L1 and L2.
func (c *Cache[T]) Delete(key string) error {
	c.ns.l1.Remove(key)
	return c.provider.store.Delete(c.ns.l2Prefix, []byte(key))
}

// InvalidateByPrefix removes all keys with the given prefix from both L1 and L2.
func (c *Cache[T]) InvalidateByPrefix(prefix string) error {
	// L1: We can't iterate an LRU by prefix, so we remove all (small trade-off)
	c.ns.l1.Purge()

	// L2: Pebble prefix delete
	return c.provider.store.DeleteByPrefix(c.ns.l2Prefix, []byte(prefix))
}

// marshalValue serializes a value to bytes.
func marshalValue(v any) ([]byte, error) {
	switch val := v.(type) {
	case []byte:
		return val, nil
	case string:
		return []byte(val), nil
	default:
		return json.Marshal(v)
	}
}

// unmarshalValue deserializes bytes into a value.
func unmarshalValue(data []byte, v any) error {
	switch ptr := v.(type) {
	case *[]byte:
		*ptr = make([]byte, len(data))
		copy(*ptr, data)
		return nil
	case *string:
		*ptr = string(data)
		return nil
	default:
		return json.Unmarshal(data, v)
	}
}

// Warmup preloads the given keys from L2 into L1 for the namespace.
// This is useful to reduce cold-start latency for frequently accessed data.
func (c *Cache[T]) Warmup(keys []string) {
	for _, key := range keys {
		pebbleKey := []byte(key)
		raw, err := c.provider.store.Get(c.ns.l2Prefix, pebbleKey)
		if err != nil || raw == nil {
			continue
		}
		c.ns.l1.Add(key, raw)
	}
}
