package portfolio

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

// GetRebalanceRecommendationsTool generates portfolio rebalancing recommendations
type GetRebalanceRecommendationsTool struct {
	client *gctclient.Client
}

// NewGetRebalanceRecommendationsTool creates a new instance
func NewGetRebalanceRecommendationsTool(client *gctclient.Client) *GetRebalanceRecommendationsTool {
	return &GetRebalanceRecommendationsTool{client: client}
}

func (t *GetRebalanceRecommendationsTool) Name() string {
	return "get_rebalance_recommendations"
}

func (t *GetRebalanceRecommendationsTool) Description() string {
	return "Generate portfolio rebalancing recommendations based on selected strategy"
}

func (t *GetRebalanceRecommendationsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"strategy": map[string]interface{}{
				"type":        "string",
				"description": "Rebalancing strategy",
				"enum":        []string{"equal_weight", "market_cap", "risk_parity", "custom"},
			},
			"current_portfolio": map[string]interface{}{
				"type":        "object",
				"description": "Current portfolio state",
				"properties": map[string]interface{}{
					"id": map[string]interface{}{
						"type": "string",
					},
					"total_value": map[string]interface{}{
						"type": "number",
					},
					"assets": map[string]interface{}{
						"type": "array",
						"items": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"symbol": map[string]interface{}{
									"type": "string",
								},
								"quantity": map[string]interface{}{
									"type": "number",
								},
								"current_price": map[string]interface{}{
									"type": "number",
								},
								"value": map[string]interface{}{
									"type": "number",
								},
								"allocation": map[string]interface{}{
									"type": "number",
								},
							},
						},
					},
				},
				"required": []string{"total_value", "assets"},
			},
			"risk_tolerance": map[string]interface{}{
				"type":        "string",
				"description": "Risk tolerance level",
				"enum":        []string{"conservative", "moderate", "aggressive"},
				"default":     "moderate",
			},
			"min_trade_amount": map[string]interface{}{
				"type":        "number",
				"description": "Minimum trade amount in USD",
				"default":     10.0,
			},
		},
		"required": []string{"strategy", "current_portfolio"},
	}
}

type PortfolioAsset struct {
	Symbol       string  `json:"symbol"`
	Quantity     float64 `json:"quantity"`
	CurrentPrice float64 `json:"current_price"`
	Value        float64 `json:"value"`
	Allocation   float64 `json:"allocation"`
}

type CurrentPortfolio struct {
	ID         string           `json:"id"`
	TotalValue float64          `json:"total_value"`
	Assets     []PortfolioAsset `json:"assets"`
}

type RebalanceInput struct {
	Strategy         string           `json:"strategy"`
	CurrentPortfolio CurrentPortfolio `json:"current_portfolio"`
	RiskTolerance    string           `json:"risk_tolerance"`
	MinTradeAmount   float64          `json:"min_trade_amount"`
}

type RebalanceRecommendation struct {
	Symbol            string  `json:"symbol"`
	CurrentAllocation float64 `json:"current_allocation"`
	TargetAllocation  float64 `json:"target_allocation"`
	Difference        float64 `json:"difference"`
	Action            string  `json:"action"`
	Amount            float64 `json:"amount"`
	Quantity          float64 `json:"quantity"`
	Reason            string  `json:"reason"`
	Priority          string  `json:"priority"`
	EstimatedCost     float64 `json:"estimated_cost"`
}

type RebalanceSummary struct {
	TotalBuyAmount     float64 `json:"total_buy_amount"`
	TotalSellAmount    float64 `json:"total_sell_amount"`
	NetChange          float64 `json:"net_change"`
	EstimatedTotalCost float64 `json:"estimated_total_cost"`
	NumberOfTrades     int     `json:"number_of_trades"`
	RebalanceNeeded    bool    `json:"rebalance_needed"`
}

type RebalanceOutput struct {
	Recommendations []RebalanceRecommendation `json:"recommendations"`
	Summary         RebalanceSummary          `json:"summary"`
	Strategy        string                    `json:"strategy"`
	RiskTolerance   string                    `json:"risk_tolerance"`
	Message         string                    `json:"message,omitempty"`
	Success         bool                      `json:"success"`
	// Estimate is always true: costs use a fixed fee and risk_parity uses
	// assumed volatilities. Assumptions lists what was assumed.
	Estimate    bool     `json:"estimate"`
	Assumptions []string `json:"assumptions"`
}

func rebalanceAssumptions(strategy string) []string {
	a := []string{"portfolio, prices and allocations are the values supplied by the caller (nothing is read from the exchange)", "estimated cost uses a fixed 0.1% fee per trade and no slippage"}
	if strategy == "risk_parity" {
		a = append(a, "risk_parity uses volatilities assumed by asset class (stablecoins 1%, BTC 50%, ETH 60%, others 80% annual), not measured")
	}
	return a
}

func (t *GetRebalanceRecommendationsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	// Parse strategy
	strategy, ok := params["strategy"].(string)
	if !ok || strategy == "" {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "strategy is required",
		}
	}
	switch strategy {
	case "equal_weight", "market_cap", "risk_parity", "custom":
	default:
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    fmt.Sprintf("unsupported strategy %q (supported: equal_weight, market_cap, risk_parity, custom)", strategy),
		}
	}

	// Parse current_portfolio
	portfolioData, ok := params["current_portfolio"].(map[string]interface{})
	if !ok {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "current_portfolio is required",
		}
	}

	// Parse portfolio fields
	totalValue, _ := portfolioData["total_value"].(float64)
	assetsData, _ := portfolioData["assets"].([]interface{})

	if len(assetsData) == 0 {
		return nil, &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "current portfolio must have at least one asset",
		}
	}

	// Convert assets
	var assets []PortfolioAsset
	for _, assetData := range assetsData {
		assetMap, ok := assetData.(map[string]interface{})
		if !ok {
			continue
		}

		asset := PortfolioAsset{
			Symbol:       assetMap["symbol"].(string),
			Quantity:     assetMap["quantity"].(float64),
			CurrentPrice: assetMap["current_price"].(float64),
			Value:        assetMap["value"].(float64),
			Allocation:   assetMap["allocation"].(float64),
		}
		assets = append(assets, asset)
	}

	portfolio := CurrentPortfolio{
		TotalValue: totalValue,
		Assets:     assets,
	}

	// Parse optional parameters
	riskTolerance := "moderate"
	if rt, ok := params["risk_tolerance"].(string); ok && rt != "" {
		riskTolerance = rt
	}

	minTradeAmount := 10.0
	if mta, ok := params["min_trade_amount"].(float64); ok && mta > 0 {
		minTradeAmount = mta
	}

	// Calculate target allocations
	targetAllocations := t.calculateTargetAllocations(strategy, portfolio, riskTolerance)

	// Generate recommendations
	recommendations := t.generateRecommendations(portfolio, targetAllocations, minTradeAmount)

	// Calculate summary
	summary := calculateSummary(recommendations)

	output := &RebalanceOutput{
		Recommendations: recommendations,
		Summary:         summary,
		Strategy:        strategy,
		RiskTolerance:   riskTolerance,
		Success:         true,
		Estimate:        true,
		Assumptions:     rebalanceAssumptions(strategy),
	}

	if !summary.RebalanceNeeded {
		output.Message = "Portfolio is well balanced - no rebalancing needed"
	}

	return output, nil
}

func (t *GetRebalanceRecommendationsTool) calculateTargetAllocations(strategy string, portfolio CurrentPortfolio, riskTolerance string) map[string]float64 {
	targets := make(map[string]float64)

	switch strategy {
	case "equal_weight":
		// Equal allocation to all assets
		equalWeight := 100.0 / float64(len(portfolio.Assets))
		for _, asset := range portfolio.Assets {
			targets[asset.Symbol] = equalWeight
		}

	case "market_cap":
		// Weight by market capitalization
		// For simplicity, use current value as proxy for market cap
		totalValue := 0.0
		for _, asset := range portfolio.Assets {
			totalValue += asset.Value
		}
		for _, asset := range portfolio.Assets {
			targets[asset.Symbol] = (asset.Value / totalValue) * 100
		}

	case "risk_parity":
		// Equal risk contribution
		// Simplified: inverse volatility weighting
		volatilities := t.estimateVolatilities(portfolio.Assets)
		totalInverseVol := 0.0
		for _, vol := range volatilities {
			if vol > 0 {
				totalInverseVol += 1.0 / vol
			}
		}
		for symbol, vol := range volatilities {
			if vol > 0 && totalInverseVol > 0 {
				targets[symbol] = (1.0 / vol / totalInverseVol) * 100
			} else {
				targets[symbol] = 100.0 / float64(len(portfolio.Assets))
			}
		}

	case "custom":
		// Use current allocations as targets (no rebalancing)
		for _, asset := range portfolio.Assets {
			targets[asset.Symbol] = asset.Allocation
		}

	default:
		// Default to equal weight
		equalWeight := 100.0 / float64(len(portfolio.Assets))
		for _, asset := range portfolio.Assets {
			targets[asset.Symbol] = equalWeight
		}
	}

	// Adjust for risk tolerance
	targets = t.adjustForRiskTolerance(targets, portfolio.Assets, riskTolerance)

	return targets
}

func (t *GetRebalanceRecommendationsTool) estimateVolatilities(assets []PortfolioAsset) map[string]float64 {
	volatilities := make(map[string]float64)

	// Simplified volatility estimates based on asset type
	for _, asset := range assets {
		vol := 0.0
		switch {
		case asset.Symbol == "USDT" || asset.Symbol == "USDC" || asset.Symbol == "DAI":
			vol = 0.01 // Stablecoins: very low volatility
		case asset.Symbol == "BTC":
			vol = 0.50 // Bitcoin: moderate volatility
		case asset.Symbol == "ETH":
			vol = 0.60 // Ethereum: moderate-high volatility
		default:
			vol = 0.80 // Altcoins: high volatility
		}
		volatilities[asset.Symbol] = vol
	}

	return volatilities
}

func (t *GetRebalanceRecommendationsTool) adjustForRiskTolerance(targets map[string]float64, assets []PortfolioAsset, riskTolerance string) map[string]float64 {
	if riskTolerance == "moderate" {
		return targets // No adjustment for moderate
	}

	adjusted := make(map[string]float64)
	stablecoinBonus := 0.0
	volatileBonus := 0.0

	switch riskTolerance {
	case "conservative":
		stablecoinBonus = 10.0 // Increase stablecoin allocation
		volatileBonus = -10.0  // Decrease volatile asset allocation
	case "aggressive":
		stablecoinBonus = -10.0 // Decrease stablecoin allocation
		volatileBonus = 10.0    // Increase volatile asset allocation
	}

	// Apply adjustments
	totalAdjustment := 0.0
	for symbol, target := range targets {
		adjustment := 0.0
		if symbol == "USDT" || symbol == "USDC" || symbol == "DAI" {
			adjustment = stablecoinBonus
		} else {
			adjustment = volatileBonus / float64(len(targets)-1) // Distribute among non-stablecoins
		}

		adjusted[symbol] = math.Max(0, target+adjustment)
		totalAdjustment += adjusted[symbol]
	}

	// Normalize to 100%
	if totalAdjustment > 0 {
		for symbol := range adjusted {
			adjusted[symbol] = (adjusted[symbol] / totalAdjustment) * 100
		}
	}

	return adjusted
}

func (t *GetRebalanceRecommendationsTool) generateRecommendations(portfolio CurrentPortfolio, targets map[string]float64, minTradeAmount float64) []RebalanceRecommendation {
	recommendations := []RebalanceRecommendation{}

	for _, asset := range portfolio.Assets {
		targetAllocation := targets[asset.Symbol]
		difference := targetAllocation - asset.Allocation

		// Skip if difference is too small
		if math.Abs(difference) < 1.0 {
			continue
		}

		// Calculate trade amount
		amount := (difference / 100.0) * portfolio.TotalValue

		// Skip if below minimum trade amount
		if math.Abs(amount) < minTradeAmount {
			continue
		}

		// Determine action
		action := "hold"
		if difference > 0 {
			action = "buy"
		} else if difference < 0 {
			action = "sell"
		}

		// Calculate quantity
		quantity := 0.0
		if asset.CurrentPrice > 0 {
			quantity = math.Abs(amount) / asset.CurrentPrice
		}

		// Determine priority
		priority := "low"
		absDiff := math.Abs(difference)
		if absDiff > 10.0 {
			priority = "high"
		} else if absDiff > 5.0 {
			priority = "medium"
		}

		// Estimate trading cost (0.1% fee)
		estimatedCost := math.Abs(amount) * 0.001

		// Generate reason
		reason := fmt.Sprintf("%s by %.1f%%",
			map[bool]string{true: "Overweight", false: "Underweight"}[difference < 0],
			math.Abs(difference))

		recommendations = append(recommendations, RebalanceRecommendation{
			Symbol:            asset.Symbol,
			CurrentAllocation: math.Round(asset.Allocation*100) / 100,
			TargetAllocation:  math.Round(targetAllocation*100) / 100,
			Difference:        math.Round(difference*100) / 100,
			Action:            action,
			Amount:            math.Round(math.Abs(amount)*100) / 100,
			Quantity:          math.Round(quantity*1000000) / 1000000,
			Reason:            reason,
			Priority:          priority,
			EstimatedCost:     math.Round(estimatedCost*100) / 100,
		})
	}

	// Sort by priority (high first) and then by absolute difference
	sort.Slice(recommendations, func(i, j int) bool {
		priorityOrder := map[string]int{"high": 0, "medium": 1, "low": 2}
		if priorityOrder[recommendations[i].Priority] != priorityOrder[recommendations[j].Priority] {
			return priorityOrder[recommendations[i].Priority] < priorityOrder[recommendations[j].Priority]
		}
		return math.Abs(recommendations[i].Difference) > math.Abs(recommendations[j].Difference)
	})

	return recommendations
}

func calculateSummary(recommendations []RebalanceRecommendation) RebalanceSummary {
	summary := RebalanceSummary{}

	for _, rec := range recommendations {
		if rec.Action == "buy" {
			summary.TotalBuyAmount += rec.Amount
		} else if rec.Action == "sell" {
			summary.TotalSellAmount += rec.Amount
		}
		summary.EstimatedTotalCost += rec.EstimatedCost
	}

	summary.NumberOfTrades = len(recommendations)
	summary.NetChange = summary.TotalBuyAmount - summary.TotalSellAmount
	summary.RebalanceNeeded = len(recommendations) > 0

	// Round values
	summary.TotalBuyAmount = math.Round(summary.TotalBuyAmount*100) / 100
	summary.TotalSellAmount = math.Round(summary.TotalSellAmount*100) / 100
	summary.NetChange = math.Round(summary.NetChange*100) / 100
	summary.EstimatedTotalCost = math.Round(summary.EstimatedTotalCost*100) / 100

	return summary
}
