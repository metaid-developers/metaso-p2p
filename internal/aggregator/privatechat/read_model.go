package privatechat

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cockroachdb/pebble"

	privatechatread "github.com/metaid-developers/metaso-p2p/internal/readmodel/privatechat"
)

const privateChatReadModelBatchSize = 5000

type PrivateChatReadModelReport struct {
	State    privatechatread.State `json:"state"`
	Duration time.Duration         `json:"duration"`
}

type privateChatBackfillRecord struct {
	message    *PrivateMessage
	storageKey []byte
	dedupeKey  string
	identityA  string
	identityB  string
}

func (a *Aggregator) canonicalParticipant(profiles identityProfileCache, ids ...string) string {
	if profile := a.identityProfileCached(profiles, ids...); profile != nil {
		for _, value := range []string{profile.GlobalMetaId, profile.MetaId, profile.Address} {
			if value = strings.TrimSpace(value); value != "" {
				return strings.ToLower(value)
			}
		}
	}
	// Without a profile, the persisted MetaID field is the stable legacy key.
	// Callers pass it first and only fall back to globalMetaId/address.
	for _, value := range ids {
		if value = strings.TrimSpace(value); value != "" {
			return strings.ToLower(value)
		}
	}
	return ""
}

func (a *Aggregator) conversationForMessage(msg *PrivateMessage, profiles identityProfileCache) (string, string, string) {
	if msg == nil {
		return "", "", ""
	}
	from := a.canonicalParticipant(profiles, msg.From, msg.FromGlobalMetaId, msg.FromAddress)
	to := a.canonicalParticipant(profiles, msg.To, msg.ToGlobalMetaId, msg.ToAddress)
	if from == "" || to == "" {
		return "", from, to
	}
	return privatechatread.ConversationID(from, to), from, to
}

func (a *Aggregator) conversationForQuery(myMetaID, otherMetaID string, profiles identityProfileCache) string {
	my := a.canonicalParticipant(profiles, myMetaID)
	other := a.canonicalParticipant(profiles, otherMetaID)
	if my == "" || other == "" {
		return ""
	}
	return privatechatread.ConversationID(my, other)
}

func (a *Aggregator) loadConversationMeta(conversation string) (privatechatread.ConversationMeta, error) {
	var meta privatechatread.ConversationMeta
	if conversation == "" {
		return meta, nil
	}
	raw, err := a.store.Get(namespace, privatechatread.MetaKey(conversation))
	if errors.Is(err, pebble.ErrNotFound) {
		return meta, nil
	}
	if err != nil {
		return meta, err
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, fmt.Errorf("decode private chat conversation metadata: %w", err)
	}
	return meta, nil
}

func (a *Aggregator) loadReadModelLocator(pinID string) (*privatechatread.Locator, error) {
	if strings.TrimSpace(pinID) == "" {
		return nil, nil
	}
	raw, err := a.store.Get(namespace, privatechatread.LocatorKey(pinID))
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var locator privatechatread.Locator
	if err := json.Unmarshal(raw, &locator); err != nil {
		return nil, err
	}
	return &locator, nil
}

func marshalPrivateChatReadModel(value any) ([]byte, error) {
	return json.Marshal(value)
}

func privateChatMessageAliases(globalMetaID, metaID, address string) []string {
	seen := make(map[string]bool)
	aliases := make([]string, 0, 3)
	for _, value := range []string{globalMetaID, metaID, address} {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		aliases = append(aliases, value)
	}
	return aliases
}

func homeRecordsForMessage(msg *PrivateMessage, conversation string, storageKey, messageRaw []byte) map[string]privatechatread.HomeRecord {
	if msg == nil || conversation == "" || len(storageKey) == 0 {
		return nil
	}
	result := make(map[string]privatechatread.HomeRecord)
	add := func(aliases []string, peerMetaID, peerGlobalMetaID, peerAddress string) {
		record := privatechatread.HomeRecord{
			StorageKey:       string(storageKey),
			Conversation:     conversation,
			Index:            msg.Index,
			Timestamp:        msg.Timestamp,
			PeerMetaID:       peerMetaID,
			PeerGlobalMetaID: peerGlobalMetaID,
			PeerAddress:      peerAddress,
			Message:          append([]byte(nil), messageRaw...),
		}
		for _, alias := range aliases {
			result[alias] = record
		}
	}
	add(privateChatMessageAliases(msg.FromGlobalMetaId, msg.From, msg.FromAddress), msg.To, msg.ToGlobalMetaId, msg.ToAddress)
	add(privateChatMessageAliases(msg.ToGlobalMetaId, msg.To, msg.ToAddress), msg.From, msg.FromGlobalMetaId, msg.FromAddress)
	return result
}

func (a *Aggregator) writeReadModelEntries(
	batch *pebble.Batch,
	msg *PrivateMessage,
	storageKey []byte,
	previous *PrivateMessage,
	created bool,
) ([]byte, error) {
	if !a.readModelReady.Load() || batch == nil || msg == nil {
		return json.Marshal(msg)
	}
	profiles := make(identityProfileCache)
	conversation, _, _ := a.conversationForMessage(msg, profiles)
	if conversation == "" {
		return nil, fmt.Errorf("private chat read model: unresolved conversation")
	}

	meta, err := a.loadConversationMeta(conversation)
	if err != nil {
		return nil, err
	}
	if created {
		msg.Index = meta.NextIndex
		meta.NextIndex++
		meta.Count++
	}
	if msg.Index < 0 || msg.Index >= meta.NextIndex {
		return nil, fmt.Errorf("private chat read model: invalid index %d for next index %d", msg.Index, meta.NextIndex)
	}
	if msg.Timestamp > meta.LatestTimestamp {
		meta.LatestTimestamp = msg.Timestamp
	}

	if previous != nil {
		previousConversation, _, _ := a.conversationForMessage(previous, profiles)
		if previousConversation != "" && (previousConversation != conversation || previous.Index != msg.Index || previous.Timestamp != msg.Timestamp) {
			if err := batch.Delete(privatechatread.IndexKey(previousConversation, previous.Index), nil); err != nil {
				return nil, err
			}
			if err := batch.Delete(privatechatread.TimeKey(previousConversation, previous.Timestamp, previous.Index), nil); err != nil {
				return nil, err
			}
		}
	}

	metaRaw, err := marshalPrivateChatReadModel(meta)
	if err != nil {
		return nil, err
	}
	if err := batch.Set(privatechatread.MetaKey(conversation), metaRaw, nil); err != nil {
		return nil, err
	}
	messageRaw, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	if err := batch.Set(privatechatread.IndexKey(conversation, msg.Index), messageRaw, nil); err != nil {
		return nil, err
	}
	if err := batch.Set(privatechatread.TimeKey(conversation, msg.Timestamp, msg.Index), messageRaw, nil); err != nil {
		return nil, err
	}
	if strings.TrimSpace(msg.PinId) != "" {
		locatorRaw, err := marshalPrivateChatReadModel(privatechatread.Locator{
			StorageKey:   string(storageKey),
			Conversation: conversation,
			Index:        msg.Index,
			Timestamp:    msg.Timestamp,
		})
		if err != nil {
			return nil, err
		}
		if err := batch.Set(privatechatread.LocatorKey(msg.PinId), locatorRaw, nil); err != nil {
			return nil, err
		}
	}

	for alias, record := range homeRecordsForMessage(msg, conversation, storageKey, messageRaw) {
		key := privatechatread.HomeKey(alias, conversation)
		shouldWrite := true
		raw, getErr := a.store.Get(namespace, key)
		if getErr == nil {
			var current privatechatread.HomeRecord
			if json.Unmarshal(raw, &current) == nil {
				shouldWrite = record.Timestamp > current.Timestamp ||
					(record.Timestamp == current.Timestamp && record.Index >= current.Index)
			}
		} else if !errors.Is(getErr, pebble.ErrNotFound) {
			return nil, getErr
		}
		if shouldWrite {
			homeRaw, err := marshalPrivateChatReadModel(record)
			if err != nil {
				return nil, err
			}
			if err := batch.Set(key, homeRaw, nil); err != nil {
				return nil, err
			}
		}
	}
	return messageRaw, nil
}

// BackfillPrivateChatReadModel rebuilds only derived keys. Existing pchat rows
// remain untouched, making the operation repeatable and rollback-safe. The
// caller must stop all writers while this method runs.
func (a *Aggregator) BackfillPrivateChatReadModel(ctx context.Context) (PrivateChatReadModelReport, error) {
	started := time.Now()
	if a == nil || a.store == nil {
		return PrivateChatReadModelReport{}, errors.New("private chat read model: nil store")
	}
	db, err := a.store.OpenDB(namespace)
	if err != nil {
		return PrivateChatReadModelReport{}, err
	}
	a.readModelReady.Store(false)
	building := privatechatread.State{Version: 1, Status: privatechatread.StatusBuilding, UpdatedAt: time.Now().UTC()}
	buildingRaw, _ := json.Marshal(building)
	if err := db.Set(privatechatread.StateKey(), buildingRaw, pebble.Sync); err != nil {
		return PrivateChatReadModelReport{}, err
	}

	clear := db.NewBatch()
	for _, prefix := range [][]byte{
		[]byte(privatechatread.IndexKeyPrefix),
		[]byte(privatechatread.TimeKeyPrefix),
		[]byte(privatechatread.MetaKeyPrefix),
		[]byte(privatechatread.LocatorPrefix),
		[]byte(privatechatread.HomeKeyPrefix),
	} {
		if err := clear.DeleteRange(prefix, privatechatread.PrefixUpperBound(prefix), nil); err != nil {
			clear.Close()
			return PrivateChatReadModelReport{}, err
		}
	}
	if err := clear.Commit(pebble.Sync); err != nil {
		clear.Close()
		return PrivateChatReadModelReport{}, err
	}
	clear.Close()

	recordsByConversation := make(map[string][]*privateChatBackfillRecord)
	positions := make(map[string]struct {
		conversation string
		position     int
	})
	profiles := make(identityProfileCache)
	state := building
	err = a.store.ScanPrefix(namespace, []byte(pchatKeyConst), func(key, value []byte) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		state.SourceCount++
		var msg PrivateMessage
		if err := json.Unmarshal(value, &msg); err != nil {
			state.InvalidCount++
			return nil
		}
		normalizePrivateMessageConfirmation(&msg)
		a.canonicalizePrivateMessageCached(&msg, profiles)
		conversation, identityA, identityB := a.conversationForMessage(&msg, profiles)
		if conversation == "" {
			state.InvalidCount++
			return nil
		}
		dedupe := strings.ToLower(privateMessageDedupeKey(&msg))
		if dedupe == "" {
			dedupe = string(key)
		}
		positionKey := conversation + "\x00" + dedupe
		if existing, ok := positions[positionKey]; ok {
			state.DuplicateCount++
			current := recordsByConversation[existing.conversation][existing.position]
			if msg.Confirmed && !current.message.Confirmed {
				copyKey := append([]byte(nil), key...)
				current.message = &msg
				current.storageKey = copyKey
			}
			return nil
		}
		copyKey := append([]byte(nil), key...)
		record := &privateChatBackfillRecord{
			message:    &msg,
			storageKey: copyKey,
			dedupeKey:  dedupe,
			identityA:  identityA,
			identityB:  identityB,
		}
		positions[positionKey] = struct {
			conversation string
			position     int
		}{conversation: conversation, position: len(recordsByConversation[conversation])}
		recordsByConversation[conversation] = append(recordsByConversation[conversation], record)
		return nil
	})
	if err != nil {
		return PrivateChatReadModelReport{State: state, Duration: time.Since(started)}, err
	}

	batch := db.NewBatch()
	entries := 0
	flush := func() error {
		if entries == 0 {
			return nil
		}
		if err := batch.Commit(pebble.Sync); err != nil {
			return err
		}
		batch.Close()
		batch = db.NewBatch()
		entries = 0
		return nil
	}
	set := func(key, value []byte) error {
		if err := batch.Set(key, value, nil); err != nil {
			return err
		}
		entries++
		if entries >= privateChatReadModelBatchSize {
			return flush()
		}
		return nil
	}

	conversations := make([]string, 0, len(recordsByConversation))
	for conversation := range recordsByConversation {
		conversations = append(conversations, conversation)
	}
	sort.Strings(conversations)
	for _, conversation := range conversations {
		select {
		case <-ctx.Done():
			batch.Close()
			return PrivateChatReadModelReport{State: state, Duration: time.Since(started)}, ctx.Err()
		default:
		}
		records := recordsByConversation[conversation]
		sort.SliceStable(records, func(i, j int) bool {
			if records[i].message.Timestamp != records[j].message.Timestamp {
				return records[i].message.Timestamp < records[j].message.Timestamp
			}
			return records[i].dedupeKey < records[j].dedupeKey
		})
		for index, record := range records {
			record.message.Index = int64(index)
			messageRaw, marshalErr := json.Marshal(record.message)
			if marshalErr != nil {
				batch.Close()
				return PrivateChatReadModelReport{}, marshalErr
			}
			if err := set(privatechatread.IndexKey(conversation, int64(index)), messageRaw); err != nil {
				batch.Close()
				return PrivateChatReadModelReport{}, err
			}
			if err := set(privatechatread.TimeKey(conversation, record.message.Timestamp, int64(index)), messageRaw); err != nil {
				batch.Close()
				return PrivateChatReadModelReport{}, err
			}
			if record.message.PinId != "" {
				locatorRaw, _ := json.Marshal(privatechatread.Locator{
					StorageKey:   string(record.storageKey),
					Conversation: conversation,
					Index:        int64(index),
					Timestamp:    record.message.Timestamp,
				})
				if err := set(privatechatread.LocatorKey(record.message.PinId), locatorRaw); err != nil {
					batch.Close()
					return PrivateChatReadModelReport{}, err
				}
				if err := set(pchatPinIndexKey(record.message.PinId), record.storageKey); err != nil {
					batch.Close()
					return PrivateChatReadModelReport{}, err
				}
				state.LocatorCount++
			}
			state.IndexedCount++
		}
		latest := records[len(records)-1]
		latestRaw, _ := json.Marshal(latest.message)
		for alias, home := range homeRecordsForMessage(latest.message, conversation, latest.storageKey, latestRaw) {
			raw, _ := json.Marshal(home)
			if err := set(privatechatread.HomeKey(alias, conversation), raw); err != nil {
				batch.Close()
				return PrivateChatReadModelReport{}, err
			}
			state.HomeCount++
		}
		meta := privatechatread.ConversationMeta{
			Count:           int64(len(records)),
			NextIndex:       int64(len(records)),
			LatestTimestamp: latest.message.Timestamp,
		}
		metaRaw, _ := json.Marshal(meta)
		if err := set(privatechatread.MetaKey(conversation), metaRaw); err != nil {
			batch.Close()
			return PrivateChatReadModelReport{}, err
		}
		state.ConversationCount++
	}
	if err := flush(); err != nil {
		batch.Close()
		return PrivateChatReadModelReport{}, err
	}
	batch.Close()

	if err := a.verifyReadModelCounts(state); err != nil {
		return PrivateChatReadModelReport{State: state, Duration: time.Since(started)}, err
	}
	state.Status = privatechatread.StatusReady
	state.UpdatedAt = time.Now().UTC()
	stateRaw, _ := json.Marshal(state)
	if err := db.Set(privatechatread.StateKey(), stateRaw, pebble.Sync); err != nil {
		return PrivateChatReadModelReport{State: state, Duration: time.Since(started)}, err
	}
	a.readModelReady.Store(true)
	return PrivateChatReadModelReport{State: state, Duration: time.Since(started)}, nil
}

func (a *Aggregator) countReadModelPrefix(prefix []byte) (int64, error) {
	var count int64
	err := a.store.ScanPrefix(namespace, prefix, func(_, _ []byte) error {
		count++
		return nil
	})
	return count, err
}

func (a *Aggregator) verifyReadModelCounts(state privatechatread.State) error {
	if state.IndexedCount != state.SourceCount-state.InvalidCount-state.DuplicateCount {
		return fmt.Errorf(
			"private chat read model source accounting mismatch: source=%d indexed=%d invalid=%d duplicate=%d",
			state.SourceCount,
			state.IndexedCount,
			state.InvalidCount,
			state.DuplicateCount,
		)
	}
	checks := []struct {
		name   string
		prefix []byte
		want   int64
	}{
		{name: "index", prefix: []byte(privatechatread.IndexKeyPrefix), want: state.IndexedCount},
		{name: "time", prefix: []byte(privatechatread.TimeKeyPrefix), want: state.IndexedCount},
		{name: "locator", prefix: []byte(privatechatread.LocatorPrefix), want: state.LocatorCount},
		{name: "home", prefix: []byte(privatechatread.HomeKeyPrefix), want: state.HomeCount},
		{name: "conversation", prefix: []byte(privatechatread.MetaKeyPrefix), want: state.ConversationCount},
	}
	for _, check := range checks {
		got, err := a.countReadModelPrefix(check.prefix)
		if err != nil {
			return err
		}
		if got != check.want {
			return fmt.Errorf("private chat read model %s count=%d, want %d", check.name, got, check.want)
		}
	}
	var metaTotal int64
	err := a.store.ScanPrefix(namespace, []byte(privatechatread.MetaKeyPrefix), func(_, value []byte) error {
		var meta privatechatread.ConversationMeta
		if err := json.Unmarshal(value, &meta); err != nil {
			return err
		}
		if meta.Count < 0 || meta.NextIndex != meta.Count {
			return fmt.Errorf("invalid private chat conversation metadata: count=%d nextIndex=%d", meta.Count, meta.NextIndex)
		}
		metaTotal += meta.Count
		return nil
	})
	if err != nil {
		return err
	}
	if metaTotal != state.IndexedCount {
		return fmt.Errorf("private chat conversation totals=%d, want %d", metaTotal, state.IndexedCount)
	}
	return nil
}

func (a *Aggregator) VerifyPrivateChatReadModel(ctx context.Context) (PrivateChatReadModelReport, error) {
	started := time.Now()
	select {
	case <-ctx.Done():
		return PrivateChatReadModelReport{}, ctx.Err()
	default:
	}
	state, err := privatechatread.LoadState(a.store, namespace)
	if err != nil {
		return PrivateChatReadModelReport{}, err
	}
	if state.Version != 1 || state.Status != privatechatread.StatusReady {
		return PrivateChatReadModelReport{State: state, Duration: time.Since(started)}, fmt.Errorf("private chat read model is not ready: version=%d status=%q", state.Version, state.Status)
	}
	if err := a.verifyReadModelCounts(state); err != nil {
		return PrivateChatReadModelReport{State: state, Duration: time.Since(started)}, err
	}
	return PrivateChatReadModelReport{State: state, Duration: time.Since(started)}, nil
}

func indexFromReadModelKey(key []byte) (int64, error) {
	position := strings.LastIndexByte(string(key), ':')
	if position < 0 {
		return 0, errors.New("private chat read model: malformed index key")
	}
	return strconv.ParseInt(string(key[position+1:]), 10, 64)
}

func (a *Aggregator) messageAtReadModelEntry(key, value []byte, profiles identityProfileCache) (*PrivateMessage, error) {
	index, err := indexFromReadModelKey(key)
	if err != nil {
		return nil, err
	}
	var msg PrivateMessage
	if err := json.Unmarshal(value, &msg); err != nil {
		return nil, fmt.Errorf("decode private chat read model message: %w", err)
	}
	msg.Index = index
	normalizePrivateMessageConfirmation(&msg)
	a.canonicalizePrivateMessageCached(&msg, profiles)
	return &msg, nil
}

func (a *Aggregator) getPrivateChatListByIndexReadModel(myMetaID, otherMetaID string, startIndex, size int64) (*PrivateChatListResult, error) {
	profiles := make(identityProfileCache)
	conversation := a.conversationForQuery(myMetaID, otherMetaID, profiles)
	meta, err := a.loadConversationMeta(conversation)
	if err != nil {
		return nil, err
	}
	if startIndex < 0 {
		startIndex = 0
	}
	if size < 1 {
		size = 20
	}
	db, err := a.store.OpenDB(namespace)
	if err != nil {
		return nil, err
	}
	prefix := privatechatread.IndexPrefix(conversation)
	iter, err := db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: privatechatread.PrefixUpperBound(prefix)})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	messages := make([]*PrivateMessage, 0, size)
	lastIndex := int64(0)
	for valid := iter.SeekGE(privatechatread.IndexKey(conversation, startIndex)); valid && int64(len(messages)) < size; valid = iter.Next() {
		msg, err := a.messageAtReadModelEntry(iter.Key(), iter.Value(), profiles)
		if err != nil {
			return nil, err
		}
		lastIndex = msg.Index
		messages = append(messages, msg)
	}
	nextCursor := ""
	if len(messages) > 0 && lastIndex+1 < meta.Count {
		nextCursor = strconv.FormatInt(lastIndex+1, 10)
	}
	return &PrivateChatListResult{Total: meta.Count, NextCursor: nextCursor, NextTimestamp: lastIndex, List: messages}, nil
}

func decodePrivateChatOffset(cursor string) int64 {
	if cursor == "" || cursor == "null" {
		return 0
	}
	decoded, err := base64.StdEncoding.DecodeString(cursor)
	if err != nil || len(decoded) < 8 {
		return 0
	}
	return int64FromBytes(decoded[:8])
}

func (a *Aggregator) getPrivateChatListByReadModel(myMetaID, otherMetaID, cursor string, size, beforeTimestamp int64) (*PrivateChatListResult, error) {
	profiles := make(identityProfileCache)
	conversation := a.conversationForQuery(myMetaID, otherMetaID, profiles)
	meta, err := a.loadConversationMeta(conversation)
	if err != nil {
		return nil, err
	}
	if size < 1 {
		size = 20
	}
	offset := decodePrivateChatOffset(cursor)
	db, err := a.store.OpenDB(namespace)
	if err != nil {
		return nil, err
	}
	messages := make([]*PrivateMessage, 0, size)
	hasMore := false
	if beforeTimestamp <= 0 {
		prefix := privatechatread.IndexPrefix(conversation)
		iter, err := db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: privatechatread.PrefixUpperBound(prefix)})
		if err != nil {
			return nil, err
		}
		defer iter.Close()
		startIndex := meta.Count - 1 - offset
		if startIndex >= 0 {
			for valid := iter.SeekLT(privatechatread.IndexKey(conversation, startIndex+1)); valid && int64(len(messages)) < size; valid = iter.Prev() {
				msg, err := a.messageAtReadModelEntry(iter.Key(), iter.Value(), profiles)
				if err != nil {
					return nil, err
				}
				messages = append(messages, msg)
			}
		}
		hasMore = offset+int64(len(messages)) < meta.Count
	} else {
		prefix := privatechatread.TimePrefix(conversation)
		iter, err := db.NewIter(&pebble.IterOptions{LowerBound: prefix, UpperBound: privatechatread.PrefixUpperBound(prefix)})
		if err != nil {
			return nil, err
		}
		defer iter.Close()
		valid := iter.SeekLT(privatechatread.TimeKey(conversation, beforeTimestamp, 0))
		for skipped := int64(0); valid && skipped < offset; skipped++ {
			valid = iter.Prev()
		}
		for ; valid && int64(len(messages)) < size; valid = iter.Prev() {
			var msg PrivateMessage
			if err := json.Unmarshal(iter.Value(), &msg); err != nil {
				return nil, fmt.Errorf("decode private chat time index message: %w", err)
			}
			keyParts := strings.Split(string(iter.Key()), ":")
			if len(keyParts) > 0 {
				msg.Index, _ = strconv.ParseInt(keyParts[len(keyParts)-1], 10, 64)
			}
			normalizePrivateMessageConfirmation(&msg)
			a.canonicalizePrivateMessageCached(&msg, profiles)
			messages = append(messages, &msg)
		}
		hasMore = valid
	}
	nextCursor := ""
	newOffset := offset + int64(len(messages))
	if len(messages) > 0 && int64(len(messages)) == size && hasMore {
		nextCursor = base64.StdEncoding.EncodeToString(int64ToBytes(newOffset))
	}
	nextTimestamp := int64(0)
	if len(messages) > 0 {
		nextTimestamp = messages[len(messages)-1].Timestamp
	}
	return &PrivateChatListResult{Total: meta.Count, NextCursor: nextCursor, NextTimestamp: nextTimestamp, List: messages}, nil
}

func (a *Aggregator) readModelHomeRecords(metaID string, profiles identityProfileCache) (map[string]privatechatread.HomeRecord, error) {
	result := make(map[string]privatechatread.HomeRecord)
	for _, alias := range a.identityAliasesCached(metaID, profiles) {
		records, err := privatechatread.ListHomes(a.store, namespace, alias)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			current, ok := result[record.Conversation]
			if !ok || record.Timestamp > current.Timestamp || (record.Timestamp == current.Timestamp && record.Index > current.Index) {
				result[record.Conversation] = record
			}
		}
	}
	return result, nil
}

func (a *Aggregator) getPrivateChatHomesByReadModel(metaID string) ([]*PrivateChatHome, error) {
	profiles := make(identityProfileCache)
	records, err := a.readModelHomeRecords(metaID, profiles)
	if err != nil {
		return nil, err
	}
	homes := make([]*PrivateChatHome, 0, len(records))
	for _, record := range records {
		var msg PrivateMessage
		if err := json.Unmarshal(record.Message, &msg); err != nil {
			return nil, fmt.Errorf("decode private chat home message: %w", err)
		}
		msg.Index = record.Index
		normalizePrivateMessageConfirmation(&msg)
		a.canonicalizePrivateMessageCached(&msg, profiles)
		homes = append(homes, &PrivateChatHome{
			MetaId:       record.PeerMetaID,
			GlobalMetaId: record.PeerGlobalMetaID,
			LastMessage:  &msg,
		})
	}
	sort.SliceStable(homes, func(i, j int) bool {
		return homes[i].LastMessage.Timestamp > homes[j].LastMessage.Timestamp
	})
	return homes, nil
}

func (a *Aggregator) getPrivateGroupPathsByReadModel(metaID string) ([]*PrivateGroupPath, error) {
	profiles := make(identityProfileCache)
	records, err := a.readModelHomeRecords(metaID, profiles)
	if err != nil {
		return nil, err
	}
	paths := make([]*PrivateGroupPath, 0, len(records))
	for _, record := range records {
		var msg PrivateMessage
		if err := json.Unmarshal(record.Message, &msg); err != nil {
			return nil, fmt.Errorf("decode private chat home message: %w", err)
		}
		lo, hi := sortMetas(msg.From, msg.To)
		path := lo + ":" + hi
		paths = append(paths, &PrivateGroupPath{Path: path, GroupId: path, PinId: msg.PinId})
	}
	sort.SliceStable(paths, func(i, j int) bool { return paths[i].Path < paths[j].Path })
	return paths, nil
}
