package backtest_test

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/backtest"
)

// ─── EV-3 drawdown unit fix ───────────────────────────────────────────────────
// Verifies that gate #4 actually fires when WorstDrawdown exceeds MaxDrawdownPct.
// The pre-fix bug: maxDrawdown() returned a fraction (~0.12) but was compared to
// 15.0, so gate #4 could never reject a Spec.

func TestValidator_DrawdownGate_RejectsHighDD(t *testing.T) {
	cfg := backtest.DefaultValidationConfig()
	// MaxDrawdownPct is 15.0 (percent). After the fix maxDrawdown returns percent too.

	verdict := &backtest.ValidationVerdict{
		NTradesTotal:    50,
		AggProfitFactor: 1.5,
		Consistency:     0.75,
		WorstDrawdown:   20.0, // 20% — must exceed 15.0 threshold
	}

	pass, reason := backtest.EvaluateVerdict(verdict, cfg)
	if pass {
		t.Errorf("expected gate #4 to reject 20%% drawdown (MaxDrawdownPct=%.1f)", cfg.MaxDrawdownPct)
	}
	if reason == "" {
		t.Error("reason must be non-empty on rejection")
	}
}

func TestValidator_DrawdownGate_AcceptsLowDD(t *testing.T) {
	cfg := backtest.DefaultValidationConfig()

	verdict := &backtest.ValidationVerdict{
		NTradesTotal:    50,
		AggProfitFactor: 1.5,
		Consistency:     0.75,
		WorstDrawdown:   10.0, // 10% — below 15.0 threshold
	}

	pass, _ := backtest.EvaluateVerdict(verdict, cfg)
	if !pass {
		t.Error("expected gate #4 to pass 10%% drawdown (MaxDrawdownPct=15.0)")
	}
}

func TestValidator_DrawdownGate_Boundary_ExactlyAtThreshold(t *testing.T) {
	cfg := backtest.DefaultValidationConfig()

	// WorstDrawdown == MaxDrawdownPct is still a pass (strict > comparison)
	verdict := &backtest.ValidationVerdict{
		NTradesTotal:    50,
		AggProfitFactor: 1.5,
		Consistency:     0.75,
		WorstDrawdown:   15.0,
	}

	pass, _ := backtest.EvaluateVerdict(verdict, cfg)
	if !pass {
		t.Error("WorstDrawdown exactly at MaxDrawdownPct must pass (strict > comparison)")
	}
}

// ─── Hard-gate boundary tests ─────────────────────────────────────────────────

func TestValidator_Gate_ProfitFactor_Below(t *testing.T) {
	cfg := backtest.DefaultValidationConfig()
	v := &backtest.ValidationVerdict{NTradesTotal: 50, AggProfitFactor: 1.29, Consistency: 0.75, WorstDrawdown: 5.0}
	pass, _ := backtest.EvaluateVerdict(v, cfg)
	if pass {
		t.Error("PF 1.29 must fail (threshold 1.30)")
	}
}

func TestValidator_Gate_ProfitFactor_Above(t *testing.T) {
	cfg := backtest.DefaultValidationConfig()
	v := &backtest.ValidationVerdict{NTradesTotal: 50, AggProfitFactor: 1.31, Consistency: 0.75, WorstDrawdown: 5.0}
	pass, _ := backtest.EvaluateVerdict(v, cfg)
	if !pass {
		t.Error("PF 1.31 must pass (threshold 1.30)")
	}
}

func TestValidator_Gate_Consistency_Below(t *testing.T) {
	cfg := backtest.DefaultValidationConfig()
	v := &backtest.ValidationVerdict{NTradesTotal: 50, AggProfitFactor: 1.5, Consistency: 0.5, WorstDrawdown: 5.0}
	pass, _ := backtest.EvaluateVerdict(v, cfg)
	if pass {
		t.Error("Consistency 0.50 must fail (threshold 0.60)")
	}
}

func TestValidator_Gate_Trades_Below(t *testing.T) {
	cfg := backtest.DefaultValidationConfig()
	v := &backtest.ValidationVerdict{NTradesTotal: 29, AggProfitFactor: 1.5, Consistency: 0.75, WorstDrawdown: 5.0}
	pass, _ := backtest.EvaluateVerdict(v, cfg)
	if pass {
		t.Error("NTrades 29 must fail (threshold 30)")
	}
}
