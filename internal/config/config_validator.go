package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/rantipay/trading-mcp/internal/gct"
)

// ValidationError represents a configuration validation error
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ConfigValidator validates trading configuration
type ConfigValidator struct {
	gctClient gct.GCTClient
}

// NewConfigValidator creates a new configuration validator
func NewConfigValidator(gctClient gct.GCTClient) *ConfigValidator {
	return &ConfigValidator{
		gctClient: gctClient,
	}
}

// Validate validates all configuration parameters
func (cv *ConfigValidator) Validate(ctx context.Context, config *DynamicTradingConfig) []ValidationError {
	var errors []ValidationError

	// Validate timeframes
	if err := cv.ValidateTimeframes(ctx, config.Timeframes, config.Exchange); err != nil {
		errors = append(errors, ValidationError{
			Field:   "timeframes",
			Message: err.Error(),
		})
	}

	// Validate risk parameters
	if errs := cv.ValidateRiskParams(config); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Validate TP/SL
	if errs := cv.ValidateTPSL(config); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Validate timing parameters
	if errs := cv.ValidateTimingParams(config); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	// Validate execution parameters
	if errs := cv.ValidateExecutionParams(config); len(errs) > 0 {
		errors = append(errors, errs...)
	}

	return errors
}

// ValidateTimeframes validates timeframes against exchange support
func (cv *ConfigValidator) ValidateTimeframes(ctx context.Context, timeframes []string, exchange string) error {
	if len(timeframes) == 0 {
		return fmt.Errorf("at least one timeframe is required")
	}

	if len(timeframes) > 5 {
		return fmt.Errorf("maximum 5 timeframes allowed, got %d", len(timeframes))
	}

	// Supported timeframes by most exchanges
	supportedTimeframes := map[string]bool{
		"1m":  true,
		"3m":  true,
		"5m":  true,
		"15m": true,
		"30m": true,
		"1h":  true,
		"2h":  true,
		"4h":  true,
		"6h":  true,
		"12h": true,
		"1d":  true,
		"3d":  true,
		"1w":  true,
	}

	for _, tf := range timeframes {
		if !supportedTimeframes[tf] {
			supported := make([]string, 0, len(supportedTimeframes))
			for k := range supportedTimeframes {
				supported = append(supported, k)
			}
			return fmt.Errorf("timeframe '%s' not supported. Supported: %s", tf, strings.Join(supported, ", "))
		}
	}

	return nil
}

// ValidateRiskParams validates risk management parameters
func (cv *ConfigValidator) ValidateRiskParams(config *DynamicTradingConfig) []ValidationError {
	var errors []ValidationError

	if config.RiskPerTradePct < 0.1 || config.RiskPerTradePct > 5.0 {
		errors = append(errors, ValidationError{
			Field:   "risk_per_trade_pct",
			Message: "must be between 0.1 and 5.0",
		})
	}

	if config.MaxDailyLoss <= 0 {
		errors = append(errors, ValidationError{
			Field:   "max_daily_loss_usd",
			Message: "must be positive",
		})
	}

	if config.MaxDailyTrades < 1 || config.MaxDailyTrades > 1000 {
		errors = append(errors, ValidationError{
			Field:   "max_daily_trades",
			Message: "must be between 1 and 1000",
		})
	}

	if config.ConsecutiveLossLimit < 1 || config.ConsecutiveLossLimit > 10 {
		errors = append(errors, ValidationError{
			Field:   "consecutive_loss_limit",
			Message: "must be between 1 and 10",
		})
	}

	if config.MaxOpenPositions < 1 || config.MaxOpenPositions > 10 {
		errors = append(errors, ValidationError{
			Field:   "max_open_positions",
			Message: "must be between 1 and 10",
		})
	}

	if config.TradeSize <= 0 {
		errors = append(errors, ValidationError{
			Field:   "trade_size_usd",
			Message: "must be positive",
		})
	}

	if config.Leverage < 1 || config.Leverage > 100 {
		errors = append(errors, ValidationError{
			Field:   "leverage",
			Message: "must be between 1 and 100",
		})
	}

	return errors
}

// ValidateTPSL validates take profit and stop loss configuration
func (cv *ConfigValidator) ValidateTPSL(config *DynamicTradingConfig) []ValidationError {
	var errors []ValidationError

	if config.TakeProfitPct < 0.1 || config.TakeProfitPct > 10.0 {
		errors = append(errors, ValidationError{
			Field:   "take_profit_pct",
			Message: "must be between 0.1 and 10.0",
		})
	}

	if config.StopLossPct < 0.1 || config.StopLossPct > 10.0 {
		errors = append(errors, ValidationError{
			Field:   "stop_loss_pct",
			Message: "must be between 0.1 and 10.0",
		})
	}

	// Validate TP levels if provided
	if len(config.TPLevels) > 0 {
		totalPct := 0.0
		for i, level := range config.TPLevels {
			if level.PricePct < 0.1 || level.PricePct > 100 {
				errors = append(errors, ValidationError{
					Field:   fmt.Sprintf("tp_levels[%d].price_pct", i),
					Message: "must be between 0.1 and 100",
				})
			}
			if level.SizePct < 1 || level.SizePct > 100 {
				errors = append(errors, ValidationError{
					Field:   fmt.Sprintf("tp_levels[%d].size_pct", i),
					Message: "must be between 1 and 100",
				})
			}
			totalPct += level.SizePct
		}

		if totalPct != 100.0 {
			errors = append(errors, ValidationError{
				Field:   "tp_levels",
				Message: fmt.Sprintf("size percentages must sum to 100, got %.2f", totalPct),
			})
		}
	}

	// Validate breakeven config
	if config.BreakevenConfig != nil && config.BreakevenConfig.Enabled {
		if config.BreakevenConfig.TriggerPct < 0 || config.BreakevenConfig.TriggerPct > 10 {
			errors = append(errors, ValidationError{
				Field:   "breakeven_config.trigger_pct",
				Message: "must be between 0 and 10",
			})
		}
		if config.BreakevenConfig.BufferPct < 0 || config.BreakevenConfig.BufferPct > 1.0 {
			errors = append(errors, ValidationError{
				Field:   "breakeven_config.buffer_pct",
				Message: "must be between 0 and 1.0",
			})
		}
	}

	// Validate trailing config
	if config.TrailingConfig != nil && config.TrailingConfig.Enabled {
		if config.TrailingConfig.DistancePct < 0.1 || config.TrailingConfig.DistancePct > 10 {
			errors = append(errors, ValidationError{
				Field:   "trailing_config.distance_pct",
				Message: "must be between 0.1 and 10",
			})
		}
	}

	return errors
}

// ValidateTimingParams validates timing parameters
func (cv *ConfigValidator) ValidateTimingParams(config *DynamicTradingConfig) []ValidationError {
	var errors []ValidationError

	if config.AnalysisIntervalSeconds < 1 || config.AnalysisIntervalSeconds > 300 {
		errors = append(errors, ValidationError{
			Field:   "analysis_interval_seconds",
			Message: "must be between 1 and 300",
		})
	}

	if config.MonitorIntervalSeconds < 1 || config.MonitorIntervalSeconds > 60 {
		errors = append(errors, ValidationError{
			Field:   "monitor_interval_seconds",
			Message: "must be between 1 and 60",
		})
	}

	if config.ReanalysisDelaySeconds < 0 || config.ReanalysisDelaySeconds > 30 {
		errors = append(errors, ValidationError{
			Field:   "reanalysis_delay_seconds",
			Message: "must be between 0 and 30",
		})
	}

	if config.MaxReentrySeconds < 1 || config.MaxReentrySeconds > 300 {
		errors = append(errors, ValidationError{
			Field:   "max_reentry_seconds",
			Message: "must be between 1 and 300",
		})
	}

	return errors
}

// ValidateExecutionParams validates execution parameters
func (cv *ConfigValidator) ValidateExecutionParams(config *DynamicTradingConfig) []ValidationError {
	var errors []ValidationError

	if config.OrderType != "market" && config.OrderType != "limit" {
		errors = append(errors, ValidationError{
			Field:   "order_type",
			Message: "must be 'market' or 'limit'",
		})
	}

	if config.ExecutionTimeoutSec < 1 || config.ExecutionTimeoutSec > 300 {
		errors = append(errors, ValidationError{
			Field:   "execution_timeout_seconds",
			Message: "must be between 1 and 300",
		})
	}

	if config.MaxExecutionRetries < 0 || config.MaxExecutionRetries > 10 {
		errors = append(errors, ValidationError{
			Field:   "max_execution_retries",
			Message: "must be between 0 and 10",
		})
	}

	if config.SlippageTolerancePct < 0 || config.SlippageTolerancePct > 5.0 {
		errors = append(errors, ValidationError{
			Field:   "slippage_tolerance_pct",
			Message: "must be between 0 and 5.0",
		})
	}

	return errors
}
