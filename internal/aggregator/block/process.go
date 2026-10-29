package block

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
)

// On-chain block protocol paths (spec §1.4):
//   - canonical ingest path: the registered protocol /protocols/simpleblock
//   - compatibility parse path: /private/block/simpleprivateblock (the
//     in-code spelling predating the protocol registration; same payload)
//
// Both dispatch to the same handler with the same payload schema
// {"to": string, "blockState": 1 | -1}. Only create operations are ingested.
// Group block protocols (simplegroupblock/simplegroupjoinblock) are out of
// scope this round and never reach this handler (spec §5.2).
const (
	pathCanonicalSimpleblock = "/protocols/simpleblock"
	pathCompatPrivateBlock   = "/private/block/simpleprivateblock"

	operationCreate = "create"

	blockStateBlocked  int64 = 1
	blockStateUnlocked int64 = -1
)

// ChainBlockReasonPrefix is the Reason prefix stamped onto notify `blocked:`
// entries written by the on-chain event bridge (spec §4.6).
const ChainBlockReasonPrefix = "on-chain:simpleblock:"

// simpleblockPayload is the wire shape of both block paths.
type simpleblockPayload struct {
	To         string          `json:"to"`
	BlockState json.RawMessage `json:"blockState"`
}

func (p *simpleblockPayload) state() int64 {
	raw := strings.TrimSpace(string(p.BlockState))
	if raw == "" {
		return blockStateBlocked
	}
	if v, err := strconv.ParseInt(strings.Trim(raw, `"`), 10, 64); err == nil {
		return v
	}
	// Absent or unparseable state defaults to block (spec §1.1 payload).
	return blockStateBlocked
}

// IsBlockPath reports whether the pin path is one of the two ingested
// simpleblock paths (compared on the normalized form).
func IsBlockPath(path string) bool {
	normalised := normalisedPinPath(path)
	return normalised == normalisedPinPath(pathCanonicalSimpleblock) ||
		normalised == normalisedPinPath(pathCompatPrivateBlock)
}

func normalisedPinPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	if idx := strings.Index(path, "@"); idx >= 0 {
		path = path[:idx]
	}
	return strings.Trim(strings.TrimRight(path, "/"), "/")
}

// IngestBlockPin ingests one confirmed block pin into the read model.
// It is the entry point shared by HandleBlockPin (registry dispatch) and the
// privatechat compatibility wiring; repeated pins are a no-op (replay dedupe).
func (a *Aggregator) IngestBlockPin(pin *aggregator.PinInscription) error {
	if pin == nil || !IsBlockPath(pin.Path) {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(pin.Operation), operationCreate) {
		// Only create operations are ingested (spec §1.4). simpleblock has
		// no revoke semantics — unblock is a new create with blockState -1.
		return nil
	}

	var payload simpleblockPayload
	if err := json.Unmarshal(pin.ContentBody, &payload); err != nil {
		log.Printf("[block] failed to parse %s pinId=%s: %v", normalisedPinPath(pin.Path), pin.Id, err)
		return nil
	}
	targetRef := strings.TrimSpace(payload.To)
	if targetRef == "" {
		log.Printf("[block] %s pinId=%s has empty to, ignored", normalisedPinPath(pin.Path), pin.Id)
		return nil
	}

	// Replay dedupe: a pin already folded into the read model never applies
	// again, keeping last-write-wins correct when backfill replay and live
	// ingest overlap.
	if _, _, seen := a.loadPairByPin(strings.TrimSpace(pin.Id)); seen {
		return nil
	}

	fromIdentity, err := a.resolveFromIdentity(pin)
	if err != nil {
		return err
	}
	if fromIdentity == nil || fromIdentity.GlobalMetaId == "" {
		log.Printf("[block] pinId=%s publisher identity unresolved, ignored", pin.Id)
		return nil
	}

	toIdentity, ok := a.normalizeTargetIdentity(targetRef)
	if !ok {
		// Unresolved peer references create no edge but stay auditable via
		// the pin-level index (same policy as the follow model, spec §2.5).
		log.Printf("[block] pinId=%s target %q unresolved, no edge", pin.Id, targetRef)
		if err := a.recordPinPair(strings.TrimSpace(pin.Id), fromIdentity.GlobalMetaId, targetRef); err != nil {
			return err
		}
		return nil
	}

	blockPinId := strings.TrimSpace(pin.Id)
	switch payload.state() {
	case blockStateUnlocked:
		if err := a.removeActiveBlock(fromIdentity.GlobalMetaId, toIdentity.GlobalMetaId, blockPinId); err != nil {
			return err
		}
		if a.chatBlockBridge != nil {
			if err := a.chatBlockBridge.RemoveChainBlock(identityAliases(fromIdentity), identityAliases(toIdentity)); err != nil {
				log.Printf("[block] notify bridge unblock failed pinId=%s: %v", blockPinId, err)
			}
		}
		log.Printf("[block] unblock folded: from=%s to=%s pinId=%s", fromIdentity.GlobalMetaId, toIdentity.GlobalMetaId, blockPinId)
	default:
		blockedAt := pin.Timestamp
		if err := a.saveActiveBlock(&BlockEdge{
			FromGlobalMetaId: fromIdentity.GlobalMetaId,
			ToGlobalMetaId:   toIdentity.GlobalMetaId,
			BlockPinId:       blockPinId,
			BlockedAt:        blockedAt,
			BlockState:       blockStateBlocked,
		}); err != nil {
			return err
		}
		if err := a.recordPinPair(blockPinId, fromIdentity.GlobalMetaId, toIdentity.GlobalMetaId); err != nil {
			return err
		}
		if a.chatBlockBridge != nil {
			if err := a.chatBlockBridge.ApplyChainBlock(identityAliases(fromIdentity), identityAliases(toIdentity), blockPinId, blockedAt); err != nil {
				log.Printf("[block] notify bridge block failed pinId=%s: %v", blockPinId, err)
			}
		}
		log.Printf("[block] block folded: from=%s to=%s pinId=%s", fromIdentity.GlobalMetaId, toIdentity.GlobalMetaId, blockPinId)
	}
	return nil
}

// resolveFromIdentity normalizes the pin publisher identity: the indexer
// supplied GlobalMetaId wins; otherwise hex/address references resolve
// through the profile lookup (spec §2.3).
func (a *Aggregator) resolveFromIdentity(pin *aggregator.PinInscription) (*IdentityRef, error) {
	if pin == nil {
		return nil, nil
	}
	if globalMetaId := strings.TrimSpace(pin.GlobalMetaId); globalMetaId != "" && isGlobalMetaIdRef(globalMetaId) {
		identity := &IdentityRef{GlobalMetaId: globalMetaId}
		a.augmentIdentity(identity)
		return identity, nil
	}
	ref := firstNonEmpty(pin.MetaId, pin.CreateMetaId, pin.Address, pin.CreateAddress)
	if ref == "" {
		return nil, nil
	}
	identity, ok := a.normalizeTargetIdentity(ref)
	if !ok {
		return nil, nil
	}
	return identity, nil
}

// normalizeTargetIdentity normalizes a peer identity reference: GlobalMetaIDs
// pass through; legacy hex and chain addresses resolve through the profile
// lookup (spec §2.3). Unresolvable inputs report ok=false.
func (a *Aggregator) normalizeTargetIdentity(ref string) (*IdentityRef, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, false
	}
	if isGlobalMetaIdRef(ref) {
		identity := &IdentityRef{GlobalMetaId: ref}
		a.augmentIdentity(identity)
		return identity, true
	}
	if a == nil || a.profileLookup == nil {
		return nil, false
	}
	target, err := a.lookupTargetRef(ref)
	if err != nil {
		log.Printf("[block] profile lookup failed for %q: %v", ref, err)
		return nil, false
	}
	if target == nil || strings.TrimSpace(target.GlobalMetaId) == "" {
		return nil, false
	}
	return target, true
}

// augmentIdentity best-effort fills hex/address aliases for a canonical
// GlobalMetaID from the profile store; failures never block ingestion.
func (a *Aggregator) augmentIdentity(identity *IdentityRef) {
	if a == nil || a.profileLookup == nil || identity == nil {
		return
	}
	if identity.MetaId != "" && identity.Address != "" {
		return
	}
	profile, err := a.profileLookup.LookupByGlobalMetaId(identity.GlobalMetaId)
	if err != nil || profile == nil {
		return
	}
	if identity.MetaId == "" {
		identity.MetaId = strings.TrimSpace(profile.MetaId)
	}
	if identity.Address == "" {
		identity.Address = strings.TrimSpace(profile.Address)
	}
	if identity.Name == "" {
		identity.Name = profile.Name
		identity.NameId = profile.NameId
		identity.AvatarId = profile.AvatarId
		identity.Bio = profile.Bio
		identity.BioId = profile.BioId
	}
}

// identityAliases renders every known identifier form of an identity,
// canonical GlobalMetaID first.
func identityAliases(identity *IdentityRef) []string {
	if identity == nil {
		return nil
	}
	aliases := make([]string, 0, 3)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range aliases {
			if strings.EqualFold(existing, value) {
				return
			}
		}
		aliases = append(aliases, value)
	}
	add(identity.GlobalMetaId)
	add(identity.MetaId)
	add(identity.Address)
	return aliases
}

func (a *Aggregator) lookupTargetRef(ref string) (*IdentityRef, error) {
	if a == nil || a.profileLookup == nil {
		return nil, nil
	}
	for _, lookup := range []func(string) (*IdentityRef, error){
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

func isGlobalMetaIdRef(ref string) bool {
	// Same pragmatic prefix check as the follow model's
	// canonicalGlobalMetaIdRef ("idq…" P2PKH), widened to "idp…" so the
	// P2SH rendering documented in MAN's GLOBALMETAID.md (spec §2.1) is
	// canonical too. Legacy 64-hex metaids can never collide ('i' is not a
	// hex digit); deep checksum validation is deliberately not applied —
	// the repository validator rejects some documented upstream renderings,
	// and unresolvable ids fall through to the profile lookup anyway.
	ref = strings.ToLower(strings.TrimSpace(ref))
	return strings.HasPrefix(ref, "idq") || strings.HasPrefix(ref, "idp")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
