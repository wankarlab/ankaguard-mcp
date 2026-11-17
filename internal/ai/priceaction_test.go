package ai

import "testing"

func c(open, high, low, close, vol float64) Candle {
	return Candle{Open: open, High: high, Low: low, Close: close, Volume: vol}
}

// flat baseline candles to satisfy lookback without injecting signal.
func baseline(n int) []Candle {
	out := make([]Candle, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, c(100, 100.2, 99.8, 100, 100))
	}
	return out
}

func TestDetectPriceAction_BullishEngulfing(t *testing.T) {
	p := DefaultPriceActionParams()
	candles := baseline(20)
	// prev: small red; last: big green engulfing it, high volume.
	candles = append(candles, c(101, 101.1, 100.4, 100.5, 100)) // red
	candles = append(candles, c(100.4, 102.2, 100.3, 102.0, 300))
	sig := DetectPriceAction(candles, p)
	if sig.Direction != "bullish" {
		t.Fatalf("expected bullish, got %s (%v)", sig.Direction, sig.Patterns)
	}
	if sig.Strength <= 0 {
		t.Fatalf("expected positive strength, got %.2f", sig.Strength)
	}
}

func TestDetectPriceAction_Hammer(t *testing.T) {
	p := DefaultPriceActionParams()
	candles := baseline(20)
	// long lower wick, small body near the top, tiny upper wick = hammer.
	candles = append(candles, c(100, 100.3, 100, 100.1, 100))
	candles = append(candles, c(100.0, 100.15, 98.5, 100.05, 120))
	sig := DetectPriceAction(candles, p)
	if sig.Direction != "bullish" {
		t.Fatalf("expected bullish (hammer), got %s (%v)", sig.Direction, sig.Patterns)
	}
}

func TestDetectPriceAction_BearishMarubozu(t *testing.T) {
	p := DefaultPriceActionParams()
	candles := baseline(20)
	candles = append(candles, c(100, 100.2, 99.9, 100.0, 100))
	// full-body red candle, tiny wicks, big volume.
	candles = append(candles, c(101.0, 101.02, 99.0, 99.05, 400))
	sig := DetectPriceAction(candles, p)
	if sig.Direction != "bearish" {
		t.Fatalf("expected bearish (marubozu), got %s (%v)", sig.Direction, sig.Patterns)
	}
}

func TestDetectPriceAction_ThreeCandleContinuationUp(t *testing.T) {
	p := DefaultPriceActionParams()
	candles := baseline(20)
	candles = append(candles, c(100, 101.5, 99.9, 101.4, 200)) // impulse up (big body)
	candles = append(candles, c(101.4, 101.6, 101.2, 101.45, 90))
	candles = append(candles, c(101.45, 101.6, 101.2, 101.4, 90))
	candles = append(candles, c(101.4, 102.8, 101.3, 102.7, 220)) // resume up (big body)
	sig := DetectPriceAction(candles, p)
	if sig.Direction != "bullish" {
		t.Fatalf("expected bullish (continuation), got %s (%v)", sig.Direction, sig.Patterns)
	}
	found := false
	for _, name := range sig.Patterns {
		if name == "three_candle_continuation_up" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected three_candle_continuation_up in %v", sig.Patterns)
	}
}

func TestDetectPriceAction_NeutralOnFlat(t *testing.T) {
	p := DefaultPriceActionParams()
	sig := DetectPriceAction(baseline(20), p)
	if sig.Direction != "neutral" {
		t.Fatalf("expected neutral on flat candles, got %s (%v)", sig.Direction, sig.Patterns)
	}
}

func TestDetectPriceAction_TooFewCandles(t *testing.T) {
	sig := DetectPriceAction(baseline(2), DefaultPriceActionParams())
	if sig.Direction != "neutral" {
		t.Fatalf("expected neutral with <3 candles, got %s", sig.Direction)
	}
}
