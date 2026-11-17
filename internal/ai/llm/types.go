package llm

import (
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
)

// LLMRequest represents a request to an LLM
type LLMRequest struct {
	Prompt      string
	Temperature float64
	MaxTokens   int
	Model       string
	Metadata    map[string]interface{}
}

// LLMResponse represents a response from an LLM
type LLMResponse struct {
	Content      string
	InputTokens  int
	OutputTokens int
	Model        string
	Provider     string
	Cost         float64
	Latency      time.Duration
	Cached       bool
	Timestamp    time.Time
}

// LLMAnalysis represents the result of LLM market analysis
type LLMAnalysis struct {
	Action          string                 // "BUY", "SELL", "WAIT"
	Confidence      float64                // 0.0 to 1.0
	Reasoning       string                 // Detailed explanation
	EntryPrice      float64                // Suggested entry price
	StopLoss        float64                // Suggested stop loss
	TakeProfit      float64                // Suggested take profit
	RiskAssessment  string                 // Risk evaluation
	MarketCondition string                 // Market condition description
	KeyFactors      []string               // Key factors in decision
	Warnings        []string               // Warnings or concerns
	Metadata        map[string]interface{} // Additional metadata
	Response        *LLMResponse           // Raw LLM response
	Timestamp       time.Time
}

// TradeExplanation represents an explanation of a trade
type TradeExplanation struct {
	Summary   string   // Brief summary
	Analysis  string   // Detailed analysis
	Reasoning string   // Why the decision was made
	Outcome   string   // Expected or actual outcome
	Lessons   []string // Lessons learned (for closed trades)
	Response  *LLMResponse
	Timestamp time.Time
}

// SentimentAnalysis represents sentiment analysis from text
type SentimentAnalysis struct {
	Score      float64  // -1.0 (bearish) to +1.0 (bullish)
	Confidence float64  // 0.0 to 1.0
	Trend      string   // "bullish", "bearish", "neutral"
	Keywords   []string // Key words/phrases identified
	Summary    string   // Summary of sentiment
	Response   *LLMResponse
	Timestamp  time.Time
}

// PromptTemplate represents a template for building prompts
type PromptTemplate struct {
	Name        string
	Template    string
	Variables   []string
	Description string
}

// CacheKey represents a key for caching LLM responses
type CacheKey struct {
	Provider    string
	Model       string
	PromptHash  string
	Temperature float64
}

// CacheEntry represents a cached LLM response
type CacheEntry struct {
	Key       CacheKey
	Response  *LLMResponse
	ExpiresAt time.Time
	CreatedAt time.Time
}

// ProviderConfig holds configuration for an LLM provider
type ProviderConfig struct {
	Name           string
	APIKey         string
	Model          string
	Endpoint       string
	TimeoutSeconds int
	MaxRetries     int
	RetryDelay     time.Duration
	Temperature    float64
	MaxTokens      int
	CostPerInputK  float64 // Cost per 1K input tokens
	CostPerOutputK float64 // Cost per 1K output tokens
}

// ProviderStats holds statistics for a provider
type ProviderStats struct {
	Provider        string
	TotalCalls      int64
	SuccessfulCalls int64
	FailedCalls     int64
	TotalCost       float64
	TotalTokens     int64
	AvgLatency      time.Duration
	LastCall        time.Time
	LastError       string
}

// getTimeframeInfo returns formatted timeframe info for prompts
func getTimeframeInfo(ctx *ai.MarketContext, index int) string {
	var tfCtx *ai.TimeframeContext

	if index == 0 {
		tfCtx = ctx.GetPrimaryContext()
	} else {
		secondaryCtxs := ctx.GetSecondaryContexts()
		if len(secondaryCtxs) > index-1 {
			tfCtx = secondaryCtxs[index-1]
		}
	}

	if tfCtx == nil {
		return ""
	}

	return tfCtx.Timeframe + `: RSI ` + formatFloat(tfCtx.Indicators.RSI) + `, ` + tfCtx.Trend
}

// MarketAnalysisPrompt builds a prompt for market analysis (optimized for <500 tokens)
func MarketAnalysisPrompt(ctx *ai.MarketContext) string {
	// Round price to 4 significant digits to improve cache hit rate for micro-movements
	return `Trade ` + ctx.Pair + ` $` + fmt.Sprintf("%.4g", ctx.CurrentPrice) + `

` + getTimeframeInfo(ctx, 0) + `
` + getTimeframeInfo(ctx, 1) + `

JSON:
{
  "action": "BUY|SELL|WAIT",
  "confidence": 0.0-1.0,
  "reasoning": "max 40 words",
  "entry_price": number,
  "stop_loss": number,
  "take_profit": number,
  "risk_assessment": "low|medium|high",
  "market_condition": "brief",
  "key_factors": ["f1","f2"],
  "warnings": ["w1"]
}

Conservative. Risk/reward focus.`
}

func formatFloat(f float64) string {
	// Round to 1 decimal place to improve cache hit rates on indicators
	return fmt.Sprintf("%.1f", f)
}
