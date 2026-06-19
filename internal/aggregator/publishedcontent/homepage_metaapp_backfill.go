package publishedcontent

import (
	"encoding/json"
	"errors"

	"github.com/cockroachdb/pebble"
)

func (a *Aggregator) ensureHomepageMetaAppGlobalIndexes() error {
	if a == nil || a.store == nil {
		return nil
	}

	ready, err := a.homepageMetaAppsGlobalIdentityStateReady()
	if err != nil || ready {
		return err
	}

	a.indexMu.Lock()
	defer a.indexMu.Unlock()

	ready, err = a.homepageMetaAppsGlobalIdentityStateReady()
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
		if rec.ProtocolPath != PathMetaApp || rec.Hidden || rec.ChainName == "" || rec.SourcePinId == "" || rec.PublisherGlobalMetaId == "" {
			return nil
		}
		return batch.Set(
			byGlobalKey(rec.ProtocolPath, rec.PublisherGlobalMetaId, rec.sortTimestamp(), rec.ChainName, rec.SourcePinId),
			[]byte{},
			pebble.Sync,
		)
	}); err != nil {
		return err
	}

	if err := batch.Set(homepageMetaAppsGlobalIdentityStateKey(), []byte("done"), pebble.Sync); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (a *Aggregator) homepageMetaAppsGlobalIdentityStateReady() (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	if _, err := a.store.Get(Namespace, homepageMetaAppsGlobalIdentityStateKey()); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return false, err
	}
	return false, nil
}
