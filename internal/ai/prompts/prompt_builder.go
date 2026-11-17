package prompts

import (
	"fmt"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
)

// PromptBuilder builds prompts for LLM analysis
type PromptBuilder struct {
	templateManager *TemplateManager
}

// NewPromptBuilder creates a new prompt builder
func NewPromptBuilder(templateManager *TemplateManager) *PromptBuilder {
	return &PromptBuilder{
		templateManager: templateManager,
	}
}

// BuildAnalysisPrompt builds a prompt for market analysis
func (pb *PromptBuilder) BuildAnalysisPrompt(
	pair string,
	ctx *ai.MarketContext,
	styleType string,
	primaryTimeframe string,
	secondaryTimeframes []string,
	knowledgeContext string,
) (string, error) {
	if styleType == "" {
		return "", fmt.Errorf("trading style is required")
	}

	// Get template for style
	template, err := pb.templateManager.GetTemplateForStyle(styleType)
	if err != nil {
		return "", fmt.Errorf("failed to get template: %w", err)
	}

	// Build variables map
	variables := pb.buildVariables(pair, ctx, primaryTimeframe, secondaryTimeframes, knowledgeContext)

	// Inject variables into template
	prompt := pb.templateManager.InjectVariables(template, variables)

	return prompt, nil
}

// buildVariables builds the variables map for template injection
func (pb *PromptBuilder) buildVariables(
	pair string,
	ctx *ai.MarketContext,
	primaryTimeframe string,
	secondaryTimeframes []string,
	knowledgeContext string,
) map[string]string {
	variables := make(map[string]string)

	// Basic info
	variables["PAIR"] = pair
	variables["CURRENT_PRICE"] = fmt.Sprintf("%.2f", ctx.CurrentPrice)
	variables["TIMESTAMP"] = time.Now().Format("2006-01-02 15:04:05 UTC")

	// Trading style info
	variables["PRIMARY_TIMEFRAME"] = primaryTimeframe
	variables["SECONDARY_TIMEFRAMES"] = strings.Join(secondaryTimeframes, ", ")

	// Timeframe analysis
	variables["TIMEFRAME_ANALYSIS"] = pb.buildTimeframeAnalysis(ctx)

	// Indicators
	variables["INDICATORS"] = pb.buildIndicatorsSection(ctx)

	// Key levels
	if ctx.KeyLevels != nil {
		variables["SUPPORT"] = fmt.Sprintf("%.2f", ctx.KeyLevels.Support)
		variables["RESISTANCE"] = fmt.Sprintf("%.2f", ctx.KeyLevels.Resistance)
	} else {
		variables["SUPPORT"] = "N/A"
		variables["RESISTANCE"] = "N/A"
	}

	// Market context
	variables["VOLATILITY"] = pb.getVolatilityDescription(ctx)
	variables["SPREAD"] = "0.05" // TODO: Get actual spread
	variables["VOLUME_STATUS"] = pb.getVolumeStatus(ctx)
	variables["TREND_ALIGNMENT"] = pb.getTrendAlignment(ctx)
	variables["TRADING_SESSION"] = pb.getTradingSession()
	variables["MARKET_PHASE"] = pb.getMarketPhase(ctx)

	// Knowledge context
	variables["KNOWLEDGE_CONTEXT"] = knowledgeContext

	return variables
}

// buildTimeframeAnalysis builds the timeframe analysis section
func (pb *PromptBuilder) buildTimeframeAnalysis(ctx *ai.MarketContext) string {
	var parts []string

	for tf, tfCtx := range ctx.Timeframes {
		if tfCtx == nil {
			continue
		}

		analysis := fmt.Sprintf("**%s Timeframe**:\n", tf)
		analysis += fmt.Sprintf("- Trend: %s (Strength: %.2f)\n", tfCtx.Trend, tfCtx.Strength)
		analysis += fmt.Sprintf("- RSI: %.2f\n", tfCtx.Indicators.RSI)

		if tfCtx.Indicators.MACD != nil {
			analysis += fmt.Sprintf("- MACD: %.4f (Signal: %.4f)\n", tfCtx.Indicators.MACD.Value, tfCtx.Indicators.MACD.Signal)
		}

		parts = append(parts, analysis)
	}

	if len(parts) == 0 {
		return "No timeframe data available"
	}

	return strings.Join(parts, "\n")
}

// buildIndicatorsSection builds the indicators section
func (pb *PromptBuilder) buildIndicatorsSection(ctx *ai.MarketContext) string {
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx == nil {
		return "No indicator data available"
	}

	indicators := fmt.Sprintf("**Primary Timeframe Indicators**:\n")
	indicators += fmt.Sprintf("- RSI(14): %.2f\n", primaryCtx.Indicators.RSI)

	if primaryCtx.Indicators.MACD != nil {
		indicators += fmt.Sprintf("- MACD: %.4f\n", primaryCtx.Indicators.MACD.Value)
		indicators += fmt.Sprintf("- MACD Signal: %.4f\n", primaryCtx.Indicators.MACD.Signal)
		indicators += fmt.Sprintf("- MACD Histogram: %.4f\n", primaryCtx.Indicators.MACD.Histogram)
	}

	if primaryCtx.Indicators.Bollinger != nil {
		indicators += fmt.Sprintf("- Bollinger Upper: %.2f\n", primaryCtx.Indicators.Bollinger.Upper)
		indicators += fmt.Sprintf("- Bollinger Middle: %.2f\n", primaryCtx.Indicators.Bollinger.Middle)
		indicators += fmt.Sprintf("- Bollinger Lower: %.2f\n", primaryCtx.Indicators.Bollinger.Lower)
	}

	indicators += fmt.Sprintf("- ATR: %.2f\n", primaryCtx.Indicators.ATR)

	return indicators
}

// getVolatilityDescription returns a description of current volatility
func (pb *PromptBuilder) getVolatilityDescription(ctx *ai.MarketContext) string {
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx == nil {
		return "Unknown"
	}

	atr := primaryCtx.Indicators.ATR
	atrPct := (atr / ctx.CurrentPrice) * 100

	switch {
	case atrPct < 0.5:
		return "Low"
	case atrPct < 2.0:
		return "Moderate"
	case atrPct < 5.0:
		return "High"
	default:
		return "Extreme"
	}
}

// getVolumeStatus returns the volume status
func (pb *PromptBuilder) getVolumeStatus(ctx *ai.MarketContext) string {
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx == nil || len(primaryCtx.Candles) == 0 {
		return "Unknown"
	}

	// Get last candle volume
	lastVolume := primaryCtx.Candles[len(primaryCtx.Candles)-1].Volume

	// Calculate average volume (last 20 candles)
	var totalVolume float64
	count := 0
	for i := len(primaryCtx.Candles) - 20; i < len(primaryCtx.Candles)-1; i++ {
		if i >= 0 {
			totalVolume += primaryCtx.Candles[i].Volume
			count++
		}
	}

	if count == 0 {
		return "Unknown"
	}

	avgVolume := totalVolume / float64(count)
	ratio := lastVolume / avgVolume

	switch {
	case ratio > 1.5:
		return "High (above average)"
	case ratio > 0.8:
		return "Normal"
	default:
		return "Low (below average)"
	}
}

// getTrendAlignment returns trend alignment status
func (pb *PromptBuilder) getTrendAlignment(ctx *ai.MarketContext) string {
	var trends []string
	for _, tfCtx := range ctx.Timeframes {
		if tfCtx != nil {
			trends = append(trends, tfCtx.Trend)
		}
	}

	if len(trends) == 0 {
		return "Unknown"
	}

	// Count trend types
	bullish := 0
	bearish := 0
	sideways := 0

	for _, trend := range trends {
		switch trend {
		case "bullish":
			bullish++
		case "bearish":
			bearish++
		case "sideways":
			sideways++
		}
	}

	total := len(trends)
	if bullish == total {
		return "Fully Aligned Bullish"
	} else if bearish == total {
		return "Fully Aligned Bearish"
	} else if bullish > total/2 {
		return "Mostly Bullish"
	} else if bearish > total/2 {
		return "Mostly Bearish"
	} else {
		return "Mixed/Neutral"
	}
}

// getTradingSession returns the current trading session
func (pb *PromptBuilder) getTradingSession() string {
	hour := time.Now().UTC().Hour()

	switch {
	case hour >= 0 && hour < 9:
		return "Asian Session"
	case hour >= 7 && hour < 16:
		return "European Session"
	case hour >= 13 && hour < 22:
		return "US Session"
	default:
		return "Off-hours"
	}
}

// getMarketPhase returns the current market phase
func (pb *PromptBuilder) getMarketPhase(ctx *ai.MarketContext) string {
	primaryCtx := ctx.GetPrimaryContext()
	if primaryCtx == nil {
		return "Unknown"
	}

	// Simple phase detection based on trend and RSI
	rsi := primaryCtx.Indicators.RSI
	trend := primaryCtx.Trend

	if trend == "bullish" && rsi > 60 {
		return "Trending Up"
	} else if trend == "bearish" && rsi < 40 {
		return "Trending Down"
	} else if rsi > 40 && rsi < 60 {
		return "Ranging/Consolidation"
	} else {
		return "Transitional"
	}
}
