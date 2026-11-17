package models

import "time"

// StopLossTemplate represents a reusable stop-loss configuration
type StopLossTemplate struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	StopLossPercent   float64   `json:"stop_loss_percent"`
	TrailingEnabled   bool      `json:"trailing_enabled"`
	TrailingDistance  float64   `json:"trailing_distance"`
	BreakevenEnabled  bool      `json:"breakeven_enabled"`
	BreakevenTrigger  float64   `json:"breakeven_trigger"`
	TakeProfitPercent float64   `json:"take_profit_percent,omitempty"`
	Description       string    `json:"description,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// AdvancedPortfolioMetrics contains comprehensive portfolio analysis
type AdvancedPortfolioMetrics struct {
	// Risk-adjusted returns
	SharpeRatio  float64 `json:"sharpe_ratio"`
	CalmarRatio  float64 `json:"calmar_ratio"`
	SortinoRatio float64 `json:"sortino_ratio"`

	// Drawdown analysis
	MaxDrawdown        float64 `json:"max_drawdown"`
	MaxDrawdownPercent float64 `json:"max_drawdown_percent"`

	// Value at Risk
	VaR95 float64 `json:"var_95"`
	VaR99 float64 `json:"var_99"`

	// Trading performance
	WinRate           float64 `json:"win_rate"`
	ProfitFactor      float64 `json:"profit_factor"`
	AverageWin        float64 `json:"average_win"`
	AverageLoss       float64 `json:"average_loss"`
	LargestWin        float64 `json:"largest_win"`
	LargestLoss       float64 `json:"largest_loss"`
	ConsecutiveWins   int     `json:"consecutive_wins"`
	ConsecutiveLosses int     `json:"consecutive_losses"`

	// Trade statistics
	TotalTrades     int     `json:"total_trades"`
	WinningTrades   int     `json:"winning_trades"`
	LosingTrades    int     `json:"losing_trades"`
	TotalProfit     float64 `json:"total_profit"`
	TotalLoss       float64 `json:"total_loss"`
	AverageReturn   float64 `json:"average_return"`
	StandardDeviation float64 `json:"standard_deviation"`

	// Analysis metadata
	AnalysisPeriodDays int    `json:"analysis_period_days"`
	Message           string `json:"message,omitempty"`
}

// RebalanceRecommendation suggests portfolio rebalancing actions
type RebalanceRecommendation struct {
	Asset         string  `json:"asset"`
	CurrentWeight float64 `json:"current_weight"`
	TargetWeight  float64 `json:"target_weight"`
	Action        string  `json:"action"` // "buy", "sell", "hold"
	Amount        float64 `json:"amount"`
	Priority      string  `json:"priority"` // "high", "medium", "low"
}

// PortfolioRiskMetrics contains risk-focused portfolio analysis
type PortfolioRiskMetrics struct {
	ConcentrationRisk     float64 `json:"concentration_risk"`     // Herfindahl-Hirschman Index
	MaxAssetConcentration float64 `json:"max_asset_concentration"` // Percentage
	MostConcentratedAsset string  `json:"most_concentrated_asset"`
	RiskLevel            string  `json:"risk_level"`             // "low", "medium", "high"
	Diversification      float64 `json:"diversification"`        // 1 - HHI
	NumberOfAssets       int     `json:"number_of_assets"`
	TotalValue           float64 `json:"total_value"`
	Message              string  `json:"message,omitempty"`
}

// StressTestResult contains results of portfolio stress testing
type StressTestResult struct {
	ScenarioName    string  `json:"scenario_name"`
	PortfolioImpact float64 `json:"portfolio_impact"`
	ImpactPercent   float64 `json:"impact_percent"`
	NewValue        float64 `json:"new_value"`
}

// TrailingStopOrder represents a trailing stop order
type TrailingStopOrder struct {
	ID           string    `json:"id"`
	Exchange     string    `json:"exchange"`
	Pair         string    `json:"pair"`
	Side         string    `json:"side"`
	Amount       float64   `json:"amount"`
	TrailAmount  float64   `json:"trail_amount"`
	TrailType    string    `json:"trail_type"` // "percentage" or "fixed"
	CurrentPrice float64   `json:"current_price"`
	StopPrice    float64   `json:"stop_price"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// FundingRateData contains funding rate information
type FundingRateData struct {
	Exchange     string    `json:"exchange"`
	Pair         string    `json:"pair"`
	FundingRate  float64   `json:"funding_rate"`
	NextFunding  time.Time `json:"next_funding"`
	Timestamp    time.Time `json:"timestamp"`
	Predicted    float64   `json:"predicted,omitempty"`
	Historical   []HistoricalFundingRate `json:"historical,omitempty"`
}

// HistoricalFundingRate contains historical funding rate data
type HistoricalFundingRate struct {
	Timestamp   time.Time `json:"timestamp"`
	FundingRate float64   `json:"funding_rate"`
}

// FundingRateAlert represents a funding rate alert configuration
type FundingRateAlert struct {
	ID        string    `json:"id"`
	Exchange  string    `json:"exchange"`
	Pair      string    `json:"pair"`
	Threshold float64   `json:"threshold"`
	Direction string    `json:"direction"` // "above", "below"
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// NotificationConfig contains push notification configuration
type NotificationConfig struct {
	FCMServerKey string `json:"fcm_server_key"`
	FCMEndpoint  string `json:"fcm_endpoint"`
	APNSKeyID    string `json:"apns_key_id,omitempty"`
	APNSTeamID   string `json:"apns_team_id,omitempty"`
	APNSKeyPath  string `json:"apns_key_path,omitempty"`
	Enabled      bool   `json:"enabled"`
}

// NotificationPreferences contains user notification preferences
type NotificationPreferences struct {
	UserID           string   `json:"user_id"`
	DeviceTokens     []string `json:"device_tokens"`
	EnabledChannels  []string `json:"enabled_channels"`  // "push", "email", "sms", "webhook"
	TradingAlerts    bool     `json:"trading_alerts"`
	PriceAlerts      bool     `json:"price_alerts"`
	RiskAlerts       bool     `json:"risk_alerts"`
	SystemAlerts     bool     `json:"system_alerts"`
	QuietHoursStart  string   `json:"quiet_hours_start,omitempty"`
	QuietHoursEnd    string   `json:"quiet_hours_end,omitempty"`
	Timezone         string   `json:"timezone,omitempty"`
}

// Trade represents a completed trade
type Trade struct {
	ID         string    `json:"id"`
	Exchange   string    `json:"exchange"`
	Pair       string    `json:"pair"`
	Side       string    `json:"side"`
	Amount     float64   `json:"amount"`
	EntryPrice float64   `json:"entry_price"`
	ExitPrice  float64   `json:"exit_price,omitempty"`
	PnL        float64   `json:"pnl"`
	Status     string    `json:"status"`
	OpenedAt   time.Time `json:"opened_at"`
	ClosedAt   time.Time `json:"closed_at,omitempty"`
}

// WebSocketSubscription represents a WebSocket subscription
type WebSocketSubscription struct {
	Channel   string                 `json:"channel"`
	Symbol    string                 `json:"symbol,omitempty"`
	Exchange  string                 `json:"exchange,omitempty"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Active    bool                   `json:"active"`
	CreatedAt time.Time              `json:"created_at"`
}

// MarketDataSubscription represents market data subscription preferences
type MarketDataSubscription struct {
	UserID    string   `json:"user_id"`
	Symbols   []string `json:"symbols"`
	Channels  []string `json:"channels"` // "price", "orderbook", "trades", "ticker"
	Exchange  string   `json:"exchange"`
	Active    bool     `json:"active"`
	UpdatedAt time.Time `json:"updated_at"`
}