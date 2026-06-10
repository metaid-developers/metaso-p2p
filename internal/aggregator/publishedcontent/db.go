package publishedcontent

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	keyRecord      = "record:"
	keyPinToSource = "pin_to_source:"
	keyByGlobal    = "by_global:"
	keyByMetaId    = "by_metaid:"
	keyByAddress   = "by_address:"
)

func recordKey(chainName, protocolPath, sourcePinId string) []byte {
	return []byte(keyRecord + chainName + ":" + protocolPath + ":" + sourcePinId)
}

func pinToSourceKey(chainName, pinId string) []byte {
	return []byte(keyPinToSource + chainName + ":" + pinId)
}

func byGlobalKey(protocolPath, globalMetaId string, sortKey int64, chainName, sourcePinId string) []byte {
	return identityIndexKey(keyByGlobal, protocolPath, globalMetaId, sortKey, chainName, sourcePinId)
}

func byMetaIdKey(protocolPath, metaId string, sortKey int64, chainName, sourcePinId string) []byte {
	return identityIndexKey(keyByMetaId, protocolPath, metaId, sortKey, chainName, sourcePinId)
}

func byAddressKey(protocolPath, address string, sortKey int64, chainName, sourcePinId string) []byte {
	return identityIndexKey(keyByAddress, protocolPath, address, sortKey, chainName, sourcePinId)
}

func identityIndexKey(prefix, protocolPath, identity string, sortKey int64, chainName, sourcePinId string) []byte {
	return []byte(prefix + protocolPath + ":" + identity + ":" + invertedTimestamp(sortKey) + ":" + chainName + ":" + sourcePinId)
}

func identityIndexPrefix(prefix, protocolPath, identity string) []byte {
	return []byte(prefix + protocolPath + ":" + identity + ":")
}

func invertedTimestamp(ts int64) string {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, ^uint64(ts))
	return fmt.Sprintf("%016x", binary.BigEndian.Uint64(buf))
}

func parseIdentityIndexKey(key []byte, prefix []byte) (chainName, sourcePinId string, ok bool) {
	rest := strings.TrimPrefix(string(key), string(prefix))
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) != 3 {
		return "", "", false
	}
	return parts[1], parts[2], true
}

func (a *Aggregator) loadRecord(chainName, protocolPath, sourcePinId string) (*Record, error) {
	if chainName == "" || protocolPath == "" || sourcePinId == "" {
		return nil, errors.New("loadRecord: chainName, protocolPath and sourcePinId required")
	}
	raw, err := a.store.Get(Namespace, recordKey(chainName, protocolPath, sourcePinId))
	if err != nil || raw == nil {
		return nil, nil
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, fmt.Errorf("loadRecord: corrupt record %s/%s/%s: %w", chainName, protocolPath, sourcePinId, err)
	}
	return &rec, nil
}

func (a *Aggregator) sourcePinIdFor(chainName, pinId string) string {
	if chainName == "" || pinId == "" {
		return ""
	}
	seen := map[string]bool{}
	current := pinId
	for i := 0; i < 32; i++ {
		if seen[current] {
			return ""
		}
		seen[current] = true
		raw, err := a.store.Get(Namespace, pinToSourceKey(chainName, current))
		if err != nil || raw == nil {
			if current == pinId {
				return pinId
			}
			return current
		}
		next := strings.TrimSpace(string(raw))
		if next == "" || next == current {
			return current
		}
		current = next
	}
	return ""
}

func (a *Aggregator) loadRecordByAnyPinId(chainName, protocolPath, anyPinId string) (*Record, error) {
	sourcePinId := a.sourcePinIdFor(chainName, anyPinId)
	if sourcePinId == "" {
		return nil, nil
	}
	return a.loadRecord(chainName, protocolPath, sourcePinId)
}

func (a *Aggregator) saveRecord(rec *Record, previous *Record) error {
	if rec == nil {
		return errors.New("saveRecord: nil record")
	}
	if rec.ChainName == "" || rec.ProtocolPath == "" || rec.SourcePinId == "" {
		return errors.New("saveRecord: missing key fields")
	}
	if previous != nil {
		a.deleteIdentityIndexes(previous)
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := a.store.Set(Namespace, recordKey(rec.ChainName, rec.ProtocolPath, rec.SourcePinId), raw); err != nil {
		return err
	}
	if err := a.mapPinToSource(rec.ChainName, rec.CurrentPinId, rec.SourcePinId); err != nil {
		return err
	}
	return a.writeIdentityIndexes(rec)
}

func (a *Aggregator) mapPinToSource(chainName, pinId, sourcePinId string) error {
	if chainName == "" || pinId == "" || sourcePinId == "" {
		return nil
	}
	return a.store.Set(Namespace, pinToSourceKey(chainName, pinId), []byte(sourcePinId))
}

func (a *Aggregator) writeIdentityIndexes(rec *Record) error {
	sortKey := rec.sortTimestamp()
	if rec.PublisherGlobalMetaId != "" {
		if err := a.store.Set(Namespace, byGlobalKey(rec.ProtocolPath, rec.PublisherGlobalMetaId, sortKey, rec.ChainName, rec.SourcePinId), []byte{}); err != nil {
			return err
		}
	}
	if rec.PublisherMetaId != "" {
		if err := a.store.Set(Namespace, byMetaIdKey(rec.ProtocolPath, rec.PublisherMetaId, sortKey, rec.ChainName, rec.SourcePinId), []byte{}); err != nil {
			return err
		}
	}
	if rec.PublisherAddress != "" {
		if err := a.store.Set(Namespace, byAddressKey(rec.ProtocolPath, rec.PublisherAddress, sortKey, rec.ChainName, rec.SourcePinId), []byte{}); err != nil {
			return err
		}
	}
	return nil
}

func (a *Aggregator) deleteIdentityIndexes(rec *Record) {
	sortKey := rec.sortTimestamp()
	if rec.PublisherGlobalMetaId != "" {
		_ = a.store.Delete(Namespace, byGlobalKey(rec.ProtocolPath, rec.PublisherGlobalMetaId, sortKey, rec.ChainName, rec.SourcePinId))
	}
	if rec.PublisherMetaId != "" {
		_ = a.store.Delete(Namespace, byMetaIdKey(rec.ProtocolPath, rec.PublisherMetaId, sortKey, rec.ChainName, rec.SourcePinId))
	}
	if rec.PublisherAddress != "" {
		_ = a.store.Delete(Namespace, byAddressKey(rec.ProtocolPath, rec.PublisherAddress, sortKey, rec.ChainName, rec.SourcePinId))
	}
}

func (r *Record) sortTimestamp() int64 {
	if r == nil {
		return 0
	}
	if r.ProtocolPath == PathSimpleBuzz {
		return r.CreatedAt
	}
	if r.UpdatedAt > 0 {
		return r.UpdatedAt
	}
	return r.CreatedAt
}
