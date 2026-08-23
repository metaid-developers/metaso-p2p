// Command metaso-p2p-metaweb-backfill replays the SimpleNote and metaprotocol
// pin history from MANAPI through the publishedcontent indexer so both
// protocols become searchable via /api/metaweb/search and readable via
// /api/metaweb/pin/:pinId. It opens the Pebble store directly and replays
// through the same handlers as live indexing — idempotent, safe to re-run.
//
// Backfill scope is full history by default (both protocols are young and
// early tutorial content must not be skipped); --since / --lookback exist
// for operational narrowing. On completion the command prints a per-chain
// report (pins fetched, records upserted, modifies, revokes, errors).
//
// See docs/specs/2026-08-23-simplenote-metaprotocol-indexing.md.
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
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	defaultTimeout  = 8 * time.Hour
	defaultPageSize = 100
)

var defaultPaths = publishedcontent.PathSimpleNote + "," + publishedcontent.PathMetaProtocol

type runOptions struct {
	DataDir       string
	MANAPIBaseURL string
	Since         time.Time // zero = full history
	Timeout       time.Duration
	PageSize      int
	Paths         []string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("metaweb backfill: %v", err)
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

	agg := &publishedcontent.Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		return fmt.Errorf("init publishedcontent aggregator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	sinceText := "full-history"
	if !opts.Since.IsZero() {
		sinceText = opts.Since.UTC().Format(time.RFC3339)
	}
	log.Printf("metaweb backfill start: dataDir=%s manapi=%s since=%s pageSize=%d paths=%s",
		opts.DataDir, opts.MANAPIBaseURL, sinceText, opts.PageSize, strings.Join(opts.Paths, ","))

	stats := &publishedcontent.BackfillStats{}
	err = agg.Backfill(publishedcontent.BackfillOptions{
		Context:  ctx,
		Client:   publishedcontent.NewBackfillClient(opts.MANAPIBaseURL, nil),
		Paths:    opts.Paths,
		Since:    opts.Since,
		PageSize: opts.PageSize,
		Stats:    stats,
	})
	if err != nil {
		return err
	}
	printReport(stats)
	return nil
}

// printReport logs the completion table required by the IDBots contract:
// per path, per chain — pins fetched, records upserted, modifies, revokes,
// errors.
func printReport(stats *publishedcontent.BackfillStats) {
	if stats == nil {
		return
	}
	paths := make([]string, 0, len(stats.Paths))
	for path := range stats.Paths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		byChain := stats.Paths[path]
		chains := make([]string, 0, len(byChain))
		for chainName := range byChain {
			chains = append(chains, chainName)
		}
		sort.Strings(chains)
		for _, chainName := range chains {
			s := byChain[chainName]
			log.Printf("[metaweb-backfill] done path=%s chain=%s fetched=%d upserted=%d modified=%d revoked=%d errors=%d",
				path, chainName, s.Fetched, s.Upserted, s.Modified, s.Revoked, s.Errors)
		}
	}
}

func parseOptions(cfg config.Config, args []string, now time.Time) (runOptions, error) {
	fs := flag.NewFlagSet("metaso-p2p-metaweb-backfill", flag.ContinueOnError)

	dataDir := strings.TrimSpace(cfg.Pebble.DataDir)
	manapiBaseURL := strings.TrimSpace(cfg.BotHomepageV2Backfill.MANAPIBaseURL)
	if manapiBaseURL == "" {
		manapiBaseURL = "https://manapi.metaid.io"
	}

	var sinceText string
	var lookback time.Duration
	timeout := defaultTimeout
	pageSize := defaultPageSize
	pathsText := defaultPaths

	fs.StringVar(&dataDir, "data-dir", dataDir, "Pebble data directory")
	fs.StringVar(&manapiBaseURL, "manapi-base-url", manapiBaseURL, "MANAPI base URL")
	fs.StringVar(&sinceText, "since", "", "inclusive cutoff time, RFC3339 or YYYY-MM-DD (empty = full history)")
	fs.DurationVar(&lookback, "lookback", 0, "lookback window used only when --since is empty (0 = full history)")
	fs.DurationVar(&timeout, "timeout", timeout, "overall backfill timeout")
	fs.IntVar(&pageSize, "page-size", pageSize, "MANAPI page size")
	fs.StringVar(&pathsText, "paths", pathsText, "comma-separated protocol paths")
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
	paths := parsePaths(pathsText)
	if len(paths) == 0 {
		return runOptions{}, errors.New("paths is required")
	}

	return runOptions{
		DataDir:       strings.TrimSpace(dataDir),
		MANAPIBaseURL: strings.TrimRight(strings.TrimSpace(manapiBaseURL), "/"),
		Since:         since,
		Timeout:       timeout,
		PageSize:      pageSize,
		Paths:         paths,
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

func parsePaths(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		path := strings.TrimSpace(part)
		if path == "" {
			continue
		}
		out = append(out, path)
	}
	return out
}
