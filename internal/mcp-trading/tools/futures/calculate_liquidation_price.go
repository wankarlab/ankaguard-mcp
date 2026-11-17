package futures

import (
	"context"
	"math"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// CalculateLiquidationPriceTool calculates liquidation price for futures positions
type CalculateLiquidationPriceTool struct {
	client *gctclient.Client
}

func NewCalculateLiquidationPriceTool(client *gctclient.Client) *CalculateLiquidationPriceTool {
	return &CalculateLiquidationPriceTool{client: client}
}

func (t *CalculateLiquidationPriceTool) Name() string {
	return "calculate_liquidation_price"
}

func (t *CalculateLiquidationPriceTool) Description() string {
	return "Calculate liquidation price for futures positions with different leverage scenarios and margin adjustments"
}

func (t *CalculateLiquidationPriceTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"entry_price": map[string]interface{}{
				"type":        "number",
				"description": "Entry price",
			},
			"position_size": map[string]interface{}{
				"type":        "number",
				"description": "Position size in base currency",
			},
			"leverage": map[string]interface{}{
				"type":        "integer",
				"description": "Leverage (1-125x)",
				"minimum":     1,
				"maximum":     125,
			},
			"side": map[string]interface{}{
				"type":        "string",
				"description": "Position side",
				"enum":        []string{"long", "short"},
			},
			"margin_mode": map[string]interface{}{
				"type":        "string",
				"description": "Margin mode",
				"enum":        []string{"isolated", "cross"},
				"default":     "isolated",
			},
			"initial_margin": map[string]interface{}{
				"type":        "number",
				"description": "Initial margin amount (optional)",
			},
			"maintenance_margin_rate": map[string]interface{}{
				"type":        "number",
				"description": "Maintenance margin rate (default: 0.005 = 0.5%)",
				"default":     0.005,
			},
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair",
			},
		},
		"required": []string{"entry_price", "position_size", "leverage", "side"},
	}
}

func (t *CalculateLiquidationPriceTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parse parameters
	entryPrice, ok := params["entry_price"].(float64)
	if !ok || entryPrice <= 0 {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid entry_price",
			Data:    "entry_price must be a positive number",
		}
	}

	positionSize, ok := params["position_size"].(float64)
	if !ok || positionSize <= 0 {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid position_size",
			Data:    "position_size must be a positive number",
		}
	}

	leverage, ok := params["leverage"].(float64)
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid leverage",
			Data:    "leverage must be a number",
		}
	}
	leverageInt := int(leverage)
	if leverageInt < 1 || leverageInt > 125 {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid leverage",
			Data:    "leverage must be between 1 and 125",
		}
	}

	side, ok := params["side"].(string)
	if !ok || (side != "long" && side != "short") {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid side",
			Data:    "side must be 'long' or 'short'",
		}
	}

	marginMode := "isolated"
	if mm, ok := params["margin_mode"].(string); ok {
		marginMode = mm
	}

	maintenanceMarginRate := 0.005
	if mmr, ok := params["maintenance_margin_rate"].(float64); ok {
		maintenanceMarginRate = mmr
	}

	// Calculate liquidation price
	liquidationPrice := t.calculateLiquidationPrice(entryPrice, leverageInt, side, maintenanceMarginRate)

	// Calculate bankruptcy price (100% loss)
	bankruptcyPrice := t.calculateBankruptcyPrice(entryPrice, leverageInt, side)

	// Calculate distance to liquidation
	distanceToLiquidation := math.Abs(entryPrice - liquidationPrice)
	distancePercentage := (distanceToLiquidation / entryPrice) * 100

	// Calculate margin ratio
	marginRatio := 1.0 / float64(leverageInt)

	// Calculate scenarios with different leverage
	scenarios := t.calculateLeverageScenarios(entryPrice, side, maintenanceMarginRate)

	// Calculate margin adjustments
	marginAdjustments := t.calculateMarginAdjustments(entryPrice, leverageInt, side, maintenanceMarginRate, positionSize)

	// Determine risk level
	riskLevel := t.determineRiskLevel(distancePercentage)

	// Generate warnings
	warnings := t.generateWarnings(distancePercentage, leverageInt, side)

	return map[string]interface{}{
		// Estimate: simplified isolated-margin formula, not the exchange's own liquidation engine.
		"estimate": true,
		"assumptions": map[string]interface{}{
			"formula":                 "entry x (1 -/+ 1/leverage +/- maintenance_margin_rate)",
			"maintenance_margin_rate": maintenanceMarginRate,
			"margin_rate_source":      "value passed by the caller or the default 0.5%; the exchange applies tiered rates per contract size",
			"not_included":            "fees, funding, unrealized PnL of other positions and cross-margin balance",
		},
		"liquidation_price":       roundToDecimals(liquidationPrice, 2),
		"distance_to_liquidation": roundToDecimals(distanceToLiquidation, 2),
		"distance_percentage":     roundToDecimals(distancePercentage, 2),
		"bankruptcy_price":        roundToDecimals(bankruptcyPrice, 2),
		"margin_ratio":            roundToDecimals(marginRatio, 4),
		"scenarios":               scenarios,
		"margin_adjustments":      marginAdjustments,
		"risk_level":              riskLevel,
		"warnings":                warnings,
		"margin_mode":             marginMode,
		"leverage":                leverageInt,
		"side":                    side,
	}, nil
}

// calculateLiquidationPrice calculates the liquidation price
func (t *CalculateLiquidationPriceTool) calculateLiquidationPrice(entryPrice float64, leverage int, side string, mmr float64) float64 {
	leverageFloat := float64(leverage)

	if side == "long" {
		// Long: Liquidation Price = Entry Price × (1 - (1/Leverage) + MMR)
		return entryPrice * (1 - (1 / leverageFloat) + mmr)
	}

	// Short: Liquidation Price = Entry Price × (1 + (1/Leverage) - MMR)
	return entryPrice * (1 + (1 / leverageFloat) - mmr)
}

// calculateBankruptcyPrice calculates the bankruptcy price (100% loss)
func (t *CalculateLiquidationPriceTool) calculateBankruptcyPrice(entryPrice float64, leverage int, side string) float64 {
	leverageFloat := float64(leverage)

	if side == "long" {
		return entryPrice * (1 - (1 / leverageFloat))
	}

	return entryPrice * (1 + (1 / leverageFloat))
}

// calculateLeverageScenarios calculates liquidation prices for different leverage levels
func (t *CalculateLiquidationPriceTool) calculateLeverageScenarios(entryPrice float64, side string, mmr float64) map[string]interface{} {
	scenarios := make(map[string]interface{})

	leverages := []int{5, 10, 20, 50, 100}
	for _, lev := range leverages {
		liqPrice := t.calculateLiquidationPrice(entryPrice, lev, side, mmr)
		distance := math.Abs(entryPrice - liqPrice)
		distancePct := (distance / entryPrice) * 100

		key := "leverage_" + string(rune(lev/10+'0')) + "x"
		if lev < 10 {
			key = "leverage_" + string(rune(lev+'0')) + "x"
		} else if lev == 10 {
			key = "leverage_10x"
		} else if lev == 20 {
			key = "leverage_20x"
		} else if lev == 50 {
			key = "leverage_50x"
		} else if lev == 100 {
			key = "leverage_100x"
		}

		scenarios[key] = map[string]interface{}{
			"leverage":            lev,
			"liquidation_price":   roundToDecimals(liqPrice, 2),
			"distance_percentage": roundToDecimals(distancePct, 2),
		}
	}

	return scenarios
}

// calculateMarginAdjustments calculates new liquidation prices with margin additions
func (t *CalculateLiquidationPriceTool) calculateMarginAdjustments(entryPrice float64, leverage int, side string, mmr float64, positionSize float64) map[string]interface{} {
	adjustments := make(map[string]interface{})

	positionValue := entryPrice * positionSize
	marginAmounts := []float64{1000, 5000, 10000}

	for _, amount := range marginAmounts {
		// Calculate new effective leverage with added margin
		currentMargin := positionValue / float64(leverage)
		newMargin := currentMargin + amount
		newLeverage := int(positionValue / newMargin)

		if newLeverage < 1 {
			newLeverage = 1
		}

		newLiqPrice := t.calculateLiquidationPrice(entryPrice, newLeverage, side, mmr)
		distance := math.Abs(entryPrice - newLiqPrice)
		distancePct := (distance / entryPrice) * 100

		key := "add_" + formatAmount(amount) + "_usd"
		adjustments[key] = map[string]interface{}{
			"new_liquidation_price":   roundToDecimals(newLiqPrice, 2),
			"new_distance_percentage": roundToDecimals(distancePct, 2),
			"new_leverage":            newLeverage,
		}
	}

	return adjustments
}

// determineRiskLevel determines risk level based on distance percentage
func (t *CalculateLiquidationPriceTool) determineRiskLevel(distancePercentage float64) string {
	if distancePercentage < 5 {
		return "critical"
	} else if distancePercentage < 10 {
		return "high"
	} else if distancePercentage < 20 {
		return "moderate"
	}
	return "low"
}

// generateWarnings generates warnings based on risk factors
func (t *CalculateLiquidationPriceTool) generateWarnings(distancePercentage float64, leverage int, side string) []string {
	warnings := make([]string, 0)

	if distancePercentage < 5 {
		warnings = append(warnings, "CRITICAL: Liquidation price is within 5% of entry price")
		warnings = append(warnings, "Strongly consider reducing leverage or adding margin immediately")
	} else if distancePercentage < 10 {
		warnings = append(warnings, "HIGH RISK: Liquidation price is within 10% of entry price")
		warnings = append(warnings, "Consider adding margin or reducing leverage")
	} else if distancePercentage < 15 {
		warnings = append(warnings, "Liquidation price is within 15% of entry price")
	}

	if leverage >= 20 {
		warnings = append(warnings, "High leverage ("+string(rune(leverage/10+'0'))+"0x) increases liquidation risk significantly")
	} else if leverage >= 10 {
		warnings = append(warnings, "Moderate leverage ("+string(rune(leverage+'0'))+"x) - monitor position closely")
	}

	if len(warnings) == 0 {
		warnings = append(warnings, "Position has adequate distance from liquidation")
	}

	return warnings
}

// Helper functions
func roundToDecimals(value float64, decimals int) float64 {
	multiplier := math.Pow(10, float64(decimals))
	return math.Round(value*multiplier) / multiplier
}

func formatAmount(amount float64) string {
	if amount >= 1000 {
		return string(rune(int(amount/1000)+'0')) + "000"
	}
	return string(rune(int(amount) + '0'))
}
