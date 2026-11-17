package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

// User represents a trading user
type User struct {
	ID        string    `json:"id" db:"id"`
	TenantID  string    `json:"tenant_id" db:"tenant_id"`
	CompanyID string    `json:"company_id" db:"company_id"`
	Level     int       `json:"level" db:"level"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Exchange represents an exchange configuration
type Exchange struct {
	ID         string            `json:"id" db:"id"`
	Name       string            `json:"name" db:"name"`
	Status     ExchangeStatus    `json:"status" db:"status"`
	APIVersion string            `json:"api_version" db:"api_version"`
	RateLimit  int               `json:"rate_limit" db:"rate_limit"`
	Features   []ExchangeFeature `json:"features" db:"features"`
	CreatedAt  time.Time         `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time         `json:"updated_at" db:"updated_at"`
}

// Order represents a trading order
type Order struct {
	ID           string          `json:"id" db:"id"`
	UserID       string          `json:"user_id" db:"user_id"`
	ExchangeID   string          `json:"exchange_id" db:"exchange_id"`
	Symbol       string          `json:"symbol" db:"symbol"`
	Side         OrderSide       `json:"side" db:"side"`
	Type         OrderType       `json:"type" db:"type"`
	Amount       decimal.Decimal `json:"amount" db:"amount"`
	Price        decimal.Decimal `json:"price" db:"price"`
	FilledAmount decimal.Decimal `json:"filled_amount" db:"filled_amount"`
	Status       OrderStatus     `json:"status" db:"status"`
	TimeInForce  TimeInForce     `json:"time_in_force" db:"time_in_force"`
	StopPrice    decimal.Decimal `json:"stop_price" db:"stop_price"`
	Leverage     int             `json:"leverage" db:"leverage"`
	CreatedAt    time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at" db:"updated_at"`
	FilledAt     *time.Time      `json:"filled_at" db:"filled_at"`
}

// Position represents an open trading position
type Position struct {
	ID            string          `json:"id" db:"id"`
	UserID        string          `json:"user_id" db:"user_id"`
	ExchangeID    string          `json:"exchange_id" db:"exchange_id"`
	Symbol        string          `json:"symbol" db:"symbol"`
	Side          PositionSide    `json:"side" db:"side"`
	Size          decimal.Decimal `json:"size" db:"size"`
	EntryPrice    decimal.Decimal `json:"entry_price" db:"entry_price"`
	CurrentPrice  decimal.Decimal `json:"current_price" db:"current_price"`
	UnrealizedPnL decimal.Decimal `json:"unrealized_pnl" db:"unrealized_pnl"`
	RealizedPnL   decimal.Decimal `json:"realized_pnl" db:"realized_pnl"`
	Leverage      int             `json:"leverage" db:"leverage"`
	MarginUsed    decimal.Decimal `json:"margin_used" db:"margin_used"`
	CreatedAt     time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at" db:"updated_at"`
}

// Balance represents account balance
type Balance struct {
	UserID     string          `json:"user_id" db:"user_id"`
	ExchangeID string          `json:"exchange_id" db:"exchange_id"`
	Asset      string          `json:"asset" db:"asset"`
	Free       decimal.Decimal `json:"free" db:"free"`
	Locked     decimal.Decimal `json:"locked" db:"locked"`
	Total      decimal.Decimal `json:"total" db:"total"`
	UpdatedAt  time.Time       `json:"updated_at" db:"updated_at"`
}

// Strategy represents a trading strategy
type Strategy struct {
	ID          string                 `json:"id" db:"id"`
	UserID      string                 `json:"user_id" db:"user_id"`
	Name        string                 `json:"name" db:"name"`
	Type        StrategyType           `json:"type" db:"type"`
	Parameters  map[string]interface{} `json:"parameters" db:"parameters"`
	Status      StrategyStatus         `json:"status" db:"status"`
	Performance *StrategyPerformance   `json:"performance,omitempty"`
	CreatedAt   time.Time              `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time              `json:"updated_at" db:"updated_at"`
}

// StrategyPerformance represents strategy performance metrics
type StrategyPerformance struct {
	TotalReturn      decimal.Decimal `json:"total_return"`
	SharpeRatio      float64         `json:"sharpe_ratio"`
	MaxDrawdown      float64         `json:"max_drawdown"`
	WinRate          float64         `json:"win_rate"`
	TotalTrades      int             `json:"total_trades"`
	ProfitableTrades int             `json:"profitable_trades"`
	AverageReturn    decimal.Decimal `json:"average_return"`
	VolatilityAnnual float64         `json:"volatility_annual"`
}

// Backtest represents a backtest result
type Backtest struct {
	ID             string                 `json:"id" db:"id"`
	StrategyID     string                 `json:"strategy_id" db:"strategy_id"`
	UserID         string                 `json:"user_id" db:"user_id"`
	StartDate      time.Time              `json:"start_date" db:"start_date"`
	EndDate        time.Time              `json:"end_date" db:"end_date"`
	InitialBalance decimal.Decimal        `json:"initial_balance" db:"initial_balance"`
	FinalBalance   decimal.Decimal        `json:"final_balance" db:"final_balance"`
	Commission     float64                `json:"commission" db:"commission"`
	Slippage       float64                `json:"slippage" db:"slippage"`
	Metrics        *StrategyPerformance   `json:"metrics,omitempty"`
	Parameters     map[string]interface{} `json:"parameters" db:"parameters"`
	Status         BacktestStatus         `json:"status" db:"status"`
	CreatedAt      time.Time              `json:"created_at" db:"created_at"`
	CompletedAt    *time.Time             `json:"completed_at" db:"completed_at"`
}

// MarketData represents market data point
type MarketData struct {
	Symbol    string          `json:"symbol"`
	Exchange  string          `json:"exchange"`
	Price     decimal.Decimal `json:"price"`
	Volume    decimal.Decimal `json:"volume"`
	High24h   decimal.Decimal `json:"high_24h"`
	Low24h    decimal.Decimal `json:"low_24h"`
	Change24h decimal.Decimal `json:"change_24h"`
	Timestamp time.Time       `json:"timestamp"`
}

// OHLCV represents OHLCV candlestick data
type OHLCV struct {
	Symbol    string          `json:"symbol"`
	Exchange  string          `json:"exchange"`
	Timestamp time.Time       `json:"timestamp"`
	Open      decimal.Decimal `json:"open"`
	High      decimal.Decimal `json:"high"`
	Low       decimal.Decimal `json:"low"`
	Close     decimal.Decimal `json:"close"`
	Volume    decimal.Decimal `json:"volume"`
	Interval  string          `json:"interval"`
}

// OrderBook represents order book data
type OrderBook struct {
	Symbol    string          `json:"symbol"`
	Exchange  string          `json:"exchange"`
	Bids      []OrderBookItem `json:"bids"`
	Asks      []OrderBookItem `json:"asks"`
	Timestamp time.Time       `json:"timestamp"`
}

// OrderBookItem represents a single order book entry
type OrderBookItem struct {
	Price  decimal.Decimal `json:"price"`
	Amount decimal.Decimal `json:"amount"`
}

// Trade represents a trade execution
type Trade struct {
	ID        string          `json:"id"`
	Symbol    string          `json:"symbol"`
	Exchange  string          `json:"exchange"`
	Price     decimal.Decimal `json:"price"`
	Amount    decimal.Decimal `json:"amount"`
	Side      OrderSide       `json:"side"`
	Timestamp time.Time       `json:"timestamp"`
}

// TechnicalIndicator represents a technical indicator value
type TechnicalIndicator struct {
	Symbol    string          `json:"symbol"`
	Indicator string          `json:"indicator"`
	Value     decimal.Decimal `json:"value"`
	Timestamp time.Time       `json:"timestamp"`
	Period    int             `json:"period,omitempty"`
}

// MACD represents MACD indicator values
type MACD struct {
	MACD      decimal.Decimal `json:"macd"`
	Signal    decimal.Decimal `json:"signal"`
	Histogram decimal.Decimal `json:"histogram"`
	Timestamp time.Time       `json:"timestamp"`
}

// BollingerBands represents Bollinger Bands values
type BollingerBands struct {
	Upper     decimal.Decimal `json:"upper"`
	Middle    decimal.Decimal `json:"middle"`
	Lower     decimal.Decimal `json:"lower"`
	Timestamp time.Time       `json:"timestamp"`
}

// RiskMetrics represents portfolio risk metrics
type RiskMetrics struct {
	UserID          string                     `json:"user_id"`
	PortfolioValue  decimal.Decimal            `json:"portfolio_value"`
	MaxDrawdown     float64                    `json:"max_drawdown"`
	SharpeRatio     float64                    `json:"sharpe_ratio"`
	VaR95           decimal.Decimal            `json:"var_95"`
	ExposureByAsset map[string]decimal.Decimal `json:"exposure_by_asset"`
	Timestamp       time.Time                  `json:"timestamp"`
}

// Alert represents a trading alert
type Alert struct {
	ID          string      `json:"id" db:"id"`
	UserID      string      `json:"user_id" db:"user_id"`
	Type        AlertType   `json:"type" db:"type"`
	Symbol      string      `json:"symbol" db:"symbol"`
	Condition   string      `json:"condition" db:"condition"`
	Value       string      `json:"value" db:"value"`
	Status      AlertStatus `json:"status" db:"status"`
	Message     string      `json:"message" db:"message"`
	CreatedAt   time.Time   `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at" db:"updated_at"`
	TriggeredAt *time.Time  `json:"triggered_at" db:"triggered_at"`
}

// APICost represents the cost of an external API call
type APICost struct {
	ID           uint            `json:"id" gorm:"primaryKey"`
	Provider     string          `json:"provider" gorm:"index;not null"`          // "openai", "anthropic", "google", "cryptopanic", etc.
	Cost         decimal.Decimal `json:"cost" gorm:"type:decimal(10,4);not null"` // Cost in USD
	CallType     string          `json:"call_type"`                               // "market_analysis", "sentiment", "explanation", etc.
	InputTokens  int             `json:"input_tokens"`                            // For LLM calls
	OutputTokens int             `json:"output_tokens"`                           // For LLM calls
	Model        string          `json:"model"`                                   // Model used (e.g., "gpt-4", "claude-3-opus")
	Success      bool            `json:"success" gorm:"default:true"`             // Whether the call succeeded
	ErrorMessage string          `json:"error_message"`                           // Error message if failed
	Metadata     string          `json:"metadata" gorm:"type:jsonb"`              // Additional metadata as JSON
	CreatedAt    time.Time       `json:"created_at" gorm:"index;not null"`
}

// TableName specifies the table name for APICost
func (APICost) TableName() string {
	return "api_costs"
}
