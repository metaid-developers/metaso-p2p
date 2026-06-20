package privatechat

import (
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/cockroachdb/pebble"
)

const HomepageSimpleMsgProtocolPath = "/protocols/simplemsg"

type HomepageInteractionListParams struct {
	GlobalMetaId string
	MetaId       string
	Address      string
	Size         int
}

type HomepageInteractionListResult struct {
	Items   []HomepageInteraction
	HasMore bool
}

type HomepageInteraction struct {
	PinId        string
	ProtocolPath string
	Timestamp    int64
	InteractWith string
}

func (a *Aggregator) ListOutgoingHomepageInteractions(params HomepageInteractionListParams) (*HomepageInteractionListResult, error) {
	result := &HomepageInteractionListResult{
		Items: []HomepageInteraction{},
	}
	if a == nil || a.store == nil {
		return result, nil
	}

	size := params.Size
	if size <= 0 {
		size = 5
	}

	aliases := a.homepageIdentityAliases(params)
	if len(aliases) == 0 {
		return result, nil
	}

	if err := a.ensureHomepageIndexes(); err != nil {
		return nil, err
	}

	var (
		matches []HomepageInteraction
		err     error
	)
	if size < homepageMaterializedChatsLimit {
		var ok bool
		matches, ok, err = a.listHomepageInteractionsFromMaterialized(aliases)
		if err != nil {
			return nil, err
		}
		if !ok {
			matches, err = a.listHomepageInteractionsBySenderIndex(aliases, size)
			if err != nil {
				return nil, err
			}
		}
	} else {
		matches, err = a.listHomepageInteractionsBySenderIndex(aliases, size)
		if err != nil {
			return nil, err
		}
	}

	sortHomepageInteractions(matches)

	result.HasMore = len(matches) > size
	if result.HasMore {
		matches = matches[:size]
	}
	result.Items = matches
	if result.Items == nil {
		result.Items = []HomepageInteraction{}
	}
	return result, nil
}

func (a *Aggregator) listHomepageInteractionsFromMaterialized(aliases map[string]bool) ([]HomepageInteraction, bool, error) {
	itemsByPin := make(map[string]HomepageInteraction)
	loaded := false

	for alias := range aliases {
		items, err := loadHomepageMaterializedChats(a.store, alias)
		if err != nil {
			return nil, false, err
		}
		if len(items) == 0 {
			continue
		}
		loaded = true
		for _, item := range items {
			if item.PinId == "" || item.InteractWith == "" || !isHomepageSimpleMsgProtocol(item.ProtocolPath) {
				continue
			}
			if existing, ok := itemsByPin[item.PinId]; !ok || item.Timestamp < existing.Timestamp {
				itemsByPin[item.PinId] = item
			}
		}
	}

	if !loaded {
		return nil, false, nil
	}

	matches := make([]HomepageInteraction, 0, len(itemsByPin))
	for _, item := range itemsByPin {
		matches = append(matches, item)
	}
	sortHomepageInteractions(matches)
	return matches, true, nil
}

func (a *Aggregator) listHomepageInteractionsBySenderIndex(aliases map[string]bool, size int) ([]HomepageInteraction, error) {
	fetchLimit := size + 1
	if fetchLimit < 2 {
		fetchLimit = 2
	}

	for {
		matches, exhausted, err := a.collectHomepageInteractionsFromIndex(aliases, fetchLimit)
		if err != nil {
			return nil, err
		}
		if len(matches) > size || exhausted {
			return matches, nil
		}
		fetchLimit *= 2
	}
}

func (a *Aggregator) collectHomepageInteractionsFromIndex(aliases map[string]bool, perAliasLimit int) ([]HomepageInteraction, bool, error) {
	itemsByPin := make(map[string]HomepageInteraction)
	exhausted := true

	for alias := range aliases {
		count := 0
		reachedLimit := false
		err := a.store.ScanPrefix(namespace, homepageSenderIndexPrefix(alias), func(key, value []byte) error {
			if count >= perAliasLimit {
				reachedLimit = true
				return errStopHomepageIndexScan
			}
			count++

			var msg PrivateMessage
			if e := json.Unmarshal(value, &msg); e != nil {
				return nil
			}
			item, ok := homepageInteractionFromMessage(&msg)
			if !ok {
				return nil
			}
			if existing, ok := itemsByPin[msg.PinId]; !ok || item.Timestamp < existing.Timestamp {
				itemsByPin[msg.PinId] = item
			}
			return nil
		})
		if err != nil && !errors.Is(err, errStopHomepageIndexScan) {
			return nil, false, err
		}
		if reachedLimit {
			exhausted = false
		}
	}

	matches := make([]HomepageInteraction, 0, len(itemsByPin))
	for _, item := range itemsByPin {
		matches = append(matches, item)
	}

	sortHomepageInteractions(matches)
	return matches, exhausted, nil
}

var errStopHomepageIndexScan = errors.New("stop homepage sender index scan")

func sortHomepageInteractions(items []HomepageInteraction) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Timestamp != items[j].Timestamp {
			return items[i].Timestamp > items[j].Timestamp
		}
		return items[i].PinId < items[j].PinId
	})
}

func (a *Aggregator) ensureHomepageSenderIndexes() error {
	return a.ensureHomepageIndexes()
}

func (a *Aggregator) ensureHomepageIndexes() error {
	if a == nil || a.store == nil {
		return nil
	}

	senderDone, err := a.homepageIndexStateDone(homepageSenderIndexStateKey())
	if err != nil {
		return err
	}
	materializedDone, err := a.homepageIndexStateDone(homepageMaterializedStateKey())
	if err != nil {
		return err
	}
	if senderDone && materializedDone {
		return nil
	}

	a.homepageIndex.Lock()
	defer a.homepageIndex.Unlock()

	senderDone, err = a.homepageIndexStateDone(homepageSenderIndexStateKey())
	if err != nil {
		return err
	}
	materializedDone, err = a.homepageIndexStateDone(homepageMaterializedStateKey())
	if err != nil {
		return err
	}
	if senderDone && materializedDone {
		return nil
	}

	db, err := a.store.OpenDB(namespace)
	if err != nil {
		return err
	}

	batch := db.NewBatch()
	defer batch.Close()

	if !materializedDone {
		if err := a.deleteHomepageMaterializedEntries(batch); err != nil {
			return err
		}
	}

	materializedByAlias := make(map[string][]HomepageInteraction)
	err = a.store.ScanPrefix(namespace, []byte(pchatKeyConst), func(key, value []byte) error {
		var msg PrivateMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		if !senderDone {
			if err := writeHomepageSenderIndexEntries(batch, &msg, value); err != nil {
				return err
			}
		}
		if !materializedDone {
			a.mergeHomepageMaterializedAliases(materializedByAlias, &msg)
		}
		return nil
	})
	if err != nil {
		return err
	}

	if !materializedDone {
		if err := a.writeHomepageMaterializedSnapshot(batch, materializedByAlias); err != nil {
			return err
		}
		if err := batch.Set(homepageMaterializedStateKey(), []byte("done"), pebble.Sync); err != nil {
			return err
		}
	}
	if !senderDone {
		if err := batch.Set(homepageSenderIndexStateKey(), []byte("done"), pebble.Sync); err != nil {
			return err
		}
	}

	return batch.Commit(pebble.Sync)
}

func (a *Aggregator) homepageIndexStateDone(key []byte) (bool, error) {
	if _, err := a.store.Get(namespace, key); err == nil {
		return true, nil
	} else if err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return false, err
	}
	return false, nil
}

func (a *Aggregator) deleteHomepageMaterializedEntries(batch *pebble.Batch) error {
	return a.store.ScanPrefix(namespace, homepageMaterializedChatsPrefix(), func(key, value []byte) error {
		return batch.Delete(key, pebble.Sync)
	})
}

func (a *Aggregator) mergeHomepageMaterializedAliases(materializedByAlias map[string][]HomepageInteraction, msg *PrivateMessage) {
	item, ok := homepageInteractionFromMessage(msg)
	if !ok {
		return
	}
	for _, alias := range homepageSenderIndexAliases(msg) {
		materializedByAlias[alias] = mergeHomepageMaterializedInteraction(materializedByAlias[alias], item, homepageMaterializedChatsLimit)
	}
}

func (a *Aggregator) writeHomepageMaterializedSnapshot(batch *pebble.Batch, materializedByAlias map[string][]HomepageInteraction) error {
	for alias, items := range materializedByAlias {
		sortHomepageInteractions(items)
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

func (a *Aggregator) homepageIdentityAliases(params HomepageInteractionListParams) map[string]bool {
	aliases := make(map[string]bool)
	for _, id := range []string{params.GlobalMetaId, params.MetaId, params.Address} {
		for _, alias := range a.identityAliases(id) {
			aliases[aliasKey(alias)] = true
		}
	}
	return aliases
}

func aliasKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func isHomepageSimpleMsgProtocol(protocol string) bool {
	normalized := strings.ToLower(strings.TrimSpace(protocol))
	normalized = "/" + strings.Trim(normalized, "/")
	return normalized == HomepageSimpleMsgProtocolPath
}
