package ai

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
)

// ContextBuilder builds market context from multiple timeframes
type ContextBuilder struct {
	dataCollector *DataCollector
	indicators    *IndicatorCalculator
	log           *logrus.Logger
}

// NewContextBuilder creates a new context builder
func NewContextBuilder(collector *DataCollector, indicators *IndicatorCalculator, log *logrus.Logger) *ContextBuilder {
	return &ContextBuilder{
		dataCollector: collector,
		indicators:    indicators,
		log:           log,
	}
}

// BuildContext builds complete market context
// REFACTORED: Now uses dynamic map structure and accepts optional style
func (cb *ContextBuilder) BuildContext(ctx context.Context, pair string, style ...*Strategy) (*MarketContext, error) {
	cb.log.Debugf("Building context for %s", pair)

	// 1. Collect multi-timeframe data (pass style if provided)
	var mtfData *MultiTimeframeData
	var err error
	if len(style) > 0 && style[0] != nil {
		mtfData, err = cb.dataCollector.CollectData(ctx, pair, style[0])
	} else {
		mtfData, err = cb.dataCollector.CollectData(ctx, pair)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to collect data: %w", err)
	}

	// 2. Build context for each available timeframe
	marketContext := &MarketContext{
		Pair:       pair,
		Timestamp:  mtfData.Timestamp,
		Timeframes: make(map[string]*TimeframeContext),
		Style:      mtfData.Style,
	}

	// Build context for each timeframe in the data
	for tf, tfData := range mtfData.Timeframes {
		tfContext, err := cb.buildTimeframeContext(ctx, pair, tfData)
		if err != nil {
			cb.log.Warnf("Failed to build %s context: %v", tf, err)
			continue
		}
		marketContext.Timeframes[tf] = tfContext
	}

	// 3. Verify primary timeframe is available
	primaryTF := "5m"
	if marketContext.Style != nil {
		primaryTF = marketContext.Style.GetPrimaryTimeframe()
	}

	primaryContext := marketContext.GetTimeframe(primaryTF)
	if primaryContext == nil {
		return nil, fmt.Errorf("primary timeframe %s not available", primaryTF)
	}

	// 3.1 Verify all required timeframes are available
	if marketContext.Style != nil && len(marketContext.Style.RequiredTimeframes) > 0 {
		missingTimeframes := []string{}
		for _, requiredTF := range marketContext.Style.RequiredTimeframes {
			if !marketContext.HasTimeframe(requiredTF) {
				missingTimeframes = append(missingTimeframes, requiredTF)
			}
		}
		if len(missingTimeframes) > 0 {
			return nil, fmt.Errorf("required timeframes not available: %v", missingTimeframes)
		}
		cb.log.Debugf("All %d required timeframes available", len(marketContext.Style.RequiredTimeframes))
	}

	// 4. Get current price from primary timeframe
	if len(primaryContext.Candles) > 0 {
		marketContext.CurrentPrice = primaryContext.Candles[len(primaryContext.Candles)-1].Close
	}

	// 5. Detect key levels (use primary timeframe)
	marketContext.KeyLevels = cb.indicators.DetectKeyLevels(primaryContext.Candles)

	// Log context summary
	logFields := logrus.Fields{
		"pair":       pair,
		"price":      marketContext.CurrentPrice,
		"timeframes": len(marketContext.Timeframes),
		"primary_tf": primaryTF,
		"support":    marketContext.KeyLevels.Support,
		"resistance": marketContext.KeyLevels.Resistance,
	}

	// Add trend info for each timeframe
	for tf, tfCtx := range marketContext.Timeframes {
		logFields[fmt.Sprintf("trend_%s", tf)] = tfCtx.Trend
		logFields[fmt.Sprintf("rsi_%s", tf)] = tfCtx.Indicators.RSI
	}

	cb.log.WithFields(logFields).Debug("Context built")

	return marketContext, nil
}

// buildTimeframeContext builds context for a single timeframe
func (cb *ContextBuilder) buildTimeframeContext(ctx context.Context, pair string, tfData *TimeframeData) (*TimeframeContext, error) {
	if tfData == nil || len(tfData.Candles) == 0 {
		return nil, fmt.Errorf("no data for timeframe")
	}

	// Calculate indicators
	indicators, err := cb.indicators.CalculateAll(ctx, pair, tfData.Candles)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate indicators: %w", err)
	}

	// Analyze trend
	trend, strength := cb.indicators.AnalyzeTrend(indicators, tfData.Candles)

	return &TimeframeContext{
		Timeframe:  tfData.Timeframe,
		Trend:      trend,
		Strength:   strength,
		Indicators: indicators,
		Candles:    tfData.Candles,
	}, nil
}

// GetTrendAlignment checks if trends are aligned across timeframes
// REFACTORED: Now uses dynamic timeframe access
func (cb *ContextBuilder) GetTrendAlignment(marketCtx *MarketContext) (bool, string) {
	// Get primary and secondary timeframes
	primaryCtx := marketCtx.GetPrimaryContext()
	if primaryCtx == nil {
		return false, "unknown"
	}

	// Get first secondary timeframe (usually 15m for scalping)
	secondaryContexts := marketCtx.GetSecondaryContexts()
	if len(secondaryContexts) == 0 {
		// Only one timeframe available, consider it aligned
		return true, primaryCtx.Trend
	}

	secondaryCtx := secondaryContexts[0]

	// Both bullish
	if primaryCtx.Trend == "bullish" && secondaryCtx.Trend == "bullish" {
		return true, "bullish"
	}

	// Both bearish
	if primaryCtx.Trend == "bearish" && secondaryCtx.Trend == "bearish" {
		return true, "bearish"
	}

	// Not aligned
	return false, "mixed"
}

// IsNearSupport checks if price is near support
func (cb *ContextBuilder) IsNearSupport(marketCtx *MarketContext, threshold float64) bool {
	if marketCtx.KeyLevels == nil || marketCtx.KeyLevels.Distance == nil {
		return false
	}
	return marketCtx.KeyLevels.Distance.ToSupport < threshold
}

// IsNearResistance checks if price is near resistance
func (cb *ContextBuilder) IsNearResistance(marketCtx *MarketContext, threshold float64) bool {
	if marketCtx.KeyLevels == nil || marketCtx.KeyLevels.Distance == nil {
		return false
	}
	return marketCtx.KeyLevels.Distance.ToResistance < threshold
}
