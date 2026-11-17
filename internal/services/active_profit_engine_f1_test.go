package services

import (
	"strings"
	"testing"
)

// TestContinuationScore_StrongUptrendLong: every factor favours holding a long
// in a strong uptrend → score must clear the default 0.65 threshold.
func TestContinuationScore_StrongUptrendLong(t *testing.T) {
	snap := &positionIndicatorSnapshot{
		CurrentPrice:   101.0,
		ADX:            35,  // >= adxMin
		DIPlus:         30,  // +DI dominates
		DIMinus:        10,
		Volume:         200, // >= 1.2 * avg
		AvgVolume:      100,
		Histogram:      0.5, // positive and...
		HistogramDelta: 0.1, // ...accelerating
	}
	score, reasons := continuationScoreFromInputs(true, snap, "bullish", 25.0, 1.2, /*high*/ 101.0, /*low*/ 0)
	if score < 0.65 {
		t.Errorf("strong uptrend long should hold (score>=0.65), got %.2f reasons=%v", score, reasons)
	}
	// All six factors should contribute.
	for _, want := range []string{"adx", "di_aligned", "macro_bull", "vol_expansion", "macd_accel", "new_high"} {
		if !strings.Contains(strings.Join(reasons, ","), want) {
			t.Errorf("expected reason %q in %v", want, reasons)
		}
	}
}

// TestContinuationScore_RangingLong: weak/no trend → score must stay below the
// hold threshold so normal profit-taking proceeds.
func TestContinuationScore_RangingLong(t *testing.T) {
	snap := &positionIndicatorSnapshot{
		CurrentPrice:   100.0,
		ADX:            12, // weak trend
		DIPlus:         15,
		DIMinus:        16, // not aligned
		Volume:         90,
		AvgVolume:      100, // no expansion
		Histogram:      -0.1,
		HistogramDelta: -0.1, // not accelerating up
	}
	score, _ := continuationScoreFromInputs(true, snap, "neutral", 25.0, 1.2, 120.0 /*far from high*/, 0)
	if score >= 0.65 {
		t.Errorf("ranging long should NOT hold, got score %.2f", score)
	}
}

// TestContinuationScore_DowntrendShort: a short in a strong downtrend holds.
func TestContinuationScore_DowntrendShort(t *testing.T) {
	snap := &positionIndicatorSnapshot{
		CurrentPrice:   99.0,
		ADX:            40,
		DIPlus:         8,
		DIMinus:        28, // -DI dominates
		Volume:         300,
		AvgVolume:      100,
		Histogram:      -0.6, // negative and...
		HistogramDelta: -0.2, // ...accelerating down
	}
	score, reasons := continuationScoreFromInputs(false, snap, "bearish", 25.0, 1.2, 0, /*low*/ 99.0)
	if score < 0.65 {
		t.Errorf("strong downtrend short should hold, got %.2f reasons=%v", score, reasons)
	}
	if strings.Contains(strings.Join(reasons, ","), "new_low") == false {
		t.Errorf("expected new_low reason for short at the low, got %v", reasons)
	}
}

// TestContinuationScore_MacroMismatch: a long whose macro is bearish must not
// get the macro bonus (guards against holding into a higher-timeframe reversal).
func TestContinuationScore_MacroMismatch(t *testing.T) {
	snap := &positionIndicatorSnapshot{ADX: 35, DIPlus: 30, DIMinus: 10}
	_, reasons := continuationScoreFromInputs(true, snap, "bearish", 25.0, 1.2, 0, 0)
	if strings.Contains(strings.Join(reasons, ","), "macro_bull") {
		t.Errorf("long must not earn macro_bull when macro is bearish: %v", reasons)
	}
}

// TestContinuationScore_NilSnap: defensive — nil snapshot scores zero.
func TestContinuationScore_NilSnap(t *testing.T) {
	if score, _ := continuationScoreFromInputs(true, nil, "bullish", 25, 1.2, 0, 0); score != 0 {
		t.Errorf("nil snapshot must score 0, got %.2f", score)
	}
}
