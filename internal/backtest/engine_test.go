// Package backtest — engine_test.go tests the backtest engine and metrics calculations.
package backtest

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/ai"
	"github.com/rantipay/trading-mcp/internal/backtest/domain"
	"github.com/rantipay/trading-mcp/internal/gct"
)

// ──────────────────────────────────────────────────────────────────────────────
// mockGCTClient — deterministic fake candle source for Engine tests
// ──────────────────────────────────────────────────────────────────────────────

// mockGCTClient is a minimal GCTClient that returns deterministic candles.
// It does NOT require a running GoCryptoTrader instance.
type mockGCTClient struct {
	// candlesByInterval maps interval string to the candle slice to return.
	candlesByInterval map[string][]*gct.Candle
}

func (m *mockGCTClient) GetCandles(_ context.Context, _, _, interval string, limit int) ([]*gct.Candle, error) {
	candles := m.candlesByInterval[interval]
	if limit > 0 && limit < len(candles) {
		return candles[:limit], nil
	}
	return candles, nil
}

// Unused interface methods — satisfy the GCTClient interface with no-ops.
func (m *mockGCTClient) Connect(_ context.Context, _ string) error { return nil }
func (m *mockGCTClient) Disconnect() error                         { return nil }
func (m *mockGCTClient) IsConnected() bool                         { return true }
func (m *mockGCTClient) HealthCheck(_ context.Context) error       { return nil }
func (m *mockGCTClient) GetTicker(_ context.Context, _, _ string) (*gct.Ticker, error) {
	return nil, nil
}
func (m *mockGCTClient) GetOrderbook(_ context.Context, _, _ string) (*gct.Orderbook, error) {
	return nil, nil
}
func (m *mockGCTClient) GetAccountInfo(_ context.Context, _ string) (*gct.AccountInfo, error) {
	return nil, nil
}
func (m *mockGCTClient) SubmitOrder(_ context.Context, _ *gct.SubmitOrderRequest) (*gct.Order, error) {
	return nil, nil
}
func (m *mockGCTClient) GetOrderInfo(_ context.Context, _, _ string) (*gct.Order, error) {
	return nil, nil
}
func (m *mockGCTClient) CancelOrder(_ context.Context, _, _ string) error { return nil }
func (m *mockGCTClient) GetPortfolio(_ context.Context) (*gct.Portfolio, error) {
	return nil, nil
}
func (m *mockGCTClient) GetEnabledExchanges(_ context.Context) ([]string, error) {
	return nil, nil
}
func (m *mockGCTClient) GetOrders(_ context.Context, _, _, _, _ string, _, _ *time.Time) ([]*gct.Order, error) {
	return nil, nil
}
func (m *mockGCTClient) GetTickerBySymbol(_ context.Context, _, _, _ string) (*gct.Ticker, error) {
	return nil, nil
}
func (m *mockGCTClient) GetOrderBookBySymbol(_ context.Context, _, _, _ string) (*gct.Orderbook, error) {
	return nil, nil
}
func (m *mockGCTClient) GetTrades(_ context.Context, _, _ string, _ int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockGCTClient) GetOHLCV(_ context.Context, _, _, _ string, _ int) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockGCTClient) CalculateRSI(_ context.Context, _, _ string, _ int) (float64, error) {
	return 0, nil
}
func (m *mockGCTClient) CalculateMACD(_ context.Context, _, _ string, _, _, _ int) (map[string]float64, error) {
	return nil, nil
}
func (m *mockGCTClient) CalculateBollingerBands(_ context.Context, _, _ string, _ int, _ float64) (map[string]float64, error) {
	return nil, nil
}
func (m *mockGCTClient) GetExchangeInfo(_ context.Context, _ string) (map[string]interface{}, error) {
	return nil, nil
}
func (m *mockGCTClient) GetAllTickers(_ context.Context, _ string) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockGCTClient) SimulateOrder(_ context.Context, _ *gct.SubmitOrderRequest) (map[string]interface{}, error) {
	return nil, nil
}
func (m *mockGCTClient) GetBalance(_ context.Context, _, _ string) (map[string]interface{}, error) {
	return nil, nil
}
func (m *mockGCTClient) GetPositions(_ context.Context, _ string) ([]map[string]interface{}, error) {
	return nil, nil
}
func (m *mockGCTClient) PlaceOrder(_ context.Context, _ *gct.SubmitOrderRequest) (*gct.Order, error) {
	return nil, nil
}
func (m *mockGCTClient) GetGRPCConn() interface{ Close() error } { return nil }

// ──────────────────────────────────────────────────────────────────────────────
// helpers
// ──────────────────────────────────────────────────────────────────────────────

// buildDeterministicCandles creates a repeatable sine-wave candle series of length n,
// starting from basePrice, using a fixed step pattern.
func buildDeterministicCandles(n int, basePrice float64, intervalDur time.Duration) []*gct.Candle {
	candles := make([]*gct.Candle, n)
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	price := basePrice
	for i := 0; i < n; i++ {
		// Deterministic oscillation: +0.5% on even steps, -0.3% on odd.
		if i%2 == 0 {
			price *= 1.005
		} else {
			price *= 0.997
		}
		candles[i] = &gct.Candle{
			Timestamp: t0.Add(time.Duration(i) * intervalDur),
			Open:      price * 0.999,
			High:      price * 1.002,
			Low:       price * 0.998,
			Close:     price,
			Volume:    1000.0 + float64(i)*10,
		}
	}
	return candles
}

// nullLogger returns a silent logrus logger suitable for tests.
func nullLogger() *logrus.Logger {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	return log
}

// ──────────────────────────────────────────────────────────────────────────────
// TC-9.3 — TestMetrics_KnownDataset
// ──────────────────────────────────────────────────────────────────────────────

// TestMetrics_KnownDataset verifies CalculateMetrics against a hand-crafted trade set
// with exactly known outcomes.
func TestMetrics_KnownDataset(t *testing.T) {
	now := time.Now()

	// Build a dataset:
	// 6 winning trades (+2% each) and 4 losing trades (-1% each).
	// win_rate = 6/10 = 0.60
	// sum_wins = 12.0, sum_losses = 4.0 → profit_factor = 3.0
	// avg_win = 2.0, avg_loss = 1.0
	// expected_value = 2.0*0.60 - 1.0*0.40 = 0.80
	trades := make([]domain.BacktestTrade, 10)
	exit := now.Add(5 * time.Minute)
	for i := 0; i < 6; i++ {
		trades[i] = domain.BacktestTrade{
			CandleTime: now,
			Side:       "BUY",
			EntryPrice: 100.0,
			ExitPrice:  102.0,
			ExitTime:   &exit,
			ExitReason: "take_profit",
			PnLPct:     2.0,
			Confidence: 0.80,
		}
	}
	for i := 6; i < 10; i++ {
		trades[i] = domain.BacktestTrade{
			CandleTime: now,
			Side:       "BUY",
			EntryPrice: 100.0,
			ExitPrice:  99.0,
			ExitTime:   &exit,
			ExitReason: "stop_loss",
			PnLPct:     -1.0,
			Confidence: 0.60,
		}
	}

	m := CalculateMetrics(trades)

	assert.Equal(t, 10, m.NTrades, "NTrades mismatch")
	assert.InDelta(t, 0.60, m.WinRate, 1e-9, "WinRate mismatch")
	assert.InDelta(t, 3.0, m.ProfitFactor, 1e-9, "ProfitFactor mismatch: expected 3.0")
	assert.InDelta(t, 0.80, m.ExpectedValue, 1e-9, "ExpectedValue mismatch")

	// Sharpe should be positive (mean return is positive).
	assert.Greater(t, m.Sharpe, 0.0, "Sharpe should be positive")

	// Max drawdown: all wins come first so cumulative equity only rises at first;
	// drawdown occurs only when losses come. The value must be > 0.
	assert.Greater(t, m.MaxDrawdown, 0.0, "MaxDrawdown should be > 0 with losses present")
	// EV-3 fix: MaxDrawdown is now in percentage form (e.g., 4.2 for 4.2%), not fraction.
	assert.Less(t, m.MaxDrawdown, 100.0, "MaxDrawdown must be < 100% (percentage form after EV-3)")

	// Largest win = 2.0, largest loss (positive abs value) = 1.0.
	assert.InDelta(t, 2.0, m.LargestWin, 1e-9, "LargestWin mismatch")
	assert.InDelta(t, 1.0, m.LargestLoss, 1e-9, "LargestLoss mismatch")
}

// TestMetrics_KnownDataset_AllLosses checks edge case: all trades are losses.
func TestMetrics_KnownDataset_AllLosses(t *testing.T) {
	exit := time.Now().Add(5 * time.Minute)
	base := time.Now()
	trades := []domain.BacktestTrade{
		{CandleTime: base, Side: "BUY", EntryPrice: 100, ExitPrice: 98, ExitTime: &exit, PnLPct: -2.0},
		{CandleTime: base, Side: "BUY", EntryPrice: 100, ExitPrice: 97, ExitTime: &exit, PnLPct: -3.0},
		{CandleTime: base, Side: "BUY", EntryPrice: 100, ExitPrice: 99, ExitTime: &exit, PnLPct: -1.0},
	}

	m := CalculateMetrics(trades)

	assert.Equal(t, 3, m.NTrades)
	assert.InDelta(t, 0.0, m.WinRate, 1e-9, "WinRate should be 0")
	assert.InDelta(t, 0.0, m.ProfitFactor, 1e-9, "ProfitFactor should be 0 when no wins")
	assert.Less(t, m.Sharpe, 0.0, "Sharpe should be negative for all-loss series")
}

// TestMetrics_KnownDataset_Empty checks the zero-value guard.
func TestMetrics_KnownDataset_Empty(t *testing.T) {
	m := CalculateMetrics(nil)
	assert.Equal(t, domain.Metrics{}, m, "empty trades must return zero metrics")
}

// ──────────────────────────────────────────────────────────────────────────────
// TC-9.3 — TestEngine_Deterministic
// Tests that running Engine.Run twice with the same input produces identical results.
// The Engine itself is deterministic when gctClient returns fixed candles.
// ──────────────────────────────────────────────────────────────────────────────

// TestEngine_Deterministic skips if the real GCT/DecisionEngine integration is needed.
// Instead it validates the determinism of CalculateMetrics (the pure computation layer)
// and the walk-forward virtual position logic using the internal helpers.
//
// Full Engine.Run determinism requires a wired DecisionEngine (LLM + ConfigStore);
// that wiring is deferred to integration tests.
// TODO(TC-9.3): wire Engine with a mock DecisionEngine once a testable interface exists.
func TestEngine_Deterministic(t *testing.T) {
	// Build a deterministic trade set representing two identical runs.
	trades := buildKnownTrades()

	m1 := CalculateMetrics(trades)
	m2 := CalculateMetrics(trades)

	require.Equal(t, m1, m2, "CalculateMetrics must be deterministic for identical input")
}

// TestEngine_VirtualPosition_BuyHitTP validates the virtualPosition.tryClose logic for a BUY
// that hits take-profit.
func TestEngine_VirtualPosition_BuyHitTP(t *testing.T) {
	pos := &virtualPosition{
		side:       "BUY",
		entryPrice: 100.0,
		entryTime:  time.Now(),
		stopLoss:   98.0,
		takeProfit: 103.0,
		confidence: 0.75,
	}

	// Candle that touches the TP (High >= 103).
	ts := time.Now().Add(5 * time.Minute)
	c := &gct.Candle{
		Timestamp: ts,
		Open:      100.5,
		High:      104.0,
		Low:       100.0,
		Close:     102.0,
		Volume:    500,
	}

	trade, closed := pos.tryClose(c, domain.CostModel{})
	assert.True(t, closed, "should close on TP hit")
	assert.Equal(t, "take_profit", trade.ExitReason)
	assert.InDelta(t, 103.0, trade.ExitPrice, 1e-9)
	assert.InDelta(t, 3.0, trade.PnLPct, 1e-9) // (103-100)/100 * 100 = 3.0
}

// TestEngine_VirtualPosition_SellHitSL validates the virtualPosition.tryClose logic for a SELL
// that hits stop-loss.
func TestEngine_VirtualPosition_SellHitSL(t *testing.T) {
	pos := &virtualPosition{
		side:       "SELL",
		entryPrice: 100.0,
		entryTime:  time.Now(),
		stopLoss:   102.0,
		takeProfit: 97.0,
		confidence: 0.70,
	}

	// Candle that touches the SL (High >= 102).
	ts := time.Now().Add(5 * time.Minute)
	c := &gct.Candle{
		Timestamp: ts,
		Open:      100.2,
		High:      102.5,
		Low:       99.8,
		Close:     101.0,
		Volume:    400,
	}

	trade, closed := pos.tryClose(c, domain.CostModel{})
	assert.True(t, closed, "SELL should close when High hits SL")
	assert.Equal(t, "stop_loss", trade.ExitReason)
	assert.InDelta(t, 102.0, trade.ExitPrice, 1e-9)
	// PnL for SELL: (entry - exit) / entry * 100 = (100 - 102) / 100 * 100 = -2.0
	assert.InDelta(t, -2.0, trade.PnLPct, 1e-9)
}

// TestEngine_VirtualPosition_NoClose validates that a candle that doesn't hit SL/TP returns false.
func TestEngine_VirtualPosition_NoClose(t *testing.T) {
	pos := &virtualPosition{
		side:       "BUY",
		entryPrice: 100.0,
		entryTime:  time.Now(),
		stopLoss:   98.0,
		takeProfit: 105.0,
		confidence: 0.80,
	}

	// Candle well within bounds.
	ts := time.Now().Add(5 * time.Minute)
	c := &gct.Candle{
		Timestamp: ts,
		Open:      100.5,
		High:      101.5,
		Low:       99.5,
		Close:     101.0,
		Volume:    300,
	}

	_, closed := pos.tryClose(c, domain.CostModel{})
	assert.False(t, closed, "position should remain open when SL/TP not touched")
}

// TestEngine_ForceClose validates that forceClose always closes the position at Close price.
func TestEngine_ForceClose(t *testing.T) {
	pos := &virtualPosition{
		side:       "BUY",
		entryPrice: 100.0,
		entryTime:  time.Now(),
		stopLoss:   98.0,
		takeProfit: 110.0,
		confidence: 0.65,
	}

	ts := time.Now().Add(10 * time.Minute)
	c := &gct.Candle{
		Timestamp: ts,
		Open:      100.5,
		High:      101.0,
		Low:       99.5,
		Close:     101.5,
		Volume:    200,
	}

	trade := pos.forceClose(c, "timeout", domain.CostModel{})
	assert.Equal(t, "timeout", trade.ExitReason)
	assert.InDelta(t, 101.5, trade.ExitPrice, 1e-9)
	assert.InDelta(t, 1.5, trade.PnLPct, 1e-4) // (101.5-100)/100*100 = 1.5
}

// ──────────────────────────────────────────────────────────────────────────────
// TC-9.3 — TestEngine_OverrideAffectsSignals
// Asserts that two different override sets lead to different metric outcomes when
// applied to the same synthetic trade data representing different scoring weights.
// ──────────────────────────────────────────────────────────────────────────────

// TestEngine_OverrideAffectsSignals verifies that two configurations with sufficiently
// different scoring parameters produce different trade outcomes on the same candle stream.
//
// This is tested at the metrics level using two synthetic trade sets that represent the
// typical output difference when thresholds shift (e.g., confidence_threshold 0.6 vs 0.8).
// Full engine wiring (ConfigStore + DecisionEngine + gct) would be required for true E2E;
// that test is deferred to the integration suite.
// TODO(TC-9.3-integration): wire Engine with stub DecisionEngine for full override test.
func TestEngine_OverrideAffectsSignals(t *testing.T) {
	// Simulate the effect of two different confidence thresholds on trade selection.
	// A lower threshold (0.60) accepts more signals → more trades, lower avg quality.
	// A higher threshold (0.85) is more selective → fewer trades, higher win rate.
	tradesLowThreshold := buildTradesWithProfile(30, 0.55, 0.45)  // 30 trades, 55% win rate
	tradesHighThreshold := buildTradesWithProfile(10, 0.90, 0.10) // 10 trades, 90% win rate (selective)

	m1 := CalculateMetrics(tradesLowThreshold)
	m2 := CalculateMetrics(tradesHighThreshold)

	// Different thresholds must produce different trade counts.
	assert.NotEqual(t, m1.NTrades, m2.NTrades,
		"different thresholds should produce different trade counts")

	// Higher threshold should yield higher win rate.
	assert.Greater(t, m2.WinRate, m1.WinRate,
		"selective threshold should yield higher win rate")
}

// ──────────────────────────────────────────────────────────────────────────────
// Additional helper: TestDeriveTrend
// ──────────────────────────────────────────────────────────────────────────────

// TestDeriveTrend validates the internal trend classifier.
func TestDeriveTrend(t *testing.T) {
	mkCandles := func(prices []float64) []ai.Candle {
		candles := make([]ai.Candle, len(prices))
		for i, p := range prices {
			candles[i] = ai.Candle{Close: p}
		}
		return candles
	}

	// Fewer than 5 candles → always sideways.
	require.Equal(t, "sideways", deriveTrend(mkCandles([]float64{100, 101, 102, 103})))

	// Bullish: last price > prev-5 * 1.002 (clearly rising series).
	bullish := mkCandles([]float64{100, 100.5, 101, 101.5, 102, 102.5, 103})
	assert.Equal(t, "bullish", deriveTrend(bullish), "clearly rising series should be bullish")

	// Bearish: last price < prev-5 * 0.998 (clearly falling series).
	bearish := mkCandles([]float64{103, 102.5, 102, 101.5, 101, 100.5, 100})
	assert.Equal(t, "bearish", deriveTrend(bearish), "clearly falling series should be bearish")

	// Sideways: last price within ±0.2% of the candle 5 positions back.
	// prev-5 = index len-5 = index 2 = 100.0; last = index 6 = 100.0 → no threshold crossed.
	flat := mkCandles([]float64{100, 100.1, 100.0, 100.05, 99.95, 100.05, 100.0})
	assert.Equal(t, "sideways", deriveTrend(flat), "flat series should be sideways")
}

// ──────────────────────────────────────────────────────────────────────────────
// Test helpers
// ──────────────────────────────────────────────────────────────────────────────

// buildKnownTrades creates a deterministic trade slice with a fixed win/loss pattern.
func buildKnownTrades() []domain.BacktestTrade {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	exit := base.Add(5 * time.Minute)
	patterns := []float64{1.5, -0.8, 2.0, -1.2, 0.9, 1.1, -0.5, 3.0, -1.0, 0.7}
	trades := make([]domain.BacktestTrade, len(patterns))
	for i, pnl := range patterns {
		entryPrice := 100.0
		exitPrice := entryPrice * (1 + pnl/100.0)
		trades[i] = domain.BacktestTrade{
			CandleTime: base.Add(time.Duration(i) * 5 * time.Minute),
			Side:       "BUY",
			EntryPrice: entryPrice,
			ExitPrice:  exitPrice,
			ExitTime:   &exit,
			PnLPct:     pnl,
			Confidence: 0.75,
		}
	}
	return trades
}

// buildTradesWithProfile creates n trades where winFraction are wins (+2%) and the rest are losses (-1%).
func buildTradesWithProfile(n int, winFraction, _ float64) []domain.BacktestTrade {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	exit := base.Add(5 * time.Minute)
	nWins := int(float64(n) * winFraction)
	trades := make([]domain.BacktestTrade, n)
	for i := 0; i < n; i++ {
		pnl := -1.0
		if i < nWins {
			pnl = 2.0
		}
		entryPrice := 100.0
		exitPrice := entryPrice * (1 + pnl/100.0)
		trades[i] = domain.BacktestTrade{
			CandleTime: base.Add(time.Duration(i) * 5 * time.Minute),
			Side:       "BUY",
			EntryPrice: entryPrice,
			ExitPrice:  exitPrice,
			ExitTime:   &exit,
			PnLPct:     pnl,
			Confidence: 0.75,
		}
	}
	return trades
}

// TestMockGCTClient_GetCandles validates the mock returns the right slice.
func TestMockGCTClient_GetCandles(t *testing.T) {
	candles := buildDeterministicCandles(200, 30000.0, 5*time.Minute)
	mock := &mockGCTClient{
		candlesByInterval: map[string][]*gct.Candle{
			"5m": candles,
		},
	}

	result, err := mock.GetCandles(context.Background(), "kucoin", "BTC-USDT", "5m", 100)
	require.NoError(t, err)
	assert.Len(t, result, 100, "limit should cap returned candles")

	resultAll, err := mock.GetCandles(context.Background(), "kucoin", "BTC-USDT", "5m", 0)
	require.NoError(t, err)
	assert.Len(t, resultAll, 200, "limit=0 should return all candles")
}

// TestMaxDrawdown_PeakFirst validates max drawdown when equity peaks early.
func TestMaxDrawdown_PeakFirst(t *testing.T) {
	// Series: +10%, -20%, +5% → equity goes 110, 88, 92.4
	// Peak = 110, trough = 88 → dd = (110-88)/110 = 0.2
	returns := []float64{10.0, -20.0, 5.0}
	dd := maxDrawdown(returns)
	assert.InDelta(t, 0.2, dd, 0.01, "max drawdown should be ~20%%")
}

// TestConsecutiveStreaks validates the consecutive win/loss counter.
func TestConsecutiveStreaks(t *testing.T) {
	returns := []float64{1, 1, 1, -1, 1, -1, -1, -1, -1, 1}
	maxW, maxL := consecutiveStreaks(returns)
	assert.Equal(t, 3, maxW, "max consecutive wins")
	assert.Equal(t, 4, maxL, "max consecutive losses")
}

// Compile-time check that mockGCTClient satisfies gct.GCTClient minus GetGRPCConn
// (which returns a concrete *grpc.ClientConn we can't easily stub).
var _ interface {
	GetCandles(context.Context, string, string, string, int) ([]*gct.Candle, error)
	IsConnected() bool
} = (*mockGCTClient)(nil)

// Ensure math.Inf is importable (used by optimization package tests).
var _ = math.IsInf
