package skillservice

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Pebble key layout
//
// All keys are stored under namespace `skillservice` unless noted otherwise.
// Keys are lexicographically ordered so prefix scans give deterministic
// ordering. The chainName prefix is included up front so cross-chain scans
// can be cheap when we want them, while same-chain prefix scans stay fast.
//
//   service:<chainName>:<sourceServicePinId>                  → ServiceRecord JSON
//   pin_to_source:<chainName>:<pinId>                         → sourceServicePinId (string)
//   service_by_provider:<chainName>:<providerMetaId>:<sourceServicePinId>
//                                                             → "" (index marker)
//   service_by_updated:<chainName>:<padded_updatedAt>:<sourceServicePinId>
//                                                             → "" (index marker, descending by inverted ts)

const (
	keyService           = "service:"
	keyPinToSource       = "pin_to_source:"
	keyServiceByProvider = "service_by_provider:"
	keyServiceByUpdated  = "service_by_updated:"
)

// serviceKey builds the primary Pebble key for a service record.
func serviceKey(chainName, sourcePinId string) []byte {
	return []byte(keyService + chainName + ":" + sourcePinId)
}

// pinToSourceKey maps any pin id in the version chain (create/modify/revoke)
// back to its sourceServicePinId. Used to resolve modify/revoke targets and
// to normalise ratings whose serviceID was written as a current or older
// pin id.
func pinToSourceKey(chainName, pinId string) []byte {
	return []byte(keyPinToSource + chainName + ":" + pinId)
}

// providerIndexKey is a secondary index used by list filtering.
func providerIndexKey(chainName, providerMetaId, sourcePinId string) []byte {
	return []byte(keyServiceByProvider + chainName + ":" + providerMetaId + ":" + sourcePinId)
}

// updatedIndexKey orders services by descending updatedAt within a chain.
// We invert the timestamp so a forward prefix scan returns newest first,
// which matches the spec's default sortBy=updated order.
func updatedIndexKey(chainName string, updatedAt int64, sourcePinId string) []byte {
	inverted := ^uint64(updatedAt)
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, inverted)
	hex := fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf))
	return []byte(keyServiceByUpdated + chainName + ":" + hex + ":" + sourcePinId)
}

// loadService fetches a ServiceRecord by its sourceServicePinId. Returns
// (nil, nil) when not found.
//
// The underlying PebbleStore.Get returns pebble.ErrNotFound for missing
// keys, but we collapse that to (nil, nil) here so callers do not need to
// import pebble themselves. Genuine I/O errors are masked alongside the
// not-found case; this matches the existing convention in
// internal/aggregator/userinfo. JSON corruption is still surfaced because
// it points to a real bug rather than absence of data.
func (a *Aggregator) loadService(chainName, sourcePinId string) (*ServiceRecord, error) {
	if chainName == "" || sourcePinId == "" {
		return nil, errors.New("loadService: chainName and sourcePinId required")
	}
	raw, err := a.store.Get(NamespaceService, serviceKey(chainName, sourcePinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rec ServiceRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("loadService: corrupt record at %s/%s: %w",
			chainName, sourcePinId, err)
	}
	return &rec, nil
}

// loadServiceByAnyPinId resolves any pin id in a version chain to its
// underlying service record. It first consults the pinToSource index for
// modify/revoke pin ids; if the lookup fails it falls back to treating the
// supplied id as a sourceServicePinId. Callers that already know the id
// is a sourceServicePinId should prefer loadService directly.
func (a *Aggregator) loadServiceByAnyPinId(chainName, anyPinId string) (*ServiceRecord, error) {
	if chainName == "" || anyPinId == "" {
		return nil, nil
	}
	sourcePinId := anyPinId
	mapped, err := a.store.Get(NamespaceService, pinToSourceKey(chainName, anyPinId))
	if err == nil && mapped != nil {
		sourcePinId = string(mapped)
	}
	return a.loadService(chainName, sourcePinId)
}

// saveService persists a service record together with its secondary
// indexes. If a previous version of the record exists, the corresponding
// index entries are removed first so the indexes stay consistent.
func (a *Aggregator) saveService(rec *ServiceRecord, previous *ServiceRecord) error {
	if rec == nil {
		return errors.New("saveService: nil record")
	}
	if rec.ChainName == "" || rec.SourceServicePinId == "" {
		return errors.New("saveService: missing chainName or sourceServicePinId")
	}

	if previous != nil {
		// Pebble's Delete is idempotent — it does not error on missing
		// keys — so we can safely tear down stale index entries even
		// when the index was never written (e.g. previous record had an
		// empty ProviderMetaId).
		if previous.ProviderMetaId != "" && previous.ProviderMetaId != rec.ProviderMetaId {
			_ = a.store.Delete(NamespaceService,
				providerIndexKey(previous.ChainName, previous.ProviderMetaId, previous.SourceServicePinId))
		}
		if previous.UpdatedAt > 0 && previous.UpdatedAt != rec.UpdatedAt {
			_ = a.store.Delete(NamespaceService,
				updatedIndexKey(previous.ChainName, previous.UpdatedAt, previous.SourceServicePinId))
		}
	}

	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := a.store.Set(NamespaceService, serviceKey(rec.ChainName, rec.SourceServicePinId), raw); err != nil {
		return err
	}
	if rec.ProviderMetaId != "" {
		if err := a.store.Set(NamespaceService,
			providerIndexKey(rec.ChainName, rec.ProviderMetaId, rec.SourceServicePinId), []byte{}); err != nil {
			return err
		}
	}
	if rec.UpdatedAt > 0 {
		if err := a.store.Set(NamespaceService,
			updatedIndexKey(rec.ChainName, rec.UpdatedAt, rec.SourceServicePinId), []byte{}); err != nil {
			return err
		}
	}
	return nil
}

// mapPinToSource records that a non-source pin id (modify / revoke) maps
// back to its sourceServicePinId. Idempotent.
func (a *Aggregator) mapPinToSource(chainName, anyPinId, sourcePinId string) error {
	if chainName == "" || anyPinId == "" || sourcePinId == "" || anyPinId == sourcePinId {
		return nil
	}
	return a.store.Set(NamespaceService, pinToSourceKey(chainName, anyPinId), []byte(sourcePinId))
}

// listServicesByChain enumerates all persisted service records on a single
// chain. The order follows the lexicographic Pebble key order; callers that
// want sorted output should sort by ServiceRecord.UpdatedAt themselves.
//
// This is intended for M1 / M5 to walk small catalogs; a future iteration
// should replace it with cursor-paged scans over the secondary indexes.
func (a *Aggregator) listServicesByChain(chainName string) ([]*ServiceRecord, error) {
	prefix := []byte(keyService + chainName + ":")
	var out []*ServiceRecord
	err := a.store.ScanPrefix(NamespaceService, prefix, func(_, value []byte) error {
		var rec ServiceRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		out = append(out, &rec)
		return nil
	})
	return out, err
}

// listAllServices walks every chainName under the service prefix. Mostly
// useful for tests and admin views.
func (a *Aggregator) listAllServices() ([]*ServiceRecord, error) {
	prefix := []byte(keyService)
	var out []*ServiceRecord
	err := a.store.ScanPrefix(NamespaceService, prefix, func(_, value []byte) error {
		var rec ServiceRecord
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		out = append(out, &rec)
		return nil
	})
	return out, err
}

// parseServiceID extracts the chain-qualified pieces of a service key for
// the rare callsites that need to introspect raw keys (tests, debug). The
// expected key shape is `service:<chainName>:<sourcePinId>`.
func parseServiceID(key string) (chainName, sourcePinId string, ok bool) {
	if !strings.HasPrefix(key, keyService) {
		return "", "", false
	}
	rest := strings.TrimPrefix(key, keyService)
	idx := strings.IndexByte(rest, ':')
	if idx < 0 {
		return "", "", false
	}
	return rest[:idx], rest[idx+1:], true
}
