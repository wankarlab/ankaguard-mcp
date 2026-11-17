// Package models contains request and response DTOs for REST API v1
package models

import "time"

// CreateOrderRequest represents a request to create a new order
type CreateOrderRequest struct {
	Exchange  string  `json:"exchange" binding:"required"`
	Pair      string  `json:"pair" binding:"required"`
	Side      string  `json:"side" binding:"required,oneof=buy sell"`
	Type      string  `json:"type" binding:"required,oneof=market limit stop stop_limit"`
	Amount    float64 `json:"amount" binding:"required,gt=0"`
	Price     float64 `json:"price,omitempty"`
	StopPrice float64 `json:"stop_price,omitempty"`
	Leverage  float64 `json:"leverage,omitempty"`
	ClientID  string  `json:"client_id,omitempty"`
	TimeInForce string `json:"time_in_force,omitempty"` // GTC, IOC, FOK
}

// ListOrdersRequest represents query parameters for listing orders
type ListOrdersRequest struct {
	Exchange  string    `form:"exchange"`
	Pair      string    `form:"pair"`
	Status    string    `form:"status"`    // open, closed, all
	Side      string    `form:"side"`      // buy, sell
	StartTime time.Time `form:"start_time" time_format:"2006-01-02T15:04:05Z07:00"`
	EndTime   time.Time `form:"end_time" time_format:"2006-01-02T15:04:05Z07:00"`
	Limit     int       `form:"limit,default=50"`
	Offset    int       `form:"offset,default=0"`
}

// GetBalancesRequest represents query parameters for getting balances
type GetBalancesRequest struct {
	Exchange string `form:"exchange"`
	Currency string `form:"currency"`
}

// GetPositionsRequest represents query parameters for getting positions
type GetPositionsRequest struct {
	Exchange string `form:"exchange"`
	Pair     string `form:"pair"`
}

// GetTickerRequest represents path/query parameters for getting ticker
type GetTickerRequest struct {
	Exchange string `uri:"exchange" binding:"required"`
	Pair     string `uri:"pair" binding:"required"`
}

// GetOrderbookRequest represents path/query parameters for getting orderbook
type GetOrderbookRequest struct {
	Exchange string `uri:"exchange" binding:"required"`
	Pair     string `uri:"pair" binding:"required"`
	Depth    int    `form:"depth,default=20"`
}

// AddCredentialsRequest represents a request to add exchange credentials
type AddCredentialsRequest struct {
	Exchange      string `json:"exchange" binding:"required"`
	APIKey        string `json:"api_key" binding:"required"`
	APISecret     string `json:"api_secret" binding:"required"`
	Passphrase    string `json:"passphrase,omitempty"`
	APIPassphrase string `json:"api_passphrase,omitempty"` // accepted as alias
	Label         string `json:"label,omitempty"`
	Sandbox       bool   `json:"sandbox,omitempty"`
}

// DeleteCredentialsRequest represents path parameters for deleting credentials
type DeleteCredentialsRequest struct {
	Exchange string `uri:"exchange" binding:"required"`
}

// CancelOrderRequest represents path parameters for canceling an order
type CancelOrderRequest struct {
	OrderID  string `uri:"id" binding:"required"`
	Exchange string `form:"exchange" binding:"required"`
}

// PaginationRequest represents common pagination parameters
type PaginationRequest struct {
	Limit  int `form:"limit,default=50"`
	Offset int `form:"offset,default=0"`
}

// TimeRangeRequest represents common time range parameters
type TimeRangeRequest struct {
	StartTime time.Time `form:"start_time" time_format:"2006-01-02T15:04:05Z07:00"`
	EndTime   time.Time `form:"end_time" time_format:"2006-01-02T15:04:05Z07:00"`
}

// ==================== AI Analysis Requests ====================

// TradingSignalRequest represents a request for AI trading signal analysis
type TradingSignalRequest struct {
	Exchange   string  `json:"exchange" binding:"required"`
	Pair       string  `json:"pair" binding:"required"`
	Timeframe  string  `json:"timeframe,omitempty"`  // Primary timeframe (default: "15m")
	Timeframes []string `json:"timeframes,omitempty"` // Multiple timeframes for analysis
}

// SentimentAnalysisRequest represents a request for market sentiment analysis
type SentimentAnalysisRequest struct {
	Symbol       string   `json:"symbol" binding:"required"`
	Sources      []string `json:"sources,omitempty"`       // Optional: specify sources (news, twitter, reddit)
	LookbackHours int     `json:"lookback_hours,omitempty"` // How far back to analyze (default: 24)
}

// BacktestRequest represents a request to run a backtest simulation
type BacktestRequest struct {
	Exchange     string    `json:"exchange" binding:"required"`
	Pair         string    `json:"pair" binding:"required"`
	StartDate    time.Time `json:"start_date" binding:"required" time_format:"2006-01-02T15:04:05Z07:00"`
	EndDate      time.Time `json:"end_date" binding:"required" time_format:"2006-01-02T15:04:05Z07:00"`
	InitialFunds float64   `json:"initial_funds" binding:"required,gt=0"`
	Strategy     string    `json:"strategy,omitempty"`     // Strategy name (default: "scalping")
	Commission   float64   `json:"commission,omitempty"`   // Trading fee percentage (default: 0.1)
	Confidence   float64   `json:"confidence,omitempty"`   // Minimum confidence threshold (default: 0.6)
}

// GetIndicatorsRequest represents query parameters for getting technical indicators
type GetIndicatorsRequest struct {
	Exchange   string `form:"exchange"`
	MarketType string `form:"market_type"` // spot, futures
	Interval   string `form:"interval"`    // Candle interval for calculations (default: "1h")
}

// ComprehensiveAnalysisRequest represents a request for comprehensive analysis
type ComprehensiveAnalysisRequest struct {
	Symbol          string `json:"symbol" binding:"required"`
	Exchange        string `json:"exchange,omitempty"`        // Default: kucoin
	EnableLLM       *bool  `json:"enable_llm,omitempty"`      // Default: true
	EnableSentiment *bool  `json:"enable_sentiment,omitempty"` // Default: true
	Interval        string `json:"interval,omitempty"`        // Default: 1h
}
