package config

import (
	"testing"
	"time"
)

// The block backfill is a maintenance path over a currently-empty chain
// inventory: it must stay disabled by default (spec §4.4 of
// docs/specs/2026-10-29-social-friend-block-api.md).
func TestDefaultBlockBackfillConfigDisabled(t *testing.T) {
	cfg := Default()

	if cfg.BlockBackfill.Enabled {
		t.Fatal("expected block backfill to be disabled by default")
	}
	if cfg.BlockBackfill.Lookback != 1440*time.Hour {
		t.Fatalf("expected default lookback 1440h, got %s", cfg.BlockBackfill.Lookback)
	}
	if cfg.BlockBackfill.Timeout != 2*time.Minute {
		t.Fatalf("expected default timeout 2m, got %s", cfg.BlockBackfill.Timeout)
	}
	if cfg.BlockBackfill.PageSize != 100 {
		t.Fatalf("expected default page size 100, got %d", cfg.BlockBackfill.PageSize)
	}
	if cfg.BlockBackfill.MANAPIBaseURL != "https://manapi.metaid.io" {
		t.Fatalf("expected default MANAPI base URL, got %q", cfg.BlockBackfill.MANAPIBaseURL)
	}
}

func TestBlockBackfillEnvOverride(t *testing.T) {
	t.Setenv("METASO_P2P_BLOCK_BACKFILL_ENABLED", "true")
	t.Setenv("METASO_P2P_BLOCK_BACKFILL_PAGE_SIZE", "50")
	t.Setenv("METASO_P2P_BLOCK_BACKFILL_MANAPI_BASE_URL", "https://manapi.example.org")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.BlockBackfill.Enabled {
		t.Fatal("expected env override to enable block backfill")
	}
	if cfg.BlockBackfill.PageSize != 50 {
		t.Fatalf("expected env page size 50, got %d", cfg.BlockBackfill.PageSize)
	}
	if cfg.BlockBackfill.MANAPIBaseURL != "https://manapi.example.org" {
		t.Fatalf("expected env MANAPI base URL, got %q", cfg.BlockBackfill.MANAPIBaseURL)
	}
}
