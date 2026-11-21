package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	_ "net/http/pprof"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/block"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/bothomepage"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/botsearch"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/ftmarket"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/groupchat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/idbotsca"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/metaweb"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/notify"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/paybuzz"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/preference"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/qa"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/social"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/socialcontent"
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
	var socialAgg *social.Aggregator
	var socialContentAgg *socialcontent.Aggregator
	var groupChatAgg *groupchat.Aggregator
	var botSearchAgg *botsearch.Aggregator
	var blockAgg *block.Aggregator
	var notifyAgg *notify.Aggregator
	var mrc721Agg *mrc721.Aggregator
	var ftmarketAgg *ftmarket.Aggregator
	var paybuzzAgg *paybuzz.Aggregator
	if store != nil && cacheProvider != nil {
		aggRegistry = aggregator.NewRegistry(store, cacheProvider)

		notifyCandidate := &notify.Aggregator{}
		if err := aggRegistry.Register(notifyCandidate); err != nil {
			log.Printf("WARNING: notify aggregator init failed: %v", err)
		} else {
			notifyAgg = notifyCandidate
		}
		userinfoAgg = &userinfo.Aggregator{}
		if err := aggRegistry.Register(userinfoAgg); err != nil {
			log.Printf("WARNING: userinfo aggregator init failed: %v", err)
		}
		socialCandidate := &social.Aggregator{}
		if err := aggRegistry.Register(socialCandidate); err != nil {
			log.Printf("WARNING: social aggregator init failed: %v", err)
		} else {
			socialAgg = socialCandidate
			socialAgg.SetProfileLookup(social.NewUserInfoLookupAdapter(userinfoAgg))
		}
		socialContentCandidate := &socialcontent.Aggregator{}
		if err := aggRegistry.Register(socialContentCandidate); err != nil {
			log.Printf("WARNING: socialcontent aggregator init failed: %v", err)
		} else {
			socialContentAgg = socialContentCandidate
			socialContentAgg.SetProfileLookup(socialcontent.NewUserInfoLookupAdapter(userinfoAgg))
		}
		if socialAgg != nil && socialContentAgg != nil {
			socialContentAgg.SetFollowLister(&socialFollowAdapter{agg: socialAgg})
		}
		groupChatCandidate := &groupchat.Aggregator{}
		if err := aggRegistry.Register(groupChatCandidate); err != nil {
			log.Printf("WARNING: groupchat aggregator init failed: %v", err)
		} else {
			groupChatAgg = groupChatCandidate
		}
		privatechatAgg := &privatechat.Aggregator{}
		if err := aggRegistry.Register(privatechatAgg); err != nil {
			log.Printf("WARNING: privatechat aggregator init failed: %v", err)
		}
		// Simpleblock aggregation (spec §1.4/§4.4-4.6): folds both block
		// paths into the bidirectional read model, serves
		// /api/social/globalmetaid/:id/blocks, mirrors confirmed pins into
		// the notify session cache, and forwards compatibility-path pins
		// arriving via privatechat.
		blockCandidate := &block.Aggregator{}
		if err := aggRegistry.Register(blockCandidate); err != nil {
			log.Printf("WARNING: block aggregator init failed: %v", err)
		} else {
			blockAgg = blockCandidate
			blockAgg.SetProfileLookup(block.NewUserInfoLookupAdapter(userinfoAgg))
			if notifyAgg != nil {
				blockAgg.SetChatBlockBridge(notifyAgg)
			}
			privatechatAgg.SetBlockPinIngestor(blockAgg)
		}
		// Unified filter wiring (spec 2026-10-29 §4.5/§4.6): the social
		// list/counts read paths filter through the real block aggregation,
		// and the privatechat surfaces consult the notify session model
		// merged with the chain state (union semantics). Without the block
		// aggregator the social filter stays on its no-op default and the
		// chat merge runs off-chain-only.
		if socialAgg != nil && blockAgg != nil {
			socialAgg.SetBlockLister(social.NewBlockLister(blockAgg))
		}
		if notifyAgg != nil {
			var chainSource notify.ChainBlockSource
			if blockAgg != nil {
				chainSource = blockAgg
			}
			privatechatAgg.SetChatBlockFilter(notify.NewChatBlockFilter(notifyAgg, chainSource))
		}
		skillserviceAgg := &skillservice.Aggregator{}
		// Wire skillservice -> userinfo before Init so startup index
		// reconciliation can resolve canonical provider identities.
		skillserviceAgg.SetProfileLookup(skillservice.NewUserInfoLookupAdapter(userinfoAgg))
		if err := aggRegistry.Register(skillserviceAgg); err != nil {
			log.Printf("WARNING: skillservice aggregator init failed: %v", err)
		}
		var publishedAgg *publishedcontent.Aggregator
		publishedCandidate := &publishedcontent.Aggregator{}
		publishedCandidate.SetProfileLookup(publishedcontent.NewUserInfoLookupAdapter(userinfoAgg))
		// Protocol registry knobs must land before Register: Init rebuilds
		// the by_protocol_path fold and applies the blocklist.
		publishedCandidate.SetProtocolRegistryBlocklist(cfg.ProtocolRegistry.Blocklist)
		publishedCandidate.SetProtocolOwnerBoundModifies(cfg.ProtocolRegistry.OwnerBoundModifies)
		if err := aggRegistry.Register(publishedCandidate); err != nil {
			log.Printf("WARNING: publishedcontent aggregator init failed: %v", err)
		} else {
			publishedAgg = publishedCandidate
		}
		botHomepageAgg = &bothomepage.Aggregator{}
		if err := aggRegistry.Register(botHomepageAgg); err != nil {
			log.Printf("WARNING: bothomepage aggregator init failed: %v", err)
		}
		idbotscaAgg := &idbotsca.Aggregator{}
		if err := aggRegistry.Register(idbotscaAgg); err != nil {
			log.Printf("WARNING: idbotsca aggregator init failed: %v", err)
			idbotscaAgg = nil
		} else {
			userinfoAgg.SetOfficialBadgeProvider(&userInfoBadgeAdapter{ca: idbotscaAgg})
			botHomepageAgg.SetOfficialBadgeProvider(&homepageBadgeAdapter{ca: idbotscaAgg})
			manapiURL := strings.TrimSpace(cfg.Mrc721.Nft.MANAPIBaseURL)
			if manapiURL == "" {
				manapiURL = strings.TrimSpace(cfg.BotHomepageV2Backfill.MANAPIBaseURL)
			}
			go func(ca *idbotsca.Aggregator, baseURL string) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				if err := ca.SyncFromMANAPI(ctx, baseURL, nil); err != nil {
					log.Printf("WARNING: idbotsca initial sync failed: %v", err)
				}
			}(idbotscaAgg, manapiURL)
		}
		privatechatAgg.SetProfileLookup(privatechat.NewUserInfoLookupAdapter(userinfoAgg))
		if groupChatAgg != nil {
			groupChatAgg.SetProfileLookup(groupchat.NewUserInfoLookupAdapter(userinfoAgg))
		}
		// Asset base URL turns chain-declared pin ids / metafile URIs
		// into HTTP URLs the Bot Hub frontend can load directly. The
		// value comes from METASO_P2P_ASSET_BASE_URL (default in
		// config.Default mirrors the documented recommendation).
		skillserviceAgg.SetAssetBaseURL(cfg.BotHub.AssetBaseURL)
		botHomepageAgg.SetProfileLookup(bothomepage.NewUserInfoLookupAdapter(userinfoAgg))
		userinfoAgg.SetProfileUpdatedHook(botHomepageAgg.InvalidateProfile)
		skillserviceAgg.SetServiceUpdatedHook(botHomepageAgg.InvalidateProfile)
		botHomepageAgg.SetServiceLister(skillserviceAgg)
		botHomepageAgg.SetHomepageServiceLister(skillserviceAgg)
		if publishedAgg != nil {
			botHomepageAgg.SetPublishedContentLister(publishedAgg)
		}
		botHomepageAgg.SetChatInteractionLister(privatechatAgg)
		botHomepageAgg.SetAssetBaseURL(cfg.BotHub.AssetBaseURL)
		// botsearch is a read-only aggregator over userinfo + groupchat +
		// skillservice; presence readers are injected after the socket server
		// and federation service exist (below, next to bothomepage).
		botSearchCandidate := &botsearch.Aggregator{}
		botSearchCandidate.SetProfileSource(userinfoAgg)
		botSearchCandidate.SetSkillLister(skillserviceAgg)
		if groupChatAgg != nil {
			botSearchCandidate.SetGroupHistorySource(groupChatAgg)
		}
		if idbotscaAgg != nil {
			botSearchCandidate.SetOfficialBadgeProvider(&botSearchBadgeAdapter{ca: idbotscaAgg})
		}
		if err := aggRegistry.Register(botSearchCandidate); err != nil {
			log.Printf("WARNING: botsearch aggregator init failed: %v", err)
		} else {
			botSearchAgg = botSearchCandidate
		}
		// metaweb is a read-only aggregator over the publishedcontent /
		// skillservice search-document snapshots; pin reads dispatch across
		// those local namespaces and fall back to MANAPI passthrough.
		metawebCandidate := &metaweb.Aggregator{}
		metawebCandidate.SetProfileNamer(metaweb.NewUserInfoProfileNamer(userinfoAgg))
		metawebCandidate.SetAssetResolver(skillservice.NewAssetResolver(cfg.BotHub.AssetBaseURL))
		metawebCandidate.SetRemotePinFetcher(metaweb.NewMANAPIPinFetcher(cfg.BotHomepageV2Backfill.MANAPIBaseURL, nil))
		metawebCandidate.SetDocumentSources(skillserviceAgg)
		metawebCandidate.SetPinLookups(nil, skillserviceAgg)
		if publishedAgg != nil {
			metawebCandidate.SetDocumentSources(publishedAgg, skillserviceAgg)
			metawebCandidate.SetPinLookups(publishedAgg, skillserviceAgg)
			metawebCandidate.SetFreshLookup(publishedAgg)
			metawebCandidate.SetProtocolRegistry(publishedAgg)
		}
		if err := aggRegistry.Register(metawebCandidate); err != nil {
			log.Printf("WARNING: metaweb aggregator init failed: %v", err)
		}
		// qa is the on-chain Q&A read model over SimpleQuestion /
		// SimpleAnswer with PayLike / PayComment engagement aggregation,
		// serving /api/qa/*; publisher enrichment reuses the metaweb
		// userinfo namer (structural interface).
		qaCandidate := &qa.Aggregator{}
		qaCandidate.SetProfileNamer(metaweb.NewUserInfoProfileNamer(userinfoAgg))
		if err := aggRegistry.Register(qaCandidate); err != nil {
			log.Printf("WARNING: qa aggregator init failed: %v", err)
		}
		// mrc721 is the MRC-721 business-index layer: the /protocols/
		// mrc721sell declaration log (latest-wins per itemPinId) behind
		// /api/mrc721/sell/*, and the collection search / issuer views over
		// the manapi-refreshed collection read model behind
		// /api/mrc721/collection/search and /api/mrc721/address/
		// collections-issued (docs/specs/2026-11-04-mrc721-sell-collection-api.md).
		// The /protocols/mrc721offer passive-offer index joins the same
		// aggregator behind METASO_P2P_MRC721_OFFER_ENABLED (default off).
		mrc721Candidate := &mrc721.Aggregator{}
		mrc721Candidate.SetOfferEnabled(cfg.Mrc721.Offer.Enabled)
		// Zero-conf declaration overlay gate (R-F-4/R-F-6): mempool sell and
		// offer pins take effect in the merged views when on.
		mrc721Candidate.SetZeroConfTrade(cfg.Mrc721.ZeroConfTrade)
		if err := aggRegistry.Register(mrc721Candidate); err != nil {
			log.Printf("WARNING: mrc721 aggregator init failed: %v", err)
		} else {
			mrc721Agg = mrc721Candidate
			mrc721Agg.SetProfileReader(&mrc721ProfileAdapter{ui: userinfoAgg})
		}
		if cfg.FtMarket.Enabled {
			ftmarketCandidate := &ftmarket.Aggregator{}
			ftmarketCandidate.SetZeroConfTrade(cfg.FtMarket.ZeroConfTrade)
			ftmarketCandidate.SetAssetBaseURL(cfg.BotHub.AssetBaseURL)
			if err := aggRegistry.Register(ftmarketCandidate); err != nil {
				log.Printf("WARNING: ftmarket aggregator init failed: %v", err)
			} else {
				ftmarketAgg = ftmarketCandidate
				if idbotscaAgg != nil {
					ftmarketAgg.SetCAReader(&ftmarketCAAdapter{ca: idbotscaAgg})
				}
				ftmarketAgg.SetProfileReader(&ftmarketProfileAdapter{ui: userinfoAgg})
				higunURL := strings.TrimSpace(cfg.FtMarket.HigunBaseURL)
				manURL := strings.TrimSpace(cfg.FtMarket.ManBaseURL)
				if manURL == "" {
					manURL = strings.TrimSpace(cfg.Mrc721.Nft.MANAPIBaseURL)
				}
				if higunURL != "" || manURL != "" {
					ftmarketAgg.SetUTXOVerifier(ftmarket.NewUpstreamUTXOVerifier(higunURL, manURL, nil))
					ftmarketAgg.SetTokenMetadataProvider(ftmarket.NewUpstreamTokenMetadataProvider(higunURL, manURL, nil))
				}
				if mmURL := strings.TrimSpace(cfg.FtMarket.MetaidMarketBaseURL); mmURL != "" {
					ftmarketAgg.SetBtcFloorProvider(ftmarket.NewMetaidMarketFloorProvider(mmURL, nil))
				}
				if cfg.FtMarket.UTXOSweepInterval > 0 && (higunURL != "" || manURL != "") {
					ftSweepStop := make(chan struct{})
					defer close(ftSweepStop)
					ftmarketAgg.StartUTXOSweeper(cfg.FtMarket.UTXOSweepInterval, ftSweepStop)
				}
			}
		}
		if idbotscaAgg != nil {
			if mrc721Agg != nil {
				mrc721Agg.SetCAReader(&mrc721CAAdapter{ca: idbotscaAgg})
			}
			idbotscaAgg.SetOnChange(func() {
				if mrc721Agg != nil {
					mrc721Agg.RefreshMarketCache()
				}
				if ftmarketAgg != nil {
					ftmarketAgg.RefreshMarketCache()
				}
			})
		}
		// paybuzz indexes /protocols/paybuzz encrypted articles (create/modify
		// version folding + owner identity verification), serving
		// GET /api/paybuzz/info and GET /api/paybuzz/list and feeding unified
		// MetaWeb search (protocol = "paybuzz").
		paybuzzCandidate := &paybuzz.Aggregator{}
		paybuzzCandidate.SetProfileLookup(paybuzz.NewUserInfoLookupAdapter(userinfoAgg))
		if err := aggRegistry.Register(paybuzzCandidate); err != nil {
			log.Printf("WARNING: paybuzz aggregator init failed: %v", err)
		} else {
			paybuzzAgg = paybuzzCandidate
		}
		// Wire unified metaweb search document sources (including mrc721, ftmarket, and paybuzz).
		docSources := []metaweb.DocumentSource{skillserviceAgg}
		if publishedAgg != nil {
			docSources = append([]metaweb.DocumentSource{publishedAgg}, docSources...)
		}
		if mrc721Agg != nil {
			docSources = append(docSources, mrc721Agg)
		}
		if ftmarketAgg != nil {
			docSources = append(docSources, ftmarketAgg)
		}
		if paybuzzAgg != nil {
			docSources = append(docSources, paybuzzAgg)
		}
		metawebCandidate.SetDocumentSources(docSources...)
		// mrc721nft is the native /nft/mrc721 NFT aggregate (collections,
		// items, transfer provenance) ported from man-p2p, serving the
		// manapi-compatible /api/mrc721 collection/item query surface. It
		// joins the registry only when enabled: until the historical
		// bootstrap has run, its read models are empty and the manapi-
		// refreshed mrc721 collection views remain the serving source for
		// the search endpoints.
		if cfg.Mrc721.Nft.Enabled {
			nftCandidate := &mrc721nft.Aggregator{}
			// Zero-confirmation gates (R-F-6): issuance and trade overlays
			// both default on (owner directive 2026-11-08); zeroConfTrade
			// switches trading back to confirmed-only via env + restart.
			nftCandidate.SetZeroConfGates(cfg.Mrc721.ZeroConfIssuance, cfg.Mrc721.ZeroConfTrade)
			if err := aggRegistry.Register(nftCandidate); err != nil {
				log.Printf("WARNING: mrc721nft aggregator init failed: %v", err)
			} else {
				nftCandidate.SetReplayClient(mrc721nft.NewUpstreamClient(cfg.Mrc721.Nft.MANAPIBaseURL, http.DefaultClient))
				nftCandidate.SetAddressResolver(&nftAddressResolverAdapter{ui: userinfoAgg})
				// The native aggregate is the bootstrapped read model: hand
				// the collection search / issuer endpoints over to it (same
				// wire contract, native source). Only meaningful after the
				// offline bootstrap has run — the operator enables the gate.
				if mrc721Agg != nil {
					mrc721Agg.SetNativeCollectionSource(nftCandidate)
					// The native item read model also backs the offer rows'
					// item → collection join (best-effort).
					mrc721Agg.SetItemCollectionSource(nftCandidate)
					mrc721Agg.SetNFTReader(nftCandidate)
					nftCandidate.SetTransferEventHook(mrc721Agg.OnItemTransferred)
					nftCandidate.SetOverlayReconciledHook(mrc721Agg.RefreshMarketCache)
					nftCandidate.SetCollectionMarketEnricher(mrc721Agg)
					nftCandidate.SetItemDetailEnricher(mrc721Agg)
				}
			}
		}
		// preference is the open comparison-feedback read model over
		// /protocols/preference/feedback, serving
		// GET /api/preference/comparisons and the open-preference summaries
		// behind /api/metaapp/list?sort=preference. The metaapp resolver
		// must be wired before Register: Init re-evaluates stored artifact
		// exclusions against the (already initialised) publishedcontent
		// store. Both wirings are guarded by a main-level source test; see
		// cmd/metaso-p2p/main_test.go.
		var preferenceCandidate *preference.Aggregator
		if publishedAgg != nil {
			preferenceCandidate = &preference.Aggregator{}
			preferenceCandidate.SetMetaAppResolver(preference.NewMetaAppResolverAdapter(publishedAgg))
			if err := aggRegistry.Register(preferenceCandidate); err != nil {
				preferenceCandidate = nil
				log.Printf("WARNING: preference aggregator init failed: %v", err)
			}
		} else {
			log.Printf("WARNING: preference aggregator skipped: publishedcontent aggregator unavailable")
		}
		if preferenceCandidate != nil {
			publishedCandidate.SetPreferenceLookup(preferenceCandidate)
		}
		// Best-effort fresh-feed engagement joins wire after qa exists; nil
		// aggregators degrade to zero counters, never to errors.
		metawebCandidate.SetEngagementLookups(metaweb.NewBuzzEngagementLookup(socialContentAgg), metaweb.NewQAEngagementLookup(qaCandidate))
		// Interactions-inbox sources: qa (answers/comments/likes on Q&A pins)
		// and socialcontent (comments/likes on buzz posts).
		interactionSources := []metaweb.InboxSource{qaCandidate}
		if socialContentAgg != nil {
			interactionSources = append(interactionSources, socialContentAgg)
		}
		metawebCandidate.SetInteractionSources(interactionSources...)
		if cfg.BotHomepageV2Backfill.Enabled && (publishedAgg != nil || userinfoAgg != nil) {
			go func() {
				if cfg.BotHomepageV2Backfill.Enabled {
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
				}
			}()
		}
		if cfg.SocialBackfill.Enabled {
			if socialAgg == nil {
				log.Printf("WARNING: social follow backfill requested but aggregator is unavailable")
			} else if err := runSocialBackfillIfEnabled(socialAgg, cfg.SocialBackfill); err != nil {
				log.Printf("WARNING: social follow backfill failed: %v", err)
			}
		}
		if cfg.BlockBackfill.Enabled {
			if blockAgg == nil {
				log.Printf("WARNING: block backfill requested but aggregator is unavailable")
			} else if err := runBlockBackfillIfEnabled(blockAgg, cfg.BlockBackfill); err != nil {
				log.Printf("WARNING: block backfill failed: %v", err)
			}
		}
		if cfg.SocialContentBackfill.Enabled {
			if socialContentAgg == nil {
				log.Printf("WARNING: social content backfill requested but aggregator is unavailable")
			} else {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), cfg.SocialContentBackfill.Timeout)
					err := socialContentAgg.Backfill(socialcontent.BackfillOptions{
						Context:  ctx,
						Client:   socialcontent.NewBackfillClient(cfg.SocialContentBackfill.MANAPIBaseURL, nil),
						Since:    time.Now().Add(-cfg.SocialContentBackfill.Lookback),
						PageSize: cfg.SocialContentBackfill.PageSize,
					})
					cancel()
					if err != nil {
						log.Printf("WARNING: social content backfill failed: %v", err)
					}
				}()
			}
		}
		startPublishedContentReplayIfEnabled(publishedAgg, cfg)
		// MRC-721 startup jobs (both opt-in via config): the sell-intent
		// declaration replay, and the periodic collection read-model
		// refresh behind the collection search / issuer views.
		if cfg.Mrc721.SellBackfill.Enabled {
			if mrc721Agg == nil {
				log.Printf("WARNING: mrc721 sell backfill requested but aggregator is unavailable")
			} else {
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), cfg.Mrc721.SellBackfill.Timeout)
					defer cancel()
					stats := &mrc721.SellBackfillStats{}
					err := mrc721Agg.BackfillSell(mrc721.SellBackfillOptions{
						Context:  ctx,
						Client:   publishedcontent.NewBackfillClient(cfg.Mrc721.SellBackfill.MANAPIBaseURL, http.DefaultClient),
						Since:    time.Now().Add(-cfg.Mrc721.SellBackfill.Lookback),
						PageSize: cfg.Mrc721.SellBackfill.PageSize,
						Stats:    stats,
					})
					if err != nil {
						log.Printf("WARNING: mrc721 sell backfill failed: %v", err)
					}
					log.Printf("mrc721 sell backfill done: fetched=%d applied=%d skipped=%d errors=%d",
						stats.Fetched, stats.Applied, stats.Skipped, stats.Errors)
				}()
			}
		}
		if cfg.Mrc721.Collection.Enabled {
			if mrc721Agg == nil {
				log.Printf("WARNING: mrc721 collection sync requested but aggregator is unavailable")
			} else {
				mrc721CollectionStop := make(chan struct{})
				defer close(mrc721CollectionStop)
				go mrc721.RunCollectionSyncLoop(
					context.Background(),
					mrc721Agg,
					mrc721.NewCollectionClient(cfg.Mrc721.Collection.MANAPIBaseURL, http.DefaultClient),
					cfg.Mrc721.Collection.SyncInterval,
					cfg.Mrc721.Collection.Timeout,
					cfg.Mrc721.Collection.PageSize,
					mrc721CollectionStop,
					log.Printf,
				)
			}
		}
		log.Printf("aggregators registered: %d", len(aggRegistry.All()))
	}

	// --- Indexer engine ---
	var idxEngine *indexer.Engine
	if store != nil && aggRegistry != nil && cfg.BlockIndex.Enabled {
		idxEngine = indexer.NewEngine(store, aggRegistry)
		idxEngine.ConfigureMempoolPolling(cfg.ZMQ.MempoolPollingEnabled, cfg.ZMQ.MempoolPollInterval, cfg.ZMQ.MempoolDedupeTTL)
		idxEngine.ConfigureMempoolFullScanInterval(cfg.ZMQ.MempoolFullScanInterval)
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

		// Mempool overlay janitors (spec 2026-10-29 §4.4b): drop social/block
		// overlay entries whose pin never confirmed past the expiry
		// threshold; sweep on a ticker until shutdown.
		sweepStop := make(chan struct{})
		if socialAgg != nil {
			socialAgg.StartMempoolSweeper(cfg.ZMQ.MempoolOverlaySweepInterval, cfg.ZMQ.MempoolOverlayExpiry, sweepStop)
		}
		if blockAgg != nil {
			blockAgg.StartMempoolSweeper(cfg.ZMQ.MempoolOverlaySweepInterval, cfg.ZMQ.MempoolOverlayExpiry, sweepStop)
		}
		defer close(sweepStop)
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

	if (botHomepageAgg != nil || botSearchAgg != nil || mrc721Agg != nil || ftmarketAgg != nil) && socketServer != nil {
		var globalReader presence.GlobalReader
		if federationService != nil {
			globalReader = federationService.GlobalReader()
		}
		if botHomepageAgg != nil {
			botHomepageAgg.SetPresenceReaders(socketServer.Manager(), globalReader)
		}
		if botSearchAgg != nil {
			botSearchAgg.SetPresenceReaders(socketServer.Manager(), globalReader)
		}
		if mrc721Agg != nil {
			mrc721Agg.SetPresenceReader(&mrc721PresenceAdapter{srv: socketServer})
		}
		if ftmarketAgg != nil {
			ftmarketAgg.SetPresenceReader(&mrc721PresenceAdapter{srv: socketServer})
		}
	}

	// --- HTTP router ---
	var txStatusResolver api.TxStatusResolver
	if idxEngine != nil {
		txStatusResolver = idxEngine
	}
	router := api.SetupRouter(cfg, store, cacheProvider, aggRegistry, socketServer, version, txStatusResolver)

	// --- Start HTTP server ---
	srv := &http.Server{
		Addr:              cfg.Service.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
	}
	var pprofServer *http.Server
	if addr := strings.TrimSpace(cfg.Service.PprofAddr); addr != "" {
		pprofServer = &http.Server{
			Addr:              addr,
			Handler:           http.DefaultServeMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Printf("pprof server listening on %s", addr)
			if err := pprofServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("pprof server error: %v", err)
			}
		}()
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
	if pprofServer != nil {
		if err := pprofServer.Shutdown(ctx); err != nil {
			log.Printf("pprof shutdown error: %v", err)
		}
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

// LookupLocalByIdentity resolves through the local reverse indexes only, so
// presence hydration never blocks on the remote profile fallback.
func (a *socketUserInfoLookupAdapter) LookupLocalByIdentity(identity string) (*socket.ProfileSnapshot, error) {
	if a == nil || a.ui == nil {
		return nil, nil
	}
	p, err := a.ui.LookupLocalByIdentity(identity)
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

// socialFollowAdapter exposes the social follow graph to the socialcontent
// read model for scope=following feed queries.
type socialFollowAdapter struct {
	agg *social.Aggregator
}

func (a *socialFollowAdapter) ListFollowing(globalMetaId string) ([]string, error) {
	if a == nil || a.agg == nil {
		return nil, errors.New("social follow aggregator unavailable")
	}
	var ids []string
	cursor := ""
	for {
		result, err := a.agg.ListFollowing(social.ListParams{
			GlobalMetaId: globalMetaId,
			Cursor:       cursor,
			Size:         100,
			View:         social.ViewCompact,
		})
		if err != nil {
			return nil, err
		}
		for _, item := range result.List {
			ids = append(ids, item.GlobalMetaId)
		}
		if result.NextCursor == "" || len(ids) >= 2000 {
			break
		}
		cursor = result.NextCursor
	}
	return ids, nil
}

type userInfoBadgeAdapter struct {
	ca *idbotsca.Aggregator
}

func (a *userInfoBadgeAdapter) GetOfficialBotBadge(globalMetaId string) *userinfo.OfficialBadge {
	if a == nil || a.ca == nil {
		return nil
	}
	b := a.ca.GetOfficialBotBadge(globalMetaId)
	if b == nil {
		return nil
	}
	return &userinfo.OfficialBadge{
		Verified:   b.Verified,
		Role:       b.Role,
		BadgeType:  b.BadgeType,
		BadgeLabel: b.BadgeLabel,
	}
}

type homepageBadgeAdapter struct {
	ca *idbotsca.Aggregator
}

func (a *homepageBadgeAdapter) GetOfficialBotBadge(globalMetaId string) *bothomepage.OfficialBadge {
	if a == nil || a.ca == nil {
		return nil
	}
	b := a.ca.GetOfficialBotBadge(globalMetaId)
	if b == nil {
		return nil
	}
	return &bothomepage.OfficialBadge{
		Verified:   b.Verified,
		Role:       b.Role,
		BadgeType:  b.BadgeType,
		BadgeLabel: b.BadgeLabel,
	}
}

type botSearchBadgeAdapter struct {
	ca *idbotsca.Aggregator
}

func (a *botSearchBadgeAdapter) GetOfficialBotBadge(globalMetaId string) *botsearch.OfficialBadge {
	if a == nil || a.ca == nil {
		return nil
	}
	b := a.ca.GetOfficialBotBadge(globalMetaId)
	if b == nil {
		return nil
	}
	return &botsearch.OfficialBadge{
		Verified:   b.Verified,
		Role:       b.Role,
		BadgeType:  b.BadgeType,
		BadgeLabel: b.BadgeLabel,
	}
}

type mrc721CAAdapter struct {
	ca *idbotsca.Aggregator
}

func (a *mrc721CAAdapter) IsCollectionCertified(collectionPinId string) (bool, string) {
	if a == nil || a.ca == nil {
		return false, ""
	}
	ok, entry := a.ca.IsCollectionCertified(collectionPinId)
	if !ok || entry == nil {
		return false, ""
	}
	title := entry.DisplayTitle
	if title == "" {
		title = entry.CollectionName
	}
	return true, title
}

func (a *mrc721CAAdapter) GetOfficialBotRole(globalMetaId string) (bool, string) {
	if a == nil || a.ca == nil {
		return false, ""
	}
	badge := a.ca.GetOfficialBotBadge(globalMetaId)
	if badge == nil || !badge.Verified {
		return false, ""
	}
	return true, badge.Role
}

type mrc721ProfileAdapter struct {
	ui *userinfo.Aggregator
}

func (a *mrc721ProfileAdapter) LookupProfile(identity string) *mrc721.SellerProfileSnapshot {
	if a == nil || a.ui == nil {
		return nil
	}
	p, _ := a.ui.LookupLocalByIdentity(identity)
	if p == nil {
		return nil
	}
	return &mrc721.SellerProfileSnapshot{
		Address:       p.Address,
		GlobalMetaId:  p.GlobalMetaID,
		MetaId:        p.MetaID,
		Name:          p.Name,
		Avatar:        p.Avatar,
		HasChatPubkey: strings.TrimSpace(p.ChatPublicKey) != "",
	}
}

func (a *mrc721ProfileAdapter) ResolveAddressesByGlobalMetaId(globalMetaId string) []string {
	if a == nil || a.ui == nil {
		return nil
	}
	p, _ := a.ui.LookupLocalByIdentity(globalMetaId)
	if p == nil || strings.TrimSpace(p.Address) == "" {
		return nil
	}
	return []string{strings.TrimSpace(p.Address)}
}

type nftAddressResolverAdapter struct {
	ui *userinfo.Aggregator
}

func (a *nftAddressResolverAdapter) ResolveAddressesByGlobalMetaId(globalMetaId string) []string {
	if a == nil || a.ui == nil {
		return nil
	}
	p, _ := a.ui.LookupLocalByIdentity(globalMetaId)
	if p == nil || strings.TrimSpace(p.Address) == "" {
		return nil
	}
	return []string{strings.TrimSpace(p.Address)}
}

type mrc721PresenceAdapter struct {
	srv *socket.Server
}

func (a *mrc721PresenceAdapter) PresenceStatus(identity string) (bool, *int64) {
	if a == nil || a.srv == nil {
		return false, nil
	}
	return a.srv.PresenceStatusByGlobalMetaId(identity)
}

type ftmarketCAAdapter struct {
	ca *idbotsca.Aggregator
}

func (a *ftmarketCAAdapter) IsTokenCertified(assetKey string) (bool, *ftmarket.CertifiedTokenInfo) {
	if a == nil || a.ca == nil {
		return false, nil
	}
	ok, entry := a.ca.IsTokenCertified(assetKey)
	if !ok || entry == nil {
		return false, nil
	}
	return true, &ftmarket.CertifiedTokenInfo{
		AssetKey:    entry.AssetKey,
		DisplayName: entry.DisplayName,
		Symbol:      entry.Symbol,
		TokenName:   entry.TokenName,
		Decimals:    entry.Decimals,
		Icon:        entry.Icon,
	}
}

func (a *ftmarketCAAdapter) GetOfficialBotRole(globalMetaId string) (bool, string) {
	if a == nil || a.ca == nil {
		return false, ""
	}
	badge := a.ca.GetOfficialBotBadge(globalMetaId)
	if badge == nil || !badge.Verified {
		return false, ""
	}
	return true, badge.Role
}

type ftmarketProfileAdapter struct {
	ui *userinfo.Aggregator
}

func (a *ftmarketProfileAdapter) LookupProfile(identity string) *ftmarket.SellerProfileSnapshot {
	if a == nil || a.ui == nil {
		return nil
	}
	p, _ := a.ui.LookupLocalByIdentity(identity)
	if p == nil {
		return nil
	}
	return &ftmarket.SellerProfileSnapshot{
		Address:       p.Address,
		GlobalMetaId:  p.GlobalMetaID,
		MetaId:        p.MetaID,
		Name:          p.Name,
		Avatar:        p.Avatar,
		HasChatPubkey: strings.TrimSpace(p.ChatPublicKey) != "",
	}
}

func (a *ftmarketProfileAdapter) ResolveAddressesByGlobalMetaId(globalMetaId string) []string {
	if a == nil || a.ui == nil {
		return nil
	}
	p, _ := a.ui.LookupLocalByIdentity(globalMetaId)
	if p == nil || strings.TrimSpace(p.Address) == "" {
		return nil
	}
	return []string{strings.TrimSpace(p.Address)}
}
