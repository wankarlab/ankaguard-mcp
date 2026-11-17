package backtest

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCandles struct {
	candles []models.Candle
	err     error
	calls   int
}

func (f *fakeCandles) GetCandles(_ context.Context, _, _, _ string, _ int) ([]models.Candle, error) {
	f.calls++
	return f.candles, f.err
}

// wave builds n hourly candles whose close follows base + amp*sin(i/period).
func wave(n int, base, amp, period float64) []models.Candle {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]models.Candle, n)
	for i := range out {
		c := base + amp*math.Sin(float64(i)/period)
		out[i] = models.Candle{Timestamp: start.Add(time.Duration(i) * time.Hour), Open: c, High: c, Low: c, Close: c, Volume: 1}
	}
	return out
}

func run(t *testing.T, src candleSource, extra map[string]interface{}) (map[string]interface{}, error) {
	t.Helper()
	params := map[string]interface{}{"exchange": "kucoin", "pair": "BTC-USDT", "strategy": "moving_average", "limit": float64(300)}
	for k, v := range extra {
		params[k] = v
	}
	res, err := (&BacktestStrategyTool{source: src}).Execute(context.Background(), params)
	if err != nil {
		return nil, err
	}
	return res.(map[string]interface{}), nil
}

func TestBacktestDeclaresEstimateAndAssumptions(t *testing.T) {
	res, err := run(t, &fakeCandles{candles: wave(300, 100, 10, 8)}, nil)
	require.NoError(t, err)
	assert.Equal(t, true, res["estimate"])
	a := res["assumptions"].(map[string]interface{})
	for _, k := range []string{"period_start", "period_end", "candle_source", "fee_pct_per_side", "slippage_pct_per_side", "funding_modeled"} {
		assert.Contains(t, a, k)
	}
	assert.Equal(t, false, a["funding_modeled"])
	assert.Equal(t, 300, a["candles_used"])
}

// The result must follow the source data: different candles, different result.
func TestBacktestResultChangesWithCandles(t *testing.T) {
	a, err := run(t, &fakeCandles{candles: wave(300, 100, 10, 8)}, nil)
	require.NoError(t, err)
	b, err := run(t, &fakeCandles{candles: wave(300, 100, 25, 5)}, nil)
	require.NoError(t, err)
	assert.NotEqual(t, a["roi"], b["roi"])
	assert.NotEqual(t, a["assumptions"].(map[string]interface{})["period_end"], "")
}

func TestBacktestFeesReduceReturn(t *testing.T) {
	src := &fakeCandles{candles: wave(300, 100, 10, 8)}
	free, err := run(t, src, map[string]interface{}{"fee_pct": 0.0, "slippage_pct": 0.0})
	require.NoError(t, err)
	costly, err := run(t, src, map[string]interface{}{"fee_pct": 0.5, "slippage_pct": 0.5})
	require.NoError(t, err)
	assert.Greater(t, free["roi"].(float64), costly["roi"].(float64))
}

func TestBacktestSortsNewestFirstCandles(t *testing.T) {
	asc := wave(300, 100, 10, 8)
	desc := make([]models.Candle, len(asc))
	for i := range asc {
		desc[len(asc)-1-i] = asc[i]
	}
	a, err := run(t, &fakeCandles{candles: asc}, nil)
	require.NoError(t, err)
	b, err := run(t, &fakeCandles{candles: desc}, nil)
	require.NoError(t, err)
	assert.Equal(t, a["roi"], b["roi"])
	assert.Equal(t, a["total_trades"], b["total_trades"])
}

func TestBacktestDataUnavailable(t *testing.T) {
	_, err := run(t, &fakeCandles{err: errors.New("gct down")}, nil)
	require.Error(t, err)
	assert.True(t, common.IsDataUnavailable(err))

	_, err = run(t, &fakeCandles{candles: wave(10, 100, 1, 3)}, nil)
	require.Error(t, err)
	require.True(t, common.IsDataUnavailable(err))
	data := err.(*models.MCPError).Data.(map[string]interface{})
	assert.Equal(t, "DATA_UNAVAILABLE", data["code"])
	assert.Greater(t, data["candles_missing"].(int), 0)
}

func TestBacktestAllStrategiesRun(t *testing.T) {
	src := &fakeCandles{candles: wave(400, 100, 15, 7)}
	for _, s := range []string{StrategyMovingAverage, StrategyRSI, StrategyBollingerBands} {
		res, err := run(t, src, map[string]interface{}{"strategy": s})
		require.NoError(t, err, s)
		assert.Greater(t, res["total_trades"].(int), 0, s)
	}
}

func TestBacktestRejectsBadInput(t *testing.T) {
	src := &fakeCandles{candles: wave(300, 100, 10, 8)}
	_, err := run(t, src, map[string]interface{}{"strategy": "magic"})
	assert.Error(t, err)
	_, err = run(t, src, map[string]interface{}{"parameters": map[string]interface{}{"fast_period": 50.0, "slow_period": 10.0}})
	assert.Error(t, err)
	_, err = run(t, src, map[string]interface{}{"interval": "zz"})
	assert.Error(t, err)
	assert.Zero(t, src.calls)
}

func TestSimulateKnownTrade(t *testing.T) {
	// Buy at 100, sell at 110, no costs: +10%.
	candles := []models.Candle{{Close: 100}, {Close: 100}, {Close: 110}, {Close: 110}}
	for i := range candles {
		candles[i].Timestamp = time.Unix(int64(i)*3600, 0)
	}
	sig := func(_ []float64, i int, _ bool) (bool, bool) { return i == 1, true }
	res := simulate(candles, sig, simConfig{InitialCapital: 1000}, 8760)
	require.Len(t, res.Trades, 1)
	assert.InDelta(t, 10, res.ROIPct, 1e-9)
}
