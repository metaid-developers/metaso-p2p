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

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	defaultLookback = 120 * 24 * time.Hour
	defaultTimeout  = 30 * time.Minute
	defaultPageSize = 100
)

type runOptions struct {
	DataDir       string
	MANAPIBaseURL string
	Since         time.Time
	Timeout       time.Duration
	PageSize      int
	Paths         []string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("skillservice backfill: %v", err)
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

	agg := &skillservice.Aggregator{}
	if err := agg.Init(store, cache.New(store)); err != nil {
		return fmt.Errorf("init skillservice aggregator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()

	log.Printf("skillservice backfill start: dataDir=%s manapi=%s since=%s pageSize=%d paths=%s",
		opts.DataDir, opts.MANAPIBaseURL, opts.Since.UTC().Format(time.RFC3339), opts.PageSize, strings.Join(opts.Paths, ","))
	err = agg.Backfill(skillservice.BackfillOptions{
		Context:  ctx,
		Client:   skillservice.NewBackfillClient(opts.MANAPIBaseURL, nil),
		Paths:    opts.Paths,
		Since:    opts.Since,
		PageSize: opts.PageSize,
	})
	if err != nil {
		return err
	}
	log.Printf("skillservice backfill complete: since=%s", opts.Since.UTC().Format(time.RFC3339))
	return nil
}

func parseOptions(cfg config.Config, args []string, now time.Time) (runOptions, error) {
	fs := flag.NewFlagSet("metaso-p2p-skillservice-backfill", flag.ContinueOnError)

	dataDir := strings.TrimSpace(cfg.Pebble.DataDir)
	manapiBaseURL := strings.TrimSpace(cfg.BotHomepageV2Backfill.MANAPIBaseURL)
	if manapiBaseURL == "" {
		manapiBaseURL = "https://manapi.metaid.io"
	}

	var sinceText string
	lookback := defaultLookback
	timeout := defaultTimeout
	pageSize := defaultPageSize
	pathsText := skillservice.PathSkillService

	fs.StringVar(&dataDir, "data-dir", dataDir, "Pebble data directory")
	fs.StringVar(&manapiBaseURL, "manapi-base-url", manapiBaseURL, "MANAPI base URL")
	fs.StringVar(&sinceText, "since", "", "inclusive cutoff time, RFC3339 or YYYY-MM-DD")
	fs.DurationVar(&lookback, "lookback", lookback, "lookback window used when --since is empty")
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
	if lookback <= 0 {
		return runOptions{}, errors.New("lookback must be greater than zero")
	}

	since := now.Add(-lookback)
	if strings.TrimSpace(sinceText) != "" {
		parsed, err := parseSince(sinceText)
		if err != nil {
			return runOptions{}, err
		}
		since = parsed
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
