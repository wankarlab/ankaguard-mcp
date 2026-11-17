package portfolio

import (
	"context"
	"math"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/rantipay/trading-mcp/internal/storage"
)

const tradingDaysPerYear = 365.0

// GetAdvancedPortfolioMetricsTool computes risk-adjusted metrics over the real
// account equity history (portfolio_snapshots).
type GetAdvancedPortfolioMetricsTool struct {
	source snapshotSource
}

// NewGetAdvancedPortfolioMetricsTool builds the tool over the snapshot repository (may be nil).
func NewGetAdvancedPortfolioMetricsTool(repo *storage.PortfolioSnapshotRepository) *GetAdvancedPortfolioMetricsTool {
	return &GetAdvancedPortfolioMetricsTool{source: asSnapshotSource(repo)}
}

func (t *GetAdvancedPortfolioMetricsTool) Name() string { return "get_advanced_portfolio_metrics" }

func (t *GetAdvancedPortfolioMetricsTool) Description() string {
	return "Get Sharpe, Sortino, Calmar, volatility, max drawdown and historical VaR computed from the recorded account equity history. Returns DATA_UNAVAILABLE (with how many daily returns are missing) when there is not enough history"
}

func (t *GetAdvancedPortfolioMetricsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"period_days":    map[string]interface{}{"type": "number", "description": "History window in days (default: 30)"},
			"risk_free_rate": map[string]interface{}{"type": "number", "description": "Annual risk-free rate as a fraction for Sharpe/Sortino (default: 0.02)"},
		},
	}
}

func (t *GetAdvancedPortfolioMetricsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	periodDays := int(common.GetFloatParam(params, "period_days", 30))
	if periodDays < 1 || periodDays > 3650 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "period_days must be between 1 and 3650"}
	}
	riskFree := common.GetFloatParam(params, "risk_free_rate", 0.02)

	pts, err := equityDaily(t.source, periodDays)
	if err != nil {
		return nil, err
	}
	rets := dailyReturns(pts)
	avg, sd := mean(rets), stdDev(rets)
	dailyRF := riskFree / tradingDaysPerYear

	sharpe := 0.0
	if sd > 0 {
		sharpe = (avg - dailyRF) / sd * math.Sqrt(tradingDaysPerYear)
	}
	downside := 0.0
	for _, r := range rets {
		if r < dailyRF {
			downside += (r - dailyRF) * (r - dailyRF)
		}
	}
	downside = math.Sqrt(downside / float64(len(rets)))
	sortino := 0.0
	if downside > 0 {
		sortino = (avg - dailyRF) / downside * math.Sqrt(tradingDaysPerYear)
	}
	ddAbs, ddPct := maxDrawdown(pts)
	first, last := pts[0].Value, pts[len(pts)-1].Value
	totalReturn := (last/first - 1) * 100
	annualized := (math.Pow(last/first, tradingDaysPerYear/float64(len(rets))) - 1) * 100
	calmar := 0.0
	if ddPct > 0 {
		calmar = annualized / ddPct
	}

	return map[string]interface{}{
		"success": true,
		"metrics": map[string]interface{}{
			"sharpe_ratio":          round(sharpe, 4),
			"sortino_ratio":         round(sortino, 4),
			"calmar_ratio":          round(calmar, 4),
			"total_return_pct":      round(totalReturn, 4),
			"annualized_return_pct": round(annualized, 4),
			"daily_volatility_pct":  round(sd*100, 4),
			"max_drawdown":          round(ddAbs, 4),
			"max_drawdown_pct":      round(ddPct, 4),
			"var_95_pct":            round(historicalVaR(rets, 0.95)*100, 4),
			"var_99_pct":            round(historicalVaR(rets, 0.99)*100, 4),
			"start_equity":          round(first, 4),
			"end_equity":            round(last, 4),
		},
		"basis": map[string]interface{}{
			"source":         "portfolio_snapshots (hourly account equity), last snapshot of each UTC day",
			"period_start":   pts[0].Day,
			"period_end":     pts[len(pts)-1].Day,
			"daily_returns":  len(rets),
			"risk_free_rate": riskFree,
			"var_method":     "historical (empirical quantile of daily returns)",
			"note":           "Equity includes deposits and withdrawals; it is account value, not trading PnL.",
		},
	}, nil
}

// GetPortfolioRiskMetricsTool combines the live holdings (concentration,
// stress tests) with the real equity history (drawdown, VaR).
type GetPortfolioRiskMetricsTool struct {
	holdings portfolioSource
	source   snapshotSource
}

// portfolioSource is the live holdings feed; *gctclient.Client implements it.
type portfolioSource interface {
	GetPortfolioForExchange(ctx context.Context, exchange string) (*models.Portfolio, error)
}

// NewGetPortfolioRiskMetricsTool builds the tool over the exchange client and the snapshot repository (may be nil).
func NewGetPortfolioRiskMetricsTool(client *gctclient.Client, repo *storage.PortfolioSnapshotRepository) *GetPortfolioRiskMetricsTool {
	return &GetPortfolioRiskMetricsTool{holdings: client, source: asSnapshotSource(repo)}
}

func (t *GetPortfolioRiskMetricsTool) Name() string { return "get_portfolio_risk_metrics" }

func (t *GetPortfolioRiskMetricsTool) Description() string {
	return "Get portfolio risk: concentration of the live holdings, max drawdown and VaR from the recorded equity history, and optional stress tests. Returns DATA_UNAVAILABLE when there is not enough equity history"
}

func (t *GetPortfolioRiskMetricsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"exchange":    map[string]interface{}{"type": "string", "description": "Exchange name"},
			"period_days": map[string]interface{}{"type": "number", "description": "History window in days (default: 90)"},
			"stress_scenarios": map[string]interface{}{
				"type":        "array",
				"description": "Stress test scenarios (optional)",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"name":          map[string]interface{}{"type": "string", "description": "Scenario name"},
						"btc_change":    map[string]interface{}{"type": "number", "description": "BTC price change percentage"},
						"market_change": map[string]interface{}{"type": "number", "description": "Change percentage for every other asset"},
					},
				},
			},
		},
		"required": []string{"exchange"},
	}
}

func (t *GetPortfolioRiskMetricsTool) Execute(ctx context.Context, params map[string]interface{}) (interface{}, error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid exchange parameter"}
	}
	periodDays := int(common.GetFloatParam(params, "period_days", 90))
	if periodDays < 1 || periodDays > 3650 {
		return nil, &models.MCPError{Code: -32602, Message: "Invalid params", Data: "period_days must be between 1 and 3650"}
	}

	// History first: without it the tool must not answer with partial numbers.
	pts, err := equityDaily(t.source, periodDays)
	if err != nil {
		return nil, err
	}

	portfolio, err := t.holdings.GetPortfolioForExchange(ctx, exchange)
	if err != nil {
		return nil, common.DataUnavailable("could not read the portfolio from the exchange: "+err.Error(), map[string]interface{}{"exchange": exchange})
	}

	conc := concentration(portfolio)
	_, ddPct := maxDrawdown(pts)
	rets := dailyReturns(pts)
	varPct := historicalVaR(rets, 0.95)

	result := map[string]interface{}{
		"success":                 true,
		"risk_level":              conc.RiskLevel,
		"risk_score":              riskScore(conc, ddPct),
		"total_exposure":          conc.TotalValue,
		"max_drawdown_pct":        round(ddPct, 2),
		"var_95_pct":              round(varPct*100, 4),
		"var_95":                  round(varPct*portfolio.TotalValueUSD, 2),
		"concentration_risk":      conc.ConcentrationRisk,
		"max_asset_concentration": conc.MaxAssetConcentration,
		"most_concentrated_asset": conc.MostConcentratedAsset,
		"diversification":         conc.Diversification,
		"number_of_assets":        conc.NumberOfAssets,
		"basis": map[string]interface{}{
			"history_source":  "portfolio_snapshots (hourly account equity), last snapshot of each UTC day",
			"period_start":    pts[0].Day,
			"period_end":      pts[len(pts)-1].Day,
			"daily_returns":   len(rets),
			"var_method":      "historical (empirical quantile of daily returns), applied to the current portfolio value",
			"holdings_source": "exchange via GoCryptoTrader",
		},
	}
	if scenarios, ok := params["stress_scenarios"].([]interface{}); ok {
		result["stress_tests"] = stressTests(portfolio, scenarios)
	}
	return result, nil
}

func concentration(portfolio *models.Portfolio) *models.PortfolioRiskMetrics {
	total := 0.0
	for _, h := range portfolio.Holdings {
		total += h.ValueUSD
	}
	if len(portfolio.Holdings) == 0 || total <= 0 {
		return &models.PortfolioRiskMetrics{RiskLevel: "low", Diversification: 1, Message: "No holdings in portfolio"}
	}
	hhi, maxW, maxAsset := 0.0, 0.0, ""
	for _, h := range portfolio.Holdings {
		w := h.ValueUSD / total
		hhi += w * w
		if w > maxW {
			maxW, maxAsset = w, h.Currency
		}
	}
	level := "low"
	if hhi > 0.25 {
		level = "high"
	} else if hhi > 0.15 {
		level = "medium"
	}
	return &models.PortfolioRiskMetrics{
		ConcentrationRisk: hhi, MaxAssetConcentration: maxW * 100, MostConcentratedAsset: maxAsset,
		RiskLevel: level, Diversification: 1 - hhi, NumberOfAssets: len(portfolio.Holdings), TotalValue: total,
	}
}

// riskScore is a 0-100 heuristic from concentration, diversification and the
// observed drawdown. It is a ranking aid, not a statistical measure.
func riskScore(m *models.PortfolioRiskMetrics, maxDrawdownPct float64) float64 {
	score := m.ConcentrationRisk*40 + (1-m.Diversification)*30 + (maxDrawdownPct/100)*10
	return round(math.Min(100, math.Max(0, score)), 2)
}

// stressTests applies the caller's scenarios to the live holdings (pure arithmetic).
func stressTests(portfolio *models.Portfolio, scenarios []interface{}) []models.StressTestResult {
	total := 0.0
	for _, h := range portfolio.Holdings {
		total += h.ValueUSD
	}
	out := make([]models.StressTestResult, 0, len(scenarios))
	for _, raw := range scenarios {
		sc, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := sc["name"].(string)
		btc, _ := sc["btc_change"].(float64)
		mkt, _ := sc["market_change"].(float64)
		impact := 0.0
		for _, h := range portfolio.Holdings {
			change := mkt
			if h.Currency == "BTC" {
				change = btc
			}
			impact += h.ValueUSD * change / 100
		}
		pct := 0.0
		if total > 0 {
			pct = impact / total * 100
		}
		out = append(out, models.StressTestResult{ScenarioName: name, PortfolioImpact: impact, ImpactPercent: pct, NewValue: total + impact})
	}
	return out
}
