package regime

import "testing"

// TestComputeADX_Uptrend verifies the exported pure ADX function reports a
// strong trend with +DI dominating on a clean uptrend.
func TestComputeADX_Uptrend(t *testing.T) {
	const n = 80
	highs := make([]float64, n)
	lows := make([]float64, n)
	closes := make([]float64, n)
	price := 100.0
	for i := 0; i < n; i++ {
		price += 1.0 // steady uptrend
		highs[i] = price + 0.5
		lows[i] = price - 0.5
		closes[i] = price
	}

	adx, diPlus, diMinus, err := ComputeADX(highs, lows, closes, 14)
	if err != nil {
		t.Fatalf("ComputeADX returned error on valid uptrend: %v", err)
	}
	if adx <= 20 {
		t.Errorf("expected strong ADX (>20) on clean uptrend, got %.2f", adx)
	}
	if diPlus <= diMinus {
		t.Errorf("expected +DI > -DI on uptrend, got +DI=%.2f -DI=%.2f", diPlus, diMinus)
	}
}

// TestComputeADX_Downtrend verifies -DI dominates on a clean downtrend.
func TestComputeADX_Downtrend(t *testing.T) {
	const n = 80
	highs := make([]float64, n)
	lows := make([]float64, n)
	closes := make([]float64, n)
	price := 200.0
	for i := 0; i < n; i++ {
		price -= 1.0
		highs[i] = price + 0.5
		lows[i] = price - 0.5
		closes[i] = price
	}

	adx, diPlus, diMinus, err := ComputeADX(highs, lows, closes, 14)
	if err != nil {
		t.Fatalf("ComputeADX error: %v", err)
	}
	if adx <= 20 {
		t.Errorf("expected strong ADX (>20) on clean downtrend, got %.2f", adx)
	}
	if diMinus <= diPlus {
		t.Errorf("expected -DI > +DI on downtrend, got +DI=%.2f -DI=%.2f", diPlus, diMinus)
	}
}

// TestComputeADX_InsufficientData verifies the error path (used as the
// "factor absent" signal by the exit scorer).
func TestComputeADX_InsufficientData(t *testing.T) {
	highs := []float64{1, 2, 3}
	if _, _, _, err := ComputeADX(highs, highs, highs, 14); err == nil {
		t.Error("expected error for insufficient data, got nil")
	}
}
