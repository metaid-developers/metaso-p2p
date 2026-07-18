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
	if lookup, ok := a.profileLookup.(localIdentityLookup); ok {
		profile, err := lookup.LookupLocalByIdentity(id)
		if err == nil && profile != nil {
			add(profile.MetaId)
			add(profile.GlobalMetaId)
			add(profile.Address)
		}
		return aliases
	}

	for _, lookup := range []func(string) (*IdentityProfile, error){
		a.profileLookup.LookupByMetaId,
		a.profileLookup.LookupByGlobalMetaId,
		a.profileLookup.LookupByAddress,
	} {
		profile, err := lookup(id)
		if err != nil || profile == nil {
			continue
		}
		add(profile.MetaId)
		add(profile.GlobalMetaId)
		add(profile.Address)
	}

	return aliases
}
