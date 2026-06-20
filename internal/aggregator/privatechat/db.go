package privatechat

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/cockroachdb/pebble"
)

// PrivateMessage represents a private chat message, matching IDCHAT_API_CONTRACT.md field names.
type PrivateMessage struct {
	FromGlobalMetaId string      `json:"fromGlobalMetaId"`
	From             string      `json:"from"`
	FromAddress      string      `json:"fromAddress"`
	FromUserInfo     interface{} `json:"fromUserInfo,omitempty"`
	ToGlobalMetaId   string      `json:"toGlobalMetaId"`
	To               string      `json:"to"`
	ToAddress        string      `json:"toAddress"`
	ToUserInfo       interface{} `json:"toUserInfo,omitempty"`
	TxId             string      `json:"txId"`
	PinId            string      `json:"pinId"`
	Protocol         string      `json:"protocol"`
	Content          string      `json:"content"`
	ContentType      string      `json:"contentType"`
	Encryption       string      `json:"encryption"`
	Timestamp        int64       `json:"timestamp"`
	Chain            string      `json:"chain"`
	BlockHeight      int64       `json:"blockHeight"`
	Index            int64       `json:"index"`
}

// PrivateChatListResult is the response format for private chat list queries.
type PrivateChatListResult struct {
	Total         int64             `json:"total"`
	NextCursor    string            `json:"nextCursor"`
	NextTimestamp int64             `json:"nextTimestamp"`
	List          []*PrivateMessage `json:"list"`
}

// PrivateChatHome represents a conversation partner with the last message preview.
type PrivateChatHome struct {
	MetaId       string          `json:"metaId"`
	GlobalMetaId string          `json:"globalMetaId,omitempty"`
	UserInfo     interface{}     `json:"userInfo,omitempty"`
	LastMessage  *PrivateMessage `json:"lastMessage,omitempty"`
}

// PrivateGroupPath mirrors the old idchat private path item shape.
type PrivateGroupPath struct {
	Path    string `json:"path"`
	GroupId string `json:"groupId"`
	PinId   string `json:"pinId"`
}

const (
	pchatKeyConst                     = "pchat:"
	homepageSenderIndexKeyConst       = "hpchat:from:"
	homepageSenderIndexStateKeyConst  = "hpchat:index-state:"
	homepageSenderIndexVersion        = "v1"
	homepageMaterializedChatsKeyConst = "hpchat:mat:"
	homepageMaterializedStateKeyConst = "hpchat:mat-state:"
	homepageMaterializedStateVersion  = "v1"
	homepageMaterializedChatsLimit    = 64
)

// sortMetas returns the lower and higher metaId alphabetically.
// This ensures bidirectional keys (A→B and B→A) land in the same prefix.
func sortMetas(a, b string) (lo, hi string) {
	if a < b {
		return a, b
	}
	return b, a
}

// pchatKey builds a key for a private chat message.
// Format: pchat:<lower_metaid>:<higher_metaid>:<timestamp>:<txId>
// Timestamp is zero-padded to 19 digits for correct key ordering.
func pchatKey(metaId1, metaId2 string, timestamp int64, txId string) []byte {
	lo, hi := sortMetas(metaId1, metaId2)
	return []byte(fmt.Sprintf("%s%s:%s:%019d:%s", pchatKeyConst, lo, hi, timestamp, txId))
}

// pchatPrefix builds a prefix for scanning all messages between two users.
// Format: pchat:<lower_metaid>:<higher_metaid>:
func pchatPrefix(metaId1, metaId2 string) []byte {
	lo, hi := sortMetas(metaId1, metaId2)
	return []byte(fmt.Sprintf("%s%s:%s:", pchatKeyConst, lo, hi))
}

// SavePrivateMessage persists a private chat message to PebbleDB.
func (a *Aggregator) SavePrivateMessage(msg *PrivateMessage) error {
	if msg == nil {
		return nil
	}

	a.homepageIndex.RLock()
	ready, err := a.homepageMaterializedStateDone()
	if err != nil {
		a.homepageIndex.RUnlock()
		return err
	}
	if !ready {
		a.homepageIndex.RUnlock()
		a.homepageIndex.Lock()
		defer a.homepageIndex.Unlock()
		if msg.Index < 0 {
			msg.Index = a.nextPrivateMessageIndex(msg.From, msg.To)
		}
		return a.savePrivateMessageUnlocked(msg)
	}
	defer a.homepageIndex.RUnlock()

	conversationLockKey := privateMessageConversationLockKey(msg)
	if conversationLockKey != "" {
		lock := a.privateMessageMutex(conversationLockKey)
		lock.Lock()
		defer lock.Unlock()
	}
	aliasLocks := a.homepageMaterializedMutexes(homepageSenderIndexAliases(msg))
	lockSyncMutexes(aliasLocks)
	defer unlockSyncMutexes(aliasLocks)

	if msg.Index < 0 {
		msg.Index = a.nextPrivateMessageIndex(msg.From, msg.To)
	}

	return a.savePrivateMessageUnlocked(msg)
}

func (a *Aggregator) savePrivateMessageUnlocked(msg *PrivateMessage) error {
	if a == nil || msg == nil {
		return nil
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	db, err := a.store.OpenDB(namespace)
	if err != nil {
		return err
	}

	batch := db.NewBatch()
	defer batch.Close()

	if err := batch.Set(pchatKey(msg.From, msg.To, msg.Timestamp, msg.TxId), raw, pebble.Sync); err != nil {
		return err
	}
	if err := writeHomepageSenderIndexEntries(batch, msg, raw); err != nil {
		return err
	}
	if err := writeHomepageMaterializedEntriesUnlocked(a.store, batch, msg, a.invalidateHomepageMaterializedState); err != nil {
		return err
	}

	return batch.Commit(pebble.Sync)
}

// GetPrivateChatList returns bidirectionally filtered messages between two users
// with cursor-based pagination (descending by timestamp, newest first).
// The cursor is a base64-encoded offset. When beforeTimestamp is positive,
// only messages older than that timestamp are considered.
func (a *Aggregator) GetPrivateChatList(myMetaId, otherMetaId string, cursorStr string, size int64, beforeTimestamp int64) (*PrivateChatListResult, error) {
	allMessages := a.collectPrivateMessages(myMetaId, otherMetaId)
	if beforeTimestamp > 0 {
		filtered := make([]*PrivateMessage, 0, len(allMessages))
		for _, msg := range allMessages {
			if msg.Timestamp < beforeTimestamp {
				filtered = append(filtered, msg)
			}
		}
		allMessages = filtered
	}

	total := int64(len(allMessages))

	// cursor encodes how many entries we've already returned (offset from the newest)
	var startFromEnd int64
	if cursorStr != "" && cursorStr != "null" {
		decoded, cursorErr := base64.StdEncoding.DecodeString(cursorStr)
		if cursorErr == nil && len(decoded) >= 8 {
			startFromEnd = int64FromBytes(decoded[:8])
		}
	}

	// Messages are in ascending order (oldest first by key).
	// We want descending (newest first), so start from the end.
	startIdx := total - 1 - startFromEnd
	if startIdx >= total {
		startIdx = total - 1
	}
	if startIdx < 0 {
		startIdx = -1
	}

	var messages []*PrivateMessage
	for i := startIdx; i >= 0 && int64(len(messages)) < size; i-- {
		messages = append(messages, allMessages[i])
	}

	// Calculate next cursor
	nextCursor := ""
	newOffset := startFromEnd + int64(len(messages))
	if newOffset < total && int64(len(messages)) == size && len(messages) > 0 {
		nextCursor = base64.StdEncoding.EncodeToString(int64ToBytes(newOffset))
	}

	nextTimestamp := int64(0)
	if len(messages) > 0 {
		nextTimestamp = messages[len(messages)-1].Timestamp
	}

	return &PrivateChatListResult{
		Total:         total,
		NextCursor:    nextCursor,
		NextTimestamp: nextTimestamp,
		List:          messages,
	}, nil
}

// GetPrivateChatListByIndex returns messages by their continuous conversation index.
func (a *Aggregator) GetPrivateChatListByIndex(myMetaId, otherMetaId string, startIndex int64, size int64) (*PrivateChatListResult, error) {
	allMessages := a.collectPrivateMessages(myMetaId, otherMetaId)

	sort.SliceStable(allMessages, func(i, j int) bool {
		if allMessages[i].Index != allMessages[j].Index {
			return allMessages[i].Index < allMessages[j].Index
		}
		if allMessages[i].Timestamp != allMessages[j].Timestamp {
			return allMessages[i].Timestamp < allMessages[j].Timestamp
		}
		return privateMessageDedupeKey(allMessages[i]) < privateMessageDedupeKey(allMessages[j])
	})

	var messages []*PrivateMessage
	lastIndex := int64(0)
	for _, msg := range allMessages {
		if msg.Index < startIndex {
			continue
		}
		messages = append(messages, msg)
		if msg.Index > lastIndex {
			lastIndex = msg.Index
		}
		if int64(len(messages)) >= size {
			break
		}
	}
	if messages == nil {
		messages = []*PrivateMessage{}
	}

	return &PrivateChatListResult{
		Total:         int64(len(messages)),
		NextTimestamp: lastIndex,
		List:          messages,
	}, nil
}

func (a *Aggregator) nextPrivateMessageIndex(from, to string) int64 {
	messages := a.collectPrivateMessages(from, to)
	maxIndex := int64(-1)
	for _, msg := range messages {
		if msg.Index > maxIndex {
			maxIndex = msg.Index
		}
	}
	return maxIndex + 1
}

func (a *Aggregator) collectPrivateMessages(myMetaId, otherMetaId string) []*PrivateMessage {
	myAliases := a.identityAliases(myMetaId)
	otherAliases := a.identityAliases(otherMetaId)
	if len(myAliases) == 0 || len(otherAliases) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var allMessages []*PrivateMessage
	for _, my := range myAliases {
		for _, other := range otherAliases {
			prefix := pchatPrefix(my, other)
			a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
				var msg PrivateMessage
				if e := json.Unmarshal(value, &msg); e != nil {
					return nil
				}
				keyID := privateMessageDedupeKey(&msg)
				if seen[keyID] {
					return nil
				}
				seen[keyID] = true
				allMessages = append(allMessages, &msg)
				return nil
			})
		}
	}

	sort.SliceStable(allMessages, func(i, j int) bool {
		if allMessages[i].Timestamp != allMessages[j].Timestamp {
			return allMessages[i].Timestamp < allMessages[j].Timestamp
		}
		return privateMessageDedupeKey(allMessages[i]) < privateMessageDedupeKey(allMessages[j])
	})

	return allMessages
}

func privateMessageDedupeKey(msg *PrivateMessage) string {
	if msg == nil {
		return ""
	}
	if msg.PinId != "" {
		return msg.PinId
	}
	return msg.TxId + ":" + msg.From + ":" + msg.To
}

func homepageSenderIndexPrefix(alias string) []byte {
	return []byte(fmt.Sprintf("%s%s:", homepageSenderIndexKeyConst, aliasKey(alias)))
}

func homepageSenderIndexKey(alias string, msg *PrivateMessage) []byte {
	return []byte(fmt.Sprintf(
		"%s%s:%019d:%s",
		homepageSenderIndexKeyConst,
		aliasKey(alias),
		descendingTimestamp(msg.Timestamp),
		homepageSenderIndexEntryID(msg),
	))
}

func homepageSenderIndexStateKey() []byte {
	return []byte(homepageSenderIndexStateKeyConst + homepageSenderIndexVersion)
}

func homepageMaterializedChatsPrefix() []byte {
	return []byte(homepageMaterializedChatsKeyConst)
}

func homepageMaterializedChatsKey(alias string) []byte {
	return []byte(homepageMaterializedChatsKeyConst + aliasKey(alias))
}

func homepageMaterializedStateKey() []byte {
	return []byte(homepageMaterializedStateKeyConst + homepageMaterializedStateVersion)
}

func descendingTimestamp(timestamp int64) int64 {
	if timestamp < 0 {
		timestamp = 0
	}
	return math.MaxInt64 - timestamp
}

func homepageSenderIndexEntryID(msg *PrivateMessage) string {
	if msg == nil {
		return ""
	}

	id := privateMessageDedupeKey(msg)
	id = strings.TrimSpace(id)
	if id == "" {
		id = fmt.Sprintf("%019d", msg.Timestamp)
	}
	return strings.ReplaceAll(id, ":", "_")
}

func homepageSenderIndexAliases(msg *PrivateMessage) []string {
	if msg == nil || !isHomepageSimpleMsgProtocol(msg.Protocol) || strings.TrimSpace(msg.To) == "" {
		return nil
	}

	seen := make(map[string]bool)
	aliases := make([]string, 0, 3)
	add := func(value string) {
		key := aliasKey(value)
		if key == "" || seen[key] {
			return
		}
		seen[key] = true
		aliases = append(aliases, key)
	}

	add(msg.From)
	add(msg.FromGlobalMetaId)
	add(msg.FromAddress)
	return aliases
}

func writeHomepageSenderIndexEntries(batch *pebble.Batch, msg *PrivateMessage, raw []byte) error {
	if batch == nil || msg == nil || len(raw) == 0 {
		return nil
	}

	for _, alias := range homepageSenderIndexAliases(msg) {
		if err := batch.Set(homepageSenderIndexKey(alias, msg), raw, pebble.Sync); err != nil {
			return err
		}
	}
	return nil
}

func homepageInteractionFromMessage(msg *PrivateMessage) (HomepageInteraction, bool) {
	if msg == nil || msg.PinId == "" || strings.TrimSpace(msg.To) == "" || !isHomepageSimpleMsgProtocol(msg.Protocol) {
		return HomepageInteraction{}, false
	}
	return HomepageInteraction{
		PinId:        msg.PinId,
		ProtocolPath: HomepageSimpleMsgProtocolPath,
		Timestamp:    msg.Timestamp,
		InteractWith: msg.To,
	}, true
}

func mergeHomepageMaterializedInteraction(items []HomepageInteraction, item HomepageInteraction, limit int) []HomepageInteraction {
	if item.PinId == "" {
		return items
	}

	replaced := false
	for i := range items {
		if items[i].PinId != item.PinId {
			continue
		}
		if item.Timestamp < items[i].Timestamp {
			items[i] = item
		}
		replaced = true
		break
	}
	if !replaced {
		items = append(items, item)
	}

	sortHomepageInteractions(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func loadHomepageMaterializedChats(store interface {
	Get(namespace string, key []byte) ([]byte, error)
}, alias string) ([]HomepageInteraction, error) {
	if store == nil {
		return nil, nil
	}

	raw, err := store.Get(namespace, homepageMaterializedChatsKey(alias))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}

	var items []HomepageInteraction
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("%w: %s", errHomepageMaterializedCorrupt, alias)
	}
	return items, nil
}

func writeHomepageMaterializedEntriesUnlocked(store interface {
	Get(namespace string, key []byte) ([]byte, error)
}, batch *pebble.Batch, msg *PrivateMessage, invalidator func() error) error {
	if store == nil || batch == nil || msg == nil {
		return nil
	}

	item, ok := homepageInteractionFromMessage(msg)
	if !ok {
		return nil
	}

	for _, alias := range homepageSenderIndexAliases(msg) {
		items, err := loadHomepageMaterializedChats(store, alias)
		if err != nil {
			if errors.Is(err, errHomepageMaterializedCorrupt) {
				if invalidator != nil {
					_ = invalidator()
				}
				return nil
			}
			return err
		}
		items = mergeHomepageMaterializedInteraction(items, item, homepageMaterializedChatsLimit)
		raw, err := json.Marshal(items)
		if err != nil {
			return err
		}
		if err := batch.Set(homepageMaterializedChatsKey(alias), raw, pebble.Sync); err != nil {
			return err
		}
	}

	return nil
}

func privateMessageConversationLockKey(msg *PrivateMessage) string {
	if msg == nil {
		return ""
	}
	from := strings.TrimSpace(msg.From)
	to := strings.TrimSpace(msg.To)
	if from == "" || to == "" {
		return ""
	}
	lo, hi := sortMetas(from, to)
	return lo + "|" + hi
}

func (a *Aggregator) privateMessageMutex(key string) *sync.Mutex {
	return syncMapMutex(&a.privateMessageLocks, key)
}

func (a *Aggregator) homepageMaterializedMutexes(aliases []string) []*sync.Mutex {
	if len(aliases) == 0 {
		return nil
	}
	sort.Strings(aliases)
	locks := make([]*sync.Mutex, 0, len(aliases))
	for _, alias := range aliases {
		locks = append(locks, syncMapMutex(&a.homepageMaterializedLock, alias))
	}
	return locks
}

func syncMapMutex(lockMap *sync.Map, key string) *sync.Mutex {
	if lockMap == nil || key == "" {
		return &sync.Mutex{}
	}
	if existing, ok := lockMap.Load(key); ok {
		return existing.(*sync.Mutex)
	}
	lock := &sync.Mutex{}
	actual, _ := lockMap.LoadOrStore(key, lock)
	return actual.(*sync.Mutex)
}

func lockSyncMutexes(locks []*sync.Mutex) {
	for _, lock := range locks {
		if lock != nil {
			lock.Lock()
		}
	}
}

func unlockSyncMutexes(locks []*sync.Mutex) {
	for i := len(locks) - 1; i >= 0; i-- {
		if locks[i] != nil {
			locks[i].Unlock()
		}
	}
}

// GetPrivateChatHomes returns a list of conversation partners with last message preview.
// Scans all pchat keys to find unique conversation partners of the given metaId.
func (a *Aggregator) GetPrivateChatHomes(metaid string) ([]*PrivateChatHome, error) {
	// Scan the global pchat prefix to find all conversations involving this user.
	partnerMap := make(map[string]*PrivateMessage)

	prefix := []byte(pchatKeyConst)
	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		var msg PrivateMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}

		// Determine the partner
		var partnerMetaId string
		if msg.From == metaid {
			partnerMetaId = msg.To
		} else if msg.To == metaid {
			partnerMetaId = msg.From
		} else {
			return nil
		}

		// Keep the latest message for this partner
		if existing, ok := partnerMap[partnerMetaId]; !ok || msg.Timestamp > existing.Timestamp {
			partnerMap[partnerMetaId] = &msg
		}
		return nil
	})

	var homes []*PrivateChatHome
	for partnerMetaId, lastMsg := range partnerMap {
		home := &PrivateChatHome{
			MetaId:      partnerMetaId,
			LastMessage: lastMsg,
		}
		// Copy globalMetaId from the message for the partner
		if lastMsg.From == partnerMetaId {
			home.GlobalMetaId = lastMsg.FromGlobalMetaId
		} else {
			home.GlobalMetaId = lastMsg.ToGlobalMetaId
		}
		homes = append(homes, home)
	}

	if homes == nil {
		homes = []*PrivateChatHome{}
	}

	return homes, nil
}

// GetPrivateGroupPaths returns the list of paths where the given metaId has private chat messages.
func (a *Aggregator) GetPrivateGroupPaths(metaid string) ([]*PrivateGroupPath, error) {
	pathMap := make(map[string]*PrivateGroupPath)

	prefix := []byte(pchatKeyConst)
	a.store.ScanPrefix(namespace, prefix, func(key, value []byte) error {
		var msg PrivateMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}

		if msg.From == metaid || msg.To == metaid {
			lo, hi := sortMetas(msg.From, msg.To)
			path := lo + ":" + hi
			if _, ok := pathMap[path]; !ok {
				pathMap[path] = &PrivateGroupPath{
					Path:    path,
					GroupId: path,
					PinId:   msg.PinId,
				}
			}
		}
		return nil
	})

	var paths []*PrivateGroupPath
	for _, p := range pathMap {
		paths = append(paths, p)
	}

	if paths == nil {
		paths = []*PrivateGroupPath{}
	}

	return paths, nil
}

// GetPrivateMessage retrieves a single private message by its Pebble key.
func (a *Aggregator) GetPrivateMessage(metaId1, metaId2 string, timestamp int64, txId string) (*PrivateMessage, error) {
	raw, err := a.store.Get(namespace, pchatKey(metaId1, metaId2, timestamp, txId))
	if err != nil {
		if err == pebble.ErrNotFound {
			return nil, nil
		}
		return nil, err
	}
	if raw == nil {
		return nil, nil
	}

	var msg PrivateMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// int64FromBytes converts up to 8 bytes to int64.
func int64FromBytes(b []byte) int64 {
	var v int64
	for i := 0; i < len(b) && i < 8; i++ {
		v = (v << 8) | int64(b[i])
	}
	return v
}

// int64ToBytes converts int64 to 8 bytes.
func int64ToBytes(v int64) []byte {
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = byte(v & 0xff)
		v >>= 8
	}
	return b
}
