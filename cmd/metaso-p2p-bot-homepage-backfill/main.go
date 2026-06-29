package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/privatechat"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/publishedcontent"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/aggregator/userinfo"
	"github.com/metaid-developers/metaso-p2p/internal/cache"
	"github.com/metaid-developers/metaso-p2p/internal/config"
	"github.com/metaid-developers/metaso-p2p/internal/storage"
)

const (
	defaultLookback = 240 * 24 * time.Hour
	defaultTimeout  = 60 * time.Minute
	defaultPageSize = 100
)

var supportedSectionOrder = []string{"profile", "services", "metaapps", "chats", "buzzes", "skills"}

type runOptions struct {
	DataDir       string
	MANAPIBaseURL string
	Since         time.Time
	Timeout       time.Duration
	PageSize      int
	Sections      []string

	UserInfoPaths         []string
	SkillServicePaths     []string
	PublishedContentPaths []string
	PrivateChatPaths      []string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("bot homepage backfill: %v", err)
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

	log.Printf("bot homepage backfill start: dataDir=%s manapi=%s since=%s timeout=%s pageSize=%d sections=%s userinfo=%s skillservice=%s publishedcontent=%s privatechat=%s",
		opts.DataDir,
		opts.MANAPIBaseURL,
		opts.Since.UTC().Format(time.RFC3339),
		opts.Timeout,
		opts.PageSize,
		strings.Join(opts.Sections, ","),
		strings.Join(opts.UserInfoPaths, ","),
		strings.Join(opts.SkillServicePaths, ","),
		strings.Join(opts.PublishedContentPaths, ","),
		strings.Join(opts.PrivateChatPaths, ","))

	if len(opts.UserInfoPaths) > 0 {
		agg := &userinfo.Aggregator{}
		if err := agg.Init(store, cacheProvider); err != nil {
			return fmt.Errorf("init userinfo aggregator: %w", err)
		}
		if err := runWithTimeout(opts.Timeout, func(ctx context.Context) error {
			return agg.Backfill(userinfo.BackfillOptions{
				Context:  ctx,
				Client:   userinfo.NewBackfillClient(opts.MANAPIBaseURL, nil),
				Paths:    opts.UserInfoPaths,
				Since:    opts.Since,
				PageSize: opts.PageSize,
			})
		}); err != nil {
			return fmt.Errorf("userinfo backfill: %w", err)
		}
	}

	if len(opts.SkillServicePaths) > 0 {
		agg := &skillservice.Aggregator{}
		if err := agg.Init(store, cacheProvider); err != nil {
			return fmt.Errorf("init skillservice aggregator: %w", err)
		}
		if err := runWithTimeout(opts.Timeout, func(ctx context.Context) error {
			return agg.Backfill(skillservice.BackfillOptions{
				Context:  ctx,
				Client:   skillservice.NewBackfillClient(opts.MANAPIBaseURL, nil),
				Paths:    opts.SkillServicePaths,
				Since:    opts.Since,
				PageSize: opts.PageSize,
			})
		}); err != nil {
			return fmt.Errorf("skillservice backfill: %w", err)
		}
	}

	if len(opts.PublishedContentPaths) > 0 {
		agg := &publishedcontent.Aggregator{}
		if err := agg.Init(store, cacheProvider); err != nil {
			return fmt.Errorf("init publishedcontent aggregator: %w", err)
		}
		if err := runWithTimeout(opts.Timeout, func(ctx context.Context) error {
			return agg.Backfill(publishedcontent.BackfillOptions{
				Context:  ctx,
				Client:   publishedcontent.NewBackfillClient(opts.MANAPIBaseURL, nil),
				Paths:    opts.PublishedContentPaths,
				Since:    opts.Since,
				PageSize: opts.PageSize,
			})
		}); err != nil {
			return fmt.Errorf("publishedcontent backfill: %w", err)
		}
	}

	if len(opts.PrivateChatPaths) > 0 {
		agg := &privatechat.Aggregator{}
		if err := agg.Init(store, cacheProvider); err != nil {
			return fmt.Errorf("init privatechat aggregator: %w", err)
		}
		if err := runWithTimeout(opts.Timeout, func(ctx context.Context) error {
			return agg.Backfill(privatechat.BackfillOptions{
				Context:  ctx,
				Client:   privatechat.NewBackfillClient(opts.MANAPIBaseURL, nil),
				Paths:    opts.PrivateChatPaths,
				Since:    opts.Since,
				PageSize: opts.PageSize,
			})
		}); err != nil {
			return fmt.Errorf("privatechat backfill: %w", err)
		}
	}

	log.Printf("bot homepage backfill complete: since=%s", opts.Since.UTC().Format(time.RFC3339))
	return nil
}

func runWithTimeout(timeout time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return fn(ctx)
}

func parseOptions(cfg config.Config, args []string, now time.Time) (runOptions, error) {
	fs := flag.NewFlagSet("metaso-p2p-bot-homepage-backfill", flag.ContinueOnError)

	dataDir := strings.TrimSpace(cfg.Pebble.DataDir)
	manapiBaseURL := strings.TrimSpace(cfg.BotHomepageV2Backfill.MANAPIBaseURL)
	if manapiBaseURL == "" {
		manapiBaseURL = "https://manapi.metaid.io"
	}

	var sinceText string
	lookback := defaultLookback
	timeout := defaultTimeout
	pageSize := defaultPageSize
	sectionsText := "all"
	pathsText := ""

	fs.StringVar(&dataDir, "data-dir", dataDir, "Pebble data directory")
	fs.StringVar(&manapiBaseURL, "manapi-base-url", manapiBaseURL, "MANAPI base URL")
	fs.StringVar(&sinceText, "since", "", "inclusive cutoff time, RFC3339 or YYYY-MM-DD")
	fs.DurationVar(&lookback, "lookback", lookback, "lookback window used when --since is empty")
	fs.DurationVar(&timeout, "timeout", timeout, "overall timeout for each selected aggregator backfill")
	fs.IntVar(&pageSize, "page-size", pageSize, "MANAPI page size")
	fs.StringVar(&sectionsText, "sections", sectionsText, "comma-separated sections: all,profile,services,metaapps,chats,buzzes,skills")
	fs.StringVar(&pathsText, "paths", pathsText, "comma-separated protocol/info paths; overrides --sections when set")
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

	opts := runOptions{
		DataDir:       strings.TrimSpace(dataDir),
		MANAPIBaseURL: strings.TrimRight(strings.TrimSpace(manapiBaseURL), "/"),
		Since:         since,
		Timeout:       timeout,
		PageSize:      pageSize,
	}

	if strings.TrimSpace(pathsText) != "" {
		if err := applyPaths(&opts, parseCSV(pathsText)); err != nil {
			return runOptions{}, err
		}
		return opts, nil
	}
	if err := applySections(&opts, parseCSV(sectionsText)); err != nil {
		return runOptions{}, err
	}
	return opts, nil
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

func parseCSV(value string) []string {
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func applySections(opts *runOptions, sections []string) error {
	if len(sections) == 0 {
		sections = []string{"all"}
	}
	expanded := make([]string, 0, len(supportedSectionOrder))
	seen := make(map[string]struct{})
	for _, section := range sections {
		normalised := strings.ToLower(strings.TrimSpace(section))
		if normalised == "all" {
			for _, item := range supportedSectionOrder {
				addUniqueString(&expanded, seen, item)
			}
			continue
		}
		if !slices.Contains(supportedSectionOrder, normalised) {
			return fmt.Errorf("unsupported section %q", section)
		}
		addUniqueString(&expanded, seen, normalised)
	}

	opts.Sections = expanded
	for _, section := range expanded {
		switch section {
		case "profile":
			opts.UserInfoPaths = appendUniqueStrings(opts.UserInfoPaths, userinfo.DefaultBackfillPaths()...)
		case "services":
			opts.SkillServicePaths = appendUniqueStrings(opts.SkillServicePaths, skillservice.DefaultBackfillPaths()...)
		case "metaapps":
			opts.PublishedContentPaths = appendUniqueStrings(opts.PublishedContentPaths, publishedcontent.PathMetaApp)
		case "chats":
			opts.PrivateChatPaths = appendUniqueStrings(opts.PrivateChatPaths, privatechat.HomepageSimpleMsgProtocolPath)
		case "buzzes":
			opts.PublishedContentPaths = appendUniqueStrings(opts.PublishedContentPaths, publishedcontent.PathSimpleBuzz)
		case "skills":
			opts.PublishedContentPaths = appendUniqueStrings(opts.PublishedContentPaths, publishedcontent.PathMetaBotSkill)
		}
	}
	return nil
}

func applyPaths(opts *runOptions, paths []string) error {
	if len(paths) == 0 {
		return errors.New("paths is required")
	}
	for _, path := range paths {
		normalised := strings.TrimSpace(path)
		switch {
		case slices.Contains(userinfo.DefaultBackfillPaths(), normalised):
			opts.UserInfoPaths = appendUniqueStrings(opts.UserInfoPaths, normalised)
		case slices.Contains(skillservice.DefaultBackfillPaths(), normalised):
			opts.SkillServicePaths = appendUniqueStrings(opts.SkillServicePaths, normalised)
		case normalised == publishedcontent.PathMetaApp || normalised == publishedcontent.PathSimpleBuzz || normalised == publishedcontent.PathMetaBotSkill:
			opts.PublishedContentPaths = appendUniqueStrings(opts.PublishedContentPaths, normalised)
		case strings.EqualFold(normalised, privatechat.HomepageSimpleMsgProtocolPath):
			opts.PrivateChatPaths = appendUniqueStrings(opts.PrivateChatPaths, privatechat.HomepageSimpleMsgProtocolPath)
		default:
			return fmt.Errorf("unsupported path %q", path)
		}
	}
	return nil
}

func appendUniqueStrings(values []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(values)+len(additions))
	for _, value := range values {
		seen[value] = struct{}{}
	}
	for _, addition := range additions {
		addUniqueString(&values, seen, addition)
	}
	return values
}

func addUniqueString(values *[]string, seen map[string]struct{}, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if _, ok := seen[value]; ok {
		return
	}
	seen[value] = struct{}{}
	*values = append(*values, value)
}
