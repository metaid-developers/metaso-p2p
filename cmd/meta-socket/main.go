package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/metaid-developers/meta-socket/internal/aggregator"
	"github.com/metaid-developers/meta-socket/internal/aggregator/groupchat"
	"github.com/metaid-developers/meta-socket/internal/aggregator/notify"
	"github.com/metaid-developers/meta-socket/internal/aggregator/privatechat"
	"github.com/metaid-developers/meta-socket/internal/aggregator/skillservice"
	"github.com/metaid-developers/meta-socket/internal/aggregator/userinfo"
	"github.com/metaid-developers/meta-socket/internal/api"
	"github.com/metaid-developers/meta-socket/internal/cache"
	bitcoinchain "github.com/metaid-developers/meta-socket/internal/chain/bitcoin"
	"github.com/metaid-developers/meta-socket/internal/config"
	"github.com/metaid-developers/meta-socket/internal/indexer"
	"github.com/metaid-developers/meta-socket/internal/socket"
	"github.com/metaid-developers/meta-socket/internal/storage"
)

var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	// --- Pebble store ---
	var store *storage.PebbleStore
	if cfg.Pebble.Enabled {
		store = storage.NewPebbleStore(cfg.Pebble.DataDir)
		log.Printf("pebble store: dataDir=%s", cfg.Pebble.DataDir)
	} else {
		log.Printf("pebble store disabled")
	}

	// --- Cache provider ---
	var cacheProvider *cache.CacheProvider
	if store != nil {
		cacheProvider = cache.New(store)
		log.Printf("cache: maxEntries=%d ttl=%ds", cfg.Cache.MaxEntries, cfg.Cache.DefaultTTLSeconds)
	}

	// --- Aggregator registry ---
	var aggRegistry *aggregator.Registry
	if store != nil && cacheProvider != nil {
		aggRegistry = aggregator.NewRegistry(store, cacheProvider)

		if err := aggRegistry.Register(&notify.Aggregator{}); err != nil {
			log.Printf("WARNING: notify aggregator init failed: %v", err)
		}
		if err := aggRegistry.Register(&userinfo.Aggregator{}); err != nil {
			log.Printf("WARNING: userinfo aggregator init failed: %v", err)
		}
		if err := aggRegistry.Register(&groupchat.Aggregator{}); err != nil {
			log.Printf("WARNING: groupchat aggregator init failed: %v", err)
		}
		if err := aggRegistry.Register(&privatechat.Aggregator{}); err != nil {
			log.Printf("WARNING: privatechat aggregator init failed: %v", err)
		}
		if err := aggRegistry.Register(&skillservice.Aggregator{}); err != nil {
			log.Printf("WARNING: skillservice aggregator init failed: %v", err)
		}
		log.Printf("aggregators registered: %d", len(aggRegistry.All()))
	}

	// --- Indexer engine ---
	var idxEngine *indexer.Engine
	if store != nil && aggRegistry != nil && cfg.BlockIndex.Enabled {
		idxEngine = indexer.NewEngine(store, aggRegistry)

		// BTC chain + indexer
		if cfg.BlockIndex.BTC.Enabled {
			btcChain := bitcoinchain.NewChain(cfg.BlockIndex.BTC)
			btcParams := bitcoinchain.NetParams("") // "" = mainnet, "1" = testnet, "2" = regtest
			btcIndexer := bitcoinchain.NewIndexer(btcChain, btcParams)

			if err := idxEngine.RegisterChain(btcChain, btcIndexer, cfg.BlockIndex.BTC.InitialHeight); err != nil {
				log.Printf("WARNING: BTC chain registration failed: %v", err)
			}
		}

		// Start the engine if any chains are registered.
		// The engine runs in background goroutines managed by the parent context.
		if idxEngine.Chains() > 0 {
			idxEngine.Start(context.Background())
		}
	}

	// --- Socket.IO server ---
	var socketServer *socket.Server
	if cfg.Socket.Enabled {
		socketServer = socket.NewServer(cfg.Socket)
		socketServer.StartTimeoutCleanup()
		log.Printf("socket.io server: path=%s legacy=%s", cfg.Socket.PrimaryPath, cfg.Socket.LegacyPath)

		// Start push consumer to route aggregator notify events to connected clients.
		if aggRegistry != nil {
			socketServer.StartPushConsumer(aggRegistry)
		}
	}

	// --- HTTP router ---
	router := api.SetupRouter(cfg, store, cacheProvider, aggRegistry, socketServer, version)

	// --- Start HTTP server ---
	srv := &http.Server{
		Addr:              cfg.Service.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("meta-socket started: %s", cfg.Summary())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-shutdownCtx.Done()

	// --- Graceful shutdown ---
	log.Printf("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Service.ShutdownTimeout)
	defer cancel()

	// Shutdown socket server first (disconnects all clients cleanly).
	if socketServer != nil {
		socketServer.Shutdown()
	}

	// Stop the indexer engine.
	if idxEngine != nil {
		idxEngine.Stop()
	}

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("shutdown error: %v", err)
	}
	if store != nil {
		store.Close()
	}
	log.Printf("meta-socket stopped")
}
