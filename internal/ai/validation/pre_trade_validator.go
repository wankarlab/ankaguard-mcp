package validation

import (
	"fmt"
	"math"
)

// PreTradeValidator validates trades before execution
type PreTradeValidator struct {
	config *Config
}

// NewPreTradeValidator creates a new pre-trade validator
func NewPreTradeValidator(config *Config) *PreTradeValidator {
	return &PreTradeValidator{
		config: config,
	}
}

// Validate performs pre-trade validation
func (ptv *PreTradeValidator) Validate(
	trade *TradingDecision,
	portfolio *PortfolioState,
) *ValidationResult {
	result := &ValidationResult{
		Passed:           true,
		CriticalFailures: []RuleViolation{},
		Warnings:         []RuleViolation{},
	}

	// Run all validation checks
	ptv.validatePositionSizing(trade, portfolio, result)
	ptv.validateRiskReward(trade, result)
	ptv.validateConfluence(trade, result)
	ptv.validateStopLoss(trade, result)
	ptv.validateTakeProfit(trade, result)
	ptv.validateLeverage(trade, result)
	ptv.validateMarketConditions(trade, result)

	return result
}

// validatePositionSizing checks if position size is within risk limits
func (ptv *PreTradeValidator) validatePositionSizing(
	trade *TradingDecision,
	portfolio *PortfolioState,
	result *ValidationResult,
) {
	// Check max risk per trade
	if trade.RiskPercent > ptv.config.MaxRiskPerTrade {
		result.addCriticalFailure(
			"RISK_TOO_HIGH",
			"Position Sizing",
			fmt.Sprintf("Risk %.2f%% exceeds maximum %.2f%%",
				trade.RiskPercent, ptv.config.MaxRiskPerTrade),
		)
	}

	// Check if position size is reasonable
	if trade.PositionSize <= 0 {
		result.addCriticalFailure(
			"INVALID_POSITION_SIZE",
			"Position Sizing",
			"Position size must be greater than zero",
		)
	}

	// Check if we have enough capital
	requiredCapital := trade.PositionSize / trade.Leverage
	if requiredCapital > portfolio.AvailableCapital {
		result.addCriticalFailure(
			"INSUFFICIENT_CAPITAL",
			"Position Sizing",
			fmt.Sprintf("Required capital $%.2f exceeds available $%.2f",
				requiredCapital, portfolio.AvailableCapital),
		)
	}

	// Warning if position is very large relative to account
	positionPercent := (trade.PositionSize / portfolio.TotalCapital) * 100
	if positionPercent > 50 {
		result.addWarning(
			"LARGE_POSITION",
			"Position Sizing",
			fmt.Sprintf("Position is %.1f%% of total capital", positionPercent),
		)
	}
}

// validateRiskReward checks if R:R ratio meets minimum requirements
func (ptv *PreTradeValidator) validateRiskReward(
	trade *TradingDecision,
	result *ValidationResult,
) {
	if trade.RiskRewardRatio < ptv.config.MinRiskReward {
		result.addCriticalFailure(
			"POOR_RISK_REWARD",
			"Risk Management",
			fmt.Sprintf("R:R ratio %.2f is below minimum %.2f",
				trade.RiskRewardRatio, ptv.config.MinRiskReward),
		)
	}

	// Warning if R:R is barely acceptable
	if trade.RiskRewardRatio < ptv.config.MinRiskReward*1.2 {
		result.addWarning(
			"MARGINAL_RISK_REWARD",
			"Risk Management",
			fmt.Sprintf("R:R ratio %.2f is barely acceptable", trade.RiskRewardRatio),
		)
	}
}

// validateConfluence checks if enough confluence factors are present
func (ptv *PreTradeValidator) validateConfluence(
	trade *TradingDecision,
	result *ValidationResult,
) {
	if trade.ConfluenceCount < ptv.config.MinConfluence {
		result.addCriticalFailure(
			"INSUFFICIENT_CONFLUENCE",
			"Trade Quality",
			fmt.Sprintf("Only %d confluence factors, minimum %d required",
				trade.ConfluenceCount, ptv.config.MinConfluence),
		)
	}

	// Warning if confluence is marginal
	if trade.ConfluenceCount == ptv.config.MinConfluence {
		result.addWarning(
			"MARGINAL_CONFLUENCE",
			"Trade Quality",
			"Confluence count is at minimum threshold",
		)
	}
}

// validateStopLoss checks if stop loss is properly set
func (ptv *PreTradeValidator) validateStopLoss(
	trade *TradingDecision,
	result *ValidationResult,
) {
	if trade.StopLoss <= 0 {
		result.addCriticalFailure(
			"MISSING_STOP_LOSS",
			"Risk Management",
			"Stop loss must be set",
		)
		return
	}

	// Check if stop is too tight
	stopDistance := math.Abs(trade.EntryPrice-trade.StopLoss) / trade.EntryPrice * 100
	if stopDistance < 0.5 {
		result.addWarning(
			"STOP_TOO_TIGHT",
			"Risk Management",
			fmt.Sprintf("Stop loss is only %.2f%% away, may get stopped out prematurely", stopDistance),
		)
	}

	// Check if stop is too wide
	if stopDistance > 5 {
		result.addWarning(
			"STOP_TOO_WIDE",
			"Risk Management",
			fmt.Sprintf("Stop loss is %.2f%% away, may be too wide", stopDistance),
		)
	}

	// Validate stop is on correct side of entry
	if trade.Direction == "LONG" && trade.StopLoss >= trade.EntryPrice {
		result.addCriticalFailure(
			"INVALID_STOP_PLACEMENT",
			"Risk Management",
			"Stop loss for LONG must be below entry price",
		)
	}
	if trade.Direction == "SHORT" && trade.StopLoss <= trade.EntryPrice {
		result.addCriticalFailure(
			"INVALID_STOP_PLACEMENT",
			"Risk Management",
			"Stop loss for SHORT must be above entry price",
		)
	}
}

// validateTakeProfit checks if take profit targets are reasonable
func (ptv *PreTradeValidator) validateTakeProfit(
	trade *TradingDecision,
	result *ValidationResult,
) {
	if trade.TakeProfit <= 0 {
		result.addWarning(
			"MISSING_TAKE_PROFIT",
			"Trade Management",
			"No take profit target set",
		)
		return
	}

	// Validate TP is on correct side of entry
	if trade.Direction == "LONG" && trade.TakeProfit <= trade.EntryPrice {
		result.addCriticalFailure(
			"INVALID_TP_PLACEMENT",
			"Trade Management",
			"Take profit for LONG must be above entry price",
		)
	}
	if trade.Direction == "SHORT" && trade.TakeProfit >= trade.EntryPrice {
		result.addCriticalFailure(
			"INVALID_TP_PLACEMENT",
			"Trade Management",
			"Take profit for SHORT must be below entry price",
		)
	}

	// Check if TP is realistic
	tpDistance := math.Abs(trade.TakeProfit-trade.EntryPrice) / trade.EntryPrice * 100
	if tpDistance > 10 {
		result.addWarning(
			"AMBITIOUS_TARGET",
			"Trade Management",
			fmt.Sprintf("Take profit is %.2f%% away, may be too ambitious", tpDistance),
		)
	}
}

// validateLeverage checks if leverage is within acceptable limits
func (ptv *PreTradeValidator) validateLeverage(
	trade *TradingDecision,
	result *ValidationResult,
) {
	// Set max leverage based on asset
	var maxLeverage float64
	if trade.Pair == "BTCUSDT" || trade.Pair == "ETHUSDT" {
		maxLeverage = 10.0
	} else {
		maxLeverage = 5.0
	}

	if trade.Leverage > maxLeverage {
		result.addCriticalFailure(
			"EXCESSIVE_LEVERAGE",
			"Risk Management",
			fmt.Sprintf("Leverage %.1fx exceeds maximum %.1fx for %s",
				trade.Leverage, maxLeverage, trade.Pair),
		)
	}

	// Warning for high leverage
	if trade.Leverage > maxLeverage*0.8 {
		result.addWarning(
			"HIGH_LEVERAGE",
			"Risk Management",
			fmt.Sprintf("Leverage %.1fx is high", trade.Leverage),
		)
	}

	// Check liquidation distance
	// Using 15% threshold to allow reasonable 5x leverage trades
	liquidationDistance := ptv.calculateLiquidationDistance(trade)
	if liquidationDistance < 15 {
		result.addCriticalFailure(
			"LIQUIDATION_TOO_CLOSE",
			"Risk Management",
			fmt.Sprintf("Liquidation price is only %.1f%% away", liquidationDistance),
		)
	}
}

// calculateLiquidationDistance estimates distance to liquidation
func (ptv *PreTradeValidator) calculateLiquidationDistance(trade *TradingDecision) float64 {
	// Simplified calculation
	// Actual liquidation depends on margin mode and exchange
	maintenanceMargin := 0.5 // 0.5% maintenance margin

	if trade.Direction == "LONG" {
		liquidationPrice := trade.EntryPrice * (1 - (1/trade.Leverage - maintenanceMargin/100))
		return (trade.EntryPrice - liquidationPrice) / trade.EntryPrice * 100
	} else {
		liquidationPrice := trade.EntryPrice * (1 + (1/trade.Leverage - maintenanceMargin/100))
		return (liquidationPrice - trade.EntryPrice) / trade.EntryPrice * 100
	}
}

// validateMarketConditions checks if market conditions are suitable
func (ptv *PreTradeValidator) validateMarketConditions(
	trade *TradingDecision,
	result *ValidationResult,
) {
	// Check volatility
	if trade.Volatility == "EXTREME" {
		result.addWarning(
			"EXTREME_VOLATILITY",
			"Market Conditions",
			"Market volatility is extreme, trade with caution",
		)
	}

	// Check spread
	if trade.Spread > 0.1 {
		result.addWarning(
			"WIDE_SPREAD",
			"Market Conditions",
			fmt.Sprintf("Spread is %.2f%%, may impact execution", trade.Spread*100),
		)
	}
}

// Helper methods for ValidationResult

func (vr *ValidationResult) addCriticalFailure(id, category, message string) {
	vr.CriticalFailures = append(vr.CriticalFailures, RuleViolation{
		RuleID:   id,
		RuleName: category,
		Severity: "CRITICAL",
		Message:  message,
	})
	vr.Passed = false
}

func (vr *ValidationResult) addWarning(id, category, message string) {
	vr.Warnings = append(vr.Warnings, RuleViolation{
		RuleID:   id,
		RuleName: category,
		Severity: "WARNING",
		Message:  message,
	})
}
