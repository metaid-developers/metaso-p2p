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

	if err := a.ensureHomepageSenderIndexes(); err != nil {
		return nil, err
	}

	matches, err := a.listHomepageInteractionsBySenderIndex(aliases, size)
	if err != nil {
		return nil, err
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Timestamp != matches[j].Timestamp {
			return matches[i].Timestamp > matches[j].Timestamp
		}
		return matches[i].PinId < matches[j].PinId
	})

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
			if msg.PinId == "" || msg.To == "" || !isHomepageSimpleMsgProtocol(msg.Protocol) {
				return nil
			}
			item := HomepageInteraction{
				PinId:        msg.PinId,
				ProtocolPath: HomepageSimpleMsgProtocolPath,
				Timestamp:    msg.Timestamp,
				InteractWith: msg.To,
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

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Timestamp != matches[j].Timestamp {
			return matches[i].Timestamp > matches[j].Timestamp
		}
		return matches[i].PinId < matches[j].PinId
	})
	return matches, exhausted, nil
}

var errStopHomepageIndexScan = errors.New("stop homepage sender index scan")

func (a *Aggregator) ensureHomepageSenderIndexes() error {
	if a == nil || a.store == nil {
		return nil
	}

	if _, err := a.store.Get(namespace, homepageSenderIndexStateKey()); err == nil {
		return nil
	} else if err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return err
	}

	a.homepageIndex.Lock()
	defer a.homepageIndex.Unlock()

	if _, err := a.store.Get(namespace, homepageSenderIndexStateKey()); err == nil {
		return nil
	} else if err != nil && !errors.Is(err, pebble.ErrNotFound) {
		return err
	}

	db, err := a.store.OpenDB(namespace)
	if err != nil {
		return err
	}

	batch := db.NewBatch()
	defer batch.Close()

	err = a.store.ScanPrefix(namespace, []byte(pchatKeyConst), func(key, value []byte) error {
		var msg PrivateMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		return writeHomepageSenderIndexEntries(batch, &msg, value)
	})
	if err != nil {
		return err
	}

	if err := batch.Set(homepageSenderIndexStateKey(), []byte("done"), pebble.Sync); err != nil {
		return err
	}

	return batch.Commit(pebble.Sync)
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
