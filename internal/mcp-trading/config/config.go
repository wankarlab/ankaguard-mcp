package config

import (
	"fmt"
	"net"
	"os"
	"time"

	"github.com/spf13/viper"

	"github.com/rantipay/trading-mcp/internal/edition"
)

// Config representa la configuración completa del servidor MCP
type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	GCT         GCTConfig         `mapstructure:"gocryptotrader"`
	RiskLimits  RiskLimitsConfig  `mapstructure:"risk_limits"`
	Exchanges   []ExchangeConfig  `mapstructure:"exchanges"`
	Logging     LoggingConfig     `mapstructure:"logging"`
	Monitoring  MonitoringConfig  `mapstructure:"monitoring"`
	Audit       AuditConfig       `mapstructure:"audit"`
	Bot         BotConfig         `mapstructure:"bot"`
	MultiPair   MultiPairConfig   `mapstructure:"multi_pair"`
	MasterAgent MasterAgentConfig `mapstructure:"master_agent"`
	AIBot       AIBotConfig       `mapstructure:"ai_bot"`
	MultiUser   MultiUserConfig   `mapstructure:"multi_user"`
	Auth        AuthConfig        `mapstructure:"auth"`
	Trading     TradingConfig     `mapstructure:"trading"`
}

// Authentication modes.
const (
	// AuthModeExternal validates tokens minted by the RantiPay auth service
	// with KONG_JWT_SECRET (the current deployment). It is the default.
	AuthModeExternal = "external"
	// AuthModeStandalone validates tokens minted by the local authority
	// (operator login and agent tokens, internal/localauth).
	AuthModeStandalone = "standalone"
)

// AuthConfig selects how tokens are validated.
type AuthConfig struct {
	// Mode is "external" (default) or "standalone".
	Mode string `mapstructure:"mode"`
	// DataDir holds the standalone authority files (default "data/auth").
	DataDir string `mapstructure:"data_dir"`
}

// TradingConfig holds the static fallback of the trading.* dynamic
// parameters, used when the dynamic config store is unavailable.
type TradingConfig struct {
	// LiveEnabled is the YAML fallback of trading.live_enabled (default true).
	LiveEnabled *bool `mapstructure:"live_enabled"`
}

// AuthSettings is the subset of the configuration the "operator" subcommand
// needs. It is read without validating or connecting to anything else.
type AuthSettings struct {
	Mode    string
	DataDir string
}

// LoadAuthSettings reads auth.mode and auth.data_dir (env AUTH_MODE and
// AUTH_DATA_DIR override the file). A missing file yields the defaults.
func LoadAuthSettings(configPath string) AuthSettings {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")
	_ = v.BindEnv("auth.mode", "AUTH_MODE")
	_ = v.BindEnv("auth.data_dir", "AUTH_DATA_DIR")
	_ = v.ReadInConfig()
	st := AuthSettings{Mode: v.GetString("auth.mode"), DataDir: v.GetString("auth.data_dir")}
	if st.Mode == "" {
		st.Mode = AuthModeExternal
	}
	if st.DataDir == "" {
		st.DataDir = "data/auth"
	}
	return st
}

// MultiUserConfig gates the per-user bot manager subsystem (MU-8.6).
// When Enabled is false (the default) the legacy single-bot path is used
// unchanged and BotManager/SessionManager/UserBotConfigRepo are not wired
// into the REST router. Set multi_user.enabled: true to activate.
type MultiUserConfig struct {
	Enabled bool `mapstructure:"enabled"`
}

// BotBreakevenConfig configures the breakeven stop automation
type BotBreakevenConfig struct {
	Enabled    bool    `mapstructure:"enabled"`
	TriggerPct float64 `mapstructure:"trigger_pct"` // move SL after this % profit
	BufferPct  float64 `mapstructure:"buffer_pct"`  // tiny buffer above entry
}

// BotTrailingConfig configures the trailing stop
type BotTrailingConfig struct {
	Enabled     bool    `mapstructure:"enabled"`
	DistancePct float64 `mapstructure:"distance_pct"` // trail distance in %
}

// BotSizingTier defines per-tier sizing and SL/TP overrides based on signal confidence.
// Tiers are matched by [MinConfidence, MaxConfidence] inclusive.
type BotSizingTier struct {
	Name          string  `mapstructure:"name"`
	MinConfidence float64 `mapstructure:"min_confidence"`  // 0.0 - 1.0
	MaxConfidence float64 `mapstructure:"max_confidence"`  // 0.0 - 1.0
	SizeUSD       float64 `mapstructure:"size_usd"`        // trade size for this tier
	TakeProfitPct float64 `mapstructure:"take_profit_pct"` // TP % override (e.g. 3.0)
	StopLossPct   float64 `mapstructure:"stop_loss_pct"`   // SL % override (e.g. 1.5)
}

// BotDynamicSizingConfig controls confidence-based dynamic sizing/TP/SL.
// When disabled or tiers list is empty the bot uses the flat trade_size_usd /
// default_take_profit_pct / default_stop_loss_pct values.
type BotDynamicSizingConfig struct {
	Enabled bool            `mapstructure:"enabled"`
	Tiers   []BotSizingTier `mapstructure:"tiers"`
}

// BotPartialTPLevel describes one rung of a scale-out ladder. When the
// position's profit reaches TPPct (measured from entry × leverage), the bot
// closes ClosePct% of the *current* (post-prior-closes) size via a reduce-only
// market order and optionally pulls the stop-loss to breakeven.
type BotPartialTPLevel struct {
	TPPct             float64 `mapstructure:"tp_pct"`
	ClosePct          float64 `mapstructure:"close_pct"`
	MoveSLToBreakeven bool    `mapstructure:"move_sl_to_breakeven"`
}

// BotPartialTPConfig enables partial take-profit scale-outs. Disabled by
// default; when enabled the original TakeProfit on the position still acts as
// the safety net for the last fraction.
type BotPartialTPConfig struct {
	Enabled bool                `mapstructure:"enabled"`
	Levels  []BotPartialTPLevel `mapstructure:"levels"`
}

// BotPairSelectionConfig enables routing the main bot to the highest-scored
// pair surfaced by the master agent on each analysis cycle. Switching only
// happens when there is no open position to avoid abandoning an active trade.
type BotPairSelectionConfig struct {
	Enabled               bool    `mapstructure:"enabled"`
	MinScore              float64 `mapstructure:"min_score"`               // 0–1, only switch if top score ≥ this
	SwitchCooldownSeconds int     `mapstructure:"switch_cooldown_seconds"` // throttle to avoid flapping
	FallbackPair          string  `mapstructure:"fallback_pair"`           // used when master gives nothing
}

// BotConfig contiene la configuración del bot de trading autónomo
type BotConfig struct {
	Enabled                 bool                   `mapstructure:"enabled"`
	DryRun                  bool                   `mapstructure:"dry_run"`
	AutoExecute             bool                   `mapstructure:"auto_execute"`
	AutonomousMode          bool                   `mapstructure:"autonomous_mode"`
	Pair                    string                 `mapstructure:"pair"`
	Exchange                string                 `mapstructure:"exchange"`
	TradeSizeUSD            float64                `mapstructure:"trade_size_usd"`
	Leverage                int                    `mapstructure:"leverage"`
	MarketType              string                 `mapstructure:"market_type"`    // spot | futures | margin
	ModeSelection           string                 `mapstructure:"mode_selection"` // auto | manual | hybrid
	ConfidenceThreshold     float64                `mapstructure:"confidence_threshold"`
	MaxDailyLossUSD         float64                `mapstructure:"max_daily_loss_usd"`
	MaxOpenPositions        int                    `mapstructure:"max_open_positions"`
	DefaultStopLossPct      float64                `mapstructure:"default_stop_loss_pct"`
	DefaultTakeProfitPct    float64                `mapstructure:"default_take_profit_pct"`
	AnalysisIntervalSeconds int                    `mapstructure:"analysis_interval_seconds"`
	MonitorIntervalSeconds  int                    `mapstructure:"monitor_interval_seconds"`
	Breakeven               BotBreakevenConfig     `mapstructure:"breakeven"`
	Trailing                BotTrailingConfig      `mapstructure:"trailing"`
	DynamicSizing           BotDynamicSizingConfig `mapstructure:"dynamic_sizing"`
	PartialTP               BotPartialTPConfig     `mapstructure:"partial_tp"`
	PairSelection           BotPairSelectionConfig `mapstructure:"pair_selection"`
}

// TLSConfig holds TLS configuration for the server
type TLSConfig struct {
	Enabled  bool   `mapstructure:"enabled"`   // Set true in production with valid certs
	CertFile string `mapstructure:"cert_file"` // Path to TLS certificate
	KeyFile  string `mapstructure:"key_file"`  // Path to TLS private key
}

// ServerConfig contiene la configuración del servidor MCP
type ServerConfig struct {
	Host string `mapstructure:"host"`
	// BindAddress, when set, is the interface every listener (:10600, :9090
	// and :8081) binds to. Empty keeps the current behaviour (Host for :10600,
	// all interfaces for the others).
	BindAddress    string    `mapstructure:"bind_address"`
	Port           int       `mapstructure:"port"`
	Mode           string    `mapstructure:"mode"`             // "production" or "sandbox"
	Protocol       string    `mapstructure:"protocol"`         // "stdio" or "http"
	ReadTimeout    int       `mapstructure:"read_timeout"`     // HTTP read timeout in seconds (default: 30)
	WriteTimeout   int       `mapstructure:"write_timeout"`    // HTTP write timeout in seconds (default: 120)
	IdleTimeout    int       `mapstructure:"idle_timeout"`     // HTTP idle timeout in seconds (default: 120)
	MaxHeaderBytes int       `mapstructure:"max_header_bytes"` // Max header size in bytes (default: 1048576)
	TLS            TLSConfig `mapstructure:"tls"`              // TLS configuration
}

// GCTConfig contiene la configuración de GoCryptoTrader
type GCTConfig struct {
	GRPCAddress  string        `mapstructure:"grpc_address"`
	Username     string        `mapstructure:"username"`
	Password     string        `mapstructure:"password"`
	Timeout      time.Duration `mapstructure:"timeout"`
	MaxRetries   int           `mapstructure:"max_retries"`
	RetryBackoff time.Duration `mapstructure:"retry_backoff"`
}

// RiskLimitsConfig contiene los límites de riesgo
type RiskLimitsConfig struct {
	MaxOrderSizeUSD       float64 `mapstructure:"max_order_size_usd"`
	MaxDailyLossUSD       float64 `mapstructure:"max_daily_loss_usd"`
	MaxPositionSizeUSD    float64 `mapstructure:"max_position_size_usd"`
	MaxOpenOrders         int     `mapstructure:"max_open_orders"`
	MaxExposurePerPairUSD float64 `mapstructure:"max_exposure_per_pair_usd"`
}

// ExchangeConfig contiene la configuración de un exchange
type ExchangeConfig struct {
	Name       string `mapstructure:"name"`
	Enabled    bool   `mapstructure:"enabled"`
	Sandbox    bool   `mapstructure:"sandbox"`
	APIKey     string `mapstructure:"api_key"`
	APISecret  string `mapstructure:"api_secret"`
	Passphrase string `mapstructure:"passphrase"` // Para KuCoin
}

// LoggingConfig contiene la configuración de logging
type LoggingConfig struct {
	Level      string `mapstructure:"level"`
	Format     string `mapstructure:"format"`
	Output     string `mapstructure:"output"`
	File       string `mapstructure:"file"`
	MaxSizeMB  int    `mapstructure:"max_size_mb"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAgeDays int    `mapstructure:"max_age_days"`
}

// MonitoringConfig contiene la configuración de monitoreo
type MonitoringConfig struct {
	PrometheusEnabled   bool          `mapstructure:"prometheus_enabled"`
	PrometheusPort      int           `mapstructure:"prometheus_port"`
	HealthCheckInterval time.Duration `mapstructure:"health_check_interval"`
	MetricsEnabled      bool          `mapstructure:"metrics_enabled"`
}

// AuditConfig contiene la configuración de auditoría
type AuditConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	LogFile        string `mapstructure:"log_file"`
	LogAllRequests bool   `mapstructure:"log_all_requests"`
	LogAllOrders   bool   `mapstructure:"log_all_orders"`
}

// Load carga la configuración desde archivo y variables de entorno
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// Configurar viper
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")
	v.AutomaticEnv()

	// Mapear variables de entorno
	v.BindEnv("server.host", "MCP_SERVER_HOST")
	v.BindEnv("server.port", "MCP_SERVER_PORT")
	v.BindEnv("server.mode", "MCP_SERVER_MODE")
	v.BindEnv("server.bind_address", "MCP_BIND_ADDRESS")
	v.BindEnv("auth.mode", "AUTH_MODE")
	v.BindEnv("auth.data_dir", "AUTH_DATA_DIR")
	v.BindEnv("gocryptotrader.grpc_address", "GCT_GRPC_ADDRESS")
	v.BindEnv("gocryptotrader.username", "GCT_USERNAME")
	v.BindEnv("gocryptotrader.password", "GCT_PASSWORD")
	v.BindEnv("logging.level", "LOG_LEVEL")
	v.BindEnv("logging.format", "LOG_FORMAT")

	// Leer archivo de configuración
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	// Unmarshal a struct
	var config Config
	if err := v.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	if config.Auth.Mode == "" {
		config.Auth.Mode = AuthModeExternal
	}
	if config.Auth.DataDir == "" {
		config.Auth.DataDir = "data/auth"
	}

	// Validar configuración
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// Validate valida la configuración
func (c *Config) Validate() error {
	// Validar servidor
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Server.Mode != "production" && c.Server.Mode != "sandbox" {
		return fmt.Errorf("invalid server mode: %s (must be 'production' or 'sandbox')", c.Server.Mode)
	}
	if c.Server.Protocol != "stdio" && c.Server.Protocol != "http" {
		return fmt.Errorf("invalid server protocol: %s (must be 'stdio' or 'http')", c.Server.Protocol)
	}

	if c.Auth.Mode != "" && c.Auth.Mode != AuthModeExternal && c.Auth.Mode != AuthModeStandalone {
		return fmt.Errorf("invalid auth.mode: %q (must be 'external' or 'standalone')", c.Auth.Mode)
	}

	// Validar GoCryptoTrader
	if c.GCT.GRPCAddress == "" {
		return fmt.Errorf("gocryptotrader.grpc_address is required")
	}
	// The product build starts without GoCryptoTrader credentials (public
	// market data and the paper broker) and reports the missing connection.
	if !edition.IsProduct() {
		if c.GCT.Username == "" {
			return fmt.Errorf("gocryptotrader.username is required")
		}
		if c.GCT.Password == "" {
			return fmt.Errorf("gocryptotrader.password is required")
		}
	}

	// Validar risk limits
	if c.RiskLimits.MaxOrderSizeUSD <= 0 {
		return fmt.Errorf("risk_limits.max_order_size_usd must be positive")
	}
	if c.RiskLimits.MaxDailyLossUSD <= 0 {
		return fmt.Errorf("risk_limits.max_daily_loss_usd must be positive")
	}

	// Validar logging
	validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
	if !validLevels[c.Logging.Level] {
		return fmt.Errorf("invalid logging level: %s", c.Logging.Level)
	}

	return nil
}

// IsSandboxMode retorna true si el servidor está en modo sandbox
func (c *Config) IsSandboxMode() bool {
	return c.Server.Mode == "sandbox"
}

// GetEnabledExchanges retorna la lista de exchanges habilitados
func (c *Config) GetEnabledExchanges() []ExchangeConfig {
	var enabled []ExchangeConfig
	for _, ex := range c.Exchanges {
		if ex.Enabled {
			enabled = append(enabled, ex)
		}
	}
	return enabled
}

// LoadFromEnv carga la configuración solo desde variables de entorno
func LoadFromEnv() (*Config, error) {
	config := &Config{
		Server: ServerConfig{
			Host:     getEnv("MCP_SERVER_HOST", "0.0.0.0"),
			Port:     getEnvInt("MCP_SERVER_PORT", 10600),
			Mode:     getEnv("MCP_SERVER_MODE", "sandbox"),
			Protocol: getEnv("MCP_SERVER_PROTOCOL", "stdio"),
		},
		GCT: GCTConfig{
			GRPCAddress:  getEnv("GCT_GRPC_ADDRESS", "localhost:9052"),
			Username:     getEnv("GCT_USERNAME", "admin"),
			Password:     getEnv("GCT_PASSWORD", "Password"),
			Timeout:      30 * time.Second,
			MaxRetries:   3,
			RetryBackoff: 2 * time.Second,
		},
		RiskLimits: RiskLimitsConfig{
			MaxOrderSizeUSD:       getEnvFloat("RISK_MAX_ORDER_SIZE_USD", 1000),
			MaxDailyLossUSD:       getEnvFloat("RISK_MAX_DAILY_LOSS_USD", 500),
			MaxPositionSizeUSD:    getEnvFloat("RISK_MAX_POSITION_SIZE_USD", 5000),
			MaxOpenOrders:         getEnvInt("RISK_MAX_OPEN_ORDERS", 10),
			MaxExposurePerPairUSD: getEnvFloat("RISK_MAX_EXPOSURE_PER_PAIR_USD", 2000),
		},
		Logging: LoggingConfig{
			Level:  getEnv("LOG_LEVEL", "info"),
			Format: getEnv("LOG_FORMAT", "json"),
			Output: getEnv("LOG_OUTPUT", "stdout"),
			File:   getEnv("LOG_FILE", "logs/trading-mcp.log"),
		},
		Monitoring: MonitoringConfig{
			PrometheusEnabled:   getEnvBool("PROMETHEUS_ENABLED", true),
			PrometheusPort:      getEnvInt("PROMETHEUS_PORT", 9090),
			HealthCheckInterval: 30 * time.Second,
			MetricsEnabled:      getEnvBool("METRICS_ENABLED", true),
		},
		Audit: AuditConfig{
			Enabled:        getEnvBool("AUDIT_ENABLED", true),
			LogFile:        getEnv("AUDIT_LOG_FILE", "logs/audit.log"),
			LogAllRequests: true,
			LogAllOrders:   true,
		},
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// Helper functions
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		var intValue int
		fmt.Sscanf(value, "%d", &intValue)
		return intValue
	}
	return defaultValue
}

func getEnvFloat(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		var floatValue float64
		fmt.Sscanf(value, "%f", &floatValue)
		return floatValue
	}
	return defaultValue
}

func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		return value == "true" || value == "1"
	}
	return defaultValue
}

// MultiPairConfig configures the multi-pair swarm mode
type MultiPairConfig struct {
	Enabled           bool                    `mapstructure:"enabled"`
	MaxConcurrentBots int                     `mapstructure:"max_concurrent_bots"`
	TotalCapitalUSD   float64                 `mapstructure:"total_capital_usd"`
	CapitalAllocation CapitalAllocationConfig `mapstructure:"capital_allocation"`
	PairUniverse      PairUniverseConfig      `mapstructure:"pair_universe"`
	BotLifecycle      BotLifecycleConfig      `mapstructure:"bot_lifecycle"`
}

// CapitalAllocationConfig configures how capital is distributed across bots
type CapitalAllocationConfig struct {
	Mode                  string        `mapstructure:"mode"`
	RebalanceInterval     time.Duration `mapstructure:"rebalance_interval"`
	MinPerBotUSD          float64       `mapstructure:"min_per_bot_usd"`
	MaxPerBotUSD          float64       `mapstructure:"max_per_bot_usd"`
	ReservePercent        float64       `mapstructure:"reserve_percent"`
	MaxCorrelatedExposure float64       `mapstructure:"max_correlated_exposure"`
	MinPairsForFullDeploy int           `mapstructure:"min_pairs_for_full_deploy"`
}

// PairUniverseConfig configures the source of tradable pairs (revived: dynamic discovery)
type PairUniverseConfig struct {
	Source           string                 `mapstructure:"source"`         // dynamic | manual | hybrid
	AssetType        string                 `mapstructure:"asset_type"`     // futures | spot
	QuoteCurrency    string                 `mapstructure:"quote_currency"` // e.g. USDT
	TopNByVolume     int                    `mapstructure:"top_n_by_volume"`
	CacheTTL         time.Duration          `mapstructure:"cache_ttl"`
	StaticPairs      []string               `mapstructure:"static_pairs"` // only if source=manual
	Exchange         string                 `mapstructure:"exchange"`
	DiscoveryFilters DiscoveryFiltersConfig `mapstructure:"discovery_filters"`
}

// DiscoveryFiltersConfig holds AUTO-mode safety filters
type DiscoveryFiltersConfig struct {
	MinVolume24hUSD     float64  `mapstructure:"min_volume_24h_usd"`
	MinVolatilityATRPct float64  `mapstructure:"min_volatility_atr_pct"`
	MaxVolatilityATRPct float64  `mapstructure:"max_volatility_atr_pct"`
	MaxBTCCorrelation   float64  `mapstructure:"max_btc_correlation"`
	ExcludeStablecoins  bool     `mapstructure:"exclude_stablecoins"`
	Exclude             []string `mapstructure:"exclude"`
}

// BotLifecycleConfig configures spawn/kill thresholds for per-pair bots
type BotLifecycleConfig struct {
	SpawnThreshold    float64       `mapstructure:"spawn_threshold"`
	KillThreshold     float64       `mapstructure:"kill_threshold"`
	CooldownAfterLoss time.Duration `mapstructure:"cooldown_after_loss"`
}

// AIBotConfig configures the singleton AI trading bot.
type AIBotConfig struct {
	// AutoStart launches the bot at server startup. Defaults to false when the
	// key is absent so a missing or misread config never starts real-money trading.
	AutoStart bool `mapstructure:"auto_start"`
}

// MasterAgentConfig configures the autonomous master agent
type MasterAgentConfig struct {
	Enabled        bool                 `mapstructure:"enabled"`
	AutoStart      bool                 `mapstructure:"auto_start"`
	LoopInterval   time.Duration        `mapstructure:"loop_interval"`
	ScanTopN       int                  `mapstructure:"scan_top_n"`
	Exchange       string               `mapstructure:"exchange"`
	ScoringWeights ScoringWeightsConfig `mapstructure:"scoring_weights"`
	Feedback       FeedbackConfig       `mapstructure:"feedback"`
	PaperTrading   PaperTradingConfig   `mapstructure:"paper_trading"`
	LLMConsensus   LLMConsensusConfig   `mapstructure:"llm_consensus"`
}

// ScoringWeightsConfig configures the master agent scoring formula
type ScoringWeightsConfig struct {
	Volatility            float64 `mapstructure:"volatility"`
	Volume                float64 `mapstructure:"volume"`
	TrendStrength         float64 `mapstructure:"trend_strength"`
	Sentiment             float64 `mapstructure:"sentiment"`
	RegimeMatch           float64 `mapstructure:"regime_match"`
	BTCCorrelationPenalty float64 `mapstructure:"btc_correlation_penalty"`
	RecentLossPenalty     float64 `mapstructure:"recent_loss_penalty"`
	ProfitFactor          float64 `mapstructure:"profit_factor"`
}

// FeedbackConfig configures the master agent self-tuning loop
type FeedbackConfig struct {
	Enabled              bool          `mapstructure:"enabled"`
	FeedbackInterval     time.Duration `mapstructure:"feedback_interval"`
	MinTradesForFeedback int           `mapstructure:"min_trades_for_feedback"`
}

// PaperTradingConfig configures paper-trading validation before going live
type PaperTradingConfig struct {
	Enabled  bool          `mapstructure:"enabled"`
	Duration time.Duration `mapstructure:"duration"`
}

// LLMConsensusConfig configures multi-provider LLM consensus voting
type LLMConsensusConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	MinProviders int           `mapstructure:"min_providers"`
	Timeout      time.Duration `mapstructure:"timeout"`
}

// ListenHost returns the interface the MCP listener (:10600) binds to:
// server.bind_address when set, otherwise server.host (current behaviour).
func (c *Config) ListenHost() string {
	if c.Server.BindAddress != "" {
		return c.Server.BindAddress
	}
	return c.Server.Host
}

// IsLocalBind reports whether host only accepts connections from this
// machine. An empty host or an unspecified address (0.0.0.0, ::) binds every
// interface and is therefore not local.
func IsLocalBind(host string) bool {
	switch host {
	case "localhost", "127.0.0.1", "::1", "[::1]":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
