package config

import (
	"strconv"
	"testing"
	"time"
)

func TestDefaultBotHubAssetBaseURLUsesFileIndexer(t *testing.T) {
	cfg := Default()
	if cfg.BotHub.AssetBaseURL != "https://file.metaid.io/metafile-indexer/content" {
		t.Fatalf("BotHub asset base URL: got %q", cfg.BotHub.AssetBaseURL)
	}
}

func TestDefaultBotHomepageV2BackfillConfigDisabled(t *testing.T) {
	cfg := Default()

	if cfg.BotHomepageV2Backfill.Enabled {
		t.Fatal("expected bot homepage v2 backfill to be disabled by default")
	}
	if cfg.BotHomepageV2Backfill.Lookback != 1440*time.Hour {
		t.Fatalf("expected default lookback 1440h, got %s", cfg.BotHomepageV2Backfill.Lookback)
	}
	if cfg.BotHomepageV2Backfill.Timeout != 2*time.Minute {
		t.Fatalf("expected default timeout 2m, got %s", cfg.BotHomepageV2Backfill.Timeout)
	}
	if cfg.BotHomepageV2Backfill.PageSize != 100 {
		t.Fatalf("expected default page size 100, got %d", cfg.BotHomepageV2Backfill.PageSize)
	}
	if cfg.BotHomepageV2Backfill.MANAPIBaseURL != "https://manapi.metaid.io" {
		t.Fatalf("expected default MANAPI base URL, got %q", cfg.BotHomepageV2Backfill.MANAPIBaseURL)
	}
}

func TestLoadBotHomepageV2BackfillEnv(t *testing.T) {
	t.Setenv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_ENABLED", "true")
	t.Setenv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_LOOKBACK", "720h")
	t.Setenv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_TIMEOUT", "30s")
	t.Setenv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_PAGE_SIZE", "25")
	t.Setenv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_MANAPI_BASE_URL", "https://manapi.example")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !cfg.BotHomepageV2Backfill.Enabled {
		t.Fatal("expected backfill enabled to load from env")
	}
	if cfg.BotHomepageV2Backfill.Lookback != 720*time.Hour {
		t.Fatalf("expected lookback env value, got %s", cfg.BotHomepageV2Backfill.Lookback)
	}
	if cfg.BotHomepageV2Backfill.Timeout != 30*time.Second {
		t.Fatalf("expected timeout env value, got %s", cfg.BotHomepageV2Backfill.Timeout)
	}
	if cfg.BotHomepageV2Backfill.PageSize != 25 {
		t.Fatalf("expected page size env value, got %d", cfg.BotHomepageV2Backfill.PageSize)
	}
	if cfg.BotHomepageV2Backfill.MANAPIBaseURL != "https://manapi.example" {
		t.Fatalf("expected MANAPI base URL env value, got %q", cfg.BotHomepageV2Backfill.MANAPIBaseURL)
	}
}

func TestDefaultFederationConfigDisabled(t *testing.T) {
	cfg := Default()

	if cfg.Federation.Enabled {
		t.Fatal("expected federation to be disabled by default")
	}
	if cfg.Federation.Network != "mvc-mainnet" {
		t.Fatalf("expected default network mvc-mainnet, got %q", cfg.Federation.Network)
	}
	if cfg.Federation.MANAPIBaseURL != "https://manapi.metaid.io/pin/path/list?path={protocol-path}&size={size}" {
		t.Fatalf("expected default MANAPI base URL template, got %q", cfg.Federation.MANAPIBaseURL)
	}
	if cfg.Federation.RegistryPath != "/protocols/metaso-p2p-node" {
		t.Fatalf("expected default registry path, got %q", cfg.Federation.RegistryPath)
	}
	if cfg.Federation.PresencePath != "/.well-known/metaso-p2p/presence" {
		t.Fatalf("expected default presence path, got %q", cfg.Federation.PresencePath)
	}
	if cfg.Federation.RegistryRenewInterval != 6*time.Hour {
		t.Fatalf("expected default registry renew interval 6h, got %s", cfg.Federation.RegistryRenewInterval)
	}
	if cfg.Federation.RegistryValidFor != 24*time.Hour {
		t.Fatalf("expected default registry valid for 24h, got %s", cfg.Federation.RegistryValidFor)
	}
	if cfg.Federation.DiscoveryInterval != 5*time.Minute {
		t.Fatalf("expected default discovery interval 5m, got %s", cfg.Federation.DiscoveryInterval)
	}
	if cfg.Federation.PresencePullInterval != 20*time.Second {
		t.Fatalf("expected default presence pull interval 20s, got %s", cfg.Federation.PresencePullInterval)
	}
	if cfg.Federation.PresenceTTL != 90*time.Second {
		t.Fatalf("expected default presence TTL 90s, got %s", cfg.Federation.PresenceTTL)
	}
	if cfg.Federation.RequestTimeout != 3*time.Second {
		t.Fatalf("expected default request timeout 3s, got %s", cfg.Federation.RequestTimeout)
	}
	if cfg.Federation.DefaultScope != "global" {
		t.Fatalf("expected default scope global, got %q", cfg.Federation.DefaultScope)
	}
}

func TestLoadFederationEnv(t *testing.T) {
	t.Setenv("METASO_P2P_FEDERATION_ENABLED", "true")
	t.Setenv("METASO_P2P_FEDERATION_NETWORK", "mvc-testnet")
	t.Setenv("METASO_P2P_FEDERATION_NODE_PRIVATE_KEY", "node-private-key")
	t.Setenv("METASO_P2P_FEDERATION_PUBLIC_BASE_URL", "https://socket.example")
	t.Setenv("METASO_P2P_FEDERATION_MANAPI_BASE_URL", "https://manapi.example/pin/path/list?path={protocol-path}&size={size}")
	t.Setenv("METASO_P2P_FEDERATION_METALET_BASE_URL", "https://metalet.example")
	t.Setenv("METASO_P2P_FEDERATION_REGISTRY_PATH", "/protocols/custom-node")
	t.Setenv("METASO_P2P_FEDERATION_PRESENCE_PATH", "/presence")
	t.Setenv("METASO_P2P_FEDERATION_REGISTRY_RENEW_INTERVAL", "7h")
	t.Setenv("METASO_P2P_FEDERATION_REGISTRY_VALID_FOR", "25h")
	t.Setenv("METASO_P2P_FEDERATION_DISCOVERY_INTERVAL", "6m")
	t.Setenv("METASO_P2P_FEDERATION_PRESENCE_PULL_INTERVAL", "21s")
	t.Setenv("METASO_P2P_FEDERATION_PRESENCE_TTL", "91s")
	t.Setenv("METASO_P2P_FEDERATION_REQUEST_TIMEOUT", "4s")
	t.Setenv("METASO_P2P_FEDERATION_DEFAULT_SCOPE", "local")
	t.Setenv("METASO_P2P_FEDERATION_ALLOW_INSECURE_HTTP", "true")
	t.Setenv("METASO_P2P_FEDERATION_MAX_PEERS", "77")
	t.Setenv("METASO_P2P_FEDERATION_MAX_SNAPSHOT_BYTES", "123456")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !cfg.Federation.Enabled {
		t.Fatal("expected federation enabled to load from env")
	}
	if cfg.Federation.Network != "mvc-testnet" {
		t.Fatalf("expected network env value, got %q", cfg.Federation.Network)
	}
	if cfg.Federation.NodePrivateKey != "node-private-key" {
		t.Fatalf("expected node private key env value, got %q", cfg.Federation.NodePrivateKey)
	}
	if cfg.Federation.PublicBaseURL != "https://socket.example" {
		t.Fatalf("expected public base URL env value, got %q", cfg.Federation.PublicBaseURL)
	}
	if cfg.Federation.MANAPIBaseURL != "https://manapi.example/pin/path/list?path={protocol-path}&size={size}" {
		t.Fatalf("expected MANAPI base URL env value, got %q", cfg.Federation.MANAPIBaseURL)
	}
	if cfg.Federation.MetaletBaseURL != "https://metalet.example" {
		t.Fatalf("expected Metalet base URL env value, got %q", cfg.Federation.MetaletBaseURL)
	}
	if cfg.Federation.RegistryPath != "/protocols/custom-node" {
		t.Fatalf("expected registry path env value, got %q", cfg.Federation.RegistryPath)
	}
	if cfg.Federation.PresencePath != "/presence" {
		t.Fatalf("expected presence path env value, got %q", cfg.Federation.PresencePath)
	}
	if cfg.Federation.RegistryRenewInterval != 7*time.Hour {
		t.Fatalf("expected registry renew interval env value, got %s", cfg.Federation.RegistryRenewInterval)
	}
	if cfg.Federation.RegistryValidFor != 25*time.Hour {
		t.Fatalf("expected registry valid for env value, got %s", cfg.Federation.RegistryValidFor)
	}
	if cfg.Federation.DiscoveryInterval != 6*time.Minute {
		t.Fatalf("expected discovery interval env value, got %s", cfg.Federation.DiscoveryInterval)
	}
	if cfg.Federation.PresencePullInterval != 21*time.Second {
		t.Fatalf("expected presence pull interval env value, got %s", cfg.Federation.PresencePullInterval)
	}
	if cfg.Federation.PresenceTTL != 91*time.Second {
		t.Fatalf("expected presence TTL env value, got %s", cfg.Federation.PresenceTTL)
	}
	if cfg.Federation.RequestTimeout != 4*time.Second {
		t.Fatalf("expected request timeout env value, got %s", cfg.Federation.RequestTimeout)
	}
	if cfg.Federation.DefaultScope != "local" {
		t.Fatalf("expected default scope env value, got %q", cfg.Federation.DefaultScope)
	}
	if !cfg.Federation.AllowInsecureHTTP {
		t.Fatal("expected allow insecure HTTP env value")
	}
	if cfg.Federation.MaxPeers != 77 {
		t.Fatalf("expected max peers env value, got %d", cfg.Federation.MaxPeers)
	}
	if cfg.Federation.MaxSnapshotBytes != 123456 {
		t.Fatalf("expected max snapshot bytes env value, got %d", cfg.Federation.MaxSnapshotBytes)
	}
}

func TestValidateFederationRequiresPublicBaseURLWhenEnabled(t *testing.T) {
	cfg := validFederationConfig()
	cfg.Federation.PublicBaseURL = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for missing public base URL")
	}
}

func TestValidateFederationPublicBaseURLRequiresHTTPSUnlessInsecureAllowed(t *testing.T) {
	cfg := validFederationConfig()
	cfg.Federation.PublicBaseURL = "http://localhost:8080"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for insecure public base URL")
	}

	cfg.Federation.AllowInsecureHTTP = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate returned error with insecure HTTP allowed: %v", err)
	}
}

func TestValidateFederationRequiresNodePrivateKeyWhenEnabled(t *testing.T) {
	cfg := validFederationConfig()
	cfg.Federation.NodePrivateKey = ""

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for missing node private key")
	}
}

func TestValidateFederationRequiresDiscoveryAndWalletURLsWhenEnabled(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "missing MANAPI base URL",
			mutate: func(cfg *Config) {
				cfg.Federation.MANAPIBaseURL = ""
			},
		},
		{
			name: "MANAPI base URL missing protocol path placeholder",
			mutate: func(cfg *Config) {
				cfg.Federation.MANAPIBaseURL = "https://manapi.example/pin/path/list?path=/protocols/metaso-p2p-node&size={size}"
			},
		},
		{
			name: "MANAPI base URL missing size placeholder",
			mutate: func(cfg *Config) {
				cfg.Federation.MANAPIBaseURL = "https://manapi.example/pin/path/list?path={protocol-path}&size=50"
			},
		},
		{
			name: "missing Metalet base URL",
			mutate: func(cfg *Config) {
				cfg.Federation.MetaletBaseURL = ""
			},
		},
		{
			name: "registry path must start with slash",
			mutate: func(cfg *Config) {
				cfg.Federation.RegistryPath = "protocols/metaso-p2p-node"
			},
		},
		{
			name: "presence path must start with slash",
			mutate: func(cfg *Config) {
				cfg.Federation.PresencePath = ".well-known/metaso-p2p/presence"
			},
		},
		{
			name: "registry renew interval must be positive",
			mutate: func(cfg *Config) {
				cfg.Federation.RegistryRenewInterval = 0
			},
		},
		{
			name: "registry valid for must be positive",
			mutate: func(cfg *Config) {
				cfg.Federation.RegistryValidFor = 0
			},
		},
		{
			name: "discovery interval must be positive",
			mutate: func(cfg *Config) {
				cfg.Federation.DiscoveryInterval = 0
			},
		},
		{
			name: "presence pull interval must be positive",
			mutate: func(cfg *Config) {
				cfg.Federation.PresencePullInterval = 0
			},
		},
		{
			name: "presence TTL must be positive",
			mutate: func(cfg *Config) {
				cfg.Federation.PresenceTTL = 0
			},
		},
		{
			name: "request timeout must be positive",
			mutate: func(cfg *Config) {
				cfg.Federation.RequestTimeout = 0
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validFederationConfig()
			tt.mutate(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateFederationRejectsNegativeLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{
			name: "negative max peers",
			mutate: func(cfg *Config) {
				cfg.Federation.MaxPeers = -1
			},
		},
		{
			name: "negative max snapshot bytes",
			mutate: func(cfg *Config) {
				cfg.Federation.MaxSnapshotBytes = -1
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validFederationConfig()
			tt.mutate(&cfg)

			if err := cfg.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestValidateFederationDefaultScope(t *testing.T) {
	for _, scope := range []string{"local", "global"} {
		t.Run(scope, func(t *testing.T) {
			cfg := validFederationConfig()
			cfg.Federation.DefaultScope = scope

			if err := cfg.Validate(); err != nil {
				t.Fatalf("Validate returned error: %v", err)
			}
		})
	}

	cfg := validFederationConfig()
	cfg.Federation.DefaultScope = "team"

	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for unsupported default scope")
	}
}

func validFederationConfig() Config {
	cfg := Default()
	cfg.Federation.Enabled = true
	cfg.Federation.NodePrivateKey = "node-private-key"
	cfg.Federation.PublicBaseURL = "https://socket.example"
	cfg.Federation.MANAPIBaseURL = "https://manapi.example/pin/path/list?path={protocol-path}&size={size}"
	cfg.Federation.MetaletBaseURL = "https://metalet.example"
	return cfg
}

func TestLoadBlockIndexEnv(t *testing.T) {
	t.Setenv("METASO_P2P_BLOCK_INDEX_ENABLED", "true")

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
			enabledEnv:    "METASO_P2P_BLOCK_INDEX_BTC_ENABLED",
			hostEnv:       "METASO_P2P_BLOCK_INDEX_BTC_RPC_HOST",
			userEnv:       "METASO_P2P_BLOCK_INDEX_BTC_RPC_USER",
			passEnv:       "METASO_P2P_BLOCK_INDEX_BTC_RPC_PASS",
			postModeEnv:   "METASO_P2P_BLOCK_INDEX_BTC_RPC_HTTP_POST_MODE",
			disableTLSEnv: "METASO_P2P_BLOCK_INDEX_BTC_RPC_DISABLE_TLS",
			heightEnv:     "METASO_P2P_BLOCK_INDEX_BTC_INITIAL_HEIGHT",
			assert: func(t *testing.T, cfg Config) {
				assertChainRPCConfig(t, cfg.BlockIndex.BTC, "btc.example:8332", "btc-user", "btc-pass", 101)
			},
		},
		{
			name:          "mvc",
			enabledEnv:    "METASO_P2P_BLOCK_INDEX_MVC_ENABLED",
			hostEnv:       "METASO_P2P_BLOCK_INDEX_MVC_RPC_HOST",
			userEnv:       "METASO_P2P_BLOCK_INDEX_MVC_RPC_USER",
			passEnv:       "METASO_P2P_BLOCK_INDEX_MVC_RPC_PASS",
			postModeEnv:   "METASO_P2P_BLOCK_INDEX_MVC_RPC_HTTP_POST_MODE",
			disableTLSEnv: "METASO_P2P_BLOCK_INDEX_MVC_RPC_DISABLE_TLS",
			heightEnv:     "METASO_P2P_BLOCK_INDEX_MVC_INITIAL_HEIGHT",
			assert: func(t *testing.T, cfg Config) {
				assertChainRPCConfig(t, cfg.BlockIndex.MVC, "mvc.example:9882", "mvc-user", "mvc-pass", 202)
			},
		},
		{
			name:          "doge",
			enabledEnv:    "METASO_P2P_BLOCK_INDEX_DOGE_ENABLED",
			hostEnv:       "METASO_P2P_BLOCK_INDEX_DOGE_RPC_HOST",
			userEnv:       "METASO_P2P_BLOCK_INDEX_DOGE_RPC_USER",
			passEnv:       "METASO_P2P_BLOCK_INDEX_DOGE_RPC_PASS",
			postModeEnv:   "METASO_P2P_BLOCK_INDEX_DOGE_RPC_HTTP_POST_MODE",
			disableTLSEnv: "METASO_P2P_BLOCK_INDEX_DOGE_RPC_DISABLE_TLS",
			heightEnv:     "METASO_P2P_BLOCK_INDEX_DOGE_INITIAL_HEIGHT",
			assert: func(t *testing.T, cfg Config) {
				assertChainRPCConfig(t, cfg.BlockIndex.DOGE, "doge.example:23116", "doge-user", "doge-pass", 303)
			},
		},
		{
			name:          "opcat",
			enabledEnv:    "METASO_P2P_BLOCK_INDEX_OPCAT_ENABLED",
			hostEnv:       "METASO_P2P_BLOCK_INDEX_OPCAT_RPC_HOST",
			userEnv:       "METASO_P2P_BLOCK_INDEX_OPCAT_RPC_USER",
			passEnv:       "METASO_P2P_BLOCK_INDEX_OPCAT_RPC_PASS",
			postModeEnv:   "METASO_P2P_BLOCK_INDEX_OPCAT_RPC_HTTP_POST_MODE",
			disableTLSEnv: "METASO_P2P_BLOCK_INDEX_OPCAT_RPC_DISABLE_TLS",
			heightEnv:     "METASO_P2P_BLOCK_INDEX_OPCAT_INITIAL_HEIGHT",
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

func TestDefaultZMQMempoolPollingConfig(t *testing.T) {
	cfg := Default()

	if !cfg.ZMQ.MempoolPollingEnabled {
		t.Fatal("expected ZMQ mempool polling to be enabled by default")
	}
	if cfg.ZMQ.MempoolPollInterval != 10*time.Second {
		t.Fatalf("expected default mempool poll interval 10s, got %s", cfg.ZMQ.MempoolPollInterval)
	}
	if cfg.ZMQ.MempoolDedupeTTL != 30*time.Minute {
		t.Fatalf("expected default mempool dedupe TTL 30m, got %s", cfg.ZMQ.MempoolDedupeTTL)
	}
}

func TestLoadZMQMempoolPollingEnv(t *testing.T) {
	t.Setenv("METASO_P2P_ZMQ_MEMPOOL_POLLING_ENABLED", "false")
	t.Setenv("METASO_P2P_ZMQ_MEMPOOL_POLL_INTERVAL", "15s")
	t.Setenv("METASO_P2P_ZMQ_MEMPOOL_DEDUPE_TTL", "45m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.ZMQ.MempoolPollingEnabled {
		t.Fatal("expected ZMQ mempool polling enabled to load from env")
	}
	if cfg.ZMQ.MempoolPollInterval != 15*time.Second {
		t.Fatalf("expected mempool poll interval env value, got %s", cfg.ZMQ.MempoolPollInterval)
	}
	if cfg.ZMQ.MempoolDedupeTTL != 45*time.Minute {
		t.Fatalf("expected mempool dedupe TTL env value, got %s", cfg.ZMQ.MempoolDedupeTTL)
	}
}

func TestLoadOPCATZMQEnv(t *testing.T) {
	t.Setenv("METASO_P2P_ZMQ_ENABLED", "true")
	t.Setenv("METASO_P2P_ZMQ_OPCAT_ENABLED", "true")
	t.Setenv("METASO_P2P_ZMQ_OPCAT_ENDPOINT", "tcp://opcat.example:18442")
	t.Setenv("METASO_P2P_ZMQ_OPCAT_TOPIC", "hashblock")
	t.Setenv("METASO_P2P_ZMQ_OPCAT_RPC_HOST", "opcat.example:18443")
	t.Setenv("METASO_P2P_ZMQ_OPCAT_RPC_USER", "opcat-user")
	t.Setenv("METASO_P2P_ZMQ_OPCAT_RPC_PASS", "opcat-pass")
	t.Setenv("METASO_P2P_ZMQ_OPCAT_RPC_HTTP_POST_MODE", "false")
	t.Setenv("METASO_P2P_ZMQ_OPCAT_RPC_DISABLE_TLS", "false")

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
