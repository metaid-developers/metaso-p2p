package privatechat

import (
	"strings"

	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// IdentityProfile is the small identity slice private-chat needs from
// userinfo to bridge chain-address MetaID keys and canonical globalMetaIds.
type IdentityProfile struct {
	MetaId        string
	GlobalMetaId  string
	Address       string
	ChatPublicKey string
}

type ProfileLookup interface {
	LookupByMetaId(metaid string) (*IdentityProfile, error)
	LookupByGlobalMetaId(globalMetaId string) (*IdentityProfile, error)
	LookupByAddress(address string) (*IdentityProfile, error)
}

type localIdentityLookup interface {
	LookupLocalByIdentity(identity string) (*IdentityProfile, error)
}

type identityProfileCache map[string]*IdentityProfile

func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.profileLookup = lookup
}

func (a *Aggregator) identityAliases(id string) []string {
	return a.identityAliasesCached(id, make(identityProfileCache))
}

func (a *Aggregator) identityAliasesCached(id string, profiles identityProfileCache) []string {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}

	aliases := make([]string, 0, 4)
	seen := make(map[string]bool)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		key := strings.ToLower(value)
		if seen[key] {
			return
		}
		seen[key] = true
		aliases = append(aliases, value)
	}
	add(id)

	if a == nil || a.profileLookup == nil {
		return aliases
	}
	if profile := a.identityProfileCached(profiles, id); profile != nil {
		add(profile.MetaId)
		add(profile.GlobalMetaId)
		add(profile.Address)
	}

	return aliases
}

func (a *Aggregator) identityProfile(ids ...string) *IdentityProfile {
	return a.identityProfileCached(make(identityProfileCache), ids...)
}

func (a *Aggregator) identityProfileCached(profiles identityProfileCache, ids ...string) *IdentityProfile {
	if a == nil || a.profileLookup == nil {
		return nil
	}
	if profiles == nil {
		profiles = make(identityProfileCache)
	}

	seen := make(map[string]bool)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		if seen[key] {
			continue
		}
		seen[key] = true
		if profile, ok := profiles[key]; ok {
			if profile != nil {
				return profile
			}
			continue
		}

		if lookup, ok := a.profileLookup.(localIdentityLookup); ok {
			profile, err := lookup.LookupLocalByIdentity(id)
			if err == nil && profile != nil {
				cacheIdentityProfile(profiles, profile)
				return profile
			}
			profiles[key] = nil
			continue
		}

		for _, lookup := range []func(string) (*IdentityProfile, error){
			a.profileLookup.LookupByMetaId,
			a.profileLookup.LookupByGlobalMetaId,
			a.profileLookup.LookupByAddress,
		} {
			profile, err := lookup(id)
			if err == nil && profile != nil {
				cacheIdentityProfile(profiles, profile)
				return profile
			}
		}
		profiles[key] = nil
	}

	return nil
}

func cacheIdentityProfile(profiles identityProfileCache, profile *IdentityProfile) {
	if profiles == nil || profile == nil {
		return
	}
	for _, id := range []string{profile.MetaId, profile.GlobalMetaId, profile.Address} {
		key := strings.ToLower(strings.TrimSpace(id))
		if key != "" {
			profiles[key] = profile
		}
	}
}

func (a *Aggregator) canonicalizePrivateMessage(msg *PrivateMessage) {
	a.canonicalizePrivateMessageCached(msg, make(identityProfileCache))
}

func (a *Aggregator) canonicalizePrivateMessages(messages []*PrivateMessage, profiles identityProfileCache) {
	if profiles == nil {
		profiles = make(identityProfileCache)
	}
	for _, msg := range messages {
		a.canonicalizePrivateMessageCached(msg, profiles)
	}
}

func (a *Aggregator) canonicalizePrivateMessageCached(msg *PrivateMessage, profiles identityProfileCache) {
	if msg == nil {
		return
	}

	if profile := a.identityProfileCached(profiles, msg.FromGlobalMetaId, msg.From, msg.FromAddress); profile != nil {
		if profile.GlobalMetaId != "" {
			msg.FromGlobalMetaId = profile.GlobalMetaId
		}
		if profile.MetaId != "" {
			msg.From = profile.MetaId
		}
		if profile.Address != "" {
			msg.FromAddress = profile.Address
		}
		msg.FromUserInfo = privateMessageUserInfo(profile)
	}
	if profile := a.identityProfileCached(profiles, msg.ToGlobalMetaId, msg.To, msg.ToAddress); profile != nil {
		if profile.GlobalMetaId != "" {
			msg.ToGlobalMetaId = profile.GlobalMetaId
		}
		if profile.MetaId != "" {
			msg.To = profile.MetaId
		}
		if profile.Address != "" {
			msg.ToAddress = profile.Address
		}
		msg.ToUserInfo = privateMessageUserInfo(profile)
	}

	if msg.PinId != "" {
		msg.TxId = extractTxId(msg.PinId)
	} else {
		msg.TxId = extractTxId(msg.TxId)
	}
}

func privateMessageUserInfo(profile *IdentityProfile) *PrivateMessageUserInfo {
	if profile == nil {
		return nil
	}
	return &PrivateMessageUserInfo{
		GlobalMetaId:  profile.GlobalMetaId,
		MetaId:        profile.MetaId,
		Address:       profile.Address,
		ChatPublicKey: profile.ChatPublicKey,
	}
}

// canonicalizePrivateMessageForDelivery rejects unresolved MVC chain identities
// instead of exposing an address or 64-hex MetaID as a globalMetaId.
func (a *Aggregator) canonicalizePrivateMessageForDelivery(msg *PrivateMessage) bool {
	a.canonicalizePrivateMessage(msg)
	if msg == nil || !strings.EqualFold(strings.TrimSpace(msg.Chain), "mvc") {
		return msg != nil && strings.TrimSpace(msg.FromGlobalMetaId) != "" && strings.TrimSpace(msg.ToGlobalMetaId) != ""
	}
	if !isResolvedGlobalIdentity(msg.FromGlobalMetaId, msg.From, msg.FromAddress) {
		if globalMetaID := idaddress.EncodeGlobalMetaId(strings.TrimSpace(msg.FromAddress), msg.Chain); globalMetaID != "" {
			msg.FromGlobalMetaId = globalMetaID
		}
	}
	if !isResolvedGlobalIdentity(msg.ToGlobalMetaId, msg.To, msg.ToAddress) {
		toAddress := strings.TrimSpace(msg.ToAddress)
		if toAddress == "" && isBase58ChainAddress(msg.ToGlobalMetaId) {
			toAddress = strings.TrimSpace(msg.ToGlobalMetaId)
			msg.ToAddress = toAddress
		}
		if globalMetaID := idaddress.EncodeGlobalMetaId(toAddress, msg.Chain); globalMetaID != "" {
			msg.ToGlobalMetaId = globalMetaID
		}
	}
	return isResolvedGlobalIdentity(msg.FromGlobalMetaId, msg.From, msg.FromAddress) &&
		isResolvedGlobalIdentity(msg.ToGlobalMetaId, msg.To, msg.ToAddress)
}

func isResolvedGlobalIdentity(globalMetaID, metaID, address string) bool {
	globalMetaID = strings.TrimSpace(globalMetaID)
	if globalMetaID == "" {
		return false
	}
	if strings.EqualFold(globalMetaID, strings.TrimSpace(address)) && strings.TrimSpace(address) != "" {
		return false
	}
	if strings.EqualFold(globalMetaID, strings.TrimSpace(metaID)) && isOnChainIdentity(globalMetaID) {
		return false
	}
	return !isOnChainIdentity(globalMetaID)
}

func isOnChainIdentity(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) == 64 && isHex(value) {
		return true
	}
	_, _, err := idaddress.DecodeGlobalMetaId(value)
	if err == nil {
		return false
	}
	_, _, err = idaddress.Base58CheckDecode(value)
	return err == nil
}

func isBase58ChainAddress(value string) bool {
	_, _, err := idaddress.Base58CheckDecode(strings.TrimSpace(value))
	return err == nil
}
