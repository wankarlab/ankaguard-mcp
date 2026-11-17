package indicators

import (
	"fmt"
	"math"
)

// ATR calculates the Average True Range
// TR = max(high - low, abs(high - prevClose), abs(low - prevClose))
// ATR = EMA or SMA of TR
func ATR(highs, lows, closes []float64, period int, useEMA bool) ([]float64, error) {
	if len(highs) != len(lows) || len(highs) != len(closes) {
		return nil, fmt.Errorf("highs, lows, and closes must have the same length")
	}

	if len(highs) < period+1 {
		return nil, fmt.Errorf("insufficient data: need at least %d candles, got %d", period+1, len(highs))
	}

	if period <= 0 {
		return nil, fmt.Errorf("period must be positive, got %d", period)
	}

	// Calculate True Range for each period
	trValues := make([]float64, len(highs)-1)

	for i := 1; i < len(highs); i++ {
		high := highs[i]
		low := lows[i]
		prevClose := closes[i-1]

		tr1 := high - low
		tr2 := math.Abs(high - prevClose)
		tr3 := math.Abs(low - prevClose)

		trValues[i-1] = math.Max(tr1, math.Max(tr2, tr3))
	}

	// Calculate ATR using EMA or SMA
	var atrValues []float64
	var err error

	if useEMA {
		atrValues, err = EMA(trValues, period)
	} else {
		atrValues, err = SMA(trValues, period)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to calculate ATR: %w", err)
	}

	return atrValues, nil
}

// ATRSimple calculates ATR from a simple price array (using close prices)
// This is a simplified version that uses close-to-close changes
func ATRSimple(prices []float64, period int) ([]float64, error) {
	if len(prices) < period+1 {
		return nil, fmt.Errorf("insufficient data: need at least %d prices, got %d", period+1, len(prices))
	}

	// Calculate absolute price changes
	changes := make([]float64, len(prices)-1)
	for i := 1; i < len(prices); i++ {
		changes[i-1] = math.Abs(prices[i] - prices[i-1])
	}

	// Calculate SMA of changes
	return SMA(changes, period)
}

// ATRCache provides caching for ATR calculations
type ATRCache struct {
	period      int
	useEMA      bool
	emaCache    *EMACache
	smaCache    *SMACache
	lastATR     float64
	lastClose   float64
	initialized bool
}

// NewATRCache creates a new ATR cache
func NewATRCache(period int, useEMA bool) *ATRCache {
	cache := &ATRCache{
		period: period,
		useEMA: useEMA,
	}

	if useEMA {
		cache.emaCache = NewEMACache(period)
	} else {
		cache.smaCache = NewSMACache(period)
	}

	return cache
}

// Calculate calculates ATR with caching
func (c *ATRCache) Calculate(high, low, close float64) (float64, error) {
	if !c.initialized {
		c.lastClose = close
		c.initialized = true
		return 0, fmt.Errorf("need at least 2 candles to calculate ATR")
	}

	// Calculate True Range
	tr1 := high - low
	tr2 := math.Abs(high - c.lastClose)
	tr3 := math.Abs(low - c.lastClose)

	tr := math.Max(tr1, math.Max(tr2, tr3))

	// Update ATR using EMA or SMA
	var atr float64
	var err error

	if c.useEMA {
		atr, err = c.emaCache.Calculate([]float64{tr})
	} else {
		atr = c.smaCache.Calculate(tr)
		if c.smaCache.IsReady() {
			err = nil
		} else {
			err = fmt.Errorf("not enough data for ATR calculation")
		}
	}

	if err == nil {
		c.lastATR = atr
	}

	c.lastClose = close

	return atr, err
}

// GetLastATR returns the last calculated ATR value
func (c *ATRCache) GetLastATR() float64 {
	return c.lastATR
}

// IsReady returns true if the cache has enough data
func (c *ATRCache) IsReady() bool {
	if c.useEMA {
		return c.emaCache.initialized
	}
	return c.smaCache.IsReady()
}

// Reset resets the cache
func (c *ATRCache) Reset() {
	if c.useEMA {
		c.emaCache.Reset()
	} else {
		c.smaCache.Reset()
	}
	c.lastATR = 0
	c.lastClose = 0
	c.initialized = false
}

// CalculateStopLoss calculates stop loss based on ATR
// For long positions: entryPrice - (atr * multiplier)
// For short positions: entryPrice + (atr * multiplier)
func CalculateStopLoss(entryPrice, atr float64, multiplier float64, isLong bool) float64 {
	if isLong {
		return entryPrice - (atr * multiplier)
	}
	return entryPrice + (atr * multiplier)
}

// CalculateTakeProfit calculates take profit based on ATR
// For long positions: entryPrice + (atr * multiplier)
// For short positions: entryPrice - (atr * multiplier)
func CalculateTakeProfit(entryPrice, atr float64, multiplier float64, isLong bool) float64 {
	if isLong {
		return entryPrice + (atr * multiplier)
	}
	return entryPrice - (atr * multiplier)
}

// ATRBasedSLTP calculates both SL and TP based on ATR
type ATRBasedSLTP struct {
	StopLoss   float64
	TakeProfit float64
}

// CalculateATRBasedSLTP calculates SL/TP based on ATR with different multipliers
func CalculateATRBasedSLTP(entryPrice, atr float64, slMultiplier, tpMultiplier float64, isLong bool) *ATRBasedSLTP {
	return &ATRBasedSLTP{
		StopLoss:   CalculateStopLoss(entryPrice, atr, slMultiplier, isLong),
		TakeProfit: CalculateTakeProfit(entryPrice, atr, tpMultiplier, isLong),
	}
}
