package userinfo

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/api"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

const (
	globalMetaIDMinimumPrefixLength = 8
	globalMetaIDMaximumPrefixLength = 90

	globalMetaIDCreationKeyPrefix    = "globalmetaid-created:v1:"
	globalMetaIDPrefixKeyPrefix      = "globalmetaid-prefix:v1:"
	globalMetaIDPrefixStateKeyString = "globalmetaid-prefix-state:v1"

	globalMetaIDPrefixStateBuilding = "building"
	globalMetaIDPrefixStateReady    = "ready"

	globalMetaIDPrefixInternalErrorCode = 50000
	globalMetaIDPrefixNotReadyCode      = 50300
)

var errGlobalMetaIDPrefixMatchFound = errors.New("globalMetaId prefix match found")

type globalMetaIDCreationRecord struct {
	GlobalMetaID  string `json:"globalMetaId"`
	MetaID        string `json:"metaid"`
	CreatedAt     int64  `json:"createdAt"`
	ChainName     string `json:"chainName"`
	GenesisHeight int64  `json:"genesisHeight"`
	PinID         string `json:"pinId"`
}

type globalMetaIDPrefixIndexState struct {
	Status           string `json:"status"`
	Cursor           string `json:"cursor,omitempty"`
	IndexedCount     int64  `json:"indexedCount"`
	DuplicateCount   int64  `json:"duplicateCount"`
	ReplacedCount    int64  `json:"replacedCount"`
	InvalidCount     int64  `json:"invalidCount"`
	MissingTimeCount int64  `json:"missingTimestampCount"`
	UpdatedAt        int64  `json:"updatedAt"`
}

type globalMetaIDPrefixUpsertResult struct {
	Inserted  int64
	Replaced  int64
	Duplicate int64
}

func (a *Aggregator) handleGlobalMetaIDPrefix(c *gin.Context) {
	prefix, err := normaliseGlobalMetaIDPrefix(c.Query("prefix"))
	if err != nil {
		api.RespErr(c, api.MetaFileInvalidParamCode, "valid globalMetaId prefix is required")
		return
	}

	state, err := a.loadGlobalMetaIDPrefixIndexState()
	if err != nil {
		api.RespErr(c, globalMetaIDPrefixInternalErrorCode, "globalMetaId lookup failed")
		return
	}
	if state == nil || state.Status != globalMetaIDPrefixStateReady {
		api.RespErr(c, globalMetaIDPrefixNotReadyCode, "globalMetaId prefix index is not ready")
		return
	}

	globalMetaID, err := a.lookupGlobalMetaIDPrefix(prefix)
	if err != nil {
		api.RespErr(c, globalMetaIDPrefixInternalErrorCode, "globalMetaId lookup failed")
		return
	}
	if globalMetaID == "" {
		api.RespErr(c, api.MetaFileNotFoundCode, "globalMetaId not found")
		return
	}

	api.RespSuccessCode(c, api.MetaFileSuccessCode, gin.H{
		"globalMetaId": globalMetaID,
	})
}

func normaliseGlobalMetaIDPrefix(raw string) (string, error) {
	prefix := strings.ToLower(strings.TrimSpace(raw))
	if len(prefix) < globalMetaIDMinimumPrefixLength || len(prefix) > globalMetaIDMaximumPrefixLength {
		return "", errors.New("globalMetaId prefix length out of range")
	}
	if !strings.HasPrefix(prefix, idaddress.HRP) || len(prefix) < 4 || prefix[3] != idaddress.Separator[0] {
		return "", errors.New("invalid globalMetaId prefix header")
	}
	if _, ok := idaddress.CharVersion[prefix[2:3]]; !ok {
		return "", errors.New("invalid globalMetaId version")
	}
	for i := 4; i < len(prefix); i++ {
		if _, ok := idaddress.CharsetMap[prefix[i]]; !ok {
			return "", errors.New("invalid globalMetaId prefix character")
		}
	}
	return prefix, nil
}

func canonicalGlobalMetaID(candidate, address, chainName string) string {
	if candidate = strings.ToLower(strings.TrimSpace(candidate)); idaddress.ValidateIDAddress(candidate) {
		return candidate
	}
	derived := strings.ToLower(strings.TrimSpace(idaddress.EncodeGlobalMetaId(strings.TrimSpace(address), strings.TrimSpace(chainName))))
	if idaddress.ValidateIDAddress(derived) {
		return derived
	}
	return ""
}

func (a *Aggregator) indexConfirmedGlobalMetaIDRoot(profile *UserProfile, pin *aggregator.PinInscription) error {
	record, reason := globalMetaIDCreationRecordFromPin(profile, pin)
	if reason != "" {
		return nil
	}
	_, err := a.upsertGlobalMetaIDCreationRecords([]globalMetaIDCreationRecord{record})
	return err
}

func globalMetaIDCreationRecordFromPin(profile *UserProfile, pin *aggregator.PinInscription) (globalMetaIDCreationRecord, string) {
	if pin == nil || normaliseInfoPath(pin.Path) != "/" {
		return globalMetaIDCreationRecord{}, "invalid"
	}
	op := strings.ToLower(strings.TrimSpace(pin.Operation))
	if op != "" && op != "create" && op != "init" {
		return globalMetaIDCreationRecord{}, "invalid"
	}
	createdAt := normalisePinTimestampMillis(pin.Timestamp)
	if createdAt <= 0 {
		return globalMetaIDCreationRecord{}, "missing_timestamp"
	}

	metaID := strings.TrimSpace(pin.MetaId)
	if metaID == "" {
		metaID = strings.TrimSpace(pin.CreateMetaId)
	}
	address := strings.TrimSpace(pin.Address)
	if address == "" {
		address = strings.TrimSpace(pin.CreateAddress)
	}
	if metaID == "" {
		metaID = address
	}
	globalMetaID := strings.TrimSpace(pin.GlobalMetaId)
	if profile != nil {
		if strings.TrimSpace(profile.MetaID) != "" {
			metaID = strings.TrimSpace(profile.MetaID)
		}
		if strings.TrimSpace(profile.Address) != "" {
			address = strings.TrimSpace(profile.Address)
		}
		if strings.TrimSpace(profile.GlobalMetaID) != "" {
			globalMetaID = strings.TrimSpace(profile.GlobalMetaID)
		}
	}
	globalMetaID = canonicalGlobalMetaID(globalMetaID, address, pin.ChainName)
	if globalMetaID == "" || metaID == "" || strings.TrimSpace(pin.Id) == "" {
		return globalMetaIDCreationRecord{}, "invalid"
	}

	return globalMetaIDCreationRecord{
		GlobalMetaID:  globalMetaID,
		MetaID:        metaID,
		CreatedAt:     createdAt,
		ChainName:     strings.ToLower(strings.TrimSpace(pin.ChainName)),
		GenesisHeight: pin.GenesisHeight,
		PinID:         strings.TrimSpace(pin.Id),
	}, ""
}

func (a *Aggregator) lookupGlobalMetaIDPrefix(prefix string) (string, error) {
	if a == nil || a.store == nil {
		return "", errors.New("userinfo store is unavailable")
	}
	normalised, err := normaliseGlobalMetaIDPrefix(prefix)
	if err != nil {
		return "", err
	}
	bucketPrefix := globalMetaIDPrefixBucketKey(normalised[:globalMetaIDMinimumPrefixLength])
	found := ""
	err = a.store.ScanPrefix(namespace, bucketPrefix, func(_, value []byte) error {
		var record globalMetaIDCreationRecord
		if err := json.Unmarshal(value, &record); err != nil {
			return fmt.Errorf("decode GlobalMetaID prefix record: %w", err)
		}
		candidate := strings.ToLower(strings.TrimSpace(record.GlobalMetaID))
		if strings.HasPrefix(candidate, normalised) {
			found = candidate
			return errGlobalMetaIDPrefixMatchFound
		}
		return nil
	})
	if errors.Is(err, errGlobalMetaIDPrefixMatchFound) {
		return found, nil
	}
	return found, err
}

func (a *Aggregator) upsertGlobalMetaIDCreationRecords(records []globalMetaIDCreationRecord) (globalMetaIDPrefixUpsertResult, error) {
	return a.upsertGlobalMetaIDCreationRecordsWithState(records, nil)
}

func (a *Aggregator) upsertGlobalMetaIDCreationRecordsWithState(records []globalMetaIDCreationRecord, state *globalMetaIDPrefixIndexState) (globalMetaIDPrefixUpsertResult, error) {
	var result globalMetaIDPrefixUpsertResult
	if a == nil || a.store == nil || (len(records) == 0 && state == nil) {
		return result, nil
	}

	// Collapse repeated identities in the same source page before reading the
	// persisted index. The earliest record is the only candidate that matters.
	deduped := make(map[string]globalMetaIDCreationRecord, len(records))
	for _, record := range records {
		key := strings.ToLower(strings.TrimSpace(record.GlobalMetaID))
		record.GlobalMetaID = key
		if current, ok := deduped[key]; !ok || globalMetaIDCreationBefore(record, current) {
			deduped[key] = record
		} else {
			result.Duplicate++
		}
	}

	a.globalMetaIDPrefixMu.Lock()
	defer a.globalMetaIDPrefixMu.Unlock()

	db, err := a.store.OpenDB(namespace)
	if err != nil {
		return result, err
	}
	batch := db.NewBatch()
	defer batch.Close()
	hasWrites := false

	for _, candidate := range deduped {
		current, err := loadGlobalMetaIDCreationRecord(db, globalMetaIDCreationKey(candidate.GlobalMetaID))
		if err != nil {
			return result, err
		}
		if current != nil && !globalMetaIDCreationBefore(candidate, *current) {
			result.Duplicate++
			continue
		}
		if current != nil {
			if err := batch.Delete(globalMetaIDPrefixRecordKey(*current), pebble.NoSync); err != nil {
				return result, err
			}
			result.Replaced++
		} else {
			result.Inserted++
		}
		raw, err := json.Marshal(candidate)
		if err != nil {
			return result, err
		}
		if err := batch.Set(globalMetaIDCreationKey(candidate.GlobalMetaID), raw, pebble.NoSync); err != nil {
			return result, err
		}
		if err := batch.Set(globalMetaIDPrefixRecordKey(candidate), raw, pebble.NoSync); err != nil {
			return result, err
		}
		hasWrites = true
	}
	var committedState globalMetaIDPrefixIndexState
	if state != nil {
		committedState = *state
		committedState.IndexedCount += result.Inserted
		committedState.DuplicateCount += result.Duplicate
		committedState.ReplacedCount += result.Replaced
		committedState.UpdatedAt = time.Now().UnixMilli()
		raw, err := json.Marshal(committedState)
		if err != nil {
			return result, err
		}
		if err := batch.Set(globalMetaIDPrefixStateKey(), raw, pebble.NoSync); err != nil {
			return result, err
		}
		hasWrites = true
	}
	if !hasWrites {
		return result, nil
	}
	if err := batch.Commit(pebble.Sync); err != nil {
		return result, err
	}
	if state != nil {
		*state = committedState
	}
	return result, nil
}

func loadGlobalMetaIDCreationRecord(db *pebble.DB, key []byte) (*globalMetaIDCreationRecord, error) {
	raw, closer, err := db.Get(key)
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	var record globalMetaIDCreationRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("decode GlobalMetaID creation record: %w", err)
	}
	return &record, nil
}

func globalMetaIDCreationBefore(left, right globalMetaIDCreationRecord) bool {
	if left.CreatedAt != right.CreatedAt {
		return left.CreatedAt < right.CreatedAt
	}
	if left.ChainName != right.ChainName {
		return left.ChainName < right.ChainName
	}
	if left.GenesisHeight != right.GenesisHeight {
		return left.GenesisHeight < right.GenesisHeight
	}
	if left.PinID != right.PinID {
		return left.PinID < right.PinID
	}
	return left.GlobalMetaID < right.GlobalMetaID
}

func globalMetaIDCreationKey(globalMetaID string) []byte {
	return []byte(globalMetaIDCreationKeyPrefix + strings.ToLower(strings.TrimSpace(globalMetaID)))
}

func globalMetaIDPrefixBucketKey(prefix string) []byte {
	return []byte(globalMetaIDPrefixKeyPrefix + strings.ToLower(strings.TrimSpace(prefix)) + ":")
}

func globalMetaIDPrefixRecordKey(record globalMetaIDCreationRecord) []byte {
	bucket := record.GlobalMetaID
	if len(bucket) > globalMetaIDMinimumPrefixLength {
		bucket = bucket[:globalMetaIDMinimumPrefixLength]
	}
	return []byte(fmt.Sprintf(
		"%s%016x:%s:%016x:%s:%s",
		globalMetaIDPrefixBucketKey(bucket),
		uint64(record.CreatedAt),
		strings.ToLower(strings.TrimSpace(record.ChainName)),
		uint64(record.GenesisHeight)^(uint64(1)<<63),
		strings.TrimSpace(record.PinID),
		strings.ToLower(strings.TrimSpace(record.GlobalMetaID)),
	))
}

func globalMetaIDPrefixStateKey() []byte {
	return []byte(globalMetaIDPrefixStateKeyString)
}

func (a *Aggregator) loadGlobalMetaIDPrefixIndexState() (*globalMetaIDPrefixIndexState, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("userinfo store is unavailable")
	}
	raw, err := a.store.Get(namespace, globalMetaIDPrefixStateKey())
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var state globalMetaIDPrefixIndexState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, fmt.Errorf("decode GlobalMetaID prefix state: %w", err)
	}
	return &state, nil
}

func (a *Aggregator) saveGlobalMetaIDPrefixIndexState(state globalMetaIDPrefixIndexState) error {
	if a == nil || a.store == nil {
		return errors.New("userinfo store is unavailable")
	}
	state.UpdatedAt = time.Now().UnixMilli()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, globalMetaIDPrefixStateKey(), raw)
}
