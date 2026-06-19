package privatechat

import (
	"encoding/json"
	"sort"
	"strings"
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

	seenPins := make(map[string]bool)
	var matches []HomepageInteraction
	err := a.store.ScanPrefix(namespace, []byte(pchatKeyConst), func(key, value []byte) error {
		var msg PrivateMessage
		if e := json.Unmarshal(value, &msg); e != nil {
			return nil
		}
		if msg.PinId == "" || msg.To == "" {
			return nil
		}
		if !isHomepageSimpleMsgProtocol(msg.Protocol) {
			return nil
		}
		if !aliases[aliasKey(msg.From)] && !aliases[aliasKey(msg.FromGlobalMetaId)] && !aliases[aliasKey(msg.FromAddress)] {
			return nil
		}
		if seenPins[msg.PinId] {
			return nil
		}

		seenPins[msg.PinId] = true
		matches = append(matches, HomepageInteraction{
			PinId:        msg.PinId,
			ProtocolPath: HomepageSimpleMsgProtocolPath,
			Timestamp:    msg.Timestamp,
			InteractWith: msg.To,
		})
		return nil
	})
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
