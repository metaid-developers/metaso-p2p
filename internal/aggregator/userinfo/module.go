package userinfo

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/api"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
	"github.com/metaid-developers/metaso-p2p/pkg/idaddress"
)

// UserProfile is the aggregated user info served to idchat.
//
// JSON tags mirror meta-file-system's MetaIDUserInfo shape so idchat's
// existing `metafileIndexerApi` client (which expects `chatpubkey` /
// `chatpubkeyId` in all-lowercase) can consume metaso-p2p as a drop-in
// replacement without any TypeScript changes.
type UserProfile struct {
	GlobalMetaID    string `json:"globalMetaId"`
	MetaID          string `json:"metaid"`
	Address         string `json:"address"`
	Name            string `json:"name,omitempty"`
	NameId          string `json:"nameId,omitempty"`
	Avatar          string `json:"avatar,omitempty"`
	AvatarId        string `json:"avatarId,omitempty"`
	NftAvatar       string `json:"nftAvatar,omitempty"`
	Bio             string `json:"bio,omitempty"`
	BioId           string `json:"bioId,omitempty"`
	Role            string `json:"role,omitempty"`
	RoleId          string `json:"roleId,omitempty"`
	Soul            string `json:"soul,omitempty"`
	SoulId          string `json:"soulId,omitempty"`
	Goal            string `json:"goal,omitempty"`
	GoalId          string `json:"goalId,omitempty"`
	ChatSkills      string `json:"chatSkills,omitempty"`
	ChatSkillsId    string `json:"chatSkillsId,omitempty"`
	LLM             string `json:"llm,omitempty"`
	LLMId           string `json:"llmId,omitempty"`
	Homepage        string `json:"homepage,omitempty"`
	HomepageId      string `json:"homepageId,omitempty"`
	Background      string `json:"background,omitempty"`
	BackgroundId    string `json:"backgroundId,omitempty"`
	ChatPublicKey   string `json:"chatpubkey,omitempty"`
	ChatPublicKeyId string `json:"chatpubkeyId,omitempty"`
	ChainName       string `json:"chainName,omitempty"`
}

// Aggregator indexes /info/* pins and serves user profile queries.
type Aggregator struct {
	store               *storage.PebbleStore
	cache               *cache.Cache[[]byte]
	notifyCh            chan *aggregator.NotifyEvent
	remoteLookup        remoteProfileLookup
	profileMode         string
	allowRemoteFallback bool
	scanProfiles        func(func(*UserProfile) bool) (*UserProfile, error)
}

const (
	namespace          = "userinfo"
	profilePrefix      = "profile:"
	metaidPrefix       = "metaid:" // metaid → address mapping
	globalMetaIdPrefix = "globalmetaid:"
	addressPrefix      = "address:"
	defaultTTL         = 10 * time.Minute
	cacheMaxEntries    = 5000
)

func (a *Aggregator) Name() string { return "userinfo" }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	a.cache = cacheProvider.Namespace(namespace, cacheMaxEntries, defaultTTL)
	a.notifyCh = make(chan *aggregator.NotifyEvent, 256)
	a.scanProfiles = a.defaultScanProfiles
	a.configureRemoteProfileLookupFromEnv()
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

// HandleBlockPin processes /info/* and / (init) paths.
func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	if pin == nil {
		return nil, nil
	}

	path := pin.Path
	metaid := pin.MetaId
	if metaid == "" {
		metaid = pin.CreateMetaId
	}
	if metaid == "" {
		return nil, nil
	}

	address := pin.Address
	if address == "" {
		address = pin.CreateAddress
	}

	// Load or create profile
	profile, err := a.getProfile(metaid)
	if err != nil || profile == nil {
		profile = &UserProfile{
			MetaID:    metaid,
			ChainName: pin.ChainName,
		}
	}
	if profile.Address == "" && address != "" {
		profile.Address = address
	}
	if profile.GlobalMetaID == "" {
		if globalMetaID := strings.TrimSpace(pin.GlobalMetaId); globalMetaID != "" {
			profile.GlobalMetaID = globalMetaID
		} else if address != "" {
			profile.GlobalMetaID = idaddress.EncodeGlobalMetaId(address, pin.ChainName)
		}
	}
	if profile.ChainName == "" && pin.ChainName != "" {
		profile.ChainName = pin.ChainName
	}

	// / (init) — first-time registration
	if path == "/" {
		if profile.Address == "" && address != "" {
			profile.Address = address
		}
		// Generate GlobalMetaId from the user's chain address using idaddress encoding.
		if profile.GlobalMetaID == "" && address != "" {
			profile.GlobalMetaID = idaddress.EncodeGlobalMetaId(address, pin.ChainName)
		}
		if err := a.saveProfile(profile); err != nil {
			return nil, err
		}
		// Store metaid→address mapping
		if err := a.store.Set(namespace, metaidKey(metaid), []byte(address)); err != nil {
			return nil, err
		}
		return nil, nil
	}

	// /info/* paths
	switch {
	case path == "/info/name":
		profile.Name = string(pin.ContentBody)
		profile.NameId = pin.Id
	case path == "/info/avatar":
		profile.Avatar = "/content/" + pin.Id
		profile.AvatarId = pin.Id
		a.cache.InvalidateByPrefix("avatar:" + metaid)
	case path == "/info/nft-avatar":
		profile.NftAvatar = "/content/" + pin.Id
	case path == "/info/bio":
		profile.Bio = string(pin.ContentBody)
		profile.BioId = pin.Id
	case path == "/info/role":
		profile.Role = string(pin.ContentBody)
		profile.RoleId = pin.Id
	case path == "/info/soul":
		profile.Soul = string(pin.ContentBody)
		profile.SoulId = pin.Id
	case path == "/info/goal":
		profile.Goal = string(pin.ContentBody)
		profile.GoalId = pin.Id
	case path == "/info/chatSkills":
		profile.ChatSkills = string(pin.ContentBody)
		profile.ChatSkillsId = pin.Id
	case path == "/info/LLM":
		profile.LLM = string(pin.ContentBody)
		profile.LLMId = pin.Id
	case path == "/info/homepage":
		profile.Homepage = string(pin.ContentBody)
		profile.HomepageId = pin.Id
	case path == "/info/background":
		profile.Background = "/content/" + pin.Id
		profile.BackgroundId = pin.Id
	case path == "/info/chatpubkey":
		profile.ChatPublicKey = string(pin.ContentBody)
		profile.ChatPublicKeyId = pin.Id
	default:
		return nil, nil
	}

	if err := a.saveProfile(profile); err != nil {
		return nil, err
	}

	// Invalidate cache for this user
	a.cache.InvalidateByPrefix("profile:" + metaid)

	return nil, nil
}

func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	// Don't index mempool user info — wait for confirmation
	return nil, nil
}

// RegisterRoutes mounts user info HTTP endpoints.
func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	router.GET("/info/address/:address", a.handleAddressInfo)
	router.GET("/info/metaid/:metaid", a.handleMetaIdInfo)
	router.GET("/info/globalmetaid/:globalMetaId", a.handleGlobalMetaIdInfo)
}

// The /info/* endpoints use code=1 for success and code=40400/40000 for errors
// to mirror meta-file-system's response shape. See internal/api/response.go for
// background.
func (a *Aggregator) handleAddressInfo(c *gin.Context) {
	address := c.Param("address")
	if address == "" {
		api.RespErr(c, api.MetaFileInvalidParamCode, "address is required")
		return
	}

	profile, err := a.lookupByAddress(c.Request.Context(), address)
	if err != nil || profile == nil {
		api.RespErr(c, api.MetaFileNotFoundCode, "user not found")
		return
	}

	api.RespSuccessCode(c, api.MetaFileSuccessCode, profile)
}

func (a *Aggregator) handleMetaIdInfo(c *gin.Context) {
	metaid := c.Param("metaid")
	if metaid == "" {
		api.RespErr(c, api.MetaFileInvalidParamCode, "metaid is required")
		return
	}

	// Check cache first
	cacheKey := "profile:" + metaid
	if val, ok := a.cache.Get(cacheKey); ok {
		var profile UserProfile
		if err := json.Unmarshal(val, &profile); err == nil {
			if completed, err := a.completeProfile(c.Request.Context(), &profile, remoteProfileQueries{
				{kind: remoteLookupByMetaId, value: metaid},
				{kind: remoteLookupByAddress, value: metaid},
			}, metaid); err == nil && completed != nil {
				if raw, err := json.Marshal(completed); err == nil {
					a.cache.Set(cacheKey, raw, defaultTTL)
				}
				api.RespSuccessCode(c, api.MetaFileSuccessCode, completed)
				return
			}
		}
	}

	profile, err := a.lookupByMetaId(c.Request.Context(), metaid)
	if err != nil || profile == nil {
		api.RespErr(c, api.MetaFileNotFoundCode, "user not found")
		return
	}

	// Cache the result
	if raw, err := json.Marshal(profile); err == nil {
		a.cache.Set(cacheKey, raw, defaultTTL)
	}

	api.RespSuccessCode(c, api.MetaFileSuccessCode, profile)
}

func (a *Aggregator) handleGlobalMetaIdInfo(c *gin.Context) {
	globalMetaId := c.Param("globalMetaId")
	if globalMetaId == "" {
		api.RespErr(c, api.MetaFileInvalidParamCode, "globalMetaId is required")
		return
	}

	// Try to resolve globalMetaId → metaid
	profile, err := a.lookupByGlobalMetaId(c.Request.Context(), globalMetaId)
	if err != nil || profile == nil {
		api.RespErr(c, api.MetaFileNotFoundCode, "user not found")
		return
	}

	api.RespSuccessCode(c, api.MetaFileSuccessCode, profile)
}

// --- In-process lookup helpers ----------------------------------------------
//
// These wrap the existing internal getters so other aggregators (e.g.
// skillservice) can resolve a provider's profile without going through the
// HTTP layer. They are intentionally thin: callers get the same data shape
// idchat sees on the wire, plus the ChatPublicKey field which is only
// populated when the chain-published /info/chatpubkey PIN has been indexed.
//
// Per the Bot Hub spec, request-path profile resolution must stay
// in-process — no HTTP fanout — so consumers should call these directly
// rather than re-hit /api/info/*.

// LookupByMetaId returns the profile for a metaid. (nil, nil) means
// "not found"; non-nil err is a real I/O / decode failure.
func (a *Aggregator) LookupByMetaId(metaid string) (*UserProfile, error) {
	return a.lookupByMetaId(context.Background(), metaid)
}

// LookupByAddress returns the profile whose Address matches (case-insensitive).
func (a *Aggregator) LookupByAddress(address string) (*UserProfile, error) {
	return a.lookupByAddress(context.Background(), address)
}

// LookupByGlobalMetaId returns the profile whose GlobalMetaID matches
// (case-insensitive).
func (a *Aggregator) LookupByGlobalMetaId(globalMetaId string) (*UserProfile, error) {
	return a.lookupByGlobalMetaId(context.Background(), globalMetaId)
}

// --- Profile persistence ---

func (a *Aggregator) getProfile(metaid string) (*UserProfile, error) {
	raw, err := a.store.Get(namespace, profileKey(metaid))
	if err != nil || raw == nil {
		return nil, nil
	}

	var profile UserProfile
	if err := json.Unmarshal(raw, &profile); err != nil {
		log.Printf("[userinfo] failed to unmarshal profile for %s: %v", metaid, err)
		return nil, err
	}
	return &profile, nil
}

func (a *Aggregator) saveProfile(profile *UserProfile) error {
	if profile.MetaID == "" {
		return nil
	}
	return a.saveProfileAtKey(profile.MetaID, profile)
}

func (a *Aggregator) saveProfileAtKey(key string, profile *UserProfile) error {
	key = strings.TrimSpace(key)
	if key == "" || profile == nil {
		return nil
	}
	raw, err := json.Marshal(profile)
	if err != nil {
		return err
	}
	if err := a.store.Set(namespace, profileKey(key), raw); err != nil {
		return err
	}

	indexMetaID := strings.TrimSpace(profile.MetaID)
	if indexMetaID == "" {
		indexMetaID = key
	}
	if globalMetaId := strings.TrimSpace(profile.GlobalMetaID); globalMetaId != "" {
		if err := a.store.Set(namespace, globalMetaIdKey(globalMetaId), []byte(indexMetaID)); err != nil {
			return err
		}
	}
	if address := strings.TrimSpace(profile.Address); address != "" {
		if err := a.store.Set(namespace, addressKey(address), []byte(indexMetaID)); err != nil {
			return err
		}
	}
	return nil
}

func (a *Aggregator) findProfileByAddress(address string) (*UserProfile, error) {
	address = strings.TrimSpace(address)
	if raw, err := a.store.Get(namespace, addressKey(address)); err == nil && len(raw) > 0 {
		indexedMetaID := string(raw)
		if profile, err := a.getProfile(indexedMetaID); err != nil {
			log.Printf("[userinfo] stale address index %q -> %q: %v", address, indexedMetaID, err)
		} else if profile != nil && strings.EqualFold(strings.TrimSpace(profile.Address), address) {
			return profile, nil
		} else if profile != nil {
			log.Printf("[userinfo] stale address index %q -> %q: profile address %q", address, indexedMetaID, profile.Address)
		}
	}

	return a.scanProfiles(func(p *UserProfile) bool {
		return strings.EqualFold(strings.TrimSpace(p.Address), address)
	})
}

func (a *Aggregator) findProfileByGlobalMetaId(globalMetaId string) (*UserProfile, error) {
	globalMetaId = strings.TrimSpace(globalMetaId)
	if raw, err := a.store.Get(namespace, globalMetaIdKey(globalMetaId)); err == nil && len(raw) > 0 {
		indexedMetaID := string(raw)
		if profile, err := a.getProfile(indexedMetaID); err != nil {
			log.Printf("[userinfo] stale globalMetaId index %q -> %q: %v", globalMetaId, indexedMetaID, err)
		} else if profile != nil && strings.EqualFold(strings.TrimSpace(profile.GlobalMetaID), globalMetaId) {
			return profile, nil
		} else if profile != nil {
			log.Printf("[userinfo] stale globalMetaId index %q -> %q: profile globalMetaId %q", globalMetaId, indexedMetaID, profile.GlobalMetaID)
		}
	}

	return a.scanProfiles(func(p *UserProfile) bool {
		return strings.EqualFold(strings.TrimSpace(p.GlobalMetaID), globalMetaId)
	})
}

func (a *Aggregator) defaultScanProfiles(match func(*UserProfile) bool) (*UserProfile, error) {
	var found *UserProfile
	err := a.store.ScanPrefix(namespace, profileKey(""), func(key, value []byte) error {
		var p UserProfile
		if err := json.Unmarshal(value, &p); err != nil {
			return nil
		}
		if match(&p) {
			found = &p
			return nil
		}
		return nil
	})
	return found, err
}

func profileKey(metaid string) []byte {
	return []byte(profilePrefix + metaid)
}

func metaidKey(metaid string) []byte {
	return []byte(metaidPrefix + metaid)
}

func globalMetaIdKey(globalMetaId string) []byte {
	return []byte(globalMetaIdPrefix + strings.ToLower(strings.TrimSpace(globalMetaId)))
}

func addressKey(address string) []byte {
	return []byte(addressPrefix + strings.ToLower(strings.TrimSpace(address)))
}
