package bothomepage

import (
	"encoding/json"
	"fmt"
	"strings"
)

type cachedV3Response struct {
	Data             DataV3             `json:"data"`
	PresenceIdentity presenceIdentityV3 `json:"presenceIdentity"`
}

type presenceIdentityV3 struct {
	GlobalMetaId string `json:"globalMetaId"`
	MetaId       string `json:"metaId"`
	Address      string `json:"address"`
}

func buildV3CacheKey(requestGlobalMetaId string, opts Options) string {
	return fmt.Sprintf(
		"%s|sections=%t|services=%t|chats=%t|metaapps=%t|buzzes=%t|inactive=%t|chain=%s",
		strings.ToLower(strings.TrimSpace(requestGlobalMetaId)),
		opts.IncludeSections,
		opts.IncludeServices,
		opts.IncludeChats,
		opts.IncludeMetaApps,
		opts.IncludeBuzzes,
		opts.IncludeInactiveServices,
		strings.ToLower(strings.TrimSpace(opts.ChainName)),
	)
}

func (a *Aggregator) loadV3FromCache(cacheKey string, includePresence bool) (*DataV3, bool) {
	if a == nil || a.v3ResultCache == nil {
		return nil, false
	}

	raw, ok := a.v3ResultCache.Get(cacheKey)
	if !ok || len(raw) == 0 {
		return nil, false
	}

	var cached cachedV3Response
	if err := json.Unmarshal(raw, &cached); err != nil {
		a.v3ResultCache.Remove(cacheKey)
		return nil, false
	}

	cached.Data.Presence = a.resolvePresence(ProfileSnapshot{
		GlobalMetaId: cached.PresenceIdentity.GlobalMetaId,
		MetaId:       cached.PresenceIdentity.MetaId,
		Address:      cached.PresenceIdentity.Address,
	}, includePresence)
	return &cached.Data, true
}

func (a *Aggregator) storeV3Cache(cacheKey string, data *DataV3, profile *ProfileSnapshot) {
	if a == nil || a.v3ResultCache == nil || data == nil || profile == nil {
		return
	}

	raw, err := json.Marshal(cachedV3Response{
		Data: *data,
		PresenceIdentity: presenceIdentityV3{
			GlobalMetaId: strings.TrimSpace(profile.GlobalMetaId),
			MetaId:       strings.TrimSpace(profile.MetaId),
			Address:      strings.TrimSpace(profile.Address),
		},
	})
	if err != nil {
		return
	}

	a.v3ResultCache.Add(cacheKey, raw)
}
