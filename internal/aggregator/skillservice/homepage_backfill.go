package skillservice

import (
	"encoding/json"
	"errors"

	"github.com/cockroachdb/pebble"
)

func (a *Aggregator) ensureHomepageProviderGlobalIndexes() error {
	if a == nil || a.store == nil {
		return nil
	}

	ready, err := a.homepageProviderGlobalIndexStateReady()
	if err != nil || ready {
		return err
	}

	a.homepageIndex.Lock()
	defer a.homepageIndex.Unlock()

	ready, err = a.homepageProviderGlobalIndexStateReady()
	if err != nil || ready {
		return err
	}

	db, err := a.store.OpenDB(NamespaceService)
	if err != nil {
		return err
	}

	batch := db.NewBatch()
	defer batch.Close()
	for _, prefix := range [][]byte{
		[]byte(keyServiceByProviderGlobal),
		[]byte(keyServiceByProviderGlobalChain),
	} {
		if err := batch.DeleteRange(prefix, homepagePrefixUpperBound(prefix), pebble.Sync); err != nil {
			return err
		}
	}

	if err := a.store.ScanPrefix(NamespaceService, []byte(keyService), func(_, value []byte) error {
		var rec ServiceRecord
		if e := json.Unmarshal(value, &rec); e != nil {
			return nil
		}
		if rec.ChainName == "" || rec.SourceServicePinId == "" || rec.UpdatedAt <= 0 {
			return nil
		}
		for _, providerGlobalMetaId := range a.homepageProviderGlobalMetaIds(&rec) {
			if err := batch.Set(
				providerGlobalIndexKey(providerGlobalMetaId, rec.UpdatedAt, rec.ChainName, rec.SourceServicePinId),
				[]byte{},
				pebble.Sync,
			); err != nil {
				return err
			}
			if err := batch.Set(
				providerGlobalChainIndexKey(providerGlobalMetaId, rec.ChainName, rec.UpdatedAt, rec.SourceServicePinId),
				[]byte{},
				pebble.Sync,
			); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}

	if err := batch.Set(homepageProviderGlobalIndexStateKey(), []byte("done"), pebble.Sync); err != nil {
		return err
	}
	return batch.Commit(pebble.Sync)
}

func (a *Aggregator) invalidateHomepageProviderGlobalIndexState() error {
	if a == nil || a.store == nil {
		return nil
	}
	a.homepageIndex.Lock()
	defer a.homepageIndex.Unlock()
	return a.store.Delete(NamespaceService, homepageProviderGlobalIndexStateKey())
}

func (a *Aggregator) homepageProviderGlobalIndexStateReady() (bool, error) {
	if a == nil || a.store == nil {
		return false, nil
	}
	if _, err := a.store.Get(NamespaceService, homepageProviderGlobalIndexStateKey()); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return false, err
	}
	return false, nil
}

func homepagePrefixUpperBound(prefix []byte) []byte {
	end := append([]byte(nil), prefix...)
	for i := len(end) - 1; i >= 0; i-- {
		end[i]++
		if end[i] != 0 {
			return end[:i+1]
		}
	}
	return nil
}
