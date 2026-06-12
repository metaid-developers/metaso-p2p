package main

import (
	"reflect"
	"testing"
	"time"

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
