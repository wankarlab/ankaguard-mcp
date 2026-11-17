package websocket

import (
	"context"
	"log/slog"
	"time"
)

// TradingBroadcaster provides methods to broadcast trading events to WebSocket clients.
type TradingBroadcaster struct {
	hub    *Hub
	logger *slog.Logger
}

// NewTradingBroadcaster creates a new trading broadcaster.
func NewTradingBroadcaster(hub *Hub, logger *slog.Logger) *TradingBroadcaster {
	if logger == nil {
		logger = slog.Default()
	}

	return &TradingBroadcaster{
		hub:    hub,
		logger: logger.With("component", "trading_broadcaster"),
	}
}

// SimpleTrade contains simplified trade information for broadcasting.
type SimpleTrade struct {
	TradeID   string  `json:"trade_id"`
	Pair      string  `json:"pair"`
	Side      string  `json:"side"` // "buy" or "sell"
	Price     float64 `json:"price"`
	Amount    float64 `json:"amount"`
	Total     float64 `json:"total"`
	Fee       float64 `json:"fee"`
	Status    string  `json:"status"` // "executed", "pending", "cancelled"
	Exchange  string  `json:"exchange"`
	Timestamp string  `json:"timestamp"`
}

// SimpleTradeUpdate contains trade status update information.
type SimpleTradeUpdate struct {
	TradeID         string  `json:"trade_id"`
	Status          string  `json:"status"`
	FilledAmount    float64 `json:"filled_amount"`
	RemainingAmount float64 `json:"remaining_amount"`
	Timestamp       string  `json:"timestamp"`
}

// SimpleTradeCancelled contains trade cancellation information.
type SimpleTradeCancelled struct {
	TradeID   string `json:"trade_id"`
	Reason    string `json:"reason"`
	Timestamp string `json:"timestamp"`
}

// SimplePnLUpdate contains P&L update information.
type SimplePnLUpdate struct {
	RealizedPnL   float64        `json:"realized_pnl"`
	UnrealizedPnL float64        `json:"unrealized_pnl"`
	TotalPnL      float64        `json:"total_pnl"`
	DailyPnL      float64        `json:"daily_pnl"`
	TotalBalance  float64        `json:"total_balance"`
	Positions     []PositionInfo `json:"positions,omitempty"`
	Timestamp     string         `json:"timestamp"`
}

// PositionInfo contains information about an open position.
type PositionInfo struct {
	Pair          string  `json:"pair"`
	Side          string  `json:"side"` // "long" or "short"
	EntryPrice    float64 `json:"entry_price"`
	CurrentPrice  float64 `json:"current_price"`
	Amount        float64 `json:"amount"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
}

// SystemNotificationPayload contains system notification information.
type SystemNotificationPayload struct {
	Message   string `json:"message"`
	Level     string `json:"level"` // "info", "warning", "error"
	Timestamp string `json:"timestamp"`
}

// RoundEndedPayload contains information about a trading round end.
type RoundEndedPayload struct {
	RoundID     string  `json:"round_id"`
	EndTime     string  `json:"end_time"`
	TotalTrades int     `json:"total_trades"`
	TotalPnL    float64 `json:"total_pnl"`
	WinRate     float64 `json:"win_rate"`
	Timestamp   string  `json:"timestamp"`
}

// BroadcastSimpleTradeExecuted broadcasts a simple trade execution event.
func (tb *TradingBroadcaster) BroadcastSimpleTradeExecuted(payload *SimpleTrade) {
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().Format(time.RFC3339)
	}

	message := NewWSMessage("trade.executed", payload)
	tb.hub.BroadcastToChannel(ChannelTrades, message)

	tb.logger.Debug("broadcasted trade execution",
		"trade_id", payload.TradeID,
		"pair", payload.Pair,
		"side", payload.Side,
	)
}

// BroadcastSimpleTradeUpdated broadcasts a simple trade status update event.
func (tb *TradingBroadcaster) BroadcastSimpleTradeUpdated(payload *SimpleTradeUpdate) {
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().Format(time.RFC3339)
	}

	message := NewWSMessage("trade.updated", payload)
	tb.hub.BroadcastToChannel(ChannelTrades, message)

	tb.logger.Debug("broadcasted trade update",
		"trade_id", payload.TradeID,
		"status", payload.Status,
	)
}

// BroadcastSimpleTradeCancelled broadcasts a simple trade cancellation event.
func (tb *TradingBroadcaster) BroadcastSimpleTradeCancelled(payload *SimpleTradeCancelled) {
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().Format(time.RFC3339)
	}

	message := NewWSMessage("trade.cancelled", payload)
	tb.hub.BroadcastToChannel(ChannelTrades, message)

	tb.logger.Debug("broadcasted trade cancellation",
		"trade_id", payload.TradeID,
		"reason", payload.Reason,
	)
}

// BroadcastSimplePnLUpdate broadcasts a simple P&L update event.
func (tb *TradingBroadcaster) BroadcastSimplePnLUpdate(payload *SimplePnLUpdate) {
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().Format(time.RFC3339)
	}

	message := NewWSMessage("pnl.update", payload)
	tb.hub.BroadcastToChannel(ChannelPnL, message)

	tb.logger.Debug("broadcasted P&L update",
		"total_pnl", payload.TotalPnL,
		"daily_pnl", payload.DailyPnL,
	)
}

// BroadcastDailyPnL broadcasts a daily P&L summary.
func (tb *TradingBroadcaster) BroadcastDailyPnL(date string, totalPnL float64, tradesCount int, winningTrades int, losingTrades int) {
	winRate := 0.0
	if tradesCount > 0 {
		winRate = float64(winningTrades) / float64(tradesCount) * 100
	}

	payload := map[string]interface{}{
		"date":           date,
		"total_pnl":      totalPnL,
		"trades_count":   tradesCount,
		"winning_trades": winningTrades,
		"losing_trades":  losingTrades,
		"win_rate":       winRate,
		"timestamp":      time.Now().Format(time.RFC3339),
	}

	message := NewWSMessage("pnl.daily", payload)
	tb.hub.BroadcastToChannel(ChannelPnL, message)

	tb.logger.Debug("broadcasted daily P&L",
		"date", date,
		"total_pnl", totalPnL,
		"trades_count", tradesCount,
	)
}

// BroadcastSystemNotification broadcasts a system notification.
func (tb *TradingBroadcaster) BroadcastSystemNotification(payload *SystemNotificationPayload) {
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().Format(time.RFC3339)
	}

	if payload.Level == "" {
		payload.Level = "info"
	}

	message := NewWSMessage("system.notification", payload)
	tb.hub.BroadcastToChannel(ChannelSystem, message)

	tb.logger.Info("broadcasted system notification",
		"message", payload.Message,
		"level", payload.Level,
	)
}

// BroadcastRoundStarted broadcasts a trading round start event using existing payload.
func (tb *TradingBroadcaster) BroadcastRoundStarted(payload *RoundStartedPayload) {
	message := NewWSMessage("round.started", payload)
	tb.hub.BroadcastToChannel(ChannelRounds, message)

	tb.logger.Info("broadcasted round started",
		"round_id", payload.RoundID,
		"symbol", payload.Symbol,
	)
}

// BroadcastRoundEnded broadcasts a trading round end event.
func (tb *TradingBroadcaster) BroadcastRoundEnded(payload *RoundEndedPayload) {
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().Format(time.RFC3339)
	}

	message := NewWSMessage("round.ended", payload)
	tb.hub.BroadcastToChannel(ChannelRounds, message)

	tb.logger.Info("broadcasted round ended",
		"round_id", payload.RoundID,
		"total_trades", payload.TotalTrades,
		"total_pnl", payload.TotalPnL,
	)
}

// BroadcastTradeExecuted broadcasts a trade execution using existing payload.
func (tb *TradingBroadcaster) BroadcastTradeExecuted(payload *TradeExecutedPayload) {
	message := NewWSMessage("trade.executed", payload)
	tb.hub.BroadcastToChannel(ChannelTrades, message)

	tb.logger.Debug("broadcasted trade execution",
		"trade_id", payload.TradeID,
		"symbol", payload.Symbol,
		"side", payload.Side,
	)
}

// BroadcastTradeClosed broadcasts a trade closure using existing payload.
func (tb *TradingBroadcaster) BroadcastTradeClosed(payload *TradeClosedPayload) {
	message := NewWSMessage("trade.closed", payload)
	tb.hub.BroadcastToChannel(ChannelTrades, message)

	tb.logger.Debug("broadcasted trade closed",
		"trade_id", payload.TradeID,
		"pnl", payload.PnL,
	)
}

// BroadcastPnLUpdate broadcasts a P&L update using existing payload.
func (tb *TradingBroadcaster) BroadcastPnLUpdate(payload *PnLUpdatePayload) {
	message := NewWSMessage("pnl.update", payload)
	tb.hub.BroadcastToChannel(ChannelPnL, message)

	tb.logger.Debug("broadcasted P&L update",
		"trade_id", payload.TradeID,
		"unrealized_pnl", payload.UnrealizedPnL,
	)
}

// BroadcastToRoom broadcasts a message to a specific room.
func (tb *TradingBroadcaster) BroadcastToRoom(roomID string, messageType string, payload interface{}) {
	message := NewWSMessage(messageType, payload)
	tb.hub.BroadcastToRoom(roomID, message)

	tb.logger.Debug("broadcasted to room",
		"room_id", roomID,
		"message_type", messageType,
	)
}

// GetHub returns the underlying hub for advanced usage.
func (tb *TradingBroadcaster) GetHub() *Hub {
	return tb.hub
}

// GetClientCount returns the number of connected clients.
func (tb *TradingBroadcaster) GetClientCount() int {
	return tb.hub.GetClientCount()
}

// GetChannelClientCount returns the number of clients subscribed to a channel.
func (tb *TradingBroadcaster) GetChannelClientCount(channel string) int {
	return tb.hub.GetChannelClientCount(channel)
}

// BroadcastSignal sends an AI trading signal over WebSocket. `symbol` is the
// pair being analyzed (e.g. "SUI-USDT") — the Flutter UI needs it to label
// each signal in the feed.
func (tb *TradingBroadcaster) BroadcastSignal(symbol string, action string, confidence float64, price float64, reason string) error {
	payload := map[string]interface{}{
		"symbol":     symbol,
		"pair":       symbol, // alias for older Flutter clients reading "pair"
		"action":     action,
		"confidence": confidence,
		"price":      price,
		"reason":     reason,
		"timestamp":  time.Now().Format(time.RFC3339),
	}
	message := NewWSMessage("signal", payload)
	// Both ChannelSystem and the generic broadcast are fine, but flutter looks for "signal" type.
	tb.hub.BroadcastToChannel(ChannelSystem, message)
	tb.logger.Info("broadcasted AI signal", "symbol", symbol, "action", action, "confidence", confidence)
	return nil
}

// BroadcastPositions broadcasts each position as a position_update WebSocket message.
// Flutter's _handlePositionUpdate processes these individually and updates state.
func (tb *TradingBroadcaster) BroadcastPositions(positions []map[string]interface{}) error {
	for _, pos := range positions {
		message := NewWSMessage("position_update", pos)
		tb.hub.BroadcastToChannel(ChannelSystem, message)
	}
	if len(positions) > 0 {
		tb.logger.Info("broadcasted position updates", "count", len(positions))
	}
	return nil
}

// Require interfaces for AITradingBot NotificationManager

func (tb *TradingBroadcaster) NotifyTradeWithAI(ctx context.Context, trade interface{}, llmCallFunc func(ctx context.Context, prompt string) (string, error)) error {
	return nil
}

func (tb *TradingBroadcaster) NotifyPositionClosedWithAI(ctx context.Context, position interface{}, llmCallFunc func(ctx context.Context, prompt string) (string, error)) error {
	return nil
}
