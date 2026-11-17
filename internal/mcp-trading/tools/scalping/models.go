package scalping

import "github.com/rantipay/trading-mcp/internal/scalping/manager"

// Type aliases — manager package owns the canonical definitions.
// These aliases let MCP tool handlers keep using the short names without changes.
type (
	BotConfig       = manager.BotConfig
	BotInstance     = manager.BotInstance
	BotInfo         = manager.BotInfo
	BotTrade        = manager.BotTrade
	Signal          = manager.Signal
	DPMPositionInfo = manager.DPMPositionInfo
	PaperPosition   = manager.PaperPosition
)

// AnalyzeOpportunityRequest represents the request to analyze a scalping opportunity.
type AnalyzeOpportunityRequest struct {
	Exchange  string `json:"exchange"`
	Pair      string `json:"pair"`
	Timeframe string `json:"timeframe,omitempty"`
}

// AnalyzeOpportunityResponse represents the opportunity analysis result.
type AnalyzeOpportunityResponse struct {
	Pair                  string   `json:"pair"`
	OverallScore          int      `json:"overall_score"`
	OpportunityScore      float64  `json:"opportunity_score"`
	VolatilityScore       int      `json:"volatility_score"`
	SpreadScore           int      `json:"spread_score"`
	VolumeScore           int      `json:"volume_score"`
	Recommendation        string   `json:"recommendation"`
	Direction             string   `json:"direction"`
	Confidence            float64  `json:"confidence"`
	Reasons               []string `json:"reasons"`
	CurrentPrice          float64  `json:"current_price"`
	EntryPrice            float64  `json:"entry_price"`
	TargetPrice           float64  `json:"target_price"`
	StopLoss              float64  `json:"stop_loss"`
	RiskRewardRatio       float64  `json:"risk_reward_ratio"`
	ExpectedProfitPercent float64  `json:"expected_profit_percent"`
	MaxLossPercent        float64  `json:"max_loss_percent"`
	Spread                float64  `json:"spread_percent"`
	Volume24h             float64  `json:"volume_24h"`
	ATR                   float64  `json:"atr"`
	Timestamp             string   `json:"timestamp"`
}

// GetSignalsRequest represents the request to get trading signals.
type GetSignalsRequest struct {
	Exchange   string   `json:"exchange"`
	Pairs      []string `json:"pairs"`
	Strategies []string `json:"strategies,omitempty"`
}

// GetSignalsResponse represents the response with trading signals.
type GetSignalsResponse struct {
	Signals []Signal `json:"signals"`
	Count   int      `json:"count"`
}

// ExecuteTradeRequest represents the request to execute a trade.
type ExecuteTradeRequest struct {
	Exchange          string  `json:"exchange"`
	Pair              string  `json:"pair"`
	Side              string  `json:"side"`
	AmountUSD         float64 `json:"amount_usd"`
	Strategy          string  `json:"strategy,omitempty"`
	StopLossPercent   float64 `json:"stop_loss_percent,omitempty"`
	TakeProfitPercent float64 `json:"take_profit_percent,omitempty"`
	Leverage          float64 `json:"leverage,omitempty"`
}

// ExecuteTradeResponse represents the trade execution result.
type ExecuteTradeResponse struct {
	OrderID           string  `json:"order_id"`
	StopLossOrderID   string  `json:"stop_loss_order_id,omitempty"`
	TakeProfitOrderID string  `json:"take_profit_order_id,omitempty"`
	Exchange          string  `json:"exchange"`
	Pair              string  `json:"pair"`
	Side              string  `json:"side"`
	Amount            float64 `json:"amount"`
	Price             float64 `json:"price"`
	StopLoss          float64 `json:"stop_loss,omitempty"`
	TakeProfit        float64 `json:"take_profit,omitempty"`
	Leverage          float64 `json:"leverage,omitempty"`
	Status            string  `json:"status"`
	ExecutedAt        string  `json:"executed_at"`
	Latency           string  `json:"latency"`
	EstimatedFee      float64 `json:"estimated_fee"`
}

// StartBotRequest represents the request to start a scalping bot.
type StartBotRequest struct {
	Exchange        string   `json:"exchange"`
	Pairs           []string `json:"pairs"`
	Strategies      []string `json:"strategies"`
	MaxPositionSize float64  `json:"max_position_size_usd"`
	MaxDailyTrades  int      `json:"max_daily_trades"`
	StopLoss        float64  `json:"stop_loss_percent"`
	TakeProfit      float64  `json:"take_profit_percent"`
}

// StartBotResponse represents the response after starting a bot.
type StartBotResponse struct {
	BotID     string    `json:"bot_id"`
	Status    string    `json:"status"`
	Config    BotConfig `json:"config"`
	StartedAt string    `json:"started_at"`
}

// GetBotStatusRequest represents the request to get bot status.
type GetBotStatusRequest struct {
	BotID string `json:"bot_id"`
}

// GetBotStatusResponse represents the bot status response.
type GetBotStatusResponse struct {
	BotID           string    `json:"bot_id"`
	Status          string    `json:"status"`
	Exchange        string    `json:"exchange"`
	Pair            string    `json:"pair"`
	Running         bool      `json:"running"`
	Config          BotConfig `json:"config,omitempty"`
	StartTime       string    `json:"start_time,omitempty"`
	Uptime          string    `json:"uptime"`
	TradesToday     int       `json:"trades_today"`
	PnLToday        float64   `json:"pnl_today"`
	WinRate         float64   `json:"win_rate"`
	ActivePositions int       `json:"active_positions"`
	LastSignals     []Signal  `json:"last_signals,omitempty"`
	TotalTrades     int       `json:"total_trades"`
	WinningTrades   int       `json:"winning_trades"`
	LosingTrades    int       `json:"losing_trades"`
	AvgProfit       float64   `json:"avg_profit"`
	AvgLoss         float64   `json:"avg_loss"`
	Message         string    `json:"message,omitempty"`
}

// StopBotRequest represents the request to stop a bot.
type StopBotRequest struct {
	BotID          string `json:"bot_id"`
	ClosePositions bool   `json:"close_positions"`
}

// StopBotResponse represents the response after stopping a bot.
type StopBotResponse struct {
	BotID           string  `json:"bot_id"`
	Status          string  `json:"status"`
	StoppedAt       string  `json:"stopped_at"`
	TotalTrades     int     `json:"total_trades"`
	TotalPnL        float64 `json:"total_pnl"`
	WinRate         float64 `json:"win_rate"`
	ClosedPositions int     `json:"closed_positions,omitempty"`
}

// GetBotTradesRequest represents the request to get bot trade history.
type GetBotTradesRequest struct {
	Exchange string `json:"exchange"`
	Pair     string `json:"pair"`
	BotID    string `json:"bot_id,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	Status   string `json:"status,omitempty"`
}

// GetBotTradesResponse represents the trade history response.
type GetBotTradesResponse struct {
	Trades     []BotTrade `json:"trades"`
	TotalCount int        `json:"total_count"`
	Limit      int        `json:"limit"`
	Offset     int        `json:"offset"`
	HasMore    bool       `json:"has_more"`
	Message    string     `json:"message,omitempty"`
}

// PerformanceDataPoint represents a single point in a performance chart.
type PerformanceDataPoint struct {
	Timestamp     string  `json:"timestamp"`
	CumulativePnL float64 `json:"cumulative_pnl"`
	WinRate       float64 `json:"win_rate"`
	TradesCount   int     `json:"trades_count"`
}

// PerformanceSummary represents aggregate performance statistics.
type PerformanceSummary struct {
	TotalPnL    float64 `json:"total_pnl"`
	AvgWinRate  float64 `json:"avg_win_rate"`
	TotalTrades int     `json:"total_trades"`
	Timeframe   string  `json:"timeframe"`
	StartTime   string  `json:"start_time"`
	EndTime     string  `json:"end_time"`
}

// GetBotPerformanceRequest represents the request for performance data.
type GetBotPerformanceRequest struct {
	Exchange  string `json:"exchange"`
	Pair      string `json:"pair"`
	BotID     string `json:"bot_id,omitempty"`
	Timeframe string `json:"timeframe"`
	Limit     int    `json:"limit,omitempty"`
}

// GetBotPerformanceResponse represents the performance data response.
type GetBotPerformanceResponse struct {
	DataPoints []PerformanceDataPoint `json:"data_points"`
	Summary    PerformanceSummary     `json:"summary"`
	Message    string                 `json:"message,omitempty"`
}
