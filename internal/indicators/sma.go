package indicators

import (
	"fmt"
)

// SMA calculates the Simple Moving Average
func SMA(prices []float64, period int) ([]float64, error) {
	if len(prices) < period {
		return nil, fmt.Errorf("insufficient data: need at least %d prices, got %d", period, len(prices))
	}

	if period <= 0 {
		return nil, fmt.Errorf("period must be positive, got %d", period)
	}

	smaValues := make([]float64, len(prices)-period+1)

	// Calculate first SMA
	var sum float64
	for i := 0; i < period; i++ {
		sum += prices[i]
	}
	smaValues[0] = sum / float64(period)

	// Calculate remaining SMAs using sliding window
	for i := 1; i < len(smaValues); i++ {
		sum = sum - prices[i-1] + prices[period+i-1]
		smaValues[i] = sum / float64(period)
	}

	return smaValues, nil
}

// SMACache provides caching for SMA calculations
type SMACache struct {
	period  int
	window  []float64
	sum     float64
	lastSMA float64
}

// NewSMACache creates a new SMA cache
func NewSMACache(period int) *SMACache {
	return &SMACache{
		period: period,
		window: make([]float64, 0, period),
	}
}

// Calculate calculates SMA with caching
func (c *SMACache) Calculate(price float64) float64 {
	// Add new price to window
	c.window = append(c.window, price)
	c.sum += price

	// If window exceeds period, remove oldest price
	if len(c.window) > c.period {
		c.sum -= c.window[0]
		c.window = c.window[1:]
	}

	// Calculate SMA
	if len(c.window) == c.period {
		c.lastSMA = c.sum / float64(c.period)
	}

	return c.lastSMA
}

// GetLastSMA returns the last calculated SMA value
func (c *SMACache) GetLastSMA() float64 {
	return c.lastSMA
}

// IsReady returns true if the cache has enough data
func (c *SMACache) IsReady() bool {
	return len(c.window) == c.period
}

// Reset resets the cache
func (c *SMACache) Reset() {
	c.window = make([]float64, 0, c.period)
	c.sum = 0
	c.lastSMA = 0
}
