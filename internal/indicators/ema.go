package indicators

import (
	"fmt"
)

// EMA calculates the Exponential Moving Average
// EMA = Price(t) * k + EMA(y) * (1 - k)
// where k = 2 / (N + 1) and N = period
func EMA(prices []float64, period int) ([]float64, error) {
	if len(prices) < period {
		return nil, fmt.Errorf("insufficient data: need at least %d prices, got %d", period, len(prices))
	}

	if period <= 0 {
		return nil, fmt.Errorf("period must be positive, got %d", period)
	}

	// Calculate multiplier
	k := 2.0 / float64(period+1)

	// Calculate initial SMA as the first EMA value
	var sum float64
	for i := 0; i < period; i++ {
		sum += prices[i]
	}
	initialEMA := sum / float64(period)

	// Calculate EMA values
	emaValues := make([]float64, len(prices)-period+1)
	emaValues[0] = initialEMA

	for i := 1; i < len(emaValues); i++ {
		price := prices[period-1+i]
		emaValues[i] = (price * k) + (emaValues[i-1] * (1 - k))
	}

	return emaValues, nil
}

// EMACache provides caching for EMA calculations
type EMACache struct {
	period      int
	lastEMA     float64
	k           float64
	initialized bool
}

// NewEMACache creates a new EMA cache
func NewEMACache(period int) *EMACache {
	return &EMACache{
		period: period,
		k:      2.0 / float64(period+1),
	}
}

// Calculate calculates EMA with caching
func (c *EMACache) Calculate(prices []float64) (float64, error) {
	if len(prices) < c.period {
		return 0, fmt.Errorf("insufficient data: need at least %d prices, got %d", c.period, len(prices))
	}

	if !c.initialized {
		// Calculate initial SMA
		var sum float64
		for i := 0; i < c.period; i++ {
			sum += prices[i]
		}
		c.lastEMA = sum / float64(c.period)
		c.initialized = true

		// If we have more data, calculate EMA for remaining prices
		for i := c.period; i < len(prices); i++ {
			c.lastEMA = (prices[i] * c.k) + (c.lastEMA * (1 - c.k))
		}
	} else {
		// Incremental update with new price
		lastPrice := prices[len(prices)-1]
		c.lastEMA = (lastPrice * c.k) + (c.lastEMA * (1 - c.k))
	}

	return c.lastEMA, nil
}

// GetLastEMA returns the last calculated EMA value
func (c *EMACache) GetLastEMA() float64 {
	return c.lastEMA
}

// Reset resets the cache
func (c *EMACache) Reset() {
	c.lastEMA = 0
	c.initialized = false
}
