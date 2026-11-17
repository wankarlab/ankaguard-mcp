package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// GCTConfig represents the complete GoCryptoTrader integration configuration
type GCTConfig struct {
	GoCryptoTrader GCTConnectionConfig `json:"gocryptotrader" mapstructure:"gocryptotrader"`
	TradingMCP     MCPServerConfig     `json:"trading_mcp" mapstructure:"trading_mcp"`
	Exchanges      ExchangesConfig     `json:"exchanges" mapstructure:"exchanges"`
	Features       FeaturesConfig      `json:"features" mapstructure:"features"`
}

// GCTConnectionConfig represents GoCryptoTrader connection settings
type GCTConnectionConfig struct {
	GRPC      GRPCConfig      `json:"grpc" mapstructure:"grpc"`
	WebSocket WebSocketConfig `json:"websocket" mapstructure:"websocket"`
	Auth      AuthConfig      `json:"auth" mapstructure:"auth"`
}

// GRPCConfig represents gRPC connection settings
type GRPCConfig struct {
	Address           string    `json:"address" mapstructure:"address"`
	TimeoutSeconds    int       `json:"timeout_seconds" mapstructure:"timeout_seconds"`
	MaxRetries        int       `json:"max_retries" mapstructure:"max_retries"`
	RetryDelaySeconds int       `json:"retry_delay_seconds" mapstructure:"retry_delay_seconds"`
	TLS               TLSConfig `json:"tls" mapstructure:"tls"`
}

// TLSConfig represents TLS settings
type TLSConfig struct {
	Enabled            bool `json:"enabled" mapstructure:"enabled"`
	InsecureSkipVerify bool `json:"insecure_skip_verify" mapstructure:"insecure_skip_verify"`
}

// AuthConfig represents authentication settings
type AuthConfig struct {
	Username string `json:"username" mapstructure:"username"`
	Password string `json:"password" mapstructure:"password"`
}

// MCPServerConfig represents Trading MCP server configuration
type MCPServerConfig struct {
	Server       ServerConfig       `json:"server" mapstructure:"server"`
	Logging      LoggingConfig      `json:"logging" mapstructure:"logging"`
	Cache        CacheConfig        `json:"cache" mapstructure:"cache"`
	RateLimiting RateLimitingConfig `json:"rate_limiting" mapstructure:"rate_limiting"`
}

// ServerConfig represents server settings
type ServerConfig struct {
	Host            string `json:"host" mapstructure:"host"`
	Port            int    `json:"port" mapstructure:"port"`
	HealthCheckPort int    `json:"health_check_port" mapstructure:"health_check_port"`
}

// LoggingConfig represents logging settings
type LoggingConfig struct {
	Level  string        `json:"level" mapstructure:"level"`
	Format string        `json:"format" mapstructure:"format"`
	Output string        `json:"output" mapstructure:"output"`
	File   FileLogConfig `json:"file" mapstructure:"file"`
}

// FileLogConfig represents file logging settings
type FileLogConfig struct {
	Enabled    bool   `json:"enabled" mapstructure:"enabled"`
	Path       string `json:"path" mapstructure:"path"`
	MaxSizeMB  int    `json:"max_size_mb" mapstructure:"max_size_mb"`
	MaxBackups int    `json:"max_backups" mapstructure:"max_backups"`
	MaxAgeDays int    `json:"max_age_days" mapstructure:"max_age_days"`
}

// CacheConfig represents cache settings
type CacheConfig struct {
	Enabled    bool `json:"enabled" mapstructure:"enabled"`
	TTLSeconds int  `json:"ttl_seconds" mapstructure:"ttl_seconds"`
	MaxEntries int  `json:"max_entries" mapstructure:"max_entries"`
}

// RateLimitingConfig represents rate limiting settings
type RateLimitingConfig struct {
	Enabled           bool `json:"enabled" mapstructure:"enabled"`
	RequestsPerSecond int  `json:"requests_per_second" mapstructure:"requests_per_second"`
	Burst             int  `json:"burst" mapstructure:"burst"`
}

// ExchangesConfig represents exchange configurations
type ExchangesConfig struct {
	KuCoin ExchangeSettings `json:"kucoin" mapstructure:"kucoin"`
}

// ExchangeSettings represents settings for a specific exchange
type ExchangeSettings struct {
	Enabled          bool     `json:"enabled" mapstructure:"enabled"`
	TradingPairs     []string `json:"trading_pairs" mapstructure:"trading_pairs"`
	DefaultAssetType string   `json:"default_asset_type" mapstructure:"default_asset_type"`
}

// FeaturesConfig represents feature flags
type FeaturesConfig struct {
	HotTesting    HotTestingConfig    `json:"hot_testing" mapstructure:"hot_testing"`
	Backtesting   BacktestingConfig   `json:"backtesting" mapstructure:"backtesting"`
	Notifications NotificationsConfig `json:"notifications" mapstructure:"notifications"`
}

// HotTestingConfig represents hot testing settings
type HotTestingConfig struct {
	Enabled            bool    `json:"enabled" mapstructure:"enabled"`
	MaxPositionSizeUSD float64 `json:"max_position_size_usd" mapstructure:"max_position_size_usd"`
	MaxDailyLossUSD    float64 `json:"max_daily_loss_usd" mapstructure:"max_daily_loss_usd"`
}

// BacktestingConfig represents backtesting settings
type BacktestingConfig struct {
	Enabled       bool   `json:"enabled" mapstructure:"enabled"`
	DataDirectory string `json:"data_directory" mapstructure:"data_directory"`
}

// NotificationsConfig represents notifications settings
type NotificationsConfig struct {
	Enabled  bool     `json:"enabled" mapstructure:"enabled"`
	Channels []string `json:"channels" mapstructure:"channels"`
}

// LoadGCTConfig loads the GoCryptoTrader configuration from a JSON file
func LoadGCTConfig(path string) (*GCTConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config GCTConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}

// ToGCTClientConfig converts GCTConfig to gct.Config
func (c *GCTConfig) ToGCTClientConfig() *gct.Config {
	return &gct.Config{
		Address:  c.GoCryptoTrader.GRPC.Address,
		Username: c.GoCryptoTrader.Auth.Username,
		Password: c.GoCryptoTrader.Auth.Password,
		Timeout:  time.Duration(c.GoCryptoTrader.GRPC.TimeoutSeconds) * time.Second,
	}
}

// Validate validates the configuration
func (c *GCTConfig) Validate() error {
	// Validate gRPC settings
	if c.GoCryptoTrader.GRPC.Address == "" {
		return fmt.Errorf("gRPC address is required")
	}
	if c.GoCryptoTrader.GRPC.TimeoutSeconds <= 0 {
		return fmt.Errorf("gRPC timeout must be positive")
	}
	if c.GoCryptoTrader.GRPC.MaxRetries < 0 {
		return fmt.Errorf("max retries cannot be negative")
	}

	// Validate auth settings
	if c.GoCryptoTrader.Auth.Username == "" {
		return fmt.Errorf("GoCryptoTrader username is required")
	}
	if c.GoCryptoTrader.Auth.Password == "" {
		return fmt.Errorf("GoCryptoTrader password is required")
	}

	// Validate server settings
	if c.TradingMCP.Server.Port <= 0 || c.TradingMCP.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.TradingMCP.Server.Port)
	}

	// Validate logging level
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.TradingMCP.Logging.Level] {
		return fmt.Errorf("invalid log level: %s", c.TradingMCP.Logging.Level)
	}

	// Validate hot testing settings if enabled
	if c.Features.HotTesting.Enabled {
		if c.Features.HotTesting.MaxPositionSizeUSD <= 0 {
			return fmt.Errorf("max position size must be positive when hot testing is enabled")
		}
		if c.Features.HotTesting.MaxDailyLossUSD <= 0 {
			return fmt.Errorf("max daily loss must be positive when hot testing is enabled")
		}
	}

	return nil
}

// ValidateGCTConnection validates the connection to GoCryptoTrader
func (c *GCTConfig) ValidateGCTConnection(ctx context.Context) error {
	// Create GCT client
	gctConfig := c.ToGCTClientConfig()
	client := gct.NewClient(gctConfig)

	// Try to connect
	if err := client.Connect(ctx, gctConfig.Address); err != nil {
		return fmt.Errorf("failed to connect to GoCryptoTrader: %w", err)
	}
	defer client.Disconnect()

	// Perform health check
	if err := client.HealthCheck(ctx); err != nil {
		return fmt.Errorf("GoCryptoTrader health check failed: %w", err)
	}

	// Verify exchanges are configured
	exchanges, err := client.GetEnabledExchanges(ctx)
	if err != nil {
		return fmt.Errorf("failed to get enabled exchanges: %w", err)
	}

	if len(exchanges) == 0 {
		return fmt.Errorf("no exchanges are enabled in GoCryptoTrader")
	}

	// Check if KuCoin is enabled (if configured)
	if c.Exchanges.KuCoin.Enabled {
		kucoinEnabled := false
		for _, exchange := range exchanges {
			if exchange == "kucoin" {
				kucoinEnabled = true
				break
			}
		}
		if !kucoinEnabled {
			return fmt.Errorf("KuCoin is enabled in config but not in GoCryptoTrader")
		}
	}

	return nil
}

// GetDefaultGCTConfig returns a default configuration
func GetDefaultGCTConfig() *GCTConfig {
	return &GCTConfig{
		GoCryptoTrader: GCTConnectionConfig{
			GRPC: GRPCConfig{
				Address:           "localhost:9052",
				TimeoutSeconds:    30,
				MaxRetries:        3,
				RetryDelaySeconds: 2,
				TLS: TLSConfig{
					Enabled:            true,
					InsecureSkipVerify: true,
				},
			},
			WebSocket: WebSocketConfig{
				Enabled:            true,
				MaxConnections:     1000,
				MaxMessageSize:     1024 * 1024,
				WriteTimeout:       10 * time.Second,
				ReadTimeout:        60 * time.Second,
				PingInterval:       54 * time.Second,
				PongTimeout:        60 * time.Second,
				CompressionEnabled: true,
			},
			Auth: AuthConfig{
				Username: "admin",
				Password: "Password",
			},
		},
		TradingMCP: MCPServerConfig{
			Server: ServerConfig{
				Host:            "localhost",
				Port:            10600,  // Changed from 8080
				HealthCheckPort: 10600,
			},
			Logging: LoggingConfig{
				Level:  "info",
				Format: "json",
				Output: "stdout",
				File: FileLogConfig{
					Enabled:    true,
					Path:       "logs/trading-mcp.log",
					MaxSizeMB:  100,
					MaxBackups: 5,
					MaxAgeDays: 30,
				},
			},
			Cache: CacheConfig{
				Enabled:    true,
				TTLSeconds: 30,
				MaxEntries: 1000,
			},
			RateLimiting: RateLimitingConfig{
				Enabled:           true,
				RequestsPerSecond: 10,
				Burst:             20,
			},
		},
		Exchanges: ExchangesConfig{
			KuCoin: ExchangeSettings{
				Enabled:          true,
				TradingPairs:     []string{"BTC-USDT", "ETH-USDT", "BNB-USDT"},
				DefaultAssetType: "spot",
			},
		},
		Features: FeaturesConfig{
			HotTesting: HotTestingConfig{
				Enabled:            false,
				MaxPositionSizeUSD: 100,
				MaxDailyLossUSD:    50,
			},
			Backtesting: BacktestingConfig{
				Enabled:       true,
				DataDirectory: "data/historical",
			},
			Notifications: NotificationsConfig{
				Enabled:  true,
				Channels: []string{"slack", "email"},
			},
		},
	}
}
