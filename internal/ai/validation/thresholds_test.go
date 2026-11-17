package validation

import (
	"context"
	"testing"
)

func TestUpdateThresholds_AppliesToValidatorRulesAndKeepsHalt(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RulesDir = "../../../config/validation"
	svc := NewValidationService(cfg)
	if err := svc.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	trade := &TradingDecision{RiskPercent: 1.5}
	riskRuleFails := func() bool {
		for _, f := range svc.ruleEngine.ExecuteRules(trade, &PortfolioState{}).CriticalFailures {
			if f.RuleID == "MAX_RISK_PER_TRADE" {
				return true
			}
		}
		return false
	}
	if !riskRuleFails() {
		t.Fatal("1.5 % risk must fail the YAML rule at its 1 % literal")
	}

	svc.circuitBreaker.ManualHalt("test halt")
	svc.UpdateThresholds(Thresholds{MaxRiskPerTrade: 2})

	if riskRuleFails() {
		t.Fatal("the YAML rule must use the hot-reloaded 2 % limit")
	}
	if got := svc.CurrentThresholds(); got.MaxRiskPerTrade != 2 || got.MinRiskReward != 1.5 {
		t.Fatalf("unexpected thresholds %+v", got)
	}
	if !svc.circuitBreaker.IsHalted() {
		t.Fatal("UpdateThresholds must not reset the circuit breaker")
	}
}

func TestUpdateThresholds_MaxStopLossOverridesRuleAndZeroIsIgnored(t *testing.T) {
	cfg := DefaultConfig()
	cfg.RulesDir = "../../../config/validation"
	svc := NewValidationService(cfg)
	if err := svc.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	trade := &TradingDecision{StopLossPercent: 6}
	slRuleFails := func() bool {
		for _, f := range svc.ruleEngine.ExecuteRules(trade, &PortfolioState{}).CriticalFailures {
			if f.RuleID == "STOP_LOSS_MAX_DISTANCE" {
				return true
			}
		}
		return false
	}
	if !slRuleFails() {
		t.Fatal("a 6 % stop must fail the YAML rule at its 5 % literal")
	}
	svc.UpdateThresholds(Thresholds{MaxStopLossPct: 7})
	if slRuleFails() {
		t.Fatal("the stop rule must use the hot-reloaded 7 % limit")
	}
	// A zero (unset) limit must never become an override that blocks every trade.
	trade.StopLossPercent = 1
	svc.UpdateThresholds(Thresholds{})
	if slRuleFails() {
		t.Fatal("a zero MaxStopLossPct must not override the rule")
	}
}
