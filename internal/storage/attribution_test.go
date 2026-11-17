package storage

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTradeHistoryRepository_Attribution_UserIsolation asserts that trades saved
// for different users are isolated and only returned when queried by that user.
func TestTradeHistoryRepository_Attribution_UserIsolation(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()

	// Save a trade for user A
	tradeA := &Trade{
		ID:               "trade_user_a",
		Pair:             "BTC-USDT",
		Exchange:         "kucoin",
		Side:             "buy",
		TradingStyle:     "scalping-a",
		TradingStyleType: "scalping",
		PrimaryTimeframe: "5m",
		TimeframesUsed:   []string{"5m"},
		EntryPrice:       50000.0,
		EntryTime:        now,
		Size:             100.0,
		Leverage:         5,
		Confidence:       0.85,
		Status:           "closed",
		ExitPrice:        51000.0,
		ExitTime:         now.Add(1 * time.Hour),
		ExitReason:       "take_profit",
		PnL:              1000.0,
		NetPnL:           950.0,
		Fees:             50.0,
	}
	err := manager.TradeHistory.SaveForUser("A", tradeA)
	require.NoError(t, err, "Should save trade for user A")

	// Save a trade for user B
	tradeB := &Trade{
		ID:               "trade_user_b",
		Pair:             "ETH-USDT",
		Exchange:         "kucoin",
		Side:             "sell",
		TradingStyle:     "day-trading-b",
		TradingStyleType: "day_trading",
		PrimaryTimeframe: "15m",
		TimeframesUsed:   []string{"15m"},
		EntryPrice:       2000.0,
		EntryTime:        now,
		Size:             50.0,
		Leverage:         3,
		Confidence:       0.80,
		Status:           "closed",
		ExitPrice:        1950.0,
		ExitTime:         now.Add(2 * time.Hour),
		ExitReason:       "stop_loss",
		PnL:              -2500.0,
		NetPnL:           -2550.0,
		Fees:             50.0,
	}
	err = manager.TradeHistory.SaveForUser("B", tradeB)
	require.NoError(t, err, "Should save trade for user B")

	// Query closed trades for user A (should return only tradeA)
	closedTradesA, err := manager.TradeHistory.GetClosedTradesForUser("A", 10)
	require.NoError(t, err)
	require.Len(t, closedTradesA, 1, "User A should have exactly 1 closed trade")
	assert.Equal(t, "trade_user_a", closedTradesA[0].ID)
	assert.Equal(t, "BTC-USDT", closedTradesA[0].Pair)

	// Query closed trades for user B (should return only tradeB)
	closedTradesB, err := manager.TradeHistory.GetClosedTradesForUser("B", 10)
	require.NoError(t, err)
	require.Len(t, closedTradesB, 1, "User B should have exactly 1 closed trade")
	assert.Equal(t, "trade_user_b", closedTradesB[0].ID)
	assert.Equal(t, "ETH-USDT", closedTradesB[0].Pair)
}

// TestTradeHistoryRepository_Attribution_DefaultUser asserts that the legacy Save()
// method (without userID) writes to the "default" user context and is isolated
// from non-default users.
func TestTradeHistoryRepository_Attribution_DefaultUser(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()

	// Save a trade via legacy Save() method (goes to "default" user)
	tradeDefault := &Trade{
		ID:               "trade_legacy",
		Pair:             "BTC-USDT",
		Exchange:         "kucoin",
		Side:             "buy",
		TradingStyle:     "legacy-style",
		TradingStyleType: "scalping",
		PrimaryTimeframe: "5m",
		TimeframesUsed:   []string{"5m"},
		EntryPrice:       50000.0,
		EntryTime:        now,
		Size:             100.0,
		Leverage:         5,
		Confidence:       0.85,
		Status:           "closed",
		ExitPrice:        51000.0,
		ExitTime:         now.Add(1 * time.Hour),
		PnL:              1000.0,
		NetPnL:           950.0,
		Fees:             50.0,
	}
	err := manager.TradeHistory.Save(tradeDefault)
	require.NoError(t, err)

	// Save a trade for user A (using different entry time to avoid any potential conflicts)
	tradeA := &Trade{
		ID:               "trade_user_a_2",
		Pair:             "ETH-USDT",
		Exchange:         "kucoin",
		Side:             "buy",
		TradingStyle:     "scalping-a",
		TradingStyleType: "scalping",
		PrimaryTimeframe: "5m",
		TimeframesUsed:   []string{"5m"},
		EntryPrice:       2000.0,
		EntryTime:        now.Add(2 * time.Second),
		Size:             50.0,
		Leverage:         5,
		Confidence:       0.80,
		Status:           "closed",
		ExitPrice:        2100.0,
		ExitTime:         now.Add(1 * time.Hour).Add(2 * time.Second),
		PnL:              5000.0,
		NetPnL:           4950.0,
		Fees:             50.0,
	}
	err = manager.TradeHistory.SaveForUser("A", tradeA)
	require.NoError(t, err)

	// Query closed trades for "default" user using the ForUser variant
	// (Note: the legacy GetClosedTrades() method does not filter by user_id, so we use GetClosedTradesForUser)
	closedTradesDefault, err := manager.TradeHistory.GetClosedTradesForUser("default", 10)
	require.NoError(t, err)
	require.Len(t, closedTradesDefault, 1, "Default user should have exactly 1 closed trade")
	assert.Equal(t, "trade_legacy", closedTradesDefault[0].ID)

	// Query closed trades for user A (should return only tradeA)
	closedTradesA, err := manager.TradeHistory.GetClosedTradesForUser("A", 10)
	require.NoError(t, err)
	require.Len(t, closedTradesA, 1, "User A should have exactly 1 closed trade")
	assert.Equal(t, "trade_user_a_2", closedTradesA[0].ID)
}

// TestPortfolioSnapshotRepository_Attribution_UserIsolation asserts that portfolio
// snapshots are isolated per user.
func TestPortfolioSnapshotRepository_Attribution_UserIsolation(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()

	// Save a snapshot for user A
	snapshotA := &PortfolioSnapshot{
		SnapshotTime:     now,
		TotalValueUSDT:   10000.0,
		AvailableBalance: 9000.0,
		UnrealizedPnL:    500.0,
		PositionsCount:   2,
		Assets:           map[string]float64{"BTC": 0.5, "ETH": 10.0},
	}
	err := manager.PortfolioSnapshots.SaveForUser("A", snapshotA)
	require.NoError(t, err)

	// Save a snapshot for user B at a slightly different time
	// (portfolio_snapshots has UNIQUE INDEX on snapshot_time only, not (user_id, snapshot_time))
	snapshotB := &PortfolioSnapshot{
		SnapshotTime:     now.Add(1 * time.Second),
		TotalValueUSDT:   20000.0,
		AvailableBalance: 18000.0,
		UnrealizedPnL:    1000.0,
		PositionsCount:   3,
		Assets:           map[string]float64{"BTC": 1.0, "USDT": 1000.0},
	}
	err = manager.PortfolioSnapshots.SaveForUser("B", snapshotB)
	require.NoError(t, err)

	// Query snapshots for user A (should return only snapshotA)
	snapshotsA, err := manager.PortfolioSnapshots.GetByTimeRangeForUser("A", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	require.Len(t, snapshotsA, 1, "User A should have exactly 1 snapshot")
	assert.Equal(t, 10000.0, snapshotsA[0].TotalValueUSDT)
	assert.Len(t, snapshotsA[0].Assets, 2)

	// Query snapshots for user B (should return only snapshotB)
	snapshotsB, err := manager.PortfolioSnapshots.GetByTimeRangeForUser("B", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	require.Len(t, snapshotsB, 1, "User B should have exactly 1 snapshot")
	assert.Equal(t, 20000.0, snapshotsB[0].TotalValueUSDT)
	assert.Len(t, snapshotsB[0].Assets, 2)

	// Query latest snapshot for user A
	latestA, err := manager.PortfolioSnapshots.GetLatestForUser("A")
	require.NoError(t, err)
	require.NotNil(t, latestA)
	assert.Equal(t, 10000.0, latestA.TotalValueUSDT)

	// Query latest snapshot for user B
	latestB, err := manager.PortfolioSnapshots.GetLatestForUser("B")
	require.NoError(t, err)
	require.NotNil(t, latestB)
	assert.Equal(t, 20000.0, latestB.TotalValueUSDT)
}

// TestPortfolioSnapshotRepository_Attribution_DefaultUser asserts that the legacy
// Save() method (without userID) writes to the "default" user context.
func TestPortfolioSnapshotRepository_Attribution_DefaultUser(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()

	// Save a snapshot via legacy Save() method (goes to "default" user)
	snapshotDefault := &PortfolioSnapshot{
		SnapshotTime:     now,
		TotalValueUSDT:   5000.0,
		AvailableBalance: 4500.0,
		UnrealizedPnL:    250.0,
		PositionsCount:   1,
		Assets:           map[string]float64{"BTC": 0.25},
	}
	err := manager.PortfolioSnapshots.Save(snapshotDefault)
	require.NoError(t, err)

	// Save a snapshot for user A at a slightly different time
	// (portfolio_snapshots has UNIQUE INDEX on snapshot_time only, not (user_id, snapshot_time),
	// so we use different times to avoid constraint violation on the legacy schema)
	snapshotA := &PortfolioSnapshot{
		SnapshotTime:     now.Add(1 * time.Second),
		TotalValueUSDT:   15000.0,
		AvailableBalance: 14000.0,
		UnrealizedPnL:    500.0,
		PositionsCount:   2,
		Assets:           map[string]float64{"BTC": 0.75, "ETH": 5.0},
	}
	err = manager.PortfolioSnapshots.SaveForUser("A", snapshotA)
	require.NoError(t, err)

	// Query snapshots for "default" user (should return only snapshotDefault)
	snapshotsDefault, err := manager.PortfolioSnapshots.GetByTimeRange(now.Add(-1*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	require.Len(t, snapshotsDefault, 1, "Default user should have exactly 1 snapshot")
	assert.Equal(t, 5000.0, snapshotsDefault[0].TotalValueUSDT)

	// Query snapshots for user A (should return only snapshotA)
	snapshotsA, err := manager.PortfolioSnapshots.GetByTimeRangeForUser("A", now.Add(-1*time.Hour), now.Add(1*time.Hour))
	require.NoError(t, err)
	require.Len(t, snapshotsA, 1, "User A should have exactly 1 snapshot")
	assert.Equal(t, 15000.0, snapshotsA[0].TotalValueUSDT)
}

// TestPerformanceMetricsRepository_Attribution_UserIsolation asserts that
// performance metrics are isolated per user.
func TestPerformanceMetricsRepository_Attribution_UserIsolation(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()
	periodStart := now.Add(-24 * time.Hour)

	// Save metrics for user A
	metricsA := &PerformanceMetrics{
		TradingStyle:     "scalping-a",
		TradingStyleType: "scalping",
		Timeframe:        "5m",
		PeriodStart:      periodStart,
		PeriodEnd:        now,
		TotalTrades:      10,
		WinningTrades:    6,
		LosingTrades:     4,
		WinRate:          0.6,
		TotalPnL:         1000.0,
		TotalFees:        50.0,
		NetPnL:           950.0,
	}
	err := manager.PerformanceMetrics.SaveMetricsForUser("A", metricsA.TradingStyle, metricsA.Timeframe, metricsA)
	require.NoError(t, err)

	// Save metrics for user B
	metricsB := &PerformanceMetrics{
		TradingStyle:     "scalping-b",
		TradingStyleType: "scalping",
		Timeframe:        "5m",
		PeriodStart:      periodStart,
		PeriodEnd:        now,
		TotalTrades:      20,
		WinningTrades:    12,
		LosingTrades:     8,
		WinRate:          0.6,
		TotalPnL:         2000.0,
		TotalFees:        100.0,
		NetPnL:           1900.0,
	}
	err = manager.PerformanceMetrics.SaveMetricsForUser("B", metricsB.TradingStyle, metricsB.Timeframe, metricsB)
	require.NoError(t, err)

	// Load metrics for user A
	loadedA, err := manager.PerformanceMetrics.LoadMetricsForUser("A", metricsA.TradingStyle, metricsA.Timeframe)
	require.NoError(t, err)
	require.NotNil(t, loadedA)
	assert.Equal(t, 10, loadedA.TotalTrades)
	assert.Equal(t, 1000.0, loadedA.TotalPnL)

	// Load metrics for user B
	loadedB, err := manager.PerformanceMetrics.LoadMetricsForUser("B", metricsB.TradingStyle, metricsB.Timeframe)
	require.NoError(t, err)
	require.NotNil(t, loadedB)
	assert.Equal(t, 20, loadedB.TotalTrades)
	assert.Equal(t, 2000.0, loadedB.TotalPnL)

	// Attempting to load user B's metrics under user A's context should fail
	// (because "scalping-b" metrics only exist for user B)
	_, err = manager.PerformanceMetrics.LoadMetricsForUser("A", metricsB.TradingStyle, metricsB.Timeframe)
	assert.Error(t, err, "Should not find user B's metrics when querying under user A")
}

// TestPerformanceMetricsRepository_Attribution_DefaultUser asserts that the legacy
// SaveMetrics() method (without userID) writes to the "default" user context.
func TestPerformanceMetricsRepository_Attribution_DefaultUser(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()
	periodStart := now.Add(-24 * time.Hour)

	// Save metrics via legacy SaveMetrics() method (goes to "default" user)
	metricsDefault := &PerformanceMetrics{
		TradingStyle:     "legacy-scalping",
		TradingStyleType: "scalping",
		Timeframe:        "5m",
		PeriodStart:      periodStart,
		PeriodEnd:        now,
		TotalTrades:      5,
		WinningTrades:    3,
		LosingTrades:     2,
		WinRate:          0.6,
		TotalPnL:         500.0,
		TotalFees:        25.0,
		NetPnL:           475.0,
	}
	err := manager.PerformanceMetrics.SaveMetrics(metricsDefault.TradingStyle, metricsDefault.Timeframe, metricsDefault)
	require.NoError(t, err)

	// Save metrics for user A
	metricsA := &PerformanceMetrics{
		TradingStyle:     "scalping-a",
		TradingStyleType: "scalping",
		Timeframe:        "5m",
		PeriodStart:      periodStart,
		PeriodEnd:        now,
		TotalTrades:      15,
		WinningTrades:    9,
		LosingTrades:     6,
		WinRate:          0.6,
		TotalPnL:         1500.0,
		TotalFees:        75.0,
		NetPnL:           1425.0,
	}
	err = manager.PerformanceMetrics.SaveMetricsForUser("A", metricsA.TradingStyle, metricsA.Timeframe, metricsA)
	require.NoError(t, err)

	// Load metrics for "default" user
	loadedDefault, err := manager.PerformanceMetrics.LoadMetrics(metricsDefault.TradingStyle, metricsDefault.Timeframe)
	require.NoError(t, err)
	require.NotNil(t, loadedDefault)
	assert.Equal(t, 5, loadedDefault.TotalTrades)

	// Load metrics for user A
	loadedA, err := manager.PerformanceMetrics.LoadMetricsForUser("A", metricsA.TradingStyle, metricsA.Timeframe)
	require.NoError(t, err)
	require.NotNil(t, loadedA)
	assert.Equal(t, 15, loadedA.TotalTrades)

	// Attempting to load "default" user's metrics under user A should fail
	_, err = manager.PerformanceMetrics.LoadMetricsForUser("A", metricsDefault.TradingStyle, metricsDefault.Timeframe)
	assert.Error(t, err, "Should not find default user's metrics when querying under user A")
}

// TestTradeHistoryRepository_GetOpenTradesForUser verifies that open trades
// are scoped to the correct user.
func TestTradeHistoryRepository_GetOpenTradesForUser(t *testing.T) {
	manager := setupTestManager(t)
	defer manager.Close()

	now := time.Now()

	// Save an open trade for user A
	tradeAOpen := &Trade{
		ID:               "trade_a_open",
		Pair:             "BTC-USDT",
		Exchange:         "kucoin",
		Side:             "buy",
		TradingStyle:     "scalping-a",
		TradingStyleType: "scalping",
		PrimaryTimeframe: "5m",
		TimeframesUsed:   []string{"5m"},
		EntryPrice:       50000.0,
		EntryTime:        now,
		Size:             100.0,
		Leverage:         5,
		Confidence:       0.85,
		Status:           "open",
	}
	err := manager.TradeHistory.SaveForUser("A", tradeAOpen)
	require.NoError(t, err)

	// Save an open trade for user B
	tradeBOpen := &Trade{
		ID:               "trade_b_open",
		Pair:             "ETH-USDT",
		Exchange:         "kucoin",
		Side:             "sell",
		TradingStyle:     "day-trading-b",
		TradingStyleType: "day_trading",
		PrimaryTimeframe: "15m",
		TimeframesUsed:   []string{"15m"},
		EntryPrice:       2000.0,
		EntryTime:        now,
		Size:             50.0,
		Leverage:         3,
		Confidence:       0.80,
		Status:           "open",
	}
	err = manager.TradeHistory.SaveForUser("B", tradeBOpen)
	require.NoError(t, err)

	// Save a closed trade for user A
	tradeAClosed := &Trade{
		ID:               "trade_a_closed",
		Pair:             "LTC-USDT",
		Exchange:         "kucoin",
		Side:             "buy",
		TradingStyle:     "scalping-a",
		TradingStyleType: "scalping",
		PrimaryTimeframe: "5m",
		TimeframesUsed:   []string{"5m"},
		EntryPrice:       1000.0,
		EntryTime:        now,
		Size:             100.0,
		Leverage:         5,
		Confidence:       0.80,
		Status:           "closed",
		ExitPrice:        1100.0,
		ExitTime:         now.Add(1 * time.Hour),
		PnL:              10000.0,
		NetPnL:           9950.0,
		Fees:             50.0,
	}
	err = manager.TradeHistory.SaveForUser("A", tradeAClosed)
	require.NoError(t, err)

	// Get open trades for user A (should return only tradeAOpen)
	openTradesA, err := manager.TradeHistory.GetOpenTradesForUser("A")
	require.NoError(t, err)
	require.Len(t, openTradesA, 1, "User A should have exactly 1 open trade")
	assert.Equal(t, "trade_a_open", openTradesA[0].ID)

	// Get open trades for user B (should return only tradeBOpen)
	openTradesB, err := manager.TradeHistory.GetOpenTradesForUser("B")
	require.NoError(t, err)
	require.Len(t, openTradesB, 1, "User B should have exactly 1 open trade")
	assert.Equal(t, "trade_b_open", openTradesB[0].ID)
}
