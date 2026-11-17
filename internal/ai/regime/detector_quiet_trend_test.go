package regime

import "testing"

// TestClassify_QuietTrendDown_OverridesRanging verifies that a would-be
// RANGING read (low ADX, tight BB) is reclassified as QUIET_TREND_DOWN when
// the closes show a persistent net downward drift over the lookback window —
// the shape observed in PEPE-USDT/COTI-USDT (2026-07-04/06): a real
// multi-hour drift that ADX/BB-width alone read as RANGING because per-candle
// noise dilutes the local directional-movement signal even though the net
// displacement over the full window is real and consistent.
func TestClassify_QuietTrendDown_OverridesRanging(t *testing.T) {
	// 20-step window, net down (14 down-steps / 20 => 0.70 consistency),
	// net move well beyond the 1.5% default threshold.
	closes := make([]float64, 21)
	price := 100.0
	closes[0] = price
	for i := 1; i <= 20; i++ {
		if i%3 == 0 {
			price *= 1.001
		} else {
			price *= 0.996
		}
		closes[i] = price
	}

	d := NewDetector(nil)
	a := &RegimeAnalysis{ADX: 12, BollingerWidth: 0.01, TrendDirection: "down"}
	d.classify(a, 0.012, closes)

	if a.Regime != RegimeQuietTrendDown {
		t.Fatalf("expected QUIET_TREND_DOWN, got %s (reasoning=%q)", a.Regime, a.Reasoning)
	}
	if a.SuggestedAction != ActionShortOnly {
		t.Errorf("expected ActionShortOnly for QUIET_TREND_DOWN, got %s", a.SuggestedAction)
	}
}

// TestClassify_TrueRanging_NoDrift verifies a genuinely range-bound series
// (oscillating, no persistent net move) stays classified RANGING rather than
// being spuriously promoted to a quiet trend.
func TestClassify_TrueRanging_NoDrift(t *testing.T) {
	closes := make([]float64, 21)
	price := 100.0
	for i := range closes {
		if i%2 == 0 {
			price *= 1.001
		} else {
			price *= 0.999
		}
		closes[i] = price
	}

	d := NewDetector(nil)
	a := &RegimeAnalysis{ADX: 12, BollingerWidth: 0.01, TrendDirection: "neutral"}
	d.classify(a, 0.012, closes)

	if a.Regime != RegimeRanging {
		t.Fatalf("expected RANGING for an oscillating series with no net drift, got %s (reasoning=%q)", a.Regime, a.Reasoning)
	}
}

func TestQuietTrendDrift_InsufficientCandles(t *testing.T) {
	if _, _, ok := quietTrendDrift([]float64{1, 2, 3}, 20); ok {
		t.Error("expected ok=false when fewer candles than lookback+1")
	}
}

func TestQuietTrendDrift_MeasuresNetMoveAndConsistency(t *testing.T) {
	closes := make([]float64, 21)
	price := 100.0
	closes[0] = price
	// 14 down-steps, 6 up-steps within a 20-step window, net down.
	for i := 1; i <= 20; i++ {
		if i%3 == 0 {
			price *= 1.002
		} else {
			price *= 0.997
		}
		closes[i] = price
	}

	netMovePct, consistency, ok := quietTrendDrift(closes, 20)
	if !ok {
		t.Fatal("expected ok=true with exactly lookback+1 candles")
	}
	if netMovePct >= 0 {
		t.Errorf("expected a negative net move, got %.2f%%", netMovePct)
	}
	if consistency <= 0.5 {
		t.Errorf("expected majority of steps to agree with the net-down direction, got %.2f", consistency)
	}
}
