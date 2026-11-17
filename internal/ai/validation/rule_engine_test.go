package validation

import (
	"context"
	"testing"
)

func loadRepoRules(t *testing.T) *RuleEngine {
	t.Helper()
	re := NewRuleEngine("../../../config/validation")
	if err := re.LoadRules(context.Background()); err != nil {
		t.Fatal(err)
	}
	return re
}

func healthyPortfolio() *PortfolioState {
	return &PortfolioState{TotalCapital: 27, AvailableCapital: 25, PeakCapital: 27, OpenPositions: 1}
}

// A healthy small-account trade must pass every rule in the repo's rule files:
// the breakers do not fire, the stop is present, LONG-only rules skip a SHORT.
func TestRepoRulesPassHealthyTrade(t *testing.T) {
	re := loadRepoRules(t)
	for _, trade := range []*TradingDecision{
		{Pair: "UAI-USDT", Direction: "LONG", EntryPrice: 100, StopLoss: 98.5, TakeProfit: 103, PositionSize: 2,
			RiskPercent: 0.11, RiskRewardRatio: 2, Leverage: 5, StopLossPercent: 1.5, ConfluenceCount: 4},
		{Pair: "ETH-USDT", Direction: "SHORT", EntryPrice: 2270, StopLoss: 2304, TakeProfit: 2202, PositionSize: 2,
			RiskPercent: 0.11, RiskRewardRatio: 2, Leverage: 5, StopLossPercent: 1.5, ConfluenceCount: 3},
	} {
		r := re.ExecuteRules(trade, healthyPortfolio())
		if !r.Passed {
			t.Errorf("%s %s blocked: %+v", trade.Pair, trade.Direction, r.CriticalFailures)
		}
		for _, w := range r.Warnings {
			if w.Severity == "ERROR" {
				t.Errorf("rule %s could not be evaluated: %s", w.RuleID, w.Message)
			}
		}
	}
}

func TestRepoRulesFireBreakersAndRequirements(t *testing.T) {
	re := loadRepoRules(t)
	trade := &TradingDecision{Pair: "UAI-USDT", Direction: "LONG", EntryPrice: 100, StopLoss: 98.5, TakeProfit: 103,
		PositionSize: 2, RiskPercent: 0.11, RiskRewardRatio: 2, Leverage: 5, StopLossPercent: 1.5, ConfluenceCount: 4}

	bleeding := healthyPortfolio()
	bleeding.DailyLoss = 0.9 // 3.3% of 27 USD
	if r := re.ExecuteRules(trade, bleeding); r.Passed || !hasRule(r, "DAILY_LOSS_HALT") {
		t.Errorf("daily loss breaker should fire: %+v", r.CriticalFailures)
	}

	noStop := *trade
	noStop.StopLoss, noStop.StopLossPercent = 0, 0
	if r := re.ExecuteRules(&noStop, healthyPortfolio()); r.Passed || !hasRule(r, "STOP_LOSS_REQUIRED") {
		t.Errorf("missing stop should fail: %+v", r.CriticalFailures)
	}

	leveraged := *trade
	leveraged.Leverage = 8
	if r := re.ExecuteRules(&leveraged, healthyPortfolio()); r.Passed || !hasRule(r, "MAX_LEVERAGE_ALTS") {
		t.Errorf("8x on an alt should fail: %+v", r.CriticalFailures)
	}
	btc := leveraged
	btc.Pair = "BTC-USDT"
	if r := re.ExecuteRules(&btc, healthyPortfolio()); !r.Passed {
		t.Errorf("8x on BTC is allowed (10x cap): %+v", r.CriticalFailures)
	}
}

func hasRule(r *ValidationResult, id string) bool {
	for _, f := range r.CriticalFailures {
		if f.RuleID == id {
			return true
		}
	}
	return false
}
