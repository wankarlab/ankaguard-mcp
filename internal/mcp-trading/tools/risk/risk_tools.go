package risk

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	internalRisk "github.com/rantipay/trading-mcp/internal/risk"
	"github.com/rantipay/trading-mcp/internal/trading"
)

// CheckRiskLimitsTool verifica límites de riesgo
type CheckRiskLimitsTool struct {
	client          *gctclient.Client
	config          *config.Config
	riskSentinel    internalRisk.RiskSentinel
	positionManager trading.PositionTracker
}

// NewCheckRiskLimitsTool creates a new CheckRiskLimitsTool with basic dependencies
func NewCheckRiskLimitsTool(client *gctclient.Client, cfg *config.Config) *CheckRiskLimitsTool {
	return &CheckRiskLimitsTool{client: client, config: cfg}
}

// NewCheckRiskLimitsToolFull creates a new CheckRiskLimitsTool with all dependencies
func NewCheckRiskLimitsToolFull(
	client *gctclient.Client,
	cfg *config.Config,
	riskSentinel internalRisk.RiskSentinel,
	positionManager trading.PositionTracker,
) *CheckRiskLimitsTool {
	return &CheckRiskLimitsTool{
		client:          client,
		config:          cfg,
		riskSentinel:    riskSentinel,
		positionManager: positionManager,
	}
}

func (t *CheckRiskLimitsTool) Name() string {
	return "check_risk_limits"
}

func (t *CheckRiskLimitsTool) Description() string {
	return "Check if a proposed order would exceed configured risk limits (order size, position size, daily loss)"
}

func (t *CheckRiskLimitsTool) InputSchema() map[string]interface{} {
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
				"description": "Order price in quote currency",
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
		},
		"required": []string{"exchange", "pair", "side", "amount", "price"},
	}
}

func (t *CheckRiskLimitsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parse required parameters
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "exchange is required"}
	}

	pair, ok := params["pair"].(string)
	if !ok || pair == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "pair is required"}
	}

	side, ok := params["side"].(string)
	if !ok || (side != "buy" && side != "sell") {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "side must be 'buy' or 'sell'"}
	}

	amount, ok := params["amount"].(float64)
	if !ok || amount <= 0 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "amount must be a positive number"}
	}

	price, ok := params["price"].(float64)
	if !ok || price <= 0 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "price must be a positive number"}
	}

	// Parse optional parameters
	marketType := "spot"
	if mt, ok := params["market_type"].(string); ok && mt != "" {
		marketType = mt
	}

	leverage := 1.0
	if lev, ok := params["leverage"].(float64); ok && lev > 0 {
		leverage = lev
	}

	orderValueUSD := amount * price
	limits := t.config.RiskLimits

	// Initialize checks map
	checks := make(map[string]interface{})
	warnings := make([]string, 0)
	errors := make([]string, 0)

	// 1. Order Size Check - verify against max_order_size_usd
	orderSizeCheck := t.checkOrderSize(orderValueUSD, limits.MaxOrderSizeUSD)
	checks["order_size_check"] = orderSizeCheck
	if !orderSizeCheck["passed"].(bool) {
		errors = append(errors, orderSizeCheck["message"].(string))
	}

	// 2. Position Size Check - verify total exposure
	positionSizeCheck := t.checkPositionSize(ctx, pair, orderValueUSD, limits.MaxPositionSizeUSD)
	checks["position_size_check"] = positionSizeCheck
	if !positionSizeCheck["passed"].(bool) {
		errors = append(errors, positionSizeCheck["message"].(string))
	} else if warning, ok := positionSizeCheck["warning"].(string); ok && warning != "" {
		warnings = append(warnings, warning)
	}

	// 3. Daily Loss Check - verify current daily loss vs limit
	dailyLossCheck := t.checkDailyLoss(ctx, limits.MaxDailyLossUSD)
	checks["daily_loss_check"] = dailyLossCheck
	if !dailyLossCheck["passed"].(bool) {
		errors = append(errors, dailyLossCheck["message"].(string))
	} else if warning, ok := dailyLossCheck["warning"].(string); ok && warning != "" {
		warnings = append(warnings, warning)
	}

	// 4. Open Orders Check - verify number of open orders
	openOrdersCheck := t.checkOpenOrders(ctx, exchange, pair, limits.MaxOpenOrders)
	checks["open_orders_check"] = openOrdersCheck
	if !openOrdersCheck["passed"].(bool) {
		errors = append(errors, openOrdersCheck["message"].(string))
	} else if warning, ok := openOrdersCheck["warning"].(string); ok && warning != "" {
		warnings = append(warnings, warning)
	}

	// 5. Leverage Check - verify leverage is within allowed limits
	leverageCheck := t.checkLeverage(ctx, leverage, marketType)
	checks["leverage_check"] = leverageCheck
	if !leverageCheck["passed"].(bool) {
		errors = append(errors, leverageCheck["message"].(string))
	} else if warning, ok := leverageCheck["warning"].(string); ok && warning != "" {
		warnings = append(warnings, warning)
	}

	// 6. Per-Pair Exposure Check
	pairExposureCheck := t.checkPairExposure(ctx, pair, orderValueUSD, limits.MaxExposurePerPairUSD)
	checks["pair_exposure_check"] = pairExposureCheck
	if !pairExposureCheck["passed"].(bool) {
		errors = append(errors, pairExposureCheck["message"].(string))
	} else if warning, ok := pairExposureCheck["warning"].(string); ok && warning != "" {
		warnings = append(warnings, warning)
	}

	// 7. Kill Switch Check - verify trading is allowed
	killSwitchCheck := t.checkKillSwitch(ctx)
	checks["kill_switch_check"] = killSwitchCheck
	if !killSwitchCheck["passed"].(bool) {
		errors = append(errors, killSwitchCheck["message"].(string))
	}

	// Determine if all checks passed
	allPassed := len(errors) == 0

	// Build summary
	summary := t.buildSummary(allPassed, exchange, pair, side, amount, price, marketType, leverage, checks)

	return map[string]interface{}{
		"all_checks_passed": allPassed,
		"checks":            checks,
		"order_value_usd":   orderValueUSD,
		"warnings":          warnings,
		"errors":            errors,
		"summary":           summary,
		"order": map[string]interface{}{
			"exchange":    strings.ToLower(exchange),
			"pair":        strings.ToUpper(pair),
			"side":        side,
			"amount":      amount,
			"price":       price,
			"value_usd":   orderValueUSD,
			"market_type": marketType,
			"leverage":    leverage,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// unknownCheck marks a check whose input data could not be read. The check
// fails closed and says so; it never reports a made-up value as if it were real.
func unknownCheck(result map[string]interface{}, what string) map[string]interface{} {
	result["passed"] = false
	result["data_available"] = false
	result["message"] = "DATA_UNAVAILABLE: could not read " + what + "; the limit cannot be verified"
	return result
}

// checkOrderSize verifies order value against max order size limit
func (t *CheckRiskLimitsTool) checkOrderSize(orderValueUSD, maxOrderSizeUSD float64) map[string]interface{} {
	passed := orderValueUSD <= maxOrderSizeUSD
	utilization := (orderValueUSD / maxOrderSizeUSD) * 100

	result := map[string]interface{}{
		"passed":      passed,
		"value":       orderValueUSD,
		"limit":       maxOrderSizeUSD,
		"utilization": fmt.Sprintf("%.1f%%", utilization),
	}

	if passed {
		result["message"] = fmt.Sprintf("Order size $%.2f within limit $%.2f (%.1f%% utilized)",
			orderValueUSD, maxOrderSizeUSD, utilization)
	} else {
		result["message"] = fmt.Sprintf("Order size $%.2f exceeds limit $%.2f",
			orderValueUSD, maxOrderSizeUSD)
	}

	return result
}

// checkPositionSize verifies total exposure including new order
func (t *CheckRiskLimitsTool) checkPositionSize(ctx context.Context, pair string, orderValueUSD, maxPositionSizeUSD float64) map[string]interface{} {
	result := map[string]interface{}{
		"limit": maxPositionSizeUSD,
	}

	currentExposure := 0.0
	known := false

	// Get current exposure from position manager if available
	if t.positionManager != nil {
		if exposure, err := t.positionManager.GetTotalExposure(ctx); err == nil {
			currentExposure, known = exposure, true
		}
	} else if t.riskSentinel != nil {
		// Fallback to risk sentinel state
		if state, err := t.riskSentinel.GetRiskState(ctx); err == nil {
			currentExposure, known = state.TotalExposure, true
		}
	} else if t.client != nil && t.client.IsConnected() {
		// Fallback to portfolio data
		if portfolio, err := t.client.GetPortfolio(ctx); err == nil {
			currentExposure, known = portfolio.TotalValueUSD, true
		}
	}
	if !known {
		return unknownCheck(result, "current exposure")
	}

	newExposure := currentExposure + orderValueUSD
	utilization := (newExposure / maxPositionSizeUSD) * 100

	result["current_exposure"] = currentExposure
	result["new_exposure"] = newExposure
	result["utilization"] = fmt.Sprintf("%.1f%%", utilization)

	passed := newExposure <= maxPositionSizeUSD
	result["passed"] = passed

	if passed {
		result["message"] = fmt.Sprintf("Position size $%.2f + $%.2f = $%.2f within limit $%.2f (%.1f%%)",
			currentExposure, orderValueUSD, newExposure, maxPositionSizeUSD, utilization)
		// Add warning if approaching limit
		if utilization >= 80 {
			result["warning"] = fmt.Sprintf("Approaching position limit: %.1f%% utilized", utilization)
		}
	} else {
		result["message"] = fmt.Sprintf("Position would exceed limit: $%.2f + $%.2f = $%.2f > $%.2f",
			currentExposure, orderValueUSD, newExposure, maxPositionSizeUSD)
	}

	return result
}

// checkDailyLoss verifies current daily loss against limit
func (t *CheckRiskLimitsTool) checkDailyLoss(ctx context.Context, maxDailyLossUSD float64) map[string]interface{} {
	result := map[string]interface{}{
		"limit": maxDailyLossUSD,
	}

	currentLossUSD := 0.0
	lossSource := ""

	// Daily drawdown from the risk sentinel, converted with the REAL portfolio
	// value. Without that value the conversion is not possible and no capital is assumed.
	if t.riskSentinel != nil && t.client != nil && t.client.IsConnected() {
		if state, err := t.riskSentinel.GetRiskState(ctx); err == nil {
			if portfolio, perr := t.client.GetPortfolio(ctx); perr == nil && portfolio.TotalValueUSD > 0 {
				currentLossUSD = (state.CurrentDrawdownDaily / 100.0) * portfolio.TotalValueUSD
				lossSource = "risk_sentinel"
			}
		}
	}

	// Alternative: calculate from today's closed trades if available
	if t.positionManager != nil && currentLossUSD == 0 {
		if history, err := t.positionManager.GetPositionHistory(ctx, 100); err == nil {
			today := time.Now().UTC().Truncate(24 * time.Hour)
			for _, pos := range history {
				// Check if position was closed today
				if pos.CloseTime != nil && pos.CloseTime.After(today) && pos.RealizedPnL < 0 {
					currentLossUSD += -pos.RealizedPnL // Convert negative PnL to positive loss
				}
			}
			lossSource = "position_history"
		}
	}
	if lossSource == "" {
		return unknownCheck(result, "today's loss")
	}

	utilization := 0.0
	if maxDailyLossUSD > 0 {
		utilization = (currentLossUSD / maxDailyLossUSD) * 100
	}

	result["current_loss"] = currentLossUSD
	result["utilization"] = fmt.Sprintf("%.1f%%", utilization)
	result["source"] = lossSource

	passed := currentLossUSD < maxDailyLossUSD
	result["passed"] = passed

	if passed {
		result["message"] = fmt.Sprintf("Daily loss $%.2f within limit $%.2f (%.1f%%)",
			currentLossUSD, maxDailyLossUSD, utilization)
		if utilization >= 80 {
			result["warning"] = fmt.Sprintf("Approaching daily loss limit: %.1f%% utilized", utilization)
		}
	} else {
		result["message"] = fmt.Sprintf("Daily loss limit reached: $%.2f >= $%.2f",
			currentLossUSD, maxDailyLossUSD)
	}

	return result
}

// checkOpenOrders verifies number of open orders against limit
func (t *CheckRiskLimitsTool) checkOpenOrders(ctx context.Context, exchange, pair string, maxOpenOrders int) map[string]interface{} {
	result := map[string]interface{}{
		"limit": maxOpenOrders,
	}

	currentOpenOrders := 0
	ordersSource := ""

	// Get open orders from GCT client
	if t.client != nil && t.client.IsConnected() {
		// Get all open orders for the exchange
		if orders, err := t.client.GetOpenOrders(ctx, exchange, ""); err == nil {
			currentOpenOrders = len(orders)
			ordersSource = "exchange_api"
		}
	}

	if ordersSource == "" {
		return unknownCheck(result, "open orders")
	}

	// New order would add 1 more
	newTotal := currentOpenOrders + 1
	utilization := 0.0
	if maxOpenOrders > 0 {
		utilization = (float64(newTotal) / float64(maxOpenOrders)) * 100
	}

	result["current_count"] = currentOpenOrders
	result["new_count"] = newTotal
	result["utilization"] = fmt.Sprintf("%.1f%%", utilization)
	result["source"] = ordersSource

	passed := newTotal <= maxOpenOrders
	result["passed"] = passed

	if passed {
		result["message"] = fmt.Sprintf("Open orders %d + 1 = %d within limit %d (%.1f%%)",
			currentOpenOrders, newTotal, maxOpenOrders, utilization)
		if utilization >= 80 {
			result["warning"] = fmt.Sprintf("Approaching open orders limit: %d/%d", newTotal, maxOpenOrders)
		}
	} else {
		result["message"] = fmt.Sprintf("Open orders limit would be exceeded: %d + 1 = %d > %d",
			currentOpenOrders, newTotal, maxOpenOrders)
	}

	return result
}

// checkLeverage verifies leverage is within allowed limits based on market type and risk mode
func (t *CheckRiskLimitsTool) checkLeverage(ctx context.Context, leverage float64, marketType string) map[string]interface{} {
	result := map[string]interface{}{
		"requested":   leverage,
		"market_type": marketType,
	}

	// Default max leverage based on market type
	maxLeverage := 1.0
	switch marketType {
	case "spot":
		maxLeverage = 1.0
	case "margin":
		maxLeverage = 5.0
	case "futures":
		maxLeverage = 20.0
	}

	// Adjust based on risk mode from sentinel
	riskMode := "normal"
	if t.riskSentinel != nil {
		if state, err := t.riskSentinel.GetRiskState(ctx); err == nil {
			riskMode = state.RiskMode.String()
			// Override max leverage based on risk mode
			switch state.RiskMode {
			case internalRisk.Conservative:
				maxLeverage = 1.0
			case internalRisk.Normal:
				if maxLeverage > 2.0 {
					maxLeverage = 2.0
				}
			case internalRisk.Aggressive:
				if maxLeverage > 3.0 {
					maxLeverage = 3.0
				}
			case internalRisk.ControlledCrazy:
				if maxLeverage > 5.0 {
					maxLeverage = 5.0
				}
			}
		}
	}

	result["max_allowed"] = maxLeverage
	result["risk_mode"] = riskMode

	passed := leverage <= maxLeverage
	result["passed"] = passed

	if marketType == "spot" && leverage == 1.0 {
		result["message"] = "Spot trading - no leverage applied"
	} else if passed {
		result["message"] = fmt.Sprintf("Leverage %.1fx within limit %.1fx for %s (risk mode: %s)",
			leverage, maxLeverage, marketType, riskMode)
		if leverage >= maxLeverage*0.8 {
			result["warning"] = fmt.Sprintf("Near maximum leverage: %.1fx / %.1fx", leverage, maxLeverage)
		}
	} else {
		result["message"] = fmt.Sprintf("Leverage %.1fx exceeds maximum %.1fx for %s (risk mode: %s)",
			leverage, maxLeverage, marketType, riskMode)
	}

	return result
}

// checkPairExposure verifies exposure for a specific trading pair
func (t *CheckRiskLimitsTool) checkPairExposure(ctx context.Context, pair string, orderValueUSD, maxExposurePerPairUSD float64) map[string]interface{} {
	result := map[string]interface{}{
		"pair":  pair,
		"limit": maxExposurePerPairUSD,
	}

	// If no limit configured, skip this check
	if maxExposurePerPairUSD <= 0 {
		result["passed"] = true
		result["message"] = "Per-pair exposure limit not configured"
		result["current_exposure"] = 0.0
		return result
	}

	currentPairExposure := 0.0
	known := false

	// Get current exposure for this pair from position manager
	if t.positionManager != nil {
		if exposure, err := t.positionManager.GetExposureBySymbol(ctx, pair); err == nil {
			currentPairExposure, known = exposure, true
		}
	} else if t.riskSentinel != nil {
		// Fallback to risk sentinel state
		if state, err := t.riskSentinel.GetRiskState(ctx); err == nil {
			known = true
			if exposure, exists := state.ExposureBySymbol[pair]; exists {
				currentPairExposure = exposure
			}
		}
	}
	if !known {
		return unknownCheck(result, "pair exposure")
	}

	newExposure := currentPairExposure + orderValueUSD
	utilization := (newExposure / maxExposurePerPairUSD) * 100

	result["current_exposure"] = currentPairExposure
	result["new_exposure"] = newExposure
	result["utilization"] = fmt.Sprintf("%.1f%%", utilization)

	passed := newExposure <= maxExposurePerPairUSD
	result["passed"] = passed

	if passed {
		result["message"] = fmt.Sprintf("Pair exposure $%.2f + $%.2f = $%.2f within limit $%.2f (%.1f%%)",
			currentPairExposure, orderValueUSD, newExposure, maxExposurePerPairUSD, utilization)
		if utilization >= 80 {
			result["warning"] = fmt.Sprintf("Approaching pair exposure limit for %s: %.1f%%", pair, utilization)
		}
	} else {
		result["message"] = fmt.Sprintf("Pair exposure would exceed limit: $%.2f + $%.2f = $%.2f > $%.2f",
			currentPairExposure, orderValueUSD, newExposure, maxExposurePerPairUSD)
	}

	return result
}

// checkKillSwitch verifies the kill switch is not active
func (t *CheckRiskLimitsTool) checkKillSwitch(ctx context.Context) map[string]interface{} {
	result := map[string]interface{}{}

	if t.riskSentinel == nil {
		return unknownCheck(result, "kill switch state")
	}

	state, err := t.riskSentinel.GetRiskState(ctx)
	if err != nil {
		return unknownCheck(result, "kill switch state: "+err.Error())
	}

	result["active"] = state.KillSwitchActive
	result["passed"] = !state.KillSwitchActive

	if state.KillSwitchActive {
		result["reason"] = state.KillSwitchReason
		result["activated_at"] = state.KillSwitchActivatedAt.Format(time.RFC3339)
		result["message"] = fmt.Sprintf("KILL SWITCH ACTIVE: %s", state.KillSwitchReason)
	} else {
		result["message"] = "Kill switch is not active - trading allowed"
	}

	return result
}

// buildSummary creates a human-readable summary of all risk checks
func (t *CheckRiskLimitsTool) buildSummary(
	allPassed bool,
	exchange, pair, side string,
	amount, price float64,
	marketType string,
	leverage float64,
	checks map[string]interface{},
) string {
	var sb strings.Builder

	if allPassed {
		sb.WriteString("RISK CHECK PASSED\n\n")
	} else {
		sb.WriteString("RISK CHECK FAILED\n\n")
	}

	sb.WriteString(fmt.Sprintf("Order: %s %.6f %s @ $%.2f ($%.2f)\n",
		strings.ToUpper(side), amount, strings.ToUpper(pair), price, amount*price))
	sb.WriteString(fmt.Sprintf("Exchange: %s | Type: %s", exchange, marketType))
	if leverage > 1 {
		sb.WriteString(fmt.Sprintf(" | Leverage: %.1fx", leverage))
	}
	sb.WriteString("\n\n")

	// Count passed/failed
	passed := 0
	failed := 0
	for _, check := range checks {
		if checkMap, ok := check.(map[string]interface{}); ok {
			if p, ok := checkMap["passed"].(bool); ok {
				if p {
					passed++
				} else {
					failed++
				}
			}
		}
	}

	sb.WriteString(fmt.Sprintf("Checks: %d passed, %d failed\n", passed, failed))

	return sb.String()
}

// CalculatePositionSizeTool calcula tamaño óptimo de posición
type CalculatePositionSizeTool struct {
	client *gctclient.Client
	config *config.Config
}

func NewCalculatePositionSizeTool(client *gctclient.Client, cfg *config.Config) *CalculatePositionSizeTool {
	return &CalculatePositionSizeTool{client: client, config: cfg}
}

func (t *CalculatePositionSizeTool) Name() string {
	return "calculate_position_size"
}

func (t *CalculatePositionSizeTool) Description() string {
	return "Calculate optimal position size based on account balance, risk percentage, and stop loss"
}

func (t *CalculatePositionSizeTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"account_balance": map[string]interface{}{"type": "number", "description": "Account balance in USD"},
			"risk_percent":    map[string]interface{}{"type": "number", "description": "Risk percentage per trade (e.g., 1 for 1%)", "default": 1},
			"entry_price":     map[string]interface{}{"type": "number", "description": "Entry price"},
			"stop_loss":       map[string]interface{}{"type": "number", "description": "Stop loss price"},
			"leverage":        map[string]interface{}{"type": "integer", "description": "Leverage (1-125x, default: 1)", "default": 1},
			"exchange":        map[string]interface{}{"type": "string", "description": "Exchange name (optional)"},
			"pair":            map[string]interface{}{"type": "string", "description": "Trading pair (optional)"},
		},
		"required": []string{"account_balance", "entry_price", "stop_loss"},
	}
}

func (t *CalculatePositionSizeTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	accountBalance, ok := params["account_balance"].(float64)
	if !ok {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "account_balance required"}
	}

	entryPrice, ok := params["entry_price"].(float64)
	if !ok {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "entry_price required"}
	}

	stopLoss, ok := params["stop_loss"].(float64)
	if !ok {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "stop_loss required"}
	}

	riskPercent := 1.0
	if rp, ok := params["risk_percent"].(float64); ok {
		riskPercent = rp
	}

	leverage := 1.0
	if lev, ok := params["leverage"].(float64); ok {
		leverage = lev
	}

	exchange := ""
	if ex, ok := params["exchange"].(string); ok {
		exchange = ex
	}

	pair := ""
	if p, ok := params["pair"].(string); ok {
		pair = p
	}

	// Calculate position size
	riskAmount := accountBalance * (riskPercent / 100)
	priceRisk := entryPrice - stopLoss
	if priceRisk < 0 {
		priceRisk = -priceRisk
	}

	positionSize := 0.0
	if priceRisk > 0 {
		positionSize = riskAmount / priceRisk
	}

	positionValueUSD := positionSize * entryPrice
	
	// Calculate margin required (for leveraged positions)
	marginRequired := positionValueUSD / leverage

	// Generate recommendations for different risk profiles
	recommendations := map[string]interface{}{
		"conservative": map[string]interface{}{
			"position_size":   positionSize * 0.5,
			"risk_percent":    riskPercent * 0.5,
			"leverage":        math.Min(leverage, 2),
			"margin_required": (positionValueUSD * 0.5) / math.Min(leverage, 2),
		},
		"moderate": map[string]interface{}{
			"position_size":   positionSize * 0.75,
			"risk_percent":    riskPercent * 0.75,
			"leverage":        math.Min(leverage, 5),
			"margin_required": (positionValueUSD * 0.75) / math.Min(leverage, 5),
		},
		"aggressive": map[string]interface{}{
			"position_size":   positionSize,
			"risk_percent":    riskPercent,
			"leverage":        leverage,
			"margin_required": marginRequired,
		},
	}

	// Generate warnings
	warnings := make([]string, 0)
	if leverage > 10 {
		warnings = append(warnings, "High leverage (>10x) significantly increases liquidation risk")
	}
	if riskPercent > 2 {
		warnings = append(warnings, "Risk percentage >2% per trade is considered aggressive")
	}
	if marginRequired > accountBalance*0.5 {
		warnings = append(warnings, "Margin requirement exceeds 50% of account balance")
	}
	if priceRisk/entryPrice > 0.05 {
		warnings = append(warnings, "Stop loss distance >5% may indicate poor entry or wide stop")
	}

	result := map[string]interface{}{
		"position_size":      positionSize,
		"position_value_usd": positionValueUSD,
		"risk_amount_usd":    riskAmount,
		"risk_percent":       riskPercent,
		"price_risk":         priceRisk,
		"leverage":           leverage,
		"margin_required":    marginRequired,
		"recommendations":    recommendations,
		"warnings":           warnings,
	}

	// Add optional fields if provided
	if exchange != "" {
		result["exchange"] = exchange
	}
	if pair != "" {
		result["pair"] = pair
	}

	return result, nil
}

// CalculateStopLossTool calcula stop loss óptimo
type CalculateStopLossTool struct {
	client *gctclient.Client
}

func NewCalculateStopLossTool(client *gctclient.Client) *CalculateStopLossTool {
	return &CalculateStopLossTool{client: client}
}

func (t *CalculateStopLossTool) Name() string {
	return "calculate_stop_loss"
}

func (t *CalculateStopLossTool) Description() string {
	return "Calculate optimal stop loss based on ATR, support levels, or percentage"
}

func (t *CalculateStopLossTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entry_price": map[string]interface{}{"type": "number", "description": "Entry price"},
			"side":        map[string]interface{}{"type": "string", "description": "'buy' or 'sell'"},
			"method":      map[string]interface{}{"type": "string", "description": "Method: only 'percentage' is supported", "enum": []string{"percentage"}, "default": "percentage"},
			"value":       map[string]interface{}{"type": "number", "description": "Value for method (e.g., 2 for 2% or 2x ATR)", "default": 2},
		},
		"required": []string{"entry_price", "side"},
	}
}

func (t *CalculateStopLossTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	entryPrice, ok := params["entry_price"].(float64)
	if !ok {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "entry_price required"}
	}

	side, ok := params["side"].(string)
	if !ok || (side != "buy" && side != "sell") {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "side must be 'buy' or 'sell'"}
	}

	method := "percentage"
	if m, ok := params["method"].(string); ok {
		method = m
	}

	value := 2.0
	if v, ok := params["value"].(float64); ok {
		value = v
	}

	var stopLoss float64
	if method == "percentage" {
		if side == "buy" {
			stopLoss = entryPrice * (1 - value/100)
		} else {
			stopLoss = entryPrice * (1 + value/100)
		}
	} else {
		// atr/support need candles that this tool does not read; do not
		// silently fall back to a fixed 2% stop under another name.
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: fmt.Sprintf("method %q is not supported; use 'percentage'", method)}
	}

	riskPercent := ((entryPrice - stopLoss) / entryPrice) * 100
	if riskPercent < 0 {
		riskPercent = -riskPercent
	}

	return map[string]interface{}{
		"stop_loss":    stopLoss,
		"entry_price":  entryPrice,
		"risk_percent": riskPercent,
		"method":       method,
		"side":         side,
	}, nil
}

// CalculateTakeProfitTool calcula take profit óptimo
type CalculateTakeProfitTool struct {
	client *gctclient.Client
}

func NewCalculateTakeProfitTool(client *gctclient.Client) *CalculateTakeProfitTool {
	return &CalculateTakeProfitTool{client: client}
}

func (t *CalculateTakeProfitTool) Name() string {
	return "calculate_take_profit"
}

func (t *CalculateTakeProfitTool) Description() string {
	return "Calculate optimal take profit based on risk-reward ratio or resistance levels"
}

func (t *CalculateTakeProfitTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entry_price":       map[string]interface{}{"type": "number", "description": "Entry price"},
			"stop_loss":         map[string]interface{}{"type": "number", "description": "Stop loss price"},
			"side":              map[string]interface{}{"type": "string", "description": "'buy' or 'sell'"},
			"risk_reward_ratio": map[string]interface{}{"type": "number", "description": "Risk-reward ratio (e.g., 2 for 1:2)", "default": 2},
		},
		"required": []string{"entry_price", "stop_loss", "side"},
	}
}

func (t *CalculateTakeProfitTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	entryPrice, ok := params["entry_price"].(float64)
	if !ok {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "entry_price required"}
	}

	stopLoss, ok := params["stop_loss"].(float64)
	if !ok {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "stop_loss required"}
	}

	side, ok := params["side"].(string)
	if !ok {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "side required"}
	}

	rrRatio := 2.0
	if rr, ok := params["risk_reward_ratio"].(float64); ok {
		rrRatio = rr
	}

	risk := entryPrice - stopLoss
	if risk < 0 {
		risk = -risk
	}

	var takeProfit float64
	if side == "buy" {
		takeProfit = entryPrice + (risk * rrRatio)
	} else {
		takeProfit = entryPrice - (risk * rrRatio)
	}

	profitPercent := ((takeProfit - entryPrice) / entryPrice) * 100
	if profitPercent < 0 {
		profitPercent = -profitPercent
	}

	return map[string]interface{}{
		"take_profit":       takeProfit,
		"entry_price":       entryPrice,
		"stop_loss":         stopLoss,
		"risk_reward_ratio": rrRatio,
		"profit_percent":    profitPercent,
		"side":              side,
	}, nil
}

// GetExposureTool obtiene exposición actual
type GetExposureTool struct {
	client *gctclient.Client
}

func NewGetExposureTool(client *gctclient.Client) *GetExposureTool {
	return &GetExposureTool{client: client}
}

func (t *GetExposureTool) Name() string {
	return "get_exposure"
}

func (t *GetExposureTool) Description() string {
	return "Get current exposure (total position value) per pair and total across all positions"
}

func (t *GetExposureTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{"type": "string", "description": "Exchange name (optional)"},
		},
	}
}

func (t *GetExposureTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	portfolio, err := t.client.GetPortfolio(ctx)
	if err != nil {
		return nil, &models.MCPError{Code: -32603, Message: "Internal error", Data: err.Error()}
	}

	totalExposure := 0.0
	for _, holding := range portfolio.Holdings {
		totalExposure += holding.ValueUSD
	}

	// Build exposures array with percentages
	type ExposureItem struct {
		Asset      string  `json:"asset"`
		Value      float64 `json:"value"`
		Percentage float64 `json:"percentage"`
	}

	exposures := make([]ExposureItem, 0, len(portfolio.Holdings))
	
	for currency, holding := range portfolio.Holdings {
		percentage := 0.0
		if totalExposure > 0 {
			percentage = (holding.ValueUSD / totalExposure) * 100
		}
		
		exposures = append(exposures, ExposureItem{
			Asset:      currency,
			Value:      holding.ValueUSD,
			Percentage: math.Round(percentage*100) / 100,  // Round to 2 decimals
		})
	}

	// Sort by value descending (highest exposure first)
	sort.Slice(exposures, func(i, j int) bool {
		return exposures[i].Value > exposures[j].Value
	})

	return map[string]interface{}{
		"success":        true,
		"exposures":      exposures,
		"total_exposure": totalExposure,
		"timestamp":      portfolio.Timestamp,
	}, nil
}
