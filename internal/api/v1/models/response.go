// Package models contains request and response DTOs for REST API v1
package models

import "time"

// APIResponse is a standard response wrapper for all API responses
type APIResponse struct {
	Success   bool        `json:"success"`
	Data      interface{} `json:"data,omitempty"`
	Error     *APIError   `json:"error,omitempty"`
	Meta      *APIMeta    `json:"meta,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}

// APIError represents an error in the API response
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// APIMeta contains pagination and other metadata
type APIMeta struct {
	Total  int `json:"total,omitempty"`
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
	Count  int `json:"count,omitempty"`
}

// HealthResponse represents the health check response
type HealthResponse struct {
	Status    string    `json:"status"`
	Service   string    `json:"service"`
	Version   string    `json:"version"`
	Uptime    int64     `json:"uptime_seconds"`
	Timestamp time.Time `json:"timestamp"`
}

// PortfolioResponse represents the user's portfolio summary
type PortfolioResponse struct {
	TotalValueUSD float64                     `json:"total_value_usd"`
	Holdings      map[string]*HoldingResponse `json:"holdings"`
	PnL           float64                     `json:"pnl"`
	PnLPercent    float64                     `json:"pnl_percent"`
	Change24h     float64                     `json:"change_24h,omitempty"`
	UpdatedAt     time.Time                   `json:"updated_at"`
}

// HoldingResponse represents a single holding in the portfolio
type HoldingResponse struct {
	Currency  string  `json:"currency"`
	Amount    float64 `json:"amount"`
	ValueUSD  float64 `json:"value_usd"`
	AvgPrice  float64 `json:"avg_price,omitempty"`
	PnL       float64 `json:"pnl,omitempty"`
	PnLPercent float64 `json:"pnl_percent,omitempty"`
}

// BalanceResponse represents a balance for a specific exchange and currency
type BalanceResponse struct {
	Exchange  string  `json:"exchange"`
	Currency  string  `json:"currency"`
	Available float64 `json:"available"`
	Locked    float64 `json:"locked"`
	Total     float64 `json:"total"`
	ValueUSD  float64 `json:"value_usd,omitempty"`
}

// BalancesListResponse represents a list of balances
type BalancesListResponse struct {
	Balances     []BalanceResponse `json:"balances"`
	TotalValueUSD float64          `json:"total_value_usd"`
}

// PositionResponse represents an open trading position
type PositionResponse struct {
	ID              string    `json:"id"`
	Exchange        string    `json:"exchange"`
	Pair            string    `json:"pair"`
	Side            string    `json:"side"`
	Size            float64   `json:"size"`
	EntryPrice      float64   `json:"entry_price"`
	CurrentPrice    float64   `json:"current_price"`
	MarkPrice       float64   `json:"mark_price,omitempty"`
	Leverage        float64   `json:"leverage,omitempty"`
	Margin          float64   `json:"margin,omitempty"`
	UnrealizedPnL   float64   `json:"unrealized_pnl"`
	RealizedPnL     float64   `json:"realized_pnl"`
	LiquidationPrice float64  `json:"liquidation_price,omitempty"`
	StopLoss        float64   `json:"stop_loss,omitempty"`
	TakeProfit      float64   `json:"take_profit,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// PositionsListResponse represents a list of positions
type PositionsListResponse struct {
	Positions       []PositionResponse `json:"positions"`
	TotalUnrealizedPnL float64         `json:"total_unrealized_pnl"`
	TotalRealizedPnL   float64         `json:"total_realized_pnl"`
}

// OrderResponse represents an order
type OrderResponse struct {
	OrderID     string    `json:"order_id"`
	ClientID    string    `json:"client_id,omitempty"`
	Exchange    string    `json:"exchange"`
	Pair        string    `json:"pair"`
	Side        string    `json:"side"`
	Type        string    `json:"type"`
	Status      string    `json:"status"`
	Amount      float64   `json:"amount"`
	Price       float64   `json:"price,omitempty"`
	StopPrice   float64   `json:"stop_price,omitempty"`
	Filled      float64   `json:"filled"`
	Remaining   float64   `json:"remaining"`
	AvgPrice    float64   `json:"avg_price,omitempty"`
	Fee         float64   `json:"fee,omitempty"`
	FeeCurrency string    `json:"fee_currency,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// OrdersListResponse represents a list of orders
type OrdersListResponse struct {
	Orders []OrderResponse `json:"orders"`
}

// TickerResponse represents market ticker data for external API clients
//
// FOR EXTERNAL API USE - This is the CORRECT model to use for all external API responses
// to Flutter, web, and mobile clients. This model includes all fields required by clients.
//
// IMPORTANT: Do NOT confuse this with gct.Ticker (internal/gct/types.go) which is used
// only for internal GoCryptoTrader communication and lacks critical fields like change_24h.
//
// Key features for client applications:
//   - Explicit 24-hour statistics: high_24h, low_24h, change_24h, change_percent_24h
//   - Futures market support: mark_price, index_price (optional)
//   - Volume in USD: volume_usd (optional)
//   - Consistent JSON field naming with snake_case
//
// Flutter clients should parse these exact field names from JSON responses.
// See docs/api/TICKER_MODELS_GUIDE.md for detailed usage guide.
type TickerResponse struct {
	Exchange   string    `json:"exchange"`
	Pair       string    `json:"pair"`
	Last       float64   `json:"last"`                         // Current/last traded price
	Bid        float64   `json:"bid"`                          // Best bid price
	Ask        float64   `json:"ask"`                          // Best ask price
	Open       float64   `json:"open,omitempty"`               // Opening price (24h)
	High24h    float64   `json:"high_24h"`                     // Highest price in last 24h
	Low24h     float64   `json:"low_24h"`                      // Lowest price in last 24h
	Volume     float64   `json:"volume"`                       // 24h volume in base currency
	VolumeUSD  float64   `json:"volume_usd,omitempty"`         // 24h volume in USD
	Change24h  float64   `json:"change_24h"`                   // Absolute price change (e.g., +500.50)
	ChangePercent24h float64 `json:"change_percent_24h,omitempty"` // Percentage change (e.g., 1.12%)
	MarkPrice  float64   `json:"mark_price,omitempty"`         // Mark price for futures
	IndexPrice float64   `json:"index_price,omitempty"`        // Index price for futures
	Timestamp  time.Time `json:"timestamp"`                    // Response timestamp
}

// OrderbookResponse represents orderbook data
type OrderbookResponse struct {
	Exchange  string              `json:"exchange"`
	Pair      string              `json:"pair"`
	Bids      []OrderbookItem     `json:"bids"`
	Asks      []OrderbookItem     `json:"asks"`
	Spread    float64             `json:"spread"`
	SpreadPercent float64         `json:"spread_percent"`
	Timestamp time.Time           `json:"timestamp"`
}

// OrderbookItem represents a single level in the orderbook
type OrderbookItem struct {
	Price  float64 `json:"price"`
	Amount float64 `json:"amount"`
	Total  float64 `json:"total,omitempty"`
}

// CredentialResponse represents stored exchange credentials (without secrets)
type CredentialResponse struct {
	Exchange   string    `json:"exchange"`
	Label      string    `json:"label,omitempty"`
	APIKeyHint string    `json:"api_key_hint"` // Last 4 characters only
	Sandbox    bool      `json:"sandbox"`
	Status     string    `json:"status"` // active, expired, invalid
	CreatedAt  time.Time `json:"created_at"`
	LastUsedAt time.Time `json:"last_used_at,omitempty"`
}

// CredentialsListResponse represents a list of credentials
type CredentialsListResponse struct {
	Credentials []CredentialResponse `json:"credentials"`
}

// NewSuccessResponse creates a successful API response
func NewSuccessResponse(data interface{}) APIResponse {
	return APIResponse{
		Success:   true,
		Data:      data,
		Timestamp: time.Now(),
	}
}

// NewSuccessResponseWithMeta creates a successful API response with metadata
func NewSuccessResponseWithMeta(data interface{}, meta *APIMeta) APIResponse {
	return APIResponse{
		Success:   true,
		Data:      data,
		Meta:      meta,
		Timestamp: time.Now(),
	}
}

// NewErrorResponse creates an error API response
func NewErrorResponse(code, message string) APIResponse {
	return APIResponse{
		Success: false,
		Error: &APIError{
			Code:    code,
			Message: message,
		},
		Timestamp: time.Now(),
	}
}

// NewErrorResponseWithDetails creates an error API response with details
func NewErrorResponseWithDetails(code, message, details string) APIResponse {
	return APIResponse{
		Success: false,
		Error: &APIError{
			Code:    code,
			Message: message,
			Details: details,
		},
		Timestamp: time.Now(),
	}
}

// ==================== AI Analysis Responses ====================

// TradingSignalResponse represents an AI-generated trading signal
type TradingSignalResponse struct {
	Pair            string    `json:"pair"`
	Action          string    `json:"action"`           // BUY, SELL, WAIT
	Confidence      float64   `json:"confidence"`       // 0.0 to 1.0
	Reasoning       string    `json:"reasoning"`        // Explanation of the decision
	EntryPrice      float64   `json:"entry_price"`      // Suggested entry price
	StopLoss        float64   `json:"stop_loss"`        // Suggested stop loss
	TakeProfit      float64   `json:"take_profit"`      // Suggested take profit
	RiskAssessment  string    `json:"risk_assessment"`  // low, medium, high
	MarketCondition string    `json:"market_condition"` // Market state description
	KeyFactors      []string  `json:"key_factors"`      // Key factors in the decision
	Warnings        []string  `json:"warnings"`         // Any warnings or concerns
	Provider        string    `json:"provider"`         // LLM provider used
	Timestamp       time.Time `json:"timestamp"`
}

// SentimentResponse represents market sentiment analysis result
type SentimentResponse struct {
	Symbol       string            `json:"symbol"`
	Overall      float64           `json:"overall"`      // -1.0 (bearish) to +1.0 (bullish)
	Confidence   float64           `json:"confidence"`   // 0.0 to 1.0
	Trend        string            `json:"trend"`        // bullish, bearish, neutral
	Sources      []string          `json:"sources"`      // Sources consulted
	Keywords     []string          `json:"keywords"`     // Key terms identified
	NewsCount    int               `json:"news_count"`   // Number of news articles analyzed
	TweetCount   int               `json:"tweet_count"`  // Number of tweets analyzed
	RedditCount  int               `json:"reddit_count"` // Number of Reddit posts analyzed
	Breakdown    SentimentBreakdown `json:"breakdown"`   // Detailed breakdown by source
	Timestamp    time.Time         `json:"timestamp"`
}

// SentimentBreakdown provides detailed sentiment by source
type SentimentBreakdown struct {
	News    float64 `json:"news"`
	Twitter float64 `json:"twitter"`
	Reddit  float64 `json:"reddit"`
}

// BacktestResponse represents backtest simulation results
type BacktestResponse struct {
	Config          BacktestConfigSummary `json:"config"`
	StartDate       time.Time             `json:"start_date"`
	EndDate         time.Time             `json:"end_date"`
	Duration        string                `json:"duration"`
	InitialBalance  float64               `json:"initial_balance"`
	FinalBalance    float64               `json:"final_balance"`
	TotalReturn     float64               `json:"total_return"`
	TotalReturnPct  float64               `json:"total_return_pct"`
	MaxDrawdown     float64               `json:"max_drawdown"`
	MaxDrawdownPct  float64               `json:"max_drawdown_pct"`
	SharpeRatio     float64               `json:"sharpe_ratio"`
	WinRate         float64               `json:"win_rate"`
	ProfitFactor    float64               `json:"profit_factor"`
	TotalTrades     int                   `json:"total_trades"`
	WinningTrades   int                   `json:"winning_trades"`
	LosingTrades    int                   `json:"losing_trades"`
	AvgWin          float64               `json:"avg_win"`
	AvgLoss         float64               `json:"avg_loss"`
	LargestWin      float64               `json:"largest_win"`
	LargestLoss     float64               `json:"largest_loss"`
	Trades          []BacktestTradeResult `json:"trades,omitempty"`
	EquityCurve     []EquityPointResponse `json:"equity_curve,omitempty"`
}

// BacktestConfigSummary provides a summary of backtest configuration
type BacktestConfigSummary struct {
	Exchange     string  `json:"exchange"`
	Pair         string  `json:"pair"`
	Strategy     string  `json:"strategy"`
	InitialFunds float64 `json:"initial_funds"`
	Commission   float64 `json:"commission"`
}

// BacktestTradeResult represents a single trade in backtest results
type BacktestTradeResult struct {
	ID         int       `json:"id"`
	EntryTime  time.Time `json:"entry_time"`
	ExitTime   time.Time `json:"exit_time"`
	Side       string    `json:"side"`
	EntryPrice float64   `json:"entry_price"`
	ExitPrice  float64   `json:"exit_price"`
	Size       float64   `json:"size"`
	PnL        float64   `json:"pnl"`
	PnLPct     float64   `json:"pnl_pct"`
	Commission float64   `json:"commission"`
	Reason     string    `json:"reason"` // take_profit, stop_loss, signal_reversal
	Duration   string    `json:"duration"`
}

// EquityPointResponse represents a point in the equity curve
type EquityPointResponse struct {
	Time    time.Time `json:"time"`
	Balance float64   `json:"balance"`
	Return  float64   `json:"return_pct"`
}

// IndicatorsResponse represents technical indicators for a trading pair
type IndicatorsResponse struct {
	Exchange  string                   `json:"exchange"`
	Pair      string                   `json:"pair"`
	Interval  string                   `json:"interval"`
	RSI       *RSIIndicator            `json:"rsi,omitempty"`
	MACD      *MACDIndicator           `json:"macd,omitempty"`
	Bollinger *BollingerBandsIndicator `json:"bollinger,omitempty"`
	EMA       map[string]float64       `json:"ema,omitempty"`  // EMA by period: {"20": 0.1234, "50": 0.1200}
	SMA       map[string]float64       `json:"sma,omitempty"`  // SMA by period
	ATR       float64                  `json:"atr,omitempty"`  // Average True Range
	Timestamp time.Time                `json:"timestamp"`
}

// RSIIndicator represents the RSI indicator data
type RSIIndicator struct {
	Value    float64 `json:"value"`
	Period   int     `json:"period"`
	Signal   string  `json:"signal"` // overbought, oversold, neutral
}

// MACDIndicator represents the MACD indicator data
type MACDIndicator struct {
	MACD      float64 `json:"macd"`
	Signal    float64 `json:"signal"`
	Histogram float64 `json:"histogram"`
	Trend     string  `json:"trend"` // bullish, bearish, neutral
}

// BollingerBandsIndicator represents Bollinger Bands indicator data
type BollingerBandsIndicator struct {
	Upper    float64 `json:"upper"`
	Middle   float64 `json:"middle"`
	Lower    float64 `json:"lower"`
	Width    float64 `json:"width"`
	Position string  `json:"position"` // upper, middle, lower, outside_upper, outside_lower
}

// ComprehensiveAnalysisResponse represents a unified analysis response for Flutter
type ComprehensiveAnalysisResponse struct {
	Symbol              string                 `json:"symbol"`
	Exchange            string                 `json:"exchange"`
	Timestamp           time.Time              `json:"timestamp"`
	PriceData           PriceData              `json:"price_data"`
	TechnicalIndicators *IndicatorsResponse    `json:"technical_indicators"`
	Recommendation      string                 `json:"recommendation"` // BUY, SELL, WAIT
	LLMAnalysis         *TradingSignalResponse `json:"llm_analysis,omitempty"`
	SentimentAnalysis   *SentimentResponse     `json:"sentiment_analysis,omitempty"`
}

// PriceData represents current price and volume information
type PriceData struct {
	Last              float64 `json:"last"`
	Bid               float64 `json:"bid"`
	Ask               float64 `json:"ask"`
	Open              float64 `json:"open,omitempty"`
	High24h           float64 `json:"high_24h"`
	Low24h            float64 `json:"low_24h"`
	Volume            float64 `json:"volume"`
	VolumeUSD         float64 `json:"volume_usd,omitempty"`
	Change24h         float64 `json:"change_24h"`
	ChangePercent24h  float64 `json:"change_percent_24h,omitempty"`
}
