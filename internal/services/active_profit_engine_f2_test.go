package services

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/gct"
)

func bear(o, c float64) *gct.Candle { return &gct.Candle{Open: o, Close: c} }

// TestCandlesConfirmReversal validates the finer-TF exit confirmation logic.
func TestCandlesConfirmReversal(t *testing.T) {
	// Long position: reversal confirmed when recent candles close DOWN (close<open).
	bearish := []*gct.Candle{bear(10, 11), bear(11, 10.5), bear(10.5, 10.0)} // last 2 bearish
	if !candlesConfirmReversal(bearish, true /*long*/, 2) {
		t.Error("long: last 2 bearish candles should confirm reversal")
	}
	// Last candle bullish → not confirmed for a long.
	mixed := []*gct.Candle{bear(10, 9.5), bear(9.5, 9.0), bear(9.0, 9.4)} // last is up
	if candlesConfirmReversal(mixed, true, 2) {
		t.Error("long: a bullish last candle must NOT confirm reversal")
	}
	// Short position: reversal confirmed when recent candles close UP.
	bullish := []*gct.Candle{bear(10, 9), bear(9, 9.5), bear(9.5, 10.0)} // last 2 bullish
	if !candlesConfirmReversal(bullish, false /*short*/, 2) {
		t.Error("short: last 2 bullish candles should confirm reversal")
	}
	// Not enough candles → not confirmed (caller fails open separately).
	if candlesConfirmReversal(bearish, true, 5) {
		t.Error("insufficient candles must not confirm")
	}
	// required<1 guarded.
	if candlesConfirmReversal(bearish, true, 0) {
		t.Error("required<1 must not confirm")
	}
}
