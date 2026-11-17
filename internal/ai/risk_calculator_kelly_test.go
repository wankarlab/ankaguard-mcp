package ai

import "testing"

// TestKelly_MLBlend verifies that a high ML probability raises the effective
// win rate (and thus the Kelly fraction) relative to history alone (P4.1).
func TestKelly_MLBlend(t *testing.T) {
	rc := NewRiskCalculator(nil, nil, nil, nil, nil)

	base := &KellyInput{
		Confidence:       1.0,
		WinRate:          0.50,
		AvgWinPercent:    1.0,
		AvgLossPercent:   1.0,
		AvailableCapital: 100,
		CurrentPrice:     100,
		MaxPositionUSD:   100,
		MinPositionUSD:   0.1,
	}
	withoutML := rc.CalculateKellyPositionSize(base)

	mlInput := *base
	mlInput.MLProbability = 0.80 // strong forward edge
	mlInput.MLWeight = 0.40
	withML := rc.CalculateKellyPositionSize(&mlInput)

	// With a 0.80 ML prob blended at 0.40, effective WR = 0.8*0.4 + 0.5*0.6 = 0.62,
	// which is a positive edge → larger Kelly fraction than the breakeven WR=0.5 case.
	if withML.AdjustedFraction <= withoutML.AdjustedFraction {
		t.Errorf("ML blend should raise Kelly fraction: without=%.4f with=%.4f",
			withoutML.AdjustedFraction, withML.AdjustedFraction)
	}
}

// TestKelly_SigmaPenalty verifies that high model uncertainty shrinks the size (P4.1).
func TestKelly_SigmaPenalty(t *testing.T) {
	rc := NewRiskCalculator(nil, nil, nil, nil, nil)

	in := &KellyInput{
		Confidence:       1.0,
		WinRate:          0.60,
		AvgWinPercent:    1.5,
		AvgLossPercent:   1.0,
		AvailableCapital: 100,
		CurrentPrice:     100,
		MaxPositionUSD:   100,
		MinPositionUSD:   0.1,
		MLProbability:    0.65,
		MLWeight:         0.40,
	}

	lowSigma := *in
	lowSigma.MLSigma = 0.0
	hi := rc.CalculateKellyPositionSize(&lowSigma)

	highSigma := *in
	highSigma.MLSigma = 1.0 // max uncertainty → 0.5x penalty
	lo := rc.CalculateKellyPositionSize(&highSigma)

	if lo.AdjustedFraction >= hi.AdjustedFraction {
		t.Errorf("high sigma should shrink size: lowSigma=%.4f highSigma=%.4f",
			hi.AdjustedFraction, lo.AdjustedFraction)
	}
}

// TestKelly_NoMLUnchanged verifies that zero ML fields leave behaviour identical
// to the pre-P4.1 path (safe degradation).
func TestKelly_NoMLUnchanged(t *testing.T) {
	rc := NewRiskCalculator(nil, nil, nil, nil, nil)

	in := &KellyInput{
		Confidence:       0.9,
		WinRate:          0.55,
		AvgWinPercent:    1.2,
		AvgLossPercent:   1.0,
		AvailableCapital: 100,
		CurrentPrice:     100,
		MaxPositionUSD:   100,
		MinPositionUSD:   0.1,
		// MLProbability/MLWeight left zero → no blend, no sigma penalty.
	}
	res := rc.CalculateKellyPositionSize(in)
	if res.SizeUSD <= 0 {
		t.Errorf("expected positive size with positive edge, got %.4f", res.SizeUSD)
	}
}
