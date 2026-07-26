package publishedcontent

// MetaAppProfileSnapshot carries the userinfo fields the MetaApp API
// surfaces alongside publisher identity. Keep it minimal: name and avatar
// pin id are what downstream renders next to each app.
type MetaAppProfileSnapshot struct {
	Name     string
	AvatarId string
}

// MetaAppProfileLookup resolves publisher profiles from the userinfo
// aggregator. Follows the same per-package interface + adapter pattern as
// skillservice.ProfileLookup so userinfo stays oblivious to consumers.
type MetaAppProfileLookup interface {
	LookupByGlobalMetaId(globalMetaId string) (*MetaAppProfileSnapshot, error)
	LookupByMetaId(metaid string) (*MetaAppProfileSnapshot, error)
	LookupByAddress(address string) (*MetaAppProfileSnapshot, error)
}

// SetProfileLookup wires the userinfo-backed profile resolver. Without it
// publisherName/publisherAvatarId stay empty and responses are unaffected.
func (a *Aggregator) SetProfileLookup(lookup MetaAppProfileLookup) {
	a.profileLookup = lookup
}

// enrichMetaAppPublisher fills publisherName/publisherAvatarId in place,
// preferring globalMetaId and falling back to metaId / address. Lookup
// failures are non-fatal: the item keeps its identity fields only.
func (a *Aggregator) enrichMetaAppPublisher(item *MetaAppItem) {
	if a == nil || a.profileLookup == nil || item == nil {
		return
	}
	var snap *MetaAppProfileSnapshot
	var err error
	switch {
	case item.PublisherGlobalMetaId != "":
		snap, err = a.profileLookup.LookupByGlobalMetaId(item.PublisherGlobalMetaId)
	case item.PublisherMetaId != "":
		snap, err = a.profileLookup.LookupByMetaId(item.PublisherMetaId)
	case item.PublisherAddress != "":
		snap, err = a.profileLookup.LookupByAddress(item.PublisherAddress)
	}
	if err != nil || snap == nil {
		return
	}
	item.PublisherName = snap.Name
	item.PublisherAvatarId = snap.AvatarId
}
