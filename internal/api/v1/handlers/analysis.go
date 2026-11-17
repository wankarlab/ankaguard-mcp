// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/ai/llm"
	"github.com/rantipay/trading-mcp/internal/ai/sentiment"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
)

// AnalysisHandler handles AI analysis endpoints
type AnalysisHandler struct {
	gctClient         *gctclient.Client
	llmAnalyzer       *llm.Analyzer
	sentimentAnalyzer *sentiment.Analyzer
	backtester        *ai.Backtester
	tradeRepo         tradeExplainer // optional: nil skips /explain/:trade_id
	logger            *logrus.Logger
}

// tradeExplainer is the minimal interface to fetch a trade for explainability.
type tradeExplainer interface {
	GetByID(tradeID string) (*storage.Trade, error)
}

// AnalysisHandlerConfig holds the configuration for the analysis handler
type AnalysisHandlerConfig struct {
	GCTClient         *gctclient.Client
	LLMAnalyzer       *llm.Analyzer
	SentimentAnalyzer *sentiment.Analyzer
	Backtester        *ai.Backtester
	Logger            *logrus.Logger
}

// NewAnalysisHandler creates a new analysis handler
func NewAnalysisHandler(cfg *AnalysisHandlerConfig) *AnalysisHandler {
	logger := cfg.Logger
	if logger == nil {
		logger = logrus.New()
	}

	return &AnalysisHandler{
		gctClient:         cfg.GCTClient,
		llmAnalyzer:       cfg.LLMAnalyzer,
		sentimentAnalyzer: cfg.SentimentAnalyzer,
		backtester:        cfg.Backtester,
		logger:            logger,
	}
}

// GetTradingSignal handles POST /api/v1/analysis/signal
// Returns AI-generated trading signal for a given pair
func (h *AnalysisHandler) GetTradingSignal(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req models.TradingSignalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Invalid request body", err.Error()))
		return
	}

	// Normalize pair format
	req.Pair = normalizePairFormat(req.Pair)

	// Set default timeframe if not provided
	if req.Timeframe == "" {
		req.Timeframe = "15m"
	}

	// Set default timeframes if not provided
	if len(req.Timeframes) == 0 {
		req.Timeframes = []string{req.Timeframe, "1h"}
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":    userID,
		"exchange":   req.Exchange,
		"pair":       req.Pair,
		"timeframes": req.Timeframes,
	}).Info("Getting AI trading signal")

	// Check if LLM analyzer is available
	if h.llmAnalyzer == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("AI_UNAVAILABLE", "AI analysis service is not configured"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	// Build market context for analysis
	marketCtx, err := h.buildMarketContext(ctx, req.Exchange, req.Pair, req.Timeframes)
	if err != nil {
		h.logger.WithError(err).Error("Failed to build market context")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("CONTEXT_ERROR", "Failed to build market context", err.Error()))
		return
	}

	// Get AI analysis
	analysis, err := h.llmAnalyzer.AnalyzeMarket(ctx, marketCtx)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get AI analysis")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("ANALYSIS_ERROR", "Failed to get AI analysis", err.Error()))
		return
	}

	// Build response
	response := models.TradingSignalResponse{
		Pair:            req.Pair,
		Action:          analysis.Action,
		Confidence:      analysis.Confidence,
		Reasoning:       analysis.Reasoning,
		EntryPrice:      analysis.EntryPrice,
		StopLoss:        analysis.StopLoss,
		TakeProfit:      analysis.TakeProfit,
		RiskAssessment:  analysis.RiskAssessment,
		MarketCondition: analysis.MarketCondition,
		KeyFactors:      analysis.KeyFactors,
		Warnings:        analysis.Warnings,
		Provider:        h.getProviderName(),
		Timestamp:       time.Now(),
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":    userID,
		"pair":       req.Pair,
		"action":     response.Action,
		"confidence": response.Confidence,
	}).Info("AI trading signal generated")

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// GetSentiment handles POST /api/v1/analysis/sentiment
// Returns market sentiment analysis for a symbol
func (h *AnalysisHandler) GetSentiment(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req models.SentimentAnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Invalid request body", err.Error()))
		return
	}

	// Normalize symbol
	req.Symbol = strings.ToUpper(req.Symbol)

	// Set default lookback hours if not provided
	if req.LookbackHours <= 0 {
		req.LookbackHours = 24
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":        userID,
		"symbol":         req.Symbol,
		"sources":        req.Sources,
		"lookback_hours": req.LookbackHours,
	}).Info("Getting market sentiment analysis")

	// Check if sentiment analyzer is available
	if h.sentimentAnalyzer == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("SENTIMENT_UNAVAILABLE", "Sentiment analysis service is not configured"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// Get sentiment analysis
	sentimentScore, err := h.sentimentAnalyzer.AnalyzeMarketSentiment(ctx, req.Symbol)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get sentiment analysis")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("SENTIMENT_ERROR", "Failed to get sentiment analysis", err.Error()))
		return
	}

	// Build response
	response := models.SentimentResponse{
		Symbol:      sentimentScore.Symbol,
		Overall:     sentimentScore.Overall,
		Confidence:  sentimentScore.Confidence,
		Trend:       sentimentScore.Trend,
		Sources:     sentimentScore.Sources,
		Keywords:    sentimentScore.Keywords,
		NewsCount:   sentimentScore.NewsCount,
		TweetCount:  sentimentScore.TweetCount,
		RedditCount: sentimentScore.RedditCount,
		Breakdown: models.SentimentBreakdown{
			News:    sentimentScore.Breakdown.News,
			Twitter: sentimentScore.Breakdown.Twitter,
			Reddit:  sentimentScore.Breakdown.Reddit,
		},
		Timestamp: sentimentScore.Timestamp,
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":    userID,
		"symbol":     req.Symbol,
		"overall":    response.Overall,
		"trend":      response.Trend,
		"confidence": response.Confidence,
	}).Info("Sentiment analysis completed")

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// RunBacktest handles POST /api/v1/analysis/backtest
// Runs a backtest simulation with the given parameters
func (h *AnalysisHandler) RunBacktest(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req models.BacktestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Invalid request body", err.Error()))
		return
	}

	// Normalize pair format
	req.Pair = normalizePairFormat(req.Pair)

	// Set defaults
	if req.Strategy == "" {
		req.Strategy = "scalping"
	}
	if req.Commission <= 0 {
		req.Commission = 0.1 // Default 0.1% commission
	}
	if req.Confidence <= 0 {
		req.Confidence = 0.6 // Default 60% confidence threshold
	}

	// Validate date range
	if req.EndDate.Before(req.StartDate) {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "End date must be after start date"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":       userID,
		"exchange":      req.Exchange,
		"pair":          req.Pair,
		"start_date":    req.StartDate,
		"end_date":      req.EndDate,
		"initial_funds": req.InitialFunds,
		"strategy":      req.Strategy,
	}).Info("Running backtest simulation")

	// Check if backtester is available
	if h.backtester == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("BACKTEST_UNAVAILABLE", "Backtest service is not configured"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	// Create strategy for backtest
	strategy := h.createDefaultStrategy(req.Strategy, req.Confidence)

	// Create backtest config
	backtestCfg := &ai.BacktestConfig{
		Exchange:     req.Exchange,
		Pair:         req.Pair,
		StartDate:    req.StartDate,
		EndDate:      req.EndDate,
		InitialFunds: req.InitialFunds,
		Strategy:     strategy,
		Commission:   req.Commission,
	}

	// Run backtest
	result, err := h.backtester.Run(ctx, backtestCfg)
	if err != nil {
		h.logger.WithError(err).Error("Failed to run backtest")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("BACKTEST_ERROR", "Failed to run backtest", err.Error()))
		return
	}

	// Build response
	response := h.convertBacktestResult(result, req)

	h.logger.WithFields(logrus.Fields{
		"user_id":       userID,
		"pair":          req.Pair,
		"total_trades":  response.TotalTrades,
		"win_rate":      response.WinRate,
		"total_return":  response.TotalReturnPct,
	}).Info("Backtest completed")

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// GetIndicators handles GET /api/v1/analysis/indicators/:pair
// Returns technical indicators for a trading pair
func (h *AnalysisHandler) GetIndicators(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	pair := c.Param("pair")
	if pair == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Trading pair is required"))
		return
	}

	// Normalize pair format
	pair = normalizePairFormat(pair)

	var req models.GetIndicatorsRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}

	// Set defaults
	exchange := req.Exchange
	if exchange == "" {
		exchange = "kucoin"
	}
	interval := req.Interval
	if interval == "" {
		interval = "1h"
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
		"pair":     pair,
		"interval": interval,
	}).Debug("Getting technical indicators")

	// Check if GCT client is available
	if h.gctClient == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("GCT_UNAVAILABLE", "Trading client is not connected"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	// Fetch indicators in parallel
	response := &models.IndicatorsResponse{
		Exchange:  exchange,
		Pair:      pair,
		Interval:  interval,
		Timestamp: time.Now(),
	}

	// Get RSI
	rsi, err := h.gctClient.CalculateRSI(ctx, exchange, pair, 14)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to calculate RSI")
	} else {
		signal := "neutral"
		if rsi > 70 {
			signal = "overbought"
		} else if rsi < 30 {
			signal = "oversold"
		}
		response.RSI = &models.RSIIndicator{
			Value:  rsi,
			Period: 14,
			Signal: signal,
		}
	}

	// Get MACD
	macd, err := h.gctClient.CalculateMACD(ctx, exchange, pair, 12, 26, 9)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to calculate MACD")
	} else {
		trend := "neutral"
		if macd.Histogram > 0 {
			trend = "bullish"
		} else if macd.Histogram < 0 {
			trend = "bearish"
		}
		response.MACD = &models.MACDIndicator{
			MACD:      macd.MACD,
			Signal:    macd.Signal,
			Histogram: macd.Histogram,
			Trend:     trend,
		}
	}

	// Get Bollinger Bands
	bb, err := h.gctClient.CalculateBollingerBands(ctx, exchange, pair, 20, 2.0)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to calculate Bollinger Bands")
	} else {
		// Get current price to determine position
		ticker, tickerErr := h.gctClient.GetTicker(ctx, exchange, pair)
		position := "middle"
		if tickerErr == nil && ticker != nil {
			if ticker.Last > bb.Upper {
				position = "outside_upper"
			} else if ticker.Last < bb.Lower {
				position = "outside_lower"
			} else if ticker.Last > bb.Middle {
				position = "upper"
			} else {
				position = "lower"
			}
		}

		width := 0.0
		if bb.Middle > 0 {
			width = (bb.Upper - bb.Lower) / bb.Middle * 100
		}

		response.Bollinger = &models.BollingerBandsIndicator{
			Upper:    bb.Upper,
			Middle:   bb.Middle,
			Lower:    bb.Lower,
			Width:    width,
			Position: position,
		}
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"pair":     pair,
		"has_rsi":  response.RSI != nil,
		"has_macd": response.MACD != nil,
		"has_bb":   response.Bollinger != nil,
	}).Info("Technical indicators retrieved")

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// buildMarketContext builds a market context for AI analysis
func (h *AnalysisHandler) buildMarketContext(ctx context.Context, exchange, pair string, timeframes []string) (*ai.MarketContext, error) {
	// Get current ticker
	ticker, err := h.gctClient.GetTicker(ctx, exchange, pair)
	if err != nil {
		return nil, err
	}

	marketCtx := &ai.MarketContext{
		Pair:         pair,
		CurrentPrice: ticker.Last,
		Timestamp:    time.Now(),
		Timeframes:   make(map[string]*ai.TimeframeContext),
	}

	// Build context for each timeframe
	for _, tf := range timeframes {
		tfCtx, err := h.buildTimeframeContext(ctx, exchange, pair, tf)
		if err != nil {
			h.logger.WithError(err).WithField("timeframe", tf).Warn("Failed to build timeframe context")
			continue
		}
		marketCtx.Timeframes[tf] = tfCtx
	}

	// Build simple key levels from ticker data
	marketCtx.KeyLevels = &ai.KeyLevels{
		Support:    ticker.Low24h,
		Resistance: ticker.High24h,
	}

	return marketCtx, nil
}

// buildTimeframeContext builds context for a specific timeframe
func (h *AnalysisHandler) buildTimeframeContext(ctx context.Context, exchange, pair, timeframe string) (*ai.TimeframeContext, error) {
	// Get RSI for trend detection
	rsi, err := h.gctClient.CalculateRSI(ctx, exchange, pair, 14)
	if err != nil {
		rsi = 50 // Default neutral
	}

	// Get MACD
	macd, err := h.gctClient.CalculateMACD(ctx, exchange, pair, 12, 26, 9)
	if err != nil {
		macd = nil
	}

	// Determine trend
	trend := "sideways"
	strength := 0.5
	if rsi > 60 {
		trend = "bullish"
		strength = (rsi - 50) / 50
	} else if rsi < 40 {
		trend = "bearish"
		strength = (50 - rsi) / 50
	}

	// Build indicators struct
	indicators := &ai.Indicators{
		RSI: rsi,
	}

	if macd != nil {
		macdTrend := "neutral"
		if macd.Histogram > 0 {
			macdTrend = "bullish"
		} else if macd.Histogram < 0 {
			macdTrend = "bearish"
		}
		indicators.MACD = &ai.MACD{
			Value:     macd.MACD,
			Signal:    macd.Signal,
			Histogram: macd.Histogram,
			Trend:     macdTrend,
		}
	}

	return &ai.TimeframeContext{
		Timeframe:  timeframe,
		Trend:      trend,
		Strength:   strength,
		Indicators: indicators,
	}, nil
}

// getProviderName returns the name of the current LLM provider
func (h *AnalysisHandler) getProviderName() string {
	if h.llmAnalyzer == nil {
		return "none"
	}
	provider := h.llmAnalyzer.GetPrimaryProvider()
	if provider == nil {
		return "none"
	}
	return provider.Name()
}

// createDefaultStrategy creates a default strategy based on style name
func (h *AnalysisHandler) createDefaultStrategy(styleName string, confidence float64) *ai.Strategy {
	// Create a basic strategy configuration
	strategy := &ai.Strategy{
		Name:       styleName,
		Confidence: confidence,
		Timeframes: []ai.TimeframeConfig{
			{Interval: "15m", Weight: 0.6},
			{Interval: "1h", Weight: 0.4},
		},
		Indicators: ai.IndicatorConfig{
			RSI: ai.RSIConfig{
				Overbought: 70,
				Oversold:   30,
				Period:     14,
				Weight:     0.4,
			},
			MACD: ai.MACDConfig{
				Fast:   12,
				Slow:   26,
				Signal: 9,
				Weight: 0.3,
			},
		},
		Risk: ai.RiskConfig{
			TradeSize:     1.0,
			Leverage:      1,
			StopLossPct:   0.5,
			TakeProfitPct: 1.0,
		},
		Execution: ai.ExecutionConfig{
			CheckInterval:       60,
			ConfidenceThreshold: confidence,
		},
	}

	// Adjust based on style
	switch styleName {
	case "scalping":
		strategy.Type = ai.StyleScalping
		strategy.Risk.StopLossPct = 0.3
		strategy.Risk.TakeProfitPct = 0.5
	case "day_trading":
		strategy.Type = ai.StyleDay
		strategy.Risk.StopLossPct = 1.0
		strategy.Risk.TakeProfitPct = 2.0
	case "swing_trading":
		strategy.Type = ai.StyleSwing
		strategy.Timeframes = []ai.TimeframeConfig{
			{Interval: "1h", Weight: 0.5},
			{Interval: "4h", Weight: 0.5},
		}
		strategy.Risk.StopLossPct = 2.0
		strategy.Risk.TakeProfitPct = 4.0
	case "position_trading":
		strategy.Type = ai.StylePosition
		strategy.Timeframes = []ai.TimeframeConfig{
			{Interval: "4h", Weight: 0.4},
			{Interval: "1d", Weight: 0.6},
		}
		strategy.Risk.StopLossPct = 5.0
		strategy.Risk.TakeProfitPct = 10.0
	}

	return strategy
}

// convertBacktestResult converts internal backtest result to API response
func (h *AnalysisHandler) convertBacktestResult(result *ai.BacktestResult, req models.BacktestRequest) *models.BacktestResponse {
	response := &models.BacktestResponse{
		Config: models.BacktestConfigSummary{
			Exchange:     req.Exchange,
			Pair:         req.Pair,
			Strategy:     req.Strategy,
			InitialFunds: req.InitialFunds,
			Commission:   req.Commission,
		},
		StartDate:       result.StartDate,
		EndDate:         result.EndDate,
		Duration:        result.Duration.String(),
		InitialBalance:  result.InitialBalance,
		FinalBalance:    result.FinalBalance,
		TotalReturn:     result.TotalReturn,
		TotalReturnPct:  result.TotalReturnPct,
		MaxDrawdown:     result.MaxDrawdown,
		MaxDrawdownPct:  result.MaxDrawdownPct,
		SharpeRatio:     result.SharpeRatio,
		WinRate:         result.WinRate,
		ProfitFactor:    result.ProfitFactor,
		TotalTrades:     result.TotalTrades,
		WinningTrades:   result.WinningTrades,
		LosingTrades:    result.LosingTrades,
		AvgWin:          result.AvgWin,
		AvgLoss:         result.AvgLoss,
		LargestWin:      result.LargestWin,
		LargestLoss:     result.LargestLoss,
	}

	// Convert trades (limit to 100 for response size)
	maxTrades := 100
	if len(result.Trades) < maxTrades {
		maxTrades = len(result.Trades)
	}
	response.Trades = make([]models.BacktestTradeResult, maxTrades)
	for i := 0; i < maxTrades; i++ {
		trade := result.Trades[i]
		response.Trades[i] = models.BacktestTradeResult{
			ID:         trade.ID,
			EntryTime:  trade.EntryTime,
			ExitTime:   trade.ExitTime,
			Side:       trade.Side,
			EntryPrice: trade.EntryPrice,
			ExitPrice:  trade.ExitPrice,
			Size:       trade.Size,
			PnL:        trade.PnL,
			PnLPct:     trade.PnLPct,
			Commission: trade.Commission,
			Reason:     trade.Reason,
			Duration:   trade.Duration.String(),
		}
	}

	// Convert equity curve (sample if too large)
	maxPoints := 100
	step := 1
	if len(result.EquityCurve) > maxPoints {
		step = len(result.EquityCurve) / maxPoints
	}
	response.EquityCurve = make([]models.EquityPointResponse, 0, maxPoints)
	for i := 0; i < len(result.EquityCurve); i += step {
		point := result.EquityCurve[i]
		response.EquityCurve = append(response.EquityCurve, models.EquityPointResponse{
			Time:    point.Time,
			Balance: point.Balance,
			Return:  point.Return,
		})
	}

	return response
}

// normalizePairFormat normalizes trading pair format
func normalizePairFormat(pair string) string {
	pair = strings.ToUpper(pair)
	pair = strings.ReplaceAll(pair, "/", "-")
	pair = strings.ReplaceAll(pair, "_", "-")
	return pair
}

// GetComprehensiveAnalysis handles POST /api/v1/analysis/comprehensive
// Returns consolidated analysis data matching Flutter's ComprehensiveAnalysis model
func (h *AnalysisHandler) GetComprehensiveAnalysis(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req models.ComprehensiveAnalysisRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Invalid request body", err.Error()))
		return
	}

	// Set defaults
	if req.Exchange == "" {
		req.Exchange = "kucoin"
	}
	if req.Interval == "" {
		req.Interval = "1h"
	}

	// Default to true for optional boolean flags
	enableLLM := true
	if req.EnableLLM != nil {
		enableLLM = *req.EnableLLM
	}
	enableSentiment := true
	if req.EnableSentiment != nil {
		enableSentiment = *req.EnableSentiment
	}

	// Normalize symbol to pair format
	pair := normalizePairFormat(req.Symbol)

	h.logger.WithFields(logrus.Fields{
		"user_id":          userID,
		"symbol":           req.Symbol,
		"pair":             pair,
		"exchange":         req.Exchange,
		"enable_llm":       enableLLM,
		"enable_sentiment": enableSentiment,
	}).Info("Getting comprehensive analysis")

	// Check if GCT client is available
	if h.gctClient == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("GCT_UNAVAILABLE", "Trading client is not connected"))
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	// Initialize response
	response := &models.ComprehensiveAnalysisResponse{
		Symbol:    req.Symbol,
		Exchange:  req.Exchange,
		Timestamp: time.Now(),
	}

	// 1. Get ticker data (price_data) - REQUIRED
	ticker, err := h.gctClient.GetTicker(ctx, req.Exchange, pair)
	if err != nil {
		h.logger.WithError(err).Error("Failed to get ticker data")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponseWithDetails("TICKER_ERROR", "Failed to get ticker data", err.Error()))
		return
	}

	// Calculate change percentage if possible
	changePercent := 0.0
	if ticker.Last > 0 && ticker.Change24h != 0 {
		prevPrice := ticker.Last - ticker.Change24h
		if prevPrice > 0 {
			changePercent = (ticker.Change24h / prevPrice) * 100
		}
	}

	response.PriceData = models.PriceData{
		Last:             ticker.Last,
		Bid:              ticker.Bid,
		Ask:              ticker.Ask,
		Open:             0,                // Not available in Ticker model
		High24h:          ticker.High24h,
		Low24h:           ticker.Low24h,
		Volume:           ticker.Volume,
		VolumeUSD:        0,                // Not available in Ticker model
		Change24h:        ticker.Change24h,
		ChangePercent24h: changePercent,
	}

	// 2. Get technical indicators - REQUIRED
	indicatorsResp := &models.IndicatorsResponse{
		Exchange:  req.Exchange,
		Pair:      pair,
		Interval:  req.Interval,
		Timestamp: time.Now(),
	}

	// Get RSI
	rsi, err := h.gctClient.CalculateRSI(ctx, req.Exchange, pair, 14)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to calculate RSI")
	} else {
		signal := "neutral"
		if rsi > 70 {
			signal = "overbought"
		} else if rsi < 30 {
			signal = "oversold"
		}
		indicatorsResp.RSI = &models.RSIIndicator{
			Value:  rsi,
			Period: 14,
			Signal: signal,
		}
	}

	// Get MACD
	macd, err := h.gctClient.CalculateMACD(ctx, req.Exchange, pair, 12, 26, 9)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to calculate MACD")
	} else {
		trend := "neutral"
		if macd.Histogram > 0 {
			trend = "bullish"
		} else if macd.Histogram < 0 {
			trend = "bearish"
		}
		indicatorsResp.MACD = &models.MACDIndicator{
			MACD:      macd.MACD,
			Signal:    macd.Signal,
			Histogram: macd.Histogram,
			Trend:     trend,
		}
	}

	// Get Bollinger Bands
	bb, err := h.gctClient.CalculateBollingerBands(ctx, req.Exchange, pair, 20, 2.0)
	if err != nil {
		h.logger.WithError(err).Warn("Failed to calculate Bollinger Bands")
	} else {
		position := "middle"
		if ticker.Last > bb.Upper {
			position = "outside_upper"
		} else if ticker.Last < bb.Lower {
			position = "outside_lower"
		} else if ticker.Last > bb.Middle {
			position = "upper"
		} else {
			position = "lower"
		}

		width := 0.0
		if bb.Middle > 0 {
			width = (bb.Upper - bb.Lower) / bb.Middle * 100
		}

		indicatorsResp.Bollinger = &models.BollingerBandsIndicator{
			Upper:    bb.Upper,
			Middle:   bb.Middle,
			Lower:    bb.Lower,
			Width:    width,
			Position: position,
		}
	}

	response.TechnicalIndicators = indicatorsResp

	// 3. Get LLM analysis (optional)
	if enableLLM && h.llmAnalyzer != nil {
		marketCtx, err := h.buildMarketContext(ctx, req.Exchange, pair, []string{req.Interval})
		if err != nil {
			h.logger.WithError(err).Warn("Failed to build market context for LLM analysis")
		} else {
			analysis, err := h.llmAnalyzer.AnalyzeMarket(ctx, marketCtx)
			if err != nil {
				h.logger.WithError(err).Warn("Failed to get LLM analysis")
			} else {
				response.LLMAnalysis = &models.TradingSignalResponse{
					Pair:            pair,
					Action:          analysis.Action,
					Confidence:      analysis.Confidence,
					Reasoning:       analysis.Reasoning,
					EntryPrice:      analysis.EntryPrice,
					StopLoss:        analysis.StopLoss,
					TakeProfit:      analysis.TakeProfit,
					RiskAssessment:  analysis.RiskAssessment,
					MarketCondition: analysis.MarketCondition,
					KeyFactors:      analysis.KeyFactors,
					Warnings:        analysis.Warnings,
					Provider:        h.getProviderName(),
					Timestamp:       time.Now(),
				}
			}
		}
	}

	// 4. Get sentiment analysis (optional)
	if enableSentiment && h.sentimentAnalyzer != nil {
		sentimentScore, err := h.sentimentAnalyzer.AnalyzeMarketSentiment(ctx, req.Symbol)
		if err != nil {
			h.logger.WithError(err).Warn("Failed to get sentiment analysis")
		} else {
			response.SentimentAnalysis = &models.SentimentResponse{
				Symbol:      sentimentScore.Symbol,
				Overall:     sentimentScore.Overall,
				Confidence:  sentimentScore.Confidence,
				Trend:       sentimentScore.Trend,
				Sources:     sentimentScore.Sources,
				Keywords:    sentimentScore.Keywords,
				NewsCount:   sentimentScore.NewsCount,
				TweetCount:  sentimentScore.TweetCount,
				RedditCount: sentimentScore.RedditCount,
				Breakdown: models.SentimentBreakdown{
					News:    sentimentScore.Breakdown.News,
					Twitter: sentimentScore.Breakdown.Twitter,
					Reddit:  sentimentScore.Breakdown.Reddit,
				},
				Timestamp: sentimentScore.Timestamp,
			}
		}
	}

	// 5. Generate recommendation based on available data
	response.Recommendation = h.generateRecommendation(response)

	h.logger.WithFields(logrus.Fields{
		"user_id":        userID,
		"symbol":         req.Symbol,
		"recommendation": response.Recommendation,
		"has_llm":        response.LLMAnalysis != nil,
		"has_sentiment":  response.SentimentAnalysis != nil,
	}).Info("Comprehensive analysis completed")

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// generateRecommendation generates a recommendation based on available analysis data
func (h *AnalysisHandler) generateRecommendation(response *models.ComprehensiveAnalysisResponse) string {
	// If LLM analysis is available, use its recommendation
	if response.LLMAnalysis != nil {
		return response.LLMAnalysis.Action
	}

	// Otherwise, use technical indicators to generate recommendation
	if response.TechnicalIndicators == nil {
		return "WAIT"
	}

	bullishSignals := 0
	bearishSignals := 0
	totalSignals := 0

	// Check RSI
	if response.TechnicalIndicators.RSI != nil {
		totalSignals++
		if response.TechnicalIndicators.RSI.Signal == "oversold" {
			bullishSignals++
		} else if response.TechnicalIndicators.RSI.Signal == "overbought" {
			bearishSignals++
		}
	}

	// Check MACD
	if response.TechnicalIndicators.MACD != nil {
		totalSignals++
		if response.TechnicalIndicators.MACD.Trend == "bullish" {
			bullishSignals++
		} else if response.TechnicalIndicators.MACD.Trend == "bearish" {
			bearishSignals++
		}
	}

	// Check Bollinger Bands
	if response.TechnicalIndicators.Bollinger != nil {
		totalSignals++
		if response.TechnicalIndicators.Bollinger.Position == "outside_lower" ||
		   response.TechnicalIndicators.Bollinger.Position == "lower" {
			bullishSignals++
		} else if response.TechnicalIndicators.Bollinger.Position == "outside_upper" ||
		          response.TechnicalIndicators.Bollinger.Position == "upper" {
			bearishSignals++
		}
	}

	// Check sentiment if available
	if response.SentimentAnalysis != nil {
		totalSignals++
		if response.SentimentAnalysis.Overall > 0.3 {
			bullishSignals++
		} else if response.SentimentAnalysis.Overall < -0.3 {
			bearishSignals++
		}
	}

	// Generate recommendation based on signal consensus
	if totalSignals == 0 {
		return "WAIT"
	}

	bullishRatio := float64(bullishSignals) / float64(totalSignals)
	bearishRatio := float64(bearishSignals) / float64(totalSignals)

	// Require at least 60% consensus for BUY/SELL
	if bullishRatio >= 0.6 {
		return "BUY"
	} else if bearishRatio >= 0.6 {
		return "SELL"
	}

	return "WAIT"
}

// ExplainTrade handles GET /api/v1/analysis/explain/:trade_id.
// API-8: returns the decomposed decision tree for a real-money decision
// rebuilt from audit.go + decision.go stored data. Level: 1+.
func (h *AnalysisHandler) ExplainTrade(c *gin.Context) {
	tradeID := c.Param("trade_id")
	if tradeID == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("VALIDATION_FAILED", "trade_id is required"))
		return
	}

	if h.tradeRepo == nil {
		c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("NOT_CONFIGURED",
			"trade explainability not configured — TradeHistoryRepository not injected"))
		return
	}

	trade, err := h.tradeRepo.GetByID(tradeID)
	if err != nil {
		c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND",
			"trade not found or not accessible"))
		return
	}

	// Decompose the stored decision_reason + indicators_snapshot into an
	// explainability tree. This is the single source of truth for 4 consumers:
	// execution, education, replays, prompt-improvement.
	tree := gin.H{
		"trade_id":        trade.ID,
		"pair":            trade.Pair,
		"side":            trade.Side,
		"entry_price":     trade.EntryPrice,
		"confidence":      trade.Confidence,
		"decision_reason": trade.DecisionReason,
		"gates": gin.H{
			"regime_gate":          extractGate(trade.DecisionReason, "regime"),
			"ml_gate":              extractGate(trade.DecisionReason, "ml"),
			"trend_filter":         extractGate(trade.DecisionReason, "trend"),
			"llm_vote":             extractGate(trade.DecisionReason, "llm"),
			"confidence_threshold": trade.Confidence,
		},
		"indicators_snapshot": trade.IndicatorsSnapshot,
		"outcome": gin.H{
			"exit_reason": trade.ExitReason,
			"pnl_pct":     trade.PnLPct,
			"status":      trade.Status,
		},
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(tree))
}

// extractGate extracts a gate decision from the decision_reason string.
// Returns "unknown" if not found in the stored reason.
func extractGate(reason, gate string) string {
	if reason == "" {
		return "unknown"
	}
	lower := strings.ToLower(reason)
	gateStr := strings.ToLower(gate)
	if strings.Contains(lower, gateStr+"=pass") || strings.Contains(lower, gateStr+" passed") {
		return "pass"
	}
	if strings.Contains(lower, gateStr+"=fail") || strings.Contains(lower, gateStr+" blocked") {
		return "fail"
	}
	return "unknown"
}
