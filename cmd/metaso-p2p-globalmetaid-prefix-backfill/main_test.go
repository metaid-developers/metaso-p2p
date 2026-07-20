package main

import (
	"testing"
	"time"

	"github.com/metaid-developers/metaso-p2p/internal/config"
)

func TestParseOptionsUsesConfigAndExplicitOverrides(t *testing.T) {
	cfg := config.Default()
	cfg.Pebble.DataDir = "/data/prod"
	cfg.BotHomepageV2Backfill.MANAPIBaseURL = "https://configured.example/"

	opts, err := parseOptions(cfg, []string{
		"--manapi-base-url", "https://override.example/",
		"--timeout", "2h",
		"--page-size", "250",
	})
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.DataDir != "/data/prod" {
		t.Fatalf("DataDir = %q, want /data/prod", opts.DataDir)
	}
	if opts.MANAPIBaseURL != "https://override.example" {
		t.Fatalf("MANAPIBaseURL = %q, want trimmed override", opts.MANAPIBaseURL)
	}
	if opts.Timeout != 2*time.Hour {
		t.Fatalf("Timeout = %s, want 2h", opts.Timeout)
	}
	if opts.PageSize != 250 {
		t.Fatalf("PageSize = %d, want 250", opts.PageSize)
	}
}

func TestParseOptionsDefaults(t *testing.T) {
	cfg := config.Default()
	opts, err := parseOptions(cfg, nil)
	if err != nil {
		t.Fatalf("parseOptions: %v", err)
	}
	if opts.Timeout != defaultTimeout || opts.PageSize != defaultPageSize {
		t.Fatalf("defaults = timeout %s pageSize %d", opts.Timeout, opts.PageSize)
	}
	if opts.MANAPIBaseURL != "https://manapi.metaid.io" {
		t.Fatalf("MANAPIBaseURL = %q", opts.MANAPIBaseURL)
	}
}

func TestParseOptionsRejectsInvalidValues(t *testing.T) {
	cfg := config.Default()
	for _, args := range [][]string{
		{"--data-dir", ""},
		{"--timeout", "0s"},
		{"--page-size", "0"},
	} {
		if _, err := parseOptions(cfg, args); err == nil {
			t.Fatalf("parseOptions(%v) returned nil error", args)
		}
	}
}
