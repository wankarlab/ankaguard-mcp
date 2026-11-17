package websocket

import (
	"log/slog"
	"time"
)

// This file contains example usage of the TradingBroadcaster.
// These examples show how trading services can broadcast events to WebSocket clients.

/*
Example 1: Broadcasting a trade execution

func (s *TradingService) ExecuteTrade(pair string, side string, price, amount float64) error {
	// Execute the trade...
	tradeID := uuid.New().String()
	
	// Broadcast the trade execution to WebSocket clients
	s.broadcaster.BroadcastTradeExecuted(&websocket.TradeExecutedPayload{
		TradeID:   tradeID,
		Pair:      pair,
		Side:      side,
		Price:     price,
		Amount:    amount,
		Total:     price * amount,
		Fee:       (price * amount) * 0.001, // 0.1% fee
		Status:    "executed",
		Exchange:  "kucoin",
		Timestamp: time.Now().Format(time.RFC3339),
	})
	
	return nil
}

Example 2: Broadcasting P&L updates

func (s *TradingService) UpdatePnL() {
	// Calculate P&L...
	realizedPnL := s.calculateRealizedPnL()
	unrealizedPnL := s.calculateUnrealizedPnL()
	
	// Get open positions
	positions := make([]websocket.PositionInfo, 0)
	for _, pos := range s.openPositions {
		positions = append(positions, websocket.PositionInfo{
			Pair:          pos.Pair,
			Side:          pos.Side,
			EntryPrice:    pos.EntryPrice,
			CurrentPrice:  pos.CurrentPrice,
			Amount:        pos.Amount,
			UnrealizedPnL: pos.UnrealizedPnL,
		})
	}
	
	// Broadcast P&L update
	s.broadcaster.BroadcastPnLUpdate(&websocket.PnLUpdatePayload{
		RealizedPnL:   realizedPnL,
		UnrealizedPnL: unrealizedPnL,
		TotalPnL:      realizedPnL + unrealizedPnL,
		DailyPnL:      s.dailyPnL,
		TotalBalance:  s.totalBalance,
		Positions:     positions,
		Timestamp:     time.Now().Format(time.RFC3339),
	})
}

Example 3: Broadcasting system notifications

func (s *TradingService) NotifySystemEvent(message string, level string) {
	s.broadcaster.BroadcastSystemNotification(&websocket.SystemNotificationPayload{
		Message:   message,
		Level:     level, // "info", "warning", "error"
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

Example 4: Broadcasting trading round events

func (s *TradingService) StartRound(roundID string, duration int) {
	s.broadcaster.BroadcastRoundStarted(&websocket.RoundStartedPayload{
		RoundID:   roundID,
		StartTime: time.Now().Format(time.RFC3339),
		Duration:  duration,
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

func (s *TradingService) EndRound(roundID string, totalTrades int, totalPnL float64, winRate float64) {
	s.broadcaster.BroadcastRoundEnded(&websocket.RoundEndedPayload{
		RoundID:     roundID,
		EndTime:     time.Now().Format(time.RFC3339),
		TotalTrades: totalTrades,
		TotalPnL:    totalPnL,
		WinRate:     winRate,
		Timestamp:   time.Now().Format(time.RFC3339),
	})
}

Example 5: Broadcasting to a specific room

func (s *TradingService) BroadcastToRoom(roomID string, messageType string, data interface{}) {
	s.broadcaster.BroadcastToRoom(roomID, messageType, data)
}

Example 6: Checking client counts

func (s *TradingService) GetActiveClients() int {
	return s.broadcaster.GetClientCount()
}

func (s *TradingService) GetTradesChannelSubscribers() int {
	return s.broadcaster.GetChannelClientCount("trades")
}

Example 7: Integration with Scalping Engine

type ScalpingEngine struct {
	broadcaster *websocket.TradingBroadcaster
	// ... other fields
}

func (e *ScalpingEngine) OnTradeExecuted(trade *Trade) {
	// Broadcast trade execution
	e.broadcaster.BroadcastTradeExecuted(&websocket.TradeExecutedPayload{
		TradeID:   trade.ID,
		Pair:      trade.Pair,
		Side:      trade.Side,
		Price:     trade.Price,
		Amount:    trade.Amount,
		Total:     trade.Total,
		Fee:       trade.Fee,
		Status:    "executed",
		Exchange:  trade.Exchange,
		Timestamp: trade.Timestamp.Format(time.RFC3339),
	})
	
	// Update and broadcast P&L
	e.updatePnL()
}

func (e *ScalpingEngine) updatePnL() {
	pnl := e.calculatePnL()
	
	e.broadcaster.BroadcastPnLUpdate(&websocket.PnLUpdatePayload{
		RealizedPnL:   pnl.Realized,
		UnrealizedPnL: pnl.Unrealized,
		TotalPnL:      pnl.Total,
		DailyPnL:      pnl.Daily,
		TotalBalance:  pnl.Balance,
		Positions:     e.getOpenPositions(),
		Timestamp:     time.Now().Format(time.RFC3339),
	})
}

Example 8: Periodic P&L updates

func (e *ScalpingEngine) StartPnLUpdates(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.updatePnL()
		}
	}
}

Example 9: Error handling with notifications

func (s *TradingService) HandleError(err error) {
	s.logger.Error("trading error", "error", err)
	
	// Notify clients about the error
	s.broadcaster.BroadcastSystemNotification(&websocket.SystemNotificationPayload{
		Message:   fmt.Sprintf("Trading error: %v", err),
		Level:     "error",
		Timestamp: time.Now().Format(time.RFC3339),
	})
}

Example 10: Daily summary broadcast

func (s *TradingService) BroadcastDailySummary() {
	date := time.Now().Format("2006-01-02")
	stats := s.getDailyStats()
	
	s.broadcaster.BroadcastDailyPnL(
		date,
		stats.TotalPnL,
		stats.TotalTrades,
		stats.WinningTrades,
		stats.LosingTrades,
	)
}
*/


// Example 6: Broadcasting Market Price Updates
func ExampleMarketPriceBroadcasting(hub *Hub) {
	logger := slog.Default()
	broadcaster := NewMarketPriceBroadcaster(hub, logger)

	// Simple price update (minimum required fields).
	broadcaster.BroadcastSimplePriceUpdate("BTC-USDT", 45123.50, "kucoin")

	// Full price update with all fields.
	broadcaster.BroadcastFullPriceUpdate(
		"ETH-USDT",
		2345.67,  // price
		2345.00,  // bid
		2346.00,  // ask
		1234567.89, // volume_24h
		50.25,    // change_24h
		2.19,     // change_24h_percent
		2400.00,  // high_24h
		2300.00,  // low_24h
		"kucoin",
	)

	// Custom price update with specific fields.
	broadcaster.BroadcastPriceUpdate(&MarketPriceUpdatePayload{
		Symbol:           "SHIB-USDT",
		Price:            0.00001234,
		Change24hPercent: 5.67,
		Volume24h:        9876543.21,
		LastUpdate:       time.Now().UTC().Format(time.RFC3339),
		Source:           "kucoin",
	})

	// Broadcast price error.
	broadcaster.BroadcastPriceError(
		"INVALID-USDT",
		"Symbol not found",
		"kucoin",
		30, // retry after 30 seconds
	)

	// Get subscriber count for a symbol.
	count := broadcaster.GetSubscriberCount("BTC-USDT")
	logger.Info("BTC-USDT subscribers", "count", count)

	// Get all subscriptions.
	subscriptions := broadcaster.GetAllSubscriptions()
	logger.Info("all subscriptions", "subscriptions", subscriptions)

	// Get cached price.
	if price, ok := broadcaster.GetCachedPrice("BTC-USDT"); ok {
		logger.Info("cached price", "symbol", price.Symbol, "price", price.Price)
	}
}

// Example 7: Client Subscribing to Market Prices
func ExampleClientMarketPriceSubscription() {
	// Client sends subscription message:
	// {
	//   "type": "subscribe",
	//   "channels": ["market_prices"],
	//   "symbols": ["BTC-USDT", "ETH-USDT", "SHIB-USDT"]
	// }

	// Server responds with:
	// {
	//   "type": "subscribed",
	//   "channels": ["market_prices"],
	//   "symbols": ["BTC-USDT", "ETH-USDT", "SHIB-USDT"],
	//   "timestamp": "2026-01-24T13:00:00Z"
	// }

	// Server sends initial snapshot:
	// {
	//   "type": "market_price.snapshot",
	//   "channel": "market_prices",
	//   "data": {
	//     "symbols": [
	//       {
	//         "symbol": "BTC-USDT",
	//         "price": 45123.50,
	//         "last_update": "2026-01-24T13:00:00Z",
	//         "source": "kucoin"
	//       },
	//       ...
	//     ]
	//   },
	//   "timestamp": "2026-01-24T13:00:00Z"
	// }

	// Server sends price updates:
	// {
	//   "type": "market_price.update",
	//   "channel": "market_prices",
	//   "data": {
	//     "symbol": "BTC-USDT",
	//     "price": 45200.00,
	//     "bid": 45195.00,
	//     "ask": 45205.00,
	//     "volume_24h": 1234567.89,
	//     "change_24h": 76.50,
	//     "change_24h_percent": 0.17,
	//     "high_24h": 45500.00,
	//     "low_24h": 44000.00,
	//     "last_update": "2026-01-24T13:00:05.123Z",
	//     "source": "kucoin"
	//   },
	//   "timestamp": "2026-01-24T13:00:05.123Z"
	// }
}
