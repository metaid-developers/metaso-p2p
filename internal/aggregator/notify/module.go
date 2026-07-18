package notify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/api"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// BlockedChat represents a blocked chat entry.
type BlockedChat struct {
	ChatID   string `json:"chatId"`
	ChatType string `json:"chatType"` // "private" or "group"
	MetaID   string `json:"metaId"`
	Reason   string `json:"reason,omitempty"`
}

// UserBlockedChats is the stored block list for a user.
type UserBlockedChats struct {
	UserID       string        `json:"userId"`
	BlockedChats []BlockedChat `json:"blockedChats"`
	UpdatedAt    int64         `json:"updatedAt"`
}

// Aggregator manages chat blocking (user-initiated block/unblock).
type Aggregator struct {
	store    *storage.PebbleStore
	cache    *cache.Cache[[]byte]
	notifyCh chan *aggregator.NotifyEvent
	pubKey   string // for signature verification
}

const (
	namespace       = "notify"
	blockedPrefix   = "blocked:"
	cacheMaxEntries = 1000
	cacheTTL        = 5 * time.Minute
)

func (a *Aggregator) Name() string { return "notify" }

func (a *Aggregator) Init(store *storage.PebbleStore, cacheProvider *cache.CacheProvider) error {
	a.store = store
	a.cache = cacheProvider.Namespace(namespace, cacheMaxEntries, cacheTTL)
	a.notifyCh = make(chan *aggregator.NotifyEvent, 64)
	return nil
}

func (a *Aggregator) NotifyChannel() <-chan *aggregator.NotifyEvent {
	return a.notifyCh
}

func (a *Aggregator) HandleBlockPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	// Notify aggregator does not process chain pins directly.
	// Block/unblock operations come via HTTP API.
	return nil, nil
}

func (a *Aggregator) HandleMempoolPin(pin *aggregator.PinInscription) (*aggregator.NotifyEvent, error) {
	return nil, nil
}

func (a *Aggregator) RegisterRoutes(router *gin.RouterGroup) {
	push := router.Group("/push-base/v1/push")
	push.GET("/get_user_blocked_chats", a.handleGetBlockedChats)
	push.POST("/add_blocked_chat", a.handleAddBlockedChat)
	push.POST("/remove_blocked_chat", a.handleRemoveBlockedChat)
}

func (a *Aggregator) handleGetBlockedChats(c *gin.Context) {
	metaID := c.Query("metaId")
	if metaID == "" {
		api.RespErr(c, 1, "metaId is required")
		return
	}

	blocked, err := a.getBlockedChats(metaID)
	if err != nil {
		api.RespErr(c, 1, "failed to get blocked chats")
		return
	}
	if blocked == nil {
		blocked = &UserBlockedChats{
			UserID:       metaID,
			BlockedChats: []BlockedChat{},
		}
	}

	api.RespSuccess(c, blocked)
}

func (a *Aggregator) handleAddBlockedChat(c *gin.Context) {
	var req struct {
		ChatID   string `json:"chatId"`
		ChatType string `json:"chatType"`
		MetaID   string `json:"metaId"`
		Reason   string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespErr(c, 1, "invalid request body")
		return
	}
	if req.ChatID == "" || req.MetaID == "" {
		api.RespErr(c, 1, "chatId and metaId are required")
		return
	}

	if !a.verifySignature(c) {
		api.RespErr(c, 403, "signature verification failed")
		return
	}

	blocked, _ := a.getBlockedChats(req.MetaID)
	if blocked == nil {
		blocked = &UserBlockedChats{UserID: req.MetaID, BlockedChats: []BlockedChat{}}
	}

	// Deduplicate
	for _, bc := range blocked.BlockedChats {
		if bc.ChatID == req.ChatID && bc.ChatType == req.ChatType {
			api.RespSuccess(c, blocked)
			return
		}
	}

	blocked.BlockedChats = append(blocked.BlockedChats, BlockedChat{
		ChatID:   req.ChatID,
		ChatType: req.ChatType,
		MetaID:   req.MetaID,
		Reason:   req.Reason,
	})
	blocked.UpdatedAt = c.GetInt64("timestamp")

	if err := a.saveBlockedChats(blocked); err != nil {
		api.RespErr(c, 1, "failed to save")
		return
	}

	a.cache.Delete("blocked:" + req.MetaID)
	api.RespSuccess(c, blocked)
}

func (a *Aggregator) handleRemoveBlockedChat(c *gin.Context) {
	var req struct {
		ChatID string `json:"chatId"`
		MetaID string `json:"metaId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.RespErr(c, 1, "invalid request body")
		return
	}

	if !a.verifySignature(c) {
		api.RespErr(c, 403, "signature verification failed")
		return
	}

	blocked, _ := a.getBlockedChats(req.MetaID)
	if blocked == nil {
		api.RespSuccess(c, &UserBlockedChats{UserID: req.MetaID, BlockedChats: []BlockedChat{}})
		return
	}

	filtered := make([]BlockedChat, 0, len(blocked.BlockedChats))
	for _, bc := range blocked.BlockedChats {
		if bc.ChatID != req.ChatID {
			filtered = append(filtered, bc)
		}
	}
	blocked.BlockedChats = filtered
	blocked.UpdatedAt = c.GetInt64("timestamp")

	if err := a.saveBlockedChats(blocked); err != nil {
		api.RespErr(c, 1, "failed to save")
		return
	}

	a.cache.Delete("blocked:" + req.MetaID)
	api.RespSuccess(c, blocked)
}

func (a *Aggregator) getBlockedChats(metaID string) (*UserBlockedChats, error) {
	raw, err := a.store.Get(namespace, blockedKey(metaID))
	if err != nil || raw == nil {
		return nil, nil
	}

	var blocked UserBlockedChats
	if err := json.Unmarshal(raw, &blocked); err != nil {
		log.Printf("[notify] failed to unmarshal blocked chats for %s: %v", metaID, err)
		return nil, err
	}
	return &blocked, nil
}

func (a *Aggregator) saveBlockedChats(blocked *UserBlockedChats) error {
	raw, err := json.Marshal(blocked)
	if err != nil {
		return err
	}
	return a.store.Set(namespace, blockedKey(blocked.UserID), raw)
}

func (a *Aggregator) SetPubKey(key string) {
	a.pubKey = key
}

// verifySignature checks X-Signature against X-Public-Key using SHA256("metaso.network").
func (a *Aggregator) verifySignature(c *gin.Context) bool {
	if a.pubKey == "" {
		// No public key configured, skip verification
		return true
	}

	signature := c.GetHeader("X-Signature")
	pubKey := c.GetHeader("X-Public-Key")
	if signature == "" || pubKey == "" {
		return false
	}
	if pubKey != a.pubKey {
		return false
	}

	hash := sha256.Sum256([]byte("metaso.network"))
	expected := hex.EncodeToString(hash[:])
	return signature == expected
}

func blockedKey(metaID string) []byte {
	return []byte(blockedPrefix + metaID)
}
