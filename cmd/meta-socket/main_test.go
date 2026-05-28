package main

import (
	"reflect"
	"testing"

	"github.com/metaid-developers/meta-socket/internal/config"
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
