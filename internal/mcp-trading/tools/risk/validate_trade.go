package risk

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	internalRisk "github.com/rantipay/trading-mcp/internal/risk"
	"github.com/rantipay/trading-mcp/internal/trading"
)

// OrderSizeLimits defines min/max order sizes per market type
type OrderSizeLimits struct {
	MinOrderSize float64
	MaxOrderSize float64
}

// ValidateTradeTool validates a trade before execution
type ValidateTradeTool struct {
	client          *gctclient.Client
	config          *config.Config
	riskSentinel    internalRisk.RiskSentinel
	positionManager trading.PositionTracker
}

// NewValidateTradeTool creates a new ValidateTradeTool
func NewValidateTradeTool(
	client *gctclient.Client,
	cfg *config.Config,
	riskSentinel internalRisk.RiskSentinel,
	positionManager trading.PositionTracker,
) *ValidateTradeTool {
	return &ValidateTradeTool{
		client:          client,
		config:          cfg,
		riskSentinel:    riskSentinel,
		positionManager: positionManager,
	}
}

// Name returns the tool name
func (t *ValidateTradeTool) Name() string {
	return "validate_trade"
}

// Description returns the tool description
func (t *ValidateTradeTool) Description() string {
	return "Validate a trade before execution. Checks order parameters, balance sufficiency, position size limits, daily loss limits, leverage limits, pair availability, market hours, and order size constraints."
}

// InputSchema returns the input schema
func (t *ValidateTradeTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name (e.g., 'kucoin', 'binance')",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT', 'ETH-USDT')",
			},
			"side": map[string]interface{}{
				"type":        "string",
				"description": "Order side: 'buy' or 'sell'",
				"enum":        []string{"buy", "sell"},
			},
			"amount": map[string]interface{}{
				"type":        "number",
				"description": "Order amount in base currency",
			},
			"price": map[string]interface{}{
				"type":        "number",
				"description": "Order price in quote currency (use current price for market orders)",
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Market type: 'spot' or 'futures' (default: 'spot')",
				"enum":        []string{"spot", "futures", "margin"},
				"default":     "spot",
			},
			"leverage": map[string]interface{}{
				"type":        "number",
				"description": "Leverage for futures/margin trades (default: 1)",
				"default":     1,
			},
			"order_type": map[string]interface{}{
				"type":        "string",
				"description": "Order type: 'market' or 'limit' (default: 'limit')",
				"enum":        []string{"market", "limit", "stop", "stop_limit"},
				"default":     "limit",
			},
		},
		"required": []string{"exchange", "pair", "side", "amount", "price"},
	}
}

// ValidationResult represents the detailed validation result
type ValidationResult struct {
	Valid       bool                        `json:"valid"`
	Order       OrderDetails                `json:"order"`
	Validations map[string]ValidationCheck  `json:"validations"`
	Warnings    []string                    `json:"warnings"`
	Errors      []string                    `json:"errors"`
	Summary     string                      `json:"summary"`
}

// OrderDetails represents the parsed order details
type OrderDetails struct {
	Exchange   string  `json:"exchange"`
	Pair       string  `json:"pair"`
	Side       string  `json:"side"`
	Amount     float64 `json:"amount"`
	Price      float64 `json:"price"`
	ValueUSD   float64 `json:"value_usd"`
	MarketType string  `json:"market_type"`
	Leverage   float64 `json:"leverage"`
	OrderType  string  `json:"order_type"`
}

// ValidationCheck represents a single validation check result
type ValidationCheck struct {
	Passed  bool        `json:"passed"`
	Message string      `json:"message,omitempty"`
	Details interface{} `json:"details,omitempty"`
}

// Execute executes the validate_trade tool
func (t *ValidateTradeTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	// Parse required parameters
	exchange, ok := args["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required and must be a string",
		}
	}

	pair, ok := args["pair"].(string)
	if !ok || pair == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "pair is required and must be a string",
		}
	}

	side, ok := args["side"].(string)
	if !ok || (side != "buy" && side != "sell") {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "side must be 'buy' or 'sell'",
		}
	}

	amount, ok := args["amount"].(float64)
	if !ok || amount <= 0 {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "amount must be a positive number",
		}
	}

	price, ok := args["price"].(float64)
	if !ok || price <= 0 {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "price must be a positive number",
		}
	}

	// Parse optional parameters
	marketType := "spot"
	if mt, ok := args["market_type"].(string); ok && mt != "" {
		marketType = mt
	}

	leverage := 1.0
	if lev, ok := args["leverage"].(float64); ok && lev > 0 {
		leverage = lev
	}

	orderType := "limit"
	if ot, ok := args["order_type"].(string); ok && ot != "" {
		orderType = ot
	}

	// Calculate order value in USD
	valueUSD := amount * price

	// Initialize result
	result := &ValidationResult{
		Valid: true,
		Order: OrderDetails{
			Exchange:   strings.ToLower(exchange),
			Pair:       strings.ToUpper(pair),
			Side:       side,
			Amount:     amount,
			Price:      price,
			ValueUSD:   valueUSD,
			MarketType: marketType,
			Leverage:   leverage,
			OrderType:  orderType,
		},
		Validations: make(map[string]ValidationCheck),
		Warnings:    make([]string, 0),
		Errors:      make([]string, 0),
	}

	// Run all validation checks
	t.validatePairAvailability(ctx, result, exchange, pair, marketType)
	t.validateBalanceSufficiency(ctx, result, exchange, pair, side, amount, price)
	t.validateOrderSize(ctx, result, exchange, pair, amount, price, marketType)
	t.validatePositionLimit(ctx, result, exchange, pair, valueUSD, marketType)
	t.validateDailyLossLimit(ctx, result)
	t.validateLeverageLimit(ctx, result, leverage, marketType)
	t.validateMarketHours(ctx, result, exchange, marketType)
	t.validateRiskSentinel(ctx, result, pair, side, amount, price, leverage)

	// Generate summary
	result.Summary = t.generateSummary(result)

	return result, nil
}

// validatePairAvailability checks if the trading pair is available on the exchange
func (t *ValidateTradeTool) validatePairAvailability(ctx context.Context, result *ValidationResult, exchange, pair, marketType string) {
	check := ValidationCheck{
		Passed: true,
		Details: map[string]interface{}{
			"exchange":    exchange,
			"pair":        pair,
			"market_type": marketType,
		},
	}

	// Try to get ticker to verify pair exists
	if t.client != nil && t.client.IsConnected() {
		_, err := t.client.GetTicker(ctx, exchange, pair)
		if err != nil {
			check.Passed = false
			check.Message = fmt.Sprintf("Pair %s may not be available on %s: %v", pair, exchange, err)
			result.Warnings = append(result.Warnings, check.Message)
		} else {
			check.Message = fmt.Sprintf("Pair %s is available on %s", pair, exchange)
		}
	} else {
		check.Message = "Cannot verify pair availability - client not connected (assuming available)"
		result.Warnings = append(result.Warnings, check.Message)
	}

	result.Validations["pair_available"] = check
}

// validateBalanceSufficiency checks if the account has sufficient balance
func (t *ValidateTradeTool) validateBalanceSufficiency(ctx context.Context, result *ValidationResult, exchange, pair, side string, amount, price float64) {
	check := ValidationCheck{
		Passed: true,
	}

	// Determine the currency needed based on side
	parts := strings.Split(pair, "-")
	var requiredCurrency string
	var requiredAmount float64

	if side == "buy" {
		// For buy orders, we need quote currency (e.g., USDT)
		if len(parts) >= 2 {
			requiredCurrency = parts[1]
		} else {
			requiredCurrency = "USDT"
		}
		requiredAmount = amount * price
	} else {
		// For sell orders, we need base currency (e.g., BTC)
		if len(parts) >= 1 {
			requiredCurrency = parts[0]
		} else {
			requiredCurrency = pair
		}
		requiredAmount = amount
	}

	details := map[string]interface{}{
		"currency": requiredCurrency,
		"required": requiredAmount,
		"side":     side,
	}

	// Try to get balance
	if t.client != nil && t.client.IsConnected() {
		balance, err := t.client.GetBalance(ctx, exchange, requiredCurrency)
		if err != nil {
			check.Message = fmt.Sprintf("Cannot verify balance for %s: %v", requiredCurrency, err)
			result.Warnings = append(result.Warnings, check.Message)
			details["available"] = "unknown"
		} else {
			details["available"] = balance.Available
			details["locked"] = balance.Locked
			details["total"] = balance.Total

			if balance.Available < requiredAmount {
				check.Passed = false
				check.Message = fmt.Sprintf("Insufficient balance: available %.6f %s, required %.6f %s",
					balance.Available, requiredCurrency, requiredAmount, requiredCurrency)
				result.Errors = append(result.Errors, check.Message)
				result.Valid = false
			} else {
				check.Message = fmt.Sprintf("Sufficient balance: %.6f %s available", balance.Available, requiredCurrency)
			}
		}
	} else {
		check.Message = "Cannot verify balance - client not connected"
		result.Warnings = append(result.Warnings, check.Message)
		details["available"] = "unknown"
	}

	check.Details = details
	result.Validations["balance_check"] = check
}

// validateOrderSize checks if the order size is within limits
func (t *ValidateTradeTool) validateOrderSize(ctx context.Context, result *ValidationResult, exchange, pair string, amount, price float64, marketType string) {
	limits := t.getOrderSizeLimits(exchange, pair, marketType)
	orderValueUSD := amount * price

	// TODO(TC-3.4): migrate MaxOrderSizeUSD to dynamic config key "risk_limits.max_order_size_usd"
	// once that key is added to the manifest.
	maxOrderSizeUSD := t.config.RiskLimits.MaxOrderSizeUSD

	check := ValidationCheck{
		Passed: true,
		Details: map[string]interface{}{
			"amount":        amount,
			"min":           limits.MinOrderSize,
			"max":           limits.MaxOrderSize,
			"value_usd":     orderValueUSD,
			"max_value_usd": maxOrderSizeUSD,
		},
	}

	// Check minimum order size
	if amount < limits.MinOrderSize {
		check.Passed = false
		check.Message = fmt.Sprintf("Order amount %.6f is below minimum %.6f", amount, limits.MinOrderSize)
		result.Errors = append(result.Errors, check.Message)
		result.Valid = false
	} else if amount > limits.MaxOrderSize {
		// Check maximum order size
		check.Passed = false
		check.Message = fmt.Sprintf("Order amount %.6f exceeds maximum %.6f", amount, limits.MaxOrderSize)
		result.Errors = append(result.Errors, check.Message)
		result.Valid = false
	} else if orderValueUSD > maxOrderSizeUSD {
		// Check USD value limit
		check.Passed = false
		check.Message = fmt.Sprintf("Order value $%.2f exceeds max order size $%.2f", orderValueUSD, maxOrderSizeUSD)
		result.Errors = append(result.Errors, check.Message)
		result.Valid = false
	} else {
		check.Message = fmt.Sprintf("Order size %.6f within limits (min: %.6f, max: %.6f)", amount, limits.MinOrderSize, limits.MaxOrderSize)
	}

	result.Validations["order_size"] = check
}

// validatePositionLimit checks if adding this position would exceed position limits
func (t *ValidateTradeTool) validatePositionLimit(ctx context.Context, result *ValidationResult, exchange, pair string, valueUSD float64, marketType string) {
	check := ValidationCheck{
		Passed: true,
	}

	currentExposure := 0.0
	// TODO(TC-3.4): migrate MaxPositionSizeUSD to dynamic config key "risk_limits.max_position_size_usd"
	// once that key is added to the manifest.
	maxExposure := t.config.RiskLimits.MaxPositionSizeUSD

	// Get current exposure from position manager if available
	if t.positionManager != nil {
		if exposure, err := t.positionManager.GetExposureBySymbol(ctx, pair); err == nil {
			currentExposure = exposure
		}
	}

	newExposure := currentExposure + valueUSD

	check.Details = map[string]interface{}{
		"current":   currentExposure,
		"limit":     maxExposure,
		"new_total": newExposure,
		"adding":    valueUSD,
	}

	if newExposure > maxExposure {
		check.Passed = false
		check.Message = fmt.Sprintf("Position would exceed limit: current $%.2f + new $%.2f = $%.2f > limit $%.2f",
			currentExposure, valueUSD, newExposure, maxExposure)
		result.Errors = append(result.Errors, check.Message)
		result.Valid = false
	} else if newExposure > maxExposure*0.8 {
		check.Message = fmt.Sprintf("Position within limit but approaching threshold (%.0f%% of limit)",
			(newExposure/maxExposure)*100)
		result.Warnings = append(result.Warnings, check.Message)
	} else {
		check.Message = fmt.Sprintf("Position within limit: $%.2f / $%.2f (%.0f%%)",
			newExposure, maxExposure, (newExposure/maxExposure)*100)
	}

	result.Validations["position_limit"] = check
}

// validateDailyLossLimit checks if the daily loss limit has been reached
func (t *ValidateTradeTool) validateDailyLossLimit(ctx context.Context, result *ValidationResult) {
	check := ValidationCheck{
		Passed: true,
	}

	currentLoss := 0.0
	// TODO(TC-3.4): migrate MaxDailyLossUSD to dynamic config key "risk_limits.max_daily_loss_usd"
	// once that key is added to the manifest.
	maxLoss := t.config.RiskLimits.MaxDailyLossUSD

	// Get current daily loss from risk sentinel if available
	if t.riskSentinel != nil {
		if state, err := t.riskSentinel.GetRiskState(ctx); err == nil {
			// Convert percentage to USD value (approximate)
			// Note: In a real implementation, you'd track actual USD losses
			currentLoss = state.CurrentDrawdownDaily * 10 // Simplified approximation
		}
	}

	check.Details = map[string]interface{}{
		"current": currentLoss,
		"limit":   maxLoss,
	}

	if currentLoss >= maxLoss {
		check.Passed = false
		check.Message = fmt.Sprintf("Daily loss limit reached: $%.2f / $%.2f", currentLoss, maxLoss)
		result.Errors = append(result.Errors, check.Message)
		result.Valid = false
	} else if currentLoss >= maxLoss*0.8 {
		check.Message = fmt.Sprintf("Approaching daily loss limit: $%.2f / $%.2f (%.0f%%)",
			currentLoss, maxLoss, (currentLoss/maxLoss)*100)
		result.Warnings = append(result.Warnings, check.Message)
	} else {
		check.Message = fmt.Sprintf("Daily loss within limit: $%.2f / $%.2f", currentLoss, maxLoss)
	}

	result.Validations["daily_loss"] = check
}

// validateLeverageLimit checks if the leverage is within allowed limits
func (t *ValidateTradeTool) validateLeverageLimit(ctx context.Context, result *ValidationResult, leverage float64, marketType string) {
	check := ValidationCheck{
		Passed: true,
	}

	maxLeverage := 1.0
	if marketType == "futures" {
		maxLeverage = 20.0 // Default max leverage for futures
	} else if marketType == "margin" {
		maxLeverage = 5.0 // Default max leverage for margin
	}

	// Get max leverage from risk sentinel if available
	if t.riskSentinel != nil {
		if state, err := t.riskSentinel.GetRiskState(ctx); err == nil {
			// Adjust max leverage based on risk mode
			switch state.RiskMode {
			case internalRisk.Conservative:
				maxLeverage = 1.0
			case internalRisk.Normal:
				maxLeverage = 2.0
			case internalRisk.Aggressive:
				maxLeverage = 3.0
			case internalRisk.ControlledCrazy:
				maxLeverage = 5.0
			}
		}
	}

	check.Details = map[string]interface{}{
		"requested":   leverage,
		"max_allowed": maxLeverage,
		"market_type": marketType,
	}

	if leverage > maxLeverage {
		check.Passed = false
		check.Message = fmt.Sprintf("Leverage %.1fx exceeds maximum %.1fx for %s", leverage, maxLeverage, marketType)
		result.Errors = append(result.Errors, check.Message)
		result.Valid = false
	} else if leverage > maxLeverage*0.8 && marketType != "spot" {
		check.Message = fmt.Sprintf("Leverage %.1fx is near maximum %.1fx", leverage, maxLeverage)
		result.Warnings = append(result.Warnings, check.Message)
	} else {
		if marketType == "spot" {
			check.Message = "Spot trading - no leverage applied"
		} else {
			check.Message = fmt.Sprintf("Leverage %.1fx within limit (max: %.1fx)", leverage, maxLeverage)
		}
	}

	result.Validations["leverage_limit"] = check
}

// validateMarketHours checks if the market is currently open
func (t *ValidateTradeTool) validateMarketHours(ctx context.Context, result *ValidationResult, exchange, marketType string) {
	check := ValidationCheck{
		Passed: true,
	}

	// Crypto markets are generally 24/7
	now := time.Now().UTC()

	check.Details = map[string]interface{}{
		"exchange":     exchange,
		"market_type":  marketType,
		"current_time": now.Format(time.RFC3339),
		"market_open":  true,
	}

	// For crypto, markets are always open
	// This could be extended for stock exchanges with actual trading hours
	check.Message = "Crypto markets are open 24/7"

	// Add maintenance window warnings (example for common exchanges)
	hour := now.Hour()
	if exchange == "binance" && hour == 0 {
		result.Warnings = append(result.Warnings, "Binance may have maintenance around 00:00 UTC")
	}
	if exchange == "kucoin" && (hour == 8 || hour == 20) {
		result.Warnings = append(result.Warnings, "KuCoin may have brief maintenance windows")
	}

	result.Validations["market_hours"] = check
}

// validateRiskSentinel runs additional validation through the risk sentinel
func (t *ValidateTradeTool) validateRiskSentinel(ctx context.Context, result *ValidationResult, pair, side string, amount, price, leverage float64) {
	check := ValidationCheck{
		Passed: true,
	}

	if t.riskSentinel == nil {
		check.Message = "Risk sentinel not available - skipping advanced risk checks"
		result.Warnings = append(result.Warnings, check.Message)
		result.Validations["risk_sentinel"] = check
		return
	}

	// Convert side to position side
	positionSide := "long"
	if side == "sell" {
		positionSide = "short"
	}

	action := &internalRisk.TradingAction{
		Type:     internalRisk.ActionOpenPosition,
		Symbol:   pair,
		Side:     positionSide,
		Size:     amount,
		Leverage: leverage,
		Price:    price,
	}

	decision, err := t.riskSentinel.EvaluateAction(ctx, action)
	if err != nil {
		check.Message = fmt.Sprintf("Risk evaluation failed: %v", err)
		result.Warnings = append(result.Warnings, check.Message)
		result.Validations["risk_sentinel"] = check
		return
	}

	check.Details = map[string]interface{}{
		"allowed":      decision.Allowed,
		"max_size":     decision.MaxSize,
		"max_leverage": decision.MaxLeverage,
		"warnings":     decision.Warnings,
	}

	if !decision.Allowed {
		check.Passed = false
		check.Message = fmt.Sprintf("Risk sentinel rejected: %s", decision.Reason)
		result.Errors = append(result.Errors, check.Message)
		result.Valid = false
	} else {
		check.Message = "Risk sentinel approved trade"
		// Add any warnings from the decision
		for _, warning := range decision.Warnings {
			result.Warnings = append(result.Warnings, warning)
		}
	}

	result.Validations["risk_sentinel"] = check
}

// getOrderSizeLimits returns order size limits for a given pair and market type
func (t *ValidateTradeTool) getOrderSizeLimits(exchange, pair, marketType string) OrderSizeLimits {
	// Default limits - these should be fetched from exchange API in production
	limits := OrderSizeLimits{
		MinOrderSize: 0.0001,
		MaxOrderSize: 10.0,
	}

	// Adjust based on the base currency
	parts := strings.Split(pair, "-")
	if len(parts) >= 1 {
		baseCurrency := strings.ToUpper(parts[0])

		switch baseCurrency {
		case "BTC":
			limits.MinOrderSize = 0.0001
			limits.MaxOrderSize = 100.0
		case "ETH":
			limits.MinOrderSize = 0.001
			limits.MaxOrderSize = 1000.0
		case "XRP", "DOGE", "SHIB":
			limits.MinOrderSize = 1.0
			limits.MaxOrderSize = 10000000.0
		case "SOL", "DOT", "LINK":
			limits.MinOrderSize = 0.01
			limits.MaxOrderSize = 10000.0
		default:
			limits.MinOrderSize = 0.001
			limits.MaxOrderSize = 10000.0
		}
	}

	// Adjust for futures (typically higher limits)
	if marketType == "futures" {
		limits.MinOrderSize *= 0.1 // Lower minimum for futures
		limits.MaxOrderSize *= 10  // Higher maximum for futures
	}

	return limits
}

// generateSummary generates a human-readable summary of the validation
func (t *ValidateTradeTool) generateSummary(result *ValidationResult) string {
	var sb strings.Builder

	if result.Valid {
		sb.WriteString("VALIDATION PASSED\n\n")
	} else {
		sb.WriteString("VALIDATION FAILED\n\n")
	}

	sb.WriteString(fmt.Sprintf("Order: %s %.6f %s @ $%.2f (Value: $%.2f)\n",
		strings.ToUpper(result.Order.Side),
		result.Order.Amount,
		result.Order.Pair,
		result.Order.Price,
		result.Order.ValueUSD))

	sb.WriteString(fmt.Sprintf("Exchange: %s | Type: %s | Market: %s\n",
		result.Order.Exchange,
		result.Order.OrderType,
		result.Order.MarketType))

	if result.Order.Leverage > 1 {
		sb.WriteString(fmt.Sprintf("Leverage: %.1fx\n", result.Order.Leverage))
	}

	sb.WriteString("\n")

	// Count passed/failed checks
	passed := 0
	failed := 0
	for _, check := range result.Validations {
		if check.Passed {
			passed++
		} else {
			failed++
		}
	}

	sb.WriteString(fmt.Sprintf("Checks: %d passed, %d failed\n", passed, failed))

	if len(result.Errors) > 0 {
		sb.WriteString("\nErrors:\n")
		for _, err := range result.Errors {
			sb.WriteString(fmt.Sprintf("  - %s\n", err))
		}
	}

	if len(result.Warnings) > 0 {
		sb.WriteString("\nWarnings:\n")
		for _, warning := range result.Warnings {
			sb.WriteString(fmt.Sprintf("  - %s\n", warning))
		}
	}

	return sb.String()
}
