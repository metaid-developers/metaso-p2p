package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

type options struct {
	dataDir    string
	timeout    time.Duration
	verifyOnly bool
}

func parseOptions(args []string, output io.Writer) (options, error) {
	var opts options
	flags := flag.NewFlagSet("metaso-p2p-privatechat-index-backfill", flag.ContinueOnError)
	flags.SetOutput(output)
	flags.StringVar(&opts.dataDir, "data-dir", "", "Pebble root containing privatechat and userinfo namespaces (required)")
	flags.DurationVar(&opts.timeout, "timeout", 60*time.Minute, "maximum backfill or verification duration")
	flags.BoolVar(&opts.verifyOnly, "verify-only", false, "verify the ready read model without rebuilding it")
	flags.Usage = func() {
		fmt.Fprintln(output, "Usage: metaso-p2p-privatechat-index-backfill --data-dir PATH [--timeout 60m] [--verify-only]")
		fmt.Fprintln(output, "The metaso-p2p service MUST be stopped before rebuilding its production database.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return opts, err
	}
	opts.dataDir = strings.TrimSpace(opts.dataDir)
	if opts.dataDir == "" {
		return opts, errors.New("--data-dir is required")
	}
	if opts.timeout <= 0 {
		return opts, errors.New("--timeout must be positive")
	}
	if flags.NArg() != 0 {
		return opts, fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	return opts, nil
}

func run(ctx context.Context, opts options, output io.Writer) error {
	store := storage.NewPebbleStore(opts.dataDir)
	defer store.Close()
	cacheProvider := cache.New(store)

	userInfoAggregator := &userinfo.Aggregator{}
	if err := userInfoAggregator.Init(store, cacheProvider); err != nil {
		return fmt.Errorf("initialize userinfo: %w", err)
	}
	privateChatAggregator := &privatechat.Aggregator{}
	if err := privateChatAggregator.Init(store, cacheProvider); err != nil {
		return fmt.Errorf("initialize privatechat: %w", err)
	}
	privateChatAggregator.SetProfileLookup(privatechat.NewUserInfoLookupAdapter(userInfoAggregator))

	var (
		report privatechat.PrivateChatReadModelReport
		err    error
	)
	if opts.verifyOnly {
		report, err = privateChatAggregator.VerifyPrivateChatReadModel(ctx)
	} else {
		report, err = privateChatAggregator.BackfillPrivateChatReadModel(ctx)
	}
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, string(encoded))
	return err
}

func main() {
	opts, err := parseOptions(os.Args[1:], os.Stderr)
	if err != nil {
		log.Printf("invalid options: %v", err)
		os.Exit(2)
	}
	parent, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(parent, opts.timeout)
	defer cancel()
	if err := run(ctx, opts, os.Stdout); err != nil {
		log.Printf("private chat read model failed: %v", err)
		os.Exit(1)
	}
}
