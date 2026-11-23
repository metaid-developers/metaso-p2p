package ftmarket

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

const (
	// Canonical mainnet official FT asset keys and identifiers.
	AssetKeyMetaidBtc   = "mrc20:8ce590918a3d493631ab9d9e3bbc89e322f3dd08354af87f97c07218818b78f4i0"
	AssetKeyMetaidMvc   = "mvc-ft:c9cc7bbd1010b44873959a8b1a2bcedeb62302b7:28a481ecdae9499a599a9457ae70b5cf234ef417"
	SensibleIdMetaidMvc = "f1c52275148e6d4516707ffeec88794c1d905283ec3fe3d3090551afb61b4b0100000000"
	IconMetaid          = "metafile://ae28eedfc956e742f1f05aebbe0118916f2848fafa0e86e2f5d20781d628415bi0"

	AssetKeyMcBtc   = "mrc20:644dba0433aced0ec4cecef9baa951eccabb1751f222d48d33e7a309738ff0d2i0"
	AssetKeyMcMvc   = "mvc-ft:a2421f1e90c6048c36745edd44fad682e8644693:07e4c5a9f866164108de005be81d40ccbd2e964c"
	SensibleIdMcMvc = "85bd1a92ef16ab7de52933495b5ecd202f5d78371b8fecc3832027f17281698f00000000"
	IconMc          = "metafile://1763f42c089e0b81637d5843676dfe4272e6f5a46be8a96306ea1b696ed1e48fi0"

	AssetKeyIdcoinBtc = "mrc20:b87d217618b6089cc1bcf97f77435fab3a90eaf9995a5179e13ec07c7a6783dci0"
)

var hex72Pattern = regexp.MustCompile(`^[0-9a-fA-F]{72}$`)

var (
	mvcSensibleMu       sync.RWMutex
	sensibleToGenesis   = make(map[string]string) // "<codehash>:<72-hex>" -> "<40-hex>"
	sensibleTxToGenesis = make(map[string]string) // "<codehash>:<64-hex>" -> "<40-hex>"
	genesisToSensible   = make(map[string]string) // "<codehash>:<40-hex>" -> "<72-hex>"
)

func init() {
	for _, info := range DefaultMainnetCertifiedTokens() {
		registerCertSensibleMapping(info)
	}
}

// DefaultMainnetCertifiedTokens returns the canonical built-in official FT
// certification seeds for mainnet ($METAID BTC/MVC, $MC BTC/MVC, $IDCOIN BTC),
// including Octopus official dual-chain bridge peers and MVC sensibleId mappings.
func DefaultMainnetCertifiedTokens() map[string]CertifiedTokenInfo {
	return map[string]CertifiedTokenInfo{
		AssetKeyMetaidBtc: {
			AssetKey:            AssetKeyMetaidBtc,
			DisplayName:         "MetaID",
			Symbol:              "METAID",
			TokenName:           "MetaID",
			Decimals:            8,
			Icon:                IconMetaid,
			BridgedPeerAssetKey: AssetKeyMetaidMvc,
			BridgedDualChain:    true,
		},
		AssetKeyMetaidMvc: {
			AssetKey:            AssetKeyMetaidMvc,
			DisplayName:         "MetaID",
			Symbol:              "METAID",
			TokenName:           "MetaID",
			Decimals:            8,
			Icon:                IconMetaid,
			SensibleId:          SensibleIdMetaidMvc,
			BridgedPeerAssetKey: AssetKeyMetaidBtc,
			BridgedDualChain:    true,
		},
		AssetKeyMcBtc: {
			AssetKey:            AssetKeyMcBtc,
			DisplayName:         "MetaCoin",
			Symbol:              "MC",
			TokenName:           "MetaCoin",
			Decimals:            8,
			Icon:                IconMc,
			BridgedPeerAssetKey: AssetKeyMcMvc,
			BridgedDualChain:    true,
		},
		AssetKeyMcMvc: {
			AssetKey:            AssetKeyMcMvc,
			DisplayName:         "MetaCoin",
			Symbol:              "MC",
			TokenName:           "MetaCoin",
			Decimals:            8,
			Icon:                IconMc,
			SensibleId:          SensibleIdMcMvc,
			BridgedPeerAssetKey: AssetKeyMcBtc,
			BridgedDualChain:    true,
		},
		AssetKeyIdcoinBtc: {
			AssetKey:    AssetKeyIdcoinBtc,
			DisplayName: "IDCOIN",
			Symbol:      "IDCOIN",
			TokenName:   "IDCOIN",
			Decimals:    8,
		},
	}
}

// RegisterMvcSensibleMapping records a canonical (codehash, 40-hex genesis, 64/72-hex sensibleId)
// mapping so MVC FT pins or queries using sensibleId normalize to the 40-hex genesis assetKey.
func RegisterMvcSensibleMapping(codehash, genesis40, sensibleId string) {
	ch := strings.ToLower(strings.TrimSpace(codehash))
	gen := strings.ToLower(strings.TrimSpace(genesis40))
	sid := strings.ToLower(strings.TrimSpace(sensibleId))
	if !hex40Pattern.MatchString(ch) || !hex40Pattern.MatchString(gen) || sid == "" {
		return
	}
	if !hex72Pattern.MatchString(sid) && !hex64Pattern.MatchString(sid) {
		return
	}
	mvcSensibleMu.Lock()
	defer mvcSensibleMu.Unlock()
	if hex72Pattern.MatchString(sid) {
		sensibleToGenesis[ch+":"+sid] = gen
		sensibleTxToGenesis[ch+":"+sid[:64]] = gen
		genesisToSensible[ch+":"+gen] = sid
	} else {
		sensibleTxToGenesis[ch+":"+sid] = gen
		if _, exists := genesisToSensible[ch+":"+gen]; !exists {
			genesisToSensible[ch+":"+gen] = sid
		}
	}
}

func registerCertSensibleMapping(info CertifiedTokenInfo) {
	key := strings.ToLower(strings.TrimSpace(info.AssetKey))
	if !strings.HasPrefix(key, "mvc-ft:") || strings.TrimSpace(info.SensibleId) == "" {
		return
	}
	parts := strings.Split(strings.TrimPrefix(key, "mvc-ft:"), ":")
	if len(parts) != 2 {
		return
	}
	RegisterMvcSensibleMapping(parts[0], parts[1], info.SensibleId)
}

// ResolveMvcGenesisAndSensibleId resolves a raw MVC genesis-or-sensibleId hex string into
// (canonical40HexGenesisOrOriginal, sensibleIdOrEmpty).
func ResolveMvcGenesisAndSensibleId(codehash, genesisOrSensible string) (string, string) {
	ch := strings.ToLower(strings.TrimSpace(codehash))
	raw := strings.ToLower(strings.TrimSpace(genesisOrSensible))
	if ch == "" || raw == "" {
		return raw, ""
	}
	mvcSensibleMu.RLock()
	defer mvcSensibleMu.RUnlock()

	if hex40Pattern.MatchString(raw) {
		return raw, genesisToSensible[ch+":"+raw]
	}
	if hex72Pattern.MatchString(raw) {
		if gen, ok := sensibleToGenesis[ch+":"+raw]; ok {
			return gen, raw
		}
		if gen, ok := sensibleTxToGenesis[ch+":"+raw[:64]]; ok {
			return gen, raw
		}
		return raw, raw
	}
	if hex64Pattern.MatchString(raw) {
		if gen, ok := sensibleTxToGenesis[ch+":"+raw]; ok {
			sid := genesisToSensible[ch+":"+gen]
			if sid == "" {
				sid = raw
			}
			return gen, sid
		}
		if gen, ok := sensibleToGenesis[ch+":"+raw+"00000000"]; ok {
			return gen, raw + "00000000"
		}
		return raw, raw
	}
	return raw, ""
}

// NormalizeAssetKey normalizes an assetKey, rewriting mvc-ft:<codehash>:<64/72-hex-sensibleId>
// into mvc-ft:<codehash>:<40-hex-genesis> whenever a mapping is known.
func NormalizeAssetKey(assetKey string) string {
	key := strings.ToLower(strings.TrimSpace(assetKey))
	if strings.HasPrefix(key, "mrc20:") {
		return "mrc20:" + normalizeMrc20Id(strings.TrimPrefix(key, "mrc20:"))
	}
	if strings.HasPrefix(key, "mvc-ft:") {
		parts := strings.Split(strings.TrimPrefix(key, "mvc-ft:"), ":")
		if len(parts) == 2 && hex40Pattern.MatchString(parts[0]) {
			gen, _ := ResolveMvcGenesisAndSensibleId(parts[0], parts[1])
			return "mvc-ft:" + parts[0] + ":" + gen
		}
	}
	return key
}

// normalizeListingKeyAssetPrefix normalizes an mvc-ft:<codehash>:<sensibleId>:<rest> listingKey
// prefix to its canonical 40-hex genesis prefix even when asset/lot are omitted (e.g. cancel pins).
func normalizeListingKeyAssetPrefix(listingKey string) string {
	lower := strings.ToLower(strings.TrimSpace(listingKey))
	if !strings.HasPrefix(lower, "mvc-ft:") {
		return lower
	}
	rest := strings.TrimPrefix(lower, "mvc-ft:")
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) < 2 || !hex40Pattern.MatchString(parts[0]) {
		return lower
	}
	gen, _ := ResolveMvcGenesisAndSensibleId(parts[0], parts[1])
	if len(parts) == 2 {
		return "mvc-ft:" + parts[0] + ":" + gen
	}
	return "mvc-ft:" + parts[0] + ":" + gen + ":" + parts[2]
}

// normalizeAssetInfoInPlace canonicalizes MVC genesis/sensibleId/assetKey in-place when known.
func normalizeAssetInfoInPlace(asset *AssetInfo) bool {
	if asset == nil {
		return false
	}
	changed := false
	if asset.Chain == "mvc" && asset.Codehash != nil && asset.Genesis != nil {
		ch := strings.ToLower(strings.TrimSpace(*asset.Codehash))
		rawGen := strings.ToLower(strings.TrimSpace(*asset.Genesis))
		canonGen, sid := ResolveMvcGenesisAndSensibleId(ch, rawGen)
		if sid == "" && asset.SensibleId != nil {
			cand := strings.ToLower(strings.TrimSpace(*asset.SensibleId))
			if hex72Pattern.MatchString(cand) || hex64Pattern.MatchString(cand) {
				sid = cand
				if hex40Pattern.MatchString(canonGen) {
					RegisterMvcSensibleMapping(ch, canonGen, sid)
				}
			}
		}
		if canonGen != rawGen {
			g := canonGen
			asset.Genesis = &g
			changed = true
		}
		canonKey := "mvc-ft:" + ch + ":" + canonGen
		if asset.AssetKey != canonKey {
			asset.AssetKey = canonKey
			changed = true
		}
		if sid != "" && (asset.SensibleId == nil || *asset.SensibleId != sid) {
			s := sid
			asset.SensibleId = &s
			changed = true
		}
	}
	return changed
}

// MvcSensibleResolver is optionally implemented by TokenMetadataProvider to dynamically resolve
// arbitrary MVC FT 64/72-hex sensibleIds to 40-hex genesis hashes via an upstream MVC indexer.
type MvcSensibleResolver interface {
	ResolveMvcSensibleId(ctx context.Context, codehash, sensibleId string) (genesis40 string, ok bool)
}

func (a *Aggregator) resolveMvcSensibleIfNeededLocked(ctx context.Context, codehash, rawGen string) (string, string) {
	canonGen, sid := ResolveMvcGenesisAndSensibleId(codehash, rawGen)
	if hex40Pattern.MatchString(canonGen) {
		return canonGen, sid
	}
	if !hex72Pattern.MatchString(rawGen) && !hex64Pattern.MatchString(rawGen) {
		return canonGen, sid
	}
	if resolver, ok := a.metaProvider.(MvcSensibleResolver); ok && resolver != nil {
		if gen40, found := resolver.ResolveMvcSensibleId(ctx, codehash, rawGen); found && hex40Pattern.MatchString(gen40) {
			RegisterMvcSensibleMapping(codehash, gen40, rawGen)
			return strings.ToLower(gen40), strings.ToLower(rawGen)
		}
	}
	return canonGen, sid
}

func (a *Aggregator) normalizeQueryAssetKey(ctx context.Context, rawKey string) string {
	norm := NormalizeAssetKey(rawKey)
	if !strings.HasPrefix(norm, "mvc-ft:") {
		return norm
	}
	parts := strings.Split(strings.TrimPrefix(norm, "mvc-ft:"), ":")
	if len(parts) != 2 || !hex40Pattern.MatchString(parts[0]) || hex40Pattern.MatchString(parts[1]) {
		return norm
	}
	a.mu.Lock()
	prov := a.metaProvider
	a.mu.Unlock()
	if resolver, ok := prov.(MvcSensibleResolver); ok && resolver != nil {
		if gen40, found := resolver.ResolveMvcSensibleId(ctx, parts[0], parts[1]); found && hex40Pattern.MatchString(gen40) {
			RegisterMvcSensibleMapping(parts[0], gen40, parts[1])
			return "mvc-ft:" + parts[0] + ":" + strings.ToLower(gen40)
		}
	}
	return norm
}

// migrateLegacyMvcSensibleKeysLocked rewrites any pre-existing persisted PebbleDB catalog,
// listing, or trade entries keyed by 64/72-hex MVC sensibleId into their canonical 40-hex
// genesis keys so historical split buckets disappear cleanly on startup.
// Callers must hold a.mu.
func (a *Aggregator) migrateLegacyMvcSensibleKeysLocked() {
	if a == nil || a.store == nil {
		return
	}

	// 1. Migrate asset:<legacyKey> catalog records.
	type legacyCatalog struct {
		oldKey string
		rec    TokenCatalogRecord
	}
	var catMoves []legacyCatalog
	_ = a.store.ScanPrefix(Namespace, []byte(keyAsset), func(k, val []byte) error {
		var rec TokenCatalogRecord
		if err := json.Unmarshal(val, &rec); err != nil || rec.Asset.AssetKey == "" {
			return nil
		}
		oldKey := strings.TrimPrefix(string(k), keyAsset)
		if normalizeAssetInfoInPlace(&rec.Asset) || oldKey != rec.Asset.AssetKey {
			catMoves = append(catMoves, legacyCatalog{oldKey: oldKey, rec: rec})
		}
		return nil
	})
	for _, m := range catMoves {
		a.saveTokenCatalogLocked(&m.rec.Asset, m.rec.UpdatedAt)
		if m.oldKey != m.rec.Asset.AssetKey {
			_ = a.store.Delete(Namespace, assetStorageKey(m.oldKey))
		}
	}

	// 2. Migrate listing:<legacyListingKey> records.
	var legacyListings []*SellListingRecord
	_ = a.store.ScanPrefix(Namespace, []byte(keyListing), func(_, val []byte) error {
		var rec SellListingRecord
		if err := json.Unmarshal(val, &rec); err != nil {
			return nil
		}
		assetChanged := false
		if rec.Asset != nil {
			cpAsset := *rec.Asset
			if normalizeAssetInfoInPlace(&cpAsset) {
				assetChanged = true
			}
		}
		newKey := normalizeListingKeyAssetPrefix(rec.ListingKey)
		if rec.Asset != nil && rec.Lot != nil {
			cpAsset := *rec.Asset
			normalizeAssetInfoInPlace(&cpAsset)
			newKey = canonicalizeListingKey(newKey, cpAsset.AssetKey, rec.Lot.UtxoOutpoint)
		}
		if assetChanged || newKey != rec.ListingKey {
			legacyListings = append(legacyListings, &rec)
		}
		return nil
	})
	for _, oldRec := range legacyListings {
		oldKey := oldRec.ListingKey
		cp := *oldRec
		if oldRec.Asset != nil {
			cpAsset := *oldRec.Asset
			normalizeAssetInfoInPlace(&cpAsset)
			cp.Asset = &cpAsset
		}
		newKey := normalizeListingKeyAssetPrefix(cp.ListingKey)
		if cp.Asset != nil && cp.Lot != nil {
			newKey = canonicalizeListingKey(newKey, cp.Asset.AssetKey, cp.Lot.UtxoOutpoint)
		}
		cp.ListingKey = newKey

		if oldKey != newKey {
			if oldRec.Seq > 0 {
				_ = a.store.Delete(Namespace, listingTimeKey(oldRec.Seq, oldKey))
			}
			a.deleteSecondaryIndexesLocked(oldRec)
			_ = a.store.Delete(Namespace, listingStorageKey(oldKey))

			existing, _ := a.loadListing(newKey)
			if existing != nil {
				if existing.SellPinId == cp.SellPinId {
					continue
				}
				if !chainOrderNewer(existing.Height, existing.UpdatedAt, existing.SellPinId, cp.Height, cp.UpdatedAt, cp.SellPinId) {
					continue
				}
				_ = a.saveListingLocked(&cp, existing)
				continue
			}
			_ = a.saveListingLocked(&cp, nil)
		} else {
			_ = a.saveListingLocked(&cp, oldRec)
		}
	}

	// 3. Migrate trade:<txId>:<listingKey> records.
	var legacyTrades []*TradeRecord
	_ = a.store.ScanPrefix(Namespace, []byte(keyTrade), func(_, val []byte) error {
		var tr TradeRecord
		if err := json.Unmarshal(val, &tr); err != nil {
			return nil
		}
		if NormalizeAssetKey(tr.AssetKey) != tr.AssetKey || normalizeListingKeyAssetPrefix(tr.ListingKey) != tr.ListingKey {
			legacyTrades = append(legacyTrades, &tr)
		}
		return nil
	})
	for _, oldTr := range legacyTrades {
		oldListingKey := oldTr.ListingKey
		_ = a.store.Delete(Namespace, tradeStorageKey(oldTr.TxId, oldListingKey))
		_ = a.store.Delete(Namespace, tradeTimeKey(oldTr.SettledAt, oldTr.TxId, oldListingKey))

		cp := *oldTr
		cp.AssetKey = NormalizeAssetKey(cp.AssetKey)
		cp.ListingKey = normalizeListingKeyAssetPrefix(cp.ListingKey)
		if existing, _ := a.loadTrade(cp.TxId, cp.ListingKey); existing != nil {
			continue
		}
		if raw, err := json.Marshal(&cp); err == nil {
			_ = a.store.Set(Namespace, tradeStorageKey(cp.TxId, cp.ListingKey), raw)
			_ = a.store.Set(Namespace, tradeTimeKey(cp.SettledAt, cp.TxId, cp.ListingKey), []byte("1"))
		}
	}
}
