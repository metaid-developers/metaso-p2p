package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Service               ServiceConfig               `json:"service"`
	Socket                SocketConfig                `json:"socket"`
	ZMQ                   ZMQConfig                   `json:"zmq"`
	BlockIndex            BlockIndexConfig            `json:"blockIndex"`
	Pebble                PebbleConfig                `json:"pebble"`
	Cache                 CacheConfig                 `json:"cache"`
	Profile               ProfileConfig               `json:"profile"`
	GroupChat             GroupChatConfig             `json:"groupChat"`
	BotHub                BotHubConfig                `json:"botHub"`
	Federation            FederationConfig            `json:"federation"`
	BotHomepageV2Backfill BotHomepageV2BackfillConfig `json:"botHomepageV2Backfill"`
}

// BotHubConfig holds the Bot Hub skill-service aggregator runtime knobs.
// Today this is just the asset base URL — the place we resolve chain-
// declared pin id / metafile references into HTTP URLs that the frontend
// can load directly. See docs/specs/2026-05-28-bot-hub-skill-service-
// aggregation-api.md for the wire contract.
type BotHubConfig struct {
	// AssetBaseURL prefixes any non-absolute icon / avatar reference
	// returned by the Bot Hub list / detail endpoints. The frontend never
	// sees a bare pin id; it sees either an already-absolute http(s) URL
	// declared on chain, or this base joined with the pin id.
	AssetBaseURL string `json:"assetBaseUrl"`
}

type FederationConfig struct {
	Enabled               bool          `json:"enabled"`
	Network               string        `json:"network"`
	NodePrivateKey        string        `json:"nodePrivateKey"`
	PublicBaseURL         string        `json:"publicBaseUrl"`
	MANAPIBaseURL         string        `json:"manapiBaseUrl"`
	MetaletBaseURL        string        `json:"metaletBaseUrl"`
	RegistryPath          string        `json:"registryPath"`
	PresencePath          string        `json:"presencePath"`
	RegistryRenewInterval time.Duration `json:"registryRenewInterval"`
	RegistryValidFor      time.Duration `json:"registryValidFor"`
	DiscoveryInterval     time.Duration `json:"discoveryInterval"`
	PresencePullInterval  time.Duration `json:"presencePullInterval"`
	PresenceTTL           time.Duration `json:"presenceTTL"`
	RequestTimeout        time.Duration `json:"requestTimeout"`
	DefaultScope          string        `json:"defaultScope"`
	AllowInsecureHTTP     bool          `json:"allowInsecureHttp"`
	MaxPeers              int           `json:"maxPeers"`
	MaxSnapshotBytes      int           `json:"maxSnapshotBytes"`
}

type BotHomepageV2BackfillConfig struct {
	Enabled       bool          `json:"enabled"`
	Lookback      time.Duration `json:"lookback"`
	Timeout       time.Duration `json:"timeout"`
	PageSize      int           `json:"pageSize"`
	MANAPIBaseURL string        `json:"manapiBaseUrl"`
}

type BlockIndexConfig struct {
	Enabled bool           `json:"enabled"`
	BTC     ChainRPCConfig `json:"btc"`
	MVC     ChainRPCConfig `json:"mvc"`
	DOGE    ChainRPCConfig `json:"doge"`
	OPCAT   ChainRPCConfig `json:"opcat"`
}

type ChainRPCConfig struct {
	Enabled         bool   `json:"enabled"`
	RPCHost         string `json:"rpcHost"`
	RPCUser         string `json:"rpcUser"`
	RPCPass         string `json:"rpcPass"`
	RPCHTTPPostMode bool   `json:"rpcHttpPostMode"`
	RPCDisableTLS   bool   `json:"rpcDisableTls"`
	InitialHeight   int64  `json:"initialHeight"`
}

type CacheConfig struct {
	MaxEntries        int `json:"maxEntries"`
	DefaultTTLSeconds int `json:"defaultTtlSeconds"`
}

type ServiceConfig struct {
	HTTPAddr        string        `json:"httpAddr"`
	HealthPath      string        `json:"healthPath"`
	ShutdownTimeout time.Duration `json:"shutdownTimeout"`
}

type SocketConfig struct {
	Enabled              bool          `json:"enabled"`
	PrimaryPath          string        `json:"primaryPath"`
	LegacyPath           string        `json:"legacyPath"`
	RoomBroadcastEnabled bool          `json:"roomBroadcastEnabled"`
	MaxConnections       int           `json:"maxConnections"`
	MaxPCPerUser         int           `json:"maxPcPerUser"`
	MaxAppPerUser        int           `json:"maxAppPerUser"`
	PingInterval         time.Duration `json:"pingInterval"`
	PingTimeout          time.Duration `json:"pingTimeout"`
	AllowEIO3            bool          `json:"allowEio3"`
	ExtraPushAuthKey     string        `json:"extraPushAuthKey"`
}

type ZMQConfig struct {
	Enabled               bool           `json:"enabled"`
	MempoolPollingEnabled bool           `json:"mempoolPollingEnabled"`
	MempoolPollInterval   time.Duration  `json:"mempoolPollInterval"`
	MempoolDedupeTTL      time.Duration  `json:"mempoolDedupeTTL"`
	BTC                   ChainZMQConfig `json:"btc"`
	MVC                   ChainZMQConfig `json:"mvc"`
	DOGE                  ChainZMQConfig `json:"doge"`
	OPCAT                 ChainZMQConfig `json:"opcat"`
}

type ChainZMQConfig struct {
	Enabled         bool   `json:"enabled"`
	Endpoint        string `json:"endpoint"`
	Topic           string `json:"topic"`
	RPCHost         string `json:"rpcHost"`
	RPCUser         string `json:"rpcUser"`
	RPCPass         string `json:"rpcPass"`
	RPCHTTPPostMode bool   `json:"rpcHttpPostMode"`
	RPCDisableTLS   bool   `json:"rpcDisableTls"`
}

type PebbleConfig struct {
	Enabled bool   `json:"enabled"`
	DataDir string `json:"dataDir"`
}

type ProfileConfig struct {
	Enabled             bool   `json:"enabled"`
	Mode                string `json:"mode"`
	RemoteBaseURL       string `json:"remoteBaseURL"`
	AllowRemoteFallback bool   `json:"allowRemoteFallback"`
}

type GroupChatConfig struct {
	MigrationEnabled bool `json:"migrationEnabled"`
	BackupEnabled    bool `json:"backupEnabled"`

	// P0 explicit exclusions (default disabled).
	LuckyBagEnabled bool `json:"luckyBagEnabled"`
	GRPCEnabled     bool `json:"grpcEnabled"`
	HeavyAPIEnabled bool `json:"heavyApiEnabled"`
}

func Default() Config {
	return Config{
		Service: ServiceConfig{
			HTTPAddr:        ":8080",
			HealthPath:      "/healthz",
			ShutdownTimeout: 10 * time.Second,
		},
		Socket: SocketConfig{
			Enabled:              true,
			PrimaryPath:          "/socket/socket.io",
			LegacyPath:           "/socket.io",
			RoomBroadcastEnabled: true,
			MaxConnections:       10000,
			MaxPCPerUser:         3,
			MaxAppPerUser:        3,
			PingInterval:         2 * time.Second,
			PingTimeout:          5 * time.Second,
			AllowEIO3:            true,
			ExtraPushAuthKey:     "",
		},
		ZMQ: ZMQConfig{
			Enabled:               false,
			MempoolPollingEnabled: true,
			MempoolPollInterval:   10 * time.Second,
			MempoolDedupeTTL:      30 * time.Minute,
			BTC: ChainZMQConfig{
				Enabled:         false,
				Endpoint:        "",
				Topic:           "rawtx",
				RPCHost:         "",
				RPCUser:         "",
				RPCPass:         "",
				RPCHTTPPostMode: true,
				RPCDisableTLS:   true,
			},
			MVC: ChainZMQConfig{
				Enabled:         false,
				Endpoint:        "",
				Topic:           "rawtx",
				RPCHost:         "",
				RPCUser:         "",
				RPCPass:         "",
				RPCHTTPPostMode: true,
				RPCDisableTLS:   true,
			},
			DOGE: ChainZMQConfig{
				Enabled:         false,
				Endpoint:        "",
				Topic:           "rawtx",
				RPCHost:         "",
				RPCUser:         "",
				RPCPass:         "",
				RPCHTTPPostMode: true,
				RPCDisableTLS:   true,
			},
			OPCAT: ChainZMQConfig{
				Enabled:         false,
				Endpoint:        "",
				Topic:           "rawtx",
				RPCHost:         "",
				RPCUser:         "",
				RPCPass:         "",
				RPCHTTPPostMode: true,
				RPCDisableTLS:   true,
			},
		},
		BlockIndex: BlockIndexConfig{
			Enabled: false,
			BTC:     ChainRPCConfig{Enabled: false, RPCHTTPPostMode: true, RPCDisableTLS: true},
			MVC:     ChainRPCConfig{Enabled: false, RPCHTTPPostMode: true, RPCDisableTLS: true},
			DOGE:    ChainRPCConfig{Enabled: false, RPCHTTPPostMode: true, RPCDisableTLS: true},
			OPCAT:   ChainRPCConfig{Enabled: false, RPCHTTPPostMode: true, RPCDisableTLS: true},
		},
		Cache: CacheConfig{
			MaxEntries:        10000,
			DefaultTTLSeconds: 300,
		},
		Pebble: PebbleConfig{
			Enabled: true,
			DataDir: "./data/pebble",
		},
		Profile: ProfileConfig{
			Enabled:             true,
			Mode:                "local-first",
			RemoteBaseURL:       "",
			AllowRemoteFallback: true,
		},
		GroupChat: GroupChatConfig{
			MigrationEnabled: true,
			BackupEnabled:    false,
			LuckyBagEnabled:  false,
			GRPCEnabled:      false,
			HeavyAPIEnabled:  false,
		},
		BotHub: BotHubConfig{
			// Default mirrors the spec's recommendation. Operators can
			// override with METASO_P2P_ASSET_BASE_URL when running
			// against a different MetaID asset host.
			AssetBaseURL: "https://file.metaid.io/metafile-indexer/content",
		},
		Federation: FederationConfig{
			Enabled:               false,
			Network:               "mvc-mainnet",
			NodePrivateKey:        "",
			PublicBaseURL:         "",
			MANAPIBaseURL:         "https://manapi.metaid.io/pin/path/list?path={protocol-path}&size={size}",
			MetaletBaseURL:        "https://www.metalet.space",
			RegistryPath:          "/protocols/metaso-p2p-node",
			PresencePath:          "/.well-known/metaso-p2p/presence",
			RegistryRenewInterval: 6 * time.Hour,
			RegistryValidFor:      24 * time.Hour,
			DiscoveryInterval:     5 * time.Minute,
			PresencePullInterval:  20 * time.Second,
			PresenceTTL:           90 * time.Second,
			RequestTimeout:        3 * time.Second,
			DefaultScope:          "global",
			AllowInsecureHTTP:     false,
			MaxPeers:              0,
			MaxSnapshotBytes:      0,
		},
		BotHomepageV2Backfill: BotHomepageV2BackfillConfig{
			Enabled:       false,
			Lookback:      1440 * time.Hour,
			Timeout:       2 * time.Minute,
			PageSize:      100,
			MANAPIBaseURL: "https://manapi.metaid.io",
		},
	}
}

func Load() (Config, error) {
	cfg := Default()

	applyStringEnv("METASO_P2P_HTTP_ADDR", &cfg.Service.HTTPAddr)
	applyStringEnv("METASO_P2P_HEALTH_PATH", &cfg.Service.HealthPath)
	applyDurationEnv("METASO_P2P_SHUTDOWN_TIMEOUT", &cfg.Service.ShutdownTimeout)

	applyBoolEnv("METASO_P2P_SOCKET_ENABLED", &cfg.Socket.Enabled)
	applyStringEnv("METASO_P2P_SOCKET_PATH", &cfg.Socket.PrimaryPath)
	applyStringEnv("METASO_P2P_SOCKET_LEGACY_PATH", &cfg.Socket.LegacyPath)
	applyBoolEnv("METASO_P2P_SOCKET_ROOM_BROADCAST_ENABLED", &cfg.Socket.RoomBroadcastEnabled)
	applyIntEnv("METASO_P2P_SOCKET_MAX_CONNECTIONS", &cfg.Socket.MaxConnections)
	applyIntEnv("METASO_P2P_SOCKET_MAX_PC_PER_USER", &cfg.Socket.MaxPCPerUser)
	applyIntEnv("METASO_P2P_SOCKET_MAX_APP_PER_USER", &cfg.Socket.MaxAppPerUser)
	applyDurationEnv("METASO_P2P_SOCKET_PING_INTERVAL", &cfg.Socket.PingInterval)
	applyDurationEnv("METASO_P2P_SOCKET_PING_TIMEOUT", &cfg.Socket.PingTimeout)
	applyBoolEnv("METASO_P2P_SOCKET_ALLOW_EIO3", &cfg.Socket.AllowEIO3)
	applyStringEnv("METASO_P2P_SOCKET_EXTRA_PUSH_AUTH_KEY", &cfg.Socket.ExtraPushAuthKey)

	applyBoolEnv("METASO_P2P_ZMQ_ENABLED", &cfg.ZMQ.Enabled)
	applyBoolEnv("METASO_P2P_ZMQ_MEMPOOL_POLLING_ENABLED", &cfg.ZMQ.MempoolPollingEnabled)
	applyDurationEnv("METASO_P2P_ZMQ_MEMPOOL_POLL_INTERVAL", &cfg.ZMQ.MempoolPollInterval)
	applyDurationEnv("METASO_P2P_ZMQ_MEMPOOL_DEDUPE_TTL", &cfg.ZMQ.MempoolDedupeTTL)
	applyBoolEnv("METASO_P2P_ZMQ_BTC_ENABLED", &cfg.ZMQ.BTC.Enabled)
	applyStringEnv("METASO_P2P_ZMQ_BTC_ENDPOINT", &cfg.ZMQ.BTC.Endpoint)
	applyStringEnv("METASO_P2P_ZMQ_BTC_TOPIC", &cfg.ZMQ.BTC.Topic)
	applyStringEnv("METASO_P2P_ZMQ_BTC_RPC_HOST", &cfg.ZMQ.BTC.RPCHost)
	applyStringEnv("METASO_P2P_ZMQ_BTC_RPC_USER", &cfg.ZMQ.BTC.RPCUser)
	applyStringEnv("METASO_P2P_ZMQ_BTC_RPC_PASS", &cfg.ZMQ.BTC.RPCPass)
	applyBoolEnv("METASO_P2P_ZMQ_BTC_RPC_HTTP_POST_MODE", &cfg.ZMQ.BTC.RPCHTTPPostMode)
	applyBoolEnv("METASO_P2P_ZMQ_BTC_RPC_DISABLE_TLS", &cfg.ZMQ.BTC.RPCDisableTLS)
	applyBoolEnv("METASO_P2P_ZMQ_MVC_ENABLED", &cfg.ZMQ.MVC.Enabled)
	applyStringEnv("METASO_P2P_ZMQ_MVC_ENDPOINT", &cfg.ZMQ.MVC.Endpoint)
	applyStringEnv("METASO_P2P_ZMQ_MVC_TOPIC", &cfg.ZMQ.MVC.Topic)
	applyStringEnv("METASO_P2P_ZMQ_MVC_RPC_HOST", &cfg.ZMQ.MVC.RPCHost)
	applyStringEnv("METASO_P2P_ZMQ_MVC_RPC_USER", &cfg.ZMQ.MVC.RPCUser)
	applyStringEnv("METASO_P2P_ZMQ_MVC_RPC_PASS", &cfg.ZMQ.MVC.RPCPass)
	applyBoolEnv("METASO_P2P_ZMQ_MVC_RPC_HTTP_POST_MODE", &cfg.ZMQ.MVC.RPCHTTPPostMode)
	applyBoolEnv("METASO_P2P_ZMQ_MVC_RPC_DISABLE_TLS", &cfg.ZMQ.MVC.RPCDisableTLS)
	applyBoolEnv("METASO_P2P_ZMQ_DOGE_ENABLED", &cfg.ZMQ.DOGE.Enabled)
	applyStringEnv("METASO_P2P_ZMQ_DOGE_ENDPOINT", &cfg.ZMQ.DOGE.Endpoint)
	applyStringEnv("METASO_P2P_ZMQ_DOGE_TOPIC", &cfg.ZMQ.DOGE.Topic)
	applyStringEnv("METASO_P2P_ZMQ_DOGE_RPC_HOST", &cfg.ZMQ.DOGE.RPCHost)
	applyStringEnv("METASO_P2P_ZMQ_DOGE_RPC_USER", &cfg.ZMQ.DOGE.RPCUser)
	applyStringEnv("METASO_P2P_ZMQ_DOGE_RPC_PASS", &cfg.ZMQ.DOGE.RPCPass)
	applyBoolEnv("METASO_P2P_ZMQ_DOGE_RPC_HTTP_POST_MODE", &cfg.ZMQ.DOGE.RPCHTTPPostMode)
	applyBoolEnv("METASO_P2P_ZMQ_DOGE_RPC_DISABLE_TLS", &cfg.ZMQ.DOGE.RPCDisableTLS)
	applyBoolEnv("METASO_P2P_ZMQ_OPCAT_ENABLED", &cfg.ZMQ.OPCAT.Enabled)
	applyStringEnv("METASO_P2P_ZMQ_OPCAT_ENDPOINT", &cfg.ZMQ.OPCAT.Endpoint)
	applyStringEnv("METASO_P2P_ZMQ_OPCAT_TOPIC", &cfg.ZMQ.OPCAT.Topic)
	applyStringEnv("METASO_P2P_ZMQ_OPCAT_RPC_HOST", &cfg.ZMQ.OPCAT.RPCHost)
	applyStringEnv("METASO_P2P_ZMQ_OPCAT_RPC_USER", &cfg.ZMQ.OPCAT.RPCUser)
	applyStringEnv("METASO_P2P_ZMQ_OPCAT_RPC_PASS", &cfg.ZMQ.OPCAT.RPCPass)
	applyBoolEnv("METASO_P2P_ZMQ_OPCAT_RPC_HTTP_POST_MODE", &cfg.ZMQ.OPCAT.RPCHTTPPostMode)
	applyBoolEnv("METASO_P2P_ZMQ_OPCAT_RPC_DISABLE_TLS", &cfg.ZMQ.OPCAT.RPCDisableTLS)

	applyBoolEnv("METASO_P2P_BLOCK_INDEX_ENABLED", &cfg.BlockIndex.Enabled)
	applyChainRPCEnv("METASO_P2P_BLOCK_INDEX_BTC", &cfg.BlockIndex.BTC)
	applyChainRPCEnv("METASO_P2P_BLOCK_INDEX_MVC", &cfg.BlockIndex.MVC)
	applyChainRPCEnv("METASO_P2P_BLOCK_INDEX_DOGE", &cfg.BlockIndex.DOGE)
	applyChainRPCEnv("METASO_P2P_BLOCK_INDEX_OPCAT", &cfg.BlockIndex.OPCAT)

	applyBoolEnv("METASO_P2P_PEBBLE_ENABLED", &cfg.Pebble.Enabled)
	applyStringEnv("METASO_P2P_PEBBLE_DATA_DIR", &cfg.Pebble.DataDir)

	applyBoolEnv("METASO_P2P_PROFILE_ENABLED", &cfg.Profile.Enabled)
	applyStringEnv("METASO_P2P_PROFILE_MODE", &cfg.Profile.Mode)
	applyStringEnv("METASO_P2P_PROFILE_REMOTE_BASE_URL", &cfg.Profile.RemoteBaseURL)
	applyBoolEnv("METASO_P2P_PROFILE_ALLOW_REMOTE_FALLBACK", &cfg.Profile.AllowRemoteFallback)

	applyBoolEnv("METASO_P2P_GROUPCHAT_MIGRATION_ENABLED", &cfg.GroupChat.MigrationEnabled)
	applyBoolEnv("METASO_P2P_GROUPCHAT_BACKUP_ENABLED", &cfg.GroupChat.BackupEnabled)
	applyBoolEnv("METASO_P2P_GROUPCHAT_LUCKYBAG_ENABLED", &cfg.GroupChat.LuckyBagEnabled)
	applyBoolEnv("METASO_P2P_GROUPCHAT_GRPC_ENABLED", &cfg.GroupChat.GRPCEnabled)
	applyBoolEnv("METASO_P2P_GROUPCHAT_HEAVY_API_ENABLED", &cfg.GroupChat.HeavyAPIEnabled)

	applyStringEnv("METASO_P2P_ASSET_BASE_URL", &cfg.BotHub.AssetBaseURL)

	applyBoolEnv("METASO_P2P_FEDERATION_ENABLED", &cfg.Federation.Enabled)
	applyStringEnv("METASO_P2P_FEDERATION_NETWORK", &cfg.Federation.Network)
	applyStringEnv("METASO_P2P_FEDERATION_NODE_PRIVATE_KEY", &cfg.Federation.NodePrivateKey)
	applyStringEnv("METASO_P2P_FEDERATION_PUBLIC_BASE_URL", &cfg.Federation.PublicBaseURL)
	applyStringEnv("METASO_P2P_FEDERATION_MANAPI_BASE_URL", &cfg.Federation.MANAPIBaseURL)
	applyStringEnv("METASO_P2P_FEDERATION_METALET_BASE_URL", &cfg.Federation.MetaletBaseURL)
	applyStringEnv("METASO_P2P_FEDERATION_REGISTRY_PATH", &cfg.Federation.RegistryPath)
	applyStringEnv("METASO_P2P_FEDERATION_PRESENCE_PATH", &cfg.Federation.PresencePath)
	applyDurationEnv("METASO_P2P_FEDERATION_REGISTRY_RENEW_INTERVAL", &cfg.Federation.RegistryRenewInterval)
	applyDurationEnv("METASO_P2P_FEDERATION_REGISTRY_VALID_FOR", &cfg.Federation.RegistryValidFor)
	applyDurationEnv("METASO_P2P_FEDERATION_DISCOVERY_INTERVAL", &cfg.Federation.DiscoveryInterval)
	applyDurationEnv("METASO_P2P_FEDERATION_PRESENCE_PULL_INTERVAL", &cfg.Federation.PresencePullInterval)
	applyDurationEnv("METASO_P2P_FEDERATION_PRESENCE_TTL", &cfg.Federation.PresenceTTL)
	applyDurationEnv("METASO_P2P_FEDERATION_REQUEST_TIMEOUT", &cfg.Federation.RequestTimeout)
	applyStringEnv("METASO_P2P_FEDERATION_DEFAULT_SCOPE", &cfg.Federation.DefaultScope)
	applyBoolEnv("METASO_P2P_FEDERATION_ALLOW_INSECURE_HTTP", &cfg.Federation.AllowInsecureHTTP)
	applyIntEnv("METASO_P2P_FEDERATION_MAX_PEERS", &cfg.Federation.MaxPeers)
	applyIntEnv("METASO_P2P_FEDERATION_MAX_SNAPSHOT_BYTES", &cfg.Federation.MaxSnapshotBytes)

	applyBoolEnv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_ENABLED", &cfg.BotHomepageV2Backfill.Enabled)
	applyDurationEnv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_LOOKBACK", &cfg.BotHomepageV2Backfill.Lookback)
	applyDurationEnv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_TIMEOUT", &cfg.BotHomepageV2Backfill.Timeout)
	applyIntEnv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_PAGE_SIZE", &cfg.BotHomepageV2Backfill.PageSize)
	applyStringEnv("METASO_P2P_BOT_HOMEPAGE_V2_BACKFILL_MANAPI_BASE_URL", &cfg.BotHomepageV2Backfill.MANAPIBaseURL)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.Service.HTTPAddr) == "" {
		return errors.New("service.httpAddr is required")
	}
	if !strings.HasPrefix(c.Service.HealthPath, "/") {
		return errors.New("service.healthPath must start with '/'")
	}
	if strings.TrimSpace(c.Socket.PrimaryPath) == "" {
		return errors.New("socket.primaryPath is required")
	}
	if strings.TrimSpace(c.Socket.LegacyPath) == "" {
		return errors.New("socket.legacyPath is required")
	}
	if c.Socket.MaxConnections <= 0 {
		return errors.New("socket.maxConnections must be greater than zero")
	}
	if c.Socket.MaxPCPerUser <= 0 {
		return errors.New("socket.maxPcPerUser must be greater than zero")
	}
	if c.Socket.MaxAppPerUser <= 0 {
		return errors.New("socket.maxAppPerUser must be greater than zero")
	}
	if c.Socket.PingInterval <= 0 {
		return errors.New("socket.pingInterval must be greater than zero")
	}
	if c.Socket.PingTimeout <= 0 {
		return errors.New("socket.pingTimeout must be greater than zero")
	}
	if c.Service.ShutdownTimeout <= 0 {
		return errors.New("service.shutdownTimeout must be greater than zero")
	}
	if err := c.validateFederation(); err != nil {
		return err
	}
	if err := c.validateBotHomepageV2Backfill(); err != nil {
		return err
	}
	return nil
}

func (c Config) validateBotHomepageV2Backfill() error {
	if c.BotHomepageV2Backfill.Lookback <= 0 {
		return errors.New("botHomepageV2Backfill.lookback must be greater than zero")
	}
	if c.BotHomepageV2Backfill.Timeout <= 0 {
		return errors.New("botHomepageV2Backfill.timeout must be greater than zero")
	}
	if c.BotHomepageV2Backfill.PageSize <= 0 {
		return errors.New("botHomepageV2Backfill.pageSize must be greater than zero")
	}
	if c.BotHomepageV2Backfill.Enabled && strings.TrimSpace(c.BotHomepageV2Backfill.MANAPIBaseURL) == "" {
		return errors.New("botHomepageV2Backfill.manapiBaseUrl is required when backfill is enabled")
	}
	return nil
}

func (c Config) validateFederation() error {
	if !c.Federation.Enabled {
		return nil
	}
	if strings.TrimSpace(c.Federation.NodePrivateKey) == "" {
		return errors.New("federation.nodePrivateKey is required when federation is enabled")
	}
	if strings.TrimSpace(c.Federation.PublicBaseURL) == "" {
		return errors.New("federation.publicBaseUrl is required when federation is enabled")
	}
	if !c.Federation.AllowInsecureHTTP && strings.HasPrefix(strings.ToLower(c.Federation.PublicBaseURL), "http://") {
		return errors.New("federation.publicBaseUrl must use https unless federation.allowInsecureHttp is true")
	}
	if strings.TrimSpace(c.Federation.MANAPIBaseURL) == "" {
		return errors.New("federation.manapiBaseUrl is required when federation is enabled")
	}
	if !strings.Contains(c.Federation.MANAPIBaseURL, "{protocol-path}") || !strings.Contains(c.Federation.MANAPIBaseURL, "{size}") {
		return errors.New("federation.manapiBaseUrl must contain {protocol-path} and {size}")
	}
	if strings.TrimSpace(c.Federation.MetaletBaseURL) == "" {
		return errors.New("federation.metaletBaseUrl is required when federation is enabled")
	}
	if !strings.HasPrefix(c.Federation.RegistryPath, "/") {
		return errors.New("federation.registryPath must start with '/'")
	}
	if !strings.HasPrefix(c.Federation.PresencePath, "/") {
		return errors.New("federation.presencePath must start with '/'")
	}
	if c.Federation.RegistryRenewInterval <= 0 {
		return errors.New("federation.registryRenewInterval must be greater than zero")
	}
	if c.Federation.RegistryValidFor <= 0 {
		return errors.New("federation.registryValidFor must be greater than zero")
	}
	if c.Federation.DiscoveryInterval <= 0 {
		return errors.New("federation.discoveryInterval must be greater than zero")
	}
	if c.Federation.PresencePullInterval <= 0 {
		return errors.New("federation.presencePullInterval must be greater than zero")
	}
	if c.Federation.PresenceTTL <= 0 {
		return errors.New("federation.presenceTTL must be greater than zero")
	}
	if c.Federation.RequestTimeout <= 0 {
		return errors.New("federation.requestTimeout must be greater than zero")
	}
	if c.Federation.DefaultScope != "local" && c.Federation.DefaultScope != "global" {
		return errors.New("federation.defaultScope must be local or global")
	}
	if c.Federation.MaxPeers < 0 {
		return errors.New("federation.maxPeers must be zero or greater")
	}
	if c.Federation.MaxSnapshotBytes < 0 {
		return errors.New("federation.maxSnapshotBytes must be zero or greater")
	}
	return nil
}

func applyStringEnv(name string, target *string) {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		*target = value
	}
}

func applyBoolEnv(name string, target *bool) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return
	}
	*target = parsed
}

func applyIntEnv(name string, target *int) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return
	}
	*target = parsed
}

func applyInt64Env(name string, target *int64) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return
	}
	*target = parsed
}

func applyDurationEnv(name string, target *time.Duration) {
	value, ok := os.LookupEnv(name)
	if !ok {
		return
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return
	}
	*target = parsed
}

func applyChainRPCEnv(prefix string, target *ChainRPCConfig) {
	applyBoolEnv(prefix+"_ENABLED", &target.Enabled)
	applyStringEnv(prefix+"_RPC_HOST", &target.RPCHost)
	applyStringEnv(prefix+"_RPC_USER", &target.RPCUser)
	applyStringEnv(prefix+"_RPC_PASS", &target.RPCPass)
	applyBoolEnv(prefix+"_RPC_HTTP_POST_MODE", &target.RPCHTTPPostMode)
	applyBoolEnv(prefix+"_RPC_DISABLE_TLS", &target.RPCDisableTLS)
	applyInt64Env(prefix+"_INITIAL_HEIGHT", &target.InitialHeight)
}

func (c Config) Summary() string {
	return fmt.Sprintf(
		"listen=%s health=%s socket_enabled=%t socket_path=%s socket_legacy_path=%s socket_room_broadcast_enabled=%t socket_max_connections=%d socket_pc_limit=%d socket_app_limit=%d zmq_enabled=%t block_index_enabled=%t pebble_enabled=%t profile_enabled=%t groupchat_migration_enabled=%t groupchat_backup_enabled=%t",
		c.Service.HTTPAddr,
		c.Service.HealthPath,
		c.Socket.Enabled,
		c.Socket.PrimaryPath,
		c.Socket.LegacyPath,
		c.Socket.RoomBroadcastEnabled,
		c.Socket.MaxConnections,
		c.Socket.MaxPCPerUser,
		c.Socket.MaxAppPerUser,
		c.ZMQ.Enabled,
		c.BlockIndex.Enabled,
		c.Pebble.Enabled,
		c.Profile.Enabled,
		c.GroupChat.MigrationEnabled,
		c.GroupChat.BackupEnabled,
	)
}
