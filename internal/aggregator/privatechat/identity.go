package privatechat

import "strings"

// IdentityProfile is the small identity slice private-chat needs from
// userinfo to bridge chain-address MetaID keys and canonical globalMetaIds.
type IdentityProfile struct {
	MetaId       string
	GlobalMetaId string
	Address      string
}

type ProfileLookup interface {
	LookupByMetaId(metaid string) (*IdentityProfile, error)
	LookupByGlobalMetaId(globalMetaId string) (*IdentityProfile, error)
	LookupByAddress(address string) (*IdentityProfile, error)
}

type localIdentityLookup interface {
	LookupLocalByIdentity(identity string) (*IdentityProfile, error)
}

func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.profileLookup = lookup
}

func (a *Aggregator) identityAliases(id string) []string {
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
	if profile := a.identityProfile(id); profile != nil {
		add(profile.MetaId)
		add(profile.GlobalMetaId)
		add(profile.Address)
	}

	return aliases
}

func (a *Aggregator) identityProfile(ids ...string) *IdentityProfile {
	if a == nil || a.profileLookup == nil {
		return nil
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

		if lookup, ok := a.profileLookup.(localIdentityLookup); ok {
			profile, err := lookup.LookupLocalByIdentity(id)
			if err == nil && profile != nil {
				return profile
			}
			continue
		}

		for _, lookup := range []func(string) (*IdentityProfile, error){
			a.profileLookup.LookupByMetaId,
			a.profileLookup.LookupByGlobalMetaId,
			a.profileLookup.LookupByAddress,
		} {
			profile, err := lookup(id)
			if err == nil && profile != nil {
				return profile
			}
		}
	}

	return nil
}

func (a *Aggregator) canonicalizePrivateMessage(msg *PrivateMessage) {
	if msg == nil {
		return
	}

	if profile := a.identityProfile(msg.FromGlobalMetaId, msg.From, msg.FromAddress); profile != nil {
		if profile.GlobalMetaId != "" {
			msg.FromGlobalMetaId = profile.GlobalMetaId
		}
		if profile.MetaId != "" {
			msg.From = profile.MetaId
		}
		if profile.Address != "" {
			msg.FromAddress = profile.Address
		}
	}
	if profile := a.identityProfile(msg.ToGlobalMetaId, msg.To, msg.ToAddress); profile != nil {
		if profile.GlobalMetaId != "" {
			msg.ToGlobalMetaId = profile.GlobalMetaId
		}
		if profile.MetaId != "" {
			msg.To = profile.MetaId
		}
		if profile.Address != "" {
			msg.ToAddress = profile.Address
		}
	}

	if msg.PinId != "" {
		msg.TxId = extractTxId(msg.PinId)
	} else {
		msg.TxId = extractTxId(msg.TxId)
	}
}
