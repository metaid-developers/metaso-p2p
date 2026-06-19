package social

import "strings"

// TargetRef is the canonical follow-target identity shape used internally by
// the social aggregator.
type TargetRef struct {
	MetaId       string
	GlobalMetaId string
	Address      string
}

type ProfileLookup interface {
	LookupByMetaId(metaId string) (*TargetRef, error)
	LookupByGlobalMetaId(globalMetaId string) (*TargetRef, error)
	LookupByAddress(address string) (*TargetRef, error)
}

func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.profileLookup = lookup
}

func (a *Aggregator) lookupTargetRef(ref string) (*TargetRef, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || a == nil || a.profileLookup == nil {
		return nil, nil
	}

	for _, lookup := range []func(string) (*TargetRef, error){
		a.profileLookup.LookupByGlobalMetaId,
		a.profileLookup.LookupByMetaId,
		a.profileLookup.LookupByAddress,
	} {
		target, err := lookup(ref)
		if err != nil {
			return nil, err
		}
		if target != nil {
			return target, nil
		}
	}

	return nil, nil
}
