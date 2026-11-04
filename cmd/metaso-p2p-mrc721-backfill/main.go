// Command metaso-p2p-mrc721-backfill builds the MRC-721 business indexes
// offline:
//
//   - sell-intent replay: pages the MANAPI history of the fixed
//     /protocols/mrc721sell declaration log (newest-first) and replays it
//     oldest-first through the latest-wins read model behind
//     GET /api/mrc721/sell/intents and GET /api/mrc721/sell/intent/info.
//   - collection refresh: sweeps the manapi-compatible
//     /api/mrc721/collection/pageList and upserts every row into the read
//     model behind GET /api/mrc721/collection/search and
//     GET /api/mrc721/address/collections-issued.
//
// The command is idempotent and safe to re-run while the daemon is stopped
// (same Pebble namespace). See docs/specs/2026-11-04-mrc721-sell-collection-api.md.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	defaultTimeout  = 8 * time.Hour
	defaultPageSize = 100
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("mrc721 backfill: %v", err)
	}
}

func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("metaso-p2p-mrc721-backfill", flag.ContinueOnError)
	dataDir := fs.String("data-dir", cfg.Pebble.DataDir, "Pebble data directory of the metaso-p2p daemon")
	manapiBaseURL := fs.String("manapi-base-url", cfg.Mrc721.SellBackfill.MANAPIBaseURL, "MANAPI base URL for the sell-intent path listing")
	collectionBaseURL := fs.String("collection-base-url", cfg.Mrc721.Collection.MANAPIBaseURL, "Base URL of the manapi-compatible MRC-721 collection page list")
	lookback := fs.Duration("lookback", 0, "only replay sell pins newer than this age (default 0 = full history)")
	timeout := fs.Duration("timeout", defaultTimeout, "overall run timeout")
	pageSize := fs.Int("page-size", defaultPageSize, "MANAPI page size")
	skipSell := fs.Bool("skip-sell", false, "skip the sell-intent replay")
	skipCollection := fs.Bool("skip-collection", false, "skip the collection refresh")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *skipSell && *skipCollection {
		return errors.New("nothing to do: both --skip-sell and --skip-collection are set")
	}

	// Full history by default (the protocol is new, so a first build wants
	// everything; same stance as the qa backfill). A positive --lookback
	// narrows the replay window.
	since := time.Time{}
	if *lookback > 0 {
		since = time.Now().Add(-*lookback)
	}

	store := storage.NewPebbleStore(*dataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close pebble store: %v", err)
		}
	}()
	cacheProvider := cache.New(store)

	agg := &mrc721.Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	sinceText := "full-history"
	if !since.IsZero() {
		sinceText = since.UTC().Format(time.RFC3339)
	}
	log.Printf("mrc721 backfill start: dataDir=%s manapi=%s collection=%s since=%s pageSize=%d",
		*dataDir, *manapiBaseURL, *collectionBaseURL, sinceText, *pageSize)

	if !*skipSell {
		stats := &mrc721.SellBackfillStats{}
		err := agg.BackfillSell(mrc721.SellBackfillOptions{
			Context:  ctx,
			Client:   publishedcontent.NewBackfillClient(*manapiBaseURL, nil),
			Since:    since,
			PageSize: *pageSize,
			Stats:    stats,
		})
		if err != nil {
			return err
		}
		log.Printf("[mrc721-backfill] sell done path=%s fetched=%d applied=%d skipped=%d errors=%d",
			mrc721.PathSell, stats.Fetched, stats.Applied, stats.Skipped, stats.Errors)
	}

	if !*skipCollection {
		syncStats, err := agg.SyncCollections(ctx, mrc721.NewCollectionClient(*collectionBaseURL, nil), *pageSize)
		if err != nil {
			if syncStats == nil || syncStats.Upserted == 0 {
				return err
			}
			log.Printf("WARNING: collection refresh ended with error after progress: %v", err)
		}
		if syncStats != nil {
			log.Printf("[mrc721-backfill] collection done pages=%d fetched=%d upserted=%d skipped=%d",
				syncStats.Pages, syncStats.Fetched, syncStats.Upserted, syncStats.Skipped)
		}
	}

	return nil
}
