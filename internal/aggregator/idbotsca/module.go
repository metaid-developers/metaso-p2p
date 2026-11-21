// Package idbotsca indexes /protocols/idbots-nft-ca (Contract C1 v1.1) and
// serves the official blue-badge registry and lookup surface (Contract C3.1).
package idbotsca

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	// Namespace is the Pebble namespace of the CA registry.
	Namespace = "idbotsca"

	// PathCA is the official IDBots NFT CA protocol path.
	PathCA = "/protocols/idbots-nft-ca"

	// Official trust root ("Bob"). Any pin on PathCA created or modified by
	// a different identity must be silently discarded.
	OfficialMetaID       = "2379692b225136b2b54e83aac52174f622b80181b17254bb7b5cf7b0aa323212"
	OfficialGlobalMetaID = "idq1w8ye5psdkqrn6ugxxwvf5p4kkeuzufa6n9tt47"
	OfficialMVCAddress   = "1BNesCuvJeW2DAF42xkyCU1ifZVuNZ61mv"
	DefaultGenesisPinID  = "77da77280b9b218cf1edd62f658ca2be561dd7e377f3980182e280487ec38c56i0"

	keyRegistryLatest = "registry:latest"

	// staleMempoolTimeoutSec evicts an unconfirmed mempool CA overlay if it
	// stays unconfirmed for more than 30 minutes (P1-2).
	staleMempoolTimeoutSec int64 = 30 * 60
)

// LegacySeriesConfig describes legacy MetaContract NFT swap mapping on a CA entry.
type LegacySeriesConfig struct {
	Codehash                string `json:"codehash,omitempty"`
	Genesis                 string `json:"genesis,omitempty"`
	SwapStationGlobalMetaId string `json:"swapStationGlobalMetaId,omitempty"`
}

// CollectionCertEntry is one certified digital-collectible collection entry.
type CollectionCertEntry struct {
	CollectionPinId string              `json:"collectionPinId"`
	CollectionName  string              `json:"collectionName,omitempty"`
	DisplayTitle    string              `json:"displayTitle,omitempty"`
	LegacySeries    *LegacySeriesConfig `json:"legacySeries,omitempty"`
	Note            string              `json:"note,omitempty"`
	CertifiedAt     int64               `json:"certifiedAt,omitempty"`
}

// SwapSeriesConfig describes one series supported by an official swap station.
type SwapSeriesConfig struct {
	CollectionSlug        string `json:"collectionSlug,omitempty"`
	TargetCollectionPinId string `json:"targetCollectionPinId,omitempty"`
	Codehash              string `json:"codehash,omitempty"`
	Genesis               string `json:"genesis,omitempty"`
	MaxTokenIndex         int64  `json:"maxTokenIndex,omitempty"`
	MinConfirmations      int    `json:"minConfirmations,omitempty"`
}

// SwapConfig configures an official swap_station bot.
type SwapConfig struct {
	VaultAddress    string             `json:"vaultAddress,omitempty"`
	SupportedSeries []SwapSeriesConfig `json:"supportedSeries,omitempty"`
}

// EscrowConfig configures an official escrow_guarantor bot.
type EscrowConfig struct {
	CustodyAddress      string   `json:"custodyAddress,omitempty"`
	SupportedCurrencies []string `json:"supportedCurrencies,omitempty"`
	MaxOrderCnyCents    int64    `json:"maxOrderCnyCents,omitempty"`
}

// OfficialBotEntry is one certified official bot entry (swap_station,
// escrow_guarantor, or official_announcer).
type OfficialBotEntry struct {
	GlobalMetaId string        `json:"globalMetaId"`
	MetaId       string        `json:"metaId,omitempty"`
	MvcAddress   string        `json:"mvcAddress,omitempty"`
	Role         string        `json:"role"`
	BadgeType    string        `json:"badgeType"`
	BadgeLabel   string        `json:"badgeLabel"`
	SwapConfig   *SwapConfig   `json:"swapConfig,omitempty"`
	EscrowConfig *EscrowConfig `json:"escrowConfig,omitempty"`
	Active       bool          `json:"active"`
	CertifiedAt  int64         `json:"certifiedAt,omitempty"`
}

// OfficialBadgeDTO is the projected badge structure injected into userinfo,
// botsearch, and bothomepage responses.
type OfficialBadgeDTO struct {
	Verified   bool   `json:"verified"`
	Role       string `json:"role"`
	BadgeType  string `json:"badgeType"`
	BadgeLabel string `json:"badgeLabel"`
}

// TokenCertEntry is one certified fungible token entry in the CA registry.
type TokenCertEntry struct {
	AssetKey    string `json:"assetKey"`
	Chain       string `json:"chain,omitempty"`
	Standard    string `json:"standard,omitempty"`
	Symbol      string `json:"symbol,omitempty"`
	TokenName   string `json:"tokenName,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
	Decimals    int    `json:"decimals,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Note        string `json:"note,omitempty"`
	CertifiedAt int64  `json:"certifiedAt,omitempty"`
}

// RegistrySnapshot is the latest active CA registry state.
type RegistrySnapshot struct {
	Verified        bool                  `json:"verified"`
	PinId           string                `json:"pinId"`
	OfficialMetaId  string                `json:"officialMetaId"`
	UpdatedAt       int64                 `json:"updatedAt"`
	Height          int64                 `json:"-"`
	Collections     []CollectionCertEntry `json:"collections"`
	OfficialBots    []OfficialBotEntry    `json:"officialBots"`
	CertifiedTokens []TokenCertEntry      `json:"certifiedTokens,omitempty"`
}

type caPayload struct {
	Collections     []CollectionCertEntry `json:"collections"`
	OfficialBots    []OfficialBotEntry    `json:"officialBots"`
	CertifiedTokens []TokenCertEntry      `json:"certifiedTokens,omitempty"`
}

// Aggregator indexes /protocols/idbots-nft-ca and serves CA lookups and HTTP routes.
type Aggregator struct {
	store    *storage.PebbleStore
	notifyCh chan *aggregator.NotifyEvent

	mu            sync.RWMutex
	confirmed     *RegistrySnapshot
	mempool       *RegistrySnapshot
	mempoolSeenAt int64
	effective     *RegistrySnapshot
	colByPinId    map[string]CollectionCertEntry
	botByGlobal   map[string]OfficialBotEntry
	tokenByAsset  map[string]TokenCertEntry
	onChange      func()
	nowFn         func() int64
}

func (a *Aggregator) Name() string { return Namespace }

// SetOnChange registers a callback fired outside locks whenever the effective
// CA registry changes (confirmed ingest, backfill, mempool overlay, or eviction).
func (a *Aggregator) SetOnChange(fn func()) {
	a.mu.Lock()
	a.onChange = fn
	a.mu.Unlock()
}

func (a *Aggregator) nowUnix() int64 {
	if a != nil && a.nowFn != nil {
		return a.nowFn()
	}
	return time.Now().Unix()
}

func (a *Aggregator) Init(store *storage.PebbleStore, _ *cache.CacheProvider) error {
	a.store = store
	a.notifyCh = make(chan *aggregator.NotifyEvent, 8)
	if a.nowFn == nil {
		a.nowFn = func() int64 { return time.Now().Unix() }
	}
	if _, err := store.OpenDB(Namespace); err != nil {
		return err
	}
	snap, err := a.loadConfirmed()
	if err != nil {
		return err
	}
	a.mu.Lock()
	a.confirmed = snap
	a.rebuildEffectiveLocked()
	a.mu.Unlock()
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if !isCAPin(pin) {
		return nil, nil
	}
	if !isTrustedPublisher(pin) {
		return nil, nil
	}
	payload, err := parseCAPayload(pinPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: idbotsca pin %s skipped: %v", strings.TrimSpace(pin.Id), err)
		return nil, nil
	}

	snap := buildSnapshotFromPin(pin, payload)

	a.mu.Lock()
	if a.confirmed != nil {
		if a.confirmed.PinId == snap.PinId {
			if a.confirmed.Height > 0 || snap.Height <= 0 {
				a.mu.Unlock()
				return nil, nil
			}
		} else if !chainOrderNewer(a.confirmed.Height, a.confirmed.UpdatedAt, a.confirmed.PinId, snap.Height, snap.UpdatedAt, snap.PinId) {
			a.mu.Unlock()
			return nil, nil
		}
	}
	if a.store != nil {
		raw, err := json.Marshal(persistedRegistrySnapshot{
			RegistrySnapshot: *snap,
			PersistedHeight:  snap.Height,
		})
		if err != nil {
			a.mu.Unlock()
			return nil, err
		}
		if err := a.store.Set(Namespace, []byte(keyRegistryLatest), raw); err != nil {
			a.mu.Unlock()
			return nil, err
		}
	}
	a.confirmed = snap
	if a.mempool != nil && (a.mempool.PinId == snap.PinId || txIdOfPinId(a.mempool.PinId) == txIdOfPinId(snap.PinId) || snap.UpdatedAt > a.mempool.UpdatedAt) {
		a.mempool = nil
		a.mempoolSeenAt = 0
	}
	a.rebuildEffectiveLocked()
	cb := a.onChange
	a.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil, nil
}

func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if !isCAPin(pin) {
		return nil, nil
	}
	if !isTrustedPublisher(pin) {
		return nil, nil
	}
	payload, err := parseCAPayload(pinPayloadBytes(pin))
	if err != nil {
		log.Printf("WARNING: idbotsca mempool pin %s skipped: %v", strings.TrimSpace(pin.Id), err)
		return nil, nil
	}
	snap := buildSnapshotFromPin(pin, payload)

	a.mu.Lock()
	if a.confirmed != nil && a.confirmed.UpdatedAt > snap.UpdatedAt {
		a.mu.Unlock()
		return nil, nil
	}
	if a.mempool != nil && a.mempool.PinId == snap.PinId {
		a.mu.Unlock()
		return nil, nil
	}
	a.mempool = snap
	a.mempoolSeenAt = a.nowUnix()
	a.rebuildEffectiveLocked()
	cb := a.onChange
	a.mu.Unlock()
	if cb != nil {
		cb()
	}
	return nil, nil
}

func (a *Aggregator) isMempoolStaleLocked(nowSec int64) bool {
	if a.mempool == nil {
		return false
	}
	if a.confirmed != nil && a.confirmed.UpdatedAt > a.mempool.UpdatedAt {
		return true
	}
	if a.mempoolSeenAt > 0 && nowSec-a.mempoolSeenAt > staleMempoolTimeoutSec {
		return true
	}
	return false
}

func (a *Aggregator) cleanupStaleMempoolIfNeeded() {
	if a == nil {
		return
	}
	nowSec := a.nowUnix()
	a.mu.RLock()
	stale := a.isMempoolStaleLocked(nowSec)
	a.mu.RUnlock()
	if !stale {
		return
	}
	var cb func()
	a.mu.Lock()
	if a.isMempoolStaleLocked(nowSec) {
		a.mempool = nil
		a.mempoolSeenAt = 0
		a.rebuildEffectiveLocked()
		cb = a.onChange
	}
	a.mu.Unlock()
	if cb != nil {
		cb()
	}
}

// RegisterRoutes mounts GET /api/mrc721/ca/registry (Contract C3.1).
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/mrc721/ca/registry", a.handleRegistry)
}

func (a *Aggregator) handleRegistry(c *gin.Context) {
	snap := a.Registry()
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "success",
		"data":    snap,
	})
}

// Registry returns a safe copy of the current effective CA snapshot.
func (a *Aggregator) Registry() RegistrySnapshot {
	if a == nil {
		return emptyRegistrySnapshot()
	}
	a.cleanupStaleMempoolIfNeeded()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.effective == nil {
		return emptyRegistrySnapshot()
	}
	cp := *a.effective
	cp.Collections = make([]CollectionCertEntry, len(a.effective.Collections))
	copy(cp.Collections, a.effective.Collections)
	cp.OfficialBots = make([]OfficialBotEntry, len(a.effective.OfficialBots))
	copy(cp.OfficialBots, a.effective.OfficialBots)
	if len(a.effective.CertifiedTokens) > 0 {
		cp.CertifiedTokens = make([]TokenCertEntry, len(a.effective.CertifiedTokens))
		copy(cp.CertifiedTokens, a.effective.CertifiedTokens)
	}
	return cp
}

// HasConfirmedRegistry reports whether a confirmed CA pin is stored.
func (a *Aggregator) HasConfirmedRegistry() bool {
	if a == nil {
		return false
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.confirmed != nil && a.confirmed.Verified
}

// IsCollectionCertified reports whether collectionPinId is certified in the latest registry.
func (a *Aggregator) IsCollectionCertified(collectionPinId string) (bool, *CollectionCertEntry) {
	collectionPinId = strings.TrimSpace(collectionPinId)
	if a == nil || collectionPinId == "" {
		return false, nil
	}
	a.cleanupStaleMempoolIfNeeded()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.colByPinId == nil {
		return false, nil
	}
	entry, ok := a.colByPinId[strings.ToLower(collectionPinId)]
	if !ok {
		return false, nil
	}
	cp := entry
	return true, &cp
}

// IsTokenCertified reports whether assetKey is certified in the latest registry.
func (a *Aggregator) IsTokenCertified(assetKey string) (bool, *TokenCertEntry) {
	assetKey = strings.ToLower(strings.TrimSpace(assetKey))
	if a == nil || assetKey == "" {
		return false, nil
	}
	a.cleanupStaleMempoolIfNeeded()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.tokenByAsset == nil {
		return false, nil
	}
	entry, ok := a.tokenByAsset[assetKey]
	if !ok {
		return false, nil
	}
	cp := entry
	return true, &cp
}

// GetOfficialBotBadge returns the verified official badge for globalMetaId if active and valid-role.
func (a *Aggregator) GetOfficialBotBadge(globalMetaId string) *OfficialBadgeDTO {
	globalMetaId = strings.TrimSpace(globalMetaId)
	if a == nil || globalMetaId == "" {
		return nil
	}
	a.cleanupStaleMempoolIfNeeded()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.botByGlobal == nil {
		return nil
	}
	bot, ok := a.botByGlobal[strings.ToLower(globalMetaId)]
	if !ok || !bot.Active || !isValidOfficialRole(bot.Role) {
		return nil
	}
	return &OfficialBadgeDTO{
		Verified:   true,
		Role:       bot.Role,
		BadgeType:  bot.BadgeType,
		BadgeLabel: bot.BadgeLabel,
	}
}

type persistedRegistrySnapshot struct {
	RegistrySnapshot
	PersistedHeight int64 `json:"height,omitempty"`
}

func (a *Aggregator) loadConfirmed() (*RegistrySnapshot, error) {
	if a.store == nil {
		return nil, nil
	}
	raw, err := a.store.Get(Namespace, []byte(keyRegistryLatest))
	if err != nil || len(raw) == 0 {
		return nil, nil
	}
	var persisted persistedRegistrySnapshot
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return nil, fmt.Errorf("corrupt idbotsca registry: %w", err)
	}
	snap := persisted.RegistrySnapshot
	snap.Height = persisted.PersistedHeight
	if snap.Collections == nil {
		snap.Collections = []CollectionCertEntry{}
	}
	if snap.OfficialBots == nil {
		snap.OfficialBots = []OfficialBotEntry{}
	}
	return &snap, nil
}

func (a *Aggregator) rebuildEffectiveLocked() {
	active := a.confirmed
	if a.mempool != nil {
		active = a.mempool
	}
	a.effective = active
	a.colByPinId = make(map[string]CollectionCertEntry)
	a.botByGlobal = make(map[string]OfficialBotEntry)
	a.tokenByAsset = make(map[string]TokenCertEntry)
	if active == nil {
		return
	}
	for _, col := range active.Collections {
		pid := strings.ToLower(strings.TrimSpace(col.CollectionPinId))
		if pid == "" {
			continue
		}
		a.colByPinId[pid] = col
	}
	for _, bot := range active.OfficialBots {
		gid := strings.ToLower(strings.TrimSpace(bot.GlobalMetaId))
		if gid == "" {
			continue
		}
		a.botByGlobal[gid] = bot
	}
	for _, tok := range active.CertifiedTokens {
		key := strings.ToLower(strings.TrimSpace(tok.AssetKey))
		if key == "" {
			continue
		}
		tok.AssetKey = key
		a.tokenByAsset[key] = tok
	}
}

func emptyRegistrySnapshot() RegistrySnapshot {
	return RegistrySnapshot{
		Verified:       false,
		PinId:          "",
		OfficialMetaId: OfficialMetaID,
		UpdatedAt:      0,
		Collections:    []CollectionCertEntry{},
		OfficialBots:   []OfficialBotEntry{},
	}
}

func isCAPin(pin *aggregator.PinInscription) bool {
	if pin == nil {
		return false
	}
	if normalisedPinPath(pin.Path) != PathCA {
		return false
	}
	op := strings.ToLower(strings.TrimSpace(pin.Operation))
	return op == "create" || op == "modify"
}

func isOfficialBobIdentityValue(raw string) bool {
	v := strings.TrimSpace(raw)
	if v == "" {
		return false
	}
	if strings.EqualFold(v, OfficialMetaID) || strings.EqualFold(v, OfficialGlobalMetaID) {
		return true
	}
	if v == OfficialMVCAddress {
		return true
	}
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:]) == OfficialMetaID
}

func isTrustedPublisher(pin *aggregator.PinInscription) bool {
	if pin == nil {
		return false
	}
	creatorFields := []string{
		pin.CreateMetaId,
		pin.Creator,
		pin.CreateAddress,
		pin.MetaId,
		pin.GlobalMetaId,
	}
	matched := false
	for _, field := range creatorFields {
		if strings.TrimSpace(field) == "" {
			continue
		}
		if !isOfficialBobIdentityValue(field) {
			return false
		}
		matched = true
	}
	if matched {
		return true
	}
	return isOfficialBobIdentityValue(pin.Address)
}

func isValidOfficialRole(role string) bool {
	switch strings.TrimSpace(role) {
	case "swap_station", "escrow_guarantor", "official_announcer":
		return true
	default:
		return false
	}
}

func buildSnapshotFromPin(pin *aggregator.PinInscription, payload *caPayload) *RegistrySnapshot {
	cols := payload.Collections
	if cols == nil {
		cols = []CollectionCertEntry{}
	}
	bots := payload.OfficialBots
	if bots == nil {
		bots = []OfficialBotEntry{}
	}
	return &RegistrySnapshot{
		Verified:        true,
		PinId:           strings.TrimSpace(pin.Id),
		OfficialMetaId:  OfficialMetaID,
		UpdatedAt:       unixSeconds(pin.Timestamp),
		Height:          pin.GenesisHeight,
		Collections:     cols,
		OfficialBots:    bots,
		CertifiedTokens: payload.CertifiedTokens,
	}
}

func parseCAPayload(raw []byte) (*caPayload, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	candidates := [][]byte{trimmed}
	var quoted string
	if err := json.Unmarshal(trimmed, &quoted); err == nil {
		if inner := bytes.TrimSpace([]byte(quoted)); len(inner) > 0 {
			candidates = append(candidates, inner)
		}
	}
	if decoded, err := base64.StdEncoding.DecodeString(string(trimmed)); err == nil {
		candidates = append(candidates, bytes.TrimSpace(decoded))
	} else if decoded, err := base64.RawStdEncoding.DecodeString(string(trimmed)); err == nil {
		candidates = append(candidates, bytes.TrimSpace(decoded))
	}

	for _, candidate := range candidates {
		if len(candidate) == 0 || candidate[0] != '{' {
			continue
		}
		var payload caPayload
		if err := json.Unmarshal(candidate, &payload); err == nil {
			return &payload, nil
		}
	}
	return nil, fmt.Errorf("payload is not a JSON object")
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

func normalisedPinPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	if idx := strings.Index(path, "@"); idx >= 0 {
		path = path[:idx]
	}
	return strings.TrimRight(path, "/")
}

func unixSeconds(ts int64) int64 {
	if ts > 100000000000 {
		return ts / 1000
	}
	return ts
}

func chainOrderNewer(curHeight, curTime int64, curPinId string, newHeight, newTime int64, newPinId string) bool {
	if newHeight < 0 && curHeight >= 0 {
		return false
	}
	if curHeight > 0 && newHeight > 0 && newHeight != curHeight {
		return newHeight > curHeight
	}
	if newTime != curTime {
		return newTime > curTime
	}
	if newHeight != curHeight {
		return newHeight > curHeight
	}
	return newPinId > curPinId
}

func txIdOfPinId(pinId string) string {
	pinId = strings.TrimSpace(pinId)
	if idx := strings.Index(pinId, "i"); idx == 64 {
		return strings.ToLower(pinId[:64])
	}
	return pinId
}
