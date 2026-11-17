package orders

import (
	"context"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/sirupsen/logrus"
)

// Default simulation parameters
const (
	DefaultSlippagePercent = 0.05 // 0.05% slippage
	DefaultMakerFeePercent = 0.10 // 0.10% maker fee
	DefaultTakerFeePercent = 0.10 // 0.10% taker fee
	EstimatedFillTimeMs    = 50   // Estimated fill time in milliseconds
)

// SimulateOrderTool simulates order execution without placing real orders
type SimulateOrderTool struct {
	BaseTool
	// ticker is the price source; *gctclient.Client in production, a fake in tests.
	ticker tickerSource
}

type tickerSource interface {
	GetTicker(ctx context.Context, exchange, pair string) (*models.Ticker, error)
}

// NewSimulateOrderTool creates a new simulate order tool
func NewSimulateOrderTool(client *gctclient.Client) *SimulateOrderTool {
	return &SimulateOrderTool{BaseTool: BaseTool{client: client}, ticker: client}
}

func (t *SimulateOrderTool) Name() string {
	return "simulate_order"
}

func (t *SimulateOrderTool) Description() string {
	return "Simulate an order execution and return an estimate with its assumptions (price source, slippage, fees). Calculates execution price with slippage, trading fees, total cost/proceeds, and validates against risk limits WITHOUT executing a real order."
}

func (t *SimulateOrderTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT' for spot, 'BTCUSDTM' for futures, or 'BTC-USDT' with market_type)",
			},
			"side": map[string]interface{}{
				"type":        "string",
				"description": "Order side: 'buy' or 'sell'",
				"enum":        []string{"buy", "sell"},
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "Order type: 'market', 'limit', or 'stop'",
				"enum":        []string{"market", "limit", "stop"},
			},
			"amount": map[string]interface{}{
				"type":        "number",
				"description": "Order amount/quantity in base currency",
			},
			"price": map[string]interface{}{
				"type":        "number",
				"description": "Limit price (required for limit orders, optional for market orders simulation)",
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Market type: 'spot', 'futures', 'margin', or 'options' (optional, enables symbol conversion and validation)",
				"enum":        []string{"spot", "futures", "margin", "options"},
			},
			"slippage_percent": map[string]interface{}{
				"type":        "number",
				"description": fmt.Sprintf("Simulated slippage percentage (default: %.2f%%)", DefaultSlippagePercent),
			},
			"maker_fee_percent": map[string]interface{}{
				"type":        "number",
				"description": fmt.Sprintf("Maker fee percentage (default: %.2f%%)", DefaultMakerFeePercent),
			},
			"taker_fee_percent": map[string]interface{}{
				"type":        "number",
				"description": fmt.Sprintf("Taker fee percentage (default: %.2f%%)", DefaultTakerFeePercent),
			},
		},
		"required": []string{"exchange", "pair", "side", "type", "amount"},
	}
}

// SimulationResult contains the detailed simulation output
type SimulationResult struct {
	Simulated   bool             `json:"simulated"`
	Order       OrderDetails     `json:"order"`
	Execution   ExecutionDetails `json:"execution"`
	RiskCheck   RiskCheckResult  `json:"risk_check"`
	Timestamp   time.Time        `json:"timestamp"`
	SimulatedAt string           `json:"simulated_at"`
}

// OrderDetails contains the order parameters that were simulated
type OrderDetails struct {
	Exchange   string  `json:"exchange"`
	Pair       string  `json:"pair"`
	Side       string  `json:"side"`
	Type       string  `json:"type"`
	Amount     float64 `json:"amount"`
	Price      float64 `json:"price,omitempty"`
	MarketType string  `json:"market_type,omitempty"`
}

// ExecutionDetails contains the simulated execution information
type ExecutionDetails struct {
	MarketPrice     float64 `json:"market_price"`
	ExecutionPrice  float64 `json:"execution_price"`
	SlippagePercent float64 `json:"slippage"`
	SlippageAmount  float64 `json:"slippage_amount"`
	Fee             float64 `json:"fee"`
	FeePercent      float64 `json:"fee_percent"`
	TotalCost       float64 `json:"total_cost"`
	TotalProceeds   float64 `json:"total_proceeds"`
	EstFillTimeMs   int     `json:"estimated_fill_time_ms"`
}

// RiskCheckResult contains the risk validation results
type RiskCheckResult struct {
	Passed   bool                   `json:"passed"`
	Warnings []string               `json:"warnings"`
	Details  map[string]interface{} `json:"details,omitempty"`
}

func (t *SimulateOrderTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	startTime := time.Now()
	logger := getLogger()

	// Validate basic order parameters
	exchange, pair, side, err := t.validateOrderParams(params)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"tool":  t.Name(),
			"error": err.Error(),
		}).Warn("Simulation validation failed")
		return nil, err
	}

	orderType := getStringParam(params, "type", "market")
	amount, err := getFloatParam(params, "amount", true)
	if err != nil {
		return nil, err
	}

	price, _ := getFloatParam(params, "price", false)
	marketType := getStringParam(params, "market_type", "")

	// Get simulation parameters with defaults
	slippagePercent := getFloatParamWithDefault(params, "slippage_percent", DefaultSlippagePercent)
	makerFeePercent := getFloatParamWithDefault(params, "maker_fee_percent", DefaultMakerFeePercent)
	takerFeePercent := getFloatParamWithDefault(params, "taker_fee_percent", DefaultTakerFeePercent)

	// Log simulation start with details
	logger.WithFields(logrus.Fields{
		"tool":             t.Name(),
		"exchange":         exchange,
		"pair":             pair,
		"side":             side,
		"order_type":       orderType,
		"amount":           amount,
		"price":            price,
		"slippage_percent": slippagePercent,
		"maker_fee":        makerFeePercent,
		"taker_fee":        takerFeePercent,
	}).Info("Executing tool")

	// Handle optional market_type parameter for symbol conversion
	if marketType != "" {
		if err := t.validateMarketType(marketType); err != nil {
			return nil, err
		}
		convertedPair, err := t.convertSymbol(pair, marketType, exchange)
		if err != nil {
			return nil, err
		}
		pair = convertedPair
	}

	// Fetch current market price
	ticker, err := t.ticker.GetTicker(ctx, exchange, pair)
	if err != nil {
		logger.WithFields(logrus.Fields{
			"tool":     t.Name(),
			"exchange": exchange,
			"pair":     pair,
			"error":    err.Error(),
		}).Error("Failed to fetch market price for simulation")
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Failed to fetch market price",
			Data:    err.Error(),
		}
	}

	marketPrice := ticker.Last
	if marketPrice <= 0 {
		logger.WithFields(logrus.Fields{
			"tool":         t.Name(),
			"exchange":     exchange,
			"pair":         pair,
			"market_price": marketPrice,
		}).Error("Invalid market price for simulation")
		return nil, &models.MCPError{
			Code:    -32603,
			Message: "Invalid market price",
			Data:    "Market price is zero or negative",
		}
	}

	// Calculate execution details
	execution := t.calculateExecution(
		side,
		orderType,
		amount,
		price,
		marketPrice,
		slippagePercent,
		makerFeePercent,
		takerFeePercent,
	)

	// Perform risk validation
	riskCheck := t.performRiskCheck(exchange, pair, side, amount, execution.ExecutionPrice)

	// Build result
	result := map[string]interface{}{
		"simulated": true,
		// Estimate: nothing here was executed or reserved at the exchange.
		"estimate": true,
		"assumptions": map[string]interface{}{
			"price_source":            "last traded price of the exchange ticker at the time of the call",
			"slippage_percent":        slippagePercent,
			"maker_fee_percent":       makerFeePercent,
			"taker_fee_percent":       takerFeePercent,
			"fee_applied":             "taker fee for market orders, maker fee for limit orders",
			"limit_orders":            "assumed to fill completely at the limit price with no slippage",
			"order_book_depth":        "not modeled; the whole amount fills at one price",
			"estimated_fill_time_ms":  "fixed nominal value, not measured",
			"risk_check_limits":       "generic reference thresholds, not the configured risk limits",
			"slippage_and_fee_inputs": "defaults unless slippage_percent, maker_fee_percent or taker_fee_percent are passed",
		},
		"order": map[string]interface{}{
			"exchange": exchange,
			"pair":     pair,
			"side":     side,
			"type":     orderType,
			"amount":   amount,
		},
		"execution": map[string]interface{}{
			"market_price":           marketPrice,
			"execution_price":        execution.ExecutionPrice,
			"slippage":               execution.SlippagePercent,
			"slippage_amount":        execution.SlippageAmount,
			"fee":                    execution.Fee,
			"fee_percent":            execution.FeePercent,
			"total_cost":             execution.TotalCost,
			"total_proceeds":         execution.TotalProceeds,
			"estimated_fill_time_ms": execution.EstFillTimeMs,
		},
		"risk_check": map[string]interface{}{
			"passed":   riskCheck.Passed,
			"warnings": riskCheck.Warnings,
		},
		"timestamp":    time.Now().UTC(),
		"simulated_at": time.Now().UTC().Format(time.RFC3339),
	}

	// Add optional fields
	if price > 0 {
		result["order"].(map[string]interface{})["price"] = price
	}
	if marketType != "" {
		result["order"].(map[string]interface{})["market_type"] = marketType
	}
	if len(riskCheck.Details) > 0 {
		result["risk_check"].(map[string]interface{})["details"] = riskCheck.Details
	}

	duration := time.Since(startTime)

	// Log simulation completion with execution details
	logger.WithFields(logrus.Fields{
		"tool":            t.Name(),
		"exchange":        exchange,
		"pair":            pair,
		"duration_ms":     duration.Milliseconds(),
		"success":         true,
		"market_price":    marketPrice,
		"execution_price": execution.ExecutionPrice,
		"total_cost":      execution.TotalCost,
		"total_proceeds":  execution.TotalProceeds,
		"fee":             execution.Fee,
		"risk_passed":     riskCheck.Passed,
		"warnings_count":  len(riskCheck.Warnings),
	}).Info("Tool completed")

	return result, nil
}

// calculateExecution calculates the simulated execution details
func (t *SimulateOrderTool) calculateExecution(
	side, orderType string,
	amount, limitPrice, marketPrice float64,
	slippagePercent, makerFeePercent, takerFeePercent float64,
) ExecutionDetails {

	var executionPrice float64
	var feePercent float64
	var slippageAmount float64

	switch orderType {
	case "market":
		// Market orders execute at market price with slippage
		slippageAmount = marketPrice * (slippagePercent / 100)
		if side == "buy" {
			// Buying pushes price up (worse execution)
			executionPrice = marketPrice + slippageAmount
		} else {
			// Selling pushes price down (worse execution)
			executionPrice = marketPrice - slippageAmount
		}
		// Market orders pay taker fee
		feePercent = takerFeePercent

	case "limit":
		// Limit orders execute at limit price (if filled)
		if limitPrice > 0 {
			executionPrice = limitPrice
			slippageAmount = 0 // No slippage for limit orders
			// Limit orders typically pay maker fee
			feePercent = makerFeePercent
		} else {
			// If no limit price, fall back to market price
			executionPrice = marketPrice
			feePercent = takerFeePercent
		}

	case "stop":
		// Stop orders execute at market price once triggered
		slippageAmount = marketPrice * (slippagePercent / 100)
		if side == "buy" {
			executionPrice = marketPrice + slippageAmount
		} else {
			executionPrice = marketPrice - slippageAmount
		}
		feePercent = takerFeePercent

	default:
		executionPrice = marketPrice
		feePercent = takerFeePercent
	}

	// Calculate total order value
	orderValue := amount * executionPrice

	// Calculate fee
	fee := orderValue * (feePercent / 100)

	// Calculate total cost (for buy) or proceeds (for sell)
	var totalCost, totalProceeds float64
	if side == "buy" {
		totalCost = orderValue + fee
		totalProceeds = 0
	} else {
		totalCost = 0
		totalProceeds = orderValue - fee
	}

	return ExecutionDetails{
		MarketPrice:     marketPrice,
		ExecutionPrice:  roundTo8Decimals(executionPrice),
		SlippagePercent: slippagePercent,
		SlippageAmount:  roundTo8Decimals(slippageAmount),
		Fee:             roundTo8Decimals(fee),
		FeePercent:      feePercent,
		TotalCost:       roundTo2Decimals(totalCost),
		TotalProceeds:   roundTo2Decimals(totalProceeds),
		EstFillTimeMs:   EstimatedFillTimeMs,
	}
}

// performRiskCheck validates the order against risk limits
func (t *SimulateOrderTool) performRiskCheck(
	exchange, pair, side string,
	amount, executionPrice float64,
) RiskCheckResult {
	warnings := []string{}
	details := make(map[string]interface{})
	passed := true

	orderValue := amount * executionPrice
	details["order_value_usd"] = roundTo2Decimals(orderValue)

	// Check order size limits (these could be configurable)
	maxOrderSize := 10000.0 // Default max order size in USD
	if orderValue > maxOrderSize {
		warnings = append(warnings, fmt.Sprintf(
			"Order value ($%.2f) exceeds recommended maximum ($%.2f)",
			orderValue, maxOrderSize,
		))
	}

	// Check for very small orders (potential waste on fees)
	minOrderSize := 1.0 // Minimum recommended order size
	if orderValue < minOrderSize {
		warnings = append(warnings, fmt.Sprintf(
			"Order value ($%.2f) is below minimum recommended size ($%.2f)",
			orderValue, minOrderSize,
		))
	}

	// Check for very large amounts
	if amount > 1000000 {
		warnings = append(warnings, "Large order amount may experience higher slippage")
		passed = false
	}

	// Estimate fee impact
	estimatedFeePercent := 0.1 // Approximate fee
	feeImpact := orderValue * (estimatedFeePercent / 100)
	if feeImpact > orderValue*0.01 { // If fees > 1% of order
		details["fee_impact_warning"] = fmt.Sprintf(
			"Trading fees (%.4f) represent a significant portion of the order",
			feeImpact,
		)
	}

	return RiskCheckResult{
		Passed:   passed,
		Warnings: warnings,
		Details:  details,
	}
}

// Helper function to get float param with default value
func getFloatParamWithDefault(params map[string]interface{}, key string, defaultValue float64) float64 {
	if val, ok := params[key]; ok {
		switch v := val.(type) {
		case float64:
			return v
		case int:
			return float64(v)
		case int64:
			return float64(v)
		}
	}
	return defaultValue
}

// Helper function to round to 8 decimal places
func roundTo8Decimals(value float64) float64 {
	return float64(int64(value*100000000+0.5)) / 100000000
}

// Helper function to round to 2 decimal places
func roundTo2Decimals(value float64) float64 {
	return float64(int64(value*100+0.5)) / 100
}
