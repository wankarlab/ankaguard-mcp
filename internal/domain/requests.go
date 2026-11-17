package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// PlaceOrderRequest represents a request to place an order
type PlaceOrderRequest struct {
	UserID        string          `json:"user_id" validate:"required"`
	Exchange      string          `json:"exchange" validate:"required"`
	Symbol        string          `json:"symbol" validate:"required"`
	Side          OrderSide       `json:"side" validate:"required,oneof=buy sell"`
	Type          OrderType       `json:"type" validate:"required"`
	Amount        decimal.Decimal `json:"amount" validate:"required,gt=0"`
	Price         decimal.Decimal `json:"price,omitempty"`
	StopPrice     decimal.Decimal `json:"stop_price,omitempty"`
	TimeInForce   TimeInForce     `json:"time_in_force,omitempty"`
	Leverage      int             `json:"leverage,omitempty" validate:"min=1,max=125"`
	ReduceOnly    bool            `json:"reduce_only,omitempty"`
	PostOnly      bool            `json:"post_only,omitempty"`
	ClientOrderID string          `json:"client_order_id,omitempty"`
}

// OrderFilter represents filters for querying orders
type OrderFilter struct {
	Symbol    string      `json:"symbol,omitempty"`
	Side      OrderSide   `json:"side,omitempty"`
	Type      OrderType   `json:"type,omitempty"`
	Status    OrderStatus `json:"status,omitempty"`
	StartTime *time.Time  `json:"start_time,omitempty"`
	EndTime   *time.Time  `json:"end_time,omitempty"`
	Limit     int         `json:"limit,omitempty" validate:"min=1,max=1000"`
	Offset    int         `json:"offset,omitempty" validate:"min=0"`
}

// ExchangeConfig represents exchange configuration
type ExchangeConfig struct {
	Name       string `json:"name" validate:"required"`
	APIKey     string `json:"api_key" validate:"required"`
	APISecret  string `json:"api_secret" validate:"required"`
	Passphrase string `json:"passphrase,omitempty"`
	Sandbox    bool   `json:"sandbox"`
	Enabled    bool   `json:"enabled"`
}

// ExchangeStatusInfo represents exchange status information
type ExchangeStatusInfo struct {
	Name         string            `json:"name"`
	Status       ExchangeStatus    `json:"status"`
	LastPing     time.Time         `json:"last_ping"`
	Latency      time.Duration     `json:"latency"`
	RateLimit    int               `json:"rate_limit"`
	RateUsed     int               `json:"rate_used"`
	Features     []ExchangeFeature `json:"features"`
	ErrorMessage string            `json:"error_message,omitempty"`
}

// PriceUpdate represents a price update message
type PriceUpdate struct {
	Symbol    string          `json:"symbol"`
	Exchange  string          `json:"exchange"`
	Price     decimal.Decimal `json:"price"`
	Volume    decimal.Decimal `json:"volume"`
	Change24h decimal.Decimal `json:"change_24h"`
	Timestamp time.Time       `json:"timestamp"`
}

// OHLCVRequest represents a request for OHLCV data
type OHLCVRequest struct {
	Symbol    string    `json:"symbol" validate:"required"`
	Exchange  string    `json:"exchange" validate:"required"`
	Interval  string    `json:"interval" validate:"required,oneof=1m 5m 15m 30m 1h 4h 1d 1w 1M"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Limit     int       `json:"limit,omitempty" validate:"min=1,max=1000"`
}

// TradesRequest represents a request for trade data
type TradesRequest struct {
	Symbol    string    `json:"symbol" validate:"required"`
	Exchange  string    `json:"exchange" validate:"required"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Limit     int       `json:"limit,omitempty" validate:"min=1,max=1000"`
}

// PositionSizeRequest represents a request for position size calculation
type PositionSizeRequest struct {
	UserID            string          `json:"user_id" validate:"required"`
	Symbol            string          `json:"symbol" validate:"required"`
	RiskPerTrade      decimal.Decimal `json:"risk_per_trade" validate:"required,gt=0,lt=1"`
	StopLossPrice     decimal.Decimal `json:"stop_loss_price" validate:"required,gt=0"`
	EntryPrice        decimal.Decimal `json:"entry_price" validate:"required,gt=0"`
	AccountBalance    decimal.Decimal `json:"account_balance" validate:"required,gt=0"`
	UseKellyCriterion bool            `json:"use_kelly_criterion"`
}

// PositionSize represents the calculated position size
type PositionSize struct {
	Amount          decimal.Decimal `json:"amount"`
	Value           decimal.Decimal `json:"value"`
	RiskAmount      decimal.Decimal `json:"risk_amount"`
	RiskPercentage  decimal.Decimal `json:"risk_percentage"`
	KellyFraction   decimal.Decimal `json:"kelly_fraction,omitempty"`
	RecommendedSize decimal.Decimal `json:"recommended_size"`
}

// DrawdownStatus represents drawdown status information
type DrawdownStatus struct {
	UserID          string          `json:"user_id"`
	CurrentDrawdown decimal.Decimal `json:"current_drawdown"`
	MaxDrawdown     decimal.Decimal `json:"max_drawdown"`
	DrawdownLimit   decimal.Decimal `json:"drawdown_limit"`
	IsLimitExceeded bool            `json:"is_limit_exceeded"`
	TradingPaused   bool            `json:"trading_paused"`
	LastUpdate      time.Time       `json:"last_update"`
}

// KellyCriterionRequest represents a request for Kelly Criterion calculation
type KellyCriterionRequest struct {
	WinRate        decimal.Decimal `json:"win_rate" validate:"required,gt=0,lt=1"`
	AverageWin     decimal.Decimal `json:"average_win" validate:"required,gt=0"`
	AverageLoss    decimal.Decimal `json:"average_loss" validate:"required,gt=0"`
	AccountBalance decimal.Decimal `json:"account_balance" validate:"required,gt=0"`
}

// CreateStrategyRequest represents a request to create a strategy
type CreateStrategyRequest struct {
	UserID     string                 `json:"user_id" validate:"required"`
	Name       string                 `json:"name" validate:"required,min=1,max=100"`
	Type       StrategyType           `json:"type" validate:"required"`
	Parameters map[string]interface{} `json:"parameters" validate:"required"`
	Symbols    []string               `json:"symbols" validate:"required,min=1"`
	Exchanges  []string               `json:"exchanges" validate:"required,min=1"`
}

// OptimizationResult represents the result of parameter optimization
type OptimizationResult struct {
	StrategyID       string                 `json:"strategy_id"`
	BestParameters   map[string]interface{} `json:"best_parameters"`
	BestPerformance  *StrategyPerformance   `json:"best_performance"`
	OptimizationRuns int                    `json:"optimization_runs"`
	Duration         time.Duration          `json:"duration"`
	CompletedAt      time.Time              `json:"completed_at"`
}

// BacktestRequest represents a request to run a backtest
type BacktestRequest struct {
	StrategyID     string                 `json:"strategy_id" validate:"required"`
	StartDate      time.Time              `json:"start_date" validate:"required"`
	EndDate        time.Time              `json:"end_date" validate:"required"`
	InitialBalance decimal.Decimal        `json:"initial_balance" validate:"required,gt=0"`
	Commission     decimal.Decimal        `json:"commission" validate:"min=0,max=0.1"`
	Slippage       decimal.Decimal        `json:"slippage" validate:"min=0,max=0.1"`
	Parameters     map[string]interface{} `json:"parameters,omitempty"`
}

// WalkForwardRequest represents a request for walk-forward analysis
type WalkForwardRequest struct {
	StrategyID      string          `json:"strategy_id" validate:"required"`
	StartDate       time.Time       `json:"start_date" validate:"required"`
	EndDate         time.Time       `json:"end_date" validate:"required"`
	InitialBalance  decimal.Decimal `json:"initial_balance" validate:"required,gt=0"`
	InSamplePeriod  time.Duration   `json:"in_sample_period" validate:"required"`
	OutSamplePeriod time.Duration   `json:"out_sample_period" validate:"required"`
	ReoptimizeFreq  time.Duration   `json:"reoptimize_frequency" validate:"required"`
}

// WalkForwardResult represents the result of walk-forward analysis
type WalkForwardResult struct {
	StrategyID         string                 `json:"strategy_id"`
	TotalPeriods       int                    `json:"total_periods"`
	InSampleResults    []*StrategyPerformance `json:"in_sample_results"`
	OutSampleResults   []*StrategyPerformance `json:"out_sample_results"`
	OverallPerformance *StrategyPerformance   `json:"overall_performance"`
	Robustness         decimal.Decimal        `json:"robustness"`
	CompletedAt        time.Time              `json:"completed_at"`
}

// Portfolio represents a user's portfolio
type Portfolio struct {
	UserID          string                     `json:"user_id"`
	TotalValue      decimal.Decimal            `json:"total_value"`
	TotalPnL        decimal.Decimal            `json:"total_pnl"`
	TotalPnLPercent decimal.Decimal            `json:"total_pnl_percent"`
	Positions       []*Position                `json:"positions"`
	Balances        []*Balance                 `json:"balances"`
	AssetAllocation map[string]decimal.Decimal `json:"asset_allocation"`
	LastUpdate      time.Time                  `json:"last_update"`
}

// PortfolioSnapshot represents a portfolio snapshot at a point in time
type PortfolioSnapshot struct {
	UserID     string          `json:"user_id"`
	Value      decimal.Decimal `json:"value"`
	PnL        decimal.Decimal `json:"pnl"`
	PnLPercent decimal.Decimal `json:"pnl_percent"`
	Timestamp  time.Time       `json:"timestamp"`
}

// PerformanceMetrics represents portfolio performance metrics
type PerformanceMetrics struct {
	TotalReturn      decimal.Decimal `json:"total_return"`
	AnnualizedReturn decimal.Decimal `json:"annualized_return"`
	SharpeRatio      decimal.Decimal `json:"sharpe_ratio"`
	MaxDrawdown      decimal.Decimal `json:"max_drawdown"`
	Volatility       decimal.Decimal `json:"volatility"`
	WinRate          decimal.Decimal `json:"win_rate"`
	ProfitFactor     decimal.Decimal `json:"profit_factor"`
	CalmarRatio      decimal.Decimal `json:"calmar_ratio"`
	SortinoRatio     decimal.Decimal `json:"sortino_ratio"`
}

// AssetAllocation represents asset allocation in portfolio
type AssetAllocation struct {
	UserID      string                     `json:"user_id"`
	Allocations map[string]decimal.Decimal `json:"allocations"`
	TotalValue  decimal.Decimal            `json:"total_value"`
	LastUpdate  time.Time                  `json:"last_update"`
}

// CreateAlertRequest represents a request to create an alert
type CreateAlertRequest struct {
	UserID    string    `json:"user_id" validate:"required"`
	Type      AlertType `json:"type" validate:"required"`
	Symbol    string    `json:"symbol" validate:"required"`
	Condition string    `json:"condition" validate:"required"`
	Value     string    `json:"value" validate:"required"`
	Message   string    `json:"message,omitempty"`
}

// UpdateAlertRequest represents a request to update an alert
type UpdateAlertRequest struct {
	Type      AlertType   `json:"type,omitempty"`
	Symbol    string      `json:"symbol,omitempty"`
	Condition string      `json:"condition,omitempty"`
	Value     string      `json:"value,omitempty"`
	Status    AlertStatus `json:"status,omitempty"`
	Message   string      `json:"message,omitempty"`
}

// MCPToolInfo represents information about an MCP tool
type MCPToolInfo struct {
	Name        MCPToolName            `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
	Category    string                 `json:"category"`
}

// WebSocketMessage represents a WebSocket message
type WebSocketMessage struct {
	Type      WebSocketMessageType `json:"type"`
	Data      interface{}          `json:"data"`
	Timestamp time.Time            `json:"timestamp"`
	UserID    string               `json:"user_id,omitempty"`
	RequestID string               `json:"request_id,omitempty"`
}
