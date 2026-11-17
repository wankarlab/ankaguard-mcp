package config

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	// Server configuration
	Port     int    `mapstructure:"port"`
	Host     string `mapstructure:"host"`
	LogLevel string `mapstructure:"log_level"`

	// Database configuration
	Database DatabaseConfig `mapstructure:"database"`

	// Valkey configuration
	Valkey ValkeyConfig `mapstructure:"redis"`

	// JWT configuration
	JWT JWTConfig `mapstructure:"jwt"`

	// Exchange configuration
	Exchanges map[string]ExchangeConfig `mapstructure:"exchanges"`

	// Auth service configuration
	AuthService AuthServiceConfig `mapstructure:"auth_service"`

	// Trading configuration
	Trading TradingConfig `mapstructure:"trading"`

	// Risk management configuration
	Risk RiskConfig `mapstructure:"risk"`

	// Monitoring configuration
	Monitoring MonitoringConfig `mapstructure:"monitoring"`

	// GoCryptoTrader configuration
	GoCryptoTrader GoCryptoTraderConfig `mapstructure:"gocryptotrader"`

	// Notification configuration
	Notifications NotificationConfig `mapstructure:"notifications"`

	// WebSocket configuration
	WebSocket WebSocketConfig `mapstructure:"websocket"`

	// MCP configuration
	MCP MCPConfig `mapstructure:"mcp"`

	// AI configuration
	AI AIConfig `mapstructure:"ai"`
}

type DatabaseConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	User         string `mapstructure:"user"`
	Password     string `mapstructure:"password"`
	Name         string `mapstructure:"name"`
	SSLMode      string `mapstructure:"ssl_mode"`
	MaxOpenConns int    `mapstructure:"max_open_conns"`
	MaxIdleConns int    `mapstructure:"max_idle_conns"`
}

// ValkeyConfig represents Valkey/Valkey configuration (using Valkey-compatible client)
type ValkeyConfig struct {
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

type JWTConfig struct {
	Secret     string `mapstructure:"secret"`
	Issuer     string `mapstructure:"issuer"`
	Audience   string `mapstructure:"audience"`
	Expiration int    `mapstructure:"expiration"`
}

type ExchangeConfig struct {
	Name       string `mapstructure:"name"`
	APIKey     string `mapstructure:"api_key"`
	APISecret  string `mapstructure:"api_secret"`
	Passphrase string `mapstructure:"passphrase"`
	Sandbox    bool   `mapstructure:"sandbox"`
	Enabled    bool   `mapstructure:"enabled"`
}

type AuthServiceConfig struct {
	BaseURL string `mapstructure:"base_url"`
	Timeout int    `mapstructure:"timeout"`
}

type TradingConfig struct {
	PaperTrading      bool    `mapstructure:"paper_trading"`
	MaxOrdersPerMin   int     `mapstructure:"max_orders_per_min"`
	MaxLeverage       int     `mapstructure:"max_leverage"`
	RiskPerTrade      float64 `mapstructure:"risk_per_trade"`
	DefaultSlippage   float64 `mapstructure:"default_slippage"`
	DefaultCommission float64 `mapstructure:"default_commission"`
}

type RiskConfig struct {
	MaxDrawdown         float64 `mapstructure:"max_drawdown"`
	MaxExposurePerAsset float64 `mapstructure:"max_exposure_per_asset"`
	KellyCriterion      bool    `mapstructure:"kelly_criterion"`
	DynamicStopLoss     bool    `mapstructure:"dynamic_stop_loss"`
}

type MonitoringConfig struct {
	PrometheusPort int  `mapstructure:"prometheus_port"`
	HealthCheck    bool `mapstructure:"health_check"`
	MetricsEnabled bool `mapstructure:"metrics_enabled"`
}

type GoCryptoTraderConfig struct {
	GRPCAddress string        `mapstructure:"grpc_address"`
	HTTPAddress string        `mapstructure:"http_address"`
	TLSEnabled  bool          `mapstructure:"tls_enabled"`
	Username    string        `mapstructure:"username"`
	Password    string        `mapstructure:"password"`
	Timeout     time.Duration `mapstructure:"timeout"`
	MaxRetries  int           `mapstructure:"max_retries"`
	RetryDelay  time.Duration `mapstructure:"retry_delay"`
}

// Load loads configuration from environment variables and config files
func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./configs")
	viper.AddConfigPath("/etc/trading-mcp")

	// Set default values
	setDefaults()

	// Enable environment variable support
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	// Read config file (optional)
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		log.Printf("No config file found, using defaults")
	} else {
		log.Printf("Loaded config from: %s", viper.ConfigFileUsed())
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	log.Printf("Config loaded - Port: %d, Host: %s", config.Port, config.Host)
	return &config, nil
}

func setDefaults() {
	// Server defaults
	viper.SetDefault("port", 10600) // Changed from 8080 to 10600 (MCP server port)
	viper.SetDefault("host", "0.0.0.0")
	viper.SetDefault("log_level", "info") // Info level

	// Database defaults
	viper.SetDefault("database.host", "localhost")
	viper.SetDefault("database.port", 5432)
	viper.SetDefault("database.user", "postgres")
	viper.SetDefault("database.password", "")
	viper.SetDefault("database.name", "trading_mcp")
	viper.SetDefault("database.ssl_mode", "disable")
	viper.SetDefault("database.max_open_conns", 50)
	viper.SetDefault("database.max_idle_conns", 10)

	// Valkey/Valkey defaults
	viper.SetDefault("redis.host", "localhost")
	viper.SetDefault("redis.port", 6379)
	viper.SetDefault("redis.password", "")
	viper.SetDefault("redis.db", 0)
	viper.SetDefault("redis.pool_size", 10)

	// JWT defaults
	viper.SetDefault("jwt.secret", "your-secret-key")
	viper.SetDefault("jwt.issuer", "trading-mcp")
	viper.SetDefault("jwt.audience", "trading-mcp")
	viper.SetDefault("jwt.expiration", 3600) // 1 hour

	// Auth service defaults
	viper.SetDefault("auth_service.base_url", "http://localhost:10100")
	viper.SetDefault("auth_service.timeout", 30)

	// Trading defaults
	viper.SetDefault("trading.paper_trading", true)
	viper.SetDefault("trading.max_orders_per_min", 50)
	viper.SetDefault("trading.max_leverage", 20)
	viper.SetDefault("trading.risk_per_trade", 0.02)
	viper.SetDefault("trading.default_slippage", 0.001)
	viper.SetDefault("trading.default_commission", 0.001)

	// Risk defaults
	viper.SetDefault("risk.max_drawdown", 0.10)
	viper.SetDefault("risk.max_exposure_per_asset", 0.20)
	viper.SetDefault("risk.kelly_criterion", true)
	viper.SetDefault("risk.dynamic_stop_loss", true)

	// Monitoring defaults
	viper.SetDefault("monitoring.prometheus_port", 9090)
	viper.SetDefault("monitoring.health_check", true)
	viper.SetDefault("monitoring.metrics_enabled", true)

	// GoCryptoTrader defaults
	viper.SetDefault("gocryptotrader.grpc_address", "localhost:9052")
	viper.SetDefault("gocryptotrader.http_address", "http://localhost:9050")
	viper.SetDefault("gocryptotrader.tls_enabled", false)
	viper.SetDefault("gocryptotrader.username", "")
	viper.SetDefault("gocryptotrader.password", "")
	viper.SetDefault("gocryptotrader.timeout", "30s")
	viper.SetDefault("gocryptotrader.max_retries", 3)
	viper.SetDefault("gocryptotrader.retry_delay", "1s")

	// Notification defaults
	viper.SetDefault("notifications.enabled", true)
	viper.SetDefault("notifications.push_service.enabled", false)
	viper.SetDefault("notifications.push_service.fcm_endpoint", "https://fcm.googleapis.com/fcm/send")
	viper.SetDefault("notifications.push_service.apns_endpoint", "https://api.push.apple.com")
	viper.SetDefault("notifications.webhook_url", "")
	viper.SetDefault("notifications.slack_webhook_url", "")

	// WebSocket defaults
	viper.SetDefault("websocket.enabled", true)
	viper.SetDefault("websocket.max_connections", 1000)
	viper.SetDefault("websocket.max_message_size", 1024*1024) // 1MB
	viper.SetDefault("websocket.write_timeout", "10s")
	viper.SetDefault("websocket.read_timeout", "60s")
	viper.SetDefault("websocket.ping_interval", "54s")
	viper.SetDefault("websocket.pong_timeout", "60s")
	viper.SetDefault("websocket.compression_enabled", true)

	// MCP defaults
	viper.SetDefault("mcp.version", "2024-11-05")
	viper.SetDefault("mcp.server_name", "trading-mcp")
	viper.SetDefault("mcp.server_version", "1.0.0")
	viper.SetDefault("mcp.max_request_size", 10*1024*1024) // 10MB
	viper.SetDefault("mcp.request_timeout", "30s")
	viper.SetDefault("mcp.log_requests", false)
	viper.SetDefault("mcp.log_responses", false)
	viper.SetDefault("mcp.enable_streaming", true)

	// AI defaults
	viper.SetDefault("ai.enabled", false) // Disabled by default

	// LLM defaults
	viper.SetDefault("ai.llm.enabled", false)
	viper.SetDefault("ai.llm.provider", "openai")
	viper.SetDefault("ai.llm.model", "gpt-4")
	viper.SetDefault("ai.llm.api_key", "")
	viper.SetDefault("ai.llm.max_calls_per_day", 100)
	viper.SetDefault("ai.llm.timeout_seconds", 10)
	viper.SetDefault("ai.llm.cache_ttl_minutes", 5)
	viper.SetDefault("ai.llm.fallback_to_technical", true)
	viper.SetDefault("ai.llm.temperature", 0.7)
	viper.SetDefault("ai.llm.max_tokens", 1000)
	viper.SetDefault("ai.llm.fallback_provider", "")

	// Sentiment defaults
	viper.SetDefault("ai.sentiment.enabled", false)
	viper.SetDefault("ai.sentiment.cache_ttl_minutes", 30)
	viper.SetDefault("ai.sentiment.sources.cryptopanic.enabled", false)
	viper.SetDefault("ai.sentiment.sources.cryptopanic.api_key", "")
	viper.SetDefault("ai.sentiment.sources.cryptopanic.max_items", 50)
	viper.SetDefault("ai.sentiment.sources.cryptopanic.weight", 0.4)
	viper.SetDefault("ai.sentiment.sources.twitter.enabled", false)
	viper.SetDefault("ai.sentiment.sources.twitter.api_key", "")
	viper.SetDefault("ai.sentiment.sources.twitter.max_items", 1000)
	viper.SetDefault("ai.sentiment.sources.twitter.weight", 0.3)
	viper.SetDefault("ai.sentiment.sources.reddit.enabled", false)
	viper.SetDefault("ai.sentiment.sources.reddit.api_key", "")
	viper.SetDefault("ai.sentiment.sources.reddit.max_items", 100)
	viper.SetDefault("ai.sentiment.sources.reddit.weight", 0.3)

	// Cost management defaults
	viper.SetDefault("ai.cost_management.enabled", true)
	viper.SetDefault("ai.cost_management.daily_limits.openai", 5.0)
	viper.SetDefault("ai.cost_management.daily_limits.anthropic", 3.0)
	viper.SetDefault("ai.cost_management.daily_limits.google", 2.0)
	viper.SetDefault("ai.cost_management.daily_limits.cryptopanic", 1.0)
	viper.SetDefault("ai.cost_management.alerts.threshold_percent", 80)
	viper.SetDefault("ai.cost_management.alerts.notify_on_limit", true)

	// Auto-configuration defaults
	viper.SetDefault("ai.auto_configuration.enabled", false)
	viper.SetDefault("ai.auto_configuration.reconfigure_interval_hours", 6)
	viper.SetDefault("ai.auto_configuration.strategies", []string{"trend_following", "scalping", "swing_trading", "grid_trading", "multi_timeframe"})
	viper.SetDefault("ai.auto_configuration.risk_adjustment.enabled", true)
	viper.SetDefault("ai.auto_configuration.risk_adjustment.based_on", "volatility")
}

// NotificationConfig represents notification system configuration
type NotificationConfig struct {
	Enabled         bool              `mapstructure:"enabled"`
	PushService     PushServiceConfig `mapstructure:"push_service"`
	WebhookURL      string            `mapstructure:"webhook_url"`
	SlackWebhookURL string            `mapstructure:"slack_webhook_url"`
	AlertRules      []AlertRuleConfig `mapstructure:"alert_rules"`
}

// PushServiceConfig represents push notification service configuration
type PushServiceConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	FCMServerKey string `mapstructure:"fcm_server_key"`
	FCMEndpoint  string `mapstructure:"fcm_endpoint"`
	APNSKeyID    string `mapstructure:"apns_key_id"`
	APNSTeamID   string `mapstructure:"apns_team_id"`
	APNSBundleID string `mapstructure:"apns_bundle_id"`
	APNSEndpoint string `mapstructure:"apns_endpoint"`
}

// AlertRuleConfig is the YAML/mapstructure representation of an alert rule loaded
// from config.yaml. It is distinct from notifications.AlertRule, which is the
// runtime struct used by MCPAlertGenerator and held in-memory. Config loading
// code is responsible for converting AlertRuleConfig → notifications.AlertRule.
type AlertRuleConfig struct {
	Name       string                 `mapstructure:"name"`
	ToolName   string                 `mapstructure:"tool_name"`
	Conditions []AlertConditionConfig `mapstructure:"conditions"`
	Severity   string                 `mapstructure:"severity"`
	Channels   []string               `mapstructure:"channels"`
	Cooldown   string                 `mapstructure:"cooldown"`
	Enabled    bool                   `mapstructure:"enabled"`
}

// AlertConditionConfig is the YAML/mapstructure counterpart of
// notifications.AlertCondition. Used only during config loading.
type AlertConditionConfig struct {
	Field    string      `mapstructure:"field"`
	Operator string      `mapstructure:"operator"`
	Value    interface{} `mapstructure:"value"`
	Type     string      `mapstructure:"type"`
}

// WebSocketConfig represents WebSocket configuration
type WebSocketConfig struct {
	Enabled            bool          `mapstructure:"enabled"`
	MaxConnections     int           `mapstructure:"max_connections"`
	MaxMessageSize     int64         `mapstructure:"max_message_size"`
	WriteTimeout       time.Duration `mapstructure:"write_timeout"`
	ReadTimeout        time.Duration `mapstructure:"read_timeout"`
	PingInterval       time.Duration `mapstructure:"ping_interval"`
	PongTimeout        time.Duration `mapstructure:"pong_timeout"`
	AllowedOrigins     []string      `mapstructure:"allowed_origins"`
	CompressionEnabled bool          `mapstructure:"compression_enabled"`
}

// MCPConfig represents MCP protocol configuration
type MCPConfig struct {
	Version         string        `mapstructure:"version"`
	ServerName      string        `mapstructure:"server_name"`
	ServerVersion   string        `mapstructure:"server_version"`
	MaxRequestSize  int64         `mapstructure:"max_request_size"`
	RequestTimeout  time.Duration `mapstructure:"request_timeout"`
	LogRequests     bool          `mapstructure:"log_requests"`
	LogResponses    bool          `mapstructure:"log_responses"`
	EnableStreaming bool          `mapstructure:"enable_streaming"`
}

// ExchangeAPIConfig represents enhanced exchange API configuration
type ExchangeAPIConfig struct {
	ExchangeConfig
	Passphrase  string           `mapstructure:"passphrase"`  // For some exchanges like Coinbase Pro
	SubAccount  string           `mapstructure:"sub_account"` // For sub-account trading
	Environment string           `mapstructure:"environment"` // "sandbox" or "production"
	RateLimit   int              `mapstructure:"rate_limit"`  // Requests per minute
	Features    []string         `mapstructure:"features"`    // Enabled features: "spot", "futures", "options"
	Credentials CredentialConfig `mapstructure:"credentials"` // Enhanced credential management
	Proxy       ProxyConfig      `mapstructure:"proxy"`       // Proxy configuration
}

// CredentialConfig represents credential management configuration
type CredentialConfig struct {
	Source       string            `mapstructure:"source"`        // "env", "file", "vault", "gct"
	FilePath     string            `mapstructure:"file_path"`     // For file-based credentials
	VaultPath    string            `mapstructure:"vault_path"`    // For HashiCorp Vault
	EnvPrefix    string            `mapstructure:"env_prefix"`    // Environment variable prefix
	Encrypted    bool              `mapstructure:"encrypted"`     // Whether credentials are encrypted
	RotationDays int               `mapstructure:"rotation_days"` // Credential rotation period
	Metadata     map[string]string `mapstructure:"metadata"`      // Additional metadata
}

// ProxyConfig represents proxy configuration
type ProxyConfig struct {
	Enabled  bool   `mapstructure:"enabled"`
	URL      string `mapstructure:"url"`
	Username string `mapstructure:"username"`
	Password string `mapstructure:"password"`
}

// EnvironmentConfig represents environment-specific configuration
type EnvironmentConfig struct {
	Name      string            `mapstructure:"name"`      // "development", "staging", "production"
	Debug     bool              `mapstructure:"debug"`     // Enable debug mode
	LogLevel  string            `mapstructure:"log_level"` // "debug", "info", "warn", "error"
	Features  []string          `mapstructure:"features"`  // Enabled features for this environment
	Overrides map[string]string `mapstructure:"overrides"` // Configuration overrides
	Secrets   SecretsConfig     `mapstructure:"secrets"`   // Secrets management
}

// SecretsConfig represents secrets management configuration
type SecretsConfig struct {
	Provider    string        `mapstructure:"provider"`     // "env", "vault", "aws", "gcp", "azure"
	VaultAddr   string        `mapstructure:"vault_addr"`   // Vault server address
	VaultToken  string        `mapstructure:"vault_token"`  // Vault authentication token
	VaultPath   string        `mapstructure:"vault_path"`   // Base path for secrets
	AWSRegion   string        `mapstructure:"aws_region"`   // AWS region for Secrets Manager
	GCPProject  string        `mapstructure:"gcp_project"`  // GCP project for Secret Manager
	AzureVault  string        `mapstructure:"azure_vault"`  // Azure Key Vault name
	RefreshRate time.Duration `mapstructure:"refresh_rate"` // Secret refresh rate
}

// AIConfig holds all AI-related configuration
type AIConfig struct {
	Enabled           bool                    `mapstructure:"enabled"`
	LLM               LLMConfig               `mapstructure:"llm"`
	Sentiment         SentimentConfig         `mapstructure:"sentiment"`
	CostManagement    CostManagementConfig    `mapstructure:"cost_management"`
	AutoConfiguration AutoConfigurationConfig `mapstructure:"auto_configuration"`
}

// LLMConfig for LLM integration
type LLMConfig struct {
	Enabled             bool    `mapstructure:"enabled"`
	Provider            string  `mapstructure:"provider"` // "openai", "anthropic", "google"
	Model               string  `mapstructure:"model"`    // "gpt-4", "claude-3-opus", "gemini-pro"
	APIKey              string  `mapstructure:"api_key"`
	MaxCallsPerDay      int     `mapstructure:"max_calls_per_day"`
	TimeoutSeconds      int     `mapstructure:"timeout_seconds"`
	CacheTTLMinutes     int     `mapstructure:"cache_ttl_minutes"`
	FallbackToTechnical bool    `mapstructure:"fallback_to_technical"`
	Temperature         float64 `mapstructure:"temperature"`
	MaxTokens           int     `mapstructure:"max_tokens"`
	FallbackProvider    string  `mapstructure:"fallback_provider"` // Secondary provider if primary fails
}

// SentimentConfig for sentiment analysis
type SentimentConfig struct {
	Enabled         bool                    `mapstructure:"enabled"`
	CacheTTLMinutes int                     `mapstructure:"cache_ttl_minutes"`
	Sources         map[string]SourceConfig `mapstructure:"sources"`
}

// SourceConfig for sentiment data sources
type SourceConfig struct {
	Enabled      bool    `mapstructure:"enabled"`
	APIKey       string  `mapstructure:"api_key"`
	ClientID     string  `mapstructure:"client_id"`     // For Reddit OAuth
	ClientSecret string  `mapstructure:"client_secret"` // For Reddit OAuth
	MaxItems     int     `mapstructure:"max_items"`
	Weight       float64 `mapstructure:"weight"`
	Endpoint     string  `mapstructure:"endpoint"`
}

// CostManagementConfig for cost tracking and budget control
type CostManagementConfig struct {
	Enabled     bool               `mapstructure:"enabled"`
	DailyLimits map[string]float64 `mapstructure:"daily_limits"` // Provider -> daily limit in USD
	Alerts      CostAlertConfig    `mapstructure:"alerts"`
}

// CostAlertConfig for cost alerts
type CostAlertConfig struct {
	ThresholdPercent int  `mapstructure:"threshold_percent"` // Alert at X% of budget
	NotifyOnLimit    bool `mapstructure:"notify_on_limit"`
}

// AutoConfigurationConfig for auto-configuration
type AutoConfigurationConfig struct {
	Enabled                  bool                 `mapstructure:"enabled"`
	ReconfigureIntervalHours int                  `mapstructure:"reconfigure_interval_hours"`
	Strategies               []string             `mapstructure:"strategies"`
	RiskAdjustment           RiskAdjustmentConfig `mapstructure:"risk_adjustment"`
}

// RiskAdjustmentConfig for risk adjustment
type RiskAdjustmentConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	BasedOn string `mapstructure:"based_on"` // "volatility", "drawdown", "win_rate"
}
