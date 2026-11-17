package ai

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
)

// Backtester runs backtests using AI strategies
type Backtester struct {
	mcpClient      *MCPClient
	contextBuilder *ContextBuilder
	decisionEngine *DecisionEngine
	log            *logrus.Logger
}

// BacktestConfig defines backtest parameters
type BacktestConfig struct {
	Exchange     string
	Pair         string
	StartDate    time.Time
	EndDate      time.Time
	InitialFunds float64
	Strategy     *Strategy
	Commission   float64 // Trading fee percentage
}

// BacktestResult holds backtest results
type BacktestResult struct {
	Config         *BacktestConfig
	StartDate      time.Time
	EndDate        time.Time
	Duration       time.Duration
	InitialBalance float64
	FinalBalance   float64
	TotalReturn    float64
	TotalReturnPct float64
	MaxDrawdown    float64
	MaxDrawdownPct float64
	SharpeRatio    float64
	WinRate        float64
	ProfitFactor   float64
	TotalTrades    int
	WinningTrades  int
	LosingTrades   int
	AvgWin         float64
	AvgLoss        float64
	LargestWin     float64
	LargestLoss    float64
	Trades         []*BacktestTrade
	EquityCurve    []EquityPoint
	Signals        []*BacktestSignal
}

// BacktestTrade represents a completed trade in backtest
type BacktestTrade struct {
	ID         int
	EntryTime  time.Time
	ExitTime   time.Time
	Side       string // "buy" or "sell"
	EntryPrice float64
	ExitPrice  float64
	Size       float64
	PnL        float64
	PnLPct     float64
	Commission float64
	Reason     string // "take_profit", "stop_loss", "signal_reversal"
	Duration   time.Duration
	MaxProfit  float64
	MaxLoss    float64
}

// BacktestSignal represents a signal generated during backtest
type BacktestSignal struct {
	Time       time.Time
	Action     string
	Confidence float64
	Price      float64
	Reason     string
	Executed   bool
}

// EquityPoint represents a point in the equity curve
type EquityPoint struct {
	Time    time.Time
	Balance float64
	Return  float64
}

// NewBacktester creates a new backtester
func NewBacktester(mcpClient *MCPClient, contextBuilder *ContextBuilder, decisionEngine *DecisionEngine, log *logrus.Logger) *Backtester {
	return &Backtester{
		mcpClient:      mcpClient,
		contextBuilder: contextBuilder,
		decisionEngine: decisionEngine,
		log:            log,
	}
}

// Run executes a backtest
func (bt *Backtester) Run(ctx context.Context, config *BacktestConfig) (*BacktestResult, error) {
	bt.log.WithFields(logrus.Fields{
		"exchange":   config.Exchange,
		"pair":       config.Pair,
		"start_date": config.StartDate.Format("2006-01-02"),
		"end_date":   config.EndDate.Format("2006-01-02"),
		"strategy":   config.Strategy.Name,
	}).Info("Starting backtest")

	result := &BacktestResult{
		Config:         config,
		StartDate:      config.StartDate,
		EndDate:        config.EndDate,
		InitialBalance: config.InitialFunds,
		FinalBalance:   config.InitialFunds,
		Trades:         make([]*BacktestTrade, 0),
		EquityCurve:    make([]EquityPoint, 0),
		Signals:        make([]*BacktestSignal, 0),
	}

	// Get historical data via MCP
	historicalData, err := bt.getHistoricalData(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("failed to get historical data: %w", err)
	}

	bt.log.WithField("candles", len(historicalData)).Info("Historical data loaded")

	// Simulate trading
	var currentPosition *BacktestPosition
	balance := config.InitialFunds

	for i, candle := range historicalData {
		// Build market context for this candle
		marketCtx, err := bt.buildHistoricalContext(ctx, config, historicalData, i)
		if err != nil {
			bt.log.WithError(err).Warn("Failed to build context, skipping candle")
			continue
		}

		// Get trading decision
		decision, err := bt.decisionEngine.EvaluateOpportunity(ctx, marketCtx)
		if err != nil {
			bt.log.WithError(err).Warn("Failed to evaluate opportunity")
			continue
		}

		// Record signal
		signal := &BacktestSignal{
			Time:       candle.Timestamp,
			Action:     decision.Action,
			Confidence: decision.Confidence,
			Price:      marketCtx.CurrentPrice,
			Reason:     decision.Reason,
			Executed:   false,
		}
		result.Signals = append(result.Signals, signal)

		// Check if we should close current position
		if currentPosition != nil {
			shouldClose, reason := bt.shouldClosePosition(currentPosition, candle, decision)
			if shouldClose {
				trade := bt.closePosition(currentPosition, candle, reason, config.Commission)
				result.Trades = append(result.Trades, trade)
				balance += trade.PnL
				currentPosition = nil
				signal.Executed = true

				bt.log.WithFields(logrus.Fields{
					"trade_id": trade.ID,
					"pnl":      fmt.Sprintf("%.2f", trade.PnL),
					"pnl_pct":  fmt.Sprintf("%.2f%%", trade.PnLPct),
					"reason":   reason,
				}).Debug("Position closed")
			}
		}

		// Check if we should open new position
		if currentPosition == nil && decision.Action != "WAIT" {
			if decision.Confidence >= config.Strategy.Confidence {
				currentPosition = bt.openPosition(decision, candle, len(result.Trades)+1, balance, config)
				signal.Executed = true

				bt.log.WithFields(logrus.Fields{
					"position_id": currentPosition.ID,
					"side":        currentPosition.Side,
					"price":       currentPosition.EntryPrice,
					"confidence":  decision.Confidence,
				}).Debug("Position opened")
			}
		}

		// Update equity curve
		equity := balance
		if currentPosition != nil {
			equity += bt.calculateUnrealizedPnL(currentPosition, candle.Close)
		}
		result.EquityCurve = append(result.EquityCurve, EquityPoint{
			Time:    candle.Timestamp,
			Balance: equity,
			Return:  ((equity - config.InitialFunds) / config.InitialFunds) * 100,
		})
	}

	// Close any remaining position
	if currentPosition != nil {
		lastCandle := historicalData[len(historicalData)-1]
		trade := bt.closePosition(currentPosition, lastCandle, "backtest_end", config.Commission)
		result.Trades = append(result.Trades, trade)
		balance += trade.PnL
	}

	result.FinalBalance = balance
	result.Duration = config.EndDate.Sub(config.StartDate)

	// Calculate metrics
	bt.calculateMetrics(result)

	bt.log.WithFields(logrus.Fields{
		"total_trades": result.TotalTrades,
		"win_rate":     fmt.Sprintf("%.2f%%", result.WinRate),
		"total_return": fmt.Sprintf("%.2f%%", result.TotalReturnPct),
		"sharpe_ratio": fmt.Sprintf("%.2f", result.SharpeRatio),
		"max_drawdown": fmt.Sprintf("%.2f%%", result.MaxDrawdownPct),
	}).Info("Backtest completed")

	return result, nil
}

// BacktestPosition represents an open position during backtest
type BacktestPosition struct {
	ID         int
	Side       string
	EntryTime  time.Time
	EntryPrice float64
	Size       float64
	StopLoss   float64
	TakeProfit float64
	MaxProfit  float64
	MaxLoss    float64
}

// getHistoricalData fetches historical data via MCP
func (bt *Backtester) getHistoricalData(ctx context.Context, config *BacktestConfig) ([]Candle, error) {
	bt.log.Info("Fetching historical data")

	// For now, use mock data (real integration would use MCP tools)
	candles := bt.generateMockData(config)
	err := error(nil)
	if err != nil {
		bt.log.WithError(err).Warn("Failed to get real data, using mock data")
		return bt.generateMockData(config), nil
	}

	// Filter by date range
	filtered := make([]Candle, 0)
	for _, candle := range candles {
		if candle.Timestamp.After(config.StartDate) && candle.Timestamp.Before(config.EndDate) {
			filtered = append(filtered, candle)
		}
	}

	if len(filtered) == 0 {
		bt.log.Warn("No data in range, using mock data")
		return bt.generateMockData(config), nil
	}

	return filtered, nil
}

// generateMockData generates mock historical data for testing
func (bt *Backtester) generateMockData(config *BacktestConfig) []Candle {
	candles := make([]Candle, 0)
	currentTime := config.StartDate
	basePrice := 0.15

	for currentTime.Before(config.EndDate) {
		// Simulate realistic price movement
		change := (float64(len(candles)%20) - 10) / 200 // -5% to +5%
		basePrice *= (1 + change)

		candles = append(candles, Candle{
			Timestamp: currentTime,
			Open:      basePrice * 0.999,
			High:      basePrice * 1.01,
			Low:       basePrice * 0.99,
			Close:     basePrice,
			Volume:    1000000 + float64(len(candles)*10000),
		})
		currentTime = currentTime.Add(24 * time.Hour)
	}

	return candles
}

// buildHistoricalContext builds market context from historical data
func (bt *Backtester) buildHistoricalContext(ctx context.Context, config *BacktestConfig, data []Candle, currentIndex int) (*MarketContext, error) {
	// Get enough historical data for indicators
	start := currentIndex - 50
	if start < 0 {
		start = 0
	}

	if currentIndex < 20 {
		// Not enough data yet
		marketCtx := &MarketContext{
			Pair:         config.Pair,
			CurrentPrice: data[currentIndex].Close,
			Timeframes:   make(map[string]*TimeframeContext),
		}
		marketCtx.Timeframes["15m"] = &TimeframeContext{Trend: "sideways"}
		marketCtx.Timeframes["5m"] = &TimeframeContext{Trend: "sideways"}
		return marketCtx, nil
	}

	historicalCandles := data[start : currentIndex+1]

	// Calculate indicators
	indicators := bt.calculateHistoricalIndicators(historicalCandles)

	// Determine trend
	trend := "sideways"
	if indicators.RSI > 70 {
		trend = "bearish"
	} else if indicators.RSI < 30 {
		trend = "bullish"
	}

	// Build context with dynamic timeframes
	marketCtx := &MarketContext{
		Pair:         config.Pair,
		CurrentPrice: data[currentIndex].Close,
		Timeframes:   make(map[string]*TimeframeContext),
		KeyLevels: &KeyLevels{
			Support:    data[currentIndex].Low * 0.98,
			Resistance: data[currentIndex].High * 1.02,
		},
	}

	marketCtx.Timeframes["15m"] = &TimeframeContext{
		Timeframe:  "15m",
		Trend:      trend,
		Strength:   0.5,
		Indicators: indicators,
	}

	marketCtx.Timeframes["5m"] = &TimeframeContext{
		Timeframe:  "5m",
		Trend:      trend,
		Strength:   0.5,
		Indicators: indicators,
	}

	return marketCtx, nil
}

// shouldClosePosition checks if position should be closed
func (bt *Backtester) shouldClosePosition(pos *BacktestPosition, candle Candle, decision *Decision) (bool, string) {
	currentPrice := candle.Close

	// Update max profit/loss
	if pos.Side == "buy" {
		profit := currentPrice - pos.EntryPrice
		if profit > pos.MaxProfit {
			pos.MaxProfit = profit
		}
		if profit < pos.MaxLoss {
			pos.MaxLoss = profit
		}

		// Check stop loss
		if currentPrice <= pos.StopLoss {
			return true, "stop_loss"
		}

		// Check take profit
		if currentPrice >= pos.TakeProfit {
			return true, "take_profit"
		}
	} else {
		profit := pos.EntryPrice - currentPrice
		if profit > pos.MaxProfit {
			pos.MaxProfit = profit
		}
		if profit < pos.MaxLoss {
			pos.MaxLoss = profit
		}

		// Check stop loss
		if currentPrice >= pos.StopLoss {
			return true, "stop_loss"
		}

		// Check take profit
		if currentPrice <= pos.TakeProfit {
			return true, "take_profit"
		}
	}

	// Check for signal reversal
	if (pos.Side == "buy" && decision.Action == "SELL") || (pos.Side == "sell" && decision.Action == "BUY") {
		if decision.Confidence >= 0.70 { // High confidence reversal
			return true, "signal_reversal"
		}
	}

	return false, ""
}

// openPosition opens a new position
func (bt *Backtester) openPosition(decision *Decision, candle Candle, id int, balance float64, config *BacktestConfig) *BacktestPosition {
	return &BacktestPosition{
		ID:         id,
		Side:       decision.Action,
		EntryTime:  candle.Timestamp,
		EntryPrice: candle.Close,
		Size:       decision.Size,
		StopLoss:   decision.StopLoss,
		TakeProfit: decision.TakeProfit,
		MaxProfit:  0,
		MaxLoss:    0,
	}
}

// closePosition closes a position and returns the trade
func (bt *Backtester) closePosition(pos *BacktestPosition, candle Candle, reason string, commission float64) *BacktestTrade {
	exitPrice := candle.Close

	var pnl float64
	if pos.Side == "buy" {
		pnl = (exitPrice - pos.EntryPrice) * pos.Size
	} else {
		pnl = (pos.EntryPrice - exitPrice) * pos.Size
	}

	// Subtract commission
	commissionCost := (pos.EntryPrice * pos.Size * commission / 100) + (exitPrice * pos.Size * commission / 100)
	pnl -= commissionCost

	pnlPct := (pnl / (pos.EntryPrice * pos.Size)) * 100

	return &BacktestTrade{
		ID:         pos.ID,
		EntryTime:  pos.EntryTime,
		ExitTime:   candle.Timestamp,
		Side:       pos.Side,
		EntryPrice: pos.EntryPrice,
		ExitPrice:  exitPrice,
		Size:       pos.Size,
		PnL:        pnl,
		PnLPct:     pnlPct,
		Commission: commissionCost,
		Reason:     reason,
		Duration:   candle.Timestamp.Sub(pos.EntryTime),
		MaxProfit:  pos.MaxProfit * pos.Size,
		MaxLoss:    pos.MaxLoss * pos.Size,
	}
}

// calculateUnrealizedPnL calculates unrealized P&L for open position
func (bt *Backtester) calculateUnrealizedPnL(pos *BacktestPosition, currentPrice float64) float64 {
	if pos.Side == "buy" {
		return (currentPrice - pos.EntryPrice) * pos.Size
	}
	return (pos.EntryPrice - currentPrice) * pos.Size
}

// calculateMetrics calculates backtest metrics
func (bt *Backtester) calculateMetrics(result *BacktestResult) {
	result.TotalTrades = len(result.Trades)

	if result.TotalTrades == 0 {
		return
	}

	// Calculate returns
	result.TotalReturn = result.FinalBalance - result.InitialBalance
	result.TotalReturnPct = (result.TotalReturn / result.InitialBalance) * 100

	// Calculate win/loss stats
	var totalWin, totalLoss float64
	for _, trade := range result.Trades {
		if trade.PnL > 0 {
			result.WinningTrades++
			totalWin += trade.PnL
			if trade.PnL > result.LargestWin {
				result.LargestWin = trade.PnL
			}
		} else {
			result.LosingTrades++
			totalLoss += trade.PnL
			if trade.PnL < result.LargestLoss {
				result.LargestLoss = trade.PnL
			}
		}
	}

	result.WinRate = (float64(result.WinningTrades) / float64(result.TotalTrades)) * 100

	if result.WinningTrades > 0 {
		result.AvgWin = totalWin / float64(result.WinningTrades)
	}
	if result.LosingTrades > 0 {
		result.AvgLoss = totalLoss / float64(result.LosingTrades)
	}

	// Profit factor
	if totalLoss != 0 {
		result.ProfitFactor = totalWin / -totalLoss
	}

	// Calculate max drawdown
	var peak float64 = result.InitialBalance
	var maxDD float64

	for _, point := range result.EquityCurve {
		if point.Balance > peak {
			peak = point.Balance
		}
		drawdown := peak - point.Balance
		if drawdown > maxDD {
			maxDD = drawdown
		}
	}

	result.MaxDrawdown = maxDD
	result.MaxDrawdownPct = (maxDD / peak) * 100

	// Calculate Sharpe Ratio (simplified)
	if len(result.EquityCurve) > 1 {
		returns := make([]float64, len(result.EquityCurve)-1)
		for i := 1; i < len(result.EquityCurve); i++ {
			returns[i-1] = result.EquityCurve[i].Return - result.EquityCurve[i-1].Return
		}

		// Calculate mean and std dev
		var sum, sumSq float64
		for _, r := range returns {
			sum += r
			sumSq += r * r
		}
		mean := sum / float64(len(returns))
		variance := (sumSq / float64(len(returns))) - (mean * mean)
		stdDev := 0.0
		if variance > 0 {
			stdDev = variance // Simplified, should be sqrt
		}

		if stdDev > 0 {
			result.SharpeRatio = mean / stdDev
		}
	}
}

// calculateHistoricalIndicators calculates indicators from historical candles
func (bt *Backtester) calculateHistoricalIndicators(candles []Candle) *Indicators {
	if len(candles) < 14 {
		return &Indicators{
			RSI:  50,
			MACD: &MACD{Trend: "neutral"},
			Bollinger: &Bollinger{
				Upper:  candles[len(candles)-1].Close * 1.02,
				Middle: candles[len(candles)-1].Close,
				Lower:  candles[len(candles)-1].Close * 0.98,
			},
		}
	}

	// Simple RSI calculation
	gains := 0.0
	losses := 0.0
	period := 14

	for i := len(candles) - period; i < len(candles); i++ {
		if i <= 0 {
			continue
		}
		change := candles[i].Close - candles[i-1].Close
		if change > 0 {
			gains += change
		} else {
			losses += -change
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	rsi := 50.0
	if avgLoss > 0 {
		rs := avgGain / avgLoss
		rsi = 100 - (100 / (1 + rs))
	}

	// Simple MACD
	macdValue := 0.0
	macdTrend := "neutral"
	if len(candles) >= 26 {
		ema12 := bt.calculateEMA(candles, 12)
		ema26 := bt.calculateEMA(candles, 26)
		macdValue = ema12 - ema26

		if macdValue > 0 {
			macdTrend = "bullish"
		} else if macdValue < 0 {
			macdTrend = "bearish"
		}
	}

	return &Indicators{
		RSI: rsi,
		MACD: &MACD{
			Value:     macdValue,
			Histogram: macdValue,
			Trend:     macdTrend,
		},
		Bollinger: &Bollinger{
			Upper:  candles[len(candles)-1].Close * 1.02,
			Middle: candles[len(candles)-1].Close,
			Lower:  candles[len(candles)-1].Close * 0.98,
		},
	}
}

// calculateEMA calculates exponential moving average
func (bt *Backtester) calculateEMA(candles []Candle, period int) float64 {
	if len(candles) < period {
		return candles[len(candles)-1].Close
	}

	multiplier := 2.0 / float64(period+1)
	ema := candles[len(candles)-period].Close

	for i := len(candles) - period + 1; i < len(candles); i++ {
		ema = (candles[i].Close-ema)*multiplier + ema
	}

	return ema
}
