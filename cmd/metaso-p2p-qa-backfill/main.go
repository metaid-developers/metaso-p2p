// Command metaso-p2p-qa-backfill replays the on-chain Q&A history from
// MANAPI through the qa read model: /protocols/simplequestion,
// /protocols/simpleanswer, /protocols/paylike and /protocols/paycomment, in
// that order and oldest-first, so question⇄answer joins settle and the
// last-state-per-publisher like aggregation converges. The two content
// protocols additionally replay through the generic publishedcontent
// pipeline so full bodies are readable via /api/metaweb/pin/:pinId and both
// become searchable via /api/metaweb/search. Idempotent, safe to re-run.
//
// Both protocols are new, so backfill scope is full history by default;
// --since / --lookback exist for operational narrowing. On completion the
// command prints a per-path, per-chain report (fetched / applied / errors
// for qa; fetched / upserted / modified / revoked / errors for
// publishedcontent).
//
// See docs/specs/2026-09-07-metaweb-qa-api.md.
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

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/qa"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	defaultTimeout  = 8 * time.Hour
	defaultPageSize = 100
)

var qaBackfillPaths = []string{qa.PathSimpleQuestion, qa.PathSimpleAnswer, qa.PathPayLike, qa.PathPayComment}

var publishedBackfillPaths = []string{publishedcontent.PathSimpleQuestion, publishedcontent.PathSimpleAnswer}

type runOptions struct {
	DataDir       string
	MANAPIBaseURL string
	Since         time.Time // zero = full history
	Timeout       time.Duration
	PageSize      int
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("qa backfill: %v", err)
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

	qaAgg := &qa.Aggregator{}
	if err := qaAgg.Init(store, cacheProvider); err != nil {
		return fmt.Errorf("init qa aggregator: %w", err)
	}
	publishedAgg := &publishedcontent.Aggregator{}
	if err := publishedAgg.Init(store, cacheProvider); err != nil {
		return fmt.Errorf("init publishedcontent aggregator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	sinceText := "full-history"
	if !opts.Since.IsZero() {
		sinceText = opts.Since.UTC().Format(time.RFC3339)
	}
	log.Printf("qa backfill start: dataDir=%s manapi=%s since=%s pageSize=%d",
		opts.DataDir, opts.MANAPIBaseURL, sinceText, opts.PageSize)

	client := publishedcontent.NewBackfillClient(opts.MANAPIBaseURL, nil)

	qaStats := &qa.BackfillStats{}
	if err := qaAgg.Backfill(qa.BackfillOptions{
		Context:  ctx,
		Client:   client,
		Since:    opts.Since,
		PageSize: opts.PageSize,
		Stats:    qaStats,
	}); err != nil {
		return err
	}
	printQAReport(qaStats)

	// Generic pipeline replay for the two content protocols (pin-read full
	// bodies + unified metaweb search documents).
	publishedStats := &publishedcontent.BackfillStats{}
	if err := publishedAgg.Backfill(publishedcontent.BackfillOptions{
		Context:  ctx,
		Client:   client,
		Paths:    publishedBackfillPaths,
		Since:    opts.Since,
		PageSize: opts.PageSize,
		Stats:    publishedStats,
	}); err != nil {
		return err
	}
	printPublishedReport(publishedStats)
	return nil
}

func printQAReport(stats *qa.BackfillStats) {
	if stats == nil {
		return
	}
	for _, path := range qaBackfillPaths {
		byChain, ok := stats.Paths[path]
		if !ok {
			continue
		}
		chains := make([]string, 0, len(byChain))
		for chainName := range byChain {
			chains = append(chains, chainName)
		}
		sort.Strings(chains)
		for _, chainName := range chains {
			s := byChain[chainName]
			log.Printf("[qa-backfill] done path=%s chain=%s fetched=%d applied=%d errors=%d",
				path, chainName, s.Fetched, s.Applied, s.Errors)
		}
	}
}

func printPublishedReport(stats *publishedcontent.BackfillStats) {
	if stats == nil {
		return
	}
	for _, path := range publishedBackfillPaths {
		byChain, ok := stats.Paths[path]
		if !ok {
			continue
		}
		chains := make([]string, 0, len(byChain))
		for chainName := range byChain {
			chains = append(chains, chainName)
		}
		sort.Strings(chains)
		for _, chainName := range chains {
			s := byChain[chainName]
			log.Printf("[qa-backfill:published] done path=%s chain=%s fetched=%d upserted=%d modified=%d revoked=%d errors=%d",
				path, chainName, s.Fetched, s.Upserted, s.Modified, s.Revoked, s.Errors)
		}
	}
}

func parseOptions(cfg config.Config, args []string, now time.Time) (runOptions, error) {
	fs := flag.NewFlagSet("metaso-p2p-qa-backfill", flag.ContinueOnError)

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
