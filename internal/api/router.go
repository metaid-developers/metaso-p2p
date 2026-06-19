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

// SetupRouter creates and configures the Gin router with all routes.
// It centralizes route registration for health checks, socket.io, aggregator APIs, and presence.
func SetupRouter(
	cfg config.Config,
	store *storage.PebbleStore,
	cacheProvider *cache.CacheProvider,
	aggRegistry *aggregator.Registry,
	socketServer *socket.Server,
	version string,
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
		for _, a := range aggRegistry.All() {
			a.RegisterRoutes(router.Group("/api"))
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
	}

	return router
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
