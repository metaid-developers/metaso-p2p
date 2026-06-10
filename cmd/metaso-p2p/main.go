package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/bothomepage"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/groupchat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/notify"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/api"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	bitcoinchain "github.com/metaid-developers/metaso-p2p/internal/chain/bitcoin"
	dogecoinchain "github.com/metaid-developers/metaso-p2p/internal/chain/dogecoin"
	mvcchain "github.com/metaid-developers/metaso-p2p/internal/chain/mvc"
	opcatchain "github.com/metaid-developers/metaso-p2p/internal/chain/opcat"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/federation"
	"github.com/metaid-developers/metaso-p2p/internal/indexer"
	"github.com/metaid-developers/metaso-p2p/internal/presence"
	"github.com/metaid-developers/metaso-p2p/internal/socket"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
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
	var userinfoAgg *userinfo.Aggregator
	var botHomepageAgg *bothomepage.Aggregator
	if store != nil && cacheProvider != nil {
		aggRegistry = aggregator.NewRegistry(store, cacheProvider)

		if err := aggRegistry.Register(&notify.Aggregator{}); err != nil {
			log.Printf("WARNING: notify aggregator init failed: %v", err)
		}
		userinfoAgg = &userinfo.Aggregator{}
		if err := aggRegistry.Register(userinfoAgg); err != nil {
			log.Printf("WARNING: userinfo aggregator init failed: %v", err)
		}
		if err := aggRegistry.Register(&groupchat.Aggregator{}); err != nil {
			log.Printf("WARNING: groupchat aggregator init failed: %v", err)
		}
		privatechatAgg := &privatechat.Aggregator{}
		if err := aggRegistry.Register(privatechatAgg); err != nil {
			log.Printf("WARNING: privatechat aggregator init failed: %v", err)
		}
		skillserviceAgg := &skillservice.Aggregator{}
		if err := aggRegistry.Register(skillserviceAgg); err != nil {
			log.Printf("WARNING: skillservice aggregator init failed: %v", err)
		}
		var publishedAgg *publishedcontent.Aggregator
		publishedCandidate := &publishedcontent.Aggregator{}
		if err := aggRegistry.Register(publishedCandidate); err != nil {
			log.Printf("WARNING: publishedcontent aggregator init failed: %v", err)
		} else {
			publishedAgg = publishedCandidate
		}
		botHomepageAgg = &bothomepage.Aggregator{}
		if err := aggRegistry.Register(botHomepageAgg); err != nil {
			log.Printf("WARNING: bothomepage aggregator init failed: %v", err)
		}
		// Wire skillservice → userinfo for provider profile resolution.
		// skillservice itself stays decoupled from remote profile services;
		// userinfo owns any configured local-first profile completion.
		skillserviceAgg.SetProfileLookup(skillservice.NewUserInfoLookupAdapter(userinfoAgg))
		privatechatAgg.SetProfileLookup(privatechat.NewUserInfoLookupAdapter(userinfoAgg))
		// Asset base URL turns chain-declared pin ids / metafile URIs
		// into HTTP URLs the Bot Hub frontend can load directly. The
		// value comes from METASO_P2P_ASSET_BASE_URL (default in
		// config.Default mirrors the documented recommendation).
		skillserviceAgg.SetAssetBaseURL(cfg.BotHub.AssetBaseURL)
		botHomepageAgg.SetProfileLookup(bothomepage.NewUserInfoLookupAdapter(userinfoAgg))
		botHomepageAgg.SetServiceLister(skillserviceAgg)
		botHomepageAgg.SetHomepageServiceLister(skillserviceAgg)
		if publishedAgg != nil {
			botHomepageAgg.SetPublishedContentLister(publishedAgg)
		}
		botHomepageAgg.SetAssetBaseURL(cfg.BotHub.AssetBaseURL)
		if cfg.BotHomepageV2Backfill.Enabled && (publishedAgg != nil || userinfoAgg != nil) {
			go func() {
				since := time.Now().Add(-cfg.BotHomepageV2Backfill.Lookback)
				if publishedAgg != nil {
					ctx, cancel := context.WithTimeout(context.Background(), cfg.BotHomepageV2Backfill.Timeout)
					err := publishedAgg.Backfill(publishedcontent.BackfillOptions{
						Context:  ctx,
						Client:   publishedcontent.NewBackfillClient(cfg.BotHomepageV2Backfill.MANAPIBaseURL, http.DefaultClient),
						Since:    since,
						PageSize: cfg.BotHomepageV2Backfill.PageSize,
					})
					cancel()
					if err != nil {
						log.Printf("WARNING: bot homepage v2 publishedcontent backfill failed: %v", err)
					}
				}
				if userinfoAgg != nil {
					ctx, cancel := context.WithTimeout(context.Background(), cfg.BotHomepageV2Backfill.Timeout)
					err := userinfoAgg.Backfill(userinfo.BackfillOptions{
						Context:  ctx,
						Client:   userinfo.NewBackfillClient(cfg.BotHomepageV2Backfill.MANAPIBaseURL, http.DefaultClient),
						Since:    since,
						PageSize: cfg.BotHomepageV2Backfill.PageSize,
					})
					cancel()
					if err != nil {
						log.Printf("WARNING: bot homepage v2 userinfo backfill failed: %v", err)
					}
				}
			}()
		}
		log.Printf("aggregators registered: %d", len(aggRegistry.All()))
	}

	// --- Indexer engine ---
	var idxEngine *indexer.Engine
	if store != nil && aggRegistry != nil && cfg.BlockIndex.Enabled {
		idxEngine = indexer.NewEngine(store, aggRegistry)
		idxEngine.ConfigureMempoolPolling(cfg.ZMQ.MempoolPollingEnabled, cfg.ZMQ.MempoolPollInterval, cfg.ZMQ.MempoolDedupeTTL)
		log.Printf("block index enabled chains: %s", strings.Join(enabledBlockIndexChainNames(cfg.BlockIndex), ","))

		// BTC chain + indexer
		if cfg.BlockIndex.BTC.Enabled {
			btcChain := bitcoinchain.NewChain(cfg.BlockIndex.BTC)
			btcParams := bitcoinchain.NetParams("") // "" = mainnet, "1" = testnet, "2" = regtest
			btcIndexer := bitcoinchain.NewIndexer(btcChain, btcParams)

			if err := idxEngine.RegisterChain(btcChain, btcIndexer, cfg.BlockIndex.BTC.InitialHeight); err != nil {
				log.Printf("WARNING: BTC chain registration failed: %v", err)
			}
		}

		// MVC chain + indexer (skill-service on MVC per Bot Hub spec).
		if cfg.BlockIndex.MVC.Enabled {
			mvcChain := mvcchain.NewChain(cfg.BlockIndex.MVC)
			mvcParams := mvcchain.NetParams("")
			mvcIndexer := mvcchain.NewIndexer(mvcChain, mvcParams)

			if err := idxEngine.RegisterChain(mvcChain, mvcIndexer, cfg.BlockIndex.MVC.InitialHeight); err != nil {
				log.Printf("WARNING: MVC chain registration failed: %v", err)
			}
		}

		if cfg.BlockIndex.DOGE.Enabled {
			dogeChain := dogecoinchain.NewChain(cfg.BlockIndex.DOGE)
			dogeParams := dogecoinchain.NetParams("")
			dogeIndexer := dogecoinchain.NewIndexer(dogeChain, dogeParams)

			if err := idxEngine.RegisterChain(dogeChain, dogeIndexer, cfg.BlockIndex.DOGE.InitialHeight); err != nil {
				log.Printf("WARNING: DOGE chain registration failed: %v", err)
			}
		}

		if cfg.BlockIndex.OPCAT.Enabled {
			opcatChain := opcatchain.NewChain(cfg.BlockIndex.OPCAT)
			opcatParams := opcatchain.NetParams("")
			opcatIndexer := opcatchain.NewIndexer(opcatChain, opcatParams)

			if err := idxEngine.RegisterChain(opcatChain, opcatIndexer, cfg.BlockIndex.OPCAT.InitialHeight); err != nil {
				log.Printf("WARNING: OPCAT chain registration failed: %v", err)
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
		socketServer.SetProfileAssetBaseURL(cfg.BotHub.AssetBaseURL)
		if userinfoAgg != nil {
			socketServer.SetProfileLookup(&socketUserInfoLookupAdapter{ui: userinfoAgg})
		}
		socketServer.StartTimeoutCleanup()
		log.Printf("socket.io server: path=%s legacy=%s", cfg.Socket.PrimaryPath, cfg.Socket.LegacyPath)

		// Start push consumer to route aggregator notify events to connected clients.
		if aggRegistry != nil {
			socketServer.StartPushConsumer(aggRegistry)
		}
	}

	// --- Federation service ---
	var federationService *federation.Service
	if cfg.Federation.Enabled {
		if socketServer == nil {
			log.Fatalf("federation is enabled but socket server is disabled")
		}
		federationService, err = federation.NewService(cfg.Federation, socketServer.Manager())
		if err != nil {
			log.Fatalf("failed to create federation service: %v", err)
		}
		socketServer.SetSnapshotProvider(federationService.SnapshotProvider())
		socketServer.SetGlobalReader(federationService.GlobalReader())
		log.Printf("federation service: node_id=%s", federationService.NodeID())
	}

	if botHomepageAgg != nil && socketServer != nil {
		var globalReader presence.GlobalReader
		if federationService != nil {
			globalReader = federationService.GlobalReader()
		}
		botHomepageAgg.SetPresenceReaders(socketServer.Manager(), globalReader)
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

	if federationService != nil {
		federationService.Start(shutdownCtx)
	}

	go func() {
		log.Printf("metaso-p2p started: %s", cfg.Summary())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	<-shutdownCtx.Done()

	// --- Graceful shutdown ---
	log.Printf("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), cfg.Service.ShutdownTimeout)
	defer cancel()

	// Stop federation loops before closing local readers and stores.
	if federationService != nil {
		federationService.Stop()
	}

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
	log.Printf("metaso-p2p stopped")
}

func enabledBlockIndexChainNames(cfg config.BlockIndexConfig) []string {
	var names []string
	if cfg.BTC.Enabled {
		names = append(names, "btc")
	}
	if cfg.MVC.Enabled {
		names = append(names, "mvc")
	}
	if cfg.DOGE.Enabled {
		names = append(names, "doge")
	}
	if cfg.OPCAT.Enabled {
		names = append(names, "opcat")
	}
	return names
}

type socketUserInfoLookupAdapter struct {
	ui *userinfo.Aggregator
}

func (a *socketUserInfoLookupAdapter) LookupByMetaId(metaid string) (*socket.ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByMetaId(metaid)
	return socketProfileFromUserInfo(p), err
}

func (a *socketUserInfoLookupAdapter) LookupByGlobalMetaId(globalMetaId string) (*socket.ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByGlobalMetaId(globalMetaId)
	return socketProfileFromUserInfo(p), err
}

func (a *socketUserInfoLookupAdapter) LookupByAddress(address string) (*socket.ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupByAddress(address)
	return socketProfileFromUserInfo(p), err
}

func socketProfileFromUserInfo(p *userinfo.UserProfile) *socket.ProfileSnapshot {
	if p == nil {
		return nil
	}
	return &socket.ProfileSnapshot{
		GlobalMetaId:  p.GlobalMetaID,
		MetaId:        p.MetaID,
		Address:       p.Address,
		Name:          p.Name,
		Avatar:        p.Avatar,
		AvatarId:      p.AvatarId,
		ChatPublicKey: p.ChatPublicKey,
		Bio:           p.Bio,
	}
}
