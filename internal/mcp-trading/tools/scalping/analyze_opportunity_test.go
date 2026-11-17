package scalping

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/rantipay/trading-mcp/internal/gct"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeGCT overrides only the calls these tools make; any other call panics on the nil embedded interface.
type fakeGCT struct {
	gct.GCTClient
	ticker  *gct.Ticker
	candles []*gct.Candle
	candErr error
}

func (f *fakeGCT) GetTicker(context.Context, string, string) (*gct.Ticker, error) { return f.ticker, nil }
func (f *fakeGCT) GetCandles(context.Context, string, string, string, int) ([]*gct.Candle, error) {
	return f.candles, f.candErr
}

func testAdapter(c gct.GCTClient) *Adapter {
	l := logrus.New()
	l.SetOutput(io.Discard)
	return &Adapter{logger: l, gctClient: c}
}

func candlesWithRange(n int, rng float64) []*gct.Candle {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]*gct.Candle, n)
	for i := range out {
		out[i] = &gct.Candle{Timestamp: start.Add(time.Duration(i) * 5 * time.Minute), High: 100 + rng/2, Low: 100 - rng/2, Close: 100, Open: 100, Volume: 1}
	}
	return out
}

func TestAnalyzeOpportunityUsesRealATR(t *testing.T) {
	ticker := &gct.Ticker{Last: 100, Bid: 99.98, Ask: 100.02, Volume: 2_000_000}
	args := map[string]interface{}{"exchange": "kucoin", "pair": "BTC-USDT"}

	calm := NewAnalyzeOpportunityTool(testAdapter(&fakeGCT{ticker: ticker, candles: candlesWithRange(40, 0.5)}))
	wild := NewAnalyzeOpportunityTool(testAdapter(&fakeGCT{ticker: ticker, candles: candlesWithRange(40, 5)}))
	r1, err := calm.Execute(context.Background(), args)
	require.NoError(t, err)
	r2, err := wild.Execute(context.Background(), args)
	require.NoError(t, err)
	a1, a2 := r1.(*AnalyzeOpportunityResponse), r2.(*AnalyzeOpportunityResponse)
	assert.InDelta(t, 0.5, a1.ATR, 1e-9)
	assert.InDelta(t, 5, a2.ATR, 1e-9)
	assert.Greater(t, a2.VolatilityScore, a1.VolatilityScore)
}

func TestAnalyzeOpportunityDataUnavailable(t *testing.T) {
	ticker := &gct.Ticker{Last: 100, Bid: 99.98, Ask: 100.02, Volume: 1}
	args := map[string]interface{}{"exchange": "kucoin", "pair": "BTC-USDT"}

	_, err := NewAnalyzeOpportunityTool(testAdapter(&fakeGCT{ticker: ticker, candErr: errors.New("down")})).Execute(context.Background(), args)
	assert.True(t, common.IsDataUnavailable(err))

	_, err = NewAnalyzeOpportunityTool(testAdapter(&fakeGCT{ticker: ticker, candles: candlesWithRange(5, 1)})).Execute(context.Background(), args)
	assert.True(t, common.IsDataUnavailable(err))
}

func TestGetSignalsDataUnavailableWhenNoPairHasData(t *testing.T) {
	a := testAdapter(&fakeGCT{ticker: &gct.Ticker{Last: 100}, candErr: errors.New("down")})
	tool := NewGetSignalsTool(a)
	res, err := tool.Execute(context.Background(), map[string]interface{}{"exchange": "kucoin", "pairs": []interface{}{"BTC-USDT"}})
	assert.Nil(t, res)
	assert.True(t, common.IsDataUnavailable(err))
}
