package indicators

import (
	"fmt"
	"math"
)

// RSI calculates the Relative Strength Index
// RSI = 100 - (100 / (1 + RS))
// where RS = Average Gain / Average Loss
func RSI(prices []float64, period int) ([]float64, error) {
	if len(prices) < period+1 {
		return nil, fmt.Errorf("insufficient data: need at least %d prices, got %d", period+1, len(prices))
	}

	if period <= 0 {
		return nil, fmt.Errorf("period must be positive, got %d", period)
	}

	// Calculate price changes
	changes := make([]float64, len(prices)-1)
	for i := 1; i < len(prices); i++ {
		changes[i-1] = prices[i] - prices[i-1]
	}

	// Separate gains and losses
	gains := make([]float64, len(changes))
	losses := make([]float64, len(changes))

	for i, change := range changes {
		if change > 0 {
			gains[i] = change
			losses[i] = 0
		} else {
			gains[i] = 0
			losses[i] = math.Abs(change)
		}
	}

	// Calculate initial average gain and loss
	var avgGain, avgLoss float64
	for i := 0; i < period; i++ {
		avgGain += gains[i]
		avgLoss += losses[i]
	}
	avgGain /= float64(period)
	avgLoss /= float64(period)

	// Calculate RSI values
	rsiValues := make([]float64, len(prices)-period)

	for i := 0; i < len(rsiValues); i++ {
		var rs, rsi float64

		if avgLoss == 0 {
			rsi = 100
		} else {
			rs = avgGain / avgLoss
			rsi = 100 - (100 / (1 + rs))
		}

		rsiValues[i] = rsi

		// Update averages for next iteration (Wilder's smoothing)
		if i < len(gains)-period {
			avgGain = ((avgGain * float64(period-1)) + gains[period+i]) / float64(period)
			avgLoss = ((avgLoss * float64(period-1)) + losses[period+i]) / float64(period)
		}
	}

	return rsiValues, nil
}

// RSIWithCache calculates RSI with caching support
type RSICache struct {
	period   int
	avgGain  float64
	avgLoss  float64
	lastRSI  float64
	dataSize int
}

// NewRSICache creates a new RSI cache
func NewRSICache(period int) *RSICache {
	return &RSICache{
		period: period,
	}
}

// Calculate calculates RSI with caching
func (c *RSICache) Calculate(prices []float64) (float64, error) {
	if len(prices) < c.period+1 {
		return 0, fmt.Errorf("insufficient data: need at least %d prices, got %d", c.period+1, len(prices))
	}

	// If this is the first calculation or data size changed significantly
	if c.dataSize == 0 || math.Abs(float64(len(prices)-c.dataSize)) > float64(c.period) {
		// Full recalculation
		rsiValues, err := RSI(prices, c.period)
		if err != nil {
			return 0, err
		}

		c.lastRSI = rsiValues[len(rsiValues)-1]
		c.dataSize = len(prices)

		// Calculate and store averages for next incremental update
		changes := make([]float64, len(prices)-1)
		for i := 1; i < len(prices); i++ {
			changes[i-1] = prices[i] - prices[i-1]
		}

		var avgGain, avgLoss float64
		startIdx := len(changes) - c.period
		for i := startIdx; i < len(changes); i++ {
			if changes[i] > 0 {
				avgGain += changes[i]
			} else {
				avgLoss += math.Abs(changes[i])
			}
		}
		c.avgGain = avgGain / float64(c.period)
		c.avgLoss = avgLoss / float64(c.period)

		return c.lastRSI, nil
	}

	// Incremental update
	lastChange := prices[len(prices)-1] - prices[len(prices)-2]
	var gain, loss float64

	if lastChange > 0 {
		gain = lastChange
	} else {
		loss = math.Abs(lastChange)
	}

	// Wilder's smoothing
	c.avgGain = ((c.avgGain * float64(c.period-1)) + gain) / float64(c.period)
	c.avgLoss = ((c.avgLoss * float64(c.period-1)) + loss) / float64(c.period)

	var rsi float64
	if c.avgLoss == 0 {
		rsi = 100
	} else {
		rs := c.avgGain / c.avgLoss
		rsi = 100 - (100 / (1 + rs))
	}

	c.lastRSI = rsi
	c.dataSize = len(prices)

	return rsi, nil
}

// GetLastRSI returns the last calculated RSI value
func (c *RSICache) GetLastRSI() float64 {
	return c.lastRSI
}

// CalculateRSI computes the final RSI value for a price series.
// It is a convenience wrapper around RSI that returns a single float64.
// Returns 50.0 (neutral) when there is insufficient data or an invalid period.
func CalculateRSI(prices []float64, period int) float64 {
	values, err := RSI(prices, period)
	if err != nil || len(values) == 0 {
		return 50.0
	}
	return values[len(values)-1]
}
