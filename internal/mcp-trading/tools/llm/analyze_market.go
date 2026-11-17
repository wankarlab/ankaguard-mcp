package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/sirupsen/logrus"
)

// AnalyzeMarketTool provides LLM-based market analysis via MCP
type AnalyzeMarketTool struct {
	analyzer marketAnalyzer
	log      *logrus.Logger
}

// marketAnalyzer is the LLM backend; *llm.Analyzer implements it.
type marketAnalyzer interface {
	AnalyzeMarket(ctx context.Context, marketCtx *ai.MarketContext) (*llm.LLMAnalysis, error)
}

// NewAnalyzeMarketTool creates a new LLM analyze market tool
func NewAnalyzeMarketTool(
	cfg *config.AIConfig,
	log *logrus.Logger,
) (*AnalyzeMarketTool, error) {
	// Without a configured provider the tool stays registered but every call
	// answers DATA_UNAVAILABLE; it never returns a canned analysis.
	if cfg == nil {
		log.Warn("AI config not available - llm_analyze_market will answer DATA_UNAVAILABLE")
		return &AnalyzeMarketTool{log: log}, nil
	}

	// Create analyzer without cost repository (optional)
	analyzer, err := llm.NewAnalyzer(cfg, nil, log)
	if err != nil {
		log.WithError(err).Warn("Failed to create LLM analyzer - llm_analyze_market will answer DATA_UNAVAILABLE")
		return &AnalyzeMarketTool{log: log}, nil
	}

	return &AnalyzeMarketTool{
		analyzer: analyzer,
		log:      log,
	}, nil
}

// Name returns the tool name
func (t *AnalyzeMarketTool) Name() string {
	return "llm_analyze_market"
}

// Description returns the tool description
func (t *AnalyzeMarketTool) Description() string {
	return "Analyze market conditions using LLM (AI) and provide trading recommendations. Supports multiple providers: Gemini, OpenAI, Anthropic, OpenRouter."
}

// InputSchema returns the JSON schema for the tool's input
func (t *AnalyzeMarketTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., BTC-USDT, DOGE-USDT)",
			},
			"current_price": map[string]interface{}{
				"type":        "number",
				"description": "Current price of the asset",
			},
			"primary_timeframe": map[string]interface{}{
				"type":        "object",
				"description": "Primary timeframe context",
				"properties": map[string]interface{}{
					"timeframe": map[string]interface{}{
						"type":        "string",
						"description": "Timeframe (e.g., 1m, 5m, 15m, 1h)",
					},
					"trend": map[string]interface{}{
						"type":        "string",
						"description": "Trend direction (bullish, bearish, neutral)",
					},
					"rsi": map[string]interface{}{
						"type":        "number",
						"description": "RSI indicator value",
					},
					"macd_histogram": map[string]interface{}{
						"type":        "number",
						"description": "MACD histogram value",
					},
				},
			},
			"secondary_timeframes": map[string]interface{}{
				"type":        "array",
				"description": "Additional timeframe contexts for multi-timeframe analysis",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"timeframe": map[string]interface{}{
							"type": "string",
						},
						"trend": map[string]interface{}{
							"type": "string",
						},
						"rsi": map[string]interface{}{
							"type": "number",
						},
					},
				},
			},
		},
		"required": []string{"pair", "current_price", "primary_timeframe"},
	}
}

// Call executes the tool
func (t *AnalyzeMarketTool) Call(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Parse input arguments
	pair, ok := args["pair"].(string)
	if !ok {
		return nil, fmt.Errorf("pair must be a string")
	}

	currentPrice, ok := args["current_price"].(float64)
	if !ok {
		return nil, fmt.Errorf("current_price must be a number")
	}

	if t.analyzer == nil {
		return nil, common.DataUnavailable("the AI provider is not configured or could not be created", nil)
	}

	// Build market context
	marketCtx := &ai.MarketContext{
		Pair:         pair,
		CurrentPrice: currentPrice,
		Timeframes:   make(map[string]*ai.TimeframeContext),
	}

	// Parse primary timeframe
	if primaryTF, ok := args["primary_timeframe"].(map[string]interface{}); ok {
		tfCtx := parseTimeframeContext(primaryTF)
		if tfCtx.Timeframe != "" {
			marketCtx.Timeframes[tfCtx.Timeframe] = tfCtx
		}
	}

	// Parse secondary timeframes
	if secondaryTFs, ok := args["secondary_timeframes"].([]interface{}); ok {
		for _, tf := range secondaryTFs {
			if tfMap, ok := tf.(map[string]interface{}); ok {
				tfCtx := parseTimeframeContext(tfMap)
				if tfCtx.Timeframe != "" {
					marketCtx.Timeframes[tfCtx.Timeframe] = tfCtx
				}
			}
		}
	}

	// Call LLM analyzer
	analysis, err := t.analyzer.AnalyzeMarket(ctx, marketCtx)
	if err != nil {
		t.log.WithError(err).Warn("LLM analysis failed")
		return nil, common.DataUnavailable("the AI provider failed: "+err.Error(), nil)
	}

	// Convert analysis to response format
	response := map[string]interface{}{
		"action":           analysis.Action,
		"confidence":       analysis.Confidence,
		"reasoning":        analysis.Reasoning,
		"entry_price":      analysis.EntryPrice,
		"stop_loss":        analysis.StopLoss,
		"take_profit":      analysis.TakeProfit,
		"risk_assessment":  analysis.RiskAssessment,
		"market_condition": analysis.MarketCondition,
		"key_factors":      analysis.KeyFactors,
		"warnings":         analysis.Warnings,
		"timestamp":        analysis.Timestamp,
	}

	// Add provider info if available
	if analysis.Response != nil {
		response["provider"] = analysis.Response.Provider
		response["model"] = analysis.Response.Model
		response["cost"] = analysis.Response.Cost
		response["latency_ms"] = analysis.Response.Latency.Milliseconds()
		response["cached"] = analysis.Response.Cached
	}

	return response, nil
}

// parseTimeframeContext parses a timeframe context from map
func parseTimeframeContext(tfMap map[string]interface{}) *ai.TimeframeContext {
	tfCtx := &ai.TimeframeContext{
		Indicators: &ai.Indicators{},
	}

	if tf, ok := tfMap["timeframe"].(string); ok {
		tfCtx.Timeframe = tf
	}

	if trend, ok := tfMap["trend"].(string); ok {
		tfCtx.Trend = trend
	}

	if rsi, ok := tfMap["rsi"].(float64); ok {
		tfCtx.Indicators.RSI = rsi
	}

	if macd, ok := tfMap["macd_histogram"].(float64); ok {
		if tfCtx.Indicators.MACD == nil {
			tfCtx.Indicators.MACD = &ai.MACD{}
		}
		tfCtx.Indicators.MACD.Histogram = macd
	}

	return tfCtx
}

// SetCostManager sets the cost manager for budget tracking
func (t *AnalyzeMarketTool) SetCostManager(costManager llm.CostManager) {
	if a, ok := t.analyzer.(*llm.Analyzer); ok && a != nil {
		a.SetCostManager(costManager)
	}
}

// GetProviderStats returns statistics for all providers
func (t *AnalyzeMarketTool) GetProviderStats() map[string]*llm.ProviderStats {
	if a, ok := t.analyzer.(*llm.Analyzer); ok && a != nil {
		return a.GetProviderStats()
	}
	return nil
}

// Execute implements the common.Tool interface
func (t *AnalyzeMarketTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	return t.Call(ctx, params)
}

// MarshalJSON implements json.Marshaler for tool registration
func (t *AnalyzeMarketTool) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]interface{}{
		"name":        t.Name(),
		"description": t.Description(),
		"inputSchema": t.InputSchema(),
	})
}
