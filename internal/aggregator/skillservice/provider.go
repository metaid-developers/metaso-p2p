package skillservice

import "strings"

// ProfileSnapshot is the subset of userinfo data that the Bot Hub API needs
// to render a service card or detail page. It is intentionally a fresh
// struct rather than re-exporting userinfo.UserProfile so this package can
// stay decoupled from userinfo's wire schema. Whatever userinfo decides to
// add or rename in the future cannot accidentally leak into Bot Hub
// responses.
//
// Fields here are exactly the provider profile pieces the spec asks the
// BotHub endpoints to populate:
//   - MetaId         → providerMetaId / provider.metaid
//   - GlobalMetaId   → providerGlobalMetaId / provider.globalMetaId
//   - Address        → providerAddress / provider.address
//   - Name           → providerName
//   - Avatar         → providerAvatar (relative path or absolute URL,
//     asset URL resolution lands in M4)
//   - AvatarId       → providerAvatarId / provider.avatarId
//   - ChatPublicKey  → providerChatPubkey (the communication addressing
//     field; missing is allowed and surfaces as "")
type ProfileSnapshot struct {
	MetaId        string
	GlobalMetaId  string
	Address       string
	Name          string
	Avatar        string
	AvatarId      string
	ChatPublicKey string
}

// ProfileLookup is the contract the skillservice aggregator depends on to
// fetch provider profile data in-process. main.go binds this to a thin
// adapter over the userinfo aggregator; tests can plug in a fake.
//
// All three methods return (nil, nil) when the profile is unknown — the
// aggregator must not synthesise a stub. Errors must only be returned for
// real I/O / decode failures; "missing" is the normal case while the
// indexer catches up and should not be conflated with failure.
type ProfileLookup interface {
	LookupByMetaId(metaid string) (*ProfileSnapshot, error)
	LookupByGlobalMetaId(globalMetaId string) (*ProfileSnapshot, error)
	LookupByAddress(address string) (*ProfileSnapshot, error)
}

// SetProfileLookup wires in a ProfileLookup implementation. main.go calls
// this once after both userinfo and skillservice are registered. It is safe
// to leave unset: ResolveProvider returns an empty snapshot when there is
// no lookup, the aggregator keeps building responses with just the chain-
// declared provider identity fields.
func (a *Aggregator) SetProfileLookup(lookup ProfileLookup) {
	a.profileLookup = lookup
}

// ResolveProvider looks up the provider profile for a service record using
// the configured ProfileLookup. It tries MetaId first, then GlobalMetaId,
// then Address — the first non-nil hit wins. Returns an empty snapshot
// (non-nil) when nothing is found so callers can blindly read fields.
//
// Per spec, a missing profile must NOT be converted into an
// action-permission verdict (canOrder=false / disabledReason=...). This
// helper simply returns empty strings; the API handlers decide how to
// present them.
func (a *Aggregator) ResolveProvider(rec *ServiceRecord) ProfileSnapshot {
	if rec == nil || a.profileLookup == nil {
		return ProfileSnapshot{}
	}

	if metaid := strings.TrimSpace(rec.ProviderMetaId); metaid != "" {
		if p, err := a.profileLookup.LookupByMetaId(metaid); err == nil && p != nil {
			return *p
		}
	}
	if gid := strings.TrimSpace(rec.ProviderGlobalMetaId); gid != "" {
		if p, err := a.profileLookup.LookupByGlobalMetaId(gid); err == nil && p != nil {
			return *p
		}
	}
	if addr := strings.TrimSpace(rec.ProviderAddress); addr != "" {
		if p, err := a.profileLookup.LookupByAddress(addr); err == nil && p != nil {
			return *p
		}
	}
	return ProfileSnapshot{}
}
