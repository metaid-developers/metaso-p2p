package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/social"
	"github.com/metaid-developers/metaso-p2p/internal/config"
)

func TestEnabledBlockIndexChainNames(t *testing.T) {
	cfg := config.BlockIndexConfig{
		BTC:   config.ChainRPCConfig{Enabled: true},
		MVC:   config.ChainRPCConfig{Enabled: true},
		DOGE:  config.ChainRPCConfig{Enabled: true},
		OPCAT: config.ChainRPCConfig{Enabled: true},
	}

	got := enabledBlockIndexChainNames(cfg)
	want := []string{"btc", "mvc", "doge", "opcat"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("enabledBlockIndexChainNames() = %#v, want %#v", got, want)
	}
}

func TestLoadPublishedContentReplayConfigDefaultDisabled(t *testing.T) {
	cfg := loadPublishedContentReplayConfig()
	if cfg.Enabled {
		t.Fatal("publishedcontent replay should be disabled by default")
	}
	if cfg.ChainName != "mvc" {
		t.Fatalf("default chain = %q, want mvc", cfg.ChainName)
	}
	if !reflect.DeepEqual(cfg.ProtocolPaths, []string{"/protocols/simplebuzz"}) {
		t.Fatalf("default protocol paths = %#v, want simplebuzz", cfg.ProtocolPaths)
	}
	if cfg.Lookback != 1440*time.Hour {
		t.Fatalf("default lookback = %s, want 1440h", cfg.Lookback)
	}
	if cfg.ProgressEvery != 100 {
		t.Fatalf("default progress every = %d, want 100", cfg.ProgressEvery)
	}
}

func TestLoadPublishedContentReplayConfigFromEnv(t *testing.T) {
	t.Setenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_ENABLED", "true")
	t.Setenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_CHAIN", "mvc")
	t.Setenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_PROTOCOLS", "/protocols/simplebuzz,/protocols/metaapp")
	t.Setenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_FROM_HEIGHT", "170000")
	t.Setenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_TO_HEIGHT", "171000")
	t.Setenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_LOOKBACK", "720h")
	t.Setenv("METASO_P2P_PUBLISHEDCONTENT_REPLAY_PROGRESS_EVERY", "25")

	cfg := loadPublishedContentReplayConfig()
	if !cfg.Enabled {
		t.Fatal("publishedcontent replay should be enabled from env")
	}
	if cfg.ChainName != "mvc" {
		t.Fatalf("chain = %q, want mvc", cfg.ChainName)
	}
	if !reflect.DeepEqual(cfg.ProtocolPaths, []string{"/protocols/simplebuzz", "/protocols/metaapp"}) {
		t.Fatalf("protocol paths = %#v", cfg.ProtocolPaths)
	}
	if cfg.FromHeight != 170000 || cfg.ToHeight != 171000 {
		t.Fatalf("height range = %d..%d, want 170000..171000", cfg.FromHeight, cfg.ToHeight)
	}
	if cfg.Lookback != 720*time.Hour {
		t.Fatalf("lookback = %s, want 720h", cfg.Lookback)
	}
	if cfg.ProgressEvery != 25 {
		t.Fatalf("progress every = %d, want 25", cfg.ProgressEvery)
	}
}

type fakeSocialBackfiller struct {
	called  chan struct{}
	release chan struct{}
	opts    social.BackfillOptions
	err     error
}

func (f *fakeSocialBackfiller) Backfill(opts social.BackfillOptions) error {
	f.opts = opts
	if f.called != nil {
		close(f.called)
	}
	if f.release != nil {
		<-f.release
	}
	return f.err
}

func TestRunSocialBackfillIfEnabledWaitsForCompletion(t *testing.T) {
	fake := &fakeSocialBackfiller{
		called:  make(chan struct{}),
		release: make(chan struct{}),
	}
	cfg := config.SocialBackfillConfig{
		Enabled:       true,
		Lookback:      720 * time.Hour,
		Timeout:       time.Minute,
		PageSize:      55,
		MANAPIBaseURL: "https://manapi.example",
	}

	done := make(chan error, 1)
	go func() {
		done <- runSocialBackfillIfEnabled(fake, cfg)
	}()

	<-fake.called
	select {
	case err := <-done:
		t.Fatalf("runSocialBackfillIfEnabled returned before backfill completed: %v", err)
	default:
	}

	close(fake.release)
	if err := <-done; err != nil {
		t.Fatalf("runSocialBackfillIfEnabled returned error: %v", err)
	}
	if fake.opts.Client == nil {
		t.Fatal("expected MANAPI client to be set")
	}
	if fake.opts.PageSize != 55 {
		t.Fatalf("page size = %d, want 55", fake.opts.PageSize)
	}
	if fake.opts.Since.IsZero() {
		t.Fatal("expected non-zero since")
	}
	if fake.opts.Context == nil {
		t.Fatal("expected context to be set")
	}
	if _, ok := fake.opts.Context.Deadline(); !ok {
		t.Fatal("expected context deadline to be set")
	}
}

func TestRunSocialBackfillIfEnabledDisabledSkipsCall(t *testing.T) {
	fake := &fakeSocialBackfiller{
		called: make(chan struct{}),
	}

	if err := runSocialBackfillIfEnabled(fake, config.SocialBackfillConfig{}); err != nil {
		t.Fatalf("runSocialBackfillIfEnabled returned error: %v", err)
	}

	select {
	case <-fake.called:
		t.Fatal("expected disabled social backfill to skip Backfill call")
	default:
	}
}

func TestRunSocialBackfillIfEnabledPropagatesError(t *testing.T) {
	fake := &fakeSocialBackfiller{err: context.DeadlineExceeded}
	cfg := config.SocialBackfillConfig{
		Enabled:       true,
		Lookback:      24 * time.Hour,
		Timeout:       time.Second,
		PageSize:      10,
		MANAPIBaseURL: "https://manapi.example",
	}

	if err := runSocialBackfillIfEnabled(fake, cfg); err != context.DeadlineExceeded {
		t.Fatalf("runSocialBackfillIfEnabled err = %v, want %v", err, context.DeadlineExceeded)
	}
}
