package config

import (
	"testing"
	"time"
)

// The MRC-721 index knobs must stay off by default: the sell-intent backfill
// and the collection refresh loop are opt-in operations (docs/specs/
// 2026-11-04-mrc721-sell-collection-api.md).
func TestDefaultMrc721ConfigDisabled(t *testing.T) {
	cfg := Default()

	if cfg.Mrc721.SellBackfill.Enabled {
		t.Fatal("expected mrc721 sell backfill to be disabled by default")
	}
	if cfg.Mrc721.SellBackfill.Lookback != 1440*time.Hour {
		t.Fatalf("expected default sell lookback 1440h, got %s", cfg.Mrc721.SellBackfill.Lookback)
	}
	if cfg.Mrc721.SellBackfill.Timeout != 2*time.Minute {
		t.Fatalf("expected default sell timeout 2m, got %s", cfg.Mrc721.SellBackfill.Timeout)
	}
	if cfg.Mrc721.SellBackfill.PageSize != 100 {
		t.Fatalf("expected default sell page size 100, got %d", cfg.Mrc721.SellBackfill.PageSize)
	}
	if cfg.Mrc721.SellBackfill.MANAPIBaseURL != "https://manapi.metaid.io" {
		t.Fatalf("expected default sell MANAPI base URL, got %q", cfg.Mrc721.SellBackfill.MANAPIBaseURL)
	}

	if cfg.Mrc721.Collection.Enabled {
		t.Fatal("expected mrc721 collection sync to be disabled by default")
	}
	if cfg.Mrc721.Collection.SyncInterval != 10*time.Minute {
		t.Fatalf("expected default collection sync interval 10m, got %s", cfg.Mrc721.Collection.SyncInterval)
	}
	if cfg.Mrc721.Collection.Timeout != time.Minute {
		t.Fatalf("expected default collection timeout 1m, got %s", cfg.Mrc721.Collection.Timeout)
	}
	if cfg.Mrc721.Collection.PageSize != 100 {
		t.Fatalf("expected default collection page size 100, got %d", cfg.Mrc721.Collection.PageSize)
	}
	if cfg.Mrc721.Collection.MANAPIBaseURL != "https://manapi.metaid.io" {
		t.Fatalf("expected default collection MANAPI base URL, got %q", cfg.Mrc721.Collection.MANAPIBaseURL)
	}
}

func TestMrc721EnvOverride(t *testing.T) {
	t.Setenv("METASO_P2P_MRC721_SELL_BACKFILL_ENABLED", "true")
	t.Setenv("METASO_P2P_MRC721_SELL_BACKFILL_PAGE_SIZE", "50")
	t.Setenv("METASO_P2P_MRC721_SELL_BACKFILL_MANAPI_BASE_URL", "https://manapi.example.org")
	t.Setenv("METASO_P2P_MRC721_COLLECTION_ENABLED", "true")
	t.Setenv("METASO_P2P_MRC721_COLLECTION_SYNC_INTERVAL", "2m")
	t.Setenv("METASO_P2P_MRC721_COLLECTION_MANAPI_BASE_URL", "https://manapi.example.org")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Mrc721.SellBackfill.Enabled {
		t.Fatal("expected env override to enable the sell backfill")
	}
	if cfg.Mrc721.SellBackfill.PageSize != 50 {
		t.Fatalf("expected env sell page size 50, got %d", cfg.Mrc721.SellBackfill.PageSize)
	}
	if cfg.Mrc721.SellBackfill.MANAPIBaseURL != "https://manapi.example.org" {
		t.Fatalf("expected env sell MANAPI base URL, got %q", cfg.Mrc721.SellBackfill.MANAPIBaseURL)
	}
	if !cfg.Mrc721.Collection.Enabled {
		t.Fatal("expected env override to enable collection sync")
	}
	if cfg.Mrc721.Collection.SyncInterval != 2*time.Minute {
		t.Fatalf("expected env collection sync interval 2m, got %s", cfg.Mrc721.Collection.SyncInterval)
	}
	if cfg.Mrc721.Collection.MANAPIBaseURL != "https://manapi.example.org" {
		t.Fatalf("expected env collection MANAPI base URL, got %q", cfg.Mrc721.Collection.MANAPIBaseURL)
	}
}

func TestMrc721Validation(t *testing.T) {
	// applyStringEnv never clears a non-empty default, so the base-URL
	// requirement is exercised through the page-size guard instead.
	t.Setenv("METASO_P2P_MRC721_SELL_BACKFILL_PAGE_SIZE", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to reject a non-positive mrc721 sell backfill page size")
	}

	t.Setenv("METASO_P2P_MRC721_SELL_BACKFILL_PAGE_SIZE", "100")
	t.Setenv("METASO_P2P_MRC721_COLLECTION_PAGE_SIZE", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected Load to reject a non-positive mrc721 collection page size")
	}
}
