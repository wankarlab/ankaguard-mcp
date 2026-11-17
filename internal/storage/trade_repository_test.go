package storage

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTradeHistoryRepository_Save(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	trade := &Trade{
		ID:               "trade_123",
		Pair:             "BTC-USDT",
		Exchange:         "kucoin",
		Side:             "buy",
		TradingStyle:     "test-strategy",
		TradingStyleType: "scalping",
		PrimaryTimeframe: "5m",
		TimeframesUsed:   []string{"5m", "15m"},
		EntryPrice:       50000.0,
		EntryTime:        time.Now(),
		Size:             100.0,
		Leverage:         5,
		StopLoss:         49000.0,
		TakeProfit:       51000.0,
		Confidence:       0.85,
		DecisionReason:   "Test trade",
		Status:           "open",
	}

	// Save trade
	err := manager.TradeHistory.Save(trade)
	assert.NoError(t, err)

	// Load trade
	loaded, err := manager.TradeHistory.GetByID("trade_123")
	assert.NoError(t, err)
	assert.Equal(t, trade.ID, loaded.ID)
	assert.Equal(t, trade.Pair, loaded.Pair)
	assert.Equal(t, trade.Confidence, loaded.Confidence)
}

func TestTradeHistoryRepository_Update(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	trade := &Trade{
		ID:               "trade_123",
		Pair:             "BTC-USDT",
		Exchange:         "kucoin",
		Side:             "buy",
		TradingStyle:     "test-strategy",
		TradingStyleType: "scalping",
		PrimaryTimeframe: "5m",
		TimeframesUsed:   []string{"5m"},
		EntryPrice:       50000.0,
		EntryTime:        time.Now(),
		Size:             100.0,
		Leverage:         5,
		Confidence:       0.85,
		Status:           "open",
	}

	// Save initial
	err := manager.TradeHistory.Save(trade)
	require.NoError(t, err)

	// Update with exit info
	trade.ExitPrice = 51000.0
	trade.ExitTime = time.Now()
	trade.ExitReason = "take_profit"
	trade.PnL = 1000.0
	trade.PnLPct = 2.0
	trade.Status = "closed"

	err = manager.TradeHistory.Save(trade)
	assert.NoError(t, err)

	// Verify update
	loaded, err := manager.TradeHistory.GetByID("trade_123")
	assert.NoError(t, err)
	assert.Equal(t, "closed", loaded.Status)
	assert.Equal(t, 1000.0, loaded.PnL)
}

func TestTradeHistoryRepository_GetByStyle(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Save trades with different styles
	trades := []*Trade{
		{
			ID: "trade_1", Pair: "BTC-USDT", Exchange: "kucoin", Side: "buy",
			TradingStyle: "scalping-1", TradingStyleType: "scalping", PrimaryTimeframe: "5m",
			TimeframesUsed: []string{"5m"}, EntryPrice: 50000, EntryTime: time.Now(),
			Size: 100, Leverage: 5, Confidence: 0.8, Status: "open",
		},
		{
			ID: "trade_2", Pair: "BTC-USDT", Exchange: "kucoin", Side: "buy",
			TradingStyle: "scalping-1", TradingStyleType: "scalping", PrimaryTimeframe: "5m",
			TimeframesUsed: []string{"5m"}, EntryPrice: 50000, EntryTime: time.Now(),
			Size: 100, Leverage: 5, Confidence: 0.8, Status: "open",
		},
		{
			ID: "trade_3", Pair: "BTC-USDT", Exchange: "kucoin", Side: "buy",
			TradingStyle: "day-trading-1", TradingStyleType: "day_trading", PrimaryTimeframe: "15m",
			TimeframesUsed: []string{"15m"}, EntryPrice: 50000, EntryTime: time.Now(),
			Size: 100, Leverage: 3, Confidence: 0.75, Status: "open",
		},
	}

	for _, trade := range trades {
		err := manager.TradeHistory.Save(trade)
		require.NoError(t, err)
	}

	// Get by style
	scalpingTrades, err := manager.TradeHistory.GetByStyle("scalping-1")
	assert.NoError(t, err)
	assert.Len(t, scalpingTrades, 2)

	dayTrades, err := manager.TradeHistory.GetByStyle("day-trading-1")
	assert.NoError(t, err)
	assert.Len(t, dayTrades, 1)
}

func TestTradeHistoryRepository_GetByTimeframe(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Save trades with different timeframes
	trades := []*Trade{
		{
			ID: "trade_1", Pair: "BTC-USDT", Exchange: "kucoin", Side: "buy",
			TradingStyle: "test", TradingStyleType: "scalping", PrimaryTimeframe: "5m",
			TimeframesUsed: []string{"5m"}, EntryPrice: 50000, EntryTime: time.Now(),
			Size: 100, Leverage: 5, Confidence: 0.8, Status: "open",
		},
		{
			ID: "trade_2", Pair: "BTC-USDT", Exchange: "kucoin", Side: "buy",
			TradingStyle: "test", TradingStyleType: "day_trading", PrimaryTimeframe: "15m",
			TimeframesUsed: []string{"15m"}, EntryPrice: 50000, EntryTime: time.Now(),
			Size: 100, Leverage: 3, Confidence: 0.75, Status: "open",
		},
	}

	for _, trade := range trades {
		err := manager.TradeHistory.Save(trade)
		require.NoError(t, err)
	}

	// Get by timeframe
	trades5m, err := manager.TradeHistory.GetByTimeframe("5m")
	assert.NoError(t, err)
	assert.Len(t, trades5m, 1)

	trades15m, err := manager.TradeHistory.GetByTimeframe("15m")
	assert.NoError(t, err)
	assert.Len(t, trades15m, 1)
}

func TestTradeHistoryRepository_GetOpenTrades(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Save open and closed trades
	trades := []*Trade{
		{
			ID: "trade_1", Pair: "BTC-USDT", Exchange: "kucoin", Side: "buy",
			TradingStyle: "test", TradingStyleType: "scalping", PrimaryTimeframe: "5m",
			TimeframesUsed: []string{"5m"}, EntryPrice: 50000, EntryTime: time.Now(),
			Size: 100, Leverage: 5, Confidence: 0.8, Status: "open",
		},
		{
			ID: "trade_2", Pair: "BTC-USDT", Exchange: "kucoin", Side: "buy",
			TradingStyle: "test", TradingStyleType: "scalping", PrimaryTimeframe: "5m",
			TimeframesUsed: []string{"5m"}, EntryPrice: 50000, EntryTime: time.Now(),
			Size: 100, Leverage: 5, Confidence: 0.8, Status: "closed",
		},
	}

	for _, trade := range trades {
		err := manager.TradeHistory.Save(trade)
		require.NoError(t, err)
	}

	// Get open trades
	openTrades, err := manager.TradeHistory.GetOpenTrades()
	assert.NoError(t, err)
	assert.Len(t, openTrades, 1)
	assert.Equal(t, "open", openTrades[0].Status)
}

func TestTradeHistoryRepository_GetClosedTrades(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	// Save closed trades
	for i := 0; i < 5; i++ {
		trade := &Trade{
			ID: fmt.Sprintf("trade_%d", i), Pair: "BTC-USDT", Exchange: "kucoin", Side: "buy",
			TradingStyle: "test", TradingStyleType: "scalping", PrimaryTimeframe: "5m",
			TimeframesUsed: []string{"5m"}, EntryPrice: 50000, EntryTime: time.Now(),
			Size: 100, Leverage: 5, Confidence: 0.8, Status: "closed",
			ExitPrice: 51000, ExitTime: time.Now(), PnL: 1000,
		}
		err := manager.TradeHistory.Save(trade)
		require.NoError(t, err)
	}

	// Get closed trades with limit
	closedTrades, err := manager.TradeHistory.GetClosedTrades(3)
	assert.NoError(t, err)
	assert.Len(t, closedTrades, 3)
}

func TestTradeHistoryRepository_GlobalTailLossStreak(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()
	save := func(id string, pair string, netPnL float64, exitOffset time.Duration) {
		trade := &Trade{
			ID:               id,
			Pair:             pair,
			Exchange:         "kucoin",
			Side:             "buy",
			TradingStyle:     "test",
			TradingStyleType: "scalping",
			PrimaryTimeframe: "5m",
			TimeframesUsed:   []string{"5m"},
			EntryPrice:       100.0,
			EntryTime:        now.Add(exitOffset - time.Minute),
			ExitPrice:        101.0,
			ExitTime:         now.Add(exitOffset),
			ExitReason:       "stop_loss",
			PnL:              netPnL,
			PnLPct:           netPnL,
			NetPnL:           netPnL,
			Status:           "closed",
		}
		require.NoError(t, manager.TradeHistory.Save(trade))
	}

	// No closed trades yet: streak 0.
	streak, _, err := manager.TradeHistory.GlobalTailLossStreak("")
	require.NoError(t, err)
	assert.Equal(t, 0, streak)

	// Win, then three losses across DIFFERENT pairs: global tail = 3.
	save("gtls_1", "AAA-USDT", 0.5, -40*time.Minute)
	save("gtls_2", "BBB-USDT", -0.1, -30*time.Minute)
	save("gtls_3", "CCC-USDT", -0.2, -20*time.Minute)
	save("gtls_4", "DDD-USDT", -0.3, -10*time.Minute)

	streak, lastExit, err := manager.TradeHistory.GlobalTailLossStreak("")
	require.NoError(t, err)
	assert.Equal(t, 3, streak)
	assert.WithinDuration(t, now.Add(-10*time.Minute), lastExit, 2*time.Second)

	// A win at the tail seals the streak at 0.
	save("gtls_5", "EEE-USDT", 0.4, -5*time.Minute)
	streak, _, err = manager.TradeHistory.GlobalTailLossStreak("")
	require.NoError(t, err)
	assert.Equal(t, 0, streak)

	// Phantom rows (|pnl_pct| >= 99) are ignored: a phantom "win" must not seal.
	save("gtls_6", "FFF-USDT", -0.1, -4*time.Minute)
	phantom := &Trade{
		ID: "gtls_7", Pair: "GGG-USDT", Exchange: "kucoin", Side: "sell",
		TradingStyle: "test", TradingStyleType: "scalping", PrimaryTimeframe: "5m",
		TimeframesUsed: []string{"5m"}, EntryPrice: 100.0,
		EntryTime: now.Add(-3 * time.Minute), ExitPrice: 200.0,
		ExitTime: now.Add(-2 * time.Minute), ExitReason: "take_profit",
		PnL: 100.0, PnLPct: 100.0, NetPnL: 100.0, Status: "closed",
	}
	require.NoError(t, manager.TradeHistory.Save(phantom))
	streak, _, err = manager.TradeHistory.GlobalTailLossStreak("")
	require.NoError(t, err)
	assert.Equal(t, 1, streak)
}
