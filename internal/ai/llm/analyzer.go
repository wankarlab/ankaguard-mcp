package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/config"
	dynamic "github.com/rantipay/trading-mcp/internal/config/dynamic"
	"github.com/rantipay/trading-mcp/internal/domain"
	"github.com/rantipay/trading-mcp/internal/repositories"
	"github.com/shopspring/decimal"
	"github.com/sirupsen/logrus"
)

// jsonStdUnmarshal aliases the stdlib unmarshaller so the small indirection
// in parseExitDecisionJSON keeps the import block consistent.
var jsonStdUnmarshal = json.Unmarshal

// CostManager interface for budget checking and tracking
type CostManager interface {
	CheckBudget(ctx context.Context, provider string) (bool, error)
	TrackAPICall(ctx context.Context, provider string, cost float64, inputTokens, outputTokens int) error
}

// GetProviderManager returns the underlying multi-provider manager so external
// components (e.g. ConsensusEngine) can build features on top of the same
// configured providers. May return nil if multi-provider mode is disabled.
func (a *Analyzer) GetProviderManager() *ProviderManager {
	return a.providerManager
}

// Analyzer orchestrates LLM providers with caching and fallback
type Analyzer struct {
	primaryProvider  Provider
	fallbackProvider Provider
	providerManager  *ProviderManager // Multi-provider manager with auto-fallback
	cache            Cache
	costRepo         repositories.APICostRepository
	costManager      CostManager
	config           *config.AIConfig
	log              *logrus.Logger
	dailyCallCount   int
	lastResetDate    time.Time
	useMultiProvider bool // Flag to use multi-provider mode
}

// NewAnalyzer creates a new LLM analyzer
func NewAnalyzer(
	cfg *config.AIConfig,
	costRepo repositories.APICostRepository,
	log *logrus.Logger,
) (*Analyzer, error) {
	if log == nil {
		log = logrus.New()
	}

	analyzer := &Analyzer{
		cache:         NewMemoryCache(),
		costRepo:      costRepo,
		config:        cfg,
		log:           log,
		lastResetDate: time.Now(),
	}

	// Try to initialize multi-provider mode first (from environment variables)
	multiProviderConfig := NewMultiProviderConfig()
	if len(multiProviderConfig.Providers) > 0 {
		providerManager, err := NewProviderManager(multiProviderConfig, log)
		if err != nil {
			log.WithError(err).Warn("Failed to create provider manager, falling back to legacy mode")
		} else {
			analyzer.providerManager = providerManager
			analyzer.useMultiProvider = true

			// Set primary provider for compatibility
			primaryProvider, pt := providerManager.GetPrimaryProvider()
			if primaryProvider != nil {
				analyzer.primaryProvider = primaryProvider
				log.WithFields(logrus.Fields{
					"mode":             "multi-provider",
					"primary_provider": pt,
					"total_providers":  len(providerManager.GetAvailableProviders()),
				}).Info("LLM Analyzer initialized with multi-provider support")
				return analyzer, nil
			}
		}
	}

	// Fall back to legacy single-provider mode
	log.Info("Using legacy single-provider mode")

	// Create primary provider based on configuration
	primaryConfig := &ProviderConfig{
		Name:           cfg.LLM.Provider,
		APIKey:         cfg.LLM.APIKey,
		Model:          cfg.LLM.Model,
		TimeoutSeconds: cfg.LLM.TimeoutSeconds,
		MaxRetries:     3,
		RetryDelay:     1 * time.Second,
		Temperature:    cfg.LLM.Temperature,
		MaxTokens:      cfg.LLM.MaxTokens,
	}

	var primaryProvider Provider
	switch cfg.LLM.Provider {
	case "google":
		primaryProvider = NewGeminiProvider(primaryConfig)
	case "openai":
		primaryProvider = NewOpenAIProvider(primaryConfig)
	case "anthropic":
		primaryProvider = NewAnthropicProvider(primaryConfig)
	case "openrouter":
		primaryProvider = NewOpenRouterProvider(primaryConfig)
	case "groq":
		primaryProvider = NewGroqProvider(primaryConfig)
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s (supported: google, openai, anthropic, openrouter, groq)", cfg.LLM.Provider)
	}

	analyzer.primaryProvider = primaryProvider

	// Create fallback provider
	var fallbackProvider Provider
	if cfg.LLM.FallbackProvider != "" {
		fallbackConfig := &ProviderConfig{
			Name:           cfg.LLM.FallbackProvider,
			APIKey:         cfg.LLM.APIKey, // Use same key or configure separately
			Model:          getModelForProvider(cfg.LLM.FallbackProvider),
			TimeoutSeconds: cfg.LLM.TimeoutSeconds,
			MaxRetries:     3,
			RetryDelay:     1 * time.Second,
			Temperature:    cfg.LLM.Temperature,
			MaxTokens:      cfg.LLM.MaxTokens,
		}

		switch cfg.LLM.FallbackProvider {
		case "google":
			fallbackProvider = NewGeminiProvider(fallbackConfig)
		case "openai":
			fallbackProvider = NewOpenAIProvider(fallbackConfig)
		case "anthropic":
			fallbackProvider = NewAnthropicProvider(fallbackConfig)
		case "openrouter":
			fallbackProvider = NewOpenRouterProvider(fallbackConfig)
		case "groq":
			fallbackProvider = NewGroqProvider(fallbackConfig)
		}
	}

	analyzer.fallbackProvider = fallbackProvider

	log.WithFields(logrus.Fields{
		"mode":              "legacy",
		"primary_provider":  cfg.LLM.Provider,
		"fallback_provider": cfg.LLM.FallbackProvider,
	}).Info("LLM Analyzer initialized in legacy mode")

	return analyzer, nil
}

// NewAnalyzerWithMultiProvider creates an analyzer with multi-provider support
func NewAnalyzerWithMultiProvider(
	cfg *config.AIConfig,
	costRepo repositories.APICostRepository,
	log *logrus.Logger,
) (*Analyzer, error) {
	if log == nil {
		log = logrus.New()
	}

	// Create multi-provider config from environment
	multiProviderConfig := NewMultiProviderConfig()
	if len(multiProviderConfig.Providers) == 0 {
		return nil, fmt.Errorf("no LLM providers configured - set API keys in environment")
	}

	providerManager, err := NewProviderManager(multiProviderConfig, log)
	if err != nil {
		return nil, fmt.Errorf("failed to create provider manager: %w", err)
	}

	primaryProvider, _ := providerManager.GetPrimaryProvider()

	return &Analyzer{
		primaryProvider:  primaryProvider,
		providerManager:  providerManager,
		cache:            NewMemoryCache(),
		costRepo:         costRepo,
		config:           cfg,
		log:              log,
		lastResetDate:    time.Now(),
		useMultiProvider: true,
	}, nil
}

// getModelForProvider returns a default model for a given provider
func getModelForProvider(provider string) string {
	switch provider {
	case "google":
		return "gemini-2.0-flash"
	case "openai":
		return "gpt-4o-mini"
	case "anthropic":
		return "claude-3-haiku-20240307"
	case "openrouter":
		// google/gemini-2.0-flash-001 returns 404 with current keys; use verified model.
		return "meta-llama/llama-3.3-70b-instruct"
	case "groq":
		return "llama-3.1-70b-versatile"
	default:
		return ""
	}
}

// AnalyzeMarket analyzes market context using LLM with caching and fallback
func (a *Analyzer) AnalyzeMarket(ctx context.Context, marketCtx *ai.MarketContext) (*LLMAnalysis, error) {
	if !a.config.LLM.Enabled {
		return nil, fmt.Errorf("LLM is disabled")
	}

	// Check daily call limit
	if err := a.checkDailyLimit(); err != nil {
		a.log.WithError(err).Warn("Daily call limit reached, using fallback")
		if a.config.LLM.FallbackToTechnical {
			return a.technicalAnalysisFallback(marketCtx), nil
		}
		return nil, err
	}

	// Check budget if cost manager is configured
	if a.costManager != nil {
		allowed, err := a.costManager.CheckBudget(ctx, a.primaryProvider.Name())
		if err != nil {
			a.log.WithError(err).Warn("Failed to check budget")
		} else if !allowed {
			a.log.WithField("provider", a.primaryProvider.Name()).Warn("💰 Budget limit exceeded, using fallback")
			if a.config.LLM.FallbackToTechnical {
				return a.technicalAnalysisFallback(marketCtx), nil
			}
			return nil, fmt.Errorf("budget limit exceeded for provider: %s", a.primaryProvider.Name())
		}
	}

	// Build prompt
	prompt := MarketAnalysisPrompt(marketCtx)

	// Log prompt details
	promptTokens := len(prompt) / 4 // Approximation: 1 token ≈ 4 characters
	a.log.WithFields(map[string]interface{}{
		"prompt_length":    len(prompt),
		"estimated_tokens": promptTokens,
		"provider":         a.primaryProvider.Name(),
		"model":            a.config.LLM.Model,
	}).Debug("🤖 Calling LLM with prompt")

	// Try cache first
	cacheKey := BuildCacheKey(
		a.primaryProvider.Name(),
		a.config.LLM.Model,
		prompt,
		a.config.LLM.Temperature,
	)

	if cachedResponse, found := a.cache.Get(cacheKey); found {
		a.log.Debug("✅ Cache hit for market analysis")
		return ParseMarketAnalysis(cachedResponse)
	}

	// Build request
	request := &LLMRequest{
		Prompt:      prompt,
		Temperature: a.config.LLM.Temperature,
		MaxTokens:   a.config.LLM.MaxTokens,
		Model:       a.config.LLM.Model,
	}

	// Try primary provider
	response, err := a.callWithFallback(ctx, request)
	if err != nil {
		a.log.WithError(err).WithFields(map[string]interface{}{
			"provider":      a.primaryProvider.Name(),
			"prompt_tokens": promptTokens,
		}).Error("❌ All LLM providers failed")

		if a.config.LLM.FallbackToTechnical {
			a.log.Info("⚠️ Falling back to technical analysis")
			return a.technicalAnalysisFallback(marketCtx), nil
		}

		return nil, err
	}

	// Log success
	a.log.WithFields(map[string]interface{}{
		"provider":      response.Provider,
		"input_tokens":  response.InputTokens,
		"output_tokens": response.OutputTokens,
		"cost":          fmt.Sprintf("$%.6f", response.Cost),
		"latency":       response.Latency,
		"cached":        response.Cached,
	}).Info("✅ LLM analysis successful")

	// Cache the response
	cacheTTL := time.Duration(a.config.LLM.CacheTTLMinutes) * time.Minute
	a.cache.Set(cacheKey, response, cacheTTL)

	// Track cost
	if a.costRepo != nil {
		a.trackCost(ctx, response)
	}

	// Increment daily call count
	a.dailyCallCount++

	// Parse and return analysis
	return ParseMarketAnalysis(response)
}

// callWithFallback tries primary provider, then fallback if it fails
func (a *Analyzer) callWithFallback(ctx context.Context, request *LLMRequest) (*LLMResponse, error) {
	// Use multi-provider mode if available
	if a.useMultiProvider && a.providerManager != nil {
		response, err := a.providerManager.Call(ctx, request)
		if err == nil {
			return response, nil
		}
		a.log.WithError(err).Error("All providers in multi-provider chain failed")
		return nil, err
	}

	// Legacy mode: Try primary provider
	response, err := a.primaryProvider.Call(ctx, request)
	if err == nil {
		return response, nil
	}

	a.log.WithError(err).WithField("provider", a.primaryProvider.Name()).Warn("Primary provider failed")

	// Try fallback provider if available
	if a.fallbackProvider != nil {
		a.log.WithField("provider", a.fallbackProvider.Name()).Info("Trying fallback provider")

		response, err = a.fallbackProvider.Call(ctx, request)
		if err == nil {
			return response, nil
		}

		a.log.WithError(err).WithField("provider", a.fallbackProvider.Name()).Error("Fallback provider also failed")
	}

	return nil, fmt.Errorf("all providers failed: %w", err)
}

// checkDailyLimit checks if daily call limit has been reached
func (a *Analyzer) checkDailyLimit() error {
	// Reset counter if it's a new day
	now := time.Now()
	if now.Day() != a.lastResetDate.Day() {
		a.dailyCallCount = 0
		a.lastResetDate = now
	}

	if a.dailyCallCount >= a.config.LLM.MaxCallsPerDay {
		return fmt.Errorf("daily call limit reached: %d/%d", a.dailyCallCount, a.config.LLM.MaxCallsPerDay)
	}

	return nil
}

// trackCost records the cost of an LLM call
func (a *Analyzer) trackCost(ctx context.Context, response *LLMResponse) {
	if response == nil {
		return
	}

	// Use cost manager if available (preferred method)
	if a.costManager != nil {
		if err := a.costManager.TrackAPICall(ctx, response.Provider, response.Cost, response.InputTokens, response.OutputTokens); err != nil {
			a.log.WithError(err).Warn("Failed to track API cost via cost manager")
		}
		return
	}

	// Fallback to direct repository access
	if a.costRepo != nil {
		cost := &domain.APICost{
			Provider:     response.Provider,
			Cost:         decimal.NewFromFloat(response.Cost),
			CallType:     "market_analysis",
			InputTokens:  response.InputTokens,
			OutputTokens: response.OutputTokens,
			Model:        response.Model,
			Success:      true,
			CreatedAt:    time.Now(),
		}

		if err := a.costRepo.Create(ctx, cost); err != nil {
			a.log.WithError(err).Warn("Failed to track API cost")
		}
	}
}

// technicalAnalysisFallback provides a basic analysis using only technical indicators
func (a *Analyzer) technicalAnalysisFallback(marketCtx *ai.MarketContext) *LLMAnalysis {
	// Load fallback thresholds from dynamic config — all hot-reloadable.
	// Manifest keys: llm.fallback.technical_buy_rsi_primary (30),
	//   llm.fallback.technical_buy_rsi_secondary (40),
	//   llm.fallback.technical_sell_rsi_primary (70),
	//   llm.fallback.technical_sell_rsi_secondary (60),
	//   llm.fallback.base_confidence (0.65),
	//   llm.fallback.aligned_bonus (0.1)
	buyRSIPrimary := 30.0
	buyRSISecondary := 40.0
	sellRSIPrimary := 70.0
	sellRSISecondary := 60.0
	baseConfidence := 0.65
	alignedBonus := 0.1
	if snap := dynamic.SnapshotFromContext(context.Background()); snap != nil {
		if v := snap.Float("llm.fallback.technical_buy_rsi_primary"); v > 0 {
			buyRSIPrimary = v
		}
		if v := snap.Float("llm.fallback.technical_buy_rsi_secondary"); v > 0 {
			buyRSISecondary = v
		}
		if v := snap.Float("llm.fallback.technical_sell_rsi_primary"); v > 0 {
			sellRSIPrimary = v
		}
		if v := snap.Float("llm.fallback.technical_sell_rsi_secondary"); v > 0 {
			sellRSISecondary = v
		}
		if v := snap.Float("llm.fallback.base_confidence"); v > 0 {
			baseConfidence = v
		}
		if v := snap.Float("llm.fallback.aligned_bonus"); v > 0 {
			alignedBonus = v
		}
	}

	// Simple rule-based analysis
	action := "WAIT"
	confidence := 0.5
	reasoning := "Using technical analysis fallback. "

	// Get primary and secondary contexts
	primaryCtx := marketCtx.GetPrimaryContext()
	secondaryCtxs := marketCtx.GetSecondaryContexts()

	if primaryCtx == nil {
		return &LLMAnalysis{
			Action:          "WAIT",
			Confidence:      0.0,
			Reasoning:       "No market context available",
			EntryPrice:      marketCtx.CurrentPrice,
			StopLoss:        marketCtx.CurrentPrice,
			TakeProfit:      marketCtx.CurrentPrice,
			RiskAssessment:  "high",
			MarketCondition: "unknown",
			KeyFactors:      []string{"No data"},
			Warnings:        []string{"LLM unavailable", "No market context"},
			Timestamp:       time.Now(),
		}
	}

	// Check RSI
	rsiPrimary := primaryCtx.Indicators.RSI
	rsiSecondary := 50.0
	if len(secondaryCtxs) > 0 {
		rsiSecondary = secondaryCtxs[0].Indicators.RSI
	}

	if rsiPrimary < buyRSIPrimary && rsiSecondary < buyRSISecondary {
		action = "BUY"
		confidence = baseConfidence
		reasoning += "RSI oversold on both timeframes. "
	} else if rsiPrimary > sellRSIPrimary && rsiSecondary > sellRSISecondary {
		action = "SELL"
		confidence = baseConfidence
		reasoning += "RSI overbought on both timeframes. "
	}

	// Check trend alignment
	if len(secondaryCtxs) > 0 && primaryCtx.Trend == secondaryCtxs[0].Trend {
		confidence += alignedBonus
		reasoning += fmt.Sprintf("Trends aligned (%s). ", primaryCtx.Trend)
	}

	// Calculate prices
	currentPrice := marketCtx.CurrentPrice
	stopLoss := currentPrice * 0.997
	takeProfit := currentPrice * 1.005

	if action == "SELL" {
		stopLoss = currentPrice * 1.003
		takeProfit = currentPrice * 0.995
	}

	marketCondition := "unknown"
	if len(secondaryCtxs) > 0 {
		marketCondition = fmt.Sprintf("%s trend", secondaryCtxs[0].Trend)
	}

	return &LLMAnalysis{
		Action:          action,
		Confidence:      confidence,
		Reasoning:       reasoning + "LLM unavailable, using technical indicators only.",
		EntryPrice:      currentPrice,
		StopLoss:        stopLoss,
		TakeProfit:      takeProfit,
		RiskAssessment:  "medium",
		MarketCondition: marketCondition,
		KeyFactors:      []string{"RSI", "Trend alignment", "Technical fallback"},
		Warnings:        []string{"LLM analysis unavailable"},
		Timestamp:       time.Now(),
	}
}

// ExplainTrade generates an explanation for a trade using LLM
func (a *Analyzer) ExplainTrade(ctx context.Context, trade interface{}, marketCtx *ai.MarketContext) (*TradeExplanation, error) {
	if !a.config.LLM.Enabled {
		return nil, fmt.Errorf("LLM is disabled")
	}

	// Build prompt for trade explanation
	prompt := fmt.Sprintf(`Explain this trading decision in simple terms:

Trade Action: %v
Market Context: %v

Provide a brief explanation covering:
1. Why this decision was made
2. What the key indicators showed
3. What the expected outcome is

Keep it concise and actionable.`, trade, marketCtx)

	request := &LLMRequest{
		Prompt:      prompt,
		Temperature: 0.7,
		MaxTokens:   500,
		Model:       a.config.LLM.Model,
	}

	response, err := a.callWithFallback(ctx, request)
	if err != nil {
		return nil, err
	}

	// Track cost
	if a.costRepo != nil {
		a.trackCost(ctx, response)
	}

	return &TradeExplanation{
		Summary:   "Trade explanation",
		Analysis:  response.Content,
		Reasoning: "Generated by LLM",
		Response:  response,
		Timestamp: time.Now(),
	}, nil
}

// ExitDecisionInput bundles the position state passed to AnalyzeExitDecision.
type ExitDecisionInput struct {
	Pair              string
	Direction         string // "buy"/"long" or "sell"/"short"
	EntryPrice        float64
	CurrentPrice      float64
	GainPct           float64 // signed % move in position's favor; negative if losing
	TakeProfit        float64
	StopLoss          float64
	MaxFavorablePrice float64 // best price seen in our favor; 0 if unknown
	AgeSeconds        int64
	RSI               float64
	MACD              float64
	MACDSignal        float64

	// Candle wick fields — populated from the last 5 closed candles.
	// These enable the LLM to reason about price rejection and exhaustion.
	WickPattern string    // "buyer_absorption" | "seller_absorption" | "doji_cluster" | "none"
	LowerWicks  []float64 // lower wick per candle, oldest→newest (last 5)
	UpperWicks  []float64 // upper wick per candle, oldest→newest (last 5)
	Bodies      []float64 // |close-open| per candle
	Volumes     []float64 // volume per candle
}

// ExitDecisionOutput is the parsed AI response for an open position review.
type ExitDecisionOutput struct {
	Action     string // HOLD | TIGHTEN_STOP | CLOSE_PARTIAL | CLOSE_ALL
	Confidence float64
	Reasoning  string
	Raw        string
}

// AnalyzeExitDecision asks the LLM whether an open position should be exited
// early, partially closed, or have its stop tightened. Designed for the
// "human-like profit taking" feature — called periodically while a position
// is in profit. Returns ("HOLD", ...) on any parse failure so the caller
// keeps holding rather than mis-acting on garbage.
func (a *Analyzer) AnalyzeExitDecision(ctx context.Context, in *ExitDecisionInput) (*ExitDecisionOutput, error) {
	if !a.config.LLM.Enabled {
		return nil, fmt.Errorf("LLM is disabled")
	}
	if in == nil {
		return nil, fmt.Errorf("nil input")
	}

	// Build candle wick section for the prompt.
	wickSection := ""
	if in.WickPattern != "" && in.WickPattern != "none" && len(in.LowerWicks) >= 3 {
		n := len(in.LowerWicks)
		wickSection = fmt.Sprintf(`
Candle wick analysis (last %d candles, oldest→newest):
`, n)
		for i := 0; i < n; i++ {
			vol := 0.0
			if i < len(in.Volumes) {
				vol = in.Volumes[i]
			}
			body := 0.0
			if i < len(in.Bodies) {
				body = in.Bodies[i]
			}
			wickSection += fmt.Sprintf("  [%d] body=%.4f  lower_wick=%.4f  upper_wick=%.4f  vol=%.1f\n",
				i+1, body, in.LowerWicks[i], in.UpperWicks[i], vol)
		}
		wickSection += fmt.Sprintf("Pattern detected: %s\n", in.WickPattern)
		switch in.WickPattern {
		case "buyer_absorption":
			wickSection += "  → Growing lower wicks = buyers absorbing the drop. " +
				"For SHORT: price rejection building at lows — reversal risk is HIGH.\n"
		case "seller_absorption":
			wickSection += "  → Growing upper wicks = sellers absorbing the rally. " +
				"For LONG: price rejection building at highs — reversal risk is HIGH.\n"
		case "doji_cluster":
			wickSection += "  → Consecutive small-body candles = momentum exhaustion. " +
				"Trend losing conviction — consider locking profits.\n"
		}
	} else {
		wickSection = "\nCandle wick analysis: no significant wick pattern detected.\n"
	}

	prompt := fmt.Sprintf(`You are an exit-management AI for a crypto futures scalping bot.
Current open position:
- Pair: %s
- Direction: %s
- Entry: %.8f | Current: %.8f | Gain: %.2f%%
- TP target: %.8f | SL: %.8f
- Best favorable price seen: %.8f
- Age (s): %d
- RSI: %.2f | MACD: %.6f | Signal: %.6f
%s
RSI interpretation (direction-aware):
- For LONG: low RSI (<35) = bullish, hold; high RSI (>70) = overbought, protect gains
- For SHORT: high RSI (>65) = bearish, hold; low RSI (<30) = oversold, BUT check wick pattern — if buyer_absorption is present, close immediately despite low RSI

WICK PATTERN RULES (override RSI when pattern is strong):
- buyer_absorption on SHORT → CLOSE_ALL or CLOSE_PARTIAL regardless of RSI
- seller_absorption on LONG → CLOSE_ALL or CLOSE_PARTIAL regardless of RSI
- doji_cluster with gain > 1.5%% → CLOSE_PARTIAL to lock profits

TIGHTEN_STOP safety rule: only use if SL buffer > 0.5%% from current price.

Decide:
- HOLD: thesis intact, no wick reversal signal
- TIGHTEN_STOP: reversal signal with sufficient SL buffer
- CLOSE_PARTIAL: partial reversal — lock 50%% of remaining size
- CLOSE_ALL: wick reversal confirmed or exhaustion — exit now

Respond ONLY with JSON: {"action":"HOLD|TIGHTEN_STOP|CLOSE_PARTIAL|CLOSE_ALL","confidence":<0.0-1.0>,"reasoning":"<one sentence>"}`,
		in.Pair, in.Direction,
		in.EntryPrice, in.CurrentPrice, in.GainPct,
		in.TakeProfit, in.StopLoss, in.MaxFavorablePrice,
		in.AgeSeconds,
		in.RSI, in.MACD, in.MACDSignal,
		wickSection,
	)

	req := &LLMRequest{
		Prompt:      prompt,
		Temperature: 0.2,
		MaxTokens:   150,
		Model:       a.config.LLM.Model,
	}

	resp, err := a.callWithFallback(ctx, req)
	if err != nil || resp == nil {
		return nil, err
	}
	if a.costRepo != nil {
		a.trackCost(ctx, resp)
	}

	out := parseExitDecisionJSON(resp.Content)
	out.Raw = resp.Content
	return out, nil
}

// GetCacheStats returns cache statistics
func (a *Analyzer) GetCacheStats() *CacheStats {
	return a.cache.GetStats()
}

// GetProviderStats returns statistics for all providers
func (a *Analyzer) GetProviderStats() map[string]*ProviderStats {
	// Use multi-provider mode if available
	if a.useMultiProvider && a.providerManager != nil {
		return a.providerManager.GetProviderStats()
	}

	// Legacy mode
	stats := make(map[string]*ProviderStats)

	if a.primaryProvider != nil {
		stats[a.primaryProvider.Name()] = a.primaryProvider.GetStats()
	}

	if a.fallbackProvider != nil {
		stats[a.fallbackProvider.Name()] = a.fallbackProvider.GetStats()
	}

	return stats
}

// GetProviderHealthStatus returns health status for all providers
func (a *Analyzer) GetProviderHealthStatus() map[string]interface{} {
	if a.useMultiProvider && a.providerManager != nil {
		healthStatus := a.providerManager.GetHealthStatus()
		result := make(map[string]interface{})
		for pt, status := range healthStatus {
			result[string(pt)] = map[string]interface{}{
				"is_healthy":        status.IsHealthy,
				"last_check":        status.LastCheck,
				"last_success":      status.LastSuccess,
				"consecutive_fails": status.ConsecutiveFails,
				"total_calls":       status.TotalCalls,
				"successful_calls":  status.SuccessfulCalls,
				"avg_latency_ms":    status.AvgLatency.Milliseconds(),
			}
			if status.LastError != nil {
				result[string(pt)].(map[string]interface{})["last_error"] = status.LastError.Error()
			}
		}
		return result
	}

	// Legacy mode - simple health status
	result := make(map[string]interface{})
	if a.primaryProvider != nil {
		stats := a.primaryProvider.GetStats()
		successRate := float64(0)
		if stats.TotalCalls > 0 {
			successRate = float64(stats.SuccessfulCalls) / float64(stats.TotalCalls)
		}
		result[a.primaryProvider.Name()] = map[string]interface{}{
			"is_healthy":       successRate >= 0.5 || stats.TotalCalls == 0,
			"total_calls":      stats.TotalCalls,
			"successful_calls": stats.SuccessfulCalls,
			"success_rate":     successRate,
		}
	}
	return result
}

// GetAvailableProviders returns list of configured providers
func (a *Analyzer) GetAvailableProviders() []string {
	if a.useMultiProvider && a.providerManager != nil {
		providers := a.providerManager.GetAvailableProviders()
		result := make([]string, len(providers))
		for i, pt := range providers {
			result[i] = string(pt)
		}
		return result
	}

	// Legacy mode
	providers := []string{}
	if a.primaryProvider != nil {
		providers = append(providers, a.primaryProvider.Name())
	}
	if a.fallbackProvider != nil {
		providers = append(providers, a.fallbackProvider.Name())
	}
	return providers
}

// IsMultiProviderMode returns whether multi-provider mode is active
func (a *Analyzer) IsMultiProviderMode() bool {
	return a.useMultiProvider
}

// HealthCheck performs a health check on all providers
func (a *Analyzer) HealthCheck(ctx context.Context) map[string]bool {
	if a.useMultiProvider && a.providerManager != nil {
		healthResults := a.providerManager.HealthCheck(ctx)
		result := make(map[string]bool)
		for pt, healthy := range healthResults {
			result[string(pt)] = healthy
		}
		return result
	}

	// Legacy mode - check circuit breaker status
	result := make(map[string]bool)
	if a.primaryProvider != nil {
		result[a.primaryProvider.Name()] = a.primaryProvider.IsAvailable(ctx)
	}
	if a.fallbackProvider != nil {
		result[a.fallbackProvider.Name()] = a.fallbackProvider.IsAvailable(ctx)
	}
	return result
}

// GetDailyCallCount returns the current daily call count
func (a *Analyzer) GetDailyCallCount() int {
	return a.dailyCallCount
}

// ClearCache clears the response cache
func (a *Analyzer) ClearCache() {
	a.cache.Clear()
	a.log.Info("Cache cleared")
}

// SetCostManager sets the cost manager for budget tracking
func (a *Analyzer) SetCostManager(costManager CostManager) {
	a.costManager = costManager
	a.log.Info("Cost manager configured")
}

// GetPrimaryProvider returns the primary LLM provider
func (a *Analyzer) GetPrimaryProvider() Provider {
	return a.primaryProvider
}

// parseExitDecisionJSON extracts the JSON object from an LLM reply and parses
// it. LLMs often wrap JSON in markdown fences or add prose; we slice between
// the first '{' and last '}' to be tolerant. On any failure returns a safe
// HOLD with zero confidence so the caller keeps the position open.
func parseExitDecisionJSON(raw string) *ExitDecisionOutput {
	out := &ExitDecisionOutput{Action: "HOLD"}
	if raw == "" {
		return out
	}
	start := -1
	end := -1
	for i := 0; i < len(raw); i++ {
		if raw[i] == '{' {
			start = i
			break
		}
	}
	for i := len(raw) - 1; i >= 0; i-- {
		if raw[i] == '}' {
			end = i
			break
		}
	}
	if start < 0 || end <= start {
		return out
	}
	body := raw[start : end+1]

	var parsed struct {
		Action     string  `json:"action"`
		Confidence float64 `json:"confidence"`
		Reasoning  string  `json:"reasoning"`
	}
	if err := jsonUnmarshal([]byte(body), &parsed); err != nil {
		return out
	}

	action := strings.ToUpper(strings.TrimSpace(parsed.Action))
	switch action {
	case "HOLD", "TIGHTEN_STOP", "CLOSE_PARTIAL", "CLOSE_ALL":
		out.Action = action
	default:
		out.Action = "HOLD"
	}
	out.Confidence = parsed.Confidence
	out.Reasoning = parsed.Reasoning
	return out
}

// jsonUnmarshal is a small indirection so the import block stays compact.
// Kept private since it's only used here.
func jsonUnmarshal(data []byte, v interface{}) error {
	return jsonStdUnmarshal(data, v)
}
