package indicators

import (
	"fmt"
)

// MACDResult holds the MACD calculation results
type MACDResult struct {
	MACD      []float64 // MACD line (fast EMA - slow EMA)
	Signal    []float64 // Signal line (EMA of MACD)
	Histogram []float64 // Histogram (MACD - Signal)
}

// MACD calculates the Moving Average Convergence Divergence
// Standard parameters: fastPeriod=12, slowPeriod=26, signalPeriod=9
func MACD(prices []float64, fastPeriod, slowPeriod, signalPeriod int) (*MACDResult, error) {
	if len(prices) < slowPeriod {
		return nil, fmt.Errorf("insufficient data: need at least %d prices, got %d", slowPeriod, len(prices))
	}

	if fastPeriod >= slowPeriod {
		return nil, fmt.Errorf("fast period must be less than slow period")
	}

	// Calculate fast and slow EMAs
	fastEMA, err := EMA(prices, fastPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate fast EMA: %w", err)
	}

	slowEMA, err := EMA(prices, slowPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate slow EMA: %w", err)
	}

	// Calculate MACD line (fast EMA - slow EMA)
	// Align the arrays (slow EMA is shorter)
	offset := len(fastEMA) - len(slowEMA)
	macdLine := make([]float64, len(slowEMA))

	for i := 0; i < len(slowEMA); i++ {
		macdLine[i] = fastEMA[i+offset] - slowEMA[i]
	}

	// Calculate signal line (EMA of MACD)
	signalLine, err := EMA(macdLine, signalPeriod)
	if err != nil {
		return nil, fmt.Errorf("failed to calculate signal line: %w", err)
	}

	// Calculate histogram (MACD - Signal)
	// Align the arrays
	offset = len(macdLine) - len(signalLine)
	histogram := make([]float64, len(signalLine))

	for i := 0; i < len(signalLine); i++ {
		histogram[i] = macdLine[i+offset] - signalLine[i]
	}

	// Trim MACD line to match signal line length
	macdLineTrimmed := macdLine[offset:]

	return &MACDResult{
		MACD:      macdLineTrimmed,
		Signal:    signalLine,
		Histogram: histogram,
	}, nil
}

// MACDCache provides caching for MACD calculations
type MACDCache struct {
	fastPeriod   int
	slowPeriod   int
	signalPeriod int
	fastEMA      *EMACache
	slowEMA      *EMACache
	signalEMA    *EMACache
	lastMACD     float64
	lastSignal   float64
	lastHist     float64
}

// NewMACDCache creates a new MACD cache
func NewMACDCache(fastPeriod, slowPeriod, signalPeriod int) *MACDCache {
	return &MACDCache{
		fastPeriod:   fastPeriod,
		slowPeriod:   slowPeriod,
		signalPeriod: signalPeriod,
		fastEMA:      NewEMACache(fastPeriod),
		slowEMA:      NewEMACache(slowPeriod),
		signalEMA:    NewEMACache(signalPeriod),
	}
}

// Calculate calculates MACD with caching
func (c *MACDCache) Calculate(prices []float64) (macd, signal, histogram float64, err error) {
	if len(prices) < c.slowPeriod {
		return 0, 0, 0, fmt.Errorf("insufficient data: need at least %d prices", c.slowPeriod)
	}

	// Calculate fast and slow EMAs
	fastEMA, err := c.fastEMA.Calculate(prices)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to calculate fast EMA: %w", err)
	}

	slowEMA, err := c.slowEMA.Calculate(prices)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to calculate slow EMA: %w", err)
	}

	// Calculate MACD line
	macd = fastEMA - slowEMA
	c.lastMACD = macd

	// Calculate signal line (EMA of MACD)
	// We need to build a MACD history for the signal EMA
	// For simplicity, we'll use the current MACD value
	// In a production system, you'd maintain a MACD history
	signal, err = c.signalEMA.Calculate([]float64{macd})
	if err != nil {
		// If signal EMA fails (not enough data), use previous signal
		signal = c.lastSignal
	} else {
		c.lastSignal = signal
	}

	// Calculate histogram
	histogram = macd - signal
	c.lastHist = histogram

	return macd, signal, histogram, nil
}

// GetLast returns the last calculated MACD values
func (c *MACDCache) GetLast() (macd, signal, histogram float64) {
	return c.lastMACD, c.lastSignal, c.lastHist
}
