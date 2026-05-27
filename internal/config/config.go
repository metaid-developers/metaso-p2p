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
	Service    ServiceConfig    `json:"service"`
	Socket     SocketConfig     `json:"socket"`
	ZMQ        ZMQConfig        `json:"zmq"`
	BlockIndex BlockIndexConfig `json:"blockIndex"`
	Pebble     PebbleConfig     `json:"pebble"`
	Cache      CacheConfig      `json:"cache"`
	Profile    ProfileConfig    `json:"profile"`
	GroupChat  GroupChatConfig  `json:"groupChat"`
	BotHub     BotHubConfig     `json:"botHub"`
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

type BlockIndexConfig struct {
	Enabled bool   `json:"enabled"`
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
	MaxEntries int `json:"maxEntries"`
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
	Enabled bool           `json:"enabled"`
	BTC     ChainZMQConfig `json:"btc"`
	MVC     ChainZMQConfig `json:"mvc"`
	DOGE    ChainZMQConfig `json:"doge"`
	OPCAT   ChainZMQConfig `json:"opcat"`
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
			Enabled: false,
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
			BTC:   ChainRPCConfig{Enabled: false, RPCHTTPPostMode: true, RPCDisableTLS: true},
			MVC:   ChainRPCConfig{Enabled: false, RPCHTTPPostMode: true, RPCDisableTLS: true},
			DOGE:  ChainRPCConfig{Enabled: false, RPCHTTPPostMode: true, RPCDisableTLS: true},
			OPCAT: ChainRPCConfig{Enabled: false, RPCHTTPPostMode: true, RPCDisableTLS: true},
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
			// override with META_SOCKET_ASSET_BASE_URL when running
			// against a different MetaID asset host.
			AssetBaseURL: "https://manapi.metaid.io/content",
		},
	}
}

func Load() (Config, error) {
	cfg := Default()

	applyStringEnv("META_SOCKET_HTTP_ADDR", &cfg.Service.HTTPAddr)
	applyStringEnv("META_SOCKET_HEALTH_PATH", &cfg.Service.HealthPath)
	applyDurationEnv("META_SOCKET_SHUTDOWN_TIMEOUT", &cfg.Service.ShutdownTimeout)

	applyBoolEnv("META_SOCKET_SOCKET_ENABLED", &cfg.Socket.Enabled)
	applyStringEnv("META_SOCKET_SOCKET_PATH", &cfg.Socket.PrimaryPath)
	applyStringEnv("META_SOCKET_SOCKET_LEGACY_PATH", &cfg.Socket.LegacyPath)
	applyBoolEnv("META_SOCKET_SOCKET_ROOM_BROADCAST_ENABLED", &cfg.Socket.RoomBroadcastEnabled)
	applyIntEnv("META_SOCKET_SOCKET_MAX_CONNECTIONS", &cfg.Socket.MaxConnections)
	applyIntEnv("META_SOCKET_SOCKET_MAX_PC_PER_USER", &cfg.Socket.MaxPCPerUser)
	applyIntEnv("META_SOCKET_SOCKET_MAX_APP_PER_USER", &cfg.Socket.MaxAppPerUser)
	applyDurationEnv("META_SOCKET_SOCKET_PING_INTERVAL", &cfg.Socket.PingInterval)
	applyDurationEnv("META_SOCKET_SOCKET_PING_TIMEOUT", &cfg.Socket.PingTimeout)
	applyBoolEnv("META_SOCKET_SOCKET_ALLOW_EIO3", &cfg.Socket.AllowEIO3)
	applyStringEnv("META_SOCKET_SOCKET_EXTRA_PUSH_AUTH_KEY", &cfg.Socket.ExtraPushAuthKey)

	applyBoolEnv("META_SOCKET_ZMQ_ENABLED", &cfg.ZMQ.Enabled)
	applyBoolEnv("META_SOCKET_ZMQ_BTC_ENABLED", &cfg.ZMQ.BTC.Enabled)
	applyStringEnv("META_SOCKET_ZMQ_BTC_ENDPOINT", &cfg.ZMQ.BTC.Endpoint)
	applyStringEnv("META_SOCKET_ZMQ_BTC_TOPIC", &cfg.ZMQ.BTC.Topic)
	applyStringEnv("META_SOCKET_ZMQ_BTC_RPC_HOST", &cfg.ZMQ.BTC.RPCHost)
	applyStringEnv("META_SOCKET_ZMQ_BTC_RPC_USER", &cfg.ZMQ.BTC.RPCUser)
	applyStringEnv("META_SOCKET_ZMQ_BTC_RPC_PASS", &cfg.ZMQ.BTC.RPCPass)
	applyBoolEnv("META_SOCKET_ZMQ_BTC_RPC_HTTP_POST_MODE", &cfg.ZMQ.BTC.RPCHTTPPostMode)
	applyBoolEnv("META_SOCKET_ZMQ_BTC_RPC_DISABLE_TLS", &cfg.ZMQ.BTC.RPCDisableTLS)
	applyBoolEnv("META_SOCKET_ZMQ_MVC_ENABLED", &cfg.ZMQ.MVC.Enabled)
	applyStringEnv("META_SOCKET_ZMQ_MVC_ENDPOINT", &cfg.ZMQ.MVC.Endpoint)
	applyStringEnv("META_SOCKET_ZMQ_MVC_TOPIC", &cfg.ZMQ.MVC.Topic)
	applyStringEnv("META_SOCKET_ZMQ_MVC_RPC_HOST", &cfg.ZMQ.MVC.RPCHost)
	applyStringEnv("META_SOCKET_ZMQ_MVC_RPC_USER", &cfg.ZMQ.MVC.RPCUser)
	applyStringEnv("META_SOCKET_ZMQ_MVC_RPC_PASS", &cfg.ZMQ.MVC.RPCPass)
	applyBoolEnv("META_SOCKET_ZMQ_MVC_RPC_HTTP_POST_MODE", &cfg.ZMQ.MVC.RPCHTTPPostMode)
	applyBoolEnv("META_SOCKET_ZMQ_MVC_RPC_DISABLE_TLS", &cfg.ZMQ.MVC.RPCDisableTLS)
	applyBoolEnv("META_SOCKET_ZMQ_DOGE_ENABLED", &cfg.ZMQ.DOGE.Enabled)
	applyStringEnv("META_SOCKET_ZMQ_DOGE_ENDPOINT", &cfg.ZMQ.DOGE.Endpoint)
	applyStringEnv("META_SOCKET_ZMQ_DOGE_TOPIC", &cfg.ZMQ.DOGE.Topic)
	applyStringEnv("META_SOCKET_ZMQ_DOGE_RPC_HOST", &cfg.ZMQ.DOGE.RPCHost)
	applyStringEnv("META_SOCKET_ZMQ_DOGE_RPC_USER", &cfg.ZMQ.DOGE.RPCUser)
	applyStringEnv("META_SOCKET_ZMQ_DOGE_RPC_PASS", &cfg.ZMQ.DOGE.RPCPass)
	applyBoolEnv("META_SOCKET_ZMQ_DOGE_RPC_HTTP_POST_MODE", &cfg.ZMQ.DOGE.RPCHTTPPostMode)
	applyBoolEnv("META_SOCKET_ZMQ_DOGE_RPC_DISABLE_TLS", &cfg.ZMQ.DOGE.RPCDisableTLS)

	applyBoolEnv("META_SOCKET_PEBBLE_ENABLED", &cfg.Pebble.Enabled)
	applyStringEnv("META_SOCKET_PEBBLE_DATA_DIR", &cfg.Pebble.DataDir)

	applyBoolEnv("META_SOCKET_PROFILE_ENABLED", &cfg.Profile.Enabled)
	applyStringEnv("META_SOCKET_PROFILE_MODE", &cfg.Profile.Mode)
	applyStringEnv("META_SOCKET_PROFILE_REMOTE_BASE_URL", &cfg.Profile.RemoteBaseURL)
	applyBoolEnv("META_SOCKET_PROFILE_ALLOW_REMOTE_FALLBACK", &cfg.Profile.AllowRemoteFallback)

	applyBoolEnv("META_SOCKET_GROUPCHAT_MIGRATION_ENABLED", &cfg.GroupChat.MigrationEnabled)
	applyBoolEnv("META_SOCKET_GROUPCHAT_BACKUP_ENABLED", &cfg.GroupChat.BackupEnabled)
	applyBoolEnv("META_SOCKET_GROUPCHAT_LUCKYBAG_ENABLED", &cfg.GroupChat.LuckyBagEnabled)
	applyBoolEnv("META_SOCKET_GROUPCHAT_GRPC_ENABLED", &cfg.GroupChat.GRPCEnabled)
	applyBoolEnv("META_SOCKET_GROUPCHAT_HEAVY_API_ENABLED", &cfg.GroupChat.HeavyAPIEnabled)

	applyStringEnv("META_SOCKET_ASSET_BASE_URL", &cfg.BotHub.AssetBaseURL)

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

func (c Config) Summary() string {
	return fmt.Sprintf(
		"listen=%s health=%s socket_enabled=%t socket_path=%s socket_legacy_path=%s socket_room_broadcast_enabled=%t socket_max_connections=%d socket_pc_limit=%d socket_app_limit=%d zmq_enabled=%t pebble_enabled=%t profile_enabled=%t groupchat_migration_enabled=%t groupchat_backup_enabled=%t",
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
		c.Pebble.Enabled,
		c.Profile.Enabled,
		c.GroupChat.MigrationEnabled,
		c.GroupChat.BackupEnabled,
	)
}
