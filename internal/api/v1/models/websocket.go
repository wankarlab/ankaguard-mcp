// Package models contains request and response DTOs for REST API v1
package models

import (
	"encoding/json"
	"time"
)

// WebSocket Message Types
const (
	WSMessageTypeWelcome      = "welcome"
	WSMessageTypeAuthSuccess  = "auth_success"
	WSMessageTypeSubscribed   = "subscribed"
	WSMessageTypeUnsubscribed = "unsubscribed"
	WSMessageTypeError        = "error"
	WSMessageTypePong         = "pong"
	WSMessageTypeTicker       = "ticker"
	WSMessageTypeTrade        = "trade"
	WSMessageTypeOrderbook    = "orderbook"
	WSMessageTypeBotStatus    = "bot_status"
	WSMessageTypePosition     = "position"
	WSMessageTypeOrder        = "order"
	WSMessageTypeBalance      = "balance"
)

// WebSocket Action Types
const (
	WSActionAuth        = "auth"
	WSActionSubscribe   = "subscribe"
	WSActionUnsubscribe = "unsubscribe"
	WSActionPing        = "ping"
)

// WSMessage represents a generic WebSocket message
type WSMessage struct {
	Type      string          `json:"type"`
	Channel   string          `json:"channel,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	RequestID string          `json:"request_id,omitempty"`
}

// WSClientMessage represents an incoming message from a client
type WSClientMessage struct {
	Action    string   `json:"action"`
	Channels  []string `json:"channels,omitempty"`
	Token     string   `json:"token,omitempty"`
	RequestID string   `json:"request_id,omitempty"`
}

// WSWelcomeData represents welcome message data
type WSWelcomeData struct {
	ClientID      string   `json:"client_id"`
	Authenticated bool     `json:"authenticated"`
	Channels      []string `json:"available_channels"`
}

// WSAuthSuccessData represents auth success data
type WSAuthSuccessData struct {
	UserID    string `json:"user_id"`
	TenantID  string `json:"tenant_id"`
	Level     int    `json:"level"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// WSSubscribedData represents subscription result data
type WSSubscribedData struct {
	Subscribed []string `json:"subscribed"`
	Failed     []string `json:"failed,omitempty"`
}

// WSErrorData represents error data
type WSErrorData struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// WSTickerUpdate represents a ticker update message
type WSTickerUpdate struct {
	Exchange         string    `json:"exchange"`
	Pair             string    `json:"pair"`
	Last             float64   `json:"last"`
	Bid              float64   `json:"bid"`
	Ask              float64   `json:"ask"`
	High24h          float64   `json:"high_24h"`
	Low24h           float64   `json:"low_24h"`
	Volume           float64   `json:"volume"`
	Change24h        float64   `json:"change_24h"`
	ChangePercent24h float64   `json:"change_percent_24h"`
	MarkPrice        float64   `json:"mark_price,omitempty"`
	IndexPrice       float64   `json:"index_price,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
}

// WSTradeUpdate represents a trade update message
type WSTradeUpdate struct {
	Exchange  string    `json:"exchange"`
	Pair      string    `json:"pair"`
	TradeID   string    `json:"trade_id"`
	Price     float64   `json:"price"`
	Amount    float64   `json:"amount"`
	Side      string    `json:"side"`
	Timestamp time.Time `json:"timestamp"`
}

// WSOrderbookUpdate represents an orderbook update message
type WSOrderbookUpdate struct {
	Exchange  string              `json:"exchange"`
	Pair      string              `json:"pair"`
	Bids      []WSOrderbookLevel  `json:"bids"`
	Asks      []WSOrderbookLevel  `json:"asks"`
	Timestamp time.Time           `json:"timestamp"`
}

// WSOrderbookLevel represents a single orderbook level
type WSOrderbookLevel struct {
	Price  float64 `json:"price"`
	Amount float64 `json:"amount"`
}

// WSBotStatusUpdate represents a bot status update message
type WSBotStatusUpdate struct {
	BotID         string     `json:"bot_id"`
	Name          string     `json:"name"`
	Status        string     `json:"status"`
	IsActive      bool       `json:"is_active"`
	Exchange      string     `json:"exchange"`
	TradingPairs  []string   `json:"trading_pairs"`
	Strategy      string     `json:"strategy"`
	LastStartedAt *time.Time `json:"last_started_at,omitempty"`
	LastStoppedAt *time.Time `json:"last_stopped_at,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
	ErrorCount    int        `json:"error_count"`
	TotalTrades   int64      `json:"total_trades"`
	TotalPnL      float64    `json:"total_pnl"`
	Timestamp     time.Time  `json:"timestamp"`
}

// WSPositionUpdate represents a position update message
type WSPositionUpdate struct {
	ID               string    `json:"id"`
	Exchange         string    `json:"exchange"`
	Pair             string    `json:"pair"`
	Side             string    `json:"side"`
	Size             float64   `json:"size"`
	EntryPrice       float64   `json:"entry_price"`
	CurrentPrice     float64   `json:"current_price"`
	MarkPrice        float64   `json:"mark_price,omitempty"`
	Leverage         float64   `json:"leverage,omitempty"`
	Margin           float64   `json:"margin,omitempty"`
	UnrealizedPnL    float64   `json:"unrealized_pnl"`
	RealizedPnL      float64   `json:"realized_pnl"`
	LiquidationPrice float64   `json:"liquidation_price,omitempty"`
	StopLoss         float64   `json:"stop_loss,omitempty"`
	TakeProfit       float64   `json:"take_profit,omitempty"`
	UpdateType       string    `json:"update_type"` // "opened", "updated", "closed"
	Timestamp        time.Time `json:"timestamp"`
}

// WSOrderUpdate represents an order update message
type WSOrderUpdate struct {
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
	UpdateType  string    `json:"update_type"` // "created", "updated", "filled", "cancelled"
	Timestamp   time.Time `json:"timestamp"`
}

// WSBalanceUpdate represents a balance update message
type WSBalanceUpdate struct {
	Exchange  string    `json:"exchange"`
	Currency  string    `json:"currency"`
	Available float64   `json:"available"`
	Locked    float64   `json:"locked"`
	Total     float64   `json:"total"`
	ValueUSD  float64   `json:"value_usd,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// NewWSMessage creates a new WebSocket message
func NewWSMessage(msgType, channel string, data interface{}) (*WSMessage, error) {
	var rawData json.RawMessage
	if data != nil {
		bytes, err := json.Marshal(data)
		if err != nil {
			return nil, err
		}
		rawData = bytes
	}

	return &WSMessage{
		Type:      msgType,
		Channel:   channel,
		Data:      rawData,
		Timestamp: time.Now(),
	}, nil
}

// NewWSTickerMessage creates a ticker update message
func NewWSTickerMessage(pair string, ticker *WSTickerUpdate) ([]byte, error) {
	msg, err := NewWSMessage(WSMessageTypeTicker, "ticker:"+pair, ticker)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// NewWSTradeMessage creates a trade update message
func NewWSTradeMessage(pair string, trade *WSTradeUpdate) ([]byte, error) {
	msg, err := NewWSMessage(WSMessageTypeTrade, "trades:"+pair, trade)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// NewWSOrderbookMessage creates an orderbook update message
func NewWSOrderbookMessage(pair string, orderbook *WSOrderbookUpdate) ([]byte, error) {
	msg, err := NewWSMessage(WSMessageTypeOrderbook, "orderbook:"+pair, orderbook)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// NewWSBotStatusMessage creates a bot status update message
func NewWSBotStatusMessage(botID string, status *WSBotStatusUpdate) ([]byte, error) {
	msg, err := NewWSMessage(WSMessageTypeBotStatus, "bot:"+botID, status)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// NewWSPositionMessage creates a position update message
func NewWSPositionMessage(position *WSPositionUpdate) ([]byte, error) {
	msg, err := NewWSMessage(WSMessageTypePosition, "positions", position)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// NewWSOrderMessage creates an order update message
func NewWSOrderMessage(order *WSOrderUpdate) ([]byte, error) {
	msg, err := NewWSMessage(WSMessageTypeOrder, "orders", order)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// NewWSBalanceMessage creates a balance update message
func NewWSBalanceMessage(balance *WSBalanceUpdate) ([]byte, error) {
	msg, err := NewWSMessage(WSMessageTypeBalance, "balances", balance)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// NewWSErrorMessage creates an error message
func NewWSErrorMessage(code, message, details string) ([]byte, error) {
	errData := &WSErrorData{
		Code:    code,
		Message: message,
		Details: details,
	}
	msg, err := NewWSMessage(WSMessageTypeError, "", errData)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}
