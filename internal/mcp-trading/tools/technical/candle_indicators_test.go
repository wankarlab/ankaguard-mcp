package technical

import (
	"context"
	"errors"
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
}

func (f fakeCandles) GetCandles(context.Context, string, string, string, int) ([]models.Candle, error) {
	return f.candles, f.err
}

func series(n int, f func(i int) (h, l, c, v float64)) []models.Candle {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]models.Candle, n)
	for i := range out {
		h, l, c, v := f(i)
		out[i] = models.Candle{Timestamp: start.Add(time.Duration(i) * time.Hour), High: h, Low: l, Close: c, Open: c, Volume: v}
	}
	return out
}

func trend(n int, slope float64) []models.Candle {
	return series(n, func(i int) (float64, float64, float64, float64) {
		c := 100 + slope*float64(i)
		return c + 1, c - 1, c, 10
	})
}

func TestIndicatorMathKnownValues(t *testing.T) {
	flat := series(30, func(int) (float64, float64, float64, float64) { return 11, 9, 10, 5 })
	assert.InDelta(t, 2, atrValue(flat, 14), 1e-9)
	assert.InDelta(t, 0, cciValue(flat, 20), 1e-9)
	assert.InDelta(t, -50, williamsRValue(flat, 14), 1e-9)

	rising := trend(40, 1)
	assert.InDelta(t, 100, mfiValue(rising, 14), 1e-9)
	k, d := stochasticValue(rising, 14, 3)
	assert.Greater(t, k, 80.0)
	assert.Greater(t, d, 80.0)
	adx, pdi, mdi := adxValue(rising, 14)
	assert.Greater(t, adx, 50.0)
	assert.Greater(t, pdi, mdi)
	assert.Equal(t, 10.0*39, obvValue(rising))

	top := series(20, func(i int) (float64, float64, float64, float64) { return 10, 0, 10, 1 })
	assert.InDelta(t, 0, williamsRValue(top, 14), 1e-9)

	v, err := vwapValue([]models.Candle{{High: 10, Low: 10, Close: 10, Volume: 1}, {High: 20, Low: 20, Close: 20, Volume: 3}})
	require.NoError(t, err)
	assert.InDelta(t, 17.5, v, 1e-9)
	_, err = vwapValue([]models.Candle{{High: 1, Low: 1, Close: 1}})
	assert.Error(t, err)
}

func specByName(t *testing.T, name string) indicatorSpec {
	for _, s := range candleIndicatorSpecs {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no spec %s", name)
	return indicatorSpec{}
}

func exec(t *testing.T, name string, src candleSource) (map[string]interface{}, error) {
	tool := &candleIndicatorTool{spec: specByName(t, name), source: src}
	res, err := tool.Execute(context.Background(), map[string]interface{}{"exchange": "kucoin", "pair": "BTC-USDT"})
	if err != nil {
		return nil, err
	}
	return res.(map[string]interface{}), nil
}

// Every candle indicator must change its answer when the candles change.
func TestEveryIndicatorFollowsCandles(t *testing.T) {
	wavy := series(250, func(i int) (float64, float64, float64, float64) {
		c := 100 + float64((i*7)%13) + float64(i)*0.1
		return c + 1.5, c - 1, c, float64(1 + i%5)
	})
	up, down := trend(250, 1), trend(250, -0.2)
	for _, spec := range candleIndicatorSpecs {
		a, err := exec(t, spec.Name, fakeCandles{candles: up})
		require.NoError(t, err, spec.Name)
		b, err := exec(t, spec.Name, fakeCandles{candles: wavy})
		require.NoError(t, err, spec.Name)
		c, err := exec(t, spec.Name, fakeCandles{candles: down})
		require.NoError(t, err, spec.Name)
		same := true
		for k, v := range a {
			switch k {
			case "exchange", "pair", "interval", "candles_used", "as_of":
				continue
			}
			if _, isNum := v.(float64); isNum && (v != b[k] || v != c[k]) {
				same = false
			}
		}
		assert.False(t, same, "%s did not change with the candles", spec.Name)
	}
}

func TestIndicatorDataUnavailable(t *testing.T) {
	_, err := exec(t, "calculate_atr", fakeCandles{err: errors.New("down")})
	assert.True(t, common.IsDataUnavailable(err))

	_, err = exec(t, "calculate_ichimoku", fakeCandles{candles: trend(20, 1)})
	require.True(t, common.IsDataUnavailable(err))
	data := err.(*models.MCPError).Data.(map[string]interface{})
	assert.Equal(t, 32, data["candles_missing"])
}

func TestIndicatorSortsNewestFirstCandles(t *testing.T) {
	asc := trend(100, 1)
	desc := make([]models.Candle, len(asc))
	for i := range asc {
		desc[len(asc)-1-i] = asc[i]
	}
	a, err := exec(t, "calculate_atr", fakeCandles{candles: asc})
	require.NoError(t, err)
	b, err := exec(t, "calculate_atr", fakeCandles{candles: desc})
	require.NoError(t, err)
	assert.Equal(t, a["atr"], b["atr"])
}
