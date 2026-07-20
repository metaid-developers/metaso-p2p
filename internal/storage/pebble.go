package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cockroachdb/pebble"
)

// PebbleStore manages multiple PebbleDB instances identified by namespace.
// Each namespace gets its own Pebble DB in a subdirectory under DataDir.
type PebbleStore struct {
	mu     sync.RWMutex
	dbs    map[string]*pebble.DB
	dir    string
	opened bool
}

// KeyValue is one entry in an atomic namespace batch.
type KeyValue struct {
	Key   []byte
	Value []byte
}

// NewPebbleStore creates a new PebbleStore rooted at dataDir.
func NewPebbleStore(dataDir string) *PebbleStore {
	return &PebbleStore{
		dbs: make(map[string]*pebble.DB),
		dir: dataDir,
	}
}

// OpenDB opens (or reuses) a Pebble DB for the given namespace.
// Each namespace maps to a subdirectory under dataDir.
func (s *PebbleStore) OpenDB(namespace string) (*pebble.DB, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if db, ok := s.dbs[namespace]; ok {
		return db, nil
	}

	dbPath := filepath.Join(s.dir, namespace)
	if err := os.MkdirAll(dbPath, 0755); err != nil {
		return nil, fmt.Errorf("create db dir %s: %w", dbPath, err)
	}

	db, err := pebble.Open(dbPath, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("open pebble %s: %w", dbPath, err)
	}

	s.dbs[namespace] = db
	s.opened = true
	return db, nil
}

// GetDB returns an already-opened DB for namespace, or nil.
func (s *PebbleStore) GetDB(namespace string) *pebble.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbs[namespace]
}

// Close closes all managed Pebble instances.
func (s *PebbleStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var errs []error
	for name, db := range s.dbs {
		if err := db.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close %s: %w", name, err))
		}
	}
	s.dbs = make(map[string]*pebble.DB)

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}

// Set writes a key-value pair to a namespace. The namespace DB is opened on demand.
func (s *PebbleStore) Set(namespace string, key, value []byte) error {
	db, err := s.OpenDB(namespace)
	if err != nil {
		return err
	}
	return db.Set(key, value, pebble.Sync)
}

// SetBatch atomically writes all entries to one namespace.
func (s *PebbleStore) SetBatch(namespace string, entries []KeyValue) error {
	if len(entries) == 0 {
		return nil
	}
	db, err := s.OpenDB(namespace)
	if err != nil {
		return err
	}
	batch := db.NewBatch()
	defer batch.Close()
	for _, entry := range entries {
		if err := batch.Set(entry.Key, entry.Value, nil); err != nil {
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

// Get reads a key from a namespace. Returns pebble.ErrNotFound if not found.
func (s *PebbleStore) Get(namespace string, key []byte) ([]byte, error) {
	db, err := s.OpenDB(namespace)
	if err != nil {
		return nil, err
	}
	val, closer, err := db.Get(key)
	if err != nil {
		return nil, err
	}
	defer closer.Close()

	// Copy the value before closing
	result := make([]byte, len(val))
	copy(result, val)
	return result, nil
}

// Delete removes a key from a namespace.
func (s *PebbleStore) Delete(namespace string, key []byte) error {
	db := s.GetDB(namespace)
	if db == nil {
		return nil
	}
	return db.Delete(key, pebble.Sync)
}

// DeleteByPrefix removes all keys with the given prefix from a namespace.
func (s *PebbleStore) DeleteByPrefix(namespace string, prefix []byte) error {
	db := s.GetDB(namespace)
	if db == nil {
		return nil
	}

	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	batch := db.NewBatch()
	for iter.First(); iter.Valid(); iter.Next() {
		if err := batch.Delete(iter.Key(), pebble.Sync); err != nil {
			batch.Close()
			return err
		}
	}
	return batch.Commit(pebble.Sync)
}

// ScanPrefix iterates over all keys matching the prefix and calls fn for each.
// If fn returns an error, iteration stops.
func (s *PebbleStore) ScanPrefix(namespace string, prefix []byte, fn func(key, value []byte) error) error {
	db, err := s.OpenDB(namespace)
	if err != nil {
		return err
	}

	iter, err := db.NewIter(&pebble.IterOptions{
		LowerBound: prefix,
		UpperBound: prefixUpperBound(prefix),
	})
	if err != nil {
		return err
	}
	defer iter.Close()

	for iter.First(); iter.Valid(); iter.Next() {
		if err := fn(iter.Key(), iter.Value()); err != nil {
			return err
		}
	}
	return nil
}

// prefixUpperBound computes the exclusive upper bound for a prefix scan.
func prefixUpperBound(prefix []byte) []byte {
	end := make([]byte, len(prefix))
	copy(end, prefix)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			return end[:i+1]
		}
	}
	return nil // prefix is all 0xff, no upper bound
}
