package gct

import (
	"fmt"
	"time"
)

// Ticker represents market ticker data for internal GCT communication
//
// INTERNAL USE ONLY - This type is used exclusively for communication with GoCryptoTrader engine.
// DO NOT use this type for external API responses to Flutter or other clients.
//
// For external API responses, use models.TickerResponse from internal/api/v1/models/response.go
// which includes required fields like high_24h, low_24h, change_24h, and change_percent_24h.
//
// Key differences from API TickerResponse:
//   - Missing: change_24h, change_percent_24h (must be calculated)
//   - Uses generic 'high'/'low' instead of 'high_24h'/'low_24h'
//   - No futures-specific fields (mark_price, index_price)
//   - No volume_usd field
//
// See docs/api/TICKER_MODELS_GUIDE.md for detailed explanation.
type Ticker struct {
	Exchange    string    `json:"exchange"`
	Pair        string    `json:"pair"`
	Last        float64   `json:"last"`
	Bid         float64   `json:"bid"`
	Ask         float64   `json:"ask"`
	Volume      float64   `json:"volume"`
	High        float64   `json:"high"`         // Generic high (not necessarily 24h)
	Low         float64   `json:"low"`          // Generic low (not necessarily 24h)
	PriceATH    float64   `json:"price_ath"`    // All-time high
	LastUpdated time.Time `json:"last_updated"` // Last update time
	Timestamp   time.Time `json:"timestamp"`
}

// Orderbook represents orderbook data
type Orderbook struct {
	Exchange  string           `json:"exchange"`
	Pair      string           `json:"pair"`
	Bids      []OrderbookEntry `json:"bids"`
	Asks      []OrderbookEntry `json:"asks"`
	Timestamp time.Time        `json:"timestamp"`
}

// OrderbookEntry represents a single orderbook entry
type OrderbookEntry struct {
	Price  float64 `json:"price"`
	Amount float64 `json:"amount"`
}

// Candle represents OHLCV candle data
type Candle struct {
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    float64   `json:"volume"`
}

// AccountInfo represents account information
type AccountInfo struct {
	Exchange string    `json:"exchange"`
	Accounts []Account `json:"accounts"`
	// Paper is true when the data comes from the paper broker, never from the
	// real exchange account (OpenSpec guarded-mcp-product, D3).
	Paper bool `json:"paper,omitempty"`
}

// PaperAssumptions documents how the paper broker priced an execution. It is
// attached to every Order served by the paper broker so a response can state
// the commission and slippage that were applied.
type PaperAssumptions struct {
	SlippagePct     float64 `json:"slippage_pct"`
	TakerFeePct     float64 `json:"taker_fee_pct"`
	FeeUSD          float64 `json:"fee_usd"`
	FundingModeled  bool    `json:"funding_modeled"`
	PartialFills    bool    `json:"partial_fills"`
	LiquidationNote string  `json:"liquidation_note,omitempty"`
}

// Account represents a single account
type Account struct {
	ID       string             `json:"id"`
	Balances map[string]Balance `json:"balances"`
}

// Balance represents a currency balance
type Balance struct {
	Currency  string  `json:"currency"`
	Total     float64 `json:"total"`
	Available float64 `json:"available"`
	Hold      float64 `json:"hold"`
	InOrder   float64 `json:"in_order"` // Alias for Hold
}

// SubmitOrderRequest represents an order submission request
type SubmitOrderRequest struct {
	Exchange      string     `json:"exchange"`
	Pair          string     `json:"pair"`
	Side          string     `json:"side"` // "buy" or "sell"
	Type          string     `json:"type"` // "market", "limit", or "stop"
	Amount        float64    `json:"amount"`
	Price         float64    `json:"price"`
	UserID        string     `json:"user_id,omitempty"`         // H0.3.7: user context for auth failure recording
	ClientID      string     `json:"client_id,omitempty"`       // Optional client order ID
	Leverage      float64    `json:"leverage,omitempty"`        // Leverage for futures (required for KuCoin futures)
	StopPrice     float64    `json:"stop_price,omitempty"`      // Stop price for stop orders
	StopPriceType string     `json:"stop_price_type,omitempty"` // "TP" (trade price) or "MP" (mark price)
	ReduceOnly    bool       `json:"reduce_only,omitempty"`     // Only reduce position (for futures)
	CloseOrder    bool       `json:"close_order,omitempty"`     // Close position in Hedge Mode (KuCoin futures)
	MarketType    MarketType `json:"market_type,omitempty"`     // Market type for symbol conversion (optional)
	// PostOnly requests maker-only (post-only) execution for futures limit orders,
	// so the order is rejected instead of crossing the spread and paying taker fees
	// (~0.06% vs ~0.02% maker on KuCoin). Defaults to false (taker) so existing
	// callers (e.g. the live directional scalper in internal/services/ai_trading_bot.go)
	// are unaffected. Opt-in only, for future maker-execution paths (F2/F5 of the
	// XSEC execution plan). Ignored for market orders (KuCoin requires limit).
	PostOnly bool `json:"post_only,omitempty"`
	// MaxNotionalFactor caps single-contract notional at this multiple of Amount
	// (USD) as a guard against a defaulted contract multiplier blowing the size up
	// (e.g. XMR multiplier=1.0 → $357 notional for a $3.5 target). 0 = use the
	// futures manager's built-in default. Supplied by the caller from dynamic
	// config (risk.max_notional_factor) so the threshold is tunable at runtime.
	MaxNotionalFactor float64 `json:"max_notional_factor,omitempty"`
}

// Order represents an order
type Order struct {
	ID            string     `json:"id"`
	Exchange      string     `json:"exchange"`
	Pair          string     `json:"pair"`
	BaseCurrency  string     `json:"base_currency"`
	QuoteCurrency string     `json:"quote_currency"`
	Side          string     `json:"side"`
	Type          string     `json:"type"`
	Price         float64    `json:"price"`
	Amount        float64    `json:"amount"`
	Filled        float64    `json:"filled"`
	Remaining     float64    `json:"remaining"`
	OpenVolume    float64    `json:"open_volume"` // Alias for Remaining
	Status        string     `json:"status"`
	MarketType    MarketType `json:"market_type,omitempty"` // spot, futures, margin, options
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	UpdateTime    time.Time  `json:"update_time"` // Alias for UpdatedAt
	// Paper is true when the order was served by the paper broker; the server
	// copies it (and PaperAssumptions) into the tool response.
	Paper            bool              `json:"paper,omitempty"`
	PaperAssumptions *PaperAssumptions `json:"paper_assumptions,omitempty"`
}

// Portfolio represents portfolio information
type Portfolio struct {
	TotalValue    float64             `json:"total_value"`
	TotalPnL      float64             `json:"total_pnl"`
	PnL           float64             `json:"pnl"`            // Alias for TotalPnL
	PnLPercentage float64             `json:"pnl_percentage"` // PnL as percentage
	Holdings      map[string]*Holding `json:"holdings"`
	Allocations   map[string]float64  `json:"allocations"` // Asset allocation percentages
	UpdatedAt     time.Time           `json:"updated_at"`
	Paper         bool                `json:"paper,omitempty"` // served by the paper broker
}

// Holding represents a single holding in the portfolio
type Holding struct {
	Currency  string  `json:"currency"`
	Total     float64 `json:"total"`
	Available float64 `json:"available"`
	InOrder   float64 `json:"in_order"`
	Value     float64 `json:"value"`
	PnL       float64 `json:"pnl"`
	PnLPct    float64 `json:"pnl_pct"`
}

// FuturesOrderRequest represents a futures order request
type FuturesOrderRequest struct {
	Symbol        string  `json:"symbol"`                  // e.g., "DOGEUSDTM"
	Side          string  `json:"side"`                    // "buy" or "sell"
	Type          string  `json:"type"`                    // "market" or "limit"
	Size          float64 `json:"size"`                    // Order size (must be > 0)
	Price         float64 `json:"price"`                   // Limit price (optional for market orders)
	Leverage      int     `json:"leverage"`                // Leverage (1-100)
	ClientOID     string  `json:"clientOid"`               // Client order ID
	ReduceOnly    bool    `json:"reduceOnly"`              // Reduce-only flag
	CloseOrder    bool    `json:"closeOrder"`              // Close position in Hedge Mode (KuCoin futures)
	PostOnly      bool    `json:"postOnly"`                // Post-only flag (limit orders only)
	StopPrice     float64 `json:"stopPrice,omitempty"`     // Stop trigger price
	StopPriceType string  `json:"stopPriceType,omitempty"` // "TP" (trade price) or "MP" (mark price)
}

// Validate validates the futures order request
func (r *FuturesOrderRequest) Validate() error {
	if r == nil {
		return fmt.Errorf("futures order request is nil")
	}
	if r.Symbol == "" {
		return fmt.Errorf("symbol is required")
	}
	if r.Side != "buy" && r.Side != "sell" {
		return fmt.Errorf("side must be 'buy' or 'sell', got: %s", r.Side)
	}
	if r.Type != "market" && r.Type != "limit" {
		return fmt.Errorf("type must be 'market' or 'limit', got: %s", r.Type)
	}
	if r.Size <= 0 {
		return fmt.Errorf("size must be greater than 0, got: %.8f", r.Size)
	}
	if r.Type == "limit" && r.Price <= 0 {
		return fmt.Errorf("price must be greater than 0 for limit orders, got: %.8f", r.Price)
	}
	if r.Leverage < 1 || r.Leverage > 100 {
		return fmt.Errorf("leverage must be between 1 and 100, got: %d", r.Leverage)
	}
	if r.PostOnly && r.Type != "limit" {
		return fmt.Errorf("postOnly can only be used with limit orders")
	}
	return nil
}

// Position represents a futures position
type Position struct {
	Symbol           string     `json:"symbol"`
	Side             string     `json:"side"` // "long" or "short"
	Size             float64    `json:"size"`
	EntryPrice       float64    `json:"entry_price"`
	CurrentPrice     float64    `json:"current_price"`
	UnrealizedPnL    float64    `json:"unrealized_pnl"`
	RealizedPnL      float64    `json:"realized_pnl"`
	Leverage         int        `json:"leverage"`
	MarginMode       string     `json:"margin_mode"` // "ISOLATED" or "CROSSED"
	LiquidationPrice float64    `json:"liquidation_price"`
	Margin           float64    `json:"margin"`
	UpdatedAt        time.Time  `json:"updated_at"`
	MarketType       MarketType `json:"market_type,omitempty"` // Market type (futures, spot, etc.)
	Paper            bool       `json:"paper,omitempty"`       // served by the paper broker
}

// IsValid checks if the position has valid data
func (p *Position) IsValid() bool {
	if p == nil {
		return false
	}
	if p.Symbol == "" {
		return false
	}
	if p.Side != "long" && p.Side != "short" {
		return false
	}
	if p.Size <= 0 {
		return false
	}
	if p.Leverage < 1 || p.Leverage > 100 {
		return false
	}
	if p.MarginMode != "ISOLATED" && p.MarginMode != "CROSSED" {
		return false
	}
	return true
}

// CalculatePnLPercent calculates PnL as percentage of margin
func (p *Position) CalculatePnLPercent() float64 {
	if p == nil || p.Margin == 0 {
		return 0
	}
	return (p.UnrealizedPnL / p.Margin) * 100
}
