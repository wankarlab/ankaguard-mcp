package technical

import (
	"context"
	"fmt"
	"math"
	"sync"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// GetCorrelationTool calculates Pearson correlation matrix between multiple trading pairs
type GetCorrelationTool struct {
	BaseTool
}

// NewGetCorrelationTool creates a new GetCorrelationTool instance
func NewGetCorrelationTool(client *gctclient.Client) *GetCorrelationTool {
	return &GetCorrelationTool{
		BaseTool: BaseTool{client: client},
	}
}

// Name returns the tool name
func (t *GetCorrelationTool) Name() string {
	return "get_correlation"
}

// Description returns the tool description
func (t *GetCorrelationTool) Description() string {
	return "Calculate Pearson correlation matrix between multiple trading pairs. " +
		"Useful for portfolio diversification analysis, identifying correlated assets, " +
		"and finding pairs for statistical arbitrage strategies."
}

// InputSchema returns the JSON schema for input parameters
func (t *GetCorrelationTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'binance', 'kucoin')",
			},
			"pairs": map[string]interface{}{
				"type":        "array",
				"description": "Array of trading pairs to analyze (minimum 2 pairs required). E.g., ['BTC-USDT', 'ETH-USDT', 'SOL-USDT']",
				"items": map[string]interface{}{
					"type": "string",
				},
				"minItems": 2,
			},
			"interval": map[string]interface{}{
				"type":        "string",
				"description": "Candle interval for correlation calculation",
				"enum":        []string{"1h", "4h", "1d"},
				"default":     "1h",
			},
			"period": map[string]interface{}{
				"type":        "integer",
				"description": "Number of candles to use for correlation calculation (default: 100, max: 500)",
				"default":     100,
				"minimum":     10,
				"maximum":     500,
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Market type: 'spot', 'futures', 'margin', or 'options' (optional, enables symbol conversion)",
				"enum":        []string{"spot", "futures", "margin", "options"},
			},
		},
		"required": []string{"exchange", "pairs"},
	}
}

// CorrelationPair represents a pair of assets and their correlation
type CorrelationPair struct {
	Pair1       string  `json:"pair1"`
	Pair2       string  `json:"pair2"`
	Correlation float64 `json:"correlation"`
}

// CorrelationResult represents the full correlation analysis result
type CorrelationResult struct {
	Exchange             string                        `json:"exchange"`
	Pairs                []string                      `json:"pairs"`
	Interval             string                        `json:"interval"`
	Period               int                           `json:"period"`
	CorrelationMatrix    map[string]map[string]float64 `json:"correlation_matrix"`
	StrongestCorrelation CorrelationPair               `json:"strongest_correlation"`
	WeakestCorrelation   CorrelationPair               `json:"weakest_correlation"`
	PositiveCorrelations []CorrelationPair             `json:"positive_correlations"`
	NegativeCorrelations []CorrelationPair             `json:"negative_correlations"`
	DataPoints           int                           `json:"data_points"`
}

// Execute runs the correlation calculation
func (t *GetCorrelationTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Validate exchange
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required and must be a string",
		}
	}

	// Validate pairs array
	pairsRaw, ok := params["pairs"]
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "pairs is required and must be an array of strings",
		}
	}

	pairs, err := t.extractPairs(pairsRaw)
	if err != nil {
		return nil, err
	}

	if len(pairs) < 2 {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "at least 2 pairs are required for correlation calculation",
		}
	}

	// Get optional parameters
	interval := getStringParam(params, "interval", "1h")
	period := getIntParam(params, "period", 100)

	// Validate interval
	validIntervals := map[string]bool{"1h": true, "4h": true, "1d": true}
	if !validIntervals[interval] {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "interval must be one of: 1h, 4h, 1d",
		}
	}

	// Clamp period
	if period < 10 {
		period = 10
	}
	if period > 500 {
		period = 500
	}

	// Handle optional market_type parameter for symbol conversion
	marketType, hasMarketType := params["market_type"].(string)
	if hasMarketType && marketType != "" {
		if err := t.validateMarketType(marketType); err != nil {
			return nil, err
		}

		// Convert all pairs
		convertedPairs := make([]string, len(pairs))
		for i, pair := range pairs {
			converted, err := t.convertSymbol(pair, marketType, exchange)
			if err != nil {
				return nil, &models.MCPError{
					Code:    -32602,
					Message: "Symbol conversion failed",
					Data:    fmt.Sprintf("failed to convert pair %s: %v", pair, err),
				}
			}
			convertedPairs[i] = converted
		}
		pairs = convertedPairs
	}

	return executeWithContext(ctx, func(ctx context.Context) (interface{}, error) {
		return t.calculateCorrelation(ctx, exchange, pairs, interval, period)
	})
}

// extractPairs extracts pair strings from the raw interface value
func (t *GetCorrelationTool) extractPairs(pairsRaw interface{}) ([]string, error) {
	var pairs []string

	switch v := pairsRaw.(type) {
	case []interface{}:
		for _, p := range v {
			if pStr, ok := p.(string); ok && pStr != "" {
				pairs = append(pairs, pStr)
			}
		}
	case []string:
		pairs = v
	default:
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "pairs must be an array of strings",
		}
	}

	return pairs, nil
}

// getStringParam gets a string parameter with default value
func getStringParam(params map[string]interface{}, key string, defaultValue string) string {
	if val, ok := params[key].(string); ok && val != "" {
		return val
	}
	return defaultValue
}

// calculateCorrelation fetches candle data and calculates correlation matrix
func (t *GetCorrelationTool) calculateCorrelation(
	ctx context.Context,
	exchange string,
	pairs []string,
	interval string,
	period int,
) (*CorrelationResult, error) {
	// Fetch candles for all pairs concurrently
	candleData := make(map[string][]models.Candle)
	var mu sync.Mutex
	var wg sync.WaitGroup
	errChan := make(chan error, len(pairs))

	for _, pair := range pairs {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()

			candles, err := t.client.GetCandles(ctx, exchange, p, interval, period)
			if err != nil {
				errChan <- fmt.Errorf("failed to get candles for %s: %w", p, err)
				return
			}

			mu.Lock()
			candleData[p] = candles
			mu.Unlock()
		}(pair)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	if err := <-errChan; err != nil {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to fetch candle data",
			Data:    err.Error(),
		}
	}

	// Extract closing prices and calculate returns
	returns := make(map[string][]float64)
	minLen := period

	for pair, candles := range candleData {
		if len(candles) < 2 {
			return nil, &models.MCPError{
				Code:    -32603,
				Message: "Insufficient data",
				Data:    fmt.Sprintf("not enough candle data for %s: got %d, need at least 2", pair, len(candles)),
			}
		}

		if len(candles) < minLen {
			minLen = len(candles)
		}

		// Calculate returns (percentage change)
		pairReturns := make([]float64, len(candles)-1)
		for i := 1; i < len(candles); i++ {
			if candles[i-1].Close != 0 {
				pairReturns[i-1] = (candles[i].Close - candles[i-1].Close) / candles[i-1].Close
			}
		}
		returns[pair] = pairReturns
	}

	// Align all return series to the same length
	dataPoints := minLen - 1
	if dataPoints < 2 {
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Insufficient data",
			Data:    "need at least 2 data points for correlation calculation",
		}
	}

	for pair := range returns {
		if len(returns[pair]) > dataPoints {
			// Take the most recent data points
			returns[pair] = returns[pair][len(returns[pair])-dataPoints:]
		}
	}

	// Calculate correlation matrix
	correlationMatrix := make(map[string]map[string]float64)
	var allCorrelations []CorrelationPair
	var positiveCorrelations []CorrelationPair
	var negativeCorrelations []CorrelationPair

	for _, pair1 := range pairs {
		correlationMatrix[pair1] = make(map[string]float64)
		for _, pair2 := range pairs {
			if pair1 == pair2 {
				correlationMatrix[pair1][pair2] = 1.0
			} else {
				corr := pearsonCorrelation(returns[pair1], returns[pair2])
				correlationMatrix[pair1][pair2] = corr

				// Only add unique pairs (avoid duplicates)
				if pair1 < pair2 {
					cp := CorrelationPair{
						Pair1:       pair1,
						Pair2:       pair2,
						Correlation: corr,
					}
					allCorrelations = append(allCorrelations, cp)

					if corr > 0 {
						positiveCorrelations = append(positiveCorrelations, cp)
					} else if corr < 0 {
						negativeCorrelations = append(negativeCorrelations, cp)
					}
				}
			}
		}
	}

	// Find strongest and weakest correlations
	var strongest, weakest CorrelationPair
	if len(allCorrelations) > 0 {
		strongest = allCorrelations[0]
		weakest = allCorrelations[0]

		for _, cp := range allCorrelations {
			// Strongest = highest absolute correlation
			if math.Abs(cp.Correlation) > math.Abs(strongest.Correlation) {
				strongest = cp
			}
			// Weakest = lowest absolute correlation (closest to 0)
			if math.Abs(cp.Correlation) < math.Abs(weakest.Correlation) {
				weakest = cp
			}
		}
	}

	// Sort correlations by absolute value (descending)
	sortCorrelationsByAbsValue(positiveCorrelations)
	sortCorrelationsByAbsValue(negativeCorrelations)

	result := &CorrelationResult{
		Exchange:             exchange,
		Pairs:                pairs,
		Interval:             interval,
		Period:               period,
		CorrelationMatrix:    correlationMatrix,
		StrongestCorrelation: strongest,
		WeakestCorrelation:   weakest,
		PositiveCorrelations: positiveCorrelations,
		NegativeCorrelations: negativeCorrelations,
		DataPoints:           dataPoints,
	}

	return result, nil
}

// pearsonCorrelation calculates Pearson correlation coefficient between two series
func pearsonCorrelation(x, y []float64) float64 {
	n := len(x)
	if n != len(y) || n == 0 {
		return 0
	}

	// Calculate means
	var sumX, sumY float64
	for i := 0; i < n; i++ {
		sumX += x[i]
		sumY += y[i]
	}
	meanX := sumX / float64(n)
	meanY := sumY / float64(n)

	// Calculate covariance and standard deviations
	var cov, varX, varY float64
	for i := 0; i < n; i++ {
		dx := x[i] - meanX
		dy := y[i] - meanY
		cov += dx * dy
		varX += dx * dx
		varY += dy * dy
	}

	// Avoid division by zero
	if varX == 0 || varY == 0 {
		return 0
	}

	stdX := math.Sqrt(varX / float64(n))
	stdY := math.Sqrt(varY / float64(n))

	correlation := (cov / float64(n)) / (stdX * stdY)

	// Clamp to [-1, 1] to handle floating point errors
	if correlation > 1 {
		correlation = 1
	} else if correlation < -1 {
		correlation = -1
	}

	// Round to 4 decimal places for cleaner output
	return math.Round(correlation*10000) / 10000
}

// sortCorrelationsByAbsValue sorts correlations by absolute value (descending)
func sortCorrelationsByAbsValue(correlations []CorrelationPair) {
	for i := 0; i < len(correlations)-1; i++ {
		for j := i + 1; j < len(correlations); j++ {
			if math.Abs(correlations[i].Correlation) < math.Abs(correlations[j].Correlation) {
				correlations[i], correlations[j] = correlations[j], correlations[i]
			}
		}
	}
}
