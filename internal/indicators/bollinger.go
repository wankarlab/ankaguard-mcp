package indicators

import (
	"fmt"
	"math"
)

// BollingerBands holds the Bollinger Bands calculation results
type BollingerBands struct {
	Upper  []float64 // Upper band (SMA + k * stddev)
	Middle []float64 // Middle band (SMA)
	Lower  []float64 // Lower band (SMA - k * stddev)
}

// BollingerBandsCalculate calculates Bollinger Bands
// Standard parameters: period=20, stdDevMultiplier=2
func BollingerBandsCalculate(prices []float64, period int, stdDevMultiplier float64) (*BollingerBands, error) {
	if len(prices) < period {
		return nil, fmt.Errorf("insufficient data: need at least %d prices, got %d", period, len(prices))
	}

	if period <= 0 {
		return nil, fmt.Errorf("period must be positive, got %d", period)
	}

	// Calculate SMA (middle band)
	smaValues, err := SMA(prices, period)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate SMA: %w", err)
	}

	// Calculate standard deviation for each period
	upper := make([]float64, len(smaValues))
	lower := make([]float64, len(smaValues))

	for i := 0; i < len(smaValues); i++ {
		// Calculate standard deviation for this window
		startIdx := i
		endIdx := i + period

		var sumSquaredDiff float64
		for j := startIdx; j < endIdx; j++ {
			diff := prices[j] - smaValues[i]
			sumSquaredDiff += diff * diff
		}

		stdDev := math.Sqrt(sumSquaredDiff / float64(period))

		upper[i] = smaValues[i] + (stdDevMultiplier * stdDev)
		lower[i] = smaValues[i] - (stdDevMultiplier * stdDev)
	}

	return &BollingerBands{
		Upper:  upper,
		Middle: smaValues,
		Lower:  lower,
	}, nil
}

// BollingerBandsCache provides caching for Bollinger Bands calculations
type BollingerBandsCache struct {
	period           int
	stdDevMultiplier float64
	smaCache         *SMACache
	window           []float64
	lastUpper        float64
	lastMiddle       float64
	lastLower        float64
}

// NewBollingerBandsCache creates a new Bollinger Bands cache
func NewBollingerBandsCache(period int, stdDevMultiplier float64) *BollingerBandsCache {
	return &BollingerBandsCache{
		period:           period,
		stdDevMultiplier: stdDevMultiplier,
		smaCache:         NewSMACache(period),
		window:           make([]float64, 0, period),
	}
}

// Calculate calculates Bollinger Bands with caching
func (c *BollingerBandsCache) Calculate(price float64) (upper, middle, lower float64) {
	// Add price to window
	c.window = append(c.window, price)
	if len(c.window) > c.period {
		c.window = c.window[1:]
	}

	// Calculate SMA (middle band)
	middle = c.smaCache.Calculate(price)
	c.lastMiddle = middle

	// If we don't have enough data yet, return previous values
	if !c.smaCache.IsReady() {
		return c.lastUpper, c.lastMiddle, c.lastLower
	}

	// Calculate standard deviation
	var sumSquaredDiff float64
	for _, p := range c.window {
		diff := p - middle
		sumSquaredDiff += diff * diff
	}

	stdDev := math.Sqrt(sumSquaredDiff / float64(c.period))

	// Calculate bands
	upper = middle + (c.stdDevMultiplier * stdDev)
	lower = middle - (c.stdDevMultiplier * stdDev)

	c.lastUpper = upper
	c.lastLower = lower

	return upper, middle, lower
}

// GetLast returns the last calculated Bollinger Bands values
func (c *BollingerBandsCache) GetLast() (upper, middle, lower float64) {
	return c.lastUpper, c.lastMiddle, c.lastLower
}

// IsReady returns true if the cache has enough data
func (c *BollingerBandsCache) IsReady() bool {
	return c.smaCache.IsReady()
}

// Reset resets the cache
func (c *BollingerBandsCache) Reset() {
	c.smaCache.Reset()
	c.window = make([]float64, 0, c.period)
	c.lastUpper = 0
	c.lastMiddle = 0
	c.lastLower = 0
}

// GetBandWidth returns the width of the bands (upper - lower)
func (c *BollingerBandsCache) GetBandWidth() float64 {
	return c.lastUpper - c.lastLower
}

// GetPercentB returns %B indicator (where price is relative to bands)
// %B = (price - lower) / (upper - lower)
func (c *BollingerBandsCache) GetPercentB(price float64) float64 {
	bandWidth := c.lastUpper - c.lastLower
	if bandWidth == 0 {
		return 0.5 // Middle of bands
	}
	return (price - c.lastLower) / bandWidth
}
