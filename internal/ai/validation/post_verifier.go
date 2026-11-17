package validation

import (
	"fmt"
	"math"
)

// PostDecisionVerifier verifies AI decisions for consistency
type PostDecisionVerifier struct{}

// NewPostDecisionVerifier creates a new post-decision verifier
func NewPostDecisionVerifier() *PostDecisionVerifier {
	return &PostDecisionVerifier{}
}

// Verify checks decision consistency
func (pdv *PostDecisionVerifier) Verify(trade *TradingDecision) *ValidationResult {
	result := &ValidationResult{
		Passed:           true,
		CriticalFailures: []RuleViolation{},
		Warnings:         []RuleViolation{},
	}

	// Run all verification checks
	pdv.verifyPriceRelationships(trade, result)
	pdv.verifyCalculations(trade, result)
	pdv.verifyLogicalConsistency(trade, result)
	pdv.verifyConfidenceAlignment(trade, result)

	return result
}

// verifyPriceRelationships checks if entry/stop/target relationships are valid
func (pdv *PostDecisionVerifier) verifyPriceRelationships(
	trade *TradingDecision,
	result *ValidationResult,
) {
	if trade.Direction == "LONG" {
		// For LONG: Stop < Entry < Target
		if trade.StopLoss >= trade.EntryPrice {
			result.addCriticalFailure(
				"INVALID_LONG_STOP",
				"Price Relationship",
				fmt.Sprintf("LONG stop loss %.2f must be below entry %.2f",
					trade.StopLoss, trade.EntryPrice),
			)
		}
		if trade.TakeProfit <= trade.EntryPrice {
			result.addCriticalFailure(
				"INVALID_LONG_TARGET",
				"Price Relationship",
				fmt.Sprintf("LONG take profit %.2f must be above entry %.2f",
					trade.TakeProfit, trade.EntryPrice),
			)
		}
	} else if trade.Direction == "SHORT" {
		// For SHORT: Target < Entry < Stop
		if trade.StopLoss <= trade.EntryPrice {
			result.addCriticalFailure(
				"INVALID_SHORT_STOP",
				"Price Relationship",
				fmt.Sprintf("SHORT stop loss %.2f must be above entry %.2f",
					trade.StopLoss, trade.EntryPrice),
			)
		}
		if trade.TakeProfit >= trade.EntryPrice {
			result.addCriticalFailure(
				"INVALID_SHORT_TARGET",
				"Price Relationship",
				fmt.Sprintf("SHORT take profit %.2f must be below entry %.2f",
					trade.TakeProfit, trade.EntryPrice),
			)
		}
	}
}

// verifyCalculations checks if calculated values are correct
func (pdv *PostDecisionVerifier) verifyCalculations(
	trade *TradingDecision,
	result *ValidationResult,
) {
	// Verify R:R calculation
	stopDistance := math.Abs(trade.EntryPrice - trade.StopLoss)
	targetDistance := math.Abs(trade.TakeProfit - trade.EntryPrice)

	if stopDistance == 0 {
		result.addCriticalFailure(
			"ZERO_STOP_DISTANCE",
			"Calculation Error",
			"Stop distance cannot be zero",
		)
		return
	}

	calculatedRR := targetDistance / stopDistance
	tolerance := 0.1 // 10% tolerance for rounding

	if math.Abs(calculatedRR-trade.RiskRewardRatio) > tolerance {
		result.addWarning(
			"RR_CALCULATION_MISMATCH",
			"Calculation Error",
			fmt.Sprintf("Stated R:R %.2f doesn't match calculated %.2f",
				trade.RiskRewardRatio, calculatedRR),
		)
	}

	// Verify stop loss percentage
	calculatedStopPercent := (stopDistance / trade.EntryPrice) * 100
	if math.Abs(calculatedStopPercent-trade.StopLossPercent) > 0.1 {
		result.addWarning(
			"STOP_PERCENT_MISMATCH",
			"Calculation Error",
			fmt.Sprintf("Stated stop %.2f%% doesn't match calculated %.2f%%",
				trade.StopLossPercent, calculatedStopPercent),
		)
	}

	// Verify position size makes sense
	if trade.PositionSize <= 0 {
		result.addCriticalFailure(
			"INVALID_POSITION_SIZE",
			"Calculation Error",
			"Position size must be positive",
		)
	}

	// Verify leverage is reasonable
	if trade.Leverage <= 0 || trade.Leverage > 125 {
		result.addCriticalFailure(
			"INVALID_LEVERAGE",
			"Calculation Error",
			fmt.Sprintf("Leverage %.1fx is invalid", trade.Leverage),
		)
	}
}

// verifyLogicalConsistency checks for logical errors in the decision
func (pdv *PostDecisionVerifier) verifyLogicalConsistency(
	trade *TradingDecision,
	result *ValidationResult,
) {
	// Check if direction matches bias
	if trade.Direction == "" {
		result.addCriticalFailure(
			"MISSING_DIRECTION",
			"Logical Error",
			"Trade direction is not specified",
		)
	}

	// Check if pair is specified
	if trade.Pair == "" {
		result.addCriticalFailure(
			"MISSING_PAIR",
			"Logical Error",
			"Trading pair is not specified",
		)
	}

	// Check if confluence factors make sense
	if trade.ConfluenceCount > 10 {
		result.addWarning(
			"EXCESSIVE_CONFLUENCE",
			"Logical Error",
			fmt.Sprintf("Confluence count %d seems excessive", trade.ConfluenceCount),
		)
	}

	// Check if confidence level matches risk parameters
	if trade.Confidence == "HIGH" && trade.RiskRewardRatio < 1.5 {
		result.addWarning(
			"CONFIDENCE_RR_MISMATCH",
			"Logical Error",
			"HIGH confidence trade should have better R:R ratio",
		)
	}

	if trade.Confidence == "LOW" && trade.RiskPercent > 0.5 {
		result.addWarning(
			"CONFIDENCE_RISK_MISMATCH",
			"Logical Error",
			"LOW confidence trade should have lower risk",
		)
	}

	// Check if timeframe is specified
	if trade.Timeframe == "" {
		result.addWarning(
			"MISSING_TIMEFRAME",
			"Logical Error",
			"Trade timeframe is not specified",
		)
	}
}

// verifyConfidenceAlignment checks if confidence aligns with trade quality
func (pdv *PostDecisionVerifier) verifyConfidenceAlignment(
	trade *TradingDecision,
	result *ValidationResult,
) {
	// Calculate a quality score
	qualityScore := 0

	// Good R:R adds to quality
	if trade.RiskRewardRatio >= 2.0 {
		qualityScore += 2
	} else if trade.RiskRewardRatio >= 1.5 {
		qualityScore += 1
	}

	// High confluence adds to quality
	if trade.ConfluenceCount >= 5 {
		qualityScore += 2
	} else if trade.ConfluenceCount >= 3 {
		qualityScore += 1
	}

	// Low risk adds to quality
	if trade.RiskPercent <= 0.5 {
		qualityScore += 1
	}

	// Check alignment
	if trade.Confidence == "HIGH" && qualityScore < 3 {
		result.addWarning(
			"OVERCONFIDENT",
			"Confidence Alignment",
			fmt.Sprintf("HIGH confidence doesn't match trade quality (score: %d/5)", qualityScore),
		)
	}

	if trade.Confidence == "LOW" && qualityScore >= 4 {
		result.addWarning(
			"UNDERCONFIDENT",
			"Confidence Alignment",
			fmt.Sprintf("LOW confidence doesn't match high trade quality (score: %d/5)", qualityScore),
		)
	}
}

// VerifyWithContext performs verification with additional market context
func (pdv *PostDecisionVerifier) VerifyWithContext(
	trade *TradingDecision,
	marketContext map[string]interface{},
) *ValidationResult {
	result := pdv.Verify(trade)

	// Additional context-based checks
	if volatility, ok := marketContext["volatility"].(string); ok {
		if volatility == "EXTREME" && trade.Leverage > 5 {
			result.addWarning(
				"HIGH_LEVERAGE_IN_VOLATILITY",
				"Context Mismatch",
				"High leverage in extreme volatility is risky",
			)
		}
	}

	if trend, ok := marketContext["trend"].(string); ok {
		if trend == "RANGING" && trade.RiskRewardRatio < 2.0 {
			result.addWarning(
				"LOW_RR_IN_RANGE",
				"Context Mismatch",
				"Ranging markets require better R:R ratios",
			)
		}
	}

	return result
}
