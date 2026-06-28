package main

import (
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/aggregator/skillservice"
	"github.com/metaid-developers/metaso-p2p/internal/config"
)

func TestParseOptionsUsesExplicitSinceOverLookback(t *testing.T) {
	cfg := config.Default()
	cfg.Pebble.DataDir = "/data/prod"
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

	opts, err := parseOptions(cfg, []string{
		"--since", "2026-02-28T00:00:00Z",
		"--lookback", "24h",
		"--manapi-base-url", "https://manapi.example",
	}, now)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	wantSince := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)
	if !opts.Since.Equal(wantSince) {
		t.Fatalf("Since = %s, want %s", opts.Since, wantSince)
	}
	if opts.DataDir != "/data/prod" {
		t.Fatalf("DataDir = %q, want /data/prod", opts.DataDir)
	}
	if opts.MANAPIBaseURL != "https://manapi.example" {
		t.Fatalf("MANAPIBaseURL = %q, want override", opts.MANAPIBaseURL)
	}
}

func TestParseOptionsDefaultsToFourMonthLookbackAndSkillServicePath(t *testing.T) {
	cfg := config.Default()
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)

	opts, err := parseOptions(cfg, nil, now)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}

	if !opts.Since.Equal(now.Add(-defaultLookback)) {
		t.Fatalf("Since = %s, want %s", opts.Since, now.Add(-defaultLookback))
	}
	if len(opts.Paths) != 1 || opts.Paths[0] != skillservice.PathSkillService {
		t.Fatalf("Paths = %#v, want only %s", opts.Paths, skillservice.PathSkillService)
	}
	if opts.Timeout != defaultTimeout {
		t.Fatalf("Timeout = %s, want %s", opts.Timeout, defaultTimeout)
	}
}
