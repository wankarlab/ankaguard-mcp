package validation

import "testing"

// The default limits are percentages, like the rule YAML and RiskPercent:
// a 0.8% risk passes, a 1.5% risk fails.
func TestDefaultConfigUsesPercentUnits(t *testing.T) {
	v := NewPreTradeValidator(DefaultConfig())
	base := TradingDecision{Pair: "SOLUSDT", Direction: "LONG", EntryPrice: 100, StopLoss: 98.5, TakeProfit: 103,
		PositionSize: 2, RiskRewardRatio: 2, Leverage: 5, ConfluenceCount: 4}
	portfolio := &PortfolioState{TotalCapital: 27, AvailableCapital: 25, PeakCapital: 27}

	ok := base
	ok.RiskPercent = 0.8
	if r := v.Validate(&ok, portfolio); !r.Passed {
		t.Fatalf("0.8%% risk should pass: %+v", r.CriticalFailures)
	}
	tooHigh := base
	tooHigh.RiskPercent = 1.5
	if r := v.Validate(&tooHigh, portfolio); r.Passed {
		t.Fatal("1.5% risk should fail under the 1% default")
	}
}

// Daily-loss rules are written in percent; the engine converts the USD loss.
func TestRuleEngineDailyLossInPercent(t *testing.T) {
	re := &RuleEngine{}
	v, err := re.getFieldValue("DailyLoss", &TradingDecision{}, &PortfolioState{DailyLoss: 0.81, TotalCapital: 27})
	if err != nil {
		t.Fatal(err)
	}
	if got := v.(float64); got < 2.99 || got > 3.01 {
		t.Fatalf("0.81 USD of 27 USD should read as 3%%, got %v", got)
	}
}
