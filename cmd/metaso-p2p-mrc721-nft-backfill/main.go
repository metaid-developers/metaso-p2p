// Command metaso-p2p-mrc721-nft-backfill bootstraps the native MRC-721 NFT
// aggregate offline from the manapi-compatible upstream (man-p2p) read views:
//
//   - phase 1 sweeps /api/mrc721/collection/pageList into the album
//     records (c:/cp:),
//   - phase 2 sweeps each album's /api/mrc721/collection/items/pageList
//     into the item records plus holder index (i:/ci:/oi:),
//   - phase 3 sweeps each item's /api/mrc721/item/transfers into the
//     provenance history (th:).
//
// Under the issuer-only protocol revision
// (docs/mrc721-protocol-revision-issuer-only.md) the hydrate is a filtered
// projection of the upstream open-mint index: item rows not minted by their
// album issuer are skipped and the surviving items are renumbered densely
// per album. Every phase is idempotent and independently re-runnable via
// --skip-*; run with the daemon stopped (same Pebble namespace).
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/mrc721nft"
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
		log.Fatalf("mrc721-nft backfill: %v", err)
	}
}

func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	fs := flag.NewFlagSet("metaso-p2p-mrc721-nft-backfill", flag.ContinueOnError)
	dataDir := fs.String("data-dir", cfg.Pebble.DataDir, "Pebble data directory of the metaso-p2p daemon")
	manapiBaseURL := fs.String("manapi-base-url", cfg.Mrc721.Nft.MANAPIBaseURL, "Base URL of the manapi-compatible MRC-721 upstream")
	timeout := fs.Duration("timeout", defaultTimeout, "overall run timeout")
	pageSize := fs.Int("page-size", defaultPageSize, "upstream page size (max 200)")
	skipCollections := fs.Bool("skip-collections", false, "skip phase 1 (collection registry)")
	skipItems := fs.Bool("skip-items", false, "skip phase 2 (item state)")
	skipTransfers := fs.Bool("skip-transfers", false, "skip phase 3 (transfer history)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *skipCollections && *skipItems && *skipTransfers {
		return errors.New("nothing to do: all --skip-* flags are set")
	}
	if *pageSize > 200 {
		*pageSize = 200 // upstream clamp
	}

	store := storage.NewPebbleStore(*dataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close pebble store: %v", err)
		}
	}()
	cacheProvider := cache.New(store)

	agg := &mrc721nft.Aggregator{}
	if err := agg.Init(store, cacheProvider); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	log.Printf("mrc721-nft backfill start: dataDir=%s upstream=%s pageSize=%d",
		*dataDir, *manapiBaseURL, *pageSize)

	stats := &mrc721nft.BackfillStats{}
	err = agg.BackfillNft(mrc721nft.BackfillOptions{
		Context:  ctx,
		Client:   mrc721nft.NewUpstreamClient(*manapiBaseURL, nil),
		PageSize: *pageSize,
		Stats:    stats,
	})
	if err != nil {
		return err
	}

	log.Printf("[mrc721-nft-backfill] done: collections=%d items=%d ownerMoves=%d transfers=%d skippedTransfers=%d skippedNonIssuer=%d",
		stats.Collections, stats.Items, stats.OwnerMoves, stats.Transfers, stats.SkippedTransfers, stats.SkippedNonIssuer)
	return nil
}
