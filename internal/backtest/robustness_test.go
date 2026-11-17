package backtest_test

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/backtest"
	"github.com/rantipay/trading-mcp/internal/backtest/domain"
)

// ─── inverseDDFactor / temporalDegradation via Score preconditions ───────────

func TestRobustnessScore_RefusesWhenPassFalse(t *testing.T) {
	scorer := backtest.NewRobustnessScorer(nil) // nil DB — should fail before querying
	verdict := &backtest.ValidationVerdict{Pass: false}
	_, err := scorer.Score(nil, verdict)
	if err == nil {
		t.Error("expected error when Pass==false, got nil")
	}
}

// ─── clampNormalize / inverseDDFactor unit checks (exported via helpers) ─────

func TestRobustness_ClampNormalize(t *testing.T) {
	cases := []struct {
		raw, min, max, want float64
	}{
		{1.5, 1.0, 3.0, 0.25}, // (1.5-1.0)/(3.0-1.0)=0.25
		{0.5, 1.0, 3.0, 0.0},  // below min → 0
		{4.0, 1.0, 3.0, 1.0},  // above max → 1
		{1.0, 1.0, 1.0, 0.0},  // degenerate max==min → 0
	}
	for _, c := range cases {
		got := backtest.ClampNormalize(c.raw, c.min, c.max)
		if math := abs(got - c.want); math > 1e-9 {
			t.Errorf("ClampNormalize(%.2f, %.2f, %.2f) = %.4f, want %.4f", c.raw, c.min, c.max, got, c.want)
		}
	}
}

func TestRobustness_InverseDD(t *testing.T) {
	if v := backtest.InverseDDFactor(0); v != 1.0 {
		t.Errorf("0%% DD should yield 1.0, got %.2f", v)
	}
	if v := backtest.InverseDDFactor(30); v != 0.0 {
		t.Errorf("30%% DD should yield 0.0, got %.2f", v)
	}
	if v := backtest.InverseDDFactor(15); !(v > 0.4 && v < 0.6) {
		t.Errorf("15%% DD should be ~0.5, got %.2f", v)
	}
}

func TestRobustness_TemporalDegradation_FewWindows(t *testing.T) {
	v := backtest.TemporalDegradation(nil)
	if v != 0.5 {
		t.Errorf("nil windows should return neutral 0.5, got %.2f", v)
	}
}

func TestRobustness_TemporalDegradation_StableWindows(t *testing.T) {
	windows := []backtest.WindowResult{
		{Metrics: domain.Metrics{ProfitFactor: 1.5}},
		{Metrics: domain.Metrics{ProfitFactor: 1.5}},
		{Metrics: domain.Metrics{ProfitFactor: 1.5}},
	}
	v := backtest.TemporalDegradation(windows)
	if v < 0.9 {
		t.Errorf("perfectly stable windows should score near 1.0, got %.2f", v)
	}
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
