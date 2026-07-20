package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	defaultTimeout  = 60 * time.Minute
	defaultPageSize = 100
)

type runOptions struct {
	DataDir       string
	MANAPIBaseURL string
	Timeout       time.Duration
	PageSize      int
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("GlobalMetaID prefix backfill: %v", err)
	}
}

func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	opts, err := parseOptions(cfg, args)
	if err != nil {
		return err
	}

	store := storage.NewPebbleStore(opts.DataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close Pebble store: %v", err)
		}
	}()
	agg := &userinfo.Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		return fmt.Errorf("init userinfo aggregator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	log.Printf("GlobalMetaID prefix backfill start: dataDir=%s manapi=%s pageSize=%d timeout=%s",
		opts.DataDir, opts.MANAPIBaseURL, opts.PageSize, opts.Timeout)
	summary, err := agg.BackfillGlobalMetaIDPrefix(userinfo.GlobalMetaIDPrefixBackfillOptions{
		Context:  ctx,
		Client:   userinfo.NewBackfillClient(opts.MANAPIBaseURL, nil),
		PageSize: opts.PageSize,
	})
	if err != nil {
		return err
	}
	log.Printf("GlobalMetaID prefix backfill complete: status=%s indexed=%d duplicate=%d replaced=%d invalid=%d missingTimestamp=%d",
		summary.Status,
		summary.IndexedCount,
		summary.DuplicateCount,
		summary.ReplacedCount,
		summary.InvalidCount,
		summary.MissingTimestampCount)
	return nil
}

func parseOptions(cfg config.Config, args []string) (runOptions, error) {
	fs := flag.NewFlagSet("metaso-p2p-globalmetaid-prefix-backfill", flag.ContinueOnError)
	dataDir := strings.TrimSpace(cfg.Pebble.DataDir)
	manapiBaseURL := strings.TrimSpace(cfg.BotHomepageV2Backfill.MANAPIBaseURL)
	if manapiBaseURL == "" {
		manapiBaseURL = "https://manapi.metaid.io"
	}
	timeout := defaultTimeout
	pageSize := defaultPageSize
	fs.StringVar(&dataDir, "data-dir", dataDir, "Pebble data directory; stop metaso-p2p before opening its production database")
	fs.StringVar(&manapiBaseURL, "manapi-base-url", manapiBaseURL, "MANAPI base URL")
	fs.DurationVar(&timeout, "timeout", timeout, "overall backfill timeout")
	fs.IntVar(&pageSize, "page-size", pageSize, "MANAPI page size")
	if err := fs.Parse(args); err != nil {
		return runOptions{}, err
	}
	if strings.TrimSpace(dataDir) == "" {
		return runOptions{}, errors.New("data-dir is required")
	}
	if strings.TrimSpace(manapiBaseURL) == "" {
		return runOptions{}, errors.New("manapi-base-url is required")
	}
	if timeout <= 0 {
		return runOptions{}, errors.New("timeout must be greater than zero")
	}
	if pageSize <= 0 {
		return runOptions{}, errors.New("page-size must be greater than zero")
	}
	return runOptions{
		DataDir:       strings.TrimSpace(dataDir),
		MANAPIBaseURL: strings.TrimRight(strings.TrimSpace(manapiBaseURL), "/"),
		Timeout:       timeout,
		PageSize:      pageSize,
	}, nil
}
