package publishedcontent

// Authoritative metaprotocol registry projection (MetaSo requirement
// "metaprotocol 权威注册表" §2–§4). The chain itself is permissionless —
// anyone can write a duplicate registration path or forge a modify pin — so
// the registry is a projection semantics: the authoritative view folds every
// valid registration of one payload `path` (lower-cased, globally unique
// across chains) into a single authoritative entry (the first valid
// registration) plus a conflict audit, and modify pins whose publisher does
// not match the source record's publisher are audited as invalid instead of
// joining the version chain.
//
// Two key families back the projection:
//
//	by_protocol_path:<注册键>  → JSON protocolRegistryState (sorted members;
//	                            members[0] is the authoritative registration,
//	                            the rest are conflicts). The registration key
//	                            is globally unique across chains (requirement
//	                            §0.2), so the key carries no chain segment and
//	                            each member records its own chain.
//	invalid_modify:<chain>:<targetSourcePinId>:<modifyPinId> → JSON
//	                            InvalidModify audit, reverse-queried per
//	                            record by the registry detail endpoint.
//
// The index is maintained at saveRecord time and rebuilt from the record
// store at every Init (self-healing; no backfill CLI needed).

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/cockroachdb/pebble"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb/metawebdoc"
)

const (
	keyByProtocolPath = "by_protocol_path:"
	keyInvalidModify  = "invalid_modify:"
)

// protocolRegistryPathPattern is the registry validation: a metaprotocol
// descriptor's payload `path` must name a protocol directory under
// /protocols/.
var protocolRegistryPathPattern = regexp.MustCompile(`^/protocols/[a-z0-9_]+(/[a-z0-9_]+)*$`)

// InvalidModifyReasonPublisherMismatch marks a modify pin whose publisher
// identity does not match the target record's source-pin publisher.
const InvalidModifyReasonPublisherMismatch = "publisher_mismatch"

// ProtocolRegistrationKey normalizes a payload `path` into the registry key
// (trim + lower-case) and validates it against the registry path shape.
func ProtocolRegistrationKey(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if key == "" || !protocolRegistryPathPattern.MatchString(key) {
		return "", false
	}
	return key, true
}

// protocolRegistryMember is one valid registration under a registration key.
type protocolRegistryMember struct {
	SourcePinId string `json:"sourcePinId"`
	ChainName   string `json:"chainName"`
	CreatedAt   int64  `json:"createdAt"` // source-pin createdAt, unix seconds
	Confirmed   bool   `json:"confirmed"` // false while only seen in mempool
}

// protocolRegistryState is the stored fold of one registration key. Members
// is always sorted by registryMemberLess; Members[0] is authoritative.
type protocolRegistryState struct {
	Members []protocolRegistryMember `json:"members"`
}

// registryMemberLess orders members: earliest createdAt first; within one
// second confirmed registrations precede mempool ones; pin id breaks ties.
func registryMemberLess(a, b protocolRegistryMember) bool {
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt < b.CreatedAt
	}
	if a.Confirmed != b.Confirmed {
		return a.Confirmed
	}
	return a.SourcePinId < b.SourcePinId
}

// PublisherIdentity is the cascade identity of a pin publisher.
type PublisherIdentity struct {
	GlobalMetaId string `json:"globalMetaId,omitempty"`
	MetaId       string `json:"metaid,omitempty"`
	Address      string `json:"address,omitempty"`
}

// InvalidModify is the audit record of a modify pin rejected by the
// publisher-identity check (requirement §4).
type InvalidModify struct {
	PinId             string            `json:"pinId"`
	TargetSourcePinId string            `json:"targetSourcePinId"`
	ModifierIdentity  PublisherIdentity `json:"modifierIdentity"`
	Reason            string            `json:"reason"`
	Timestamp         int64             `json:"timestamp"` // modify pin timestamp, unix seconds
}

// ProtocolRegistration is the folded read view of one registration key:
// the authoritative record plus every conflict record (sorted by the
// registry order, earliest first).
type ProtocolRegistration struct {
	RegKey        string
	Authoritative *Record
	Conflicts     []*Record
}

// RejectedProtocol is one metaprotocol record that fails the registry
// payload validation (the v1 `rejected[]` audit, unchanged).
type RejectedProtocol struct {
	PinId  string
	Reason string
}

// SetProtocolRegistryBlocklist installs the pin-id blocklist excluded from
// the registry projection (requirement §3.4). Must be called before Init so
// the startup rebuild applies it.
func (a *Aggregator) SetProtocolRegistryBlocklist(pinIds []string) {
	blocklist := make(map[string]bool, len(pinIds))
	for _, pinId := range pinIds {
		if trimmed := strings.TrimSpace(pinId); trimmed != "" {
			blocklist[trimmed] = true
		}
	}
	a.protocolBlocklist = blocklist
}

// SetProtocolOwnerBoundModifies toggles the processModify publisher-identity
// check (requirement §4; METASO_P2P_PROTOCOL_OWNER_BOUND_MODIFIES). A nil
// field means the documented default (true).
func (a *Aggregator) SetProtocolOwnerBoundModifies(enabled bool) {
	a.protocolOwnerBoundModifies = &enabled
}

func (a *Aggregator) ownerBoundModifiesEnabled() bool {
	return a.protocolOwnerBoundModifies == nil || *a.protocolOwnerBoundModifies
}

func protocolRegistryKeyFor(regKey string) []byte {
	return []byte(keyByProtocolPath + regKey)
}

func invalidModifyKey(chainName, targetSourcePinId, modifyPinId string) []byte {
	return []byte(keyInvalidModify + chainName + ":" + targetSourcePinId + ":" + modifyPinId)
}

func invalidModifyPrefix(chainName, targetSourcePinId string) []byte {
	return []byte(keyInvalidModify + chainName + ":" + targetSourcePinId + ":")
}

// protocolRegistrationKey projects a record onto its registration key;
// ok=false when the record is not a valid registration (hidden/revoked,
// blocklisted, payload not exposed as JSON, or payload path failing the
// registry shape).
func (a *Aggregator) protocolRegistrationKey(rec *Record) (string, bool) {
	if rec == nil || rec.Hidden || rec.Operation == OperationRevoke {
		return "", false
	}
	if a.protocolBlocklist[rec.SourcePinId] {
		return "", false
	}
	if !rec.PayloadExposed || rec.PayloadJSON == nil {
		return "", false
	}
	raw, _ := rec.PayloadJSON["path"].(string)
	return ProtocolRegistrationKey(raw)
}

// protocolRejectionReason explains why a record is not a valid registration;
// "" means it is valid. Mirrors the v1 registry audit reasons.
func (a *Aggregator) protocolRejectionReason(rec *Record) string {
	if rec == nil || !rec.PayloadExposed || rec.PayloadJSON == nil {
		return "missing or non-JSON payload"
	}
	raw, _ := rec.PayloadJSON["path"].(string)
	if strings.TrimSpace(raw) == "" {
		return "missing path"
	}
	if _, ok := ProtocolRegistrationKey(raw); !ok {
		return "invalid path: " + strings.TrimSpace(raw)
	}
	return ""
}

// maintainProtocolRegistry folds one committed metaprotocol record change
// into the by_protocol_path index (requirement §3.1): a valid registration
// upserts its member (a new earliest member becomes authoritative, the old
// authoritative turns conflict; a later one joins the conflict list); a
// revoked/invalidated record leaves its key, and the earliest remaining
// member succeeds as authoritative (the key is deleted when none remain).
// Called from saveRecord with the committed record and its previous state.
func (a *Aggregator) maintainProtocolRegistry(rec, previous *Record) error {
	isMetaProtocol := rec != nil && rec.ProtocolPath == PathMetaProtocol
	wasMetaProtocol := previous != nil && previous.ProtocolPath == PathMetaProtocol
	if !isMetaProtocol && !wasMetaProtocol {
		return nil
	}
	a.registryMu.Lock()
	defer a.registryMu.Unlock()

	currentKey, currentValid := "", false
	if isMetaProtocol {
		currentKey, currentValid = a.protocolRegistrationKey(rec)
	}
	previousKey, previousValid := "", false
	if wasMetaProtocol {
		previousKey, previousValid = a.protocolRegistrationKey(previous)
	}

	// The registration key is derived from the (mutable) payload path, so a
	// modify can move the record between keys: leave the old key whenever the
	// key changed or the record stopped being a valid registration.
	if previousValid && (!currentValid || previousKey != currentKey) {
		if err := a.removeRegistryMemberLocked(previousKey, previous.ChainName, previous.SourcePinId); err != nil {
			return err
		}
	}
	if currentValid {
		if err := a.upsertRegistryMemberLocked(currentKey, rec); err != nil {
			return err
		}
	}
	return nil
}

func (a *Aggregator) loadRegistryStateLocked(regKey string) (*protocolRegistryState, error) {
	raw, err := a.store.Get(Namespace, protocolRegistryKeyFor(regKey))
	if err != nil {
		if errors.Is(err, pebble.ErrNotFound) {
			return &protocolRegistryState{}, nil
		}
		return nil, err
	}
	var state protocolRegistryState
	if err := json.Unmarshal(raw, &state); err != nil {
		// Corrupt state self-heals: the next upsert rewrites the key and the
		// Init rebuild regenerates it from the record store.
		return &protocolRegistryState{}, nil
	}
	return &state, nil
}

func (a *Aggregator) writeRegistryStateLocked(regKey string, state *protocolRegistryState) error {
	if len(state.Members) == 0 {
		return a.store.Delete(Namespace, protocolRegistryKeyFor(regKey))
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return a.store.Set(Namespace, protocolRegistryKeyFor(regKey), raw)
}

func (a *Aggregator) upsertRegistryMemberLocked(regKey string, rec *Record) error {
	state, err := a.loadRegistryStateLocked(regKey)
	if err != nil {
		return err
	}
	member := protocolRegistryMember{
		SourcePinId: rec.SourcePinId,
		ChainName:   rec.ChainName,
		CreatedAt:   metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
		Confirmed:   !rec.IsMempool,
	}
	kept := state.Members[:0]
	for _, existing := range state.Members {
		if existing.SourcePinId == member.SourcePinId && existing.ChainName == member.ChainName {
			continue
		}
		kept = append(kept, existing)
	}
	state.Members = append(kept, member)
	sort.Slice(state.Members, func(i, j int) bool {
		return registryMemberLess(state.Members[i], state.Members[j])
	})
	return a.writeRegistryStateLocked(regKey, state)
}

func (a *Aggregator) removeRegistryMemberLocked(regKey, chainName, sourcePinId string) error {
	state, err := a.loadRegistryStateLocked(regKey)
	if err != nil {
		return err
	}
	kept := state.Members[:0]
	for _, existing := range state.Members {
		if existing.SourcePinId == sourcePinId && existing.ChainName == chainName {
			continue
		}
		kept = append(kept, existing)
	}
	if len(kept) == len(state.Members) {
		return nil
	}
	state.Members = kept
	return a.writeRegistryStateLocked(regKey, state)
}

// rebuildProtocolRegistry regenerates the by_protocol_path index from the
// record store (requirement §3.2). Runs at every Init — same pattern as the
// search-document snapshot rebuild — so existing deployments fold their
// stored registrations once at startup and every later start self-heals.
func (a *Aggregator) rebuildProtocolRegistry() error {
	if a == nil || a.store == nil {
		return nil
	}
	a.registryMu.Lock()
	defer a.registryMu.Unlock()

	if err := a.store.DeleteByPrefix(Namespace, []byte(keyByProtocolPath)); err != nil {
		return fmt.Errorf("rebuildProtocolRegistry: clear: %w", err)
	}

	states := make(map[string]*protocolRegistryState)
	if err := a.store.ScanPrefix(Namespace, []byte(keyRecord), func(_, value []byte) error {
		var rec Record
		if err := json.Unmarshal(value, &rec); err != nil {
			return nil
		}
		if rec.ProtocolPath != PathMetaProtocol {
			return nil
		}
		regKey, ok := a.protocolRegistrationKey(&rec)
		if !ok {
			return nil
		}
		state := states[regKey]
		if state == nil {
			state = &protocolRegistryState{}
			states[regKey] = state
		}
		state.Members = append(state.Members, protocolRegistryMember{
			SourcePinId: rec.SourcePinId,
			ChainName:   rec.ChainName,
			CreatedAt:   metawebdoc.NormalizeUnixSeconds(rec.CreatedAt),
			Confirmed:   !rec.IsMempool,
		})
		return nil
	}); err != nil {
		return fmt.Errorf("rebuildProtocolRegistry: scan: %w", err)
	}

	for regKey, state := range states {
		sort.Slice(state.Members, func(i, j int) bool {
			return registryMemberLess(state.Members[i], state.Members[j])
		})
		if err := a.writeRegistryStateLocked(regKey, state); err != nil {
			return fmt.Errorf("rebuildProtocolRegistry: write %s: %w", regKey, err)
		}
	}
	return nil
}

// ProtocolRegistrationByPath returns the folded registration of one payload
// path (normalized internally), or (nil, nil) when the path is unregistered.
// Mempool registrations count as registered (requirement §3.3).
func (a *Aggregator) ProtocolRegistrationByPath(path string) (*ProtocolRegistration, error) {
	regKey, ok := ProtocolRegistrationKey(path)
	if !ok || a == nil || a.store == nil {
		return nil, nil
	}
	a.registryMu.Lock()
	defer a.registryMu.Unlock()
	state, err := a.loadRegistryStateLocked(regKey)
	if err != nil {
		return nil, err
	}
	return a.protocolRegistrationFromState(regKey, state)
}

// ProtocolRegistrations returns every folded registration (the read model of
// the registry list endpoint). Registration order is not defined here; the
// HTTP layer sorts.
func (a *Aggregator) ProtocolRegistrations() ([]*ProtocolRegistration, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("publishedcontent store unavailable")
	}
	type entry struct {
		regKey string
		state  *protocolRegistryState
	}
	var entries []entry
	a.registryMu.Lock()
	err := a.store.ScanPrefix(Namespace, []byte(keyByProtocolPath), func(key, value []byte) error {
		var state protocolRegistryState
		if err := json.Unmarshal(value, &state); err != nil || len(state.Members) == 0 {
			return nil
		}
		entries = append(entries, entry{
			regKey: strings.TrimPrefix(string(key), keyByProtocolPath),
			state:  &state,
		})
		return nil
	})
	a.registryMu.Unlock()
	if err != nil {
		return nil, err
	}

	registrations := make([]*ProtocolRegistration, 0, len(entries))
	for _, e := range entries {
		registration, err := a.protocolRegistrationFromState(e.regKey, e.state)
		if err != nil {
			return nil, err
		}
		if registration != nil {
			registrations = append(registrations, registration)
		}
	}
	return registrations, nil
}

// protocolRegistrationFromState loads the member records of one folded
// state. Returns nil when no member record resolves (index/record drift;
// the next Init rebuild regenerates the key).
func (a *Aggregator) protocolRegistrationFromState(regKey string, state *protocolRegistryState) (*ProtocolRegistration, error) {
	if state == nil || len(state.Members) == 0 {
		return nil, nil
	}
	registration := &ProtocolRegistration{RegKey: regKey}
	for i, member := range state.Members {
		rec, err := a.loadRecord(member.ChainName, PathMetaProtocol, member.SourcePinId)
		if err != nil {
			return nil, err
		}
		if rec == nil {
			continue
		}
		if i == 0 {
			registration.Authoritative = rec
		} else {
			registration.Conflicts = append(registration.Conflicts, rec)
		}
	}
	if registration.Authoritative == nil {
		return nil, nil
	}
	return registration, nil
}

// RejectedProtocolRecords returns the registry audit of visible metaprotocol
// records that fail the payload validation (requirement §3.5: the v1
// rejected[] semantics, unchanged). Blocklisted pins are excluded silently —
// they are removed by policy, not rejected for shape.
func (a *Aggregator) RejectedProtocolRecords() ([]RejectedProtocol, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("publishedcontent store unavailable")
	}
	rejected := make([]RejectedProtocol, 0)
	for _, chainName := range searchLookupChains {
		prefix := []byte(keyRecord + chainName + ":" + PathMetaProtocol + ":")
		if err := a.store.ScanPrefix(Namespace, prefix, func(_, value []byte) error {
			var rec Record
			if err := json.Unmarshal(value, &rec); err != nil {
				return nil
			}
			if rec.Hidden || rec.Operation == OperationRevoke || a.protocolBlocklist[rec.SourcePinId] {
				return nil
			}
			if reason := a.protocolRejectionReason(&rec); reason != "" {
				rejected = append(rejected, RejectedProtocol{PinId: rec.SourcePinId, Reason: reason})
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return rejected, nil
}

// recordInvalidProtocolModify audits a modify pin whose publisher identity
// fails the owner check (requirement §4): no new version record, no
// pin_to_source pointer — the version chain stays clean and the pin's reads
// fall back to the MANAPI passthrough.
func (a *Aggregator) recordInvalidProtocolModify(pin *aggregator.PinInscription, target *Record) error {
	audit := InvalidModify{
		PinId:             pin.Id,
		TargetSourcePinId: target.SourcePinId,
		ModifierIdentity:  publisherIdentityOfPin(pin),
		Reason:            InvalidModifyReasonPublisherMismatch,
		Timestamp:         metawebdoc.NormalizeUnixSeconds(pin.Timestamp),
	}
	raw, err := json.Marshal(audit)
	if err != nil {
		return err
	}
	return a.store.Set(Namespace, invalidModifyKey(pin.ChainName, target.SourcePinId, pin.Id), raw)
}

// InvalidModifies returns the audited invalid modifies targeting one record,
// oldest first (requirement §4: the detail endpoint's invalidModifies[]).
func (a *Aggregator) InvalidModifies(chainName, targetSourcePinId string) ([]*InvalidModify, error) {
	if a == nil || a.store == nil {
		return nil, errors.New("publishedcontent store unavailable")
	}
	audits := make([]*InvalidModify, 0)
	if err := a.store.ScanPrefix(Namespace, invalidModifyPrefix(chainName, targetSourcePinId), func(_, value []byte) error {
		var audit InvalidModify
		if err := json.Unmarshal(value, &audit); err != nil {
			return nil
		}
		audits = append(audits, &audit)
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(audits, func(i, j int) bool {
		if audits[i].Timestamp != audits[j].Timestamp {
			return audits[i].Timestamp < audits[j].Timestamp
		}
		return audits[i].PinId < audits[j].PinId
	})
	return audits, nil
}

// publisherIdentityOfPin extracts the modify pin's publisher identity (same
// field precedence as newRecordFromPin).
func publisherIdentityOfPin(pin *aggregator.PinInscription) PublisherIdentity {
	if pin == nil {
		return PublisherIdentity{}
	}
	return PublisherIdentity{
		GlobalMetaId: canonicalPublisherGlobalMetaId(pin),
		MetaId:       firstNonEmpty(pin.MetaId, pin.CreateMetaId),
		Address:      firstNonEmpty(pin.Address, pin.CreateAddress),
	}
}

// publisherIdentityMatches applies the requirement §2 cascade: when both
// sides carry a globalMetaId compare it; else when both carry a metaId
// compare it; else compare addresses.
func publisherIdentityMatches(source, modifier PublisherIdentity) bool {
	if source.GlobalMetaId != "" && modifier.GlobalMetaId != "" {
		return strings.EqualFold(source.GlobalMetaId, modifier.GlobalMetaId)
	}
	if source.MetaId != "" && modifier.MetaId != "" {
		return strings.EqualFold(source.MetaId, modifier.MetaId)
	}
	return source.Address != "" && source.Address == modifier.Address
}
