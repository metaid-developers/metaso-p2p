// Command metaso-p2p-preference-backfill replays the on-chain history of
// /protocols/preference/feedback from MANAPI through the preference read
// model (oldest-first, so latest-per-signer/pair converges to the same state
// as live confirmation order). ArtifactRef validation resolves against the
// metaapp records already in the store, so the publishedcontent aggregator is
// initialised first and the metaapp history must already be backfilled (run
// the metaweb/metaapp backfill first when bootstrapping a fresh data dir).
// Idempotent, safe to re-run; on completion it prints a per-chain report
// (fetched / applied / skipped / errors).
//
// See docs/specs/2026-10-24-motion-video-preference-api.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/preference"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	defaultTimeout  = 8 * time.Hour
	defaultPageSize = 100
)

type runOptions struct {
	DataDir       string
	MANAPIBaseURL string
	Since         time.Time // zero = full history
	Timeout       time.Duration
	PageSize      int
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("preference backfill: %v", err)
	}
}

func run(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	opts, err := parseOptions(cfg, args, time.Now())
	if err != nil {
		return err
	}

	store := storage.NewPebbleStore(opts.DataDir)
	defer func() {
		if err := store.Close(); err != nil {
			log.Printf("WARNING: close pebble store: %v", err)
		}
	}()
	cacheProvider := cache.New(store)

	publishedAgg := &publishedcontent.Aggregator{}
	if err := publishedAgg.Init(store, cacheProvider); err != nil {
		return fmt.Errorf("init publishedcontent aggregator: %w", err)
	}
	prefAgg := &preference.Aggregator{}
	prefAgg.SetMetaAppResolver(preference.NewMetaAppResolverAdapter(publishedAgg))
	if err := prefAgg.Init(store, cacheProvider); err != nil {
		return fmt.Errorf("init preference aggregator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	sinceText := "full-history"
	if !opts.Since.IsZero() {
		sinceText = opts.Since.UTC().Format(time.RFC3339)
	}
	log.Printf("preference backfill start: dataDir=%s manapi=%s since=%s pageSize=%d",
		opts.DataDir, opts.MANAPIBaseURL, sinceText, opts.PageSize)

	client := publishedcontent.NewBackfillClient(opts.MANAPIBaseURL, nil)
	stats := &preference.BackfillStats{}
	if err := prefAgg.Backfill(preference.BackfillOptions{
		Context:  ctx,
		Client:   client,
		Since:    opts.Since,
		PageSize: opts.PageSize,
		Stats:    stats,
	}); err != nil {
		return err
	}
	printReport(stats)
	log.Printf("preference backfill done")
	return nil
}

func printReport(stats *preference.BackfillStats) {
	if stats == nil {
		return
	}
	for path, byChain := range stats.Paths {
		chains := make([]string, 0, len(byChain))
		for chainName := range byChain {
			chains = append(chains, chainName)
		}
		sort.Strings(chains)
		for _, chainName := range chains {
			s := byChain[chainName]
			log.Printf("[preference-backfill] done path=%s chain=%s fetched=%d applied=%d skipped=%d errors=%d",
				path, chainName, s.Fetched, s.Applied, s.Skipped, s.Errors)
		}
	}
}

func parseOptions(cfg config.Config, args []string, now time.Time) (runOptions, error) {
	fs := flag.NewFlagSet("metaso-p2p-preference-backfill", flag.ContinueOnError)

	dataDir := strings.TrimSpace(cfg.Pebble.DataDir)
	manapiBaseURL := strings.TrimSpace(cfg.BotHomepageV2Backfill.MANAPIBaseURL)
	if manapiBaseURL == "" {
		manapiBaseURL = "https://manapi.metaid.io"
	}

	var sinceText string
	var lookback time.Duration
	timeout := defaultTimeout
	pageSize := defaultPageSize

	fs.StringVar(&dataDir, "data-dir", dataDir, "Pebble data directory")
	fs.StringVar(&manapiBaseURL, "manapi-base-url", manapiBaseURL, "MANAPI base URL")
	fs.StringVar(&sinceText, "since", "", "inclusive cutoff time, RFC3339 or YYYY-MM-DD (empty = full history)")
	fs.DurationVar(&lookback, "lookback", 0, "lookback window used only when --since is empty (0 = full history)")
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
	if pageSize <= 0 {
		return runOptions{}, errors.New("page-size must be greater than zero")
	}
	if timeout <= 0 {
		return runOptions{}, errors.New("timeout must be greater than zero")
	}
	if lookback < 0 {
		return runOptions{}, errors.New("lookback must not be negative")
	}

	var since time.Time
	if strings.TrimSpace(sinceText) != "" {
		parsed, err := parseSince(sinceText)
		if err != nil {
			return runOptions{}, err
		}
		since = parsed
	} else if lookback > 0 {
		since = now.Add(-lookback)
	}

	return runOptions{
		DataDir:       strings.TrimSpace(dataDir),
		MANAPIBaseURL: strings.TrimRight(strings.TrimSpace(manapiBaseURL), "/"),
		Since:         since,
		Timeout:       timeout,
		PageSize:      pageSize,
	}, nil
}

func parseSince(value string) (time.Time, error) {
	text := strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339, text); err == nil {
		return parsed, nil
	}
	if parsed, err := time.Parse("2006-01-02", text); err == nil {
		return parsed, nil
	}
	return time.Time{}, fmt.Errorf("invalid since %q: use RFC3339 or YYYY-MM-DD", value)
}
