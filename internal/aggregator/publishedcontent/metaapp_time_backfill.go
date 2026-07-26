package publishedcontent

import (
	"encoding/json"
	"errors"

	"github.com/cockroachdb/pebble"
)

// ensureMetaAppTimeIndexes backfills the metaapp reverse-time index for
// records indexed before the by_time index existed. Mirrors
// ensureHomepageMetaAppGlobalIndexes: state-key gated, single-flight via
// indexMu, one batch over the record namespace.
func (a *Aggregator) ensureMetaAppTimeIndexes() error {
	if a == nil || a.store == nil {
		return nil
	}

	ready, err := a.metaAppTimeIndexStateReady()
	if err != nil || ready {
		return err
	}

	a.indexMu.Lock()
	defer a.indexMu.Unlock()

	ready, err = a.metaAppTimeIndexStateReady()
	if err != nil || ready {
		return err
	}

	db, err := a.store.OpenDB(Namespace)
	if err != nil {
		return err
	}

	batch := db.NewBatch()
	defer batch.Close()

	if err := a.store.ScanPrefix(Namespace, []byte(keyRecord), func(_, value []byte) error {
		var rec Record
		if e := json.Unmarshal(value, &rec); e != nil {
			return nil
		}
		if rec.ProtocolPath != PathMetaApp || rec.Hidden || rec.ChainName == "" || rec.SourcePinId == "" {
			return nil
		}
		return batch.Set(
			byTimeKey(rec.ProtocolPath, rec.sortTimestamp(), rec.ChainName, rec.SourcePinId),
			[]byte{},
			pebble.Sync,
		)
	}); err != nil {
		return err
	}

	if err := batch.Set(metaAppTimeIndexStateKey(), []byte("done"), pebble.Sync); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (a *Aggregator) metaAppTimeIndexStateReady() (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	if _, err := a.store.Get(Namespace, metaAppTimeIndexStateKey()); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return false, err
	}
	return false, nil
}
