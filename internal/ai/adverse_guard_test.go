package ai

import (
	"testing"
	"time"
)

// mkCandles builds a candle series from closes, stamping ascending timestamps
// (oldest first) unless reverse=true (newest first, the REST-only ordering).
func mkCandles(closes []float64, reverse bool) []Candle {
	base := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	out := make([]Candle, len(closes))
	for i, c := range closes {
		ts := base.Add(time.Duration(i) * time.Minute)
		out[i] = Candle{Timestamp: ts, Open: c, High: c, Low: c, Close: c}
	}
	if reverse {
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
	}
	return out
}

func TestRecentMovePct_Ascending(t *testing.T) {
	// 100 → 96 over 4 candles = -4% drop.
	candles := mkCandles([]float64{100, 99, 98, 97, 96}, false)
	mv, ok := recentMovePct(candles, 4)
	if !ok {
		t.Fatal("expected ok")
	}
	if mv > -0.039 || mv < -0.041 {
		t.Fatalf("expected ≈-0.04, got %.4f", mv)
	}
}

func TestRecentMovePct_NewestFirstSameAnswer(t *testing.T) {
	// Same series but newest-first (REST ordering) must yield the SAME move:
	// the guard is order-agnostic. This is exactly the candle-ordering bug
	// that made the bot misread the current price.
	asc := mkCandles([]float64{100, 99, 98, 97, 96}, false)
	desc := mkCandles([]float64{100, 99, 98, 97, 96}, true)
	a, _ := recentMovePct(asc, 4)
	d, _ := recentMovePct(desc, 4)
	if a != d {
		t.Fatalf("order-agnostic guard expected equal moves, got asc=%.4f desc=%.4f", a, d)
	}
}

func TestRecentMovePct_Rally(t *testing.T) {
	candles := mkCandles([]float64{100, 101, 102, 103, 104}, false)
	mv, ok := recentMovePct(candles, 4)
	if !ok || mv < 0.039 || mv > 0.041 {
		t.Fatalf("expected ≈+0.04, got %.4f (ok=%v)", mv, ok)
	}
}

func TestRecentMovePct_InsufficientData(t *testing.T) {
	if _, ok := recentMovePct(mkCandles([]float64{100}, false), 6); ok {
		t.Fatal("expected ok=false for single candle")
	}
	if _, ok := recentMovePct(nil, 6); ok {
		t.Fatal("expected ok=false for nil")
	}
}

func TestRecentMovePct_LookbackClampedToSeries(t *testing.T) {
	// lookback larger than the series clamps to (n-1) instead of panicking.
	candles := mkCandles([]float64{100, 95}, false)
	mv, ok := recentMovePct(candles, 50)
	if !ok || mv > -0.049 || mv < -0.051 {
		t.Fatalf("expected ≈-0.05 with clamp, got %.4f (ok=%v)", mv, ok)
	}
}
