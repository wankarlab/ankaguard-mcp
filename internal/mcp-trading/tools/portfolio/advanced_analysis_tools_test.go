package portfolio

import (
	"context"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/rantipay/trading-mcp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeSnapshots struct{ snaps []*storage.PortfolioSnapshot }

func (f fakeSnapshots) GetByTimeRange(_, _ time.Time) ([]*storage.PortfolioSnapshot, error) {
	return f.snaps, nil
}

// dailyEquitySeries builds one snapshot per day ending today with the given equity values.
func dailyEquitySeries(values []float64) fakeSnapshots {
	end := time.Now().UTC().Truncate(24 * time.Hour)
	var out []*storage.PortfolioSnapshot
	for i, v := range values {
		day := end.AddDate(0, 0, -(len(values) - 1 - i))
		out = append(out, &storage.PortfolioSnapshot{SnapshotTime: day.Add(12 * time.Hour), TotalValueUSDT: v})
	}
	return fakeSnapshots{snaps: out}
}

func growing(n int, start, step float64) []float64 {
	v := make([]float64, n)
	for i := range v {
		v[i] = start + step*float64(i)
	}
	return v
}

func metricsOf(t *testing.T, res interface{}) map[string]interface{} {
	t.Helper()
	return res.(map[string]interface{})["metrics"].(map[string]interface{})
}

func TestAdvancedMetricsFollowsEquityHistory(t *testing.T) {
	up := &GetAdvancedPortfolioMetricsTool{source: dailyEquitySeries(growing(40, 100, 1))}
	resUp, err := up.Execute(context.Background(), map[string]interface{}{"period_days": 40.0})
	require.NoError(t, err)
	assert.Equal(t, 0.0, metricsOf(t, resUp)["max_drawdown"], "monotonic growth has no drawdown")

	series := growing(40, 100, 1)
	series[30] = 70 // a real drop in the history
	down := &GetAdvancedPortfolioMetricsTool{source: dailyEquitySeries(series)}
	resDown, err := down.Execute(context.Background(), map[string]interface{}{"period_days": 40.0})
	require.NoError(t, err)
	assert.Greater(t, metricsOf(t, resDown)["max_drawdown_pct"].(float64), 20.0)
	assert.Greater(t, metricsOf(t, resDown)["var_99_pct"].(float64), 0.0)
	assert.NotEqual(t, metricsOf(t, resUp)["sharpe_ratio"], metricsOf(t, resDown)["sharpe_ratio"])
}

func TestAdvancedMetricsDataUnavailable(t *testing.T) {
	short := &GetAdvancedPortfolioMetricsTool{source: dailyEquitySeries(growing(8, 100, 1))}
	_, err := short.Execute(context.Background(), map[string]interface{}{})
	require.Error(t, err)
	require.True(t, common.IsDataUnavailable(err))
	data := err.(*models.MCPError).Data.(map[string]interface{})
	assert.Equal(t, "DATA_UNAVAILABLE", data["code"])
	assert.Equal(t, 7, data["daily_returns_available"])
	assert.Equal(t, minDailyReturns-7, data["daily_returns_missing"])

	none := &GetAdvancedPortfolioMetricsTool{}
	_, err = none.Execute(context.Background(), map[string]interface{}{})
	assert.True(t, common.IsDataUnavailable(err))
}

type fakeHoldings struct{ p *models.Portfolio }

func (f fakeHoldings) GetPortfolioForExchange(_ context.Context, _ string) (*models.Portfolio, error) {
	return f.p, nil
}

func portfolioOf(btc, usdt float64) *models.Portfolio {
	return &models.Portfolio{TotalValueUSD: btc + usdt, Holdings: map[string]*models.Holding{
		"BTC":  {Currency: "BTC", ValueUSD: btc},
		"USDT": {Currency: "USDT", ValueUSD: usdt},
	}}
}

func TestRiskMetricsFollowHistoryAndHoldings(t *testing.T) {
	series := growing(40, 100, 1)
	series[20] = 60
	tool := &GetPortfolioRiskMetricsTool{holdings: fakeHoldings{portfolioOf(900, 100)}, source: dailyEquitySeries(series)}
	args := map[string]interface{}{"exchange": "kucoin", "period_days": 40.0}
	res1, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	r1 := res1.(map[string]interface{})
	assert.Greater(t, r1["max_drawdown_pct"].(float64), 30.0)
	assert.Equal(t, "BTC", r1["most_concentrated_asset"])

	tool.holdings = fakeHoldings{portfolioOf(500, 500)}
	res2, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	r2 := res2.(map[string]interface{})
	assert.NotEqual(t, r1["concentration_risk"], r2["concentration_risk"])

	tool.source = dailyEquitySeries(growing(40, 100, 1))
	res3, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.NotEqual(t, r2["max_drawdown_pct"], res3.(map[string]interface{})["max_drawdown_pct"])
}

func TestRiskMetricsDataUnavailableWithShortHistory(t *testing.T) {
	tool := &GetPortfolioRiskMetricsTool{holdings: fakeHoldings{portfolioOf(1, 1)}, source: dailyEquitySeries(growing(5, 100, 1))}
	_, err := tool.Execute(context.Background(), map[string]interface{}{"exchange": "kucoin"})
	require.Error(t, err)
	assert.True(t, common.IsDataUnavailable(err))
}
