package models

import "time"

// OrderRequest representa una solicitud de orden
type OrderRequest struct {
	Exchange   string  `json:"exchange"`
	Pair       string  `json:"pair"`
	Side       string  `json:"side"` // "buy" or "sell"
	Type       string  `json:"type"` // "market", "limit", "stop"
	Amount     float64 `json:"amount"`
	Price      float64 `json:"price,omitempty"`
	StopPrice  float64 `json:"stop_price,omitempty"`
	Leverage   float64 `json:"leverage,omitempty"` // Leverage for futures/margin
	ClientID   string  `json:"client_id,omitempty"`
	ReduceOnly bool    `json:"reduce_only,omitempty"` // Only reduce existing position (futures)
	CloseOrder bool    `json:"close_order,omitempty"` // Close position in Hedge Mode (futures)
}

// OrderResponse representa la respuesta de una orden
type OrderResponse struct {
	OrderID   string    `json:"order_id"`
	Exchange  string    `json:"exchange"`
	Pair      string    `json:"pair"`
	Side      string    `json:"side"`
	Type      string    `json:"type"`
	Amount    float64   `json:"amount"`
	Price     float64   `json:"price"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

// Ticker representa información de precio actual
type Ticker struct {
	Exchange   string    `json:"exchange"`
	Pair       string    `json:"pair"`
	Last       float64   `json:"last"`
	Bid        float64   `json:"bid"`
	Ask        float64   `json:"ask"`
	Volume     float64   `json:"volume"`
	High24h    float64   `json:"high_24h"`
	Low24h     float64   `json:"low_24h"`
	Change24h  float64   `json:"change_24h"`
	MarkPrice  float64   `json:"mark_price,omitempty"`  // Para futuros
	IndexPrice float64   `json:"index_price,omitempty"` // Para futuros
	Timestamp  time.Time `json:"timestamp"`
}

// Orderbook representa el libro de órdenes
type Orderbook struct {
	Exchange  string          `json:"exchange"`
	Pair      string          `json:"pair"`
	Bids      []OrderbookItem `json:"bids"`
	Asks      []OrderbookItem `json:"asks"`
	Timestamp time.Time       `json:"timestamp"`
}

// OrderbookItem representa un item en el orderbook
type OrderbookItem struct {
	Price  float64 `json:"price"`
	Amount float64 `json:"amount"`
}

// Candle representa una vela OHLCV
type Candle struct {
	Timestamp time.Time `json:"timestamp"`
	Open      float64   `json:"open"`
	High      float64   `json:"high"`
	Low       float64   `json:"low"`
	Close     float64   `json:"close"`
	Volume    float64   `json:"volume"`
}

// Portfolio representa el portfolio completo
type Portfolio struct {
	TotalValueUSD float64             `json:"total_value_usd"`
	Holdings      map[string]*Holding `json:"holdings"`
	PnL           float64             `json:"pnl"`
	PnLPercent    float64             `json:"pnl_percent"`
	Timestamp     time.Time           `json:"timestamp"`
}

// Holding representa una posición en el portfolio
type Holding struct {
	Currency string  `json:"currency"`
	Amount   float64 `json:"amount"`
	ValueUSD float64 `json:"value_usd"`
	AvgPrice float64 `json:"avg_price"`
}

// Balance representa el balance de una moneda
type Balance struct {
	Exchange  string  `json:"exchange"`
	Currency  string  `json:"currency"`
	Available float64 `json:"available"`
	Locked    float64 `json:"locked"`
	Total     float64 `json:"total"`
}

// MACD representa los valores del indicador MACD
type MACD struct {
	MACD      float64 `json:"macd"`
	Signal    float64 `json:"signal"`
	Histogram float64 `json:"histogram"`
}

// BollingerBands representa las bandas de Bollinger
type BollingerBands struct {
	Upper  float64 `json:"upper"`
	Middle float64 `json:"middle"`
	Lower  float64 `json:"lower"`
}

// BacktestRequest representa una solicitud de backtest
type BacktestRequest struct {
	Exchange   string                 `json:"exchange"`
	Pair       string                 `json:"pair"`
	StartDate  time.Time              `json:"start_date"`
	EndDate    time.Time              `json:"end_date"`
	Strategy   string                 `json:"strategy"`
	Parameters map[string]interface{} `json:"parameters"`
}

// BacktestResult representa el resultado de un backtest
type BacktestResult struct {
	ROI           float64         `json:"roi"`
	SharpeRatio   float64         `json:"sharpe_ratio"`
	MaxDrawdown   float64         `json:"max_drawdown"`
	TotalTrades   int             `json:"total_trades"`
	WinningTrades int             `json:"winning_trades"`
	LosingTrades  int             `json:"losing_trades"`
	Trades        []BacktestTrade `json:"trades"`

	// Metadata fields for GCT integration
	Exchange   string `json:"exchange,omitempty"`
	Pair       string `json:"pair,omitempty"`
	Strategy   string `json:"strategy,omitempty"`
	StartDate  string `json:"start_date,omitempty"`
	EndDate    string `json:"end_date,omitempty"`
	DataSource string `json:"data_source,omitempty"`
}

// BacktestTrade representa un trade en el backtest
type BacktestTrade struct {
	Timestamp time.Time `json:"timestamp"`
	Side      string    `json:"side"`
	Price     float64   `json:"price"`
	Amount    float64   `json:"amount"`
	PnL       float64   `json:"pnl"`
}
