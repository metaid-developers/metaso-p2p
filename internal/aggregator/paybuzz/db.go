package paybuzz

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// Pebble key layout in the "paybuzz" namespace:
//
//	rec:<sourcePinId>                          -> JSON Record
//	pin2src:<pinId>                            -> <sourcePinId>
//	ptime:<invCreatedAt16>:<sourcePinId>       -> "1"
//	pauthor:<identityLower>\x00<sourcePinId>   -> "1"
//	ptoken:<assetKeyLower>\x00<sourcePinId>    -> "1"
//	pvip:<collectionPinIdLower>\x00<sourcePinId> -> "1"
const (
	keyRec    = "rec:"
	keyPinSrc = "pin2src:"
	keyTime   = "ptime:"
	keyAuthor = "pauthor:"
	keyToken  = "ptoken:"
	keyVip    = "pvip:"
)

func recKey(sourcePinId string) []byte {
	return []byte(keyRec + strings.ToLower(strings.TrimSpace(sourcePinId)))
}

func pinSrcKey(pinId string) []byte {
	return []byte(keyPinSrc + strings.ToLower(strings.TrimSpace(pinId)))
}

func timeKey(createdAt int64, sourcePinId string) []byte {
	var ts uint64
	if createdAt > 0 {
		ts = uint64(createdAt)
	}
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, ^ts)
	return []byte(fmt.Sprintf("%s%016x:%s", keyTime, binary.BigEndian.Uint64(buf), strings.ToLower(strings.TrimSpace(sourcePinId))))
}

func authorIndexKey(identity, sourcePinId string) []byte {
	return []byte(keyAuthor + strings.ToLower(strings.TrimSpace(identity)) + "\x00" + strings.ToLower(strings.TrimSpace(sourcePinId)))
}

func authorIndexPrefix(identity string) []byte {
	return []byte(keyAuthor + strings.ToLower(strings.TrimSpace(identity)) + "\x00")
}

func tokenIndexKey(assetKey, sourcePinId string) []byte {
	return []byte(keyToken + normalizeAssetKey(assetKey) + "\x00" + strings.ToLower(strings.TrimSpace(sourcePinId)))
}

func tokenIndexPrefix(assetKey string) []byte {
	return []byte(keyToken + normalizeAssetKey(assetKey) + "\x00")
}

func vipIndexKey(collectionPinId, sourcePinId string) []byte {
	return []byte(keyVip + strings.ToLower(strings.TrimSpace(collectionPinId)) + "\x00" + strings.ToLower(strings.TrimSpace(sourcePinId)))
}

func vipIndexPrefix(collectionPinId string) []byte {
	return []byte(keyVip + strings.ToLower(strings.TrimSpace(collectionPinId)) + "\x00")
}

func (a *Aggregator) ingestPin(pin *aggregator.PinInscription, isMempool bool) error {
	if a == nil || a.store == nil || pin == nil || strings.TrimSpace(pin.Id) == "" {
		return nil
	}
	op := strings.ToLower(strings.TrimSpace(pin.Operation))
	basePath := normalisePinPath(pin.Path)

	switch op {
	case OperationCreate:
		if basePath != PathPayBuzz {
			return nil
		}
		return a.ingestCreate(pin, isMempool)
	case OperationModify:
		if basePath != PathPayBuzz && !hasTargetReference(pin) {
			return nil
		}
		return a.ingestModify(pin, isMempool)
	default:
		return nil
	}
}

func (a *Aggregator) ingestCreate(pin *aggregator.PinInscription, isMempool bool) error {
	pinId := strings.ToLower(strings.TrimSpace(pin.Id))
	parsed, err := parsePaybuzzPayload(pinPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: paybuzz create pin %s skipped: %v", pinId, err)
		return nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	existing, err := a.loadRecordLocked(pinId)
	if err != nil {
		return err
	}

	creatorGID := canonicalPinGlobalMetaId(pin)
	creatorMetaId := firstNonEmpty(pin.MetaId, pin.CreateMetaId)
	creatorAddr := firstNonEmpty(pin.Address, pin.CreateAddress)

	chainTs := normalizeUnixSec(pin.Timestamp)
	createdAt := parsed.CreatedAt
	if createdAt <= 0 {
		createdAt = chainTs
	}
	if createdAt <= 0 {
		createdAt = time.Now().Unix()
	}
	updatedAt := chainTs
	if updatedAt <= 0 {
		updatedAt = createdAt
	}

	authorMetaId := parsed.AuthorMetaId
	if authorMetaId == "" {
		authorMetaId = firstNonEmpty(creatorGID, creatorMetaId)
	}
	authorMvcAddr := parsed.AuthorMvcAddress
	if authorMvcAddr == "" {
		authorMvcAddr = creatorAddr
	}

	if existing != nil {
		// If a mempool create is now confirming on-chain, promote confirmation
		// while preserving any newer valid modify already folded on top of it.
		if !isMempool {
			updated := *existing
			if updated.CurrentPinId == pinId {
				updated.Confirmed = true
				updated.Height = pin.GenesisHeight
			}
			if updated.CreatorGlobalMetaId == "" {
				updated.CreatorGlobalMetaId = creatorGID
			}
			if updated.CreatorMetaId == "" {
				updated.CreatorMetaId = creatorMetaId
			}
			if updated.CreatorAddress == "" {
				updated.CreatorAddress = creatorAddr
			}
			return a.saveRecordLocked(&updated, existing, pinId)
		}
		return nil
	}

	rec := &Record{
		SourcePinId:         pinId,
		CurrentPinId:        pinId,
		ChainName:           firstNonEmpty(strings.ToLower(strings.TrimSpace(pin.ChainName)), "mvc"),
		Title:               parsed.Title,
		Preview:             parsed.Preview,
		ContentType:         parsed.ContentType,
		WordCount:           parsed.WordCount,
		Encryption:          parsed.Encryption,
		AuthorMetaId:        authorMetaId,
		AuthorMvcAddress:    authorMvcAddr,
		ShopMetaAppPinId:    parsed.ShopMetaAppPinId,
		UnlockRules:         parsed.UnlockRules,
		CreatedAt:           createdAt,
		UpdatedAt:           updatedAt,
		Height:              pin.GenesisHeight,
		Confirmed:           !isMempool,
		CreatorGlobalMetaId: creatorGID,
		CreatorMetaId:       creatorMetaId,
		CreatorAddress:      creatorAddr,
	}
	return a.saveRecordLocked(rec, nil, pinId)
}

func (a *Aggregator) ingestModify(pin *aggregator.PinInscription, isMempool bool) error {
	targetPinId := extractTargetPinID(pin)
	if targetPinId == "" {
		return nil
	}
	modifyPinId := strings.ToLower(strings.TrimSpace(pin.Id))

	a.mu.Lock()
	defer a.mu.Unlock()

	previous, err := a.loadRecordByAnyPinIdLocked(targetPinId)
	if err != nil || previous == nil {
		return err
	}

	// Enforce owner-only modify rule: modifier identity must match creator identity.
	if !a.modifierMatchesCreatorLocked(previous, pin) {
		log.Printf("WARNING: paybuzz modify pin %s rejected: modifier does not match creator of %s", modifyPinId, previous.SourcePinId)
		return nil
	}

	parsed, err := parsePaybuzzPayload(pinPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: paybuzz modify pin %s skipped (invalid payload): %v", modifyPinId, err)
		return nil
	}

	newTs := normalizeUnixSec(pin.Timestamp)
	if newTs <= 0 {
		newTs = parsed.CreatedAt
	}
	if newTs <= 0 {
		newTs = previous.UpdatedAt
	}

	if previous.CurrentPinId == modifyPinId {
		if !isMempool && !previous.Confirmed {
			updated := *previous
			updated.Confirmed = true
			updated.Height = pin.GenesisHeight
			return a.saveRecordLocked(&updated, previous, modifyPinId)
		}
		return nil
	}

	// Ignore stale confirmed modifies older than an already-confirmed newer version.
	if !isMempool && previous.Confirmed && previous.CurrentPinId != previous.SourcePinId {
		if !modifyVersionNewer(previous.Height, previous.UpdatedAt, previous.CurrentPinId, pin.GenesisHeight, newTs, modifyPinId) {
			return nil
		}
	}

	authorMetaId := parsed.AuthorMetaId
	if authorMetaId == "" {
		authorMetaId = previous.AuthorMetaId
	}
	authorMvcAddr := parsed.AuthorMvcAddress
	if authorMvcAddr == "" {
		authorMvcAddr = previous.AuthorMvcAddress
	}
	shopPinId := parsed.ShopMetaAppPinId
	if shopPinId == "" {
		shopPinId = previous.ShopMetaAppPinId
	}

	updated := &Record{
		SourcePinId:         previous.SourcePinId,
		CurrentPinId:        modifyPinId,
		ChainName:           previous.ChainName,
		Title:               parsed.Title,
		Preview:             parsed.Preview,
		ContentType:         parsed.ContentType,
		WordCount:           parsed.WordCount,
		Encryption:          parsed.Encryption,
		AuthorMetaId:        authorMetaId,
		AuthorMvcAddress:    authorMvcAddr,
		ShopMetaAppPinId:    shopPinId,
		UnlockRules:         parsed.UnlockRules,
		CreatedAt:           previous.CreatedAt,
		UpdatedAt:           newTs,
		Height:              pin.GenesisHeight,
		Confirmed:           !isMempool,
		CreatorGlobalMetaId: previous.CreatorGlobalMetaId,
		CreatorMetaId:       previous.CreatorMetaId,
		CreatorAddress:      previous.CreatorAddress,
	}
	return a.saveRecordLocked(updated, previous, modifyPinId)
}

func (a *Aggregator) modifierMatchesCreatorLocked(source *Record, modifyPin *aggregator.PinInscription) bool {
	if source == nil || modifyPin == nil {
		return false
	}
	modGID := canonicalPinGlobalMetaId(modifyPin)
	modMetaId := firstNonEmpty(modifyPin.MetaId, modifyPin.CreateMetaId)
	modAddr := firstNonEmpty(modifyPin.Address, modifyPin.CreateAddress)

	// Cascade matching aligned with publishedcontent §2:
	// 1. Both carry GlobalMetaId -> must match.
	if source.CreatorGlobalMetaId != "" && modGID != "" {
		return strings.EqualFold(source.CreatorGlobalMetaId, modGID)
	}
	// 2. Both carry MetaId -> must match.
	if source.CreatorMetaId != "" && modMetaId != "" {
		return strings.EqualFold(source.CreatorMetaId, modMetaId)
	}
	// 3. Address match.
	if source.CreatorAddress != "" && modAddr != "" {
		return source.CreatorAddress == modAddr
	}
	// 4. Fallback cross-identity check via userinfo if wired.
	if a.profileLookup != nil {
		srcSnap := a.lookupProfileLocked(source.CreatorGlobalMetaId, source.CreatorMetaId, source.CreatorAddress)
		modSnap := a.lookupProfileLocked(modGID, modMetaId, modAddr)
		if srcSnap != nil && modSnap != nil && srcSnap.GlobalMetaId != "" && strings.EqualFold(srcSnap.GlobalMetaId, modSnap.GlobalMetaId) {
			return true
		}
	}
	return false
}

func (a *Aggregator) saveRecordLocked(rec, prev *Record, mappedPinId string) error {
	raw, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if prev != nil {
		a.deleteSecondaryIndexesLocked(prev)
	}
	if err := a.store.Set(Namespace, recKey(rec.SourcePinId), raw); err != nil {
		return err
	}
	if err := a.store.Set(Namespace, pinSrcKey(rec.SourcePinId), []byte(rec.SourcePinId)); err != nil {
		return err
	}
	if mappedPinId != "" && !strings.EqualFold(mappedPinId, rec.SourcePinId) {
		if err := a.store.Set(Namespace, pinSrcKey(mappedPinId), []byte(rec.SourcePinId)); err != nil {
			return err
		}
	}
	if err := a.store.Set(Namespace, timeKey(rec.CreatedAt, rec.SourcePinId), []byte("1")); err != nil {
		return err
	}
	a.writeSecondaryIndexesLocked(rec)
	a.invalidateCachesLocked(rec, prev, mappedPinId)
	return nil
}

func (a *Aggregator) writeSecondaryIndexesLocked(rec *Record) {
	for _, id := range authorIdentityKeys(rec) {
		_ = a.store.Set(Namespace, authorIndexKey(id, rec.SourcePinId), []byte("1"))
	}
	for _, tok := range rec.UnlockRules.TokenPrices {
		if k := normalizeAssetKey(tok.AssetKey); k != "" {
			_ = a.store.Set(Namespace, tokenIndexKey(k, rec.SourcePinId), []byte("1"))
		}
	}
	for _, col := range rec.UnlockRules.VipMrc721Collections {
		if cid := strings.ToLower(strings.TrimSpace(col.CollectionPinId)); cid != "" {
			_ = a.store.Set(Namespace, vipIndexKey(cid, rec.SourcePinId), []byte("1"))
		}
	}
}

func (a *Aggregator) deleteSecondaryIndexesLocked(rec *Record) {
	if rec == nil {
		return
	}
	_ = a.store.Delete(Namespace, timeKey(rec.CreatedAt, rec.SourcePinId))
	for _, id := range authorIdentityKeys(rec) {
		_ = a.store.Delete(Namespace, authorIndexKey(id, rec.SourcePinId))
	}
	for _, tok := range rec.UnlockRules.TokenPrices {
		if k := normalizeAssetKey(tok.AssetKey); k != "" {
			_ = a.store.Delete(Namespace, tokenIndexKey(k, rec.SourcePinId))
		}
	}
	for _, col := range rec.UnlockRules.VipMrc721Collections {
		if cid := strings.ToLower(strings.TrimSpace(col.CollectionPinId)); cid != "" {
			_ = a.store.Delete(Namespace, vipIndexKey(cid, rec.SourcePinId))
		}
	}
}

func (a *Aggregator) invalidateCachesLocked(rec, prev *Record, mappedPinId string) {
	if a.infoCache != nil {
		if rec != nil {
			_ = a.infoCache.Delete(rec.SourcePinId)
			_ = a.infoCache.Delete(rec.CurrentPinId)
		}
		if prev != nil && prev.CurrentPinId != "" {
			_ = a.infoCache.Delete(prev.CurrentPinId)
		}
		if mappedPinId != "" {
			_ = a.infoCache.Delete(mappedPinId)
		}
	}
	a.searchMu.Lock()
	a.searchDirty = true
	a.searchMu.Unlock()
}

func (a *Aggregator) loadRecordLocked(sourcePinId string) (*Record, error) {
	sourcePinId = strings.ToLower(strings.TrimSpace(sourcePinId))
	if sourcePinId == "" || a.store == nil {
		return nil, nil
	}
	raw, err := a.store.Get(Namespace, recKey(sourcePinId))
	if err != nil || len(raw) == 0 {
		return nil, nil
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (a *Aggregator) loadRecordByAnyPinIdLocked(pinId string) (*Record, error) {
	pinId = strings.ToLower(strings.TrimSpace(pinId))
	if pinId == "" || a.store == nil {
		return nil, nil
	}
	if rec, err := a.loadRecordLocked(pinId); err != nil || rec != nil {
		return rec, err
	}
	srcBytes, err := a.store.Get(Namespace, pinSrcKey(pinId))
	if err != nil || len(srcBytes) == 0 {
		return nil, nil
	}
	return a.loadRecordLocked(string(srcBytes))
}

// LookupByPinId resolves a folded Record by its sourcePinId, currentPinId, or
// any historical modify pinId, utilizing the L1/L2 cache when configured.
func (a *Aggregator) LookupByPinId(pinId string) (*Record, error) {
	pinId = strings.ToLower(strings.TrimSpace(pinId))
	if pinId == "" || a == nil || a.store == nil {
		return nil, nil
	}
	if a.infoCache != nil {
		if cachedBytes, ok := a.infoCache.Get(pinId); ok && len(cachedBytes) > 0 {
			var rec Record
			if err := json.Unmarshal(cachedBytes, &rec); err == nil {
				return &rec, nil
			}
		}
	}
	a.mu.RLock()
	rec, err := a.loadRecordByAnyPinIdLocked(pinId)
	a.mu.RUnlock()
	if err != nil || rec == nil {
		return nil, err
	}
	if a.infoCache != nil {
		if raw, mErr := json.Marshal(rec); mErr == nil {
			_ = a.infoCache.Set(pinId, raw, infoCacheTTL)
		}
	}
	return rec, nil
}

func (a *Aggregator) listAllRecordsNewestFirst() ([]*Record, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make([]*Record, 0)
	seen := make(map[string]struct{})
	err := a.store.ScanPrefix(Namespace, []byte(keyTime), func(k, _ []byte) error {
		rest := strings.TrimPrefix(string(k), keyTime)
		idx := strings.Index(rest, ":")
		if idx < 0 || idx+1 >= len(rest) {
			return nil
		}
		srcPinId := rest[idx+1:]
		if _, dup := seen[srcPinId]; dup {
			return nil
		}
		rec, err := a.loadRecordLocked(srcPinId)
		if err != nil || rec == nil {
			return nil
		}
		seen[srcPinId] = struct{}{}
		out = append(out, rec)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (a *Aggregator) scanSourceSetByPrefix(prefix []byte) (map[string]struct{}, error) {
	if a == nil || a.store == nil || len(prefix) == 0 {
		return nil, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	out := make(map[string]struct{})
	prefixStr := string(prefix)
	err := a.store.ScanPrefix(Namespace, prefix, func(k, _ []byte) error {
		srcPinId := strings.TrimPrefix(string(k), prefixStr)
		if srcPinId != "" {
			out[srcPinId] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func authorIdentityKeys(rec *Record) []string {
	if rec == nil {
		return nil
	}
	candidates := []string{
		rec.AuthorMetaId,
		rec.AuthorMvcAddress,
		rec.CreatorGlobalMetaId,
		rec.CreatorMetaId,
		rec.CreatorAddress,
	}
	seen := make(map[string]struct{}, len(candidates))
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		lower := strings.ToLower(strings.TrimSpace(c))
		if lower == "" {
			continue
		}
		if _, dup := seen[lower]; dup {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, lower)
	}
	return out
}

func canonicalPinGlobalMetaId(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	gid := strings.TrimSpace(pin.GlobalMetaId)
	addr := firstNonEmpty(pin.Address, pin.CreateAddress)
	if addr == "" {
		return gid
	}
	if gid != "" && !strings.EqualFold(gid, addr) {
		return gid
	}
	if encoded := idaddress.EncodeGlobalMetaId(addr, pin.ChainName); encoded != "" {
		return encoded
	}
	return gid
}

func normalisePinPath(path string) string {
	base := strings.TrimRight(strings.TrimSpace(path), "/")
	if at := strings.Index(base, "@"); at > 0 {
		base = base[:at]
	}
	if idx := strings.LastIndex(base, ":/"); idx >= 0 {
		if cand := base[idx+1:]; strings.HasPrefix(strings.ToLower(cand), "/protocols/") {
			base = cand
		}
	}
	return strings.ToLower(base)
}

func hasTargetReference(pin *aggregator.PinInscription) bool {
	return extractTargetPinID(pin) != ""
}

func extractTargetPinID(pin *aggregator.PinInscription) string {
	if pin == nil {
		return ""
	}
	if at := strings.Index(pin.Path, "@"); at >= 0 {
		cand := strings.Trim(strings.TrimSpace(pin.Path[at+1:]), "/")
		if cand != "" && !strings.EqualFold(cand, pin.Id) {
			return strings.ToLower(cand)
		}
	}
	cand := strings.TrimPrefix(strings.TrimSpace(pin.OriginalId), "@")
	if cand != "" && !strings.EqualFold(cand, pin.Id) {
		return strings.ToLower(cand)
	}
	return ""
}

func modifyVersionNewer(curHeight, curTime int64, curPinId string, newHeight, newTime int64, newPinId string) bool {
	if newHeight != curHeight && newHeight > 0 && curHeight > 0 {
		return newHeight > curHeight
	}
	if newTime != curTime {
		return newTime > curTime
	}
	return newPinId > curPinId
}

func pinPayloadBytes(pin *aggregator.PinInscription) []byte {
	if pin == nil {
		return nil
	}
	if len(pin.ContentBody) > 0 {
		return pin.ContentBody
	}
	return []byte(pin.ContentSummary)
}

func normalizeUnixSec(ts int64) int64 {
	if ts > 100000000000 {
		return ts / 1000
	}
	return ts
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
