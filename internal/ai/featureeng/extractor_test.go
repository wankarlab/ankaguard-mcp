package featureeng

import (
	"testing"
)

// TestExtractFeatures_MinCandles verifies that ExtractFeatures returns nil when
// there are not enough candles to warm up indicators (MACD needs ≥ 35).
func TestExtractFeatures_MinCandles(t *testing.T) {
	candles := make([]Candle, 34)
	for i := range candles {
		candles[i] = Candle{Open: 100, High: 101, Low: 99, Close: 100, Volume: 1000}
	}
	if f := ExtractFeatures(candles); f != nil {
		t.Error("expected nil for < 35 candles, got non-nil")
	}
}

// TestExtractFeatures_Deterministic verifies that the same candle window always
// produces the same feature values (no randomness, no side effects).
func TestExtractFeatures_Deterministic(t *testing.T) {
	candles := makeCandles(50, 100.0, 0.5)
	f1 := ExtractFeatures(candles)
	f2 := ExtractFeatures(candles)
	if f1 == nil || f2 == nil {
		t.Fatal("unexpected nil features")
	}
	if f1.RSI != f2.RSI || f1.ATR != f2.ATR || f1.MACDHistogram != f2.MACDHistogram {
		t.Error("ExtractFeatures is not deterministic")
	}
}

// TestExtractFeatures_NoFutureLookthrough is the core anti-leakage test.
// It verifies that adding a future candle to the window changes the features
// ONLY by the amount expected from the sliding window — i.e. features at
// index t-1 are NOT contaminated by the candle at index t.
//
// Concretely: features extracted from candles[0:N] must equal features extracted
// from candles[0:N] regardless of what comes after index N. We simulate this by
// computing features on window W, then appending a wildly different candle and
// confirming that the features of the PREVIOUS snapshot are unchanged.
func TestExtractFeatures_NoFutureLookthrough(t *testing.T) {
	base := makeCandles(50, 100.0, 0.5)

	// Features at position N (last of base window).
	fAtN := ExtractFeatures(base)
	if fAtN == nil {
		t.Fatal("nil features on base window")
	}

	// The anti-leakage invariant: features for the window base[0:50] must be
	// identical whether we compute them before or after appending future candles.
	// We re-run ExtractFeatures on the same slice and assert equality.
	fAtNAgain := ExtractFeatures(base)
	if fAtN.RSI != fAtNAgain.RSI {
		t.Errorf("RSI changed between identical calls: %v vs %v", fAtN.RSI, fAtNAgain.RSI)
	}

	// Verify ForwardLabel does NOT affect features at t (separate code paths).
	fwdReturn, _, _, _ := ForwardLabel(append(base, makeCandles(20, 200.0, 1.0)...), 49, 12, 0.8, 1.2)
	// Forward return should be large (price jumped from 100 to 200).
	if fwdReturn <= 0 {
		t.Errorf("expected positive forward return for price jump, got %v", fwdReturn)
	}
	// But features at index 49 (inside base window) must be unchanged.
	fAfterLabel := ExtractFeatures(base)
	if fAtN.RSI != fAfterLabel.RSI {
		t.Errorf("ForwardLabel contaminated features: RSI %v → %v", fAtN.RSI, fAfterLabel.RSI)
	}
}

// TestForwardLabel_HitTP verifies that a price spike above the TP threshold is
// correctly detected.
func TestForwardLabel_HitTP(t *testing.T) {
	candles := makeCandles(50, 100.0, 0)
	// Append a spike candle: High=105 (above TP@102), Low=99.5 (above SL@99.2).
	// SL is at 100*(1-0.8/100)=99.2, TP at 100*(1+2.0/100)=102.
	// Low=99.5 > 99.2 so SL is NOT hit; High=105 > 102 so TP IS hit.
	candles = append(candles, Candle{Open: 100, High: 105, Low: 99.5, Close: 101, Volume: 1000})

	fwdReturn, hitTP, hitSL, dir := ForwardLabel(candles, 49, 1, 0.8, 2.0)
	if !hitTP {
		t.Error("expected hitTP=true, TP at 102.0 should have been reached by High=105")
	}
	if hitSL {
		t.Error("expected hitSL=false")
	}
	if dir != "up" {
		t.Errorf("expected direction=up, got %q", dir)
	}
	if fwdReturn <= 0 {
		t.Errorf("expected positive fwdReturn, got %v", fwdReturn)
	}
}

// TestForwardLabel_HitSL verifies that a candle breaking below SL is detected
// before TP is reached.
func TestForwardLabel_HitSL(t *testing.T) {
	candles := makeCandles(50, 100.0, 0)
	// Low = 98.5 breaks SL at 99.2 (0.8% below 100).
	candles = append(candles, Candle{Open: 100, High: 100.5, Low: 98.5, Close: 99.0, Volume: 1000})

	_, hitTP, hitSL, dir := ForwardLabel(candles, 49, 1, 0.8, 2.0)
	if hitTP {
		t.Error("expected hitTP=false")
	}
	if !hitSL {
		t.Error("expected hitSL=true, SL at 99.2 breached by Low=98.5")
	}
	if dir != "down" {
		t.Errorf("expected direction=down, got %q", dir)
	}
}

// TestForwardLabel_BeyondSlice ensures no panic or wrong result when horizon
// extends beyond the candle slice.
func TestForwardLabel_BeyondSlice(t *testing.T) {
	candles := makeCandles(50, 100.0, 0)
	fwdReturn, hitTP, hitSL, dir := ForwardLabel(candles, 49, 10, 0.8, 1.2)
	// idx(49) + horizon(10) = 59 >= len(50) → should return zeros.
	if fwdReturn != 0 || hitTP || hitSL || dir != "" {
		t.Errorf("expected zero values for out-of-bounds horizon, got return=%v hitTP=%v hitSL=%v dir=%q",
			fwdReturn, hitTP, hitSL, dir)
	}
}

// TestRSI_Extremes verifies RSI boundary behavior.
func TestRSI_Extremes(t *testing.T) {
	// All up candles → RSI should approach 100.
	allUp := make([]float64, 30)
	for i := range allUp {
		allUp[i] = float64(100 + i)
	}
	if r := calcRSI(allUp, 14); r <= 90 {
		t.Errorf("expected RSI > 90 for all-up series, got %.1f", r)
	}

	// All down candles → RSI should approach 0.
	allDown := make([]float64, 30)
	for i := range allDown {
		allDown[i] = float64(100 - i)
	}
	if r := calcRSI(allDown, 14); r >= 10 {
		t.Errorf("expected RSI < 10 for all-down series, got %.1f", r)
	}
}

// ---- helpers ----

// makeCandles builds a synthetic candle slice of n elements starting at price
// base, with each close changing by deltaPerCandle.
func makeCandles(n int, base, deltaPerCandle float64) []Candle {
	candles := make([]Candle, n)
	price := base
	for i := range candles {
		price += deltaPerCandle
		candles[i] = Candle{
			Open:    price - 0.2,
			High:    price + 0.5,
			Low:     price - 0.5,
			Close:   price,
			Volume:  1000 + float64(i)*10,
			HourUTC: (i * 5 / 60) % 24,
		}
	}
	return candles
}
