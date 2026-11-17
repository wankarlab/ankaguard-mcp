package config

import (
	"time"
)

// DynamicTradingConfig holds all dynamic trading parameters
// ALL parameters are configurable via API - NO hardcoded values
type DynamicTradingConfig struct {
	// Identity
	ProfileName string `json:"profile_name,omitempty"`
	Description string `json:"description,omitempty"`

	// Trading Parameters (DYNAMIC)
	Pair                string   `json:"pair" validate:"required"`
	Exchange            string   `json:"exchange" validate:"required"`
	Timeframes          []string `json:"timeframes" validate:"required,min=1,max=5"`
	ConfidenceThreshold float64  `json:"confidence_threshold" validate:"min=0.5,max=1.0"`
	TradeSize           float64  `json:"trade_size_usd" validate:"gt=0"`
	Leverage            int      `json:"leverage" validate:"gte=1,lte=100"`

	// Timing (DYNAMIC - all in seconds/minutes)
	AnalysisIntervalSeconds int `json:"analysis_interval_seconds" validate:"gte=1,lte=300"`
	MonitorIntervalSeconds  int `json:"monitor_interval_seconds" validate:"gte=1,lte=60"`

	// Risk Management (DYNAMIC)
	RiskPerTradePct      float64 `json:"risk_per_trade_pct" validate:"gte=0.1,lte=5.0"`
	MaxDailyLoss         float64 `json:"max_daily_loss_usd" validate:"gt=0"`
	MaxDailyTrades       int     `json:"max_daily_trades" validate:"gte=1,lte=1000"`
	ConsecutiveLossLimit int     `json:"consecutive_loss_limit" validate:"gte=1,lte=10"`
	LossReductionPct     float64 `json:"loss_reduction_pct" validate:"gte=0,lte=100"`
	PauseAfterLosses     int     `json:"pause_after_losses" validate:"gte=1,lte=20"`
	PauseDurationMinutes int     `json:"pause_duration_minutes" validate:"gte=1,lte=1440"`
	MinWinRatePct        float64 `json:"min_win_rate_pct" validate:"gte=0,lte=100"`
	MaxOpenPositions     int     `json:"max_open_positions" validate:"gte=1,lte=10"`

	// Take Profit / Stop Loss (DYNAMIC)
	TakeProfitPct   float64          `json:"take_profit_pct" validate:"gte=0.1,lte=10.0"`
	StopLossPct     float64          `json:"stop_loss_pct" validate:"gte=0.1,lte=10.0"`
	TPLevels        []TPLevel        `json:"tp_levels,omitempty"`
	BreakevenConfig *BreakevenConfig `json:"breakeven_config,omitempty"`
	TrailingConfig  *TrailingConfig  `json:"trailing_config,omitempty"`

	// Post-Close Behavior (DYNAMIC)
	PostCloseReanalysis    bool `json:"post_close_reanalysis"`
	ReanalysisDelaySeconds int  `json:"reanalysis_delay_seconds" validate:"gte=0,lte=30"`
	MinConfluenceFactors   int  `json:"min_confluence_factors" validate:"gte=1,lte=10"`
	QuickReentryEnabled    bool `json:"quick_reentry_enabled"`
	MaxReentrySeconds      int  `json:"max_reentry_seconds" validate:"gte=1,lte=300"`
	ReanalysisIntervalSec  int  `json:"reanalysis_interval_seconds" validate:"gte=1,lte=300"`

	// Direction Switching (DYNAMIC)
	DirectionSwitchingEnabled bool `json:"direction_switching_enabled"`
	MinSwitchIntervalMinutes  int  `json:"min_switch_interval_minutes" validate:"gte=1,lte=1440"`
	AutoCloseOpposite         bool `json:"auto_close_opposite"`
	SwitchCooldownMinutes     int  `json:"switch_cooldown_minutes" validate:"gte=0,lte=1440"`

	// Tool Configuration (DYNAMIC)
	EnabledTools       []string `json:"enabled_tools,omitempty"`
	MinToolsRequired   int      `json:"min_tools_required" validate:"gte=0,lte=100"`
	ToolTimeoutSeconds int      `json:"tool_timeout_seconds" validate:"gte=1,lte=300"`
	ToolRetryEnabled   bool     `json:"tool_retry_enabled"`
	MaxToolRetries     int      `json:"max_tool_retries" validate:"gte=0,lte=10"`

	// Execution (DYNAMIC)
	OrderType            string  `json:"order_type" validate:"oneof=market limit"`
	ExecutionTimeoutSec  int     `json:"execution_timeout_seconds" validate:"gte=1,lte=300"`
	RetryOnFailure       bool    `json:"retry_on_failure"`
	MaxExecutionRetries  int     `json:"max_execution_retries" validate:"gte=0,lte=10"`
	MaxLatencyMs         int     `json:"max_latency_ms" validate:"gte=0"`
	SlippageTolerancePct float64 `json:"slippage_tolerance_pct" validate:"gte=0,lte=5.0"`

	// Market Filters (DYNAMIC)
	MaxSpreadPct            float64            `json:"max_spread_pct" validate:"gte=0,lte=10.0"`
	MinVolumeMultiplier     float64            `json:"min_volume_multiplier" validate:"gte=0"`
	MaxVolatilityMultiplier float64            `json:"max_volatility_multiplier" validate:"gte=0"`
	PauseOnNews             bool               `json:"pause_on_news"`
	NewsPauseMinutes        int                `json:"news_pause_minutes" validate:"gte=0,lte=1440"`
	FundingRateLimits       *FundingRateLimits `json:"funding_rate_limits,omitempty"`

	// Monitoring & Alerts (DYNAMIC)
	LogLevel                 string               `json:"log_level" validate:"oneof=debug info warn error"`
	DetailedLogging          bool                 `json:"detailed_logging"`
	ReportIntervalMinutes    int                  `json:"report_interval_minutes" validate:"gte=1,lte=1440"`
	WebhookURL               string               `json:"webhook_url,omitempty"`
	SaveTradeHistory         bool                 `json:"save_trade_history"`
	MetricsUpdateIntervalSec int                  `json:"metrics_update_interval_seconds" validate:"gte=1,lte=300"`
	AlertThresholds          *AlertThresholds     `json:"alert_thresholds,omitempty"`
	NotificationChannels     []string             `json:"notification_channels,omitempty"`
	EmergencyStopConditions  *EmergencyConditions `json:"emergency_stop_conditions,omitempty"`

	// Trading mode selection (Phase 1)
	MarketType    string `json:"market_type,omitempty" yaml:"market_type,omitempty"`       // spot | futures | margin
	ModeSelection string `json:"mode_selection,omitempty" yaml:"mode_selection,omitempty"` // auto | manual | hybrid

	// Mode
	AutoExecute    bool `json:"auto_execute"`
	DryRun         bool `json:"dry_run"`
	AutonomousMode bool `json:"autonomous_mode"`
	HotReload      bool `json:"hot_reload"`

	// Metadata
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Version   int       `json:"version"`
}

// TPLevel represents a take profit level with size percentage
type TPLevel struct {
	PricePct float64 `json:"price_pct" yaml:"price_pct" validate:"gte=0.1,lte=100"` // e.g., 0.5 for 0.5%
	SizePct  float64 `json:"size_pct"  yaml:"size_pct"  validate:"gte=1,lte=100"`   // e.g., 50 for 50%
}

// BreakevenConfig holds breakeven configuration
type BreakevenConfig struct {
	Enabled    bool    `json:"enabled"     yaml:"enabled"`
	TriggerPct float64 `json:"trigger_pct" yaml:"trigger_pct" validate:"gte=0,lte=10"` // e.g., 0.5
	BufferPct  float64 `json:"buffer_pct"  yaml:"buffer_pct"  validate:"gte=0,lte=1.0"` // e.g., 0.05
}

// TrailingConfig holds trailing stop configuration
type TrailingConfig struct {
	Enabled     bool    `json:"enabled"      yaml:"enabled"`
	DistancePct float64 `json:"distance_pct" yaml:"distance_pct" validate:"gte=0.1,lte=10"` // e.g., 0.3
}

// FundingRateLimits holds funding rate limits
type FundingRateLimits struct {
	MaxPositive float64 `json:"max_positive" yaml:"max_positive" validate:"gte=-1,lte=1"` // e.g., 0.1
	MaxNegative float64 `json:"max_negative" yaml:"max_negative" validate:"gte=-1,lte=1"` // e.g., -0.1
}

// AlertThresholds holds alert threshold configuration
type AlertThresholds struct {
	MaxDailyLossPct float64 `json:"max_daily_loss_pct" validate:"gte=0,lte=100"`
	MinWinRatePct   float64 `json:"min_win_rate_pct" validate:"gte=0,lte=100"`
	MaxDrawdownPct  float64 `json:"max_drawdown_pct" validate:"gte=0,lte=100"`
}

// EmergencyConditions holds emergency stop conditions
type EmergencyConditions struct {
	MaxDailyLoss      float64 `json:"max_daily_loss" validate:"gte=0"`
	MaxDrawdownPct    float64 `json:"max_drawdown_pct" validate:"gte=0,lte=100"`
	ConsecutiveLosses int     `json:"consecutive_losses" validate:"gte=1,lte=20"`
}

// GetDefault returns default configuration for moderate scalping
func GetDefault() *DynamicTradingConfig {
	return &DynamicTradingConfig{
		ProfileName:         "default",
		Description:         "Default moderate scalping configuration",
		Pair:                "DOGE-USDT",
		Exchange:            "kucoin",
		Timeframes:          []string{"2m", "3m", "5m"},
		ConfidenceThreshold: 0.65,
		TradeSize:           2.0,
		Leverage:            5,

		AnalysisIntervalSeconds: 10,
		MonitorIntervalSeconds:  5,

		RiskPerTradePct:      0.5,
		MaxDailyLoss:         20.0,
		MaxDailyTrades:       50,
		ConsecutiveLossLimit: 3,
		LossReductionPct:     50.0,
		PauseAfterLosses:     5,
		PauseDurationMinutes: 30,
		MinWinRatePct:        40.0,
		MaxOpenPositions:     1,

		TakeProfitPct: 1.0,
		StopLossPct:   1.5,
		TPLevels: []TPLevel{
			{PricePct: 0.5, SizePct: 50},
			{PricePct: 1.0, SizePct: 50},
		},
		BreakevenConfig: &BreakevenConfig{
			Enabled:    true,
			TriggerPct: 0.5,
			BufferPct:  0.05,
		},
		TrailingConfig: &TrailingConfig{
			Enabled:     false,
			DistancePct: 0.3,
		},

		PostCloseReanalysis:    true,
		ReanalysisDelaySeconds: 2,
		MinConfluenceFactors:   3,
		QuickReentryEnabled:    true,
		MaxReentrySeconds:      10,
		ReanalysisIntervalSec:  30,

		DirectionSwitchingEnabled: true,
		MinSwitchIntervalMinutes:  15,
		AutoCloseOpposite:         true,
		SwitchCooldownMinutes:     10,

		EnabledTools:       []string{}, // Empty means all tools
		MinToolsRequired:   3,
		ToolTimeoutSeconds: 30,
		ToolRetryEnabled:   true,
		MaxToolRetries:     2,

		OrderType:            "market",
		ExecutionTimeoutSec:  10,
		RetryOnFailure:       true,
		MaxExecutionRetries:  3,
		MaxLatencyMs:         1000,
		SlippageTolerancePct: 0.1,

		MaxSpreadPct:            0.1,
		MinVolumeMultiplier:     0.5,
		MaxVolatilityMultiplier: 2.0,
		PauseOnNews:             true,
		NewsPauseMinutes:        30,
		FundingRateLimits: &FundingRateLimits{
			MaxPositive: 0.1,
			MaxNegative: -0.1,
		},

		LogLevel:                 "info",
		DetailedLogging:          false,
		ReportIntervalMinutes:    60,
		WebhookURL:               "",
		SaveTradeHistory:         true,
		MetricsUpdateIntervalSec: 30,
		AlertThresholds: &AlertThresholds{
			MaxDailyLossPct: 5.0,
			MinWinRatePct:   35.0,
			MaxDrawdownPct:  15.0,
		},
		NotificationChannels: []string{"webhook"},
		EmergencyStopConditions: &EmergencyConditions{
			MaxDailyLoss:      50.0,
			MaxDrawdownPct:    25.0,
			ConsecutiveLosses: 10,
		},

		AutoExecute:    false,
		DryRun:         true,
		AutonomousMode: true,
		HotReload:      true,

		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Version:   1,
	}
}
