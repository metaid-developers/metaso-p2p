package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/socket"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

// TxStatusResolver resolves the confirmation facts of a transaction on a
// named chain (satisfied by the indexer engine). ok=false means the chain
// is unknown or has no status provider.
type TxStatusResolver interface {
	TransactionStatus(chainName, txid string) (ok bool, confirmed bool, confirmations int64, blockHash string, height int64, err error)
}

// SetupRouter creates and configures the Gin router with all routes.
// It centralizes route registration for health checks, socket.io, aggregator APIs, and presence.

func SetupRouter(
	cfg config.Config,
	store *storage.PebbleStore,
	cacheProvider *cache.CacheProvider,
	aggRegistry *aggregator.Registry,
	socketServer *socket.Server,
	version string,
	txStatusResolver TxStatusResolver,
) *gin.Engine {
	router := gin.New()
	router.Use(RequestTimingMiddleware(), corsMiddleware(), gin.Logger(), gin.Recovery())

	// Health check
	router.GET(cfg.Service.HealthPath, func(c *gin.Context) {
		RespSuccess(c, gin.H{
			"status":  "ok",
			"service": "metaso-p2p",
			"version": version,
		})
	})

	// Socket.IO routes
	if socketServer != nil {
		handler := socketServer.Handler()

		// Primary path: /socket/socket.io
		router.Any(cfg.Socket.PrimaryPath+"/*any", handler)

		// Legacy path: /socket.io
		router.Any(cfg.Socket.LegacyPath+"/*any", handler)

		// Presence routes
		socketServer.RegisterPresenceRoutes(router, cfg.Federation.PresencePath)
		router.GET("/chat-api/group-chat/socket/online-users", socketServer.HandleIdchatOnlineUsers)
		router.GET("/chat-api/group-chat/socket/user-online", socketServer.HandleIdchatUserOnline)
		router.GET("/chat-api/group-chat/socket/stats", socketServer.HandleOnlineStats)
	}

	// Aggregator routes (mounted under /api/ prefix for native metaso-p2p clients).
	if aggRegistry != nil {
		apiGroup := router.Group("/api")
		// Fleet-civility rate limiting (R7): opt-in via config; applies to
		// the aggregation API group only — health, socket and chat
		// compatibility prefixes are unaffected.
		if cfg.RateLimit.Enabled {
			limiter := NewRateLimiter(cfg.RateLimit.RequestsPerSecond, cfg.RateLimit.Burst)
			apiGroup.Use(limiter.Middleware(cfg.RateLimit.Keys))
		}
		for _, a := range aggRegistry.All() {
			a.RegisterRoutes(apiGroup)
		}

		// idchat's current runtime config builds chat HTTP URLs as
		// `<metaSoBaseURL>/chat-api/group-chat/*`, so expose the existing group
		// and private chat handlers under that compatibility prefix as well.
		chatAPIGroup := router.Group("/chat-api")
		for _, a := range aggRegistry.All() {
			switch a.Name() {
			case "groupchat", "privatechat":
				a.RegisterRoutes(chatAPIGroup)
			}
		}

		// idchat's chat-notify client builds blocking URLs as
		// `<metaSoBaseURL>/push-base/v1/push/*`, without the native /api
		// prefix. Keep /api/push-base/* above and add this root alias.
		for _, a := range aggRegistry.All() {
			if a.Name() == "notify" {
				a.RegisterRoutes(router.Group(""))
			}
		}

		// Also expose the userinfo aggregator under /metafile-indexer/api so
		// idchat's `metafileIndexerApi` client (configured as
		// `<metaFSBaseURL>/metafile-indexer/api`) can target metaso-p2p as a
		// drop-in replacement for the meta-file-system user info subset
		// without any frontend code changes. Only `/info/*` routes are
		// duplicated here; file upload / avatar content stay with
		// meta-file-system.
		metafileGroup := router.Group("/metafile-indexer/api")
		for _, a := range aggRegistry.All() {
			if a.Name() == "userinfo" {
				a.RegisterRoutes(metafileGroup)
			}
		}

		// Generic transaction-status lookup (man-p2p parity, not module
		// gated): confirmed=false means mempool-unconfirmed
		// (confirmations=0), so readers can tell "sent, unconfirmed" from
		// "no such tx". Unresolvable txids and RPC errors both answer
		// found=false/txStatus=unknown without leaking the raw error.
		router.GET("/api/tx/status", txStatusHandler(txStatusResolver))
	}

	return router
}

// txStatusHandler serves GET /api/tx/status with man-p2p's exact envelope
// (respond codes 404 parameter error / 1 ok, HTTP always 200).
func txStatusHandler(resolver TxStatusResolver) gin.HandlerFunc {
	type txStatusEnvelope struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    any    `json:"data"`
	}
	return func(c *gin.Context) {
		chainName := c.Query("chainName")
		txid := c.Query("txid")
		if chainName == "" || txid == "" || resolver == nil {
			c.JSON(http.StatusOK, txStatusEnvelope{Code: 404, Message: "Parameter error."})
			return
		}
		ok, confirmed, confirmations, blockHash, height, err := resolver.TransactionStatus(chainName, txid)
		if !ok {
			// Unknown chain (or no provider configured): parameter error,
			// same observable shape as man-p2p's missing ChainAdapter.
			c.JSON(http.StatusOK, txStatusEnvelope{Code: 404, Message: "Parameter error."})
			return
		}
		if err != nil {
			c.JSON(http.StatusOK, txStatusEnvelope{Code: 1, Message: "ok", Data: gin.H{
				"chainName": chainName,
				"txid":      txid,
				"found":     false,
				"txStatus":  "unknown",
			}})
			return
		}
		status := "unconfirmed"
		if confirmed {
			status = "confirmed"
		}
		c.JSON(http.StatusOK, txStatusEnvelope{Code: 1, Message: "ok", Data: gin.H{
			"chainName":     chainName,
			"txid":          txid,
			"found":         true,
			"txStatus":      status,
			"confirmations": confirmations,
			"blockHash":     blockHash,
			"height":        height,
		}})
	}
}

func corsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		headers := c.Writer.Header()
		headers.Set("Access-Control-Allow-Origin", "*")
		headers.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		headers.Set("Access-Control-Allow-Headers", "Origin, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, AccessToken, X-API-KEY, X-Signature, X-Public-Key")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
