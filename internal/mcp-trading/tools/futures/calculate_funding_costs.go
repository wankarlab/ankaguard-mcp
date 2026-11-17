package futures

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
)

// CalculateFundingCostsTool calculates funding rate costs and projections
type CalculateFundingCostsTool struct {
	source fundingSource
}

// fundingSource is the real funding data feed; *gctclient.Client implements it.
type fundingSource interface {
	GetFundingRate(ctx context.Context, exchange, pair string) (*models.FundingRateData, error)
	GetFundingHistory(ctx context.Context, exchange, pair string, start, end time.Time) ([]*models.FundingRateData, error)
}

// fundingIntervalHours is the funding settlement interval assumed for projections
// (KuCoin and most perpetual venues settle every 8 hours).
const fundingIntervalHours = 8

func NewCalculateFundingCostsTool(client *gctclient.Client) *CalculateFundingCostsTool {
	return &CalculateFundingCostsTool{source: client}
}

func (t *CalculateFundingCostsTool) Name() string {
	return "calculate_funding_costs"
}

func (t *CalculateFundingCostsTool) Description() string {
	return "Calculate the funding cost of a futures position from the real current funding rate of the exchange, with projections that assume the rate stays constant (estimate) and the realized rates of the last 7 days when available"
}

func (t *CalculateFundingCostsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange": map[string]interface{}{
				"type":        "string",
				"description": "Exchange name",
			},
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair (e.g., 'BTC-USDT')",
			},
			"position_size": map[string]interface{}{
				"type":        "number",
				"description": "Position size in base currency",
			},
			"position_value_usd": map[string]interface{}{
				"type":        "number",
				"description": "Position value in USD",
			},
			"side": map[string]interface{}{
				"type":        "string",
				"description": "Position side",
				"enum":        []string{"long", "short"},
			},
			"holding_periods": map[string]interface{}{
				"type":        "object",
				"description": "Holding periods for projections",
				"properties": map[string]interface{}{
					"hours": map[string]interface{}{"type": "integer"},
					"days":  map[string]interface{}{"type": "integer"},
					"weeks": map[string]interface{}{"type": "integer"},
				},
			},
		},
		"required": []string{"exchange", "pair", "position_size", "position_value_usd", "side"},
	}
}

func (t *CalculateFundingCostsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid exchange", Data: "exchange is required"}
	}
	pair, ok := params["pair"].(string)
	if !ok || pair == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid pair", Data: "pair is required"}
	}
	if size, ok := params["position_size"].(float64); !ok || size <= 0 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid position_size", Data: "position_size must be a positive number"}
	}
	positionValueUSD, ok := params["position_value_usd"].(float64)
	if !ok || positionValueUSD <= 0 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid position_value_usd", Data: "position_value_usd must be a positive number"}
	}
	side, ok := params["side"].(string)
	if !ok || (side != "long" && side != "short") {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid side", Data: "side must be 'long' or 'short'"}
	}

	fundingData, err := t.source.GetFundingRate(ctx, exchange, pair)
	if err != nil {
		return nil, common.DataUnavailable("could not get the funding rate from the exchange: "+err.Error(), map[string]interface{}{
			"exchange": exchange, "pair": pair,
		})
	}

	currentRate := fundingData.FundingRate
	nextFundingTime := fundingData.NextFunding
	singlePeriodCost := positionValueUSD * currentRate
	isCost := (side == "long" && currentRate > 0) || (side == "short" && currentRate < 0)
	timeUntilNextStr := formatDuration(time.Until(nextFundingTime))

	result := map[string]interface{}{
		"estimate":                        true,
		"current_funding_rate":            roundToDecimals(currentRate, 6),
		"current_funding_rate_percentage": roundToDecimals(currentRate*100, 4),
		"next_funding_time":               nextFundingTime.Format(time.RFC3339),
		"time_until_next_funding":         timeUntilNextStr,
		"single_period_cost": map[string]interface{}{
			"amount_usd":  roundToDecimals(math.Abs(singlePeriodCost), 2),
			"is_cost":     isCost,
			"description": t.formatCostDescription(singlePeriodCost, isCost, timeUntilNextStr),
		},
		"projections":        t.calculateProjections(singlePeriodCost, currentRate, isCost),
		"risk_level":         t.determineRiskLevel(currentRate),
		"warnings":           t.generateWarnings(currentRate, side, singlePeriodCost),
		"position_value_usd": positionValueUSD,
		"side":               side,
		"assumptions": map[string]interface{}{
			"rate_source":            "current funding rate of the exchange via GoCryptoTrader",
			"projection_model":       "the current rate stays constant; real rates change every period",
			"funding_interval_hours": fundingIntervalHours,
			"position_value_fixed":   true,
		},
	}

	// Realized rates of the last 7 days (real data). If the feed is down the
	// section is omitted and the reason is stated, never invented.
	end := time.Now()
	history, herr := t.source.GetFundingHistory(ctx, exchange, pair, end.Add(-7*24*time.Hour), end)
	if herr != nil || len(history) == 0 {
		reason := "no realized funding rates returned for the last 7 days"
		if herr != nil {
			reason = herr.Error()
		}
		result["historical_analysis"] = map[string]interface{}{"available": false, "reason": reason}
	} else {
		result["historical_analysis"] = summarizeHistory(history)
	}
	return result, nil
}

// summarizeHistory reduces realized funding rates to average, min, max and the count.
func summarizeHistory(rates []*models.FundingRateData) map[string]interface{} {
	sum, minR, maxR := 0.0, rates[0].FundingRate, rates[0].FundingRate
	for _, r := range rates {
		sum += r.FundingRate
		minR = math.Min(minR, r.FundingRate)
		maxR = math.Max(maxR, r.FundingRate)
	}
	return map[string]interface{}{
		"available":    true,
		"window":       "last 7 days",
		"periods":      len(rates),
		"average_rate": roundToDecimals(sum/float64(len(rates)), 6),
		"min_rate":     roundToDecimals(minR, 6),
		"max_rate":     roundToDecimals(maxR, 6),
	}
}

// calculateProjections projects the cost for a day, week and month assuming the current rate holds.
func (t *CalculateFundingCostsTool) calculateProjections(singlePeriodCost, currentRate float64, isCost bool) map[string]interface{} {
	perDay := 24 / fundingIntervalHours
	proj := func(days int) map[string]interface{} {
		periods := perDay * days
		return map[string]interface{}{
			"periods":        periods,
			"total_cost_usd": roundToDecimals(math.Abs(singlePeriodCost*float64(periods)), 2),
			"average_rate":   roundToDecimals(currentRate, 6),
			"is_cost":        isCost,
		}
	}
	return map[string]interface{}{"daily": proj(1), "weekly": proj(7), "monthly": proj(30)}
}

// determineRiskLevel determines risk level based on funding rate
func (t *CalculateFundingCostsTool) determineRiskLevel(rate float64) string {
	absRate := math.Abs(rate)

	if absRate > 0.0005 {
		return "high"
	} else if absRate > 0.0002 {
		return "moderate"
	}
	return "low"
}

// generateWarnings generates warnings based on funding rate
func (t *CalculateFundingCostsTool) generateWarnings(rate float64, side string, singlePeriodCost float64) []string {
	warnings := make([]string, 0)

	absRate := math.Abs(rate)
	absCost := math.Abs(singlePeriodCost)

	if absRate > 0.001 {
		warnings = append(warnings, "Extremely high funding rate - consider closing position")
	} else if absRate > 0.0005 {
		warnings = append(warnings, "High funding rate - monitor closely")
	}

	if absCost > 1000 {
		warnings = append(warnings, fmt.Sprintf("High funding cost per period: $%.2f", absCost))
	}

	if len(warnings) == 0 && rate != 0 {
		if (side == "long" && rate > 0) || (side == "short" && rate < 0) {
			warnings = append(warnings, "You are paying funding - consider timing of position")
		}
	}

	return warnings
}

// formatCostDescription formats the cost description
func (t *CalculateFundingCostsTool) formatCostDescription(cost float64, isCost bool, timeUntil string) string {
	absCost := math.Abs(cost)
	action := "PAY"
	if !isCost {
		action = "RECEIVE"
	}

	return fmt.Sprintf("You will %s $%.2f in %s", action, absCost, timeUntil)
}

// formatDuration formats a duration into human-readable string
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "0m"
	}

	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
