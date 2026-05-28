package config

import (
	"strconv"
	"testing"
)

func TestLoadBlockIndexEnv(t *testing.T) {
	t.Setenv("META_SOCKET_BLOCK_INDEX_ENABLED", "true")

	chains := []struct {
		name          string
		enabledEnv    string
		hostEnv       string
		userEnv       string
		passEnv       string
		postModeEnv   string
		disableTLSEnv string
		heightEnv     string
		assert        func(t *testing.T, cfg Config)
	}{
		{
			name:          "btc",
			enabledEnv:    "META_SOCKET_BLOCK_INDEX_BTC_ENABLED",
			hostEnv:       "META_SOCKET_BLOCK_INDEX_BTC_RPC_HOST",
			userEnv:       "META_SOCKET_BLOCK_INDEX_BTC_RPC_USER",
			passEnv:       "META_SOCKET_BLOCK_INDEX_BTC_RPC_PASS",
			postModeEnv:   "META_SOCKET_BLOCK_INDEX_BTC_RPC_HTTP_POST_MODE",
			disableTLSEnv: "META_SOCKET_BLOCK_INDEX_BTC_RPC_DISABLE_TLS",
			heightEnv:     "META_SOCKET_BLOCK_INDEX_BTC_INITIAL_HEIGHT",
			assert: func(t *testing.T, cfg Config) {
				assertChainRPCConfig(t, cfg.BlockIndex.BTC, "btc.example:8332", "btc-user", "btc-pass", 101)
			},
		},
		{
			name:          "mvc",
			enabledEnv:    "META_SOCKET_BLOCK_INDEX_MVC_ENABLED",
			hostEnv:       "META_SOCKET_BLOCK_INDEX_MVC_RPC_HOST",
			userEnv:       "META_SOCKET_BLOCK_INDEX_MVC_RPC_USER",
			passEnv:       "META_SOCKET_BLOCK_INDEX_MVC_RPC_PASS",
			postModeEnv:   "META_SOCKET_BLOCK_INDEX_MVC_RPC_HTTP_POST_MODE",
			disableTLSEnv: "META_SOCKET_BLOCK_INDEX_MVC_RPC_DISABLE_TLS",
			heightEnv:     "META_SOCKET_BLOCK_INDEX_MVC_INITIAL_HEIGHT",
			assert: func(t *testing.T, cfg Config) {
				assertChainRPCConfig(t, cfg.BlockIndex.MVC, "mvc.example:9882", "mvc-user", "mvc-pass", 202)
			},
		},
		{
			name:          "doge",
			enabledEnv:    "META_SOCKET_BLOCK_INDEX_DOGE_ENABLED",
			hostEnv:       "META_SOCKET_BLOCK_INDEX_DOGE_RPC_HOST",
			userEnv:       "META_SOCKET_BLOCK_INDEX_DOGE_RPC_USER",
			passEnv:       "META_SOCKET_BLOCK_INDEX_DOGE_RPC_PASS",
			postModeEnv:   "META_SOCKET_BLOCK_INDEX_DOGE_RPC_HTTP_POST_MODE",
			disableTLSEnv: "META_SOCKET_BLOCK_INDEX_DOGE_RPC_DISABLE_TLS",
			heightEnv:     "META_SOCKET_BLOCK_INDEX_DOGE_INITIAL_HEIGHT",
			assert: func(t *testing.T, cfg Config) {
				assertChainRPCConfig(t, cfg.BlockIndex.DOGE, "doge.example:23116", "doge-user", "doge-pass", 303)
			},
		},
		{
			name:          "opcat",
			enabledEnv:    "META_SOCKET_BLOCK_INDEX_OPCAT_ENABLED",
			hostEnv:       "META_SOCKET_BLOCK_INDEX_OPCAT_RPC_HOST",
			userEnv:       "META_SOCKET_BLOCK_INDEX_OPCAT_RPC_USER",
			passEnv:       "META_SOCKET_BLOCK_INDEX_OPCAT_RPC_PASS",
			postModeEnv:   "META_SOCKET_BLOCK_INDEX_OPCAT_RPC_HTTP_POST_MODE",
			disableTLSEnv: "META_SOCKET_BLOCK_INDEX_OPCAT_RPC_DISABLE_TLS",
			heightEnv:     "META_SOCKET_BLOCK_INDEX_OPCAT_INITIAL_HEIGHT",
			assert: func(t *testing.T, cfg Config) {
				assertChainRPCConfig(t, cfg.BlockIndex.OPCAT, "opcat.example:18443", "opcat-user", "opcat-pass", 404)
			},
		},
	}

	for i, chain := range chains {
		t.Setenv(chain.enabledEnv, "true")
		t.Setenv(chain.hostEnv, chain.name+".example:8332")
		if chain.name == "mvc" {
			t.Setenv(chain.hostEnv, "mvc.example:9882")
		}
		if chain.name == "doge" {
			t.Setenv(chain.hostEnv, "doge.example:23116")
		}
		if chain.name == "opcat" {
			t.Setenv(chain.hostEnv, "opcat.example:18443")
		}
		t.Setenv(chain.userEnv, chain.name+"-user")
		t.Setenv(chain.passEnv, chain.name+"-pass")
		t.Setenv(chain.postModeEnv, "false")
		t.Setenv(chain.disableTLSEnv, "false")
		t.Setenv(chain.heightEnv, strconv.Itoa((i+1)*101))
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.BlockIndex.Enabled {
		t.Fatal("expected block index master toggle to load from env")
	}

	for _, chain := range chains {
		t.Run(chain.name, func(t *testing.T) {
			chain.assert(t, cfg)
		})
	}
}

func TestLoadOPCATZMQEnv(t *testing.T) {
	t.Setenv("META_SOCKET_ZMQ_ENABLED", "true")
	t.Setenv("META_SOCKET_ZMQ_OPCAT_ENABLED", "true")
	t.Setenv("META_SOCKET_ZMQ_OPCAT_ENDPOINT", "tcp://opcat.example:18442")
	t.Setenv("META_SOCKET_ZMQ_OPCAT_TOPIC", "hashblock")
	t.Setenv("META_SOCKET_ZMQ_OPCAT_RPC_HOST", "opcat.example:18443")
	t.Setenv("META_SOCKET_ZMQ_OPCAT_RPC_USER", "opcat-user")
	t.Setenv("META_SOCKET_ZMQ_OPCAT_RPC_PASS", "opcat-pass")
	t.Setenv("META_SOCKET_ZMQ_OPCAT_RPC_HTTP_POST_MODE", "false")
	t.Setenv("META_SOCKET_ZMQ_OPCAT_RPC_DISABLE_TLS", "false")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !cfg.ZMQ.Enabled {
		t.Fatal("expected ZMQ master toggle to load from env")
	}
	if !cfg.ZMQ.OPCAT.Enabled {
		t.Fatal("expected OPCAT ZMQ toggle to load from env")
	}
	if cfg.ZMQ.OPCAT.Endpoint != "tcp://opcat.example:18442" {
		t.Fatalf("expected OPCAT endpoint to load from env, got %q", cfg.ZMQ.OPCAT.Endpoint)
	}
	if cfg.ZMQ.OPCAT.Topic != "hashblock" {
		t.Fatalf("expected OPCAT topic to load from env, got %q", cfg.ZMQ.OPCAT.Topic)
	}
	if cfg.ZMQ.OPCAT.RPCHost != "opcat.example:18443" {
		t.Fatalf("expected OPCAT RPC host to load from env, got %q", cfg.ZMQ.OPCAT.RPCHost)
	}
	if cfg.ZMQ.OPCAT.RPCUser != "opcat-user" {
		t.Fatalf("expected OPCAT RPC user to load from env, got %q", cfg.ZMQ.OPCAT.RPCUser)
	}
	if cfg.ZMQ.OPCAT.RPCPass != "opcat-pass" {
		t.Fatalf("expected OPCAT RPC pass to load from env, got %q", cfg.ZMQ.OPCAT.RPCPass)
	}
	if cfg.ZMQ.OPCAT.RPCHTTPPostMode {
		t.Fatal("expected OPCAT RPC HTTP post mode to load from env")
	}
	if cfg.ZMQ.OPCAT.RPCDisableTLS {
		t.Fatal("expected OPCAT RPC TLS flag to load from env")
	}
}

func assertChainRPCConfig(t *testing.T, cfg ChainRPCConfig, host, user, pass string, height int64) {
	t.Helper()
	if !cfg.Enabled {
		t.Fatal("expected chain toggle to load from env")
	}
	if cfg.RPCHost != host {
		t.Fatalf("expected rpc host %q, got %q", host, cfg.RPCHost)
	}
	if cfg.RPCUser != user {
		t.Fatalf("expected rpc user %q, got %q", user, cfg.RPCUser)
	}
	if cfg.RPCPass != pass {
		t.Fatalf("expected rpc pass %q, got %q", pass, cfg.RPCPass)
	}
	if cfg.RPCHTTPPostMode {
		t.Fatal("expected RPC HTTP post mode to load from env")
	}
	if cfg.RPCDisableTLS {
		t.Fatal("expected RPC TLS flag to load from env")
	}
	if cfg.InitialHeight != height {
		t.Fatalf("expected initial height %d, got %d", height, cfg.InitialHeight)
	}
}
