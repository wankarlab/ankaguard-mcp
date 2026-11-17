package ai

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Strategy represents a complete trading strategy configuration
// This is the main configuration structure for dynamic multi-style trading
type Strategy struct {
	Name               string                   `yaml:"name"`
	Type               StyleType                `yaml:"type"` // NEW: scalping, day_trading, swing_trading, position_trading
	Timeframes         []TimeframeConfig        `yaml:"timeframes"`
	RequiredTimeframes []string                 `yaml:"required_timeframes"` // NEW: Timeframes that must be available
	CandleLimits       map[string]int           `yaml:"candle_limits"`       // NEW: Number of candles per timeframe
	Indicators         IndicatorConfig          `yaml:"indicators"`
	Filters            FilterConfig             `yaml:"filters"` // NEW: Filter configuration
	Alignment          AlignmentConfig          `yaml:"alignment"`
	Confidence         float64                  `yaml:"confidence_threshold"`
	Risk               RiskConfig               `yaml:"risk"`
	Execution          ExecutionConfig          `yaml:"execution"`
	PositionManagement PositionManagementConfig `yaml:"position_management"`
}

// StyleType represents the type of trading style
type StyleType string

const (
	StyleScalping StyleType = "scalping"
	StyleDay      StyleType = "day_trading"
	StyleSwing    StyleType = "swing_trading"
	StylePosition StyleType = "position_trading"
)

// FilterConfig holds configuration for decision filters
type FilterConfig struct {
	EnableMacroTrendFilter   bool    `yaml:"macro_trend_filter"`
	EnableReversalFilter     bool    `yaml:"reversal_filter"`
	EnableSupportFilter      bool    `yaml:"support_filter"`
	EnableVolumeFilter       bool    `yaml:"volume_filter"`
	MacroTrendThreshold      float64 `yaml:"macro_trend_threshold"`
	SupportDistanceThreshold float64 `yaml:"support_distance_threshold"`
	VolumeHighThreshold      float64 `yaml:"volume_high_threshold"`
	VolumeLowThreshold       float64 `yaml:"volume_low_threshold"`
}

// TimeframeConfig defines a timeframe and its weight
type TimeframeConfig struct {
	Interval string  `yaml:"interval"`
	Weight   float64 `yaml:"weight"`
}

// IndicatorConfig holds all indicator configurations
type IndicatorConfig struct {
	RSI       RSIConfig       `yaml:"rsi"`
	MACD      MACDConfig      `yaml:"macd"`
	Bollinger BollingerConfig `yaml:"bollinger"`
	EMA       EMAConfig       `yaml:"ema"`
}

// RSIConfig for RSI indicator
type RSIConfig struct {
	Overbought int     `yaml:"overbought"`
	Oversold   int     `yaml:"oversold"`
	Period     int     `yaml:"period"`
	Weight     float64 `yaml:"weight"`
}

// MACDConfig for MACD indicator
type MACDConfig struct {
	Fast   int     `yaml:"fast"`
	Slow   int     `yaml:"slow"`
	Signal int     `yaml:"signal"`
	Weight float64 `yaml:"weight"`
}

// BollingerConfig for Bollinger Bands
type BollingerConfig struct {
	Period int     `yaml:"period"`
	StdDev float64 `yaml:"std_dev"`
	Weight float64 `yaml:"weight"`
}

// EMAConfig for EMA indicator
type EMAConfig struct {
	Periods []int   `yaml:"periods"`
	Weight  float64 `yaml:"weight"`
}

// AlignmentConfig for trend alignment rules
type AlignmentConfig struct {
	Required      bool `yaml:"required"`
	MinTimeframes int  `yaml:"min_timeframes"`
}

// RiskConfig for risk management
type RiskConfig struct {
	TradeSize               float64 `yaml:"trade_size"`
	Leverage                int     `yaml:"leverage"`
	StopLossPct             float64 `yaml:"stop_loss_pct"`
	TakeProfitPct           float64 `yaml:"take_profit_pct"`
	ATRStopLossMultiplier   float64 `yaml:"atr_sl_multiplier"` // NEW: ATR multiplier for stop loss
	ATRTakeProfitMultiplier float64 `yaml:"atr_tp_multiplier"` // NEW: ATR multiplier for take profit
}

// ExecutionConfig for execution parameters
type ExecutionConfig struct {
	CheckInterval       int     `yaml:"check_interval"`       // seconds
	ConfidenceThreshold float64 `yaml:"confidence_threshold"` // NEW: Moved from Strategy level for clarity
}

// PositionManagementConfig for position management
type PositionManagementConfig struct {
	Enabled            bool    `yaml:"enabled"`              // Enable position management
	AutoClose          bool    `yaml:"auto_close"`           // Auto close at TP/SL
	TrailingStop       bool    `yaml:"trailing_stop"`        // Enable trailing stop
	TrailingStopPct    float64 `yaml:"trailing_stop_pct"`    // Trailing stop %
	BreakevenAfterPct  float64 `yaml:"breakeven_after_pct"`  // Move to breakeven after %
	PartialTakeProfits bool    `yaml:"partial_take_profits"` // Partial TP
	CheckInterval      int     `yaml:"check_interval"`       // Seconds between checks
	RequireConfirm     bool    `yaml:"require_confirm"`      // Require confirmation
}

// LoadStrategy loads a strategy from a YAML file
func LoadStrategy(path string) (*Strategy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read strategy file: %w", err)
	}

	var strategy Strategy
	if err := yaml.Unmarshal(data, &strategy); err != nil {
		return nil, fmt.Errorf("failed to parse strategy file: %w", err)
	}

	// Validate strategy
	if err := strategy.Validate(); err != nil {
		return nil, fmt.Errorf("invalid strategy: %w", err)
	}

	return &strategy, nil
}

// Validate checks if the strategy configuration is valid
func (s *Strategy) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("strategy name is required")
	}

	if len(s.Timeframes) == 0 {
		return fmt.Errorf("at least one timeframe is required")
	}

	// Validate timeframe weights sum
	totalWeight := 0.0
	for _, tf := range s.Timeframes {
		totalWeight += tf.Weight

		// Validate timeframe interval is not empty
		if tf.Interval == "" {
			return fmt.Errorf("timeframe interval cannot be empty")
		}
	}
	if totalWeight < 0.99 || totalWeight > 1.01 {
		return fmt.Errorf("timeframe weights must sum to 1.0, got %.2f", totalWeight)
	}

	// Validate required timeframes are in timeframes list
	allTimeframes := s.GetTimeframeIntervals()
	for _, required := range s.RequiredTimeframes {
		found := false
		for _, tf := range allTimeframes {
			if tf == required {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("required timeframe %s not found in timeframes list", required)
		}
	}

	// Validate candle limits
	for tf, limit := range s.CandleLimits {
		if limit < 10 || limit > 500 {
			return fmt.Errorf("candle limit for %s must be between 10 and 500, got %d", tf, limit)
		}
	}

	if s.Confidence < 0 || s.Confidence > 1 {
		return fmt.Errorf("confidence threshold must be between 0 and 1")
	}

	if s.Risk.TradeSize <= 0 {
		return fmt.Errorf("trade size must be positive")
	}

	if s.Risk.Leverage < 1 {
		return fmt.Errorf("leverage must be at least 1")
	}

	if s.Execution.CheckInterval < 1 {
		return fmt.Errorf("check interval must be at least 1 second")
	}

	// Validate ATR multipliers if set
	if s.Risk.ATRStopLossMultiplier < 0 {
		return fmt.Errorf("ATR stop loss multiplier must be positive")
	}
	if s.Risk.ATRTakeProfitMultiplier < 0 {
		return fmt.Errorf("ATR take profit multiplier must be positive")
	}

	// Validate filter thresholds
	if s.Filters.MacroTrendThreshold < 0 || s.Filters.MacroTrendThreshold > 1 {
		return fmt.Errorf("macro trend threshold must be between 0 and 1")
	}
	if s.Filters.SupportDistanceThreshold < 0 {
		return fmt.Errorf("support distance threshold must be positive")
	}
	if s.Filters.VolumeHighThreshold <= 0 {
		return fmt.Errorf("volume high threshold must be positive")
	}
	if s.Filters.VolumeLowThreshold < 0 || s.Filters.VolumeLowThreshold >= 1 {
		return fmt.Errorf("volume low threshold must be between 0 and 1")
	}

	return nil
}

// GetTimeframeIntervals returns all timeframe intervals
func (s *Strategy) GetTimeframeIntervals() []string {
	intervals := make([]string, len(s.Timeframes))
	for i, tf := range s.Timeframes {
		intervals[i] = tf.Interval
	}
	return intervals
}

// GetTimeframeWeight returns the weight for a specific timeframe
func (s *Strategy) GetTimeframeWeight(interval string) float64 {
	for _, tf := range s.Timeframes {
		if tf.Interval == interval {
			return tf.Weight
		}
	}
	return 0.0
}

// GetPrimaryTimeframe returns the primary (first) timeframe
func (s *Strategy) GetPrimaryTimeframe() string {
	if len(s.Timeframes) == 0 {
		return ""
	}
	return s.Timeframes[0].Interval
}

// GetSecondaryTimeframes returns all secondary timeframes (all except first)
func (s *Strategy) GetSecondaryTimeframes() []string {
	if len(s.Timeframes) <= 1 {
		return nil
	}
	intervals := make([]string, len(s.Timeframes)-1)
	for i := 1; i < len(s.Timeframes); i++ {
		intervals[i-1] = s.Timeframes[i].Interval
	}
	return intervals
}

// GetMacroTimeframe returns the longest (last) timeframe
func (s *Strategy) GetMacroTimeframe() string {
	if len(s.Timeframes) == 0 {
		return ""
	}
	return s.Timeframes[len(s.Timeframes)-1].Interval
}

// IsTimeframeRequired checks if a timeframe is in the required list
func (s *Strategy) IsTimeframeRequired(tf string) bool {
	for _, required := range s.RequiredTimeframes {
		if required == tf {
			return true
		}
	}
	return false
}

// GetCandleLimit returns the candle limit for a timeframe, or default if not set
func (s *Strategy) GetCandleLimit(tf string) int {
	if s.CandleLimits == nil {
		return 50 // default
	}
	if limit, ok := s.CandleLimits[tf]; ok {
		return limit
	}
	return 50 // default
}
