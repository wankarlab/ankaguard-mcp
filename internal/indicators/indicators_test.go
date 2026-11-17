package indicators

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test data - simple price series
var testPrices = []float64{
	44.34, 44.09, 44.15, 43.61, 44.33,
	44.83, 45.10, 45.42, 45.84, 46.08,
	45.89, 46.03, 45.61, 46.28, 46.28,
	46.00, 46.03, 46.41, 46.22, 45.64,
}

// TestRSI tests RSI calculation
func TestRSI(t *testing.T) {
	// Test with period 14
	rsi, err := RSI(testPrices, 14)
	require.NoError(t, err)
	require.NotEmpty(t, rsi)

	// RSI should be between 0 and 100
	for i, val := range rsi {
		assert.True(t, val >= 0 && val <= 100, "RSI[%d] = %.2f should be between 0 and 100", i, val)
	}

	// Last RSI value should be reasonable (around 70 for uptrend)
	lastRSI := rsi[len(rsi)-1]
	assert.True(t, lastRSI > 40 && lastRSI < 80, "Last RSI = %.2f", lastRSI)
}

func TestRSI_InsufficientData(t *testing.T) {
	shortPrices := []float64{44.34, 44.09}
	_, err := RSI(shortPrices, 14)
	assert.Error(t, err)
}

func TestRSI_InvalidPeriod(t *testing.T) {
	_, err := RSI(testPrices, 0)
	assert.Error(t, err)

	_, err = RSI(testPrices, -1)
	assert.Error(t, err)
}

// TestRSICache tests RSI caching
func TestRSICache(t *testing.T) {
	cache := NewRSICache(14)

	// First calculation
	rsi1, err := cache.Calculate(testPrices)
	require.NoError(t, err)
	assert.True(t, rsi1 > 0 && rsi1 <= 100)

	// Add one more price and recalculate
	newPrices := append(testPrices, 45.50)
	rsi2, err := cache.Calculate(newPrices)
	require.NoError(t, err)
	assert.True(t, rsi2 > 0 && rsi2 <= 100)

	// RSI should change slightly
	assert.NotEqual(t, rsi1, rsi2)
}

// TestSMA tests Simple Moving Average
func TestSMA(t *testing.T) {
	sma, err := SMA(testPrices, 5)
	require.NoError(t, err)
	require.NotEmpty(t, sma)

	// First SMA should be average of first 5 prices
	expected := (testPrices[0] + testPrices[1] + testPrices[2] + testPrices[3] + testPrices[4]) / 5
	assert.InDelta(t, expected, sma[0], 0.01)
}

func TestSMA_InsufficientData(t *testing.T) {
	shortPrices := []float64{44.34, 44.09}
	_, err := SMA(shortPrices, 5)
	assert.Error(t, err)
}

// TestEMA tests Exponential Moving Average
func TestEMA(t *testing.T) {
	ema, err := EMA(testPrices, 5)
	require.NoError(t, err)
	require.NotEmpty(t, ema)

	// EMA should be different from SMA
	sma, _ := SMA(testPrices, 5)
	assert.NotEqual(t, sma[len(sma)-1], ema[len(ema)-1])

	// EMA should react faster to price changes
	assert.True(t, len(ema) == len(sma))
}

func TestEMA_InsufficientData(t *testing.T) {
	shortPrices := []float64{44.34, 44.09}
	_, err := EMA(shortPrices, 5)
	assert.Error(t, err)
}

// TestEMACache tests EMA caching
func TestEMACache(t *testing.T) {
	cache := NewEMACache(5)

	// Calculate EMA
	ema1, err := cache.Calculate(testPrices[:10])
	require.NoError(t, err)
	assert.True(t, ema1 > 0)

	// Add more data
	ema2, err := cache.Calculate(testPrices)
	require.NoError(t, err)
	assert.True(t, ema2 > 0)

	// EMA should change
	assert.NotEqual(t, ema1, ema2)
}

// TestMACD tests MACD calculation
func TestMACD(t *testing.T) {
	// Need more data for MACD (at least 26 + 9 = 35 periods)
	longPrices := make([]float64, 50)
	for i := range longPrices {
		longPrices[i] = 100.0 + float64(i)*0.5 + math.Sin(float64(i)*0.3)*2
	}

	macd, err := MACD(longPrices, 12, 26, 9)
	require.NoError(t, err)
	require.NotNil(t, macd)
	require.NotEmpty(t, macd.MACD)
	require.NotEmpty(t, macd.Signal)
	require.NotEmpty(t, macd.Histogram)

	// All arrays should have the same length
	assert.Equal(t, len(macd.MACD), len(macd.Signal))
	assert.Equal(t, len(macd.MACD), len(macd.Histogram))

	// Histogram should be MACD - Signal
	for i := range macd.Histogram {
		expected := macd.MACD[i] - macd.Signal[i]
		assert.InDelta(t, expected, macd.Histogram[i], 0.0001)
	}
}

func TestMACD_InsufficientData(t *testing.T) {
	_, err := MACD(testPrices, 12, 26, 9)
	assert.Error(t, err)
}

func TestMACD_InvalidPeriods(t *testing.T) {
	longPrices := make([]float64, 50)
	for i := range longPrices {
		longPrices[i] = 100.0 + float64(i)*0.5
	}

	// Fast period >= slow period should fail
	_, err := MACD(longPrices, 26, 12, 9)
	assert.Error(t, err)
}

// TestBollingerBands tests Bollinger Bands calculation
func TestBollingerBands(t *testing.T) {
	bb, err := BollingerBandsCalculate(testPrices, 5, 2.0)
	require.NoError(t, err)
	require.NotNil(t, bb)
	require.NotEmpty(t, bb.Upper)
	require.NotEmpty(t, bb.Middle)
	require.NotEmpty(t, bb.Lower)

	// All arrays should have the same length
	assert.Equal(t, len(bb.Upper), len(bb.Middle))
	assert.Equal(t, len(bb.Upper), len(bb.Lower))

	// Upper should be > Middle > Lower
	for i := range bb.Upper {
		assert.True(t, bb.Upper[i] > bb.Middle[i], "Upper[%d] should be > Middle[%d]", i, i)
		assert.True(t, bb.Middle[i] > bb.Lower[i], "Middle[%d] should be > Lower[%d]", i, i)
	}
}

func TestBollingerBands_InsufficientData(t *testing.T) {
	shortPrices := []float64{44.34, 44.09}
	_, err := BollingerBandsCalculate(shortPrices, 5, 2.0)
	assert.Error(t, err)
}

// TestBollingerBandsCache tests Bollinger Bands caching
func TestBollingerBandsCache(t *testing.T) {
	cache := NewBollingerBandsCache(5, 2.0)

	// Feed prices one by one
	for _, price := range testPrices {
		upper, middle, lower := cache.Calculate(price)

		if cache.IsReady() {
			assert.True(t, upper > middle)
			assert.True(t, middle > lower)
		}
	}

	// Check final values
	upper, middle, lower := cache.GetLast()
	assert.True(t, upper > middle)
	assert.True(t, middle > lower)

	// Test %B indicator
	lastPrice := testPrices[len(testPrices)-1]
	percentB := cache.GetPercentB(lastPrice)
	assert.True(t, percentB >= 0 && percentB <= 1, "%%B should be between 0 and 1")
}

// TestATR tests ATR calculation
func TestATR(t *testing.T) {
	// Create test OHLC data
	highs := make([]float64, len(testPrices))
	lows := make([]float64, len(testPrices))
	closes := testPrices

	for i := range testPrices {
		highs[i] = testPrices[i] + 0.5
		lows[i] = testPrices[i] - 0.5
	}

	// Test with EMA
	atrEMA, err := ATR(highs, lows, closes, 14, true)
	require.NoError(t, err)
	require.NotEmpty(t, atrEMA)

	// ATR should be positive
	for i, val := range atrEMA {
		assert.True(t, val > 0, "ATR[%d] = %.2f should be positive", i, val)
	}

	// Test with SMA
	atrSMA, err := ATR(highs, lows, closes, 14, false)
	require.NoError(t, err)
	require.NotEmpty(t, atrSMA)

	// Both should have same length
	assert.Equal(t, len(atrEMA), len(atrSMA))
}

func TestATR_InsufficientData(t *testing.T) {
	highs := []float64{45.0, 46.0}
	lows := []float64{44.0, 45.0}
	closes := []float64{44.5, 45.5}

	_, err := ATR(highs, lows, closes, 14, true)
	assert.Error(t, err)
}

func TestATR_MismatchedLengths(t *testing.T) {
	highs := []float64{45.0, 46.0, 47.0}
	lows := []float64{44.0, 45.0}
	closes := []float64{44.5, 45.5, 46.5}

	_, err := ATR(highs, lows, closes, 2, true)
	assert.Error(t, err)
}

// TestATRCache tests ATR caching
func TestATRCache(t *testing.T) {
	cache := NewATRCache(5, true)

	// Feed OHLC data
	var lastATR float64
	var lastErr error
	for i := range testPrices {
		high := testPrices[i] + 0.5
		low := testPrices[i] - 0.5
		close := testPrices[i]

		atr, err := cache.Calculate(high, low, close)
		lastATR = atr
		lastErr = err

		if i > 5 && cache.IsReady() {
			require.NoError(t, err)
			assert.True(t, atr > 0)
		}
	}

	// Check final ATR - should be valid after enough data
	if lastErr == nil {
		assert.True(t, lastATR > 0)
	}
}

// TestCalculateStopLoss tests ATR-based stop loss calculation
func TestCalculateStopLoss(t *testing.T) {
	entryPrice := 100.0
	atr := 2.0
	multiplier := 1.5

	// Long position
	slLong := CalculateStopLoss(entryPrice, atr, multiplier, true)
	assert.Equal(t, 97.0, slLong) // 100 - (2 * 1.5)

	// Short position
	slShort := CalculateStopLoss(entryPrice, atr, multiplier, false)
	assert.Equal(t, 103.0, slShort) // 100 + (2 * 1.5)
}

// TestCalculateTakeProfit tests ATR-based take profit calculation
func TestCalculateTakeProfit(t *testing.T) {
	entryPrice := 100.0
	atr := 2.0
	multiplier := 2.0

	// Long position
	tpLong := CalculateTakeProfit(entryPrice, atr, multiplier, true)
	assert.Equal(t, 104.0, tpLong) // 100 + (2 * 2)

	// Short position
	tpShort := CalculateTakeProfit(entryPrice, atr, multiplier, false)
	assert.Equal(t, 96.0, tpShort) // 100 - (2 * 2)
}

// TestCalculateATRBasedSLTP tests combined SL/TP calculation
func TestCalculateATRBasedSLTP(t *testing.T) {
	entryPrice := 100.0
	atr := 2.0
	slMultiplier := 1.5
	tpMultiplier := 3.0

	// Long position
	sltpLong := CalculateATRBasedSLTP(entryPrice, atr, slMultiplier, tpMultiplier, true)
	assert.Equal(t, 97.0, sltpLong.StopLoss)
	assert.Equal(t, 106.0, sltpLong.TakeProfit)

	// Short position
	sltpShort := CalculateATRBasedSLTP(entryPrice, atr, slMultiplier, tpMultiplier, false)
	assert.Equal(t, 103.0, sltpShort.StopLoss)
	assert.Equal(t, 94.0, sltpShort.TakeProfit)
}
