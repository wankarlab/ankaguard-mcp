package ai

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// Mock GCT Client
type mockGCTClient struct{}

func (m *mockGCTClient) GetCandles(ctx context.Context, exchange, pair, interval string, limit int) ([]interface{}, error) {
	// Return mock candles
	candles := make([]interface{}, limit)
	for i := 0; i < limit; i++ {
		candles[i] = map[string]interface{}{
			"open":   100.0 + float64(i)*0.1,
			"high":   101.0 + float64(i)*0.1,
			"low":    99.0 + float64(i)*0.1,
			"close":  100.5 + float64(i)*0.1,
			"volume": 1000.0,
		}
	}
	return candles, nil
}

func (m *mockGCTClient) GetTicker(ctx context.Context, exchange, pair string) (interface{}, error) {
	return map[string]interface{}{
		"last": 100.5,
	}, nil
}

func (m *mockGCTClient) CalculateRSI(ctx context.Context, exchange, pair string, period int) (float64, error) {
	return 45.0, nil
}

func (m *mockGCTClient) CalculateMACD(ctx context.Context, exchange, pair string, fast, slow, signal int) (interface{}, error) {
	return map[string]interface{}{
		"macd":      0.5,
		"signal":    0.3,
		"histogram": 0.2,
	}, nil
}

func (m *mockGCTClient) CalculateBollingerBands(ctx context.Context, exchange, pair string, period int, stdDev float64) (interface{}, error) {
	return map[string]interface{}{
		"upper":  102.0,
		"middle": 100.0,
		"lower":  98.0,
	}, nil
}

func TestDataCollector(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	client := &mockGCTClient{}
	collector := NewDataCollector(client, "kucoin", log)

	ctx := context.Background()
	data, err := collector.CollectData(ctx, "BTC-USDT")

	assert.NoError(t, err)
	assert.NotNil(t, data)
	assert.NotNil(t, data.GetTimeframe("15m"))
	assert.NotNil(t, data.GetTimeframe("5m"))
	assert.Equal(t, "BTC-USDT", data.Pair)
}

func TestIndicatorCalculator(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	client := &mockGCTClient{}
	calc := NewIndicatorCalculator(client, "kucoin", log)

	// Enough candles for local RSI(14)/MACD(12,26,9)/Bollinger(20) computation,
	// monotonically rising closes → RSI must read strongly bullish.
	candles := make([]Candle, 60)
	price := 100.0
	for i := range candles {
		price *= 1.01 // accelerating rally → RSI high, MACD histogram positive
		candles[i] = Candle{
			Timestamp: time.Now().Add(time.Duration(i-60) * time.Minute),
			Open:      price * 0.998, High: price * 1.005, Low: price * 0.995,
			Close: price, Volume: 1000,
		}
	}

	ctx := context.Background()
	indicators, err := calc.CalculateAll(ctx, "BTC-USDT", candles)

	assert.NoError(t, err)
	assert.NotNil(t, indicators)
	// Indicators are computed from the provided candles, NOT the remote client
	// (the mock returns RSI 45 — a rising series must NOT produce that).
	assert.Greater(t, indicators.RSI, 70.0)
	assert.LessOrEqual(t, indicators.RSI, 100.0)
	assert.NotNil(t, indicators.MACD)
	assert.Equal(t, "bullish", indicators.MACD.Trend)
	assert.NotNil(t, indicators.Bollinger)
	assert.Greater(t, indicators.Bollinger.Upper, indicators.Bollinger.Lower)

	// Insufficient candles → neutral RSI fallback (50), no remote call.
	short := candles[:5]
	shortInd, err := calc.CalculateAll(ctx, "BTC-USDT", short)
	assert.NoError(t, err)
	assert.Equal(t, 50.0, shortInd.RSI)
}

func TestContextBuilder(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	client := &mockGCTClient{}
	collector := NewDataCollector(client, "kucoin", log)
	indicatorCalc := NewIndicatorCalculator(client, "kucoin", log)
	builder := NewContextBuilder(collector, indicatorCalc, log)

	ctx := context.Background()
	marketCtx, err := builder.BuildContext(ctx, "BTC-USDT")

	assert.NoError(t, err)
	assert.NotNil(t, marketCtx)
	assert.Equal(t, "BTC-USDT", marketCtx.Pair)
	assert.NotNil(t, marketCtx.GetTimeframe("15m"))
	assert.NotNil(t, marketCtx.GetTimeframe("5m"))
	assert.Greater(t, marketCtx.CurrentPrice, 0.0)
}

func TestDecisionEngine(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	client := &mockGCTClient{}
	collector := NewDataCollector(client, "kucoin", log)
	indicatorCalc := NewIndicatorCalculator(client, "kucoin", log)
	builder := NewContextBuilder(collector, indicatorCalc, log)
	engine := NewDecisionEngine(builder, log)

	ctx := context.Background()
	marketCtx, err := builder.BuildContext(ctx, "BTC-USDT")
	assert.NoError(t, err)

	decision, err := engine.EvaluateOpportunity(ctx, marketCtx)

	assert.NoError(t, err)
	assert.NotNil(t, decision)
	assert.Contains(t, []string{"BUY", "SELL", "WAIT"}, decision.Action)
	assert.GreaterOrEqual(t, decision.Confidence, 0.0)
	assert.LessOrEqual(t, decision.Confidence, 1.0)
	assert.NotEmpty(t, decision.Reason)
}

func TestTrendAlignment(t *testing.T) {
	log := logrus.New()
	builder := NewContextBuilder(nil, nil, log)

	// Test aligned bullish
	ctx := &MarketContext{
		Style: &Strategy{
			Timeframes: []TimeframeConfig{
				{Interval: "5m", Weight: 1.0},
				{Interval: "15m", Weight: 0.5},
			},
		},
		Timeframes: map[string]*TimeframeContext{
			"15m": {Trend: "bullish"},
			"5m":  {Trend: "bullish"},
		},
	}
	aligned, direction := builder.GetTrendAlignment(ctx)
	assert.True(t, aligned)
	assert.Equal(t, "bullish", direction)

	// Test aligned bearish
	ctx = &MarketContext{
		Style: &Strategy{
			Timeframes: []TimeframeConfig{
				{Interval: "5m", Weight: 1.0},
				{Interval: "15m", Weight: 0.5},
			},
		},
		Timeframes: map[string]*TimeframeContext{
			"15m": {Trend: "bearish"},
			"5m":  {Trend: "bearish"},
		},
	}
	aligned, direction = builder.GetTrendAlignment(ctx)
	assert.True(t, aligned)
	assert.Equal(t, "bearish", direction)

	// Test not aligned
	ctx = &MarketContext{
		Style: &Strategy{
			Timeframes: []TimeframeConfig{
				{Interval: "5m", Weight: 1.0},
				{Interval: "15m", Weight: 0.5},
			},
		},
		Timeframes: map[string]*TimeframeContext{
			"15m": {Trend: "bullish"},
			"5m":  {Trend: "bearish"},
		},
	}
	aligned, direction = builder.GetTrendAlignment(ctx)
	assert.False(t, aligned)
	assert.Equal(t, "mixed", direction)
}

func TestKeyLevels(t *testing.T) {
	log := logrus.New()
	calc := NewIndicatorCalculator(nil, "kucoin", log)

	candles := []Candle{
		{Close: 100, High: 102, Low: 98},
		{Close: 101, High: 103, Low: 99},
		{Close: 102, High: 104, Low: 100},
		{Close: 101, High: 103, Low: 99},
		{Close: 100, High: 102, Low: 98},
		{Close: 99, High: 101, Low: 97},
		{Close: 100, High: 102, Low: 98},
		{Close: 101, High: 103, Low: 99},
		{Close: 102, High: 104, Low: 100},
		{Close: 103, High: 105, Low: 101},
	}

	levels := calc.DetectKeyLevels(candles)

	assert.NotNil(t, levels)
	assert.Greater(t, levels.Resistance, levels.Support)
	assert.NotNil(t, levels.Distance)
}
