package publishedcontent

import (
	"encoding/json"
	"sort"
	"strings"
)

const (
	defaultListSize = 20
	maxListSize     = 100
)

func (a *Aggregator) List(params ListParams) (*ListResult, error) {
	params = normaliseListParams(params)
	limit := params.Size + 1

	records, err := a.collectRecords(params, limit)
	if err != nil {
		return nil, err
	}
	if !params.hasIdentityFilter() {
		sort.Slice(records, func(i, j int) bool {
			left := records[i].sortTimestamp()
			right := records[j].sortTimestamp()
			if left == right {
				if records[i].ChainName == records[j].ChainName {
					return records[i].SourcePinId > records[j].SourcePinId
				}
				return records[i].ChainName > records[j].ChainName
			}
			return left > right
		})
		if len(records) > limit {
			records = records[:limit]
		}
	}

	hasMore := len(records) > params.Size
	if hasMore {
		records = records[:params.Size]
	}

	items := make([]SectionItem, 0, len(records))
	for _, rec := range records {
		items = append(items, toSectionItem(rec))
	}
	return &ListResult{Items: items, HasMore: hasMore}, nil
}

func normaliseListParams(params ListParams) ListParams {
	params.ProtocolPath = protocolPathFromPinPath(params.ProtocolPath)
	params.ChainName = strings.ToLower(strings.TrimSpace(params.ChainName))
	params.PublisherGlobalMetaId = strings.TrimSpace(params.PublisherGlobalMetaId)
	params.PublisherMetaId = strings.TrimSpace(params.PublisherMetaId)
	params.PublisherAddress = strings.TrimSpace(params.PublisherAddress)
	if params.Size <= 0 {
		params.Size = defaultListSize
	}
	if params.Size > maxListSize {
		params.Size = maxListSize
	}
	return params
}

func (params ListParams) hasIdentityFilter() bool {
	return params.PublisherGlobalMetaId != "" || params.PublisherMetaId != "" || params.PublisherAddress != ""
}

func (a *Aggregator) collectRecords(params ListParams, limit int) ([]*Record, error) {
	switch {
	case params.PublisherGlobalMetaId != "":
		return a.scanIdentity(identityIndexPrefix(keyByGlobal, params.ProtocolPath, params.PublisherGlobalMetaId), params, limit)
	case params.PublisherMetaId != "":
		return a.scanIdentity(identityIndexPrefix(keyByMetaId, params.ProtocolPath, params.PublisherMetaId), params, limit)
	case params.PublisherAddress != "":
		return a.scanIdentity(identityIndexPrefix(keyByAddress, params.ProtocolPath, params.PublisherAddress), params, limit)
	default:
		return a.scanRecords(params, limit)
	}
}

func (a *Aggregator) scanIdentity(prefix []byte, params ListParams, limit int) ([]*Record, error) {
	out := make([]*Record, 0, limit)
	err := a.store.ScanPrefix(Namespace, prefix, func(key, _ []byte) error {
		chainName, sourcePinId, ok := parseIdentityIndexKey(key, prefix)
		if !ok {
			return nil
		}
		if params.ChainName != "" && chainName != params.ChainName {
			return nil
		}
		rec, err := a.loadRecord(chainName, params.ProtocolPath, sourcePinId)
		if err != nil || rec == nil {
			return err
		}
		if !recordMatches(rec, params) {
			return nil
		}
		out = append(out, rec)
		if len(out) >= limit {
			return errStopScan
		}
		return nil
	})
	if err == errStopScan {
		err = nil
	}
	return out, err
}

func (a *Aggregator) scanRecords(params ListParams, limit int) ([]*Record, error) {
	prefix := []byte(keyRecord)
	if params.ChainName != "" {
		prefix = []byte(keyRecord + params.ChainName + ":")
	}
	out := make([]*Record, 0, limit)
	err := a.store.ScanPrefix(Namespace, prefix, func(_, value []byte) error {
		var rec Record
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		if !recordMatches(&rec, params) {
			return nil
		}
		out = append(out, &rec)
		return nil
	})
	return out, err
}

func recordMatches(rec *Record, params ListParams) bool {
	if rec == nil {
		return false
	}
	if params.ProtocolPath != "" && rec.ProtocolPath != params.ProtocolPath {
		return false
	}
	if params.ChainName != "" && rec.ChainName != params.ChainName {
		return false
	}
	if !params.IncludeHidden && rec.Hidden {
		return false
	}
	if params.PublisherGlobalMetaId != "" && rec.PublisherGlobalMetaId != params.PublisherGlobalMetaId {
		return false
	}
	if params.PublisherMetaId != "" && rec.PublisherMetaId != params.PublisherMetaId {
		return false
	}
	if params.PublisherAddress != "" && rec.PublisherAddress != params.PublisherAddress {
		return false
	}
	return true
}

func toSectionItem(rec *Record) SectionItem {
	return SectionItem{
		SourcePinId:  rec.SourcePinId,
		CurrentPinId: rec.CurrentPinId,
		ChainName:    rec.ChainName,
		ProtocolPath: rec.ProtocolPath,

		PublisherGlobalMetaId: rec.PublisherGlobalMetaId,
		PublisherMetaId:       rec.PublisherMetaId,
		PublisherAddress:      rec.PublisherAddress,

		Operation: rec.Operation,
		Hidden:    rec.Hidden,
		IsMempool: rec.IsMempool,

		ContentType:    rec.ContentType,
		PayloadText:    rec.PayloadText,
		PayloadJSON:    rec.PayloadJSON,
		PayloadExposed: rec.PayloadExposed,

		CreatedAt: rec.CreatedAt,
		UpdatedAt: rec.UpdatedAt,

		SourceNumber:  rec.SourceNumber,
		CurrentNumber: rec.CurrentNumber,
		SourcePath:    rec.SourcePath,
		CurrentPath:   rec.CurrentPath,
		SourceHost:    rec.SourceHost,
		CurrentHost:   rec.CurrentHost,
	}
}

type stopScanError struct{}

func (stopScanError) Error() string { return "stop scan" }

var errStopScan error = stopScanError{}
